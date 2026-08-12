<div align="right">Language: <a href="agent-guide.md">中文</a> | English</div>

# Agent Development Guide

This guide covers how to develop various types of AI Agents with Hexagon.

## Agent Types

### BaseAgent

The foundational Agent that provides the simplest LLM invocation capability:

```go
assistant := agent.NewBaseAgent(
    agent.WithName("assistant"),
    agent.WithLLM(provider),
    agent.WithSystemPrompt("You are a professional assistant"),
)
```

### ReActAgent

Implements the ReAct (Reasoning + Acting) pattern, supporting multi-step reasoning and tool calls:

```go
researcher := agent.NewReAct(
    agent.WithName("researcher"),
    agent.WithLLM(provider),
    agent.WithTools(searchTool, calculatorTool),
    agent.WithMaxIterations(10),
)
```

### RAG (Retrieval-Augmented)

There is no dedicated RAG Agent constructor. Instead, use a `rag.Engine` to retrieve from a knowledge base, fold the context into the query, and let an ordinary Agent answer:

```go
import (
    "fmt"

    "github.com/hexagon-codes/hexagon/rag"
    "github.com/hexagon-codes/hexagon/rag/embedder"
)

// Create the RAG engine
engine := rag.NewEngine(
    rag.WithStore(store),
    rag.WithEngineEmbedder(embedder.NewOpenAIEmbedder(provider)),
    rag.WithEngineTopK(5),
)
if err := engine.Index(ctx, docs); err != nil {
    return fmt.Errorf("index documents: %w", err)
}

// Retrieve context and let the Agent answer
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

`agent.Input.Context` carries metadata for orchestration and callers; the current `BaseAgent` and `ReActAgent` do not automatically add it to LLM messages. RAG context must therefore be folded into `Query` explicitly, as above, or handled by an application-owned prompt builder.

## Configuration Options

### Basic Configuration

```go
agent.WithID("agent-123")            // 设置稳定 ID（可选）
agent.WithName("my-agent")           // set name
agent.WithDescription("...")        // 设置描述
agent.WithLLM(provider)              // 设置 LLM Provider
agent.WithSystemPrompt("...")        // set system prompt
agent.WithMaxIterations(10)          // set max reasoning iterations
```

`WithMaxIterations` only affects Agents with an iterative loop, such as `ReActAgent`. `WithVerbose` remains a compatibility configuration field, but the current `BaseAgent`/`ReActAgent` do not emit logs from it; use request-level `LoggingMiddleware` or runtime middleware for logging.

### Provider Configuration

```go
provider := openai.New(
    os.Getenv("OPENAI_API_KEY"),
    openai.WithModel("gpt-4o"),
    openai.WithRequestTimeout(60*time.Second),
)
```

The first argument to `openai.New` is the API key. Its options configure Provider properties such as the default model, base URL, HTTP client, and timeouts. Temperature and max tokens belong to `llm.CompletionRequest`; they are not `openai.New` or Agent options. The current `BaseAgent`/`ReActAgent` do not expose per-request sampling parameters. Call the Provider directly when those parameters are required:

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

### Tool Configuration

`tool.NewFunc` builds a tool and its JSON Schema from an input struct. Tool calls are executed by `ReActAgent`; `BaseAgent.Invoke` only performs one ordinary LLM completion and does not execute registered tools.

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

Both tools and Agents expose their current schemas: `wordCountTool.Schema()` returns the tool input schema, while `researcher.InputSchema()` and `researcher.OutputSchema()` return the Agent input and output schemas. All three are `*schema.Schema` values (the Agent API exposes it through the `core.Schema` alias).

### Memory Configuration

```go
mem := memory.NewBuffer(20) // 最多保留 20 条 entry

researcher := agent.NewReAct(
    agent.WithLLM(provider),
    agent.WithMemory(mem),
)
```

When `WithMemory` is omitted, `BaseAgent`/`ReActAgent` create a Buffer Memory with capacity 100. `ReActAgent` retrieves history before a call and saves the successful turn afterward; `BaseAgent.Invoke` does not currently read or write Memory automatically.

## Middleware System

Hexagon has two middleware layers: the `agent.AgentMiddleware` in this section wraps one complete Agent request; `runtime.Middleware` is injected with `agent.WithMiddleware` and observes the ReAct LLM/tool lifecycle.

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
handler := chain.Wrap(func(ctx context.Context, input agent.Input) (agent.Output, error) {
    return myAgent.Invoke(ctx, input)
})

// Run
output, err := handler(ctx, input)
if err != nil {
    return fmt.Errorf("handle agent request: %w", err)
}
fmt.Println(output.Content)
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

### Runtime Lifecycle Middleware

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

`runtime.Middleware` also defines `AfterLLM`, `BeforeTool`, `AfterTool`, and `Finalize` lifecycle methods. `runtime.MiddlewareFuncSet` lets an application implement only the callbacks it needs.

## State Management

### Four-Layer State

`agent.StateManager` is an explicit application-level state utility. It is not the same object as `WithMemory` or ReAct's internal `runtime.State`. The caller must create and retain it, and call `NewTurn` at the appropriate boundary:

```go
global := agent.NewGlobalState()
state := agent.NewStateManager("session-123", global)

// Turn state: single conversation turn
state.Turn().Set("key", value)

// Session state: conversation level
state.Session().Set("user_id", "123")

// Agent state: persistent Agent state
state.Agent().Set("config", config)

// Global state: cross-Agent shared state
state.Global().Set("shared_data", data)

// 开始下一轮并清空 Turn 状态
state.NewTurn()
```

## Role System

Define the role characteristics of an Agent. `WithRole` stores role metadata, but the current `BaseAgent`/`ReActAgent` do not automatically add it to the system prompt. To make the role affect model behavior, use `Role.ToSystemPrompt()` explicitly:

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

## Team Collaboration

Create a multi-Agent team:

Assume `researcher`, `writer`, and `reviewer` are initialized `agent.Agent` values:

```go
// Create the team (the first argument is the team name)
team := agent.NewTeam("content-team",
    agent.WithMode(agent.TeamModeSequential),
    agent.WithAgents(researcher, writer, reviewer),
)

// Run the team task
output, err := team.Invoke(ctx, agent.Input{Query: "Prepare a technical article."})
if err != nil {
    return fmt.Errorf("invoke team: %w", err)
}
fmt.Println(output.Content)
```

### Team Modes

- `agent.TeamModeSequential`: sequential execution
- `agent.TeamModeHierarchical`: hierarchical execution
- `agent.TeamModeCollaborative`: collaborative execution
- `agent.TeamModeRoundRobin`: round-robin execution

Hierarchical mode requires a Manager through `agent.WithManager(manager)` and still requires members through `agent.WithAgents(...)`. `WithManager` automatically selects `TeamModeHierarchical`.

## Streaming Output

`Stream` returns `*streamx.StreamReader[agent.Output]`. The current `BaseAgent`, `ReActAgent`, and `Team` complete the call first and then place one complete `Output` in the stream; this is not Provider token-level streaming.

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

## Error Handling

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

`Run` is a backward-compatibility method and is deprecated; new code should use `Invoke`. Wrap errors with `%w` so callers can inspect the cause with `errors.Is`/`errors.As`.

## Best Practices

1. **Prefer Invoke**: do not make the compatibility method `Run` the primary entry point in new code
2. **Set timeouts**: bound the complete call with `context.WithTimeout` or request middleware
3. **Use the right middleware boundary**: request middleware covers recovery, logging, and rate limiting; runtime middleware covers LLM/tool lifecycle events
4. **Retry carefully**: use request-level retry only when the whole call and all tool side effects are safe to repeat
5. **Limit iterations**: set a reasonable `MaxIterations` for `ReActAgent` and inspect the output `StopReason`
6. **Clean up resources**: close each StreamReader and handle the error returned by `Close`
