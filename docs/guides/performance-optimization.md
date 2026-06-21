<div align="right">语言: 中文 | <a href="performance-optimization.en.md">English</a></div>

# 性能优化指南

本指南提供 Hexagon 应用的性能优化最佳实践。

## Agent 优化

### 1. 流式输出

```go
// 使用流式输出减少首字节时间
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

### 2. 批量处理

```go
// 批量处理多个请求
inputs := []agent.Input{input1, input2, input3}
results, _ := agent.Batch(ctx, inputs)
```

### 3. 合理的记忆窗口

```go
// 避免记忆窗口过大
memory := memory.NewBufferMemory(
    memory.WithMaxMessages(10), // 只保留最近10条
)
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
embedder := embedder.NewCachedEmbedder(
    baseEmbedder,
    embedder.WithCacheSize(1000),
)
```

### 2. 批量索引

```go
indexer.IndexBatch(ctx, docs,
    indexer.WithBatchSize(100),
    indexer.WithConcurrency(4),
)
```

### 3. 查询缓存

```go
// 缓存相似查询的结果
retriever := retriever.NewCachedRetriever(
    baseRetriever,
    cache.NewLRU(100),
)
```

## 多 Agent 优化

### 1. 并行执行

```go
// 使用并行模式加速独立任务
team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeParallel),
)
```

### 2. 超时控制

```go
team := agent.NewTeam(
    agent.WithTeamTimeout(5 * time.Minute),
)
```

### 3. 结果缓存

```go
// 缓存 Agent 结果避免重复计算
agent := agent.NewCachedAgent(baseAgent, cache.NewLRU(50))
```

## 系统优化

### 1. 连接池

```go
// LLM 客户端使用连接池
provider := openai.New(apiKey,
    openai.WithMaxConnections(100),
)
```

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
go test -bench=. -benchmem ./bench/...

# 生成 CPU profile
go test -cpuprofile=cpu.prof -bench=.

# 生成内存 profile
go test -memprofile=mem.prof -bench=.

# 分析 profile
go tool pprof cpu.prof
```

更多详情参见 [bench/](../../bench/)。
