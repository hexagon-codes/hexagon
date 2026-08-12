<div align="right">语言: 中文 | <a href="observability.en.md">English</a></div>

# 可观测性集成指南

Hexagon 提供追踪、指标和日志等可观测性组件。

可观测性采用 **Hook（钩子）** 机制：先创建 `hooks.Manager` 并注册追踪 / 指标钩子，再通过 `hooks.ContextWithManager` 注入 `context`，Agent 在执行时会从 `context` 中取出 Manager 并自动触发钩子。Agent 本身没有 `WithTracer` / `WithMetrics` / `WithLogger` 选项。

## 追踪 (Tracing)

### 内存追踪器

适合开发与测试，将 Span 记录在内存中。内存追踪器是手工追踪 API：仅创建追踪器或执行 Agent 不会自动产生 Span，调用方需要显式开始并结束 Span；需要自动追踪 Agent 生命周期时，请使用下文的 Hook + OpenTelemetry 链路。

```go
import (
    "context"
    "fmt"

    "github.com/hexagon-codes/hexagon/observe/tracer"
)

t := tracer.NewMemoryTracer()

_, span := t.StartSpan(context.Background(), "manual.operation")
span.SetAttribute("component", "demo")
// ... 执行需要手工追踪的操作 ...
span.End()

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
    "context"
    "log"
    "time"

    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/hexagon/hooks"
    "github.com/hexagon-codes/hexagon/observe/otel"
)

manager := hooks.NewManager()

// 创建 OTel 追踪器并注册追踪钩子
tracing, err := otel.SetupTracing(manager, otel.WithTracerServiceName("my-agent"))
if err != nil {
    return err
}

// Shutdown 会等待后台导出并刷新剩余 Span。
defer func() {
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := tracing.Shutdown(shutdownCtx); err != nil {
        log.Printf("OpenTelemetry shutdown failed: %v", err)
    }
}()

exporter, err := otel.NewOTLPExporter("https://otel.example.com/v1/traces")
if err != nil {
    return err
}

// SetExporter 调用开始后，导出器所有权即转移给 tracing；调用方不得再使用或关闭 exporter。
if err := tracing.SetExporter(ctx, exporter); err != nil {
    return err
}

// 将 Manager 注入 context，Agent 执行时自动触发钩子
ctx = hooks.ContextWithManager(ctx, manager)
if _, err := myAgent.Run(ctx, agent.Input{Query: "你好"}); err != nil {
    return err
}
```

`SetExporter` 即使返回错误也已经接管传入的导出器，因此清理由 `tracing.Shutdown` 统一完成。不要再对该导出器调用 `Shutdown`。

### Langfuse（OTLP）

Langfuse 走同一条 OTLP 追踪链路。如果目标后端是 Langfuse，将上例创建通用 OTLP 导出器的部分替换为：

```go
import "os"

exporter, err := otel.NewLangfuseExporter(otel.LangfuseConfig{
    Endpoint:  "https://cloud.langfuse.com/api/public/otel",
    PublicKey: os.Getenv("LANGFUSE_PUBLIC_KEY"),
    SecretKey: os.Getenv("LANGFUSE_SECRET_KEY"),
})
if err != nil {
    return err
}
if err := tracing.SetExporter(ctx, exporter); err != nil {
    return err
}
```

这里的 `tracing`、Hook Manager 注入和 `Shutdown` 生命周期与上一节完全相同；密钥必须成对提供。对手工创建的 LLM Span 调用 `otel.ApplyGenAI`，可写入模型与 Token 用量等 `gen_ai.*` 属性；该写入不是 `NewLangfuseExporter` 自动完成的。

## 指标 (Metrics)

### Prometheus HTTP 端点

`prometheus.NewExporter` 重新导出自 toolkit，并创建一个隔离的 Prometheus Registry。默认 Registry 包含 Go runtime 指标，可通过 `/metrics` 暴露：

```go
import (
    "context"
    "log"
    "time"

    "github.com/hexagon-codes/hexagon/observe/prometheus"
)

exporter, err := prometheus.NewExporter(
    prometheus.WithNamespace("hexagon"),
)
if err != nil {
    return err
}

defer func() {
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := exporter.Shutdown(shutdownCtx); err != nil {
        log.Printf("Prometheus exporter shutdown failed: %v", err)
    }
}()

go func() {
    if err := exporter.ListenAndServe("127.0.0.1:9090"); err != nil {
        log.Printf("Prometheus exporter stopped: %v", err)
    }
}()
```

### Hook 进程内指标

Hexagon 的 Agent / LLM / Tool / Retriever 指标钩子写入 `observe/metrics.Metrics`。下面使用独立的 `MemoryMetrics` 收集 Hook 指标：

```go
import (
    "fmt"

    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/hexagon/hooks"
    "github.com/hexagon-codes/hexagon/observe/metrics"
    "github.com/hexagon-codes/hexagon/observe/prometheus"
)

manager := hooks.NewManager()
hookMetrics := metrics.NewMemoryMetrics()
prometheus.SetupMetrics(manager, prometheus.WithMetricsInstance(hookMetrics))

// 将 Manager 注入 context
ctx = hooks.ContextWithManager(ctx, manager)
if _, err := myAgent.Run(ctx, agent.Input{Query: "你好"}); err != nil {
    return err
}

snapshot := hookMetrics.Snapshot()
fmt.Printf("Agent counters: %#v\n", snapshot.Counters)
```

这套 Hook `MemoryMetrics` 与上一节的 Prometheus Registry 是两套独立数据源，当前没有自动桥接：注册 `SetupMetrics` 不会让 Agent 指标自动出现在该 `/metrics` 端点。若应用需要在同一 Prometheus 端点导出业务指标，必须通过 `exporter.Registry()` / `exporter.Factory()` 显式注册并写入相应指标。

### 内置指标汇总

`GetHexagonMetrics` 提供另一套进程内业务汇总。它不会从 Hook `MemoryMetrics` 或 Prometheus Registry 自动同步数据，调用方需要显式调用 `Record*` 方法：

```go
import (
    "fmt"
    "time"

    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/hexagon/observe/metrics"
)

collector := metrics.GetHexagonMetrics()

start := time.Now()
_, runErr := myAgent.Run(ctx, agent.Input{Query: "你好"})
collector.RecordAgentRun(ctx, "my-agent", time.Since(start), runErr)

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
import (
    "context"
    "log"
    "time"

    "github.com/hexagon-codes/hexagon/hooks"
    "github.com/hexagon-codes/hexagon/observe/devui"
)

// 默认地址也是 127.0.0.1:8080；显式写出便于说明仅监听本机回环地址。
ui := devui.New(devui.WithAddr("127.0.0.1:8080"))
defer func() {
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := ui.Stop(shutdownCtx); err != nil {
        log.Printf("Dev UI shutdown failed: %v", err)
    }
}()

go func() {
    if err := ui.Start(); err != nil {
        log.Printf("Dev UI stopped: %v", err)
    }
}()

// 将 ui.HookManager() 注入 context，即可在 Dev UI 实时查看执行事件
ctx = hooks.ContextWithManager(ctx, ui.HookManager())

// 访问 http://localhost:8080 查看实时状态
```

非回环地址（例如 `0.0.0.0:8080`）必须同时配置至少 32 个无空白字节的 `devui.WithAuthToken`；默认回环地址不需要显式 Token。

更多详情参见 [DESIGN.md](../DESIGN.md#可观测性)。
