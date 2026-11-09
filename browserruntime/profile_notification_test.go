package browserruntime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/tool"
)

// 驱动只记录真实租约边界的调用，不模拟 RPC 取消完成或页面写入成功。
type profileNotificationDriver struct {
	binds, actions, inspections atomic.Int32
}

func (*profileNotificationDriver) observation() Observation {
	return Observation{Page: Page{ID: "page", Index: 0}, Pages: []Page{{ID: "page", Index: 0}}, Width: 100, Height: 100}
}
func (d *profileNotificationDriver) Bind(context.Context, string, string, bool) (Observation, error) {
	d.binds.Add(1)
	return d.observation(), nil
}
func (d *profileNotificationDriver) Execute(context.Context, SessionRef, Action) (ActionResult, error) {
	d.actions.Add(1)
	return ActionResult{Outcome: "unknown"}, &UnknownCallError{Cause: errors.New("original response unavailable")}
}
func (*profileNotificationDriver) Quiesce(context.Context, string) error { return ErrNotQuiescent }
func (*profileNotificationDriver) ClosePage(context.Context, string, string) (tool.Result, error) {
	return tool.NewResult(nil), nil
}
func (d *profileNotificationDriver) Inspect(context.Context, string) (Observation, error) {
	d.inspections.Add(1)
	return d.observation(), nil
}

type profileAcquireResult struct {
	lease *Lease
	err   error
}

func profileNotificationCoordinator(t *testing.T, driver *profileNotificationDriver, waiting func(context.Context, TaskRef, SessionRef)) *Coordinator {
	t.Helper()
	c := New(driver, Options{Waiting: waiting})
	if err := c.RegisterSession(SessionRef{ID: "session", ServerName: "server", ProfileID: "profile", Generation: 1}); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCoordinatorUnknownWakesQueuedAcquireWithoutDispatch(t *testing.T) {
	driver := &profileNotificationDriver{}
	waiting := make(chan struct{})
	var once sync.Once
	c := profileNotificationCoordinator(t, driver, func(_ context.Context, task TaskRef, _ SessionRef) {
		if task.ID == "second" {
			once.Do(func() { close(waiting) })
		}
	})
	first, err := c.Acquire(context.Background(), TaskRef{ID: "first", OwnerID: "owner"}, Target{ProfileID: "profile"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan profileAcquireResult, 1)
	go func() {
		lease, err := c.Acquire(ctx, TaskRef{ID: "second", OwnerID: "owner"}, Target{ProfileID: "profile"})
		done <- profileAcquireResult{lease: lease, err: err}
	}()
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal("second task never entered the profile queue")
	}
	_, err = first.Execute(context.Background(), Action{Type: "mcp", ToolName: "write"})
	var unknown *UnknownCallError
	if !errors.As(err, &unknown) {
		t.Fatalf("original action err=%v, want unknown", err)
	}
	result := <-done
	if result.lease != nil || !errors.Is(result.err, ErrNotQuiescent) {
		t.Fatalf("queued acquisition result=%+v, want no lease and ErrNotQuiescent", result)
	}
	if driver.binds.Load() != 2 || driver.actions.Load() != 1 {
		t.Fatalf("unknown state dispatched another operation: binds=%d actions=%d", driver.binds.Load(), driver.actions.Load())
	}
	if err := first.Release(context.Background(), "cancelled"); !errors.Is(err, ErrNotQuiescent) {
		t.Fatalf("unknown holder was released: %v", err)
	}
}

func TestCoordinatorUnknownRejectsInspectWithoutDispatch(t *testing.T) {
	driver := &profileNotificationDriver{}
	c := profileNotificationCoordinator(t, driver, nil)
	first, err := c.Acquire(context.Background(), TaskRef{ID: "first", OwnerID: "owner"}, Target{ProfileID: "profile"})
	if err != nil {
		t.Fatal(err)
	}
	first.markUnknown()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = c.Inspect(ctx, TaskRef{ID: "second", OwnerID: "owner"}, Target{ProfileID: "profile"})
	if !errors.Is(err, ErrNotQuiescent) {
		t.Fatalf("inspect err=%v, want ErrNotQuiescent", err)
	}
	if driver.binds.Load() != 1 || driver.actions.Load() != 0 || driver.inspections.Load() != 0 {
		t.Fatal("inspection dispatched or allocated a page while the holder was unknown")
	}
}

func TestCoordinatorLateUnknownDoesNotRejectNewGenerationQueue(t *testing.T) {
	driver := &profileNotificationDriver{}
	waiting := make(chan struct{})
	var once sync.Once
	c := profileNotificationCoordinator(t, driver, func(_ context.Context, task TaskRef, _ SessionRef) {
		if task.ID == "second-new" {
			once.Do(func() { close(waiting) })
		}
	})
	old, err := c.Acquire(context.Background(), TaskRef{ID: "old", OwnerID: "owner"}, Target{ProfileID: "profile"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CloseSession(context.Background(), "profile", func(context.Context, SessionRef) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := c.RegisterSession(SessionRef{ID: "new", ServerName: "server", ProfileID: "profile", Generation: 2}); err != nil {
		t.Fatal(err)
	}
	first, err := c.Acquire(context.Background(), TaskRef{ID: "first-new", OwnerID: "owner"}, Target{ProfileID: "profile"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan profileAcquireResult, 1)
	go func() {
		lease, err := c.Acquire(ctx, TaskRef{ID: "second-new", OwnerID: "owner"}, Target{ProfileID: "profile"})
		done <- profileAcquireResult{lease: lease, err: err}
	}()
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal("new-generation task did not enter the queue")
	}
	old.markUnknown()
	if err := first.Release(context.Background(), "completed"); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || result.lease == nil || result.lease.Ref().Generation != 2 {
		t.Fatalf("late old result changed the new generation: %+v", result)
	}
	if err := result.lease.Release(context.Background(), "completed"); err != nil {
		t.Fatal(err)
	}
	if driver.binds.Load() != 3 || driver.actions.Load() != 0 {
		t.Fatalf("unexpected physical calls: binds=%d actions=%d", driver.binds.Load(), driver.actions.Load())
	}
}
