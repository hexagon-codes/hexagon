// pregel.go - Pregel 循环图编排模式
//
// Pregel 是 Google 的图计算模型，适合需要迭代计算的循环图。
// 借鉴 Eino 框架的精细实现，支持：
//   - 循环图执行（有环图）
//   - AnyPredecessor 触发模式
//   - 超级步（Superstep）并行执行
//   - 最大迭代限制
//
// 使用场景：
//   - 多轮对话推理
//   - 迭代优化算法
//   - 自我修正循环
//   - 复杂决策树

package graph

import (
	"context"
	"fmt"
)

// ============== Pregel 触发模式 ==============

// TriggerMode 节点触发模式
type TriggerMode int

const (
	// TriggerAllPredecessors 所有前驱完成后触发（默认 DAG 模式）
	// 节点等待所有入边的前驱节点都执行完成后才开始执行
	TriggerAllPredecessors TriggerMode = iota

	// TriggerAnyPredecessor 任意前驱完成后触发（Pregel 模式）
	// 节点在任意一个前驱节点完成后就可以开始执行
	// 适用于循环图，允许节点在不同轮次多次执行
	TriggerAnyPredecessor
)

// String 返回触发模式的字符串表示
func (m TriggerMode) String() string {
	switch m {
	case TriggerAllPredecessors:
		return "all_predecessors"
	case TriggerAnyPredecessor:
		return "any_predecessor"
	default:
		return "unknown"
	}
}

// ============== Pregel 配置 ==============

// PregelStateMerger 状态合并器接口
// 用于在 Pregel 并行执行时合并多个节点的输出状态
type PregelStateMerger[S State] interface {
	// Merge 合并多个状态为一个
	// base 是执行前的基础状态
	// states 是各节点执行后的状态列表
	Merge(base S, states []S) S
}

// DefaultPregelMerger 显式选择最后结果的合并器；仅在调用者注入时生效。
type DefaultPregelMerger[S State] struct{}

// Merge 使用最后一个状态
func (m *DefaultPregelMerger[S]) Merge(base S, states []S) S {
	if len(states) == 0 {
		return base
	}
	return states[len(states)-1]
}

// PregelConfig Pregel 执行配置
type PregelConfig struct {
	RetryPolicy *RetryPolicy
	NodeTimeout int64
	// MaxSupersteps 最大超级步数量（防止无限循环）
	// 默认 100
	MaxSupersteps int

	// TriggerMode 默认触发模式
	// 默认 TriggerAnyPredecessor
	TriggerMode TriggerMode

	// ParallelExecution 是否并行执行同一超级步内的节点
	// 默认 true
	ParallelExecution bool

	// TerminationCheck 终止检查函数
	// 返回 true 表示应该终止迭代
	// 如果为 nil，则只检查是否到达 END 节点
	TerminationCheck func(step int, activeNodes []string) bool

	// Debug 调试模式
	Debug bool
}

// DefaultPregelConfig 返回默认的 Pregel 配置
func DefaultPregelConfig() *PregelConfig {
	return &PregelConfig{
		MaxSupersteps:     100,
		TriggerMode:       TriggerAnyPredecessor,
		ParallelExecution: true,
		TerminationCheck:  nil,
		Debug:             false,
	}
}

// PregelOption Pregel 配置选项
type PregelOption func(*PregelConfig)

// WithMaxSupersteps 设置最大超级步数量
func WithMaxSupersteps(n int) PregelOption {
	return func(c *PregelConfig) {
		if n > 0 {
			c.MaxSupersteps = n
		}
	}
}

// WithPregelTriggerMode 设置触发模式
func WithPregelTriggerMode(mode TriggerMode) PregelOption {
	return func(c *PregelConfig) {
		c.TriggerMode = mode
	}
}

// WithParallelExecution 设置是否并行执行
func WithParallelExecution(parallel bool) PregelOption {
	return func(c *PregelConfig) {
		c.ParallelExecution = parallel
	}
}

// WithTerminationCheck 设置终止检查函数
func WithTerminationCheck(fn func(step int, activeNodes []string) bool) PregelOption {
	return func(c *PregelConfig) {
		c.TerminationCheck = fn
	}
}

// WithPregelDebug 设置调试模式
func WithPregelDebug(debug bool) PregelOption {
	return func(c *PregelConfig) {
		c.Debug = debug
	}
}

func WithPregelRetryPolicy(policy *RetryPolicy) PregelOption {
	return func(c *PregelConfig) { c.RetryPolicy = policy }
}
func WithPregelNodeTimeout(milliseconds int64) PregelOption {
	return func(c *PregelConfig) { c.NodeTimeout = milliseconds }
}

// PregelExecutorOption PregelExecutor 配置选项
type PregelExecutorOption[S State] func(*PregelExecutor[S])

// WithPregelMerger 设置状态合并器
// 用于在并行执行时正确合并多个节点的输出状态
func WithPregelMerger[S State](merger PregelStateMerger[S]) PregelExecutorOption[S] {
	return func(pe *PregelExecutor[S]) {
		pe.merger = merger
	}
}

// ============== Pregel 执行器 ==============

// PregelExecutor Pregel 图执行器
// 支持循环图的迭代执行
type PregelExecutor[S State] struct {
	graph  *Graph[S]
	config *PregelConfig

	// 状态合并器
	merger PregelStateMerger[S]
}

// NewPregelExecutor 创建 Pregel 执行器
func NewPregelExecutor[S State](g *Graph[S], opts ...PregelOption) *PregelExecutor[S] {
	config := DefaultPregelConfig()
	if g.plan != nil {
		config.TriggerMode = g.plan.TriggerMode
	}
	for _, opt := range opts {
		opt(config)
	}

	pe := &PregelExecutor[S]{graph: g, config: config}

	return pe
}

// NewPregelExecutorWithMerger 创建带状态合并器的 Pregel 执行器
func NewPregelExecutorWithMerger[S State](g *Graph[S], merger PregelStateMerger[S], opts ...PregelOption) *PregelExecutor[S] {
	pe := NewPregelExecutor(g, opts...)
	if merger != nil {
		pe.merger = merger
	}
	return pe
}

// Run 执行 Pregel 图
// 返回最终状态和执行的超级步数量
func (pe *PregelExecutor[S]) Run(ctx context.Context, initialState S) (S, int, error) {
	if !pe.graph.compiled || pe.graph.plan == nil {
		return initialState, 0, fmt.Errorf("graph not compiled")
	}
	if pe.graph.plan.Mode != GraphModePregel {
		return initialState, 0, fmt.Errorf("graph was not compiled in Pregel mode")
	}
	if pe.config.TriggerMode != pe.graph.plan.TriggerMode {
		return initialState, 0, fmt.Errorf("trigger mode differs from compiled graph plan")
	}
	local := *pe.graph
	if pe.graph.frozen != nil {
		local = *pe.graph.frozen
	}
	local.frozen = nil
	if pe.merger != nil {
		local.merger = pe.merger.Merge
	}
	x := newPlanExecutor(&local, initialState)
	x.pregel = pe.config
	x.defaults = pe.config.RetryPolicy
	x.defaultTimeout = pe.config.NodeTimeout
	state, err := x.run(ctx)
	return state, int(x.frontier.Activation), err
}

// ============== Graph 扩展方法 ==============

// RunPregelMode 以 Pregel 模式执行图
// 支持循环图和迭代执行
func (g *Graph[S]) RunPregelMode(ctx context.Context, initialState S, opts ...PregelOption) (S, int, error) {
	executor := NewPregelExecutor(g, opts...)
	return executor.Run(ctx, initialState)
}

// StreamPregelMode 以 Pregel 模式流式执行图
// 每个超级步发送一个事件
func (g *Graph[S]) StreamPregelMode(ctx context.Context, initialState S, opts ...PregelOption) (<-chan PregelEvent[S], error) {
	if !g.compiled || g.plan == nil {
		return nil, fmt.Errorf("graph not compiled")
	}
	if g.plan.Mode != GraphModePregel {
		return nil, fmt.Errorf("graph was not compiled in Pregel mode")
	}
	pe := NewPregelExecutor(g, opts...)
	if pe.config.TriggerMode != g.plan.TriggerMode {
		return nil, fmt.Errorf("trigger mode differs from compiled graph plan")
	}
	events := make(chan PregelEvent[S], 10)
	go func() {
		defer close(events)
		x := newPlanExecutor(g, initialState)
		x.pregel = pe.config
		x.defaults = pe.config.RetryPolicy
		x.defaultTimeout = pe.config.NodeTimeout
		x.observe = func(event StreamEvent[S]) error {
			kind := PregelEventSuperstepStart
			if event.Type == EventTypeNodeEnd {
				kind = PregelEventSuperstepEnd
			}
			select {
			case events <- PregelEvent[S]{Type: kind, Superstep: int(x.frontier.Activation), State: event.State, ActiveNodes: append([]string(nil), x.frontier.Ready...)}:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		state, err := x.run(ctx)
		kind := PregelEventComplete
		if err != nil {
			kind = PregelEventError
		}
		select {
		case events <- PregelEvent[S]{Type: kind, State: state, Error: err, Superstep: int(x.frontier.Activation)}:
		case <-ctx.Done():
		}
	}()
	return events, nil
}

// PregelEvent Pregel 执行事件
type PregelEvent[S State] struct {
	// Type 事件类型
	Type PregelEventType

	// Superstep 当前超级步编号
	Superstep int

	// ActiveNodes 活跃节点列表
	ActiveNodes []string

	// State 当前状态
	State S

	// Error 错误信息
	Error error
}

// PregelEventType Pregel 事件类型
type PregelEventType int

const (
	// PregelEventSuperstepStart 超级步开始
	PregelEventSuperstepStart PregelEventType = iota
	// PregelEventSuperstepEnd 超级步结束
	PregelEventSuperstepEnd
	// PregelEventComplete 执行完成
	PregelEventComplete
	// PregelEventError 错误
	PregelEventError
)

// String 返回事件类型的字符串表示
func (t PregelEventType) String() string {
	switch t {
	case PregelEventSuperstepStart:
		return "superstep_start"
	case PregelEventSuperstepEnd:
		return "superstep_end"
	case PregelEventComplete:
		return "complete"
	case PregelEventError:
		return "error"
	default:
		return "unknown"
	}
}
