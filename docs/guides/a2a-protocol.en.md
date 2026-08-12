<div align="right">Language: <a href="a2a-protocol.md">中文</a> | English</div>

# A2A (Agent-to-Agent) Protocol Guide

Hexagon's `agent/a2a` package provides Agent Cards, Tasks, JSON-RPC, SSE, authentication, push delivery, and Agent bridges. This guide follows the current repository source; `ProtocolVersion` is currently `1.0`.

## Capability Boundaries

- Regular requests use JSON-RPC 2.0; streaming requests use Server-Sent Events (SSE).
- `Server` includes an in-memory Task store and optional push delivery, but it does not install authentication middleware automatically.
- `Client` supports synchronous Task APIs and SSE event streams. Calls made after closing it return `ErrClientClosed`.
- `AgentCard.Capabilities` is both a declaration and a server-side gate. Declare only capabilities that are actually configured.
- The default store, asynchronous push queue, and subscribers are in-process components, not persistence or reliable-messaging guarantees.

## Core Model

### Agent Card

An Agent Card is published at `GET /.well-known/agent-card.json`. Name, URL, and version are the essential fields. Capabilities, authentication schemes, and skills must match the running service.

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

`NewAgentCard` sets `Streaming` to `true` by default. Set it explicitly to `false` when the service does not offer SSE.

### Tasks and States

A Task is the durable identity of one interaction. It contains an `ID`, optional `SessionID`, current `Status`, `History`, `Artifacts`, and metadata.

A typical flow is:

```text
submitted → working → input-required → working
                    └→ completed | failed | canceled
```

`completed`, `failed`, and `canceled` are terminal states; use `TaskState.IsTerminal()` to test them. Supplying an existing ID in `SendMessageRequest.TaskID` continues that Task. Supplying an unknown ID returns a task-not-found error. `SessionID` correlates multiple Tasks and does not replace `TaskID`.

### Messages, Parts, and Artifacts

A Message has a `user` or `agent` role and is composed of `TextPart`, `FilePart`, or `DataPart` values. Artifacts use the same Part model for outputs.

```go
msg := a2a.NewUserMessage("Summarize this document")
msg.Parts = append(msg.Parts, &a2a.DataPart{
	Data: map[string]any{"language": "en"},
})

artifact := a2a.NewTextArtifact("summary", "Summary content")
artifact.LastChunk = true
```

`Message.GetTextContent()` and `Artifact.GetTextContent()` collect only text Parts. When the server receives an Artifact with `Append=true`, it appends its Parts to the last existing Artifact. Otherwise it creates a new Artifact and assigns its `Index` on the server.

## Server and Handler

### Minimal Server

The complete `TaskHandler` signature is:

```go
type TaskHandler interface {
	HandleTask(
		ctx context.Context,
		task *a2a.Task,
		msg *a2a.Message,
	) (*a2a.TaskUpdate, error)
}
```

Use `NewFuncHandler` to wrap a function. `Server.Start` blocks and returns an `error`; never discard it.

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

For signal handling, custom timeouts, or middleware, use `server.Handler()` as the Handler of an externally managed `http.Server`, and check errors from both `ListenAndServe` and `Shutdown`. When using `Start` directly, graceful shutdown is available through `server.Stop(ctx)`; its error must also be handled.

`NewServer` uses `MemoryTaskStore` by default. Inject a production `TaskStore` with `WithStore` when Tasks must survive process restarts. If push configuration is enabled, a custom store should also implement `PushConfigStore`.

### Results and Errors

A `TaskUpdate` may contain `Status`, `Message`, `Artifact`, `Metadata`, and `Final`. Common constructors are:

- `NewStatusUpdate`
- `NewMessageUpdate`
- `NewArtifactUpdate`
- `NewCompletedUpdate`
- `NewFailedUpdate`
- `NewInputRequiredUpdate`

When regular `HandleTask` returns a non-nil `error`, the server records the Task as `failed` and returns a JSON-RPC or SSE error. When `HandleTaskStream` returns an error while opening the stream, the current implementation writes an SSE `error` event but does not automatically persist a `failed` terminal state. To expose a business failure as a queryable terminal Task, return `NewFailedUpdate(message), nil` from a regular Handler or send that update on the streaming channel.

### Streaming Handler

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

`NewStreamingFuncHandler` requires both the regular and streaming functions:

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

Before closing its channel, a streaming Handler should send a terminal update with `Final=true`. If it closes immediately, the Server still emits `done`, but the final Task may remain `working`.

The server accepts streaming endpoints only when `AgentCard.Capabilities.Streaming=true`. If the Card declares streaming but the Handler implements only `TaskHandler`, the server calls `HandleTask` synchronously and ends the SSE response with the final Task status and a `done` event. It does not produce genuine intermediate chunks.

## Client

### Regular Requests

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

To continue the same Task, call `SendMessage` again with `TaskID: task.ID`. `GetTask`, `ListTasks`, `CancelTask`, `SetPushNotification`, and `GetPushNotification` all return errors that must be handled. Pass every network call a deadline-bound or cancelable `context.Context`. The client preserves error chains, so `errors.Is(err, context.DeadlineExceeded)` can identify a timeout.

### Consuming SSE

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

Parsing or transport errors that occur after the HTTP response is established arrive as `*ErrorEvent`; they are not returned later by `SendMessageStream`. Consume the channel until it closes and handle `ErrorEvent`. `Resubscribe(ctx, taskID)` uses the same event model.

## Authentication and Authorization

### Server Side

The server package provides `BearerTokenValidator`, `APIKeyValidator`, `BasicAuthValidator`, `ChainValidator`, `AuthMiddleware`, and `OptionalAuthMiddleware`. `NewServer` and `Start` do not enable them automatically. Wrap `server.Handler()` in an externally managed `http.Server`.

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

This example deliberately leaves the Agent Card public. The current `Client.GetAgentCard` does not apply `WithAuth`, while JSON-RPC and SSE calls do. Protecting the Card makes it undiscoverable through the standard `Client`, `NewRemoteAgent`, and `ConnectToA2AAgent`.

For RBAC, first use `AuthMiddleware(rbac)` to populate `AuthContext`, then apply `rbac.RequirePermission(...)` inside it. Authentication failures return HTTP 401 and permission failures return HTTP 403; the body remains a JSON-RPC error object.

### Client Side

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

The client also provides `APIKeyAuth` and `BasicAuth`. Do not put credentials in an Agent Card, logs, or the repository. Use TLS and restrict server-side CORS origins in production.

## Push Notifications

### Configuration

Push configuration APIs are available only when the Card declares `PushNotifications=true`; actual delivery also requires the Server to receive a `PushService` through `WithPushService`. `NewDefaultPushService` now returns `(*AsyncPushService, error)`: check the error and call `Close` during shutdown.

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

A new Task can carry its push configuration directly:

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

Configure an existing Task with `client.SetPushNotification(ctx, task.ID, config)` and verify it with `GetPushNotification`. Handle errors from both calls.

### Immutable Notification Contract

`PushNotification` fields are private and notifications can be created only through constructors. Construction serializes a snapshot. Mutating the source Task or Artifact later, or mutating a copy returned by an accessor, cannot change the notification.

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

Use `NewArtifactNotification(taskID, artifact)` for Artifact notifications and read a copy with `Artifact()`; check errors from both construction and access. `TaskID()`, `EventType()`, and `Timestamp()` expose notification metadata. `NewWebhookPushService` and `NewAsyncPushService` also return configuration-validation errors.

### Delivery Guarantees

The current implementation is best-effort. It does not promise automatic Webhook retries or reliable delivery:

- Each `WebhookPushService.Push` call performs one HTTP POST. `WithWebhookRetry` currently validates and stores configuration only; it is not an executed automatic-retry guarantee.
- A successful `AsyncPushService.Push` means only that the request was enqueued. A full queue returns `ErrPushQueueFull`; a closed service returns `ErrPushServiceClosed`. Workers do not propagate underlying delivery failures to callers.
- `Server` triggers an asynchronous Task-status push after regular `tasks/send`, without waiting for or exposing the delivery error. Built-in streaming updates do not automatically emit Artifact Webhooks.
- `Close` stops workers and releases the remaining queue; it does not promise to flush every pending message.

For at-least-once delivery, implement a custom `PushService` and persistent outbox with bounded retries, idempotency keys, observable failures, and dead-letter handling. `PushManager` can provide per-Task configuration and rate limiting; its `NewPushManager` constructor also returns an `error`.

## Agent Discovery

`Discovery` defines `Discover`, `Register`, `Deregister`, `Get`, and `Watch`. Every method returns an error that callers must handle.

| Implementation | Data source | Actual filtering | `Watch` semantics |
| --- | --- | --- | --- |
| `StaticDiscovery` | In-process Card map | Exact Name or `*`, plus Skill IDs | Immediately returns a closed channel |
| `RegistryDiscovery` | `agent.Registry` events and manual registration | Name, Skill IDs, Tags, Streaming/Push capabilities | Returns a buffered channel; currently ignores filter/context and has no unsubscribe API |
| `RemoteDiscovery` | Preconfigured remote URLs and a TTL cache | Exact Name or `*` only | Immediately returns a closed channel |

`AgentFilter.Name` is not a general glob. Every value except the special `*` is an exact match.

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

`RegistryDiscovery` watches Registry events that occur after construction; it does not replay entries registered before construction. Create the discovery instance before registration, or register Cards explicitly through the discovery instance.

`RemoteDiscovery.Discover` skips URLs whose Cards cannot be fetched and may still return a nil error. Use `Get(ctx, url)` and check its error when one specific Agent must be available. Its internal Clients have no authentication options, so it is suitable for public Agent Cards.

## Hexagon Bridges

### Exposing a Local Agent

`WrapAgent` converts an `agent.Agent` into a regular `TaskHandler`; `WrapStreamingAgent` converts it into a `StreamingTaskHandler`. `ExposeAgent` creates and returns a `*Server`; **it does not start it**.

```go
func expose(localAgent agent.Agent) error {
	server := a2a.ExposeAgent(localAgent, "http://localhost:8080")
	if err := server.Start(":8080"); err != nil {
		return fmt.Errorf("start A2A server: %w", err)
	}
	return nil
}
```

`ExposeAgent` declares streaming support and uses the Agent's `Stream` method. The regular wrapper turns an Agent execution error into a failed Task update. The streaming wrapper turns content chunks into Artifacts and emits one merged completion Message at the end.

### Calling a Remote Agent

`NewRemoteAgent` and `ConnectToA2AAgent` fetch the Agent Card immediately during construction, so both return an `error` that must be handled. Construction uses an internal background context; use `WithTimeout` to bound HTTP duration.

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

Despite its name, the current `RemoteAgent` exposes only `ID`, `Name`, `Run`, `Card`, `Close`, and `NewSession`. It **does not satisfy the complete `agent.Agent`/`core.Runnable` interface**. Do not pass it directly to `Team` or `AgentNetwork.Register`. Write an explicit adapter for the missing methods, or use `Client` directly.

In addition, `RemoteAgent.Run` repeatedly polls `GetTask` without backoff when a Task is non-terminal. For asynchronous remote services or long-running Tasks, prefer direct `Client` use with deadline-bound, interval-controlled, rate-limited polling.

### AgentNetwork Bridge

`NewNetworkBridge(network, taskStore)` exposes three explicit methods:

- `SendToAgent` finds the target Agent, calls `Run` directly, and returns the converted A2A Message.
- `BroadcastMessage` broadcasts the Message's text content through the network.
- `SendToAgentNetwork` sends the text content to a named Agent through the network queue.

```go
reply, err := bridge.SendToAgent(ctx, "researcher", &message)
if err != nil {
	return err
}
if reply.GetTextContent() == "" {
	return fmt.Errorf("Agent returned an empty response")
}
```

The current `NetworkBridge` methods do not use its `taskStore` field, so it does not imply Task tracking or persistence. The two network-routing methods forward only `GetTextContent()` and do not preserve File or Data Parts.

## JSON-RPC and Endpoints

### HTTP Paths

| Path | Method | Purpose |
| --- | --- | --- |
| `/.well-known/agent-card.json` | GET | Fetch the Agent Card |
| `/tasks` | POST | Unified endpoint for the `Client`'s non-streaming JSON-RPC calls |
| `/tasks/send` | POST | Server-registered non-streaming alias |
| `/tasks/sendSubscribe` | POST | `SendMessageStream` SSE |
| `/tasks/get` | POST | Server-registered non-streaming alias |
| `/tasks/cancel` | POST | Server-registered non-streaming alias |
| `/tasks/resubscribe` | POST | `Resubscribe` SSE |
| `/tasks/pushNotification/get` | POST | Server-registered non-streaming alias |
| `/tasks/pushNotification/set` | POST | Server-registered non-streaming alias |

The Server also registers matching split paths for send/get/cancel/push operations, but the official `Client` posts every non-streaming method to `/tasks`.

### JSON-RPC Methods

| Method | Result |
| --- | --- |
| `tasks/send` | Create or continue a Task; return the Task |
| `tasks/sendSubscribe` | Create or continue a Task; return SSE |
| `tasks/get` | Get a Task |
| `tasks/list` | List Tasks by Session/state with pagination |
| `tasks/cancel` | Cancel a non-terminal Task |
| `tasks/resubscribe` | Resubscribe to an existing Task |
| `tasks/pushNotification/set` | Set a Task's push configuration |
| `tasks/pushNotification/get` | Get a Task's push configuration |

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

### SSE Events

Event names are fixed as `task-status`, `artifact`, `error`, and `done`. `task-status` carries a status and `final`; `artifact` carries an Artifact; `error` carries an `*a2a.Error`; and `done` may carry the final Task.

### Error Codes

| Code | Meaning |
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

A regular protocol error may appear in the JSON-RPC `error` field of an HTTP 200 response; do not inspect only the HTTP status. The official `Client` returns that field as `*a2a.Error`; use `a2a.GetA2AError(err)` to read its `Code`.

## Pre-production Checklist

- The Agent Card's URL, capabilities, skills, input/output modes, and authentication declaration match runtime behavior.
- Every Client, Discovery, and Bridge call has a deadline, cancellation, and error classification; every Stream is consumed until closed.
- Authentication middleware actually wraps Task and SSE routes, and the Agent Card publication policy matches current Client behavior.
- Production Tasks use a persistent `TaskStore`; callback-dependent workflows use a reliable outbox and do not treat the built-in Webhook as an automatic retry queue.
- Shutdown checks `Stop`/`Shutdown` errors and closes Clients and any asynchronous push service that was created.

## Related Documentation

- [API reference](../API.en.md)
- [Multi-agent orchestration](multi-agent.en.md)
- [`agent/a2a` source](../../agent/a2a/a2a.go)
