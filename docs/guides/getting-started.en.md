<div align="right">Language: <a href="getting-started.md">中文</a> | English</div>

# Getting Started Guide

This guide will help you get up and running with the Hexagon AI Agent framework quickly.

## Installation

```bash
go get github.com/hexagon-codes/hexagon
```

## Minimal Example

Create an AI Agent with just 3 lines of code:

```go
package main

import (
    "context"
    "fmt"

    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/ai-core/llm/openai"
)

func main() {
    // Create an LLM Provider
    llm, _ := openai.New(openai.WithAPIKey("your-api-key"))

    // Create an Agent
    myAgent := agent.NewBaseAgent(
        agent.WithLLM(llm),
        agent.WithSystemPrompt("You are a helpful assistant"),
    )

    // Run
    output, _ := myAgent.Run(context.Background(), agent.Input{
        Query: "Hello, please introduce yourself",
    })

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

### Component

All components (Agent, Tool, Chain, Graph) implement the unified `Component[I, O]` interface:

```go
type Component[I, O any] interface {
    Name() string
    Run(ctx context.Context, input I) (O, error)
    Stream(ctx context.Context, input I) (Stream[O], error)
    Batch(ctx context.Context, inputs []I) ([]O, error)
}
```

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

// Create memory
mem := memory.NewConversationMemory(10) // retain the last 10 conversation turns

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
- [RAG System Usage](rag-guide.md) - Build knowledge-augmented applications
- [Plugin Development Guide](plugin-guide.md) - Extend the framework
