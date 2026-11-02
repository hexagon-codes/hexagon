package runtime

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

func TestModelFallbackDiscardsFailedDraftAndKeepsBothUsages(t *testing.T) {
	primary := &continuationProvider{stream: func(llm.CompletionRequest) (*llm.Stream, error) {
		return llm.NewStream(io.MultiReader(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"wrong prefix\"}}],\"usage\":{\"total_tokens\":4}}\n\n"), continuationReadFailure{}), llm.StreamOpenAIFormat), nil
	}}
	backup := &continuationProvider{stream: func(req llm.CompletionRequest) (*llm.Stream, error) {
		for _, message := range req.Messages {
			if strings.Contains(message.Content, "wrong prefix") {
				t.Fatal("failed draft reached fallback input")
			}
		}
		return llm.NewStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"fresh answer\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":3}}\n\ndata: [DONE]\n\n"), llm.StreamOpenAIFormat), nil
	}}
	var attempts []ModelAttempt
	var visible strings.Builder
	runner := NewRunner(Config{ProviderSelector: fallbackSelector{primary: primary, backup: backup}, Middleware: []Middleware{MiddlewareFuncSet{AfterModelAttemptFunc: func(_ context.Context, _ *State, attempt ModelAttempt) error {
		attempts = append(attempts, attempt)
		return nil
	}}}})
	result, err := runner.Stream(t.Context(), Request{ID: "draft-test", Messages: []llm.Message{llm.UserMessage("answer")}}, EventSinkFunc(func(_ context.Context, event Event) error {
		if event.Type == EventProviderFallback && event.Metadata["output_action"] == "replace_draft" {
			visible.Reset()
		}
		if event.Chunk != nil {
			visible.WriteString(event.Chunk.Content)
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "fresh answer" || visible.String() != "fresh answer" {
		t.Fatalf("answers mixed: result=%q visible=%q", result.Content, visible.String())
	}
	if result.Usage.TotalTokens != 7 || len(attempts) != 2 || attempts[0].Outcome != "failed" || attempts[0].Usage.TotalTokens != 4 || attempts[1].Usage.TotalTokens != 3 || !attempts[0].UsageKnown || attempts[0].ID == attempts[1].ID {
		t.Fatalf("attempt facts lost: result=%+v attempts=%+v", result, attempts)
	}
	for _, message := range result.OutputMessages {
		if strings.Contains(message.Content, "wrong prefix") {
			t.Fatal("failed draft adopted in output messages")
		}
	}
}

func TestModelWrapperNextCountsEachActualCall(t *testing.T) {
	provider := &fakeProvider{responses: []*llm.CompletionResponse{{Content: "discard", Usage: llm.Usage{TotalTokens: 2}}, {Content: "accepted", Usage: llm.Usage{TotalTokens: 3}}}}
	var attempts []ModelAttempt
	middleware := MiddlewareFuncSet{WrapModelCallFunc: func(ctx context.Context, state *State, req llm.CompletionRequest, next ModelCall) (*llm.CompletionResponse, error) {
		state.Final, state.FinalText = true, "forged"
		if _, err := next(ctx, req); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}, AfterModelAttemptFunc: func(_ context.Context, _ *State, attempt ModelAttempt) error {
		attempts = append(attempts, attempt)
		return nil
	}}
	result, err := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}, Middleware: []Middleware{middleware}}).Run(t.Context(), Request{ID: "wrapper-test"})
	if err != nil || result.Content != "accepted" || result.Usage.TotalTokens != 5 || len(attempts) != 2 {
		t.Fatalf("actual calls not tracked: result=%+v attempts=%+v error=%v", result, attempts, err)
	}
}

type contractToolExecutor struct{ received llm.ToolCall }

func (e *contractToolExecutor) Execute(_ context.Context, call llm.ToolCall) (ToolResult, error) {
	e.received = call
	return ToolResult{Content: "original tool result", Raw: map[string]any{"value": "original"}, Status: ToolStatusSuccess}, nil
}

func TestObserverCannotChangeDispatchedToolOrAcceptedResponse(t *testing.T) {
	provider := &fakeProvider{responses: []*llm.CompletionResponse{{ToolCalls: []llm.ToolCall{{ID: "call", Name: "original", Arguments: "{}"}}}, {Content: "original answer"}}}
	executor := &contractToolExecutor{}
	runner := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}, ToolExecutor: executor})
	result, err := runner.RunWithSink(t.Context(), Request{ID: "observer-test"}, EventSinkFunc(func(_ context.Context, event Event) error {
		if event.State != nil {
			event.State.Final, event.State.FinalText = true, "forged"
			event.State.Messages = nil
		}
		if event.Response != nil {
			event.Response.Content = "forged"
			for i := range event.Response.ToolCalls {
				event.Response.ToolCalls[i].Name = "forged"
			}
		}
		if event.ToolCall != nil {
			event.ToolCall.Name = "forged"
		}
		if event.ToolResult != nil {
			event.ToolResult.Stop = true
			event.ToolResult.Raw.(map[string]any)["value"] = "forged"
		}
		if final, ok := event.Payload.(*Result); ok {
			final.Content = "forged"
		}
		return nil
	}))
	if err != nil || result.Content != "original answer" || executor.received.Name != "original" || len(result.ToolCalls) != 1 || result.ToolCalls[0].Result.Raw.(map[string]any)["value"] != "original" {
		t.Fatalf("observer mutated run: result=%+v call=%+v error=%v", result, executor.received, err)
	}
}

func TestMiddlewareFailedCandidateDoesNotReplaceMessages(t *testing.T) {
	state := &State{Request: Request{ID: "original"}, Messages: []llm.Message{llm.UserMessage("original")}, Attributes: map[string]any{}}
	failure := errors.New("compaction failed")
	err := runBeforeLLM(t.Context(), []Middleware{MiddlewareFuncSet{BeforeLLMFunc: func(_ context.Context, candidate *State) error {
		candidate.Messages[0].Content = "forged"
		candidate.Request.ID, candidate.Final = "another", true
		return failure
	}}}, state)
	if !errors.Is(err, failure) || state.Messages[0].Content != "original" || state.Request.ID != "original" || state.Final {
		t.Fatalf("failed candidate adopted: %+v, %v", state, err)
	}
}
