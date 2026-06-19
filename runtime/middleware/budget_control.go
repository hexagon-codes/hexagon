package middleware

import (
	"context"

	"github.com/hexagon-codes/ai-core/llm"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

// 本文件把"预算"收敛为单一中间件抽象 BudgetControl，免去使用方同时挂 Budget（per-run）
// 与 CostControl（cross-run）两个中间件、并理解二者微妙差异：一次挂载即同时获得
// 「单次 run 的 token/墙钟/成本上限」与「跨 run 累计成本封顶」，各维度按需启用。

// BudgetControlConfig 是统一预算配置。零值字段表示对应维度关闭。
type BudgetControlConfig struct {
	// Limits 单次 run 的 token/墙钟/成本上限（per-run 维度）。
	Limits BudgetLimits
	// Cost 单次 run 的成本估算函数（per-run 成本维度，可选；消费 security/cost）。
	Cost CostFunc
	// Record 跨 run 累计记账 + 超限 fail-closed（cross-run 维度，可选；消费 security/cost）。
	Record RecordUsageFunc
}

// BudgetControl 是预算的单一抽象：内部组合 Budget（per-run）与 CostControl（cross-run），
// 把"预算"用一个中间件表达。各 hook 依次委托两者，任一返回错误即 fail-closed。
type BudgetControl struct {
	budget Budget
	cost   CostControl
}

// NewBudgetControl 用统一配置构造预算控制中间件。
//
// 等价于同时挂 Budget{Limits,Cost} 与 CostControl{Record}，但作为单一中间件挂载：
//
//	agent.WithMiddleware(middleware.NewBudgetControl(cfg))
func NewBudgetControl(cfg BudgetControlConfig) BudgetControl {
	return BudgetControl{
		budget: Budget{Limits: cfg.Limits, Cost: cfg.Cost},
		cost:   CostControl{Record: cfg.Record},
	}
}

// BeforeLLM 依次执行 per-run 上限检查与跨 run 维度的 BeforeLLM。
func (b BudgetControl) BeforeLLM(ctx context.Context, state *hruntime.State) error {
	if err := b.budget.BeforeLLM(ctx, state); err != nil {
		return err
	}
	return b.cost.BeforeLLM(ctx, state)
}

// AfterLLM 依次执行 per-run 维度与跨 run 累计记账/封顶。
func (b BudgetControl) AfterLLM(ctx context.Context, state *hruntime.State, resp *llm.CompletionResponse) error {
	if err := b.budget.AfterLLM(ctx, state, resp); err != nil {
		return err
	}
	return b.cost.AfterLLM(ctx, state, resp)
}

// BeforeTool 依次委托两者。
func (b BudgetControl) BeforeTool(ctx context.Context, state *hruntime.State, call llm.ToolCall) error {
	if err := b.budget.BeforeTool(ctx, state, call); err != nil {
		return err
	}
	return b.cost.BeforeTool(ctx, state, call)
}

// AfterTool 依次委托两者。
func (b BudgetControl) AfterTool(ctx context.Context, state *hruntime.State, call llm.ToolCall, result hruntime.ToolResult) error {
	if err := b.budget.AfterTool(ctx, state, call, result); err != nil {
		return err
	}
	return b.cost.AfterTool(ctx, state, call, result)
}

// Finalize 依次委托两者。
func (b BudgetControl) Finalize(ctx context.Context, state *hruntime.State) error {
	if err := b.budget.Finalize(ctx, state); err != nil {
		return err
	}
	return b.cost.Finalize(ctx, state)
}

// 编译期断言：BudgetControl 满足 runtime.Middleware。
var _ hruntime.Middleware = BudgetControl{}
