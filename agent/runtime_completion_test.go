package agent

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

func TestRunCompletionWithRuntimeEmitsVersionedEvents(t *testing.T) {
	provider := &mockLLMProvider{response: "runtime response"}
	var events []agentruntime.Event

	resp, err := runCompletionWithRuntime(context.Background(), provider, "run-test", []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	}, agentruntime.EventSinkFunc(func(_ context.Context, event agentruntime.Event) error {
		events = append(events, event)
		return nil
	}))
	if err != nil {
		t.Fatalf("runCompletionWithRuntime() error = %v", err)
	}
	if resp.Content != "runtime response" {
		t.Fatalf("Content = %q, want runtime response", resp.Content)
	}

	wantTypes := []agentruntime.EventType{
		agentruntime.EventRunStarted,
		agentruntime.EventProviderSelected,
		agentruntime.EventLLMStarted,
		agentruntime.EventModelAttemptStarted,
		agentruntime.EventModelAttemptFinished,
		agentruntime.EventLLMCompleted,
		agentruntime.EventRunFinished,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %d, want %d: %#v", len(events), len(wantTypes), events)
	}
	for i, event := range events {
		if event.Type != wantTypes[i] {
			t.Fatalf("event[%d].Type = %s, want %s", i, event.Type, wantTypes[i])
		}
		if event.Version != agentruntime.EventVersionV1 {
			t.Fatalf("event[%d].Version = %q", i, event.Version)
		}
		if event.Sequence != int64(i+1) {
			t.Fatalf("event[%d].Sequence = %d, want %d", i, event.Sequence, i+1)
		}
		if event.RunID != "run-test" {
			t.Fatalf("event[%d].RunID = %q, want run-test", i, event.RunID)
		}
	}
}
