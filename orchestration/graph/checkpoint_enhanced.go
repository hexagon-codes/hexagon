// Package graph 提供图编排引擎
//
// 本文件实现增强的检查点系统，对标 LangGraph：
//   - 分支执行支持
//   - 版本管理
//   - 状态差异追踪
//   - 从任意检查点恢复
//   - 检查点配置
package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/hexagon/internal/util"
)

// ============== 增强检查点类型 ==============

// CheckpointVersion 检查点版本
type CheckpointVersion struct {
	Major int `json:"major"` // 主版本号（不兼容变更）
	Minor int `json:"minor"` // 次版本号（向后兼容变更）
	Patch int `json:"patch"` // 补丁版本号
}

// String 返回版本字符串
func (v CheckpointVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// CheckpointStatus 检查点状态
type CheckpointStatus string

const (
	// CheckpointStatusPending 等待执行
	CheckpointStatusPending CheckpointStatus = "pending"
	// CheckpointStatusRunning 执行中
	CheckpointStatusRunning CheckpointStatus = "running"
	// CheckpointStatusCompleted 已完成
	CheckpointStatusCompleted CheckpointStatus = "completed"
	// CheckpointStatusFailed 失败
	CheckpointStatusFailed CheckpointStatus = "failed"
	// CheckpointStatusInterrupted 中断
	CheckpointStatusInterrupted CheckpointStatus = "interrupted"
)

// EnhancedCheckpoint 增强检查点
// 支持分支、版本管理、状态差异等高级特性
type EnhancedCheckpoint struct {
	// 基本信息
	ID        string            `json:"id"`
	ThreadID  string            `json:"thread_id"`
	GraphName string            `json:"graph_name"`
	Version   CheckpointVersion `json:"version"`
	Status    CheckpointStatus  `json:"status"`

	// 执行状态
	CurrentNode    string   `json:"current_node"`
	PendingNodes   []string `json:"pending_nodes"`
	CompletedNodes []string `json:"completed_nodes"`
	VisitedNodes   []string `json:"visited_nodes"` // 所有访问过的节点（含重复）

	// 状态数据
	State json.RawMessage `json:"state"`
	// Frontier 保存冻结计划、激活、前驱身份和结果采用记录。
	Frontier  json.RawMessage `json:"frontier,omitempty"`
	StateDiff json.RawMessage `json:"state_diff,omitempty"` // 与父检查点的差异
	StateHash string          `json:"state_hash,omitempty"` // 状态哈希，用于快速比较

	// 分支支持
	ParentID   string   `json:"parent_id,omitempty"`   // 父检查点 ID
	BranchID   string   `json:"branch_id,omitempty"`   // 分支 ID
	BranchName string   `json:"branch_name,omitempty"` // 分支名称
	ChildIDs   []string `json:"child_ids,omitempty"`   // 子检查点 ID 列表

	// 元数据
	Metadata    map[string]any `json:"metadata,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Description string         `json:"description,omitempty"`

	// 时间戳
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// 执行统计
	Stats *CheckpointStats `json:"stats,omitempty"`
}

// CheckpointStats 检查点统计
type CheckpointStats struct {
	StepCount     int                      `json:"step_count"`               // 步骤计数
	TotalDuration time.Duration            `json:"total_duration"`           // 总耗时
	NodeDurations map[string]time.Duration `json:"node_durations,omitempty"` // 各节点耗时
	LLMTokens     int                      `json:"llm_tokens,omitempty"`     // LLM Token 数
	ToolCalls     int                      `json:"tool_calls,omitempty"`     // 工具调用次数
}

// ============== 增强检查点保存器接口 ==============

// EnhancedCheckpointSaver 增强检查点保存器接口
type EnhancedCheckpointSaver interface {
	CheckpointSaver // 继承基础接口

	// SaveEnhanced 保存增强检查点
	SaveEnhanced(ctx context.Context, checkpoint *EnhancedCheckpoint) error

	// LoadEnhanced 加载最新的增强检查点
	LoadEnhanced(ctx context.Context, threadID string) (*EnhancedCheckpoint, error)

	// LoadEnhancedByID 根据 ID 加载增强检查点
	LoadEnhancedByID(ctx context.Context, id string) (*EnhancedCheckpoint, error)

	// ListEnhanced 列出线程的所有增强检查点
	ListEnhanced(ctx context.Context, threadID string, opts *ListOptions) ([]*EnhancedCheckpoint, error)

	// GetHistory 获取检查点历史链
	GetHistory(ctx context.Context, checkpointID string, limit int) ([]*EnhancedCheckpoint, error)

	// GetBranches 获取分支列表
	GetBranches(ctx context.Context, threadID string) ([]*BranchInfo, error)

	// CreateBranch 从检查点创建分支
	CreateBranch(ctx context.Context, checkpointID string, branchName string) (*EnhancedCheckpoint, error)

	// MergeBranch 合并分支
	MergeBranch(ctx context.Context, sourceBranchID, targetBranchID string, strategy MergeStrategy) (*EnhancedCheckpoint, error)

	// Search 搜索检查点
	Search(ctx context.Context, query *CheckpointQuery) ([]*EnhancedCheckpoint, error)

	// Cleanup 清理旧检查点
	Cleanup(ctx context.Context, policy *CleanupPolicy) (int, error)
}

// ListOptions 列表选项
type ListOptions struct {
	Limit     int              // 限制数量
	Offset    int              // 偏移量
	Order     string           // 排序方式: "asc" 或 "desc"
	Status    CheckpointStatus // 状态过滤
	BranchID  string           // 分支过滤
	StartTime *time.Time       // 开始时间
	EndTime   *time.Time       // 结束时间
}

// BranchInfo 分支信息
type BranchInfo struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	ThreadID           string    `json:"thread_id"`
	BaseCheckpointID   string    `json:"base_checkpoint_id"`
	LatestCheckpointID string    `json:"latest_checkpoint_id"`
	CheckpointCount    int       `json:"checkpoint_count"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// MergeStrategy 合并策略
type MergeStrategy string

const (
	// MergeStrategyOverwrite 覆盖目标状态
	MergeStrategyOverwrite MergeStrategy = "overwrite"
	// MergeStrategyMerge 合并状态
	MergeStrategyMerge MergeStrategy = "merge"
	// MergeStrategyKeepBoth 保留两者
	MergeStrategyKeepBoth MergeStrategy = "keep_both"
)

// CheckpointQuery 检查点查询
type CheckpointQuery struct {
	ThreadID  string           `json:"thread_id,omitempty"`
	GraphName string           `json:"graph_name,omitempty"`
	Status    CheckpointStatus `json:"status,omitempty"`
	BranchID  string           `json:"branch_id,omitempty"`
	Tags      []string         `json:"tags,omitempty"`
	StartTime *time.Time       `json:"start_time,omitempty"`
	EndTime   *time.Time       `json:"end_time,omitempty"`
	Metadata  map[string]any   `json:"metadata,omitempty"`
	Limit     int              `json:"limit,omitempty"`
	Offset    int              `json:"offset,omitempty"`
}

// CleanupPolicy 清理策略
type CleanupPolicy struct {
	// MaxAge 最大保留时间
	MaxAge time.Duration `json:"max_age,omitempty"`
	// MaxCount 最大保留数量（每个线程）
	MaxCount int `json:"max_count,omitempty"`
	// KeepCompleted 是否保留已完成的检查点
	KeepCompleted bool `json:"keep_completed,omitempty"`
	// KeepBranchHeads 是否保留分支头
	KeepBranchHeads bool `json:"keep_branch_heads,omitempty"`
	// KeepTagged 是否保留有标签的检查点
	KeepTagged bool `json:"keep_tagged,omitempty"`
}

// ============== 内存增强检查点保存器 ==============

// MemoryEnhancedCheckpointSaver 内存增强检查点保存器
type MemoryEnhancedCheckpointSaver struct {
	*MemoryCheckpointSaver
	enhanced map[string]*EnhancedCheckpoint
	branches map[string]*BranchInfo
	mu       sync.RWMutex
}

// NewMemoryEnhancedCheckpointSaver 创建内存增强检查点保存器
func NewMemoryEnhancedCheckpointSaver() *MemoryEnhancedCheckpointSaver {
	return &MemoryEnhancedCheckpointSaver{
		MemoryCheckpointSaver: NewMemoryCheckpointSaver(),
		enhanced:              make(map[string]*EnhancedCheckpoint),
		branches:              make(map[string]*BranchInfo),
	}
}

// SaveEnhanced 保存增强检查点
func (s *MemoryEnhancedCheckpointSaver) SaveEnhanced(ctx context.Context, checkpoint *EnhancedCheckpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if checkpoint == nil {
		return fmt.Errorf("checkpoint is nil")
	}
	if checkpoint.ThreadID == "" {
		return fmt.Errorf("checkpoint thread_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if checkpoint.ID == "" {
		checkpoint.ID = util.GenerateID("ckpt")
	}

	// 更新已存在检查点时，如果未显式设置 CreatedAt，沿用原值
	if existing, ok := s.enhanced[checkpoint.ID]; ok && checkpoint.CreatedAt.IsZero() {
		checkpoint.CreatedAt = existing.CreatedAt
	}

	checkpoint.UpdatedAt = time.Now()
	if checkpoint.CreatedAt.IsZero() {
		checkpoint.CreatedAt = checkpoint.UpdatedAt
	}

	// 计算状态哈希
	if checkpoint.State != nil {
		checkpoint.StateHash = hashState(checkpoint.State)
	}

	// 更新父检查点的子列表
	if checkpoint.ParentID != "" {
		if parent, ok := s.enhanced[checkpoint.ParentID]; ok {
			parent.ChildIDs = appendUniqueID(parent.ChildIDs, checkpoint.ID)
		}
	}

	// 保存副本，避免外部修改引用导致检查点污染
	s.enhanced[checkpoint.ID] = cloneEnhancedCheckpoint(checkpoint)

	// 同一个检查点 ID 只记录一次线程索引，避免重复列表项
	s.threads[checkpoint.ThreadID] = appendUniqueID(s.threads[checkpoint.ThreadID], checkpoint.ID)

	// 更新分支信息
	if checkpoint.BranchID != "" {
		if branch, ok := s.branches[checkpoint.BranchID]; ok {
			branch.LatestCheckpointID = checkpoint.ID
			branch.CheckpointCount = countBranchCheckpoints(s.enhanced, checkpoint.BranchID)
			branch.UpdatedAt = time.Now()
		}
	}

	return nil
}

// LoadEnhanced 加载最新的增强检查点
func (s *MemoryEnhancedCheckpointSaver) LoadEnhanced(ctx context.Context, threadID string) (*EnhancedCheckpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	ids, ok := s.threads[threadID]
	if !ok || len(ids) == 0 {
		return nil, fmt.Errorf("no checkpoint found for thread %s", threadID)
	}

	latestID := ids[len(ids)-1]
	cp, ok := s.enhanced[latestID]
	if !ok {
		return nil, fmt.Errorf("checkpoint %s not found", latestID)
	}

	return cloneEnhancedCheckpoint(cp), nil
}

// LoadEnhancedByID 根据 ID 加载增强检查点
func (s *MemoryEnhancedCheckpointSaver) LoadEnhancedByID(ctx context.Context, id string) (*EnhancedCheckpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, fmt.Errorf("checkpoint id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	cp, ok := s.enhanced[id]
	if !ok {
		return nil, fmt.Errorf("checkpoint %s not found", id)
	}

	return cloneEnhancedCheckpoint(cp), nil
}

// ListEnhanced 列出线程的所有增强检查点
func (s *MemoryEnhancedCheckpointSaver) ListEnhanced(ctx context.Context, threadID string, opts *ListOptions) ([]*EnhancedCheckpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	ids, ok := s.threads[threadID]
	if !ok {
		return nil, nil
	}

	result := make([]*EnhancedCheckpoint, 0, len(ids))
	for _, id := range ids {
		cp, ok := s.enhanced[id]
		if !ok {
			continue
		}

		// 应用过滤
		if opts != nil {
			if opts.Status != "" && cp.Status != opts.Status {
				continue
			}
			if opts.BranchID != "" && cp.BranchID != opts.BranchID {
				continue
			}
			if opts.StartTime != nil && cp.CreatedAt.Before(*opts.StartTime) {
				continue
			}
			if opts.EndTime != nil && cp.CreatedAt.After(*opts.EndTime) {
				continue
			}
		}
		result = append(result, cloneEnhancedCheckpoint(cp))
	}

	// 应用分页
	if opts != nil && opts.Limit > 0 {
		start := opts.Offset
		if start < 0 {
			start = 0
		}
		if start >= len(result) {
			return nil, nil
		}
		end := start + opts.Limit
		if end > len(result) {
			end = len(result)
		}
		result = result[start:end]
	}

	return result, nil
}

// GetHistory 获取检查点历史链
func (s *MemoryEnhancedCheckpointSaver) GetHistory(ctx context.Context, checkpointID string, limit int) ([]*EnhancedCheckpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if checkpointID == "" {
		return nil, fmt.Errorf("checkpoint id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var history []*EnhancedCheckpoint
	currentID := checkpointID

	for currentID != "" && (limit <= 0 || len(history) < limit) {
		cp, ok := s.enhanced[currentID]
		if !ok {
			break
		}
		history = append(history, cloneEnhancedCheckpoint(cp))
		currentID = cp.ParentID
	}

	return history, nil
}

// GetBranches 获取分支列表
func (s *MemoryEnhancedCheckpointSaver) GetBranches(ctx context.Context, threadID string) ([]*BranchInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*BranchInfo
	for _, branch := range s.branches {
		if branch.ThreadID == threadID {
			result = append(result, cloneBranchInfo(branch))
		}
	}

	return result, nil
}

// CreateBranch 从检查点创建分支
func (s *MemoryEnhancedCheckpointSaver) CreateBranch(ctx context.Context, checkpointID string, branchName string) (*EnhancedCheckpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if checkpointID == "" {
		return nil, fmt.Errorf("checkpoint id is required")
	}
	if branchName == "" {
		return nil, fmt.Errorf("branch name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 获取源检查点
	source, ok := s.enhanced[checkpointID]
	if !ok {
		return nil, fmt.Errorf("checkpoint %s not found", checkpointID)
	}

	// 创建分支
	now := time.Now()
	branchID := util.GenerateID("branch")
	branch := &BranchInfo{
		ID:               branchID,
		Name:             branchName,
		ThreadID:         source.ThreadID,
		BaseCheckpointID: checkpointID,
		CheckpointCount:  0,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	s.branches[branchID] = branch

	// 创建新检查点
	newCheckpoint := &EnhancedCheckpoint{
		ID:             util.GenerateID("ckpt"),
		ThreadID:       source.ThreadID,
		GraphName:      source.GraphName,
		Version:        source.Version,
		Status:         CheckpointStatusPending,
		CurrentNode:    source.CurrentNode,
		PendingNodes:   append([]string(nil), source.PendingNodes...),
		CompletedNodes: append([]string(nil), source.CompletedNodes...),
		VisitedNodes:   append([]string(nil), source.VisitedNodes...),
		State:          append(json.RawMessage(nil), source.State...),
		Frontier:       append(json.RawMessage(nil), source.Frontier...),
		StateDiff:      append(json.RawMessage(nil), source.StateDiff...),
		ParentID:       checkpointID,
		BranchID:       branchID,
		BranchName:     branchName,
		Metadata:       copyMetadata(source.Metadata),
		Tags:           append([]string(nil), source.Tags...),
		Description:    source.Description,
		CreatedAt:      now,
		UpdatedAt:      now,
		Stats:          cloneCheckpointStats(source.Stats),
	}

	if len(newCheckpoint.State) > 0 {
		newCheckpoint.StateHash = hashState(newCheckpoint.State)
	}

	s.enhanced[newCheckpoint.ID] = cloneEnhancedCheckpoint(newCheckpoint)
	s.threads[newCheckpoint.ThreadID] = appendUniqueID(s.threads[newCheckpoint.ThreadID], newCheckpoint.ID)

	branch.LatestCheckpointID = newCheckpoint.ID
	branch.CheckpointCount = 1

	// 更新源检查点的子列表
	source.ChildIDs = appendUniqueID(source.ChildIDs, newCheckpoint.ID)

	return cloneEnhancedCheckpoint(newCheckpoint), nil
}

// MergeBranch 合并分支
func (s *MemoryEnhancedCheckpointSaver) MergeBranch(ctx context.Context, sourceBranchID, targetBranchID string, strategy MergeStrategy) (*EnhancedCheckpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sourceBranchID == "" {
		return nil, fmt.Errorf("source branch id is required")
	}
	if targetBranchID == "" {
		return nil, fmt.Errorf("target branch id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sourceBranch, ok := s.branches[sourceBranchID]
	if !ok {
		return nil, fmt.Errorf("source branch %s not found", sourceBranchID)
	}

	targetBranch, ok := s.branches[targetBranchID]
	if !ok {
		return nil, fmt.Errorf("target branch %s not found", targetBranchID)
	}
	if sourceBranch.LatestCheckpointID == "" {
		return nil, fmt.Errorf("source branch %s has no latest checkpoint", sourceBranchID)
	}
	if targetBranch.LatestCheckpointID == "" {
		return nil, fmt.Errorf("target branch %s has no latest checkpoint", targetBranchID)
	}

	sourceCP, ok := s.enhanced[sourceBranch.LatestCheckpointID]
	if !ok {
		return nil, fmt.Errorf("source checkpoint not found")
	}

	targetCP, ok := s.enhanced[targetBranch.LatestCheckpointID]
	if !ok {
		return nil, fmt.Errorf("target checkpoint not found")
	}

	// 根据策略合并
	var mergedState json.RawMessage
	switch strategy {
	case MergeStrategyOverwrite:
		mergedState = append(json.RawMessage(nil), sourceCP.State...)
	case MergeStrategyKeepBoth:
		mergedState = append(json.RawMessage(nil), targetCP.State...)
	default:
		// 默认合并策略 - 简单使用源状态
		mergedState = append(json.RawMessage(nil), sourceCP.State...)
	}

	metadata := copyMetadata(targetCP.Metadata)
	metadata["merged_from"] = sourceBranchID
	metadata["merge_strategy"] = string(strategy)

	now := time.Now()

	// 创建合并检查点
	merged := &EnhancedCheckpoint{
		ID:             util.GenerateID("ckpt"),
		ThreadID:       targetCP.ThreadID,
		GraphName:      targetCP.GraphName,
		Version:        targetCP.Version,
		Status:         CheckpointStatusPending,
		CurrentNode:    sourceCP.CurrentNode,
		PendingNodes:   append([]string(nil), sourceCP.PendingNodes...),
		CompletedNodes: mergeSlices(sourceCP.CompletedNodes, targetCP.CompletedNodes),
		VisitedNodes:   mergeSlices(sourceCP.VisitedNodes, targetCP.VisitedNodes),
		State:          mergedState,
		ParentID:       targetCP.ID,
		BranchID:       targetBranchID,
		BranchName:     targetBranch.Name,
		Metadata:       metadata,
		CreatedAt:      now,
		UpdatedAt:      now,
		Stats:          cloneCheckpointStats(targetCP.Stats),
	}

	if len(merged.State) > 0 {
		merged.StateHash = hashState(merged.State)
	}

	s.enhanced[merged.ID] = cloneEnhancedCheckpoint(merged)
	s.threads[merged.ThreadID] = appendUniqueID(s.threads[merged.ThreadID], merged.ID)
	targetCP.ChildIDs = appendUniqueID(targetCP.ChildIDs, merged.ID)

	targetBranch.LatestCheckpointID = merged.ID
	targetBranch.CheckpointCount = countBranchCheckpoints(s.enhanced, targetBranchID)
	targetBranch.UpdatedAt = now

	return cloneEnhancedCheckpoint(merged), nil
}

// Search 搜索检查点
func (s *MemoryEnhancedCheckpointSaver) Search(ctx context.Context, query *CheckpointQuery) ([]*EnhancedCheckpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query == nil {
		query = &CheckpointQuery{}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*EnhancedCheckpoint
	for _, cp := range s.enhanced {
		if matchesQuery(cp, query) {
			result = append(result, cloneEnhancedCheckpoint(cp))
		}
	}

	// 应用分页
	if query.Limit > 0 {
		start := query.Offset
		if start < 0 {
			start = 0
		}
		if start >= len(result) {
			return nil, nil
		}
		end := start + query.Limit
		if end > len(result) {
			end = len(result)
		}
		result = result[start:end]
	}

	return result, nil
}

// Cleanup 清理旧检查点
func (s *MemoryEnhancedCheckpointSaver) Cleanup(ctx context.Context, policy *CleanupPolicy) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if policy == nil {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	branchHeads := make(map[string]struct{})
	if policy.KeepBranchHeads {
		for _, branch := range s.branches {
			if branch.LatestCheckpointID != "" {
				branchHeads[branch.LatestCheckpointID] = struct{}{}
			}
		}
	}

	shouldKeep := func(id string, cp *EnhancedCheckpoint) bool {
		if policy.KeepCompleted && cp.Status == CheckpointStatusCompleted {
			return true
		}
		if policy.KeepTagged && len(cp.Tags) > 0 {
			return true
		}
		if policy.KeepBranchHeads {
			_, ok := branchHeads[id]
			return ok
		}
		return false
	}

	toDelete := make(map[string]struct{})

	// 按年龄清理
	if policy.MaxAge > 0 {
		for id, cp := range s.enhanced {
			if shouldKeep(id, cp) {
				continue
			}
			if now.Sub(cp.CreatedAt) > policy.MaxAge {
				toDelete[id] = struct{}{}
			}
		}
	}

	// 每线程最大数量清理（优先删除最旧且未受保护的检查点）
	if policy.MaxCount > 0 {
		for _, ids := range s.threads {
			liveIDs := make([]string, 0, len(ids))
			seen := make(map[string]struct{}, len(ids))
			for _, id := range ids {
				if _, dup := seen[id]; dup {
					continue
				}
				if _, ok := s.enhanced[id]; !ok {
					continue
				}
				seen[id] = struct{}{}
				liveIDs = append(liveIDs, id)
			}

			if len(liveIDs) <= policy.MaxCount {
				continue
			}

			alreadyMarked := 0
			for _, id := range liveIDs {
				if _, marked := toDelete[id]; marked {
					alreadyMarked++
				}
			}

			needDelete := len(liveIDs) - alreadyMarked - policy.MaxCount
			if needDelete <= 0 {
				continue
			}

			for _, id := range liveIDs {
				if needDelete == 0 {
					break
				}
				if _, marked := toDelete[id]; marked {
					continue
				}
				cp, ok := s.enhanced[id]
				if !ok || shouldKeep(id, cp) {
					continue
				}
				toDelete[id] = struct{}{}
				needDelete--
			}
		}
	}

	if len(toDelete) == 0 {
		return 0, nil
	}

	deleted := 0
	for id := range toDelete {
		cp, ok := s.enhanced[id]
		if !ok {
			continue
		}

		delete(s.enhanced, id)
		s.threads[cp.ThreadID] = removeIDValue(s.threads[cp.ThreadID], id)
		if len(s.threads[cp.ThreadID]) == 0 {
			delete(s.threads, cp.ThreadID)
		}

		if cp.ParentID != "" {
			if parent, ok := s.enhanced[cp.ParentID]; ok {
				parent.ChildIDs = removeIDValue(parent.ChildIDs, id)
			}
		}

		deleted++
	}

	// 清理后重建分支统计与头指针
	for _, branch := range s.branches {
		branch.CheckpointCount = countBranchCheckpoints(s.enhanced, branch.ID)
		if _, ok := s.enhanced[branch.LatestCheckpointID]; !ok {
			branch.LatestCheckpointID = findLatestBranchCheckpointID(s.threads[branch.ThreadID], s.enhanced, branch.ID)
		}
		branch.UpdatedAt = now
	}

	return deleted, nil
}

// ============== 辅助函数 ==============

// hashState 计算状态哈希
func hashState(state json.RawMessage) string {
	// 简化实现：使用内容长度和前几个字节
	if len(state) == 0 {
		return "empty"
	}
	return fmt.Sprintf("%d:%x", len(state), state[:min(32, len(state))])
}

// copyMetadata 复制元数据
func copyMetadata(m map[string]any) map[string]any {
	if m == nil {
		return make(map[string]any)
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

func cloneEnhancedCheckpoint(cp *EnhancedCheckpoint) *EnhancedCheckpoint {
	if cp == nil {
		return nil
	}

	return &EnhancedCheckpoint{
		ID:             cp.ID,
		ThreadID:       cp.ThreadID,
		GraphName:      cp.GraphName,
		Version:        cp.Version,
		Status:         cp.Status,
		CurrentNode:    cp.CurrentNode,
		PendingNodes:   append([]string(nil), cp.PendingNodes...),
		CompletedNodes: append([]string(nil), cp.CompletedNodes...),
		VisitedNodes:   append([]string(nil), cp.VisitedNodes...),
		State:          append(json.RawMessage(nil), cp.State...),
		Frontier:       append(json.RawMessage(nil), cp.Frontier...),
		StateDiff:      append(json.RawMessage(nil), cp.StateDiff...),
		StateHash:      cp.StateHash,
		ParentID:       cp.ParentID,
		BranchID:       cp.BranchID,
		BranchName:     cp.BranchName,
		ChildIDs:       append([]string(nil), cp.ChildIDs...),
		Metadata:       copyMetadata(cp.Metadata),
		Tags:           append([]string(nil), cp.Tags...),
		Description:    cp.Description,
		CreatedAt:      cp.CreatedAt,
		UpdatedAt:      cp.UpdatedAt,
		Stats:          cloneCheckpointStats(cp.Stats),
	}
}

func cloneCheckpointStats(stats *CheckpointStats) *CheckpointStats {
	if stats == nil {
		return nil
	}

	return &CheckpointStats{
		StepCount:     stats.StepCount,
		TotalDuration: stats.TotalDuration,
		NodeDurations: cloneDurationMap(stats.NodeDurations),
		LLMTokens:     stats.LLMTokens,
		ToolCalls:     stats.ToolCalls,
	}
}

func cloneDurationMap(src map[string]time.Duration) map[string]time.Duration {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]time.Duration, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneBranchInfo(branch *BranchInfo) *BranchInfo {
	if branch == nil {
		return nil
	}

	return &BranchInfo{
		ID:                 branch.ID,
		Name:               branch.Name,
		ThreadID:           branch.ThreadID,
		BaseCheckpointID:   branch.BaseCheckpointID,
		LatestCheckpointID: branch.LatestCheckpointID,
		CheckpointCount:    branch.CheckpointCount,
		CreatedAt:          branch.CreatedAt,
		UpdatedAt:          branch.UpdatedAt,
	}
}

func appendUniqueID(ids []string, id string) []string {
	for _, existingID := range ids {
		if existingID == id {
			return ids
		}
	}
	return append(ids, id)
}

func removeIDValue(ids []string, id string) []string {
	if len(ids) == 0 {
		return nil
	}
	result := ids[:0]
	for _, existingID := range ids {
		if existingID != id {
			result = append(result, existingID)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func countBranchCheckpoints(checkpoints map[string]*EnhancedCheckpoint, branchID string) int {
	count := 0
	for _, cp := range checkpoints {
		if cp.BranchID == branchID {
			count++
		}
	}
	return count
}

func findLatestBranchCheckpointID(threadIDs []string, checkpoints map[string]*EnhancedCheckpoint, branchID string) string {
	for i := len(threadIDs) - 1; i >= 0; i-- {
		id := threadIDs[i]
		cp, ok := checkpoints[id]
		if !ok {
			continue
		}
		if cp.BranchID == branchID {
			return id
		}
	}
	return ""
}

// mergeSlices 合并切片（去重）
func mergeSlices(a, b []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// matchesQuery 检查检查点是否匹配查询
func matchesQuery(cp *EnhancedCheckpoint, query *CheckpointQuery) bool {
	if query.ThreadID != "" && cp.ThreadID != query.ThreadID {
		return false
	}
	if query.GraphName != "" && cp.GraphName != query.GraphName {
		return false
	}
	if query.Status != "" && cp.Status != query.Status {
		return false
	}
	if query.BranchID != "" && cp.BranchID != query.BranchID {
		return false
	}
	if query.StartTime != nil && cp.CreatedAt.Before(*query.StartTime) {
		return false
	}
	if query.EndTime != nil && cp.CreatedAt.After(*query.EndTime) {
		return false
	}
	// 检查标签
	if len(query.Tags) > 0 {
		for _, tag := range query.Tags {
			found := false
			for _, cpTag := range cp.Tags {
				if cpTag == tag {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

// min 返回较小值
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// 确保实现了接口
var _ EnhancedCheckpointSaver = (*MemoryEnhancedCheckpointSaver)(nil)
