package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/tokenizer"
	hr "github.com/hexagon-codes/hexagon/runtime"
)

func TestCompactionSeesDiscoveredToolsWithoutDurable(t *testing.T) {
	provider := &testProvider{responses: []*llm.CompletionResponse{
		{ToolCalls: []llm.ToolCall{{ID: "search", Name: "search_tools", Arguments: `{"query":"large"}`}}, FinishReason: "tool_calls"},
		{Content: "done", FinishReason: "stop"},
	}}
	view, err := hr.NewDynamicTools(hr.DynamicToolConfig{Discover: func(context.Context, string) (hr.ToolBindingBatch, error) {
		return hr.ToolBindingBatch{Bindings: []hr.ToolBinding{{Definition: llm.NewToolDefinition("large", strings.Repeat("large schema ", 300), &llm.Schema{Type: "object"}), Executor: &unusedToolExecutor{}}}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer view.Release()
	compactions := 0
	compact := Compaction{MaxTokens: 200, Compact: func(_ context.Context, messages []llm.Message) ([]llm.Message, error) {
		compactions++
		return messages, nil
	}}
	runner := hr.NewRunner(hr.Config{ProviderSelector: hr.StaticProviderSelector{Provider: provider}, ToolExecutor: view, Middleware: []hr.Middleware{compact}})
	result, err := runner.Run(t.Context(), hr.Request{ID: "discovery-meter", Messages: []llm.Message{llm.UserMessage("find tools")}})
	if err != nil || result.Content != "done" || compactions != 1 || provider.calls != 2 {
		t.Fatalf("discovered schema was not metered: compactions=%d calls=%d error=%v", compactions, provider.calls, err)
	}
}

func TestCompactionCandidatePreservesInternalBudgetOrigin(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	main := &compactionContractProvider{name: "main"}
	summary := &compactionContractProvider{name: "summary", usage: 7}
	compact := Compaction{ShouldCompact: func([]llm.Message) bool { return true }, Compact: func(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
		_, err := llm.InvokeComplete(ctx, summary, llm.CompletionRequest{Messages: messages}, CompactionPurpose)
		now = now.Add(2 * time.Hour)
		return messages, err
	}}
	runner := hr.NewRunner(hr.Config{ProviderSelector: hr.StaticProviderSelector{Provider: main}, Middleware: []hr.Middleware{
		compact, Budget{Limits: BudgetLimits{MaxDuration: time.Hour}, Now: func() time.Time { return now }},
	}})
	result, err := runner.Run(t.Context(), hr.Request{ID: "summary-clock", Messages: []llm.Message{llm.UserMessage("history")}})
	if !errors.Is(err, hr.ErrBudgetExceeded) || summary.calls != 1 || main.calls != 0 || result == nil || result.Usage.TotalTokens != 7 {
		t.Fatalf("candidate reset budget or lost usage: result=%+v summary=%d main=%d error=%v", result, summary.calls, main.calls, err)
	}
}

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
