<div align="right">语言: 中文 | <a href="getting-started.en.md">English</a></div>

# 快速入门指南

本指南将帮助您快速上手 Hexagon AI Agent 框架。

## 安装

```bash
go get github.com/hexagon-codes/hexagon
```

## 最简示例

使用少量代码即可创建一个 AI Agent：

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/hexagon-codes/ai-core/llm/openai"
    "github.com/hexagon-codes/hexagon/agent"
)

func main() {
    // 创建 LLM Provider
    provider := openai.New("your-api-key")

    // 创建 Agent
    myAgent := agent.NewBaseAgent(
        agent.WithLLM(provider),
        agent.WithSystemPrompt("你是一个有帮助的助手"),
    )

    // 执行
    output, err := myAgent.Invoke(context.Background(), agent.Input{
        Query: "你好，请介绍一下你自己",
    })
    if err != nil {
        log.Fatalf("agent execution failed: %v", err)
    }

    fmt.Println(output.Content)
}
```

## 核心概念

### Agent

Agent 是 Hexagon 的核心概念，代表一个可执行的 AI 实体。每个 Agent 可以：

- 接收用户输入
- 调用 LLM 进行推理
- 使用工具执行任务
- 返回处理结果

### Runnable 与 Component

`core.Runnable[I, O]` 是 Hexagon 可执行组件的统一抽象，Agent 实现了该接口。`core.Component[I, O]` 是为旧代码保留的兼容接口，直接嵌入 `Runnable`。新代码应优先使用 `Runnable` 和 `Invoke`：

```go
type Runnable[I, O any] interface {
    Invoke(ctx context.Context, input I, opts ...Option) (O, error)
    Stream(ctx context.Context, input I, opts ...Option) (*StreamReader[O], error)
    Batch(ctx context.Context, inputs []I, opts ...Option) ([]O, error)
    Collect(ctx context.Context, input *StreamReader[I], opts ...Option) (O, error)
    Transform(ctx context.Context, input *StreamReader[I], opts ...Option) (*StreamReader[O], error)
    BatchStream(ctx context.Context, inputs []I, opts ...Option) (*StreamReader[O], error)

    Name() string
    Description() string
    InputSchema() *Schema
    OutputSchema() *Schema
}

type Component[I, O any] interface {
    Runnable[I, O]
}
```

Agent 的 `Run` 仅作为向后兼容方法保留；新代码使用 `Invoke`。

### 中间件

使用中间件扩展 Agent 功能：

```go
chain := agent.NewMiddlewareChain(
    agent.RecoverMiddleware(),    // panic 恢复
    agent.LoggingMiddleware(nil), // 日志记录
    agent.TimeoutMiddleware(30*time.Second), // 超时控制
)

handler := chain.WrapAgent(myAgent)
output, err := handler(ctx, input)
```

## 添加工具

让 Agent 能够执行具体任务：

```go
import "github.com/hexagon-codes/ai-core/tool"

// 定义搜索工具
searchTool := tool.NewFunc("web_search",
    "搜索网页信息",
    func(ctx context.Context, input struct {
        Query string `json:"query" description:"搜索关键词"`
    }) (string, error) {
        // 实现搜索逻辑
        return "搜索结果...", nil
    },
)

// 创建带工具的 Agent
myAgent := agent.NewReAct(
    agent.WithLLM(llm),
    agent.WithTools(searchTool),
)
```

## 使用记忆

为 Agent 添加对话记忆：

```go
import "github.com/hexagon-codes/ai-core/memory"

// 创建记忆（保留最近 10 条消息）
mem := memory.NewBuffer(10)

// 创建带记忆的 Agent
myAgent := agent.NewBaseAgent(
    agent.WithLLM(llm),
    agent.WithMemory(mem),
)
```

## RAG 检索增强

集成知识库增强 Agent 能力：

```go
import (
    "github.com/hexagon-codes/hexagon/rag"
    "github.com/hexagon-codes/hexagon/rag/embedder"
    "github.com/hexagon-codes/ai-core/store/vector/qdrant"
)

// 创建向量存储（Qdrant 实现位于 ai-core）
store, _ := qdrant.NewWithOptions(
    qdrant.WithCollection("docs"),
    qdrant.WithDimension(1536),
)

// 创建嵌入器
emb := embedder.NewOpenAIEmbedder(provider)

// 创建 RAG 引擎
engine := rag.NewEngine(
    rag.WithStore(store),
    rag.WithEngineEmbedder(emb),
    rag.WithEngineTopK(3),
)

// 索引文档并查询
_ = engine.Index(ctx, docs)
answer, _ := engine.Query(ctx, "什么是 Hexagon？")
```

ai-core v0.2.7 起，新建 Qdrant 集合默认使用 SHA-256 派生的 UUIDv8 point ID。迁移旧集合时，使用 `qdrant.WithPointIDStrategy(qdrant.PointIDLegacyHash31)` 临时读取旧数据，再将数据重建到采用默认 UUIDv8 策略的新集合；不要在同一集合中混用两种策略。

## 可观测性

添加指标和追踪：

```go
import "github.com/hexagon-codes/hexagon/observe/metrics"

// 获取指标收集器
collector := metrics.GetHexagonMetrics()

// 记录 Agent 执行
collector.RecordAgentRun(ctx, "my-agent", duration, err)

// 获取统计汇总
summary := collector.GetSummary()
fmt.Printf("总执行次数: %d\n", summary.TotalAgentRuns)
```

## 下一步

- [Agent 开发指南](agent-guide.md) - 深入了解 Agent 开发
- [RAG 系统使用](rag-guide.md) - 构建知识增强应用
- [插件开发指南](plugin-guide.md) - 扩展框架功能
