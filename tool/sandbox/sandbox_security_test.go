package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessMode_FailsClosedBeforeRunningWithoutNetworkIsolation(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	s := New(&Config{
		Type:           TypeProcess,
		Timeout:        5 * time.Second,
		NetworkEnabled: false,
	})

	_, err := s.execute(context.Background(), ExecuteInput{
		Language: "bash",
		Code:     fmt.Sprintf("printf executed > %q", marker),
	})
	if err == nil {
		t.Fatal("safe process mode must fail closed when network isolation is unavailable")
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("process ran before isolation was rejected: stat error = %v", statErr)
	}
}

func TestProcessMode_FailsClosedBeforeRunningWithUnenforceableResourceLimits(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	s := New(&Config{
		Type:           TypeProcess,
		Timeout:        5 * time.Second,
		Memory:         64 * 1024 * 1024,
		CPU:            0.5,
		NetworkEnabled: true,
	})

	_, err := s.execute(context.Background(), ExecuteInput{
		Language: "bash",
		Code:     fmt.Sprintf("printf executed > %q", marker),
	})
	if err == nil {
		t.Fatal("safe process mode must fail closed when configured limits cannot be guaranteed")
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("process ran before resource isolation was rejected: stat error = %v", statErr)
	}
}

func TestUnsafeProcessMode_RequiresExplicitOptInAndRuns(t *testing.T) {
	s := New(&Config{
		Type:          TypeUnsafeProcess,
		Timeout:       5 * time.Second,
		MaxOutputSize: 1024,
	})

	out, err := s.execute(context.Background(), ExecuteInput{
		Language: "bash",
		Code:     "printf unsafe-opt-in",
	})
	if err != nil {
		t.Fatalf("explicit unsafe process mode failed: %v", err)
	}
	if out == nil || !strings.Contains(out.Stdout, "unsafe-opt-in") {
		t.Fatalf("unexpected output: %#v", out)
	}
}
