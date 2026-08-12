<div align="right">语言: 中文 | <a href="graph-orchestration.en.md">English</a></div>

# 图编排最佳实践

图编排用有向图组织节点，支持条件路由、并行处理、人工介入和检查点恢复。本文示例与当前 `orchestration/graph` 和 `checkpoint` 的导出 API 保持一致。

## 状态约束

图状态必须实现 `graph.State` 的 `Clone() graph.State`。并行节点会为每个处理器克隆状态，因此包含 slice、map 或指针时必须做深拷贝。

以下是后续片段共用的状态定义：

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

## 快速开始

下方是可放入函数中的完整构图与运行片段；它复用上方 `MyState`，并需导入 `context`、`fmt` 和 `github.com/hexagon-codes/hexagon/orchestration/graph`。

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

`NewGraph` 需要图名，`Build` 会验证边所引用的节点并返回可直接 `Run` 的 `*graph.Graph[S]`。

## 条件分支与循环

`AddConditionalEdge` 的 router 只接收状态并返回标签；第三个参数必须把每个标签映射到真实目标节点。以下片段复用 `MyState`：

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

## 并行执行

普通图执行器对一个节点的多条普通出边只选取第一条，不要用多次 `AddEdge` 表示 fan-out。需要真正的并行执行时，使用 `ParallelNodeWithMerger` 并显式合并各分支状态：

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

## 人工介入（HITL）

`NewApprovalNode` 生成真实的 HITL 节点；`ChannelHITLHandler` 将请求交给 UI 或审批服务，节点在 `SubmitResponse` 到达前会等待。以下是复用 `ctx` 和 `MyState` 的可执行片段：

```go
handler := graph.NewChannelHITLHandler(1)
approval := graph.NewApprovalNode[MyState](
	"approval",
	handler,
	graph.WithHITLTitle[MyState]("需要审批"),
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

## 检查点与恢复

### 基础检查点仓库

`WithCheckpointer` 把 `graph.CheckpointSaver` 关联到图。当前的普通 `Graph.Run` 不会自动调用它的 `Save`；如果直接使用基础仓库，应在业务边界显式保存并加载状态。以下片段需额外导入 `encoding/json`：

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

### 恢复图执行

需要在失败或中断后继续待执行节点时，使用 `CheckpointRunner` 和 `EnhancedCheckpointSaver`。下面的片段先制造一次可恢复失败，然后通过真实的 `Resume(ctx, checkpointID)` 重试保存在 `PendingNodes` 中的节点：

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

`Resume` 可按检查点 ID 恢复，`ResumeFromLatest` 可按 thread ID 恢复最新的增强检查点；已完成的检查点没有待执行节点，恢复时只返回其最终状态。

> `graph.CheckpointSaver` 与顶层 `checkpoint.Checkpointer` 是两个不同的接口。前者的列表方法是 `List(ctx, threadID)`；后者的方法是 `List(ctx, namespace, limit)`，用于统一字节快照仓库，不能直接传给 `GraphBuilder.WithCheckpointer`。

更多设计细节参见 [架构设计文档](../DESIGN.md#图编排引擎)。
