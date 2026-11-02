package graph

import (
	"context"
	"errors"
	"testing"
)

func TestMapStateClonePreservesTypesAndIsolation(t *testing.T) {
	type object struct{ Values []int }
	input := MapState{"typed": []int{1, 2}, "object": &object{Values: []int{3}}}
	value, err := input.Clone()
	if err != nil {
		t.Fatal(err)
	}
	got := value.(MapState)
	got["typed"].([]int)[0] = 10
	got["object"].(*object).Values[0] = 20
	if input["typed"].([]int)[0] != 1 || input["object"].(*object).Values[0] != 3 {
		t.Fatal("cloned state changed original references")
	}
}

func TestParallelNodeCloneFailureDoesNotDispatch(t *testing.T) {
	called := false
	node := ParallelNode[MapState]("resource", func(_ context.Context, s MapState) (MapState, error) {
		called = true
		return s, nil
	})
	if _, err := node.Handler(t.Context(), MapState{"resource": make(chan int)}); err == nil {
		t.Fatal("expected explicit clone error")
	}
	if called {
		t.Fatal("handler ran with an input that could not be isolated")
	}
}

func TestParallelNodePanicReturnsTaskFailure(t *testing.T) {
	node := ParallelNode[MapState]("panic", func(context.Context, MapState) (MapState, error) {
		panic("extension failed")
	})
	_, err := node.Handler(t.Context(), MapState{})
	var failure *NodePanicError
	if !errors.As(err, &failure) || failure.Node != "panic[0]" || len(failure.Stack) == 0 {
		t.Fatalf("error = %v, want located panic failure", err)
	}
}
