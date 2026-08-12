<div align="right">Language: <a href="graph-orchestration.md">中文</a> | English</div>

# Graph Orchestration Best Practices

Graph orchestration organizes nodes as a directed graph and supports conditional routing, parallel processing, human-in-the-loop workflows, and checkpoint recovery. The examples below match the currently exported APIs in `orchestration/graph` and `checkpoint`.

## State Constraint

Every graph state must implement `Clone() graph.State` from `graph.State`. Parallel nodes clone the state for each handler, so states containing slices, maps, or pointers require a deep copy.

The following state definition is shared by the later snippets:

```go
type MyState struct {
	Input    string
	Output   string
	Count    int
	Results  []string
	Approved bool
}

func (s MyState) Clone() graph.State {
	cloned := s
	cloned.Results = append([]string(nil), s.Results...)
	return cloned
}
```

## Quick Start

The following complete build-and-run fragment can be placed inside a function. It reuses `MyState` above and requires `context`, `fmt`, and `github.com/hexagon-codes/hexagon/orchestration/graph` imports.

```go
ctx := context.Background()

workflow, err := graph.NewGraph[MyState]("quick-start").
	AddNode("process", func(ctx context.Context, state MyState) (MyState, error) {
		state.Count++
		state.Output = "processed: " + state.Input
		return state, nil
	}).
	AddEdge(graph.START, "process").
	AddEdge("process", graph.END).
	Build()
if err != nil {
	return err
}

result, err := workflow.Run(ctx, MyState{Input: "hello"})
if err != nil {
	return err
}
fmt.Println(result.Output)
```

`NewGraph` requires a graph name. `Build` validates referenced nodes and returns a `*graph.Graph[S]` that can run directly.

## Conditional Branching and Loops

The `AddConditionalEdge` router accepts only the state and returns a label. Its third argument must map every label to a real target node. This fragment reuses `MyState`:

```go
conditionalGraph, err := graph.NewGraph[MyState]("conditional-loop").
	AddNode("process", func(ctx context.Context, state MyState) (MyState, error) {
		state.Count++
		return state, nil
	}).
	AddNode("retry", func(ctx context.Context, state MyState) (MyState, error) {
		return state, nil
	}).
	AddNode("done", func(ctx context.Context, state MyState) (MyState, error) {
		state.Output = "done"
		return state, nil
	}).
	AddEdge(graph.START, "process").
	AddConditionalEdge("process", func(state MyState) string {
		if state.Count < 3 {
			return "again"
		}
		return "complete"
	}, map[string]string{
		"again":    "retry",
		"complete": "done",
	}).
	AddEdge("retry", "process").
	AddEdge("done", graph.END).
	Build()
if err != nil {
	return err
}

result, err := conditionalGraph.Run(ctx, MyState{})
if err != nil {
	return err
}
fmt.Println(result.Count, result.Output)
```

## Parallel Execution

The regular graph executor follows only the first normal outgoing edge from a node, so multiple `AddEdge` calls do not express fan-out. Use `ParallelNodeWithMerger` for real parallel execution and merge every branch state explicitly:

```go
parallel := graph.ParallelNodeWithMerger[MyState](
	"parallel",
	func(original MyState, outputs []MyState) MyState {
		merged := original.Clone().(MyState)
		for _, output := range outputs {
			merged.Results = append(merged.Results, output.Results...)
		}
		return merged
	},
	func(ctx context.Context, state MyState) (MyState, error) {
		state.Results = []string{"task-1"}
		return state, nil
	},
	func(ctx context.Context, state MyState) (MyState, error) {
		state.Results = []string{"task-2"}
		return state, nil
	},
)

parallelGraph, err := graph.NewGraph[MyState]("parallel-work").
	AddNodeWithBuilder(parallel).
	AddEdge(graph.START, "parallel").
	AddEdge("parallel", graph.END).
	Build()
if err != nil {
	return err
}

result, err := parallelGraph.Run(ctx, MyState{})
if err != nil {
	return err
}
fmt.Println(result.Results)
```

## Human in the Loop (HITL)

`NewApprovalNode` creates a real HITL node. `ChannelHITLHandler` delivers requests to a UI or approval service, and the node waits until `SubmitResponse` supplies a response. The following executable fragment reuses `ctx` and `MyState`:

```go
handler := graph.NewChannelHITLHandler(1)
approval := graph.NewApprovalNode[MyState](
	"approval",
	handler,
	graph.WithHITLTitle[MyState]("Approval required"),
)
approval.ResponseHandler = func(state MyState, response *graph.HITLResponse) MyState {
	state.Approved = response.Approved
	return state
}

approvalGraph, err := graph.NewGraph[MyState]("approval-flow").
	AddNode("approval", approval.Execute).
	AddEdge(graph.START, "approval").
	AddEdge("approval", graph.END).
	Build()
if err != nil {
	return err
}

responseErr := make(chan error, 1)
go func() {
	request := <-handler.GetRequests()
	responseErr <- handler.SubmitResponse(&graph.HITLResponse{
		RequestID:      request.ID,
		Approved:       true,
		SelectedOption: "approve",
		RespondedBy:    "reviewer@example.com",
	})
}()

result, err := approvalGraph.Run(ctx, MyState{})
if err != nil {
	return err
}
if err := <-responseErr; err != nil {
	return err
}
fmt.Println(result.Approved)
```

## Checkpoints and Recovery

### Basic Checkpoint Store

`WithCheckpointer` associates a `graph.CheckpointSaver` with a graph. The current regular `Graph.Run` does not automatically call its `Save` method. When using the basic store directly, save and load state explicitly at application boundaries. The following fragment additionally requires `encoding/json`:

```go
saver := graph.NewMemoryCheckpointSaver()
checkpointGraph, err := graph.NewGraph[MyState]("checkpoint-storage").
	AddNode("process", func(ctx context.Context, state MyState) (MyState, error) {
		state.Output = "saved"
		return state, nil
	}).
	AddEdge(graph.START, "process").
	AddEdge("process", graph.END).
	WithCheckpointer(saver).
	Build()
if err != nil {
	return err
}

const threadID = "thread-42"
stateJSON, err := json.Marshal(MyState{Output: "saved"})
if err != nil {
	return err
}
if err := saver.Save(ctx, &graph.Checkpoint{
	ThreadID:    threadID,
	GraphName:   checkpointGraph.Name,
	CurrentNode: graph.END,
	State:       stateJSON,
}); err != nil {
	return err
}

checkpoints, err := saver.List(ctx, threadID)
if err != nil {
	return err
}
latest := checkpoints[len(checkpoints)-1]

var restored MyState
if err := json.Unmarshal(latest.State, &restored); err != nil {
	return err
}
fmt.Println(restored.Output)
```

### Resuming Graph Execution

To continue pending nodes after a failure or interruption, use `CheckpointRunner` with an `EnhancedCheckpointSaver`. This fragment first creates a recoverable failure and then uses the real `Resume(ctx, checkpointID)` API to retry the node stored in `PendingNodes`:

```go
dependencyReady := false
resumeGraph, err := graph.NewGraph[MyState]("resume-flow").
	AddNode("unstable", func(ctx context.Context, state MyState) (MyState, error) {
		if !dependencyReady {
			return state, fmt.Errorf("dependency unavailable")
		}
		state.Output = "recovered"
		return state, nil
	}).
	AddEdge(graph.START, "unstable").
	AddEdge("unstable", graph.END).
	Build()
if err != nil {
	return err
}

enhancedSaver := graph.NewMemoryEnhancedCheckpointSaver()
config := graph.DefaultCheckpointRunnerConfig()
config.MaxRetries = 0

runner := graph.NewCheckpointRunner(resumeGraph, enhancedSaver, config)
if _, err := runner.Run(ctx, "thread-resume", MyState{}); err == nil {
	return fmt.Errorf("expected the first run to fail")
}
failed := runner.GetCurrentCheckpoint()

dependencyReady = true
resumed, err := graph.NewCheckpointRunner(resumeGraph, enhancedSaver, config).
	Resume(ctx, failed.ID)
if err != nil {
	return err
}
fmt.Println(resumed.Output)
```

`Resume` restores by checkpoint ID, while `ResumeFromLatest` restores the latest enhanced checkpoint by thread ID. A completed checkpoint has no pending nodes, so restoring it only returns its final state.

> `graph.CheckpointSaver` and the top-level `checkpoint.Checkpointer` are different interfaces. The former exposes `List(ctx, threadID)`. The latter exposes `List(ctx, namespace, limit)` for the framework-wide byte snapshot store and cannot be passed directly to `GraphBuilder.WithCheckpointer`.

For more design details, see the [architecture design document](../DESIGN.en.md#graph-orchestration-engine).
