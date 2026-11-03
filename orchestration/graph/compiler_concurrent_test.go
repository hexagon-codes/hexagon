package graph

import (
	"context"
	"sync"
	"testing"
)

func TestCompiledGraphRunIsConcurrentSafe(t *testing.T) {
	graph, err := NewGraph[TestState]("compiled-concurrent").
		AddNode("increment", func(_ context.Context, state TestState) (TestState, error) {
			state.Counter++
			return state, nil
		}).
		AddEdge(START, "increment").
		AddEdge("increment", END).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(graph)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			state, err := compiled.Run(context.Background(), TestState{Counter: index})
			if err != nil {
				t.Errorf("run %d: %v", index, err)
				return
			}
			if state.Counter != index+1 {
				t.Errorf("run %d state=%+v", index, state)
			}
		}(index)
	}
	wait.Wait()
	compiled.statsMu.Lock()
	total := compiled.Stats.TotalExecutions
	compiled.statsMu.Unlock()
	if total != 2 {
		t.Fatalf("total executions=%d", total)
	}
}
