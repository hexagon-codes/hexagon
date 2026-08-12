<div align="right">Language: <a href="rag-guide.md">中文</a> | English</div>

# RAG System User Guide

This guide explains how to build a RAG (Retrieval-Augmented Generation) system with Hexagon.

## Overview

A RAG system enhances LLM responses by retrieving relevant documents. The main steps are:

1. **Document Loading**: Load documents from various sources
2. **Document Splitting**: Split long documents into appropriately sized chunks
3. **Vectorization**: Convert documents into vector representations
4. **Index Storage**: Store vectors in a database
5. **Retrieval**: Retrieve relevant documents based on a query
6. **Generation**: Generate answers based on the retrieved results

## Quick Start

The recommended entry point is `rag.Engine`, which wires the vector store, embedder, and retrieval into a single facade:

```go
import (
    "github.com/hexagon-codes/hexagon/rag"
    "github.com/hexagon-codes/hexagon/rag/embedder"
    "github.com/hexagon-codes/ai-core/store/vector/qdrant"
)

// 1. Create vector store
store, err := qdrant.NewWithOptions(
    qdrant.WithCollection("knowledge"),
    qdrant.WithDimension(1536),
    qdrant.WithCreateCollection(true),
)

// 2. Create embedder (provider is any LLM Provider implementing Embed)
emb := embedder.NewOpenAIEmbedder(provider)

// 3. Create the RAG engine
engine := rag.NewEngine(
    rag.WithStore(store),
    rag.WithEngineEmbedder(emb),
    rag.WithEngineTopK(5),
)

// 4. Index documents
_ = engine.Index(ctx, docs)

// 5. Retrieve and assemble context (returns a string ready to feed an LLM)
context, err := engine.Query(ctx, "What is Hexagon?")
```

> To assemble a pipeline manually, use the positional constructor
> `rag.NewPipeline(loader, splitter, indexer, retriever)`; its `Query` method returns `[]rag.Document`.

## Document Loading

Loaders live in the `rag/loader` subpackage.

### Text Files

```go
import "github.com/hexagon-codes/hexagon/rag/loader"

l := loader.NewTextLoader("./docs/intro.txt")
docs, err := l.Load(ctx)
```

### PDF Files

```go
l := loader.NewPDFLoader("./documents/manual.pdf")
docs, err := l.Load(ctx)
```

### Web Pages

```go
l := loader.NewURLLoader("https://example.com/page1")
docs, err := l.Load(ctx)
```

### Directory

```go
l := loader.NewDirectoryLoader("./docs",
    loader.WithPattern("*.md"),
    loader.WithRecursive(true),
)
docs, err := l.Load(ctx)
```

### Custom Loader

Implement the `rag.Loader` interface (`Load` + `Name`):

```go
type MyLoader struct{}

func (l *MyLoader) Load(ctx context.Context) ([]rag.Document, error) {
    // Custom loading logic
    return docs, nil
}

func (l *MyLoader) Name() string { return "my-loader" }
```

## Document Splitting

Splitters live in the `rag/splitter` subpackage; the `Split` method signature is `Split(ctx, docs)`.

### Character Splitting

```go
import "github.com/hexagon-codes/hexagon/rag/splitter"

s := splitter.NewCharacterSplitter(
    splitter.WithChunkSize(1000),
    splitter.WithChunkOverlap(200),
)
chunks, err := s.Split(ctx, docs)
```

### Recursive Splitting

```go
s := splitter.NewRecursiveSplitter(
    splitter.WithSeparators([]string{"\n\n", "\n", " "}),
    splitter.WithRecursiveChunkSize(1000),
)
chunks, err := s.Split(ctx, docs)
```

### Sentence Splitting

```go
s := splitter.NewSentenceSplitter(
    splitter.WithSentenceChunkSize(1000),
    splitter.WithSentenceChunkOverlap(100),
)
chunks, err := s.Split(ctx, docs)
```

## Vector Storage

Vector store implementations live in ai-core's `store/vector` package.

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

#### Point ID migration for ai-core v0.2.7

Starting with ai-core v0.2.7, new collections default to `PointIDUUIDv8`, which derives UUIDv8 point IDs from document IDs using SHA-256. Collections created by earlier versions still use the legacy hash31 mapping. Migrate them in this order:

1. Stop writes to the existing collection and retain it for read-only verification and rollback.
2. Create a dedicated client for the existing collection with `PointIDLegacyHash31` and `WithCreateCollection(false)`. This option only selects the old ID mapping; it does **not** make the client read-only. Migration code may call read methods such as `Get`, `Search`, and `Count`, but must not call `Add`, `Delete`, or `Clear`.
3. Create a differently named collection using the default `PointIDUUIDv8` strategy (or select it explicitly), then reindex from the authoritative source documents or verified migration data. Switch read traffic only after validating counts and sampled retrieval results.

```go
legacyStore, err := qdrant.NewWithOptions(
    qdrant.WithHost("localhost"),
    qdrant.WithPort(6333),
    qdrant.WithCollection("documents_legacy"),
    qdrant.WithDimension(1536),
    qdrant.WithCreateCollection(false),
    qdrant.WithPointIDStrategy(qdrant.PointIDLegacyHash31),
)
// legacyStore 仅用于读取与迁移校验，禁止写入。

uuidStore, err := qdrant.NewWithOptions(
    qdrant.WithHost("localhost"),
    qdrant.WithPort(6333),
    qdrant.WithCollection("documents_uuidv8"),
    qdrant.WithDimension(1536),
    qdrant.WithCreateCollection(true),
    qdrant.WithPointIDStrategy(qdrant.PointIDUUIDv8), // Default for new collections
)
// 使用 indexer 将完整文档集写入 uuidStore。
```

> **Do not mix strategies:** Never let `PointIDLegacyHash31` and `PointIDUUIDv8` clients write to the same collection. The same logical document ID maps to different Qdrant point IDs under the two strategies, which can create duplicate or stale data; legacy hash31 also has deterministic collision risk.

### In-Memory Store (Development & Testing)

```go
import "github.com/hexagon-codes/ai-core/store/vector"

store := vector.NewMemoryStore(1536)
```

## Retrieval Strategies

Retrievers live in the `rag/retriever` subpackage.

### Vector Retrieval

```go
import "github.com/hexagon-codes/hexagon/rag/retriever"

vectorRetriever := retriever.NewVectorRetriever(store, embedder,
    retriever.WithTopK(5),
    retriever.WithMinScore(0.7),
)
```

### Keyword Retrieval

```go
keywordRetriever := retriever.NewKeywordRetriever(docs,
    retriever.WithKeywordTopK(10),
)
```

### Hybrid Retrieval

```go
hybridRetriever := retriever.NewHybridRetriever(
    vectorRetriever,
    keywordRetriever,
    retriever.WithVectorWeight(0.7),
    retriever.WithKeywordWeight(0.3),
)
```

## Reranking

Improve the relevance of retrieved results:

### Score Filtering

```go
reranker := reranker.NewScoreReranker(
    reranker.WithScoreMin(0.5),
    reranker.WithScoreTopK(5),
)
```

### Cross-Encoder Reranking

```go
reranker := reranker.NewCrossEncoderReranker(
    reranker.WithCrossEncoderModel("http://localhost:8080"),
    reranker.WithCrossEncoderTopK(5),
)
```

### Cohere Reranking

```go
reranker := reranker.NewCohereReranker(apiKey,
    reranker.WithCohereModel("rerank-english-v2.0"),
    reranker.WithCohereTopK(5),
)
```

### LLM Reranking

```go
reranker := reranker.NewLLMReranker(llm,
    reranker.WithLLMRerankerTopK(5),
)
```

### RRF Fusion

Merge results from multiple retrievers:

```go
reranker := reranker.NewRRFReranker(
    reranker.WithRRFK(60),
    reranker.WithRRFTopK(10),
)

// Fuse multiple ranking lists
results := reranker.FuseRankings(ranking1, ranking2, ranking3)
```

### Chained Reranking

```go
chain := reranker.NewChainReranker(
    scoreReranker,
    crossEncoderReranker,
)
```

## Response Synthesis

Synthesizers live in the `rag/synthesizer` subpackage; the LLM is passed via an option, and `Synthesize(ctx, query, docs)` returns a `*synthesizer.Response`.

### Simple Synthesis

```go
import "github.com/hexagon-codes/hexagon/rag/synthesizer"

s := synthesizer.NewSimpleSummarizeSynthesizer(
    synthesizer.WithSimpleSynthesizerLLM(llm),
)
response, err := s.Synthesize(ctx, query, docs)
```

### Refine Synthesis

Iteratively refine the answer:

```go
s := synthesizer.NewRefineSynthesizer(
    synthesizer.WithRefineSynthesizerLLM(llm),
    synthesizer.WithRefinePrompt("Improve the answer based on the following new information..."),
)
```

### Compact Synthesis

Compress multiple documents:

```go
s := synthesizer.NewCompactSynthesizer(
    synthesizer.WithCompactSynthesizerLLM(llm),
    synthesizer.WithCompactSynthesizerMaxContext(4000),
)
```

## Full Pipeline

`rag.NewPipeline` takes positional arguments `(loader, splitter, indexer, retriever)`; `Ingest` runs load-split-index, and `Query` returns `[]rag.Document`:

```go
pipeline := rag.NewPipeline(textLoader, charSplitter, vectorIndexer, vectorRetriever)

// Run load, split, and index in one pass
_ = pipeline.Ingest(ctx)

// Retrieve (returns a document list you can hand to a reranker / synthesizer)
docs, err := pipeline.Query(ctx, "Your question",
    rag.WithTopK(10),
    rag.WithMinScore(0.5),
)
```

## Indexer

Indexers live in the `rag/indexer` subpackage; `Index(ctx, docs)` writes to the vector store:

```go
import "github.com/hexagon-codes/hexagon/rag/indexer"

idx := indexer.NewVectorIndexer(store, embedder,
    indexer.WithBatchSize(100),
)

// Index documents
err := idx.Index(ctx, docs)
```

## Monitoring Metrics

```go
import "github.com/hexagon-codes/hexagon/observe/metrics"

collector := metrics.GetHexagonMetrics()

// RAG Engine/Pipeline 不会自动填充这些聚合指标；
// 每次检索完成后必须显式记录。
collector.RecordRetrieval(ctx, "vector_search", docCount, duration)

// View statistics
stats := collector.GetRetrievalStats()
fmt.Printf("Average retrieval time: %v\n", stats.AverageDuration)
fmt.Printf("Average document count: %.2f\n", stats.AverageDocCount)
```

## Best Practices

1. **Reasonable chunk size**: 500–1500 characters typically works well
2. **Appropriate overlap**: 10–20% overlap prevents information loss
3. **Multi-stage retrieval**: Coarse filtering followed by fine ranking
4. **Cache embeddings**: Avoid recomputing vectors redundantly
5. **Monitor recall**: Regularly evaluate retrieval quality
6. **Incremental updates**: Support adding, deleting, and modifying documents
