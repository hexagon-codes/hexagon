<div align="right">Language: <a href="agent-guide.md">中文</a> | English</div>

# Agent Development Guide

This guide covers how to develop various types of AI Agents with Hexagon.

## Agent Types

### BaseAgent

The foundational Agent that provides the simplest LLM invocation capability:

```go
agent := agent.NewBaseAgent(
    agent.WithName("assistant"),
    agent.WithLLM(llm),
    agent.WithSystemPrompt("You are a professional assistant"),
)
```

### ReActAgent

Implements the ReAct (Reasoning + Acting) pattern, supporting multi-step reasoning and tool calls:

```go
agent := agent.NewReAct(
    agent.WithName("researcher"),
    agent.WithLLM(llm),
    agent.WithTools(searchTool, calculatorTool),
    agent.WithMaxIterations(10),
)
```

### RAG (Retrieval-Augmented)

There is no dedicated RAG Agent constructor. Instead, use a `rag.Engine` to retrieve from a knowledge base, fold the context into the query, and let an ordinary Agent answer:

```go
import (
    "github.com/hexagon-codes/hexagon/rag"
    "github.com/hexagon-codes/hexagon/rag/embedder"
)

// Create the RAG engine
engine := rag.NewEngine(
    rag.WithStore(store),
    rag.WithEngineEmbedder(embedder.NewOpenAIEmbedder(provider)),
    rag.WithEngineTopK(5),
)
_ = engine.Index(ctx, docs)

// Retrieve context and let the Agent answer
context, _ := engine.Query(ctx, "What is Hexagon?")
output, _ := myAgent.Run(ctx, agent.Input{
    Query:   "What is Hexagon?",
    Context: map[string]any{"knowledge": context},
})
```

## Configuration Options

### Basic Configuration

```go
agent.WithName("my-agent")           // set name
agent.WithLLM(llm)                   // set LLM
agent.WithSystemPrompt("...")        // set system prompt
agent.WithMaxIterations(10)          // set max reasoning iterations
agent.WithVerbose(true)              // enable verbose logging
```

> Note: sampling parameters such as temperature and max tokens are configured on the LLM Provider when it is created (e.g. options on `openai.New(...)`), not as Agent options.

### Tool Configuration

```go
agent.WithTools(tool1, tool2)        // add tools
```

### Memory Configuration

```go
agent.WithMemory(mem)                // add memory (capacity is set when building the memory)
```

## Middleware System

### Built-in Middleware

```go
// Panic recovery
agent.RecoverMiddleware()

// Logging
agent.LoggingMiddleware(logger)

// Metrics collection
agent.MetricsMiddleware(collector)

// Timeout control
agent.TimeoutMiddleware(30*time.Second)

// Retry mechanism
agent.RetryMiddleware(3, time.Second)

// Tracing
agent.TracingMiddleware("my-service")

// Rate limiting
agent.RateLimitMiddleware(limiter)
```

### Composing Middleware

```go
chain := agent.NewMiddlewareChain(
    agent.RecoverMiddleware(),
    agent.LoggingMiddleware(nil),
    agent.TimeoutMiddleware(30*time.Second),
)

// Wrap the Agent
handler := chain.WrapAgent(myAgent)

// Run
output, err := handler(ctx, input)
```

### Preset Combinations

```go
// Default middleware (Recover + Logging + Timeout)
middlewares := agent.DefaultMiddlewares()

// Production middleware
middlewares := agent.ProductionMiddlewares("my-service", metricsCollector)
```

## Custom Middleware

```go
func MyMiddleware() agent.AgentMiddleware {
    return func(next agent.AgentHandler) agent.AgentHandler {
        return func(ctx context.Context, input agent.Input) (agent.Output, error) {
            // Pre-processing
            log.Println("Starting execution")

            // Call the next handler
            output, err := next(ctx, input)

            // Post-processing
            log.Println("Execution complete")

            return output, err
        }
    }
}
```

## State Management

### Four-Layer State

```go
// Turn state: single conversation turn
state.Turn().Set("key", value)

// Session state: conversation level
state.Session().Set("user_id", "123")

// Agent state: persistent Agent state
state.Agent().Set("config", config)

// Global state: cross-Agent shared state
state.Global().Set("shared_data", data)
```

## Role System

Define the role characteristics of an Agent:

```go
agent := agent.NewBaseAgent(
    agent.WithRole(agent.Role{
        Name:      "Researcher",
        Goal:      "Collect and analyze information",
        Backstory: "You are an experienced researcher skilled at extracting key insights from complex information",
    }),
)
```

## Team Collaboration

Create a multi-Agent team:

```go
// Create team members
researcher := agent.NewReAct(...)
writer := agent.NewBaseAgent(...)
reviewer := agent.NewBaseAgent(...)

// Create the team (the first argument is the team name)
team := agent.NewTeam("content-team",
    agent.WithMode(agent.TeamModeSequential),
    agent.WithAgents(researcher, writer, reviewer),
)

// Run the team task
output, err := team.Run(ctx, input)
```

### Team Modes

- `agent.TeamModeSequential`: sequential execution
- `agent.TeamModeHierarchical`: hierarchical execution
- `agent.TeamModeCollaborative`: collaborative execution
- `agent.TeamModeRoundRobin`: round-robin execution

## Streaming Output

```go
import (
    "errors"
    "io"
)

// Get a streaming response: Stream returns *stream.StreamReader[agent.Output]
reader, err := myAgent.Stream(ctx, input)
if err != nil {
    return err
}
defer reader.Close()

// Loop over Recv to process streaming data; io.EOF signals the end
for {
    chunk, err := reader.Recv()
    if errors.Is(err, io.EOF) {
        break
    }
    if err != nil {
        return err
    }
    fmt.Print(chunk.Content)
}
```

## Error Handling

```go
output, err := myAgent.Run(ctx, input)
if err != nil {
    switch {
    case errors.Is(err, context.DeadlineExceeded):
        // handle timeout
    default:
        // Errors are wrapped layer by layer with fmt.Errorf("...: %w", err);
        // use errors.Is / errors.As to unwrap and inspect the root cause
    }
}
```

## Best Practices

1. **Use middleware**: always add `RecoverMiddleware` to guard against panics
2. **Set timeouts**: avoid indefinite blocking
3. **Add logging**: facilitates debugging and monitoring
4. **Limit iterations**: set a reasonable `MaxIterations` for `ReActAgent`
5. **Clean up resources**: use `defer` to ensure resources are released
