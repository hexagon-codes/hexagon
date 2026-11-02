package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/plugin"
	rt "github.com/hexagon-codes/hexagon/runtime"
	"github.com/hexagon-codes/hexagon/runtime/middleware"
)

func TestModelSessionBudgetKeepsOriginalClock(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	provider := &modelSessionTestProvider{name: "budget", complete: func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
		return &llm.CompletionResponse{Content: "done"}, nil
	}}
	beforeLLMCalls := 0
	runner := rt.NewRunner(rt.Config{Middleware: []rt.Middleware{
		middleware.Budget{Limits: middleware.BudgetLimits{MaxDuration: time.Hour}, Now: func() time.Time { return now }},
		rt.MiddlewareFuncSet{BeforeLLMFunc: func(context.Context, *rt.State) error { beforeLLMCalls++; return nil }},
	}})
	session, err := runner.NewModelSession(t.Context(), rt.Request{ID: "session-budget-clock"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := session.InvokeComplete(t.Context(), provider, llm.CompletionRequest{}, "first"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := session.InvokeComplete(t.Context(), provider, llm.CompletionRequest{}, "second"); !errors.Is(err, rt.ErrBudgetExceeded) {
		t.Fatalf("elapsed budget was reset: %v", err)
	}
	if provider.calls.Load() != 1 || beforeLLMCalls != 0 {
		t.Fatalf("unexpected dispatch or recursive middleware: calls=%d before=%d", provider.calls.Load(), beforeLLMCalls)
	}
}

type modelSessionTestProvider struct {
	name     string
	calls    atomic.Int32
	complete func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error)
}

func (p *modelSessionTestProvider) Name() string { return p.name }
func (p *modelSessionTestProvider) Complete(ctx context.Context, request llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.calls.Add(1)
	return p.complete(ctx, request)
}
func (*modelSessionTestProvider) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, errors.New("stream is not used")
}

func TestModelSessionPreservesPassedProviderAndFailedUsage(t *testing.T) {
	failure := errors.New("summary response failed")
	usage := llm.Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}
	primary := &modelSessionTestProvider{name: "primary", complete: func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
		return nil, errors.New("wrong provider was used")
	}}
	actual := &modelSessionTestProvider{name: "summary", complete: func(_ context.Context, request llm.CompletionRequest) (*llm.CompletionResponse, error) {
		if request.Model != "summary-model" || request.MaxTokens != 23 || request.ToolChoice != "none" || request.ResponseFormat == nil || len(request.Tools) != 1 || request.Tools[0].Function.Name != "lookup" || request.Metadata["custom"] != "kept" {
			return nil, fmt.Errorf("request configuration was not preserved: %+v", request)
		}
		return &llm.CompletionResponse{ID: "summary-response", Content: "partial", Usage: usage, UsageReceipt: &usage}, failure
	}}
	runner := rt.NewRunner(rt.Config{ProviderSelector: rt.StaticProviderSelector{Provider: primary}})
	var session *rt.ModelSession
	session, err := runner.NewModelSession(t.Context(), rt.Request{ID: "child-summary", Metadata: map[string]any{"parent_run_id": "parent"}, StructuredOutput: &rt.StructuredOutputSpec{}}, rt.EventSinkFunc(func(_ context.Context, event rt.Event) error {
		if event.Type == rt.EventModelAttemptFinished {
			report := session.Report()
			if report.Usage.TotalTokens != 9 || len(report.Attempts) != 1 {
				return fmt.Errorf("observer did not receive the completed usage projection")
			}
			if event.State.Request.StructuredOutput != nil {
				return errors.New("summary inherited the main final schema")
			}
			if event.State.Request.Metadata["parent_run_id"] != "parent" {
				return errors.New("model session lost its parent correlation metadata")
			}
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	request := llm.CompletionRequest{Model: "summary-model", Messages: []llm.Message{llm.UserMessage("history")}, Tools: []llm.ToolDefinition{llm.NewToolDefinition("lookup", "description", &llm.Schema{Type: "object"})}, ToolChoice: "none", MaxTokens: 23, ResponseFormat: &llm.ResponseFormat{Type: "text"}, Metadata: map[string]any{"custom": "kept"}}
	response, err := llm.InvokeComplete(session.Context(t.Context()), actual, request, "context_compaction")
	if !errors.Is(err, failure) || response == nil || primary.calls.Load() != 0 || actual.calls.Load() != 1 {
		t.Fatalf("wrong provider/error: response=%+v err=%v primary=%d actual=%d", response, err, primary.calls.Load(), actual.calls.Load())
	}
	report := session.Report()
	if report.RunID != "child-summary" || report.ParentRunID != "parent" || report.Usage != usage || report.ReportedCalls != 1 || report.UnknownCalls != 0 || len(report.Attempts) != 1 || report.Attempts[0].Purpose != "context_compaction" || report.Attempts[0].Outcome != "failed" {
		t.Fatalf("failed usage facts were lost: %+v", report)
	}
	report.Attempts[0].Usage.TotalTokens = 999
	if session.Report().Attempts[0].Usage.TotalTokens != 9 || session.UsageSnapshot() != usage {
		t.Fatal("report aliases session facts or re-counts usage")
	}
}

type modelSessionBundle struct {
	*plugin.BasePlugin
	version string
	stops   atomic.Int32
	calls   atomic.Int32
}

func newModelSessionBundle(version string) *modelSessionBundle {
	return &modelSessionBundle{BasePlugin: plugin.NewBasePlugin(plugin.PluginInfo{Name: "session-resource", Version: version}), version: version}
}
func (p *modelSessionBundle) Name() string { return "resource-" + p.version }
func (p *modelSessionBundle) Complete(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.calls.Add(1)
	if p.stops.Load() != 0 {
		return nil, errors.New("resource was stopped")
	}
	usage := llm.Usage{TotalTokens: 2}
	return &llm.CompletionResponse{Content: p.version, Usage: usage, UsageReceipt: &usage}, nil
}
func (*modelSessionBundle) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, errors.New("stream is not used")
}
func (p *modelSessionBundle) Stop(context.Context) error { p.stops.Add(1); return nil }

func TestModelSessionPreparePinsResourceUntilClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	manager := plugin.NewPluginManager()
	old := newModelSessionBundle("v1")
	if err := manager.Load(old); err != nil {
		t.Fatal(err)
	}
	if err := manager.Enable(ctx, "session-resource", nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Disable(context.Background(), "session-resource"); err != nil {
			t.Error(err)
		}
	})
	var releases atomic.Int32
	runner := rt.NewRunner(rt.Config{PrepareRun: func(context.Context, rt.Request) (rt.PreparedRun, error) {
		lease, err := manager.Registry.Acquire("session-resource")
		if err != nil {
			return rt.PreparedRun{}, err
		}
		value, ok := lease.Plugin("session-resource")
		if !ok {
			lease.Release()
			return rt.PreparedRun{}, errors.New("resource absent")
		}
		provider := value.(*modelSessionBundle)
		return rt.PreparedRun{ProviderSelector: rt.StaticProviderSelector{Provider: provider, Model: "model-" + provider.version}, Release: func() { releases.Add(1); lease.Release() }}, nil
	}})
	session, err := runner.NewModelSession(ctx, rt.Request{ID: "pinned-summary"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	selection, err := session.SelectProvider(ctx)
	if err != nil || selection.Provider != old {
		t.Fatalf("wrong prepared binding: %+v %v", selection, err)
	}
	next := newModelSessionBundle("v2")
	retired, err := manager.ReplaceCandidate(ctx, "session-resource", next, nil)
	if err != nil {
		t.Fatal(err)
	}
	if old.stops.Load() != 0 {
		t.Fatal("publication stopped an in-use model session resource")
	}
	response, err := session.InvokeComplete(ctx, selection.Provider, llm.CompletionRequest{Model: selection.Model}, "summary")
	if err != nil || response.Content != "v1" {
		t.Fatalf("session did not retain prepared resource: %+v %v", response, err)
	}
	current, err := runner.NewModelSession(ctx, rt.Request{ID: "new-summary"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	selected, err := current.SelectProvider(ctx)
	if err != nil || selected.Provider != next {
		t.Fatalf("new session did not use published resource: %+v %v", selected, err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := retired.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if releases.Load() != 1 || old.stops.Load() != 1 {
		t.Fatalf("old pin released incorrectly: releases=%d stops=%d", releases.Load(), old.stops.Load())
	}
	if _, err := session.SelectProvider(ctx); !errors.Is(err, rt.ErrModelSessionClosed) {
		t.Fatalf("closed session resolved resources: %v", err)
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	if releases.Load() != 2 {
		t.Fatalf("release count=%d", releases.Load())
	}
}

func TestModelSessionCloseCancelsCooperativeCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{})
	usage := llm.Usage{TotalTokens: 4}
	provider := &modelSessionTestProvider{name: "cancel-summary", complete: func(callCtx context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
		close(entered)
		<-callCtx.Done()
		return &llm.CompletionResponse{Usage: usage, UsageReceipt: &usage}, callCtx.Err()
	}}
	var releases atomic.Int32
	runner := rt.NewRunner(rt.Config{PrepareRun: func(context.Context, rt.Request) (rt.PreparedRun, error) {
		return rt.PreparedRun{Release: func() { releases.Add(1) }}, nil
	}})
	session, err := runner.NewModelSession(ctx, rt.Request{ID: "cancel-summary"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := session.InvokeComplete(ctx, provider, llm.CompletionRequest{}, "summary")
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("call error=%v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if session.UsageSnapshot() != usage || releases.Load() != 1 {
		t.Fatalf("cancel lost usage or released twice: %+v %d", session.Report(), releases.Load())
	}
}

type modelSessionPanicNameProvider struct {
	nameCalls     atomic.Int32
	completeCalls atomic.Int32
}

func (p *modelSessionPanicNameProvider) Name() string {
	p.nameCalls.Add(1)
	panic("provider name failed")
}
func (p *modelSessionPanicNameProvider) Complete(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.completeCalls.Add(1)
	return nil, errors.New("unexpected complete")
}
func (*modelSessionPanicNameProvider) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, errors.New("stream is not used")
}

func TestModelSessionProviderNamePanicStillReleasesPin(t *testing.T) {
	var releases atomic.Int32
	runner := rt.NewRunner(rt.Config{PrepareRun: func(context.Context, rt.Request) (rt.PreparedRun, error) {
		return rt.PreparedRun{Release: func() { releases.Add(1) }}, nil
	}})
	session, err := runner.NewModelSession(t.Context(), rt.Request{ID: "name-panic-session"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider := &modelSessionPanicNameProvider{}
	_, err = session.InvokeComplete(t.Context(), provider, llm.CompletionRequest{}, "summary")
	var unsafe interface{ UnsafeToReplay() bool }
	if !errors.As(err, &unsafe) || !unsafe.UnsafeToReplay() || provider.nameCalls.Load() != 1 || provider.completeCalls.Load() != 0 {
		t.Fatalf("provider name panic escaped or dispatched: err=%v names=%d complete=%d", err, provider.nameCalls.Load(), provider.completeCalls.Load())
	}
	if report := session.Report(); len(report.Attempts) != 0 || report.ReportedCalls != 0 || report.UnknownCalls != 0 {
		t.Fatalf("name failure fabricated a model call: %+v", report)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if releases.Load() != 1 || !session.Report().Closed {
		t.Fatalf("panic prevented close: releases=%d report=%+v", releases.Load(), session.Report())
	}
}
