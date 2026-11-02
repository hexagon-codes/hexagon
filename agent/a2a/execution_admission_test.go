package a2a

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type admissionReadKey struct{}

// 延迟锁外读取的真实副本，使前一次执行先完成；不修改任务内容。
type admissionReadStore struct {
	*MemoryTaskStore
	captured chan struct{}
	release  chan struct{}
	held     atomic.Bool
}

func (s *admissionReadStore) Get(ctx context.Context, id string) (*Task, error) {
	task, err := s.MemoryTaskStore.Get(ctx, id)
	if err == nil && ctx.Value(admissionReadKey{}) == true && s.held.CompareAndSwap(false, true) {
		close(s.captured)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return task, err
}

type admissionHTTPResponse struct {
	data []byte
	err  error
}

func sendAdmissionMessage(ctx context.Context, client *http.Client, address, method, taskID, label string, delayed bool) admissionHTTPResponse {
	request, err := NewJSONRPCRequest(label, method, SendMessageRequest{TaskID: taskID, Message: NewUserMessage(label)})
	if err != nil {
		return admissionHTTPResponse{err: err}
	}
	data, err := json.Marshal(request)
	if err != nil {
		return admissionHTTPResponse{err: err}
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, address+PathTasks, strings.NewReader(string(data)))
	if err != nil {
		return admissionHTTPResponse{err: err}
	}
	httpRequest.Header.Set("Content-Type", ContentTypeJSON)
	if delayed {
		httpRequest.Header.Set("X-Admission-Delay", "true")
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return admissionHTTPResponse{err: err}
	}
	defer response.Body.Close()
	data, err = io.ReadAll(response.Body)
	return admissionHTTPResponse{data: data, err: err}
}

func TestA2AStaleReadCannotReopenCompletedTask(t *testing.T) {
	for _, method := range []string{MethodSendMessage, MethodSendMessageStream} {
		t.Run(method, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			store := &admissionReadStore{MemoryTaskStore: NewMemoryTaskStore(WithTaskTTL(0)), captured: make(chan struct{}), release: make(chan struct{})}
			started, finish := make(chan struct{}), make(chan struct{})
			var finishOnce, releaseOnce sync.Once
			var calls atomic.Int32
			if err := store.Create(ctx, NewTask("admission-task")); err != nil {
				t.Fatal(err)
			}
			handler := NewFuncHandler(func(ctx context.Context, task *Task, message *Message) (*TaskUpdate, error) {
				if calls.Add(1) == 1 {
					close(started)
					select {
					case <-finish:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				reply := NewAgentMessage("original completed reply")
				return NewCompletedUpdate(&reply), nil
			})
			card := NewAgentCard("admission", "", "1")
			card.Capabilities.Streaming = true
			server := NewServer(card, handler, WithStore(store))
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Admission-Delay") == "true" {
					r = r.WithContext(context.WithValue(r.Context(), admissionReadKey{}, true))
				}
				server.ServeHTTP(w, r)
			}))
			t.Cleanup(func() {
				cancel()
				finishOnce.Do(func() { close(finish) })
				releaseOnce.Do(func() { close(store.release) })
				httpServer.Close()
				store.Close()
			})
			first, second := make(chan admissionHTTPResponse, 1), make(chan admissionHTTPResponse, 1)
			go func() {
				first <- sendAdmissionMessage(ctx, httpServer.Client(), httpServer.URL, MethodSendMessage, "admission-task", "original", false)
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("first handler did not start")
			}
			go func() {
				second <- sendAdmissionMessage(ctx, httpServer.Client(), httpServer.URL, method, "admission-task", "stale", true)
			}()
			select {
			case <-store.captured:
			case <-ctx.Done():
				t.Fatal("second request did not capture its old task")
			}
			finishOnce.Do(func() { close(finish) })
			if response := <-first; response.err != nil {
				t.Fatal(response.err)
			}
			completed, err := store.MemoryTaskStore.Get(ctx, "admission-task")
			if err != nil || completed.Status.State != TaskStateCompleted {
				t.Fatalf("first request is not completed: task=%+v err=%v", completed, err)
			}
			before, err := json.Marshal(completed)
			if err != nil {
				t.Fatal(err)
			}
			releaseOnce.Do(func() { close(store.release) })
			response := <-second
			if response.err != nil || !strings.Contains(string(response.data), `"error"`) {
				t.Fatalf("stale request was not rejected: data=%s err=%v", response.data, response.err)
			}
			current, err := store.MemoryTaskStore.Get(ctx, "admission-task")
			if err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(current)
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 || string(after) != string(before) {
				t.Fatalf("completed task was re-executed or overwritten: calls=%d before=%s after=%s", calls.Load(), before, after)
			}
		})
	}
}

func TestA2AExecutionAdmissionPreservesWaitingStates(t *testing.T) {
	for _, status := range []TaskState{TaskStateInputRequired, TaskStateAuthRequired} {
		t.Run(string(status), func(t *testing.T) {
			store := NewMemoryTaskStore(WithTaskTTL(0))
			defer store.Close()
			task := NewTask("waiting-task")
			task.Status.State = status
			if err := store.Create(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			handler := NewFuncHandler(func(context.Context, *Task, *Message) (*TaskUpdate, error) {
				calls.Add(1)
				reply := NewAgentMessage("resumed")
				return NewCompletedUpdate(&reply), nil
			})
			server := httptest.NewServer(NewServer(NewAgentCard("waiting", "", "1"), handler, WithStore(store)).Handler())
			defer server.Close()
			response := sendAdmissionMessage(t.Context(), server.Client(), server.URL, MethodSendMessage, task.ID, "resume", false)
			if response.err != nil || strings.Contains(string(response.data), `"error"`) {
				t.Fatalf("waiting task resume failed: body=%s err=%v", response.data, response.err)
			}
			completed, err := store.Get(t.Context(), task.ID)
			if err != nil || completed.Status.State != TaskStateCompleted || calls.Load() != 1 {
				t.Fatalf("waiting task did not execute once: task=%+v calls=%d err=%v", completed, calls.Load(), err)
			}
		})
	}
}
