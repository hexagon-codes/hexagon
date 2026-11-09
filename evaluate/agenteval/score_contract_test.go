package agenteval

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/hexagon-codes/hexagon/evaluate"
)

func TestQualityBaselineRejectsInvalidScoresAndKeepsReportSerializable(t *testing.T) {
	for _, test := range []struct {
		name     string
		score    float64
		subscore bool
		valid    bool
	}{
		{name: "finite", score: 1, valid: true},
		{name: "nan", score: math.NaN()},
		{name: "positive infinity", score: math.Inf(1)},
		{name: "negative infinity", score: math.Inf(-1)},
		{name: "below range", score: -0.1},
		{name: "above range", score: 1.1},
		{name: "invalid subscore", score: math.NaN(), subscore: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := &evaluate.EvalResult{Score: test.score}
			if test.subscore {
				original.Score = 1
				original.SubScores = map[string]float64{"detail": test.score}
			}
			metric := evaluate.NewCustomEvaluator("quality", "score contract", func(context.Context, evaluate.EvalInput) (*evaluate.EvalResult, error) {
				return &evaluate.EvalResult{Score: original.Score, SubScores: original.SubScores}, nil
			})
			dataset := baselineErrorDataset()
			result, err := QualityBaseline(t.Context(), baselineSuccessAgent(), dataset, 0.9, metric)
			if err != nil {
				t.Fatal(err)
			}
			if result.Passed != test.valid {
				t.Fatalf("baseline passed=%v, want %v: %+v", result.Passed, test.valid, result.Failures)
			}
			if !test.valid && result.Report.FailedSamples != len(dataset.Samples) {
				t.Fatalf("invalid evaluation counted as success: %+v", result.Report)
			}
			if _, err := json.Marshal(result.Report); err != nil {
				t.Fatalf("report is not serializable: %v", err)
			}
			if test.subscore && !math.IsNaN(original.SubScores["detail"]) {
				t.Fatal("normalization mutated the evaluator result")
			}
		})
	}
}

func TestQualityBaselineRejectsNaNThresholdBeforeExecution(t *testing.T) {
	called := false
	metric := evaluate.NewCustomEvaluator("quality", "score contract", func(context.Context, evaluate.EvalInput) (*evaluate.EvalResult, error) {
		called = true
		return &evaluate.EvalResult{Score: 1}, nil
	})
	result, err := QualityBaseline(t.Context(), baselineSuccessAgent(), baselineErrorDataset(), math.NaN(), metric)
	if err == nil || result != nil || called {
		t.Fatalf("invalid threshold executed evaluation: result=%v error=%v called=%v", result, err, called)
	}
}
