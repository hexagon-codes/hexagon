package cost

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/hexagon-codes/toolkit/util/rate"
)

func newTestController(t *testing.T, opts ...ControllerOption) *Controller {
	t.Helper()
	controller, err := NewController(opts...)
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	return controller
}

func TestNewController(t *testing.T) {
	c := newTestController(t)
	if c == nil {
		t.Fatal("NewController returned nil")
	}

	// 检查默认值
	if c.requestsPerMinute != 60 {
		t.Errorf("expected requestsPerMinute=60, got %d", c.requestsPerMinute)
	}
	if c.maxTokensPerRequest != 8000 {
		t.Errorf("expected maxTokensPerRequest=8000, got %d", c.maxTokensPerRequest)
	}
	if c.maxTokensTotal != 1000000 {
		t.Errorf("expected maxTokensTotal=1000000, got %d", c.maxTokensTotal)
	}
	if c.rateLimiter == nil {
		t.Error("rateLimiter should be initialized")
	}
}

func TestNewControllerRejectsInvalidRequestsPerMinute(t *testing.T) {
	for _, rpm := range []int{0, -1} {
		t.Run(fmt.Sprintf("rpm_%d", rpm), func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("NewController panicked for invalid requests per minute: %v", recovered)
				}
			}()

			controller, err := NewController(WithRequestsPerMinute(rpm))
			if controller != nil {
				t.Errorf("NewController() controller = %v, want nil", controller)
			}
			if !errors.Is(err, rate.ErrInvalidCapacity) {
				t.Errorf("NewController() error = %v, want %v", err, rate.ErrInvalidCapacity)
			}
			if !errors.Is(err, ErrInvalidControllerConfig) {
				t.Errorf("NewController() error = %v, want ErrInvalidControllerConfig", err)
			}
		})
	}
}

func TestNewController_WithOptions(t *testing.T) {
	c := newTestController(t,
		WithBudget(100.0),
		WithMaxTokensPerRequest(4000),
		WithMaxTokensTotal(500000),
		WithRequestsPerMinute(30),
	)

	if c.budget != 100.0 {
		t.Errorf("expected budget=100.0, got %f", c.budget)
	}
	if c.remaining != 100.0 {
		t.Errorf("expected remaining=100.0, got %f", c.remaining)
	}
	if c.maxTokensPerRequest != 4000 {
		t.Errorf("expected maxTokensPerRequest=4000, got %d", c.maxTokensPerRequest)
	}
	if c.maxTokensTotal != 500000 {
		t.Errorf("expected maxTokensTotal=500000, got %d", c.maxTokensTotal)
	}
	if c.requestsPerMinute != 30 {
		t.Errorf("expected requestsPerMinute=30, got %d", c.requestsPerMinute)
	}
}

func expectControllerConfigError(t *testing.T, opts ...ControllerOption) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Errorf("NewController() panicked: %v", recovered)
		}
	}()

	controller, err := NewController(opts...)
	if controller != nil {
		t.Errorf("NewController() controller = %v, want nil", controller)
	}
	if !errors.Is(err, ErrInvalidControllerConfig) {
		t.Errorf("NewController() error = %v, want ErrInvalidControllerConfig", err)
	}
}

func TestNewControllerRejectsNilOption(t *testing.T) {
	expectControllerConfigError(t, nil)
}

func TestNewControllerRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		option ControllerOption
	}{
		{name: "negative budget", option: WithBudget(-1)},
		{name: "nan budget", option: WithBudget(math.NaN())},
		{name: "positive infinite budget", option: WithBudget(math.Inf(1))},
		{name: "negative infinite budget", option: WithBudget(math.Inf(-1))},
		{name: "negative request token limit", option: WithMaxTokensPerRequest(-1)},
		{name: "negative total token limit", option: WithMaxTokensTotal(-1)},
		{name: "negative prompt price", option: WithPricing(map[string]ModelPricing{"custom": {PromptPrice: -1}})},
		{name: "nan prompt price", option: WithPricing(map[string]ModelPricing{"custom": {PromptPrice: math.NaN()}})},
		{name: "infinite prompt price", option: WithPricing(map[string]ModelPricing{"custom": {PromptPrice: math.Inf(1)}})},
		{name: "negative completion price", option: WithPricing(map[string]ModelPricing{"custom": {CompletionPrice: -1}})},
		{name: "nan completion price", option: WithPricing(map[string]ModelPricing{"custom": {CompletionPrice: math.NaN()}})},
		{name: "infinite completion price", option: WithPricing(map[string]ModelPricing{"custom": {CompletionPrice: math.Inf(1)}})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expectControllerConfigError(t, tt.option)
		})
	}
}

func TestNewControllerAllowsZeroConfiguration(t *testing.T) {
	controller, err := NewController(
		WithBudget(0),
		WithMaxTokensPerRequest(0),
		WithMaxTokensTotal(0),
		WithPricing(map[string]ModelPricing{"free": {}}),
	)
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	if err := controller.CheckRequest(context.Background(), math.MaxInt64); err != nil {
		t.Errorf("CheckRequest() error = %v, want unlimited token limits", err)
	}
	if got := controller.RemainingTokens(); got != math.MaxInt64 {
		t.Errorf("RemainingTokens() = %d, want %d", got, int64(math.MaxInt64))
	}
}

func TestNewControllerDoesNotRetainConfigSnapshot(t *testing.T) {
	var snapshot *controllerConfig
	controller, err := NewController(func(config *controllerConfig) {
		snapshot = config
		config.budget = 10
		config.pricing["snapshot"] = ModelPricing{PromptPrice: 1, CompletionPrice: 2}
	})
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}

	snapshot.budget = 20
	snapshot.pricing["snapshot"] = ModelPricing{PromptPrice: 3, CompletionPrice: 4}
	if controller.budget != 10 {
		t.Errorf("controller budget = %v, want immutable snapshot value 10", controller.budget)
	}
	if got := controller.pricing["snapshot"]; got != (ModelPricing{PromptPrice: 1, CompletionPrice: 2}) {
		t.Errorf("controller pricing = %+v, want immutable snapshot value", got)
	}
}

func TestController_WithPricing(t *testing.T) {
	customPricing := map[string]ModelPricing{
		"custom-model": {PromptPrice: 0.01, CompletionPrice: 0.02},
	}

	c := newTestController(t, WithPricing(customPricing))

	if pricing, ok := c.pricing["custom-model"]; !ok {
		t.Error("custom pricing not added")
	} else {
		if pricing.PromptPrice != 0.01 {
			t.Errorf("expected PromptPrice=0.01, got %f", pricing.PromptPrice)
		}
	}
}

func TestController_CheckRequest(t *testing.T) {
	c := newTestController(t,
		WithMaxTokensPerRequest(1000),
		WithMaxTokensTotal(5000),
		WithRequestsPerMinute(100), // 设置高一点避免速率限制影响测试
	)
	ctx := context.Background()

	// 正常请求
	err := c.CheckRequest(ctx, 500)
	if err != nil {
		t.Fatalf("CheckRequest failed for valid request: %v", err)
	}

	// 超过单次请求限制
	err = c.CheckRequest(ctx, 1500)
	if err == nil {
		t.Error("expected error for exceeding per-request limit")
	}

	// 累计超过总限制
	c.usedTokens = 4800
	err = c.CheckRequest(ctx, 300)
	if err == nil {
		t.Error("expected error for exceeding total limit")
	}
}

func TestController_CheckRequestRejectsUnsafeTokenCounts(t *testing.T) {
	t.Run("negative estimate", func(t *testing.T) {
		c := newTestController(t, WithRequestsPerMinute(100))
		beforeCount := c.rateLimiter.Count()

		if err := c.CheckRequest(context.Background(), -1); err == nil {
			t.Fatal("CheckRequest() error = nil, want invalid token count error")
		}
		if got := c.rateLimiter.Count(); got != beforeCount {
			t.Errorf("rate limiter count = %d, want unchanged %d", got, beforeCount)
		}
	})

	t.Run("projected total overflow", func(t *testing.T) {
		c := newTestController(t, WithRequestsPerMinute(100))
		c.usedTokens = math.MaxInt64
		beforeCount := c.rateLimiter.Count()

		if err := c.CheckRequest(context.Background(), 1); err == nil {
			t.Fatal("CheckRequest() error = nil, want token overflow error")
		}
		if got := c.usedTokens; got != math.MaxInt64 {
			t.Errorf("used tokens = %d, want unchanged %d", got, int64(math.MaxInt64))
		}
		if got := c.rateLimiter.Count(); got != beforeCount {
			t.Errorf("rate limiter count = %d, want unchanged %d", got, beforeCount)
		}
	})
}

func TestController_CheckRequestRejectsInvalidContextWithoutConsumingRate(t *testing.T) {
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name    string
		ctx     context.Context
		wantErr error
	}{
		{name: "nil context", ctx: nil},
		{name: "canceled context", ctx: canceledContext, wantErr: context.Canceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestController(t, WithRequestsPerMinute(100))
			beforeCount := c.rateLimiter.Count()

			err := c.CheckRequest(tt.ctx, 1)
			if err == nil {
				t.Fatal("CheckRequest() error = nil, want context error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("CheckRequest() error = %v, want %v", err, tt.wantErr)
			}
			if got := c.rateLimiter.Count(); got != beforeCount {
				t.Errorf("rate limiter count = %d, want unchanged %d", got, beforeCount)
			}
		})
	}
}

func TestController_CheckRequest_RateLimit(t *testing.T) {
	c := newTestController(t,
		WithRequestsPerMinute(2), // 非常低的限制
	)
	ctx := context.Background()

	// 前两个请求应该成功
	err := c.CheckRequest(ctx, 100)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}

	err = c.CheckRequest(ctx, 100)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}

	// 第三个请求应该被限流
	err = c.CheckRequest(ctx, 100)
	if err == nil {
		t.Error("expected rate limit error")
	}
}

func TestController_RecordUsage(t *testing.T) {
	c := newTestController(t, WithBudget(10.0))

	usage := TokenUsage{
		PromptTokens:     1000,
		CompletionTokens: 500,
		TotalTokens:      1500,
	}

	err := c.RecordUsage("gpt-4", usage)
	if err != nil {
		t.Fatalf("RecordUsage failed: %v", err)
	}

	// 检查 Token 累计
	if c.usedTokens != 1500 {
		t.Errorf("expected usedTokens=1500, got %d", c.usedTokens)
	}

	// 检查成本计算
	// gpt-4: prompt $0.03/1K, completion $0.06/1K
	// cost = 1000/1000 * 0.03 + 500/1000 * 0.06 = 0.03 + 0.03 = 0.06
	expectedCost := 0.06
	if c.used < expectedCost-0.001 || c.used > expectedCost+0.001 {
		t.Errorf("expected used=~%f, got %f", expectedCost, c.used)
	}
}

func TestController_RecordUsage_BudgetExceeded(t *testing.T) {
	var callbackCalled bool
	c := newTestController(t,
		WithBudget(0.01), // 非常低的预算
		OnBudgetExceeded(func(used, budget float64) {
			callbackCalled = true
		}),
	)

	usage := TokenUsage{
		PromptTokens:     10000,
		CompletionTokens: 10000,
		TotalTokens:      20000,
	}

	err := c.RecordUsage("gpt-4", usage)
	if err == nil {
		t.Error("expected budget exceeded error")
	}

	if !callbackCalled {
		t.Error("onBudgetExceeded callback should be called")
	}
}

func TestController_RecordUsage_UnknownModel(t *testing.T) {
	c := newTestController(t)

	usage := TokenUsage{
		PromptTokens:     1000,
		CompletionTokens: 1000,
		TotalTokens:      2000,
	}

	// 未知模型应该使用默认定价
	err := c.RecordUsage("unknown-model", usage)
	if err != nil {
		t.Fatalf("RecordUsage failed: %v", err)
	}

	// 默认定价: prompt $0.001/1K, completion $0.002/1K
	// cost = 1000/1000 * 0.001 + 1000/1000 * 0.002 = 0.003
	expectedCost := 0.003
	if c.used < expectedCost-0.0001 || c.used > expectedCost+0.0001 {
		t.Errorf("expected used=~%f, got %f", expectedCost, c.used)
	}
}

func TestController_RecordUsageRejectsInvalidTokenUsage(t *testing.T) {
	tests := []struct {
		name  string
		usage TokenUsage
	}{
		{
			name:  "negative prompt tokens",
			usage: TokenUsage{PromptTokens: -1, CompletionTokens: 1, TotalTokens: 0},
		},
		{
			name:  "negative completion tokens",
			usage: TokenUsage{PromptTokens: 1, CompletionTokens: -1, TotalTokens: 0},
		},
		{
			name:  "negative total tokens",
			usage: TokenUsage{TotalTokens: -1},
		},
		{
			name: "component sum overflow",
			usage: TokenUsage{
				PromptTokens:     math.MaxInt,
				CompletionTokens: 1,
				TotalTokens:      math.MaxInt,
			},
		},
		{
			name:  "inconsistent total tokens",
			usage: TokenUsage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestController(t, WithBudget(100))
			before := c.Stats()

			if err := c.RecordUsage("default", tt.usage); err == nil {
				t.Fatal("RecordUsage() error = nil, want invalid usage error")
			}
			if got := c.Stats(); got != before {
				t.Errorf("controller stats = %+v, want unchanged %+v", got, before)
			}
		})
	}
}

func TestController_RecordUsageCanonicalizesTokenTotals(t *testing.T) {
	tests := []struct {
		name       string
		usage      TokenUsage
		wantTokens int64
		wantCost   float64
	}{
		{
			name:       "derive omitted total from components",
			usage:      TokenUsage{PromptTokens: 2, CompletionTokens: 1},
			wantTokens: 3,
			wantCost:   0.000004,
		},
		{
			name:       "accept matching explicit total",
			usage:      TokenUsage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3},
			wantTokens: 3,
			wantCost:   0.000004,
		},
		{
			// 缺少拆分维度时保留 aggregate-only 配额计数，成本因无定价维度为零。
			name:       "accept aggregate-only total",
			usage:      TokenUsage{TotalTokens: 1000},
			wantTokens: 1000,
			wantCost:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestController(t, WithBudget(100))

			if err := c.RecordUsage("default", tt.usage); err != nil {
				t.Fatalf("RecordUsage() error = %v, want nil", err)
			}
			if got := c.usedTokens; got != tt.wantTokens {
				t.Errorf("used tokens = %d, want %d", got, tt.wantTokens)
			}
			if math.Abs(c.used-tt.wantCost) > 1e-12 {
				t.Errorf("used cost = %.12f, want %.12f", c.used, tt.wantCost)
			}
		})
	}
}

func TestController_RecordUsageAllowsAllZeroTokenUsage(t *testing.T) {
	c := newTestController(t, WithBudget(100))
	before := c.Stats()

	if err := c.RecordUsage("default", TokenUsage{}); err != nil {
		t.Fatalf("RecordUsage() error = %v, want nil", err)
	}
	if got := c.Stats(); got != before {
		t.Errorf("controller stats = %+v, want unchanged %+v", got, before)
	}
}

func TestController_RecordUsageRejectsTokenLedgerOverflow(t *testing.T) {
	c := newTestController(t)
	c.usedTokens = math.MaxInt64
	before := c.Stats()

	err := c.RecordUsage("default", TokenUsage{PromptTokens: 1, TotalTokens: 1})
	if err == nil {
		t.Fatal("RecordUsage() error = nil, want token ledger overflow error")
	}
	if got := c.Stats(); got != before {
		t.Errorf("controller stats = %+v, want unchanged %+v", got, before)
	}
}

func TestController_RecordUsageEnforcesTokenLimitAtomically(t *testing.T) {
	const workers = 16
	c := newTestController(t,
		WithMaxTokensPerRequest(1),
		WithMaxTokensTotal(1),
		WithRequestsPerMinute(workers+1),
	)

	var checked sync.WaitGroup
	var finished sync.WaitGroup
	checked.Add(workers)
	finished.Add(workers)
	releaseRecords := make(chan struct{})
	precheckResults := make(chan error, workers)
	recordResults := make(chan error, workers)

	for range workers {
		go func() {
			defer finished.Done()
			precheckErr := c.CheckRequest(context.Background(), 1)
			precheckResults <- precheckErr
			checked.Done()
			<-releaseRecords
			if precheckErr == nil {
				recordResults <- c.RecordUsage("default", TokenUsage{TotalTokens: 1})
			}
		}()
	}

	checked.Wait()
	for range workers {
		if err := <-precheckResults; err != nil {
			t.Errorf("CheckRequest() error = %v, want all prechecks to pass", err)
		}
	}
	close(releaseRecords)
	finished.Wait()
	close(recordResults)

	successes := 0
	for err := range recordResults {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Errorf("successful RecordUsage() calls = %d, want 1", successes)
	}
	if got := c.Stats().UsedTokens; got != 1 {
		t.Errorf("used tokens = %d, want 1", got)
	}
}

func TestController_RecordUsageUsesCheckedCostArithmetic(t *testing.T) {
	t.Run("price beyond microdollar int64 range", func(t *testing.T) {
		const price = 1e20
		c := newTestController(t, WithPricing(map[string]ModelPricing{
			"extreme": {PromptPrice: price},
		}))

		if err := c.RecordUsage("extreme", TokenUsage{PromptTokens: 1, TotalTokens: 1}); err != nil {
			t.Fatalf("RecordUsage() error = %v, want nil", err)
		}
		if math.IsNaN(c.used) || math.IsInf(c.used, 0) || c.used <= 0 {
			t.Errorf("used cost = %v, want finite positive cost", c.used)
		}
	})

	t.Run("maximum token count", func(t *testing.T) {
		c := newTestController(t,
			WithMaxTokensTotal(0),
			WithPricing(map[string]ModelPricing{
				"extreme": {PromptPrice: 1},
			}),
		)

		usage := TokenUsage{PromptTokens: math.MaxInt, TotalTokens: math.MaxInt}
		if err := c.RecordUsage("extreme", usage); err != nil {
			t.Fatalf("RecordUsage() error = %v, want nil", err)
		}
		if math.IsNaN(c.used) || math.IsInf(c.used, 0) || c.used <= 0 {
			t.Errorf("used cost = %v, want finite positive cost", c.used)
		}
		if c.usedTokens != int64(math.MaxInt) {
			t.Errorf("used tokens = %d, want %d", c.usedTokens, int64(math.MaxInt))
		}
	})

	t.Run("single usage cost overflow", func(t *testing.T) {
		c := newTestController(t, WithPricing(map[string]ModelPricing{
			"extreme": {PromptPrice: math.MaxFloat64},
		}))
		before := c.Stats()

		err := c.RecordUsage("extreme", TokenUsage{PromptTokens: 2000, TotalTokens: 2000})
		if err == nil {
			t.Fatal("RecordUsage() error = nil, want cost overflow error")
		}
		if got := c.Stats(); got != before {
			t.Errorf("controller stats = %+v, want unchanged %+v", got, before)
		}
	})

	t.Run("accumulated cost overflow", func(t *testing.T) {
		c := newTestController(t, WithPricing(map[string]ModelPricing{
			"extreme": {PromptPrice: math.MaxFloat64},
		}))
		usage := TokenUsage{PromptTokens: 1000, TotalTokens: 1000}
		if err := c.RecordUsage("extreme", usage); err != nil {
			t.Fatalf("first RecordUsage() error = %v, want nil", err)
		}
		before := c.Stats()

		if err := c.RecordUsage("extreme", usage); err == nil {
			t.Fatal("second RecordUsage() error = nil, want accumulated cost overflow error")
		}
		if got := c.Stats(); got != before {
			t.Errorf("controller stats = %+v, want unchanged %+v", got, before)
		}
	})
}

func TestController_Stats(t *testing.T) {
	c := newTestController(t,
		WithBudget(100.0),
		WithMaxTokensTotal(10000),
		WithRequestsPerMinute(60),
	)

	// 记录一些使用量
	c.RecordUsage("gpt-4o-mini", TokenUsage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	})

	stats := c.Stats()

	if stats.Budget != 100.0 {
		t.Errorf("expected budget=100.0, got %f", stats.Budget)
	}
	if stats.UsedTokens != 150 {
		t.Errorf("expected usedTokens=150, got %d", stats.UsedTokens)
	}
	if stats.MaxTokensTotal != 10000 {
		t.Errorf("expected maxTokensTotal=10000, got %d", stats.MaxTokensTotal)
	}
	if stats.RequestsPerMin != 60 {
		t.Errorf("expected requestsPerMin=60, got %d", stats.RequestsPerMin)
	}
}

func TestController_Reset(t *testing.T) {
	c := newTestController(t, WithBudget(100.0))

	// 记录一些使用量
	c.RecordUsage("gpt-4", TokenUsage{TotalTokens: 1000, PromptTokens: 500, CompletionTokens: 500})

	// 重置
	c.Reset()

	if c.used != 0 {
		t.Errorf("expected used=0 after reset, got %f", c.used)
	}
	if c.usedTokens != 0 {
		t.Errorf("expected usedTokens=0 after reset, got %d", c.usedTokens)
	}
	if c.remaining != 100.0 {
		t.Errorf("expected remaining=100.0 after reset, got %f", c.remaining)
	}
}

func TestController_EstimateCost(t *testing.T) {
	c := newTestController(t)

	// GPT-4: prompt $0.03/1K, completion $0.06/1K
	cost := c.EstimateCost("gpt-4", 1000, 1000)
	expected := 0.03 + 0.06 // 0.09
	if cost < expected-0.001 || cost > expected+0.001 {
		t.Errorf("expected cost=~%f, got %f", expected, cost)
	}

	// GPT-4o-mini: prompt $0.00015/1K, completion $0.0006/1K
	cost = c.EstimateCost("gpt-4o-mini", 10000, 5000)
	expected = 10*0.00015 + 5*0.0006 // 0.0015 + 0.003 = 0.0045
	if cost < expected-0.0001 || cost > expected+0.0001 {
		t.Errorf("expected cost=~%f, got %f", expected, cost)
	}
}

func TestController_EstimateCostFailsClosedForUnsafeInputs(t *testing.T) {
	c := newTestController(t, WithPricing(map[string]ModelPricing{
		"extreme": {PromptPrice: math.MaxFloat64},
	}))
	tests := []struct {
		name             string
		model            string
		promptTokens     int
		completionTokens int
	}{
		{name: "negative prompt tokens", model: "default", promptTokens: -1},
		{name: "negative completion tokens", model: "default", completionTokens: -1},
		{name: "cost overflow", model: "extreme", promptTokens: 2000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.EstimateCost(tt.model, tt.promptTokens, tt.completionTokens)
			if !math.IsInf(got, 1) {
				t.Errorf("EstimateCost() = %v, want +Inf", got)
			}
		})
	}
}

func TestController_RemainingBudget(t *testing.T) {
	c := newTestController(t, WithBudget(100.0))

	// 记录一些使用量
	c.RecordUsage("gpt-4o-mini", TokenUsage{
		PromptTokens:     1000,
		CompletionTokens: 1000,
		TotalTokens:      2000,
	})

	remaining := c.RemainingBudget()
	if remaining >= 100.0 {
		t.Error("remaining should be less than initial budget")
	}
	if remaining <= 0 {
		t.Error("remaining should be positive")
	}
}

func TestController_RemainingTokens(t *testing.T) {
	c := newTestController(t, WithMaxTokensTotal(10000))

	// 初始应该是全部
	if c.RemainingTokens() != 10000 {
		t.Errorf("expected remainingTokens=10000, got %d", c.RemainingTokens())
	}

	// 记录一些使用量
	c.usedTokens = 3000

	if c.RemainingTokens() != 7000 {
		t.Errorf("expected remainingTokens=7000, got %d", c.RemainingTokens())
	}
}

func TestController_CanAfford(t *testing.T) {
	c := newTestController(t, WithBudget(1.0))

	// 可以负担
	if !c.CanAfford(0.5) {
		t.Error("should be able to afford 0.5")
	}

	// 不能负担
	if c.CanAfford(2.0) {
		t.Error("should not be able to afford 2.0")
	}

	// 无预算限制
	c2 := newTestController(t) // budget = 0
	if !c2.CanAfford(1000.0) {
		t.Error("should be able to afford anything with no budget limit")
	}
}

func TestController_CanAffordRejectsNonFiniteAndNegativeCosts(t *testing.T) {
	tests := []struct {
		name string
		cost float64
	}{
		{name: "negative", cost: -1},
		{name: "nan", cost: math.NaN()},
		{name: "positive infinity", cost: math.Inf(1)},
		{name: "negative infinity", cost: math.Inf(-1)},
	}

	for _, budget := range []float64{0, 1} {
		c := newTestController(t, WithBudget(budget))
		for _, tt := range tests {
			t.Run(fmt.Sprintf("budget_%g/%s", budget, tt.name), func(t *testing.T) {
				if c.CanAfford(tt.cost) {
					t.Errorf("CanAfford(%v) = true, want false", tt.cost)
				}
			})
		}
	}
}

func TestContextWithController(t *testing.T) {
	c := newTestController(t)
	ctx := context.Background()

	// 添加控制器到 context
	ctx = ContextWithController(ctx, c)

	// 从 context 获取控制器
	got := ControllerFromContext(ctx)
	if got != c {
		t.Error("controller not retrieved correctly from context")
	}

	// 没有控制器的 context
	got = ControllerFromContext(context.Background())
	if got != nil {
		t.Error("expected nil for context without controller")
	}
}

func TestCheckAndRecord(t *testing.T) {
	c := newTestController(t,
		WithBudget(10.0),
		WithRequestsPerMinute(100),
	)
	ctx := ContextWithController(context.Background(), c)

	usage := TokenUsage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	err := CheckAndRecord(ctx, "gpt-4o-mini", usage)
	if err != nil {
		t.Fatalf("CheckAndRecord failed: %v", err)
	}

	// 检查是否记录
	if c.usedTokens != 150 {
		t.Errorf("expected usedTokens=150, got %d", c.usedTokens)
	}
}

func TestCheckAndRecord_NoController(t *testing.T) {
	// 没有控制器的 context 应该跳过检查
	ctx := context.Background()
	usage := TokenUsage{TotalTokens: 1000}

	err := CheckAndRecord(ctx, "gpt-4", usage)
	if err != nil {
		t.Errorf("CheckAndRecord should skip when no controller: %v", err)
	}
}

func TestCallbacks(t *testing.T) {
	var tokenCallbackCalled bool
	var rateCallbackCalled bool

	c := newTestController(t,
		WithMaxTokensTotal(100),
		WithRequestsPerMinute(1),
		OnTokensExceeded(func(used, limit int64) {
			tokenCallbackCalled = true
		}),
		OnRateExceeded(func(requests, limit int) {
			rateCallbackCalled = true
		}),
	)
	ctx := context.Background()

	// 触发速率限制回调
	c.CheckRequest(ctx, 10) // 第一次
	c.CheckRequest(ctx, 10) // 第二次应该触发限流

	if !rateCallbackCalled {
		t.Error("onRateExceeded callback should be called")
	}

	// 重置以测试 Token 回调
	c.rateLimiter.Reset()
	c.usedTokens = 90

	c.CheckRequest(ctx, 20) // 超过总限制
	if !tokenCallbackCalled {
		t.Error("onTokensExceeded callback should be called")
	}
}

func invokeControllerWithTimeout(t *testing.T, invoke func() error) error {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		result <- invoke()
	}()

	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("controller call timed out; callback may be running under the controller lock")
		return nil
	}
}

func TestControllerCallbacksCanReenterReadAPIs(t *testing.T) {
	t.Run("token callback from request check", func(t *testing.T) {
		var c *Controller
		callbackCalled := false
		c = newTestController(t,
			WithMaxTokensTotal(1),
			OnTokensExceeded(func(_, _ int64) {
				_ = c.Stats()
				_ = c.RemainingTokens()
				callbackCalled = true
			}),
		)
		c.usedTokens = 1

		err := invokeControllerWithTimeout(t, func() error {
			return c.CheckRequest(context.Background(), 1)
		})
		if err == nil {
			t.Fatal("CheckRequest() error = nil, want token limit error")
		}
		if !callbackCalled {
			t.Fatal("token callback was not called")
		}
	})

	t.Run("token callback from usage record", func(t *testing.T) {
		var c *Controller
		callbackCalled := false
		c = newTestController(t,
			WithMaxTokensTotal(1),
			OnTokensExceeded(func(_, _ int64) {
				_ = c.Stats()
				_ = c.RemainingTokens()
				callbackCalled = true
			}),
		)

		err := invokeControllerWithTimeout(t, func() error {
			return c.RecordUsage("default", TokenUsage{TotalTokens: 2})
		})
		if err == nil {
			t.Fatal("RecordUsage() error = nil, want token limit error")
		}
		if !callbackCalled {
			t.Fatal("token callback was not called")
		}
	})

	t.Run("rate callback", func(t *testing.T) {
		var c *Controller
		callbackCalled := false
		c = newTestController(t,
			WithRequestsPerMinute(1),
			OnRateExceeded(func(_, _ int) {
				_ = c.Stats()
				_ = c.RemainingTokens()
				callbackCalled = true
			}),
		)
		if err := c.CheckRequest(context.Background(), 1); err != nil {
			t.Fatalf("first CheckRequest() error = %v, want nil", err)
		}

		err := invokeControllerWithTimeout(t, func() error {
			return c.CheckRequest(context.Background(), 1)
		})
		if err == nil {
			t.Fatal("second CheckRequest() error = nil, want rate limit error")
		}
		if !callbackCalled {
			t.Fatal("rate callback was not called")
		}
	})

	t.Run("budget callback", func(t *testing.T) {
		var c *Controller
		callbackCalled := false
		c = newTestController(t,
			WithBudget(0.000001),
			OnBudgetExceeded(func(_, _ float64) {
				_ = c.Stats()
				_ = c.RemainingBudget()
				callbackCalled = true
			}),
		)

		err := invokeControllerWithTimeout(t, func() error {
			return c.RecordUsage("default", TokenUsage{PromptTokens: 1000, TotalTokens: 1000})
		})
		if err == nil {
			t.Fatal("RecordUsage() error = nil, want budget limit error")
		}
		if !callbackCalled {
			t.Fatal("budget callback was not called")
		}
	})
}

func TestDefaultPricing(t *testing.T) {
	// 验证默认定价表包含预期的模型
	pricing := DefaultPricing()
	expectedModels := []string{
		"gpt-4", "gpt-4-turbo", "gpt-4o", "gpt-4o-mini", "gpt-3.5-turbo",
		"claude-3-opus", "claude-3-sonnet", "claude-3-haiku",
		"deepseek-chat", "deepseek-reasoner",
		"default",
	}

	for _, model := range expectedModels {
		if _, ok := pricing[model]; !ok {
			t.Errorf("expected model %s in DefaultPricing", model)
		}
	}
}

func TestModelPricing(t *testing.T) {
	// 测试定价结构
	pricing := ModelPricing{
		PromptPrice:     0.01,
		CompletionPrice: 0.02,
	}

	if pricing.PromptPrice != 0.01 {
		t.Errorf("expected PromptPrice=0.01, got %f", pricing.PromptPrice)
	}
	if pricing.CompletionPrice != 0.02 {
		t.Errorf("expected CompletionPrice=0.02, got %f", pricing.CompletionPrice)
	}
}

func TestTokenUsage(t *testing.T) {
	usage := TokenUsage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	if usage.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != 50 {
		t.Errorf("expected CompletionTokens=50, got %d", usage.CompletionTokens)
	}
	if usage.TotalTokens != 150 {
		t.Errorf("expected TotalTokens=150, got %d", usage.TotalTokens)
	}
}

func TestControllerStats(t *testing.T) {
	stats := ControllerStats{
		Budget:          100.0,
		Used:            25.0,
		Remaining:       75.0,
		UsedTokens:      5000,
		MaxTokensTotal:  100000,
		RequestsLastMin: 10,
		RequestsPerMin:  60,
	}

	if stats.Budget != 100.0 {
		t.Errorf("expected Budget=100.0, got %f", stats.Budget)
	}
	if stats.Remaining != 75.0 {
		t.Errorf("expected Remaining=75.0, got %f", stats.Remaining)
	}
}

func TestController_ConcurrentAccess(t *testing.T) {
	c := newTestController(t,
		WithBudget(1000.0),
		WithMaxTokensTotal(1000000),
		WithRequestsPerMinute(1000),
	)
	ctx := context.Background()

	// 并发访问测试
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				c.CheckRequest(ctx, 100)
				c.RecordUsage("gpt-4o-mini", TokenUsage{
					PromptTokens:     10,
					CompletionTokens: 10,
					TotalTokens:      20,
				})
				c.Stats()
				c.RemainingBudget()
				c.RemainingTokens()
			}
			done <- true
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent test timeout")
		}
	}
}
