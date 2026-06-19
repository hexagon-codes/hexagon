// Package otel 提供 OpenTelemetry 集成
//
// 本包提供完整的 OpenTelemetry 追踪集成，包括：
//   - OTelHexagonTracer: 实现 Hexagon Tracer 接口的 OTel 追踪器
//   - TracingHook: 基于钩子的自动追踪
//   - 上下文传播支持
//
// 使用示例:
//
//	// 创建 OTel 追踪器
//	tracer := otel.NewOTelHexagonTracer(
//	    otel.WithServiceName("my-agent"),
//	    otel.WithEndpoint("localhost:4317"),
//	)
//
//	// 注册追踪钩子
//	hooks.RegisterRunHook(otel.NewTracingRunHook(tracer))
//	hooks.RegisterToolHook(otel.NewTracingToolHook(tracer))
package otel

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hexagon-codes/hexagon/hooks"
	"github.com/hexagon-codes/hexagon/observe/tracer"
	"github.com/hexagon-codes/toolkit/util/idgen"
)

// ============== OTelHexagonTracer ==============

// OTelHexagonTracer 实现 Hexagon Tracer 接口的 OTel 追踪器
// 将 Hexagon 的追踪抽象映射到 OTel 标准
type OTelHexagonTracer struct {
	config     *TracerConfig
	otelTracer *OTelTracer
	spans      sync.Map   // traceID -> []*OTelHexagonSpan
	spansMu    sync.Mutex // 保护对 spans 中切片值的「读-改-写」追加操作
	closed     int32
}

// TracerConfig 追踪器配置
type TracerConfig struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	Endpoint       string
	SamplingRate   float64
	Exporters      []Exporter
	Propagator     Propagator
}

// TracerOption 追踪器选项
type TracerOption func(*TracerConfig)

// WithTracerServiceName 设置服务名称
func WithTracerServiceName(name string) TracerOption {
	return func(c *TracerConfig) {
		c.ServiceName = name
	}
}

// WithTracerServiceVersion 设置服务版本
func WithTracerServiceVersion(version string) TracerOption {
	return func(c *TracerConfig) {
		c.ServiceVersion = version
	}
}

// WithTracerEnvironment 设置环境
func WithTracerEnvironment(env string) TracerOption {
	return func(c *TracerConfig) {
		c.Environment = env
	}
}

// WithTracerEndpoint 设置 OTLP 端点
func WithTracerEndpoint(endpoint string) TracerOption {
	return func(c *TracerConfig) {
		c.Endpoint = endpoint
	}
}

// WithTracerSamplingRate 设置采样率
func WithTracerSamplingRate(rate float64) TracerOption {
	return func(c *TracerConfig) {
		c.SamplingRate = rate
	}
}

// WithTracerExporters 设置导出器
func WithTracerExporters(exporters ...Exporter) TracerOption {
	return func(c *TracerConfig) {
		c.Exporters = exporters
	}
}

// WithTracerPropagator 设置传播器
func WithTracerPropagator(propagator Propagator) TracerOption {
	return func(c *TracerConfig) {
		c.Propagator = propagator
	}
}

// NewOTelHexagonTracer 创建 OTel Hexagon 追踪器
func NewOTelHexagonTracer(opts ...TracerOption) *OTelHexagonTracer {
	config := &TracerConfig{
		ServiceName:  "hexagon-agent",
		Environment:  "development",
		SamplingRate: 1.0,
	}
	for _, opt := range opts {
		opt(config)
	}

	// 创建底层 OTel 追踪器
	otelOpts := []OTelOption{
		WithServiceName(config.ServiceName),
		WithServiceVersion(config.ServiceVersion),
		WithEnvironment(config.Environment),
		WithSamplingRate(config.SamplingRate),
	}
	if config.Endpoint != "" {
		otelOpts = append(otelOpts, WithEndpoint(config.Endpoint))
	}

	otelTracer := NewOTelTracer(otelOpts...)

	return &OTelHexagonTracer{
		config:     config,
		otelTracer: otelTracer,
	}
}

// StartSpan 开始一个新的 Span
func (t *OTelHexagonTracer) StartSpan(ctx context.Context, name string, opts ...tracer.SpanOption) (context.Context, tracer.Span) {
	if atomic.LoadInt32(&t.closed) == 1 {
		return ctx, &noopSpan{}
	}

	config := &tracer.SpanConfig{
		Attributes: make(map[string]any),
	}
	for _, opt := range opts {
		opt(config)
	}

	// 获取或创建 Trace ID
	traceID := t.ExtractTraceID(ctx)
	if traceID == "" {
		traceID = idgen.NanoID()
	}

	// 创建 Span ID
	spanID := idgen.ShortID()

	// 获取父 Span
	var parentSpanID string
	if parentSpan := tracer.SpanFromContext(ctx); parentSpan != nil {
		parentSpanID = parentSpan.SpanID()
	}

	// 创建 Hexagon Span
	span := &OTelHexagonSpan{
		tracer:       t,
		traceID:      traceID,
		spanID:       spanID,
		parentSpanID: parentSpanID,
		name:         name,
		kind:         config.Kind,
		attributes:   config.Attributes,
		events:       make([]spanEvent, 0),
		startTime:    config.StartTime,
		recording:    true,
	}

	if span.startTime.IsZero() {
		span.startTime = time.Now()
	}

	// 设置 Span 类型属性
	span.SetAttribute("hexagon.span.kind", spanKindString(config.Kind))

	// 将 Span 存储到追踪器
	t.storeSpan(traceID, span)

	// 将 Span 添加到 context
	ctx = t.InjectTraceID(ctx, traceID)
	ctx = tracer.ContextWithSpan(ctx, span)

	return ctx, span
}

// ExtractTraceID 从 context 中提取 Trace ID
func (t *OTelHexagonTracer) ExtractTraceID(ctx context.Context) string {
	if traceID, ok := ctx.Value(traceIDKey{}).(string); ok {
		return traceID
	}
	return ""
}

// InjectTraceID 将 Trace ID 注入到 context 中
func (t *OTelHexagonTracer) InjectTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

// Shutdown 关闭追踪器
func (t *OTelHexagonTracer) Shutdown(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&t.closed, 0, 1) {
		return nil
	}
	if t.otelTracer != nil {
		return t.otelTracer.Shutdown(ctx)
	}
	return nil
}

// GetSpans 获取指定 Trace ID 的所有 Span
//
// 返回内部切片的浅拷贝（copy-on-read），避免调用方与 storeSpan 的并发追加竞争，
// 也防止调用方修改返回切片污染内部状态。
func (t *OTelHexagonTracer) GetSpans(traceID string) []*OTelHexagonSpan {
	t.spansMu.Lock()
	defer t.spansMu.Unlock()
	if spans, ok := t.spans.Load(traceID); ok {
		src := spans.([]*OTelHexagonSpan)
		out := make([]*OTelHexagonSpan, len(src))
		copy(out, src)
		return out
	}
	return nil
}

// storeSpan 存储 Span
//
// 实现说明：sync.Map 的 value 是 []*OTelHexagonSpan 切片，而切片不可比较，
// 因此不能用 CompareAndSwap 做乐观更新（CAS 内部用 == 比较旧值会 panic
// "comparing uncomparable type"）。这里改用专用互斥锁保护「读-改-写」追加，
// 既避免 CAS panic，又保证同一 traceID 并发追加 span 的写入安全与计数一致。
func (t *OTelHexagonTracer) storeSpan(traceID string, span *OTelHexagonSpan) {
	t.spansMu.Lock()
	defer t.spansMu.Unlock()

	if existing, ok := t.spans.Load(traceID); ok {
		spans := existing.([]*OTelHexagonSpan)
		t.spans.Store(traceID, append(spans, span))
		return
	}
	t.spans.Store(traceID, []*OTelHexagonSpan{span})
}

type traceIDKey struct{}

// ============== OTelHexagonSpan ==============

// spanEvent Span 事件
type spanEvent struct {
	Name       string
	Timestamp  time.Time
	Attributes map[string]any
}

// OTelHexagonSpan 实现 tracer.Span 接口
type OTelHexagonSpan struct {
	tracer       *OTelHexagonTracer
	traceID      string
	spanID       string
	parentSpanID string
	name         string
	kind         tracer.SpanKind
	attributes   map[string]any
	events       []spanEvent
	input        any
	output       any
	tokenUsage   tracer.TokenUsage
	statusCode   tracer.StatusCode
	statusMsg    string
	startTime    time.Time
	endTime      time.Time
	recording    bool
	mu           sync.RWMutex
}

// SpanID 返回 Span ID
func (s *OTelHexagonSpan) SpanID() string {
	return s.spanID
}

// TraceID 返回 Trace ID
func (s *OTelHexagonSpan) TraceID() string {
	return s.traceID
}

// SetName 设置 Span 名称
func (s *OTelHexagonSpan) SetName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.name = name
}

// SetInput 设置输入
func (s *OTelHexagonSpan) SetInput(input any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.input = input
}

// SetOutput 设置输出
func (s *OTelHexagonSpan) SetOutput(output any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.output = output
}

// SetTokenUsage 设置 Token 使用量
func (s *OTelHexagonSpan) SetTokenUsage(usage tracer.TokenUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenUsage = usage
	s.attributes[tracer.AttrLLMPromptTokens] = usage.PromptTokens
	s.attributes[tracer.AttrLLMCompletionTokens] = usage.CompletionTokens
	s.attributes[tracer.AttrLLMTotalTokens] = usage.TotalTokens
}

// SetAttribute 设置属性
func (s *OTelHexagonSpan) SetAttribute(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attributes[key] = value
}

// SetAttributes 批量设置属性
func (s *OTelHexagonSpan) SetAttributes(attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range attrs {
		s.attributes[k] = v
	}
}

// AddEvent 添加事件
func (s *OTelHexagonSpan) AddEvent(name string, attrs ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	event := spanEvent{
		Name:       name,
		Timestamp:  time.Now(),
		Attributes: make(map[string]any),
	}

	// 解析属性对
	for i := 0; i < len(attrs)-1; i += 2 {
		if key, ok := attrs[i].(string); ok {
			event.Attributes[key] = attrs[i+1]
		}
	}

	s.events = append(s.events, event)
}

// RecordError 记录错误
func (s *OTelHexagonSpan) RecordError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.attributes[tracer.AttrErrorType] = fmt.Sprintf("%T", err)
	s.attributes[tracer.AttrErrorMessage] = err.Error()
	s.statusCode = tracer.StatusCodeError
	s.statusMsg = err.Error()

	s.events = append(s.events, spanEvent{
		Name:      "exception",
		Timestamp: time.Now(),
		Attributes: map[string]any{
			"exception.type":    fmt.Sprintf("%T", err),
			"exception.message": err.Error(),
		},
	})
}

// SetStatus 设置状态
func (s *OTelHexagonSpan) SetStatus(code tracer.StatusCode, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusCode = code
	s.statusMsg = message
}

// End 结束 Span
func (s *OTelHexagonSpan) End() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.recording {
		return
	}
	s.recording = false
	s.endTime = time.Now()
}

// EndWithError 结束 Span 并记录错误
func (s *OTelHexagonSpan) EndWithError(err error) {
	s.RecordError(err)
	s.End()
}

// IsRecording 是否正在记录
func (s *OTelHexagonSpan) IsRecording() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recording
}

// Duration 返回 Span 持续时间
func (s *OTelHexagonSpan) Duration() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.endTime.IsZero() {
		return time.Since(s.startTime)
	}
	return s.endTime.Sub(s.startTime)
}

// Attributes 返回所有属性
func (s *OTelHexagonSpan) Attributes() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	attrs := make(map[string]any, len(s.attributes))
	for k, v := range s.attributes {
		attrs[k] = v
	}
	return attrs
}

// noopSpan 空操作 Span
type noopSpan struct{}

func (s *noopSpan) SpanID() string                                   { return "" }
func (s *noopSpan) TraceID() string                                  { return "" }
func (s *noopSpan) SetName(name string)                              {}
func (s *noopSpan) SetInput(input any)                               {}
func (s *noopSpan) SetOutput(output any)                             {}
func (s *noopSpan) SetTokenUsage(usage tracer.TokenUsage)            {}
func (s *noopSpan) SetAttribute(key string, value any)               {}
func (s *noopSpan) SetAttributes(attrs map[string]any)               {}
func (s *noopSpan) AddEvent(name string, attrs ...any)               {}
func (s *noopSpan) RecordError(err error)                            {}
func (s *noopSpan) SetStatus(code tracer.StatusCode, message string) {}
func (s *noopSpan) End()                                             {}
func (s *noopSpan) EndWithError(err error)                           {}
func (s *noopSpan) IsRecording() bool                                { return false }

// spanKindString 返回 SpanKind 的字符串表示
func spanKindString(kind tracer.SpanKind) string {
	switch kind {
	case tracer.SpanKindInternal:
		return "internal"
	case tracer.SpanKindAgent:
		return "agent"
	case tracer.SpanKindLLM:
		return "llm"
	case tracer.SpanKindTool:
		return "tool"
	case tracer.SpanKindRetrieval:
		return "retrieval"
	case tracer.SpanKindEmbedding:
		return "embedding"
	default:
		return "unknown"
	}
}

// ============== Tracing Hooks ==============

// TracingRunHook 基于 OTel 的运行追踪钩子
type TracingRunHook struct {
	tracer  *OTelHexagonTracer
	spans   sync.Map // runID -> Span
	enabled bool
}

// NewTracingRunHook 创建运行追踪钩子
func NewTracingRunHook(tracer *OTelHexagonTracer) *TracingRunHook {
	return &TracingRunHook{
		tracer:  tracer,
		enabled: true,
	}
}

// Name 返回钩子名称
func (h *TracingRunHook) Name() string { return "otel-tracing-run" }

// Enabled 返回钩子是否启用
func (h *TracingRunHook) Enabled() bool { return h.enabled }

// Timings 返回关心的时机
func (h *TracingRunHook) Timings() hooks.Timing {
	return hooks.TimingRunStart | hooks.TimingRunEnd | hooks.TimingRunError
}

// OnStart 运行开始时创建 Span
func (h *TracingRunHook) OnStart(ctx context.Context, event *hooks.RunStartEvent) error {
	// 注意：RunStartEvent 仅携带 AgentID，不含真实 Agent 名称。
	// 不能把 AgentID 复制进 agent.name —— 那是误导性的「假名称」，会丢失/掩盖真实名称。
	// 此处只写 agent.id；待事件层补充 AgentName 后再补写 agent.name。
	_, span := h.tracer.StartSpan(ctx, "agent.run",
		tracer.WithSpanKind(tracer.SpanKindAgent),
		tracer.WithAttributes(map[string]any{
			tracer.AttrAgentID: event.AgentID,
		}),
	)
	span.SetInput(event.Input)
	h.spans.Store(event.RunID, span)
	return nil
}

// OnEnd 运行结束时结束 Span
func (h *TracingRunHook) OnEnd(ctx context.Context, event *hooks.RunEndEvent) error {
	if spanI, ok := h.spans.LoadAndDelete(event.RunID); ok {
		span := spanI.(tracer.Span)
		span.SetOutput(event.Output)
		span.SetStatus(tracer.StatusCodeOK, "success")
		span.End()
	}
	return nil
}

// OnError 运行错误时记录错误
func (h *TracingRunHook) OnError(ctx context.Context, event *hooks.ErrorEvent) error {
	if spanI, ok := h.spans.LoadAndDelete(event.RunID); ok {
		span := spanI.(tracer.Span)
		span.EndWithError(event.Error)
	}
	return nil
}

// TracingToolHook 基于 OTel 的工具追踪钩子
type TracingToolHook struct {
	tracer  *OTelHexagonTracer
	spans   sync.Map // toolID -> Span
	enabled bool
}

// NewTracingToolHook 创建工具追踪钩子
func NewTracingToolHook(tracer *OTelHexagonTracer) *TracingToolHook {
	return &TracingToolHook{
		tracer:  tracer,
		enabled: true,
	}
}

// Name 返回钩子名称
func (h *TracingToolHook) Name() string { return "otel-tracing-tool" }

// Enabled 返回钩子是否启用
func (h *TracingToolHook) Enabled() bool { return h.enabled }

// Timings 返回关心的时机
func (h *TracingToolHook) Timings() hooks.Timing {
	return hooks.TimingToolStart | hooks.TimingToolEnd
}

// OnToolStart 工具开始时创建 Span
func (h *TracingToolHook) OnToolStart(ctx context.Context, event *hooks.ToolStartEvent) error {
	_, span := h.tracer.StartSpan(ctx, "tool.execute",
		tracer.WithSpanKind(tracer.SpanKindTool),
		tracer.WithAttributes(map[string]any{
			tracer.AttrToolName: event.ToolName,
		}),
	)
	span.SetInput(event.Input)
	h.spans.Store(event.ToolID, span)
	return nil
}

// OnToolEnd 工具结束时结束 Span
func (h *TracingToolHook) OnToolEnd(ctx context.Context, event *hooks.ToolEndEvent) error {
	if spanI, ok := h.spans.LoadAndDelete(event.ToolID); ok {
		span := spanI.(tracer.Span)
		span.SetOutput(event.Output)
		if event.Error != nil {
			span.EndWithError(event.Error)
		} else {
			span.SetStatus(tracer.StatusCodeOK, "success")
			span.End()
		}
	}
	return nil
}

// TracingLLMHook 基于 OTel 的 LLM 追踪钩子
type TracingLLMHook struct {
	tracer  *OTelHexagonTracer
	spans   sync.Map // requestID -> Span
	enabled bool
}

// NewTracingLLMHook 创建 LLM 追踪钩子
func NewTracingLLMHook(tracer *OTelHexagonTracer) *TracingLLMHook {
	return &TracingLLMHook{
		tracer:  tracer,
		enabled: true,
	}
}

// Name 返回钩子名称
func (h *TracingLLMHook) Name() string { return "otel-tracing-llm" }

// Enabled 返回钩子是否启用
func (h *TracingLLMHook) Enabled() bool { return h.enabled }

// Timings 返回关心的时机
func (h *TracingLLMHook) Timings() hooks.Timing {
	return hooks.TimingLLMStart | hooks.TimingLLMEnd | hooks.TimingLLMStream
}

// OnLLMStart LLM 开始时创建 Span
func (h *TracingLLMHook) OnLLMStart(ctx context.Context, event *hooks.LLMStartEvent) error {
	_, span := h.tracer.StartSpan(ctx, "llm.complete",
		tracer.WithSpanKind(tracer.SpanKindLLM),
		tracer.WithAttributes(map[string]any{
			tracer.AttrLLMProvider: event.Provider,
			tracer.AttrLLMModel:    event.Model,
		}),
	)
	span.SetInput(event.Messages)
	h.spans.Store(event.RequestID, span)
	return nil
}

// OnLLMEnd LLM 结束时结束 Span
func (h *TracingLLMHook) OnLLMEnd(ctx context.Context, event *hooks.LLMEndEvent) error {
	if spanI, ok := h.spans.LoadAndDelete(event.RequestID); ok {
		span := spanI.(tracer.Span)
		span.SetOutput(event.Response)
		span.SetTokenUsage(tracer.TokenUsage{
			PromptTokens:     event.PromptTokens,
			CompletionTokens: event.CompletionTokens,
			TotalTokens:      event.PromptTokens + event.CompletionTokens,
		})
		if event.Error != nil {
			span.EndWithError(event.Error)
		} else {
			span.SetStatus(tracer.StatusCodeOK, "success")
			span.End()
		}
	}
	return nil
}

// OnLLMStream LLM 流式输出时添加事件
func (h *TracingLLMHook) OnLLMStream(ctx context.Context, event *hooks.LLMStreamEvent) error {
	if spanI, ok := h.spans.Load(event.RequestID); ok {
		span := spanI.(tracer.Span)
		span.AddEvent("llm.chunk",
			"chunk_index", event.ChunkIndex,
			"chunk_content", event.Content,
		)
	}
	return nil
}

// TracingRetrieverHook 基于 OTel 的检索追踪钩子
type TracingRetrieverHook struct {
	tracer  *OTelHexagonTracer
	spans   sync.Map // queryID -> Span
	enabled bool
}

// NewTracingRetrieverHook 创建检索追踪钩子
func NewTracingRetrieverHook(tracer *OTelHexagonTracer) *TracingRetrieverHook {
	return &TracingRetrieverHook{
		tracer:  tracer,
		enabled: true,
	}
}

// Name 返回钩子名称
func (h *TracingRetrieverHook) Name() string { return "otel-tracing-retriever" }

// Enabled 返回钩子是否启用
func (h *TracingRetrieverHook) Enabled() bool { return h.enabled }

// Timings 返回关心的时机
func (h *TracingRetrieverHook) Timings() hooks.Timing {
	return hooks.TimingRetrieverStart | hooks.TimingRetrieverEnd
}

// OnRetrieverStart 检索开始时创建 Span
func (h *TracingRetrieverHook) OnRetrieverStart(ctx context.Context, event *hooks.RetrieverStartEvent) error {
	_, span := h.tracer.StartSpan(ctx, "retriever.search",
		tracer.WithSpanKind(tracer.SpanKindRetrieval),
		tracer.WithAttributes(map[string]any{
			tracer.AttrRetrievalQuery: event.Query,
			tracer.AttrRetrievalTopK:  event.TopK,
		}),
	)
	h.spans.Store(event.QueryID, span)
	return nil
}

// OnRetrieverEnd 检索结束时结束 Span
func (h *TracingRetrieverHook) OnRetrieverEnd(ctx context.Context, event *hooks.RetrieverEndEvent) error {
	if spanI, ok := h.spans.LoadAndDelete(event.QueryID); ok {
		span := spanI.(tracer.Span)
		span.SetAttribute(tracer.AttrRetrievalDocCount, event.DocCount)
		span.SetOutput(event.Documents)
		if event.Error != nil {
			span.EndWithError(event.Error)
		} else {
			span.SetStatus(tracer.StatusCodeOK, "success")
			span.End()
		}
	}
	return nil
}

// ============== 快捷函数 ==============

// SetupTracing 设置完整追踪
// 创建追踪器并注册所有追踪钩子
func SetupTracing(manager *hooks.Manager, opts ...TracerOption) (*OTelHexagonTracer, error) {
	tracer := NewOTelHexagonTracer(opts...)

	manager.RegisterRunHook(NewTracingRunHook(tracer))
	manager.RegisterToolHook(NewTracingToolHook(tracer))
	manager.RegisterLLMHook(NewTracingLLMHook(tracer))
	manager.RegisterRetrieverHook(NewTracingRetrieverHook(tracer))

	return tracer, nil
}
