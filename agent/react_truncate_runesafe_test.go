package agent

import (
	"strings"
	"testing"
)

// TestTruncateToolResult_RuneSafe 钉死：工具结果头尾截断按 rune 切分，
// 绝不在多字节中文/emoji 中间切裂产生 U+FFFD（�）。
func TestTruncateToolResult_RuneSafe(t *testing.T) {
	// 全 emoji（每个 4 字节）+ 中文，长度远超 maxLen，逼迫在多字节边界截断。
	s := strings.Repeat("🌍杭州天气🌡", 200)
	out := truncateToolResult(s, 100)

	if strings.ContainsRune(out, '�') {
		t.Fatalf("结果含替换字符 U+FFFD，说明在多字节字符中间切裂了")
	}
	if out == s {
		t.Fatalf("超长输入未被截断")
	}
	if !strings.Contains(out, "结果已截断") {
		t.Fatalf("缺少截断标记")
	}
}

// TestTruncateToolResult_ShortPassThrough 短结果原样返回（按 rune 计数）。
func TestTruncateToolResult_ShortPassThrough(t *testing.T) {
	s := "🌍 杭州 27°C 烟雾霾" // rune 数远小于 maxLen，但字节数较大
	if got := truncateToolResult(s, 100); got != s {
		t.Fatalf("短结果被改动: %q", got)
	}
}
