// Package core 提供 Hexagon 框架的核心接口和类型
//
// 本文件实现 WithFallback 机制：
//   - Fallback: 降级处理
//   - Retry: 重试机制
//   - CircuitBreaker: 熔断器
//   - RunnableWithFallback: 带降级的 Runnable
//
//   - Resilience4j: 弹性模式
//   - Polly: 弹性和瞬态故障处理
package core

import (
	"context"
	"errors"
	"time"

	"github.com/hexagon-codes/toolkit/util/circuit"
	"github.com/hexagon-codes/toolkit/util/retry"
)

// ============== 错误定义 ==============

var (
	// ErrAllFallbacksFailed 所有降级都失败
	ErrAllFallbacksFailed = errors.New("all fallbacks failed")

	// ErrCircuitOpen 熔断器打开
	ErrCircuitOpen = errors.New("circuit breaker is open")

	// ErrMaxRetriesExceeded 超过最大重试次数
	ErrMaxRetriesExceeded = errors.New("max retries exceeded")
)

// ============== Fallback 接口 ==============

// FallbackOption Fallback 选项
type FallbackOption func(*FallbackConfig)

// FallbackConfig Fallback 配置
type FallbackConfig struct {
	// ExceptionsToHandle 要处理的异常类型
	ExceptionsToHandle []error

	// OnFallback 降级回调
	OnFallback func(err error, fallbackIndex int)
}

// WithExceptions 设置要处理的异常类型
func WithExceptions(errs ...error) FallbackOption {
	return func(c *FallbackConfig) {
		c.ExceptionsToHandle = errs
	}
}

// WithFallbackCallback 设置降级回调
func WithFallbackCallback(fn func(err error, fallbackIndex int)) FallbackOption {
	return func(c *FallbackConfig) {
		c.OnFallback = fn
	}
}

// ============== RunnableWithFallback ==============

// RunnableWithFallback 带降级的 Runnable
type RunnableWithFallback[I, O any] struct {
	primary   Runnable[I, O]
	fallbacks []Runnable[I, O]
	config    *FallbackConfig
}

// WithFallback 创建带降级的 Runnable
//
// 示例:
//
//	runnable := core.WithFallback(
//	    primaryRunnable,
//	    fallbackRunnable1,
//	    fallbackRunnable2,
//	)
//	result, err := runnable.Invoke(ctx, input)
func WithFallback[I, O any](primary Runnable[I, O], fallbacks ...Runnable[I, O]) *RunnableWithFallback[I, O] {
	return &RunnableWithFallback[I, O]{
		primary:   primary,
		fallbacks: fallbacks,
		config:    &FallbackConfig{},
	}
}

// WithOptions 设置选项
func (r *RunnableWithFallback[I, O]) WithOptions(opts ...FallbackOption) *RunnableWithFallback[I, O] {
	for _, opt := range opts {
		opt(r.config)
	}
	return r
}

// Name 返回名称
func (r *RunnableWithFallback[I, O]) Name() string {
	return r.primary.Name() + "_with_fallback"
}

// Description 返回描述
func (r *RunnableWithFallback[I, O]) Description() string {
	return r.primary.Description()
}

// InputSchema 返回输入 Schema
func (r *RunnableWithFallback[I, O]) InputSchema() *Schema {
	return r.primary.InputSchema()
}

// OutputSchema 返回输出 Schema
func (r *RunnableWithFallback[I, O]) OutputSchema() *Schema {
	return r.primary.OutputSchema()
}

// Invoke 执行（带降级）
func (r *RunnableWithFallback[I, O]) Invoke(ctx context.Context, input I, opts ...Option) (O, error) {
	// 先尝试主 Runnable
	result, err := r.primary.Invoke(ctx, input, opts...)
	if err == nil {
		return result, nil
	}

	// 检查是否应该降级
	if !r.shouldFallback(err) {
		return result, err
	}

	// 尝试降级 Runnables
	for i, fallback := range r.fallbacks {
		if r.config.OnFallback != nil {
			r.config.OnFallback(err, i)
		}

		result, err = fallback.Invoke(ctx, input, opts...)
		if err == nil {
			return result, nil
		}

		if !r.shouldFallback(err) {
			return result, err
		}
	}

	var zero O
	return zero, ErrAllFallbacksFailed
}

// Stream 流式执行（带降级）
func (r *RunnableWithFallback[I, O]) Stream(ctx context.Context, input I, opts ...Option) (*StreamReader[O], error) {
	stream, err := r.primary.Stream(ctx, input, opts...)
	if err == nil {
		return stream, nil
	}

	if !r.shouldFallback(err) {
		return nil, err
	}

	for i, fallback := range r.fallbacks {
		if r.config.OnFallback != nil {
			r.config.OnFallback(err, i)
		}

		stream, err = fallback.Stream(ctx, input, opts...)
		if err == nil {
			return stream, nil
		}

		if !r.shouldFallback(err) {
			return nil, err
		}
	}

	return nil, ErrAllFallbacksFailed
}

// Batch 批量执行（带降级）
func (r *RunnableWithFallback[I, O]) Batch(ctx context.Context, inputs []I, opts ...Option) ([]O, error) {
	results, err := r.primary.Batch(ctx, inputs, opts...)
	if err == nil {
		return results, nil
	}

	if !r.shouldFallback(err) {
		return nil, err
	}

	for i, fallback := range r.fallbacks {
		if r.config.OnFallback != nil {
			r.config.OnFallback(err, i)
		}

		results, err = fallback.Batch(ctx, inputs, opts...)
		if err == nil {
			return results, nil
		}

		if !r.shouldFallback(err) {
			return nil, err
		}
	}

	return nil, ErrAllFallbacksFailed
}

// Collect 流收集（带降级）
func (r *RunnableWithFallback[I, O]) Collect(ctx context.Context, input *StreamReader[I], opts ...Option) (O, error) {
	return r.primary.Collect(ctx, input, opts...)
}

// Transform 流转换（带降级）
func (r *RunnableWithFallback[I, O]) Transform(ctx context.Context, input *StreamReader[I], opts ...Option) (*StreamReader[O], error) {
	return r.primary.Transform(ctx, input, opts...)
}

// BatchStream 批量流式（带降级）
func (r *RunnableWithFallback[I, O]) BatchStream(ctx context.Context, inputs []I, opts ...Option) (*StreamReader[O], error) {
	stream, err := r.primary.BatchStream(ctx, inputs, opts...)
	if err == nil {
		return stream, nil
	}

	if !r.shouldFallback(err) {
		return nil, err
	}

	for i, fallback := range r.fallbacks {
		if r.config.OnFallback != nil {
			r.config.OnFallback(err, i)
		}

		stream, err = fallback.BatchStream(ctx, inputs, opts...)
		if err == nil {
			return stream, nil
		}

		if !r.shouldFallback(err) {
			return nil, err
		}
	}

	return nil, ErrAllFallbacksFailed
}

func (r *RunnableWithFallback[I, O]) shouldFallback(err error) bool {
	if len(r.config.ExceptionsToHandle) == 0 {
		return true
	}

	for _, e := range r.config.ExceptionsToHandle {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// ============== Retry Runnable ==============

// RetryConfig 重试配置
type RetryConfig struct {
	// MaxRetries 最大重试次数
	MaxRetries int

	// InitialDelay 初始延迟
	InitialDelay time.Duration

	// MaxDelay 最大延迟
	MaxDelay time.Duration

	// Multiplier 延迟倍数
	Multiplier float64

	// Jitter 抖动比例 (0-1)
	Jitter float64

	// RetryOn 判断是否重试
	RetryOn func(error) bool

	// OnRetry 重试回调
	OnRetry func(attempt int, err error)
}

// DefaultRetryConfig 默认重试配置
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxRetries:   3,
		InitialDelay: time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
		Jitter:       0.1,
		RetryOn:      func(err error) bool { return err != nil },
	}
}

// RunnableWithRetry 带重试的 Runnable
type RunnableWithRetry[I, O any] struct {
	runnable Runnable[I, O]
	config   *RetryConfig
}

// WithRetry 创建带重试的 Runnable
func WithRetry[I, O any](runnable Runnable[I, O], config ...*RetryConfig) *RunnableWithRetry[I, O] {
	cfg := DefaultRetryConfig()
	if len(config) > 0 && config[0] != nil {
		cfg = config[0]
	}

	return &RunnableWithRetry[I, O]{
		runnable: runnable,
		config:   cfg,
	}
}

// Name 返回名称
func (r *RunnableWithRetry[I, O]) Name() string {
	return r.runnable.Name() + "_with_retry"
}

// Description 返回描述
func (r *RunnableWithRetry[I, O]) Description() string {
	return r.runnable.Description()
}

// InputSchema 返回输入 Schema
func (r *RunnableWithRetry[I, O]) InputSchema() *Schema {
	return r.runnable.InputSchema()
}

// OutputSchema 返回输出 Schema
func (r *RunnableWithRetry[I, O]) OutputSchema() *Schema {
	return r.runnable.OutputSchema()
}

// retryOptions 将 RetryConfig 映射为 toolkit/util/retry 的 Option 列表。
//
// 语义对齐说明（与下沉前手写循环逐项等价）：
//   - 总尝试次数：手写循环为 attempt 0..MaxRetries，即首次调用 + MaxRetries 次重试，
//     共 MaxRetries+1 次。toolkit 的 MaxAttempts 即"总尝试次数"，故取 MaxRetries+1。
//   - 退避曲线：手写循环首次重试等待 InitialDelay，之后每次乘以 Multiplier 并以
//     MaxDelay 封顶；toolkit 在 Multiplier>0 时自动启用 ExponentialBackoff，其
//     第 n 次（一基）延迟为 Delay*Multiplier^(n-1) 并由 MaxDelay 封顶，曲线一致。
//     手写循环不使用 Jitter 字段，故此处也不设置抖动，保持无抖动行为。
//   - RetryOn → RetryIf：判定为不可重试时直接返回原始错误，语义一致。
//   - OnRetry 计数：手写循环以零基传入（首次重试 attempt==0），故开启
//     WithOnRetryZeroBased() 将 toolkit 默认的一基计数平移为零基。
//   - 最终错误可解包：手写循环重试耗尽直接返回原始 lastErr，调用方可
//     errors.Is(err, 原始错误)。toolkit 默认用 %v 嵌入会丢失错误链，故开启
//     WithUnwrapFinalError() 使最终错误多 %w 包装，errors.Is 同时命中
//     ErrMaxAttemptsReached 与原始 lastErr。
func (r *RunnableWithRetry[I, O]) retryOptions() []retry.Option {
	opts := []retry.Option{
		retry.Attempts(r.config.MaxRetries + 1),
		retry.Delay(r.config.InitialDelay),
		retry.MaxDelay(r.config.MaxDelay),
		retry.Multiplier(r.config.Multiplier),
		// OnRetry 采用零基计数，对齐手写循环的 attempt 语义
		retry.WithOnRetryZeroBased(),
		// 重试耗尽时最终错误可被 errors.Is 解包到原始 lastErr
		retry.WithUnwrapFinalError(),
	}
	if r.config.RetryOn != nil {
		opts = append(opts, retry.RetryIf(r.config.RetryOn))
	}
	if r.config.OnRetry != nil {
		// toolkit 的 OnRetry(n, err) 中 n 已按零基平移，与手写循环一致
		opts = append(opts, retry.OnRetry(r.config.OnRetry))
	}
	return opts
}

// Invoke 执行（带重试）
//
// 重试与退避逻辑下沉至 toolkit/util/retry.DoWithContext，
// 本方法仅负责承接 RunnableWithRetry 的输入/输出并捕获最近一次结果。
func (r *RunnableWithRetry[I, O]) Invoke(ctx context.Context, input I, opts ...Option) (O, error) {
	var result O
	err := retry.DoWithContext(ctx, func() error {
		var callErr error
		result, callErr = r.runnable.Invoke(ctx, input, opts...)
		return callErr
	}, r.retryOptions()...)

	if err != nil {
		// 失败时（不可重试 / 重试耗尽 / ctx 取消）返回最近一次调用的结果。
		// 不可重试路径下 result 为该次失败调用的产出；其余路径下为零值或
		// 最近一次产出，与下沉前手写循环的返回语义一致。
		return result, err
	}
	return result, nil
}

// Stream 流式执行（带重试）
//
// 重试与退避逻辑下沉至 toolkit/util/retry.DoWithContext，
// 本方法仅负责承接流的获取并捕获最近一次成功的流读取器。
func (r *RunnableWithRetry[I, O]) Stream(ctx context.Context, input I, opts ...Option) (*StreamReader[O], error) {
	var stream *StreamReader[O]
	err := retry.DoWithContext(ctx, func() error {
		var callErr error
		stream, callErr = r.runnable.Stream(ctx, input, opts...)
		return callErr
	}, r.retryOptions()...)

	if err != nil {
		return nil, err
	}
	return stream, nil
}

// Batch 批量执行（带重试）
func (r *RunnableWithRetry[I, O]) Batch(ctx context.Context, inputs []I, opts ...Option) ([]O, error) {
	return r.runnable.Batch(ctx, inputs, opts...)
}

// Collect 流收集
func (r *RunnableWithRetry[I, O]) Collect(ctx context.Context, input *StreamReader[I], opts ...Option) (O, error) {
	return r.runnable.Collect(ctx, input, opts...)
}

// Transform 流转换
func (r *RunnableWithRetry[I, O]) Transform(ctx context.Context, input *StreamReader[I], opts ...Option) (*StreamReader[O], error) {
	return r.runnable.Transform(ctx, input, opts...)
}

// BatchStream 批量流式
func (r *RunnableWithRetry[I, O]) BatchStream(ctx context.Context, inputs []I, opts ...Option) (*StreamReader[O], error) {
	return r.runnable.BatchStream(ctx, inputs, opts...)
}

// ============== Circuit Breaker ==============

// CircuitState 熔断器状态
type CircuitState int

const (
	// CircuitClosed 关闭状态（正常）
	CircuitClosed CircuitState = iota
	// CircuitOpen 打开状态（熔断）
	CircuitOpen
	// CircuitHalfOpen 半开状态（尝试恢复）
	CircuitHalfOpen
)

// CircuitBreakerConfig 熔断器配置
type CircuitBreakerConfig struct {
	// FailureThreshold 失败阈值
	FailureThreshold int

	// SuccessThreshold 成功阈值（半开状态）
	SuccessThreshold int

	// Timeout 熔断超时
	Timeout time.Duration

	// OnStateChange 状态变化回调
	OnStateChange func(from, to CircuitState)
}

// DefaultCircuitBreakerConfig 默认熔断器配置
func DefaultCircuitBreakerConfig() *CircuitBreakerConfig {
	return &CircuitBreakerConfig{
		FailureThreshold: 5,
		SuccessThreshold: 3,
		Timeout:          30 * time.Second,
	}
}

// CircuitBreaker 熔断器
//
// 状态机委托 toolkit/util/circuit，避免与 toolkit 各维护一份私网/状态逻辑产生防护漂移。
// 用法契约：Allow() 作门控检查（允许返回 true），随后**恰好**配对一次
// RecordSuccess() 或 RecordFailure()（RunnableWithCircuitBreaker.Invoke/Stream 即如此）。
type CircuitBreaker struct {
	config  *CircuitBreakerConfig
	breaker *circuit.Breaker
}

// NewCircuitBreaker 创建熔断器
func NewCircuitBreaker(config ...*CircuitBreakerConfig) *CircuitBreaker {
	cfg := DefaultCircuitBreakerConfig()
	if len(config) > 0 && config[0] != nil {
		cfg = config[0]
	}

	// 半开探测数取 SuccessThreshold（累计该数次成功即关闭），至少 1。
	halfOpenMax := max(cfg.SuccessThreshold, 1)
	opts := []circuit.Option{
		circuit.WithThreshold(cfg.FailureThreshold),
		circuit.WithSuccessThreshold(cfg.SuccessThreshold),
		circuit.WithTimeout(cfg.Timeout),
		circuit.WithHalfOpenMaxRequests(halfOpenMax),
	}
	if cfg.OnStateChange != nil {
		onChange := cfg.OnStateChange
		opts = append(opts, circuit.WithOnStateChange(func(from, to circuit.State) {
			onChange(CircuitState(from), CircuitState(to))
		}))
	}

	return &CircuitBreaker{
		config:  cfg,
		breaker: circuit.New(opts...),
	}
}

// State 获取当前状态
func (cb *CircuitBreaker) State() CircuitState {
	return CircuitState(cb.breaker.State())
}

// Allow 检查是否允许执行（开路返回 false）
func (cb *CircuitBreaker) Allow() bool {
	return cb.breaker.Allow() == nil
}

// RecordSuccess 记录成功（须与一次 Allow 配对）
func (cb *CircuitBreaker) RecordSuccess() {
	cb.breaker.Success()
}

// RecordFailure 记录失败（须与一次 Allow 配对）
func (cb *CircuitBreaker) RecordFailure() {
	cb.breaker.Failure()
}

// RunnableWithCircuitBreaker 带熔断器的 Runnable
type RunnableWithCircuitBreaker[I, O any] struct {
	runnable Runnable[I, O]
	breaker  *CircuitBreaker
}

// WithCircuitBreaker 创建带熔断器的 Runnable
func WithCircuitBreaker[I, O any](runnable Runnable[I, O], config ...*CircuitBreakerConfig) *RunnableWithCircuitBreaker[I, O] {
	return &RunnableWithCircuitBreaker[I, O]{
		runnable: runnable,
		breaker:  NewCircuitBreaker(config...),
	}
}

// Name 返回名称
func (r *RunnableWithCircuitBreaker[I, O]) Name() string {
	return r.runnable.Name() + "_with_circuit_breaker"
}

// Description 返回描述
func (r *RunnableWithCircuitBreaker[I, O]) Description() string {
	return r.runnable.Description()
}

// InputSchema 返回输入 Schema
func (r *RunnableWithCircuitBreaker[I, O]) InputSchema() *Schema {
	return r.runnable.InputSchema()
}

// OutputSchema 返回输出 Schema
func (r *RunnableWithCircuitBreaker[I, O]) OutputSchema() *Schema {
	return r.runnable.OutputSchema()
}

// Invoke 执行（带熔断）
func (r *RunnableWithCircuitBreaker[I, O]) Invoke(ctx context.Context, input I, opts ...Option) (O, error) {
	if !r.breaker.Allow() {
		var zero O
		return zero, ErrCircuitOpen
	}

	result, err := r.runnable.Invoke(ctx, input, opts...)
	if err != nil {
		r.breaker.RecordFailure()
		return result, err
	}

	r.breaker.RecordSuccess()
	return result, nil
}

// Stream 流式执行（带熔断）
func (r *RunnableWithCircuitBreaker[I, O]) Stream(ctx context.Context, input I, opts ...Option) (*StreamReader[O], error) {
	if !r.breaker.Allow() {
		return nil, ErrCircuitOpen
	}

	stream, err := r.runnable.Stream(ctx, input, opts...)
	if err != nil {
		r.breaker.RecordFailure()
		return nil, err
	}

	r.breaker.RecordSuccess()
	return stream, nil
}

// Batch 批量执行
func (r *RunnableWithCircuitBreaker[I, O]) Batch(ctx context.Context, inputs []I, opts ...Option) ([]O, error) {
	if !r.breaker.Allow() {
		return nil, ErrCircuitOpen
	}

	results, err := r.runnable.Batch(ctx, inputs, opts...)
	if err != nil {
		r.breaker.RecordFailure()
		return nil, err
	}

	r.breaker.RecordSuccess()
	return results, nil
}

// Collect 流收集
func (r *RunnableWithCircuitBreaker[I, O]) Collect(ctx context.Context, input *StreamReader[I], opts ...Option) (O, error) {
	return r.runnable.Collect(ctx, input, opts...)
}

// Transform 流转换
func (r *RunnableWithCircuitBreaker[I, O]) Transform(ctx context.Context, input *StreamReader[I], opts ...Option) (*StreamReader[O], error) {
	return r.runnable.Transform(ctx, input, opts...)
}

// BatchStream 批量流式
func (r *RunnableWithCircuitBreaker[I, O]) BatchStream(ctx context.Context, inputs []I, opts ...Option) (*StreamReader[O], error) {
	if !r.breaker.Allow() {
		return nil, ErrCircuitOpen
	}

	stream, err := r.runnable.BatchStream(ctx, inputs, opts...)
	if err != nil {
		r.breaker.RecordFailure()
		return nil, err
	}

	r.breaker.RecordSuccess()
	return stream, nil
}
