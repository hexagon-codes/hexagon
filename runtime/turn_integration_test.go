package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

func TestTurnStopAfterModelRetainsFactsAndDoesNotStopNextTurn(t *testing.T) {
	controller := NewTurnController()
	identity := TurnIdentity{SessionID: "session", RunID: "original", TurnID: "a"}
	handle, err := controller.Begin(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Finish()
	provider := &fakeProvider{responses: []*llm.CompletionResponse{{ToolCalls: []llm.ToolCall{{ID: "one", Name: "write", Arguments: "{}"}}, Usage: llm.Usage{TotalTokens: 3}}}}
	executor := &fakeToolExecutor{}
	runner := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}, ToolExecutor: executor, Middleware: []Middleware{MiddlewareFuncSet{AfterModelAttemptFunc: func(context.Context, *State, ModelAttempt) error {
		controller.RequestStop(identity, StopAfterModel)
		return nil
	}}}})
	result, err := runner.Run(handle.Context(), Request{ID: identity.RunID})
	if !errors.Is(err, ErrTurnStopped) || result == nil || result.Usage.TotalTokens != 3 || executor.calls != 0 || provider.calls != 1 {
		t.Fatalf("stop boundary lost facts or dispatched tools: result=%+v model=%d tools=%d error=%v", result, provider.calls, executor.calls, err)
	}
	select {
	case <-handle.Done():
		t.Fatal("cancel request was reported as execution completion")
	default:
	}
	handle.Finish()
	nextIdentity := TurnIdentity{SessionID: "session", RunID: "next", TurnID: "b"}
	next, err := controller.Begin(t.Context(), nextIdentity)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Finish()
	if controller.RequestStop(identity, StopImmediate) {
		t.Fatal("finished turn remained a cancellation target")
	}
	other := &fakeProvider{responses: []*llm.CompletionResponse{{Content: "next completes"}}}
	result, err = NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: other}}).Run(next.Context(), Request{ID: nextIdentity.RunID})
	if err != nil || result.Content != "next completes" || other.calls != 1 {
		t.Fatalf("late old cancellation reached next turn: result=%+v error=%v", result, err)
	}
}

func TestTurnCancellationDuringBeforeModelDoesNotDispatchProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	provider := &fakeProvider{}
	runner := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}, Middleware: []Middleware{MiddlewareFuncSet{BeforeLLMFunc: func(context.Context, *State) error { cancel(); return nil }}}})
	result, err := runner.Run(ctx, Request{ID: "cancel-before-model"})
	if !errors.Is(err, context.Canceled) || provider.calls != 0 || result == nil || result.Usage.TotalTokens != 0 {
		t.Fatalf("provider was dispatched after cancellation: calls=%d result=%+v error=%v", provider.calls, result, err)
	}
}
