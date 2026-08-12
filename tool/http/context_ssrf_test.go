package http

import (
	"context"
	"errors"
	stdhttp "net/http"
	"net/url"
	"testing"
)

func TestValidateURLSafetyUsesCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	target, err := url.Parse("https://context-propagation.invalid/resource")
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}

	if err := validateURLSafety(ctx, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("validateURLSafety() error = %v, want context.Canceled", err)
	}
}

func TestHTTPRedirectValidationUsesRequestContext(t *testing.T) {
	tool := NewHTTPTool()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, "https://context-propagation.invalid/redirect", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}

	if err := tool.client.CheckRedirect(request, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("redirect validation error = %v, want context.Canceled", err)
	}
}
