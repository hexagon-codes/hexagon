// compose.go 提供 Runnable 组合适配器和自动流转换
//
// 核心功能：
//   - InvokeToStream: 将 Invoke-only 的 Runnable 适配为支持流输出
//   - StreamToInvoke: 将 Stream-only 的 Runnable 适配为支持同步返回
//   - Compose: 组合两个 Runnable，自动处理类型和流转换
//   - ComposeStream: 组合两个 Runnable，输出始终为流
//
// 在编排层自动应用 Concat（流→值）与 Box（值→流）。
//
// 使用示例：
//
//	// 组合两个 Runnable，类型自动匹配
//	combined := Compose[string, int, string](parser, formatter)
//	result, err := combined.Invoke(ctx, "42")
//
//	// 输出始终为流
//	streaming := ComposeStream[string, int, string](parser, formatter)
//	sr, err := streaming.Stream(ctx, "42")
package core

import (
	"context"

	stream "github.com/hexagon-codes/ai-core/streamx"
)

// InvokeToStream 将 Invoke-only 的 Runnable 适配为返回流
//
// 对于只实现了 Invoke 的组件，自动将结果包装为 FromValue 单元素流。
// 适用于将同步组件接入流式管道的场景。
//
// 返回的 Runnable 特性：
//   - Invoke: 直接委托给原始 Runnable
//   - Stream: 调用 Invoke 后通过 FromValue 包装为流
//   - 其他方法通过 BaseRunnable 自动推导
func InvokeToStream[I, O any](r Runnable[I, O]) Runnable[I, O] {
	return NewRunnable[I, O](
		r.Name()+".stream_adapted",
		r.Description(),
		func(ctx context.Context, input I, opts ...Option) (O, error) {
			return r.Invoke(ctx, input, opts...)
		},
	).WithStream(func(ctx context.Context, input I, opts ...Option) (*StreamReader[O], error) {
		result, err := r.Invoke(ctx, input, opts...)
		if err != nil {
			return nil, err
		}
		return stream.FromValue(result), nil
	})
}

// StreamToInvoke 将 Stream-only 的 Runnable 适配为返回值
//
// 对于只实现了 Stream 的组件，自动将流结果通过 Concat 合并为单值。
// 适用于将流式组件接入同步管道的场景。
//
// 返回的 Runnable 特性：
//   - Stream: 直接委托给原始 Runnable
//   - Invoke: 调用 Stream 后通过 Concat 合并为单值
//   - 其他方法通过 BaseRunnable 自动推导
func StreamToInvoke[I, O any](r Runnable[I, O]) Runnable[I, O] {
	return NewRunnable[I, O](
		r.Name()+".invoke_adapted",
		r.Description(),
		func(ctx context.Context, input I, opts ...Option) (O, error) {
			sr, err := r.Stream(ctx, input, opts...)
			if err != nil {
				var zero O
				return zero, err
			}
			return stream.Concat(ctx, sr)
		},
	).WithStream(func(ctx context.Context, input I, opts ...Option) (*StreamReader[O], error) {
		return r.Stream(ctx, input, opts...)
	})
}

// Compose 组合两个 Runnable，自动处理类型匹配
//
// 执行路径（优先级从高到低）：
//   - Invoke 路径: r1.Invoke → r2.Invoke
//   - Stream 路径: r1.Invoke → r2.Stream
//
// 类型参数：
//   - I: 输入类型（第一个 Runnable 的输入）
//   - M: 中间类型（r1 的输出 = r2 的输入）
//   - O: 输出类型（第二个 Runnable 的输出）
func Compose[I, M, O any](r1 Runnable[I, M], r2 Runnable[M, O]) Runnable[I, O] {
	br := NewRunnable[I, O](
		r1.Name()+" -> "+r2.Name(),
		"composed: "+r1.Description()+" -> "+r2.Description(),
		// Invoke 路径: r1.Invoke → r2.Invoke
		func(ctx context.Context, input I, opts ...Option) (O, error) {
			mid, err := r1.Invoke(ctx, input, opts...)
			if err != nil {
				var zero O
				return zero, err
			}
			return r2.Invoke(ctx, mid, opts...)
		},
	)

	// Stream 路径: r1.Invoke → r2.Stream
	br.WithStream(func(ctx context.Context, input I, opts ...Option) (*StreamReader[O], error) {
		mid, err := r1.Invoke(ctx, input, opts...)
		if err != nil {
			return nil, err
		}
		return r2.Stream(ctx, mid, opts...)
	})

	// Collect 路径: Concat 输入流 → r1.Invoke → r2.Invoke
	br.WithCollect(func(ctx context.Context, input *StreamReader[I], opts ...Option) (O, error) {
		in, err := stream.Concat(ctx, input)
		if err != nil {
			var zero O
			return zero, err
		}
		mid, err := r1.Invoke(ctx, in, opts...)
		if err != nil {
			var zero O
			return zero, err
		}
		return r2.Invoke(ctx, mid, opts...)
	})

	// Transform 路径按完整流能力组合，不把 delta 隐式当作独立请求。
	br.WithTransform(func(ctx context.Context, input *StreamReader[I], opts ...Option) (*StreamReader[O], error) {
		return composeTransform(ctx, input, r1, r2, opts...)
	})
	return br
}

// ComposeStream 组合两个 Runnable，输出始终为流
//
// 与 Compose 的区别：Stream 方法会尝试使用 r1.Stream + r2.Transform
// 下游注册原生 Transform 时保留增量管道；否则按其收集后调用的适配合同执行。
//
// 执行路径：
//   - Invoke 路径: r1.Invoke → r2.Invoke（同 Compose）
//   - Stream 路径: r1.Stream → r2.Transform；非流下游先完整收集，逐元素调用需显式 MapEach
func ComposeStream[I, M, O any](r1 Runnable[I, M], r2 Runnable[M, O]) Runnable[I, O] {
	br := NewRunnable[I, O](r1.Name()+" ~> "+r2.Name(), "composed_stream: "+r1.Description()+" -> "+r2.Description(),
		func(ctx context.Context, input I, opts ...Option) (O, error) {
			mid, err := r1.Invoke(ctx, input, opts...)
			if err != nil {
				var zero O
				return zero, err
			}
			return r2.Invoke(ctx, mid, opts...)
		})
	br.WithStream(func(ctx context.Context, input I, opts ...Option) (*StreamReader[O], error) {
		callCtx, cancel := context.WithCancel(ctx)
		mid, err := r1.Stream(callCtx, input, opts...)
		if err != nil {
			cancel()
			return nil, err
		}
		stop := context.AfterFunc(callCtx, func() { _ = mid.Close() })
		out, err := r2.Transform(callCtx, mid, opts...)
		if err != nil {
			stop()
			cancel()
			_ = mid.Close()
			return nil, err
		}
		return forwardOwnedStream(callCtx, out, func() error { stop(); cancel(); return mid.Close() }), nil
	})
	br.WithTransform(func(ctx context.Context, input *StreamReader[I], opts ...Option) (*StreamReader[O], error) {
		return composeTransform(ctx, input, r1, r2, opts...)
	})
	return br
}
