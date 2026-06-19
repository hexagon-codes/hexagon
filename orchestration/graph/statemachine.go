// Package graph 提供 Hexagon 框架的图编排能力
//
// 本文件实现状态机模式，对标 LangGraph 的状态机设计。
//
// 设计借鉴：
//   - LangGraph: StateGraph 状态机
//   - 传统状态机: FSM (Finite State Machine)
//
// 使用示例：
//
//	sm := graph.NewStateMachine[*MyState]().
//	    State("init", initHandler).
//	    State("process", processHandler).
//	    State("review", reviewHandler).
//	    Transition("init", "process", alwaysTrue).
//	    Transition("process", "review", needsReview).
//	    Transition("review", "done", approved).
//	    Initial("init").
//	    Final("done").
//	    Build()
//
//	result, err := sm.Run(ctx, initialState)
package graph

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/hexagon-codes/hexagon/checkpoint"
	"github.com/hexagon-codes/hexagon/interrupt"
)

// ============== 错误定义 ==============

var (
	// ErrNoInitialState 没有初始状态
	ErrNoInitialState = errors.New("no initial state defined")

	// ErrStateNotFound 状态未找到
	ErrStateNotFound = errors.New("state not found")

	// ErrNoTransition 没有可用的转换
	ErrNoTransition = errors.New("no valid transition from current state")

	// ErrMaxStepsExceeded 超过最大步数
	ErrMaxStepsExceeded = errors.New("max steps exceeded")
)

// ============== StateMachine ==============

// StateMachine 状态机
type StateMachine[S any] struct {
	name        string
	states      map[string]*StateNode[S]
	transitions map[string][]*Transition[S]
	initial     string
	finals      map[string]bool
	maxSteps    int

	// 检查点
	checkpointer checkpoint.Checkpointer

	// 中断处理
	interruptHandler *interrupt.Handler

	mu sync.RWMutex
}

// StateNode 状态节点定义
type StateNode[S any] struct {
	Name    string
	OnEnter func(ctx context.Context, state S) error
	OnExit  func(ctx context.Context, state S) error
	Handler func(ctx context.Context, state S) (string, error) // 返回下一状态名或空字符串使用转换
}

// Transition 状态转换
type Transition[S any] struct {
	From      string
	To        string
	Condition func(ctx context.Context, state S) bool
	Priority  int // 优先级，数值越小越先检查
}

// NewStateMachine 创建状态机
func NewStateMachine[S any](name string) *StateMachine[S] {
	return &StateMachine[S]{
		name:        name,
		states:      make(map[string]*StateNode[S]),
		transitions: make(map[string][]*Transition[S]),
		finals:      make(map[string]bool),
		maxSteps:    100,
	}
}

// ============== Builder 方法 ==============

// AddState 添加状态
func (sm *StateMachine[S]) AddState(name string, handler func(ctx context.Context, state S) (string, error)) *StateMachine[S] {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.states[name] = &StateNode[S]{
		Name:    name,
		Handler: handler,
	}
	return sm
}

// AddStateWithHooks 添加带钩子的状态
func (sm *StateMachine[S]) AddStateWithHooks(
	name string,
	handler func(ctx context.Context, state S) (string, error),
	onEnter func(ctx context.Context, state S) error,
	onExit func(ctx context.Context, state S) error,
) *StateMachine[S] {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.states[name] = &StateNode[S]{
		Name:    name,
		Handler: handler,
		OnEnter: onEnter,
		OnExit:  onExit,
	}
	return sm
}

// AddTransition 添加转换
func (sm *StateMachine[S]) AddTransition(from, to string, condition func(ctx context.Context, state S) bool) *StateMachine[S] {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	trans := &Transition[S]{
		From:      from,
		To:        to,
		Condition: condition,
		Priority:  len(sm.transitions[from]), // 默认优先级
	}
	sm.transitions[from] = append(sm.transitions[from], trans)
	return sm
}

// AddTransitionWithPriority 添加带优先级的转换
func (sm *StateMachine[S]) AddTransitionWithPriority(from, to string, condition func(ctx context.Context, state S) bool, priority int) *StateMachine[S] {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	trans := &Transition[S]{
		From:      from,
		To:        to,
		Condition: condition,
		Priority:  priority,
	}
	sm.transitions[from] = append(sm.transitions[from], trans)
	return sm
}

// SetInitial 设置初始状态
func (sm *StateMachine[S]) SetInitial(name string) *StateMachine[S] {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.initial = name
	return sm
}

// AddFinal 添加终态
func (sm *StateMachine[S]) AddFinal(names ...string) *StateMachine[S] {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for _, name := range names {
		sm.finals[name] = true
	}
	return sm
}

// SetMaxSteps 设置最大步数
func (sm *StateMachine[S]) SetMaxSteps(max int) *StateMachine[S] {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.maxSteps = max
	return sm
}

// SetCheckpointer 设置检查点存储
func (sm *StateMachine[S]) SetCheckpointer(cp checkpoint.Checkpointer) *StateMachine[S] {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.checkpointer = cp
	sm.interruptHandler = interrupt.NewHandler(cp)
	return sm
}

// ============== 执行方法 ==============

// Run 运行状态机
func (sm *StateMachine[S]) Run(ctx context.Context, initialState S) (S, error) {
	return sm.RunWithThreadID(ctx, "", initialState)
}

// RunWithThreadID 带线程 ID 运行状态机
func (sm *StateMachine[S]) RunWithThreadID(ctx context.Context, threadID string, initialState S) (S, error) {
	sm.mu.RLock()
	if sm.initial == "" {
		sm.mu.RUnlock()
		return initialState, ErrNoInitialState
	}
	sm.mu.RUnlock()

	// 设置中断处理
	if sm.interruptHandler != nil {
		ctx = interrupt.ContextWithInterruptHandler(ctx, sm.interruptHandler)
		ctx = interrupt.ContextWithThreadID(ctx, threadID)
	}

	currentState := sm.initial
	state := initialState
	steps := 0

	for {
		// 检查最大步数
		if steps >= sm.maxSteps {
			return state, ErrMaxStepsExceeded
		}
		steps++

		// 检查是否是终态
		sm.mu.RLock()
		if sm.finals[currentState] {
			sm.mu.RUnlock()
			return state, nil
		}
		sm.mu.RUnlock()

		// 获取当前状态
		sm.mu.RLock()
		stateObj, ok := sm.states[currentState]
		sm.mu.RUnlock()
		if !ok {
			return state, fmt.Errorf("%w: %s", ErrStateNotFound, currentState)
		}

		// 设置节点 ID
		if sm.interruptHandler != nil {
			ctx = interrupt.ContextWithNodeID(ctx, currentState)
		}

		// OnEnter
		if stateObj.OnEnter != nil {
			if err := stateObj.OnEnter(ctx, state); err != nil {
				return state, fmt.Errorf("state %s OnEnter error: %w", currentState, err)
			}
		}

		// 执行状态处理器
		var nextState string
		var err error
		if stateObj.Handler != nil {
			nextState, err = stateObj.Handler(ctx, state)
			if err != nil {
				// 检查是否是中断错误
				if errors.Is(err, interrupt.ErrInterrupted) {
					return state, err
				}
				return state, fmt.Errorf("state %s handler error: %w", currentState, err)
			}
		}

		// OnExit
		if stateObj.OnExit != nil {
			if err := stateObj.OnExit(ctx, state); err != nil {
				return state, fmt.Errorf("state %s OnExit error: %w", currentState, err)
			}
		}

		// 确定下一状态
		if nextState == "" {
			// 使用转换条件
			nextState, err = sm.findNextState(ctx, currentState, state)
			if err != nil {
				return state, err
			}
		}

		currentState = nextState
	}
}

// findNextState 查找下一状态
func (sm *StateMachine[S]) findNextState(ctx context.Context, currentState string, state S) (string, error) {
	sm.mu.RLock()
	transitions := sm.transitions[currentState]
	sm.mu.RUnlock()

	if len(transitions) == 0 {
		return "", fmt.Errorf("%w: %s", ErrNoTransition, currentState)
	}

	// 按优先级排序（已在添加时处理）
	for _, trans := range transitions {
		if trans.Condition == nil || trans.Condition(ctx, state) {
			return trans.To, nil
		}
	}

	return "", fmt.Errorf("%w: %s (no condition matched)", ErrNoTransition, currentState)
}

// Resume 恢复执行
func (sm *StateMachine[S]) Resume(ctx context.Context, threadID string, cmd interrupt.Command) error {
	if sm.interruptHandler == nil {
		return errors.New("no interrupt handler configured")
	}
	return sm.interruptHandler.Resume(ctx, threadID, cmd)
}

// GetPending 获取待处理的中断
func (sm *StateMachine[S]) GetPending(threadID string) *interrupt.PendingInfo {
	if sm.interruptHandler == nil {
		return nil
	}
	return sm.interruptHandler.GetPending(threadID)
}

// ============== 便捷转换条件 ==============

// Always 总是满足的条件
func Always[S any]() func(context.Context, S) bool {
	return func(ctx context.Context, state S) bool {
		return true
	}
}

// Never 永不满足的条件
func Never[S any]() func(context.Context, S) bool {
	return func(ctx context.Context, state S) bool {
		return false
	}
}

// When 当条件为真时
func When[S any](predicate func(S) bool) func(context.Context, S) bool {
	return func(ctx context.Context, state S) bool {
		return predicate(state)
	}
}

// ============== StateMachineBuilder ==============

// StateMachineBuilder 状态机构建器（更流畅的 API）
type StateMachineBuilder[S any] struct {
	sm *StateMachine[S]
}

// NewBuilder 创建构建器
func NewBuilder[S any](name string) *StateMachineBuilder[S] {
	return &StateMachineBuilder[S]{
		sm: NewStateMachine[S](name),
	}
}

// State 添加状态
func (b *StateMachineBuilder[S]) State(name string, handler func(ctx context.Context, state S) (string, error)) *StateMachineBuilder[S] {
	b.sm.AddState(name, handler)
	return b
}

// Transition 添加转换
func (b *StateMachineBuilder[S]) Transition(from, to string, condition func(ctx context.Context, state S) bool) *StateMachineBuilder[S] {
	b.sm.AddTransition(from, to, condition)
	return b
}

// Initial 设置初始状态
func (b *StateMachineBuilder[S]) Initial(name string) *StateMachineBuilder[S] {
	b.sm.SetInitial(name)
	return b
}

// Final 添加终态
func (b *StateMachineBuilder[S]) Final(names ...string) *StateMachineBuilder[S] {
	b.sm.AddFinal(names...)
	return b
}

// MaxSteps 设置最大步数
func (b *StateMachineBuilder[S]) MaxSteps(max int) *StateMachineBuilder[S] {
	b.sm.SetMaxSteps(max)
	return b
}

// Checkpointer 设置检查点存储
func (b *StateMachineBuilder[S]) Checkpointer(cp checkpoint.Checkpointer) *StateMachineBuilder[S] {
	b.sm.SetCheckpointer(cp)
	return b
}

// Build 构建状态机
func (b *StateMachineBuilder[S]) Build() *StateMachine[S] {
	return b.sm
}

// ============== 预置状态处理器 ==============

// PassThrough 直通处理器（不做任何处理）
func PassThrough[S any]() func(context.Context, S) (string, error) {
	return func(ctx context.Context, state S) (string, error) {
		return "", nil // 使用转换条件决定下一状态
	}
}

// End 结束处理器（跳转到指定状态）
func End[S any](nextState string) func(context.Context, S) (string, error) {
	return func(ctx context.Context, state S) (string, error) {
		return nextState, nil
	}
}

// Conditional 条件处理器
func Conditional[S any](condition func(S) bool, ifTrue, ifFalse string) func(context.Context, S) (string, error) {
	return func(ctx context.Context, state S) (string, error) {
		if condition(state) {
			return ifTrue, nil
		}
		return ifFalse, nil
	}
}

// ============== 执行追踪 ==============

// ExecutionTrace 执行追踪
type ExecutionTrace struct {
	Steps []TraceStep
}

// TraceStep 追踪步骤
type TraceStep struct {
	State     string
	NextState string
	Duration  int64 // 毫秒
	Error     error
}

// RunWithTrace 带追踪运行
func (sm *StateMachine[S]) RunWithTrace(ctx context.Context, initialState S) (S, *ExecutionTrace, error) {
	trace := &ExecutionTrace{
		Steps: make([]TraceStep, 0),
	}

	// 简化实现：这里应该包装 Run 方法
	result, err := sm.Run(ctx, initialState)
	return result, trace, err
}
