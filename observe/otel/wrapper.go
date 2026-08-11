// Package otel 提供 OpenTelemetry 集成
//
// 本包重新导出 toolkit/infra/otel 的实现，保持向后兼容性。
//
// 使用示例:
//
//	tracer := otel.NewTracer(
//	    otel.WithServiceName("my-service"),
//	)
//	defer tracer.Shutdown(context.Background())
//
//	exporter, err := otel.NewOTLPExporter("https://collector:4318")
//	if err != nil {
//	    // 处理错误
//	}
//	_ = tracer.SetExporter(context.Background(), exporter)
package otel

import (
	toolkitOtel "github.com/hexagon-codes/toolkit/infra/otel"
)

// 重新导出类型
type (
	// Tracer OpenTelemetry 追踪器（toolkit v0.3.0 新名称）
	Tracer = toolkitOtel.Tracer

	// Config OpenTelemetry 配置（toolkit v0.3.0 新名称）
	Config = toolkitOtel.Config

	// Option 配置选项（toolkit v0.3.0 新名称）
	Option = toolkitOtel.Option

	// Span OpenTelemetry Span（toolkit v0.3.0 新名称）
	Span = toolkitOtel.Span

	// OTelTracer OpenTelemetry 追踪器（旧名兼容）
	OTelTracer = toolkitOtel.Tracer

	// OTelConfig OpenTelemetry 配置（旧名兼容）
	OTelConfig = toolkitOtel.Config

	// OTelOption 配置选项（旧名兼容）
	OTelOption = toolkitOtel.Option

	// OTelSpan OpenTelemetry Span（旧名兼容）
	OTelSpan = toolkitOtel.Span

	// SpanData 导出数据
	SpanData = toolkitOtel.SpanData

	// SpanEvent Span 事件
	SpanEvent = toolkitOtel.SpanEvent

	// Exporter 导出器接口
	Exporter = toolkitOtel.Exporter

	// ConsoleExporter 控制台导出器
	ConsoleExporter = toolkitOtel.ConsoleExporter

	// OTLPExporter OTLP 导出器
	OTLPExporter = toolkitOtel.OTLPExporter

	// OTLPExporterOption OTLP 导出器选项
	OTLPExporterOption = toolkitOtel.OTLPExporterOption

	// JaegerExporter Jaeger 导出器
	JaegerExporter = toolkitOtel.JaegerExporter

	// ZipkinExporter Zipkin 导出器
	ZipkinExporter = toolkitOtel.ZipkinExporter

	// MultiExporter 多导出器
	MultiExporter = toolkitOtel.MultiExporter

	// Sampler 采样器接口
	Sampler = toolkitOtel.Sampler

	// AlwaysSampler 总是采样
	AlwaysSampler = toolkitOtel.AlwaysSampler

	// NeverSampler 从不采样
	NeverSampler = toolkitOtel.NeverSampler

	// ProbabilitySampler 概率采样器
	ProbabilitySampler = toolkitOtel.ProbabilitySampler

	// RateLimitingSampler 限流采样器
	RateLimitingSampler = toolkitOtel.RateLimitingSampler

	// Propagator 传播器接口
	Propagator = toolkitOtel.Propagator

	// Carrier 载体接口
	Carrier = toolkitOtel.Carrier

	// MapCarrier map 载体
	MapCarrier = toolkitOtel.MapCarrier

	// W3CTraceContextPropagator W3C 传播器
	W3CTraceContextPropagator = toolkitOtel.W3CTraceContextPropagator

	// B3Propagator B3 传播器
	B3Propagator = toolkitOtel.B3Propagator

	// CompositePropagator 组合传播器
	CompositePropagator = toolkitOtel.CompositePropagator
)

// 重新导出函数
var (
	// NewTracer 创建 OpenTelemetry 追踪器（toolkit v0.3.0 新名称）
	NewTracer = toolkitOtel.NewTracer

	// DefaultConfig 返回默认配置（toolkit v0.3.0 新名称）
	DefaultConfig = toolkitOtel.DefaultConfig

	// NewOTelTracer 创建 OpenTelemetry 追踪器（旧名兼容）
	NewOTelTracer = toolkitOtel.NewTracer

	// DefaultOTelConfig 返回默认配置（旧名兼容）
	DefaultOTelConfig = toolkitOtel.DefaultConfig

	// WithServiceName 设置服务名称
	WithServiceName = toolkitOtel.WithServiceName

	// WithServiceVersion 设置服务版本
	WithServiceVersion = toolkitOtel.WithServiceVersion

	// WithEnvironment 设置环境
	WithEnvironment = toolkitOtel.WithEnvironment

	// WithSamplingRate 设置采样率
	WithSamplingRate = toolkitOtel.WithSamplingRate

	// NewConsoleExporter 创建控制台导出器
	NewConsoleExporter = toolkitOtel.NewConsoleExporter

	// NewOTLPExporter 创建 OTLP 导出器
	NewOTLPExporter = toolkitOtel.NewOTLPExporter

	// WithOTLPHeaders 设置 OTLP 请求头
	WithOTLPHeaders = toolkitOtel.WithOTLPHeaders

	// WithOTLPBatchSize 设置 OTLP 批量大小
	WithOTLPBatchSize = toolkitOtel.WithOTLPBatchSize

	// NewJaegerExporter 创建 Jaeger 导出器
	NewJaegerExporter = toolkitOtel.NewJaegerExporter

	// NewZipkinExporter 创建 Zipkin 导出器
	NewZipkinExporter = toolkitOtel.NewZipkinExporter

	// NewMultiExporter 创建多导出器
	NewMultiExporter = toolkitOtel.NewMultiExporter

	// NewProbabilitySampler 创建概率采样器
	NewProbabilitySampler = toolkitOtel.NewProbabilitySampler

	// NewRateLimitingSampler 创建限流采样器
	NewRateLimitingSampler = toolkitOtel.NewRateLimitingSampler

	// NewW3CTraceContextPropagator 创建 W3C 传播器
	NewW3CTraceContextPropagator = toolkitOtel.NewW3CTraceContextPropagator

	// NewB3Propagator 创建 B3 传播器
	NewB3Propagator = toolkitOtel.NewB3Propagator

	// NewCompositePropagator 创建组合传播器
	NewCompositePropagator = toolkitOtel.NewCompositePropagator
)
