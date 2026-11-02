package middleware

import (
	"context"
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/tokenizer"
	hr "github.com/hexagon-codes/hexagon/runtime"
)

func TestSummaryCompactorDoesNotAdoptIncompleteTermination(t *testing.T) {
	for _, reason := range []string{"length", "MAX_TOKENS", "refusal", "pause_turn", "cancelled", "unknown"} {
		t.Run(reason, func(t *testing.T) {
			provider := &summaryProvider{response: &llm.CompletionResponse{Content: "incomplete summary", FinishReason: reason}}
			original := []llm.Message{llm.UserMessage("preserve original"), llm.AssistantMessage("old answer"), llm.UserMessage("latest")}
			result, err := ModelSummaryCompactor(provider, llm.CompletionRequest{}, 1)(t.Context(), original)
			if err == nil || result != nil || original[0].Content != "preserve original" || provider.calls != 1 {
				t.Fatalf("incomplete summary adopted: result=%+v calls=%d error=%v", result, provider.calls, err)
			}
		})
	}
}

func TestCompactionMetersDerivedSchemaWithoutAdoptingInjectedPrompt(t *testing.T) {
	for _, mode := range []hr.StructuredOutputMode{hr.StructuredModePrompt, hr.StructuredModeNative, hr.StructuredModeTool} {
		t.Run(string(mode), func(t *testing.T) {
			calls := 0
			state := &hr.State{Request: hr.Request{StructuredOutput: &hr.StructuredOutputSpec{Mode: mode, Schema: &llm.Schema{Type: "object", Description: strings.Repeat("schema description ", 300)}}}, Messages: []llm.Message{llm.UserMessage("hello")}, Attributes: map[string]any{}, MessageRevision: 5}
			compact := Compaction{MaxTokens: 100, Compact: func(_ context.Context, messages []llm.Message) ([]llm.Message, error) {
				calls++
				if len(messages) != 1 || messages[0].Content != "hello" {
					t.Fatalf("derived prompt was inserted into original history: %+v", messages)
				}
				return messages, nil
			}}
			if err := compact.BeforeLLM(t.Context(), state); err != nil {
				t.Fatal(err)
			}
			prepared, err := hr.PrepareModelRequest(state.Request, state.Messages, "")
			if err != nil {
				t.Fatal(err)
			}
			estimate, err := tokenizer.EstimateCompletionRequest(prepared)
			if err != nil || estimate.Tokens <= 100 || calls != 1 || len(state.Messages) != 1 || state.Messages[0].Content != "hello" {
				t.Fatalf("full request not metered or history changed: tokens=%d calls=%d history=%+v error=%v", estimate.Tokens, calls, state.Messages, err)
			}
		})
	}
}
