package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestAuditCJKTruncation_LogQuery 钉死不变量：日志查询截断必须 rune-safe，
// 纯中文串跨越字节边界时不得腰斩多字节 UTF-8 产生非法字节 (AP-141)。
func TestAuditCJKTruncation_LogQuery(t *testing.T) {
	// 60 个中文字 = 180 字节；[:100] 落在第 34 个字（字节 99/100/101）中间 → 腰斩。
	query := strings.Repeat("好", 60)
	got := truncateForLog(query)

	if !utf8.ValidString(got) {
		t.Fatalf("truncateForLog 产生非法 UTF-8（CJK 腰斩）: %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("truncateForLog 结果含替换字符 \\uFFFD: %q", got)
	}
}
