<div align="right">Language: <a href="performance-optimization.md">中文</a> | English</div>

# Performance Optimization Guide

This guide provides performance optimization guidance for Hexagon applications. The examples use the current public APIs; batch sizes, concurrency limits, cache capacities, and timeouts are only starting points and should be tuned against real benchmarks and upstream limits.

## Agent Optimization

### 1. Consuming the Stream API

```go
reader, err := myAgent.Stream(ctx, input)
if err != nil {
    return err
}
defer reader.Close()

for {
    output, err := reader.Recv()
    if errors.Is(err, io.EOF) {
        return nil
    }
    if err != nil {
        return err
    }
    fmt.Print(output.Content)
}
```

`Stream` returns a `StreamReader[agent.Output]`, but do not assume that every Agent implementation emits token-level increments. Whether it improves time-to-first-byte depends on the implementation and must be verified with benchmarks.

### 2. Batch Processing

```go
inputs := []agent.Input{input1, input2, input3}
results, err := myAgent.Batch(ctx, inputs)
if err != nil {
    return err
}
```

`Batch` is the unified bulk API; it does not guarantee concurrent execution for every Agent implementation. For example, the current `BaseAgent` and `ReActAgent` process inputs sequentially.

### 3. Appropriate Memory Window Size

```go
// 最多保留最近 10 条记忆。
chatMemory := memory.NewBuffer(10)
```

### 4. Tool Execution Limits

```go
// Limit tool call count to prevent infinite loops
myAgent := agent.NewReAct(
    agent.WithMaxIterations(5),
)
```

## RAG Optimization

### 1. Embedding Cache

```go
cachedEmbedder := embedder.NewCachedEmbedder(
    baseEmbedder,
    embedder.WithMaxCacheSize(1000),
)
```

### 2. Batch Indexing

```go
concurrentIndexer := indexer.NewConcurrentIndexer(
    vectorStore,
    baseEmbedder,
    indexer.WithConcurrentBatchSize(100),
    indexer.WithConcurrency(4),
)

if err := concurrentIndexer.Index(ctx, docs); err != nil {
    return err
}
```

Concurrency must respect both embedding-service rate limits and vector-store connection capacity; increasing it does not necessarily improve throughput.

### 3. Query Cache

Hexagon currently has no generic `CachedRetriever`. The following example uses `github.com/hexagon-codes/toolkit/cache/local` to create an application-owned bounded cache; create the cache at application startup and reuse it across requests.

```go
// Hexagon 不提供通用 CachedRetriever；该缓存由应用持有。
queryCache := local.NewCache(1000)
defer queryCache.Stop() // stop it only when the application shuts down

const topK = 5
// key 必须包含租户、知识库版本、查询以及所有检索参数。
keySource := fmt.Sprintf("%q|%q|%q|%d", tenantID, knowledgeBaseVersion, query, topK)
cacheKey := fmt.Sprintf("%x", sha256.Sum256([]byte(keySource)))

var documents []rag.Document
err := queryCache.GetOrLoad(
    ctx,
    cacheKey,
    5*time.Minute,
    &documents,
    func(loadCtx context.Context) (any, error) {
        return baseRetriever.Retrieve(loadCtx, query, rag.WithTopK(topK))
    },
)
if err != nil {
    return err
}
```

Cache only reusable, side-effect-free results, and configure a capacity, TTL, and invalidation policy. A permission-scope or knowledge-base-version change must produce a different key.

## Multi-Agent Optimization

### 1. Parallel Execution

```go
parallelAgent := agent.NewParallelAgent(
    "analysis-team",
    []agent.Agent{researcher, writer, reviewer},
    agent.WithMaxParallel(3),
)

result, err := parallelAgent.Invoke(ctx, input)
if err != nil {
    return err
}
```

Only put independent tasks that are safe to execute concurrently in a `ParallelAgent`.

### 2. Timeout Control

```go
runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
defer cancel()

result, err := parallelAgent.Invoke(runCtx, input)
if ctxErr := runCtx.Err(); ctxErr != nil {
    return ctxErr
}
if err != nil {
    return err
}
```

Timeouts are propagated through `context`; child Agents and the Providers and tools they call must also honor cancellation to stop promptly.

## System Optimization

### 1. Connection Pooling

```go
transport := http.DefaultTransport.(*http.Transport).Clone()
transport.MaxIdleConns = 100
transport.MaxIdleConnsPerHost = 20
transport.IdleConnTimeout = 90 * time.Second

httpClient := &http.Client{Transport: transport}
provider := openai.New(
    apiKey,
    openai.WithHTTPClient(httpClient),
)

// 应用关闭时调用。
defer transport.CloseIdleConnections()
```

Long-lived streaming requests are normally bounded by the caller's `context`; avoid setting an arbitrarily short timeout on the entire `http.Client`.

### 2. Object Reuse

```go
// Use an object pool to reuse objects
var bufferPool = sync.Pool{
    New: func() any {
        return new(bytes.Buffer)
    },
}
```

### 3. Goroutine Limiting

```go
// Limit the number of concurrent goroutines
semaphore := make(chan struct{}, 10)
for _, task := range tasks {
    semaphore <- struct{}{}
    go func(t Task) {
        defer func() { <-semaphore }()
        processTask(t)
    }(task)
}
```

## Benchmarking

```bash
# Run benchmarks
go test -run '^$' -bench=. -benchmem ./bench/...

# Generate a CPU profile
go test -run '^$' -bench=. -cpuprofile=cpu.prof ./bench

# Generate a memory profile
go test -run '^$' -bench=. -memprofile=mem.prof ./bench

# Analyze a profile
go tool pprof cpu.prof
```

For more details, see [bench/](../../bench/).
