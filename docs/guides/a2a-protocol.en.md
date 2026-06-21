<div align="right">Language: <a href="a2a-protocol.md">中文</a> | English</div>

# A2A (Agent-to-Agent) Protocol Guide

Hexagon implements the Google A2A protocol, enabling AI Agents to communicate securely, coordinate tasks, and share context with each other.

## Overview

A2A (Agent-to-Agent) is an open protocol that defines a standardized communication method between AI Agents. With the A2A protocol, you can:

- Expose Hexagon Agents as standard A2A services
- Connect to any remote Agent that conforms to the A2A specification
- Achieve cross-platform, cross-framework Agent interoperability

## Quick Start

### Creating an A2A Server

```go
import (
    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/hexagon/agent/a2a"
)

// Create a Hexagon Agent
myAgent := agent.NewBaseAgent(
    agent.WithName("assistant"),
    agent.WithLLM(llmProvider),
)

// Expose it as an A2A service with one line
server := a2a.ExposeAgent(myAgent, "http://localhost:8080")
server.Start(":8080")
```

### Connecting to an A2A Agent

```go
// Connect to a remote A2A Agent
client := a2a.NewClient("http://localhost:8080")

// Retrieve the Agent Card
card, _ := client.GetAgentCard(ctx)
fmt.Printf("Agent: %s - %s\n", card.Name, card.Description)

// Send a message
task, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("Hello"),
})

fmt.Printf("Task ID: %s, Status: %s\n", task.ID, task.Status.State)
```

## Core Concepts

### Agent Card

The Agent Card is a central concept in the A2A protocol. It describes an Agent's basic information, capabilities, and skills.

```go
card := &a2a.AgentCard{
    Name:        "assistant",
    Description: "General-purpose assistant Agent",
    URL:         "http://localhost:8080",
    Version:     "1.0.0",
    Provider: &a2a.AgentProvider{
        Organization: "My Company",
        URL:          "https://example.com",
    },
    Capabilities: a2a.AgentCapabilities{
        Streaming:         true,  // supports streaming responses
        PushNotifications: true,  // supports push notifications
    },
    Skills: []a2a.AgentSkill{
        {ID: "search", Name: "Search", Description: "Web search"},
        {ID: "code", Name: "Coding", Description: "Code generation"},
    },
}
```

The Agent Card is served at `/.well-known/agent-card.json`.

### Task Lifecycle

A Task is the core unit of work in A2A, representing the complete lifecycle of a single Agent interaction.

```
submitted → working → input-required → completed
                  ↘                 ↗
                   → failed/canceled
```

State descriptions:
- `submitted`: Task created, awaiting processing
- `working`: Agent is processing the task
- `input-required`: Waiting for the user to provide more information
- `completed`: Task completed successfully
- `failed`: Task execution failed
- `canceled`: Task was canceled

### Message

A Message is the communication unit between an Agent and a user, and supports multimodal content.

```go
// Text message
msg := a2a.NewUserMessage("Hello")

// Multimodal message
msg := a2a.Message{
    Role: a2a.RoleUser,
    Parts: []a2a.Part{
        &a2a.TextPart{Text: "Please analyze this image"},
        &a2a.FilePart{
            File: a2a.FileContent{
                Name:     "image.png",
                MimeType: "image/png",
                Bytes:    base64EncodedData,
            },
        },
    },
}
```

### Artifact

An Artifact is an output produced during task execution.

```go
artifact := a2a.Artifact{
    Name:        "analysis_result",
    Description: "Analysis result",
    Parts: []a2a.Part{
        &a2a.TextPart{Text: "Analysis complete..."},
        &a2a.DataPart{
            Data: map[string]any{
                "score": 0.95,
                "tags":  []string{"positive", "technical"},
            },
        },
    },
}
```

## Server-Side Development

### Custom TaskHandler

```go
type MyHandler struct {
    llm llm.Provider
}

func (h *MyHandler) HandleTask(ctx context.Context, task *a2a.Task, msg *a2a.Message) (*a2a.TaskUpdate, error) {
    // Retrieve user message text
    userText := msg.GetTextContent()

    // Call LLM for processing
    resp, err := h.llm.Complete(ctx, llm.CompletionRequest{
        Messages: []llm.Message{
            {Role: "user", Content: userText},
        },
    })
    if err != nil {
        return a2a.NewFailedUpdate(err.Error()), nil
    }

    // Return completed status
    return a2a.NewCompletedUpdate(&a2a.Message{
        Role: a2a.RoleAgent,
        Parts: []a2a.Part{
            &a2a.TextPart{Text: resp.Content},
        },
    }), nil
}
```

### Streaming Processing

```go
func (h *MyHandler) HandleTaskStream(ctx context.Context, task *a2a.Task, msg *a2a.Message) (<-chan *a2a.TaskUpdate, error) {
    updates := make(chan *a2a.TaskUpdate)

    go func() {
        defer close(updates)

        // Stream content generation
        stream, _ := h.llm.Stream(ctx, req)

        for chunk := range stream {
            updates <- &a2a.TaskUpdate{
                Artifact: &a2a.Artifact{
                    Name:   "response",
                    Append: true,
                    Parts: []a2a.Part{
                        &a2a.TextPart{Text: chunk.Content},
                    },
                },
            }
        }

        // Send completion status
        updates <- a2a.NewCompletedUpdate(...)
    }()

    return updates, nil
}
```

### Creating a Full Server

```go
card := &a2a.AgentCard{
    Name:    "my-agent",
    URL:     "http://localhost:8080",
    Version: "1.0.0",
    Capabilities: a2a.AgentCapabilities{
        Streaming:         true,
        PushNotifications: true,
    },
}

handler := &MyHandler{llm: llmProvider}
server := a2a.NewServer(card, handler,
    a2a.WithStore(a2a.NewMemoryTaskStore()),
    a2a.WithPushService(a2a.NewDefaultPushService()),
    a2a.WithCORS(true, "*"),
)

server.Start(":8080")
```

## Client-Side Development

### Basic Usage

```go
client := a2a.NewClient("http://localhost:8080",
    a2a.WithTimeout(30 * time.Second),
    a2a.WithAuth(&a2a.BearerAuth{Token: "my-token"}),
)
defer client.Close()

// Send a message and get the result
task, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("Hello"),
})

// Check task status
if task.Status.State == a2a.TaskStateCompleted {
    // Retrieve Agent response
    for _, msg := range task.History {
        if msg.Role == a2a.RoleAgent {
            fmt.Println(msg.GetTextContent())
        }
    }
}
```

### Streaming Interaction

```go
events, _ := client.SendMessageStream(ctx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("Write a poem"),
})

for event := range events {
    switch e := event.(type) {
    case *a2a.TaskStatusEvent:
        fmt.Printf("Status: %s\n", e.Status.State)

    case *a2a.ArtifactEvent:
        fmt.Print(e.Artifact.GetTextContent())

    case *a2a.DoneEvent:
        fmt.Println("\nDone!")
    }
}
```

### Multi-Turn Conversation

```go
// First turn
task1, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("Hello"),
})

// Second turn - continue the same task
task2, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    TaskID:  task1.ID,  // continue existing task
    Message: a2a.NewUserMessage("Please continue"),
})
```

## Authentication and Security

### Server-Side Authentication

```go
// Bearer Token authentication
validator := a2a.NewBearerTokenValidator()
validator.AddToken("secret-token", "client-1")

// API Key authentication
validator := a2a.NewAPIKeyValidator("X-API-Key", "header")
validator.AddKey("my-api-key", "client-1")

// RBAC access control
rbac := a2a.NewRBACValidator(validator)
rbac.SetPermissions("client-1",
    a2a.PermissionRead,
    a2a.PermissionSendMessage,
)

// Apply authentication middleware
mux := http.NewServeMux()
handler := a2a.AuthMiddleware(validator)(server.Handler())
```

### Client-Side Authentication

```go
// Bearer Token
client := a2a.NewClient(url,
    a2a.WithAuth(&a2a.BearerAuth{Token: "my-token"}),
)

// API Key
client := a2a.NewClient(url,
    a2a.WithAuth(&a2a.APIKeyAuth{
        Key:   "X-API-Key",
        Value: "my-api-key",
        In:    "header",
    }),
)

// Basic Auth
client := a2a.NewClient(url,
    a2a.WithAuth(&a2a.BasicAuth{
        Username: "user",
        Password: "pass",
    }),
)
```

## Push Notifications

### Configuring Push Notifications

```go
// Client-side push notification configuration
client.SetPushNotification(ctx, task.ID, &a2a.PushNotificationConfig{
    URL:   "https://my-webhook.example.com/callback",
    Token: "callback-token",
})

// Or configure when sending a message
task, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("Hello"),
    PushNotification: &a2a.PushNotificationConfig{
        URL: "https://my-webhook.example.com/callback",
    },
})
```

### Handling Push Callbacks

```go
http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
    var task a2a.Task
    json.NewDecoder(r.Body).Decode(&task)

    fmt.Printf("Task %s status: %s\n", task.ID, task.Status.State)

    w.WriteHeader(http.StatusOK)
})
```

## Agent Discovery

### Static Discovery

```go
discovery := a2a.NewStaticDiscovery(
    &a2a.AgentCard{Name: "agent-1", URL: "http://agent1.example.com"},
    &a2a.AgentCard{Name: "agent-2", URL: "http://agent2.example.com"},
)

// Discover all Agents
cards, _ := discovery.Discover(ctx, nil)

// Filter by skill
cards, _ := discovery.Discover(ctx, &a2a.AgentFilter{
    Skills: []string{"search"},
})
```

### Registry Integration

```go
// Integrate with the Hexagon Registry
registry := agent.GlobalRegistry()
discovery := a2a.NewRegistryDiscovery(registry, "http://localhost:8080")

// Agents registered in the Registry are automatically discoverable
cards, _ := discovery.Discover(ctx, nil)
```

### Remote Discovery

```go
// Remote Agent discovery
discovery := a2a.NewRemoteDiscovery(5 * time.Minute) // cache for 5 minutes
discovery.AddAgent("http://agent1.example.com")
discovery.AddAgent("http://agent2.example.com")

// Automatically fetches Agent Cards
cards, _ := discovery.Discover(ctx, nil)
```

## Integration with Hexagon

### Wrapping an Existing Agent

```go
// Wrap a Hexagon Agent as an A2A Handler
myAgent := agent.NewReAct(...)
handler := a2a.WrapAgent(myAgent)

// Or use a streaming wrapper
handler := a2a.WrapStreamingAgent(myAgent)
```

### Using a Remote A2A Agent

```go
// Connect to a remote A2A Agent and use it as a Hexagon Agent
remoteAgent, _ := a2a.ConnectToA2AAgent("http://remote-agent.example.com")
defer remoteAgent.Close()

// Use it just like a local Agent
output, _ := remoteAgent.Run(ctx, agent.Input{Query: "Hello"})
```

### Using in an Agent Network

```go
// Create an Agent network
network := agent.NewAgentNetwork("my-network")

// Add a local Agent
network.Register(localAgent)

// Add a remote A2A Agent
remoteAgent, _ := a2a.ConnectToA2AAgent("http://remote.example.com")
network.Register(remoteAgent)

// Agents can communicate normally
network.SendTo(ctx, "local-agent", "remote-agent", "Collaboration message")
```

## API Reference

### Endpoints

| Path | Method | Description |
|------|--------|-------------|
| `/.well-known/agent-card.json` | GET | Retrieve Agent Card |
| `/tasks` | POST | JSON-RPC endpoint |
| `/tasks/sendSubscribe` | POST | Stream message sending |
| `/tasks/resubscribe` | POST | Resubscribe to a task |

### JSON-RPC Methods

| Method | Description |
|--------|-------------|
| `tasks/send` | Send a message |
| `tasks/get` | Get a task |
| `tasks/list` | List tasks |
| `tasks/cancel` | Cancel a task |
| `tasks/pushNotification/set` | Set push notification config |
| `tasks/pushNotification/get` | Get push notification config |

### Error Codes

| Code | Meaning |
|------|---------|
| -32700 | JSON parse error |
| -32600 | Invalid request |
| -32601 | Method not found |
| -32602 | Invalid parameters |
| -32603 | Internal error |
| -32001 | Task not found |
| -32002 | Task cannot be canceled |
| -32003 | Push notifications not supported |
| -32010 | Authentication required |
| -32011 | Authentication failed |
| -32012 | Insufficient permissions |

## Best Practices

### 1. Design a Clear Agent Card

```go
card := &a2a.AgentCard{
    Name:        "customer-service",  // concise, descriptive name
    Description: "Enterprise customer service Agent for product inquiries and technical support",
    Version:     "1.2.0",  // semantic versioning
    Skills: []a2a.AgentSkill{
        {
            ID:          "product-qa",
            Name:        "Product Inquiries",
            Description: "Answers questions about product features, pricing, and usage",
            Examples:    []string{"What features does this product have?", "How much does it cost?"},
        },
        {
            ID:          "tech-support",
            Name:        "Technical Support",
            Description: "Resolves technical issues and troubleshooting",
            Examples:    []string{"I can't log in, what should I do?", "Why am I seeing an error?"},
        },
    },
}
```

### 2. Handle Errors Gracefully

```go
func (h *MyHandler) HandleTask(ctx context.Context, task *a2a.Task, msg *a2a.Message) (*a2a.TaskUpdate, error) {
    // Business errors should return a TaskUpdate, not an error
    if !isValid(msg) {
        return a2a.NewFailedUpdate("Invalid message format, please try again"), nil
    }

    result, err := h.process(ctx, msg)
    if err != nil {
        // Distinguish between retryable and non-retryable errors
        if isRetryable(err) {
            return a2a.NewFailedUpdate("Service temporarily unavailable, please try again later"), nil
        }
        return a2a.NewFailedUpdate("Processing failed: " + err.Error()), nil
    }

    return a2a.NewCompletedUpdate(result), nil
}
```

### 3. Use Sessions for Multi-Turn Conversations

```go
// Associate multiple tasks via SessionID
task1, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    SessionID: "user-session-123",
    Message:   a2a.NewUserMessage("First turn"),
})

task2, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    SessionID: "user-session-123",  // same session
    Message:   a2a.NewUserMessage("Second turn"),
})

// Query all tasks in the session
tasks, _ := client.ListTasks(ctx, &a2a.ListTasksRequest{
    SessionID: "user-session-123",
})
```

## References

- [Google A2A Protocol Specification](https://google.github.io/A2A/)
- [JSON-RPC 2.0 Specification](https://www.jsonrpc.org/specification)
- [Server-Sent Events (SSE)](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events)
