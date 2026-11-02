package graph

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/hexagon/interrupt"
	"github.com/hexagon-codes/hexagon/runtime"
)

func TestNodeRetryIsolatesFailedAttempt(t *testing.T) {
	calls := 0
	node := RetryNode("retry", func(_ context.Context, s MapState) (MapState, error) {
		calls++
		s["value"] = s["value"].(int) + 1
		if calls == 1 {
			return s, errors.New("temporary read failure")
		}
		return s, nil
	}, &RetryPolicy{MaxRetries: 1})
	input := MapState{"value": 0}
	got, err := executeNodeHandler(t.Context(), node, input, nil)
	if err != nil || calls != 2 || got["value"] != 1 || input["value"] != 0 {
		t.Fatalf("result=%v original=%v calls=%d error=%v", got, input, calls, err)
	}
}

func TestNodeRetryDoesNotReplayControlOrUnknownOutcome(t *testing.T) {
	for name, failure := range map[string]error{
		"interrupt":         interrupt.InterruptSignalFunc(t.Context(), "waiting"),
		"canceled":          context.Canceled,
		"unknown_operation": runtime.ErrUnsafeReplay,
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			node := RetryNode("controlled", func(_ context.Context, s MapState) (MapState, error) { calls++; return s, failure }, &RetryPolicy{MaxRetries: 3, RetryOn: func(error) bool { return true }})
			_, err := executeNodeHandler(t.Context(), node, MapState{}, nil)
			if calls != 1 || !errors.Is(err, failure) {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	}
}

func TestNodeTimeoutRejectsExpiredResult(t *testing.T) {
	node := NewNode("timeout", func(ctx context.Context, s MapState) (MapState, error) {
		<-ctx.Done()
		s["late"] = true
		return s, nil
	}).WithTimeout(1).Build()
	input := MapState{}
	_, err := executeNodeHandler(t.Context(), node, input, nil)
	if !errors.Is(err, context.DeadlineExceeded) || input["late"] != nil {
		t.Fatalf("input=%v error=%v", input, err)
	}
}

func TestNodeExplicitZeroRetriesOverridesExecutorDefault(t *testing.T) {
	calls := 0
	node := RetryNode("once", func(_ context.Context, s MapState) (MapState, error) { calls++; return s, errors.New("failure") }, &RetryPolicy{})
	_, _ = executeNodeHandler(t.Context(), node, MapState{}, &RetryPolicy{MaxRetries: 3})
	if calls != 1 {
		t.Fatalf("explicit zero retries made %d calls", calls)
	}
}

func TestNodeTimeoutDoesNotBlindlyReplay(t *testing.T) {
	calls := 0
	node := TimeoutNode("unknown-timeout", func(ctx context.Context, state MapState) (MapState, error) {
		calls++
		<-ctx.Done()
		return state, ctx.Err()
	}, 1)
	node.RetryPolicy = &RetryPolicy{MaxRetries: 2, RetryOn: func(error) bool { return true }}
	_, err := executeNodeHandler(context.Background(), node, MapState{}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("timeout replayed: calls=%d err=%v", calls, err)
	}
}
