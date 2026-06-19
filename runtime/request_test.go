package runtime

import (
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// TestStateAddUsage_AccumulatesAllDimensions 验证 State.AddUsage 累加全部四个
// Token 维度（含 CacheCreationTokens/CacheReadTokens），多次调用正确求和。
func TestStateAddUsage_AccumulatesAllDimensions(t *testing.T) {
	s := &State{}

	s.AddUsage(llm.Usage{
		PromptTokens:        10,
		CompletionTokens:    5,
		TotalTokens:         15,
		CacheCreationTokens: 2,
		CacheReadTokens:     3,
	})
	s.AddUsage(llm.Usage{
		PromptTokens:        20,
		CompletionTokens:    7,
		TotalTokens:         27,
		CacheCreationTokens: 4,
		CacheReadTokens:     6,
	})

	want := llm.Usage{
		PromptTokens:        30,
		CompletionTokens:    12,
		TotalTokens:         42,
		CacheCreationTokens: 6,
		CacheReadTokens:     9,
	}
	if s.Usage != want {
		t.Errorf("AddUsage 累加结果 = %+v, want %+v", s.Usage, want)
	}
}
