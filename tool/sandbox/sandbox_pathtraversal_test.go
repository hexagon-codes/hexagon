package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExecuteDocker_RejectsPathTraversal proves that executeDocker validates
// input.Files paths (rejecting ".." escapes and absolute paths) the same way
// executeProcess does, instead of blindly filepath.Join-ing them into tempDir
// and writing on the host filesystem.
//
// The test plants a sentinel target on the host via a "../"-escaping relative
// path and asserts (a) execution fails with an unsafe-path error and (b) the
// host sentinel file is NOT created.
func TestExecuteDocker_RejectsPathTraversal(t *testing.T) {
	// Sentinel lives in the test's own temp dir; the malicious relative path
	// would, via filepath.Join(tempDir, path), resolve to it if unvalidated.
	hostDir := t.TempDir()
	sentinel := filepath.Join(hostDir, "escaped_sentinel.txt")
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("sentinel already exists before test")
	}

	cases := []struct {
		name string
		path string
	}{
		{"absolute path", sentinel},
		{"dotdot escape", "../../../../../../../../" + strings.TrimPrefix(sentinel, "/")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(&Config{
				Type:    TypeDocker,
				Image:   "alpine:latest",
				Timeout: 5 * time.Second,
				WorkDir: "/workspace",
			})

			_, err := s.executeDocker(context.Background(), ExecuteInput{
				Language: "bash",
				Code:     "echo hi",
				Files:    map[string]string{tc.path: "PWNED"},
			})

			// Must be rejected as an unsafe path, not silently written nor masked
			// by an unrelated "docker not available" error.
			if err == nil {
				t.Fatalf("expected unsafe-path rejection, got nil error")
			}
			if !strings.Contains(err.Error(), "不安全的文件路径") {
				t.Fatalf("expected unsafe-path error, got %v", err)
			}

			// The sentinel must never be written on the host.
			if _, statErr := os.Stat(sentinel); statErr == nil {
				_ = os.Remove(sentinel)
				t.Fatalf("HOST ESCAPE: malicious path wrote sentinel file %s", sentinel)
			}
		})
	}
}
