package browser

import (
	"context"
	"errors"
	"testing"
)

func TestValidateBrowserURLUsesCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := validateBrowserURL(ctx, "https://context-propagation.invalid/page"); !errors.Is(err, context.Canceled) {
		t.Fatalf("validateBrowserURL() error = %v, want context.Canceled", err)
	}
}
