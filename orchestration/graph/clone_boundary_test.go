package graph

import (
	"context"
	"errors"
	"github.com/hexagon-codes/ai-core/llm"
	"sync/atomic"
	"testing"
)

type cloneBoundaryState struct {
	counter *atomic.Int32
	failAt  int32
}

func (s cloneBoundaryState) Clone() (State, error) {
	if s.counter.Add(1) == s.failAt {
		panic("copy failed")
	}
	return s, nil
}
func TestStateClonePanicAllExecutionEntrypoints(t *testing.T) {
	for _, mode := range []string{"run", "stream", "streamrun"} {
		for _, at := range []int32{2, 3} {
			t.Run(mode+string(rune('0'+at)), func(t *testing.T) {
				g, e := NewGraph[cloneBoundaryState]("clone").AddNode("work", func(_ context.Context, s cloneBoundaryState) (cloneBoundaryState, error) { return s, nil }).AddEdge(START, "work").AddEdge("work", END).Build()
				if e != nil {
					t.Fatal(e)
				}
				state := cloneBoundaryState{counter: &atomic.Int32{}, failAt: at}
				var failure error
				switch mode {
				case "run":
					_, failure = g.Run(t.Context(), state)
				case "stream":
					events, e := g.Stream(t.Context(), state)
					if e != nil {
						t.Fatal(e)
					}
					for event := range events {
						if event.Type == EventTypeError {
							failure = event.Error
						}
					}
				default:
					events, e := g.StreamRun(t.Context(), state)
					if e != nil {
						t.Fatal(e)
					}
					found := false
					for event := range events.Events() {
						if event.Type == EventNodeError {
							found = true
							if stack, ok := event.Metadata["stack"].(string); !ok || stack == "" {
								t.Fatal("panic stack lost from stream terminal")
							}
						}
					}
					if !found {
						t.Fatal("missing error terminal")
					}
					return
				}
				var p *NodePanicError
				if !errors.As(failure, &p) || len(p.Stack) == 0 || !llm.IsUnsafeToReplay(failure) {
					t.Fatalf("failure=%v", failure)
				}
			})
		}
	}
}
