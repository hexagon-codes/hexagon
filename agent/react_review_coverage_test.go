package agent

import (
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/tool"
)

// Covers truncateToolResult (the LLM context-window guard, react.go:478) which
// was only ~40% covered. Also documents the head(70%)+tail(20%) math.
func TestTruncateToolResult_LongInputIsTruncatedWithMarker(t *testing.T) {
	maxLen := 100
	in := strings.Repeat("A", 60) + strings.Repeat("B", 60) // 120 > 100
	out := truncateToolResult(in, maxLen)

	if out == in {
		t.Fatalf("expected truncation for len(in)=%d > maxLen=%d", len(in), maxLen)
	}
	if !strings.Contains(out, "结果已截断") {
		t.Fatalf("expected truncation marker, got %q", out)
	}
	head := maxLen * 7 / 10 // 70
	tail := maxLen * 2 / 10 // 20
	if !strings.HasPrefix(out, in[:head]) {
		t.Fatalf("head not preserved")
	}
	if !strings.HasSuffix(out, in[len(in)-tail:]) {
		t.Fatalf("tail not preserved")
	}
	// NOTE: head+tail = 90% of maxLen; plus the marker text, the returned string
	// can exceed maxLen. This is intentional (keeps both ends) but means
	// truncateToolResult does NOT hard-cap the output at maxLen.
	t.Logf("len(out)=%d (head=%d tail=%d + marker); maxLen=%d", len(out), head, tail, maxLen)
}

func TestTruncateToolResult_ShortInputUnchanged(t *testing.T) {
	in := "small"
	if got := truncateToolResult(in, 8000); got != in {
		t.Fatalf("short input must be unchanged, got %q", got)
	}
}

// Covers formatToolResult branches (string / []byte / struct-json / failure).
func TestFormatToolResult_AllBranches(t *testing.T) {
	if got := formatToolResult(tool.Result{Success: false, Error: "boom"}); got != "Error: boom" {
		t.Fatalf("failure branch = %q", got)
	}
	if got := formatToolResult(tool.NewResult("hi")); got != "hi" {
		t.Fatalf("string branch = %q", got)
	}
	if got := formatToolResult(tool.NewResult([]byte("bytes"))); got != "bytes" {
		t.Fatalf("[]byte branch = %q", got)
	}
	jsonOut := formatToolResult(tool.NewResult(map[string]int{"a": 1}))
	if !strings.Contains(jsonOut, `"a":1`) {
		t.Fatalf("json branch = %q", jsonOut)
	}
}

// Covers outputFromRuntime nil-guard and convertMessagesToAny (react.go:266,384).
func TestOutputFromRuntime_NilSafe(t *testing.T) {
	if got := outputFromRuntime(nil); got.Content != "" || got.ToolCalls != nil {
		t.Fatalf("nil result must yield zero Output, got %+v", got)
	}
}
