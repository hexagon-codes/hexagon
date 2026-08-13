package record

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

type recordContextKey struct{}

type legacyTokenProvider struct {
	legacyCalls int
	result      int
	err         error
}

func (p *legacyTokenProvider) Name() string {
	return "legacy"
}

func (p *legacyTokenProvider) Complete(
	context.Context,
	llm.CompletionRequest,
) (*llm.CompletionResponse, error) {
	return nil, nil
}

func (p *legacyTokenProvider) Stream(
	context.Context,
	llm.CompletionRequest,
) (*llm.Stream, error) {
	return nil, nil
}

func (p *legacyTokenProvider) Models() []llm.ModelInfo {
	return nil
}

func (p *legacyTokenProvider) CountTokens([]llm.Message) (int, error) {
	p.legacyCalls++
	return p.result, p.err
}

type contextTokenProvider struct {
	*legacyTokenProvider
	contextCalls     int
	receivedContext  context.Context
	receivedMessages []llm.Message
	contextResult    int
	contextErr       error
}

func (p *contextTokenProvider) CountTokensContext(
	ctx context.Context,
	messages []llm.Message,
) (int, error) {
	p.contextCalls++
	p.receivedContext = ctx
	p.receivedMessages = messages
	return p.contextResult, p.contextErr
}

func requireContextTokenCounter(t *testing.T, value any) llm.ContextTokenCounter {
	t.Helper()
	counter, ok := value.(llm.ContextTokenCounter)
	if !ok {
		t.Fatalf("value type %T does not preserve context-aware token counting", value)
	}
	return counter
}

func TestRecorderCountTokensContextDelegatesWithoutLegacyFallback(t *testing.T) {
	wantErr := errors.New("context token counting failed")
	provider := &contextTokenProvider{
		legacyTokenProvider: &legacyTokenProvider{},
		contextResult:       17,
		contextErr:          wantErr,
	}
	recorder := NewRecorder(provider, "context-aware")
	ctx := context.WithValue(t.Context(), recordContextKey{}, "request")
	messages := []llm.Message{{Role: llm.RoleUser, Content: "count me"}}

	count, err := requireContextTokenCounter(t, recorder).CountTokensContext(ctx, messages)
	if count != 17 {
		t.Fatalf("CountTokensContext() count = %d, want 17", count)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("CountTokensContext() error = %v, want %v", err, wantErr)
	}
	if provider.contextCalls != 1 || provider.legacyCalls != 0 {
		t.Fatalf("context calls = %d, legacy calls = %d, want 1/0", provider.contextCalls, provider.legacyCalls)
	}
	if provider.receivedContext != ctx {
		t.Fatal("CountTokensContext() did not preserve the original context")
	}
	if !reflect.DeepEqual(provider.receivedMessages, messages) {
		t.Fatalf("CountTokensContext() messages = %#v, want %#v", provider.receivedMessages, messages)
	}
}

func TestRecorderCountTokensContextRejectsLegacyProvider(t *testing.T) {
	provider := &legacyTokenProvider{result: 23}
	recorder := NewRecorder(provider, "legacy")

	count, err := requireContextTokenCounter(t, recorder).CountTokensContext(t.Context(), nil)
	if count != 0 {
		t.Fatalf("CountTokensContext() count = %d, want 0", count)
	}
	if !errors.Is(err, llm.ErrContextTokenCountingUnsupported) {
		t.Fatalf("CountTokensContext() error = %v, want unsupported", err)
	}
	if provider.legacyCalls != 0 {
		t.Fatalf("legacy CountTokens() calls = %d, want 0", provider.legacyCalls)
	}
}

func TestRecorderCountTokensContextHonorsPreCanceledContext(t *testing.T) {
	provider := &contextTokenProvider{legacyTokenProvider: &legacyTokenProvider{}}
	recorder := NewRecorder(provider, "canceled")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := requireContextTokenCounter(t, recorder).CountTokensContext(ctx, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CountTokensContext() error = %v, want context.Canceled", err)
	}
	if provider.contextCalls != 0 || provider.legacyCalls != 0 {
		t.Fatalf("context calls = %d, legacy calls = %d, want 0/0", provider.contextCalls, provider.legacyCalls)
	}
}

func TestReplayerCountTokensContextDelegatesToAwareFallback(t *testing.T) {
	provider := &contextTokenProvider{
		legacyTokenProvider: &legacyTokenProvider{},
		contextResult:       31,
	}
	replayer := NewReplayer(NewCassette("aware"), WithFallbackProvider(provider))
	ctx := context.WithValue(t.Context(), recordContextKey{}, "replay")
	messages := []llm.Message{{Role: llm.RoleUser, Content: "replay count"}}

	count, err := requireContextTokenCounter(t, replayer).CountTokensContext(ctx, messages)
	if err != nil {
		t.Fatalf("CountTokensContext() error = %v", err)
	}
	if count != 31 {
		t.Fatalf("CountTokensContext() count = %d, want 31", count)
	}
	if provider.contextCalls != 1 || provider.legacyCalls != 0 {
		t.Fatalf("context calls = %d, legacy calls = %d, want 1/0", provider.contextCalls, provider.legacyCalls)
	}
	if provider.receivedContext != ctx || !reflect.DeepEqual(provider.receivedMessages, messages) {
		t.Fatal("CountTokensContext() did not preserve the original context and messages")
	}
}

func TestReplayerCountTokensContextRejectsLegacyFallback(t *testing.T) {
	provider := &legacyTokenProvider{result: 37}
	replayer := NewReplayer(NewCassette("legacy"), WithFallbackProvider(provider))

	count, err := requireContextTokenCounter(t, replayer).CountTokensContext(t.Context(), nil)
	if count != 0 {
		t.Fatalf("CountTokensContext() count = %d, want 0", count)
	}
	if !errors.Is(err, llm.ErrContextTokenCountingUnsupported) {
		t.Fatalf("CountTokensContext() error = %v, want unsupported", err)
	}
	if provider.legacyCalls != 0 {
		t.Fatalf("legacy CountTokens() calls = %d, want 0", provider.legacyCalls)
	}
}

func TestReplayerCountTokensContextHonorsPreCanceledContextBeforeFallback(t *testing.T) {
	provider := &contextTokenProvider{legacyTokenProvider: &legacyTokenProvider{}}
	replayer := NewReplayer(NewCassette("canceled-fallback"), WithFallbackProvider(provider))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := requireContextTokenCounter(t, replayer).CountTokensContext(ctx, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CountTokensContext() error = %v, want context.Canceled", err)
	}
	if provider.contextCalls != 0 || provider.legacyCalls != 0 {
		t.Fatalf("context calls = %d, legacy calls = %d, want 0/0", provider.contextCalls, provider.legacyCalls)
	}
}

func TestReplayerCountTokensContextEstimatesWithoutFallback(t *testing.T) {
	replayer := NewReplayer(NewCassette("estimate"))
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "12345678"},
		{Role: llm.RoleAssistant, Content: "1234"},
	}

	count, err := requireContextTokenCounter(t, replayer).CountTokensContext(t.Context(), messages)
	if err != nil {
		t.Fatalf("CountTokensContext() error = %v", err)
	}
	if count != 3 {
		t.Fatalf("CountTokensContext() count = %d, want 3", count)
	}
}

func TestReplayerCountTokensContextHonorsPreCanceledContext(t *testing.T) {
	replayer := NewReplayer(NewCassette("canceled"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := requireContextTokenCounter(t, replayer).CountTokensContext(ctx, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CountTokensContext() error = %v, want context.Canceled", err)
	}
}
