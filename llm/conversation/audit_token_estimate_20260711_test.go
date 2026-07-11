package conversation

import (
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/tokenizer"
)

// TestAuditTokenEstimate_CJKAlignment 钉死不变量：conversation 的 token 估算口径必须与
// ai-core tokenizer.CountGPT4 规范口径对齐（中文感知）。原手写 runeCount/2 对英文严重高估
// （2.4x）、对中文低估，口径漂移 (RU-8)。修复后委托 CountGPT4，两口径一致。
func TestAuditTokenEstimate_CJKAlignment(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"CJK", strings.Repeat("你好世界", 50)},        // 200 中文字：原 rune/2=100 vs CountGPT4=133
		{"EN", strings.Repeat("hello world ", 50)}, // 原 rune/2=300 vs CountGPT4=125（2.4x 高估）
		{"mixed", strings.Repeat("你好 world 世界 code ", 30)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := estimateTokens(c.text)
			want := tokenizer.CountGPT4(c.text)
			if got != want {
				t.Fatalf("estimateTokens 口径漂移: got=%d, tokenizer.CountGPT4=%d", got, want)
			}
		})
	}
}
