package evaluate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEvaluatorResultSnapshotPreservesPriorFailure(t *testing.T) {
	for _, entry := range []string{"single", "batch", "dataset"} {
		t.Run(entry, func(t *testing.T) {
			passed := false
			shared := &EvalResult{Score: .25, Passed: &passed, Error: "critical failure", Details: map[string]any{"labels": []string{"original"}}, SubScores: map[string]float64{"quality": .25}}
			first := NewCustomEvaluator("critical", "required rule", func(context.Context, EvalInput) (*EvalResult, error) { return shared, nil })
			second := NewCustomEvaluator("ordinary", "ordinary rule", func(context.Context, EvalInput) (*EvalResult, error) {
				passed = true
				shared.Score, shared.Error = 1, ""
				shared.Details["labels"].([]string)[0] = "changed"
				shared.SubScores["quality"] = 1
				return shared, nil
			})
			runner := NewRunner(&EvalConfig{Concurrency: 1}).AddEvaluators(first, second)
			var results map[string]*EvalResult
			var err error
			switch entry {
			case "single":
				results, err = runner.EvaluateSingle(t.Context(), EvalInput{})
			case "batch":
				var batch []map[string]*EvalResult
				batch, err = runner.EvaluateBatch(t.Context(), []EvalInput{{}})
				if err == nil {
					results = batch[0]
				}
			case "dataset":
				var report *EvalReport
				report, err = runner.EvaluateDataset(t.Context(), &Dataset{Name: "one", Samples: []Sample{{ID: "one"}}}, SystemFunc(func(context.Context, string) (*SystemResponse, error) {
					return &SystemResponse{Response: "output"}, nil
				}))
				if err == nil {
					results = report.Results[0].Results
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			critical := results["critical"]
			if critical == nil || critical.Name != "critical" || critical.Passed == nil || *critical.Passed || critical.Error != "critical failure" || critical.Score != .25 || critical.Details["labels"].([]string)[0] != "original" || critical.SubScores["quality"] != .25 {
				t.Fatalf("later evaluator replaced accepted failure: %+v", critical)
			}
		})
	}
}

func TestEvaluatorUncopyableDetailsPreserveErrorFacts(t *testing.T) {
	passed := false
	evaluator := partialResultEvaluator{
		result: &EvalResult{Score: .25, Passed: &passed, Error: "recorded failure", Details: map[string]any{"callback": func() {}}, SubScores: map[string]float64{"quality": .25}},
		err:    errors.New("call failure"),
	}
	results, err := NewRunner(nil).AddEvaluator(evaluator).EvaluateSingle(t.Context(), EvalInput{})
	if err != nil {
		t.Fatal(err)
	}
	result := results["critical"]
	if result.Score != .25 || result.Passed == nil || *result.Passed || result.SubScores["quality"] != .25 || result.Details != nil || !strings.Contains(result.Error, "recorded failure") || !strings.Contains(result.Error, "call failure") || !strings.Contains(result.Error, "cannot be isolated") {
		t.Fatalf("copy failure lost evaluation facts: %+v", result)
	}
}

type partialResultEvaluator struct {
	result *EvalResult
	err    error
}

func (partialResultEvaluator) Name() string        { return "critical" }
func (partialResultEvaluator) Description() string { return "partial failure" }
func (partialResultEvaluator) RequiresLLM() bool   { return false }

func (e partialResultEvaluator) Evaluate(context.Context, EvalInput) (*EvalResult, error) {
	return e.result, e.err
}

// 配置歧义在执行前失败，不通过覆盖早先结果来选出一个成功值。
func TestDuplicateEvaluatorIdentityFailsBeforeExecution(t *testing.T) {
	for _, entry := range []string{"dataset", "single", "batch"} {
		t.Run(entry, func(t *testing.T) {
			calls, systemCalls := 0, 0
			makeEval := func() *CustomEvaluator {
				return NewCustomEvaluator("same", "local rule", func(context.Context, EvalInput) (*EvalResult, error) { calls++; return &EvalResult{Score: 1}, nil })
			}
			runner := NewRunner(&EvalConfig{Concurrency: 1}).AddEvaluators(makeEval(), makeEval())
			var err error
			switch entry {
			case "dataset":
				_, err = runner.EvaluateDataset(t.Context(), &Dataset{Name: "one", Samples: []Sample{{ID: "one"}}}, SystemFunc(func(context.Context, string) (*SystemResponse, error) {
					systemCalls++
					return &SystemResponse{Response: "real result"}, nil
				}))
			case "single":
				_, err = runner.EvaluateSingle(t.Context(), EvalInput{})
			case "batch":
				_, err = runner.EvaluateBatch(t.Context(), []EvalInput{{}, {}})
			}
			if err == nil || !strings.Contains(err.Error(), "duplicate evaluator name") {
				t.Fatalf("duplicate identity accepted: %v", err)
			}
			if calls != 0 || systemCalls != 0 {
				t.Fatalf("work started before validation: evaluator=%d system=%d", calls, systemCalls)
			}
		})
	}
}

type changingNameEvaluator struct{ name string }

func (e *changingNameEvaluator) Name() string      { return e.name }
func (*changingNameEvaluator) Description() string { return "mutable external label" }
func (*changingNameEvaluator) RequiresLLM() bool   { return false }
func (e *changingNameEvaluator) Evaluate(context.Context, EvalInput) (*EvalResult, error) {
	e.name = "later"
	return &EvalResult{Score: 1}, nil
}

// 一个批次中的结果始终归属执行前固定的身份，回调不能把它移动到另一个键。
func TestEvaluatorIdentityIsFrozenForBatch(t *testing.T) {
	runner := NewRunner(&EvalConfig{Concurrency: 1}).AddEvaluator(&changingNameEvaluator{name: "original"})
	results, err := runner.EvaluateBatch(t.Context(), []EvalInput{{}, {}})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if len(result) != 1 || result["original"] == nil {
			t.Fatalf("identity changed during batch: %+v", result)
		}
	}
}
