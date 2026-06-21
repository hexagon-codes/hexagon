<div align="right">Language: <a href="graph-orchestration.md">中文</a> | English</div>

# Graph Orchestration Best Practices

Graph orchestration provides flexible control flow with support for conditional branching, loops, and parallel execution.

## Quick Start

```go
import "github.com/hexagon-codes/hexagon/orchestration/graph"

// Define state
type MyState struct {
    Input  string
    Output string
    Count  int
}

// Create graph
g := graph.New[MyState]()

// Add nodes
g.AddNode("start", func(ctx context.Context, state MyState) (MyState, error) {
    state.Count++
    return state, nil
})

g.AddNode("process", func(ctx context.Context, state MyState) (MyState, error) {
    state.Output = "processed: " + state.Input
    return state, nil
})

g.AddNode("end", func(ctx context.Context, state MyState) (MyState, error) {
    return state, nil
})

// Add edges
g.AddEdge("start", "process")
g.AddEdge("process", "end")

// Set entry and finish points
g.SetEntryPoint("start")
g.SetFinishPoint("end")

// Compile and run
compiled, _ := g.Compile()
result, _ := compiled.Run(ctx, MyState{Input: "hello"})

fmt.Println(result.Output) // "processed: hello"
```

## Conditional Branching

```go
// Add a conditional edge
g.AddConditionalEdge("process", func(ctx context.Context, state MyState) (string, error) {
    if state.Count > 5 {
        return "end", nil
    }
    return "start", nil // loop back
})
```

## Parallel Execution

```go
// Execute multiple nodes in parallel
g.AddNode("parallel_start", func(ctx context.Context, state MyState) (MyState, error) {
    return state, nil
})

g.AddNode("task1", ...)
g.AddNode("task2", ...)
g.AddNode("task3", ...)

// Fan out to multiple tasks
g.AddEdge("parallel_start", "task1")
g.AddEdge("parallel_start", "task2")
g.AddEdge("parallel_start", "task3")

// Merge results
g.AddNode("merge", ...)
g.AddEdge("task1", "merge")
g.AddEdge("task2", "merge")
g.AddEdge("task3", "merge")
```

## Interrupts and Resumption

```go
// Add an interrupt point
g.AddInterrupt("approval", graph.InterruptTypeApproval,
    graph.WithInterruptTitle("Approval Required"),
    graph.WithInterruptOptions([]graph.InterruptOption{
        {Value: "approve", Label: "Approve"},
        {Value: "reject", Label: "Reject"},
    }),
)

// Resume execution
result := <-interrupt.Wait(ctx)
if result.Value == "approve" {
    compiled.Resume(ctx, interrupt.ID)
}
```

## Checkpoints

```go
// The checkpoint saver lives in the orchestration/graph package (imported above)

// Use checkpoints
saver := graph.NewMemoryCheckpointSaver()

compiled, _ := g.Compile(
    graph.WithCheckpointSaver(saver),
)

// Execution automatically saves checkpoints
result, _ := compiled.Run(ctx, state)

// Resume from a checkpoint
checkpoints := saver.List(threadID)
latest := checkpoints[len(checkpoints)-1]
compiled.ResumeFromCheckpoint(ctx, latest)
```

For more details, see [DESIGN.md](../DESIGN.md#图编排).
