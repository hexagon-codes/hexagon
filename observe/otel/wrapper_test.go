package otel_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/hexagon-codes/hexagon/observe/otel"
)

// TestErrTracerShutdownReexport 锁定 Hexagon 对 toolkit 关闭错误的直接重导出。
func TestErrTracerShutdownReexport(t *testing.T) {
	tracer := otel.NewTracer()
	if err := tracer.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	err := tracer.SetExporter(context.Background(), otel.NewConsoleExporter(io.Discard))
	if !errors.Is(err, otel.ErrTracerShutdown) {
		t.Fatalf("SetExporter() error = %v, want ErrTracerShutdown", err)
	}
}

// TestOTelTypes 测试导出的类型
func TestOTelTypes(t *testing.T) {
	// 验证所有导出的类型都不为 nil
	t.Run("Types", func(t *testing.T) {
		// 这里主要是编译时检查，确保类型正确导出
		var _ otel.Tracer
		var _ otel.Config
		var _ otel.Option
		var _ otel.Span
		var _ otel.SpanData
		var _ otel.SpanEvent
		var _ otel.Exporter
		var _ otel.ConsoleExporter
		var _ otel.OTLPExporter
		var _ otel.OTLPExporterOption
		var _ otel.JaegerExporter
		var _ otel.ZipkinExporter
		var _ otel.MultiExporter
		var _ otel.Sampler
		var _ otel.AlwaysSampler
		var _ otel.NeverSampler
		var _ otel.ProbabilitySampler
		var _ otel.RateLimitingSampler
		var _ otel.Propagator
		var _ otel.Carrier
		var _ otel.MapCarrier
		var _ otel.W3CTraceContextPropagator
		var _ otel.B3Propagator
		var _ otel.CompositePropagator
	})
}

// TestOTelFunctions 测试导出的函数
func TestOTelFunctions(t *testing.T) {
	t.Run("Functions", func(t *testing.T) {
		// 验证函数不为 nil
		if otel.NewTracer == nil {
			t.Error("NewTracer should not be nil")
		}
		if otel.DefaultConfig == nil {
			t.Error("DefaultConfig should not be nil")
		}
		if otel.WithServiceName == nil {
			t.Error("WithServiceName should not be nil")
		}
		if otel.WithServiceVersion == nil {
			t.Error("WithServiceVersion should not be nil")
		}
		if otel.WithEnvironment == nil {
			t.Error("WithEnvironment should not be nil")
		}
		if otel.WithSamplingRate == nil {
			t.Error("WithSamplingRate should not be nil")
		}
	})

	t.Run("ExporterFunctions", func(t *testing.T) {
		if otel.NewConsoleExporter == nil {
			t.Error("NewConsoleExporter should not be nil")
		}
		if otel.NewOTLPExporter == nil {
			t.Error("NewOTLPExporter should not be nil")
		}
		if otel.WithOTLPHeaders == nil {
			t.Error("WithOTLPHeaders should not be nil")
		}
		if otel.WithOTLPBatchSize == nil {
			t.Error("WithOTLPBatchSize should not be nil")
		}
		if otel.NewJaegerExporter == nil {
			t.Error("NewJaegerExporter should not be nil")
		}
		if otel.NewZipkinExporter == nil {
			t.Error("NewZipkinExporter should not be nil")
		}
		if otel.NewMultiExporter == nil {
			t.Error("NewMultiExporter should not be nil")
		}
	})

	t.Run("SamplerFunctions", func(t *testing.T) {
		if otel.NewProbabilitySampler == nil {
			t.Error("NewProbabilitySampler should not be nil")
		}
		if otel.NewRateLimitingSampler == nil {
			t.Error("NewRateLimitingSampler should not be nil")
		}
	})

	t.Run("PropagatorFunctions", func(t *testing.T) {
		if otel.NewW3CTraceContextPropagator == nil {
			t.Error("NewW3CTraceContextPropagator should not be nil")
		}
		if otel.NewB3Propagator == nil {
			t.Error("NewB3Propagator should not be nil")
		}
		if otel.NewCompositePropagator == nil {
			t.Error("NewCompositePropagator should not be nil")
		}
	})
}

// TestDefaultOTelConfig 测试默认配置
func TestDefaultConfig(t *testing.T) {
	t.Run("DefaultConfig", func(t *testing.T) {
		config := otel.DefaultConfig()

		// 验证默认配置有合理的值
		if config.ServiceName == "" {
			t.Error("Default ServiceName should not be empty")
		}
	})
}

// TestOTelTracerCreation 测试追踪器创建
func TestTracerCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping OTel integration test in short mode")
	}

	t.Run("NewTracer", func(t *testing.T) {
		// 测试创建追踪器（可能需要实际的 OTEL 后端）
		tracer := otel.NewTracer(
			otel.WithServiceName("test-service"),
			otel.WithServiceVersion("1.0.0"),
			otel.WithEnvironment("test"),
		)

		if tracer == nil {
			// 在没有后端的情况下可能返回 nil，这是可以接受的
			t.Log("OTel tracer is nil (expected if no backend is configured)")
			return
		}

		// 如果创建成功，测试 Shutdown
		defer func() {
			ctx := context.Background()
			if err := tracer.Shutdown(ctx); err != nil {
				t.Logf("Shutdown error (expected): %v", err)
			}
		}()
	})
}

// TestConsoleExporter 测试控制台导出器
func TestConsoleExporter(t *testing.T) {
	t.Run("NewConsoleExporter", func(t *testing.T) {
		// ConsoleExporter 需要 io.Writer 参数
		// 这里只测试函数存在性
		if otel.NewConsoleExporter == nil {
			t.Fatal("NewConsoleExporter should not be nil")
		}
	})
}

// TestSamplers 测试采样器
func TestSamplers(t *testing.T) {
	t.Run("ProbabilitySampler", func(t *testing.T) {
		sampler := otel.NewProbabilitySampler(0.5)
		if sampler == nil {
			t.Fatal("NewProbabilitySampler() returned nil")
		}
	})

	t.Run("RateLimitingSampler", func(t *testing.T) {
		sampler := otel.NewRateLimitingSampler(100) // 100 traces/sec
		if sampler == nil {
			t.Fatal("NewRateLimitingSampler() returned nil")
		}
	})
}

// TestPropagators 测试传播器
func TestPropagators(t *testing.T) {
	t.Run("W3CTraceContextPropagator", func(t *testing.T) {
		propagator := otel.NewW3CTraceContextPropagator()
		if propagator == nil {
			t.Fatal("NewW3CTraceContextPropagator() returned nil")
		}
	})

	t.Run("B3Propagator", func(t *testing.T) {
		propagator := otel.NewB3Propagator()
		if propagator == nil {
			t.Fatal("NewB3Propagator() returned nil")
		}
	})

	t.Run("CompositePropagator", func(t *testing.T) {
		w3c := otel.NewW3CTraceContextPropagator()
		b3 := otel.NewB3Propagator()
		propagator := otel.NewCompositePropagator(w3c, b3)
		if propagator == nil {
			t.Fatal("NewCompositePropagator() returned nil")
		}
	})
}

// TestMultiExporter 测试多导出器
func TestMultiExporter(t *testing.T) {
	t.Run("NewMultiExporter", func(t *testing.T) {
		// 测试函数存在性
		if otel.NewMultiExporter == nil {
			t.Fatal("NewMultiExporter should not be nil")
		}
	})
}

// TestOTelOptions 测试配置选项
func TestOTelOptions(t *testing.T) {
	t.Run("WithServiceName", func(t *testing.T) {
		opt := otel.WithServiceName("test-service")
		if opt == nil {
			t.Error("WithServiceName should return a valid option")
		}
	})

	t.Run("WithServiceVersion", func(t *testing.T) {
		opt := otel.WithServiceVersion("1.0.0")
		if opt == nil {
			t.Error("WithServiceVersion should return a valid option")
		}
	})

	t.Run("WithEnvironment", func(t *testing.T) {
		opt := otel.WithEnvironment("production")
		if opt == nil {
			t.Error("WithEnvironment should return a valid option")
		}
	})

	t.Run("WithSamplingRate", func(t *testing.T) {
		opt := otel.WithSamplingRate(0.5)
		if opt == nil {
			t.Error("WithSamplingRate should return a valid option")
		}
	})

}

// TestOTLPExporterOptions 测试 OTLP 导出器选项
func TestOTLPExporterOptions(t *testing.T) {
	t.Run("WithOTLPHeaders", func(t *testing.T) {
		headers := map[string]string{
			"Authorization": "Bearer token",
		}
		opt := otel.WithOTLPHeaders(headers)
		if opt == nil {
			t.Error("WithOTLPHeaders should return a valid option")
		}
	})

	t.Run("WithOTLPBatchSize", func(t *testing.T) {
		opt := otel.WithOTLPBatchSize(512)
		if opt == nil {
			t.Error("WithOTLPBatchSize should return a valid option")
		}
	})

	t.Run("WithOTLPBatchTimeout", func(t *testing.T) {
		opt := otel.WithOTLPBatchTimeout(time.Second)
		if opt == nil {
			t.Error("WithOTLPBatchTimeout should return a valid option")
		}
	})

	t.Run("WithOTLPMaxQueueSize", func(t *testing.T) {
		opt := otel.WithOTLPMaxQueueSize(2048)
		if opt == nil {
			t.Error("WithOTLPMaxQueueSize should return a valid option")
		}
	})
}

// TestMapCarrier 测试 Map 载体
func TestMapCarrier(t *testing.T) {
	t.Run("MapCarrier", func(t *testing.T) {
		carrier := otel.MapCarrier(make(map[string]string))
		if carrier == nil {
			t.Error("MapCarrier should not be nil")
		}

		// 测试设置和获取
		carrier.Set("traceparent", "00-trace-id-00")
		val := carrier.Get("traceparent")
		if val != "00-trace-id-00" {
			t.Errorf("Get() = %s, want 00-trace-id-00", val)
		}

		// 测试 Keys
		keys := carrier.Keys()
		if len(keys) == 0 {
			t.Error("Keys() should not be empty")
		}
	})
}

// BenchmarkOTelTracer 基准测试
func BenchmarkOTelTracer(b *testing.B) {
	b.Run("DefaultConfig", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = otel.DefaultConfig()
		}
	})
}
