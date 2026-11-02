package graph

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamChannelConcurrentCloseAndEmit(t *testing.T) {
	ch := NewStreamChannel(10)
	start := make(chan struct{})
	failures := make(chan any, 8)
	var wg sync.WaitGroup
	wg.Add(9)
	for i := 0; i < 8; i++ {
		go func() {
			defer wg.Done()
			defer func() {
				if v := recover(); v != nil {
					failures <- v
				}
			}()
			<-start
			ch.Emit(StreamModeEvent{Type: EventCustom})
		}()
	}
	go func() { defer wg.Done(); <-start; ch.Close() }()
	close(start)
	wg.Wait()
	close(failures)
	for v := range failures {
		t.Errorf("send/close panic: %v", v)
	}
}

func TestStreamRunKeepsTerminalWithSlowConsumer(t *testing.T) {
	for _, kind := range []string{"normal", "cancel", "failure"} {
		t.Run(kind, func(t *testing.T) {
			g, err := NewGraph[MapState]("terminal").AddNode("step", func(_ context.Context, s MapState) (MapState, error) {
				if kind == "failure" {
					return s, errors.New("node failure")
				}
				return s, nil
			}).AddEdge(START, "step").AddEdge("step", END).Build()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if kind == "cancel" {
				cancel()
			}
			ch, err := g.StreamRun(ctx, MapState{}, WithStreamMode(StreamModeDebug), WithStreamBufferSize(1))
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-ch.Done():
			case <-time.After(2 * time.Second):
				t.Fatal("stream did not close")
			}
			want := EventGraphEnd
			if kind != "normal" {
				want = EventNodeError
			}
			found := 0
			for event := range ch.Events() {
				if event.Type == want {
					found++
				}
			}
			if found != 1 {
				t.Fatalf("terminal count=%d, want 1 (%s)", found, want)
			}
		})
	}
}

func TestStreamFilterPanicCancelsInFlightGraph(t *testing.T) {
	started, exited := make(chan struct{}), make(chan struct{})
	var third atomic.Int32
	g, err := NewGraph[MapState]("filter-panic").AddNode("first", func(_ context.Context, s MapState) (MapState, error) { return s, nil }).AddNode("second", func(ctx context.Context, s MapState) (MapState, error) {
		close(started)
		<-ctx.Done()
		close(exited)
		return s, ctx.Err()
	}).AddNode("third", func(_ context.Context, s MapState) (MapState, error) { third.Add(1); return s, nil }).AddEdge(START, "first").AddEdge("first", "second").AddEdge("second", "third").AddEdge("third", END).Build()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch, err := g.StreamRun(ctx, MapState{}, WithStreamFilter(func(StreamModeEvent) bool {
		select {
		case <-started:
			panic("filter failed")
		case <-ctx.Done():
			return false
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	terminal := 0
	for event := range ch.Events() {
		if event.Type == EventNodeError {
			terminal++
			if event.Metadata["stack"] == "" || event.Metadata["stack"] == nil {
				t.Error("panic stack missing")
			}
			if event.Data == nil {
				t.Error("panic failure missing")
			}
		}
	}
	if terminal != 1 {
		t.Fatalf("terminal errors=%d, want 1", terminal)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("in-flight handler did not observe cancellation")
	}
	if third.Load() != 0 {
		t.Fatalf("third handler called %d times", third.Load())
	}
}
