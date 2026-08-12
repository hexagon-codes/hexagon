<div align="right">语言: 中文 | <a href="agent-guide.en.md">English</a></div>

# Agent 开发指南

本指南详细介绍如何使用 Hexagon 开发各类 AI Agent。

## Agent 类型

### BaseAgent

基础 Agent，提供最简单的 LLM 调用能力：

```go
assistant := agent.NewBaseAgent(
    agent.WithName("assistant"),
    agent.WithLLM(provider),
    agent.WithSystemPrompt("你是一个专业的助手"),
)
```

### ReActAgent

实现 ReAct (Reasoning + Acting) 模式，支持多步推理和工具调用：

```go
researcher := agent.NewReAct(
    agent.WithName("researcher"),
    agent.WithLLM(provider),
    agent.WithTools(searchTool, calculatorTool),
    agent.WithMaxIterations(10),
)
```

### RAG（检索增强）

框架没有专门的 RAG Agent 构造器，而是用 `rag.Engine` 检索知识库、把上下文拼进查询后交给普通 Agent 回答：

```go
import (
    "fmt"

    "github.com/hexagon-codes/hexagon/rag"
    "github.com/hexagon-codes/hexagon/rag/embedder"
)

// 创建 RAG 引擎
engine := rag.NewEngine(
    rag.WithStore(store),
    rag.WithEngineEmbedder(embedder.NewOpenAIEmbedder(provider)),
    rag.WithEngineTopK(5),
)
if err := engine.Index(ctx, docs); err != nil {
    return fmt.Errorf("index documents: %w", err)
}

// 检索上下文并交给 Agent 回答
question := "What is Hexagon?"
knowledge, err := engine.Query(ctx, question)
if err != nil {
    return fmt.Errorf("query knowledge: %w", err)
}
query := fmt.Sprintf(
    "Use the following knowledge to answer the question.\n\n%s\n\nQuestion: %s",
    knowledge, question,
)
output, err := myAgent.Invoke(ctx, agent.Input{
    Query: query,
})
if err != nil {
    return fmt.Errorf("invoke agent: %w", err)
}
fmt.Println(output.Content)
```

`agent.Input.Context` 是供编排和调用方传递元数据的字段；当前 `BaseAgent` 和 `ReActAgent` 不会自动把它写入 LLM 消息。因此 RAG 上下文必须像上例一样显式拼入 `Query`（或由应用自己的提示词构造器处理）。

## 配置选项

### 基础配置

```go
agent.WithID("agent-123")            // 设置稳定 ID（可选）
agent.WithName("my-agent")           // 设置名称
agent.WithDescription("...")        // 设置描述
agent.WithLLM(provider)              // 设置 LLM Provider
agent.WithSystemPrompt("...")        // 设置系统提示
agent.WithMaxIterations(10)          // 设置最大推理迭代次数
```

`WithMaxIterations` 只影响具有迭代循环的 Agent（如 `ReActAgent`）。`WithVerbose` 仍是兼容配置字段，但当前 `BaseAgent`/`ReActAgent` 不会据此输出日志；需要日志时使用请求级 `LoggingMiddleware` 或 runtime middleware。

### Provider 配置

```go
provider := openai.New(
    os.Getenv("OPENAI_API_KEY"),
    openai.WithModel("gpt-4o"),
    openai.WithRequestTimeout(60*time.Second),
)
```

`openai.New` 的第一个参数是 API Key；其选项配置默认模型、Base URL、HTTP 客户端和超时等 Provider 属性。温度和最大 token 属于 `llm.CompletionRequest`，不是 `openai.New` 或 Agent 选项。当前 `BaseAgent`/`ReActAgent` 不暴露逐请求采样参数；需要这些参数时，可直接调用 Provider：

```go
temperature := 0.2
response, err := provider.Complete(ctx, llm.CompletionRequest{
    Messages: []llm.Message{
        {Role: llm.RoleUser, Content: "Summarize the current task."},
    },
    Temperature: &temperature,
    MaxTokens:   1024,
})
if err != nil {
    return fmt.Errorf("complete request: %w", err)
}
fmt.Println(response.Content)
```

### 工具配置

`tool.NewFunc` 从输入结构体生成工具及其 JSON Schema。工具调用由 `ReActAgent` 执行；`BaseAgent.Invoke` 只进行一次普通 LLM 补全，不会执行已注册工具。

```go
type WordCountInput struct {
    Text string `json:"text" desc:"Text to count" required:"true"`
}

wordCountTool := tool.NewFunc(
    "word_count",
    "Count Unicode characters in text",
    func(_ context.Context, input WordCountInput) (int, error) {
        return utf8.RuneCountInString(input.Text), nil
    },
)

researcher := agent.NewReAct(
    agent.WithLLM(provider),
    agent.WithTools(wordCountTool),
)
```

工具和 Agent 都公开当前 Schema：`wordCountTool.Schema()` 返回工具输入 Schema；`researcher.InputSchema()` 和 `researcher.OutputSchema()` 返回 Agent 的输入、输出 Schema。三者都是 `*schema.Schema`（Agent 侧经 `core.Schema` 别名公开）。

### 记忆配置

```go
mem := memory.NewBuffer(20) // 最多保留 20 条 entry

researcher := agent.NewReAct(
    agent.WithLLM(provider),
    agent.WithMemory(mem),
)
```

未传 `WithMemory` 时，`BaseAgent`/`ReActAgent` 默认创建容量为 100 的 Buffer Memory。`ReActAgent` 会在调用前检索历史并在成功后保存本轮内容；`BaseAgent.Invoke` 目前不会自动读写 Memory。

## 中间件系统

Hexagon 有两个不同层次的中间件：本节的 `agent.AgentMiddleware` 包装一次完整的 Agent 请求；`runtime.Middleware` 则通过 `agent.WithMiddleware` 注入 ReAct 的 LLM/工具生命周期。

### 内置中间件

```go
// Panic 恢复
agent.RecoverMiddleware()

// 日志记录
agent.LoggingMiddleware(logger)

// 指标采集
agent.MetricsMiddleware(collector)

// 超时控制
agent.TimeoutMiddleware(30*time.Second)

// 重试机制
agent.RetryMiddleware(3, time.Second)

// 追踪
agent.TracingMiddleware("my-service")

// 限流
agent.RateLimitMiddleware(limiter)
```

### 组合中间件

```go
chain := agent.NewMiddlewareChain(
    agent.RecoverMiddleware(),
    agent.LoggingMiddleware(nil),
    agent.TimeoutMiddleware(30*time.Second),
)

// 包装 Agent
handler := chain.Wrap(func(ctx context.Context, input agent.Input) (agent.Output, error) {
    return myAgent.Invoke(ctx, input)
})

// 执行
output, err := handler(ctx, input)
if err != nil {
    return fmt.Errorf("handle agent request: %w", err)
}
fmt.Println(output.Content)
```

### 预设组合

```go
// 默认中间件（Recover + Logging + Timeout）
middlewares := agent.DefaultMiddlewares()

// 生产环境中间件
middlewares := agent.ProductionMiddlewares("my-service", metricsCollector)
```

## 自定义中间件

```go
func MyMiddleware() agent.AgentMiddleware {
    return func(next agent.AgentHandler) agent.AgentHandler {
        return func(ctx context.Context, input agent.Input) (agent.Output, error) {
            // 前置处理
            log.Println("starting agent execution")

            // 调用下一个处理器
            output, err := next(ctx, input)

            // 后置处理
            log.Println("agent execution finished")

            return output, err
        }
    }
}
```

### Runtime 生命周期中间件

```go
lifecycle := runtime.MiddlewareFuncSet{
    BeforeLLMFunc: func(_ context.Context, state *runtime.State) error {
        log.Printf("before LLM: messages=%d", len(state.Messages))
        return nil
    },
}

researcher := agent.NewReAct(
    agent.WithLLM(provider),
    agent.WithMiddleware(lifecycle),
)
```

`runtime.Middleware` 还提供 `AfterLLM`、`BeforeTool`、`AfterTool` 和 `Finalize` 生命周期方法；`runtime.MiddlewareFuncSet` 允许只实现需要的回调。

## 状态管理

### 四层状态

`agent.StateManager` 是显式的应用级状态工具，与 `WithMemory` 和 ReAct 内部的 `runtime.State` 不是同一个对象。调用方需要自行创建、保存并在合适的边界调用 `NewTurn`：

```go
global := agent.NewGlobalState()
state := agent.NewStateManager("session-123", global)

// Turn 状态：单轮对话
state.Turn().Set("key", value)

// Session 状态：会话级别
state.Session().Set("user_id", "123")

// Agent 状态：Agent 持久状态
state.Agent().Set("config", config)

// Global 状态：全局共享
state.Global().Set("shared_data", data)

// 开始下一轮并清空 Turn 状态
state.NewTurn()
```

## 角色系统

定义 Agent 的角色特征。`WithRole` 保存角色元数据，但当前 `BaseAgent`/`ReActAgent` 不会自动把它加入系统提示；需要角色影响模型行为时，应显式使用 `Role.ToSystemPrompt()`：

```go
role := agent.Role{
    Name:      "Researcher",
    Goal:      "Collect and analyze information",
    Backstory: "You are experienced at extracting key insights from complex information.",
}
roleAgent := agent.NewBaseAgent(
    agent.WithLLM(provider),
    agent.WithRole(role),
    agent.WithSystemPrompt(role.ToSystemPrompt()),
)
```

## 团队协作

创建多 Agent 团队：

假设 `researcher`、`writer` 和 `reviewer` 已经是初始化完成的 `agent.Agent`：

```go
// 创建团队（第一个参数为团队名称）
team := agent.NewTeam("content-team",
    agent.WithMode(agent.TeamModeSequential),
    agent.WithAgents(researcher, writer, reviewer),
)

// 执行团队任务
output, err := team.Invoke(ctx, agent.Input{Query: "Prepare a technical article."})
if err != nil {
    return fmt.Errorf("invoke team: %w", err)
}
fmt.Println(output.Content)
```

### 团队模式

- `agent.TeamModeSequential`: 顺序执行
- `agent.TeamModeHierarchical`: 层级执行
- `agent.TeamModeCollaborative`: 协作执行
- `agent.TeamModeRoundRobin`: 轮询执行

层级模式必须通过 `agent.WithManager(manager)` 提供 Manager，并仍需通过 `agent.WithAgents(...)` 提供团队成员。`WithManager` 会自动把模式设为 `TeamModeHierarchical`。

## 流式输出

`Stream` 返回 `*streamx.StreamReader[agent.Output]`。当前 `BaseAgent`、`ReActAgent` 和 `Team` 会先完成调用，再把单个完整 `Output` 放入流中；这不是 Provider 的 token 级流式输出。

```go
func printAgentStream(ctx context.Context, myAgent agent.Agent, input agent.Input) (err error) {
    reader, err := myAgent.Stream(ctx, input)
    if err != nil {
        return fmt.Errorf("start agent stream: %w", err)
    }
    defer func() {
        err = errors.Join(err, reader.Close())
    }()

    for {
        chunk, recvErr := reader.Recv()
        if errors.Is(recvErr, io.EOF) {
            return nil
        }
        if recvErr != nil {
            return fmt.Errorf("receive agent stream: %w", recvErr)
        }
        if _, writeErr := fmt.Print(chunk.Content); writeErr != nil {
            return fmt.Errorf("write agent output: %w", writeErr)
        }
    }
}
```

## 错误处理

```go
output, err := myAgent.Invoke(ctx, input)
if err != nil {
    if errors.Is(err, context.DeadlineExceeded) {
        return fmt.Errorf("agent timed out: %w", err)
    }
    return fmt.Errorf("invoke agent: %w", err)
}
if _, err := fmt.Println(output.Content); err != nil {
    return fmt.Errorf("write agent output: %w", err)
}
```

`Run` 仅为向后兼容方法，已标记 deprecated；新代码应使用 `Invoke`。错误用 `%w` 逐层包装后，可用 `errors.Is`/`errors.As` 判断根因。

## 最佳实践

1. **首选 Invoke**: 新代码不要以兼容方法 `Run` 作为主入口
2. **设置超时**: 用 `context.WithTimeout` 或请求级中间件限制完整调用
3. **按边界使用中间件**: 请求级关注恢复、日志和限流；runtime 级关注 LLM/工具生命周期
4. **谨慎重试**: 只有完整调用及其工具副作用可安全重复时，才使用请求级重试
5. **限制迭代**: 为 `ReActAgent` 设置合理的 `MaxIterations`，并检查输出的 `StopReason`
6. **清理资源**: 关闭 StreamReader，并处理 `Close` 返回的错误
