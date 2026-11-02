package runtime

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

func TestModelAbnormalFinishDoesNotDispatchTools(t *testing.T) {
	for _, reason := range []string{"length", "MAX_TOKENS", "refusal", "content_filter", "pause_turn", "cancelled", "vendor_specific"} {
		t.Run(reason, func(t *testing.T) {
			provider := &fakeProvider{responses: []*llm.CompletionResponse{{Content: "partial", FinishReason: reason, ToolCalls: []llm.ToolCall{{ID: "call", Name: "write", Arguments: "{}"}}, Usage: llm.Usage{TotalTokens: 3}}}}
			executor := &fakeToolExecutor{}
			result, err := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}, ToolExecutor: executor}).Run(t.Context(), Request{ID: "abnormal-tools"})
			if err != nil || result == nil || result.ModelFinishReason != reason || executor.calls != 0 || provider.calls != 1 || result.Usage.TotalTokens != 3 {
				t.Fatalf("abnormal turn dispatched or lost facts: result=%+v model=%d tools=%d error=%v", result, provider.calls, executor.calls, err)
			}
		})
	}
	for _, reason := range []string{"", "STOP", "stop", "tool_calls", "tool_use", "function_call", "end_turn"} {
		t.Run("complete_"+reason, func(t *testing.T) {
			provider := &fakeProvider{responses: []*llm.CompletionResponse{{FinishReason: reason, ToolCalls: []llm.ToolCall{{ID: "call", Name: "write", Arguments: "{}"}}}, {Content: "done", FinishReason: "stop"}}}
			executor := &fakeToolExecutor{}
			result, err := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}, ToolExecutor: executor}).Run(t.Context(), Request{ID: "complete-tools"})
			if err != nil || result.Content != "done" || executor.calls != 1 || provider.calls != 2 {
				t.Fatalf("complete tool turn was blocked: result=%+v model=%d tools=%d error=%v", result, provider.calls, executor.calls, err)
			}
		})
	}
}

func TestAfterToolFailedCandidateKeepsAllResultContainers(t *testing.T) {
	result := ToolResult{Raw: map[string]any{"original": true}, ContentParts: []llm.ContentPart{llm.NewImageURLPart("original-image", "auto")}, Structured: &StructuredPayload{Kind: "object", Version: 1, Data: []byte(`{"kept":true}`)}, ToolSnapshot: &ToolSnapshot{Version: 1, RunID: "run", Bindings: []ToolBindingSpec{{Definition: llm.NewToolDefinition("original", "original description", nil)}}}}
	failure := errors.New("candidate rejected")
	hook := MiddlewareFuncSet{AfterToolFunc: func(_ context.Context, _ *State, _ llm.ToolCall, candidate ToolResult) error {
		candidate.Raw.(map[string]any)["original"] = false
		candidate.ContentParts[0].ImageURL.URL = "changed-image"
		candidate.Structured.Data[0] = '!'
		candidate.ToolSnapshot.Bindings[0].Definition.Function.Description = "changed definition"
		return failure
	}}
	err := runAfterTool(t.Context(), []Middleware{hook}, &State{Attributes: map[string]any{}}, llm.ToolCall{}, result)
	if !errors.Is(err, failure) || result.Raw.(map[string]any)["original"] != true || result.ContentParts[0].ImageURL.URL != "original-image" || string(result.Structured.Data) != `{"kept":true}` || result.ToolSnapshot.Bindings[0].Definition.Function.Description != "original description" {
		t.Fatalf("rejected tool observation changed accepted result: %+v error=%v", result, err)
	}
}

func TestWrapperRetryKeepsContinuePrefixAndDiscardsOnlyCurrentDraft(t *testing.T) {
	calls := 0
	provider := &continuationProvider{stream: func(llm.CompletionRequest) (*llm.Stream, error) {
		calls++
		text, usage := "BASE_", "1"
		if calls == 2 {
			text, usage = "BASE_wrong", "2"
		}
		frame := "data: {\"choices\":[{\"delta\":{\"content\":\"" + text + "\"}}],\"usage\":{\"total_tokens\":" + usage + "}}\n\n"
		if calls < 3 {
			return llm.NewStream(io.MultiReader(strings.NewReader(frame), continuationReadFailure{}), llm.StreamOpenAIFormat), nil
		}
		return llm.NewStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"BASE_right\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":3}}\n\ndata: [DONE]\n\n"), llm.StreamOpenAIFormat), nil
	}}
	visible := ""
	replacements := 0
	sink := EventSinkFunc(func(_ context.Context, event Event) error {
		if event.Metadata["output_action"] == "replace_draft" {
			visible, _ = event.Metadata["replacement_text"].(string)
			replacements++
		}
		if event.Chunk != nil {
			visible += event.Chunk.Content
		}
		return nil
	})
	request := Request{ID: "continue-wrapper", Messages: []llm.Message{llm.UserMessage("original")}, StreamMode: StreamModeTokens}
	_, firstErr := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}}).Stream(t.Context(), request, sink)
	var interrupted *ProviderCallError
	if !errors.As(firstErr, &interrupted) || visible != "BASE_" {
		t.Fatalf("initial partial missing: visible=%q error=%v", visible, firstErr)
	}
	wrapper := MiddlewareFuncSet{WrapModelCallFunc: func(ctx context.Context, _ *State, req llm.CompletionRequest, next ModelCall) (*llm.CompletionResponse, error) {
		if _, err := next(ctx, req); err == nil {
			return nil, errors.New("expected failed first continuation attempt")
		}
		return next(ctx, req)
	}}
	request.Messages = []llm.Message{llm.UserMessage("continued")}
	result, err := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}, Middleware: []Middleware{wrapper}}).Continue(t.Context(), request, interrupted, sink)
	if err != nil || result.Content != "BASE_right" || visible != "BASE_right" || replacements != 1 || calls != 3 || result.Usage.TotalTokens != 6 {
		t.Fatalf("continuation prefix lost or failed draft adopted: result=%+v visible=%q replaces=%d calls=%d error=%v", result, visible, replacements, calls, err)
	}
}
