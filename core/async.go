// Package core 提供 Hexagon 框架的核心接口和类型
//
// 本文件实现异步 API 变体：
//   - AsyncRunnable: 异步执行接口
//   - Future: 异步结果封装
//   - 并行执行: 多任务并行
//   - 超时控制: 异步超时处理
//
// 设计借鉴：
//   - Java CompletableFuture
//   - Rust async/await
//   - Python asyncio
package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/retry"
)

// ============== Future 类型 ==============

// Future 异步结果
//
// 线程安全：Future 使用 sync.Once 确保只完成一次，
// 并使用 sync.RWMutex 保护结果的读写，确保并发安全。
type Future[T any] struct {
	// done 完成 channel
	done chan struct{}

	// result 结果
	result T

	// err 错误
	err error

	// once 确保只完成一次
	once sync.Once

	// mu 保护 result 和 err 的读写
	mu sync.RWMutex
}

// NewFuture 创建 Future
func NewFuture[T any]() *Future[T] {
	return &Future[T]{
		done: make(chan struct{}),
	}
}

// Complete 完成 Future
//
// 线程安全：可以被多个 goroutine 并发调用，但只有第一次调用会生效。
func (f *Future[T]) Complete(result T, err error) {
	f.once.Do(func() {
		f.mu.Lock()
		f.result = result
		f.err = err
		f.mu.Unlock()
		close(f.done)
	})
}

// Get 获取结果（阻塞）
//
// 线程安全：等待 Future 完成后返回结果，多个 goroutine 可以并发调用。
func (f *Future[T]) Get() (T, error) {
	<-f.done
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.result, f.err
}

// GetWithTimeout 带超时获取结果
func (f *Future[T]) GetWithTimeout(timeout time.Duration) (T, error) {
	select {
	case <-f.done:
		f.mu.RLock()
		defer f.mu.RUnlock()
		return f.result, f.err
	case <-time.After(timeout):
		var zero T
		return zero, fmt.Errorf("future timeout after %v", timeout)
	}
}

// GetWithContext 带上下文获取结果
func (f *Future[T]) GetWithContext(ctx context.Context) (T, error) {
	select {
	case <-f.done:
		f.mu.RLock()
		defer f.mu.RUnlock()
		return f.result, f.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// IsDone 是否完成
func (f *Future[T]) IsDone() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

// Then 链式处理
func (f *Future[T]) Then(fn func(T) (T, error)) *Future[T] {
	next := NewFuture[T]()

	go func() {
		result, err := f.Get()
		if err != nil {
			next.Complete(result, err)
			return
		}
		newResult, newErr := fn(result)
		next.Complete(newResult, newErr)
	}()

	return next
}

// Catch 错误处理
func (f *Future[T]) Catch(fn func(error) (T, error)) *Future[T] {
	next := NewFuture[T]()

	go func() {
		result, err := f.Get()
		if err != nil {
			newResult, newErr := fn(err)
			next.Complete(newResult, newErr)
			return
		}
		next.Complete(result, nil)
	}()

	return next
}

// ============== AsyncRunnable 接口 ==============

// AsyncRunnable 异步执行接口
type AsyncRunnable[I, O any] interface {
	// InvokeAsync 异步调用
	InvokeAsync(ctx context.Context, input I, opts ...Option) *Future[O]

	// BatchAsync 异步批量调用
	BatchAsync(ctx context.Context, inputs []I, opts ...Option) *Future[[]O]
}

// AsyncWrapper 将同步 Runnable 包装为异步
type AsyncWrapper[I, O any] struct {
	runnable Runnable[I, O]
}

// WrapAsync 包装为异步
func WrapAsync[I, O any](r Runnable[I, O]) *AsyncWrapper[I, O] {
	return &AsyncWrapper[I, O]{runnable: r}
}

// InvokeAsync 异步调用
func (w *AsyncWrapper[I, O]) InvokeAsync(ctx context.Context, input I, opts ...Option) *Future[O] {
	future := NewFuture[O]()

	go func() {
		result, err := w.runnable.Invoke(ctx, input, opts...)
		future.Complete(result, err)
	}()

	return future
}

// BatchAsync 异步批量调用
func (w *AsyncWrapper[I, O]) BatchAsync(ctx context.Context, inputs []I, opts ...Option) *Future[[]O] {
	future := NewFuture[[]O]()

	go func() {
		results, err := w.runnable.Batch(ctx, inputs, opts...)
		future.Complete(results, err)
	}()

	return future
}

// ============== 并行执行 ==============

// Parallel 并行执行多个 Future
func Parallel[T any](futures ...*Future[T]) *Future[[]T] {
	result := NewFuture[[]T]()

	go func() {
		results := make([]T, len(futures))
		var firstErr error

		var wg sync.WaitGroup
		var mu sync.Mutex

		for i, f := range futures {
			wg.Add(1)
			go func(idx int, future *Future[T]) {
				defer wg.Done()
				r, err := future.Get()
				mu.Lock()
				results[idx] = r
				if err != nil && firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}(i, f)
		}

		wg.Wait()
		result.Complete(results, firstErr)
	}()

	return result
}

// ParallelWithLimit 带并发限制的并行执行
func ParallelWithLimit[T any](limit int, futures ...*Future[T]) *Future[[]T] {
	result := NewFuture[[]T]()

	go func() {
		results := make([]T, len(futures))
		var firstErr error

		sem := make(chan struct{}, limit)
		var wg sync.WaitGroup
		var mu sync.Mutex

		for i, f := range futures {
			wg.Add(1)
			sem <- struct{}{}

			go func(idx int, future *Future[T]) {
				defer wg.Done()
				defer func() { <-sem }()

				r, err := future.Get()
				mu.Lock()
				results[idx] = r
				if err != nil && firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}(i, f)
		}

		wg.Wait()
		result.Complete(results, firstErr)
	}()

	return result
}

// Race 竞争执行（返回第一个完成的）
func Race[T any](futures ...*Future[T]) *Future[T] {
	result := NewFuture[T]()

	for _, f := range futures {
		go func(future *Future[T]) {
			r, err := future.Get()
			result.Complete(r, err) // 只有第一个会生效
		}(f)
	}

	return result
}

// Any 任意成功（返回第一个成功的）
//
// 线程安全：使用互斥锁保护错误计数和错误列表。
func Any[T any](futures ...*Future[T]) *Future[T] {
	result := NewFuture[T]()

	var mu sync.Mutex
	var allErrs []error
	errCount := 0
	total := len(futures)

	for _, f := range futures {
		go func(future *Future[T]) {
			r, err := future.Get()
			if err == nil {
				result.Complete(r, nil)
			} else {
				mu.Lock()
				allErrs = append(allErrs, err)
				errCount++
				currentCount := errCount
				mu.Unlock()

				// 所有 future 都失败时才返回错误
				if currentCount == total {
					var zero T
					result.Complete(zero, fmt.Errorf("all futures failed: %v", allErrs))
				}
			}
		}(f)
	}

	return result
}

// ============== 异步工具函数 ==============

// RunAsync 异步执行函数
func RunAsync[T any](fn func() (T, error)) *Future[T] {
	future := NewFuture[T]()

	go func() {
		result, err := fn()
		future.Complete(result, err)
	}()

	return future
}

// RunAsyncWithContext 带上下文的异步执行
func RunAsyncWithContext[T any](ctx context.Context, fn func(context.Context) (T, error)) *Future[T] {
	future := NewFuture[T]()

	go func() {
		result, err := fn(ctx)
		future.Complete(result, err)
	}()

	return future
}

// Delay 延迟执行
func Delay[T any](duration time.Duration, fn func() (T, error)) *Future[T] {
	future := NewFuture[T]()

	go func() {
		time.Sleep(duration)
		result, err := fn()
		future.Complete(result, err)
	}()

	return future
}

// Retry 重试执行
//
// 在独立 goroutine 中以固定延迟重试 fn，结果经 Future 异步返回。
// 重试循环本身下沉至 toolkit/util/retry.Do，本函数保留 Future/goroutine 封装。
//
// 语义对齐说明（与下沉前手写循环逐项等价）：
//   - 总尝试次数：手写循环为 i 0..maxRetries，即首次调用 + maxRetries 次重试，
//     共 maxRetries+1 次，故 toolkit 的 Attempts 取 maxRetries+1。
//   - 固定延迟：手写循环每次重试前固定 Sleep(delay)，无指数退避。toolkit 的
//     DefaultConfig 默认 Multiplier=2.0 会自动启用指数退避，故显式以
//     DelayType(FixedDelay) 覆盖为固定延迟；同时将 MaxDelay 设为 delay，
//     避免默认 30s 上限在 delay>30s 时反向裁剪固定延迟（FixedDelay 返回值
//     会被 MaxDelay 封顶）。FixedDelay 路径不叠加抖动，与手写循环一致。
//   - 重试条件：手写循环对任意非 nil 错误重试，等同 toolkit 默认 RetryIf。
//   - 最终错误可解包：手写循环重试耗尽直接返回原始 lastErr。toolkit 默认用
//     %v 嵌入会丢失错误链，故开启 WithUnwrapFinalError() 使最终错误多 %w
//     包装，errors.Is(err, 原始错误) 仍成立（错误文案会带 ErrMaxAttemptsReached 前缀）。
//
// 说明：本函数签名不含 context.Context，仍使用 retry.Do（非 ctx 版），
// 延迟期间不感知取消——这是签名约束，ctx 感知属另一议题，本次下沉不引入。
func Retry[T any](maxRetries int, delay time.Duration, fn func() (T, error)) *Future[T] {
	future := NewFuture[T]()

	go func() {
		var result T
		err := retry.Do(func() error {
			var callErr error
			result, callErr = fn()
			return callErr
		},
			retry.Attempts(maxRetries+1),
			retry.Delay(delay),
			retry.MaxDelay(delay),
			retry.DelayType(retry.FixedDelay),
			retry.WithUnwrapFinalError(),
		)
		if err != nil {
			var zero T
			future.Complete(zero, err)
			return
		}
		future.Complete(result, nil)
	}()

	return future
}

// ============== Promise 模式 ==============

// Promise 承诺模式
type Promise[T any] struct {
	future *Future[T]
}

// NewPromise 创建 Promise
func NewPromise[T any]() *Promise[T] {
	return &Promise[T]{
		future: NewFuture[T](),
	}
}

// Resolve 成功完成
func (p *Promise[T]) Resolve(value T) {
	p.future.Complete(value, nil)
}

// Reject 失败
func (p *Promise[T]) Reject(err error) {
	var zero T
	p.future.Complete(zero, err)
}

// Future 获取 Future
func (p *Promise[T]) Future() *Future[T] {
	return p.future
}

// ============== 回调模式 ==============

// Callback 回调类型
type Callback[T any] func(result T, err error)

// InvokeWithCallback 带回调的调用
func InvokeWithCallback[I, O any](
	ctx context.Context,
	runnable Runnable[I, O],
	input I,
	callback Callback[O],
	opts ...Option,
) {
	go func() {
		result, err := runnable.Invoke(ctx, input, opts...)
		callback(result, err)
	}()
}

// ============== Channel 模式 ==============

// ResultChannel 结果通道
type ResultChannel[T any] struct {
	ch chan Result[T]
}

// Result 结果
type Result[T any] struct {
	Value T
	Err   error
}

// NewResultChannel 创建结果通道
func NewResultChannel[T any](buffer int) *ResultChannel[T] {
	return &ResultChannel[T]{
		ch: make(chan Result[T], buffer),
	}
}

// Send 发送结果
func (rc *ResultChannel[T]) Send(value T, err error) {
	rc.ch <- Result[T]{Value: value, Err: err}
}

// Receive 接收结果
func (rc *ResultChannel[T]) Receive() (T, error) {
	r := <-rc.ch
	return r.Value, r.Err
}

// ReceiveWithContext 带上下文接收
func (rc *ResultChannel[T]) ReceiveWithContext(ctx context.Context) (T, error) {
	select {
	case r := <-rc.ch:
		return r.Value, r.Err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// Close 关闭通道
func (rc *ResultChannel[T]) Close() {
	close(rc.ch)
}

// Channel 获取底层通道
func (rc *ResultChannel[T]) Channel() <-chan Result[T] {
	return rc.ch
}
