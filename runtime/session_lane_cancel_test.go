package runtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/toolkit/util/lease"
)

type observedCancellationContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *observedCancellationContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestSessionLaneAdmissionAndDuplicateWaitAreCancellable(t *testing.T) {
	for _, session := range []string{"same-session", ""} {
		t.Run(session, func(t *testing.T) {
			lane := NewSessionLane()
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer func() { releaseOnce.Do(func() { close(release) }); <-done }()
			go func() {
				defer close(done)
				_, _ = lane.DoRun(t.Context(), session, "same-request", func(context.Context) (*Result, error) {
					close(started)
					<-release
					return &Result{Content: "original"}, nil
				})
			}()
			<-started
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx := &observedCancellationContext{Context: parent, observed: make(chan struct{})}
			var called atomic.Bool
			out := make(chan error, 1)
			go func() {
				_, err := lane.DoRun(ctx, session, "same-request", func(context.Context) (*Result, error) { called.Store(true); return nil, nil })
				out <- err
			}()
			<-ctx.observed
			cancel()
			var err error
			select {
			case err = <-out:
			case <-time.After(time.Second):
				t.Fatal("waiter did not respond to cancellation")
			}
			if !errors.Is(err, context.Canceled) || called.Load() {
				t.Fatalf("cancelled waiter: error=%v called=%v", err, called.Load())
			}
			releaseOnce.Do(func() { close(release) })
			<-done
			result, err := lane.DoRun(t.Context(), session, "same-request", func(context.Context) (*Result, error) { t.Fatal("completed request ran again"); return nil, nil })
			if err != nil || result.Content != "original" {
				t.Fatalf("original result lost: %+v %v", result, err)
			}
		})
	}
}

type lostSessionLease struct{ released atomic.Int32 }

func (*lostSessionLease) Acquire(context.Context, string, time.Duration) (lease.FencingToken, bool, error) {
	return 41, true, nil
}
func (l *lostSessionLease) Release(context.Context, string, lease.FencingToken) error {
	l.released.Add(1)
	return nil
}
func (*lostSessionLease) Refresh(context.Context, string, lease.FencingToken, time.Duration) error {
	return lease.ErrNotHolder
}

func TestSessionLanePropagatesFenceAndLossCancellation(t *testing.T) {
	lane := NewSessionLane()
	backend := &lostSessionLease{}
	lane.SetDistributedLease(backend, 30*time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result, err := lane.DoRun(ctx, "session", "request", func(ctx context.Context) (*Result, error) {
		fence, ok := SessionFenceFromContext(ctx)
		if !ok || fence.Token != 41 || fence.SessionKey != "session" {
			t.Errorf("missing fencing identity: %+v", fence)
		}
		<-ctx.Done()
		return &Result{Content: "already completed partial result"}, nil
	})
	if !errors.Is(err, ErrExecutionLost) || result == nil || result.Content == "" || backend.released.Load() != 1 {
		t.Fatalf("result=%+v error=%v releases=%d", result, err, backend.released.Load())
	}
}

func TestTurnControllerLateStopAndBoundaryHandshake(t *testing.T) {
	controller := NewTurnController()
	a := TurnIdentity{SessionID: "session", RunID: "A", TurnID: "turn-A"}
	b := TurnIdentity{SessionID: "session", RunID: "B", TurnID: "turn-B"}
	first, err := controller.Begin(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	first.Finish()
	second, err := controller.Begin(t.Context(), b)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Finish()
	if controller.RequestStop(a, StopImmediate) || second.Context().Err() != nil {
		t.Fatal("late stop of A reached B")
	}
	if !controller.RequestStop(b, StopAfterModel) || second.Context().Err() != nil {
		t.Fatal("cooperative boundary stopped before boundary")
	}
	if err := ReachTurnBoundary(second.Context(), "child", StopAfterModel); err != nil {
		t.Fatalf("child advanced parent boundary: %v", err)
	}
	if err := ReachTurnBoundary(second.Context(), "B", StopAfterModel); !errors.Is(err, ErrTurnStopped) {
		t.Fatalf("boundary stop=%v", err)
	}
	select {
	case <-second.Done():
		t.Fatal("request stop falsely reported execution finished")
	default:
	}
	second.Finish()
	select {
	case <-second.Done():
	default:
		t.Fatal("finished turn did not acknowledge completion")
	}
}
