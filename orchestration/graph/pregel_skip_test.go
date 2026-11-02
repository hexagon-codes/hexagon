package graph

import (
	"context"
	"testing"

	"github.com/hexagon-codes/hexagon/interrupt"
)

func TestPregelAllSkippedBranchSurvivesResume(t *testing.T) {
	for _, mode := range []GraphMode{GraphModeDAG, GraphModePregel} {
		calls := map[string]int{}
		handler := func(name string) NodeHandler[TestState] {
			return func(_ context.Context, s TestState) (TestState, error) { calls[name]++; return s, nil }
		}
		g, err := NewGraph[TestState]("conditional-join").WithExecutionMode(mode).WithTriggerMode(TriggerAllPredecessors).
			AddNode("route", handler("route")).AddNode("a", func(ctx context.Context, s TestState) (TestState, error) {
			resuming, _, _ := interrupt.GetResumeContext[any](ctx)
			if !resuming {
				return s, interrupt.InterruptSignalFunc(ctx, "pause selected branch")
			}
			calls["a"]++
			return s, nil
		}).
			AddNode("b", handler("b")).AddNode("join", handler("join")).AddEdge(START, "route").AddConditionalEdge("route", func(TestState) string { return "a" }, map[string]string{"a": "a", "b": "b"}).AddEdge("a", "join").AddEdge("b", "join").AddEdge("join", END).Build()
		if err != nil {
			t.Fatal(err)
		}
		saver := NewMemoryEnhancedCheckpointSaver()
		runner := NewCheckpointRunner(g, saver, nil)
		_, err = runner.Run(t.Context(), "skip-resume", TestState{})
		signal, ok := interrupt.IsInterruptSignal(err)
		if !ok {
			t.Fatalf("missing wait: %v", err)
		}
		target := ""
		for _, point := range interrupt.ToInterruptContexts(signal) {
			if point.IsRoot {
				target = point.ID
			}
		}
		_, err = NewCheckpointRunner(g, saver, nil).ResumeTargets(t.Context(), runner.GetCurrentCheckpoint().ID, map[string]any{target: nil})
		if err != nil || calls["route"] != 1 || calls["a"] != 1 || calls["b"] != 0 || calls["join"] != 1 {
			t.Fatalf("mode=%v calls=%v err=%v", mode, calls, err)
		}
	}
}

func TestPregelAllSkippedReturnEdgeDoesNotBlockSelectedReturn(t *testing.T) {
	entryCalls := 0
	identity := func(_ context.Context, s TestState) (TestState, error) { return s, nil }
	g, err := NewGraph[TestState]("conditional-loop").WithExecutionMode(GraphModePregel).WithTriggerMode(TriggerAllPredecessors).
		WithStateMerger(func(s TestState, _ []TestState) TestState { return s }).
		AddNode("entry", func(_ context.Context, s TestState) (TestState, error) { entryCalls++; s.Counter++; return s, nil }).AddNode("a", identity).AddNode("b", identity).
		AddEdge(START, "entry").AddEdge("entry", "a").AddEdge("entry", "b").
		AddConditionalEdge("a", func(s TestState) string {
			if s.Counter < 2 {
				return "again"
			}
			return "done"
		}, map[string]string{"again": "entry", "done": END}).
		AddConditionalEdge("b", func(TestState) string { return "done" }, map[string]string{"again": "entry", "done": END}).Build()
	if err != nil {
		t.Fatal(err)
	}
	state, err := g.Run(t.Context(), TestState{})
	if err != nil || entryCalls != 2 || state.Counter != 2 {
		t.Fatalf("conditional loop did not settle: %+v calls=%d err=%v", state, entryCalls, err)
	}
}
