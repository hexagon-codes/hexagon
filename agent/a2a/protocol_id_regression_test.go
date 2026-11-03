package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type contextCaptureStore struct {
	*MemoryTaskStore
	mu   sync.Mutex
	last context.Context
}

func (s *contextCaptureStore) Get(ctx context.Context, id string) (*Task, error) {
	s.mu.Lock()
	s.last = ctx
	s.mu.Unlock()
	return s.MemoryTaskStore.Get(ctx, id)
}

func TestDataPartPreservesLargeInteger(t *testing.T) {
	part, err := UnmarshalPart([]byte(`{"data":{"n":9007199254740993}}`))
	if err != nil {
		t.Fatal(err)
	}
	dataPart, ok := part.(*DataPart)
	if !ok {
		t.Fatalf("part type %T", part)
	}
	data, ok := dataPart.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type %T", dataPart.Data)
	}
	if fmt.Sprint(data["n"]) != "9007199254740993" {
		t.Fatalf("large integer changed: %v", data["n"])
	}
}

func TestArtifactDataPartPreservesLargeInteger(t *testing.T) {
	var artifact Artifact
	if err := json.Unmarshal([]byte(`{"artifactId":"a","parts":[{"data":{"n":9007199254740993}}]}`), &artifact); err != nil {
		t.Fatal(err)
	}
	if len(artifact.Parts) != 1 {
		t.Fatalf("parts=%d", len(artifact.Parts))
	}
	dataPart, ok := artifact.Parts[0].(*DataPart)
	if !ok {
		t.Fatalf("part type %T", artifact.Parts[0])
	}
	data := dataPart.Data.(map[string]any)
	if fmt.Sprint(data["n"]) != "9007199254740993" {
		t.Fatalf("large integer changed: %v", data["n"])
	}
}

func TestPreciseStreamPartsDirect(t *testing.T) {
	event := &TaskEvent{Task: &Task{Artifacts: []Artifact{{Parts: []Part{&DataPart{Data: map[string]any{"n": float64(9007199254740992)}}}}}}}
	preciseStreamParts(json.RawMessage(`{"task":{"artifacts":[{"parts":[{"data":{"n":9007199254740993}}]}]}}`), event)
	dataPart := event.Task.Artifacts[0].Parts[0].(*DataPart)
	data := dataPart.Data.(map[string]any)
	if fmt.Sprint(data["n"]) != "9007199254740993" {
		t.Fatalf("direct override failed: %v", data["n"])
	}
}

func assertDataPartNumber(t *testing.T, parts []Part, want string) {
	t.Helper()
	if len(parts) != 1 {
		t.Fatalf("parts=%d", len(parts))
	}
	dataPart, ok := parts[0].(*DataPart)
	if !ok {
		t.Fatalf("part type %T", parts[0])
	}
	data, ok := dataPart.Data.(map[string]any)
	if !ok {
		t.Fatalf("data type %T", dataPart.Data)
	}
	if fmt.Sprint(data["n"]) != want {
		t.Fatalf("large integer changed: %v", data["n"])
	}
}

func TestProtocolEventMessagePreservesLargeInteger(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"result":{"message":{"messageId":"m","role":"ROLE_AGENT","parts":[{"data":{"n":9007199254740993}}]}}}`
	event := decodeProtocolEvent(payload)
	messageEvent, ok := event.(*MessageEvent)
	if !ok || messageEvent.Message == nil {
		t.Fatalf("unexpected event: %#v", event)
	}
	assertDataPartNumber(t, messageEvent.Message.Parts, "9007199254740993")
}

func TestProtocolEventArtifactPreservesLargeInteger(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"result":{"artifactUpdate":{"taskId":"t","artifact":{"artifactId":"a","parts":[{"data":{"n":9007199254740993}}]}}}}`
	event := decodeProtocolEvent(payload)
	artifactEvent, ok := event.(*ArtifactEvent)
	if !ok {
		t.Fatalf("unexpected event: %#v", event)
	}
	assertDataPartNumber(t, artifactEvent.Artifact.Parts, "9007199254740993")
}

func TestProtocolEventStatusMessagePreservesLargeInteger(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"result":{"statusUpdate":{"taskId":"t","status":{"state":"completed","message":{"messageId":"m","role":"ROLE_AGENT","parts":[{"data":{"n":9007199254740993}}]}}}}}`
	event := decodeProtocolEvent(payload)
	statusEvent, ok := event.(*TaskStatusEvent)
	if !ok || statusEvent.Status.Message == nil {
		t.Fatalf("unexpected event: %#v", event)
	}
	assertDataPartNumber(t, statusEvent.Status.Message.Parts, "9007199254740993")
}

func TestProtocolEventTaskStatusMessagePreservesLargeInteger(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"result":{"task":{"id":"t","status":{"state":"working","message":{"messageId":"m","role":"ROLE_AGENT","parts":[{"data":{"n":9007199254740993}}]}}}}}`
	event := decodeProtocolEvent(payload)
	taskEvent, ok := event.(*TaskEvent)
	if !ok || taskEvent.Task == nil || taskEvent.Task.Status.Message == nil {
		t.Fatalf("unexpected event: %#v", event)
	}
	assertDataPartNumber(t, taskEvent.Task.Status.Message.Parts, "9007199254740993")
}

func assertJSONNumber(t *testing.T, value any, want string) {
	t.Helper()
	if got := fmt.Sprint(value); got != want {
		t.Fatalf("number = %v (%T), want %s", value, value, want)
	}
}

func TestProtocolEventMetadataPreservesLargeInteger(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"result":{"task":{"id":"t","metadata":{"n":9007199254740993},"status":{"state":"working","message":{"messageId":"m","role":"ROLE_AGENT","metadata":{"n":9007199254740993},"parts":[{"data":{"n":9007199254740993},"metadata":{"n":9007199254740993}}]}},"history":[{"messageId":"h","role":"ROLE_USER","metadata":{"n":9007199254740993},"parts":[{"text":"x"}]}],"artifacts":[{"artifactId":"a","metadata":{"n":9007199254740993},"parts":[{"text":"x","metadata":{"n":9007199254740993}}]}]}}}`
	event := decodeProtocolEvent(payload)
	taskEvent, ok := event.(*TaskEvent)
	if !ok || taskEvent.Task == nil {
		t.Fatalf("unexpected event: %#v", event)
	}
	assertJSONNumber(t, taskEvent.Task.Metadata["n"], "9007199254740993")
	if taskEvent.Task.Status.Message == nil {
		t.Fatal("status message missing")
	}
	assertJSONNumber(t, taskEvent.Task.Status.Message.Metadata["n"], "9007199254740993")
	assertJSONNumber(t, taskEvent.Task.Status.Message.Parts[0].(*DataPart).Metadata["n"], "9007199254740993")
	if len(taskEvent.Task.History) != 1 || len(taskEvent.Task.Artifacts) != 1 {
		t.Fatalf("history=%d artifacts=%d", len(taskEvent.Task.History), len(taskEvent.Task.Artifacts))
	}
	assertJSONNumber(t, taskEvent.Task.History[0].Metadata["n"], "9007199254740993")
	assertJSONNumber(t, taskEvent.Task.Artifacts[0].Metadata["n"], "9007199254740993")
	assertJSONNumber(t, taskEvent.Task.Artifacts[0].Parts[0].(*TextPart).Metadata["n"], "9007199254740993")
}

func TestProtocolEventStatusAndArtifactMetadataPreservesLargeInteger(t *testing.T) {
	statusPayload := `{"jsonrpc":"2.0","id":1,"result":{"statusUpdate":{"taskId":"t","metadata":{"n":9007199254740993},"status":{"state":"working","message":{"messageId":"m","role":"ROLE_AGENT","metadata":{"n":9007199254740993},"parts":[{"data":{"n":9007199254740993}}]}}}}}`
	statusEvent, ok := decodeProtocolEvent(statusPayload).(*TaskStatusEvent)
	if !ok || statusEvent.Status.Message == nil {
		t.Fatalf("unexpected status event")
	}
	assertJSONNumber(t, statusEvent.Metadata["n"], "9007199254740993")
	assertJSONNumber(t, statusEvent.Status.Message.Metadata["n"], "9007199254740993")
	assertDataPartNumber(t, statusEvent.Status.Message.Parts, "9007199254740993")

	artifactPayload := `{"jsonrpc":"2.0","id":1,"result":{"artifactUpdate":{"taskId":"t","metadata":{"n":9007199254740993},"artifact":{"artifactId":"a","metadata":{"n":9007199254740993},"parts":[{"text":"x","metadata":{"n":9007199254740993}}]}}}}`
	artifactEvent, ok := decodeProtocolEvent(artifactPayload).(*ArtifactEvent)
	if !ok {
		t.Fatalf("unexpected artifact event")
	}
	assertJSONNumber(t, artifactEvent.Metadata["n"], "9007199254740993")
	assertJSONNumber(t, artifactEvent.Artifact.Metadata["n"], "9007199254740993")
	assertJSONNumber(t, artifactEvent.Artifact.Parts[0].(*TextPart).Metadata["n"], "9007199254740993")
}

func TestProtocolEventNullPartAlignmentPreservesLargeInteger(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"result":{"message":{"messageId":"m","role":"ROLE_AGENT","parts":[null,{"data":{"n":9007199254740993}}]}}}`
	messageEvent, ok := decodeProtocolEvent(payload).(*MessageEvent)
	if !ok || messageEvent.Message == nil {
		t.Fatalf("unexpected event")
	}
	if len(messageEvent.Message.Parts) != 1 {
		t.Fatalf("parts=%d", len(messageEvent.Message.Parts))
	}
	assertDataPartNumber(t, messageEvent.Message.Parts, "9007199254740993")
}

func TestStreamResponseRejectsStringVsNumberID(t *testing.T) {
	body := `data: {"jsonrpc":"2.0","id":1,"result":{"message":{"messageId":"m","role":"ROLE_AGENT","parts":[{"text":"x"}]}}}

`
	events := make(chan StreamEvent, 4)
	client := &Client{}
	client.readSSEEvents(context.Background(), io.NopCloser(strings.NewReader(body)), events, "1")
	first := <-events
	if _, ok := first.(*ErrorEvent); !ok {
		t.Fatalf("expected correlation error for string request ID, got %#v", first)
	}
}

func TestStreamResponseAcceptsNumericID(t *testing.T) {
	body := `data: {"jsonrpc":"2.0","id":1,"result":{"message":{"messageId":"m","role":"ROLE_AGENT","parts":[{"text":"x"}]}}}

`
	events := make(chan StreamEvent, 4)
	client := &Client{}
	client.readSSEEvents(context.Background(), io.NopCloser(strings.NewReader(body)), events, int64(1))
	first := <-events
	if _, ok := first.(*MessageEvent); !ok {
		t.Fatalf("expected message for matching numeric ID, got %#v", first)
	}
}

func TestProtocolEventDataPartPreservesLargeInteger(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"result":{"task":{"id":"task","status":{"state":"completed"},"artifacts":[{"artifactId":"a","parts":[{"data":{"n":9007199254740993}}]}]}}}`
	event := decodeProtocolEvent(payload)
	taskEvent, ok := event.(*TaskEvent)
	if !ok || taskEvent.Task == nil || len(taskEvent.Task.Artifacts) != 1 {
		t.Fatalf("unexpected event: %#v", event)
	}
	dataPart, ok := taskEvent.Task.Artifacts[0].Parts[0].(*DataPart)
	if !ok {
		t.Fatalf("part type %T", taskEvent.Task.Artifacts[0].Parts[0])
	}
	data := dataPart.Data.(map[string]any)
	if fmt.Sprint(data["n"]) != "9007199254740993" {
		t.Fatalf("large integer changed: %v", data["n"])
	}
}

func TestProtocolHTTPRejectsTrailingJSON(t *testing.T) {
	server := NewServer(NewAgentCard("review", "", "1.0.0"), NewEchoHandler())
	host := httptest.NewServer(server.Handler())
	defer host.Close()
	defer server.Stop(context.Background())
	body := `{"jsonrpc":"2.0","id":1,"method":"GetTask","params":{"id":"missing"}} {}`
	response, err := host.Client().Post(host.URL+PathTasks, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Error *Error `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error == nil || envelope.Error.Code != -32700 {
		t.Fatalf("trailing JSON was not a parse error: %s", data)
	}
}

func TestProtocolEventUsesRequestContext(t *testing.T) {
	base := NewMemoryTaskStore()
	defer base.Close()
	store := &contextCaptureStore{MemoryTaskStore: base}
	server := &Server{store: store}
	key := struct{}{}
	ctx := context.WithValue(context.Background(), key, "request-context")
	_, _ = server.protocolEvent(ctx, &TaskStatusEvent{ID: "missing", Status: TaskStatus{State: TaskStateWorking}})
	store.mu.Lock()
	observed := store.last
	store.mu.Unlock()
	if observed == nil || observed.Value(key) != "request-context" {
		t.Fatalf("store.Get did not receive the request context: %v", observed)
	}
}

func TestProtocolHTTPPreservesLargeIntegerID(t *testing.T) {
	server := NewServer(NewAgentCard("review", "", "1.0.0"), NewEchoHandler())
	host := httptest.NewServer(server.Handler())
	defer host.Close()
	defer server.Stop(context.Background())
	body := `{"jsonrpc":"2.0","id":9007199254740993,"method":"GetTask","params":{"id":"missing"}}`
	response, err := host.Client().Post(host.URL+PathTasks, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if string(envelope.ID) != "9007199254740993" {
		t.Fatalf("response ID=%s body=%s", envelope.ID, data)
	}
}
