// Package graph 提供 Hexagon AI Agent 框架的图编排引擎
//
// 本文件实现循环图支持：
//   - WhileLoop: while 循环
//   - DoWhile: do-while 循环
//   - ForLoop: for 循环（有限次数）
//   - ForEach: 遍历集合
//   - Until: 条件退出循环
//   - LoopWithBreak: 带中断的循环
//
// 设计借鉴：
//   - LangGraph: 条件循环
//   - Mastra: 工作流循环
//   - BPMN: 循环网关
package graph

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync/atomic"
	"time"
)

// ============== 错误定义 ==============

var (
	// ErrLoopBreak 循环中断
	ErrLoopBreak = errors.New("loop break")

	// ErrLoopContinue 循环继续
	ErrLoopContinue = errors.New("loop continue")

	// ErrMaxIterationsReached 达到最大迭代次数
	ErrMaxIterationsReached = errors.New("max iterations reached")

	// ErrLoopTimeout 循环超时
	ErrLoopTimeout = errors.New("loop timeout")
)

// ============== 循环配置 ==============

// LoopConfig 循环配置
type LoopConfig struct {
	// MaxIterations 最大迭代次数（0 表示无限制）
	MaxIterations int

	// Timeout 循环超时时间（0 表示无超时）
	Timeout time.Duration

	// OnIteration 每次迭代回调
	OnIteration func(iteration int)

	// OnBreak 中断回调
	OnBreak func(iteration int, reason string)

	// OnComplete 完成回调
	OnComplete func(iterations int)

	// BreakOnError 遇到错误时中断
	BreakOnError bool

	// ContinueOnError 遇到错误时继续
	ContinueOnError bool
}

// DefaultLoopConfig 默认循环配置
func DefaultLoopConfig() *LoopConfig {
	return &LoopConfig{
		MaxIterations:   1000,
		Timeout:         0,
		BreakOnError:    true,
		ContinueOnError: false,
	}
}

// ============== While 循环 ==============

// WhileLoopNode 创建 while 循环节点
//
// 循环执行 body 直到 condition 返回 false
//
// 示例:
//
//	loop := WhileLoopNode[MyState](
//	    "retry-loop",
//	    func(s MyState) bool { return s.RetryCount < 3 }, // 条件
//	    func(ctx context.Context, s MyState) (MyState, error) { // 循环体
//	        s.RetryCount++
//	        return s, nil
//	    },
//	)
func WhileLoopNode[S State](name string, condition func(S) bool, body NodeHandler[S], config ...*LoopConfig) *Node[S] {
	cfg := DefaultLoopConfig()
	if len(config) > 0 && config[0] != nil {
		cfg = config[0]
	}

	handler := func(ctx context.Context, state S) (S, error) {
		iteration := 0
		startTime := time.Now()

		for condition(state) {
			// 检查迭代次数
			if cfg.MaxIterations > 0 && iteration >= cfg.MaxIterations {
				if cfg.OnBreak != nil {
					cfg.OnBreak(iteration, "max iterations reached")
				}
				return state, ErrMaxIterationsReached
			}

			// 检查超时
			if cfg.Timeout > 0 && time.Since(startTime) > cfg.Timeout {
				if cfg.OnBreak != nil {
					cfg.OnBreak(iteration, "timeout")
				}
				return state, ErrLoopTimeout
			}

			// 检查 context
			select {
			case <-ctx.Done():
				return state, ctx.Err()
			default:
			}

			// 迭代回调
			if cfg.OnIteration != nil {
				cfg.OnIteration(iteration)
			}

			// 执行循环体
			newState, err := body(ctx, state)
			if err != nil {
				if errors.Is(err, ErrLoopBreak) {
					if cfg.OnBreak != nil {
						cfg.OnBreak(iteration, "break")
					}
					return newState, nil
				}
				if errors.Is(err, ErrLoopContinue) {
					iteration++
					continue
				}
				if cfg.BreakOnError {
					return state, err
				}
				if cfg.ContinueOnError {
					iteration++
					continue
				}
				return state, err
			}

			state = newState
			iteration++
		}

		if cfg.OnComplete != nil {
			cfg.OnComplete(iteration)
		}

		return state, nil
	}

	return &Node[S]{
		Name:     name,
		Type:     NodeTypeLoop,
		Handler:  handler,
		Metadata: map[string]any{"loop_type": "while"},
	}
}

// ============== Do-While 循环 ==============

// DoWhileLoopNode 创建 do-while 循环节点
//
// 先执行 body，再检查 condition，至少执行一次
func DoWhileLoopNode[S State](name string, condition func(S) bool, body NodeHandler[S], config ...*LoopConfig) *Node[S] {
	cfg := DefaultLoopConfig()
	if len(config) > 0 && config[0] != nil {
		cfg = config[0]
	}

	handler := func(ctx context.Context, state S) (S, error) {
		iteration := 0
		startTime := time.Now()

		for {
			// 检查迭代次数
			if cfg.MaxIterations > 0 && iteration >= cfg.MaxIterations {
				if cfg.OnBreak != nil {
					cfg.OnBreak(iteration, "max iterations reached")
				}
				return state, ErrMaxIterationsReached
			}

			// 检查超时
			if cfg.Timeout > 0 && time.Since(startTime) > cfg.Timeout {
				if cfg.OnBreak != nil {
					cfg.OnBreak(iteration, "timeout")
				}
				return state, ErrLoopTimeout
			}

			// 检查 context
			select {
			case <-ctx.Done():
				return state, ctx.Err()
			default:
			}

			// 迭代回调
			if cfg.OnIteration != nil {
				cfg.OnIteration(iteration)
			}

			// 执行循环体
			newState, err := body(ctx, state)
			if err != nil {
				if errors.Is(err, ErrLoopBreak) {
					if cfg.OnBreak != nil {
						cfg.OnBreak(iteration, "break")
					}
					return newState, nil
				}
				if errors.Is(err, ErrLoopContinue) {
					iteration++
					if !condition(state) {
						break
					}
					continue
				}
				if cfg.BreakOnError {
					return state, err
				}
				if cfg.ContinueOnError {
					iteration++
					if !condition(state) {
						break
					}
					continue
				}
				return state, err
			}

			state = newState
			iteration++

			// 检查条件
			if !condition(state) {
				break
			}
		}

		if cfg.OnComplete != nil {
			cfg.OnComplete(iteration)
		}

		return state, nil
	}

	return &Node[S]{
		Name:     name,
		Type:     NodeTypeLoop,
		Handler:  handler,
		Metadata: map[string]any{"loop_type": "do_while"},
	}
}

// ============== For 循环 ==============

// ForLoopNode 创建 for 循环节点
//
// 执行固定次数的循环
func ForLoopNode[S State](name string, iterations int, body func(ctx context.Context, state S, index int) (S, error), config ...*LoopConfig) *Node[S] {
	cfg := DefaultLoopConfig()
	if len(config) > 0 && config[0] != nil {
		cfg = config[0]
	}

	handler := func(ctx context.Context, state S) (S, error) {
		startTime := time.Now()

		for i := 0; i < iterations; i++ {
			// 检查超时
			if cfg.Timeout > 0 && time.Since(startTime) > cfg.Timeout {
				if cfg.OnBreak != nil {
					cfg.OnBreak(i, "timeout")
				}
				return state, ErrLoopTimeout
			}

			// 检查 context
			select {
			case <-ctx.Done():
				return state, ctx.Err()
			default:
			}

			// 迭代回调
			if cfg.OnIteration != nil {
				cfg.OnIteration(i)
			}

			// 执行循环体
			newState, err := body(ctx, state, i)
			if err != nil {
				if errors.Is(err, ErrLoopBreak) {
					if cfg.OnBreak != nil {
						cfg.OnBreak(i, "break")
					}
					return newState, nil
				}
				if errors.Is(err, ErrLoopContinue) {
					continue
				}
				if cfg.BreakOnError {
					return state, err
				}
				if cfg.ContinueOnError {
					continue
				}
				return state, err
			}

			state = newState
		}

		if cfg.OnComplete != nil {
			cfg.OnComplete(iterations)
		}

		return state, nil
	}

	return &Node[S]{
		Name:     name,
		Type:     NodeTypeLoop,
		Handler:  handler,
		Metadata: map[string]any{"loop_type": "for", "iterations": iterations},
	}
}

// ============== ForEach 循环 ==============

// ForEachLoopNode 创建 forEach 循环节点
//
// 遍历集合执行循环体
func ForEachLoopNode[S State, T any](
	name string,
	getItems func(S) []T,
	body func(ctx context.Context, state S, item T, index int) (S, error),
	config ...*LoopConfig,
) *Node[S] {
	cfg := DefaultLoopConfig()
	if len(config) > 0 && config[0] != nil {
		cfg = config[0]
	}

	handler := func(ctx context.Context, state S) (S, error) {
		items := getItems(state)
		startTime := time.Now()

		for i, item := range items {
			// 检查迭代次数
			if cfg.MaxIterations > 0 && i >= cfg.MaxIterations {
				if cfg.OnBreak != nil {
					cfg.OnBreak(i, "max iterations reached")
				}
				return state, ErrMaxIterationsReached
			}

			// 检查超时
			if cfg.Timeout > 0 && time.Since(startTime) > cfg.Timeout {
				if cfg.OnBreak != nil {
					cfg.OnBreak(i, "timeout")
				}
				return state, ErrLoopTimeout
			}

			// 检查 context
			select {
			case <-ctx.Done():
				return state, ctx.Err()
			default:
			}

			// 迭代回调
			if cfg.OnIteration != nil {
				cfg.OnIteration(i)
			}

			// 执行循环体
			newState, err := body(ctx, state, item, i)
			if err != nil {
				if errors.Is(err, ErrLoopBreak) {
					if cfg.OnBreak != nil {
						cfg.OnBreak(i, "break")
					}
					return newState, nil
				}
				if errors.Is(err, ErrLoopContinue) {
					continue
				}
				if cfg.BreakOnError {
					return state, err
				}
				if cfg.ContinueOnError {
					continue
				}
				return state, err
			}

			state = newState
		}

		if cfg.OnComplete != nil {
			cfg.OnComplete(len(items))
		}

		return state, nil
	}

	return &Node[S]{
		Name:     name,
		Type:     NodeTypeLoop,
		Handler:  handler,
		Metadata: map[string]any{"loop_type": "foreach"},
	}
}

// ============== Until 循环 ==============

// UntilLoopNode 创建 until 循环节点
//
// 循环执行直到条件满足
func UntilLoopNode[S State](name string, condition func(S) bool, body NodeHandler[S], config ...*LoopConfig) *Node[S] {
	// until 就是 while not condition
	return WhileLoopNode(name, func(s S) bool { return !condition(s) }, body, config...)
}

// ============== 循环控制 ==============

// Break 返回中断错误
func Break() error {
	return ErrLoopBreak
}

// Continue 返回继续错误
func Continue() error {
	return ErrLoopContinue
}

// BreakIf 条件中断
func BreakIf[S State](condition func(S) bool) NodeHandler[S] {
	return func(ctx context.Context, state S) (S, error) {
		if condition(state) {
			return state, ErrLoopBreak
		}
		return state, nil
	}
}

// ContinueIf 条件继续
func ContinueIf[S State](condition func(S) bool) NodeHandler[S] {
	return func(ctx context.Context, state S) (S, error) {
		if condition(state) {
			return state, ErrLoopContinue
		}
		return state, nil
	}
}

// ============== 循环计数器 ==============

// LoopCounter 循环计数器
type LoopCounter struct {
	count int64
}

// NewLoopCounter 创建循环计数器
func NewLoopCounter() *LoopCounter {
	return &LoopCounter{}
}

// Increment 增加计数
func (c *LoopCounter) Increment() int64 {
	return atomic.AddInt64(&c.count, 1)
}

// Get 获取当前计数
func (c *LoopCounter) Get() int64 {
	return atomic.LoadInt64(&c.count)
}

// Reset 重置计数
func (c *LoopCounter) Reset() {
	atomic.StoreInt64(&c.count, 0)
}

// ============== 循环节点构建器 ==============

// AddWhileLoop 添加 while 循环
func (b *GraphBuilder[S]) AddWhileLoop(name string, condition func(S) bool, body NodeHandler[S], config ...*LoopConfig) *GraphBuilder[S] {
	return b.AddNodeWithBuilder(WhileLoopNode(name, condition, body, config...))
}

// AddDoWhileLoop 添加 do-while 循环
func (b *GraphBuilder[S]) AddDoWhileLoop(name string, condition func(S) bool, body NodeHandler[S], config ...*LoopConfig) *GraphBuilder[S] {
	return b.AddNodeWithBuilder(DoWhileLoopNode(name, condition, body, config...))
}

// AddForLoop 添加 for 循环
func (b *GraphBuilder[S]) AddForLoop(name string, iterations int, body func(ctx context.Context, state S, index int) (S, error), config ...*LoopConfig) *GraphBuilder[S] {
	return b.AddNodeWithBuilder(ForLoopNode(name, iterations, body, config...))
}

// AddUntilLoop 添加 until 循环
func (b *GraphBuilder[S]) AddUntilLoop(name string, condition func(S) bool, body NodeHandler[S], config ...*LoopConfig) *GraphBuilder[S] {
	return b.AddNodeWithBuilder(UntilLoopNode(name, condition, body, config...))
}

// ============== 循环边（支持循环回边）==============

// AddLoopBackEdge 添加循环回边
//
// 创建从 from 到 to 的循环边，支持条件判断
//
// 并发安全说明：使用原子操作 IncrementAndGet 来避免 Get 和 Increment 之间的 TOCTOU 竞态条件
func (b *GraphBuilder[S]) AddLoopBackEdge(from, to string, condition func(S) bool, maxIterations int) *GraphBuilder[S] {
	if b.err != nil {
		return b
	}

	// 创建计数器
	counter := NewLoopCounter()

	// 添加条件边
	return b.AddConditionalEdge(from, func(s S) string {
		// 检查条件
		if condition(s) {
			// 原子地增加并获取新值，避免 TOCTOU 竞态条件
			newCount := counter.Increment()

			// 检查最大迭代次数（使用增加后的值）
			if maxIterations > 0 && int(newCount) > maxIterations {
				counter.Reset()
				return "exit"
			}
			return "loop"
		}

		counter.Reset()
		return "exit"
	}, map[string]string{
		"loop": to,
		"exit": END,
	})
}

// ============== 并行循环 ==============

// ParallelForEachLoopNode 并行 forEach 循环
//
// 并行处理集合中的元素
func ParallelForEachLoopNode[S State, T any](
	name string,
	getItems func(S) []T,
	body func(ctx context.Context, item T, index int) error,
	merger func(state S, results []error) (S, error),
	maxConcurrency int,
) *Node[S] {
	if maxConcurrency <= 0 {
		maxConcurrency = 10
	}

	handler := func(ctx context.Context, state S) (S, error) {
		items := getItems(state)
		if len(items) == 0 {
			return state, nil
		}

		// 创建工作队列
		type work struct {
			index int
			item  T
		}

		workCh := make(chan work, len(items))
		for i, item := range items {
			workCh <- work{index: i, item: item}
		}
		close(workCh)

		// 收集结果
		results := make([]error, len(items))
		resultCh := make(chan struct {
			index int
			err   error
		}, len(items))

		// 启动 worker
		workerCount := maxConcurrency
		if len(items) < workerCount {
			workerCount = len(items)
		}

		for i := 0; i < workerCount; i++ {
			go func() {
				for w := range workCh {
					err := func() (err error) {
						defer func() {
							if value := recover(); value != nil {
								err = &NodePanicError{Node: fmt.Sprintf("%s[%d]", name, w.index), Value: value, Stack: debug.Stack()}
							}
						}()
						if err := ctx.Err(); err != nil {
							return err
						}
						err = body(ctx, w.item, w.index)
						return errors.Join(err, ctx.Err())
					}()
					resultCh <- struct {
						index int
						err   error
					}{w.index, err}
				}
			}()
		}

		// 取消只阻止后续派发；收敛在途回调，不能把已成功项改写为取消。
		outcomes := make([]ParallelResult[S], len(items))
		controlFailure := false
		for range items {
			result := <-resultCh
			results[result.index] = result.err
			outcomes[result.index] = ParallelResult[S]{Name: fmt.Sprint(result.index), State: state, Error: result.err}
			controlFailure = controlFailure || nodeControlOrUnknown(result.err)
		}

		// 普通分项错误仍由既有 merger 决定；panic、未知结果和取消不得被吞为成功。
		merged, err := merger(state, results)
		if err != nil || controlFailure {
			return merged, &ParallelExecutionError[S]{Name: name, Results: outcomes, Cause: err}
		}
		return merged, nil
	}

	return &Node[S]{
		Name:     name,
		Type:     NodeTypeLoop,
		Handler:  handler,
		Metadata: map[string]any{"loop_type": "parallel_foreach", "max_concurrency": maxConcurrency},
	}
}

// ============== 重试循环 ==============

// RetryConfig 重试配置
type RetryConfig struct {
	// MaxRetries 最大重试次数
	MaxRetries int

	// Delay 基础延迟
	Delay time.Duration

	// MaxDelay 最大延迟
	MaxDelay time.Duration

	// Backoff 退避因子
	Backoff float64

	// Jitter 抖动比例 (0-1)
	Jitter float64

	// ShouldRetry 判断是否应该重试
	ShouldRetry func(err error) bool

	// OnRetry 重试回调
	OnRetry func(attempt int, err error, delay time.Duration)
}

// DefaultRetryConfig 默认重试配置
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxRetries:  3,
		Delay:       time.Second,
		MaxDelay:    30 * time.Second,
		Backoff:     2.0,
		Jitter:      0.1,
		ShouldRetry: func(err error) bool { return err != nil },
	}
}

// RetryLoopNode 创建重试循环节点
func RetryLoopNode[S State](name string, body NodeHandler[S], config *RetryConfig) *Node[S] {
	if config == nil {
		config = DefaultRetryConfig()
	}

	handler := func(ctx context.Context, state S) (S, error) {
		var lastErr error
		delay := config.Delay

		for attempt := 0; attempt <= config.MaxRetries; attempt++ {
			// 执行
			newState, err := body(ctx, state)
			if err == nil {
				return newState, nil
			}

			lastErr = err

			// 检查是否应该重试
			if config.ShouldRetry != nil && !config.ShouldRetry(err) {
				return state, err
			}

			// 最后一次不等待
			if attempt == config.MaxRetries {
				break
			}

			// 回调
			if config.OnRetry != nil {
				config.OnRetry(attempt, err, delay)
			}

			// 等待
			select {
			case <-ctx.Done():
				return state, ctx.Err()
			case <-time.After(delay):
			}

			// 计算下次延迟
			delay = time.Duration(float64(delay) * config.Backoff)
			if delay > config.MaxDelay {
				delay = config.MaxDelay
			}
		}

		return state, fmt.Errorf("max retries exceeded: %w", lastErr)
	}

	return &Node[S]{
		Name:     name,
		Type:     NodeTypeLoop,
		Handler:  handler,
		Metadata: map[string]any{"loop_type": "retry", "max_retries": config.MaxRetries},
	}
}

// AddRetryLoop 添加重试循环
func (b *GraphBuilder[S]) AddRetryLoop(name string, body NodeHandler[S], config *RetryConfig) *GraphBuilder[S] {
	return b.AddNodeWithBuilder(RetryLoopNode(name, body, config))
}
