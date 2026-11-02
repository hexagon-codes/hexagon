// Package graph 提供 Hexagon AI Agent 框架的图编排引擎
//
// functional.go 实现 Functional API（函数式工作流定义）：
//   - Entrypoint: 标记工作流入口函数
//   - Task: 定义可被编排的异步任务
//   - Workflow: 通过函数注册自动构建图
//
// 对标 LangGraph 的 @entrypoint/@task 装饰器模式。
// Go 没有装饰器语法，采用注册器+泛型实现同等能力。
//
// 使用示例：
//
//	wf := NewWorkflow[MyState]("my-flow")
//
//	// 定义任务
//	fetchTask := DefineTask(wf, "fetch", fetchFunc)
//	processTask := DefineTask(wf, "process", processFunc)
//
//	// 定义入口点：编排任务执行顺序
//	DefineEntrypoint(wf, func(ctx context.Context, state MyState) (MyState, error) {
//	    state, err := fetchTask.Run(ctx, state)
//	    if err != nil { return state, err }
//	    return processTask.Run(ctx, state)
//	})
//
//	result, err := wf.Run(ctx, initialState)
package graph

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Workflow 函数式工作流
// 通过注册函数而非手动构建图来定义工作流
type Workflow[S State] struct {
	name       string
	entrypoint func(ctx context.Context, state S) (S, error)
	tasks      map[string]*TaskDef[S]
	mu         sync.RWMutex
}

// TaskDef 任务定义
type TaskDef[S State] struct {
	// Name 任务名称
	Name string

	// handler 任务处理函数
	handler func(ctx context.Context, state S) (S, error)

	// 执行统计
	execCount atomic.Int64

	// 缓存支持
	cache    NodeCache
	cacheKey func(state S) string
}

// TaskOption 任务选项
type TaskOption[S State] func(*TaskDef[S])

// WithTaskCache 为任务添加缓存
func WithTaskCache[S State](cache NodeCache) TaskOption[S] {
	return func(t *TaskDef[S]) {
		t.cache = cache
	}
}

// WithTaskCacheKey 自定义任务缓存 key 生成
func WithTaskCacheKey[S State](keyFunc func(state S) string) TaskOption[S] {
	return func(t *TaskDef[S]) {
		t.cacheKey = keyFunc
	}
}

// NewWorkflow 创建函数式工作流
func NewWorkflow[S State](name string) *Workflow[S] {
	return &Workflow[S]{
		name:  name,
		tasks: make(map[string]*TaskDef[S]),
	}
}

// DefineTask 定义一个任务
// 返回 TaskDef 可在 entrypoint 中调用 Run 执行
func DefineTask[S State](wf *Workflow[S], name string, handler func(ctx context.Context, state S) (S, error), opts ...TaskOption[S]) *TaskDef[S] {
	task := &TaskDef[S]{
		Name:    name,
		handler: handler,
	}
	for _, opt := range opts {
		opt(task)
	}

	wf.mu.Lock()
	wf.tasks[name] = task
	wf.mu.Unlock()

	return task
}

// Run 执行任务
// 在 entrypoint 函数中调用此方法来执行任务
func (t *TaskDef[S]) Run(ctx context.Context, state S) (S, error) {
	t.execCount.Add(1)
	return runIsolatedNode(ctx, t.Name, t.run, state)
}

// run 在公开入口的隔离边界内处理缓存和用户回调。
func (t *TaskDef[S]) run(ctx context.Context, state S) (S, error) {
	// 检查缓存
	if t.cache != nil {
		key := ComputeCacheKey(t.Name, state)
		if t.cacheKey != nil {
			key = t.cacheKey(state)
		}
		if cached, hit := t.cache.Get(key); hit {
			if cachedState, ok := cached.(S); ok {
				return cachedState, nil
			}
		}

		// 执行并缓存
		result, err := t.handler(ctx, state)
		if err != nil {
			return result, err
		}
		t.cache.Set(key, result)
		return result, nil
	}

	return t.handler(ctx, state)
}

// ExecCount 返回任务执行次数
func (t *TaskDef[S]) ExecCount() int64 {
	return t.execCount.Load()
}

// DefineEntrypoint 定义工作流入口点
// entrypoint 函数编排各任务的执行顺序和逻辑
func DefineEntrypoint[S State](wf *Workflow[S], handler func(ctx context.Context, state S) (S, error)) {
	wf.entrypoint = handler
}

// Run 执行工作流
func (wf *Workflow[S]) Run(ctx context.Context, state S) (S, error) {
	if wf.entrypoint == nil {
		return state, fmt.Errorf("工作流 %q 未定义入口点，请调用 DefineEntrypoint", wf.name)
	}
	return runIsolatedNode(ctx, wf.name+" entrypoint", wf.entrypoint, state)
}

// Name 返回工作流名称
func (wf *Workflow[S]) Name() string {
	return wf.name
}

// Tasks 返回所有已注册的任务
func (wf *Workflow[S]) Tasks() map[string]*TaskDef[S] {
	wf.mu.RLock()
	defer wf.mu.RUnlock()

	result := make(map[string]*TaskDef[S], len(wf.tasks))
	for k, v := range wf.tasks {
		result[k] = v
	}
	return result
}

// ============== 并行任务辅助 ==============

// RunParallel 并行执行多个任务
// 所有任务共享同一初始状态的克隆，结果通过 merger 合并
func RunParallel[S State](ctx context.Context, state S, merger func(original S, results []S) S, tasks ...*TaskDef[S]) (S, error) {
	if len(tasks) == 0 {
		return state, nil
	}

	type taskResult struct {
		index int
		state S
		err   error
	}

	resultCh := make(chan taskResult, len(tasks))
	for i, task := range tasks {
		go func(idx int, t *TaskDef[S]) {
			result, err := t.Run(ctx, state)
			resultCh <- taskResult{index: idx, state: result, err: err}
		}(i, task)
	}

	results := make([]S, len(tasks))
	for range tasks {
		r := <-resultCh
		if r.err != nil {
			return state, fmt.Errorf("并行任务 %q 失败: %w", tasks[r.index].Name, r.err)
		}
		results[r.index] = r.state
	}

	if merger == nil {
		return results[len(results)-1], nil
	}
	return runIsolatedNode(ctx, "parallel task merger", func(_ context.Context, candidate S) (S, error) {
		return merger(candidate, results), nil
	}, state)
}

// RunConditional 条件执行任务
// 根据 condition 返回值选择执行哪个任务
func RunConditional[S State](ctx context.Context, state S, condition func(S) string, routes map[string]*TaskDef[S]) (S, error) {
	return runIsolatedNode(ctx, "conditional task route", func(callCtx context.Context, candidate S) (S, error) {
		label := condition(candidate)
		task, ok := routes[label]
		if !ok {
			return candidate, fmt.Errorf("conditional task route not found: %q", label)
		}
		return task.Run(callCtx, candidate)
	}, state)
}

// ToGraph 将函数式工作流转换为图
// 每个注册的任务变为图中的一个节点，入口点作为编排逻辑
func (wf *Workflow[S]) ToGraph() (*Graph[S], error) {
	builder := NewGraph[S](wf.name)

	// 将入口点作为唯一执行节点
	builder.AddNode("__entrypoint__", func(ctx context.Context, state S) (S, error) {
		return wf.Run(ctx, state)
	})

	builder.AddEdge(START, "__entrypoint__")
	builder.AddEdge("__entrypoint__", END)

	return builder.Build()
}
