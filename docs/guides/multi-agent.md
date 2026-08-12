<div align="right">语言: 中文 | <a href="multi-agent.en.md">English</a></div>

# 多 Agent 协作指南

Hexagon 当前提供四类进程内多 Agent 原语：`Team` 负责固定编排，`ParallelAgent` 负责并行扇出，`SwarmRunner` 负责由工具调用驱动的 Agent 交接，`AgentNetwork` 负责按 Agent ID 传递消息。需要对多个 Agent 的回答做投票时，使用 `ConsensusProtocol`；跨服务通信则使用 `agent/a2a` 的服务器和客户端。

本文中的 `researcher`、`writer`、`reviewer`、`manager` 等变量均表示已经配置好 LLM 的 `agent.Agent`。例如：

```go
researcher := agent.NewReAct(
    agent.WithName("researcher"),
    agent.WithRole(agent.Role{
        Name:      "研究员",
        Goal:      "收集并核验事实",
        Backstory: "你是一名严谨的技术研究员。",
    }),
    agent.WithLLM(provider),
)
```

## 如何选择协作原语

| 需求 | 原语 | 关键行为 |
|------|------|----------|
| 固定顺序、管理者协调、协作汇总或轮询 | `Team` | 接收一个 `agent.Input`，按配置模式执行成员 |
| 同一输入并行交给多个 Agent | `ParallelAgent` | 并行执行并合并成功结果 |
| 由模型决定转交给哪个 Agent | `TransferTo` + `SwarmRunner` | 模型调用转交工具后切换当前 Agent |
| 按 ID 点对点发送或广播 | `AgentNetwork` | 消息进入注册节点的收件箱或异步路由器 |
| 多 Agent 投票 | `ConsensusProtocol` | 并发询问在线 Agent 并计算共识结果 |
| 跨进程或跨语言调用 | `agent/a2a` | HTTP + JSON-RPC 任务接口 |

## Team：固定团队编排

### 创建和执行

`NewTeam` 的第一个参数是团队名称，成员和模式通过 option 配置。执行输入始终是 `agent.Input`，其中 `Query` 是任务文本，`Context` 是可选的附加数据。

```go
team := agent.NewTeam(
    "content-team",
    agent.WithTeamDescription("研究、撰写和审核"),
    agent.WithAgents(researcher, writer, reviewer),
    agent.WithMode(agent.TeamModeSequential),
)

output, err := team.Run(ctx, agent.Input{
    Query: "撰写一篇 RAG 技术说明",
    Context: map[string]any{
        "audience": "Go developers",
    },
})
if err != nil {
    return err
}
fmt.Println(output.Content)
```

### 四种模式

| 模式 | 执行语义 | 配置 |
|------|----------|------|
| `TeamModeSequential` | 按成员顺序执行；前一个输出的 `Content` 成为下一个输入的 `Query`，输出 `Metadata` 成为下一个输入的 `Context` | 默认模式，或 `WithMode` |
| `TeamModeHierarchical` | Manager 先生成指导，成员分别处理原任务，Manager 最后汇总成功结果 | `WithManager(manager)`，该 option 会自动设置模式 |
| `TeamModeCollaborative` | 所有成员并发处理同一输入，成功输出被拼接汇总 | `WithMode` |
| `TeamModeRoundRobin` | 成员循环执行，上一个输出成为下一个输入；当 `Metadata["done"]` 为 `true` 或达到最大轮数时停止 | `WithMode` + `WithMaxRounds` |

层级模式必须同时提供 Manager 和至少一个成员：

```go
team := agent.NewTeam(
    "review-team",
    agent.WithAgents(researcher, writer, reviewer),
    agent.WithManager(manager),
)

output, err := team.Run(ctx, agent.Input{Query: "评审这份技术方案"})
```

`Team` 不提供独立的并行或共识模式。只需并行扇出时使用 `ParallelAgent`；需要投票时使用 `ConsensusProtocol`。

## ParallelAgent：并行扇出

`ParallelAgent` 把同一个输入并行传给所有子 Agent。默认合并函数按成员顺序拼接非空 `Content`；部分子 Agent 失败时仍返回成功结果，并在 `Output.Metadata` 中记录 `failed` 和 `errors`。只有全部子 Agent 都失败时才返回错误。

```go
parallel := agent.NewParallelAgent(
    "independent-review",
    []agent.Agent{securityReviewer, apiReviewer, testReviewer},
    agent.WithMaxParallel(2),
)

output, err := parallel.Run(ctx, agent.Input{
    Query: "独立评审这次 API 变更",
})
if err != nil {
    return err
}

failed, _ := output.Metadata["failed"].(int)
fmt.Printf("failed=%d\n%s\n", failed, output.Content)
```

需要结构化合并时传入 `WithMergeFunc`：

```go
parallel := agent.NewParallelAgent(
    "review-summary",
    []agent.Agent{securityReviewer, apiReviewer},
    agent.WithMergeFunc(func(outputs []agent.Output) agent.Output {
        parts := make([]string, 0, len(outputs))
        for _, output := range outputs {
            if output.Content != "" {
                parts = append(parts, output.Content)
            }
        }
        return agent.Output{Content: strings.Join(parts, "\n---\n")}
    }),
)
```

## TransferTo 与 SwarmRunner：模型驱动交接

`TransferTo(target)` 创建一个真实的 LLM 工具。把它加入当前 Agent 的工具列表后，模型可以使用 `message`、`reason` 和可选 `context` 调用该工具。`SwarmRunner` 从输出的工具调用记录中读取交接结果，将 `message` 和 `context` 作为目标 Agent 的新输入，然后继续执行。

```go
billing := agent.NewReAct(
    agent.WithName("billing"),
    agent.WithDescription("处理账单问题"),
    agent.WithLLM(provider),
)

frontDesk := agent.NewReAct(
    agent.WithName("front-desk"),
    agent.WithDescription("识别用户诉求"),
    agent.WithLLM(provider),
    agent.WithTools(agent.TransferTo(billing)),
)

runner := agent.NewSwarmRunner(frontDesk)
runner.MaxHandoffs = 4

output, err := runner.Run(ctx, agent.Input{
    Query: "请解释本月账单中的重复扣款",
})
if err != nil {
    return err
}
fmt.Println(output.Content)
```

注册转交工具并不会自动发生交接；只有当前 Agent 的模型实际调用该工具，输出中才会出现 `Handoff`。超过 `MaxHandoffs` 会返回错误。

## AgentNetwork：进程内消息传递

### 注册与点对点发送

先创建网络并注册 Agent。重复注册相同 Agent ID 会返回错误。

```go
network := agent.NewAgentNetwork(
    "review-network",
    agent.WithNetworkTopology(agent.TopologyMesh),
    agent.WithNetworkInboxSize(128),
)

if err := network.Register(sender); err != nil {
    return err
}
if err := network.Register(receiver); err != nil {
    return err
}
```

`RegisterHandler` 按消息 `Topic` 注册处理器。`SendTo` 创建的便捷消息使用空 topic；若希望它由处理器消费，应为 `""` 注册处理器并启动网络路由器。

```go
network.RegisterHandler("", func(
    ctx context.Context,
    msg *agent.NetworkMessage,
) (*agent.NetworkMessage, error) {
    fmt.Printf("%s -> %s: %v\n", msg.From, msg.To, msg.Content)
    return nil, nil
})

networkCtx, stopNetwork := context.WithCancel(context.Background())
defer stopNetwork()

if err := network.Start(networkCtx); err != nil {
    return err
}
defer network.Stop()

if err := network.SendTo(
    ctx,
    sender.ID(),
    receiver.ID(),
    "请审核这份结果",
); err != nil {
    return err
}
```

网络启动后，`SendTo` 成功表示消息已进入异步路由队列，并不表示处理器已经执行完成。网络未启动时，消息会直接投递到目标节点的 `Inbox`，不会调用已注册处理器。

### 广播

`Broadcast` 把消息副本直接投递给除发送者外的所有已注册节点：

```go
if err := network.Broadcast(
    ctx,
    sender.ID(),
    "开始新一轮审核",
); err != nil {
    return err
}
```

广播走节点收件箱，不经过 `RegisterHandler`。收件箱已满、已关闭、节点离线或 context 取消时，发送可能返回错误。

## ConsensusProtocol：投票共识

`ConsensusProtocol` 使用网络中注册且在线的 Agent 作为投票者。`Propose` 会并发调用这些 Agent；它不依赖 `AgentNetwork.Start`，因为投票直接调用注册的 Agent。

```go
network := agent.NewAgentNetwork("architecture-vote")
for _, voter := range []agent.Agent{expertA, expertB, expertC} {
    if err := network.Register(voter); err != nil {
        return err
    }
}

protocol := agent.NewConsensusProtocol(
    network,
    agent.WithConsensusStrategy(agent.ConsensusMajority),
    agent.WithConsensusThreshold(2.0/3.0),
    agent.WithMinParticipation(2.0/3.0),
    agent.WithConsensusTimeout(15*time.Second),
)

result, err := protocol.Propose(
    ctx,
    "是否接受这份架构方案？",
    []any{"accept", "reject"},
)
if err != nil {
    return err
}

if !result.Reached {
    fmt.Printf("no consensus: %s\n", result.Reason)
} else {
    fmt.Printf("decision=%v confidence=%.2f\n", result.Decision, result.Confidence)
}
```

投票值通过 Agent 回复与候选项的精确或子字符串匹配得到。超时或单个 Agent 失败会减少参与票数；这时应同时检查 `Reached`、`Participation` 和 `Reason`，不能只读取 `Decision`。

## StateManager：四层状态

`StateManager` 提供 Turn、Session、Agent 和 Global 四层状态。它与 `agent.Input.Context` 不同：`Input.Context` 只随一次调用传入，而状态管理器可以跨轮次保存值、创建快照并恢复。

```go
global := agent.NewGlobalState()
state := agent.NewStateManager("session-42", global)

state.Turn().Set("draft", "v1")
state.Session().Set("user_id", "u-123")
state.Agent().Set("preferred_style", "concise")
state.Global().Set("release", "v0.6.0")

if userID, ok := state.Session().Get("user_id"); ok {
    fmt.Println(userID)
}

snapshot := state.Snapshot()
state.NewTurn()

if err := state.Restore(snapshot); err != nil {
    return err
}

ctx = agent.ContextWithStateManager(ctx, state)
current := agent.StateManagerFromContext(ctx)
if current == nil {
    return errors.New("state manager missing")
}
```

多个状态管理器需要共享应用级数据时，应显式复用同一个 `GlobalState`。`NewTurn` 会重置 Turn 状态并增加 Session 的轮次计数。

## 超时与取消

`Team`、`ParallelAgent`、`SwarmRunner`、网络发送和 A2A 客户端都接收调用方提供的 `context.Context`。在入口统一设置截止时间，并保留 `context` 原始错误判定：

```go
runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
defer cancel()

output, err := team.Run(runCtx, agent.Input{Query: "完成本轮评审"})
if err != nil {
    switch {
    case errors.Is(err, context.DeadlineExceeded):
        return fmt.Errorf("team timed out: %w", err)
    case errors.Is(err, context.Canceled):
        return fmt.Errorf("team canceled: %w", err)
    default:
        return err
    }
}
_ = output
```

底层 Agent 或 Provider 也必须遵守传入的 context，超时才能及时终止实际工作。`WithConsensusTimeout` 另外限制投票收集时间，但投票超时通常体现为参与率不足的结果，而不是 `Propose` 错误。

## A2A：跨服务任务调用

`agent/a2a` 提供独立的服务器和客户端。A2A 客户端不是 `agent.Agent` 适配器，不能直接放入 `Team`；应用应通过 `a2a.Client` 查询 Agent Card、提交任务和查询任务状态。

### 启动服务器

```go
card := &a2a.AgentCard{
    Name:               "remote-reviewer",
    Description:        "远程审核服务",
    URL:                "http://localhost:8080",
    Version:            "0.1.0",
    DefaultInputModes:  []string{"text"},
    DefaultOutputModes: []string{"text"},
}

server := a2a.NewServer(card, a2a.NewEchoHandler())
if err := server.Start(":8080"); err != nil {
    log.Fatal(err)
}
```

`Start` 会阻塞直到服务器关闭或监听失败。需要由应用的生命周期管理代码在单独 goroutine 中启动时，应在退出阶段使用带截止时间的 `server.Stop(ctx)`。

### 查询与提交任务

```go
client := a2a.NewClient(
    "http://localhost:8080",
    a2a.WithTimeout(10*time.Second),
)
defer client.Close()

queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()

card, err := client.GetAgentCard(queryCtx)
if err != nil {
    return err
}
fmt.Println(card.Name)

task, err := client.SendMessage(queryCtx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("请审核这份方案"),
})
if err != nil {
    return err
}

stored, err := client.GetTask(queryCtx, task.ID)
if err != nil {
    return err
}
fmt.Println(stored.Status.State)
```

### 错误处理

A2A 的 JSON-RPC 错误实现了 `error`，可以使用 `GetA2AError` 读取协议错误码；网络错误和 context 错误保持普通 Go error 链：

```go
_, err := client.GetTask(queryCtx, "missing-task")
if err != nil {
    if protocolErr := a2a.GetA2AError(err); protocolErr != nil {
        if protocolErr.Code == a2a.CodeTaskNotFound {
            return fmt.Errorf("remote task not found: %w", err)
        }
        return fmt.Errorf("A2A error %d: %w", protocolErr.Code, err)
    }
    if errors.Is(err, context.DeadlineExceeded) {
        return fmt.Errorf("A2A query timed out: %w", err)
    }
    return err
}
```

## 使用边界

- 使用 `TeamModeRoundRobin` 时通过 `WithMaxRounds` 限制轮数；为 `ParallelAgent` 设置与资源容量匹配的 `WithMaxParallel`，并限制 `SwarmRunner.MaxHandoffs`。
- 对 `ParallelAgent` 的部分失败检查 `Output.Metadata`；对共识结果检查 `Reached` 和 `Participation`。
- 注册网络节点后再发送消息；异步发送成功不等于业务处理完成。
- 通过 context 统一传递取消和截止时间，不在各层创建无法由调用方取消的后台任务。
- 跨服务边界显式处理 A2A 协议错误、HTTP/网络错误和 context 错误。

## 下一步

- 阅读 [A2A 协议指南](./a2a-protocol.md)
- 阅读 [图编排最佳实践](./graph-orchestration.md)
- 阅读 [性能优化指南](./performance-optimization.md)
- 阅读 [可观测性集成指南](./observability.md)
