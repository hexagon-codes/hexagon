// Package core 提供 Hexagon 框架的核心接口和类型
//
// 本包定义了框架的基础抽象：
//   - Runnable[I, O]: 六范式统一执行接口
//   - Component[I, O]: 向后兼容的组件接口
//   - Option: 执行选项
//   - Schema: JSON Schema 类型定义
//
// 六范式统一执行模型：
//   - Invoke: 普通输入 → 普通输出
//   - Stream: 普通输入 → 流输出
//   - Batch: 多输入 → 多输出（并发）
//   - Collect: 流输入 → 普通输出
//   - Transform: 流输入 → 流输出
//   - BatchStream: 多输入 → 流输出
//
// 设计借鉴：
//   - LangChain: Runnable 接口 (invoke/stream/batch)
//   - Eino: 四范式 (Invoke/Stream/Collect/Transform)
//   - Spring AI: Fluent API 风格
//
// 使用示例：
//
//	// 实现 Runnable 接口
//	type MyComponent struct{}
//
//	func (c *MyComponent) Invoke(ctx context.Context, input string, opts ...Option) (string, error) {
//	    return "processed: " + input, nil
//	}
//
//	// 其他方法由 BaseRunnable 自动实现
package core

import (
	"context"
	"errors"
	"io"

	"github.com/hexagon-codes/ai-core/llm"
	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/hexagon/internal/pool"
)

// ============== 类型别名 ==============

// Schema 是 ai-core/llm.Schema 的别名
type Schema = llm.Schema

// StreamReader 是 stream.StreamReader 的别名
type StreamReader[T any] = stream.StreamReader[T]

// StreamWriter 是 stream.StreamWriter 的别名
type StreamWriter[T any] = stream.StreamWriter[T]

// Stream 是 stream.StreamReader 的向后兼容别名
// Deprecated: 请使用 StreamReader
type Stream[T any] = stream.StreamReader[T]

// SchemaOf 从 Go 类型生成 Schema
func SchemaOf[T any]() *Schema {
	return llm.SchemaOf[T]()
}

// NewSliceStream 从切片创建流（向后兼容）
// Deprecated: 请使用 stream.FromSlice
func NewSliceStream[T any](items []T) *stream.StreamReader[T] {
	return stream.FromSlice(items)
}

// ============== Option 选项系统 ==============

// Option 执行选项
type Option interface {
	Apply(*Options)
}

// Options 选项集合
type Options struct {
	// 基础选项
	Timeout    int64          // 超时时间（毫秒）
	MaxRetries int            // 最大重试次数
	Metadata   map[string]any // 元数据

	// 流式选项
	StreamBufferSize int   // 流缓冲区大小
	StreamTimeout    int64 // 流操作超时（毫秒）

	// 节点选项（用于图编排）
	NodeID   string // 目标节点ID
	NodeType string // 目标节点类型

	// 扩展选项
	Extra map[string]any
}

// OptionFunc 函数式选项
type OptionFunc func(*Options)

func (f OptionFunc) Apply(opts *Options) {
	f(opts)
}

// WithTimeout 设置超时
func WithTimeout(ms int64) Option {
	return OptionFunc(func(o *Options) {
		o.Timeout = ms
	})
}

// WithMaxRetries 设置最大重试次数
func WithMaxRetries(n int) Option {
	return OptionFunc(func(o *Options) {
		o.MaxRetries = n
	})
}

// WithMetadata 设置元数据
func WithMetadata(meta map[string]any) Option {
	return OptionFunc(func(o *Options) {
		o.Metadata = meta
	})
}

// WithStreamBuffer 设置流缓冲区大小
func WithStreamBuffer(size int) Option {
	return OptionFunc(func(o *Options) {
		o.StreamBufferSize = size
	})
}

// WithNodeID 设置目标节点ID
func WithNodeID(id string) Option {
	return OptionFunc(func(o *Options) {
		o.NodeID = id
	})
}

// WithNodeType 设置目标节点类型
func WithNodeType(typ string) Option {
	return OptionFunc(func(o *Options) {
		o.NodeType = typ
	})
}

// ApplyOptions 应用选项
func ApplyOptions(opts ...Option) *Options {
	o := &Options{
		Metadata: make(map[string]any),
		Extra:    make(map[string]any),
	}
	for _, opt := range opts {
		opt.Apply(o)
	}
	return o
}

// ============== Runnable 六范式接口 ==============

// Runnable 是所有可执行组件的统一接口
// 六范式统一执行模型，超越 Eino 的四范式
//
// 类型参数：
//   - I: 输入类型
//   - O: 输出类型
type Runnable[I, O any] interface {
	// === 基础三范式 (对标 LangChain) ===

	// Invoke 同步调用：普通输入 → 普通输出
	// 这是最基本的执行方式
	Invoke(ctx context.Context, input I, opts ...Option) (O, error)

	// Stream 流式输出：普通输入 → 流输出
	// 用于需要流式返回的场景，如 LLM 对话
	Stream(ctx context.Context, input I, opts ...Option) (*StreamReader[O], error)

	// Batch 批量调用：多输入 → 多输出（并发执行）
	// 自动并发执行，结果保持输入顺序
	Batch(ctx context.Context, inputs []I, opts ...Option) ([]O, error)

	// === 流输入范式 (对标 Eino) ===

	// Collect 流收集：流输入 → 普通输出
	// 消费整个流，返回单一结果
	Collect(ctx context.Context, input *StreamReader[I], opts ...Option) (O, error)

	// Transform 流转换：流输入 → 流输出
	// 流到流的转换，支持背压
	Transform(ctx context.Context, input *StreamReader[I], opts ...Option) (*StreamReader[O], error)

	// === 扩展范式 (Hexagon 独创) ===

	// BatchStream 批量流式：多输入 → 流输出（合并流）
	// 并发执行多个输入，将结果合并为单一流
	BatchStream(ctx context.Context, inputs []I, opts ...Option) (*StreamReader[O], error)

	// === 元信息 ===

	// Name 返回组件名称
	Name() string

	// Description 返回组件描述
	Description() string

	// InputSchema 返回输入参数的 Schema
	InputSchema() *Schema

	// OutputSchema 返回输出参数的 Schema
	OutputSchema() *Schema
}

// ============== Component 向后兼容接口 ==============

// Component 是 Runnable 的向后兼容别名
// 保持与旧版本 API 的兼容性
type Component[I, O any] interface {
	Runnable[I, O]
}

// ============== BaseRunnable 基础实现 ==============

// BaseRunnable 提供 Runnable 接口的基础实现
// 只需实现 Invoke 方法，其他方法自动推导
type BaseRunnable[I, O any] struct {
	name        string
	description string

	// 核心实现函数
	invokeFn      func(context.Context, I, ...Option) (O, error)
	streamFn      func(context.Context, I, ...Option) (*StreamReader[O], error)
	batchFn       func(context.Context, []I, ...Option) ([]O, error)
	collectFn     func(context.Context, *StreamReader[I], ...Option) (O, error)
	transformFn   func(context.Context, *StreamReader[I], ...Option) (*StreamReader[O], error)
	batchStreamFn func(context.Context, []I, ...Option) (*StreamReader[O], error)
}

// NewRunnable 创建基础 Runnable
func NewRunnable[I, O any](name, description string, invokeFn func(context.Context, I, ...Option) (O, error)) *BaseRunnable[I, O] {
	return &BaseRunnable[I, O]{
		name:        name,
		description: description,
		invokeFn:    invokeFn,
	}
}

// Name 返回组件名称
func (r *BaseRunnable[I, O]) Name() string {
	return r.name
}

// Description 返回组件描述
func (r *BaseRunnable[I, O]) Description() string {
	return r.description
}

// InputSchema 返回输入 Schema
func (r *BaseRunnable[I, O]) InputSchema() *Schema {
	return llm.SchemaOf[I]()
}

// OutputSchema 返回输出 Schema
func (r *BaseRunnable[I, O]) OutputSchema() *Schema {
	return llm.SchemaOf[O]()
}

// Invoke 执行组件
func (r *BaseRunnable[I, O]) Invoke(ctx context.Context, input I, opts ...Option) (O, error) {
	if r.invokeFn != nil {
		return r.invokeFn(ctx, input, opts...)
	}
	// 从 Stream 推导
	if r.streamFn != nil {
		sr, err := r.streamFn(ctx, input, opts...)
		if err != nil {
			var zero O
			return zero, err
		}
		return stream.Concat(ctx, sr)
	}
	// 从 Collect 推导
	if r.collectFn != nil {
		sr := stream.FromValue(input)
		return r.collectFn(ctx, sr, opts...)
	}
	// 从 Transform 推导
	if r.transformFn != nil {
		sr := stream.FromValue(input)
		outSr, err := r.transformFn(ctx, sr, opts...)
		if err != nil {
			var zero O
			return zero, err
		}
		return stream.Concat(ctx, outSr)
	}
	var zero O
	return zero, nil
}

// Stream 流式执行组件
func (r *BaseRunnable[I, O]) Stream(ctx context.Context, input I, opts ...Option) (*StreamReader[O], error) {
	if r.streamFn != nil {
		return r.streamFn(ctx, input, opts...)
	}
	// 从 Invoke 推导
	if r.invokeFn != nil {
		result, err := r.invokeFn(ctx, input, opts...)
		if err != nil {
			return nil, err
		}
		return stream.FromValue(result), nil
	}
	// 从 Transform 推导
	if r.transformFn != nil {
		sr := stream.FromValue(input)
		return r.transformFn(ctx, sr, opts...)
	}
	return stream.FromSlice[O](nil), nil
}

// Batch 批量执行组件
// 使用 toolkit/poolx 协程池进行并发控制，避免 goroutine 爆炸
func (r *BaseRunnable[I, O]) Batch(ctx context.Context, inputs []I, opts ...Option) ([]O, error) {
	if r.batchFn != nil {
		return r.batchFn(ctx, inputs, opts...)
	}

	if len(inputs) == 0 {
		return nil, nil
	}

	// 单个输入直接执行
	if len(inputs) == 1 {
		result, err := r.Invoke(ctx, inputs[0], opts...)
		if err != nil {
			return nil, err
		}
		return []O{result}, nil
	}

	// 使用协程池并发执行
	// pool.Map 内部使用 toolkit/poolx，自动控制并发数
	return pool.Map(ctx, inputs, func(input I) (O, error) {
		return r.Invoke(ctx, input, opts...)
	})
}

// Collect 流收集
func (r *BaseRunnable[I, O]) Collect(ctx context.Context, input *StreamReader[I], opts ...Option) (O, error) {
	if r.collectFn != nil {
		return r.collectFn(ctx, input, opts...)
	}
	// 从 Invoke 推导：先合并输入流，再调用 Invoke
	if r.invokeFn != nil {
		in, err := stream.Concat(ctx, input)
		if err != nil {
			var zero O
			return zero, err
		}
		return r.invokeFn(ctx, in, opts...)
	}
	// 从 Transform 推导：Transform 后合并输出流
	if r.transformFn != nil {
		outSr, err := r.transformFn(ctx, input, opts...)
		if err != nil {
			var zero O
			return zero, err
		}
		return stream.Concat(ctx, outSr)
	}
	var zero O
	return zero, nil
}

// Transform 流转换
//
// 注意：从 Invoke 推导时，goroutine 会正确处理 context 取消和 EOF，
// 确保不会发生 goroutine 泄漏。
func (r *BaseRunnable[I, O]) Transform(ctx context.Context, input *StreamReader[I], opts ...Option) (*StreamReader[O], error) {
	if r.transformFn != nil {
		return r.transformFn(ctx, input, opts...)
	}
	// 从 Stream 推导：先合并输入流，再调用 Stream
	if r.streamFn != nil {
		in, err := stream.Concat(ctx, input)
		if err != nil {
			return nil, err
		}
		return r.streamFn(ctx, in, opts...)
	}
	// 从 Invoke 推导：对每个输入元素调用 Invoke
	if r.invokeFn != nil {
		reader, writer := stream.Pipe[O](10)
		go func() {
			defer writer.Close()
			for {
				// 先检查 context 是否已取消
				select {
				case <-ctx.Done():
					writer.CloseWithError(ctx.Err())
					return
				default:
				}

				in, err := input.Recv()
				if err != nil {
					// 区分 EOF 和真正的错误
					// EOF 表示输入流正常结束，不需要报错
					// 其他错误需要传递给输出流
					// 使用 errors.Is 而非字符串比较，以正确识别被包装的 EOF 错误
					if !errors.Is(err, io.EOF) && err.Error() != "stream closed" {
						writer.CloseWithError(err)
					}
					return
				}

				out, err := r.invokeFn(ctx, in, opts...)
				if err != nil {
					writer.CloseWithError(err)
					return
				}

				// 发送时也检查 context
				select {
				case <-ctx.Done():
					writer.CloseWithError(ctx.Err())
					return
				default:
					if err := writer.Send(out); err != nil {
						return
					}
				}
			}
		}()
		return reader, nil
	}
	return stream.FromSlice[O](nil), nil
}

// BatchStream 批量流式
// 使用 toolkit/poolx 协程池进行并发控制
func (r *BaseRunnable[I, O]) BatchStream(ctx context.Context, inputs []I, opts ...Option) (*StreamReader[O], error) {
	if r.batchStreamFn != nil {
		return r.batchStreamFn(ctx, inputs, opts...)
	}

	if len(inputs) == 0 {
		return stream.FromSlice[O](nil), nil
	}

	// 使用协程池并发执行 Stream
	readers, err := pool.Map(ctx, inputs, func(input I) (*StreamReader[O], error) {
		return r.Stream(ctx, input, opts...)
	})
	if err != nil {
		return nil, err
	}

	// 过滤掉 nil
	validReaders := make([]*StreamReader[O], 0, len(readers))
	for _, rd := range readers {
		if rd != nil {
			validReaders = append(validReaders, rd)
		}
	}

	return stream.Merge(validReaders...), nil
}

// === Builder 方法 ===

// WithStream 设置 Stream 实现
func (r *BaseRunnable[I, O]) WithStream(fn func(context.Context, I, ...Option) (*StreamReader[O], error)) *BaseRunnable[I, O] {
	r.streamFn = fn
	return r
}

// WithBatch 设置 Batch 实现
func (r *BaseRunnable[I, O]) WithBatch(fn func(context.Context, []I, ...Option) ([]O, error)) *BaseRunnable[I, O] {
	r.batchFn = fn
	return r
}

// WithCollect 设置 Collect 实现
func (r *BaseRunnable[I, O]) WithCollect(fn func(context.Context, *StreamReader[I], ...Option) (O, error)) *BaseRunnable[I, O] {
	r.collectFn = fn
	return r
}

// WithTransform 设置 Transform 实现
func (r *BaseRunnable[I, O]) WithTransform(fn func(context.Context, *StreamReader[I], ...Option) (*StreamReader[O], error)) *BaseRunnable[I, O] {
	r.transformFn = fn
	return r
}

// WithBatchStream 设置 BatchStream 实现
func (r *BaseRunnable[I, O]) WithBatchStream(fn func(context.Context, []I, ...Option) (*StreamReader[O], error)) *BaseRunnable[I, O] {
	r.batchStreamFn = fn
	return r
}

// ============== 函数式 Runnable ==============

// RunnableFunc 从函数创建 Runnable
func RunnableFunc[I, O any](name string, fn func(context.Context, I) (O, error)) Runnable[I, O] {
	return NewRunnable(name, "", func(ctx context.Context, input I, opts ...Option) (O, error) {
		return fn(ctx, input)
	})
}

// RunnableLambda 从 lambda 创建 Runnable（简化版）
func RunnableLambda[I, O any](fn func(I) O) Runnable[I, O] {
	return NewRunnable("lambda", "", func(ctx context.Context, input I, opts ...Option) (O, error) {
		return fn(input), nil
	})
}
