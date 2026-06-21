<div align="right">Language: <a href="observability.md">中文</a> | English</div>

# Observability Integration Guide

Hexagon provides a complete observability solution including tracing, metrics, and logging.

Observability uses a **Hook** mechanism: create a `hooks.Manager`, register tracing/metrics hooks on it, then inject it into the `context` via `hooks.ContextWithManager`. During execution an Agent reads the Manager from the `context` and fires the hooks automatically. Agents themselves do NOT have `WithTracer` / `WithMetrics` / `WithLogger` options.

## Tracing

### In-Memory Tracer

Good for development and testing; records spans in memory:

```go
import "github.com/hexagon-codes/hexagon/observe/tracer"

t := tracer.NewMemoryTracer()

// ... run the Agent / retrieval ...

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
    "github.com/hexagon-codes/hexagon/hooks"
    "github.com/hexagon-codes/hexagon/observe/otel"
)

manager := hooks.NewManager()

// Create the OTel tracer and register tracing hooks
_, err := otel.SetupTracing(manager,
    otel.WithTracerServiceName("my-agent"),
    otel.WithTracerEndpoint("localhost:4317"),
)
if err != nil {
    panic(err)
}

// Inject the Manager into the context; the Agent fires hooks automatically
ctx = hooks.ContextWithManager(ctx, manager)
output, _ := myAgent.Run(ctx, agent.Input{Query: "Hello"})
```

## Metrics

### Prometheus

`prometheus.NewExporter` is re-exported from toolkit and exposes the `/metrics` endpoint; `prometheus.SetupMetrics` registers the metrics hooks on the Hook Manager:

```go
import (
    "net/http"

    "github.com/hexagon-codes/hexagon/hooks"
    "github.com/hexagon-codes/hexagon/observe/prometheus"
)

exporter := prometheus.NewExporter(
    prometheus.WithNamespace("hexagon"),
)

// Expose the /metrics endpoint
http.Handle("/metrics", exporter.Handler())
go http.ListenAndServe(":9090", nil)

// Register the metrics hooks
manager := hooks.NewManager()
prometheus.SetupMetrics(manager)

// Inject the Manager into the context
ctx = hooks.ContextWithManager(ctx, manager)
output, _ := myAgent.Run(ctx, agent.Input{Query: "Hello"})
```

### Built-in Metrics Summary

`observe/metrics` provides an in-process metrics summary that needs no external dependency:

```go
import "github.com/hexagon-codes/hexagon/observe/metrics"

collector := metrics.GetHexagonMetrics()

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
import "github.com/hexagon-codes/hexagon/observe/devui"

// The listen address is set via WithAddr; Start() takes no args
ui := devui.New(devui.WithAddr(":8080"))
go ui.Start()

// Inject ui.HookManager() into the context to view execution events live in the Dev UI
ctx = hooks.ContextWithManager(ctx, ui.HookManager())

// Visit http://localhost:8080 to view real-time status
```

For more details, see [DESIGN.md](../DESIGN.md#可观测性).
