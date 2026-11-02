package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWorkflowCallbackFailurePrecedesCompletion(t *testing.T) {
	for _, phase := range []string{"step_hook", "complete_hook", "step_event", "complete_event"} {
		t.Run(phase, func(t *testing.T) {
			store := NewMemoryWorkflowStore()
			hooks := &WorkflowHooks{}
			if phase == "step_hook" {
				hooks.OnStepComplete = func(context.Context, Step, any) error { panic("callback failure") }
			}
			if phase == "complete_hook" {
				hooks.OnComplete = func(context.Context, *Workflow, *WorkflowOutput) error { return errors.New("callback failure") }
			}
			ex := NewExecutor(WithStore(store), WithHooks(hooks))
			ex.OnEvent(func(event *WorkflowEvent) {
				if phase == "step_event" && event.Type == EventStepCompleted || phase == "complete_event" && event.Type == EventWorkflowCompleted {
					panic("callback failure")
				}
			})
			wf := New("callbacks").AddFunc("work", "work", func(context.Context, StepInput) (*StepOutput, error) { return &StepOutput{Data: "accepted"}, nil }).MustBuild()
			id, err := ex.RunAsync(t.Context(), wf, WorkflowInput{})
			if err != nil {
				t.Fatal(err)
			}
			record, err := ex.WaitForCompletion(t.Context(), id, time.Second)
			wantStatus := StatusFailed
			if phase == "complete_event" {
				wantStatus = StatusCompleted
			}
			if err == nil || !strings.Contains(err.Error(), "callback failure") || record.Status != wantStatus {
				t.Fatalf("completion escaped callback failure: %+v %v", record, err)
			}
			persisted, err := store.GetExecution(t.Context(), id)
			if err != nil || persisted.Status != wantStatus || persisted.StepResults["work"].Status != StatusCompleted {
				t.Fatalf("wrong persisted outcome: %+v %v", persisted, err)
			}
		})
	}
}

func TestWorkflowObserversAndHooksCannotMutateAcceptedOutput(t *testing.T) {
	ex := NewExecutor(WithHooks(&WorkflowHooks{OnStepComplete: func(_ context.Context, _ Step, value any) error {
		value.(*StepOutput).Data.(map[string]any)["value"] = "hook"
		return nil
	}}))
	ex.OnEvent(func(event *WorkflowEvent) {
		if event.Type == EventStepCompleted {
			event.Data.(*StepOutput).Data.(map[string]any)["value"] = "observer"
		}
	})
	observed := false
	ex.OnEvent(func(event *WorkflowEvent) {
		if event.Type == EventStepCompleted {
			observed = true
			if event.Data.(*StepOutput).Data.(map[string]any)["value"] != "accepted" {
				t.Error("observer shared another callback payload")
			}
		}
	})
	wf := New("isolated").AddFunc("first", "first", func(context.Context, StepInput) (*StepOutput, error) {
		return &StepOutput{Data: map[string]any{"value": "accepted"}}, nil
	}).AddFunc("next", "next", func(_ context.Context, in StepInput) (*StepOutput, error) { return &StepOutput{Data: in.Data}, nil }).MustBuild()
	out, err := ex.Run(t.Context(), wf, WorkflowInput{})
	if err != nil || !observed || out.Data.(map[string]any)["value"] != "accepted" {
		t.Fatalf("accepted output mutated: %+v %v", out, err)
	}
}

func TestWorkflowExpiredStepPreservesResultWithoutDispatchingNext(t *testing.T) {
	var next int
	ex := NewExecutor()
	wf := New("timeout").Add(NewStep("slow", "slow", func(ctx context.Context, _ StepInput) (*StepOutput, error) {
		<-ctx.Done()
		return &StepOutput{Data: "late"}, nil
	}, WithStepTimeout(time.Millisecond))).AddFunc("next", "next", func(context.Context, StepInput) (*StepOutput, error) { next++; return nil, nil }).MustBuild()
	id, err := ex.RunAsync(t.Context(), wf, WorkflowInput{})
	if err != nil {
		t.Fatal(err)
	}
	record, err := ex.WaitForCompletion(t.Context(), id, time.Second)
	if err != nil || next != 0 || record.Status != StatusFailed || record.StepResults["slow"].Output != "late" || !strings.Contains(record.StepResults["slow"].Error, context.DeadlineExceeded.Error()) {
		t.Fatalf("late result was adopted or discarded: %+v %v next=%d", record, err, next)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	step := NewStep("cancelled", "cancelled", func(context.Context, StepInput) (*StepOutput, error) { called = true; return nil, nil })
	if _, err := step.Execute(ctx, StepInput{}); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancelled step dispatched: %v %v", called, err)
	}
}

func TestWorkflowPausedObserverFailurePersistsFailure(t *testing.T) {
	store := NewMemoryWorkflowStore()
	ex := NewExecutor(WithStore(store))
	ex.OnEvent(func(event *WorkflowEvent) {
		if event.Type == EventWorkflowStarted {
			if err := ex.Pause(t.Context(), event.ExecutionID); err != nil {
				t.Error(err)
			}
		}
		if event.Type == EventWorkflowPaused {
			panic("pause observer failed")
		}
	})
	called := false
	wf := New("pause-observer").AddFunc("work", "work", func(context.Context, StepInput) (*StepOutput, error) { called = true; return nil, nil }).MustBuild()
	id, err := ex.RunAsync(t.Context(), wf, WorkflowInput{})
	if err != nil {
		t.Fatal(err)
	}
	record, err := ex.WaitForCompletion(t.Context(), id, time.Second)
	if err == nil || !strings.Contains(err.Error(), "pause observer failed") || record.Status != StatusFailed || called {
		t.Fatalf("paused observer failed without terminal record: %+v %v called=%v", record, err, called)
	}
	persisted, err := store.GetExecution(t.Context(), id)
	if err != nil || persisted.Status != StatusFailed {
		t.Fatalf("failure not persisted: %+v %v", persisted, err)
	}
}
