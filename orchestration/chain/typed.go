// typed.go 提供编译时类型安全的管道组合
//
// Go 泛型限制无法支持可变长度类型参数，因此提供 Pipe2/Pipe3/Pipe4 三个固定长度版本。
// 每个 Pipe 函数将多个类型安全的函数串联，编译时即可检查中间类型匹配。
//
// 使用示例：
//
//	// 类型安全的三步管道：string → int → bool
//	step := Pipe3[string, int, bool, string]("pipeline",
//	    func(ctx context.Context, s string) (int, error) { return len(s), nil },
//	    func(ctx context.Context, n int) (bool, error) { return n > 5, nil },
//	    func(ctx context.Context, b bool) (string, error) { return fmt.Sprintf("%v", b), nil },
//	)
//
//	chain := NewTypedChain("my-chain", step)
//	result, err := chain.Invoke(ctx, "hello world")
package chain

import (
	"context"
	"fmt"

	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/hexagon/core"
)

// Pipe2 连接两个类型安全的函数，编译时检查类型匹配
//
// 类型参数：
//   - I: 管道输入类型
//   - M: 中间类型（fn1 输出 = fn2 输入）
//   - O: 管道输出类型
//
// 返回的 TypedStep 可直接用于 NewTypedChain 或继续通过 Then 组合
func Pipe2[I, M, O any](
	name string,
	fn1 func(ctx context.Context, input I) (M, error),
	fn2 func(ctx context.Context, input M) (O, error),
) *TypedStep[I, O] {
	return Then(
		NewTypedStep(name+".1", fn1),
		NewTypedStep(name+".2", fn2),
	)
}

// Pipe3 连接三个类型安全的函数
//
// 类型参数：
//   - I: 管道输入类型
//   - M1: 第一个中间类型（fn1 输出 = fn2 输入）
//   - M2: 第二个中间类型（fn2 输出 = fn3 输入）
//   - O: 管道输出类型
func Pipe3[I, M1, M2, O any](
	name string,
	fn1 func(ctx context.Context, input I) (M1, error),
	fn2 func(ctx context.Context, input M1) (M2, error),
	fn3 func(ctx context.Context, input M2) (O, error),
) *TypedStep[I, O] {
	return Then(
		Pipe2[I, M1, M2](name+".a", fn1, fn2),
		NewTypedStep(name+".3", fn3),
	)
}

// Pipe4 连接四个类型安全的函数
//
// 类型参数：
//   - I: 管道输入类型
//   - M1: 第一个中间类型
//   - M2: 第二个中间类型
//   - M3: 第三个中间类型
//   - O: 管道输出类型
func Pipe4[I, M1, M2, M3, O any](
	name string,
	fn1 func(ctx context.Context, input I) (M1, error),
	fn2 func(ctx context.Context, input M1) (M2, error),
	fn3 func(ctx context.Context, input M2) (M3, error),
	fn4 func(ctx context.Context, input M3) (O, error),
) *TypedStep[I, O] {
	return Then(
		Pipe3[I, M1, M2, M3](name+".a", fn1, fn2, fn3),
		NewTypedStep(name+".4", fn4),
	)
}

// TypedChain 类型安全的链
// 将 TypedStep 包装为完整的 Runnable[I, O] 实现
//
// 与普通 Chain 的区别：
//   - Chain 内部步骤使用 any，运行时才发现类型错误
//   - TypedChain 所有步骤的类型在编译时即被检查
//
// 线程安全：TypedChain 是不可变的，可安全并发使用
type TypedChain[I, O any] struct {
	name        string
	description string
	step        *TypedStep[I, O]
}

// NewTypedChain 创建类型安全的链
//
// 参数：
//   - name: 链名称
//   - step: 通过 Pipe2/Pipe3/Pipe4/Then 组合的类型安全步骤
func NewTypedChain[I, O any](name string, step *TypedStep[I, O]) *TypedChain[I, O] {
	return &TypedChain[I, O]{
		name: name,
		step: step,
	}
}

// WithDescription 设置链描述
func (c *TypedChain[I, O]) WithDescription(desc string) *TypedChain[I, O] {
	c.description = desc
	return c
}

// Name 返回链名称
func (c *TypedChain[I, O]) Name() string {
	return c.name
}

// Description 返回链描述
func (c *TypedChain[I, O]) Description() string {
	return c.description
}

// InputSchema 返回输入 Schema
func (c *TypedChain[I, O]) InputSchema() *core.Schema {
	return core.SchemaOf[I]()
}

// OutputSchema 返回输出 Schema
func (c *TypedChain[I, O]) OutputSchema() *core.Schema {
	return core.SchemaOf[O]()
}

// Invoke 同步执行链
func (c *TypedChain[I, O]) Invoke(ctx context.Context, input I, opts ...core.Option) (O, error) {
	if c.step == nil {
		var zero O
		return zero, fmt.Errorf("TypedChain %q 没有步骤", c.name)
	}
	return c.step.handler(ctx, input)
}

// Stream 流式执行链
// 将 Invoke 结果包装为单元素流
func (c *TypedChain[I, O]) Stream(ctx context.Context, input I, opts ...core.Option) (*stream.StreamReader[O], error) {
	result, err := c.Invoke(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(result), nil
}

// Batch 批量执行链
func (c *TypedChain[I, O]) Batch(ctx context.Context, inputs []I, opts ...core.Option) ([]O, error) {
	results := make([]O, len(inputs))
	for i, input := range inputs {
		output, err := c.Invoke(ctx, input, opts...)
		if err != nil {
			return nil, fmt.Errorf("TypedChain %q 批量执行第 %d 项失败: %w", c.name, i, err)
		}
		results[i] = output
	}
	return results, nil
}

// Collect 收集流式输入并执行
func (c *TypedChain[I, O]) Collect(ctx context.Context, input *stream.StreamReader[I], opts ...core.Option) (O, error) {
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		var zero O
		return zero, err
	}
	return c.Invoke(ctx, collected, opts...)
}

// Transform 流转换：对每个输入元素应用链
func (c *TypedChain[I, O]) Transform(ctx context.Context, input *stream.StreamReader[I], opts ...core.Option) (*stream.StreamReader[O], error) {
	reader, writer := stream.Pipe[O](10)
	go func() {
		defer writer.Close()
		for {
			select {
			case <-ctx.Done():
				writer.CloseWithError(ctx.Err())
				return
			default:
			}

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
func (c *TypedChain[I, O]) BatchStream(ctx context.Context, inputs []I, opts ...core.Option) (*stream.StreamReader[O], error) {
	results, err := c.Batch(ctx, inputs, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}

// 确保 TypedChain 实现了 Runnable 接口
var _ core.Runnable[any, any] = (*TypedChain[any, any])(nil)
