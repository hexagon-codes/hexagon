package guard

// hex-test 审计 · AP-141：maskEmail 对国际化邮箱（本地部分含 CJK）按字节切片脱敏。
// name[:2] 与 len(name)<=2 都是字节口径——3 字节的中文字符被 [:2] 劈裂 → 非法 UTF-8 脱敏串。
// RED：CJK 邮箱脱敏结果非合法 UTF-8 → FAIL；GREEN：rune-aware 切片后合法。

import (
	"testing"
	"unicode/utf8"
)

func TestMaskEmail_CJKLocalPartStaysValidUTF8_AP141(t *testing.T) {
	cases := []string{
		"用户名@example.com", // 3 CJK 本地部分
		"张@example.com",   // 1 CJK（len(name)=3 字节，旧 len<=2 判假 → 劈裂）
		"李四@example.com",  // 2 CJK
		"ab@example.com",  // ASCII 回归
	}
	for _, in := range cases {
		got := maskEmail(in)
		if !utf8.ValidString(got) {
			t.Fatalf("AP-141: maskEmail(%q)=%q 不是合法 UTF-8（CJK 被字节切片劈裂）", in, got)
		}
		if got == "" {
			t.Fatalf("maskEmail(%q) 不应为空", in)
		}
	}
}
