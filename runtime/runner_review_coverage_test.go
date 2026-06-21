package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// loopingProvider always returns a tool call, never a final answer — models an
// LLM stuck in a tool-calling loop. Each turn uses a fresh tool-call ID (as a
// real LLM would) so the per-tool idempotency dedup does not collapse them.
type loopingProvider struct{ calls int }

func (p *loopingProvider) Name() string { return "loop" }
func (p *loopingProvider) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.calls++
	return &llm.CompletionResponse{
		Content:   "thinking step",
		ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("t%d", p.calls), Name: "noop", Arguments: "{}"}},
		Usage:     llm.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}, nil
}
func (p *loopingProvider) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, errors.New("unused")
}

type noopToolExec struct{ calls int }

func (e *noopToolExec) Execute(context.Context, llm.ToolCall) (ToolResult, error) {
	e.calls++
	return ToolResult{Content: "ok"}, nil
}

// When MaxTurns is exhausted without a final answer, the runner returns
// ErrMaxTurns WITH a non-nil partial result carrying the accumulated usage,
// reasoning and tool-call records — so a caller can recover partial work and
// the token usage already billed.
func TestMaxTurns_DiscardsAllPartialWorkAndUsage(t *testing.T) {
	provider := &loopingProvider{}
	exec := &noopToolExec{}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "loop", Model: "m"},
		ToolExecutor:     exec,
		DefaultMaxTurns:  3,
	})

	result, err := runner.Run(context.Background(), Request{
		ID:       "run-maxturns",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Limits:   Limits{MaxTurns: 3},
	})

	if !errors.Is(err, ErrMaxTurns) {
		t.Fatalf("want ErrMaxTurns, got %v", err)
	}
	if result == nil {
		t.Fatal("partial result must be returned on max-turns, got nil")
	}
	// The provider was invoked MaxTurns times and tools ran each turn; the
	// partial result must carry that work and the billed usage.
	if provider.calls != 3 {
		t.Fatalf("provider.calls = %d, want 3 (work done)", provider.calls)
	}
	if exec.calls != 3 {
		t.Fatalf("exec.calls = %d, want 3 (side effects happened)", exec.calls)
	}
	if result.Usage.TotalTokens != 6 {
		t.Fatalf("partial usage TotalTokens = %d, want 6 (3 turns x 2)", result.Usage.TotalTokens)
	}
	if len(result.ToolCalls) != 3 {
		t.Fatalf("partial ToolCalls = %d, want 3", len(result.ToolCalls))
	}
}

// toolErrProvider emits one tool call then (on the 2nd turn) a final answer.
type toolErrProvider struct{ calls int }

func (p *toolErrProvider) Name() string { return "te" }
func (p *toolErrProvider) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{{ID: "t1", Name: "boom", Arguments: "{}"}},
		}, nil
	}
	return &llm.CompletionResponse{Content: "final"}, nil
}
func (p *toolErrProvider) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, errors.New("unused")
}

// erroringToolExec returns a non-nil error from Execute.
type erroringToolExec struct{}

func (erroringToolExec) Execute(context.Context, llm.ToolCall) (ToolResult, error) {
	return ToolResult{}, errors.New("tool blew up")
}

// FINDING (tool error propagation — POSITIVE/contract): a tool execution error
// does NOT abort the run; it is converted into a tool message fed back to the
// LLM, which can then recover. Locks in the intended self-healing contract
// (runner.go:442-448) so a future refactor can't silently turn tool errors
// into fatal run failures.
func TestToolExecutionError_IsFedBackNotFatal(t *testing.T) {
	provider := &toolErrProvider{}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "te", Model: "m"},
		ToolExecutor:     erroringToolExec{},
		DefaultMaxTurns:  5,
	})

	result, err := runner.Run(context.Background(), Request{
		ID:       "run-toolerr",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("tool error must NOT be fatal, got err=%v", err)
	}
	if result == nil || result.Content != "final" {
		t.Fatalf("expected recovery to final answer, got %+v", result)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Result.Error == "" {
		t.Fatalf("expected the failed tool to be recorded with an Error, got %+v", result.ToolCalls)
	}
	t.Logf("CONFIRMED: tool error surfaced as ToolResult.Error=%q and fed back; run recovered",
		result.ToolCalls[0].Result.Error)
}

// FINDING (edge case): with no ToolExecutor configured, a tool call yields a
// synthetic "not available" tool result rather than a crash — and the run still
// completes. Covers runner.go:439-440 (previously uncovered nil-executor path).
func TestNilToolExecutor_DoesNotPanicAndReportsUnavailable(t *testing.T) {
	provider := &toolErrProvider{}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "te", Model: "m"},
		// ToolExecutor intentionally nil
		DefaultMaxTurns: 5,
	})
	result, err := runner.Run(context.Background(), Request{
		ID:       "run-noexec",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("nil executor must not fail the run, got %v", err)
	}
	if result == nil || len(result.ToolCalls) != 1 {
		t.Fatalf("expected one recorded tool call, got %+v", result)
	}
	if result.ToolCalls[0].Result.Error != "tool executor not configured" {
		t.Fatalf("Result.Error = %q", result.ToolCalls[0].Result.Error)
	}
}

// dupIDProvider returns a tool call with the SAME id "dup" on turn 1, again on
// turn 2, then a final answer on turn 3.
type dupIDProvider struct{ calls int }

func (p *dupIDProvider) Name() string { return "dup" }
func (p *dupIDProvider) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.calls++
	if p.calls <= 2 {
		return &llm.CompletionResponse{
			ToolCalls: []llm.ToolCall{{ID: "dup", Name: "noop", Arguments: "{}"}},
		}, nil
	}
	return &llm.CompletionResponse{Content: "final"}, nil
}
func (p *dupIDProvider) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, errors.New("unused")
}

// Duplicate tool-call ID across turns on the normal (non-durable) path: the
// per-tool idempotency dedup is scoped per-step (only tool results that follow
// the current step's assistant tool_call message count as "already done"), so a
// provider reusing the SAME tool-call ID on a later turn is executed again and
// gets its own paired tool result. Every assistant tool_call stays paired.
func TestDuplicateToolCallID_SilentlySkippedOnNormalPath(t *testing.T) {
	provider := &dupIDProvider{}
	exec := &noopToolExec{}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "dup", Model: "m"},
		ToolExecutor:     exec,
		DefaultMaxTurns:  5,
	})
	result, err := runner.Run(context.Background(), Request{
		ID:       "run-dupid",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// Two turns both declared tool call "dup"; each must execute and be recorded.
	if exec.calls != 2 {
		t.Fatalf("exec.calls = %d, want 2 (each turn's tool call executed)", exec.calls)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("recorded ToolCalls = %d, want 2", len(result.ToolCalls))
	}
}

// FINDING (context cancellation): a context canceled before the loop body is
// observed and surfaced as the ctx error (not ErrMaxTurns / not a hang).
// Covers runner.go:212-215.
func TestContextCanceled_SurfacesCtxError(t *testing.T) {
	provider := &loopingProvider{}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "loop", Model: "m"},
		ToolExecutor:     &noopToolExec{},
		DefaultMaxTurns:  3,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runner.Run(ctx, Request{
		ID:       "run-cancel",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
