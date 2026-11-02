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

func TestUsageAccumulatorKeepsFractionalCostsAndRefunds(t *testing.T) {
	var u UsageAccumulator
	for i := 0; i < 4; i++ {
		u.RecordCost(.0000004)
	}
	if u.Snapshot().CostMicroUSD != 2 || u.CheckLimits(0, 0, .000001) == nil {
		t.Fatalf("fractional costs lost: %+v", u.Snapshot())
	}
	u.RefundUnused(0, .0000012)
	if u.Snapshot().CostMicroUSD != 0 {
		t.Fatal(u.Snapshot())
	}
	u.RecordCost(.0000004)
	if u.Snapshot().CostMicroUSD != 1 {
		t.Fatal("refund lost remainder")
	}
	u.RefundUnused(0, 1)
	u.RecordCost(.0000004)
	if u.Snapshot().CostMicroUSD != 0 {
		t.Fatal("over-refund left a negative balance")
	}
}
