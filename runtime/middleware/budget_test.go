package middleware

import (
	"context"
	"sync"
	"testing"
	"testing/quick"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

// fixedStart 预置一个 State，其 run 起始时间固定为 start，token 用量为 usedTokens。
// 这样配合"恒返回 start+elapsed 的时钟"可使墙钟维度的耗时确定可控。
func fixedStart(usedTokens int, start time.Time) *hruntime.State {
	return &hruntime.State{
		Usage:      llm.Usage{TotalTokens: usedTokens},
		Attributes: map[string]any{attrBudgetStartedAt: start},
	}
}

// TestBudget_Unlimited 三维全为 0 时永不超限（零配置行为等价于"无预算"）。
func TestBudget_Unlimited(t *testing.T) {
	b := Budget{}
	st := &hruntime.State{Usage: llm.Usage{TotalTokens: 1 << 30}}
	if err := b.BeforeLLM(context.Background(), st); err != nil {
		t.Fatalf("unlimited budget returned error: %v", err)
	}
}

// TestBudget_Dimensions 覆盖 token / 墙钟 / 成本三维的"未超/达限/超限"。
func TestBudget_Dimensions(t *testing.T) {
	start := time.Unix(1000, 0)
	cases := []struct {
		name    string
		limits  BudgetLimits
		used    int
		elapsed time.Duration
		cost    float64
		wantErr bool
	}{
		{"token_under", BudgetLimits{MaxTokens: 100}, 99, 0, 0, false},
		{"token_at", BudgetLimits{MaxTokens: 100}, 100, 0, 0, true},
		{"token_over", BudgetLimits{MaxTokens: 100}, 101, 0, 0, true},
		{"duration_under", BudgetLimits{MaxDuration: time.Second}, 0, 999 * time.Millisecond, 0, false},
		{"duration_at", BudgetLimits{MaxDuration: time.Second}, 0, time.Second, 0, true},
		{"duration_over", BudgetLimits{MaxDuration: time.Second}, 0, 2 * time.Second, 0, true},
		{"cost_under", BudgetLimits{MaxCostUSD: 1.0}, 0, 0, 0.99, false},
		{"cost_at", BudgetLimits{MaxCostUSD: 1.0}, 0, 0, 1.0, true},
		{"cost_over", BudgetLimits{MaxCostUSD: 1.0}, 0, 0, 1.5, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := Budget{
				Limits: tc.limits,
				Cost:   func(*hruntime.State) float64 { return tc.cost },
				Now:    func() time.Time { return start.Add(tc.elapsed) },
			}
			st := fixedStart(tc.used, start)
			err := b.BeforeLLM(context.Background(), st)
			if (err != nil) != tc.wantErr {
				t.Fatalf("BeforeLLM err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// TestBudget_Precedence 三维同时超限时，报告顺序为 token > duration > cost。
func TestBudget_Precedence(t *testing.T) {
	start := time.Unix(1000, 0)
	b := Budget{
		Limits: BudgetLimits{MaxTokens: 10, MaxDuration: time.Second, MaxCostUSD: 1.0},
		Cost:   func(*hruntime.State) float64 { return 100 },
		Now:    func() time.Time { return start.Add(time.Hour) },
	}
	if got := b.breach(1000, time.Hour, 100); got == "" || got[:6] != "tokens" {
		t.Fatalf("precedence: breach=%q, want tokens first", got)
	}
	if got := b.breach(0, time.Hour, 100); got == "" || got[:8] != "duration" {
		t.Fatalf("precedence: breach=%q, want duration when tokens ok", got)
	}
	if got := b.breach(0, 0, 100); got == "" || got[:8] != "cost_usd" {
		t.Fatalf("precedence: breach=%q, want cost when token/duration ok", got)
	}
}

// TestBudget_NilCostFuncDisablesCostDim Cost 为 nil 时成本维度被禁用，不会因成本超限。
func TestBudget_NilCostFuncDisablesCostDim(t *testing.T) {
	b := Budget{Limits: BudgetLimits{MaxCostUSD: 0.0001}}
	st := &hruntime.State{}
	if err := b.BeforeLLM(context.Background(), st); err != nil {
		t.Fatalf("nil CostFunc should disable cost dim, got %v", err)
	}
}

// TestBudget_PerRunDurationIsolation 起始时间按 run 隔离：不同 State 各自计时。
func TestBudget_PerRunDurationIsolation(t *testing.T) {
	b := Budget{Limits: BudgetLimits{MaxDuration: time.Second}}
	st := &hruntime.State{Attributes: map[string]any{}}
	// 首次调用惰性写入起始时间。
	_ = b.BeforeLLM(context.Background(), st)
	if _, ok := st.Attributes[attrBudgetStartedAt].(time.Time); !ok {
		t.Fatal("started_at not recorded into State.Attributes on first call")
	}
	// 另一个 State 不应共享起始时间。
	st2 := &hruntime.State{Attributes: map[string]any{}}
	if _, ok := st2.Attributes[attrBudgetStartedAt]; ok {
		t.Fatal("started_at leaked across States")
	}
}

// TestBudget_Property 不变量交叉校验：BeforeLLM 返回错误 当且仅当 至少一个被设置的维度达到/超过上限。
// 这里用独立推导的 oracle（非复用 breach()）做对照，且保证任意随机输入不 panic。
func TestBudget_Property(t *testing.T) {
	start := time.Unix(0, 0)
	prop := func(maxTokens, used int, maxDurMs, elapsedMs int64, maxCost, cost float64) bool {
		// 归一到非负，避免 quick 生成的负值产生无意义场景。
		maxTokens, used = absInt(maxTokens), absInt(used)
		maxDurMs, elapsedMs = absInt64(maxDurMs)%100000, absInt64(elapsedMs)%100000
		if maxCost < 0 {
			maxCost = -maxCost
		}
		if cost < 0 {
			cost = -cost
		}
		maxDur := time.Duration(maxDurMs) * time.Millisecond
		elapsed := time.Duration(elapsedMs) * time.Millisecond

		b := Budget{
			Limits: BudgetLimits{MaxTokens: maxTokens, MaxDuration: maxDur, MaxCostUSD: maxCost},
			Cost:   func(*hruntime.State) float64 { return cost },
			Now:    func() time.Time { return start.Add(elapsed) },
		}
		st := fixedStart(used, start)
		err := b.BeforeLLM(context.Background(), st)

		// 独立 oracle：任一被设置(>0)的消耗维度达到上限即应超限。
		wantBreach := (maxTokens > 0 && used >= maxTokens) ||
			(maxDur > 0 && elapsed >= maxDur) ||
			(maxCost > 0 && cost >= maxCost)
		return (err != nil) == wantBreach
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 2000}); err != nil {
		t.Fatalf("budget invariant violated: %v", err)
	}
}

// TestBudget_ConcurrentNoSharedState 并发在独立 State 上调用 BeforeLLM，配合 -race 验证无共享可变状态。
func TestBudget_ConcurrentNoSharedState(t *testing.T) {
	b := Budget{Limits: BudgetLimits{MaxTokens: 100, MaxDuration: time.Minute, MaxCostUSD: 10}}
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			st := &hruntime.State{
				Usage:      llm.Usage{TotalTokens: n},
				Attributes: map[string]any{},
			}
			_ = b.BeforeLLM(context.Background(), st)
		}(i)
	}
	wg.Wait()
}

// TestBudget_EmitsExceededEventViaRunner 集成测试：通过 runner+sink 验证成本超限时
// 发出 BudgetExceeded 与 BudgetChecked 事件，且 run 以错误中止（fail-closed）。
//
// 用成本维度而非墙钟：墙钟首轮 elapsed≈0（符合"首次调用不判超时"语义），
// 而注入的常量 CostFunc 可在首次 BeforeLLM 即触发超限，便于在单轮 run 中验证事件与阻断。
func TestBudget_EmitsExceededEventViaRunner(t *testing.T) {
	provider := &testProvider{responses: []*llm.CompletionResponse{{Content: "should not reach"}}}

	runner := hruntime.NewRunner(hruntime.Config{
		ProviderSelector: hruntime.StaticProviderSelector{Provider: provider, Name: "test", Model: "m"},
		Middleware: []hruntime.Middleware{Budget{
			Limits: BudgetLimits{MaxCostUSD: 1.0},
			Cost:   func(*hruntime.State) float64 { return 100 }, // 远超 1.0，首轮即超限
		}},
	})

	events := map[hruntime.EventType]bool{}
	_, err := runner.RunWithSink(context.Background(), hruntime.Request{
		ID:       "run-budget",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}, hruntime.EventSinkFunc(func(_ context.Context, ev hruntime.Event) error {
		events[ev.Type] = true
		return nil
	}))

	if err == nil {
		t.Fatal("expected fail-closed budget error")
	}
	if !events[hruntime.EventBudgetChecked] {
		t.Fatal("BudgetChecked event not emitted")
	}
	if !events[hruntime.EventBudgetExceeded] {
		t.Fatal("BudgetExceeded event not emitted")
	}
	if provider.calls != 0 {
		t.Fatalf("provider called %d times, want 0 (budget should block before LLM)", provider.calls)
	}
}

// TestBudget_NilStateAndNoopHooks 覆盖 nil State 防御分支与接口空实现（无逻辑钩子）。
func TestBudget_NilStateAndNoopHooks(t *testing.T) {
	b := Budget{Limits: BudgetLimits{MaxTokens: 1}}
	// nil State 不应 panic；usedTokens 视为 0，MaxTokens=1 未达限 → 不超限。
	if err := b.BeforeLLM(context.Background(), nil); err != nil {
		t.Fatalf("BeforeLLM(nil) err=%v, want nil", err)
	}
	// 接口空实现一律返回 nil（满足 Middleware 接口）。
	if err := b.AfterLLM(context.Background(), nil, nil); err != nil {
		t.Fatalf("AfterLLM = %v", err)
	}
	if err := b.BeforeTool(context.Background(), nil, llm.ToolCall{}); err != nil {
		t.Fatalf("BeforeTool = %v", err)
	}
	if err := b.AfterTool(context.Background(), nil, llm.ToolCall{}, hruntime.ToolResult{}); err != nil {
		t.Fatalf("AfterTool = %v", err)
	}
	if err := b.Finalize(context.Background(), nil); err != nil {
		t.Fatalf("Finalize = %v", err)
	}
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func absInt64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
