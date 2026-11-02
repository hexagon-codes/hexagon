package runtime

import (
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

func TestModelFinishReasonIsNotAlwaysNaturalCompletion(t *testing.T) {
	for _, test := range []struct {
		upstream string
		stop     StopReason
	}{
		{"stop", StopReasonEndTurn}, {"end_turn", StopReasonEndTurn},
		{"length", StopReasonOutputLimit}, {"max_tokens", StopReasonOutputLimit},
		{"refusal", StopReasonRefusal}, {"content_filter", StopReasonContentFilter},
		{"pause_turn", StopReasonPauseTurn}, {"vendor_specific", StopReasonModelStopped},
	} {
		t.Run(test.upstream, func(t *testing.T) {
			provider := &fakeProvider{responses: []*llm.CompletionResponse{{Content: "partial or final", FinishReason: test.upstream}}}
			result, err := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}}).Run(t.Context(), Request{ID: "finish-reason"})
			if err != nil || result.StopReason != test.stop || result.ModelFinishReason != test.upstream || provider.calls != 1 {
				t.Fatalf("reason was lost or automatically continued: result=%+v calls=%d error=%v", result, provider.calls, err)
			}
		})
	}
	provider := &fakeProvider{responses: []*llm.CompletionResponse{{Content: "incomplete", FinishReason: "tool_calls"}}}
	result, err := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}}).Run(t.Context(), Request{ID: "invalid-tool-finish"})
	if err == nil || (result != nil && result.StopReason != "") {
		t.Fatalf("incomplete tool call reported success: result=%+v error=%v", result, err)
	}
}
