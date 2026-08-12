<div align="right">语言: 中文 | <a href="a2a-protocol.en.md">English</a></div>

# A2A (Agent-to-Agent) 协议指南

Hexagon 的 `agent/a2a` 包提供 Agent Card、Task、JSON-RPC、SSE、认证、推送和 Agent 桥接。本文以当前仓库源码为准；`ProtocolVersion` 当前为 `1.0`。

## 能力边界

- 普通请求使用 JSON-RPC 2.0，流式请求使用 Server-Sent Events (SSE)。
- `Server` 内置内存 Task 存储和可选推送，但不会自动安装认证中间件。
- `Client` 支持同步 Task API 和 SSE 事件流；关闭后的请求返回 `ErrClientClosed`。
- `AgentCard.Capabilities` 同时是能力声明和服务端门禁。只声明已经配置的能力。
- 默认存储、异步推送队列和订阅者都在进程内，不是持久化或可靠消息保证。

## 核心模型

### Agent Card

Agent Card 由 `GET /.well-known/agent-card.json` 公布。名称、URL 和版本是最基本的字段；能力、认证方式和技能应与实际服务一致。

```go
card := a2a.NewAgentCard("assistant", "http://localhost:8080", "1.0.0")
card.Description = "Text assistant"
card.Capabilities = a2a.AgentCapabilities{
	Streaming:         true,
	PushNotifications: false,
}
card.Authentication = &a2a.AuthConfig{
	Schemes: []a2a.AuthScheme{{Type: "bearer", BearerFormat: "JWT"}},
}
card.Skills = []a2a.AgentSkill{{
	ID:          "answer",
	Name:        "Answer questions",
	Tags:        []string{"text", "qa"},
	InputModes:  []string{"text"},
	OutputModes: []string{"text"},
}}
```

`NewAgentCard` 默认将 `Streaming` 设为 `true`。若不提供 SSE，应显式改为 `false`。

### Task 与状态

Task 是一次交互的持久身份，包含 `ID`、可选 `SessionID`、当前 `Status`、`History`、`Artifacts` 和元数据。

典型流程如下：

```text
submitted → working → input-required → working
                    └→ completed | failed | canceled
```

`completed`、`failed` 和 `canceled` 是终态，可用 `TaskState.IsTerminal()` 判断。在 `SendMessageRequest.TaskID` 中传入已存在的 ID 会继续该 Task；传入未知 ID 会返回 Task-not-found 错误。`SessionID` 用于关联多个 Task，不代替 `TaskID`。

### Message、Part 与 Artifact

Message 的角色为 `user` 或 `agent`，内容由 `TextPart`、`FilePart` 或 `DataPart` 组成。Artifact 使用同样的 Part 模型表达产物。

```go
msg := a2a.NewUserMessage("Summarize this document")
msg.Parts = append(msg.Parts, &a2a.DataPart{
	Data: map[string]any{"language": "zh-CN"},
})

artifact := a2a.NewTextArtifact("summary", "Summary content")
artifact.LastChunk = true
```

`Message.GetTextContent()` 和 `Artifact.GetTextContent()` 只汇总文本 Part。服务端收到 `Artifact.Append=true` 时，会将 Part 追加到最后一个已存在产物；否则创建新产物并由服务端分配 `Index`。

## Server 与 Handler

### 最小 Server

`TaskHandler` 的完整签名是：

```go
type TaskHandler interface {
	HandleTask(
		ctx context.Context,
		task *a2a.Task,
		msg *a2a.Message,
	) (*a2a.TaskUpdate, error)
}
```

可用 `NewFuncHandler` 包装函数。`Server.Start` 是阻塞调用并返回 `error`，不能忽略。

```go
func main() {
	card := a2a.NewAgentCard("assistant", "http://localhost:8080", "1.0.0")
	card.Capabilities.Streaming = false

	handler := a2a.NewFuncHandler(func(
		ctx context.Context,
		task *a2a.Task,
		msg *a2a.Message,
	) (*a2a.TaskUpdate, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return a2a.NewCompletedUpdate(
			a2aMessage("Echo: " + msg.GetTextContent()),
		), nil
	})

	server := a2a.NewServer(card, handler, a2a.WithCORS(false))
	if err := server.Start(":8080"); err != nil {
		log.Fatal(err)
	}
}

func a2aMessage(text string) *a2a.Message {
	msg := a2a.NewAgentMessage(text)
	return &msg
}
```

若需要信号处理、自定义超时或中间件，使用 `server.Handler()` 作为自建 `http.Server` 的 Handler，并检查 `ListenAndServe` 和 `Shutdown` 的错误。直接使用 `Start` 时，优雅停止由 `server.Stop(ctx)` 提供，其错误也必须处理。

`NewServer` 默认使用 `MemoryTaskStore`。生产环境要持久化 Task 时，通过 `WithStore` 注入自己的 `TaskStore`；需要推送配置时，自定义 Store 还应实现 `PushConfigStore`。

### 处理结果和错误

`TaskUpdate` 可同时携带 `Status`、`Message`、`Artifact`、`Metadata` 和 `Final`。常用构造器有：

- `NewStatusUpdate`
- `NewMessageUpdate`
- `NewArtifactUpdate`
- `NewCompletedUpdate`
- `NewFailedUpdate`
- `NewInputRequiredUpdate`

普通 `HandleTask` 返回非空 `error` 时，服务端会将 Task 记为 `failed` 并返回 JSON-RPC/SSE 错误。`HandleTaskStream` 在建立流时直接返回错误，当前只会写出 SSE `error` 事件，不会自动持久化 `failed` 终态。若要把“业务失败”作为可查询的 Task 终态返回，普通 Handler 返回 `NewFailedUpdate(message), nil`，流式 Handler 则把该 update 发入通道。

### 流式 Handler

```go
type StreamingTaskHandler interface {
	a2a.TaskHandler
	HandleTaskStream(
		ctx context.Context,
		task *a2a.Task,
		msg *a2a.Message,
	) (<-chan *a2a.TaskUpdate, error)
}
```

`NewStreamingFuncHandler` 必须同时接收普通函数和流式函数：

```go
normal := func(
	ctx context.Context,
	task *a2a.Task,
	msg *a2a.Message,
) (*a2a.TaskUpdate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reply := a2a.NewAgentMessage(msg.GetTextContent())
	return a2a.NewCompletedUpdate(&reply), nil
}

stream := func(
	ctx context.Context,
	task *a2a.Task,
	msg *a2a.Message,
) (<-chan *a2a.TaskUpdate, error) {
	updates := make(chan *a2a.TaskUpdate)
	go func() {
		defer close(updates)
		chunk := a2a.NewTextArtifact("response", msg.GetTextContent())
		select {
		case updates <- a2a.NewArtifactUpdate(&chunk):
		case <-ctx.Done():
			return
		}
		reply := a2a.NewAgentMessage("done")
		select {
		case updates <- a2a.NewCompletedUpdate(&reply):
		case <-ctx.Done():
		}
	}()
	return updates, nil
}

handler := a2a.NewStreamingFuncHandler(normal, stream)
```

流式 Handler 应在关闭通道前发送终态且 `Final=true` 的 update；若直接关闭，Server 仍会发送 `done`，但最终 Task 可能仍是 `working`。

只有 `AgentCard.Capabilities.Streaming=true` 时服务端才接受流式端点。若 Card 声明支持流式、但 Handler 只实现 `TaskHandler`，服务端会同步调用 `HandleTask`，然后以最终 Task 状态和 `done` 事件结束 SSE，不会产生真正的中间分块。

## Client

### 普通请求

```go
func call(ctx context.Context) (_ *a2a.Task, err error) {
	client := a2a.NewClient(
		"http://localhost:8080",
		a2a.WithTimeout(10*time.Second),
	)
	defer func() {
		if closeErr := client.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	card, err := client.GetAgentCard(ctx)
	if err != nil {
		return nil, fmt.Errorf("get Agent Card: %w", err)
	}
	if card.Name == "" {
		return nil, fmt.Errorf("Agent Card has no name")
	}

	task, err := client.SendMessage(ctx, &a2a.SendMessageRequest{
		SessionID: "conversation-1",
		Message:   a2a.NewUserMessage("Hello"),
	})
	if err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}
	return task, nil
}
```

继续同一 Task 时再次调用 `SendMessage` 并传入 `TaskID: task.ID`。`GetTask`、`ListTasks`、`CancelTask`、`SetPushNotification` 和 `GetPushNotification` 都返回需要处理的错误。所有网络调用都应传入带截止时间或可取消的 `context.Context`；客户端保留错误链，可用 `errors.Is(err, context.DeadlineExceeded)` 判断超时。

### 消费 SSE

```go
func consume(
	ctx context.Context,
	client *a2a.Client,
	onArtifact func(string),
) error {
	if onArtifact == nil {
		return fmt.Errorf("onArtifact must not be nil")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := client.SendMessageStream(streamCtx, &a2a.SendMessageRequest{
		Message: a2a.NewUserMessage("Stream the answer"),
	})
	if err != nil {
		return err
	}

	for event := range events {
		switch value := event.(type) {
		case *a2a.TaskStatusEvent:
			if value.Final {
				continue
			}
		case *a2a.ArtifactEvent:
			onArtifact(value.Artifact.GetTextContent())
		case *a2a.ErrorEvent:
			if value.Error == nil {
				return fmt.Errorf("empty SSE error")
			}
			return value.Error
		case *a2a.DoneEvent:
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
```

HTTP 请求建立后发生的解析或流错误会通过 `*ErrorEvent` 传递，不会再从 `SendMessageStream` 返回。因此必须消费通道直到关闭并处理 `ErrorEvent`。`Resubscribe(ctx, taskID)` 使用同样的事件模型。

## 认证与授权

### 服务端

服务端提供 `BearerTokenValidator`、`APIKeyValidator`、`BasicAuthValidator`、`ChainValidator`、`AuthMiddleware` 和 `OptionalAuthMiddleware`。`NewServer`/`Start` 不会自动启用它们；需要把 `server.Handler()` 包装进自建 `http.Server`。

```go
func protectedHandler(server *a2a.Server) http.Handler {
	tokens := a2a.NewBearerTokenValidator()
	tokens.AddToken("secret", "client-1")

	routes := server.Handler()
	protected := a2a.AuthMiddleware(tokens)(routes)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == a2a.PathAgentCard {
			routes.ServeHTTP(w, r)
			return
		}
		protected.ServeHTTP(w, r)
	})
}
```

上例刻意让 Agent Card 保持公开。当前 `Client.GetAgentCard` 不应用 `WithAuth`，而 JSON-RPC 和 SSE 请求会应用；若连 Card 也放在认证后，标准 `Client`、`NewRemoteAgent` 和 `ConnectToA2AAgent` 将无法发现它。

RBAC 的正确顺序是先用 `AuthMiddleware(rbac)` 写入 `AuthContext`，再在内层使用 `rbac.RequirePermission(...)`。认证失败返回 HTTP 401，权限不足返回 HTTP 403，响应体仍是 JSON-RPC 错误对象。

### 客户端

```go
client := a2a.NewClient(
	"https://agent.example.com",
	a2a.WithAuth(&a2a.BearerAuth{Token: "secret"}),
	a2a.WithTimeout(10*time.Second),
)
defer func() {
	if err := client.Close(); err != nil {
		log.Printf("close A2A client: %v", err)
	}
}()
```

客户端还提供 `APIKeyAuth` 和 `BasicAuth`。认证凭证不要写入 Agent Card、日志或仓库；生产环境必须使用 TLS，并在服务端限制 CORS 来源。

## 推送通知

### 配置

只有 Card 声明 `PushNotifications=true` 时，推送配置 API 才可用；真正交付还要求 Server 通过 `WithPushService` 注入 `PushService`。`NewDefaultPushService` 现在返回 `(*AsyncPushService, error)`，必须检查错误并在退出时调用 `Close`。

```go
func serveWithPush(card *a2a.AgentCard, handler a2a.TaskHandler) error {
	push, err := a2a.NewDefaultPushService()
	if err != nil {
		return fmt.Errorf("create push service: %w", err)
	}
	defer push.Close()

	card.Capabilities.PushNotifications = true
	server := a2a.NewServer(card, handler, a2a.WithPushService(push))
	return server.Start(":8080")
}
```

新建 Task 时可直接携带推送配置：

```go
task, err := client.SendMessage(ctx, &a2a.SendMessageRequest{
	Message: a2a.NewUserMessage("Run the job"),
	PushNotification: &a2a.PushNotificationConfig{
		URL:   "https://consumer.example.com/a2a/events",
		Token: "callback-token",
	},
})
if err != nil {
	return err
}
if task.ID == "" {
	return fmt.Errorf("server returned an empty Task ID")
}
```

已存在 Task 可用 `client.SetPushNotification(ctx, task.ID, config)` 配置，并用 `GetPushNotification` 核对；两者的错误都要处理。

### 不可变通知契约

`PushNotification` 的字段是私有的，只能通过构造器创建。构造时会序列化一份快照；之后修改原 Task/Artifact，或修改访问器返回的副本，都不会改变通知。

```go
notification, err := a2a.NewTaskStatusNotification(task)
if err != nil {
	return err
}
snapshot, err := notification.Task()
if err != nil {
	return err
}
if snapshot == nil || notification.EventType() != a2a.EventTypeTaskStatus {
	return fmt.Errorf("invalid Task notification")
}
```

产物通知使用 `NewArtifactNotification(taskID, artifact)`，并通过 `Artifact()` 读取副本；构造和读取都要检查错误。`TaskID()`、`EventType()` 和 `Timestamp()` 提供元数据。`NewWebhookPushService` 和 `NewAsyncPushService` 也会返回配置校验错误。

### 交付保证

当前实现是 best-effort，不承诺 Webhook 自动重试或可靠交付：

- `WebhookPushService.Push` 每次调用只发送一次 HTTP POST。`WithWebhookRetry` 当前只校验并保存配置，不是已执行的自动重试保证。
- `AsyncPushService.Push` 成功只代表入队；队列满返回 `ErrPushQueueFull`，关闭后返回 `ErrPushServiceClosed`。Worker 不向调用方传递底层交付错误。
- `Server` 在普通 `tasks/send` 结束后异步触发 Task 状态推送，不等待也不暴露推送错误；内建流式更新不会自动发送 Artifact Webhook。
- `Close` 会停止 Worker 并释放剩余队列，不承诺刷新完成所有待发消息。

需要至少一次交付时，实现自己的 `PushService` 和持久化 outbox，加入有界重试、幂等键、可观测失败与死信处理。`PushManager` 可用于任务级配置和速率限制，其构造函数 `NewPushManager` 也返回 `error`。

## Agent 发现

`Discovery` 统一定义 `Discover`、`Register`、`Deregister`、`Get` 和 `Watch`，每个方法都返回需要处理的错误。

| 实现 | 数据源 | 实际过滤 | `Watch` 语义 |
| --- | --- | --- | --- |
| `StaticDiscovery` | 进程内 Card map | Name 精确匹配或 `*`，以及 Skill ID | 立即返回已关闭通道 |
| `RegistryDiscovery` | `agent.Registry` 事件和手工注册 | Name、Skill ID、Tag、Streaming/Push 能力 | 返回缓冲通道；当前不应用 filter/context，也没有取消订阅 API |
| `RemoteDiscovery` | 预先添加的远端 URL 和 TTL 缓存 | 仅 Name 精确匹配或 `*` | 立即返回已关闭通道 |

`AgentFilter.Name` 不是通用 glob；除了特殊值 `*`，都是精确匹配。

```go
discovery := a2a.NewStaticDiscovery(card)
cards, err := discovery.Discover(ctx, &a2a.AgentFilter{
	Name:   "assistant",
	Skills: []string{"answer"},
})
if err != nil {
	return err
}
if len(cards) == 0 {
	return fmt.Errorf("no matching Agent")
}
```

`RegistryDiscovery` 在构造后监听 Registry 的后续事件，不会回放构造前已注册的条目；应先创建 discovery 再注册，或通过 discovery 显式注册 Card。

`RemoteDiscovery.Discover` 会跳过无法获取 Card 的 URL，仍可能返回 `nil` 错误；需要严格确认某个 Agent 时使用 `Get(ctx, url)` 并检查错误。该实现创建的内部 Client 不带认证选项，适用于公开 Agent Card。

## Hexagon 桥接

### 暴露本地 Agent

`WrapAgent` 把 `agent.Agent` 转换为普通 `TaskHandler`，`WrapStreamingAgent` 转换为 `StreamingTaskHandler`。`ExposeAgent` 创建并返回 `*Server`，**不会启动它**。

```go
func expose(localAgent agent.Agent) error {
	server := a2a.ExposeAgent(localAgent, "http://localhost:8080")
	if err := server.Start(":8080"); err != nil {
		return fmt.Errorf("start A2A server: %w", err)
	}
	return nil
}
```

`ExposeAgent` 声明流式能力并使用 Agent 的 `Stream` 方法。普通包装器会将 Agent 执行错误转成 `failed` Task update；流式包装器将内容块转成 Artifact，并在结束时返回合并后的完成消息。

### 调用远端 Agent

`NewRemoteAgent` 和 `ConnectToA2AAgent` 会在构造期间立即获取 Agent Card，因此都返回 `error`，不能忽略。构造阶段使用内部 background context，可用 `WithTimeout` 限制 HTTP 耗时。

```go
func invokeRemote(ctx context.Context, input agent.Input) (_ agent.Output, err error) {
	remote, err := a2a.ConnectToA2AAgent(
		"https://agent.example.com",
		a2a.WithTimeout(10*time.Second),
		a2a.WithAuth(&a2a.BearerAuth{Token: "secret"}),
	)
	if err != nil {
		return agent.Output{}, fmt.Errorf("connect to A2A Agent: %w", err)
	}
	defer func() {
		if closeErr := remote.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	return remote.Run(ctx, input)
}
```

尽管类型名是 `RemoteAgent`，当前类型只提供 `ID`、`Name`、`Run`、`Card`、`Close` 和 `NewSession`，**不满足完整的 `agent.Agent`/`core.Runnable` 接口**；不要直接传给 `Team` 或 `AgentNetwork.Register`。需要完整 Agent 时，显式写一个实现缺失方法的适配器，或直接使用 `Client`。

另外，`RemoteAgent.Run` 遇到非终态 Task 时会连续轮询 `GetTask`，当前没有轮询退避。对异步远端或长任务，优先直接使用 `Client`，实现带截止时间、间隔和限速的有界轮询。

### AgentNetwork 桥接

`NewNetworkBridge(network, taskStore)` 提供三个显式方法：

- `SendToAgent`：查找目标 Agent 并直接调用 `Run`，返回转换后的 A2A Message。
- `BroadcastMessage`：通过网络广播 Message 的纯文本内容。
- `SendToAgentNetwork`：通过网络队列向指定 Agent 发送纯文本内容。

```go
reply, err := bridge.SendToAgent(ctx, "researcher", &message)
if err != nil {
	return err
}
if reply.GetTextContent() == "" {
	return fmt.Errorf("Agent returned an empty response")
}
```

当前 `NetworkBridge` 的 `taskStore` 字段未被这些方法使用，不能据此假设有 Task 追踪或持久化；后两个网络方法也只转发 `GetTextContent()`，不会保留 File/Data Part。

## JSON-RPC 与端点

### HTTP 路径

| 路径 | 方法 | 用途 |
| --- | --- | --- |
| `/.well-known/agent-card.json` | GET | 获取 Agent Card |
| `/tasks` | POST | `Client` 的非流式 JSON-RPC 统一入口 |
| `/tasks/send` | POST | Server 注册的非流式别名 |
| `/tasks/sendSubscribe` | POST | `SendMessageStream` SSE |
| `/tasks/get` | POST | Server 注册的非流式别名 |
| `/tasks/cancel` | POST | Server 注册的非流式别名 |
| `/tasks/resubscribe` | POST | `Resubscribe` SSE |
| `/tasks/pushNotification/get` | POST | Server 注册的非流式别名 |
| `/tasks/pushNotification/set` | POST | Server 注册的非流式别名 |

Server 也为 send/get/cancel/push 注册了同名的分路径，但官方 `Client` 的非流式方法统一 POST 到 `/tasks`。

### JSON-RPC 方法

| 方法 | 结果 |
| --- | --- |
| `tasks/send` | 创建或继续 Task，返回 Task |
| `tasks/sendSubscribe` | 创建或继续 Task，返回 SSE |
| `tasks/get` | 获取 Task |
| `tasks/list` | 按 Session/状态分页列出 Task |
| `tasks/cancel` | 取消非终态 Task |
| `tasks/resubscribe` | 重新订阅已存在 Task |
| `tasks/pushNotification/set` | 设置 Task 推送配置 |
| `tasks/pushNotification/get` | 获取 Task 推送配置 |

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tasks/send",
  "params": {
    "message": {
      "role": "user",
      "parts": [{"type": "text", "text": "Hello"}]
    }
  }
}
```

### SSE 事件

事件名固定为 `task-status`、`artifact`、`error` 和 `done`。`task-status` 携带状态和 `final`，`artifact` 携带产物，`error` 携带 `*a2a.Error`，`done` 可携带最终 Task。

### 错误码

| 代码 | 含义 |
| --- | --- |
| `-32700` | Parse error |
| `-32600` | Invalid request |
| `-32601` | Method not found |
| `-32602` | Invalid params |
| `-32603` | Internal error |
| `-32001` | Task not found |
| `-32002` | Task not cancelable |
| `-32003` | Push notification not supported |
| `-32004` | Unsupported operation |
| `-32005` | Content type not supported |
| `-32010` | Authentication required |
| `-32011` | Authentication failed |
| `-32012` | Permission denied |

普通协议错误可能在 HTTP 200 响应的 JSON-RPC `error` 字段中返回；不能只检查 HTTP 状态码。官方 `Client` 会将该字段返回为 `*a2a.Error`，可用 `a2a.GetA2AError(err)` 获取 `Code`。

## 上线前检查

- Agent Card 的 URL、能力、技能、输入/输出模式和认证声明与运行时一致。
- 每个 Client/Discovery/Bridge 调用都有截止时间、取消和错误分类；所有 Stream 都被消费至关闭。
- 认证中间件实际包装了 Task/SSE 路由，Agent Card 的公开策略与 Client 行为一致。
- 生产任务使用持久化 `TaskStore`；依赖回调的业务使用可靠 outbox，不把内建 Webhook 当作自动重试队列。
- 服务关闭时检查 `Stop`/`Shutdown` 错误，关闭 Client 和已创建的异步推送服务。

## 相关文档

- [API 参考](../API.md)
- [多 Agent 编排](multi-agent.md)
- [`agent/a2a` 源码](../../agent/a2a/a2a.go)
