package interrupt

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestInterruptWithOptions_DefaultTypeMismatchReturnsError proves that pairing
// WithDefault[string] with InterruptWithOptions[int] no longer panics on the
// timeout path. The options are type-erased into InterruptOption, so a mismatch
// can't be caught at compile time; the runtime must surface a typed error
// instead of an "interface conversion" panic.
func TestInterruptWithOptions_DefaultTypeMismatchReturnsError(t *testing.T) {
	h := NewHandler(nil)
	ctx := ContextWithInterruptHandler(context.Background(), h)
	ctx = ContextWithThreadID(ctx, "thread-default-mismatch")

	// WithTimeout small so the interrupt times out (no Resume happens),
	// triggering the default-value path.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC instead of typed error: %v", r)
		}
	}()

	_, err := InterruptWithOptions[int](
		ctx,
		"payload",
		WithTimeout(20*time.Millisecond),
		WithDefault("a string default"), // WithDefault[string], but T = int
	)
	if err == nil {
		t.Fatal("expected a typed error from default type mismatch, got nil")
	}
	if !errors.Is(err, ErrInvalidResume) {
		t.Fatalf("expected ErrInvalidResume, got %v", err)
	}
}

// TestInterruptWithOptions_ValidatorTypeMismatchReturnsError proves that a
// WithValidator[string] paired with InterruptWithOptions[int] does not panic
// when the validator runs against the resumed (int) value.
func TestInterruptWithOptions_ValidatorTypeMismatchReturnsError(t *testing.T) {
	h := NewHandler(nil)
	ctx := ContextWithInterruptHandler(context.Background(), h)
	ctx = ContextWithThreadID(ctx, "thread-validator-mismatch")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PANIC instead of typed error: %v", r)
		}
	}()

	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		_, err = InterruptWithOptions[int](
			ctx,
			"payload",
			WithValidator(func(s string) error { return nil }), // WithValidator[string], T = int
		)
	}()

	// Resume with an int — validator was built for string, so applying it must
	// not panic.
	waitForPending(t, h, "thread-validator-mismatch")
	if rerr := h.Resume(context.Background(), "thread-validator-mismatch", Command{Resume: 42}); rerr != nil {
		t.Fatalf("Resume failed: %v", rerr)
	}
	<-done

	if err == nil {
		t.Fatal("expected a typed validation error from validator type mismatch, got nil")
	}
	if !errors.Is(err, ErrInvalidResume) {
		t.Fatalf("expected ErrInvalidResume, got %v", err)
	}
}

func waitForPending(t *testing.T, h *Handler, threadID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.GetPending(threadID) != nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for pending interrupt")
}
