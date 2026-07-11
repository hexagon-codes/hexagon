package citation

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hexagon-codes/hexagon/rag"
)

// TestAuditCJKTruncation_ExtractTitle 钉死不变量：文档标题截断（用户可见）必须 rune-safe，
// 纯中文首行跨越字节边界时不得腰斩产生乱码 (AP-141)。
func TestAuditCJKTruncation_ExtractTitle(t *testing.T) {
	// 60 个中文字 = 180 字节；[:50] 落在第 17 个字（字节 48/49/50）中间 → 腰斩。
	doc := rag.Document{Content: strings.Repeat("界", 60)}
	got := (&CitationEngine{}).extractTitle(doc)

	if !utf8.ValidString(got) {
		t.Fatalf("extractTitle 产生非法 UTF-8（CJK 腰斩）: %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("extractTitle 结果含替换字符 \\uFFFD: %q", got)
	}
}
