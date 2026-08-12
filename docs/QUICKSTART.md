<div align="right">语言: 中文 | <a href="QUICKSTART.en.md">English</a></div>

# Hexagon 快速入门指南

本指南帮助你在 30 分钟内上手 Hexagon AI Agent 框架。

## 项目简介

**Hexagon** 取名自网络热词「六边形战士」，寓意均衡覆盖多个能力维度。框架聚焦 **易用性、性能、扩展性、任务编排、可观测性、安全性** 六个核心维度，为 Go 开发者提供 AI Agent 开发基座。

### 生态系统

Hexagon 是一个完整的 AI Agent 开发生态：

| 仓库 | 说明 |
|-----|------|
| **hexagon** | AI Agent 框架核心 (编排、RAG、Graph、Hooks) |
| **ai-core** | AI 基础能力库 (LLM/Tool/Memory/Schema) |
| **toolkit** | Go 通用工具库 (lang/crypto/net/cache/util) |
| **hexagon-ui** | Dev UI 前端 (Vue 3 + TypeScript) |

## 目录

- [环境准备](#环境准备)
- [安装](#安装)
- [3 行代码入门](#3-行代码入门)
- [带工具的 Agent](#带工具的-agent)
- [RAG 检索增强](#rag-检索增强)
- [图编排](#图编排)
- [多 Agent 协作](#多-agent-协作)
- [Dev UI](#dev-ui)
- [下一步](#下一步)

---

## 环境准备

### 系统要求

- Go 1.25.12 或更高版本
- 网络连接（访问 LLM API）

### 环境变量

Hexagon 支持多种 LLM Provider，需要配置相应的 API Key：

```bash
# OpenAI (默认)
export OPENAI_API_KEY=your-api-key

# DeepSeek
export DEEPSEEK_API_KEY=your-api-key
```

---

## 安装

```bash
go get github.com/hexagon-codes/hexagon
```

验证安装：

```bash
go list -m github.com/hexagon-codes/hexagon
```

---

## 3 行代码入门

这是最简单的使用方式：

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

func main() {
    response, _ := hexagon.Chat(context.Background(), "What is Go?")
    fmt.Println(response)
}
```

运行：

```bash
export OPENAI_API_KEY=your-api-key
go run main.go
```

---

## 带工具的 Agent

Agent 可以使用工具来完成任务：

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

// 定义计算器工具的输入
type CalculatorInput struct {
    A  float64 `json:"a" desc:"第一个数字" required:"true"`
    B  float64 `json:"b" desc:"第二个数字" required:"true"`
    Op string  `json:"op" desc:"运算符" required:"true" enum:"add,sub,mul,div"`
}

func main() {
    ctx := context.Background()

    // 创建计算器工具
    calculator := hexagon.NewTool("calculator", "执行数学计算",
        func(ctx context.Context, input CalculatorInput) (float64, error) {
            switch input.Op {
            case "add":
                return input.A + input.B, nil
            case "sub":
                return input.A - input.B, nil
            case "mul":
                return input.A * input.B, nil
            case "div":
                if input.B == 0 {
                    return 0, fmt.Errorf("division by zero")
                }
                return input.A / input.B, nil
            default:
                return 0, fmt.Errorf("unknown operator: %s", input.Op)
            }
        },
    )

    // 创建带工具的 Agent
    agent := hexagon.QuickStart(
        hexagon.WithTools(calculator),
        hexagon.WithSystemPrompt("你是一个数学助手"),
    )

    // 执行查询
    output, _ := agent.Run(ctx, hexagon.Input{
        Query: "请计算 123 乘以 456",
    })

    fmt.Println(output.Content)
}
```

### 工具定义说明

- `name`: 工具名称，LLM 用来识别和调用
- `desc`: 工具描述，帮助 LLM 理解何时使用
- 输入结构体标签：
  - `json`: 字段名
  - `desc`: 字段描述
  - `required`: 是否必填
  - `enum`: 可选值列表

---

## RAG 检索增强

RAG (Retrieval-Augmented Generation) 让 Agent 能够基于外部知识库回答问题：

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/hexagon-codes/ai-core/llm/openai"
    "github.com/hexagon-codes/ai-core/store/vector"
    "github.com/hexagon-codes/hexagon/rag"
    "github.com/hexagon-codes/hexagon/rag/embedder"
)

func main() {
    ctx := context.Background()

    // Provider 同时提供 LLM 与 Embedding 能力
    provider := openai.New(os.Getenv("OPENAI_API_KEY"))
    model := "text-embedding-3-small"
    dimension := openai.EmbeddingDimension(model)

    // 存储维度必须与 Embedding 模型一致
    store := vector.NewMemoryStore(dimension)
    embeddingEngine := embedder.NewOpenAIEmbedder(
        provider,
        embedder.WithModel(model),
        embedder.WithDimension(dimension),
    )

    // 创建 RAG 引擎
    engine := rag.NewEngine(
        rag.WithStore(store),
        rag.WithEngineEmbedder(embeddingEngine),
    )

    // 索引文档
    docs := []rag.Document{
        {ID: "1", Content: "Go 是一种静态类型、编译型语言，由 Google 开发。"},
        {ID: "2", Content: "Go 支持并发编程，通过 goroutine 和 channel 实现。"},
        {ID: "3", Content: "Go 的标准库非常丰富，包括 HTTP、JSON、加密等。"},
    }
    if err := engine.Index(ctx, docs); err != nil {
        panic(err)
    }

    // 检索相关文档
    results, err := engine.Retrieve(ctx, "Go 的并发特性",
        rag.WithTopK(2),
        rag.WithMinScore(0.5),
    )
    if err != nil {
        panic(err)
    }

    for _, doc := range results {
        fmt.Printf("[%.2f] %s\n", doc.Score, doc.Content)
    }
}
```

### 使用 Qdrant 向量数据库

对于生产环境，推荐使用 Qdrant：

```go
package main

import (
    "context"
    "fmt"

    "github.com/hexagon-codes/ai-core/store/vector"
    "github.com/hexagon-codes/ai-core/store/vector/qdrant"
)

func migrateKnownDocuments(ctx context.Context, legacy, current *qdrant.Store, ids []string) error {
    for _, id := range ids {
        doc, err := legacy.Get(ctx, id)
        if err != nil {
            return fmt.Errorf("read legacy document %q: %w", id, err)
        }
        if doc == nil {
            continue
        }
        if err := current.Add(ctx, []vector.Document{*doc}); err != nil {
            return fmt.Errorf("write UUIDv8 document %q: %w", id, err)
        }
    }
    return nil
}

func main() {
    ctx := context.Background()

    // 旧策略仅用于从旧集合读取迁移数据，禁止继续写入。
    legacyStore, err := qdrant.New(qdrant.Config{
        Host:            "localhost",
        Port:            6333,
        Collection:      "my-docs-legacy",
        Dimension:       1536,
        PointIDStrategy: qdrant.PointIDLegacyHash31,
    })
    if err != nil {
        panic(err)
    }
    defer legacyStore.Close()

    // 新集合使用 UUIDv8；也可以省略 PointIDStrategy，零值默认即 UUIDv8。
    currentStore, err := qdrant.New(qdrant.Config{
        Host:             "localhost",
        Port:             6333,
        Collection:       "my-docs-v2",
        Dimension:        1536,
        CreateCollection: true,
        PointIDStrategy:  qdrant.PointIDUUIDv8,
    })
    if err != nil {
        panic(err)
    }
    defer currentStore.Close()

    // 生产迁移应从你的权威 ID 清单分批读取，并记录进度后再切换集合。
    if err := migrateKnownDocuments(ctx, legacyStore, currentStore, []string{"doc-1", "doc-2"}); err != nil {
        panic(err)
    }
}
```

ai-core v0.2.7 起，新集合默认使用 SHA-256 派生 UUIDv8 作为 point ID。旧集合需要迁移到名称不同的新集合；`PointIDLegacyHash31` 只能用于迁移窗口读取旧映射，不要通过旧策略新增数据，也不要在同一集合中混用两种 ID 策略。

---

## 图编排

图编排允许构建复杂的多步骤工作流：

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon/orchestration/graph"
)

// 定义状态
type MyState struct {
    Input   string
    Step1   string
    Step2   string
    Final   string
}

func (s MyState) Clone() graph.State {
    return s
}

func main() {
    ctx := context.Background()

    // 构建图
    g, _ := graph.NewGraph[MyState]("example-graph").
        AddNode("analyze", func(ctx context.Context, s MyState) (MyState, error) {
            s.Step1 = "Analyzed: " + s.Input
            return s, nil
        }).
        AddNode("process", func(ctx context.Context, s MyState) (MyState, error) {
            s.Step2 = "Processed: " + s.Step1
            return s, nil
        }).
        AddNode("summarize", func(ctx context.Context, s MyState) (MyState, error) {
            s.Final = "Summary: " + s.Step2
            return s, nil
        }).
        AddEdge(graph.START, "analyze").
        AddEdge("analyze", "process").
        AddEdge("process", "summarize").
        AddEdge("summarize", graph.END).
        Build()

    // 执行
    result, _ := g.Run(ctx, MyState{Input: "Hello World"})
    fmt.Println(result.Final)
}
```

### 条件分支

```go
package main

import (
    "context"
    "fmt"

    "github.com/hexagon-codes/hexagon/orchestration/graph"
)

type ConditionalState struct {
    ShouldUsePathA bool
    Result         string
}

func (s ConditionalState) Clone() graph.State { return s }

func main() {
    g, err := graph.NewGraph[ConditionalState]("conditional-graph").
        AddNode("check", func(_ context.Context, s ConditionalState) (ConditionalState, error) {
            return s, nil
        }).
        AddNode("path_a", func(_ context.Context, s ConditionalState) (ConditionalState, error) {
            s.Result = "A"
            return s, nil
        }).
        AddNode("path_b", func(_ context.Context, s ConditionalState) (ConditionalState, error) {
            s.Result = "B"
            return s, nil
        }).
        AddEdge(graph.START, "check").
        AddConditionalEdge("check", func(s ConditionalState) string {
            if s.ShouldUsePathA {
                return "a"
            }
            return "b"
        }, map[string]string{
            "a": "path_a",
            "b": "path_b",
        }).
        AddEdge("path_a", graph.END).
        AddEdge("path_b", graph.END).
        Build()
    if err != nil {
        panic(err)
    }

    result, err := g.Run(context.Background(), ConditionalState{ShouldUsePathA: true})
    if err != nil {
        panic(err)
    }
    fmt.Println(result.Result)
}
```

---

## 多 Agent 协作

### 团队模式

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/hexagon-codes/ai-core/llm/openai"
    "github.com/hexagon-codes/hexagon/agent"
)

func main() {
    ctx := context.Background()
    provider := openai.New(os.Getenv("OPENAI_API_KEY"))

    // 创建 Agent
    researcher := agent.NewReAct(
        agent.WithLLM(provider),
        agent.WithName("researcher"),
        agent.WithSystemPrompt("你是一个研究员，负责收集信息"),
    )
    writer := agent.NewReAct(
        agent.WithLLM(provider),
        agent.WithName("writer"),
        agent.WithSystemPrompt("你是一个作家，负责撰写内容"),
    )

    // 创建团队（顺序执行）
    team := agent.NewTeam("content-team",
        agent.WithAgents(researcher, writer),
        agent.WithMode(agent.TeamModeSequential),
    )

    // 执行
    output, err := team.Run(ctx, agent.Input{
        Query: "写一篇关于 Go 语言的介绍",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(output.Content)
}
```

### Agent 交接 (Swarm 模式)

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/hexagon-codes/ai-core/llm/openai"
    "github.com/hexagon-codes/hexagon/agent"
)

func main() {
    ctx := context.Background()
    provider := openai.New(os.Getenv("OPENAI_API_KEY"))

    // 先创建目标 Agent，避免局部变量的前向引用。
    supportAgent := agent.NewReAct(
        agent.WithLLM(provider),
        agent.WithName("support"),
        agent.WithSystemPrompt("你是技术支持"),
    )
    salesAgent := agent.NewReAct(
        agent.WithLLM(provider),
        agent.WithName("sales"),
        agent.WithSystemPrompt("你是销售客服"),
        agent.WithTools(agent.TransferTo(supportAgent)),
    )

    runner := agent.NewSwarmRunner(salesAgent)
    runner.MaxHandoffs = 5

    output, err := runner.Run(ctx, agent.Input{
        Query: "我想了解产品价格，还有一些技术问题",
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(output.Content)
}
```

---

## 安全防护

### Prompt 注入检测

```go
package main

import (
    "context"
    "fmt"

    "github.com/hexagon-codes/hexagon/security/guard"
)

func checkPrompt(ctx context.Context, userInput string) error {
    promptGuard := guard.NewPromptInjectionGuard()
    result, err := promptGuard.Check(ctx, userInput)
    if err != nil {
        return err
    }

    if !result.Passed {
        return fmt.Errorf("prompt rejected: %s", result.Reason)
    }
    return nil
}
```

### 成本控制

```go
package main

import (
    "context"

    "github.com/hexagon-codes/ai-core/llm"
    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/hexagon/runtime/middleware"
    "github.com/hexagon-codes/hexagon/security/cost"
)

func runWithBudget(ctx context.Context, provider llm.Provider, query string, estimatedTokens int64) (agent.Output, error) {
    controller, err := cost.NewController(
        cost.WithBudget(10.0),
        cost.WithMaxTokensTotal(100_000),
        cost.WithRequestsPerMinute(60),
    )
    if err != nil {
        return agent.Output{}, err
    }

    budgetedAgent := agent.NewReAct(
        agent.WithLLM(provider),
        agent.WithMiddleware(middleware.NewBudgetControl(middleware.BudgetControlConfig{
            Limits: middleware.BudgetLimits{
                MaxTokens:  100_000,
                MaxCostUSD: 10.0,
            },
            Cost:   controller.BudgetCostFunc(),
            Record: controller.RecordUsageFunc(),
        })),
    )

    // 每次请求进入 Agent 前执行累计 Token 与请求频率检查。
    if err := controller.CheckRequest(ctx, estimatedTokens); err != nil {
        return agent.Output{}, err
    }
    return budgetedAgent.Run(ctx, agent.Input{Query: query})
}
```

---

## 可观测性

### 追踪

```go
package main

import (
    "context"

    "github.com/hexagon-codes/hexagon/observe/tracer"
)

func tracedOperation(ctx context.Context) {
    traceStore := tracer.NewMemoryTracer()
    ctx = tracer.ContextWithTracer(ctx, traceStore)

    ctx, span := tracer.StartSpan(ctx, "my_operation")
    defer span.End()

    span.SetAttribute("user_id", "123")
    _ = ctx
}
```

### 指标

```go
package main

import "github.com/hexagon-codes/hexagon/observe/metrics"

func main() {
    collector := metrics.NewMemoryMetrics()
    collector.Counter("agent_calls", "agent", "react").Inc()
    collector.Histogram("latency_ms").Observe(123.5)
}
```

---

## Dev UI

内置开发调试界面，实时查看 Agent 执行过程。

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/hexagon-codes/hexagon/observe/devui"
)

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    // 默认只监听本机回环地址，不对局域网或公网暴露调试接口。
    ui := devui.New(
        devui.WithAddr("127.0.0.1:8080"),
        devui.WithMaxEvents(1000),
    )

    errCh := make(chan error, 1)
    go func() {
        errCh <- ui.Start()
    }()

    select {
    case err := <-errCh:
        if err != nil {
            log.Fatal(err)
        }
        return
    case <-ctx.Done():
    }

    shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := ui.Stop(shutdownCtx); err != nil {
        log.Fatal(err)
    }
    if err := <-errCh; err != nil {
        log.Fatal(err)
    }
}

// 访问 http://127.0.0.1:8080
```

**运行示例：**

```bash
# 运行上面的 Dev UI 程序
go run main.go

# 启动前端 (hexagon-ui)
cd ../hexagon-ui
npm install
npm run dev
# 访问 http://localhost:5173
```

**功能特性：**
- 实时事件流 (SSE 推送)
- 指标仪表板
- 事件详情查看
- LLM 流式输出展示

---

## 本地基础设施与部署模板

本仓库的 `deploy/` 目录只提供本地基础设施编排和 Helm 模板，不会启动 Hexagon 应用或 Dev UI。

### Docker Compose 启动本地基础设施

```bash
cd deploy
cp .env.example .env
make up
# 启动 Qdrant、Redis/Redis Insight 和 PostgreSQL
```

### 渲染 Kubernetes / Helm 模板

```bash
cd deploy
make helm-template
```

`helm-template` 仅在本地渲染清单，不会修改集群。评审生成结果后，再按你的发布流程部署。详见 [部署指南](../deploy/README.md)。

---

## 下一步

- 阅读 [API 参考文档](API.md) 了解完整 API
- 阅读 [架构设计文档](DESIGN.md) 深入理解框架设计
- 阅读 [框架对比](comparison.md) 了解与其他框架的差异
- 阅读 [部署指南](../deploy/README.md) 了解部署配置
- 查看 [示例代码](../examples/) 获取更多用例
- 访问 [GitHub](https://github.com/hexagon-codes/hexagon) 参与贡献

## 常见问题

### Q: 如何切换 LLM Provider？

```go
package main

import (
    "os"

    "github.com/hexagon-codes/ai-core/llm/deepseek"
    "github.com/hexagon-codes/hexagon/agent"
)

func main() {
    provider := deepseek.New(os.Getenv("DEEPSEEK_API_KEY"))
    myAgent := agent.NewReAct(agent.WithLLM(provider))
    _ = myAgent
}
```

### Q: 如何自定义 Memory？

```go
package main

import (
    "github.com/hexagon-codes/ai-core/llm"
    "github.com/hexagon-codes/ai-core/memory"
    "github.com/hexagon-codes/hexagon/agent"
)

func newAgentWithMemory(provider llm.Provider) *agent.ReActAgent {
    // 使用更大的 buffer。
    conversationMemory := memory.NewBuffer(1000)
    return agent.NewReAct(
        agent.WithLLM(provider),
        agent.WithMemory(conversationMemory),
    )
}
```

### Q: 如何调试 Agent？

`WithVerbose` 是 `agent` 包的 Agent 选项，需要直接构造 Agent 时使用：

```go
package main

import (
    "github.com/hexagon-codes/ai-core/llm"
    "github.com/hexagon-codes/hexagon/agent"
)

func newVerboseAgent(provider llm.Provider) *agent.ReActAgent {
    return agent.NewReAct(
        agent.WithLLM(provider),
        agent.WithVerbose(true), // 开启详细日志
    )
}
```
