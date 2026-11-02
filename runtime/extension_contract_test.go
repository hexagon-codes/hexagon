package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

type panickingFallback struct{ provider Provider }

func (s panickingFallback) Select(context.Context, Request) (ProviderSelection, error) {
	return ProviderSelection{Provider: s.provider}, nil
}
func (panickingFallback) Fallback(context.Context, ProviderSelection, error) (ProviderSelection, error) {
	panic("fallback callback failed")
}

func TestExtensionFallbackPanicPreservesOriginalFailureAndUsage(t *testing.T) {
	primaryErr := errors.New("original provider failure")
	provider := &continuationProvider{complete: func(llm.CompletionRequest) (*llm.CompletionResponse, error) {
		return &llm.CompletionResponse{Usage: llm.Usage{TotalTokens: 3}}, primaryErr
	}}
	result, err := NewRunner(Config{ProviderSelector: panickingFallback{provider}}).Run(t.Context(), Request{ID: "fallback-panic"})
	var panicErr *ExtensionPanicError
	if !errors.Is(err, primaryErr) || !errors.As(err, &panicErr) || panicErr.Boundary != "provider.fallback" || len(panicErr.Stack) == 0 || result == nil || result.Usage.TotalTokens != 3 || result.StopReason != "" {
		t.Fatalf("panic or facts lost: result=%+v error=%v", result, err)
	}
}

type panickingClassifier struct{ calls int }

func (e *panickingClassifier) Execute(context.Context, llm.ToolCall) (ToolResult, error) {
	e.calls++
	return ToolResult{}, nil
}
func (*panickingClassifier) SideEffectOf(llm.ToolCall) ToolSideEffect { panic("classification failed") }

func TestExtensionClassifierPanicStopsBeforeToolDispatch(t *testing.T) {
	provider := &fakeProvider{responses: []*llm.CompletionResponse{{ToolCalls: []llm.ToolCall{{ID: "one", Name: "write", Arguments: "{}"}}, Usage: llm.Usage{TotalTokens: 3}}}}
	executor := &panickingClassifier{}
	result, err := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}, ToolExecutor: executor}).Run(t.Context(), Request{ID: "classifier-panic"})
	var panicErr *ExtensionPanicError
	if !errors.As(err, &panicErr) || panicErr.Boundary != "tool.side_effect" || executor.calls != 0 || result == nil || result.Usage.TotalTokens != 3 {
		t.Fatalf("classifier panic escaped or tool dispatched: calls=%d result=%+v error=%v", executor.calls, result, err)
	}
}

func TestExtensionPrepareCleanupPanicPreservesPreparationFailure(t *testing.T) {
	original := errors.New("preparation failed")
	releases := 0
	runner := NewRunner(Config{PrepareRun: func(context.Context, Request) (PreparedRun, error) {
		return PreparedRun{Release: func() { releases++; panic("release failed") }}, original
	}})
	_, err := runner.Run(t.Context(), Request{ID: "prepare-panic"})
	var panicErr *ExtensionPanicError
	if !errors.Is(err, original) || !errors.As(err, &panicErr) || releases != 1 {
		t.Fatalf("cleanup obscured failure or repeated: releases=%d error=%v", releases, err)
	}
}

func TestExtensionSnapshotMigrationPanicPreservesOriginalBytes(t *testing.T) {
	codecs := NewSnapshotCodecs()
	source := StateCodec{Type: "old", Version: "1"}
	data := []byte(`{"run_id":"original"}`)
	if err := codecs.Register(source, func(input []byte) ([]byte, error) { input[0] = '!'; panic("migration failed") }); err != nil {
		t.Fatal(err)
	}
	_, err := codecs.Decode(data, source, "original")
	var panicErr *ExtensionPanicError
	if !errors.As(err, &panicErr) || string(data) != `{"run_id":"original"}` {
		t.Fatalf("migration panic escaped or changed bytes: data=%s error=%v", data, err)
	}
}
