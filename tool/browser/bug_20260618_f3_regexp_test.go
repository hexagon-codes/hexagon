package browser

import (
	"strings"
	"testing"
)

// BUG-20260618-F3: extractText / htmlToMarkdown 使用 Go RE2 不支持的 `\1` 反向引用，
// regexp.MustCompile 在函数被调用时 panic（生产可达路径 browser.go:254/257）。
//
// 修复前：这两条测试因 panic 而 FAIL（RED）。
// 修复后：正常剥离/转换并返回（GREEN）。
// 永久保留作回归锁定，防止再引入 RE2 反向引用。

func TestBUG_20260618_F3_ExtractText_NoBackreferencePanic(t *testing.T) {
	out := extractText(`<p>hello</p><script>alert(1)</script><style>p{color:red}</style> world`)
	if strings.Contains(out, "alert") {
		t.Fatalf("script 内容未被剥离: %q", out)
	}
	if strings.Contains(out, "color:red") {
		t.Fatalf("style 内容未被剥离: %q", out)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Fatalf("正文丢失: %q", out)
	}
}

func TestBUG_20260618_F3_HtmlToMarkdown_NoBackreferencePanic(t *testing.T) {
	out := htmlToMarkdown(`<strong>bold</strong> mid <em>italic</em>`)
	if !strings.Contains(out, "**bold**") {
		t.Fatalf("bold 转换失败: %q", out)
	}
	if !strings.Contains(out, "*italic*") {
		t.Fatalf("italic 转换失败: %q", out)
	}
}
