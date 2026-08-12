// Package otel 提供 OpenTelemetry 集成
//
// 本包直接复用 toolkit/infra/otel 的实现。
//
// 使用示例:
//
//	tracer := otel.NewTracer(
//	    otel.WithServiceName("my-service"),
//	)
//	defer tracer.Shutdown(context.Background())
package otel

import (
	toolkitOtel "github.com/hexagon-codes/toolkit/infra/otel"
)

// 重新导出类型
type (
	// Tracer OpenTelemetry 追踪器
	Tracer = toolkitOtel.Tracer

	// Config OpenTelemetry 配置
	Config = toolkitOtel.Config

	// Option 配置选项
	Option = toolkitOtel.Option

	// Span 表示 OpenTelemetry 跨度。
	Span = toolkitOtel.Span

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
	// NewTracer 创建 OpenTelemetry 追踪器
	NewTracer = toolkitOtel.NewTracer

	// DefaultConfig 返回默认配置
	DefaultConfig = toolkitOtel.DefaultConfig

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

	// WithOTLPBatchTimeout 设置 OTLP 定时刷新间隔
	WithOTLPBatchTimeout = toolkitOtel.WithOTLPBatchTimeout

	// WithOTLPMaxQueueSize 设置 OTLP 队列容量上限
	WithOTLPMaxQueueSize = toolkitOtel.WithOTLPMaxQueueSize

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

var (
	// ErrTracerShutdown 表示追踪器已经关闭，不能再接管新的导出器。
	ErrTracerShutdown = toolkitOtel.ErrTracerShutdown
	// ErrExporterShutdown 表示 OTLP 导出器已经关闭。
	ErrExporterShutdown = toolkitOtel.ErrExporterShutdown
	// ErrExporterQueueFull 表示 OTLP 导出队列已满。
	ErrExporterQueueFull = toolkitOtel.ErrExporterQueueFull
	// ErrInvalidExporterConfig 表示 OTLP 导出器配置无效。
	ErrInvalidExporterConfig = toolkitOtel.ErrInvalidExporterConfig
	// ErrInvalidSpan 表示待导出的 Span 无效。
	ErrInvalidSpan = toolkitOtel.ErrInvalidSpan
)
