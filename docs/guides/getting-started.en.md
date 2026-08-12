<div align="right">Language: <a href="getting-started.md">中文</a> | English</div>

# Getting Started Guide

This guide will help you get up and running with the Hexagon AI Agent framework quickly.

## Installation

```bash
go get github.com/hexagon-codes/hexagon
```

## Minimal Example

Create an AI Agent with a small amount of code:

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
    // Create an LLM Provider
    provider := openai.New("your-api-key")

    // Create an Agent
    myAgent := agent.NewBaseAgent(
        agent.WithLLM(provider),
        agent.WithSystemPrompt("You are a helpful assistant"),
    )

    // Run
    output, err := myAgent.Invoke(context.Background(), agent.Input{
        Query: "Hello, please introduce yourself",
    })
    if err != nil {
        log.Fatalf("agent execution failed: %v", err)
    }

    fmt.Println(output.Content)
}
```

## Core Concepts

### Agent

An Agent is the central concept in Hexagon — it represents an executable AI entity. Each Agent can:

- Receive user input
- Call an LLM for reasoning
- Use tools to perform tasks
- Return processed results

### Runnable and Component

`core.Runnable[I, O]` is Hexagon's unified abstraction for executable components, and Agent implements this interface. `core.Component[I, O]` is a compatibility interface retained for older code and directly embeds `Runnable`. New code should prefer `Runnable` and `Invoke`:

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

Agent's `Run` method remains only for backward compatibility; new code should use `Invoke`.

### Middleware

Extend Agent functionality using middleware:

```go
chain := agent.NewMiddlewareChain(
    agent.RecoverMiddleware(),    // panic recovery
    agent.LoggingMiddleware(nil), // logging
    agent.TimeoutMiddleware(30*time.Second), // timeout control
)

handler := chain.WrapAgent(myAgent)
output, err := handler(ctx, input)
```

## Adding Tools

Enable the Agent to perform concrete tasks:

```go
import "github.com/hexagon-codes/ai-core/tool"

// Define a search tool
searchTool := tool.NewFunc("web_search",
    "Search the web for information",
    func(ctx context.Context, input struct {
        Query string `json:"query" description:"search keywords"`
    }) (string, error) {
        // implement search logic
        return "search results...", nil
    },
)

// Create an Agent with tools
myAgent := agent.NewReAct(
    agent.WithLLM(llm),
    agent.WithTools(searchTool),
)
```

## Using Memory

Add conversational memory to an Agent:

```go
import "github.com/hexagon-codes/ai-core/memory"

// 创建记忆（保留最近 10 条消息）
mem := memory.NewBuffer(10)

// Create an Agent with memory
myAgent := agent.NewBaseAgent(
    agent.WithLLM(llm),
    agent.WithMemory(mem),
)
```

## RAG Retrieval-Augmented Generation

Integrate a knowledge base to enhance Agent capabilities:

```go
import (
    "github.com/hexagon-codes/hexagon/rag"
    "github.com/hexagon-codes/hexagon/rag/embedder"
    "github.com/hexagon-codes/ai-core/store/vector/qdrant"
)

// Create a vector store (the Qdrant implementation lives in ai-core)
store, _ := qdrant.NewWithOptions(
    qdrant.WithCollection("docs"),
    qdrant.WithDimension(1536),
)

// Create an embedder
emb := embedder.NewOpenAIEmbedder(provider)

// Create the RAG engine
engine := rag.NewEngine(
    rag.WithStore(store),
    rag.WithEngineEmbedder(emb),
    rag.WithEngineTopK(3),
)

// Index documents and query
_ = engine.Index(ctx, docs)
answer, _ := engine.Query(ctx, "What is Hexagon?")
```

Starting with ai-core v0.2.7, new Qdrant collections use SHA-256-derived UUIDv8 point IDs by default. To migrate an older collection, temporarily read it with `qdrant.WithPointIDStrategy(qdrant.PointIDLegacyHash31)`, then rebuild the data into a new collection that uses the default UUIDv8 strategy. Do not mix both strategies in one collection.

## Observability

Add metrics and tracing:

```go
import "github.com/hexagon-codes/hexagon/observe/metrics"

// Get the metrics collector
collector := metrics.GetHexagonMetrics()

// Record an Agent run
collector.RecordAgentRun(ctx, "my-agent", duration, err)

// Get a statistics summary
summary := collector.GetSummary()
fmt.Printf("Total runs: %d\n", summary.TotalAgentRuns)
```

## Next Steps

- [Agent Development Guide](agent-guide.en.md) - Deep dive into Agent development
- [RAG System Usage](rag-guide.en.md) - Build knowledge-augmented applications
- [Plugin Development Guide](plugin-guide.en.md) - Extend the framework
