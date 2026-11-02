package graph

import (
	"context"
	"errors"
	"testing"
)

func TestFunctionalTaskPanicReturnsFailure(t *testing.T) {
	for _, cached := range []bool{false, true} {
		name := "without cache"
		if cached {
			name = "with cache"
		}
		t.Run(name, func(t *testing.T) {
			wf := NewWorkflow[MapState]("task-boundary")
			calls := 0
			var options []TaskOption[MapState]
			if cached {
				options = append(options, WithTaskCache[MapState](NewMemoryNodeCache()))
			}
			task := DefineTask(wf, "failing-task", func(_ context.Context, state MapState) (MapState, error) {
				calls++
				state["value"] = "partial"
				panic("task callback failed")
			}, options...)
			input := MapState{"value": "original"}
			for attempt := 1; attempt <= 2; attempt++ {
				result, err := task.Run(t.Context(), input)
				var failure *NodePanicError
				if !errors.As(err, &failure) || failure.Node != "failing-task" || result["value"] != "original" || input["value"] != "original" || calls != attempt {
					t.Fatalf("panic became success or entered cache: result=%v input=%v calls=%d error=%v", result, input, calls, err)
				}
			}
		})
	}
}

func TestFunctionalWorkflowPanicStopsDispatch(t *testing.T) {
	wf := NewWorkflow[MapState]("entry-boundary")
	dispatched := 0
	task := DefineTask(wf, "downstream", func(_ context.Context, state MapState) (MapState, error) {
		dispatched++
		return state, nil
	})
	DefineEntrypoint(wf, func(_ context.Context, state MapState) (MapState, error) {
		state["value"] = "partial"
		panic("entrypoint failed")
	})
	input := MapState{"value": "original"}
	result, err := wf.Run(t.Context(), input)
	if err == nil {
		_, err = task.Run(t.Context(), result)
	}
	var failure *NodePanicError
	if !errors.As(err, &failure) || failure.Node != "entry-boundary entrypoint" || dispatched != 0 || input["value"] != "original" || result["value"] != "original" {
		t.Fatalf("entrypoint panic escaped its error contract: result=%v dispatched=%d error=%v", result, dispatched, err)
	}
}

func TestFunctionalParallelMergerPanicReturnsFailure(t *testing.T) {
	wf := NewWorkflow[MapState]("merge-boundary")
	task := DefineTask(wf, "source", func(_ context.Context, state MapState) (MapState, error) {
		state["value"] = "branch"
		return state, nil
	})
	input := MapState{"value": "original"}
	result, err := RunParallel(t.Context(), input, func(state MapState, results []MapState) MapState {
		state["value"] = results[0]["value"]
		panic("merger failed")
	}, task)
	var failure *NodePanicError
	if !errors.As(err, &failure) || failure.Node != "parallel task merger" || input["value"] != "original" || result["value"] != "original" {
		t.Fatalf("merger panic adopted a partial result: result=%v input=%v error=%v", result, input, err)
	}
}

func TestFunctionalConditionalPanicDoesNotDispatch(t *testing.T) {
	wf := NewWorkflow[MapState]("route-boundary")
	dispatched := 0
	task := DefineTask(wf, "target", func(_ context.Context, state MapState) (MapState, error) {
		dispatched++
		return state, nil
	})
	input := MapState{"value": "original"}
	result, err := RunConditional(t.Context(), input, func(state MapState) string {
		state["value"] = "partial"
		panic("router failed")
	}, map[string]*TaskDef[MapState]{"target": task})
	var failure *NodePanicError
	if !errors.As(err, &failure) || failure.Node != "conditional task route" || dispatched != 0 || input["value"] != "original" || result["value"] != "original" {
		t.Fatalf("router panic dispatched or adopted output: result=%v dispatched=%d error=%v", result, dispatched, err)
	}
}

type panicIdentityCodec struct {
	JSONStateCodec[TestState]
	method string
}

func (c panicIdentityCodec) Type() string {
	if c.method == "type" {
		panic("codec type failed")
	}
	return "test-state"
}

func (c panicIdentityCodec) Version() string {
	if c.method == "version" {
		panic("codec version failed")
	}
	return "1"
}

func TestCompileCodecIdentityPanicAllEntrypoints(t *testing.T) {
	for _, method := range []string{"type", "version"} {
		for _, entry := range []string{"builder", "compile", "dynamic"} {
			t.Run(method+"/"+entry, func(t *testing.T) {
				g := &Graph[TestState]{Name: "identity-boundary", stateCodec: panicIdentityCodec{method: method},
					Nodes: map[string]*Node[TestState]{START: StartNode[TestState](), END: EndNode[TestState]()},
					Edges: []*Edge{{From: START, To: END}}}
				var err error
				switch entry {
				case "builder":
					_, err = (&GraphBuilder[TestState]{graph: g}).Build()
				case "compile":
					_, err = Compile(g)
				case "dynamic":
					_, err = (&DynamicGraph[TestState]{Graph: g}).Build()
				}
				var failure *NodePanicError
				if !errors.As(err, &failure) || failure.Node != "graph definition identity-boundary" || g.compiled || g.codecType != "" || g.codecVersion != "" {
					t.Fatalf("codec panic escaped or published partial identity: compiled=%v type=%q version=%q error=%v", g.compiled, g.codecType, g.codecVersion, err)
				}
			})
		}
	}
}
