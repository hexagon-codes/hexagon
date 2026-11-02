package graph

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hexagon-codes/hexagon/interrupt"
)

func TestGraphPlanModeIsSelectedBeforeBuild(t *testing.T) {
	build := func(mode GraphMode) (*Graph[TestState], error) {
		return NewGraph[TestState]("loop-plan").WithExecutionMode(mode).AddNode("loop", func(_ context.Context, s TestState) (TestState, error) { s.Counter++; return s, nil }).AddEdge(START, "loop").AddConditionalEdge("loop", func(s TestState) string {
			if s.Counter < 3 {
				return "again"
			}
			return "done"
		}, map[string]string{"again": "loop", "done": END}).Build()
	}
	if _, err := build(GraphModeDAG); err == nil {
		t.Fatal("DAG accepted a cycle")
	}
	g, err := build(GraphModePregel)
	if err != nil {
		t.Fatal(err)
	}
	state, err := g.Run(context.Background(), TestState{})
	if err != nil || state.Counter != 3 {
		t.Fatalf("Run=%+v, %v", state, err)
	}
	var final TestState
	events, err := g.Stream(context.Background(), TestState{})
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if event.Error != nil {
			t.Fatal(event.Error)
		}
		if event.Type == EventTypeEnd {
			final = event.State
		}
	}
	if final.Counter != 3 {
		t.Fatalf("Stream counter=%d", final.Counter)
	}
	dag, err := NewGraph[TestState]("dag").AddNode("a", func(_ context.Context, s TestState) (TestState, error) { return s, nil }).AddEdge(START, "a").AddEdge("a", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := dag.RunPregelMode(context.Background(), TestState{}); err == nil {
		t.Fatal("RunPregelMode silently switched the compiled mode")
	}
}

func TestGraphDAGDispatchSkipAndEndUseWholeFrontier(t *testing.T) {
	calls := map[string]int{}
	handler := func(name string) NodeHandler[TestState] {
		return func(_ context.Context, s TestState) (TestState, error) { calls[name]++; s.Path += name; return s, nil }
	}
	g, err := NewGraph[TestState]("branch-frontier").AddNode("a", handler("a")).AddNode("b", handler("b")).AddNode("unused", handler("unused")).AddNode("join", handler("join")).AddEdge(START, "a").AddEdge(START, "b").AddEdge(START, "b").AddConditionalEdge(START, func(TestState) string { return "a" }, map[string]string{"a": "a", "b": "b", "unused": "unused"}).AddEdge("a", END).AddEdge("b", "join").AddEdge("unused", "join").AddEdge("join", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.Run(context.Background(), TestState{})
	if err != nil {
		t.Fatal(err)
	}
	if calls["a"] != 1 || calls["b"] != 1 || calls["unused"] != 0 || calls["join"] != 1 {
		t.Fatalf("wrong activation counts: %v", calls)
	}
}

type mappedContractState struct {
	X, Y  int
	Items []string
}

func (s mappedContractState) Clone() (State, error) {
	s.Items = append([]string(nil), s.Items...)
	return s, nil
}

type mappedContractOutput struct {
	Value int
	Items []string
}

func TestMappedParallelWritesAdoptInPlanOrderOnce(t *testing.T) {
	releaseA := make(chan struct{})
	a := NewMappedNode[mappedContractState, struct{}, mappedContractOutput]("a", func(context.Context, struct{}) (mappedContractOutput, error) {
		<-releaseA
		return mappedContractOutput{Value: 1, Items: []string{"A"}}, nil
	}, nil, []FieldMapping{{From: "Value", To: "X"}, {From: "Items", To: "Items"}})
	b := NewMappedNode[mappedContractState, struct{}, mappedContractOutput]("b", func(context.Context, struct{}) (mappedContractOutput, error) {
		return mappedContractOutput{Value: 2, Items: []string{"B"}}, nil
	}, nil, []FieldMapping{{From: "Value", To: "Y"}, {From: "Items", To: "Items"}})
	g, err := NewGraph[mappedContractState]("mapped-parallel").WithExecutionMode(GraphModePregel).WithFieldReducer("Items", func(current, incoming any) (any, error) {
		return append(current.([]string), incoming.([]string)...), nil
	}).AddNodeWithBuilder(a).AddNodeWithBuilder(b).AddEdge(START, "a").AddEdge(START, "b").AddEdge("a", END).AddEdge("b", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	x := newPlanExecutor(g, mappedContractState{})
	savedB := false
	x.persist = func(_ context.Context, _ mappedContractState, f *graphFrontier) error {
		if f.Activation == 1 && f.Results[x.taskID("b")] != nil && !savedB {
			if f.Results[x.taskID("a")] != nil {
				t.Error("A completed before B was saved")
			}
			savedB = true
			close(releaseA)
		}
		return nil
	}
	result, err := x.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.X != 1 || result.Y != 2 || !reflect.DeepEqual(result.Items, []string{"A", "B"}) {
		t.Fatalf("writes overwritten or reordered: %+v", result)
	}
	for _, result := range x.frontier.Results {
		if !result.Adopted {
			t.Fatalf("task not adopted: %s", result.TaskID)
		}
	}
	before := append([]string(nil), result.Items...)
	if _, err := x.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(x.state.Items, before) {
		t.Fatal("reducer applied twice")
	}
}

func TestGraphInterfaceMismatchStopsBeforeDownstreamHandler(t *testing.T) {
	producerCalls, consumerCalls := 0, 0
	producer := NewMappedNode[MapState, struct{}, any]("produce", func(context.Context, struct{}) (any, error) { producerCalls++; return "not-an-int", nil }, nil, []FieldMapping{{From: "", To: "payload"}})
	consumer := NewMappedNode[MapState, int, int]("consume", func(_ context.Context, value int) (int, error) { consumerCalls++; return value, nil }, []FieldMapping{{From: "payload", To: ""}}, []FieldMapping{{From: "", To: "result"}})
	g, err := NewGraph[MapState]("dynamic-type-mismatch").AddNodeWithBuilder(producer).AddNodeWithBuilder(consumer).AddEdge(START, "produce").AddEdge("produce", "consume").AddEdge("consume", END).Build()
	if err != nil {
		t.Fatalf("interface output must remain a runtime check: %v", err)
	}
	input := MapState{"untouched": "keep"}
	state, err := g.Run(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), `node "consume"`) {
		t.Fatalf("missing downstream type mismatch location: %v", err)
	}
	if producerCalls != 1 || consumerCalls != 0 {
		t.Fatalf("dynamic mismatch reached downstream handler: producer=%d consumer=%d", producerCalls, consumerCalls)
	}
	if state["payload"] != "not-an-int" {
		t.Fatalf("completed upstream output was lost: %v", state)
	}
	if _, exists := state["result"]; exists {
		t.Fatal("downstream result was committed after a type mismatch")
	}
	if _, exists := input["payload"]; exists {
		t.Fatal("execution changed the original input")
	}
}

type waitProgress struct{ Marker string }

func TestGraphResumeTargetsPreserveOtherWaitsAndCompletedResults(t *testing.T) {
	codecs := interrupt.NewStateCodecs()
	if err := interrupt.RegisterJSONState[*waitProgress](codecs, "test.wait", "1"); err != nil {
		t.Fatal(err)
	}
	calls := map[string]int{}
	waiter := func(name string) NodeHandler[TestState] {
		return func(ctx context.Context, s TestState) (TestState, error) {
			calls[name]++
			was, has, progress := interrupt.GetInterruptState[*waitProgress](ctx)
			target, _, _ := interrupt.GetResumeContext[any](ctx)
			if was && !has {
				return s, errors.New("lost progress type")
			}
			if target {
				if progress.Marker != name {
					return s, errors.New("wrong progress")
				}
				s.Counter++
				return s, nil
			}
			return s, interrupt.StatefulInterrupt(ctx, name, &waitProgress{Marker: name})
		}
	}
	g, err := NewGraph[TestState]("directed-waits").WithInterruptCodecs(codecs).AddNode("a", waiter("a")).AddNode("b", waiter("b")).AddEdge(START, "a").AddEdge(START, "b").AddEdge("a", END).AddEdge("b", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	saver := NewMemoryEnhancedCheckpointSaver()
	runner := NewCheckpointRunner(g, saver, nil)
	_, err = runner.Run(context.Background(), "directed", TestState{})
	signal, ok := interrupt.IsInterruptSignal(err)
	if !ok {
		t.Fatalf("expected wait: %v", err)
	}
	ids := map[string]string{}
	for _, point := range interrupt.ToInterruptContexts(signal) {
		if point.IsRoot {
			ids[point.Address[len(point.Address)-1].ID] = point.ID
		}
	}
	state, err := NewCheckpointRunner(g, saver, nil).ResumeTargets(context.Background(), runner.GetCurrentCheckpoint().ID, map[string]any{ids["a"]: nil})
	remaining, ok := interrupt.IsInterruptSignal(err)
	if !ok {
		t.Fatalf("B wait disappeared: %v", err)
	}
	if state.Counter != 1 {
		t.Fatalf("A update missing: %+v", state)
	}
	foundB := false
	for _, point := range interrupt.ToInterruptContexts(remaining) {
		if point.ID == ids["b"] {
			foundB = true
		}
	}
	if !foundB {
		t.Fatal("B original wait identity was replaced")
	}
	state, err = NewCheckpointRunner(g, saver, nil).ResumeTargets(context.Background(), runner.GetCurrentCheckpoint().ID, map[string]any{ids["b"]: nil})
	if err != nil {
		t.Fatal(err)
	}
	if state.Counter != 2 || calls["a"] != 2 || calls["b"] != 2 {
		t.Fatalf("completed branch reran or other wait was resumed: state=%+v calls=%v", state, calls)
	}
}

func TestGraphCorruptExecutionSpecNeverStartsNodes(t *testing.T) {
	calls := 0
	g, err := NewGraph[TestState]("decode-failure").AddNode("a", func(_ context.Context, s TestState) (TestState, error) { calls++; return s, nil }).AddEdge(START, "a").AddEdge("a", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	saver := NewMemoryEnhancedCheckpointSaver()
	runner := NewCheckpointRunner(g, saver, nil)
	if _, err := runner.Run(context.Background(), "corrupt", TestState{}); err != nil {
		t.Fatal(err)
	}
	cp := runner.GetCurrentCheckpoint()
	cp.Frontier = json.RawMessage(`{"plan_id":`)
	if err := saver.SaveEnhanced(context.Background(), cp); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCheckpointRunner(g, saver, nil).Resume(context.Background(), cp.ID); err == nil {
		t.Fatal("corrupt specification was treated as fresh execution")
	}
	if calls != 1 {
		t.Fatalf("unexpected replay: calls=%d", calls)
	}
}

func TestNestedSubgraphResumeRetainsChildFrontier(t *testing.T) {
	codecs := interrupt.NewStateCodecs()
	if err := interrupt.RegisterJSONState[*waitProgress](codecs, "test.wait", "1"); err != nil {
		t.Fatal(err)
	}
	completedCalls := 0
	child, err := NewGraph[TestState]("child").WithInterruptCodecs(codecs).AddNode("completed", func(_ context.Context, s TestState) (TestState, error) { completedCalls++; s.Counter++; return s, nil }).AddNode("wait", func(ctx context.Context, s TestState) (TestState, error) {
		resume, _, _ := interrupt.GetResumeContext[any](ctx)
		if resume {
			s.Counter++
			return s, nil
		}
		return s, interrupt.StatefulInterrupt(ctx, "child wait", &waitProgress{Marker: "child"})
	}).AddEdge(START, "completed").AddEdge("completed", "wait").AddEdge("wait", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	parent, err := NewGraph[TestState]("parent").WithInterruptCodecs(codecs).AddNodeWithBuilder(SubgraphNode("child", child)).AddEdge(START, "child").AddEdge("child", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	saver := NewMemoryEnhancedCheckpointSaver()
	runner := NewCheckpointRunner(parent, saver, nil)
	_, err = runner.Run(context.Background(), "nested", TestState{})
	signal, ok := interrupt.IsInterruptSignal(err)
	if !ok {
		t.Fatalf("missing nested wait: %v", err)
	}
	target := ""
	for _, point := range interrupt.ToInterruptContexts(signal) {
		if point.IsRoot {
			target = point.ID
		}
	}
	state, err := NewCheckpointRunner(parent, saver, nil).ResumeTargets(context.Background(), runner.GetCurrentCheckpoint().ID, map[string]any{target: nil})
	if err != nil {
		t.Fatal(err)
	}
	if state.Counter != 2 || completedCalls != 1 {
		t.Fatalf("child work was replayed: %+v calls=%d", state, completedCalls)
	}
}

func TestPregelAllCycleBootstrapsAndResumesItsGeneration(t *testing.T) {
	aCalls, bCalls := 0, 0
	g, err := NewGraph[TestState]("all-cycle").WithExecutionMode(GraphModePregel).WithTriggerMode(TriggerAllPredecessors).AddNode("a", func(_ context.Context, s TestState) (TestState, error) { aCalls++; return s, nil }).AddNode("b", func(ctx context.Context, s TestState) (TestState, error) {
		bCalls++
		resuming, _, _ := interrupt.GetResumeContext[any](ctx)
		if s.Counter == 1 && !resuming {
			return s, interrupt.InterruptSignalFunc(ctx, "pause generation one")
		}
		s.Counter++
		return s, nil
	}).AddEdge(START, "a").AddEdge("a", "b").AddConditionalEdge("b", func(s TestState) string {
		if s.Counter < 3 {
			return "again"
		}
		return "done"
	}, map[string]string{"again": "a", "done": END}).Build()
	if err != nil {
		t.Fatal(err)
	}
	saver := NewMemoryEnhancedCheckpointSaver()
	runner := NewCheckpointRunner(g, saver, nil)
	state, err := runner.Run(context.Background(), "all-cycle", TestState{})
	signal, ok := interrupt.IsInterruptSignal(err)
	if !ok || state.Counter != 1 {
		t.Fatalf("cycle did not bootstrap to generation one: state=%+v err=%v", state, err)
	}
	target := ""
	for _, point := range interrupt.ToInterruptContexts(signal) {
		if point.IsRoot {
			target = point.ID
		}
	}
	state, err = NewCheckpointRunner(g, saver, nil).ResumeTargets(context.Background(), runner.GetCurrentCheckpoint().ID, map[string]any{target: nil})
	if err != nil {
		t.Fatal(err)
	}
	if state.Counter != 3 || aCalls != 3 || bCalls != 4 {
		t.Fatalf("wrong generation recovery: state=%+v A=%d B=%d", state, aCalls, bCalls)
	}
}

func TestPregelAllDiamondWaitsAcrossSupersteps(t *testing.T) {
	joinCalls := 0
	id := func(_ context.Context, s TestState) (TestState, error) { return s, nil }
	g, err := NewGraph[TestState]("all-diamond").WithExecutionMode(GraphModePregel).WithTriggerMode(TriggerAllPredecessors).WithStateMerger(func(base TestState, _ []TestState) TestState { return base }).AddNode("a", id).AddNode("b", id).AddNode("c", func(_ context.Context, s TestState) (TestState, error) {
		if joinCalls != 0 {
			t.Error("join ran before C completed")
		}
		return s, nil
	}).AddNode("join", func(_ context.Context, s TestState) (TestState, error) { joinCalls++; return s, nil }).AddEdge(START, "a").AddEdge(START, "b").AddEdge("a", "join").AddEdge("b", "c").AddEdge("c", "join").AddEdge("join", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(context.Background(), TestState{}); err != nil {
		t.Fatal(err)
	}
	if joinCalls != 1 {
		t.Fatalf("join calls=%d", joinCalls)
	}
}

func TestPregelAllDuplicateArrivalsCannotCrossGenerations(t *testing.T) {
	id := func(_ context.Context, s TestState) (TestState, error) { return s, nil }
	g, err := NewGraph[TestState]("all-arrivals").WithExecutionMode(GraphModePregel).WithTriggerMode(TriggerAllPredecessors).WithStateMerger(func(base TestState, _ []TestState) TestState { return base }).AddNode("a", id).AddNode("b", id).AddNode("c", id).AddEdge(START, "a").AddEdge("a", "b").AddEdge("a", "c").AddEdge("b", "a").AddEdge("c", "a").Build()
	if err != nil {
		t.Fatal(err)
	}
	x := newPlanExecutor(g, TestState{})
	x.frontier.Ready = nil
	x.frontier.ReadyGenerations = map[string][]uint64{}
	for i := 0; i < 2; i++ {
		if err := x.arrivePregelAll("b", "a", 0, x.taskIDFor("b", 0)); err != nil {
			t.Fatal(err)
		}
	}
	if err := x.arrivePregelAll("c", "a", 1, x.taskIDFor("c", 1)); err != nil {
		t.Fatal(err)
	}
	if len(x.frontier.Ready) != 0 {
		t.Fatal("duplicate B or generation-two C satisfied generation-one join")
	}
	if err := x.arrivePregelAll("c", "a", 0, x.taskIDFor("c", 0)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(x.frontier.ReadyGenerations["a"], []uint64{1}) {
		t.Fatalf("wrong ready generations: %v", x.frontier.ReadyGenerations)
	}
	if err := x.arrivePregelAll("b", "a", 1, x.taskIDFor("b", 0)); err == nil {
		t.Fatal("old task identity was accepted for a new generation")
	}
	data, err := g.encodeFrontier(x.frontier)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := g.decodeFrontier(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.Expected, x.frontier.Expected) || !reflect.DeepEqual(restored.ActivationArrivals, x.frontier.ActivationArrivals) || !reflect.DeepEqual(restored.ReadyGenerations, x.frontier.ReadyGenerations) {
		t.Fatal("activation identity or expected predecessors were not persisted")
	}
}

type panicDecodeCodec struct{ JSONStateCodec[TestState] }

func (panicDecodeCodec) Decode([]byte) (TestState, error) { panic("decode callback failed") }

func TestGraphStateCodecPanicFailsBeforeResumeDispatch(t *testing.T) {
	calls := 0
	g, err := NewGraph[TestState]("codec-panic").WithStateCodec(panicDecodeCodec{}).AddNode("a", func(_ context.Context, s TestState) (TestState, error) { calls++; return s, nil }).AddEdge(START, "a").AddEdge("a", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	saver := NewMemoryEnhancedCheckpointSaver()
	runner := NewCheckpointRunner(g, saver, nil)
	if _, err := runner.Run(context.Background(), "codec-panic", TestState{}); err != nil {
		t.Fatal(err)
	}
	_, err = NewCheckpointRunner(g, saver, nil).Resume(context.Background(), runner.GetCurrentCheckpoint().ID)
	var panicErr *NodePanicError
	if !errors.As(err, &panicErr) || calls != 1 {
		t.Fatalf("codec panic was swallowed or resumed nodes: calls=%d err=%v", calls, err)
	}
}
