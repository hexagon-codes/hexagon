package otel

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	toolkitObserve "github.com/hexagon-codes/toolkit/infra/observe"

	"github.com/hexagon-codes/hexagon/observe/tracer"
)

// recordingExporter 在内存中记录导出结果，避免测试访问真实 OTLP 端点。
type recordingExporter struct {
	mu            sync.Mutex
	spans         []*SpanData
	shutdownCount int
}

type blockingShutdownExporter struct {
	shutdownStarted chan struct{}
	releaseShutdown chan struct{}
	startedOnce     sync.Once
	shutdownCalls   atomic.Int32
	shutdownErr     error
}

func (e *blockingShutdownExporter) ExportSpans(context.Context, []*SpanData) error {
	return nil
}

func (e *blockingShutdownExporter) Shutdown(context.Context) error {
	e.shutdownCalls.Add(1)
	e.startedOnce.Do(func() { close(e.shutdownStarted) })
	<-e.releaseShutdown
	return e.shutdownErr
}

func (e *recordingExporter) ExportSpans(_ context.Context, spans []*SpanData) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *recordingExporter) Shutdown(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.shutdownCount++
	return nil
}

func (e *recordingExporter) snapshot() ([]*SpanData, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	spans := append([]*SpanData(nil), e.spans...)
	return spans, e.shutdownCount
}

// TestOTelHexagonTracerSetExporterExportsSpan 锁定显式 exporter 注入与真实导出链路。
func TestOTelHexagonTracerSetExporterExportsSpan(t *testing.T) {
	ctx := context.Background()
	exporter := &recordingExporter{}
	tr := NewOTelHexagonTracer(WithTracerServiceName("test-service"))
	if err := tr.SetExporter(ctx, exporter); err != nil {
		t.Fatalf("SetExporter() error = %v", err)
	}

	fixedStart := time.Date(2026, time.August, 11, 9, 8, 7, 654321000, time.UTC)
	_, span := tr.StartSpan(ctx, "agent.run",
		tracer.WithSpanKind(tracer.SpanKindAgent),
		tracer.WithAttributes(map[string]any{"request.id": "req-1"}),
		tracer.WithStartTime(fixedStart),
	)
	span.SetInput("hello")
	span.SetOutput("world")
	span.SetTokenUsage(tracer.TokenUsage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5})
	span.AddEvent("response.received", "attempt", 1)
	span.SetStatus(tracer.StatusCodeOK, "success")
	span.End()
	span.End()

	spans, shutdownCount := exporter.snapshot()
	if shutdownCount != 0 {
		t.Fatalf("shutdown count before tracer shutdown = %d, want 0", shutdownCount)
	}
	if len(spans) != 1 {
		t.Fatalf("exported span count = %d, want 1", len(spans))
	}
	got := spans[0]
	if got.Name != "agent.run" {
		t.Errorf("exported span name = %q, want %q", got.Name, "agent.run")
	}
	if !got.StartTime.Equal(fixedStart) {
		t.Errorf("exported StartTime = %s, want %s", got.StartTime.Format(time.RFC3339Nano), fixedStart.Format(time.RFC3339Nano))
	}
	if got.Attributes["service.name"] != "test-service" {
		t.Errorf("service.name = %v, want %q", got.Attributes["service.name"], "test-service")
	}
	if got.Attributes["hexagon.span.kind"] != "agent" {
		t.Errorf("hexagon.span.kind = %v, want %q", got.Attributes["hexagon.span.kind"], "agent")
	}
	if got.Attributes["request.id"] != "req-1" {
		t.Errorf("request.id = %v, want %q", got.Attributes["request.id"], "req-1")
	}
	if got.Attributes["input"] != "hello" || got.Attributes["output"] != "world" {
		t.Errorf("exported input/output = %v/%v, want hello/world", got.Attributes["input"], got.Attributes["output"])
	}
	if got.Attributes[tracer.AttrLLMTotalTokens] != 5 {
		t.Errorf("llm.total_tokens = %v, want 5", got.Attributes[tracer.AttrLLMTotalTokens])
	}
	if len(got.Events) != 1 || got.Events[0].Name != "response.received" {
		t.Errorf("exported events = %#v, want one response.received event", got.Events)
	}
	if got.Status != toolkitObserve.StatusCodeOK || got.StatusMsg != "success" {
		t.Errorf("exported status = %v/%q, want OK/success", got.Status, got.StatusMsg)
	}

	if err := tr.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	_, shutdownCount = exporter.snapshot()
	if shutdownCount != 1 {
		t.Fatalf("shutdown count = %d, want 1", shutdownCount)
	}
}

// TestOTelHexagonTracerRejectsInvalidEndpointBeforeInjection 锁定端点配置的失败前置语义。
func TestOTelHexagonTracerRejectsInvalidEndpointBeforeInjection(t *testing.T) {
	exporter, err := NewOTLPExporter("localhost:4317")
	if exporter != nil {
		t.Fatalf("NewOTLPExporter() exporter = %T, want nil", exporter)
	}
	if !errors.Is(err, ErrInvalidExporterConfig) {
		t.Fatalf("NewOTLPExporter() error = %v, want ErrInvalidExporterConfig", err)
	}
}

// TestOTelHexagonTracerShutdownWaitsAndRemembersError 锁定并发等待与错误记忆语义。
func TestOTelHexagonTracerShutdownWaitsAndRemembersError(t *testing.T) {
	sentinel := errors.New("exporter shutdown failed")
	exporter := &blockingShutdownExporter{
		shutdownStarted: make(chan struct{}),
		releaseShutdown: make(chan struct{}),
		shutdownErr:     sentinel,
	}
	tr := NewOTelHexagonTracer()
	if err := tr.SetExporter(context.Background(), exporter); err != nil {
		t.Fatalf("SetExporter() error = %v", err)
	}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- tr.Shutdown(context.Background())
	}()
	select {
	case <-exporter.shutdownStarted:
	case <-time.After(time.Second):
		t.Fatal("first Shutdown() did not reach exporter")
	}

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- tr.Shutdown(context.Background())
	}()
	var secondErr error
	secondReturnedEarly := false
	select {
	case secondErr = <-secondDone:
		secondReturnedEarly = true
	case <-time.After(20 * time.Millisecond):
	}
	close(exporter.releaseShutdown)

	firstErr := <-firstDone
	if !secondReturnedEarly {
		secondErr = <-secondDone
	}
	if secondReturnedEarly {
		t.Fatal("concurrent Shutdown() returned before the shared shutdown completed")
	}
	if !errors.Is(firstErr, sentinel) || !errors.Is(secondErr, sentinel) {
		t.Fatalf("concurrent Shutdown() errors = %v / %v, want sentinel", firstErr, secondErr)
	}
	if err := tr.Shutdown(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("repeated Shutdown() error = %v, want sentinel", err)
	}
	if got := exporter.shutdownCalls.Load(); got != 1 {
		t.Fatalf("exporter Shutdown() calls = %d, want 1", got)
	}
	if _, span := tr.StartSpan(context.Background(), "after-shutdown"); span.IsRecording() {
		t.Fatal("StartSpan() after Shutdown() returned a recording span")
	}
}

// TestOTelHexagonTracerSetExporterAfterShutdown 锁定关闭后的所有权回收与错误链。
func TestOTelHexagonTracerSetExporterAfterShutdown(t *testing.T) {
	tr := NewOTelHexagonTracer()
	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	rejected := &recordingExporter{}
	err := tr.SetExporter(context.Background(), rejected)
	if !errors.Is(err, ErrTracerShutdown) {
		t.Fatalf("SetExporter() error = %v, want ErrTracerShutdown", err)
	}
	_, shutdownCount := rejected.snapshot()
	if shutdownCount != 1 {
		t.Fatalf("rejected exporter shutdown count = %d, want 1", shutdownCount)
	}
}
