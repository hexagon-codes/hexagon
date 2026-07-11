// Package agent 提供 AI Agent 核心接口和实现
package agent

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/hexagon-codes/hexagon/internal/util"
	"github.com/hexagon-codes/toolkit/lang/stringx"
	"github.com/hexagon-codes/toolkit/util/retry"
)

// AgentHandler Agent 处理函数
//
// 接收输入，返回输出和错误
type AgentHandler func(ctx context.Context, input Input) (Output, error)

// AgentMiddleware Agent 中间件函数
//
// 接收下一个处理器，返回一个包装后的处理器
// 中间件可以：
//   - 在调用前后执行逻辑
//   - 修改输入或输出
//   - 拦截请求
//   - 处理错误
type AgentMiddleware func(next AgentHandler) AgentHandler

// MiddlewareChain 中间件链
//
// 管理一组中间件，按顺序执行
// 中间件执行顺序：外层 -> 内层 -> 核心处理 -> 内层 -> 外层
//
// 线程安全：所有方法都是并发安全的
type MiddlewareChain struct {
	mu          sync.RWMutex
	middlewares []AgentMiddleware
}

// NewMiddlewareChain 创建中间件链
//
// 参数：
//   - middlewares: 要添加的中间件列表
//
// 返回：
//   - 新的中间件链实例
//
// 使用示例：
//
//	chain := NewMiddlewareChain(
//	    RecoverMiddleware(),
//	    LoggingMiddleware(),
//	    TimeoutMiddleware(30*time.Second),
//	)
func NewMiddlewareChain(middlewares ...AgentMiddleware) *MiddlewareChain {
	return &MiddlewareChain{
		middlewares: middlewares,
	}
}

// Use 添加中间件到链
//
// 参数：
//   - middlewares: 要添加的中间件
//
// 返回：
//   - 返回自身，支持链式调用
//
// 线程安全：此方法是并发安全的
func (c *MiddlewareChain) Use(middlewares ...AgentMiddleware) *MiddlewareChain {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.middlewares = append(c.middlewares, middlewares...)
	return c
}

// Prepend 在链头部添加中间件
//
// 这些中间件会最先执行（最后返回）
//
// 线程安全：此方法是并发安全的
func (c *MiddlewareChain) Prepend(middlewares ...AgentMiddleware) *MiddlewareChain {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.middlewares = append(middlewares, c.middlewares...)
	return c
}

// Wrap 用中间件链包装处理器
//
// 参数：
//   - handler: 核心处理函数
//
// 返回：
//   - 包装后的处理函数
//
// 使用示例：
//
//	handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
//	    return agent.Run(ctx, input)
//	})
//	output, err := handler(ctx, input)
//
// 线程安全：此方法是并发安全的
func (c *MiddlewareChain) Wrap(handler AgentHandler) AgentHandler {
	c.mu.RLock()
	// 复制中间件切片，避免持有锁
	middlewares := make([]AgentMiddleware, len(c.middlewares))
	copy(middlewares, c.middlewares)
	c.mu.RUnlock()

	// 从后往前包装，这样执行顺序就是从前往后
	wrapped := handler
	for i := len(middlewares) - 1; i >= 0; i-- {
		wrapped = middlewares[i](wrapped)
	}
	return wrapped
}

// WrapAgent 用中间件链包装 Agent
//
// 返回一个带中间件的 AgentHandler
func (c *MiddlewareChain) WrapAgent(agent Agent) AgentHandler {
	return c.Wrap(func(ctx context.Context, input Input) (Output, error) {
		return agent.Run(ctx, input)
	})
}

// Len 返回中间件数量
//
// 线程安全：此方法是并发安全的
func (c *MiddlewareChain) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.middlewares)
}

// ============== 内置中间件 ==============

// RecoverMiddleware panic 恢复中间件
//
// 捕获处理过程中的 panic，转换为错误返回
// 防止单个请求的 panic 导致整个服务崩溃
//
// 使用示例：
//
//	chain.Use(RecoverMiddleware())
func RecoverMiddleware() AgentMiddleware {
	return func(next AgentHandler) AgentHandler {
		return func(ctx context.Context, input Input) (output Output, err error) {
			defer func() {
				if r := recover(); r != nil {
					// 记录堆栈信息
					stack := string(debug.Stack())
					err = fmt.Errorf("panic recovered: %v\n%s", r, stack)
					output = Output{
						Metadata: map[string]any{
							"panic":      true,
							"panic_info": fmt.Sprintf("%v", r),
						},
					}
				}
			}()
			return next(ctx, input)
		}
	}
}

// LoggingMiddleware 日志记录中间件
//
// 记录请求开始、结束、耗时和错误信息
//
// 参数：
//   - logger: 可选的日志记录器，nil 时使用标准库 log
//
// 使用示例：
//
//	chain.Use(LoggingMiddleware(nil))
func LoggingMiddleware(logger *log.Logger) AgentMiddleware {
	if logger == nil {
		logger = log.Default()
	}

	return func(next AgentHandler) AgentHandler {
		return func(ctx context.Context, input Input) (Output, error) {
			start := time.Now()

			// 截断查询用于日志
			query := truncateForLog(input.Query)

			logger.Printf("[Agent] 开始处理: query=%q", query)

			output, err := next(ctx, input)

			duration := time.Since(start)

			if err != nil {
				logger.Printf("[Agent] 处理失败: duration=%v error=%v", duration, err)
			} else {
				contentLen := len(output.Content)
				toolCalls := len(output.ToolCalls)
				logger.Printf("[Agent] 处理完成: duration=%v content_len=%d tool_calls=%d",
					duration, contentLen, toolCalls)
			}

			return output, err
		}
	}
}

// truncateForLog 截断查询用于日志展示（rune-safe，避免 CJK 字节切断乱码 AP-141）。
func truncateForLog(query string) string {
	return stringx.TruncateBytes(query, 100, "...")
}

// MetricsMiddleware 指标采集中间件
//
// 收集 Agent 执行的各种指标
//
// 参数：
//   - collector: 指标收集器
//
// 使用示例：
//
//	collector := NewMetricsCollector()
//	chain.Use(MetricsMiddleware(collector))
func MetricsMiddleware(collector MetricsCollector) AgentMiddleware {
	return func(next AgentHandler) AgentHandler {
		return func(ctx context.Context, input Input) (Output, error) {
			start := time.Now()

			output, err := next(ctx, input)

			duration := time.Since(start)

			// 记录指标
			if collector != nil {
				collector.RecordDuration(duration)
				collector.RecordCall(err == nil)
				if len(output.ToolCalls) > 0 {
					collector.RecordToolCalls(len(output.ToolCalls))
				}
				if output.Usage.TotalTokens > 0 {
					collector.RecordTokens(output.Usage.TotalTokens)
				}
			}

			return output, err
		}
	}
}

// MetricsCollector 指标收集器接口
type MetricsCollector interface {
	// RecordDuration 记录执行时长
	RecordDuration(duration time.Duration)

	// RecordCall 记录调用（成功或失败）
	RecordCall(success bool)

	// RecordToolCalls 记录工具调用次数
	RecordToolCalls(count int)

	// RecordTokens 记录 Token 使用量
	RecordTokens(count int)
}

// TimeoutMiddleware 超时控制中间件
//
// 设置请求处理的最大时间
//
// 参数：
//   - timeout: 超时时间
//
// 使用示例：
//
//	chain.Use(TimeoutMiddleware(30 * time.Second))
func TimeoutMiddleware(timeout time.Duration) AgentMiddleware {
	return func(next AgentHandler) AgentHandler {
		return func(ctx context.Context, input Input) (Output, error) {
			// 如果上下文已有超时且更短，使用原有的
			if deadline, ok := ctx.Deadline(); ok {
				if time.Until(deadline) < timeout {
					return next(ctx, input)
				}
			}

			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			return next(ctx, input)
		}
	}
}

// maxBackoffDuration 最大退避时间，防止退避时间过长
const maxBackoffDuration = 30 * time.Second

// RetryMiddleware 重试中间件
//
// 在失败时自动重试，使用指数退避策略。
// 退避时间 = min(backoff * 2^attempt, maxBackoffDuration)
//
// 参数：
//   - maxRetries: 最大重试次数
//   - backoff: 基础重试间隔
//
// 使用示例：
//
//	chain.Use(RetryMiddleware(3, 1*time.Second))
func RetryMiddleware(maxRetries int, backoff time.Duration) AgentMiddleware {
	return func(next AgentHandler) AgentHandler {
		return func(ctx context.Context, input Input) (Output, error) {
			var output Output
			var lastErr error

			// 复用 toolkit/util/retry 的指数退避与上下文控制，
			// 与 llm/batch、mcp/reconnect、a2a/push 保持一致。
			// 退避等价于原实现：backoff * 2^attempt，封顶 maxBackoffDuration。
			//   - Attempts = maxRetries+1（1 次初始 + maxRetries 次重试）
			//   - Delay = backoff，Multiplier = 2，DelayType = ExponentialBackoff
			//   - MaxDelay = maxBackoffDuration
			err := retry.DoWithContext(ctx, func() error {
				out, err := next(ctx, input)
				if err != nil {
					lastErr = err
					return err
				}
				output = out
				return nil
			},
				retry.Attempts(maxRetries+1),
				retry.Delay(backoff),
				retry.MaxDelay(maxBackoffDuration),
				retry.Multiplier(2),
				retry.DelayType(retry.ExponentialBackoff),
			)
			if err == nil {
				return output, nil
			}

			// 上下文取消时直接返回 ctx.Err()，与原实现一致。
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Output{}, ctxErr
			}

			// 保留原始错误的 %w 包装，确保调用方仍可 errors.Is/As。
			return Output{}, fmt.Errorf("all %d retries failed: %w", maxRetries, lastErr)
		}
	}
}

// TracingMiddleware 追踪中间件
//
// 添加追踪信息到上下文和输出
//
// 参数：
//   - serviceName: 服务名称
//
// 使用示例：
//
//	chain.Use(TracingMiddleware("my-agent"))
func TracingMiddleware(serviceName string) AgentMiddleware {
	return func(next AgentHandler) AgentHandler {
		return func(ctx context.Context, input Input) (Output, error) {
			// 生成或获取追踪 ID
			traceID := getTraceID(ctx)
			if traceID == "" {
				traceID = generateTraceID()
			}

			// 设置到上下文（如果支持）
			ctx = context.WithValue(ctx, traceIDKey, traceID)

			output, err := next(ctx, input)

			// 添加追踪信息到输出
			if output.Metadata == nil {
				output.Metadata = make(map[string]any)
			}
			output.Metadata["trace_id"] = traceID
			output.Metadata["service"] = serviceName

			return output, err
		}
	}
}

// traceIDKey 追踪 ID 的上下文键
type traceIDKeyType struct{}

var traceIDKey = traceIDKeyType{}

// getTraceID 从上下文获取追踪 ID
func getTraceID(ctx context.Context) string {
	if v := ctx.Value(traceIDKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// generateTraceID 生成唯一追踪 ID
//
// 使用 toolkit 提供的 NanoID 实现，确保一致性和安全性。
// 格式: trace-<nanoid>
func generateTraceID() string {
	return util.TraceID()
}

// RateLimitMiddleware 限流中间件
//
// 限制 Agent 的调用频率
//
// 参数：
//   - limiter: 限流器实例
//
// 使用示例：
//
//	limiter := rate.NewLimiter(10, 100) // 10 QPS，突发 100
//	chain.Use(RateLimitMiddleware(limiter))
func RateLimitMiddleware(limiter RateLimiter) AgentMiddleware {
	return func(next AgentHandler) AgentHandler {
		return func(ctx context.Context, input Input) (Output, error) {
			if limiter != nil {
				if err := limiter.Wait(ctx); err != nil {
					return Output{}, fmt.Errorf("rate limit exceeded: %w", err)
				}
			}
			return next(ctx, input)
		}
	}
}

// RateLimiter 限流器接口
type RateLimiter interface {
	// Wait 等待获取令牌
	Wait(ctx context.Context) error
}

// ============== 中间件组合 ==============

// DefaultMiddlewares 返回默认的中间件组合
//
// 包含：
//   - RecoverMiddleware: panic 恢复
//   - LoggingMiddleware: 日志记录
//   - TimeoutMiddleware: 超时控制（默认 60 秒）
//
// 使用示例：
//
//	chain := NewMiddlewareChain(DefaultMiddlewares()...)
func DefaultMiddlewares() []AgentMiddleware {
	return []AgentMiddleware{
		RecoverMiddleware(),
		LoggingMiddleware(nil),
		TimeoutMiddleware(60 * time.Second),
	}
}

// ProductionMiddlewares 返回生产环境推荐的中间件组合
//
// 包含：
//   - RecoverMiddleware: panic 恢复
//   - TracingMiddleware: 追踪
//   - MetricsMiddleware: 指标采集
//   - TimeoutMiddleware: 超时控制
//   - RetryMiddleware: 重试
//
// 参数：
//   - serviceName: 服务名称
//   - collector: 指标收集器
//
// 使用示例：
//
//	chain := NewMiddlewareChain(ProductionMiddlewares("my-service", collector)...)
func ProductionMiddlewares(serviceName string, collector MetricsCollector) []AgentMiddleware {
	return []AgentMiddleware{
		RecoverMiddleware(),
		TracingMiddleware(serviceName),
		MetricsMiddleware(collector),
		TimeoutMiddleware(60 * time.Second),
		RetryMiddleware(3, 1*time.Second),
	}
}
