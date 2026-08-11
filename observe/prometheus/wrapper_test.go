package prometheus_test

import (
	"testing"

	"github.com/hexagon-codes/hexagon/observe/prometheus"
)

// TestPrometheusTypes 测试导出的类型
func TestPrometheusTypes(t *testing.T) {
	t.Run("Types", func(t *testing.T) {
		// 编译时检查类型导出
		var _ prometheus.Exporter
		var _ prometheus.ExporterOption
		var _ prometheus.Registry
		var _ prometheus.PrometheusCounter
		var _ prometheus.PrometheusGauge
		var _ prometheus.PrometheusHistogram
		var _ prometheus.PrometheusSummary
		var _ prometheus.MetricsAdapter
	})
}

// TestPrometheusVariables 测试导出的默认配置
// 注：DefaultBuckets/DefaultQuantiles 在 toolkit v0.3.0 中由变量改为函数，
// 相应断言改为对函数值及其返回值做校验。
func TestPrometheusVariables(t *testing.T) {
	t.Run("DefaultBuckets", func(t *testing.T) {
		if prometheus.DefaultBuckets == nil {
			t.Fatal("DefaultBuckets should not be nil")
		}
		if len(prometheus.DefaultBuckets()) == 0 {
			t.Error("DefaultBuckets() should not be empty")
		}
	})

	t.Run("DefaultQuantiles", func(t *testing.T) {
		if prometheus.DefaultQuantiles == nil {
			t.Fatal("DefaultQuantiles should not be nil")
		}
		if len(prometheus.DefaultQuantiles()) == 0 {
			t.Error("DefaultQuantiles() should not be empty")
		}
	})
}

// TestNewExporter 测试创建导出器
// 注：toolkit v0.3.0 的 NewExporter 返回 (exporter, error)，配置非法时返回错误。
func TestNewExporter(t *testing.T) {
	t.Run("DefaultExporter", func(t *testing.T) {
		exporter, err := prometheus.NewExporter()
		if err != nil {
			t.Fatal("NewExporter() returned error:", err)
		}
		if exporter == nil {
			t.Fatal("NewExporter() returned nil")
		}

		// 测试 Handler
		handler := exporter.Handler()
		if handler == nil {
			t.Error("Handler() should not be nil")
		}
	})

	t.Run("WithNamespace", func(t *testing.T) {
		exporter, err := prometheus.NewExporter(
			prometheus.WithNamespace("test"),
		)
		if err != nil {
			t.Fatal("NewExporter() with namespace returned error:", err)
		}
		if exporter == nil {
			t.Fatal("NewExporter() with namespace returned nil")
		}
	})

	t.Run("WithSubsystem", func(t *testing.T) {
		exporter, err := prometheus.NewExporter(
			prometheus.WithNamespace("test"),
			prometheus.WithSubsystem("api"),
		)
		if err != nil {
			t.Fatal("NewExporter() with subsystem returned error:", err)
		}
		if exporter == nil {
			t.Fatal("NewExporter() with subsystem returned nil")
		}
	})
}

// TestPrometheusOptions 测试选项
func TestPrometheusOptions(t *testing.T) {
	t.Run("WithNamespace", func(t *testing.T) {
		opt := prometheus.WithNamespace("myapp")
		if opt == nil {
			t.Error("WithNamespace should return a valid option")
		}
	})

	t.Run("WithSubsystem", func(t *testing.T) {
		opt := prometheus.WithSubsystem("api")
		if opt == nil {
			t.Error("WithSubsystem should return a valid option")
		}
	})
}

// TestNewRegistry 测试创建注册表
func TestNewRegistry(t *testing.T) {
	t.Run("NewRegistry", func(t *testing.T) {
		registry := prometheus.NewRegistry()
		if registry == nil {
			t.Fatal("NewRegistry() returned nil")
		}
	})
}

// BenchmarkPrometheusExporter 基准测试
func BenchmarkPrometheusExporter(b *testing.B) {
	b.Run("NewExporter", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = prometheus.NewExporter()
		}
	})

	b.Run("NewRegistry", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = prometheus.NewRegistry()
		}
	})
}
