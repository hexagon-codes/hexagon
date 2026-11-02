package graph

import (
	"context"
	"errors"
	"testing"
)

// TestState for graph tests
type TestState struct {
	Counter int
	Path    string
	Data    map[string]string
}

func (s TestState) Clone() (State, error) {
	data := make(map[string]string)
	for k, v := range s.Data {
		data[k] = v
	}
	return TestState{
		Counter: s.Counter,
		Path:    s.Path,
		Data:    data,
	}, nil
}

func TestNewGraph(t *testing.T) {
	builder := NewGraph[TestState]("test-graph")

	if builder == nil {
		t.Fatal("expected non-nil builder")
	}
}

func TestGraphAddNode(t *testing.T) {
	g, err := NewGraph[TestState]("test-graph").
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddEdge(START, "step1").
		AddEdge("step1", END).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if g == nil {
		t.Fatal("expected non-nil graph")
	}

	if g.Name != "test-graph" {
		t.Errorf("expected name 'test-graph', got '%s'", g.Name)
	}

	// Should have 3 nodes: START, step1, END
	if len(g.Nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(g.Nodes))
	}
}

func TestGraphAddNodeReservedName(t *testing.T) {
	_, err := NewGraph[TestState]("test-graph").
		AddNode(START, func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		}).
		Build()

	if err == nil {
		t.Error("expected error for reserved node name")
	}
}

func TestGraphAddDuplicateNode(t *testing.T) {
	_, err := NewGraph[TestState]("test-graph").
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		}).
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		}).
		Build()

	if err == nil {
		t.Error("expected error for duplicate node")
	}
}

func TestGraphRun(t *testing.T) {
	g, err := NewGraph[TestState]("test-graph").
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			s.Path += "1"
			return s, nil
		}).
		AddNode("step2", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			s.Path += "2"
			return s, nil
		}).
		AddEdge(START, "step1").
		AddEdge("step1", "step2").
		AddEdge("step2", END).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	result, err := g.Run(ctx, TestState{Counter: 0, Path: ""})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Counter != 2 {
		t.Errorf("expected counter 2, got %d", result.Counter)
	}

	if result.Path != "12" {
		t.Errorf("expected path '12', got '%s'", result.Path)
	}
}

func TestGraphRunWithError(t *testing.T) {
	expectedErr := errors.New("node failed")

	g, err := NewGraph[TestState]("test-graph").
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			return s, expectedErr
		}).
		AddEdge(START, "step1").
		AddEdge("step1", END).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	_, err = g.Run(ctx, TestState{})

	if err == nil {
		t.Error("expected error")
	}
}

func TestGraphRunWithCanceledContext(t *testing.T) {
	g, err := NewGraph[TestState]("test-graph").
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		}).
		AddEdge(START, "step1").
		AddEdge("step1", END).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err = g.Run(ctx, TestState{})

	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestGraphConditionalEdge(t *testing.T) {
	g, err := NewGraph[TestState]("conditional-graph").
		AddNode("check", func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		}).
		AddNode("path_a", func(ctx context.Context, s TestState) (TestState, error) {
			s.Path = "A"
			return s, nil
		}).
		AddNode("path_b", func(ctx context.Context, s TestState) (TestState, error) {
			s.Path = "B"
			return s, nil
		}).
		AddEdge(START, "check").
		AddConditionalEdge("check", func(s TestState) string {
			if s.Counter%2 == 0 {
				return "even"
			}
			return "odd"
		}, map[string]string{
			"even": "path_a",
			"odd":  "path_b",
		}).
		AddEdge("path_a", END).
		AddEdge("path_b", END).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()

	// Test even path
	result, err := g.Run(ctx, TestState{Counter: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Path != "A" {
		t.Errorf("expected path 'A', got '%s'", result.Path)
	}

	// Test odd path
	result, err = g.Run(ctx, TestState{Counter: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Path != "B" {
		t.Errorf("expected path 'B', got '%s'", result.Path)
	}
}

func TestGraphStream(t *testing.T) {
	g, err := NewGraph[TestState]("stream-graph").
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddNode("step2", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddEdge(START, "step1").
		AddEdge("step1", "step2").
		AddEdge("step2", END).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	events, err := g.Stream(ctx, TestState{Counter: 0})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	eventCount := 0
	var lastState TestState
	for event := range events {
		eventCount++
		if event.Type == EventTypeError {
			t.Fatalf("unexpected error event: %v", event.Error)
		}
		if event.Type == EventTypeEnd {
			lastState = event.State
		}
	}

	// Should have: 2x NodeStart + 2x NodeEnd + 1x End = 5 events
	if eventCount != 5 {
		t.Errorf("expected 5 events, got %d", eventCount)
	}

	if lastState.Counter != 2 {
		t.Errorf("expected counter 2, got %d", lastState.Counter)
	}
}

func TestGraphValidationMissingNode(t *testing.T) {
	_, err := NewGraph[TestState]("test-graph").
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		}).
		AddEdge(START, "step1").
		AddEdge("step1", "nonexistent"). // This node doesn't exist
		Build()

	if err == nil {
		t.Error("expected validation error for missing node")
	}
}

func TestGraphMetadata(t *testing.T) {
	g, err := NewGraph[TestState]("test-graph").
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		}).
		AddEdge(START, "step1").
		AddEdge("step1", END).
		WithMetadata("version", "1.0").
		WithMetadata("author", "test").
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if g.Metadata["version"] != "1.0" {
		t.Errorf("expected version '1.0', got '%v'", g.Metadata["version"])
	}

	if g.Metadata["author"] != "test" {
		t.Errorf("expected author 'test', got '%v'", g.Metadata["author"])
	}
}

func TestGraphMustBuild(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Error("expected no panic for valid graph")
		}
	}()

	g := NewGraph[TestState]("test-graph").
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		}).
		AddEdge(START, "step1").
		AddEdge("step1", END).
		MustBuild()

	if g == nil {
		t.Error("expected non-nil graph")
	}
}

func TestGraphMustBuildPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for invalid graph")
		}
	}()

	NewGraph[TestState]("test-graph").
		AddNode(START, func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		}). // Invalid: using reserved name
		MustBuild()
}

func TestEventTypeString(t *testing.T) {
	tests := []struct {
		eventType EventType
		expected  string
	}{
		{EventTypeNodeStart, "node_start"},
		{EventTypeNodeEnd, "node_end"},
		{EventTypeError, "error"},
		{EventTypeEnd, "end"},
		{EventType(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.eventType.String(); got != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, got)
			}
		})
	}
}

func TestGraphWithInterrupt(t *testing.T) {
	g, err := NewGraph[TestState]("test-graph").
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddNode("step2", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddEdge(START, "step1").
		AddEdge("step1", "step2").
		AddEdge("step2", END).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	_, err = g.Run(ctx, TestState{Counter: 0}, WithInterrupt("step2"))

	if err == nil {
		t.Error("expected interrupt error")
	}
}

// ============== Pregel 模式测试 ==============

func TestPregelBasicExecution(t *testing.T) {
	g, err := NewGraph[TestState]("pregel-graph").
		WithExecutionMode(GraphModePregel).
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			s.Path += "1"
			return s, nil
		}).
		AddNode("step2", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			s.Path += "2"
			return s, nil
		}).
		AddEdge(START, "step1").
		AddEdge("step1", "step2").
		AddEdge("step2", END).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	result, steps, err := g.RunPregelMode(ctx, TestState{Counter: 0, Path: ""})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Counter != 2 {
		t.Errorf("expected counter 2, got %d", result.Counter)
	}

	if result.Path != "12" {
		t.Errorf("expected path '12', got '%s'", result.Path)
	}

	if steps < 1 {
		t.Errorf("expected at least 1 superstep, got %d", steps)
	}
}

func TestPregelCyclicGraph(t *testing.T) {
	// 测试循环图：iterate -> check -> (回到 iterate 或 finish)
	g, err := NewGraph[TestState]("cyclic-graph").
		WithExecutionMode(GraphModePregel).
		AddNode("iterate", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddNode("check", func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		}).
		AddNode("finish", func(ctx context.Context, s TestState) (TestState, error) {
			s.Path = "done"
			return s, nil
		}).
		AddEdge(START, "iterate").
		AddEdge("iterate", "check").
		AddConditionalEdge("check", func(s TestState) string {
			if s.Counter >= 3 {
				return "done"
			}
			return "continue"
		}, map[string]string{
			"continue": "iterate", // 循环回去
			"done":     "finish",
		}).
		AddEdge("finish", END).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	result, steps, err := g.RunPregelMode(ctx, TestState{Counter: 0, Path: ""})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Counter 应该达到 3（循环 3 次）
	if result.Counter != 3 {
		t.Errorf("expected counter 3, got %d", result.Counter)
	}

	if result.Path != "done" {
		t.Errorf("expected path 'done', got '%s'", result.Path)
	}

	t.Logf("Cyclic graph completed in %d supersteps", steps)
}

func TestPregelMaxSupersteps(t *testing.T) {
	// 创建一个无限循环的图
	g, err := NewGraph[TestState]("infinite-graph").
		WithExecutionMode(GraphModePregel).
		AddNode("loop", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddEdge(START, "loop").
		AddEdge("loop", "loop"). // 无限循环
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	_, _, err = g.RunPregelMode(ctx, TestState{Counter: 0},
		WithMaxSupersteps(5), // 限制最大 5 步
	)

	if err == nil {
		t.Error("expected max supersteps exceeded error")
	}
}

func TestPregelTriggerMode(t *testing.T) {
	tests := []struct {
		name    string
		mode    TriggerMode
		wantStr string
	}{
		{"AllPredecessors", TriggerAllPredecessors, "all_predecessors"},
		{"AnyPredecessor", TriggerAnyPredecessor, "any_predecessor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mode.String(); got != tt.wantStr {
				t.Errorf("TriggerMode.String() = %v, want %v", got, tt.wantStr)
			}
		})
	}
}

func TestPregelStreamMode(t *testing.T) {
	g, err := NewGraph[TestState]("stream-pregel").
		WithExecutionMode(GraphModePregel).
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddEdge(START, "step1").
		AddEdge("step1", END).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	events, err := g.StreamPregelMode(ctx, TestState{Counter: 0})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var eventTypes []PregelEventType
	for event := range events {
		eventTypes = append(eventTypes, event.Type)
		if event.Type == PregelEventError {
			t.Fatalf("unexpected error: %v", event.Error)
		}
	}

	// 应该有超级步开始、结束和完成事件
	hasStart := false
	hasEnd := false
	hasComplete := false
	for _, et := range eventTypes {
		switch et {
		case PregelEventSuperstepStart:
			hasStart = true
		case PregelEventSuperstepEnd:
			hasEnd = true
		case PregelEventComplete:
			hasComplete = true
		}
	}

	if !hasStart || !hasEnd || !hasComplete {
		t.Errorf("missing expected events: start=%v end=%v complete=%v",
			hasStart, hasEnd, hasComplete)
	}
}

func TestPregelEventTypeString(t *testing.T) {
	tests := []struct {
		eventType PregelEventType
		expected  string
	}{
		{PregelEventSuperstepStart, "superstep_start"},
		{PregelEventSuperstepEnd, "superstep_end"},
		{PregelEventComplete, "complete"},
		{PregelEventError, "error"},
		{PregelEventType(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.eventType.String(); got != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, got)
			}
		})
	}
}

func TestPregelTerminationCheck(t *testing.T) {
	g, err := NewGraph[TestState]("termination-graph").
		WithExecutionMode(GraphModePregel).
		AddNode("iterate", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddEdge(START, "iterate").
		AddEdge("iterate", "iterate"). // 无限循环
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	result, steps, err := g.RunPregelMode(ctx, TestState{Counter: 0},
		WithTerminationCheck(func(step int, _ []string) bool {
			return step >= 3 // 在第 3 步终止
		}),
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if steps != 3 {
		t.Errorf("expected 3 supersteps, got %d", steps)
	}

	t.Logf("Termination check stopped at counter=%d", result.Counter)
}

func TestPregelParallelExecution(t *testing.T) {
	// 测试并行执行：两个独立节点可以并行执行
	g, err := NewGraph[TestState]("parallel-graph").
		WithStateMerger(func(_ TestState, outputs []TestState) TestState { return outputs[len(outputs)-1] }).
		WithExecutionMode(GraphModePregel).
		AddNode("nodeA", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter += 10
			return s, nil
		}).
		AddNode("nodeB", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter += 100
			return s, nil
		}).
		AddNode("merge", func(ctx context.Context, s TestState) (TestState, error) {
			s.Path = "merged"
			return s, nil
		}).
		AddEdge(START, "nodeA").
		AddEdge(START, "nodeB"). // nodeA 和 nodeB 可以并行
		AddEdge("nodeA", "merge").
		AddEdge("nodeB", "merge").
		AddEdge("merge", END).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	result, _, err := g.RunPregelMode(ctx, TestState{Counter: 0},
		WithParallelExecution(true),
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Path != "merged" {
		t.Errorf("expected path 'merged', got '%s'", result.Path)
	}
}
