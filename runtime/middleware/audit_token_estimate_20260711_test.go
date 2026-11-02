package middleware

import (
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/tokenizer"
)

// TestAuditTokenEstimate_CJKAlignment 钉死不变量：compaction 的内置 token 估算口径必须与
// ai-core tokenizer.CountGPT4 中文感知口径对齐。原手写 len(content)/4+4 是字节口径，
// 对 CJK 与英文均漂移 (RU-8)。修复后每条消息委托 CountGPT4，与规范口径一致。
func TestAuditTokenEstimate_CJKAlignment(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"CJK", strings.Repeat("你好世界", 50)},   // 原 len/4+4=154 vs CountGPT4=133
		{"EN", strings.Repeat("hello world ", 50)}, // 原 len/4+4=154 vs CountGPT4=125
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msgs := []llm.Message{{Role: llm.RoleUser, Content: c.content}}
			got := estimateTokens(msgs)
			want := tokenizer.CountGPT4(c.content)
			if got != want {
				t.Fatalf("estimateTokens 口径漂移: got=%d, tokenizer.CountGPT4=%d", got, want)
			}
		})
	}
}
