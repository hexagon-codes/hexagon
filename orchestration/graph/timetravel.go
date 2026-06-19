// Package graph 提供图编排引擎
//
// 本文件实现时间旅行调试功能：
//   - 状态快照：保存每一步执行的状态
//   - 时间回溯：回到任意历史状态
//   - 状态重放：从历史状态继续执行
//   - 差异对比：对比不同时间点的状态
//
// 设计借鉴：
//   - LangGraph: Time Travel 功能
//   - Redux DevTools: 时间旅行调试
//   - Event Sourcing: 事件溯源模式
package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/idgen"
)

// ============== 时间旅行核心 ==============

// Executable 可执行接口，用于时间旅行调试
type Executable interface {
	// ExecuteNode 执行单个节点
	ExecuteNode(ctx context.Context, nodeID string, state map[string]any) (output any, nextNode string, err error)

	// GetEntryPoint 获取入口节点
	GetEntryPoint() string

	// GetNodeName 获取节点名称
	GetNodeName(nodeID string) string
}

// TimeTravelDebugger 时间旅行调试器
//
// 功能：
//   - 记录每一步执行的状态快照
//   - 支持回溯到任意历史状态
//   - 支持从历史状态重新执行
//   - 支持状态差异对比
//
// 使用示例：
//
//	debugger := NewTimeTravelDebugger(graphAdapter)
//	result, _ := debugger.Run(ctx, initialState)
//
//	// 查看历史记录
//	history := debugger.GetHistory()
//
//	// 回溯到某个时间点
//	debugger.GoTo(3)
//
//	// 从该点重新执行
//	result2, _ := debugger.Replay(ctx)
type TimeTravelDebugger struct {
	// 可执行对象
	executable Executable

	// 历史记录
	history []*StateSnapshot

	// 当前位置
	currentIndex int

	// 最大历史记录数
	maxHistory int

	// 启用详细记录
	enableDetailedTrace bool

	// 状态存储后端
	storage SnapshotStorage

	// 错误处理函数（用于处理存储失败等非致命错误）
	errorHandler func(error)

	mu sync.RWMutex
}

// StateSnapshot 状态快照
type StateSnapshot struct {
	// Index 快照索引
	Index int `json:"index"`

	// Timestamp 时间戳
	Timestamp time.Time `json:"timestamp"`

	// NodeID 执行的节点 ID
	NodeID string `json:"node_id"`

	// NodeName 节点名称
	NodeName string `json:"node_name"`

	// State 状态数据（深拷贝）
	State map[string]any `json:"state"`

	// Input 节点输入
	Input any `json:"input,omitempty"`

	// Output 节点输出
	Output any `json:"output,omitempty"`

	// Error 错误信息
	Error string `json:"error,omitempty"`

	// Duration 执行耗时
	Duration time.Duration `json:"duration"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// ParentIndex 父快照索引（用于分支）
	ParentIndex int `json:"parent_index,omitempty"`

	// BranchID 分支 ID
	BranchID string `json:"branch_id,omitempty"`
}

// SnapshotStorage 快照存储接口
type SnapshotStorage interface {
	// Save 保存快照
	Save(ctx context.Context, snapshot *StateSnapshot) error

	// Load 加载快照
	Load(ctx context.Context, index int) (*StateSnapshot, error)

	// LoadRange 加载范围内的快照
	LoadRange(ctx context.Context, start, end int) ([]*StateSnapshot, error)

	// Delete 删除快照
	Delete(ctx context.Context, index int) error

	// Clear 清空所有快照
	Clear(ctx context.Context) error
}

// TimeTravelOption 时间旅行调试器选项
type TimeTravelOption func(*TimeTravelDebugger)

// WithMaxHistory 设置最大历史记录数
func WithMaxHistory(max int) TimeTravelOption {
	return func(d *TimeTravelDebugger) {
		d.maxHistory = max
	}
}

// WithDetailedTrace 启用详细跟踪
func WithDetailedTrace() TimeTravelOption {
	return func(d *TimeTravelDebugger) {
		d.enableDetailedTrace = true
	}
}

// WithSnapshotStorage 设置快照存储
func WithSnapshotStorage(storage SnapshotStorage) TimeTravelOption {
	return func(d *TimeTravelDebugger) {
		d.storage = storage
	}
}

// WithErrorHandler 设置错误处理函数
// 用于处理存储失败等非致命错误
func WithErrorHandler(handler func(error)) TimeTravelOption {
	return func(d *TimeTravelDebugger) {
		d.errorHandler = handler
	}
}

// NewTimeTravelDebugger 创建时间旅行调试器
func NewTimeTravelDebugger(exec Executable, opts ...TimeTravelOption) *TimeTravelDebugger {
	d := &TimeTravelDebugger{
		executable:   exec,
		history:      make([]*StateSnapshot, 0),
		currentIndex: -1,
		maxHistory:   1000,
		storage:      NewMemorySnapshotStorage(),
	}

	for _, opt := range opts {
		opt(d)
	}

	return d
}

// Run 执行并记录历史
func (d *TimeTravelDebugger) Run(ctx context.Context, initialState map[string]any) (map[string]any, error) {
	d.mu.Lock()
	// 清空历史（新执行）
	d.history = make([]*StateSnapshot, 0)
	d.currentIndex = -1
	d.mu.Unlock()

	return d.executeWithTrace(ctx, initialState, "")
}

// executeWithTrace 带跟踪的执行
func (d *TimeTravelDebugger) executeWithTrace(ctx context.Context, initialState map[string]any, branchID string) (map[string]any, error) {
	state := d.cloneState(initialState)

	// 记录初始状态
	d.recordSnapshot(&StateSnapshot{
		NodeID:    "__start__",
		NodeName:  "Start",
		State:     d.cloneState(state),
		Timestamp: time.Now(),
		BranchID:  branchID,
	})

	// 执行图
	currentNode := d.executable.GetEntryPoint()
	for currentNode != "" {
		startTime := time.Now()

		// 执行节点
		output, nextNode, err := d.executable.ExecuteNode(ctx, currentNode, state)
		duration := time.Since(startTime)

		// 创建快照
		snapshot := &StateSnapshot{
			NodeID:    currentNode,
			NodeName:  d.executable.GetNodeName(currentNode),
			State:     d.cloneState(state),
			Output:    output,
			Duration:  duration,
			Timestamp: time.Now(),
			BranchID:  branchID,
		}

		if err != nil {
			snapshot.Error = err.Error()
			d.recordSnapshot(snapshot)
			return state, err
		}

		d.recordSnapshot(snapshot)

		// 移动到下一个节点
		currentNode = nextNode
	}

	// 记录结束状态
	d.recordSnapshot(&StateSnapshot{
		NodeID:    "__end__",
		NodeName:  "End",
		State:     d.cloneState(state),
		Timestamp: time.Now(),
		BranchID:  branchID,
	})

	return state, nil
}

// recordSnapshot 记录快照
func (d *TimeTravelDebugger) recordSnapshot(snapshot *StateSnapshot) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 设置索引和父索引
	snapshot.Index = len(d.history)
	if snapshot.Index > 0 {
		snapshot.ParentIndex = snapshot.Index - 1
	}

	// 添加到历史
	d.history = append(d.history, snapshot)
	d.currentIndex = snapshot.Index

	// 限制历史记录数量
	if d.maxHistory > 0 && len(d.history) > d.maxHistory {
		d.history = d.history[1:]
		// 更新索引
		for i := range d.history {
			d.history[i].Index = i
			if d.history[i].ParentIndex > 0 {
				d.history[i].ParentIndex--
			}
		}
		d.currentIndex--
	}

	// 保存到存储
	if d.storage != nil {
		if err := d.storage.Save(context.Background(), snapshot); err != nil {
			if d.errorHandler != nil {
				d.errorHandler(fmt.Errorf("failed to save snapshot: %w", err))
			}
		}
	}
}

// cloneState 深拷贝状态
func (d *TimeTravelDebugger) cloneState(state map[string]any) map[string]any {
	if state == nil {
		return make(map[string]any)
	}

	// 使用 JSON 序列化进行深拷贝
	data, err := json.Marshal(state)
	if err != nil {
		return make(map[string]any)
	}

	var clone map[string]any
	if err := json.Unmarshal(data, &clone); err != nil {
		return make(map[string]any)
	}

	return clone
}

// GetHistory 获取历史记录
func (d *TimeTravelDebugger) GetHistory() []*StateSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]*StateSnapshot, len(d.history))
	copy(result, d.history)
	return result
}

// GetSnapshot 获取指定索引的快照
func (d *TimeTravelDebugger) GetSnapshot(index int) (*StateSnapshot, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if index < 0 || index >= len(d.history) {
		return nil, fmt.Errorf("invalid snapshot index: %d", index)
	}
	return d.history[index], nil
}

// CurrentIndex 获取当前索引
func (d *TimeTravelDebugger) CurrentIndex() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.currentIndex
}

// GoTo 跳转到指定快照
func (d *TimeTravelDebugger) GoTo(index int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if index < 0 || index >= len(d.history) {
		return fmt.Errorf("invalid snapshot index: %d", index)
	}

	d.currentIndex = index
	return nil
}

// GoBack 后退一步
func (d *TimeTravelDebugger) GoBack() error {
	return d.GoTo(d.currentIndex - 1)
}

// GoForward 前进一步
func (d *TimeTravelDebugger) GoForward() error {
	return d.GoTo(d.currentIndex + 1)
}

// Replay 从当前位置重新执行
func (d *TimeTravelDebugger) Replay(ctx context.Context) (map[string]any, error) {
	d.mu.RLock()
	if d.currentIndex < 0 || d.currentIndex >= len(d.history) {
		d.mu.RUnlock()
		return nil, fmt.Errorf("invalid current index")
	}

	snapshot := d.history[d.currentIndex]
	branchID := fmt.Sprintf("branch_%d_%s", d.currentIndex, idgen.NanoID())
	state := d.cloneState(snapshot.State)
	startNode := snapshot.NodeID
	d.mu.RUnlock()

	// 从快照状态继续执行
	return d.executeFromSnapshot(ctx, state, startNode, branchID)
}

// ReplayFrom 从指定快照重新执行
func (d *TimeTravelDebugger) ReplayFrom(ctx context.Context, index int) (map[string]any, error) {
	if err := d.GoTo(index); err != nil {
		return nil, err
	}
	return d.Replay(ctx)
}

// executeFromSnapshot 从快照开始执行
func (d *TimeTravelDebugger) executeFromSnapshot(ctx context.Context, state map[string]any, startNode string, branchID string) (map[string]any, error) {
	if startNode == "__end__" {
		// 已经是最后一个节点
		return state, nil
	}

	// 获取下一个节点
	currentNode := startNode
	if currentNode == "__start__" {
		currentNode = d.executable.GetEntryPoint()
	}

	// 继续执行
	for currentNode != "" {
		startTime := time.Now()
		output, nextNode, err := d.executable.ExecuteNode(ctx, currentNode, state)
		duration := time.Since(startTime)

		newSnapshot := &StateSnapshot{
			NodeID:    currentNode,
			NodeName:  d.executable.GetNodeName(currentNode),
			State:     d.cloneState(state),
			Output:    output,
			Duration:  duration,
			Timestamp: time.Now(),
			BranchID:  branchID,
		}

		if err != nil {
			newSnapshot.Error = err.Error()
			d.recordSnapshot(newSnapshot)
			return state, err
		}

		d.recordSnapshot(newSnapshot)
		currentNode = nextNode
	}

	return state, nil
}

// ============== 状态差异对比 ==============

// StateDiff 状态差异
type StateDiff struct {
	// Key 键名
	Key string `json:"key"`

	// OldValue 旧值
	OldValue any `json:"old_value,omitempty"`

	// NewValue 新值
	NewValue any `json:"new_value,omitempty"`

	// Type 差异类型：added, removed, changed
	Type string `json:"type"`
}

// Compare 比较两个快照的差异
func (d *TimeTravelDebugger) Compare(index1, index2 int) ([]StateDiff, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if index1 < 0 || index1 >= len(d.history) {
		return nil, fmt.Errorf("invalid snapshot index: %d", index1)
	}
	if index2 < 0 || index2 >= len(d.history) {
		return nil, fmt.Errorf("invalid snapshot index: %d", index2)
	}

	snapshot1 := d.history[index1]
	snapshot2 := d.history[index2]

	return d.compareStates(snapshot1.State, snapshot2.State), nil
}

// compareStates 比较两个状态
func (d *TimeTravelDebugger) compareStates(state1, state2 map[string]any) []StateDiff {
	var diffs []StateDiff

	// 检查 state1 中的键
	for key, val1 := range state1 {
		if val2, exists := state2[key]; exists {
			// 键存在于两个状态中
			if !d.deepEqual(val1, val2) {
				diffs = append(diffs, StateDiff{
					Key:      key,
					OldValue: val1,
					NewValue: val2,
					Type:     "changed",
				})
			}
		} else {
			// 键只存在于 state1 中
			diffs = append(diffs, StateDiff{
				Key:      key,
				OldValue: val1,
				Type:     "removed",
			})
		}
	}

	// 检查 state2 中新增的键
	for key, val2 := range state2 {
		if _, exists := state1[key]; !exists {
			diffs = append(diffs, StateDiff{
				Key:      key,
				NewValue: val2,
				Type:     "added",
			})
		}
	}

	return diffs
}

// deepEqual 深度比较
func (d *TimeTravelDebugger) deepEqual(a, b any) bool {
	// 使用 JSON 序列化比较
	jsonA, _ := json.Marshal(a)
	jsonB, _ := json.Marshal(b)
	return string(jsonA) == string(jsonB)
}

// ============== 分支管理 ==============

// GetBranches 获取所有分支
func (d *TimeTravelDebugger) GetBranches() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	branches := make(map[string]bool)
	for _, snapshot := range d.history {
		if snapshot.BranchID != "" {
			branches[snapshot.BranchID] = true
		}
	}

	result := make([]string, 0, len(branches))
	for branch := range branches {
		result = append(result, branch)
	}
	return result
}

// GetBranchHistory 获取指定分支的历史
func (d *TimeTravelDebugger) GetBranchHistory(branchID string) []*StateSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []*StateSnapshot
	for _, snapshot := range d.history {
		if snapshot.BranchID == branchID || (branchID == "" && snapshot.BranchID == "") {
			result = append(result, snapshot)
		}
	}
	return result
}

// ============== 搜索和过滤 ==============

// SearchSnapshots 搜索快照
func (d *TimeTravelDebugger) SearchSnapshots(predicate func(*StateSnapshot) bool) []*StateSnapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []*StateSnapshot
	for _, snapshot := range d.history {
		if predicate(snapshot) {
			result = append(result, snapshot)
		}
	}
	return result
}

// FindByNodeID 按节点 ID 查找
func (d *TimeTravelDebugger) FindByNodeID(nodeID string) []*StateSnapshot {
	return d.SearchSnapshots(func(s *StateSnapshot) bool {
		return s.NodeID == nodeID
	})
}

// FindErrors 查找错误
func (d *TimeTravelDebugger) FindErrors() []*StateSnapshot {
	return d.SearchSnapshots(func(s *StateSnapshot) bool {
		return s.Error != ""
	})
}

// FindByTimeRange 按时间范围查找
func (d *TimeTravelDebugger) FindByTimeRange(start, end time.Time) []*StateSnapshot {
	return d.SearchSnapshots(func(s *StateSnapshot) bool {
		return !s.Timestamp.Before(start) && !s.Timestamp.After(end)
	})
}

// ============== 导出和导入 ==============

// Export 导出历史记录
func (d *TimeTravelDebugger) Export() ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return json.MarshalIndent(d.history, "", "  ")
}

// Import 导入历史记录
func (d *TimeTravelDebugger) Import(data []byte) error {
	var history []*StateSnapshot
	if err := json.Unmarshal(data, &history); err != nil {
		return err
	}

	d.mu.Lock()
	d.history = history
	if len(history) > 0 {
		d.currentIndex = len(history) - 1
	} else {
		d.currentIndex = -1
	}
	d.mu.Unlock()

	return nil
}

// Clear 清空历史
func (d *TimeTravelDebugger) Clear() {
	d.mu.Lock()
	d.history = make([]*StateSnapshot, 0)
	d.currentIndex = -1
	d.mu.Unlock()

	if d.storage != nil {
		_ = d.storage.Clear(context.Background())
	}
}

// ============== 内存存储实现 ==============

// MemorySnapshotStorage 内存快照存储
type MemorySnapshotStorage struct {
	snapshots map[int]*StateSnapshot
	mu        sync.RWMutex
}

// NewMemorySnapshotStorage 创建内存快照存储
func NewMemorySnapshotStorage() *MemorySnapshotStorage {
	return &MemorySnapshotStorage{
		snapshots: make(map[int]*StateSnapshot),
	}
}

// Save 保存快照
func (s *MemorySnapshotStorage) Save(ctx context.Context, snapshot *StateSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[snapshot.Index] = snapshot
	return nil
}

// Load 加载快照
func (s *MemorySnapshotStorage) Load(ctx context.Context, index int) (*StateSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot, exists := s.snapshots[index]
	if !exists {
		return nil, fmt.Errorf("snapshot not found: %d", index)
	}
	return snapshot, nil
}

// LoadRange 加载范围内的快照
func (s *MemorySnapshotStorage) LoadRange(ctx context.Context, start, end int) ([]*StateSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*StateSnapshot
	for i := start; i <= end; i++ {
		if snapshot, exists := s.snapshots[i]; exists {
			result = append(result, snapshot)
		}
	}
	return result, nil
}

// Delete 删除快照
func (s *MemorySnapshotStorage) Delete(ctx context.Context, index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snapshots, index)
	return nil
}

// Clear 清空所有快照
func (s *MemorySnapshotStorage) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots = make(map[int]*StateSnapshot)
	return nil
}

// ============== 调试视图 ==============

// DebugView 调试视图
type DebugView struct {
	// TotalSnapshots 总快照数
	TotalSnapshots int `json:"total_snapshots"`

	// CurrentIndex 当前索引
	CurrentIndex int `json:"current_index"`

	// Branches 分支列表
	Branches []string `json:"branches"`

	// Errors 错误数量
	Errors int `json:"errors"`

	// TotalDuration 总耗时
	TotalDuration time.Duration `json:"total_duration"`

	// NodeExecutions 节点执行统计
	NodeExecutions map[string]int `json:"node_executions"`
}

// GetDebugView 获取调试视图
func (d *TimeTravelDebugger) GetDebugView() *DebugView {
	d.mu.RLock()
	defer d.mu.RUnlock()

	view := &DebugView{
		TotalSnapshots: len(d.history),
		CurrentIndex:   d.currentIndex,
		Branches:       d.GetBranches(),
		NodeExecutions: make(map[string]int),
	}

	for _, snapshot := range d.history {
		if snapshot.Error != "" {
			view.Errors++
		}
		view.TotalDuration += snapshot.Duration
		view.NodeExecutions[snapshot.NodeID]++
	}

	return view
}
