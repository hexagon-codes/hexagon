package middleware

import (
	"context"
	"fmt"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

// attrBudgetStartedAt 是 Budget 在 State.Attributes 中记录"本次 run 起始时间"的键。
//
// 之所以把起始时间存进 State.Attributes（而非 Budget 结构体字段），是因为同一个
// Budget 中间件实例可能被多次 run 复用；而 State 由 runner 每次 run 新建，
// 在其中惰性记录起始时间可保证墙钟维度按"单次 run"计量，且对中间件复用安全。
const attrBudgetStartedAt = "runtime.budget.started_at"

// BudgetLimits 定义运行时预算的三个维度上限。任一维度取 0 表示该维度不限。
//
// 维度说明：
//   - MaxTokens:   累计 token 用量上限（消耗型，达到即超限）
//   - MaxDuration: 单次 run 墙钟耗时上限（达到即超限）
//   - MaxCostUSD:  累计成本上限（美元，消耗型，达到即超限）
type BudgetLimits struct {
	// MaxTokens 累计 token 上限，0 表示不限。
	MaxTokens int
	// MaxDuration 单次 run 墙钟耗时上限，0 表示不限。
	MaxDuration time.Duration
	// MaxCostUSD 累计成本上限（美元），0 表示不限。
	MaxCostUSD float64
}

// CostFunc 从当前运行状态估算累计成本（美元）。
//
// 它以依赖注入方式提供，使 runtime/middleware **不必硬依赖** security/cost：
// 成本估算的所有权仍在 security/cost.Controller.EstimateCost，Budget 只是
// 唯一的"强制点"，消费其估算结果。这实现了"预算单一所有权 + 单一强制点"。
// 传入 nil 表示禁用成本维度。
type CostFunc func(state *hruntime.State) float64

// Budget 是运行时预算的**单一 fail-closed 强制点**，统一覆盖 token / 墙钟 / 成本三个维度。
//
// 设计要点：
//   - 单一所有权：成本估算来自注入的 CostFunc（消费 security/cost），Budget 不重复实现计费。
//   - fail-closed：任一维度超限即在 BeforeLLM 返回错误，由中间件链中断本次 LLM 调用。
//   - 零配置兼容：三个维度全为 0 时永不超限，行为与"无预算"一致。
//
// 线程安全：Budget 自身无可变状态（墙钟起始时间存于各自 State），不同 run 可并发使用同一实例。
type Budget struct {
	// Limits 三维上限。
	Limits BudgetLimits
	// Cost 成本估算函数；nil 时禁用成本维度。
	Cost CostFunc
	// Now 可注入的时钟，便于测试；nil 时使用 time.Now。
	Now func() time.Time
}

// clock 返回当前时间，优先使用注入的时钟。
func (b Budget) clock() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// ensureStart 惰性记录并返回本次 run 的起始时间（存于 State.Attributes，按 run 隔离）。
//
// 首次调用时把起始时间设为传入的 now（与当次 BeforeLLM 读取的时钟同源），
// 保证"首次 LLM 调用前 elapsed≈0、不会误判超时"，且全程每次 BeforeLLM 只读一次时钟、
// 避免依赖 Go 表达式求值顺序产生负耗时。
func (b Budget) ensureStart(state *hruntime.State, now time.Time) time.Time {
	if state == nil {
		return now
	}
	if state.Attributes == nil {
		state.Attributes = map[string]any{}
	}
	if v, ok := state.Attributes[attrBudgetStartedAt].(time.Time); ok {
		return v
	}
	state.Attributes[attrBudgetStartedAt] = now
	return now
}

// BeforeLLM 在每次 LLM 调用前检查三维预算：先发 BudgetChecked 事件，
// 若任一维度超限则发 BudgetExceeded 事件并返回 fail-closed 错误。
func (b Budget) BeforeLLM(ctx context.Context, state *hruntime.State) error {
	usedTokens := 0
	if state != nil {
		usedTokens = state.Usage.TotalTokens
	}
	now := b.clock()
	elapsed := now.Sub(b.ensureStart(state, now))
	cost := 0.0
	if b.Cost != nil {
		cost = b.Cost(state)
	}

	meta := map[string]any{
		"used_tokens":   usedTokens,
		"max_tokens":    b.Limits.MaxTokens,
		"elapsed":       elapsed.String(),
		"max_duration":  b.Limits.MaxDuration.String(),
		"used_cost_usd": cost,
		"max_cost_usd":  b.Limits.MaxCostUSD,
	}
	_ = state.Emit(ctx, hruntime.Event{Type: hruntime.EventBudgetChecked, Metadata: meta})

	if reason := b.breach(usedTokens, elapsed, cost); reason != "" {
		err := fmt.Errorf("%w: %s", hruntime.ErrBudgetExceeded, reason)
		_ = state.Emit(ctx, hruntime.Event{
			Type:     hruntime.EventBudgetExceeded,
			Error:    err,
			Metadata: meta,
		})
		return err
	}
	return nil
}

// breach 返回首个被突破维度的原因；无突破返回空串。
//
// 维度优先级（用于报告"哪一维先超"）：token > duration > cost。
// token/cost 为消耗型用 >=（达到上限即视为耗尽）；duration 同样用 >=。
func (b Budget) breach(tokens int, elapsed time.Duration, cost float64) string {
	if b.Limits.MaxTokens > 0 && tokens >= b.Limits.MaxTokens {
		return fmt.Sprintf("tokens=%d max=%d", tokens, b.Limits.MaxTokens)
	}
	if b.Limits.MaxDuration > 0 && elapsed >= b.Limits.MaxDuration {
		return fmt.Sprintf("duration=%s max=%s", elapsed, b.Limits.MaxDuration)
	}
	if b.Limits.MaxCostUSD > 0 && cost >= b.Limits.MaxCostUSD {
		return fmt.Sprintf("cost_usd=%.6f max=%.6f", cost, b.Limits.MaxCostUSD)
	}
	return ""
}

// AfterLLM 无操作（预算检查在 BeforeLLM 完成）。
func (Budget) AfterLLM(context.Context, *hruntime.State, *llm.CompletionResponse) error {
	return nil
}

// BeforeTool 无操作。
func (Budget) BeforeTool(context.Context, *hruntime.State, llm.ToolCall) error { return nil }

// AfterTool 无操作。
func (Budget) AfterTool(context.Context, *hruntime.State, llm.ToolCall, hruntime.ToolResult) error {
	return nil
}

// Finalize 无操作。
func (Budget) Finalize(context.Context, *hruntime.State) error { return nil }
