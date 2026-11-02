package workflow

import (
	"context"
	"encoding/json"
	"errors"
	hr "github.com/hexagon-codes/hexagon/runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecoverPendingKeepsOriginalIdentityAndCompletedOutputs(t *testing.T) {
	store := NewMemoryWorkflowStore()
	registry := NewWorkflowRegistry()
	var firstCalls, secondCalls atomic.Int32
	wf, err := New("recover").WithID("workflow").AddFunc("first", "first", func(context.Context, StepInput) (*StepOutput, error) { firstCalls.Add(1); return nil, nil }).AddFunc("second", "second", func(_ context.Context, input StepInput) (*StepOutput, error) {
		secondCalls.Add(1)
		if input.Data != "original-first-output" || input.PreviousOutputs["first"] != "original-first-output" || input.Variables["source"] != "saved" {
			return nil, errors.New("original progress was not restored")
		}
		return &StepOutput{Data: "finished-original"}, nil
	}).Build()
	if err != nil {
		t.Fatal(err)
	}
	registry.Register(wf)
	original := &WorkflowExecution{ID: "original-execution", WorkflowID: wf.ID, Status: StatusRunning, StartedAt: time.Now().Add(-time.Minute), Input: json.RawMessage(`{"data":"business-value","keep":"field"}`), Context: &ExecutionContext{Variables: map[string]any{"source": "saved"}, CompletedSteps: []string{"first"}, CurrentStepID: "first"}, StepResults: map[string]*StepResult{"first": {StepID: "first", Status: StatusCompleted, Output: "original-first-output", HasOutput: true}}}
	if err := store.SaveExecution(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(WithStore(store))
	count, err := NewExecutionRecovery(store, registry, executor).RecoverPending(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	restored, err := executor.WaitForCompletion(t.Context(), original.ID, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, executions := store.Stats()
	if firstCalls.Load() != 0 || secondCalls.Load() != 1 || restored.ID != original.ID || string(restored.Input) != string(original.Input) || restored.Status != StatusCompleted || executions != 1 {
		t.Fatalf("restored=%+v first=%d second=%d executions=%d", restored, firstCalls.Load(), secondCalls.Load(), executions)
	}
}

type stepReconcileFunc func(context.Context, string, Step, *StepResult) (*StepOutput, bool, error)

func (f stepReconcileFunc) ReconcileStep(ctx context.Context, id string, step Step, result *StepResult) (*StepOutput, bool, error) {
	return f(ctx, id, step, result)
}

func TestWorkflowUnknownStepMustReconcileOriginalOperation(t *testing.T) {
	var externalCalls, reconciliations atomic.Int32
	wf, err := New("operation").WithID("operation-workflow").AddFunc("paid", "paid", func(context.Context, StepInput) (*StepOutput, error) { externalCalls.Add(1); return nil, nil }).AddFunc("after", "after", func(_ context.Context, input StepInput) (*StepOutput, error) {
		if input.Data != "already-paid-result" {
			return nil, errors.New("original result missing")
		}
		return &StepOutput{Data: "done"}, nil
	}).Build()
	if err != nil {
		t.Fatal(err)
	}
	stored := &WorkflowExecution{ID: "original-operation-execution", WorkflowID: wf.ID, Status: StatusRunning, Input: json.RawMessage(`"original-input"`), Context: &ExecutionContext{CurrentStepID: "paid"}, StepResults: map[string]*StepResult{"paid": {StepID: "paid", Status: StatusRunning}}}
	_, err = NewExecutor().RecoverExecution(t.Context(), wf, stored)
	var unknown *StepOutcomeUnknownError
	if !errors.As(err, &unknown) || externalCalls.Load() != 0 || stored.StepResults["paid"].Status != StatusRunning {
		t.Fatalf("unknown side effect replayed: %v calls=%d", err, externalCalls.Load())
	}
	executor := NewExecutor(WithStepReconciler(stepReconcileFunc(func(_ context.Context, id string, step Step, _ *StepResult) (*StepOutput, bool, error) {
		reconciliations.Add(1)
		if id != stored.ID || step.ID() != "paid" {
			t.Error("reconciliation identity changed")
		}
		return &StepOutput{Data: "already-paid-result"}, true, nil
	})))
	id, err := executor.RecoverExecution(t.Context(), wf, stored)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := executor.WaitForCompletion(t.Context(), id, time.Second)
	if err != nil || completed.Status != StatusCompleted || externalCalls.Load() != 0 || reconciliations.Load() != 1 {
		t.Fatalf("completed=%+v err=%v calls=%d queries=%d", completed, err, externalCalls.Load(), reconciliations.Load())
	}
}

func TestWorkflowRecoveryPreservesLegacyRawBusinessData(t *testing.T) {
	wf, err := New("legacy-input").WithID("legacy-workflow").AddFunc("first", "first", func(_ context.Context, input StepInput) (*StepOutput, error) {
		data, ok := input.Data.(map[string]any)
		if !ok || data["data"] != "business-value" || data["keep"] != "field" || data["large"] != json.Number("1000000000000000001") {
			return nil, errors.New("legacy raw data was mistaken for workflow input envelope")
		}
		return &StepOutput{Data: "preserved"}, nil
	}).Build()
	if err != nil {
		t.Fatal(err)
	}
	stored := &WorkflowExecution{ID: "legacy-execution", WorkflowID: wf.ID, Status: StatusRunning, Input: json.RawMessage(`{"data":"business-value","keep":"field","large":1000000000000000001}`), Context: &ExecutionContext{}, StepResults: map[string]*StepResult{}}
	executor := NewExecutor()
	id, err := executor.RecoverExecution(t.Context(), wf, stored)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := executor.WaitForCompletion(t.Context(), id, time.Second)
	if err != nil || completed.Status != StatusCompleted {
		t.Fatalf("legacy input result=%+v error=%v", completed, err)
	}
}

func TestWorkflowUnknownOutcomeIsNotRetriedAndKeepsPartialResult(t *testing.T) {
	calls := 0
	step := NewStep("external", "external", func(context.Context, StepInput) (*StepOutput, error) {
		calls++
		return &StepOutput{Data: "original-receipt-partial"}, hr.ErrOperationUnresolved
	}, WithStepRetryPolicy(&RetryPolicy{MaxRetries: 2}))
	output, err := step.Execute(t.Context(), StepInput{})
	if !errors.Is(err, hr.ErrOperationUnresolved) || calls != 1 || output == nil || output.Data != "original-receipt-partial" {
		t.Fatalf("unknown operation output=%+v error=%v calls=%d", output, err, calls)
	}
}

func TestWorkflowRawObjectOutputRoundTrip(t *testing.T) {
	wf, err := New("object-output").AddFunc("result", "result", func(context.Context, StepInput) (*StepOutput, error) {
		return &StepOutput{Data: map[string]any{"data": "business-value", "kept": "required-field"}}, nil
	}).Build()
	if err != nil {
		t.Fatal(err)
	}
	output, err := NewExecutor().Run(t.Context(), wf, WorkflowInput{})
	if err != nil {
		t.Fatal(err)
	}
	data, ok := output.Data.(map[string]any)
	if !ok || data["data"] != "business-value" || data["kept"] != "required-field" {
		t.Fatalf("raw business output was mistaken for output envelope: %+v", output)
	}
}

type failingWorkflowStore struct {
	*MemoryWorkflowStore
	calls  int
	failAt int
	cause  error
}

func (s *failingWorkflowStore) SaveExecution(ctx context.Context, execution *WorkflowExecution) error {
	s.calls++
	if s.calls == s.failAt {
		return s.cause
	}
	return s.MemoryWorkflowStore.SaveExecution(ctx, execution)
}

func TestWorkflowProgressSaveFailureStopsFurtherDispatch(t *testing.T) {
	cause := errors.New("synthetic progress persistence failure")
	store := &failingWorkflowStore{MemoryWorkflowStore: NewMemoryWorkflowStore(), failAt: 3, cause: cause}
	var secondCalls atomic.Int32
	wf, err := New("save-failure").AddFunc("first", "first", func(context.Context, StepInput) (*StepOutput, error) { return &StepOutput{Data: "actual-result"}, nil }).AddFunc("second", "second", func(context.Context, StepInput) (*StepOutput, error) { secondCalls.Add(1); return nil, nil }).Build()
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(WithStore(store))
	id, err := executor.RunAsync(t.Context(), wf, WorkflowInput{Data: map[string]any{"original": "input"}})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := executor.WaitForCompletion(t.Context(), id, time.Second)
	if !errors.Is(err, cause) || secondCalls.Load() != 0 || execution.StepResults["first"].Output != "actual-result" {
		t.Fatalf("execution=%+v error=%v second=%d", execution, err, secondCalls.Load())
	}
	persisted, err := store.GetExecution(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.StepResults["first"].Status != StatusRunning {
		t.Fatal("failed save was incorrectly treated as durable completion")
	}
}

func TestWorkflowPauseCancelKeepsCompletedResultsAndWaitIsCancellable(t *testing.T) {
	started, release, paused := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var secondCalls atomic.Int32
	wf, err := New("pause").AddFunc("first", "first", func(context.Context, StepInput) (*StepOutput, error) {
		close(started)
		<-release
		return &StepOutput{Data: "completed-before-pause"}, nil
	}).AddFunc("second", "second", func(context.Context, StepInput) (*StepOutput, error) { secondCalls.Add(1); return nil, nil }).Build()
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor()
	executor.OnEvent(func(event *WorkflowEvent) {
		if event.Type == EventWorkflowPaused {
			close(paused)
		}
	})
	id, err := executor.RunAsync(t.Context(), wf, WorkflowInput{})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := executor.WaitForCompletion(ctx, id, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting ignored cancellation: %v", err)
	}
	if err := executor.Pause(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-paused:
	case <-time.After(time.Second):
		t.Fatal("pause did not reach step boundary")
	}
	if err := executor.Cancel(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	execution, err := executor.WaitForCompletion(t.Context(), id, time.Second)
	if err != nil || execution.Status != StatusCancelled || secondCalls.Load() != 0 || execution.StepResults["first"].Status != StatusCompleted {
		t.Fatalf("execution=%+v err=%v second=%d", execution, err, secondCalls.Load())
	}
}
