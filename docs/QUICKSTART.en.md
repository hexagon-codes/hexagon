<div align="right">Language: <a href="QUICKSTART.md">中文</a> | English</div>

# Hexagon Quick Start Guide

This guide helps you get started with the Hexagon AI Agent framework in 30 minutes.

## Project Overview

**Hexagon** is named after the Chinese internet term "hexagonal warrior" (六边形战士), symbolizing balanced strength with no weak points. The framework focuses on six core dimensions — **ease of use, performance, extensibility, task orchestration, observability, and security** — providing Go developers with a production-ready AI Agent development foundation.

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

- Go 1.25.7 or higher
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
    "github.com/hexagon-codes/hexagon"
)

func main() {
    ctx := context.Background()

    // Create an in-memory vector store
    store := hexagon.NewMemoryVectorStore()

    // Create an embedder
    embedder := hexagon.NewOpenAIEmbedder()

    // Create the RAG engine
    engine := hexagon.NewRAGEngine(
        hexagon.WithRAGStore(store),
        hexagon.WithRAGEmbedder(embedder),
    )

    // Index documents
    docs := []hexagon.Document{
        {ID: "1", Content: "Go is a statically typed, compiled language developed by Google."},
        {ID: "2", Content: "Go supports concurrent programming through goroutines and channels."},
        {ID: "3", Content: "Go's standard library is extensive, covering HTTP, JSON, cryptography, and more."},
    }
    engine.Index(ctx, docs)

    // Retrieve relevant documents
    results, _ := engine.Retrieve(ctx, "Go's concurrency features",
        hexagon.WithTopK(2),
        hexagon.WithMinScore(0.5),
    )

    for _, doc := range results {
        fmt.Printf("[%.2f] %s\n", doc.Score, doc.Content)
    }
}
```

### Using Qdrant Vector Database

For production environments, Qdrant is recommended:

```go
// Create a Qdrant store
store, _ := hexagon.NewQdrantStore(hexagon.QdrantConfig{
    Host:             "localhost",
    Port:             6333,
    Collection:       "my-docs",
    Dimension:        1536,
    CreateCollection: true,
})
```

---

## Graph Orchestration

Graph orchestration allows you to build complex multi-step workflows:

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
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
    g, _ := hexagon.NewGraph[MyState]("example-graph").
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
        AddEdge(hexagon.START, "analyze").
        AddEdge("analyze", "process").
        AddEdge("process", "summarize").
        AddEdge("summarize", hexagon.END).
        Build()

    // Execute
    result, _ := g.Run(ctx, MyState{Input: "Hello World"})
    fmt.Println(result.Final)
}
```

### Conditional Branching

```go
g, _ := hexagon.NewGraph[MyState]("conditional-graph").
    AddNode("check", checkHandler).
    AddNode("path_a", pathAHandler).
    AddNode("path_b", pathBHandler).
    AddEdge(hexagon.START, "check").
    AddConditionalEdge("check", func(s MyState) string {
        if s.ShouldUsePathA {
            return "a"
        }
        return "b"
    }, map[string]string{
        "a": "path_a",
        "b": "path_b",
    }).
    AddEdge("path_a", hexagon.END).
    AddEdge("path_b", hexagon.END).
    Build()
```

---

## Multi-Agent Collaboration

### Team Mode

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

func main() {
    ctx := context.Background()

    // Create agents
    researcher := hexagon.QuickStart(
        hexagon.WithSystemPrompt("You are a researcher responsible for gathering information"),
    )
    writer := hexagon.QuickStart(
        hexagon.WithSystemPrompt("You are a writer responsible for creating content"),
    )

    // Create a team (sequential execution)
    team := hexagon.NewTeam("content-team",
        hexagon.WithAgents(researcher, writer),
        hexagon.WithMode(hexagon.TeamModeSequential),
    )

    // Execute
    output, _ := team.Run(ctx, hexagon.Input{
        Query: "Write an introduction to the Go programming language",
    })

    fmt.Println(output.Content)
}
```

### Agent Handoff (Swarm Mode)

```go
// Create agents
salesAgent := hexagon.QuickStart(
    hexagon.WithSystemPrompt("You are a sales representative"),
    hexagon.WithTools(
        hexagon.TransferTo(supportAgent), // handoff tool
    ),
)

supportAgent := hexagon.QuickStart(
    hexagon.WithSystemPrompt("You are technical support"),
    hexagon.WithTools(
        hexagon.TransferTo(salesAgent),
    ),
)

// Create Swarm runner
runner := agent.NewSwarmRunner(salesAgent)
runner.MaxHandoffs = 5

// Run
output, _ := runner.Run(ctx, hexagon.Input{
    Query: "I'd like to know about pricing, and I also have some technical questions",
})
```

---

## Security

### Prompt Injection Detection

```go
guard := hexagon.NewPromptInjectionGuard()
result, _ := guard.Check(ctx, userInput)

if !result.Passed {
    fmt.Printf("Potential injection attack detected: %s\n", result.Reason)
}
```

### Cost Control

```go
controller := hexagon.NewCostController(
    hexagon.WithBudget(10.0),           // $10 budget
    hexagon.WithMaxTokensTotal(100000), // total token limit
)
```

---

## Observability

### Tracing

```go
tracer := hexagon.NewTracer()
ctx := hexagon.ContextWithTracer(ctx, tracer)

span := hexagon.StartSpan(ctx, "my_operation")
defer span.End()

span.SetAttribute("user_id", "123")
```

### Metrics

```go
metrics := hexagon.NewMetrics()
metrics.Counter("agent_calls", "agent", "react").Inc()
metrics.Histogram("latency_ms").Observe(123.5)
```

---

## Dev UI

A built-in development and debugging interface for real-time inspection of agent execution.

```go
import "github.com/hexagon-codes/hexagon/observe/devui"

// Create DevUI
ui := devui.New(
    devui.WithAddr(":8080"),
    devui.WithMaxEvents(1000),
)

// Start the server
go ui.Start()

// Visit http://localhost:8080
```

**Running the example:**

```bash
# Start the backend
go run examples/devui/main.go

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

## Deployment

Hexagon supports three deployment options:

### Docker Quick Start

```bash
cd deploy
cp .env.example .env
# Edit .env and fill in your LLM API key
make up
# Main app: http://localhost:8000  Dev UI: http://localhost:8080
```

### Kubernetes / Helm

```bash
cd deploy
make helm-install
```

See the [Deployment Guide](../deploy/README.md) for details.

---

## Next Steps

- Read the [API Reference](API.en.md) for the complete API
- Read the [Architecture Design](DESIGN.en.md) to understand the framework in depth
- Read the [Framework Comparison](comparison.en.md) to see how Hexagon differs from alternatives
- Read the [Deployment Guide](../deploy/README.md) for deployment configuration
- Browse the [Example Code](../examples/) for more use cases
- Visit [GitHub](https://github.com/hexagon-codes/hexagon) to contribute

## FAQ

### Q: How do I switch LLM providers?

```go
import "github.com/hexagon-codes/ai-core/llm/deepseek"

provider := deepseek.New(os.Getenv("DEEPSEEK_API_KEY"))
agent := hexagon.QuickStart(
    hexagon.WithProvider(provider),
)
```

### Q: How do I customize Memory?

```go
// Use a larger buffer
memory := hexagon.NewBufferMemory(1000)
agent := hexagon.QuickStart(
    hexagon.WithMemory(memory),
)
```

### Q: How do I debug an Agent?

`WithVerbose` is an Agent option in the `agent` package; use it when constructing an Agent directly:

```go
import "github.com/hexagon-codes/hexagon/agent"

myAgent := agent.NewReAct(
    agent.WithLLM(provider),
    agent.WithVerbose(true), // enable verbose logging
)
```
