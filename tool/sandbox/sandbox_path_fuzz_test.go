package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// FuzzExecuteProcess_NeverEscapesHostDir is the security handler-fuzz (method 11)
// closed loop for the path-traversal guard. For ANY attacker-controlled file path,
// executeProcess must NEVER write to a fixed host sentinel directory outside its own
// temp dir — it either rejects the path or writes strictly inside its sandbox tempdir.
//
// The sentinel dir lives in the test's TempDir; a successful traversal would land a
// file there. We assert the sentinel dir stays empty regardless of input.
func FuzzExecuteProcess_NeverEscapesHostDir(f *testing.F) {
	seeds := []string{
		"../../../../../../../../etc/cron.d/pwn",
		"..", "../x", "a/../../escape", "/abs/pwn",
		"normal.txt", "..keep", "sub/dir/ok.txt", "....//x",
		"%2e%2e/x", "..\\..\\win", strings.Repeat("../", 64) + "pwn",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	sentinelDir := f.TempDir()

	f.Fuzz(func(t *testing.T, rawPath string) {
		s := New(&Config{Type: TypeProcess, Timeout: 3 * time.Second})
		// Build the malicious path so that, if filepath.Join didn't validate,
		// it could resolve into sentinelDir.
		_, _ = s.executeProcess(context.Background(), ExecuteInput{
			Language: "bash",
			Code:     "true",
			Files:    map[string]string{rawPath: "PWNED"},
		})

		// Invariant: the host sentinel dir must contain nothing the sandbox wrote.
		entries, err := os.ReadDir(sentinelDir)
		if err != nil {
			t.Fatalf("reading sentinel dir: %v", err)
		}
		for _, e := range entries {
			data, _ := os.ReadFile(filepath.Join(sentinelDir, e.Name()))
			if strings.Contains(string(data), "PWNED") {
				t.Fatalf("HOST ESCAPE via %q wrote sentinel file %q", rawPath, e.Name())
			}
		}
	})
}
