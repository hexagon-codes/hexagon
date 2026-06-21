<div align="right">语言: 中文 | <a href="observability.en.md">English</a></div>

# 可观测性集成指南

Hexagon 提供完整的可观测性方案，包括追踪、指标和日志。

可观测性采用 **Hook（钩子）** 机制：先创建 `hooks.Manager` 并注册追踪 / 指标钩子，再通过 `hooks.ContextWithManager` 注入 `context`，Agent 在执行时会从 `context` 中取出 Manager 并自动触发钩子。Agent 本身没有 `WithTracer` / `WithMetrics` / `WithLogger` 选项。

## 追踪 (Tracing)

### 内存追踪器

适合开发与测试，将 Span 记录在内存中：

```go
import "github.com/hexagon-codes/hexagon/observe/tracer"

t := tracer.NewMemoryTracer()

// ... 执行 Agent / 检索 ...

// 查看记录的 Span（返回 []*tracer.DefaultSpan）
for _, span := range t.Spans() {
    data := span.Export() // SpanData，含 Name / Duration 等字段
    fmt.Printf("%s: %v\n", data.Name, data.Duration)
}
```

### OpenTelemetry

通过 `otel.SetupTracing` 创建 OTel 追踪器并把追踪钩子注册到 Hook Manager：

```go
import (
    "github.com/hexagon-codes/hexagon/hooks"
    "github.com/hexagon-codes/hexagon/observe/otel"
)

manager := hooks.NewManager()

// 创建 OTel 追踪器并注册追踪钩子
_, err := otel.SetupTracing(manager,
    otel.WithTracerServiceName("my-agent"),
    otel.WithTracerEndpoint("localhost:4317"),
)
if err != nil {
    panic(err)
}

// 将 Manager 注入 context，Agent 执行时自动触发钩子
ctx = hooks.ContextWithManager(ctx, manager)
output, _ := myAgent.Run(ctx, agent.Input{Query: "你好"})
```

## 指标 (Metrics)

### Prometheus

`prometheus.NewExporter` 重新导出自 toolkit，可暴露 `/metrics` 端点；`prometheus.SetupMetrics` 负责把指标钩子注册到 Hook Manager：

```go
import (
    "net/http"

    "github.com/hexagon-codes/hexagon/hooks"
    "github.com/hexagon-codes/hexagon/observe/prometheus"
)

exporter := prometheus.NewExporter(
    prometheus.WithNamespace("hexagon"),
)

// 暴露 /metrics 端点
http.Handle("/metrics", exporter.Handler())
go http.ListenAndServe(":9090", nil)

// 注册指标钩子
manager := hooks.NewManager()
prometheus.SetupMetrics(manager)

// 将 Manager 注入 context
ctx = hooks.ContextWithManager(ctx, manager)
output, _ := myAgent.Run(ctx, agent.Input{Query: "你好"})
```

### 内置指标汇总

`observe/metrics` 提供进程内的指标汇总，无需外部依赖即可查看：

```go
import "github.com/hexagon-codes/hexagon/observe/metrics"

collector := metrics.GetHexagonMetrics()

summary := collector.GetSummary()
fmt.Printf("总执行次数: %d\n", summary.TotalAgentRuns)
```

## 日志 (Logging)

```go
import "github.com/hexagon-codes/hexagon/observe/logger"

// 设置全局日志级别（取值为字符串 "debug" / "info" / "warn" / "error"）
logger.SetLevel("info")

// 获取默认 Logger
log := logger.Default()
log.Info("agent started")
```

## Dev UI

```go
import "github.com/hexagon-codes/hexagon/observe/devui"

// 监听地址通过 WithAddr 配置，Start() 不接收参数
ui := devui.New(devui.WithAddr(":8080"))
go ui.Start()

// 将 ui.HookManager() 注入 context，即可在 Dev UI 实时查看执行事件
ctx = hooks.ContextWithManager(ctx, ui.HookManager())

// 访问 http://localhost:8080 查看实时状态
```

更多详情参见 [DESIGN.md](../DESIGN.md#可观测性)。
