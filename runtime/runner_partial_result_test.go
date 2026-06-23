package runtime

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// TestMaxTurns_ReturnsPartialResultWithUsage proves that on MaxTurns
// exhaustion the runner returns a non-nil partial *Result carrying the
// accumulated token usage, reasoning and tool-call records — with NO error
// (max-turns is a normal terminal outcome reported via StopReason, like the
// Anthropic/OpenAI stop_reason model), instead of discarding billed work.
func TestMaxTurns_ReturnsPartialResultWithUsage(t *testing.T) {
	provider := &loopingProvider{}
	exec := &noopToolExec{}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "loop", Model: "m"},
		ToolExecutor:     exec,
		DefaultMaxTurns:  3,
	})

	result, err := runner.Run(context.Background(), Request{
		ID:       "run-maxturns-partial",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Limits:   Limits{MaxTurns: 3},
	})

	if err != nil {
		t.Fatalf("max-turns is not an error, want nil, got %v", err)
	}
	if result == nil {
		t.Fatal("partial result must be returned on max-turns, got nil")
	}
	if result.StopReason != StopReasonMaxTurns {
		t.Fatalf("StopReason = %q, want %q", result.StopReason, StopReasonMaxTurns)
	}
	// Each of the 3 turns billed 2 tokens; the partial result must carry them.
	if result.Usage.TotalTokens != 6 {
		t.Fatalf("partial usage TotalTokens = %d, want 6 (3 turns x 2)", result.Usage.TotalTokens)
	}
	// All 3 tool executions must be recorded so the caller can recover partial work.
	if len(result.ToolCalls) != 3 {
		t.Fatalf("partial ToolCalls = %d, want 3", len(result.ToolCalls))
	}
}
