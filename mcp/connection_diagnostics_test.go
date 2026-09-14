package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStdioConnectionStartupStage(t *testing.T) {
	_, _, err := ConnectStdioServerV2(context.Background(), filepath.Join(t.TempDir(), "missing-mcp-command"))
	var diagnostic *StdioConnectError
	if !errors.As(err, &diagnostic) || diagnostic.Stage != "connect" || diagnostic.HasExitCode {
		t.Fatalf("unexpected startup diagnostic: %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original startup cause was lost: %v", err)
	}
}

func TestStdioConnectionFailureLifecycle(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		stage string
		exit  int
	}{
		{"unsupported-version", "initialize", 0},
		{"initialize-error", "initialize", 0},
		{"tools-list-error", "tools/list", 0},
		{"startup-exit", "initialize", 23},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "stdin-closed")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _, err = ConnectStdioServerV2WithEnv(ctx, executable, map[string]string{
				"HEXAGON_MCP_DIAGNOSTIC_HELPER": tc.mode,
				"HEXAGON_MCP_DIAGNOSTIC_MARKER": marker,
			}, "-test.run=^TestMCPDiagnosticHelper$")
			var diagnostic *StdioConnectError
			if !errors.As(err, &diagnostic) {
				t.Fatalf("missing stdio diagnostic: %v", err)
			}
			if diagnostic.Stage != tc.stage || !diagnostic.HasExitCode || diagnostic.ExitCode != tc.exit {
				t.Fatalf("unexpected process result: %+v", diagnostic)
			}
			if !strings.Contains(diagnostic.Stderr, "fixture startup diagnostic") {
				t.Fatalf("missing stderr: %v", err)
			}
			if tc.mode != "startup-exit" {
				if data, err := os.ReadFile(marker); err != nil || string(data) != "closed" {
					t.Fatalf("subprocess did not observe stdin closure: data=%q err=%v", data, err)
				}
			}
		})
	}
}

func TestStdioConnectionInheritedStderrReturns(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	stop := filepath.Join(dir, "stop")
	done := filepath.Join(dir, "done")
	t.Cleanup(func() {
		_ = os.WriteFile(stop, nil, 0600)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(done); err == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Error("stderr helper did not stop")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()
	_, _, err = ConnectStdioServerV2WithEnv(ctx, executable, map[string]string{
		"HEXAGON_MCP_DIAGNOSTIC_HELPER": "inherited-stderr",
		"HEXAGON_MCP_DIAGNOSTIC_STOP":   stop,
		"HEXAGON_MCP_DIAGNOSTIC_DONE":   done,
	}, "-test.run=^TestMCPDiagnosticHelper$")
	if elapsed := time.Since(started); elapsed >= 10*time.Second {
		t.Fatalf("connection failure waited for a descendant's stderr: %v", elapsed)
	}
	var diagnostic *StdioConnectError
	if !errors.As(err, &diagnostic) || diagnostic.Stage != "initialize" {
		t.Fatalf("unexpected inherited stderr diagnostic: %v", err)
	}
}

// 子进程只使用本地协议和临时文件，不连接外部服务。
func TestMCPDiagnosticHelper(t *testing.T) {
	mode := os.Getenv("HEXAGON_MCP_DIAGNOSTIC_HELPER")
	if mode == "" {
		return
	}
	if mode == "hold-stderr" {
		defer os.Exit(0)
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(os.Getenv("HEXAGON_MCP_DIAGNOSTIC_STOP")); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		_ = os.WriteFile(os.Getenv("HEXAGON_MCP_DIAGNOSTIC_DONE"), nil, 0600)
		return
	}
	if mode == "inherited-stderr" {
		executable, err := os.Executable()
		if err != nil {
			os.Exit(90)
		}
		child := exec.Command(executable, "-test.run=^TestMCPDiagnosticHelper$")
		child.Env = append(os.Environ(), "HEXAGON_MCP_DIAGNOSTIC_HELPER=hold-stderr")
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(91)
		}
	}
	fmt.Fprintln(os.Stderr, "fixture startup diagnostic")
	if mode == "startup-exit" {
		os.Exit(23)
	}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"params"`
		}
		if err := decoder.Decode(&request); err != nil {
			if err != io.EOF {
				os.Exit(92)
			}
			if marker := os.Getenv("HEXAGON_MCP_DIAGNOSTIC_MARKER"); marker != "" {
				_ = os.WriteFile(marker, []byte("closed"), 0600)
			}
			os.Exit(0)
		}
		if len(request.ID) == 0 {
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
		if request.Method == "initialize" && mode == "initialize-error" || request.Method == "tools/list" && mode == "tools-list-error" {
			response["error"] = map[string]any{"code": -32603, "message": "fixture rejected request"}
		} else if request.Method == "initialize" {
			version := request.Params.ProtocolVersion
			if mode == "unsupported-version" || mode == "inherited-stderr" {
				version = "unsupported-fixture-version"
			}
			response["result"] = map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fixture", "version": "1"},
			}
		} else {
			response["result"] = map[string]any{"tools": []any{}}
		}
		if err := encoder.Encode(response); err != nil {
			os.Exit(93)
		}
	}
}
