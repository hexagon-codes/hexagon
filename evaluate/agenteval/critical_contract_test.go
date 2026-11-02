package agenteval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hexagon-codes/hexagon/agent"
	"github.com/hexagon-codes/hexagon/evaluate"
)

type baselineAgentFunc func(context.Context, agent.Input) (agent.Output, error)

func (f baselineAgentFunc) Run(ctx context.Context, input agent.Input) (agent.Output, error) {
	return f(ctx, input)
}

func baselineErrorDataset() *evaluate.Dataset {
	return &evaluate.Dataset{Name: "synthetic-errors", Samples: []evaluate.Sample{{ID: "failed-case", Query: "failed"}, {ID: "successful-case", Query: "successful"}}}
}
func baselineSuccessAgent() Runnable {
	return baselineAgentFunc(func(context.Context, agent.Input) (agent.Output, error) {
		return agent.Output{Content: "synthetic response"}, nil
	})
}

func TestBaselineErrorsCannotBeAveragedAway(t *testing.T) {
	t.Run("system error", func(t *testing.T) {
		system := baselineAgentFunc(func(_ context.Context, input agent.Input) (agent.Output, error) {
			if input.Query == "failed" {
				return agent.Output{}, errors.New("synthetic system failure")
			}
			return agent.Output{Content: "successful"}, nil
		})
		metric := evaluate.NewCustomEvaluator("quality", "local score", func(context.Context, evaluate.EvalInput) (*evaluate.EvalResult, error) {
			return &evaluate.EvalResult{Score: 1}, nil
		})
		result, err := QualityBaseline(t.Context(), system, baselineErrorDataset(), 0.5, metric)
		if err != nil {
			t.Fatal(err)
		}
		if result.Report.Summary["quality"].Mean != 1 || result.Passed || !strings.Contains(strings.Join(result.Failures, " "), "failed-case") {
			t.Fatalf("system error was hidden by mean: %+v", result)
		}
	})
	t.Run("evaluation error field", func(t *testing.T) {
		passed := true
		metric := evaluate.NewCustomEvaluator("broken-evaluator", "local score", func(context.Context, evaluate.EvalInput) (*evaluate.EvalResult, error) {
			return &evaluate.EvalResult{Score: 1, Passed: &passed, Error: "synthetic invalid evaluation"}, nil
		})
		result, err := QualityBaseline(t.Context(), baselineSuccessAgent(), baselineErrorDataset(), 0.5, metric)
		if err != nil {
			t.Fatal(err)
		}
		if result.Report.Summary["broken-evaluator"].Mean != 1 || result.Passed || !strings.Contains(strings.Join(result.Failures, " "), "broken-evaluator") {
			t.Fatalf("evaluation error field was hidden by mean: %+v", result)
		}
		if result.Report.FailedSamples != 2 || result.Report.SuccessSamples != 0 {
			t.Fatalf("evaluation errors were reported as successful executions: %+v", result.Report)
		}
		if rate := result.Report.Summary["broken-evaluator"].PassRate; rate != nil && *rate != 0 {
			t.Fatalf("evaluation error counted as pass rate: %v", *rate)
		}
	})
	t.Run("returned evaluator error", func(t *testing.T) {
		metric := evaluate.NewCustomEvaluator("quality", "local score", func(_ context.Context, input evaluate.EvalInput) (*evaluate.EvalResult, error) {
			if input.Query == "failed" {
				return nil, errors.New("synthetic evaluator failure")
			}
			return &evaluate.EvalResult{Score: 1}, nil
		})
		result, err := QualityBaseline(t.Context(), baselineSuccessAgent(), baselineErrorDataset(), 0.5, metric)
		if err != nil {
			t.Fatal(err)
		}
		if result.Report.Summary["quality"].Mean != 0.5 || result.Passed {
			t.Fatalf("returned error was hidden by mean: %+v", result)
		}
	})
}

func TestCriticalEvaluatorsUseExplicitPerCaseVerdict(t *testing.T) {
	passed, failed := true, false
	for _, test := range []struct {
		name             string
		verdict          *bool
		wantPass         bool
		wantUndetermined int
		wantFailures     int
	}{
		{name: "explicit true", verdict: &passed, wantPass: true},
		{name: "explicit false", verdict: &failed, wantFailures: 1},
		{name: "undetermined", verdict: nil, wantUndetermined: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			metric := evaluate.NewCustomEvaluator("critical", "local explicit rule", func(_ context.Context, input evaluate.EvalInput) (*evaluate.EvalResult, error) {
				verdict := &passed
				if input.Query == "failed" {
					verdict = test.verdict
				}
				return &evaluate.EvalResult{Score: 1, Passed: verdict, Reason: "synthetic rule verdict"}, nil
			})
			result, err := QualityBaselineWithCriteria(t.Context(), baselineSuccessAgent(), baselineErrorDataset(), BaselineCriteria{MinScore: 0.9, CriticalEvaluators: []string{"critical", "critical"}}, metric)
			if err != nil {
				t.Fatal(err)
			}
			if result.Passed != test.wantPass || len(result.Undetermined) != test.wantUndetermined || len(result.Failures) != test.wantFailures || result.Report.Summary["critical"].Mean != 1 {
				t.Fatalf("critical verdict was replaced by mean: %+v", result)
			}
			if test.wantUndetermined > 0 && !strings.Contains(result.Undetermined[0], "failed-case") {
				t.Fatalf("undetermined case identity missing: %+v", result)
			}
			if test.wantFailures > 0 && !strings.Contains(result.Failures[0], "failed-case") {
				t.Fatalf("failure case identity missing: %+v", result)
			}
		})
	}
}

func TestCriticalEvaluatorMissingResultCannotPass(t *testing.T) {
	metric := evaluate.NewCustomEvaluator("quality", "local score", func(context.Context, evaluate.EvalInput) (*evaluate.EvalResult, error) {
		return &evaluate.EvalResult{Score: 1}, nil
	})
	result, err := QualityBaselineWithCriteria(t.Context(), baselineSuccessAgent(), baselineErrorDataset(), BaselineCriteria{MinScore: 0.9, CriticalEvaluators: []string{"required-rule"}}, metric)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(result.Failures, " ")
	if result.Passed || len(result.Failures) != 2 || !strings.Contains(joined, "failed-case") || !strings.Contains(joined, "successful-case") || !strings.Contains(joined, "required-rule") {
		t.Fatalf("missing critical results were accepted: %+v", result)
	}
}

func TestCriticalEvaluatorEmptyReturnIsRecordedAsError(t *testing.T) {
	passed := true
	metric := evaluate.NewCustomEvaluator("critical", "local score", func(_ context.Context, input evaluate.EvalInput) (*evaluate.EvalResult, error) {
		if input.Query == "failed" {
			return nil, nil
		}
		return &evaluate.EvalResult{Score: 1, Passed: &passed}, nil
	})
	result, err := QualityBaselineWithCriteria(t.Context(), baselineSuccessAgent(), baselineErrorDataset(), BaselineCriteria{MinScore: 0, CriticalEvaluators: []string{"critical"}}, metric)
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || result.Report.FailedSamples != 1 || !strings.Contains(strings.Join(result.Failures, " "), "evaluator returned no result") {
		t.Fatalf("empty critical result was accepted or lost: %+v", result)
	}
}

func TestCriticalEvaluatorNamesAreNotInferred(t *testing.T) {
	failed := false
	metric := evaluate.NewCustomEvaluator("severe-looking-name", "ordinary local score", func(context.Context, evaluate.EvalInput) (*evaluate.EvalResult, error) {
		return &evaluate.EvalResult{Score: 1, Passed: &failed}, nil
	})
	ordinary, err := QualityBaseline(t.Context(), baselineSuccessAgent(), baselineErrorDataset(), 0.9, metric)
	if err != nil {
		t.Fatal(err)
	}
	if !ordinary.Passed {
		t.Fatalf("unconfigured name changed ordinary threshold semantics: %+v", ordinary)
	}
	critical, err := QualityBaselineWithCriteria(t.Context(), baselineSuccessAgent(), baselineErrorDataset(), BaselineCriteria{MinScore: 0.9, CriticalEvaluators: []string{"severe-looking-name"}}, metric)
	if err != nil {
		t.Fatal(err)
	}
	if critical.Passed || len(critical.Failures) != 2 {
		t.Fatalf("explicit critical configuration was ignored: %+v", critical)
	}
}
