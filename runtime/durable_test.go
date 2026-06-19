package runtime

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/checkpoint"
)

func TestSnapshotState_AndRestore(t *testing.T) {
	st := &State{
		Turn:      3,
		Messages:  []llm.Message{{Role: "user", Content: "hi"}},
		ToolCalls: []ToolCallRecord{{}},
		Usage:     llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		Final:     true,
		FinalText: "done",
	}
	snap := SnapshotState(st, "run-1")
	if snap.RunID != "run-1" || snap.Step != 3 || !snap.Final || snap.FinalText != "done" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if snap.Usage.TotalTokens != 15 || len(snap.Messages) != 1 || len(snap.ToolCalls) != 1 {
		t.Fatalf("snapshot content mismatch: %+v", snap)
	}

	rs := snap.RestoreState()
	if rs.Turn != 3 || len(rs.Messages) != 1 || rs.Usage.TotalTokens != 15 || !rs.Final || rs.FinalText != "done" {
		t.Fatalf("restore mismatch: %+v", rs)
	}
	if rs.Attributes == nil {
		t.Error("restored State.Attributes should be initialized")
	}

	// Snapshot must be decoupled from the evolving source State.
	st.Messages[0].Content = "mutated"
	if snap.Messages[0].Content != "hi" {
		t.Error("snapshot shares backing array with source State")
	}
}

func TestSnapshotState_NilState(t *testing.T) {
	snap := SnapshotState(nil, "run-z")
	if snap.RunID != "run-z" || snap.Step != 0 {
		t.Errorf("nil state should yield empty snapshot with RunID, got %+v", snap)
	}
}

func TestDurableExecution_SaveLoadLatest(t *testing.T) {
	de := NewDurableExecution(checkpoint.NewMemory())
	ctx := context.Background()

	if _, ok, err := de.Load(ctx, "run-x"); err != nil || ok {
		t.Fatalf("expected empty load, ok=%v err=%v", ok, err)
	}

	for step := 0; step < 3; step++ {
		if err := de.Save(ctx, Snapshot{RunID: "run-x", Step: step, Usage: llm.Usage{TotalTokens: step * 10}}); err != nil {
			t.Fatalf("save step %d: %v", step, err)
		}
	}
	got, ok, err := de.Load(ctx, "run-x")
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.Step != 2 || got.Usage.TotalTokens != 20 {
		t.Errorf("expected latest step 2 / 20 tokens, got %+v", got)
	}
}

func TestDurableExecution_SaveRequiresRunID(t *testing.T) {
	de := NewDurableExecution(checkpoint.NewMemory())
	if err := de.Save(context.Background(), Snapshot{Step: 1}); err == nil {
		t.Error("expected error for empty RunID (precondition)")
	}
}

func TestDurableExecution_IdempotentStepOverwrite(t *testing.T) {
	de := NewDurableExecution(checkpoint.NewMemory())
	ctx := context.Background()
	_ = de.Save(ctx, Snapshot{RunID: "r", Step: 1, FinalText: "v1"})
	_ = de.Save(ctx, Snapshot{RunID: "r", Step: 1, FinalText: "v2"}) // same (RunID, Step) overwrites
	got, _, _ := de.Load(ctx, "r")
	if got.FinalText != "v2" {
		t.Errorf("expected idempotent overwrite to v2, got %q", got.FinalText)
	}
}

func TestNewDurableExecution_NilCheckpointerPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on nil Checkpointer")
		}
	}()
	NewDurableExecution(nil)
}
