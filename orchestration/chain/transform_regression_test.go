package chain

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/hexagon/core"
)

func TestChainTransformPreservesSourceErrorAndMergeTail(t *testing.T) {
	for _, typed := range []bool{false, true} {
		t.Run(map[bool]string{false: "chain", true: "typed"}[typed], func(t *testing.T) {
			failure := errors.New("upstream failed")
			source, writer := streamx.Pipe[int](1)
			if err := writer.Send(1); err != nil {
				t.Fatal(err)
			}
			if err := writer.CloseWithError(failure); err != nil {
				t.Fatal(err)
			}
			run := func(ctx context.Context, value int) (int, error) { return value * 2, nil }
			var target interface {
				Transform(context.Context, *streamx.StreamReader[int], ...core.Option) (*streamx.StreamReader[int], error)
			}
			if typed {
				target = NewTypedChain("typed", NewTypedStep("step", run))
			} else {
				target = NewChain[int, int]("plain").PipeFunc("step", func(ctx context.Context, value any) (any, error) {
					return run(ctx, value.(int))
				}).MustBuild()
			}
			out, err := target.Transform(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			values, err := out.Collect(context.Background())
			if !errors.Is(err, failure) || len(values) != 1 || values[0] != 2 {
				t.Fatalf("values=%v error=%v", values, err)
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChainTransformKeepsMergeTail(t *testing.T) {
	for _, typed := range []bool{false, true} {
		t.Run(map[bool]string{false: "chain", true: "typed"}[typed], func(t *testing.T) {
			run := func(ctx context.Context, value int) (int, error) { return value * 2, nil }
			var target interface {
				Transform(context.Context, *streamx.StreamReader[int], ...core.Option) (*streamx.StreamReader[int], error)
			}
			if typed {
				target = NewTypedChain("typed", NewTypedStep("step", run))
			} else {
				target = NewChain[int, int]("plain").PipeFunc("step", func(ctx context.Context, value any) (any, error) {
					return run(ctx, value.(int))
				}).MustBuild()
			}
			source := streamx.Merge(streamx.FromSlice([]int{1}), streamx.FromSlice([]int{2, 3}))
			out, err := target.Transform(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			values, err := out.Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(values) != 3 || values[0] != 2 || values[1] != 4 || values[2] != 6 {
				t.Fatalf("values=%v", values)
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChainTransformOutputCloseStopsInput(t *testing.T) {
	for _, typed := range []bool{false, true} {
		t.Run(map[bool]string{false: "chain", true: "typed"}[typed], func(t *testing.T) {
			var calls atomic.Int32
			run := func(context.Context, int) (int, error) { calls.Add(1); return 1, nil }
			var target interface {
				Transform(context.Context, *streamx.StreamReader[int], ...core.Option) (*streamx.StreamReader[int], error)
			}
			if typed {
				target = NewTypedChain("typed", NewTypedStep("step", run))
			} else {
				target = NewChain[int, int]("plain").PipeFunc("step", func(ctx context.Context, value any) (any, error) {
					return run(ctx, value.(int))
				}).MustBuild()
			}
			source, writer := streamx.Pipe[int](0)
			out, err := target.Transform(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			if err := out.Close(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(50 * time.Millisecond)
			if calls.Load() != 0 {
				t.Fatalf("step invoked after output close: %d", calls.Load())
			}
			_ = source.Close()
			_ = writer.Close()
		})
	}
}

func TestChainTransformPanicBecomesStreamError(t *testing.T) {
	target := NewTypedChain("typed", NewTypedStep("step", func(context.Context, int) (int, error) {
		panic("chain callback failed")
	}))
	out, err := target.Transform(context.Background(), streamx.FromValue(1))
	if err != nil {
		t.Fatal(err)
	}
	_, err = out.Collect(context.Background())
	var panicErr *core.AsyncPanicError
	if !errors.As(err, &panicErr) || !llm.IsUnsafeToReplay(err) {
		t.Fatalf("panic not converted: %v", err)
	}
}

func TestParallelPreservesResultsAndWaitsForSiblings(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var successful atomic.Int32
	parallel := NewParallel[int, int]("parallel", func(values []int) int {
		total := 0
		for _, value := range values {
			total += value
		}
		return total
	}).Add(func(context.Context, int) (int, error) {
		successful.Add(1)
		close(started)
		<-release
		return 7, nil
	}).Add(func(context.Context, int) (int, error) {
		<-started
		return 0, errors.New("other branch failed")
	})
	done := make(chan error, 1)
	go func() {
		_, err := parallel.Invoke(context.Background(), 0)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("parallel returned before sibling completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	err := <-done
	var parallelErr *ParallelError[int]
	if !errors.As(err, &parallelErr) || len(parallelErr.Results) != 2 {
		t.Fatalf("parallel error=%v", err)
	}
	if successful.Load() != 1 || parallelErr.Results[0].Err != nil || parallelErr.Results[1].Err == nil {
		t.Fatalf("branch results lost: %+v", parallelErr.Results)
	}
	if !parallelErr.UnsafeToReplay() {
		t.Fatal("successful branch must make the group unsafe to replay")
	}
}

func TestParallelDoesNotMergeFailedBranches(t *testing.T) {
	parallel := NewParallel[int, int]("parallel", func([]int) int {
		panic("merge must not run when a branch failed")
	}).
		Add(func(context.Context, int) (int, error) { return 7, nil }).
		Add(func(context.Context, int) (int, error) { return 0, errors.New("other branch failed") })
	_, err := parallel.Invoke(context.Background(), 0)
	var parallelErr *ParallelError[int]
	if !errors.As(err, &parallelErr) || len(parallelErr.Results) != 2 || parallelErr.Results[0].Output != 7 {
		t.Fatalf("partial results lost: %v %+v", err, parallelErr)
	}
}

func TestParallelPanicIsContained(t *testing.T) {
	parallel := NewParallel[int, int]("parallel", func(values []int) int { return values[0] }).
		Add(func(context.Context, int) (int, error) { panic("parallel callback failed") })
	_, err := parallel.Invoke(context.Background(), 1)
	var panicErr *core.AsyncPanicError
	if !errors.As(err, &panicErr) || !llm.IsUnsafeToReplay(err) {
		t.Fatalf("panic not contained: %v", err)
	}
}

type unknownError struct{}

func (unknownError) Error() string        { return "external outcome unknown" }
func (unknownError) UnsafeToReplay() bool { return true }

type reconciliationError struct{}

func (reconciliationError) Error() string             { return "reconciliation required" }
func (reconciliationError) NeedsReconciliation() bool { return true }

func TestRetryMiddlewareStopsOnCancellationAndUnknownOutcome(t *testing.T) {
	unsafe := func(error) bool { return true }
	for _, failure := range []error{context.Canceled, unknownError{}, fmt.Errorf("wrapped: %w", reconciliationError{})} {
		calls := 0
		step := RetryMiddleware(2, unsafe)(func(context.Context, any) (any, error) {
			calls++
			return nil, failure
		})
		if _, err := step(context.Background(), nil); !errors.Is(err, failure) {
			t.Fatalf("failure=%v error=%v", failure, err)
		}
		if calls != 1 {
			t.Fatalf("failure=%v calls=%d", failure, calls)
		}
	}
}
