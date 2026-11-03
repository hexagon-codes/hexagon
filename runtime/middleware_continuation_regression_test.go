package runtime

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/template"
)

func TestAfterLLMClearsStaleContinuationWhenResponseChanges(t *testing.T) {
	state := &State{Attributes: map[string]any{}}
	original := &template.ProviderContinuation{Protocol: "responses", Source: "fixture", Model: "model"}
	response := &llm.CompletionResponse{Content: "original", ProviderContinuation: original.Clone()}
	middleware := MiddlewareFuncSet{AfterLLMFunc: func(_ context.Context, _ *State, candidate *llm.CompletionResponse) error {
		candidate.Content = "changed"
		return nil
	}}
	if err := runAfterLLM(context.Background(), []Middleware{middleware}, state, response); err != nil {
		t.Fatal(err)
	}
	if response.Content != "changed" {
		t.Fatalf("content not adopted: %+v", response)
	}
	if response.ProviderContinuation != nil {
		t.Fatalf("stale continuation retained after content change: %+v", response.ProviderContinuation)
	}
}

func TestAfterLLMAdoptsContinuationChanges(t *testing.T) {
	state := &State{Attributes: map[string]any{}}
	response := &llm.CompletionResponse{
		Content:              "original",
		ProviderContinuation: &template.ProviderContinuation{Protocol: "responses", Source: "fixture", Model: "model"},
	}
	middleware := MiddlewareFuncSet{AfterLLMFunc: func(_ context.Context, _ *State, candidate *llm.CompletionResponse) error {
		candidate.Content = "changed"
		candidate.ToolCalls = []llm.ToolCall{{ID: "call", Name: "tool", Arguments: `{"value":"changed"}`}}
		candidate.ProviderContinuation = nil
		return nil
	}}
	if err := runAfterLLM(context.Background(), []Middleware{middleware}, state, response); err != nil {
		t.Fatal(err)
	}
	if response.Content != "changed" || len(response.ToolCalls) != 1 {
		t.Fatalf("response transform not adopted: %+v", response)
	}
	if response.ProviderContinuation != nil {
		t.Fatalf("stale continuation retained: %+v", response.ProviderContinuation)
	}
}
