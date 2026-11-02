package core

import (
	"context"
	"errors"
	"github.com/hexagon-codes/ai-core/llm"
	s "github.com/hexagon-codes/ai-core/streamx"
	"testing"
)

func TestStreamAdapterPanicsCannotBeRetried(t *testing.T) {
	for _, mode := range []string{"map", "collect", "source"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			failure := errors.New("failed after effect")
			effect := func(string) string { calls++; panic(failure) }
			leaf := NewRunnable[string, string]("leaf", "", func(_ context.Context, v string, _ ...Option) (string, error) { return effect(v), nil })
			var target Runnable[string, string]
			switch mode {
			case "map":
				target = NewRunnable[string, string]("derived", "", nil).WithTransform(MapEach(leaf).Transform)
			case "collect":
				target = NewRunnable[string, string]("derived", "", nil).WithTransform(leaf.Transform)
			default:
				first := NewRunnable[string, string]("source", "", nil).WithStream(func(_ context.Context, v string, _ ...Option) (*s.StreamReader[string], error) {
					return s.Map(s.FromValue(v), effect), nil
				})
				pass := NewRunnable[string, string]("pass", "", nil).WithTransform(func(_ context.Context, r *s.StreamReader[string], _ ...Option) (*s.StreamReader[string], error) {
					return r, nil
				})
				target = NewRunnable[string, string]("derived", "", nil).WithStream(ComposeStream(first, pass).Stream)
			}
			_, e := WithRetry(target, &RetryConfig{MaxRetries: 1}).Invoke(t.Context(), "once")
			var p *AsyncPanicError
			if calls != 1 || !errors.Is(e, failure) || !llm.IsUnsafeToReplay(e) || !errors.As(e, &p) || len(p.Stack) == 0 {
				t.Fatalf("calls=%d error=%v", calls, e)
			}
		})
	}
}

func TestOwnedStreamPropagatesCloseCallbackFailure(t *testing.T) {
	failure := errors.New("cleanup failed")
	first := NewRunnable[string, string]("source", "", nil).WithStream(func(context.Context, string, ...Option) (*s.StreamReader[string], error) {
		r, w := s.PipeWithClose[string](1, func() error { panic(failure) })
		w.Send("value")
		w.Close()
		return r, nil
	})
	pass := NewRunnable[string, string]("pass", "", nil).WithTransform(func(_ context.Context, r *s.StreamReader[string], _ ...Option) (*s.StreamReader[string], error) {
		return r, nil
	})
	out, e := ComposeStream(first, pass).Stream(t.Context(), "input")
	if e != nil {
		t.Fatal(e)
	}
	_, e = out.Collect(t.Context())
	if !errors.Is(e, failure) || !llm.IsUnsafeToReplay(e) {
		t.Fatalf("cleanup failure lost: %v", e)
	}
}

func TestTransformInputCloseFailureCannotReplayInvoke(t *testing.T) {
	for _, mode := range []string{"map", "collect"} {
		t.Run(mode, func(t *testing.T) {
			actions := 0
			failure := errors.New("input close failed")
			leaf := NewRunnable[string, string]("leaf", "", func(_ context.Context, value string, _ ...Option) (string, error) { actions++; return value, nil })
			var target Runnable[string, string] = leaf
			if mode == "map" {
				target = MapEach(leaf)
			}
			input, w := s.PipeWithClose[string](1, func() error { panic(failure) })
			w.Send("value")
			w.Close()
			out, e := target.Transform(t.Context(), input)
			if e != nil {
				t.Fatal(e)
			}
			_, e = out.Collect(t.Context())
			if !errors.Is(e, failure) || !llm.IsUnsafeToReplay(e) || actions > 1 {
				t.Fatalf("actions=%d error=%v", actions, e)
			}
		})
	}
}
