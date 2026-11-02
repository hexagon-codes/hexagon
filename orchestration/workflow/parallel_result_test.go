package workflow

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

type parallelStepUnknown struct{ error }

func (parallelStepUnknown) UnsafeToReplay() bool { return true }
func (e parallelStepUnknown) Unwrap() error      { return e.error }

func TestParallelStepPreservesCompleteFailureTree(t *testing.T) {
	ordinary, unknown := errors.New("ordinary"), errors.New("unknown receipt")
	var calls [3]atomic.Int32
	steps := make([]Step, 3)
	for i, id := range []string{"ordinary", "success", "unknown"} {
		steps[i] = NewStep(id, id, func(context.Context, StepInput) (*StepOutput, error) {
			calls[i].Add(1)
			output := &StepOutput{Data: id, Variables: map[string]any{"variable": id}, Metadata: map[string]any{"metadata": id}}
			if i == 0 {
				return output, ordinary
			}
			if i == 2 {
				return output, parallelStepUnknown{unknown}
			}
			return output, nil
		})
	}
	parallel := NewParallelStep("group", "group", steps, WithFailFast(false))
	outer := NewStep("outer", "outer", parallel.Execute, WithStepRetryPolicy(&RetryPolicy{MaxRetries: 1}))
	output, err := outer.Execute(t.Context(), StepInput{})
	var failure *ParallelStepError
	if output == nil || !errors.As(err, &failure) || !errors.Is(err, ordinary) || !errors.Is(err, unknown) || !llm.IsUnsafeToReplay(err) || len(failure.Results) != 3 {
		t.Fatalf("parallel facts lost: %+v %v", output, err)
	}
	if failure.Results[1].Output.Variables["variable"] != "success" || failure.Results[2].Output.Metadata["metadata"] != "unknown" {
		t.Fatal("complete child output was discarded")
	}
	for i := range calls {
		if calls[i].Load() != 1 {
			t.Fatalf("branch %d repeated %d times", i, calls[i].Load())
		}
	}
}

func TestParallelStepKnownFailuresCanRetry(t *testing.T) {
	var calls [2]atomic.Int32
	steps := make([]Step, 2)
	for i, id := range []string{"a", "b"} {
		steps[i] = NewStep(id, id, func(context.Context, StepInput) (*StepOutput, error) {
			if calls[i].Add(1) == 1 {
				return nil, errors.New("ordinary failure")
			}
			return &StepOutput{Data: id}, nil
		})
	}
	parallel := NewParallelStep("group", "group", steps, WithFailFast(false))
	outer := NewStep("outer", "outer", parallel.Execute, WithStepRetryPolicy(&RetryPolicy{MaxRetries: 1}))
	if _, err := outer.Execute(t.Context(), StepInput{}); err != nil || calls[0].Load() != 2 || calls[1].Load() != 2 {
		t.Fatalf("ordinary retry contract changed: %v calls=%d,%d", err, calls[0].Load(), calls[1].Load())
	}
}
