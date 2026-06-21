package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestSandbox_BenignDotDotPrefixFileAccepted 证明路径穿越守卫只拒绝**真实穿越**，
// 不再误杀字面以 ".." 开头的普通文件名（如 "..keep"）——它清理后仍是临时目录内的
// 相对路径，应放行。
//
// 修复前：守卫用 strings.HasPrefix(cleanPath, "..") 会把 "..keep" 当穿越拒绝。
// 修复后：仅 cleanPath=="." 后为 ".." 自身或以 "../" 开头才拒绝 → "..keep" 放行。
func TestSandbox_BenignDotDotPrefixFileAccepted(t *testing.T) {
	const benign = "..keep" // 合法文件名，filepath.Clean 后为 "..keep"，非穿越

	t.Run("executeProcess accepts benign dotdot-prefix", func(t *testing.T) {
		s := New(&Config{Type: TypeProcess, Timeout: 5 * time.Second})
		out, err := s.executeProcess(context.Background(), ExecuteInput{
			Language: "bash",
			Code:     "echo hi",
			Files:    map[string]string{benign: "data"},
		})
		// 关键断言：绝不能因路径守卫被拒。允许其它非穿越错误（如解释器缺失），但
		// 不能是 "不安全的文件路径"。
		if err != nil && strings.Contains(err.Error(), "不安全的文件路径") {
			t.Fatalf("benign filename %q wrongly rejected as unsafe path: %v", benign, err)
		}
		if err == nil && out != nil && !strings.Contains(out.Stdout, "hi") {
			t.Logf("note: stdout=%q (execution path may vary by host)", out.Stdout)
		}
	})

	t.Run("executeDocker accepts benign dotdot-prefix at guard", func(t *testing.T) {
		s := New(&Config{Type: TypeDocker, Image: "alpine:latest", Timeout: 5 * time.Second})
		_, err := s.executeDocker(context.Background(), ExecuteInput{
			Language: "bash",
			Code:     "echo hi",
			Files:    map[string]string{benign: "data"},
		})
		// docker 可能不可用 → 报 "docker not available"；但绝不能是路径守卫拒绝。
		if err != nil && strings.Contains(err.Error(), "不安全的文件路径") {
			t.Fatalf("benign filename %q wrongly rejected as unsafe path: %v", benign, err)
		}
	})
}

// TestSandbox_BareDotDotStillRejected 守卫加固后，裸 ".." 与 "../x" 仍必须被拒。
func TestSandbox_BareDotDotStillRejected(t *testing.T) {
	for _, p := range []string{"..", "../x", "a/../../escape"} {
		s := New(&Config{Type: TypeProcess, Timeout: 5 * time.Second})
		_, err := s.executeProcess(context.Background(), ExecuteInput{
			Language: "bash",
			Code:     "echo hi",
			Files:    map[string]string{p: "PWNED"},
		})
		if err == nil || !strings.Contains(err.Error(), "不安全的文件路径") {
			t.Fatalf("traversal path %q must be rejected as unsafe, got err=%v", p, err)
		}
	}
}
