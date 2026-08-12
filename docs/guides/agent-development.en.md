<div align="right">Language: <a href="agent-development.md">中文</a> | English</div>

# Agent Development Guide

This guide will help you get started developing Hexagon AI Agents quickly.

## Quick Start

### Creating Your First Agent

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
    provider := openai.New("your-api-key")

    // Create an Agent
    myAgent := agent.NewBaseAgent(
        agent.WithName("my-agent"),
        agent.WithSystemPrompt("You are a helpful assistant"),
        agent.WithLLM(provider),
    )

    // Run the Agent
    ctx := context.Background()
    result, err := myAgent.Invoke(ctx, agent.Input{
        Query: "Hello!",
    })

    if err != nil {
        panic(err)
    }

    fmt.Println(result.Content)
}
```

## Agent Types

### BaseAgent

The most fundamental Agent implementation, suitable for simple conversational scenarios.

**Characteristics**:
- Lightweight
- Supports tool calls
- Supports the memory system

**Use cases**:
- Simple Q&A
- Customer service bots
- Knowledge lookup

### ReActAgent

An Agent that implements the ReAct (Reasoning + Acting) reasoning pattern.

**Characteristics**:
- Reason-act loop
- Automatic tool selection
- Visible chain of thought

**Use cases**:
- Complex task decomposition
- Multi-step reasoning required
- Tool-intensive tasks

```go
reactAgent := agent.NewReAct(
    agent.WithName("react-agent"),
    agent.WithSystemPrompt("You are an AI assistant capable of reasoning and acting"),
    agent.WithLLM(provider),
    agent.WithTools(
        searchTool,
        calculatorTool,
    ),
    agent.WithMaxIterations(5), // up to 5 reasoning rounds
)
```

## Adding Tools

Tools allow an Agent to perform concrete operations.

### Using Built-in Tools

```go
import (
    "github.com/hexagon-codes/hexagon/tool/file"
    "github.com/hexagon-codes/hexagon/tool/shell"
)

// File operation tools
fileTools := file.New(file.DefaultConfig()).All()

// Shell execution tool
shellTool := shell.New(shell.DefaultConfig()).ExecuteTool()

myAgent := agent.NewBaseAgent(
    agent.WithTools(append(fileTools, shellTool)...),
)
```

### Creating Custom Tools

```go
import "github.com/hexagon-codes/ai-core/tool"

// Use a function-based tool
weatherTool := tool.NewFunc(
    "get_weather",
    "Get the weather information for a specified city",
    func(ctx context.Context, input struct {
        City string `json:"city" description:"city name"`
    }) (struct {
        Temperature int    `json:"temperature"`
        Condition   string `json:"condition"`
    }, error) {
        // implement weather query logic
        return struct {
            Temperature int    `json:"temperature"`
            Condition   string `json:"condition"`
        }{
            Temperature: 25,
            Condition:   "Sunny",
        }, nil
    },
)
```

## Memory System

### Configuring Memory

```go
import "github.com/hexagon-codes/ai-core/memory"

// Create a memory instance (capacity is the number of recent messages to keep)
mem := memory.NewBuffer(10)

myAgent := agent.NewBaseAgent(
    agent.WithMemory(mem),
)
```

### Memory Types

- **BufferMemory** (`memory.NewBuffer`): simple buffer memory that retains the most recent N messages
- **SummaryMemory** (`memory.NewSummaryMemory`): summary memory that periodically summarizes conversation history
- **VectorMemory** (`memory.NewVectorMemory`): vector memory that retrieves based on semantic similarity
- **MultiLayerMemory** (`memory.NewMultiLayerMemory`): multi-layer memory combining a buffer with long-term storage

## Configuring an Agent

### Using YAML Configuration

```yaml
# agent.yaml
name: my-agent
type: react
role:
  name: Assistant
  goal: Help users solve problems
  backstory: You are an experienced AI assistant
llm:
  provider: openai
  model: gpt-4o
  api_key: ${OPENAI_API_KEY}
  temperature: 0.7
max_iterations: 5
verbose: false
memory:
  type: buffer
  max_size: 10
```

```go
import (
    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/hexagon/config"
)

func buildAgentFromConfig(path string) (agent.Agent, error) {
    cfg, err := config.LoadAgentConfig(path)
    if err != nil {
        return nil, err
    }

    return config.NewBuilder().BuildAgent(cfg)
}
```

`LoadAgentConfig` only parses the file and expands environment variables; `AgentConfig` has no `Build` method. Construct the Agent explicitly with `config.Builder.BuildAgent`. The default tool factory does not create file or shell tools from YAML automatically. If you need those tools, construct and inject them explicitly as shown in “Using Built-in Tools,” or provide a custom `ToolFactory`.

## Best Practices

### 1. System Prompt Design

**Good prompt**:
```
You are a professional customer service assistant. Your responsibilities are:
1. Answer customer questions politely
2. Use tools to look up order information
3. If you cannot answer, direct the customer to a human agent

Guidelines:
- Maintain a professional and friendly tone
- Keep answers concise and clear
- Confirm key information before taking action
```

**Poor prompt**:
```
You are an assistant, answer questions.
```

### 2. Tool Naming Conventions

- Use lowercase with underscores: `get_user_info`
- Descriptions should be clear and accurate
- Parameters should have detailed `description` fields

### 3. Error Handling

```go
result, err := myAgent.Invoke(ctx, input)
if err != nil {
    switch {
    case errors.Is(err, context.DeadlineExceeded):
        // Handle timeout
        fmt.Println("execution timed out")
    default:
        // Errors are wrapped layer by layer with fmt.Errorf("...: %w", err);
        // use errors.Is / errors.As to unwrap and inspect the root cause
        fmt.Printf("execution failed: %v\n", err)
    }

    return err
}
```

### 4. Performance Optimization

- Use streaming output to improve response speed
- Set a reasonable memory window size
- Limit the number of tool executions to prevent infinite loops
- Use caching to reduce repeated computation

```go
import (
    "errors"
    "io"
)

// Streaming output: Stream returns *stream.StreamReader[agent.Output]
reader, err := myAgent.Stream(ctx, input)
if err != nil {
    panic(err)
}
defer reader.Close()

for {
    chunk, err := reader.Recv()
    if errors.Is(err, io.EOF) {
        break
    }
    if err != nil {
        panic(err)
    }
    fmt.Print(chunk.Content)
}
```

## Debugging Tips

### Enable Verbose Logging

```go
import "github.com/hexagon-codes/hexagon/observe/logger"

// Set the global log level (a string: "debug" / "info" / "warn" / "error")
logger.SetLevel("debug")

// Or enable verbose logging when constructing the Agent
myAgent := agent.NewReAct(
    agent.WithLLM(provider),
    agent.WithVerbose(true),
)
```

### Using Dev UI

```go
import (
    "context"
    "log"
    "time"

    "github.com/hexagon-codes/hexagon/hooks"
    "github.com/hexagon-codes/hexagon/observe/devui"
)

ui := devui.New(devui.WithAddr("127.0.0.1:8080"))
ctx = hooks.ContextWithManager(ctx, ui.HookManager())

go func() {
    if err := ui.Start(); err != nil {
        log.Printf("Dev UI stopped with error: %v", err)
    }
}()

defer func() {
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := ui.Stop(shutdownCtx); err != nil {
        log.Printf("Failed to stop Dev UI: %v", err)
    }
}()
```

Both the default configuration and this example bind Dev UI only to the local loopback interface. If you switch to a non-loopback address, configure a token containing at least 32 non-whitespace bytes with `devui.WithAuthToken`; otherwise the server refuses to start.

## Next Steps

- Learn about [Multi-Agent Collaboration](./multi-agent.en.md)
- Explore [RAG System Integration](./rag-integration.en.md)
- Master [Graph Orchestration](./graph-orchestration.en.md)
