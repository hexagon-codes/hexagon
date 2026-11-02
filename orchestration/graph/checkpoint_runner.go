// Package graph 提供图编排引擎
//
// 本文件实现检查点恢复执行器：
//   - 从任意检查点恢复执行
//   - 支持断点续跑
//   - 支持分支执行
package graph

import (
	"context"
	"encoding/json"
	"errors"

	"fmt"
	"time"
)

// CheckpointRunner 检查点恢复执行器
// 支持从任意检查点恢复图执行
type CheckpointRunner[S State] struct {
	graph  *Graph[S]
	saver  EnhancedCheckpointSaver
	config *CheckpointRunnerConfig

	// 当前执行状态
	currentCheckpoint *EnhancedCheckpoint
	threadID          string
	branchID          string
}

// CheckpointRunnerConfig 检查点执行器配置
type CheckpointRunnerConfig struct {
	// AutoSave 保留的历史配置；恢复结果与采用前沿仍逐节点保存，不能关闭。
	AutoSave bool
	// SaveInterval 不再控制恢复事实的保存间隔。
	SaveInterval int
	// SaveOnError 错误时是否保存检查点
	SaveOnError bool
	// SaveOnInterrupt 中断时是否保存检查点
	SaveOnInterrupt bool
	// MaxRetries 最大重试次数
	MaxRetries int
	// RetryDelay 重试延迟
	RetryDelay time.Duration
	// NodeTimeout 为节点未显式设置时继承的毫秒时限。
	NodeTimeout int64
}

// DefaultCheckpointRunnerConfig 默认配置
func DefaultCheckpointRunnerConfig() *CheckpointRunnerConfig {
	return &CheckpointRunnerConfig{
		AutoSave:        true,
		SaveInterval:    1,
		SaveOnError:     true,
		SaveOnInterrupt: true,
		MaxRetries:      0,
		RetryDelay:      time.Second,
	}
}

// NewCheckpointRunner 创建检查点执行器
func NewCheckpointRunner[S State](g *Graph[S], saver EnhancedCheckpointSaver, config *CheckpointRunnerConfig) *CheckpointRunner[S] {
	if g != nil && g.frozen != nil {
		g = g.frozen
	}
	if config == nil {
		config = DefaultCheckpointRunnerConfig()
	}
	return &CheckpointRunner[S]{
		graph:  g,
		saver:  saver,
		config: config,
	}
}

// Run 从初始状态运行
func (r *CheckpointRunner[S]) Run(ctx context.Context, threadID string, initialState S) (S, error) {
	if r.graph == nil || !r.graph.compiled || r.graph.plan == nil {
		return initialState, fmt.Errorf("graph not compiled")
	}
	if r.saver == nil {
		return initialState, fmt.Errorf("checkpoint saver is required")
	}
	r.threadID = threadID

	// 创建初始检查点
	stateBytes, err := r.graph.codec().Encode(initialState)
	if err != nil {
		return initialState, fmt.Errorf("marshal initial state: %w", err)
	}

	r.currentCheckpoint = &EnhancedCheckpoint{
		ThreadID:       threadID,
		GraphName:      r.graph.Name,
		Version:        CheckpointVersion{Major: 1, Minor: 0, Patch: 0},
		Status:         CheckpointStatusRunning,
		CurrentNode:    START,
		PendingNodes:   r.getInitialPendingNodes(),
		CompletedNodes: []string{},
		VisitedNodes:   []string{START},
		State:          stateBytes,
		Stats:          &CheckpointStats{StepCount: 0},
		Metadata:       make(map[string]any),
	}

	frontier, err := r.graph.encodeFrontier(newPlanExecutor(r.graph, initialState).frontier)
	if err != nil {
		return initialState, err
	}
	r.currentCheckpoint.Frontier = frontier
	if err := r.saver.SaveEnhanced(ctx, r.currentCheckpoint); err != nil {
		return initialState, fmt.Errorf("save initial checkpoint: %w", err)
	}

	return r.executeFromCheckpoint(ctx, initialState)
}

// Resume 从检查点恢复执行
func (r *CheckpointRunner[S]) Resume(ctx context.Context, checkpointID string) (S, error) {
	var zero S

	// 加载检查点
	checkpoint, err := r.saver.LoadEnhancedByID(ctx, checkpointID)
	if err != nil {
		return zero, fmt.Errorf("load checkpoint: %w", err)
	}

	r.currentCheckpoint = checkpoint
	r.threadID = checkpoint.ThreadID
	r.branchID = checkpoint.BranchID

	// 反序列化状态
	state, err := r.graph.codec().Decode(checkpoint.State)
	if err != nil {
		return zero, fmt.Errorf("unmarshal state: %w", err)
	}

	if _, err := r.graph.decodeFrontier(checkpoint.Frontier); err != nil {
		return zero, err
	}
	if checkpoint.Status == CheckpointStatusCompleted {
		return state, nil
	}
	if len(checkpoint.Frontier) == 0 {
		return zero, fmt.Errorf("checkpoint is missing its graph execution specification")
	}
	// 更新检查点状态
	checkpoint.Status = CheckpointStatusRunning
	if err := r.saver.SaveEnhanced(ctx, checkpoint); err != nil {
		return zero, fmt.Errorf("save checkpoint: %w", err)
	}

	return r.executeFromCheckpoint(ctx, state)
}

// ResumeFromLatest 从最新检查点恢复
func (r *CheckpointRunner[S]) ResumeFromLatest(ctx context.Context, threadID string) (S, error) {
	var zero S

	checkpoint, err := r.saver.LoadEnhanced(ctx, threadID)
	if err != nil {
		return zero, fmt.Errorf("load latest checkpoint: %w", err)
	}

	return r.Resume(ctx, checkpoint.ID)
}

// Fork 从检查点创建分支并执行
func (r *CheckpointRunner[S]) Fork(ctx context.Context, checkpointID string, branchName string, modifyState func(S) S) (S, error) {
	var zero S

	// 创建分支
	newCheckpoint, err := r.saver.CreateBranch(ctx, checkpointID, branchName)
	if err != nil {
		return zero, fmt.Errorf("create branch: %w", err)
	}

	// 反序列化状态
	state, err := r.graph.codec().Decode(newCheckpoint.State)
	if err != nil {
		return zero, fmt.Errorf("unmarshal state: %w", err)
	}

	// 修改状态（如果提供了修改函数）
	if modifyState != nil {
		state = modifyState(state)
		stateBytes, err := r.graph.codec().Encode(state)
		if err != nil {
			return zero, fmt.Errorf("marshal modified state: %w", err)
		}
		newCheckpoint.State = stateBytes
	}

	r.currentCheckpoint = newCheckpoint
	r.threadID = newCheckpoint.ThreadID
	r.branchID = newCheckpoint.BranchID

	return r.executeFromCheckpoint(ctx, state)
}

// executeFromCheckpoint 从检查点执行
func (r *CheckpointRunner[S]) executeFromCheckpoint(ctx context.Context, state S) (S, error) {
	startTime := time.Now()
	x := newPlanExecutor(r.graph, state)
	if len(r.currentCheckpoint.Frontier) > 0 {
		frontier, err := r.graph.decodeFrontier(r.currentCheckpoint.Frontier)
		if err != nil {
			return state, err
		}
		x.frontier = frontier
	} else {
		return state, fmt.Errorf("checkpoint is missing its graph execution specification")
	}
	x.defaults = &RetryPolicy{MaxRetries: r.config.MaxRetries, InitialDelay: r.config.RetryDelay.Milliseconds(), Multiplier: 1}
	x.defaultTimeout = r.config.NodeTimeout
	x.persist = func(saveCtx context.Context, next S, frontier *graphFrontier) error {
		return r.saveFrontier(saveCtx, next, frontier)
	}
	x.observe = func(event StreamEvent[S]) error {
		if event.Type == EventTypeNodeEnd {
			r.currentCheckpoint.CurrentNode = event.NodeName
			r.currentCheckpoint.VisitedNodes = append(r.currentCheckpoint.VisitedNodes, event.NodeName)
			r.currentCheckpoint.Stats.StepCount++
		}
		return nil
	}
	final, err := x.run(ctx)
	if err != nil {
		if _, ok := graphWaitingSignal(ctx, err); ok {
			r.currentCheckpoint.Status = CheckpointStatusInterrupted
		} else if ctx.Err() != nil {
			r.currentCheckpoint.Status = CheckpointStatusInterrupted
		} else {
			r.currentCheckpoint.Status = CheckpointStatusFailed
		}
		r.currentCheckpoint.Metadata["error"] = err.Error()
		if r.config.SaveOnError || r.config.SaveOnInterrupt {
			if saveErr := r.saveFrontier(context.WithoutCancel(ctx), final, x.frontier); saveErr != nil {
				return final, errors.Join(err, saveErr)
			}
		}
		return final, err
	}
	r.currentCheckpoint.Status = CheckpointStatusCompleted
	r.currentCheckpoint.CurrentNode = END
	r.currentCheckpoint.Stats.TotalDuration = time.Since(startTime)
	if err := r.saveFrontier(ctx, final, x.frontier); err != nil {
		return final, err
	}
	return final, nil
}

// executeNode 执行单个节点
func (r *CheckpointRunner[S]) executeNode(ctx context.Context, nodeName string, state S) (S, error) {
	node, ok := r.graph.Nodes[nodeName]
	if !ok {
		return state, fmt.Errorf("node %s not found", nodeName)
	}

	defaults := &RetryPolicy{MaxRetries: r.config.MaxRetries, InitialDelay: r.config.RetryDelay.Milliseconds(), Multiplier: 1}
	return executeNodeHandler(ctx, node, state, defaults)
}

// getSuccessors 获取后继节点
func (r *CheckpointRunner[S]) getSuccessors(nodeName string, state S) []string {
	var successors []string

	// 检查条件边
	if condEdges, ok := r.graph.conditionalEdges[nodeName]; ok {
		for _, ce := range condEdges {
			label := ce.router(state)
			if target, ok := ce.edges[label]; ok {
				successors = append(successors, target)
			}
		}
	}

	// 检查普通边
	if targets, ok := r.graph.adjacency[nodeName]; ok {
		successors = append(successors, targets...)
	}

	return successors
}

// getInitialPendingNodes 获取初始待执行节点
func (r *CheckpointRunner[S]) getInitialPendingNodes() []string {
	if targets, ok := r.graph.adjacency[START]; ok {
		return targets
	}
	if r.graph.EntryPoint != "" {
		return []string{r.graph.EntryPoint}
	}
	return nil
}

// saveInterruptCheckpoint 保存中断检查点
func (r *CheckpointRunner[S]) saveInterruptCheckpoint(ctx context.Context, state S, pendingNodes []string) {
	stateBytes, marshalErr := json.Marshal(state)
	if marshalErr != nil {
		// 序列化失败时记录错误到元数据，使用空状态继续保存以保留其他上下文
		r.currentCheckpoint.Metadata["marshal_error"] = marshalErr.Error()
	}
	r.currentCheckpoint.State = stateBytes
	r.currentCheckpoint.PendingNodes = pendingNodes
	r.currentCheckpoint.Status = CheckpointStatusInterrupted
	r.saver.SaveEnhanced(context.Background(), r.currentCheckpoint)
}

// saveErrorCheckpoint 保存错误检查点
func (r *CheckpointRunner[S]) saveErrorCheckpoint(ctx context.Context, state S, failedNode string, pendingNodes []string, err error) {
	stateBytes, marshalErr := json.Marshal(state)
	if marshalErr != nil {
		r.currentCheckpoint.Metadata["marshal_error"] = marshalErr.Error()
	}
	r.currentCheckpoint.State = stateBytes
	r.currentCheckpoint.PendingNodes = append([]string{failedNode}, pendingNodes...)
	r.currentCheckpoint.Status = CheckpointStatusFailed
	r.currentCheckpoint.Metadata["error"] = err.Error()
	r.currentCheckpoint.Metadata["failed_node"] = failedNode
	r.saver.SaveEnhanced(context.Background(), r.currentCheckpoint)
}

// GetCurrentCheckpoint 获取当前检查点
func (r *CheckpointRunner[S]) GetCurrentCheckpoint() *EnhancedCheckpoint {
	return r.currentCheckpoint
}

// GetHistory 获取执行历史
func (r *CheckpointRunner[S]) GetHistory(ctx context.Context, limit int) ([]*EnhancedCheckpoint, error) {
	if r.currentCheckpoint == nil {
		return nil, nil
	}
	return r.saver.GetHistory(ctx, r.currentCheckpoint.ID, limit)
}
