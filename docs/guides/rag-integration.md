<div align="right">语言: 中文 | <a href="rag-integration.en.md">English</a></div>

# RAG 系统使用指南

Hexagon 提供了完整的 RAG (Retrieval-Augmented Generation) 系统，用于构建知识密集型 AI 应用。

## 快速开始

### 基本 RAG 流程

```go
package main

import (
    "context"

    "github.com/hexagon-codes/hexagon/rag"
    "github.com/hexagon-codes/hexagon/rag/loader"
    "github.com/hexagon-codes/hexagon/rag/splitter"
    "github.com/hexagon-codes/hexagon/rag/embedder"
    "github.com/hexagon-codes/hexagon/rag/indexer"
    "github.com/hexagon-codes/hexagon/rag/retriever"
    "github.com/hexagon-codes/hexagon/rag/synthesizer"
    "github.com/hexagon-codes/ai-core/llm/openai"
    "github.com/hexagon-codes/ai-core/store/vector"
)

func main() {
    ctx := context.Background()

    // 1. 加载文档
    docs, _ := loader.NewDirectoryLoader("./docs").Load(ctx)

    // 2. 文档分割
    chunks := splitter.NewRecursiveSplitter(
        splitter.WithChunkSize(1000),
        splitter.WithChunkOverlap(200),
    ).Split(docs)

    // 3. 生成向量
    provider := openai.New("your-api-key")
    emb := embedder.NewOpenAIEmbedder(provider, "text-embedding-ada-002")
    emb.Embed(ctx, chunks)

    // 4. 索引文档
    store := vector.NewMemoryStore(1536)
    idx := indexer.NewVectorIndexer(store, emb)
    idx.Index(ctx, chunks)

    // 5. 检索相关文档
    ret := retriever.NewVectorRetriever(store, emb)
    results, _ := ret.Retrieve(ctx, "如何创建 Agent?", retriever.WithTopK(3))

    // 6. 生成答案
    syn := synthesizer.NewRefine(provider)
    answer, _ := syn.Synthesize(ctx, "如何创建 Agent?", results)

    println(answer)
}
```

## 文档加载器

### 支持的文档类型

#### 1. 文本文件

```go
// 单个文本文件
doc, err := loader.NewTextLoader("document.txt").Load(ctx)

// 目录下的所有文本文件
docs, err := loader.NewDirectoryLoader("./docs",
    loader.WithPattern("*.txt"),
    loader.WithRecursive(true),
).Load(ctx)
```

#### 2. Markdown

```go
docs, err := loader.NewMarkdownLoader("README.md").Load(ctx)
```

#### 3. URL 内容

```go
docs, err := loader.NewURLLoader("https://example.com/article").Load(ctx)
```

### 自定义加载器

```go
type CustomLoader struct{}

func (l *CustomLoader) Load(ctx context.Context) ([]rag.Document, error) {
    // 自定义加载逻辑
    return []rag.Document{
        {
            ID:      "doc-1",
            Content: "文档内容",
            Metadata: map[string]any{
                "source": "custom",
            },
        },
    }, nil
}
```

## 文档分割

### 分割策略

#### 1. 字符分割

按固定字符数分割，最简单的策略。

```go
splitter := splitter.NewCharacterSplitter(
    splitter.WithChunkSize(1000),
    splitter.WithChunkOverlap(200),
)
```

#### 2. 递归分割

智能分割，优先在段落、句子边界分割。

```go
splitter := splitter.NewRecursiveSplitter(
    splitter.WithChunkSize(1000),
    splitter.WithChunkOverlap(200),
    splitter.WithSeparators([]string{"\n\n", "\n", "。", "，"}),
)
```

**推荐使用**，效果最好。

#### 3. Markdown 分割

按 Markdown 结构分割（标题、段落）。

```go
splitter := splitter.NewMarkdownSplitter(
    splitter.WithChunkSize(1000),
)
```

#### 4. 语义分割

基于语义相似度分割，效果最好但速度较慢。

```go
splitter := splitter.NewSemanticSplitter(
    embedder,
    splitter.WithThreshold(0.75), // 相似度阈值
)
```

### 分割参数调优

| 参数 | 推荐值 | 说明 |
|-----|-------|------|
| ChunkSize | 500-1500 | 根据文档特性调整 |
| ChunkOverlap | 100-300 | 10-20% 的 ChunkSize |
| Separators | `["\n\n", "\n", "。"]` | 中文优先句号 |

## 向量生成

### OpenAI Embeddings

```go
embedder := embedder.NewOpenAIEmbedder(
    provider,
    "text-embedding-ada-002", // 1536 维
)

// 或使用更小的模型
embedder := embedder.NewOpenAIEmbedder(
    provider,
    "text-embedding-3-small", // 512 维，速度更快
)
```

### 缓存 Embedder

避免重复计算向量，提升性能。

```go
cachedEmbedder := embedder.NewCachedEmbedder(
    baseEmbedder,
    embedder.WithCacheSize(1000),
)
```

## 向量存储

### 内存存储

适合开发和小规模数据。

```go
store := vector.NewMemoryStore(1536) // 向量维度
```

### Qdrant

生产环境推荐，性能优秀。

```go
import "github.com/hexagon-codes/ai-core/store/vector/qdrant"

store, err := qdrant.New(qdrant.Config{
    Host:       "localhost",
    Port:       6333,
    Collection: "documents",
    Dimension:  1536,
})
defer store.Close()
```

## 检索策略

### 1. 向量检索

基于语义相似度的检索。

```go
retriever := retriever.NewVectorRetriever(store, embedder)

results, _ := retriever.Retrieve(ctx, query,
    retriever.WithTopK(5),              // 返回前5个结果
    retriever.WithMinScore(0.7),        // 最小相似度0.7
    retriever.WithFilters(map[string]any{
        "type": "technical",             // 元数据过滤
    }),
)
```

### 2. 关键词检索

基于 BM25 算法的关键词匹配。

```go
retriever := retriever.NewKeywordRetriever(index)

results, _ := retriever.Retrieve(ctx, query,
    retriever.WithTopK(5),
)
```

### 3. 混合检索

结合向量和关键词检索。

```go
retriever := retriever.NewHybridRetriever(
    vectorRetriever,
    keywordRetriever,
    retriever.WithAlpha(0.7), // 向量权重 0.7，关键词权重 0.3
)
```

### 4. 多查询检索

生成多个查询变体提升召回率。

```go
retriever := retriever.NewMultiQueryRetriever(
    baseRetriever,
    llmProvider,
    retriever.WithNumQueries(3), // 生成3个查询变体
)
```

## 重排序

提升检索结果的相关性。

```go
import "github.com/hexagon-codes/hexagon/rag/reranker"

// 创建重排序器
reranker := reranker.NewLLMReranker(llmProvider)

// 对检索结果重排序
reranked, _ := reranker.Rerank(ctx, query, results,
    reranker.WithTopN(3), // 返回前3个
)
```

## 答案合成

### 1. Refine 策略

逐个处理文档，精炼答案。

```go
synthesizer := synthesizer.NewRefine(llmProvider,
    synthesizer.WithSystemPrompt("基于提供的上下文回答问题"),
)

answer, _ := synthesizer.Synthesize(ctx, query, results)
```

**适用场景**: 需要综合多个文档的信息。

### 2. Compact 策略

将所有文档合并为一个提示。

```go
synthesizer := synthesizer.NewCompact(llmProvider)

answer, _ := synthesizer.Synthesize(ctx, query, results)
```

**适用场景**: 文档较少，上下文窗口足够大。

### 3. Tree 策略

树状聚合，分层总结。

```go
synthesizer := synthesizer.NewTree(llmProvider,
    synthesizer.WithBranchFactor(3), // 每层3个分支
)

answer, _ := synthesizer.Synthesize(ctx, query, results)
```

**适用场景**: 大量文档需要处理。

## 完整 RAG Engine

使用 RAG Engine 简化流程。

```go
engine := rag.NewEngine(
    rag.WithLLM(llmProvider),
    rag.WithEmbedder(embedder),
    rag.WithVectorStore(store),
    rag.WithRetrieverType(rag.RetrieverTypeHybrid),
    rag.WithSynthesizerType(rag.SynthesizerTypeRefine),
)

// 索引文档
engine.Index(ctx, docs)

// 查询
answer, _ := engine.Query(ctx, "如何使用 RAG?")
```

## 性能优化

### 1. 批量处理

```go
// 批量生成向量
embedder.EmbedBatch(ctx, chunks, embedder.WithBatchSize(100))

// 批量索引
indexer.IndexBatch(ctx, chunks, indexer.WithConcurrency(4))
```

### 2. 异步索引

```go
// 在后台索引文档
go func() {
    indexer.Index(ctx, docs)
}()
```

### 3. 增量更新

```go
// 只索引新文档
indexer.IndexIncremental(ctx, newDocs)
```

### 4. 缓存策略

```go
// 缓存查询结果
engine := rag.NewEngine(
    rag.WithQueryCache(
        cache.NewLRU(100), // 缓存100个查询结果
    ),
)
```

## 评估指标

### 检索质量

```go
import "github.com/hexagon-codes/hexagon/evaluate/metrics"

// 相关性评估
relevance := metrics.NewRelevanceMetric(llmProvider)
score, _ := relevance.Evaluate(ctx, evaluate.EvalInput{
    Query:    query,
    Context:  results,
    Response: answer,
})

// 忠实度评估（答案是否基于上下文）
faithfulness := metrics.NewFaithfulnessMetric(llmProvider)
score, _ := faithfulness.Evaluate(ctx, input)
```

## 常见问题

### Q: 如何选择分块大小？

**A**:
- 短文本 (新闻、评论): 300-500
- 中等文本 (文章、文档): 500-1000
- 长文本 (书籍、论文): 1000-1500

### Q: 向量检索 vs 关键词检索？

**A**:
- 向量检索: 语义理解好，但对精确匹配较弱
- 关键词检索: 精确匹配好，但理解能力弱
- **推荐**: 混合检索，结合两者优势

### Q: 如何处理中文？

**A**:
- 使用支持中文的 Embedder
- 分割时使用中文标点符号: `["。", "！", "？", "\n\n"]`
- Markdown 分割器对中文支持良好

### Q: 如何提升检索准确率？

**A**:
1. 使用语义分割而非固定长度分割
2. 合适的 Chunk Overlap
3. 使用重排序器
4. 多查询检索生成查询变体
5. 混合检索策略

## 下一步

- 了解 [Agent 集成 RAG](./agent-development.md#rag-集成)
- 学习 [图编排中的 RAG 节点](./graph-orchestration.md#rag-节点)
- 掌握 [RAG 性能优化](./performance-optimization.md#rag-优化)
