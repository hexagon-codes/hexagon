package runtime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/checkpoint"
)

type identityCancelExecutor struct {
	calls  []string
	cancel context.CancelFunc
}

func TestToolStopSurvivesPendingProgressResume(t *testing.T) {
	durable := NewDurableExecution(checkpoint.NewMemory())
	start := 0
	if err := durable.Save(context.Background(), Snapshot{
		RunID: "stopped-progress", Step: 0, PendingToolCallStart: &start,
		ToolCalls: []ToolCallRecord{{ID: "first", Result: ToolResult{Content: "stopped result", Stop: true}}},
		Pending: []PendingTool{
			{Call: llm.ToolCall{ID: "first"}, SideEffect: SideEffectUnsafe},
			{Call: llm.ToolCall{ID: "second"}, SideEffect: SideEffectUnsafe},
		},
	}); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{name: "fixture"}
	executor := &identityCancelExecutor{}
	finalizations := 0
	runner := NewRunner(Config{Durable: durable, ToolExecutor: executor,
		ProviderSelector: StaticProviderSelector{Provider: provider},
		Middleware:       []Middleware{MiddlewareFuncSet{FinalizeFunc: func(context.Context, *State) error { finalizations++; return nil }}},
	})
	result, err := runner.Resume(context.Background(), "stopped-progress", Request{ID: "stopped-progress"}, nil)
	if err != nil || result == nil || result.Content != "stopped result" || provider.calls != 0 || len(executor.calls) != 0 || finalizations != 1 {
		t.Fatalf("result=%+v err=%v model=%d tools=%v finalizations=%d", result, err, provider.calls, executor.calls, finalizations)
	}
	snapshot, ok, err := durable.Load(context.Background(), "stopped-progress")
	if err != nil || !ok || !snapshot.Final || len(snapshot.Pending) != 0 {
		t.Fatalf("final snapshot = %+v, %v", snapshot, err)
	}
}

func (e *identityCancelExecutor) Execute(ctx context.Context, call llm.ToolCall) (ToolResult, error) {
	e.calls = append(e.calls, call.Name)
	if e.cancel != nil && len(e.calls) == 1 {
		e.cancel()
		return ToolResult{}, ctx.Err()
	}
	return ToolResult{Content: "observed", Status: ToolStatusSuccess}, nil
}

func TestResumeReusedCallIDBelongsToPendingTurn(t *testing.T) {
	for _, effect := range []ToolSideEffect{SideEffectReadOnly, SideEffectUnsafe} {
		t.Run(map[ToolSideEffect]string{SideEffectReadOnly: "safe", SideEffectUnsafe: "unsafe"}[effect], func(t *testing.T) {
			durable := NewDurableExecution(checkpoint.NewMemory())
			pendingCall := llm.ToolCall{ID: "dup", Name: "new-operation", Arguments: "{}"}
			if err := durable.Save(context.Background(), Snapshot{
				RunID: "reuse", Step: 1,
				Messages: []llm.Message{
					{Role: llm.RoleUser, Content: "hello"},
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCallRef{{ID: "dup", Name: pendingCall.Name, Arguments: "{}"}}},
					{Role: llm.RoleTool, ToolCallID: "dup", Content: "old-result"},
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCallRef{{ID: "dup", Name: pendingCall.Name, Arguments: "{}"}}},
				},
				ToolCalls: []ToolCallRecord{{ID: "dup", Name: pendingCall.Name, Arguments: "{}", Result: ToolResult{Content: "old-result", Status: ToolStatusSuccess}}},
				Pending:   []PendingTool{{Call: pendingCall, SideEffect: effect}},
			}); err != nil {
				t.Fatal(err)
			}
			executor := &identityCancelExecutor{}
			provider := &fakeProvider{name: "fixture", responses: []*llm.CompletionResponse{{Content: "finished"}}}
			runner := NewRunner(Config{Durable: durable, ToolExecutor: executor,
				ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fixture", Model: "fixture"}})
			_, err := runner.Resume(context.Background(), "reuse", Request{ID: "reuse"}, nil)
			if effect == SideEffectUnsafe {
				if !errors.Is(err, ErrUnsafeReplay) || len(executor.calls) != 0 {
					t.Fatalf("unknown new write: error=%v calls=%v", err, executor.calls)
				}
			} else if err != nil || !reflect.DeepEqual(executor.calls, []string{"new-operation"}) {
				t.Fatalf("pending safe operation: error=%v calls=%v", err, executor.calls)
			}
		})
	}
}

type intentBoundaryDurable struct {
	DurableExecution
	stopped bool
	cause   error
}

func (d *intentBoundaryDurable) Save(ctx context.Context, snapshot Snapshot) error {
	if err := d.DurableExecution.Save(ctx, snapshot); err != nil {
		return err
	}
	if !d.stopped && snapshot.Step == 1 && len(snapshot.Pending) > 0 {
		d.stopped = true
		return d.cause
	}
	return nil
}

func TestRunnerPersistsCurrentPendingScope(t *testing.T) {
	cause := errors.New("stop after second intent is persisted")
	durable := &intentBoundaryDurable{DurableExecution: NewDurableExecution(checkpoint.NewMemory()), cause: cause}
	call := llm.ToolCall{ID: "dup", Name: "same-operation", Arguments: "{}"}
	provider := &fakeProvider{name: "fixture", responses: []*llm.CompletionResponse{
		{ToolCalls: []llm.ToolCall{call}}, {ToolCalls: []llm.ToolCall{call}}, {Content: "finished"},
	}}
	executor := &identityCancelExecutor{}
	runner := NewRunner(Config{Durable: durable, ToolExecutor: executor,
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fixture", Model: "fixture"}})
	request := Request{ID: "scope", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}
	if _, err := runner.Run(context.Background(), request); !errors.Is(err, cause) {
		t.Fatalf("expected stored intent interruption, got %v", err)
	}
	snapshot, ok, err := durable.Load(context.Background(), "scope")
	if err != nil || !ok || snapshot.PendingToolCallStart == nil || *snapshot.PendingToolCallStart != 1 {
		t.Fatalf("current-step boundary was lost: snapshot=%+v error=%v", snapshot, err)
	}
	// 本轮同参写尚未确认完成，前轮同 ID 的结果不能使它获得已完成身份。
	if _, err := runner.Resume(context.Background(), "scope", request, nil); !errors.Is(err, ErrUnsafeReplay) {
		t.Fatalf("unsafe second intent was accepted: %v", err)
	}
	if !reflect.DeepEqual(executor.calls, []string{"same-operation"}) {
		t.Fatalf("unknown operation was dispatched again: %v", executor.calls)
	}
}

func TestCanceledBatchDoesNotDispatchNextTool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := &identityCancelExecutor{cancel: cancel}
	provider := &fakeProvider{name: "fixture", responses: []*llm.CompletionResponse{{ToolCalls: []llm.ToolCall{
		{ID: "one", Name: "first", Arguments: "{}"}, {ID: "two", Name: "second", Arguments: "{}"},
	}}}}
	runner := NewRunner(Config{ToolExecutor: executor,
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fixture", Model: "fixture"}})
	result, err := runner.Run(ctx, Request{ID: "cancel-batch", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}})
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(executor.calls, []string{"first"}) {
		t.Fatalf("error=%v dispatched=%v, want cancellation after first tool", err, executor.calls)
	}
	if result == nil || len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "first" || result.StopReason != "" {
		t.Fatalf("observed partial result was lost or marked complete: %+v", result)
	}
}

func TestCanceledLastToolDoesNotFinishSuccessfully(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := &identityCancelExecutor{cancel: cancel}
	provider := &fakeProvider{name: "fixture", responses: []*llm.CompletionResponse{{ToolCalls: []llm.ToolCall{{ID: "one", Name: "last", Arguments: "{}"}}}}}
	runner := NewRunner(Config{DefaultMaxTurns: 1, ToolExecutor: executor, ProviderSelector: StaticProviderSelector{Provider: provider}})
	result, err := runner.Run(ctx, Request{ID: "cancel-last"})
	if !errors.Is(err, context.Canceled) || result == nil || len(result.ToolCalls) != 1 || result.StopReason != "" {
		t.Fatalf("last tool cancellation: result=%+v err=%v", result, err)
	}
}
