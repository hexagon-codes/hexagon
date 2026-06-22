package config

import (
	"strings"
	"testing"
)

// 回归测试 — gosec G703（2026-06-22 hex-test ①）：CopyConfig 的 name 未校验，
// `../..` 可逃逸 baseDir。修复后必须拒绝路径穿越名。
func TestCopyConfig_RejectsTraversalName(t *testing.T) {
	m := &EnvironmentManager{baseDir: t.TempDir()}
	for _, bad := range []string{"../../etc/passwd", "..", "a/../../b", "/abs/path", "sub/name"} {
		err := m.CopyConfig(bad, EnvDevelopment, EnvTest)
		if err == nil || !strings.Contains(err.Error(), "invalid config name") {
			t.Errorf("name=%q 应被拒(invalid config name)，实际 err=%v", bad, err)
		}
	}
}
