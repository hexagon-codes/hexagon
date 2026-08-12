<div align="right">Language: <a href="observability.md">中文</a> | English</div>

# Observability Integration Guide

Hexagon provides observability components for tracing, metrics, and logging.

Observability uses a **Hook** mechanism: create a `hooks.Manager`, register tracing/metrics hooks on it, then inject it into the `context` via `hooks.ContextWithManager`. During execution an Agent reads the Manager from the `context` and fires the hooks automatically. Agents themselves do NOT have `WithTracer` / `WithMetrics` / `WithLogger` options.

## Tracing

### In-Memory Tracer

Good for development and testing; records spans in memory. The in-memory tracer is a manual tracing API: constructing it or merely running an Agent does not create spans automatically. Callers must explicitly start and end each span. Use the Hook + OpenTelemetry path below when Agent lifecycle tracing should be automatic.

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

// Inspect recorded spans (returns []*tracer.DefaultSpan)
for _, span := range t.Spans() {
    data := span.Export() // SpanData, includes Name / Duration fields
    fmt.Printf("%s: %v\n", data.Name, data.Duration)
}
```

### OpenTelemetry

Use `otel.SetupTracing` to create an OTel tracer and register the tracing hooks on the Hook Manager:

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

// Create the OTel tracer and register tracing hooks
tracing, err := otel.SetupTracing(manager, otel.WithTracerServiceName("my-agent"))
if err != nil {
    return err
}

// Shutdown 等待后台导出并刷新剩余 span。
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

// SetExporter 调用开始后即把所有权转移给 tracing。
// 此后调用方不得继续使用或关闭 exporter。
if err := tracing.SetExporter(ctx, exporter); err != nil {
    return err
}

// Inject the Manager into the context; the Agent fires hooks automatically
ctx = hooks.ContextWithManager(ctx, manager)
if _, err := myAgent.Run(ctx, agent.Input{Query: "Hello"}); err != nil {
    return err
}
```

`SetExporter` owns the supplied exporter even when it returns an error, so cleanup is always performed through `tracing.Shutdown`. Do not call `Shutdown` on that exporter separately.

### Langfuse (OTLP)

Langfuse uses the same OTLP tracing path. When Langfuse is the destination, replace the generic OTLP exporter construction in the previous example with:

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

The `tracing` instance, Hook Manager injection, and `Shutdown` lifecycle are identical to the previous section; the two keys must be provided together. Call `otel.ApplyGenAI` on a manually created LLM span to write `gen_ai.*` attributes such as model and token usage; `NewLangfuseExporter` does not add those attributes automatically.

## Metrics

### Prometheus HTTP Endpoint

`prometheus.NewExporter` is re-exported from toolkit and creates an isolated Prometheus Registry. The default Registry includes Go runtime metrics and can be exposed at `/metrics`:

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

### In-Process Hook Metrics

Hexagon's Agent / LLM / Tool / Retriever hooks write to `observe/metrics.Metrics`. The following example uses an independent `MemoryMetrics` instance for Hook data:

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

// Inject the Manager into the context
ctx = hooks.ContextWithManager(ctx, manager)
if _, err := myAgent.Run(ctx, agent.Input{Query: "Hello"}); err != nil {
    return err
}

snapshot := hookMetrics.Snapshot()
fmt.Printf("Agent counters: %#v\n", snapshot.Counters)
```

This Hook `MemoryMetrics` instance and the Prometheus Registry in the previous section are separate data sources and are not bridged automatically. Registering `SetupMetrics` does not make Agent metrics appear at that `/metrics` endpoint. To publish business metrics from the same endpoint, the application must explicitly register and update them through `exporter.Registry()` / `exporter.Factory()`.

### Built-in Metrics Summary

`GetHexagonMetrics` provides another in-process business summary. It does not synchronize automatically from Hook `MemoryMetrics` or the Prometheus Registry; callers must explicitly invoke its `Record*` methods:

```go
import (
    "fmt"
    "time"

    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/hexagon/observe/metrics"
)

collector := metrics.GetHexagonMetrics()

start := time.Now()
_, runErr := myAgent.Run(ctx, agent.Input{Query: "Hello"})
collector.RecordAgentRun(ctx, "my-agent", time.Since(start), runErr)

summary := collector.GetSummary()
fmt.Printf("total agent runs: %d\n", summary.TotalAgentRuns)
```

## Logging

```go
import "github.com/hexagon-codes/hexagon/observe/logger"

// Set the global log level (a string: "debug" / "info" / "warn" / "error")
logger.SetLevel("info")

// Get the default Logger
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

// 默认值同样是 127.0.0.1:8080；这里显式写出以强调仅绑定回环地址。
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

// Inject ui.HookManager() into the context to view execution events live in the Dev UI
ctx = hooks.ContextWithManager(ctx, ui.HookManager())

// Visit http://localhost:8080 to view real-time status
```

A non-loopback address such as `0.0.0.0:8080` also requires `devui.WithAuthToken` containing at least 32 non-whitespace bytes. The default loopback address does not require an explicit token.

For more details, see [DESIGN.en.md](../DESIGN.en.md#observability).
