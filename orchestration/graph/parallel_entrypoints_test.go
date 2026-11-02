package graph

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/interrupt"
)

func additionalParallelHandler(t *testing.T, mode string, handlers []NodeHandler[TestState]) NodeHandler[TestState] {
	t.Helper()
	if mode == "mapreduce" {
		return MapReduceNode("group", func(state TestState) []TestState {
			states := make([]TestState, len(handlers))
			for i := range states {
				states[i] = state
				states[i].Counter = i
			}
			return states
		}, func(ctx context.Context, state TestState) (TestState, error) {
			return handlers[state.Counter](ctx, state)
		}, func(state TestState, _ []TestState) TestState { return state }, 3).Handler
	}
	builder := NewGraph[TestState]("pregel-group").WithExecutionMode(GraphModePregel).
		WithStateMerger(func(state TestState, _ []TestState) TestState { return state }).
		AddNode("start", func(_ context.Context, state TestState) (TestState, error) { return state, nil }).AddEdge(START, "start")
	for i, handler := range handlers {
		name := fmt.Sprintf("branch_%d", i)
		builder.AddNode(name, handler).AddEdge("start", name).AddEdge(name, END)
	}
	graph, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, state TestState) (TestState, error) { return graph.Run(ctx, state) }
}

func TestAdditionalParallelGroupsPreserveOutcomesAndRetry(t *testing.T) {
	for _, mode := range []string{"mapreduce", "pregel"} {
		for _, mixed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/mixed=%t", mode, mixed), func(t *testing.T) {
				ordinary, unknown := errors.New("ordinary"), errors.New("unknown receipt")
				var calls [3]atomic.Int32
				handlers := make([]NodeHandler[TestState], 3)
				for i := range handlers {
					handlers[i] = func(_ context.Context, state TestState) (TestState, error) {
						attempt := calls[i].Add(1)
						if mixed {
							switch i {
							case 1:
								state.Counter = 7
								return state, nil
							case 2:
								return state, parallelUnknownError{unknown}
							}
						}
						if attempt == 1 || mixed {
							return state, ordinary
						}
						return state, nil
					}
				}
				node := NewNode("group", additionalParallelHandler(t, mode, handlers)).WithRetry(&RetryPolicy{MaxRetries: 1}).Build()
				outer, err := NewGraph[TestState]("outer").AddNodeWithBuilder(node).AddEdge(START, "group").AddEdge("group", END).Build()
				if err != nil {
					t.Fatal(err)
				}
				_, err = outer.Run(t.Context(), TestState{})
				wantCalls := int32(2)
				if mixed {
					wantCalls = 1
					var result *ParallelExecutionError[TestState]
					if !errors.As(err, &result) || !errors.Is(err, ordinary) || !errors.Is(err, unknown) || !llm.IsUnsafeToReplay(err) || len(result.Results) != 3 || result.Results[1].State.Counter != 7 {
						t.Fatalf("parallel facts lost: %+v %v", result, err)
					}
				} else if err != nil {
					t.Fatalf("ordinary failures did not retry successfully: %v", err)
				}
				for i := range calls {
					if calls[i].Load() != wantCalls {
						t.Fatalf("branch %d: calls=%d, want %d", i, calls[i].Load(), wantCalls)
					}
				}
			})
		}
	}
}

func TestPregelMixedFailureIsNotPureWaiting(t *testing.T) {
	ordinary := errors.New("ordinary failure")
	handler := additionalParallelHandler(t, "pregel", []NodeHandler[TestState]{
		func(_ context.Context, s TestState) (TestState, error) { return s, ordinary },
		func(ctx context.Context, s TestState) (TestState, error) {
			return s, interrupt.InterruptSignalFunc(ctx, "wait")
		},
	})
	graph, err := NewGraph[TestState]("mixed-wrapper").AddNode("mixed", handler).AddEdge(START, "mixed").AddEdge("mixed", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	runner := NewCheckpointRunner(graph, NewMemoryEnhancedCheckpointSaver(), nil)
	_, err = runner.Run(t.Context(), "mixed", TestState{})
	if !errors.Is(err, ordinary) {
		t.Fatalf("ordinary branch failure lost: %v", err)
	}
	if _, found := interrupt.IsInterruptSignal(err); !found {
		t.Fatalf("waiting branch fact lost: %v", err)
	}
	if runner.GetCurrentCheckpoint().Status != CheckpointStatusFailed {
		t.Fatalf("mixed failure incorrectly classified as pure waiting: %s", runner.GetCurrentCheckpoint().Status)
	}
}

func TestParallelForEachPanicCannotBeSwallowedByMerger(t *testing.T) {
	var calls atomic.Int32
	node := ParallelForEachLoopNode("items", func(TestState) []int { return []int{1} }, func(context.Context, int, int) error {
		calls.Add(1)
		panic("callback failed")
	}, func(state TestState, _ []error) (TestState, error) { return state, nil }, 1)
	node.RetryPolicy = &RetryPolicy{MaxRetries: 1}
	graph, err := NewGraph[TestState]("foreach").AddNodeWithBuilder(node).AddEdge(START, "items").AddEdge("items", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	_, err = graph.Run(t.Context(), TestState{})
	var failure *NodePanicError
	if !errors.As(err, &failure) || failure.Node != "items[0]" || !llm.IsUnsafeToReplay(err) || calls.Load() != 1 {
		t.Fatalf("worker panic lost or replayed: %v calls=%d", err, calls.Load())
	}
}

func TestParallelForEachOrdinaryFailuresKeepRetry(t *testing.T) {
	var calls [2]atomic.Int32
	node := ParallelForEachLoopNode("items", func(TestState) []int { return []int{0, 1} }, func(_ context.Context, item, _ int) error {
		if calls[item].Add(1) == 1 {
			return errors.New("ordinary failure")
		}
		return nil
	}, func(state TestState, results []error) (TestState, error) { return state, errors.Join(results...) }, 2)
	node.RetryPolicy = &RetryPolicy{MaxRetries: 1}
	graph, err := NewGraph[TestState]("foreach-retry").AddNodeWithBuilder(node).AddEdge(START, "items").AddEdge("items", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.Run(t.Context(), TestState{}); err != nil || calls[0].Load() != 2 || calls[1].Load() != 2 {
		t.Fatalf("ordinary failures did not retry: %v calls=%d,%d", err, calls[0].Load(), calls[1].Load())
	}
}

func TestParallelForEachCancellationPreservesCompletedItems(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var observed []error
	node := ParallelForEachLoopNode("items", func(TestState) []int { return []int{0, 1} }, func(_ context.Context, item, _ int) error {
		if item == 1 {
			cancel()
		}
		return nil
	}, func(state TestState, results []error) (TestState, error) { observed = results; return state, nil }, 1)
	_, err := node.Handler(ctx, TestState{})
	if !errors.Is(err, context.Canceled) || len(observed) != 2 || observed[0] != nil || !errors.Is(observed[1], context.Canceled) {
		t.Fatalf("cancellation overwrote completed result: %v results=%v", err, observed)
	}
}

func TestPregelObserverFailureWaitsForDispatchedBranch(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	started, observed, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	observerError := errors.New("observer failure")
	graph, err := NewGraph[TestState]("observer").WithExecutionMode(GraphModePregel).
		AddNode("start", func(_ context.Context, s TestState) (TestState, error) { return s, nil }).
		AddNode("a", func(ctx context.Context, s TestState) (TestState, error) {
			close(started)
			select {
			case <-release:
				return s, nil
			case <-ctx.Done():
				return s, ctx.Err()
			}
		}).AddNode("b", func(_ context.Context, s TestState) (TestState, error) {
		t.Error("undispatched branch ran")
		return s, nil
	}).
		AddEdge(START, "start").AddEdge("start", "a").AddEdge("start", "b").AddEdge("a", END).AddEdge("b", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	executor := newPlanExecutor(graph, TestState{})
	executor.observe = func(event StreamEvent[TestState]) error {
		if event.Type == EventTypeNodeStart && event.NodeName == "b" {
			select {
			case <-started:
			case <-ctx.Done():
				return ctx.Err()
			}
			close(observed)
			return observerError
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { _, err := executor.run(ctx); done <- err }()
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal("observer did not run")
	}
	select {
	case err := <-done:
		t.Fatalf("execution returned before dispatched branch settled: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	released = true
	err = <-done
	if !errors.Is(err, observerError) || !llm.IsUnsafeToReplay(err) {
		t.Fatalf("observer or completed branch fact lost: %v", err)
	}
}
