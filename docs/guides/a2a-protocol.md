<div align="right">语言: 中文 | <a href="a2a-protocol.en.md">English</a></div>

# A2A (Agent-to-Agent) 协议指南

Hexagon 实现了 Google A2A 协议，使 AI Agent 能够安全地相互通信、协调任务、共享上下文。

## 概述

A2A (Agent-to-Agent) 是一个开放协议，定义了 AI Agent 之间标准化的通信方式。通过 A2A 协议，您可以：

- 将 Hexagon Agent 暴露为标准 A2A 服务
- 连接任意符合 A2A 规范的远程 Agent
- 实现跨平台、跨框架的 Agent 互操作

## 快速开始

### 创建 A2A 服务器

```go
import (
    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/hexagon/agent/a2a"
)

// 创建 Hexagon Agent
myAgent := agent.NewBaseAgent(
    agent.WithName("assistant"),
    agent.WithLLM(llmProvider),
)

// 一键暴露为 A2A 服务
server := a2a.ExposeAgent(myAgent, "http://localhost:8080")
server.Start(":8080")
```

### 连接 A2A Agent

```go
// 连接远程 A2A Agent
client := a2a.NewClient("http://localhost:8080")

// 获取 Agent Card
card, _ := client.GetAgentCard(ctx)
fmt.Printf("Agent: %s - %s\n", card.Name, card.Description)

// 发送消息
task, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("你好"),
})

fmt.Printf("Task ID: %s, Status: %s\n", task.ID, task.Status.State)
```

## 核心概念

### Agent Card

Agent Card 是 A2A 协议的核心概念，描述了 Agent 的基本信息、能力和技能。

```go
card := &a2a.AgentCard{
    Name:        "assistant",
    Description: "通用助手 Agent",
    URL:         "http://localhost:8080",
    Version:     "1.0.0",
    Provider: &a2a.AgentProvider{
        Organization: "My Company",
        URL:          "https://example.com",
    },
    Capabilities: a2a.AgentCapabilities{
        Streaming:         true,  // 支持流式响应
        PushNotifications: true,  // 支持推送通知
    },
    Skills: []a2a.AgentSkill{
        {ID: "search", Name: "搜索", Description: "网络搜索"},
        {ID: "code", Name: "编程", Description: "代码生成"},
    },
}
```

Agent Card 通过 `/.well-known/agent-card.json` 路径提供。

### Task 生命周期

Task 是 A2A 中的核心工作单元，表示一次 Agent 交互的完整生命周期。

```
submitted → working → input-required → completed
                  ↘                 ↗
                   → failed/canceled
```

状态说明：
- `submitted`: 任务已创建，等待处理
- `working`: Agent 正在处理任务
- `input-required`: 等待用户提供更多信息
- `completed`: 任务成功完成
- `failed`: 任务执行失败
- `canceled`: 任务被取消

### Message

Message 是 Agent 与用户之间的通信单元，支持多模态内容。

```go
// 文本消息
msg := a2a.NewUserMessage("Hello")

// 多模态消息
msg := a2a.Message{
    Role: a2a.RoleUser,
    Parts: []a2a.Part{
        &a2a.TextPart{Text: "请分析这张图片"},
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

Artifact 是任务执行过程中生成的输出产物。

```go
artifact := a2a.Artifact{
    Name:        "analysis_result",
    Description: "分析结果",
    Parts: []a2a.Part{
        &a2a.TextPart{Text: "分析完成..."},
        &a2a.DataPart{
            Data: map[string]any{
                "score": 0.95,
                "tags":  []string{"positive", "technical"},
            },
        },
    },
}
```

## 服务端开发

### 自定义 TaskHandler

```go
type MyHandler struct {
    llm llm.Provider
}

func (h *MyHandler) HandleTask(ctx context.Context, task *a2a.Task, msg *a2a.Message) (*a2a.TaskUpdate, error) {
    // 获取用户消息
    userText := msg.GetTextContent()

    // 调用 LLM 处理
    resp, err := h.llm.Complete(ctx, llm.CompletionRequest{
        Messages: []llm.Message{
            {Role: "user", Content: userText},
        },
    })
    if err != nil {
        return a2a.NewFailedUpdate(err.Error()), nil
    }

    // 返回完成状态
    return a2a.NewCompletedUpdate(&a2a.Message{
        Role: a2a.RoleAgent,
        Parts: []a2a.Part{
            &a2a.TextPart{Text: resp.Content},
        },
    }), nil
}
```

### 流式处理

```go
func (h *MyHandler) HandleTaskStream(ctx context.Context, task *a2a.Task, msg *a2a.Message) (<-chan *a2a.TaskUpdate, error) {
    updates := make(chan *a2a.TaskUpdate)

    go func() {
        defer close(updates)

        // 流式生成内容
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

        // 发送完成状态
        updates <- a2a.NewCompletedUpdate(...)
    }()

    return updates, nil
}
```

### 创建完整服务器

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

## 客户端开发

### 基本使用

```go
client := a2a.NewClient("http://localhost:8080",
    a2a.WithTimeout(30 * time.Second),
    a2a.WithAuth(&a2a.BearerAuth{Token: "my-token"}),
)
defer client.Close()

// 发送消息并获取结果
task, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("Hello"),
})

// 检查任务状态
if task.Status.State == a2a.TaskStateCompleted {
    // 获取 Agent 响应
    for _, msg := range task.History {
        if msg.Role == a2a.RoleAgent {
            fmt.Println(msg.GetTextContent())
        }
    }
}
```

### 流式交互

```go
events, _ := client.SendMessageStream(ctx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("写一首诗"),
})

for event := range events {
    switch e := event.(type) {
    case *a2a.TaskStatusEvent:
        fmt.Printf("Status: %s\n", e.Status.State)

    case *a2a.ArtifactEvent:
        fmt.Print(e.Artifact.GetTextContent())

    case *a2a.DoneEvent:
        fmt.Println("\n完成!")
    }
}
```

### 多轮对话

```go
// 第一轮
task1, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("你好"),
})

// 第二轮 - 继续同一任务
task2, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    TaskID:  task1.ID,  // 继续现有任务
    Message: a2a.NewUserMessage("请继续"),
})
```

## 认证和安全

### 服务端认证

```go
// Bearer Token 认证
validator := a2a.NewBearerTokenValidator()
validator.AddToken("secret-token", "client-1")

// API Key 认证
validator := a2a.NewAPIKeyValidator("X-API-Key", "header")
validator.AddKey("my-api-key", "client-1")

// RBAC 权限控制
rbac := a2a.NewRBACValidator(validator)
rbac.SetPermissions("client-1",
    a2a.PermissionRead,
    a2a.PermissionSendMessage,
)

// 应用认证中间件
mux := http.NewServeMux()
handler := a2a.AuthMiddleware(validator)(server.Handler())
```

### 客户端认证

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

## 推送通知

### 配置推送

```go
// 客户端配置推送
client.SetPushNotification(ctx, task.ID, &a2a.PushNotificationConfig{
    URL:   "https://my-webhook.example.com/callback",
    Token: "callback-token",
})

// 或在发送消息时配置
task, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("Hello"),
    PushNotification: &a2a.PushNotificationConfig{
        URL: "https://my-webhook.example.com/callback",
    },
})
```

### 处理推送回调

```go
http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
    var task a2a.Task
    json.NewDecoder(r.Body).Decode(&task)

    fmt.Printf("Task %s status: %s\n", task.ID, task.Status.State)

    w.WriteHeader(http.StatusOK)
})
```

## Agent 发现

### 静态发现

```go
discovery := a2a.NewStaticDiscovery(
    &a2a.AgentCard{Name: "agent-1", URL: "http://agent1.example.com"},
    &a2a.AgentCard{Name: "agent-2", URL: "http://agent2.example.com"},
)

// 发现所有 Agent
cards, _ := discovery.Discover(ctx, nil)

// 按技能过滤
cards, _ := discovery.Discover(ctx, &a2a.AgentFilter{
    Skills: []string{"search"},
})
```

### Registry 集成

```go
// 与 Hexagon Registry 集成
registry := agent.GlobalRegistry()
discovery := a2a.NewRegistryDiscovery(registry, "http://localhost:8080")

// Registry 中注册的 Agent 自动可发现
cards, _ := discovery.Discover(ctx, nil)
```

### 远程发现

```go
// 远程 Agent 发现
discovery := a2a.NewRemoteDiscovery(5 * time.Minute) // 缓存 5 分钟
discovery.AddAgent("http://agent1.example.com")
discovery.AddAgent("http://agent2.example.com")

// 自动获取 Agent Card
cards, _ := discovery.Discover(ctx, nil)
```

## 与 Hexagon 整合

### 包装现有 Agent

```go
// 将 Hexagon Agent 包装为 A2A Handler
myAgent := agent.NewReAct(...)
handler := a2a.WrapAgent(myAgent)

// 或使用流式包装器
handler := a2a.WrapStreamingAgent(myAgent)
```

### 使用远程 A2A Agent

```go
// 连接远程 A2A Agent 作为 Hexagon Agent 使用
remoteAgent, _ := a2a.ConnectToA2AAgent("http://remote-agent.example.com")
defer remoteAgent.Close()

// 像普通 Agent 一样使用
output, _ := remoteAgent.Run(ctx, agent.Input{Query: "Hello"})
```

### 在 Agent 网络中使用

```go
// 创建 Agent 网络
network := agent.NewAgentNetwork("my-network")

// 添加本地 Agent
network.Register(localAgent)

// 添加远程 A2A Agent
remoteAgent, _ := a2a.ConnectToA2AAgent("http://remote.example.com")
network.Register(remoteAgent)

// Agent 间可以正常通信
network.SendTo(ctx, "local-agent", "remote-agent", "协作消息")
```

## API 参考

### 路径

| 路径 | 方法 | 说明 |
|------|------|------|
| `/.well-known/agent-card.json` | GET | 获取 Agent Card |
| `/tasks` | POST | JSON-RPC 端点 |
| `/tasks/sendSubscribe` | POST | 流式发送消息 |
| `/tasks/resubscribe` | POST | 重新订阅任务 |

### JSON-RPC 方法

| 方法 | 说明 |
|------|------|
| `tasks/send` | 发送消息 |
| `tasks/get` | 获取任务 |
| `tasks/list` | 列出任务 |
| `tasks/cancel` | 取消任务 |
| `tasks/pushNotification/set` | 设置推送配置 |
| `tasks/pushNotification/get` | 获取推送配置 |

### 错误码

| 代码 | 含义 |
|------|------|
| -32700 | JSON 解析错误 |
| -32600 | 无效请求 |
| -32601 | 方法不存在 |
| -32602 | 无效参数 |
| -32603 | 内部错误 |
| -32001 | 任务不存在 |
| -32002 | 任务不可取消 |
| -32003 | 不支持推送通知 |
| -32010 | 需要认证 |
| -32011 | 认证失败 |
| -32012 | 权限不足 |

## 最佳实践

### 1. 设计清晰的 Agent Card

```go
card := &a2a.AgentCard{
    Name:        "customer-service",  // 简洁明确的名称
    Description: "企业客服 Agent，提供产品咨询和技术支持",  // 详细描述
    Version:     "1.2.0",  // 语义化版本
    Skills: []a2a.AgentSkill{
        {
            ID:          "product-qa",
            Name:        "产品咨询",
            Description: "回答产品功能、价格、使用方法等问题",
            Examples:    []string{"这个产品有什么功能？", "价格是多少？"},
        },
        {
            ID:          "tech-support",
            Name:        "技术支持",
            Description: "解决技术问题和故障排查",
            Examples:    []string{"无法登录怎么办？", "为什么显示错误？"},
        },
    },
}
```

### 2. 优雅处理错误

```go
func (h *MyHandler) HandleTask(ctx context.Context, task *a2a.Task, msg *a2a.Message) (*a2a.TaskUpdate, error) {
    // 业务错误应返回 TaskUpdate，不要返回 error
    if !isValid(msg) {
        return a2a.NewFailedUpdate("消息格式不正确，请重试"), nil
    }

    result, err := h.process(ctx, msg)
    if err != nil {
        // 区分可重试和不可重试错误
        if isRetryable(err) {
            return a2a.NewFailedUpdate("服务暂时不可用，请稍后重试"), nil
        }
        return a2a.NewFailedUpdate("处理失败: " + err.Error()), nil
    }

    return a2a.NewCompletedUpdate(result), nil
}
```

### 3. 使用会话管理多轮对话

```go
// 通过 SessionID 关联多个任务
task1, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    SessionID: "user-session-123",
    Message:   a2a.NewUserMessage("第一轮"),
})

task2, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    SessionID: "user-session-123",  // 同一会话
    Message:   a2a.NewUserMessage("第二轮"),
})

// 查询会话中的所有任务
tasks, _ := client.ListTasks(ctx, &a2a.ListTasksRequest{
    SessionID: "user-session-123",
})
```

## 参考资料

- [Google A2A Protocol Specification](https://google.github.io/A2A/)
- [JSON-RPC 2.0 Specification](https://www.jsonrpc.org/specification)
- [Server-Sent Events (SSE)](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events)
