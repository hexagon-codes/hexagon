<div align="right">语言: 中文 | <a href="graph-orchestration.en.md">English</a></div>

# 图编排最佳实践

图编排提供灵活的控制流，支持条件分支、循环和并行执行。

## 快速开始

```go
import "github.com/hexagon-codes/hexagon/orchestration/graph"

// 定义状态
type MyState struct {
    Input  string
    Output string
    Count  int
}

// 创建图
g := graph.New[MyState]()

// 添加节点
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

// 添加边
g.AddEdge("start", "process")
g.AddEdge("process", "end")

// 设置入口和出口
g.SetEntryPoint("start")
g.SetFinishPoint("end")

// 编译并运行
compiled, _ := g.Compile()
result, _ := compiled.Run(ctx, MyState{Input: "hello"})

fmt.Println(result.Output) // "processed: hello"
```

## 条件分支

```go
// 添加条件边
g.AddConditionalEdge("process", func(ctx context.Context, state MyState) (string, error) {
    if state.Count > 5 {
        return "end", nil
    }
    return "start", nil // 循环
})
```

## 并行执行

```go
// 并行执行多个节点
g.AddNode("parallel_start", func(ctx context.Context, state MyState) (MyState, error) {
    return state, nil
})

g.AddNode("task1", ...)
g.AddNode("task2", ...)
g.AddNode("task3", ...)

// 分支到多个任务
g.AddEdge("parallel_start", "task1")
g.AddEdge("parallel_start", "task2")
g.AddEdge("parallel_start", "task3")

// 汇聚
g.AddNode("merge", ...)
g.AddEdge("task1", "merge")
g.AddEdge("task2", "merge")
g.AddEdge("task3", "merge")
```

## 中断和恢复

```go
// 添加中断点
g.AddInterrupt("approval", graph.InterruptTypeApproval,
    graph.WithInterruptTitle("需要审批"),
    graph.WithInterruptOptions([]graph.InterruptOption{
        {Value: "approve", Label: "批准"},
        {Value: "reject", Label: "拒绝"},
    }),
)

// 恢复执行
result := <-interrupt.Wait(ctx)
if result.Value == "approve" {
    compiled.Resume(ctx, interrupt.ID)
}
```

## 检查点

```go
// 检查点保存器位于 orchestration/graph 包（已在上方导入）

// 使用检查点
saver := graph.NewMemoryCheckpointSaver()

compiled, _ := g.Compile(
    graph.WithCheckpointSaver(saver),
)

// 执行会自动保存检查点
result, _ := compiled.Run(ctx, state)

// 从检查点恢复
checkpoints := saver.List(threadID)
latest := checkpoints[len(checkpoints)-1]
compiled.ResumeFromCheckpoint(ctx, latest)
```

更多详情参见 [DESIGN.md](../DESIGN.md#图编排)。
