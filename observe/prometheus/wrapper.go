// Package prometheus 提供 Prometheus 指标导出
//
// 本包直接复用 toolkit/infra/prometheus 的实现。
//
// 使用示例:
//
//	exporter, err := prometheus.NewExporter(
//	    prometheus.WithNamespace("myapp"),
//	)
//	if err != nil {
//	    return err
//	}
//	http.Handle("/metrics", exporter.Handler())
package prometheus

import (
	toolkitProm "github.com/hexagon-codes/toolkit/infra/prometheus"
)

// 重新导出类型
type (
	// Exporter Prometheus 导出器
	Exporter = toolkitProm.Exporter

	// ExporterOption 导出器选项
	ExporterOption = toolkitProm.ExporterOption

	// Registry 指标注册表
	Registry = toolkitProm.Registry

	// Factory 指标工厂
	Factory = toolkitProm.Factory

	// Counter Prometheus 计数器
	Counter = toolkitProm.Counter

	// Gauge Prometheus 仪表
	Gauge = toolkitProm.Gauge

	// Histogram Prometheus 直方图
	Histogram = toolkitProm.Histogram

	// Summary Prometheus 摘要
	Summary = toolkitProm.Summary

	// MetricsAdapter 指标适配器
	MetricsAdapter = toolkitProm.MetricsAdapter
)

// 重新导出函数
var (
	// NewExporter 创建 Prometheus 导出器
	NewExporter = toolkitProm.NewExporter

	// WithNamespace 设置命名空间
	WithNamespace = toolkitProm.WithNamespace

	// WithSubsystem 设置子系统
	WithSubsystem = toolkitProm.WithSubsystem

	// NewRegistry 创建注册表
	NewRegistry = toolkitProm.NewRegistry

	// NewFactory 创建指标工厂
	NewFactory = toolkitProm.NewFactory

	// NewMetricsAdapter 创建指标适配器
	NewMetricsAdapter = toolkitProm.NewMetricsAdapter
)

// 重新导出默认配置工厂
var (
	// DefaultBuckets 返回默认桶
	DefaultBuckets = toolkitProm.DefaultBuckets

	// DefaultQuantiles 返回默认分位数
	DefaultQuantiles = toolkitProm.DefaultQuantiles
)
