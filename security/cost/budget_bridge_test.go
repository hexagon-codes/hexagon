package cost

import (
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

// TestBudgetCostFunc 验证 meter→cost→budget 单向数据流的规范桥接：
// State.Usage 经本控制器 EstimateCost 估算成本，供 Budget 强制点消费。
func TestBudgetCostFunc(t *testing.T) {
	c := NewController(WithPricing(map[string]ModelPricing{
		"gpt-4": {PromptPrice: 30, CompletionPrice: 60}, // 每 1k token
	}))
	fn := c.BudgetCostFunc()

	if got := fn(nil); got != 0 {
		t.Errorf("nil state should cost 0, got %v", got)
	}

	st := &hruntime.State{
		Usage:      llm.Usage{PromptTokens: 1000, CompletionTokens: 1000},
		Attributes: map[string]any{"model": "gpt-4"},
	}
	// 1000/1000*30 + 1000/1000*60 = 90
	if got := fn(st); got != 90 {
		t.Errorf("expected cost 90, got %v", got)
	}
}

// TestNewController_PricingIsolatedFromGlobal 钉住不变量：WithPricing 改价只影响
// 本 controller，不得泄漏污染包级 DefaultPricing 或串扰其他 controller。
// （回归：原 NewController 直接引用 DefaultPricing，WithPricing 会改全局。）
func TestNewController_PricingIsolatedFromGlobal(t *testing.T) {
	_ = NewController(WithPricing(map[string]ModelPricing{
		"gpt-4": {PromptPrice: 999, CompletionPrice: 999},
	}))
	c2 := NewController()
	// c2 应看到原始 DefaultPricing 的 gpt-4（~0.09），而非被污染的 999（~1998）。
	if cost := c2.EstimateCost("gpt-4", 1000, 1000); cost > 100 {
		t.Errorf("WithPricing 泄漏到全局 DefaultPricing：gpt-4 cost=%v（应≈0.09）", cost)
	}
}

// TestBudgetCostFunc_DelegatesToEstimate 确认桥接函数忠实委托给 EstimateCost
// （未知 model 走其 default 定价回退），而非自行实现估算逻辑。
func TestBudgetCostFunc_DelegatesToEstimate(t *testing.T) {
	c := NewController()
	fn := c.BudgetCostFunc()
	st := &hruntime.State{
		Usage:      llm.Usage{PromptTokens: 100, CompletionTokens: 100},
		Attributes: map[string]any{"model": "unknown-model"},
	}
	want := c.EstimateCost("unknown-model", 100, 100)
	if got := fn(st); got != want {
		t.Errorf("bridge should delegate to EstimateCost: got %v want %v", got, want)
	}
}

// TestBudgetCostFunc_MissingModelAttr 确认 State 无 model 属性时不 panic（model 取空串）。
func TestBudgetCostFunc_MissingModelAttr(t *testing.T) {
	c := NewController()
	fn := c.BudgetCostFunc()
	st := &hruntime.State{Usage: llm.Usage{PromptTokens: 100, CompletionTokens: 100}}
	if got := fn(st); got != c.EstimateCost("", 100, 100) {
		t.Errorf("missing model attr should use empty model, got %v", got)
	}
}
