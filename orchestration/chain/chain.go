// Package chain 提供 Hexagon AI Agent 框架的链式编排
//
// Chain 是一种简化的编排模式，将多个组件按顺序串联执行。
// 相比 Graph，Chain 更加简单直观，适合简单的线性流程。
//
// 基本用法：
//
//	chain := NewChain[Input, Output]("my-chain").
//	    Pipe(step1).
//	    Pipe(step2).
//	    Pipe(step3).
//	    Build()
//
//	result, err := chain.Invoke(ctx, input)
package chain

import (
	"context"
	"fmt"

	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/hexagon/core"
)

// Chain 链式组件
type Chain[I, O any] struct {
	name        string
	description string
	steps       []step
	middleware  []Middleware
}

// step 链中的步骤
type step struct {
	name    string
	handler func(ctx context.Context, input any) (any, error)
}

// Middleware 中间件
type Middleware func(next StepFunc) StepFunc

// StepFunc 步骤函数
type StepFunc func(ctx context.Context, input any) (any, error)

// ChainBuilder 链构建器
type ChainBuilder[I, O any] struct {
	chain *Chain[I, O]
	err   error
}

// NewChain 创建链构建器
func NewChain[I, O any](name string) *ChainBuilder[I, O] {
	return &ChainBuilder[I, O]{
		chain: &Chain[I, O]{
			name:  name,
			steps: make([]step, 0),
		},
	}
}

// WithDescription 设置描述
func (b *ChainBuilder[I, O]) WithDescription(desc string) *ChainBuilder[I, O] {
	if b.err != nil {
		return b
	}
	b.chain.description = desc
	return b
}

// Pipe 添加 Runnable 到链中
func (b *ChainBuilder[I, O]) Pipe(r core.Runnable[any, any]) *ChainBuilder[I, O] {
	if b.err != nil {
		return b
	}

	b.chain.steps = append(b.chain.steps, step{
		name: r.Name(),
		handler: func(ctx context.Context, input any) (any, error) {
			return r.Invoke(ctx, input)
		},
	})
	return b
}

// PipeFunc 添加函数到链中
func (b *ChainBuilder[I, O]) PipeFunc(name string, fn func(ctx context.Context, input any) (any, error)) *ChainBuilder[I, O] {
	if b.err != nil {
		return b
	}

	b.chain.steps = append(b.chain.steps, step{
		name:    name,
		handler: fn,
	})
	return b
}

// Use 添加中间件
func (b *ChainBuilder[I, O]) Use(middleware ...Middleware) *ChainBuilder[I, O] {
	if b.err != nil {
		return b
	}
	b.chain.middleware = append(b.chain.middleware, middleware...)
	return b
}

// Build 构建链
func (b *ChainBuilder[I, O]) Build() (*Chain[I, O], error) {
	if b.err != nil {
		return nil, b.err
	}
	if len(b.chain.steps) == 0 {
		return nil, fmt.Errorf("chain must have at least one step")
	}
	return b.chain, nil
}

// MustBuild 构建链，失败时 panic
//
// ⚠️ 警告：构建失败时会 panic。
// 仅在初始化时使用，不要在运行时调用。
// 推荐使用 Build() 方法并正确处理错误。
//
// 使用场景：
//   - 程序启动时的全局初始化
//   - 测试代码中
func (b *ChainBuilder[I, O]) MustBuild() *Chain[I, O] {
	c, err := b.Build()
	if err != nil {
		panic(fmt.Sprintf("chain build failed: %v", err))
	}
	return c
}

// Name 返回链名称
func (c *Chain[I, O]) Name() string {
	return c.name
}

// Description 返回链描述
func (c *Chain[I, O]) Description() string {
	return c.description
}

// InputSchema 返回输入 Schema
func (c *Chain[I, O]) InputSchema() *core.Schema {
	return core.SchemaOf[I]()
}

// OutputSchema 返回输出 Schema
func (c *Chain[I, O]) OutputSchema() *core.Schema {
	return core.SchemaOf[O]()
}

// Invoke 执行链
func (c *Chain[I, O]) Invoke(ctx context.Context, input I, opts ...core.Option) (O, error) {
	var zero O
	var current any = input

	for i, step := range c.steps {
		// 包装处理函数以应用中间件
		handler := step.handler
		for j := len(c.middleware) - 1; j >= 0; j-- {
			handler = c.middleware[j](handler)
		}

		result, err := handler(ctx, current)
		if err != nil {
			return zero, fmt.Errorf("step %d (%s) failed: %w", i, step.name, err)
		}
		current = result
	}

	// 尝试转换最终结果
	output, ok := current.(O)
	if !ok {
		return zero, fmt.Errorf("final output type mismatch: expected %T, got %T", zero, current)
	}

	return output, nil
}

// Stream 流式执行链
func (c *Chain[I, O]) Stream(ctx context.Context, input I, opts ...core.Option) (*stream.StreamReader[O], error) {
	output, err := c.Invoke(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(output), nil
}

// Batch 批量执行链
func (c *Chain[I, O]) Batch(ctx context.Context, inputs []I, opts ...core.Option) ([]O, error) {
	results := make([]O, len(inputs))
	for i, input := range inputs {
		output, err := c.Invoke(ctx, input, opts...)
		if err != nil {
			return nil, fmt.Errorf("batch item %d failed: %w", i, err)
		}
		results[i] = output
	}
	return results, nil
}

// Collect 收集流式输入并执行
func (c *Chain[I, O]) Collect(ctx context.Context, input *stream.StreamReader[I], opts ...core.Option) (O, error) {
	var zero O
	// 收集所有输入
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		return zero, err
	}
	return c.Invoke(ctx, collected, opts...)
}

// Transform 转换流
func (c *Chain[I, O]) Transform(ctx context.Context, input *stream.StreamReader[I], opts ...core.Option) (*stream.StreamReader[O], error) {
	reader, writer := stream.Pipe[O](10)
	go func() {
		defer writer.Close()
		for {
			in, err := input.Recv()
			if err != nil {
				return
			}
			result, err := c.Invoke(ctx, in, opts...)
			if err != nil {
				writer.CloseWithError(err)
				return
			}
			if err := writer.Send(result); err != nil {
				return
			}
		}
	}()
	return reader, nil
}

// BatchStream 批量流式执行
func (c *Chain[I, O]) BatchStream(ctx context.Context, inputs []I, opts ...core.Option) (*stream.StreamReader[O], error) {
	results, err := c.Batch(ctx, inputs, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}

// 确保实现了 Runnable 接口
var _ core.Runnable[any, any] = (*Chain[any, any])(nil)

// ============== 常用中间件 ==============

// LoggingMiddleware 日志中间件
func LoggingMiddleware(logger func(name string, input, output any, err error)) Middleware {
	return func(next StepFunc) StepFunc {
		return func(ctx context.Context, input any) (any, error) {
			output, err := next(ctx, input)
			logger("step", input, output, err)
			return output, err
		}
	}
}

// RetryMiddleware 重试中间件
func RetryMiddleware(maxRetries int, shouldRetry func(error) bool) Middleware {
	return func(next StepFunc) StepFunc {
		return func(ctx context.Context, input any) (any, error) {
			var lastErr error
			for i := 0; i <= maxRetries; i++ {
				output, err := next(ctx, input)
				if err == nil {
					return output, nil
				}
				lastErr = err
				if !shouldRetry(err) {
					return nil, err
				}
			}
			return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
		}
	}
}

// RecoverMiddleware panic 恢复中间件
func RecoverMiddleware() Middleware {
	return func(next StepFunc) StepFunc {
		return func(ctx context.Context, input any) (output any, err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic recovered: %v", r)
				}
			}()
			return next(ctx, input)
		}
	}
}

// ============== 类型安全的链式组件 ==============

// TypedStep 类型安全的步骤
type TypedStep[I, O any] struct {
	name    string
	handler func(ctx context.Context, input I) (O, error)
}

// NewTypedStep 创建类型安全的步骤
func NewTypedStep[I, O any](name string, handler func(ctx context.Context, input I) (O, error)) *TypedStep[I, O] {
	return &TypedStep[I, O]{
		name:    name,
		handler: handler,
	}
}

// ToStep 转换为通用步骤
func (s *TypedStep[I, O]) ToStep() step {
	return step{
		name: s.name,
		handler: func(ctx context.Context, input any) (any, error) {
			typedInput, ok := input.(I)
			if !ok {
				var zero I
				return nil, fmt.Errorf("input type mismatch: expected %T, got %T", zero, input)
			}
			return s.handler(ctx, typedInput)
		},
	}
}

// Then 连接另一个类型安全的步骤
func Then[I, M, O any](first *TypedStep[I, M], second *TypedStep[M, O]) *TypedStep[I, O] {
	return &TypedStep[I, O]{
		name: first.name + " -> " + second.name,
		handler: func(ctx context.Context, input I) (O, error) {
			var zero O
			mid, err := first.handler(ctx, input)
			if err != nil {
				return zero, err
			}
			return second.handler(ctx, mid)
		},
	}
}

// ============== 并行执行 ==============

// Parallel 并行执行多个组件
type Parallel[I, O any] struct {
	name     string
	handlers []func(ctx context.Context, input I) (O, error)
	merge    func(results []O) O
}

// NewParallel 创建并行执行器
func NewParallel[I, O any](name string, merge func(results []O) O) *Parallel[I, O] {
	return &Parallel[I, O]{
		name:  name,
		merge: merge,
	}
}

// Add 添加并行执行的处理函数
func (p *Parallel[I, O]) Add(handler func(ctx context.Context, input I) (O, error)) *Parallel[I, O] {
	p.handlers = append(p.handlers, handler)
	return p
}

// Invoke 执行并行处理
func (p *Parallel[I, O]) Invoke(ctx context.Context, input I, opts ...core.Option) (O, error) {
	var zero O
	if len(p.handlers) == 0 {
		return zero, fmt.Errorf("no handlers in parallel")
	}

	type result struct {
		output O
		err    error
	}

	results := make(chan result, len(p.handlers))

	for _, h := range p.handlers {
		h := h
		go func() {
			output, err := h(ctx, input)
			results <- result{output: output, err: err}
		}()
	}

	outputs := make([]O, 0, len(p.handlers))
	for range p.handlers {
		r := <-results
		if r.err != nil {
			return zero, r.err
		}
		outputs = append(outputs, r.output)
	}

	return p.merge(outputs), nil
}
