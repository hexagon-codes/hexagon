package middleware

import (
	"context"
	"fmt"

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
// 在 AfterModelAttempt 记录每次实际调用的已知用量，包括失败/取消；未知回执不当零。
// 累加器错误由执行核作为记账/预算错误处理，不冒充 Provider 错误或触发模型重做。
//
// 线程安全：CostControl 自身无可变状态；并发安全性由注入的 RecordUsageFunc（累加器）负责。
type CostControl struct {
	// Record 跨 run 累计记账 + fail-closed 检查函数；nil 时本中间件为 no-op。
	Record RecordUsageFunc
	// RecordAttempt 供持久账本按实际 attempt ID 幂等入账，优先于简便 Record。
	// 返回的错误保持原分类；预算耗尽由宿主显式包装 ErrBudgetExceeded。
	RecordAttempt func(context.Context, hruntime.ModelAttempt) error
}

// BeforeLLM 无操作（累计记账在尝试结束时完成）。
func (CostControl) BeforeLLM(context.Context, *hruntime.State) error { return nil }

// AfterLLM 不重复记账，成功结果采用与实际调用用量分开。
func (CostControl) AfterLLM(context.Context, *hruntime.State, *llm.CompletionResponse) error {
	return nil
}

func (c CostControl) AfterModelAttempt(ctx context.Context, _ *hruntime.State, attempt hruntime.ModelAttempt) error {
	if !attempt.ProviderCalled || !attempt.UsageKnown {
		return nil
	}
	if c.RecordAttempt != nil {
		return c.RecordAttempt(ctx, attempt)
	}
	if c.Record == nil {
		return nil
	}
	if err := c.Record(attempt.Model, attempt.Usage); err != nil {
		return fmt.Errorf("%w: %w", hruntime.ErrBudgetExceeded, err)
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
