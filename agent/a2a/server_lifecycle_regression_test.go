package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func lifecycleAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}
func lifecycleStart(t *testing.T, s *Server, addr string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.Start(addr) }()
	deadline := time.After(2 * time.Second)
	for {
		s.lifecycleMu.Lock()
		ready := s.httpServer != nil
		s.lifecycleMu.Unlock()
		if ready {
			return done
		}
		select {
		case err := <-done:
			t.Fatalf("Start failed: %v", err)
		case <-deadline:
			t.Fatal("Start did not publish listener")
		case <-time.After(time.Millisecond):
		}
	}
}
func lifecycleStop(t *testing.T, s *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Error(err)
	}
}
func lifecycleWait(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Start did not return")
	}
}

func TestServerStopWaitsForActualHandlerDrain(t *testing.T) {
	entered, cancelSeen, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(finish) }) }
	defer unblock()
	var calls, active, maxActive atomic.Int32
	handler := NewFuncHandler(func(ctx context.Context, _ *Task, _ *Message) (*TaskUpdate, error) {
		n := active.Add(1)
		for {
			old := maxActive.Load()
			if old >= n || maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		defer active.Add(-1)
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			close(cancelSeen)
			<-finish
			return nil, ctx.Err()
		}
		message := NewAgentMessage("done")
		return NewCompletedUpdate(&message), nil
	})
	s := NewServer(NewAgentCard("drain", "", "1"), handler)
	defer func() { unblock(); lifecycleStop(t, s) }()
	oldAddr := lifecycleAddress(t)
	oldDone := lifecycleStart(t, s, oldAddr)
	client := NewClient("http://" + oldAddr)
	defer client.Close()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		_, _ = client.SendMessage(context.Background(), &SendMessageRequest{Message: NewUserMessage("first")})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler not entered")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	err := s.Stop(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Stop error=%v", err)
	}
	select {
	case <-cancelSeen:
	case <-time.After(time.Second):
		t.Fatal("handler did not observe cancel")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 80*time.Millisecond)
	err = s.Stop(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("second Stop falsely drained: %v", err)
	}
	newDone := make(chan error, 1)
	newAddr := lifecycleAddress(t)
	go func() { newDone <- s.Start(newAddr) }()
	rejected := false
	select {
	case err := <-newDone:
		rejected = err != nil
	case <-time.After(100 * time.Millisecond):
	}
	if !rejected {
		t.Error("Start accepted before handler cleanup")
	}
	unblock()
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Error("old request did not exit")
	}
	lifecycleStop(t, s)
	lifecycleWait(t, oldDone)
	if !rejected {
		lifecycleWait(t, newDone)
	}
	if active.Load() != 0 {
		t.Error("handler still active after cleanup")
	}
	restartAddr := lifecycleAddress(t)
	restartDone := lifecycleStart(t, s, restartAddr)
	next := NewClient("http://" + restartAddr)
	defer next.Close()
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	_, err = next.SendMessage(ctx, &SendMessageRequest{Message: NewUserMessage("second")})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	lifecycleStop(t, s)
	lifecycleWait(t, restartDone)
	if maxActive.Load() != 1 || calls.Load() != 2 {
		t.Fatalf("active max=%d calls=%d", maxActive.Load(), calls.Load())
	}
	list, err := s.store.List(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tasks) != 2 {
		t.Fatalf("restart lost stored tasks: %d", len(list.Tasks))
	}
}

type lifecycleGatedStore struct {
	*MemoryTaskStore
	entered, release chan struct{}
	once             sync.Once
}

func (s *lifecycleGatedStore) Get(ctx context.Context, id string) (*Task, error) {
	s.once.Do(func() {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
		}
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.MemoryTaskStore.Get(ctx, id)
}
func TestServerStopDeadlineDoesNotWaitForBusinessStoreLock(t *testing.T) {
	store := &lifecycleGatedStore{MemoryTaskStore: NewMemoryTaskStore(WithTaskTTL(0)), entered: make(chan struct{}), release: make(chan struct{})}
	defer store.Close()
	var released sync.Once
	unblock := func() { released.Do(func() { close(store.release) }) }
	defer unblock()
	s := NewServer(NewAgentCard("deadline", "", "1"), NewEchoHandler(), WithStore(store))
	defer lifecycleStop(t, s)
	addr := lifecycleAddress(t)
	startDone := lifecycleStart(t, s, addr)
	client := NewClient("http://" + addr)
	defer client.Close()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		_, _ = client.SendMessage(context.Background(), &SendMessageRequest{Message: NewUserMessage("request")})
	}()
	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("store not entered")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- s.Stop(ctx) }()
	select {
	case err := <-stopped:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Stop error=%v", err)
		}
	case <-time.After(200 * time.Millisecond):
		unblock()
		t.Error("Stop waited beyond deadline for business store")
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Error("request did not observe cancellation")
	}
	lifecycleStop(t, s)
	lifecycleWait(t, startDone)
}

func TestServerOwnsOnlyDefaultStoreCleanup(t *testing.T) {
	s := NewServer(NewAgentCard("default", "", "1"), nil)
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.ownedStore.cleanupDone:
	default:
		t.Fatal("default cleanup remains live")
	}
	custom := NewMemoryTaskStore()
	defer custom.Close()
	injected := NewServer(NewAgentCard("injected", "", "1"), nil, WithStore(custom))
	if err := injected.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-custom.cleanupDone:
		t.Fatal("host-owned store was closed")
	default:
	}
}

func TestServerStopWaitsForStreamingProducerDrain(t *testing.T) {
	entered, canceled, finish, exited := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var released sync.Once
	unblock := func() { released.Do(func() { close(finish) }) }
	defer unblock()
	handler := NewStreamingFuncHandler(func(context.Context, *Task, *Message) (*TaskUpdate, error) {
		return nil, errors.New("unexpected blocking call")
	}, func(ctx context.Context, _ *Task, _ *Message) (<-chan *TaskUpdate, error) {
		updates := make(chan *TaskUpdate)
		go func() {
			defer close(updates)
			defer close(exited)
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-finish
		}()
		return updates, nil
	})
	s := NewServer(NewAgentCard("stream-drain", "", "1"), handler)
	defer func() { unblock(); lifecycleStop(t, s) }()
	addr := lifecycleAddress(t)
	started := lifecycleStart(t, s, addr)
	request, _ := NewJSONRPCRequest(1, MethodSendMessageStream, &SendMessageRequest{Message: NewUserMessage("stream")})
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post("http://"+addr+PathTasks, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("producer not entered")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	err = s.Stop(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("first Stop=%v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("producer not canceled")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 80*time.Millisecond)
	err = s.Stop(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("stream producer falsely drained: %v", err)
	}
	unblock()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Error("producer did not exit")
	}
	lifecycleStop(t, s)
	lifecycleWait(t, started)
}
