package a2a

import (
	"bytes"
	"context"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	official "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// 独立官方客户端通过真实 HTTP 访问 Hexagon 服务，验证 A2A 1.0 的内容、任务和失败语义。
func TestA2AOfficialClientToHexagon(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	store := NewMemoryTaskStore(WithTaskTTL(0))
	defer store.Close()
	card := NewAgentCard("interop", "", "1.0.0")
	handler := NewFuncHandler(func(ctx context.Context, task *Task, msg *Message) (*TaskUpdate, error) {
		if len(msg.Parts) != 4 || msg.ID == "" || msg.ContextID == "" {
			return nil, errors.New("message metadata or parts lost")
		}
		file, ok := msg.Parts[1].(*FilePart)
		if !ok || file.File.Bytes != "AAECAw==" || file.Metadata["source"] != "fixture" {
			return nil, errors.New("file payload lost")
		}
		artifact := &Artifact{ID: "mixed-artifact", Name: "report", Parts: msg.Parts, Metadata: map[string]any{"complete": false}}
		return &TaskUpdate{Status: &TaskStatus{State: TaskStateFailed, Timestamp: time.Now()}, Artifact: artifact, Message: ptrMessage(NewAgentMessage("partial report retained")), Final: true}, errors.New("render failed after partial output")
	})
	server := NewServer(card, handler, WithStore(store))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	card.URL = httpServer.URL
	resolved, err := agentcard.DefaultResolver.Resolve(ctx, httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.SupportedInterfaces) != 1 || resolved.SupportedInterfaces[0].ProtocolBinding != official.TransportProtocolJSONRPC || resolved.SupportedInterfaces[0].ProtocolVersion != "1.0" || resolved.Capabilities.PushNotifications {
		t.Fatalf("invalid capability declaration: %+v", resolved)
	}
	client := a2aclient.NewJSONRPCTransport(resolved.SupportedInterfaces[0].URL, httpServer.Client())
	raw := official.NewRawPart([]byte{0, 1, 2, 3})
	raw.Filename = "image.bin"
	raw.MediaType = "application/octet-stream"
	raw.Metadata = map[string]any{"source": "fixture"}
	url := official.NewFileURLPart("https://example.invalid/asset.png", "image/png")
	message := official.NewMessage(official.MessageRoleUser, official.NewTextPart("analyze"), raw, url, official.NewDataPart([]any{"a", float64(2)}))
	message.Extensions = []string{"urn:test:parts"}
	result, err := client.SendMessage(ctx, nil, &official.SendMessageRequest{Message: message})
	if err != nil {
		t.Fatal(err)
	}
	task, ok := result.(*official.Task)
	if !ok || task.Status.State != official.TaskStateFailed || task.ContextID == "" || len(task.Artifacts) != 1 {
		t.Fatalf("failed task result lost: %#v", result)
	}
	parts := task.Artifacts[0].Parts
	if len(parts) != 4 || !bytes.Equal(parts[1].Raw(), []byte{0, 1, 2, 3}) || parts[1].Metadata["source"] != "fixture" || parts[2].URL() != "https://example.invalid/asset.png" || parts[3].Data() == nil {
		t.Fatalf("artifact content lost: %+v", parts)
	}
	zero := 0
	projected, err := client.GetTask(ctx, nil, &official.GetTaskRequest{ID: task.ID, HistoryLength: &zero})
	if err != nil || len(projected.History) != 0 {
		t.Fatalf("zero history: %+v %v", projected, err)
	}
	full, err := client.GetTask(ctx, nil, &official.GetTaskRequest{ID: task.ID})
	if err != nil || len(full.History) != 2 || full.History[0].ID != message.ID || len(full.History[0].Extensions) != 1 {
		t.Fatalf("persisted history lost: %+v %v", full, err)
	}
	listed, err := client.ListTasks(ctx, nil, &official.ListTasksRequest{ContextID: task.ContextID, IncludeArtifacts: true, PageSize: 1})
	if err != nil || listed.TotalSize != 1 || len(listed.Tasks) != 1 || len(listed.Tasks[0].Artifacts) != 1 {
		t.Fatalf("list tasks: %+v %v", listed, err)
	}
	_, err = client.CancelTask(ctx, nil, &official.CancelTaskRequest{ID: task.ID})
	if !errors.Is(err, official.ErrTaskNotCancelable) {
		t.Fatalf("terminal cancel error mapping: %v", err)
	}
	_, err = client.GetTask(ctx, nil, &official.GetTaskRequest{ID: "missing"})
	if !errors.Is(err, official.ErrTaskNotFound) {
		t.Fatalf("not found mapping: %v", err)
	}
}

func ptrMessage(message Message) *Message { return &message }

// 独立官方服务端负责协议编解码及任务执行；Hexagon 客户端同时消费消息、任务和 SSE。
func TestA2AHexagonClientToOfficialServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	executor := a2asrv.AgentExecutorFunc(func(ctx context.Context, execution *a2asrv.ExecutorContext) iter.Seq2[official.Event, error] {
		return func(yield func(official.Event, error) bool) {
			if execution.Message.Parts[0].Text() == "message" {
				message := official.NewMessage(official.MessageRoleAgent, official.NewTextPart("standalone"), official.NewRawPart([]byte{7, 8, 9}))
				message.Parts[1].Filename = "attachment.bin"
				message.Metadata = map[string]any{"retained": true}
				yield(message, nil)
				return
			}
			task := official.NewSubmittedTask(execution, execution.Message)
			if !yield(task, nil) {
				return
			}
			artifact := official.NewArtifactEvent(task, official.NewRawPart([]byte{4, 5, 6}), official.NewDataPart(map[string]any{"result": "ready"}))
			artifact.Artifact.ID = "official-artifact"
			artifact.Artifact.Parts[0].Filename = "output.bin"
			artifact.LastChunk = true
			if !yield(artifact, nil) {
				return
			}
			state := official.TaskStateCompleted
			if execution.Message.Parts[0].Text() == "wait" {
				state = official.TaskStateInputRequired
			}
			yield(official.NewStatusUpdateEvent(task, state, official.NewMessage(official.MessageRoleAgent, official.NewTextPart("done"))), nil)
		}
	})
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))
	mux.Handle(PathAgentCard, a2asrv.NewStaticAgentCardHandler(&official.AgentCard{Name: "official", Version: "v2.7.0", SupportedInterfaces: []*official.AgentInterface{official.NewAgentInterface(server.URL+"/invoke", official.TransportProtocolJSONRPC)}, Capabilities: official.AgentCapabilities{Streaming: true}}))
	client := NewClient(server.URL, WithHTTPClient(server.Client()))
	if _, err := client.GetAgentCard(ctx); err != nil {
		t.Fatal(err)
	}
	message, err := client.Send(ctx, &SendMessageRequest{Message: NewUserMessage("message")})
	if err != nil || message.Message == nil || message.Task != nil || message.Message.Metadata["retained"] != true || len(message.Message.Parts) != 2 {
		t.Fatalf("message-only result: %+v %v", message, err)
	}
	file := message.Message.Parts[1].(*FilePart)
	if file.File.Bytes != "BwgJ" || file.File.Name != "attachment.bin" {
		t.Fatalf("message attachment: %+v", file)
	}
	task, err := client.SendMessage(ctx, &SendMessageRequest{Message: NewUserMessage("wait")})
	if err != nil || task.Status.State != TaskStateInputRequired || len(task.Artifacts) != 1 {
		t.Fatalf("task result: %+v %v", task, err)
	}
	got, err := client.GetTask(ctx, task.ID)
	if err != nil || got.SessionID == "" || got.Artifacts[0].ID != "official-artifact" {
		t.Fatalf("get task: %+v %v", got, err)
	}
	canceled, err := client.CancelTask(ctx, task.ID)
	if err != nil || canceled.Status.State != TaskStateCanceled {
		t.Fatalf("cancel: %+v %v", canceled, err)
	}
	_, err = client.GetTask(ctx, "missing")
	var rpcError *Error
	if !errors.As(err, &rpcError) || rpcError.Code != CodeTaskNotFound {
		t.Fatalf("error details: %v", err)
	}
	events, err := client.SendMessageStream(ctx, &SendMessageRequest{Message: NewUserMessage("stream")})
	if err != nil {
		t.Fatal(err)
	}
	var artifactSeen, terminalSeen bool
	for event := range events {
		switch e := event.(type) {
		case *ArtifactEvent:
			artifactSeen = e.Artifact.ID == "official-artifact" && e.Artifact.Parts[0].(*FilePart).File.Bytes == "BAUG" && e.Artifact.LastChunk
		case *TaskStatusEvent:
			terminalSeen = e.Status.State == TaskStateCompleted
		case *ErrorEvent:
			t.Fatalf("stream: %v", e.Error)
		}
	}
	if !artifactSeen || !terminalSeen {
		t.Fatalf("stream lost content or terminal event: artifact=%v terminal=%v", artifactSeen, terminalSeen)
	}
}

type cancellableInteropHandler struct{ stopped chan struct{} }

func (h *cancellableInteropHandler) HandleTask(ctx context.Context, task *Task, msg *Message) (*TaskUpdate, error) {
	return nil, errors.New("streaming required")
}
func (h *cancellableInteropHandler) HandleTaskStream(ctx context.Context, task *Task, msg *Message) (<-chan *TaskUpdate, error) {
	updates := make(chan *TaskUpdate)
	go func() { defer close(updates); <-ctx.Done(); close(h.stopped) }()
	return updates, nil
}

// 取消经过独立 SDK 的 JSONRPC 请求传播到实际执行上下文，订阅收到同一终态。
func TestA2AOfficialStreamingCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	store := NewMemoryTaskStore(WithTaskTTL(0))
	defer store.Close()
	handler := &cancellableInteropHandler{stopped: make(chan struct{})}
	server := httptest.NewServer(NewServer(NewAgentCard("cancel", "", "1"), handler, WithStore(store)).Handler())
	defer server.Close()
	client := a2aclient.NewJSONRPCTransport(server.URL+PathTasks, server.Client())
	var taskID official.TaskID
	var canceled bool
	for event, err := range client.SendStreamingMessage(ctx, nil, &official.SendMessageRequest{Message: official.NewMessage(official.MessageRoleUser, official.NewTextPart("wait"))}) {
		if err != nil {
			t.Fatal(err)
		}
		switch event := event.(type) {
		case *official.Task:
			taskID = event.ID
			for subscribed, err := range client.SubscribeToTask(ctx, nil, &official.SubscribeToTaskRequest{ID: taskID}) {
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := subscribed.(*official.Task); ok {
					if _, err := client.CancelTask(ctx, nil, &official.CancelTaskRequest{ID: taskID}); err != nil {
						t.Fatal(err)
					}
				}
				if status, ok := subscribed.(*official.TaskStatusUpdateEvent); ok && status.Status.State == official.TaskStateCanceled {
					break
				}
			}
		case *official.TaskStatusUpdateEvent:
			canceled = event.Status.State == official.TaskStateCanceled
		}
	}
	if taskID == "" || !canceled {
		t.Fatal("cancel state missing from stream")
	}
	select {
	case <-handler.stopped:
	case <-ctx.Done():
		t.Fatal("execution did not observe cancellation")
	}
	stored, err := client.GetTask(ctx, nil, &official.GetTaskRequest{ID: taskID})
	if err != nil || stored.Status.State != official.TaskStateCanceled {
		t.Fatalf("cancel state not persisted: %+v %v", stored, err)
	}
}
