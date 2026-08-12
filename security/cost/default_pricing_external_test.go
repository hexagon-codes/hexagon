package cost_test

import (
	"math"
	"testing"

	"github.com/hexagon-codes/hexagon/security/cost"
)

func TestDefaultPricingReturnsIndependentSnapshots(t *testing.T) {
	first := cost.DefaultPricing()
	first["gpt-4"] = cost.ModelPricing{PromptPrice: 999, CompletionPrice: 999}
	first["external-only"] = cost.ModelPricing{PromptPrice: 1, CompletionPrice: 1}
	delete(first, "default")

	second := cost.DefaultPricing()
	if got := second["gpt-4"]; got != (cost.ModelPricing{PromptPrice: 0.03, CompletionPrice: 0.06}) {
		t.Errorf("second snapshot gpt-4 pricing = %+v, want package defaults", got)
	}
	if _, ok := second["external-only"]; ok {
		t.Error("second snapshot contains caller mutation")
	}
	if _, ok := second["default"]; !ok {
		t.Error("second snapshot lost default pricing")
	}

	controller, err := cost.NewController()
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	if got := controller.EstimateCost("gpt-4", 1000, 1000); math.Abs(got-0.09) > 1e-12 {
		t.Errorf("controller cost = %v, want 0.09", got)
	}
}
