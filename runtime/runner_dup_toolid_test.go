package runtime

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// TestDuplicateToolCallID_PairsEveryToolCall proves that when a provider reuses
// the SAME tool-call ID across turns on the normal (non-durable) path, the
// runner executes the tool each turn AND appends a paired tool result message
// for every assistant tool_call — instead of silently skipping the later
// occurrence and leaving the turn's assistant tool_call unpaired.
//
// An unpaired tool_call in the message history breaks the next provider request
// (the contract requires every assistant tool_call to be followed by a matching
// tool result).
func TestDuplicateToolCallID_PairsEveryToolCall(t *testing.T) {
	provider := &dupIDProvider{}
	exec := &noopToolExec{}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "dup", Model: "m"},
		ToolExecutor:     exec,
		DefaultMaxTurns:  5,
	})
	result, err := runner.Run(context.Background(), Request{
		ID:       "run-dupid-paired",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	// Both turns declared tool call "dup"; both must execute.
	if exec.calls != 2 {
		t.Fatalf("exec.calls = %d, want 2 (both turns' tool calls executed)", exec.calls)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("recorded ToolCalls = %d, want 2", len(result.ToolCalls))
	}

	// Every assistant tool_call in the final message history must have a paired
	// tool result message with the same ToolCallID. Reconstruct the runner's
	// outgoing sequence the same way it does before each provider call.
	assertEveryToolCallPaired(t, result)
}

// assertEveryToolCallPaired re-runs the run to capture the final sanitized
// message sequence and verifies no assistant tool_call is left without a
// matching tool result. We reach into a capturing provider to grab the exact
// messages the runner would send on the turn AFTER the duplicate.
func assertEveryToolCallPaired(t *testing.T, _ *Result) {
	t.Helper()

	cap := &capturingDupProvider{}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: cap, Name: "dup", Model: "m"},
		ToolExecutor:     &noopToolExec{},
		DefaultMaxTurns:  5,
	})
	_, err := runner.Run(context.Background(), Request{
		ID:       "run-dupid-capture",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("capture run err: %v", err)
	}

	// Inspect the messages the provider received on its LAST call (the final
	// turn, after both duplicate tool calls). Every assistant tool_call ID must
	// have a corresponding tool result.
	msgs := cap.lastMessages
	toolResults := make(map[string]bool)
	for _, m := range msgs {
		if m.Role == llm.RoleTool && m.ToolCallID != "" {
			toolResults[m.ToolCallID] = true
		}
	}
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant {
			for _, tc := range m.ToolCalls {
				if tc.ID != "" && !toolResults[tc.ID] {
					t.Fatalf("UNPAIRED tool_call %q: assistant declared it but no tool result followed (messages=%+v)", tc.ID, msgs)
				}
			}
		}
	}
}

// capturingDupProvider behaves like dupIDProvider (reuses ID "dup" on turns 1
// and 2, final on turn 3) and records the messages of its most recent call.
type capturingDupProvider struct {
	calls        int
	lastMessages []llm.Message
}

func (p *capturingDupProvider) Name() string { return "dup" }
func (p *capturingDupProvider) Complete(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.calls++
	p.lastMessages = append([]llm.Message(nil), req.Messages...)
	if p.calls <= 2 {
		return &llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{{ID: "dup", Name: "noop", Arguments: "{}"}},
		}, nil
	}
	return &llm.CompletionResponse{Content: "final"}, nil
}
func (p *capturingDupProvider) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, context.Canceled
}
