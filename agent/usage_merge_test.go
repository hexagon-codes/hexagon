package agent

import (
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// TestMergeUsage_AccumulatesCacheDimensions 验证 mergeUsage 合并两个 Usage 时
// 同时累加缓存读/写两个维度，避免多步推理中缓存 Token 被静默丢弃。
func TestMergeUsage_AccumulatesCacheDimensions(t *testing.T) {
	got := mergeUsage(
		llm.Usage{
			PromptTokens:        1,
			CompletionTokens:    2,
			TotalTokens:         3,
			CacheCreationTokens: 4,
			CacheReadTokens:     5,
		},
		llm.Usage{
			PromptTokens:        10,
			CompletionTokens:    20,
			TotalTokens:         30,
			CacheCreationTokens: 40,
			CacheReadTokens:     50,
		},
	)

	want := llm.Usage{
		PromptTokens:        11,
		CompletionTokens:    22,
		TotalTokens:         33,
		CacheCreationTokens: 44,
		CacheReadTokens:     55,
	}
	if got != want {
		t.Errorf("mergeUsage = %+v, want %+v", got, want)
	}
}
