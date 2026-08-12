// Package tool 的中间件模块
//
// 提供工具执行中间件，增强工具能力：
//   - 超时控制：限制工具执行时间
//   - 自动重试：失败后指数退避重试
//   - 速率限制：Token Bucket 限流
//   - 中间件链：组合多个中间件
//
// 使用示例：
//
//	wrapped := tool.WithTimeout(myTool, 5*time.Second)
//	wrapped = tool.WithRetry(wrapped, 3, time.Second)
//	result, err := wrapped.Execute(ctx, args)
package tool

import (
	"context"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	aitool "github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/toolkit/util/retry"
)

// ============== 中间件类型 ==============

// Middleware 工具中间件函数
type Middleware func(next aitool.Tool) aitool.Tool

// ============== 超时中间件 ==============

// WithTimeout 为工具添加执行超时限制
func WithTimeout(t aitool.Tool, timeout time.Duration) aitool.Tool {
	return &timeoutTool{inner: t, timeout: timeout}
}

// TimeoutMiddleware 返回超时中间件
func TimeoutMiddleware(timeout time.Duration) Middleware {
	return func(next aitool.Tool) aitool.Tool {
		return WithTimeout(next, timeout)
	}
}

type timeoutTool struct {
	inner   aitool.Tool
	timeout time.Duration
}

func (t *timeoutTool) Name() string                       { return t.inner.Name() }
func (t *timeoutTool) Description() string                { return t.inner.Description() }
func (t *timeoutTool) Schema() *llm.Schema                { return t.inner.Schema() }
func (t *timeoutTool) Validate(args map[string]any) error { return t.inner.Validate(args) }

func (t *timeoutTool) Execute(ctx context.Context, args map[string]any) (aitool.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	return t.inner.Execute(ctx, args)
}

// ============== 重试中间件 ==============

// WithRetry 为工具添加失败重试能力
//
// maxRetries 为最大重试次数（不含首次执行），backoff 为初始退避时间。
// 每次重试退避时间翻倍，上限 30 秒。
func WithRetry(t aitool.Tool, maxRetries int, backoff time.Duration) aitool.Tool {
	return &retryTool{inner: t, maxRetries: maxRetries, backoff: backoff}
}

// RetryMiddleware 返回重试中间件
func RetryMiddleware(maxRetries int, backoff time.Duration) Middleware {
	return func(next aitool.Tool) aitool.Tool {
		return WithRetry(next, maxRetries, backoff)
	}
}

type retryTool struct {
	inner      aitool.Tool
	maxRetries int
	backoff    time.Duration
}

func (t *retryTool) Name() string                       { return t.inner.Name() }
func (t *retryTool) Description() string                { return t.inner.Description() }
func (t *retryTool) Schema() *llm.Schema                { return t.inner.Schema() }
func (t *retryTool) Validate(args map[string]any) error { return t.inner.Validate(args) }

func (t *retryTool) Execute(ctx context.Context, args map[string]any) (aitool.Result, error) {
	var result aitool.Result
	err := retry.DoWithContext(ctx, func() error {
		var execErr error
		result, execErr = t.inner.Execute(ctx, args)
		return execErr
	},
		retry.Attempts(t.maxRetries+1), // maxRetries 不含首次
		retry.Delay(t.backoff),
		retry.MaxDelay(30*time.Second),
		retry.Multiplier(2.0),
		retry.DelayType(retry.ExponentialBackoff),
	)
	return result, err
}

// ============== 速率限制中间件 ==============

// WithRateLimit 为工具添加速率限制
//
// rps 为每秒允许的请求数（基于 Token Bucket 算法）。
func WithRateLimit(t aitool.Tool, rps float64) aitool.Tool {
	return &rateLimitTool{
		inner:    t,
		rps:      rps,
		tokens:   rps,
		maxBurst: rps,
		lastTime: time.Now(),
	}
}

// RateLimitMiddleware 返回速率限制中间件
func RateLimitMiddleware(rps float64) Middleware {
	return func(next aitool.Tool) aitool.Tool {
		return WithRateLimit(next, rps)
	}
}

type rateLimitTool struct {
	inner    aitool.Tool
	rps      float64
	tokens   float64
	maxBurst float64
	lastTime time.Time
	mu       sync.Mutex
}

func (t *rateLimitTool) Name() string                       { return t.inner.Name() }
func (t *rateLimitTool) Description() string                { return t.inner.Description() }
func (t *rateLimitTool) Schema() *llm.Schema                { return t.inner.Schema() }
func (t *rateLimitTool) Validate(args map[string]any) error { return t.inner.Validate(args) }

func (t *rateLimitTool) Execute(ctx context.Context, args map[string]any) (aitool.Result, error) {
	// 等待获取令牌
	if err := t.waitForToken(ctx); err != nil {
		return aitool.Result{}, err
	}
	return t.inner.Execute(ctx, args)
}

// waitForToken 等待获取速率令牌
func (t *rateLimitTool) waitForToken(ctx context.Context) error {
	for {
		t.mu.Lock()
		now := time.Now()
		elapsed := now.Sub(t.lastTime).Seconds()
		t.tokens += elapsed * t.rps
		if t.tokens > t.maxBurst {
			t.tokens = t.maxBurst
		}
		t.lastTime = now

		if t.tokens >= 1 {
			t.tokens--
			t.mu.Unlock()
			return nil
		}

		// 计算等待时间
		waitDuration := time.Duration((1 - t.tokens) / t.rps * float64(time.Second))
		t.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitDuration):
		}
	}
}

// ============== 中间件链 ==============

// WithMiddleware 将多个中间件应用到工具上
//
// 中间件按从左到右的顺序包裹，即第一个中间件在最外层。
func WithMiddleware(t aitool.Tool, mws ...Middleware) aitool.Tool {
	result := t
	// 从右到左应用，使得第一个中间件在最外层
	for i := len(mws) - 1; i >= 0; i-- {
		result = mws[i](result)
	}
	return result
}
