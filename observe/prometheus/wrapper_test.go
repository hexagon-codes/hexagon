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
		var _ prometheus.Factory
		var _ prometheus.Counter
		var _ prometheus.Gauge
		var _ prometheus.Histogram
		var _ prometheus.Summary
		var _ prometheus.MetricsAdapter
	})
}

// TestPrometheusFunctions 测试导出的函数
func TestPrometheusFunctions(t *testing.T) {
	t.Run("Functions", func(t *testing.T) {
		if prometheus.NewExporter == nil {
			t.Error("NewExporter should not be nil")
		}
		if prometheus.WithNamespace == nil {
			t.Error("WithNamespace should not be nil")
		}
		if prometheus.WithSubsystem == nil {
			t.Error("WithSubsystem should not be nil")
		}
		if prometheus.NewRegistry == nil {
			t.Error("NewRegistry should not be nil")
		}
		if prometheus.NewFactory == nil {
			t.Error("NewFactory should not be nil")
		}
		if prometheus.NewMetricsAdapter == nil {
			t.Error("NewMetricsAdapter should not be nil")
		}
	})
}

// TestPrometheusVariables 测试导出的变量
func TestPrometheusVariables(t *testing.T) {
	t.Run("DefaultBuckets", func(t *testing.T) {
		if prometheus.DefaultBuckets == nil {
			t.Fatal("DefaultBuckets should not be nil")
		}
		if len(prometheus.DefaultBuckets()) == 0 {
			t.Error("DefaultBuckets should not be empty")
		}
	})

	t.Run("DefaultQuantiles", func(t *testing.T) {
		if prometheus.DefaultQuantiles == nil {
			t.Fatal("DefaultQuantiles should not be nil")
		}
		if len(prometheus.DefaultQuantiles()) == 0 {
			t.Error("DefaultQuantiles should not be empty")
		}
	})
}

// TestNewExporter 测试创建导出器
func TestNewExporter(t *testing.T) {
	t.Run("DefaultExporter", func(t *testing.T) {
		exporter, err := prometheus.NewExporter()
		if err != nil {
			t.Fatal(err)
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
			t.Fatal(err)
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
			t.Fatal(err)
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
