package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/runtime/middleware"
	"github.com/hexagon-codes/hexagon/security/cost"
)

// budgetMockProvider 每次 Complete 都返回一个 tool call（驱动状态机进入下一轮）+ 固定高用量，
// 用于验证 Budget 中间件在累计用量/成本突破上限时于下一轮 BeforeLLM fail-closed。
type budgetMockProvider struct{ calls int }

func (p *budgetMockProvider) Name() string            { return "budget-mock" }
func (p *budgetMockProvider) Models() []llm.ModelInfo { return nil }
func (p *budgetMockProvider) CountTokens([]llm.Message) (int, error) {
	return 0, nil
}
func (p *budgetMockProvider) Complete(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.calls++
	return &llm.CompletionResponse{
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "noop", Arguments: "{}"}},
		Usage:     llm.Usage{PromptTokens: 1000, CompletionTokens: 1000, TotalTokens: 2000},
	}, nil
}
func (p *budgetMockProvider) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, nil
}

// TestReActAgentBudgetCostDimFailClosed 验证闭环：cost.Controller.BudgetCostFunc() →
// middleware.Budget（成本维）→ 经 WithMiddleware 注入 ReActAgent 的 runtime →
// 累计成本突破上限时在下一轮 BeforeLLM fail-closed 中断执行。
func TestReActAgentBudgetCostDimFailClosed(t *testing.T) {
	provider := &budgetMockProvider{}
	ctrl := cost.NewController(cost.WithBudget(100.0)) // 默认定价表 default=0.001/0.002 per 1k

	a := NewReAct(
		WithLLM(provider),
		WithMaxIterations(5),
		WithMiddleware(middleware.Budget{
			// model 解析为 ""（agent StaticProviderSelector 未设 Model）→ 用 default 定价：
			// 第 0 轮后累计 cost = 1000/1000*0.001 + 1000/1000*0.002 = 0.003 USD，>= 0.001 上限。
			Limits: middleware.BudgetLimits{MaxCostUSD: 0.001},
			Cost:   ctrl.BudgetCostFunc(),
		}),
	)

	_, err := a.Run(context.Background(), Input{Query: "hi"})
	if err == nil {
		t.Fatal("预期成本维 fail-closed 报错，实际 nil")
	}
	if !strings.Contains(err.Error(), "budget exceeded") || !strings.Contains(err.Error(), "cost_usd") {
		t.Fatalf("错误未体现成本维超限: %v", err)
	}
	// 第 0 轮调用一次后于第 1 轮 BeforeLLM 即中断，不应有第 2 次 Provider 调用。
	if provider.calls != 1 {
		t.Fatalf("Provider 调用次数 = %d，want 1（第 1 轮 BeforeLLM 应先 fail-closed）", provider.calls)
	}
}

// TestReActAgentBudgetTokenDimFailClosed 验证 token 维：无需 cost.Controller，
// 仅靠 middleware.Budget 的 token 维即可经 WithMiddleware 在 ReActAgent 上 fail-closed
// （证明中间件扩展点已正确接线，且 Agent 不强依赖 security/cost）。
func TestReActAgentBudgetTokenDimFailClosed(t *testing.T) {
	provider := &budgetMockProvider{}

	a := NewReAct(
		WithLLM(provider),
		WithMaxIterations(5),
		WithMiddleware(middleware.Budget{
			Limits: middleware.BudgetLimits{MaxTokens: 500}, // 第 0 轮后累计 2000 >= 500
		}),
	)

	_, err := a.Run(context.Background(), Input{Query: "hi"})
	if err == nil {
		t.Fatal("预期 token 维 fail-closed 报错，实际 nil")
	}
	if !strings.Contains(err.Error(), "budget exceeded") || !strings.Contains(err.Error(), "tokens") {
		t.Fatalf("错误未体现 token 维超限: %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("Provider 调用次数 = %d，want 1", provider.calls)
	}
}

// TestCostControlCumulativeBudgetAcrossRuns 验证"跨 run 累计预算"闭环：CostControl 经
// 共享 cost.Controller 累加用量，使预算封顶 agent 全部内部 LLM 调用的总量（多 run agent 的
// 全程预算）——这正是 per-run Budget 补不上的语义。
//
// 每次调用成本（default 定价 0.001/0.002 per 1k，Usage{P:10,C:10}）= 0.00003 USD。
// 预算 0.00005：第 1 次 run 累计 0.00003 未超；第 2 次 run 累计 0.00006 > 0.00005 → fail-closed。
// 关键：单次调用成本 0.00003 本身不超预算，只有跨 run 共享累加器才会在第 2 次触发。
func TestCostControlCumulativeBudgetAcrossRuns(t *testing.T) {
	ctrl := cost.NewController(cost.WithBudget(0.00005))
	cc := middleware.CostControl{Record: ctrl.RecordUsageFunc()}
	provider := &contentMockProvider{}
	ctx := context.Background()
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}

	// 第 1 次 run：累计 0.00003，未超预算。
	if _, err := runCompletionWithRuntime(ctx, provider, "run-1", msgs, nil, cc); err != nil {
		t.Fatalf("第 1 次 run 不应超预算: %v", err)
	}
	// 第 2 次 run：累计 0.00006 > 0.00005 → 经共享累加器 fail-closed。
	_, err := runCompletionWithRuntime(ctx, provider, "run-2", msgs, nil, cc)
	if err == nil {
		t.Fatal("第 2 次 run 累计应超预算并 fail-closed，实际 nil")
	}
	if !strings.Contains(err.Error(), "budget exceeded") {
		t.Fatalf("错误未体现累计预算超限: %v", err)
	}
}

// TestReActAgentNoMiddlewareRunsNormally 验证行为保持：不注入任何中间件时（默认），
// 底层 runner 不挂中间件，正常执行（与重构前一致）。
func TestReActAgentNoMiddlewareRunsNormally(t *testing.T) {
	provider := &budgetMockProvider{}
	a := NewReAct(WithLLM(provider), WithMaxIterations(2))

	// 无预算约束 → 跑满 MaxIterations（每轮都返回 tool call，永不终态）→ 应得 max turns 错误，
	// 而非预算错误（证明默认无中间件）。
	_, err := a.Run(context.Background(), Input{Query: "hi"})
	if err != nil && strings.Contains(err.Error(), "budget") {
		t.Fatalf("默认不应有预算中间件，却报预算错误: %v", err)
	}
}
