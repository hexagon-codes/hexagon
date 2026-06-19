package middleware

import (
	"context"

	"github.com/hexagon-codes/ai-core/llm"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

// RecordUsageFunc 把一次 LLM 调用的用量记入**跨 run 共享累加器**，并在累计预算耗尽时
// 返回错误（fail-closed）。
//
// 它以依赖注入方式提供（裸 func 签名），使 runtime/middleware 不必反向依赖 security/cost：
// 累计账的所有权仍在注入方（通常是 security/cost.Controller.RecordUsageFunc()），CostControl
// 只负责在步边界调用它并据其返回值 fail-closed。传入 nil 时 CostControl 退化为 no-op。
type RecordUsageFunc func(model string, usage llm.Usage) error

// CostControl 是"跨 run 累计预算"的 fail-closed 强制点。
//
// 与 Budget 的分工（二者可叠加使用）：
//   - Budget 按**单次 run** 的 State.Usage 检查 token/墙钟/成本；对多 run 的 agent
//     （PlanExecute/Reflection 等，每次内部 LLM 调用是独立 run）退化为"每调用各自封顶"。
//   - CostControl 经注入的 RecordUsageFunc 把每次调用用量写入**跨 run 共享累加器**，
//     故对多 run agent 实现"agent 全程累计封顶"——这正是 Budget per-run 模型补不上的语义。
//
// 工作方式：在 AfterLLM（本次 LLM 调用已返回）把 resp.Usage 记入累加器；若累计成本突破
// 预算，累加器返回错误，本中间件发 BudgetExceeded 事件并 fail-closed 中断本次 run。
// 由于累加器在 agent 的多次 run 间共享，超限会在"累计跨过预算的那一次调用"处触发。
//
// 线程安全：CostControl 自身无可变状态；并发安全性由注入的 RecordUsageFunc（累加器）负责。
type CostControl struct {
	// Record 跨 run 累计记账 + fail-closed 检查函数；nil 时本中间件为 no-op。
	Record RecordUsageFunc
}

// BeforeLLM 无操作（累计记账在 AfterLLM 完成）。
func (CostControl) BeforeLLM(context.Context, *hruntime.State) error { return nil }

// AfterLLM 把本次 LLM 调用的用量记入跨 run 累加器；累计超预算时 fail-closed。
func (c CostControl) AfterLLM(ctx context.Context, state *hruntime.State, resp *llm.CompletionResponse) error {
	if c.Record == nil || resp == nil {
		return nil
	}
	model := ""
	if state != nil {
		model, _ = state.Attributes["model"].(string)
	}
	if err := c.Record(model, resp.Usage); err != nil {
		_ = state.Emit(ctx, hruntime.Event{
			Type:  hruntime.EventBudgetExceeded,
			Error: err,
			Metadata: map[string]any{
				"dimension": "cumulative_cost",
				"model":     model,
			},
		})
		return err
	}
	return nil
}

// BeforeTool 无操作。
func (CostControl) BeforeTool(context.Context, *hruntime.State, llm.ToolCall) error { return nil }

// AfterTool 无操作。
func (CostControl) AfterTool(context.Context, *hruntime.State, llm.ToolCall, hruntime.ToolResult) error {
	return nil
}

// Finalize 无操作。
func (CostControl) Finalize(context.Context, *hruntime.State) error { return nil }
