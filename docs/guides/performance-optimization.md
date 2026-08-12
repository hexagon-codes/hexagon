<div align="right">语言: 中文 | <a href="performance-optimization.en.md">English</a></div>

# 性能优化指南

本指南提供 Hexagon 应用的性能优化建议。示例使用当前公开 API；批量大小、并发数、缓存容量和超时都只是起点，应以实际压测结果和上游限额为准。

## Agent 优化

### 1. 消费流接口

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

`Stream` 返回 `StreamReader[agent.Output]`，但不要假定每种 Agent 实现都会按 token 增量输出。是否能降低首字节时间取决于具体实现，应通过基准测试确认。

### 2. 批量处理

```go
inputs := []agent.Input{input1, input2, input3}
results, err := myAgent.Batch(ctx, inputs)
if err != nil {
    return err
}
```

`Batch` 是统一批量接口，不保证每种 Agent 实现都会并发执行；例如当前 `BaseAgent` 和 `ReActAgent` 会顺序处理输入。

### 3. 合理的记忆窗口

```go
// 最多保留最近 10 个记忆条目
chatMemory := memory.NewBuffer(10)
```

### 4. 工具执行限制

```go
// 限制工具调用次数，防止死循环
myAgent := agent.NewReAct(
    agent.WithMaxIterations(5),
)
```

## RAG 优化

### 1. 向量缓存

```go
cachedEmbedder := embedder.NewCachedEmbedder(
    baseEmbedder,
    embedder.WithMaxCacheSize(1000),
)
```

### 2. 批量索引

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

并发数应同时受嵌入服务限流和向量存储连接容量约束；提高并发数不一定提高吞吐。

### 3. 查询缓存

Hexagon 当前没有通用的 `CachedRetriever`。下面由应用使用 `github.com/hexagon-codes/toolkit/cache/local` 建立有界缓存；缓存实例应在应用启动时创建并跨请求复用。

```go
// Hexagon 不提供通用 CachedRetriever；缓存由应用按业务边界建立。
queryCache := local.NewCache(1000)
defer queryCache.Stop() // 仅在应用退出时停止

const topK = 5
// key 必须包含租户、知识库版本、查询和所有检索参数。
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

只缓存可复用的无副作用结果，并设置容量、TTL 和失效策略。权限范围或知识库版本变化时必须生成不同的 key。

## 多 Agent 优化

### 1. 并行执行

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

仅将彼此独立、可安全并行的子任务放入 `ParallelAgent`。

### 2. 超时控制

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

超时通过 `context` 传递；子 Agent 和其调用的 Provider、工具也必须遵守取消信号，才能及时停止。

## 系统优化

### 1. 连接池

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

// 应用退出时调用。
defer transport.CloseIdleConnections()
```

长流式请求通常由调用方 `context` 控制总时长；不要为整个 `http.Client` 随意设置过短的 `Timeout`。

### 2. 对象复用

```go
// 使用对象池复用对象
var bufferPool = sync.Pool{
    New: func() any {
        return new(bytes.Buffer)
    },
}
```

### 3. Goroutine 限制

```go
// 限制并发 Goroutine 数量
semaphore := make(chan struct{}, 10)
for _, task := range tasks {
    semaphore <- struct{}{}
    go func(t Task) {
        defer func() { <-semaphore }()
        processTask(t)
    }(task)
}
```

## 基准测试

```bash
# 运行基准测试
go test -run '^$' -bench=. -benchmem ./bench/...

# 生成 CPU profile
go test -run '^$' -bench=. -cpuprofile=cpu.prof ./bench

# 生成内存 profile
go test -run '^$' -bench=. -memprofile=mem.prof ./bench

# 分析 profile
go tool pprof cpu.prof
```

更多详情参见 [bench/](../../bench/)。
