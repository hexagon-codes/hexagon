package core

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	stream "github.com/hexagon-codes/ai-core/streamx"
)

func TestComposeStreamCollectsBeforeInvoke(t *testing.T) {
	first := NewRunnable[string, string]("source", "", func(_ context.Context, value string, _ ...Option) (string, error) { return value, nil }).
		WithStream(func(context.Context, string, ...Option) (*StreamReader[string], error) {
			return stream.FromSlice([]string{"a", "b", "c"}), nil
		})
	calls := 0
	second := RunnableFunc("sink", func(_ context.Context, value string) (string, error) { calls++; return strings.ToUpper(value), nil })
	reader, err := ComposeStream[string, string, string](first, second).Stream(t.Context(), "input")
	if err != nil {
		t.Fatal(err)
	}
	values, err := reader.Collect(t.Context())
	if err != nil || !reflect.DeepEqual(values, []string{"ABC"}) || calls != 1 {
		t.Fatalf("values=%v calls=%d error=%v", values, calls, err)
	}
}

func TestComposeStreamPreservesUpstreamError(t *testing.T) {
	failure := errors.New("upstream failed")
	first := NewRunnable[string, string]("source", "", nil).WithStream(func(context.Context, string, ...Option) (*StreamReader[string], error) {
		r, w := stream.Pipe[string](0)
		_ = w.CloseWithError(failure)
		return r, nil
	})
	calls := 0
	second := RunnableFunc("sink", func(_ context.Context, value string) (string, error) { calls++; return value, nil })
	reader, err := ComposeStream[string, string, string](first, second).Stream(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Collect(t.Context()); !errors.Is(err, failure) || calls != 0 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}

func TestComposeStreamCloseReleasesSilentSource(t *testing.T) {
	released := make(chan struct{})
	first := NewRunnable[string, string]("source", "", nil).WithStream(func(context.Context, string, ...Option) (*StreamReader[string], error) {
		r, _ := stream.PipeWithClose[string](0, func() error { close(released); return nil })
		return r, nil
	})
	second := RunnableFunc("sink", func(_ context.Context, value string) (string, error) { return value, nil })
	reader, err := ComposeStream[string, string, string](first, second).Stream(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("downstream close did not release upstream")
	}
}

func TestComposeStreamPreservesNativeIncrementalTransform(t *testing.T) {
	producerDone := make(chan struct{})
	first := NewRunnable[string, string]("native source", "", nil).WithStream(func(ctx context.Context, _ string, _ ...Option) (*StreamReader[string], error) {
		reader, writer := stream.Pipe[string](0)
		go func() { defer close(producerDone); defer writer.Close(); _ = writer.Send("a"); <-ctx.Done() }()
		return reader, nil
	})
	second := NewRunnable[string, string]("native transform", "", nil).WithTransform(func(_ context.Context, input *StreamReader[string], _ ...Option) (*StreamReader[string], error) {
		return stream.Map(input, strings.ToUpper), nil
	})
	reader, err := ComposeStream[string, string, string](first, second).Stream(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	got := make(chan string, 1)
	go func() { value, _ := reader.Recv(); got <- value }()
	select {
	case value := <-got:
		if value != "A" {
			t.Fatalf("value=%q", value)
		}
	case <-time.After(time.Second):
		t.Fatal("native transform waited for source EOF")
	}
	_ = reader.Close()
	select {
	case <-producerDone:
	case <-time.After(time.Second):
		t.Fatal("native producer did not receive downstream cancellation")
	}
}

func TestMapEachIsExplicit(t *testing.T) {
	r := RunnableFunc("upper", func(_ context.Context, value string) (string, error) { return strings.ToUpper(value), nil })
	reader, err := MapEach[string, string](r).Transform(t.Context(), stream.FromSlice([]string{"a", "b"}))
	if err != nil {
		t.Fatal(err)
	}
	values, err := reader.Collect(t.Context())
	if err != nil || !reflect.DeepEqual(values, []string{"A", "B"}) {
		t.Fatalf("values=%v error=%v", values, err)
	}
}
