<div align="right">Language: <a href="rag-integration.md">中文</a> | English</div>

# RAG System Integration Guide

Hexagon provides a complete RAG (Retrieval-Augmented Generation) system for building knowledge-intensive AI applications.

## Quick Start

### Basic RAG Flow

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

    // 1. Load documents
    docs, _ := loader.NewDirectoryLoader("./docs").Load(ctx)

    // 2. Split documents
    chunks := splitter.NewRecursiveSplitter(
        splitter.WithChunkSize(1000),
        splitter.WithChunkOverlap(200),
    ).Split(docs)

    // 3. Generate embeddings
    provider := openai.New("your-api-key")
    emb := embedder.NewOpenAIEmbedder(provider, "text-embedding-ada-002")
    emb.Embed(ctx, chunks)

    // 4. Index documents
    store := vector.NewMemoryStore(1536)
    idx := indexer.NewVectorIndexer(store, emb)
    idx.Index(ctx, chunks)

    // 5. Retrieve relevant documents
    ret := retriever.NewVectorRetriever(store, emb)
    results, _ := ret.Retrieve(ctx, "How do I create an Agent?", retriever.WithTopK(3))

    // 6. Generate answer
    syn := synthesizer.NewRefine(provider)
    answer, _ := syn.Synthesize(ctx, "How do I create an Agent?", results)

    println(answer)
}
```

## Document Loaders

### Supported Document Types

#### 1. Text Files

```go
// Single text file
doc, err := loader.NewTextLoader("document.txt").Load(ctx)

// All text files in a directory
docs, err := loader.NewDirectoryLoader("./docs",
    loader.WithPattern("*.txt"),
    loader.WithRecursive(true),
).Load(ctx)
```

#### 2. Markdown

```go
docs, err := loader.NewMarkdownLoader("README.md").Load(ctx)
```

#### 3. URL Content

```go
docs, err := loader.NewURLLoader("https://example.com/article").Load(ctx)
```

### Custom Loader

```go
type CustomLoader struct{}

func (l *CustomLoader) Load(ctx context.Context) ([]rag.Document, error) {
    // Custom loading logic
    return []rag.Document{
        {
            ID:      "doc-1",
            Content: "Document content",
            Metadata: map[string]any{
                "source": "custom",
            },
        },
    }, nil
}
```

## Document Splitting

### Splitting Strategies

#### 1. Character Splitting

Splits by a fixed number of characters — the simplest strategy.

```go
splitter := splitter.NewCharacterSplitter(
    splitter.WithChunkSize(1000),
    splitter.WithChunkOverlap(200),
)
```

#### 2. Recursive Splitting

Intelligent splitting that prefers paragraph and sentence boundaries.

```go
splitter := splitter.NewRecursiveSplitter(
    splitter.WithChunkSize(1000),
    splitter.WithChunkOverlap(200),
    splitter.WithSeparators([]string{"\n\n", "\n", "。", "，"}),
)
```

**Recommended** — produces the best results.

#### 3. Markdown Splitting

Splits along Markdown structure (headings, paragraphs).

```go
splitter := splitter.NewMarkdownSplitter(
    splitter.WithChunkSize(1000),
)
```

#### 4. Semantic Splitting

Splits based on semantic similarity — highest quality but slower.

```go
splitter := splitter.NewSemanticSplitter(
    embedder,
    splitter.WithThreshold(0.75), // similarity threshold
)
```

### Splitting Parameter Tuning

| Parameter | Recommended Value | Description |
|-----------|------------------|-------------|
| ChunkSize | 500–1500 | Adjust based on document characteristics |
| ChunkOverlap | 100–300 | 10–20% of ChunkSize |
| Separators | `["\n\n", "\n", "。"]` | Prefer Chinese period for Chinese text |

## Embedding Generation

### OpenAI Embeddings

```go
embedder := embedder.NewOpenAIEmbedder(
    provider,
    "text-embedding-ada-002", // 1536 dimensions
)

// Or use a smaller model
embedder := embedder.NewOpenAIEmbedder(
    provider,
    "text-embedding-3-small", // 512 dimensions, faster
)
```

### Cached Embedder

Avoid recomputing vectors to improve performance.

```go
cachedEmbedder := embedder.NewCachedEmbedder(
    baseEmbedder,
    embedder.WithCacheSize(1000),
)
```

## Vector Storage

### In-Memory Store

Suitable for development and small-scale data.

```go
store := vector.NewMemoryStore(1536) // vector dimensions
```

### Qdrant

Recommended for production — excellent performance.

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

## Retrieval Strategies

### 1. Vector Retrieval

Retrieval based on semantic similarity.

```go
retriever := retriever.NewVectorRetriever(store, embedder)

results, _ := retriever.Retrieve(ctx, query,
    retriever.WithTopK(5),              // return top 5 results
    retriever.WithMinScore(0.7),        // minimum similarity score 0.7
    retriever.WithFilters(map[string]any{
        "type": "technical",             // metadata filtering
    }),
)
```

### 2. Keyword Retrieval

Keyword matching based on the BM25 algorithm.

```go
retriever := retriever.NewKeywordRetriever(index)

results, _ := retriever.Retrieve(ctx, query,
    retriever.WithTopK(5),
)
```

### 3. Hybrid Retrieval

Combines vector and keyword retrieval.

```go
retriever := retriever.NewHybridRetriever(
    vectorRetriever,
    keywordRetriever,
    retriever.WithAlpha(0.7), // vector weight 0.7, keyword weight 0.3
)
```

### 4. Multi-Query Retrieval

Generates multiple query variants to improve recall.

```go
retriever := retriever.NewMultiQueryRetriever(
    baseRetriever,
    llmProvider,
    retriever.WithNumQueries(3), // generate 3 query variants
)
```

## Reranking

Improve the relevance of retrieved results.

```go
import "github.com/hexagon-codes/hexagon/rag/reranker"

// Create a reranker
reranker := reranker.NewLLMReranker(llmProvider)

// Rerank retrieval results
reranked, _ := reranker.Rerank(ctx, query, results,
    reranker.WithTopN(3), // return top 3
)
```

## Answer Synthesis

### 1. Refine Strategy

Processes documents one by one, progressively refining the answer.

```go
synthesizer := synthesizer.NewRefine(llmProvider,
    synthesizer.WithSystemPrompt("Answer the question based on the provided context"),
)

answer, _ := synthesizer.Synthesize(ctx, query, results)
```

**Use case**: When you need to synthesize information from multiple documents.

### 2. Compact Strategy

Merges all documents into a single prompt.

```go
synthesizer := synthesizer.NewCompact(llmProvider)

answer, _ := synthesizer.Synthesize(ctx, query, results)
```

**Use case**: When the number of documents is small and the context window is large enough.

### 3. Tree Strategy

Tree-based aggregation with hierarchical summarization.

```go
synthesizer := synthesizer.NewTree(llmProvider,
    synthesizer.WithBranchFactor(3), // 3 branches per level
)

answer, _ := synthesizer.Synthesize(ctx, query, results)
```

**Use case**: When a large number of documents need to be processed.

## Full RAG Engine

Use the RAG Engine to simplify the workflow.

```go
engine := rag.NewEngine(
    rag.WithLLM(llmProvider),
    rag.WithEmbedder(embedder),
    rag.WithVectorStore(store),
    rag.WithRetrieverType(rag.RetrieverTypeHybrid),
    rag.WithSynthesizerType(rag.SynthesizerTypeRefine),
)

// Index documents
engine.Index(ctx, docs)

// Query
answer, _ := engine.Query(ctx, "How do I use RAG?")
```

## Performance Optimization

### 1. Batch Processing

```go
// Batch embedding generation
embedder.EmbedBatch(ctx, chunks, embedder.WithBatchSize(100))

// Batch indexing
indexer.IndexBatch(ctx, chunks, indexer.WithConcurrency(4))
```

### 2. Asynchronous Indexing

```go
// Index documents in the background
go func() {
    indexer.Index(ctx, docs)
}()
```

### 3. Incremental Updates

```go
// Only index new documents
indexer.IndexIncremental(ctx, newDocs)
```

### 4. Caching Strategy

```go
// Cache query results
engine := rag.NewEngine(
    rag.WithQueryCache(
        cache.NewLRU(100), // cache 100 query results
    ),
)
```

## Evaluation Metrics

### Retrieval Quality

```go
import "github.com/hexagon-codes/hexagon/evaluate/metrics"

// Relevance evaluation
relevance := metrics.NewRelevanceMetric(llmProvider)
score, _ := relevance.Evaluate(ctx, evaluate.EvalInput{
    Query:    query,
    Context:  results,
    Response: answer,
})

// Faithfulness evaluation (whether the answer is grounded in context)
faithfulness := metrics.NewFaithfulnessMetric(llmProvider)
score, _ := faithfulness.Evaluate(ctx, input)
```

## FAQ

### Q: How do I choose a chunk size?

**A**:
- Short text (news, comments): 300–500
- Medium text (articles, documents): 500–1000
- Long text (books, papers): 1000–1500

### Q: Vector retrieval vs. keyword retrieval?

**A**:
- Vector retrieval: Strong semantic understanding, but weaker on exact matches
- Keyword retrieval: Strong on exact matches, but limited in understanding
- **Recommendation**: Hybrid retrieval — combines the strengths of both

### Q: How do I handle Chinese text?

**A**:
- Use an Embedder that supports Chinese
- Use Chinese punctuation as separators: `["。", "！", "？", "\n\n"]`
- The Markdown splitter has good Chinese support

### Q: How do I improve retrieval accuracy?

**A**:
1. Use semantic splitting instead of fixed-length splitting
2. Set an appropriate Chunk Overlap
3. Use a reranker
4. Use multi-query retrieval to generate query variants
5. Use a hybrid retrieval strategy

## Next Steps

- Learn about [Agent RAG Integration](./agent-development.md#rag-集成)
- Explore [RAG Nodes in Graph Orchestration](./graph-orchestration.md#rag-节点)
- Master [RAG Performance Optimization](./performance-optimization.md#rag-优化)
