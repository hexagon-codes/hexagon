package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestMiddlewareChain 测试中间件链
func TestMiddlewareChain(t *testing.T) {
	t.Run("创建空链", func(t *testing.T) {
		chain := NewMiddlewareChain()
		if chain.Len() != 0 {
			t.Errorf("Len() = %d, want 0", chain.Len())
		}
	})

	t.Run("创建带中间件的链", func(t *testing.T) {
		chain := NewMiddlewareChain(
			RecoverMiddleware(),
			TimeoutMiddleware(time.Second),
		)
		if chain.Len() != 2 {
			t.Errorf("Len() = %d, want 2", chain.Len())
		}
	})

	t.Run("Use 添加中间件", func(t *testing.T) {
		chain := NewMiddlewareChain()
		chain.Use(RecoverMiddleware())
		chain.Use(TimeoutMiddleware(time.Second))

		if chain.Len() != 2 {
			t.Errorf("Len() = %d, want 2", chain.Len())
		}
	})

	t.Run("链式调用", func(t *testing.T) {
		chain := NewMiddlewareChain().
			Use(RecoverMiddleware()).
			Use(TimeoutMiddleware(time.Second))

		if chain.Len() != 2 {
			t.Errorf("Len() = %d, want 2", chain.Len())
		}
	})

	t.Run("Prepend 在头部添加", func(t *testing.T) {
		order := []string{}

		middleware1 := func(next AgentHandler) AgentHandler {
			return func(ctx context.Context, input Input) (Output, error) {
				order = append(order, "1")
				return next(ctx, input)
			}
		}

		middleware2 := func(next AgentHandler) AgentHandler {
			return func(ctx context.Context, input Input) (Output, error) {
				order = append(order, "2")
				return next(ctx, input)
			}
		}

		chain := NewMiddlewareChain(middleware1)
		chain.Prepend(middleware2) // 2 应该在 1 之前执行

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			return Output{Content: "done"}, nil
		})

		handler(context.Background(), Input{})

		if len(order) != 2 || order[0] != "2" || order[1] != "1" {
			t.Errorf("执行顺序不正确: %v", order)
		}
	})
}

// TestMiddlewareChainWrap 测试包装处理器
func TestMiddlewareChainWrap(t *testing.T) {
	t.Run("空链直接执行", func(t *testing.T) {
		chain := NewMiddlewareChain()

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			return Output{Content: input.Query}, nil
		})

		output, err := handler(context.Background(), Input{Query: "test"})
		if err != nil {
			t.Fatalf("执行错误: %v", err)
		}
		if output.Content != "test" {
			t.Errorf("Content = %s, want test", output.Content)
		}
	})

	t.Run("中间件按顺序执行", func(t *testing.T) {
		var order []int

		chain := NewMiddlewareChain(
			func(next AgentHandler) AgentHandler {
				return func(ctx context.Context, input Input) (Output, error) {
					order = append(order, 1)
					output, err := next(ctx, input)
					order = append(order, 1)
					return output, err
				}
			},
			func(next AgentHandler) AgentHandler {
				return func(ctx context.Context, input Input) (Output, error) {
					order = append(order, 2)
					output, err := next(ctx, input)
					order = append(order, 2)
					return output, err
				}
			},
		)

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			order = append(order, 0)
			return Output{}, nil
		})

		handler(context.Background(), Input{})

		// 期望顺序: 1进, 2进, 0处理, 2出, 1出
		expected := []int{1, 2, 0, 2, 1}
		if len(order) != len(expected) {
			t.Errorf("执行次数不正确: %v", order)
			return
		}
		for i, v := range expected {
			if order[i] != v {
				t.Errorf("位置 %d: got %d, want %d", i, order[i], v)
			}
		}
	})
}

// TestRecoverMiddleware 测试 panic 恢复中间件
func TestRecoverMiddleware(t *testing.T) {
	t.Run("正常执行", func(t *testing.T) {
		chain := NewMiddlewareChain(RecoverMiddleware())

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			return Output{Content: "success"}, nil
		})

		output, err := handler(context.Background(), Input{})
		if err != nil {
			t.Errorf("不应返回错误: %v", err)
		}
		if output.Content != "success" {
			t.Errorf("Content = %s, want success", output.Content)
		}
	})

	t.Run("捕获 panic", func(t *testing.T) {
		chain := NewMiddlewareChain(RecoverMiddleware())

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			panic("test panic")
		})

		output, err := handler(context.Background(), Input{})
		if err == nil {
			t.Fatal("应该返回错误")
		}

		if output.Metadata == nil || output.Metadata["panic"] != true {
			t.Error("应该在 metadata 中标记 panic")
		}
	})
}

// TestLoggingMiddleware 测试日志中间件
func TestLoggingMiddleware(t *testing.T) {
	t.Run("正常执行", func(t *testing.T) {
		chain := NewMiddlewareChain(LoggingMiddleware(nil))

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			return Output{Content: "logged"}, nil
		})

		output, err := handler(context.Background(), Input{Query: "test query"})
		if err != nil {
			t.Errorf("不应返回错误: %v", err)
		}
		if output.Content != "logged" {
			t.Errorf("Content = %s, want logged", output.Content)
		}
	})

	t.Run("记录错误", func(t *testing.T) {
		chain := NewMiddlewareChain(LoggingMiddleware(nil))

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			return Output{}, errors.New("test error")
		})

		_, err := handler(context.Background(), Input{Query: "test"})
		if err == nil {
			t.Error("应该返回错误")
		}
	})
}

// TestTimeoutMiddleware 测试超时中间件
func TestTimeoutMiddleware(t *testing.T) {
	t.Run("未超时", func(t *testing.T) {
		chain := NewMiddlewareChain(TimeoutMiddleware(1 * time.Second))

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			return Output{Content: "quick"}, nil
		})

		output, err := handler(context.Background(), Input{})
		if err != nil {
			t.Errorf("不应返回错误: %v", err)
		}
		if output.Content != "quick" {
			t.Errorf("Content = %s, want quick", output.Content)
		}
	})

	t.Run("超时", func(t *testing.T) {
		chain := NewMiddlewareChain(TimeoutMiddleware(50 * time.Millisecond))

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			select {
			case <-ctx.Done():
				return Output{}, ctx.Err()
			case <-time.After(200 * time.Millisecond):
				return Output{Content: "slow"}, nil
			}
		})

		_, err := handler(context.Background(), Input{})
		if err == nil {
			t.Error("应该返回超时错误")
		}
	})

	t.Run("保留更短的超时", func(t *testing.T) {
		// 创建一个更短超时的上下文
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		chain := NewMiddlewareChain(TimeoutMiddleware(1 * time.Second))

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			select {
			case <-ctx.Done():
				return Output{}, ctx.Err()
			case <-time.After(500 * time.Millisecond):
				return Output{Content: "done"}, nil
			}
		})

		_, err := handler(ctx, Input{})
		if err == nil {
			t.Error("应该使用原有的更短超时")
		}
	})
}

// TestRetryMiddleware 测试重试中间件
func TestRetryMiddleware(t *testing.T) {
	t.Run("第一次成功", func(t *testing.T) {
		chain := NewMiddlewareChain(RetryMiddleware(3, 10*time.Millisecond))

		attempts := 0
		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			attempts++
			return Output{Content: "success"}, nil
		})

		output, err := handler(context.Background(), Input{})
		if err != nil {
			t.Errorf("不应返回错误: %v", err)
		}
		if attempts != 1 {
			t.Errorf("attempts = %d, want 1", attempts)
		}
		if output.Content != "success" {
			t.Errorf("Content = %s, want success", output.Content)
		}
	})

	t.Run("重试后成功", func(t *testing.T) {
		chain := NewMiddlewareChain(RetryMiddleware(3, 10*time.Millisecond))

		attempts := 0
		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			attempts++
			if attempts < 3 {
				return Output{}, errors.New("temporary error")
			}
			return Output{Content: "success"}, nil
		})

		output, err := handler(context.Background(), Input{})
		if err != nil {
			t.Errorf("不应返回错误: %v", err)
		}
		if attempts != 3 {
			t.Errorf("attempts = %d, want 3", attempts)
		}
		if output.Content != "success" {
			t.Errorf("Content = %s, want success", output.Content)
		}
	})

	t.Run("所有重试失败", func(t *testing.T) {
		chain := NewMiddlewareChain(RetryMiddleware(2, 10*time.Millisecond))

		attempts := 0
		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			attempts++
			return Output{}, errors.New("persistent error")
		})

		_, err := handler(context.Background(), Input{})
		if err == nil {
			t.Error("应该返回错误")
		}
		// 1 次初始 + 2 次重试 = 3 次
		if attempts != 3 {
			t.Errorf("attempts = %d, want 3", attempts)
		}
	})

	// 等价性测试：复用 toolkit/util/retry 后，最终错误仍以 %w 包装原始错误，
	// 调用方可继续用 errors.Is 检索底层错误。
	t.Run("最终错误保留底层错误链", func(t *testing.T) {
		sentinel := errors.New("sentinel error")
		chain := NewMiddlewareChain(RetryMiddleware(2, 1*time.Millisecond))

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			return Output{}, sentinel
		})

		_, err := handler(context.Background(), Input{})
		if !errors.Is(err, sentinel) {
			t.Errorf("err 应包装 sentinel，errors.Is 失败: %v", err)
		}
	})

	// 等价性测试：退避采用指数策略 backoff*2^attempt，
	// 第一次重试约等待 1 个 backoff，第二次约 2 个 backoff，总计约 3 个 backoff。
	t.Run("指数退避时序", func(t *testing.T) {
		backoff := 20 * time.Millisecond
		chain := NewMiddlewareChain(RetryMiddleware(2, backoff))

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			return Output{}, errors.New("always fail")
		})

		start := time.Now()
		_, _ = handler(context.Background(), Input{})
		elapsed := time.Since(start)

		// 两次退避：backoff(第一次) + 2*backoff(第二次) = 3*backoff
		// 留出调度余量，仅校验下界，确保确实发生了指数退避等待。
		if elapsed < 3*backoff {
			t.Errorf("elapsed = %v, 应不小于 %v（指数退避总时长）", elapsed, 3*backoff)
		}
	})

	// 等价性测试：上下文取消时返回 ctx.Err()，而非重试耗尽错误。
	t.Run("上下文取消返回 ctx 错误", func(t *testing.T) {
		chain := NewMiddlewareChain(RetryMiddleware(5, 50*time.Millisecond))

		ctx, cancel := context.WithCancel(context.Background())
		handler := chain.Wrap(func(c context.Context, input Input) (Output, error) {
			return Output{}, errors.New("fail to trigger backoff")
		})

		// 在第一次退避等待期间取消
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()

		_, err := handler(ctx, Input{})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err 应为 context.Canceled，实际: %v", err)
		}
	})
}

// TestTracingMiddleware 测试追踪中间件
func TestTracingMiddleware(t *testing.T) {
	t.Run("添加追踪信息", func(t *testing.T) {
		chain := NewMiddlewareChain(TracingMiddleware("test-service"))

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			return Output{Content: "traced"}, nil
		})

		output, err := handler(context.Background(), Input{})
		if err != nil {
			t.Errorf("不应返回错误: %v", err)
		}

		if output.Metadata == nil {
			t.Fatal("Metadata 不应为空")
		}

		if output.Metadata["trace_id"] == nil {
			t.Error("应该有 trace_id")
		}

		if output.Metadata["service"] != "test-service" {
			t.Errorf("service = %v, want test-service", output.Metadata["service"])
		}
	})
}

// mockMetricsCollector 模拟指标收集器
type mockMetricsCollector struct {
	durations  []time.Duration
	calls      int
	successes  int
	toolCalls  int
	tokenCount int
}

func (m *mockMetricsCollector) RecordDuration(d time.Duration) {
	m.durations = append(m.durations, d)
}

func (m *mockMetricsCollector) RecordCall(success bool) {
	m.calls++
	if success {
		m.successes++
	}
}

func (m *mockMetricsCollector) RecordToolCalls(count int) {
	m.toolCalls += count
}

func (m *mockMetricsCollector) RecordTokens(count int) {
	m.tokenCount += count
}

// TestMetricsMiddleware 测试指标中间件
func TestMetricsMiddleware(t *testing.T) {
	t.Run("收集成功指标", func(t *testing.T) {
		collector := &mockMetricsCollector{}
		chain := NewMiddlewareChain(MetricsMiddleware(collector))

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			return Output{
				Content:   "success",
				ToolCalls: []ToolCallRecord{{Name: "tool1"}, {Name: "tool2"}},
			}, nil
		})

		handler(context.Background(), Input{})

		if collector.calls != 1 {
			t.Errorf("calls = %d, want 1", collector.calls)
		}
		if collector.successes != 1 {
			t.Errorf("successes = %d, want 1", collector.successes)
		}
		if collector.toolCalls != 2 {
			t.Errorf("toolCalls = %d, want 2", collector.toolCalls)
		}
	})

	t.Run("收集失败指标", func(t *testing.T) {
		collector := &mockMetricsCollector{}
		chain := NewMiddlewareChain(MetricsMiddleware(collector))

		handler := chain.Wrap(func(ctx context.Context, input Input) (Output, error) {
			return Output{}, errors.New("error")
		})

		handler(context.Background(), Input{})

		if collector.calls != 1 {
			t.Errorf("calls = %d, want 1", collector.calls)
		}
		if collector.successes != 0 {
			t.Errorf("successes = %d, want 0", collector.successes)
		}
	})
}

// TestDefaultMiddlewares 测试默认中间件
func TestDefaultMiddlewares(t *testing.T) {
	middlewares := DefaultMiddlewares()
	if len(middlewares) != 3 {
		t.Errorf("默认中间件数量 = %d, want 3", len(middlewares))
	}
}

// TestProductionMiddlewares 测试生产环境中间件
func TestProductionMiddlewares(t *testing.T) {
	collector := &mockMetricsCollector{}
	middlewares := ProductionMiddlewares("test-service", collector)

	if len(middlewares) != 5 {
		t.Errorf("生产环境中间件数量 = %d, want 5", len(middlewares))
	}
}
