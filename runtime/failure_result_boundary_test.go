package runtime

import (
	"context"
	"errors"
	"github.com/hexagon-codes/ai-core/llm"
	"testing"
)

func TestFallbackEventFailureKeepsExecutedFacts(t *testing.T) {
	original, sinkFailure := errors.New("provider failed"), errors.New("event failed")
	calls := 0
	p := &continuationProvider{complete: func(llm.CompletionRequest) (*llm.CompletionResponse, error) {
		calls++
		if calls == 1 {
			return &llm.CompletionResponse{ToolCalls: []llm.ToolCall{{ID: "save", Name: "noop", Arguments: "{}"}}, Usage: llm.Usage{TotalTokens: 3}}, nil
		}
		return nil, original
	}}
	exec := &noopToolExec{}
	r := NewRunner(Config{ProviderSelector: fallbackSelector{primary: p, backup: p}, ToolExecutor: exec})
	result, e := r.RunWithSink(t.Context(), Request{ID: "fallback-result"}, EventSinkFunc(func(_ context.Context, event Event) error {
		if event.Type == EventProviderFallback {
			return sinkFailure
		}
		return nil
	}))
	if !errors.Is(e, original) || !errors.Is(e, sinkFailure) || result == nil || result.Usage.TotalTokens != 3 || len(result.ToolCalls) != 1 || exec.calls != 1 {
		t.Fatalf("result=%+v error=%v actions=%d", result, e, exec.calls)
	}
}
func TestNilModelResponseKeepsPreviousTurnFacts(t *testing.T) {
	calls := 0
	p := &continuationProvider{complete: func(llm.CompletionRequest) (*llm.CompletionResponse, error) {
		calls++
		if calls == 1 {
			return &llm.CompletionResponse{ToolCalls: []llm.ToolCall{{ID: "save", Name: "noop", Arguments: "{}"}}, Usage: llm.Usage{TotalTokens: 3}}, nil
		}
		return nil, nil
	}}
	r := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: p}, ToolExecutor: &noopToolExec{}})
	result, e := r.Run(t.Context(), Request{ID: "nil-result"})
	if e == nil || result == nil || result.Usage.TotalTokens != 3 || len(result.ToolCalls) != 1 {
		t.Fatalf("result=%+v error=%v", result, e)
	}
}
func TestPreparationFailureKeepsPreviousTurnFacts(t *testing.T) {
	p := &continuationProvider{complete: func(llm.CompletionRequest) (*llm.CompletionResponse, error) {
		return &llm.CompletionResponse{ToolCalls: []llm.ToolCall{{ID: "save", Name: "noop", Arguments: "{}"}}, Usage: llm.Usage{TotalTokens: 3}}, nil
	}}
	mw := MiddlewareFuncSet{BeforeLLMFunc: func(_ context.Context, s *State) error {
		if s.Turn == 1 {
			s.Request.Generation.ResponseFormat = &llm.ResponseFormat{Type: "json_object"}
		}
		return nil
	}}
	r := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: p}, ToolExecutor: &noopToolExec{}, Middleware: []Middleware{mw}})
	result, e := r.Run(t.Context(), Request{ID: "prepare-result", StructuredOutput: &StructuredOutputSpec{Mode: StructuredModePrompt, Schema: &llm.Schema{Type: "object"}}})
	if e == nil || result == nil || result.Usage.TotalTokens != 3 || len(result.ToolCalls) != 1 {
		t.Fatalf("result=%+v error=%v", result, e)
	}
}
