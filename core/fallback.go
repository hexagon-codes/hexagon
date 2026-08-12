// Package core 提供 Hexagon 框架的核心接口和类型
//
// 本文件实现 WithFallback 机制：
//
//   - Fallback: 降级处理
//
//   - Retry: 重试机制
//
//   - CircuitBreaker: 熔断器
//
//   - RunnableWithFallback: 带降级的 Runnable
//
//   - Resilience4j: 弹性模式
//
//   - Polly: 弹性和瞬态故障处理
package core

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/hexagon-codes/toolkit/util/circuit"
	"github.com/hexagon-codes/toolkit/util/retry"
)

// ============== 错误定义 ==============

var (
	// ErrAllFallbacksFailed 所有降级都失败
	ErrAllFallbacksFailed = errors.New("all fallbacks failed")

	// ErrCircuitOpen 熔断器打开
	ErrCircuitOpen = circuit.ErrCircuitOpen

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
	// MaxRetries 最大重试次数，必须非负
	MaxRetries int

	// InitialDelay 初始延迟，必须非负；零值表示立即重试
	InitialDelay time.Duration

	// MaxDelay 最大延迟，必须非负；零值表示不等待
	MaxDelay time.Duration

	// Multiplier 延迟倍数；零值表示固定使用 InitialDelay
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
//     MaxDelay 封顶；此适配器显式选择 ExponentialBackoff，其第 n 次（一基）延迟为
//     Delay*Multiplier^(n-1) 并由 MaxDelay 封顶，曲线一致。
//     为兼容 toolkit v0.2.6 的既有行为，MaxDelay 为零时使用零固定延迟，
//     Multiplier 为零时使用 InitialDelay 固定延迟；正值组合才使用指数退避。
//     负值等非法配置仍交由 toolkit v0.3.4 的严格校验拒绝。
//     手写循环不使用 Jitter 字段，故此处也不设置抖动，保持无抖动行为。
//   - RetryOn → If：判定为不可重试时直接返回原始错误，语义一致。
//   - OnRetry 计数：手写循环以零基传入（首次重试 attempt==0），toolkit
//     使用一基计数，故只在本适配器回调边界减一。
//   - 最终错误可解包：手写循环重试耗尽直接返回原始 lastErr，调用方可
//     errors.Is(err, 原始错误)。toolkit 当前默认以双 %w 同时保留
//     ErrMaxAttemptsReached 与原始 lastErr，无需额外 Option。
func (r *RunnableWithRetry[I, O]) retryOptions() []retry.Option {
	opts := []retry.Option{
		retry.Attempts(r.config.MaxRetries + 1),
	}
	switch {
	case r.config.InitialDelay < 0 || r.config.MaxDelay < 0 || r.config.Multiplier < 0 ||
		math.IsNaN(r.config.Multiplier) || math.IsInf(r.config.Multiplier, 0):
		// 非法非零值继续下传，由 toolkit 统一返回 ErrInvalidConfig。
		opts = append(opts,
			retry.Delay(r.config.InitialDelay),
			retry.MaxDelay(r.config.MaxDelay),
			retry.Multiplier(r.config.Multiplier),
			retry.DelayType(retry.ExponentialBackoff),
		)
	case r.config.MaxDelay == 0:
		opts = append(opts,
			retry.Delay(0),
			retry.DelayType(retry.FixedDelay),
		)
	case r.config.Multiplier == 0:
		opts = append(opts,
			retry.Delay(r.config.InitialDelay),
			retry.MaxDelay(r.config.MaxDelay),
			retry.DelayType(retry.FixedDelay),
		)
	default:
		opts = append(opts,
			retry.Delay(r.config.InitialDelay),
			retry.MaxDelay(r.config.MaxDelay),
			retry.Multiplier(r.config.Multiplier),
			retry.DelayType(retry.ExponentialBackoff),
		)
	}
	if r.config.RetryOn != nil {
		opts = append(opts, retry.If(r.config.RetryOn))
	}
	if r.config.OnRetry != nil {
		// toolkit 的 n 为一基；Hexagon 的公开合同保持零基。
		opts = append(opts, retry.OnRetry(func(n int, err error) {
			r.config.OnRetry(n-1, err)
		}))
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

const defaultCircuitBreakerHalfOpenMaxRequests = 3

// CircuitState 熔断器状态，直接复用 toolkit 的唯一状态定义。
type CircuitState = circuit.State

const (
	// CircuitClosed 关闭状态（正常）
	CircuitClosed = circuit.StateClosed
	// CircuitOpen 打开状态（熔断）
	CircuitOpen = circuit.StateOpen
	// CircuitHalfOpen 半开状态（尝试恢复）
	CircuitHalfOpen = circuit.StateHalfOpen
)

// CircuitBreakerConfig 熔断器配置
type CircuitBreakerConfig struct {
	// FailureThreshold 失败阈值
	FailureThreshold int

	// SuccessThreshold 成功阈值（半开状态）
	SuccessThreshold int

	// HalfOpenMaxRequests 半开状态允许的最大并发探测数；为零时使用默认值 3。
	HalfOpenMaxRequests int

	// Timeout 熔断超时
	Timeout time.Duration

	// OnStateChange 状态变化回调
	OnStateChange func(from, to CircuitState)
}

// DefaultCircuitBreakerConfig 默认熔断器配置
func DefaultCircuitBreakerConfig() *CircuitBreakerConfig {
	return &CircuitBreakerConfig{
		FailureThreshold:    5,
		SuccessThreshold:    3,
		HalfOpenMaxRequests: defaultCircuitBreakerHalfOpenMaxRequests,
		Timeout:             30 * time.Second,
	}
}

// CircuitBreaker 是 toolkit 熔断器的领域配置适配器，不复制状态机。
type CircuitBreaker struct {
	breaker *circuit.Breaker
}

// NewCircuitBreaker 创建熔断器
func NewCircuitBreaker(config ...*CircuitBreakerConfig) (*CircuitBreaker, error) {
	cfg := *DefaultCircuitBreakerConfig()
	if len(config) > 0 && config[0] != nil {
		cfg = *config[0]
	}
	if cfg.HalfOpenMaxRequests < 0 {
		return nil, errors.New("core: circuit breaker half-open max requests must not be negative")
	}
	if cfg.HalfOpenMaxRequests == 0 {
		cfg.HalfOpenMaxRequests = defaultCircuitBreakerHalfOpenMaxRequests
	}

	opts := []circuit.Option{
		circuit.WithThreshold(cfg.FailureThreshold),
		circuit.WithSuccessThreshold(cfg.SuccessThreshold),
		circuit.WithTimeout(cfg.Timeout),
		circuit.WithHalfOpenMaxRequests(cfg.HalfOpenMaxRequests),
	}
	if cfg.OnStateChange != nil {
		opts = append(opts, circuit.WithOnStateChange(cfg.OnStateChange))
	}

	breaker, err := circuit.New(opts...)
	if err != nil {
		return nil, err
	}
	return &CircuitBreaker{breaker: breaker}, nil
}

// State 获取当前状态
func (cb *CircuitBreaker) State() CircuitState {
	return cb.breaker.State()
}

// Acquire 获取与一次执行严格绑定的许可。
func (cb *CircuitBreaker) Acquire() (*circuit.Permit, error) {
	permit, err := cb.breaker.Acquire()
	if errors.Is(err, circuit.ErrCircuitOpen) || errors.Is(err, circuit.ErrTooManyRequests) {
		return nil, ErrCircuitOpen
	}
	return permit, err
}

// Close 关闭熔断器并释放生命周期资源。
func (cb *CircuitBreaker) Close() {
	cb.breaker.Close()
}

// RunnableWithCircuitBreaker 带熔断器的 Runnable
type RunnableWithCircuitBreaker[I, O any] struct {
	runnable Runnable[I, O]
	breaker  *CircuitBreaker
}

// WithCircuitBreaker 创建带熔断器的 Runnable
func WithCircuitBreaker[I, O any](runnable Runnable[I, O], config ...*CircuitBreakerConfig) (*RunnableWithCircuitBreaker[I, O], error) {
	breaker, err := NewCircuitBreaker(config...)
	if err != nil {
		return nil, err
	}
	return &RunnableWithCircuitBreaker[I, O]{
		runnable: runnable,
		breaker:  breaker,
	}, nil
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
	return executeWithCircuit(r.breaker, func() (O, error) {
		return r.runnable.Invoke(ctx, input, opts...)
	})
}

// Stream 流式执行（带熔断）
func (r *RunnableWithCircuitBreaker[I, O]) Stream(ctx context.Context, input I, opts ...Option) (*StreamReader[O], error) {
	return executeWithCircuit(r.breaker, func() (*StreamReader[O], error) {
		return r.runnable.Stream(ctx, input, opts...)
	})
}

// Batch 批量执行
func (r *RunnableWithCircuitBreaker[I, O]) Batch(ctx context.Context, inputs []I, opts ...Option) ([]O, error) {
	return executeWithCircuit(r.breaker, func() ([]O, error) {
		return r.runnable.Batch(ctx, inputs, opts...)
	})
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
	return executeWithCircuit(r.breaker, func() (*StreamReader[O], error) {
		return r.runnable.BatchStream(ctx, inputs, opts...)
	})
}

func executeWithCircuit[T any](breaker *CircuitBreaker, run func() (T, error)) (result T, resultErr error) {
	permit, err := breaker.Acquire()
	if err != nil {
		var zero T
		if errors.Is(err, circuit.ErrCircuitOpen) || errors.Is(err, circuit.ErrTooManyRequests) {
			return zero, ErrCircuitOpen
		}
		return zero, err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = permit.Complete(errors.New("circuit execution panicked")) //nolint:errcheck // 必须优先传播原始 panic。
			panic(recovered)
		}
		resultErr = errors.Join(resultErr, permit.Complete(resultErr))
	}()
	return run()
}
