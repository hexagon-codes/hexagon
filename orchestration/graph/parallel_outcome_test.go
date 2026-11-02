package graph

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

type parallelUnknownError struct{ cause error }

func (e parallelUnknownError) Error() string      { return e.cause.Error() }
func (e parallelUnknownError) Unwrap() error      { return e.cause }
func (parallelUnknownError) UnsafeToReplay() bool { return true }

func TestParallelGroupsPreserveAllOutcomesWithoutRetry(t *testing.T) {
	builders := map[string]func([]NodeHandler[TestState]) NodeHandler[TestState]{
		"node": func(handlers []NodeHandler[TestState]) NodeHandler[TestState] {
			return ParallelNodeWithMerger("group", func(s TestState, _ []TestState) TestState { return s }, handlers...).Handler
		},
		"subgraphs": func(handlers []NodeHandler[TestState]) NodeHandler[TestState] {
			var subgraphs []*Graph[TestState]
			for _, handler := range handlers {
				sub, err := NewGraph[TestState]("child").AddNode("work", handler).AddEdge(START, "work").AddEdge("work", END).Build()
				if err != nil {
					t.Fatal(err)
				}
				subgraphs = append(subgraphs, sub)
			}
			return ParallelSubgraphs("group", subgraphs, func(s TestState, _ []TestState) TestState { return s }).Handler
		},
		"tasks": func(handlers []NodeHandler[TestState]) NodeHandler[TestState] {
			wf := NewWorkflow[TestState]("group")
			var tasks []*TaskDef[TestState]
			for _, handler := range handlers {
				tasks = append(tasks, DefineTask(wf, "child", handler))
			}
			return func(ctx context.Context, s TestState) (TestState, error) { return RunParallel(ctx, s, nil, tasks...) }
		},
	}
	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			ordinary, unknown := errors.New("ordinary"), errors.New("unknown receipt")
			var calls [3]atomic.Int32
			handlers := []NodeHandler[TestState]{
				func(_ context.Context, s TestState) (TestState, error) { calls[0].Add(1); return s, ordinary },
				func(_ context.Context, s TestState) (TestState, error) { calls[1].Add(1); s.Counter = 7; return s, nil },
				func(_ context.Context, s TestState) (TestState, error) {
					calls[2].Add(1)
					return s, parallelUnknownError{unknown}
				},
			}
			node := NewNode("group", build(handlers)).WithRetry(&RetryPolicy{MaxRetries: 1}).Build()
			graph, err := NewGraph[TestState]("outer").AddNodeWithBuilder(node).AddEdge(START, "group").AddEdge("group", END).Build()
			if err != nil {
				t.Fatal(err)
			}
			_, err = graph.Run(t.Context(), TestState{})
			var group *ParallelExecutionError[TestState]
			if !errors.As(err, &group) || !errors.Is(err, ordinary) || !errors.Is(err, unknown) || !llm.IsUnsafeToReplay(err) {
				t.Fatalf("lost group facts: %v", err)
			}
			if len(group.Results) != 3 || group.Results[1].Error != nil || group.Results[1].State.Counter != 7 {
				t.Fatalf("successful branch lost: %+v", group.Results)
			}
			for i := range calls {
				if calls[i].Load() != 1 {
					t.Fatalf("branch %d repeated %d times", i, calls[i].Load())
				}
			}
		})
	}
}

func TestParallelGroupKnownFailuresKeepRetryPolicy(t *testing.T) {
	var calls [2]atomic.Int32
	handler := func(i int) NodeHandler[TestState] {
		return func(_ context.Context, state TestState) (TestState, error) {
			if calls[i].Add(1) == 1 {
				return state, errors.New("known retryable failure")
			}
			return state, nil
		}
	}
	node := ParallelNodeWithMerger("group", func(s TestState, _ []TestState) TestState { return s }, handler(0), handler(1))
	node.RetryPolicy = &RetryPolicy{MaxRetries: 1}
	graph, err := NewGraph[TestState]("known-failure-retry").AddNodeWithBuilder(node).AddEdge(START, "group").AddEdge("group", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = graph.Run(t.Context(), TestState{}); err != nil {
		t.Fatal(err)
	}
	if calls[0].Load() != 2 || calls[1].Load() != 2 {
		t.Fatalf("retry disabled: %d %d", calls[0].Load(), calls[1].Load())
	}
}
