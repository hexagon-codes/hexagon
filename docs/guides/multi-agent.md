<div align="right">语言: 中文 | <a href="multi-agent.en.md">English</a></div>

# 多 Agent 协作指南

Hexagon 提供强大的多 Agent 协作能力，支持团队协作、Agent 交接和网络通信。

## 角色系统

### 定义角色

```go
import "github.com/hexagon-codes/hexagon/agent"

role := agent.Role{
    Name:      "研究员",
    Goal:      "深入研究技术问题并提供详细分析",
    Backstory: "你是一名经验丰富的技术研究员，擅长分析复杂的技术问题。",
}

researcher := agent.NewBaseAgent(
    agent.WithRole(role),
    agent.WithLLM(provider),
)
```

### 角色最佳实践

**好的角色定义**:
```go
role := agent.Role{
    Name: "客服专员",
    Goal: "快速响应客户问题，提供专业的解决方案",
    Backstory: `你是一名经验丰富的客服专员，工作了5年。
你了解公司所有产品和服务，能够快速定位问题并提供解决方案。
你的沟通风格友好、专业，始终站在客户角度思考问题。`,
}
```

**要点**:
- Name: 清晰的角色名称
- Goal: 具体可衡量的目标
- Backstory: 丰富的背景故事，增强角色感

## Team 协作

### 工作模式

#### 1. Sequential (顺序模式)

Agent 按顺序执行任务。

```go
team := agent.NewTeam(
    agent.WithTeamName("research-team"),
    agent.WithTeamMode(agent.TeamModeSequential),
    agent.WithTeamAgents(researcher, writer, reviewer),
)

result, _ := team.Run(ctx, agent.TeamInput{
    Task: "研究并撰写关于 RAG 的技术文章",
})
```

**适用场景**: 流水线式任务，如 研究 → 撰写 → 审核

#### 2. Hierarchical (层级模式)

有一个 Manager Agent 协调其他 Agent。

```go
manager := agent.NewBaseAgent(
    agent.WithRole(agent.Role{
        Name: "项目经理",
        Goal: "协调团队完成项目",
    }),
)

team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeHierarchical),
    agent.WithTeamManager(manager),
    agent.WithTeamAgents(researcher, developer, tester),
)
```

**适用场景**: 需要动态任务分配和协调

#### 3. Consensus (共识模式)

所有 Agent 投票达成共识。

```go
team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeConsensus),
    agent.WithTeamAgents(expert1, expert2, expert3),
    agent.WithConsensusThreshold(0.67), // 67% 同意即通过
)
```

**适用场景**: 决策类任务，需要多个专家意见

#### 4. Parallel (并行模式)

Agent 并行执行，最后合并结果。

```go
team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeParallel),
    agent.WithTeamAgents(agent1, agent2, agent3),
)
```

**适用场景**: 独立子任务可并行处理

### 完整示例

```go
package main

import (
    "context"
    "fmt"

    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/ai-core/llm/openai"
)

func main() {
    provider := openai.New("your-api-key")

    // 定义 Agent
    researcher := agent.NewBaseAgent(
        agent.WithName("researcher"),
        agent.WithRole(agent.Role{
            Name:      "研究员",
            Goal:      "深入研究并收集信息",
            Backstory: "你是技术研究专家",
        }),
        agent.WithLLM(provider),
    )

    writer := agent.NewBaseAgent(
        agent.WithName("writer"),
        agent.WithRole(agent.Role{
            Name:      "作者",
            Goal:      "将研究内容整理成文章",
            Backstory: "你是专业技术作家",
        }),
        agent.WithLLM(provider),
    )

    reviewer := agent.NewBaseAgent(
        agent.WithName("reviewer"),
        agent.WithRole(agent.Role{
            Name:      "审核员",
            Goal:      "审核文章质量",
            Backstory: "你是资深编辑",
        }),
        agent.WithLLM(provider),
    )

    // 创建团队
    team := agent.NewTeam(
        agent.WithTeamName("content-team"),
        agent.WithTeamMode(agent.TeamModeSequential),
        agent.WithTeamAgents(researcher, writer, reviewer),
    )

    // 执行任务
    ctx := context.Background()
    result, err := team.Run(ctx, agent.TeamInput{
        Task: "撰写一篇关于 AI Agent 的技术文章",
    })

    if err != nil {
        panic(err)
    }

    fmt.Println(result.Output)
}
```

## Agent Handoff (交接)

Agent 之间可以相互交接任务。

### 基本交接

```go
// Agent A 执行任务并交接给 Agent B
handoff := agent.NewHandoff(
    agent.WithHandoffFrom(agentA),
    agent.WithHandoffTo(agentB),
    agent.WithHandoffCondition(func(result agent.Output) bool {
        // 当满足条件时交接
        return result.Status == "need_review"
    }),
)

result, _ := handoff.Execute(ctx, input)
```

### 路由交接

根据结果动态选择交接目标。

```go
router := agent.NewRouter(
    agent.WithRoutes(map[string]agent.Agent{
        "technical": techAgent,
        "business":  bizAgent,
        "general":   generalAgent,
    }),
    agent.WithRouteDecision(func(ctx context.Context, input agent.Input) (string, error) {
        // 使用 LLM 决定路由
        // 返回 "technical", "business" 或 "general"
    }),
)
```

### 循环交接

多个 Agent 循环协作直到完成。

```go
loop := agent.NewHandoffLoop(
    agent.WithLoopAgents(coder, reviewer, tester),
    agent.WithMaxIterations(5),
    agent.WithStopCondition(func(result agent.Output) bool {
        return result.Metadata["tests_passed"] == true
    }),
)
```

## Agent 网络通信

### 点对点通信

```go
// Agent A 发送消息给 Agent B
agentA.Send(ctx, agentB, agent.Message{
    Type:    "request",
    Content: "请帮我分析这个问题",
    Data: map[string]any{
        "problem": problem,
    },
})

// Agent B 接收消息
msgChan := agentB.Receive(ctx)
for msg := range msgChan {
    // 处理消息
    response := processMessage(msg)

    // 回复
    agentB.Send(ctx, agentA, response)
}
```

### 广播通信

```go
network := agent.NewNetwork(
    agent.WithNetworkAgents(agent1, agent2, agent3),
)

// 广播消息给所有 Agent
network.Broadcast(ctx, agent.Message{
    Type:    "announcement",
    Content: "开始新任务",
})
```

### 订阅模式

```go
// Agent 订阅特定主题
network.Subscribe(agent1, "data_updated")
network.Subscribe(agent2, "data_updated")

// 发布消息
network.Publish(ctx, "data_updated", agent.Message{
    Content: "数据已更新",
    Data: map[string]any{
        "version": "1.2",
    },
})
```

## 共识机制

多个 Agent 协商达成共识。

### 投票共识

```go
consensus := agent.NewConsensus(
    agent.WithConsensusAgents(agent1, agent2, agent3),
    agent.WithConsensusStrategy(agent.ConsensusStrategyVoting),
    agent.WithConsensusThreshold(0.67), // 需要67%同意
)

decision, _ := consensus.Decide(ctx, agent.ConsensusInput{
    Proposal: "是否采用新的技术方案",
    Options:  []string{"同意", "拒绝", "需要更多信息"},
})

fmt.Println(decision.Result) // "同意" / "拒绝" / "需要更多信息"
```

### 加权投票

```go
consensus := agent.NewConsensus(
    agent.WithConsensusWeights(map[string]float64{
        "senior_expert": 2.0, // 资深专家权重2
        "junior_expert": 1.0, // 初级专家权重1
    }),
)
```

## 状态管理

### 共享状态

```go
// 全局状态
globalState := agent.NewGlobalState()
globalState.Set("project_status", "in_progress")

// Agent 访问全局状态
status := globalState.Get("project_status")
```

### Session 状态

```go
// 会话级状态
session := agent.NewSession()
session.Set("user_id", "123")
session.Set("conversation_history", []string{})

// Agent 使用 Session
result, _ := agentA.RunWithSession(ctx, input, session)
```

## 最佳实践

### 1. 角色分工明确

```go
// ✅ 好的做法
researcher := agent.NewBaseAgent(
    agent.WithRole(agent.Role{
        Name: "数据分析师",
        Goal: "分析数据并生成报告",
    }),
)

writer := agent.NewBaseAgent(
    agent.WithRole(agent.Role{
        Name: "技术作家",
        Goal: "将分析结果写成易懂的文章",
    }),
)

// ❌ 不好的做法
generic := agent.NewBaseAgent(
    agent.WithRole(agent.Role{
        Name: "助手",
        Goal: "做各种事情",
    }),
)
```

### 2. 合理的团队规模

- 2-5 个 Agent: 最佳规模
- 5-10 个: 需要更强的协调
- 10+ 个: 考虑分层或分组

### 3. 避免循环依赖

```go
// ❌ 不好的做法
agentA → agentB → agentC → agentA // 循环

// ✅ 好的做法
agentA → agentB → agentC // 单向
```

### 4. 超时和重试

```go
team := agent.NewTeam(
    agent.WithTeamTimeout(5 * time.Minute),
    agent.WithTeamRetryPolicy(&agent.RetryPolicy{
        MaxRetries: 3,
        BackoffStrategy: agent.ExponentialBackoff,
    }),
)
```

### 5. 监控和可观测

```go
import "github.com/hexagon-codes/hexagon/observe"

// 为团队添加观察者
team.OnAgentStart(func(agentName string) {
    fmt.Printf("Agent %s 开始工作\n", agentName)
})

team.OnAgentComplete(func(agentName string, result agent.Output) {
    fmt.Printf("Agent %s 完成工作\n", agentName)
})

team.OnAgentError(func(agentName string, err error) {
    fmt.Printf("Agent %s 出错: %v\n", agentName, err)
})
```

## 实战案例

### 案例1: 内容创作团队

```go
// 研究员 → 作家 → 审核员 → 发布员
team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeSequential),
    agent.WithTeamAgents(
        newResearcher(),
        newWriter(),
        newReviewer(),
        newPublisher(),
    ),
)
```

### 案例2: 客服系统

```go
// 路由 Agent 根据问题类型分发给专业 Agent
router := agent.NewRouter(
    agent.WithRoutes(map[string]agent.Agent{
        "technical_support": techAgent,
        "billing":           billingAgent,
        "general":           generalAgent,
    }),
)
```

### 案例3: 代码审核

```go
// 多个审核员并行审核，达成共识
team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeConsensus),
    agent.WithTeamAgents(
        seniorReviewer1,
        seniorReviewer2,
        juniorReviewer,
    ),
    agent.WithConsensusThreshold(0.67),
)
```

## 调试技巧

```go
// 启用团队调试模式
team.SetDebug(true)

// 查看 Agent 交互
team.OnMessage(func(from, to string, msg agent.Message) {
    fmt.Printf("%s → %s: %s\n", from, to, msg.Content)
})

// 查看决策过程
consensus.OnVote(func(agent string, vote string) {
    fmt.Printf("%s 投票: %s\n", agent, vote)
})
```

## A2A 协议：跨框架 Agent 协作

Hexagon 支持 Google A2A (Agent-to-Agent) 协议，使你的 Agent 能够与其他框架、其他语言实现的 Agent 进行标准化通信。

### 什么是 A2A 协议

A2A 是一个开放协议，定义了 AI Agent 之间通信的标准方式：

- **Agent Card**: Agent 能力描述 (`.well-known/agent-card.json`)
- **Task**: 任务生命周期管理 (提交 → 执行 → 完成)
- **Message**: 多模态消息 (文本、文件、数据)
- **Streaming**: 实时流式响应
- **Push Notification**: 任务状态推送

### A2A 与 Hexagon 多 Agent 的关系

| 场景 | 使用方式 |
|------|---------|
| 同进程 Agent 协作 | 使用 Team、Network、Handoff |
| 跨服务 Agent 协作 | 使用 A2A 协议 |
| 混合场景 | 两者结合使用 |

### 暴露 Agent 为 A2A 服务

```go
import (
    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/hexagon/agent/a2a"
)

// 创建 Hexagon Agent
myAgent := agent.NewBaseAgent(
    agent.WithName("assistant"),
    agent.WithRole(agent.Role{
        Name: "助手",
        Goal: "帮助用户解决问题",
    }),
    agent.WithLLM(provider),
)

// 暴露为 A2A 服务
server := a2a.ExposeAgent(myAgent, ":8080")
defer server.Stop(ctx)
```

### 连接远程 A2A Agent

```go
// 连接远程 A2A Agent
remoteAgent := a2a.ConnectToA2AAgent("http://remote-agent:8080")

// 像使用本地 Agent 一样使用
result, _ := remoteAgent.Run(ctx, agent.Input{
    Messages: []llm.Message{{Role: "user", Content: "你好"}},
})
```

### 混合团队：本地 + 远程 Agent

```go
// 本地 Agent
localAgent := agent.NewBaseAgent(...)

// 远程 A2A Agent
remoteAgent1 := a2a.ConnectToA2AAgent("http://python-agent:8080")
remoteAgent2 := a2a.ConnectToA2AAgent("http://nodejs-agent:8080")

// 组成混合团队
team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeSequential),
    agent.WithTeamAgents(localAgent, remoteAgent1, remoteAgent2),
)

// 执行任务
result, _ := team.Run(ctx, agent.TeamInput{
    Task: "分析数据并生成报告",
})
```

### A2A 适用场景

1. **跨语言协作**: Go Agent 与 Python/Node.js Agent 协作
2. **微服务架构**: Agent 作为独立服务部署
3. **多团队协作**: 不同团队开发的 Agent 集成
4. **第三方 Agent**: 接入外部 A2A 兼容服务

> 📖 详细信息请参阅 [A2A 协议指南](./a2a-protocol.md)

## 下一步

- 学习 [图编排中的多 Agent](./graph-orchestration.md#多-agent-节点)
- 了解 [性能优化](./performance-optimization.md#多-agent-优化)
- 掌握 [可观测性](./observability.md)
