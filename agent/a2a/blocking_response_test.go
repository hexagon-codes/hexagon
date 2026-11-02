package a2a

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestA2ABlockingSendMessageRequiresFinalState(t *testing.T) {
	for _, test := range []struct {
		name    string
		state   TaskState
		invalid bool
	}{
		{"final-only", "", true},
		{"working", TaskStateWorking, true},
		{"completed", TaskStateCompleted, false},
		{"failed", TaskStateFailed, false},
		{"canceled", TaskStateCanceled, false},
		{"input-required", TaskStateInputRequired, false},
		{"auth-required", TaskStateAuthRequired, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			store := NewMemoryTaskStore(WithTaskTTL(0))
			defer store.Close()
			task := NewTask("blocking-contract")
			if err := store.Create(ctx, task); err != nil {
				t.Fatal(err)
			}
			handler := NewFuncHandler(func(context.Context, *Task, *Message) (*TaskUpdate, error) {
				reply := NewAgentMessage("partial report")
				update := &TaskUpdate{Message: &reply, Artifact: &Artifact{ID: "report", Parts: []Part{&FilePart{File: FileContent{Bytes: "AQID"}}}}, Final: true}
				if test.state != "" {
					update.Status = &TaskStatus{State: test.state, Timestamp: time.Now()}
				}
				return update, nil
			})
			server := NewServer(NewAgentCard("blocking-contract", "", "1"), handler, WithStore(store))
			events := make(chan StreamEvent, 1)
			server.subscribe(task.ID, events)
			defer server.unsubscribe(task.ID, events)
			host := httptest.NewServer(server.Handler())
			defer host.Close()
			response := sendAdmissionMessage(ctx, host.Client(), host.URL, MethodSendMessage, task.ID, "blocking", false)
			if response.err != nil {
				t.Fatal(response.err)
			}
			var envelope JSONRPCResponse
			if err := json.Unmarshal(response.data, &envelope); err != nil {
				t.Fatal(err)
			}
			if test.invalid {
				if envelope.Error == nil || envelope.Error.Code != CodeInvalidAgentResponse || envelope.Error.Data.(map[string]any)["taskId"] != task.ID {
					t.Fatalf("invalid handler response reported as success: %s", response.data)
				}
			} else if envelope.Error != nil {
				t.Fatalf("valid final state rejected: %s", response.data)
			}
			stored, err := store.Get(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := test.state
			if test.invalid {
				want = TaskStateFailed
			}
			if stored.Status.State != want || len(stored.History) != 2 || stored.History[1].GetTextContent() != "partial report" || len(stored.Artifacts) != 1 || stored.Artifacts[0].Parts[0].(*FilePart).File.Bytes != "AQID" {
				t.Fatalf("state or partial output was lost: %+v", stored)
			}
			select {
			case event := <-events:
				status, ok := event.(*TaskStatusEvent)
				if !ok || status.Status.State != stored.Status.State || !status.Final {
					t.Fatalf("notification differs from committed task: %+v", event)
				}
			case <-ctx.Done():
				t.Fatal("committed final status notification missing")
			}
		})
	}
}
