package runtime

import "testing"

func TestUsageAccumulatorPositiveSmallBudgetDoesNotStartExhausted(t *testing.T) {
	for _, budget := range []float64{0.0000001, 0.0000005, 0.000001, 1} {
		var u UsageAccumulator
		if err := u.CheckLimits(0, 0, budget); err != nil {
			t.Fatalf("zero usage at positive budget %g: %v", budget, err)
		}
	}
	var u UsageAccumulator
	u.RecordCost(0.000001)
	if u.CheckLimits(0, 0, 0.0000001) == nil {
		t.Fatal("one micro-dollar should exhaust smaller positive budget")
	}
	if err := u.CheckLimits(0, 0, 0.0000015); err != nil {
		t.Fatalf("one micro-dollar below 1.5 micro budget: %v", err)
	}
}
