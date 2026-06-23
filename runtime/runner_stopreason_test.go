package runtime

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// StopReason 是一等终止原因（对齐 Anthropic/OpenAI stop_reason）：
//   - 自然终态（模型给出最终答案）→ end_turn
//   - 轮次耗尽 → max_turns，且**不是错误**（到达 limit 返回部分结果 + StopReason，而非报错）
// 两条用例钉死这一契约，确保调用方可读 result.StopReason 而无需 errors.Is 反查。

func TestStopReason_EndTurnOnNaturalFinish(t *testing.T) {
	provider := &fakeProvider{name: "fake"} // 无 responses → 首轮返回 "done"、无工具调用 → Final
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fake", Model: "m"},
		ToolExecutor:     &noopToolExec{},
		DefaultMaxTurns:  5,
	})

	result, err := runner.Run(context.Background(), Request{
		ID:       "run-endturn",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Limits:   Limits{MaxTurns: 5},
	})

	if err != nil {
		t.Fatalf("natural finish should not error, got %v", err)
	}
	if reason := stopReasonOf(result); reason != StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", reason, StopReasonEndTurn)
	}
}

func TestStopReason_MaxTurnsOnExhaustion(t *testing.T) {
	provider := &loopingProvider{}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "loop", Model: "m"},
		ToolExecutor:     &noopToolExec{},
		DefaultMaxTurns:  3,
	})

	result, err := runner.Run(context.Background(), Request{
		ID:       "run-maxturns-stopreason",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Limits:   Limits{MaxTurns: 3},
	})

	// 达到轮次上限不是错误（stop_reason 模型）。
	if err != nil {
		t.Fatalf("max-turns must not be an error, got %v", err)
	}
	// 一等终止原因可读。
	if reason := stopReasonOf(result); reason != StopReasonMaxTurns {
		t.Fatalf("StopReason = %q, want %q", reason, StopReasonMaxTurns)
	}
}

func stopReasonOf(r *Result) StopReason {
	if r == nil {
		return ""
	}
	return r.StopReason
}
