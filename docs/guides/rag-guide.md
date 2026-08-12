<div align="right">语言: 中文 | <a href="rag-guide.en.md">English</a></div>

# RAG 系统使用指南

本指南介绍如何使用 Hexagon 构建 RAG（检索增强生成）系统。

## 概述

RAG 系统通过检索相关文档来增强 LLM 的回答能力，主要包含以下步骤：

1. **文档加载**: 从各种来源加载文档
2. **文档分割**: 将长文档切分为适当大小的块
3. **向量化**: 将文档转换为向量表示
4. **索引存储**: 将向量存储到数据库
5. **检索**: 根据查询检索相关文档
6. **生成**: 基于检索结果生成回答

## 快速开始

推荐使用 `rag.Engine`，它把向量存储、嵌入器与检索串成一站式入口：

```go
import (
    "github.com/hexagon-codes/hexagon/rag"
    "github.com/hexagon-codes/hexagon/rag/embedder"
    "github.com/hexagon-codes/ai-core/store/vector/qdrant"
)

// 1. 创建向量存储
store, err := qdrant.NewWithOptions(
    qdrant.WithCollection("knowledge"),
    qdrant.WithDimension(1536),
    qdrant.WithCreateCollection(true),
)

// 2. 创建嵌入器（provider 为实现 Embed 的 LLM Provider）
emb := embedder.NewOpenAIEmbedder(provider)

// 3. 创建 RAG 引擎
engine := rag.NewEngine(
    rag.WithStore(store),
    rag.WithEngineEmbedder(emb),
    rag.WithEngineTopK(5),
)

// 4. 索引文档
_ = engine.Index(ctx, docs)

// 5. 检索并拼接上下文（返回可直接喂给 LLM 的字符串）
context, err := engine.Query(ctx, "什么是 Hexagon？")
```

> 如需手工组装管道，可使用位置参数构造器
> `rag.NewPipeline(loader, splitter, indexer, retriever)`，其 `Query` 方法返回 `[]rag.Document`。

## 文档加载

加载器位于 `rag/loader` 子包。

### 文本文件

```go
import "github.com/hexagon-codes/hexagon/rag/loader"

l := loader.NewTextLoader("./docs/intro.txt")
docs, err := l.Load(ctx)
```

### PDF 文件

```go
l := loader.NewPDFLoader("./documents/manual.pdf")
docs, err := l.Load(ctx)
```

### 网页

```go
l := loader.NewURLLoader("https://example.com/page1")
docs, err := l.Load(ctx)
```

### 目录

```go
l := loader.NewDirectoryLoader("./docs",
    loader.WithPattern("*.md"),
    loader.WithRecursive(true),
)
docs, err := l.Load(ctx)
```

### 自定义加载器

实现 `rag.Loader` 接口（`Load` + `Name`）即可：

```go
type MyLoader struct{}

func (l *MyLoader) Load(ctx context.Context) ([]rag.Document, error) {
    // 自定义加载逻辑
    return docs, nil
}

func (l *MyLoader) Name() string { return "my-loader" }
```

## 文档分割

分割器位于 `rag/splitter` 子包，`Split` 方法签名为 `Split(ctx, docs)`。

### 字符分割

```go
import "github.com/hexagon-codes/hexagon/rag/splitter"

s := splitter.NewCharacterSplitter(
    splitter.WithChunkSize(1000),
    splitter.WithChunkOverlap(200),
)
chunks, err := s.Split(ctx, docs)
```

### 递归分割

```go
s := splitter.NewRecursiveSplitter(
    splitter.WithSeparators([]string{"\n\n", "\n", " "}),
    splitter.WithRecursiveChunkSize(1000),
)
chunks, err := s.Split(ctx, docs)
```

### 句子分割

```go
s := splitter.NewSentenceSplitter(
    splitter.WithSentenceChunkSize(1000),
    splitter.WithSentenceChunkOverlap(100),
)
chunks, err := s.Split(ctx, docs)
```

## 向量存储

向量存储实现位于 ai-core 的 `store/vector` 包。

### Qdrant

```go
import "github.com/hexagon-codes/ai-core/store/vector/qdrant"

store, err := qdrant.NewWithOptions(
    qdrant.WithHost("localhost"),
    qdrant.WithPort(6333),
    qdrant.WithCollection("documents"),
    qdrant.WithDimension(1536),
    qdrant.WithCreateCollection(true),
)
```

#### ai-core v0.2.7 point ID 迁移

ai-core v0.2.7 起，新集合默认使用 `PointIDUUIDv8`，将文档 ID 通过 SHA-256 派生为 UUIDv8。旧版本创建的集合仍使用 legacy hash31 映射，升级时应按以下顺序迁移：

1. 停止对旧集合的写入，保留它用于只读校验和回滚。
2. 为旧集合单独创建客户端，显式设置 `PointIDLegacyHash31` 和 `WithCreateCollection(false)`。该选项只选择旧 ID 映射，**不会**把客户端变成只读；迁移代码只能调用 `Get`、`Search`、`Count` 等读方法，不得调用 `Add`、`Delete` 或 `Clear`。
3. 创建不同名称的新集合，使用默认的 `PointIDUUIDv8`（也可显式设置），从权威原始文档或经核验的迁移数据重新索引。校验数量和抽样检索结果后，再把读流量切换到新集合。

```go
legacyStore, err := qdrant.NewWithOptions(
    qdrant.WithHost("localhost"),
    qdrant.WithPort(6333),
    qdrant.WithCollection("documents_legacy"),
    qdrant.WithDimension(1536),
    qdrant.WithCreateCollection(false),
    qdrant.WithPointIDStrategy(qdrant.PointIDLegacyHash31),
)
// legacyStore 仅用于读取和迁移校验，不再写入。

uuidStore, err := qdrant.NewWithOptions(
    qdrant.WithHost("localhost"),
    qdrant.WithPort(6333),
    qdrant.WithCollection("documents_uuidv8"),
    qdrant.WithDimension(1536),
    qdrant.WithCreateCollection(true),
    qdrant.WithPointIDStrategy(qdrant.PointIDUUIDv8), // 新集合的默认策略
)
// 使用索引器将完整文档重新写入 uuidStore。
```

> **禁止混写：** 不要让 `PointIDLegacyHash31` 与 `PointIDUUIDv8` 客户端向同一集合写入。同一逻辑文档 ID 在两种策略下会映射为不同的 Qdrant point ID，可能产生重复或陈旧数据；legacy hash31 还存在确定性碰撞风险。

### 内存存储（开发测试）

```go
import "github.com/hexagon-codes/ai-core/store/vector"

store := vector.NewMemoryStore(1536)
```

## 检索策略

检索器位于 `rag/retriever` 子包。

### 向量检索

```go
import "github.com/hexagon-codes/hexagon/rag/retriever"

vectorRetriever := retriever.NewVectorRetriever(store, embedder,
    retriever.WithTopK(5),
    retriever.WithMinScore(0.7),
)
```

### 关键词检索

```go
keywordRetriever := retriever.NewKeywordRetriever(docs,
    retriever.WithKeywordTopK(10),
)
```

### 混合检索

```go
hybridRetriever := retriever.NewHybridRetriever(
    vectorRetriever,
    keywordRetriever,
    retriever.WithVectorWeight(0.7),
    retriever.WithKeywordWeight(0.3),
)
```

## 重排序

提高检索结果的相关性：

### 分数过滤

```go
reranker := reranker.NewScoreReranker(
    reranker.WithScoreMin(0.5),
    reranker.WithScoreTopK(5),
)
```

### 跨编码器重排序

```go
reranker := reranker.NewCrossEncoderReranker(
    reranker.WithCrossEncoderModel("http://localhost:8080"),
    reranker.WithCrossEncoderTopK(5),
)
```

### Cohere 重排序

```go
reranker := reranker.NewCohereReranker(apiKey,
    reranker.WithCohereModel("rerank-english-v2.0"),
    reranker.WithCohereTopK(5),
)
```

### LLM 重排序

```go
reranker := reranker.NewLLMReranker(llm,
    reranker.WithLLMRerankerTopK(5),
)
```

### RRF 融合

合并多个检索结果：

```go
reranker := reranker.NewRRFReranker(
    reranker.WithRRFK(60),
    reranker.WithRRFTopK(10),
)

// 融合多个排名列表
results := reranker.FuseRankings(ranking1, ranking2, ranking3)
```

### 链式重排序

```go
chain := reranker.NewChainReranker(
    scoreReranker,
    crossEncoderReranker,
)
```

## 响应合成

合成器位于 `rag/synthesizer` 子包，LLM 通过选项传入，`Synthesize(ctx, query, docs)` 返回 `*synthesizer.Response`。

### 简单合成

```go
import "github.com/hexagon-codes/hexagon/rag/synthesizer"

s := synthesizer.NewSimpleSummarizeSynthesizer(
    synthesizer.WithSimpleSynthesizerLLM(llm),
)
response, err := s.Synthesize(ctx, query, docs)
```

### 精炼合成

迭代优化回答：

```go
s := synthesizer.NewRefineSynthesizer(
    synthesizer.WithRefineSynthesizerLLM(llm),
    synthesizer.WithRefinePrompt("基于以下新信息完善回答..."),
)
```

### 压缩合成

压缩多个文档：

```go
s := synthesizer.NewCompactSynthesizer(
    synthesizer.WithCompactSynthesizerLLM(llm),
    synthesizer.WithCompactSynthesizerMaxContext(4000),
)
```

## 完整管道

`rag.NewPipeline` 使用位置参数 `(loader, splitter, indexer, retriever)`，`Ingest` 负责加载-分割-索引，`Query` 返回 `[]rag.Document`：

```go
pipeline := rag.NewPipeline(textLoader, charSplitter, vectorIndexer, vectorRetriever)

// 一次性完成加载、分割、索引
_ = pipeline.Ingest(ctx)

// 检索（返回文档列表，可再交给 reranker / synthesizer）
docs, err := pipeline.Query(ctx, "你的问题",
    rag.WithTopK(10),
    rag.WithMinScore(0.5),
)
```

## 索引器

索引器位于 `rag/indexer` 子包，`Index(ctx, docs)` 写入向量存储：

```go
import "github.com/hexagon-codes/hexagon/rag/indexer"

idx := indexer.NewVectorIndexer(store, embedder,
    indexer.WithBatchSize(100),
)

// 索引文档
err := idx.Index(ctx, docs)
```

## 监控指标

```go
import "github.com/hexagon-codes/hexagon/observe/metrics"

collector := metrics.GetHexagonMetrics()

// RAG Engine/Pipeline 不会自动写入这组聚合指标；检索完成后需显式记录。
collector.RecordRetrieval(ctx, "vector_search", docCount, duration)

// 查看统计
stats := collector.GetRetrievalStats()
fmt.Printf("平均检索时间: %v\n", stats.AverageDuration)
fmt.Printf("平均文档数: %.2f\n", stats.AverageDocCount)
```

## 最佳实践

1. **合理的分块大小**: 通常 500-1500 字符效果较好
2. **适当的重叠**: 10-20% 的重叠避免信息丢失
3. **多级检索**: 先粗筛后精排
4. **缓存嵌入**: 避免重复计算向量
5. **监控召回率**: 定期评估检索质量
6. **增量更新**: 支持文档的增删改
