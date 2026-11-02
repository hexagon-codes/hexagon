package a2a

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTimeoutMiddlewareRecoversBothOrders(t *testing.T) {
	for _, innerRecovery := range []bool{false, true} {
		handler := NewFuncHandler(func(context.Context, *Task, *Message) (*TaskUpdate, error) { panic("callback failed") })
		var wrapped TaskHandler
		if innerRecovery {
			wrapped = ApplyMiddleware(handler, TimeoutMiddleware(time.Second), RecoveryMiddleware())
		} else {
			wrapped = ApplyMiddleware(handler, RecoveryMiddleware(), TimeoutMiddleware(time.Second))
		}
		update, e := wrapped.HandleTask(t.Context(), NewTask("owned"), nil)
		if e != nil || update == nil || update.Status == nil || update.Status.State != TaskStateFailed {
			t.Fatalf("update=%+v error=%v", update, e)
		}
	}
}
func TestTimeoutWorkerKeepsExecutionUntilCleanup(t *testing.T) {
	entered, finish, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(finish) }) }
	defer release()
	inner := NewFuncHandler(func(ctx context.Context, task *Task, _ *Message) (*TaskUpdate, error) {
		defer close(exited)
		close(entered)
		<-ctx.Done()
		<-finish
		task.Status.State = TaskStateCompleted
		return NewCompletedUpdate(nil), nil
	})
	s := NewServer(NewAgentCard("timeout", "", "1"), TimeoutMiddleware(20*time.Millisecond)(inner))
	defer func() { release(); lifecycleStop(t, s) }()
	addr := lifecycleAddress(t)
	started := lifecycleStart(t, s, addr)
	client := NewClient("http://" + addr)
	defer client.Close()
	task, e := client.SendMessage(t.Context(), &SendMessageRequest{Message: NewUserMessage("work")})
	if e != nil {
		t.Fatal(e)
	}
	<-entered
	if task.Status.State != TaskStateFailed {
		t.Fatal(task.Status.State)
	}
	ctx, c := context.WithTimeout(t.Context(), 40*time.Millisecond)
	e = s.Stop(ctx)
	c()
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("worker falsely drained: %v", e)
	}
	if e := s.Start(lifecycleAddress(t)); e == nil {
		t.Fatal("restart accepted before worker cleanup")
	}
	release()
	<-exited
	lifecycleStop(t, s)
	lifecycleWait(t, started)
	stored, e := s.store.Get(t.Context(), task.ID)
	if e != nil || stored.Status.State != TaskStateFailed {
		t.Fatalf("late worker changed persisted task: %+v %v", stored, e)
	}
}
func TestServerStopEndsLiveSubscriptionWithoutDeadline(t *testing.T) {
	store := NewMemoryTaskStore(WithTaskTTL(0))
	defer store.Close()
	task := NewTask("subscriber")
	if e := store.Create(t.Context(), task); e != nil {
		t.Fatal(e)
	}
	s := NewServer(NewAgentCard("subscribe", "", "1"), NewFuncHandler(func(context.Context, *Task, *Message) (*TaskUpdate, error) { return nil, nil }), WithStore(store))
	addr := lifecycleAddress(t)
	started := lifecycleStart(t, s, addr)
	client := NewClient("http://" + addr)
	defer client.Close()
	events, e := client.Resubscribe(t.Context(), task.ID)
	if e != nil {
		t.Fatal(e)
	}
	select {
	case _, open := <-events:
		if !open {
			t.Fatal("subscription closed early")
		}
	case <-time.After(time.Second):
		t.Fatal("subscription not started")
	}
	done := make(chan error, 1)
	go func() { done <- s.Stop(context.Background()) }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("live SSE prevented Stop")
	}
	lifecycleWait(t, started)
}
