package cost

import (
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

// TestBudgetCostFunc 验证 meter→cost→budget 单向数据流的规范桥接：
// State.Usage 经本控制器 EstimateCost 估算成本，供 Budget 强制点消费。
func TestBudgetCostFunc(t *testing.T) {
	c := newTestController(t, WithPricing(map[string]ModelPricing{
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
// 本 controller，不得污染私有默认定价真源或串扰其他 controller。
// （回归：原 NewController 直接引用导出 map，WithPricing 会改全局。）
func TestNewController_PricingIsolatedFromGlobal(t *testing.T) {
	_ = newTestController(t, WithPricing(map[string]ModelPricing{
		"gpt-4": {PromptPrice: 999, CompletionPrice: 999},
	}))
	c2 := newTestController(t)
	// c2 应看到原始默认 gpt-4 定价（~0.09），而非被污染的 999（~1998）。
	if cost := c2.EstimateCost("gpt-4", 1000, 1000); cost > 100 {
		t.Errorf("WithPricing leaked into default pricing: gpt-4 cost=%v, want about 0.09", cost)
	}
}

// TestBudgetCostFunc_DelegatesToEstimate 确认桥接函数忠实委托给 EstimateCost
// （未知 model 走其 default 定价回退），而非自行实现估算逻辑。
func TestBudgetCostFunc_DelegatesToEstimate(t *testing.T) {
	c := newTestController(t)
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
	c := newTestController(t)
	fn := c.BudgetCostFunc()
	st := &hruntime.State{Usage: llm.Usage{PromptTokens: 100, CompletionTokens: 100}}
	if got := fn(st); got != c.EstimateCost("", 100, 100) {
		t.Errorf("missing model attr should use empty model, got %v", got)
	}
}

// TestRecordUsageFunc_CanonicalizesOmittedTotal 验证上游 llm.Usage 省略总数时，
// 控制器仍按输入与输出之和记录配额，并按拆分维度计算成本。
func TestRecordUsageFunc_CanonicalizesOmittedTotal(t *testing.T) {
	c := newTestController(t, WithBudget(1))
	record := c.RecordUsageFunc()

	if err := record("default", llm.Usage{PromptTokens: 2, CompletionTokens: 1}); err != nil {
		t.Fatalf("RecordUsageFunc() error = %v, want nil", err)
	}
	if got := c.Stats().UsedTokens; got != 3 {
		t.Errorf("used tokens = %d, want 3", got)
	}
	if c.used <= 0 {
		t.Errorf("used cost = %v, want positive cost", c.used)
	}
}
