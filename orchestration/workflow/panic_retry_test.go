package workflow

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type directPanicStep struct{ *BaseStep }

func (directPanicStep) Execute(context.Context, StepInput) (*StepOutput, error) {
	panic("direct user step panic")
}

func TestWorkflowPanicPersistsFailedStep(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	store := NewMemoryWorkflowStore()
	var later atomic.Int32
	step := directPanicStep{NewStep("panic-step", "panic-step", func(context.Context, StepInput) (*StepOutput, error) { return nil, nil })}
	wf, err := New("panic-flow").Add(step).AddFunc("later", "later", func(context.Context, StepInput) (*StepOutput, error) {
		later.Add(1)
		return nil, nil
	}).Build()
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewExecutor(WithStore(store)).Run(ctx, wf, WorkflowInput{})
	if err == nil || !strings.Contains(err.Error(), "panic-step") || later.Load() != 0 {
		t.Fatalf("panic did not stop the workflow: error=%v later=%d", err, later.Load())
	}
	executions, err := store.ListExecutions(ctx, wf.ID, StatusFailed, 10)
	if err != nil || len(executions) != 1 {
		t.Fatalf("failed execution not persisted: count=%d error=%v", len(executions), err)
	}
	result := executions[0].StepResults["panic-step"]
	if result == nil || result.Status != StatusFailed || result.CompletedAt == nil || !strings.Contains(result.Error, "direct user step panic") {
		t.Fatalf("failed step facts missing: %+v", result)
	}
}

func TestStepPanicIsNotRetriedAndParallelKeepsIdentity(t *testing.T) {
	var calls atomic.Int32
	step := NewStep("base-panic", "base-panic", func(context.Context, StepInput) (*StepOutput, error) {
		calls.Add(1)
		panic("base user panic")
	}, WithStepRetryPolicy(&RetryPolicy{MaxRetries: 2}))
	_, err := step.Execute(t.Context(), StepInput{})
	var failure *StepPanicError
	if !errors.As(err, &failure) || failure.StepID != "base-panic" || len(failure.Stack) == 0 || calls.Load() != 1 {
		t.Fatalf("panic failure lost identity or was retried: error=%v calls=%d", err, calls.Load())
	}
	child := directPanicStep{NewStep("parallel-child", "parallel-child", func(context.Context, StepInput) (*StepOutput, error) { return nil, nil })}
	parallel := NewParallelStep("parallel", "parallel", []Step{child})
	_, err = parallel.Execute(t.Context(), StepInput{})
	failure = nil
	if !errors.As(err, &failure) || failure.StepID != "parallel-child" {
		t.Fatalf("parallel panic lost child identity: %v", err)
	}
}

type retryTreeMarker struct {
	unsafe bool
	inner  error
}

func (e retryTreeMarker) Error() string        { return "synthetic unknown result" }
func (e retryTreeMarker) Unwrap() error        { return e.inner }
func (e retryTreeMarker) UnsafeToReplay() bool { return e.unsafe }

func TestWorkflowRetryStopsOnAnyUnsafeBranch(t *testing.T) {
	unsafe := retryTreeMarker{unsafe: true, inner: errors.New("original outcome unknown")}
	for name, failure := range map[string]error{
		"wrapped": retryTreeMarker{inner: unsafe},
		"joined":  errors.Join(retryTreeMarker{}, unsafe),
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			step := NewStep(name, name, func(context.Context, StepInput) (*StepOutput, error) {
				calls++
				return nil, failure
			}, WithStepRetryPolicy(&RetryPolicy{MaxRetries: 1, InitialInterval: time.Millisecond, MaxInterval: time.Millisecond, Multiplier: 1}))
			if _, err := step.Execute(t.Context(), StepInput{}); err == nil || calls != 1 {
				t.Fatalf("unknown outcome was replayed: calls=%d error=%v", calls, err)
			}
		})
	}
}
