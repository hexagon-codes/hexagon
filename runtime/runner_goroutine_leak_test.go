package runtime

import (
	goruntime "runtime"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// TestRunner_NoGoroutineLeakAcrossRuns is the goroutine-leak (method 3, goleak-
// equivalent without the extra dependency) closed loop. It runs the runner many
// times — including the MaxTurns tool-calling path that was just changed — and
// asserts the live goroutine count does not grow unboundedly. A leak in tool
// execution / event emission would show up as monotonic growth.
func TestRunner_NoGoroutineLeakAcrossRuns(t *testing.T) {
	newRunner := func() Runner {
		return NewRunner(Config{
			ProviderSelector: StaticProviderSelector{Provider: &loopingProvider{}, Name: "loop", Model: "m"},
			ToolExecutor:     &noopToolExec{},
			DefaultMaxTurns:  3,
		})
	}

	// warm up so lazily-started goroutines (pools, etc.) exist before baseline.
	for range 5 {
		_, _ = newRunner().Run(t.Context(), Request{
			ID:       "warmup",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
			Limits:   Limits{MaxTurns: 3},
		})
	}
	goruntime.GC()
	base := goruntime.NumGoroutine()

	const N = 60
	for range N {
		_, _ = newRunner().Run(t.Context(), Request{
			ID:       "leak-probe",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
			Limits:   Limits{MaxTurns: 3},
		})
	}
	goruntime.GC()
	after := goruntime.NumGoroutine()

	// Allow a small slack for scheduler/GC workers; a real leak would be ~N.
	if after > base+10 {
		t.Fatalf("goroutine leak suspected: base=%d after %d runs=%d (grew by %d)", base, N, after, after-base)
	}
	t.Logf("goroutine count stable: base=%d after=%d (N=%d runs)", base, after, N)
}
