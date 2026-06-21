<div align="right">Language: <a href="performance-optimization.md">中文</a> | English</div>

# Performance Optimization Guide

This guide provides best practices for optimizing Hexagon applications.

## Agent Optimization

### 1. Streaming Output

```go
// Use streaming output to reduce time-to-first-byte
reader, _ := myAgent.Stream(ctx, input)
defer reader.Close()
for {
    chunk, err := reader.Recv()
    if errors.Is(err, io.EOF) {
        break
    }
    if err != nil {
        break
    }
    fmt.Print(chunk.Content)
}
```

### 2. Batch Processing

```go
// Process multiple requests in a batch
inputs := []agent.Input{input1, input2, input3}
results, _ := agent.Batch(ctx, inputs)
```

### 3. Appropriate Memory Window Size

```go
// Avoid excessively large memory windows
memory := memory.NewBufferMemory(
    memory.WithMaxMessages(10), // keep only the last 10 messages
)
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
embedder := embedder.NewCachedEmbedder(
    baseEmbedder,
    embedder.WithCacheSize(1000),
)
```

### 2. Batch Indexing

```go
indexer.IndexBatch(ctx, docs,
    indexer.WithBatchSize(100),
    indexer.WithConcurrency(4),
)
```

### 3. Query Cache

```go
// Cache results for similar queries
retriever := retriever.NewCachedRetriever(
    baseRetriever,
    cache.NewLRU(100),
)
```

## Multi-Agent Optimization

### 1. Parallel Execution

```go
// Use parallel mode to speed up independent tasks
team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeParallel),
)
```

### 2. Timeout Control

```go
team := agent.NewTeam(
    agent.WithTeamTimeout(5 * time.Minute),
)
```

### 3. Result Caching

```go
// Cache Agent results to avoid redundant computation
agent := agent.NewCachedAgent(baseAgent, cache.NewLRU(50))
```

## System Optimization

### 1. Connection Pooling

```go
// Use connection pooling for the LLM client
provider := openai.New(apiKey,
    openai.WithMaxConnections(100),
)
```

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
go test -bench=. -benchmem ./bench/...

# Generate a CPU profile
go test -cpuprofile=cpu.prof -bench=.

# Generate a memory profile
go test -memprofile=mem.prof -bench=.

# Analyze a profile
go tool pprof cpu.prof
```

For more details, see [bench/](../../bench/).
