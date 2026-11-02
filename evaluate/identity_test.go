package evaluate

import (
	"context"
	"strings"
	"testing"
)

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
