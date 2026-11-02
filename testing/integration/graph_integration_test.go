package integration

import (
	"context"
	"testing"

	"github.com/hexagon-codes/hexagon/orchestration/graph"
)

// WorkflowState represents state for integration tests
type WorkflowState struct {
	Counter   int
	Steps     []string
	Data      map[string]string
	Completed bool
}

func (s WorkflowState) Clone() (graph.State, error) {
	steps := make([]string, len(s.Steps))
	copy(steps, s.Steps)

	data := make(map[string]string)
	for k, v := range s.Data {
		data[k] = v
	}

	return WorkflowState{
		Counter:   s.Counter,
		Steps:     steps,
		Data:      data,
		Completed: s.Completed,
	}, nil
}

// TestGraphLinearWorkflow tests a simple linear graph workflow
func TestGraphLinearWorkflow(t *testing.T) {
	g, err := graph.NewGraph[WorkflowState]("linear-workflow").
		AddNode("validate", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			s.Steps = append(s.Steps, "validate")
			s.Counter++
			return s, nil
		}).
		AddNode("process", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			s.Steps = append(s.Steps, "process")
			s.Counter++
			return s, nil
		}).
		AddNode("finalize", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			s.Steps = append(s.Steps, "finalize")
			s.Counter++
			s.Completed = true
			return s, nil
		}).
		AddEdge(graph.START, "validate").
		AddEdge("validate", "process").
		AddEdge("process", "finalize").
		AddEdge("finalize", graph.END).
		Build()

	if err != nil {
		t.Fatalf("failed to build graph: %v", err)
	}

	ctx := context.Background()
	result, err := g.Run(ctx, WorkflowState{
		Data: make(map[string]string),
	})

	if err != nil {
		t.Fatalf("graph execution failed: %v", err)
	}

	// Verify all steps executed
	expectedSteps := []string{"validate", "process", "finalize"}
	if len(result.Steps) != len(expectedSteps) {
		t.Fatalf("expected %d steps, got %d", len(expectedSteps), len(result.Steps))
	}

	for i, step := range expectedSteps {
		if result.Steps[i] != step {
			t.Errorf("expected step %d to be '%s', got '%s'", i, step, result.Steps[i])
		}
	}

	if result.Counter != 3 {
		t.Errorf("expected counter 3, got %d", result.Counter)
	}

	if !result.Completed {
		t.Error("expected workflow to be completed")
	}
}

// TestGraphConditionalWorkflow tests conditional branching
func TestGraphConditionalWorkflow(t *testing.T) {
	g, err := graph.NewGraph[WorkflowState]("conditional-workflow").
		AddNode("check", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			s.Steps = append(s.Steps, "check")
			return s, nil
		}).
		AddNode("path_high", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			s.Steps = append(s.Steps, "path_high")
			s.Data["path"] = "high"
			return s, nil
		}).
		AddNode("path_low", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			s.Steps = append(s.Steps, "path_low")
			s.Data["path"] = "low"
			return s, nil
		}).
		AddNode("merge", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			s.Steps = append(s.Steps, "merge")
			s.Completed = true
			return s, nil
		}).
		AddEdge(graph.START, "check").
		AddConditionalEdge("check", func(s WorkflowState) string {
			if s.Counter >= 10 {
				return "high"
			}
			return "low"
		}, map[string]string{
			"high": "path_high",
			"low":  "path_low",
		}).
		AddEdge("path_high", "merge").
		AddEdge("path_low", "merge").
		AddEdge("merge", graph.END).
		Build()

	if err != nil {
		t.Fatalf("failed to build graph: %v", err)
	}

	ctx := context.Background()

	// Test high path
	result, err := g.Run(ctx, WorkflowState{
		Counter: 15,
		Data:    make(map[string]string),
	})
	if err != nil {
		t.Fatalf("graph execution failed: %v", err)
	}
	if result.Data["path"] != "high" {
		t.Errorf("expected high path, got '%s'", result.Data["path"])
	}

	// Test low path
	result, err = g.Run(ctx, WorkflowState{
		Counter: 5,
		Data:    make(map[string]string),
	})
	if err != nil {
		t.Fatalf("graph execution failed: %v", err)
	}
	if result.Data["path"] != "low" {
		t.Errorf("expected low path, got '%s'", result.Data["path"])
	}
}

// TestGraphStreamExecution tests streaming graph execution
func TestGraphStreamExecution(t *testing.T) {
	g, err := graph.NewGraph[WorkflowState]("stream-workflow").
		AddNode("step1", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			s.Counter++
			return s, nil
		}).
		AddNode("step2", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			s.Counter++
			return s, nil
		}).
		AddNode("step3", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			s.Counter++
			s.Completed = true
			return s, nil
		}).
		AddEdge(graph.START, "step1").
		AddEdge("step1", "step2").
		AddEdge("step2", "step3").
		AddEdge("step3", graph.END).
		Build()

	if err != nil {
		t.Fatalf("failed to build graph: %v", err)
	}

	ctx := context.Background()
	events, err := g.Stream(ctx, WorkflowState{Data: make(map[string]string)})
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}

	nodeStartCount := 0
	nodeEndCount := 0
	var finalState WorkflowState

	for event := range events {
		switch event.Type {
		case graph.EventTypeNodeStart:
			nodeStartCount++
		case graph.EventTypeNodeEnd:
			nodeEndCount++
		case graph.EventTypeEnd:
			finalState = event.State
		case graph.EventTypeError:
			t.Fatalf("unexpected error: %v", event.Error)
		}
	}

	// Should have 3 node starts and 3 node ends
	if nodeStartCount != 3 {
		t.Errorf("expected 3 node starts, got %d", nodeStartCount)
	}
	if nodeEndCount != 3 {
		t.Errorf("expected 3 node ends, got %d", nodeEndCount)
	}

	if finalState.Counter != 3 {
		t.Errorf("expected counter 3, got %d", finalState.Counter)
	}

	if !finalState.Completed {
		t.Error("expected workflow to be completed")
	}
}

// TestGraphMapState tests MapState operations
func TestGraphMapState(t *testing.T) {
	state := graph.MapState{
		"initial": "value",
	}

	// Test Set and Get
	state.Set("key1", "value1")
	state.Set("key2", 42)

	val1, ok := state.Get("key1")
	if !ok || val1 != "value1" {
		t.Error("expected to get key1")
	}

	// Test Clone
	copy, err := state.Clone()
	if err != nil {
		t.Fatal(err)
	}
	cloned := copy.(graph.MapState)
	cloned.Set("new_key", "new_value")

	_, ok = state.Get("new_key")
	if ok {
		t.Error("original should not be affected by clone")
	}

	// Test Delete
	state.Delete("key1")
	_, ok = state.Get("key1")
	if ok {
		t.Error("key1 should be deleted")
	}

	// Test setting multiple keys
	state.Set("a", 1)
	state.Set("b", 2)

	valA, _ := state.Get("a")
	valB, _ := state.Get("b")
	if valA != 1 || valB != 2 {
		t.Error("expected both keys to be set")
	}

	// Test Merge
	other := graph.MapState{"merged": "data"}
	state.Merge(other)
	val, ok := state.Get("merged")
	if !ok || val != "data" {
		t.Error("merge should add keys from other")
	}
}

// TestGraphWithMetadata tests graph metadata
func TestGraphWithMetadata(t *testing.T) {
	g, err := graph.NewGraph[WorkflowState]("metadata-test").
		AddNode("step", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			return s, nil
		}).
		AddEdge(graph.START, "step").
		AddEdge("step", graph.END).
		WithMetadata("version", "1.0.0").
		WithMetadata("author", "test").
		WithMetadata("tags", []string{"test", "integration"}).
		Build()

	if err != nil {
		t.Fatalf("failed to build graph: %v", err)
	}

	if g.Metadata["version"] != "1.0.0" {
		t.Errorf("expected version '1.0.0', got '%v'", g.Metadata["version"])
	}

	if g.Metadata["author"] != "test" {
		t.Errorf("expected author 'test', got '%v'", g.Metadata["author"])
	}

	tags, ok := g.Metadata["tags"].([]string)
	if !ok || len(tags) != 2 {
		t.Error("expected tags to be a 2-element slice")
	}
}
