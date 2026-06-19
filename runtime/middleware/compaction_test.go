package middleware

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

func msgs(n int) []llm.Message {
	out := make([]llm.Message, 0, n+1)
	out = append(out, llm.Message{Role: llm.RoleSystem, Content: "sys"})
	for i := 0; i < n; i++ {
		out = append(out, llm.Message{Role: llm.RoleUser, Content: "0123456789012345678901234567890123456789"}) // 40 chars ≈ 14 tok
	}
	return out
}

// TestCompaction_TriggersAboveThreshold 验证超阈值触发压缩：保留 system + 最近 N，发事件。
func TestCompaction_TriggersAboveThreshold(t *testing.T) {
	c := Compaction{MaxTokens: 50, KeepRecent: 3}
	st := &hruntime.State{Messages: msgs(10), Attributes: map[string]any{}}

	if err := c.BeforeLLM(context.Background(), st); err != nil {
		t.Fatalf("BeforeLLM error = %v", err)
	}
	// 应保留 1 条 system + 最近 3 条 user = 4 条
	if len(st.Messages) != 4 {
		t.Fatalf("压缩后应剩 4 条（system+最近3），实际 %d", len(st.Messages))
	}
	if st.Messages[0].Role != llm.RoleSystem {
		t.Errorf("system 消息应保留在首位")
	}
}

// TestCompaction_NotTriggeredBelowThreshold 验证未超阈值不压缩。
func TestCompaction_NotTriggeredBelowThreshold(t *testing.T) {
	c := Compaction{MaxTokens: 100000, KeepRecent: 3}
	st := &hruntime.State{Messages: msgs(5), Attributes: map[string]any{}}
	before := len(st.Messages)

	if err := c.BeforeLLM(context.Background(), st); err != nil {
		t.Fatalf("BeforeLLM error = %v", err)
	}
	if len(st.Messages) != before {
		t.Fatalf("未超阈值不应压缩；%d → %d", before, len(st.Messages))
	}
}

// TestCompaction_DisabledWhenMaxTokensZero 验证阈值<=0 完全关闭（行为不变）。
func TestCompaction_DisabledWhenMaxTokensZero(t *testing.T) {
	c := Compaction{MaxTokens: 0}
	st := &hruntime.State{Messages: msgs(100), Attributes: map[string]any{}}
	before := len(st.Messages)
	if err := c.BeforeLLM(context.Background(), st); err != nil {
		t.Fatalf("BeforeLLM error = %v", err)
	}
	if len(st.Messages) != before {
		t.Fatalf("MaxTokens<=0 应关闭压缩；%d → %d", before, len(st.Messages))
	}
}

// TestCompaction_CustomCompactor 验证可注入压缩器（如摘要式）。
func TestCompaction_CustomCompactor(t *testing.T) {
	called := false
	c := Compaction{
		MaxTokens: 1,
		Count:     func([]llm.Message) int { return 999 }, // 强制超阈值
		Compact: func(_ context.Context, m []llm.Message) []llm.Message {
			called = true
			return m[:1] // 自定义：只留第一条
		},
	}
	st := &hruntime.State{Messages: msgs(10), Attributes: map[string]any{}}
	if err := c.BeforeLLM(context.Background(), st); err != nil {
		t.Fatalf("BeforeLLM error = %v", err)
	}
	if !called {
		t.Fatal("自定义 Compactor 应被调用")
	}
	if len(st.Messages) != 1 {
		t.Fatalf("自定义压缩后应剩 1 条，实际 %d", len(st.Messages))
	}
}
