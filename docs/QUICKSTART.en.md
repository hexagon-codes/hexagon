<div align="right">Language: <a href="QUICKSTART.md">中文</a> | English</div>

# Hexagon Quick Start Guide

This guide helps you get started with the Hexagon AI Agent framework in 30 minutes.

## Project Overview

**Hexagon** is named after the Chinese internet term "hexagonal warrior" (六边形战士), referring to balanced coverage across multiple capabilities. The framework focuses on six core dimensions — **ease of use, performance, extensibility, task orchestration, observability, and security** — and provides Go developers with an AI Agent framework.

### Ecosystem

Hexagon is a complete AI Agent development ecosystem:

| Repository | Description |
|-----|------|
| **hexagon** | AI Agent framework core (orchestration, RAG, Graph, Hooks) |
| **ai-core** | AI capability library (LLM/Tool/Memory/Schema) |
| **toolkit** | Go general-purpose toolkit (lang/crypto/net/cache/util) |
| **hexagon-ui** | Dev UI frontend (Vue 3 + TypeScript) |

## Table of Contents

- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [3-Line Quick Start](#3-line-quick-start)
- [Agent with Tools](#agent-with-tools)
- [RAG Retrieval Augmentation](#rag-retrieval-augmentation)
- [Graph Orchestration](#graph-orchestration)
- [Multi-Agent Collaboration](#multi-agent-collaboration)
- [Dev UI](#dev-ui)
- [Next Steps](#next-steps)

---

## Prerequisites

### System Requirements

- Go 1.25.12 or higher
- Network access (to reach LLM APIs)

### Environment Variables

Hexagon supports multiple LLM providers. Configure the corresponding API key:

```bash
# OpenAI (default)
export OPENAI_API_KEY=your-api-key

# DeepSeek
export DEEPSEEK_API_KEY=your-api-key
```

---

## Installation

```bash
go get github.com/hexagon-codes/hexagon
```

Verify the installation:

```bash
go list -m github.com/hexagon-codes/hexagon
```

---

## 3-Line Quick Start

The simplest way to get started:

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

Run it:

```bash
export OPENAI_API_KEY=your-api-key
go run main.go
```

---

## Agent with Tools

Agents can use tools to accomplish tasks:

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

// Define the calculator tool input
type CalculatorInput struct {
    A  float64 `json:"a" desc:"first number" required:"true"`
    B  float64 `json:"b" desc:"second number" required:"true"`
    Op string  `json:"op" desc:"operator" required:"true" enum:"add,sub,mul,div"`
}

func main() {
    ctx := context.Background()

    // Create the calculator tool
    calculator := hexagon.NewTool("calculator", "Perform mathematical calculations",
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

    // Create an agent with tools
    agent := hexagon.QuickStart(
        hexagon.WithTools(calculator),
        hexagon.WithSystemPrompt("You are a math assistant"),
    )

    // Run a query
    output, _ := agent.Run(ctx, hexagon.Input{
        Query: "Please calculate 123 multiplied by 456",
    })

    fmt.Println(output.Content)
}
```

### Tool Definition Reference

- `name`: Tool name, used by the LLM to identify and invoke the tool
- `desc`: Tool description, helps the LLM understand when to use it
- Input struct tags:
  - `json`: Field name
  - `desc`: Field description
  - `required`: Whether the field is required
  - `enum`: List of allowed values

---

## RAG Retrieval Augmentation

RAG (Retrieval-Augmented Generation) allows an Agent to answer questions based on an external knowledge base:

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

    // Provider 同时提供 LLM 与嵌入能力。
    provider := openai.New(os.Getenv("OPENAI_API_KEY"))
    model := "text-embedding-3-small"
    dimension := openai.EmbeddingDimension(model)

    // 存储维度必须与嵌入模型一致。
    store := vector.NewMemoryStore(dimension)
    embeddingEngine := embedder.NewOpenAIEmbedder(
        provider,
        embedder.WithModel(model),
        embedder.WithDimension(dimension),
    )

    // Create the RAG engine
    engine := rag.NewEngine(
        rag.WithStore(store),
        rag.WithEngineEmbedder(embeddingEngine),
    )

    // Index documents
    docs := []rag.Document{
        {ID: "1", Content: "Go is a statically typed, compiled language developed by Google."},
        {ID: "2", Content: "Go supports concurrent programming through goroutines and channels."},
        {ID: "3", Content: "Go's standard library is extensive, covering HTTP, JSON, cryptography, and more."},
    }
    if err := engine.Index(ctx, docs); err != nil {
        panic(err)
    }

    // Retrieve relevant documents
    results, err := engine.Retrieve(ctx, "Go's concurrency features",
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

### Using Qdrant Vector Database

For production environments, Qdrant is recommended:

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

    // 旧策略仅用于读取迁移数据，禁止继续通过它写入。
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

    // 新集合使用 UUIDv8；省略 PointIDStrategy 时默认值相同。
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

    // 生产迁移应从权威清单分批处理 ID，并在切流前记录检查点。
    if err := migrateKnownDocuments(ctx, legacyStore, currentStore, []string{"doc-1", "doc-2"}); err != nil {
        panic(err)
    }
}
```

Starting with ai-core v0.2.7, new collections use SHA-256-derived UUIDv8 point IDs by default. Migrate an old collection into a differently named new collection. Use `PointIDLegacyHash31` only to read the old mapping during the migration window; do not add data through the legacy strategy or mix both ID strategies in one collection.

---

## Graph Orchestration

Graph orchestration allows you to build complex multi-step workflows:

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon/orchestration/graph"
)

// Define state
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

    // Build the graph
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

    // Execute
    result, _ := g.Run(ctx, MyState{Input: "Hello World"})
    fmt.Println(result.Final)
}
```

### Conditional Branching

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

## Multi-Agent Collaboration

### Team Mode

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

    // Create agents
    researcher := agent.NewReAct(
        agent.WithLLM(provider),
        agent.WithName("researcher"),
        agent.WithSystemPrompt("You are a researcher responsible for gathering information"),
    )
    writer := agent.NewReAct(
        agent.WithLLM(provider),
        agent.WithName("writer"),
        agent.WithSystemPrompt("You are a writer responsible for creating content"),
    )

    // Create a team (sequential execution)
    team := agent.NewTeam("content-team",
        agent.WithAgents(researcher, writer),
        agent.WithMode(agent.TeamModeSequential),
    )

    // Execute
    output, err := team.Run(ctx, agent.Input{
        Query: "Write an introduction to the Go programming language",
    })
    if err != nil {
        panic(err)
    }

    fmt.Println(output.Content)
}
```

### Agent Handoff (Swarm Mode)

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

    // 先创建目标 Agent，避免局部变量前向引用。
    supportAgent := agent.NewReAct(
        agent.WithLLM(provider),
        agent.WithName("support"),
        agent.WithSystemPrompt("You are technical support"),
    )
    salesAgent := agent.NewReAct(
        agent.WithLLM(provider),
        agent.WithName("sales"),
        agent.WithSystemPrompt("You are a sales representative"),
        agent.WithTools(agent.TransferTo(supportAgent)),
    )

    runner := agent.NewSwarmRunner(salesAgent)
    runner.MaxHandoffs = 5

    output, err := runner.Run(ctx, agent.Input{
        Query: "I'd like to know about pricing, and I also have some technical questions",
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(output.Content)
}
```

---

## Security

### Prompt Injection Detection

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

### Cost Control

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

    // 每次 Agent 请求前检查累计 token 与请求速率。
    if err := controller.CheckRequest(ctx, estimatedTokens); err != nil {
        return agent.Output{}, err
    }
    return budgetedAgent.Run(ctx, agent.Input{Query: query})
}
```

---

## Observability

### Tracing

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

### Metrics

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

A built-in development and debugging interface for real-time inspection of agent execution.

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

    // 仅绑定回环地址，避免将调试 API 暴露到局域网或互联网。
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

**Running the example:**

```bash
# Run the Dev UI program above
go run main.go

# Start the frontend (hexagon-ui)
cd ../hexagon-ui
npm install
npm run dev
# Visit http://localhost:5173
```

**Features:**
- Real-time event streaming (SSE push)
- Metrics dashboard
- Event detail viewer
- LLM streaming output display

---

## Local Infrastructure and Deployment Templates

The repository's `deploy/` directory provides local infrastructure orchestration and Helm templates only. It does not start a Hexagon application or Dev UI.

### Start Local Infrastructure with Docker Compose

```bash
cd deploy
cp .env.example .env
make up
# Starts Qdrant, Redis/Redis Insight, and PostgreSQL
```

### Render Kubernetes / Helm Templates

```bash
cd deploy
make helm-template
```

`helm-template` renders manifests locally and does not modify a cluster. Review the generated manifests before deploying them through your release process. See the [Deployment Guide](../deploy/README.en.md) for details.

---

## Next Steps

- Read the [API Reference](API.en.md) for the complete API
- Read the [Architecture Design](DESIGN.en.md) to understand the framework in depth
- Read the [Framework Comparison](comparison.en.md) to see how Hexagon differs from alternatives
- Read the [Deployment Guide](../deploy/README.en.md) for deployment configuration
- Browse the [Example Code](../examples/) for more use cases
- Visit [GitHub](https://github.com/hexagon-codes/hexagon) to contribute

## FAQ

### Q: How do I switch LLM providers?

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

### Q: How do I customize Memory?

```go
package main

import (
    "github.com/hexagon-codes/ai-core/llm"
    "github.com/hexagon-codes/ai-core/memory"
    "github.com/hexagon-codes/hexagon/agent"
)

func newAgentWithMemory(provider llm.Provider) *agent.ReActAgent {
    // 使用更大的缓冲区。
    conversationMemory := memory.NewBuffer(1000)
    return agent.NewReAct(
        agent.WithLLM(provider),
        agent.WithMemory(conversationMemory),
    )
}
```

### Q: How do I debug an Agent?

`WithVerbose` is an Agent option in the `agent` package; use it when constructing an Agent directly:

```go
package main

import (
    "github.com/hexagon-codes/ai-core/llm"
    "github.com/hexagon-codes/hexagon/agent"
)

func newVerboseAgent(provider llm.Provider) *agent.ReActAgent {
    return agent.NewReAct(
        agent.WithLLM(provider),
        agent.WithVerbose(true), // enable verbose logging
    )
}
```
