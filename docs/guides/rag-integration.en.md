<div align="right">Language: <a href="rag-integration.md">中文</a> | English</div>

# End-to-End RAG Integration

This guide follows the current Hexagon source and its `ai-core` vector-store dependency. It covers one runnable RAG path. See the [RAG guide](rag-guide.en.md) for a broader component reference.

## Actual boundaries

```text
Loader -> Splitter -> Indexer -> Embedder -> ai-core vector.Store
                                      ^                 |
                                      |                 v
                                  Retriever <-----------+
                                      |
                                      v
                                 Synthesizer -> Response
```

- `Loader` and `Splitter` process `rag.Document` values.
- `Indexer` calls the Embedder and writes vectors to `github.com/hexagon-codes/ai-core/store/vector.Store`.
- `Retriever` uses the same Embedder and Store and returns `[]rag.Document`.
- `Synthesizer` separately calls an `ai-core/llm.Provider` to produce the final answer.
- `rag.Engine` wraps loading, splitting, indexing, and retrieval only. `Engine.Query` returns formatted retrieval context; it neither calls an LLM nor replaces a Synthesizer.

## Complete compilable example

Set `OPENAI_API_KEY` and ensure that `./docs` contains Markdown files before running this program. `VectorIndexer.Index` performs batched embedding internally; do not embed the chunks first.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/hexagon-codes/ai-core/llm/openai"
	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/hexagon/rag/embedder"
	"github.com/hexagon-codes/hexagon/rag/indexer"
	"github.com/hexagon-codes/hexagon/rag/loader"
	"github.com/hexagon-codes/hexagon/rag/retriever"
	"github.com/hexagon-codes/hexagon/rag/splitter"
	"github.com/hexagon-codes/hexagon/rag/synthesizer"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	if os.Getenv("OPENAI_API_KEY") == "" {
		return fmt.Errorf("OPENAI_API_KEY is required")
	}

	provider := openai.New(
		os.Getenv("OPENAI_API_KEY"),
		openai.WithModel("gpt-4o"),
	)
	emb := embedder.NewOpenAIEmbedder(
		provider,
		embedder.WithModel("text-embedding-3-small"),
		embedder.WithDimension(1536),
		embedder.WithBatchSize(100),
	)

	store := vector.NewMemoryStore(emb.Dimension())
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("close vector store: %v", err)
		}
	}()

	source := loader.NewDirectoryLoader(
		"./docs",
		loader.WithPattern("*.md"),
		loader.WithRecursive(true),
	)
	docs, err := source.Load(ctx)
	if err != nil {
		return err
	}

	chunker := splitter.NewRecursiveSplitter(
		splitter.WithRecursiveChunkSize(1000),
		splitter.WithRecursiveChunkOverlap(200),
		splitter.WithSeparators([]string{"\n\n", "\n", "。", ".", " ", ""}),
	)
	chunks, err := chunker.Split(ctx, docs)
	if err != nil {
		return err
	}

	idx := indexer.NewVectorIndexer(
		store,
		emb,
		indexer.WithBatchSize(100),
	)
	if err := idx.Index(ctx, chunks); err != nil {
		return err
	}

	ret := retriever.NewVectorRetriever(
		store,
		emb,
		retriever.WithTopK(5),
		retriever.WithMinScore(0.2),
	)
	const query = "What does Hexagon's RAG Engine do?"
	hits, err := ret.Retrieve(
		ctx,
		query,
		rag.WithTopK(3),
		rag.WithMinScore(0.2),
	)
	if err != nil {
		return err
	}

	syn := synthesizer.NewCompactSynthesizer(
		synthesizer.WithCompactSynthesizerLLM(provider),
		synthesizer.WithCompactSynthesizerMaxContext(8000),
	)
	response, err := syn.Synthesize(
		ctx,
		query,
		hits,
		synthesizer.WithSourceDocuments(true),
	)
	if err != nil {
		return err
	}

	fmt.Println(response.Content)
	return nil
}
```

## Component APIs

Except for the complete program above, the Go blocks below are integration fragments. They assume that the referenced `ctx`, Provider, documents, or component variables are defined by the caller; every listed function and option name comes from the current source.

### Loader and Splitter

The common Loaders implement `rag.Loader`; every entry point is `Load(ctx)`:

```go
textSource := loader.NewTextLoader("document.txt")
markdownSource := loader.NewMarkdownLoader(
	"README.md",
	loader.WithRemoveImages(false),
	loader.WithRemoveLinks(false),
	loader.WithExtractMetadata(true),
)
directorySource := loader.NewDirectoryLoader(
	"./docs",
	loader.WithPattern("*.md"),
	loader.WithRecursive(true),
)
urlSource := loader.NewURLLoader("https://example.com/article")

docs, err := directorySource.Load(ctx)
```

Use `NewReaderLoader(reader, source)` and `NewStringLoader(content, source)` for in-memory input. `URLLoader` returns the response body; it does not clean HTML into plain text.

Splitters implement `Split(ctx, docs)`. Each splitter type has its own option namespace:

```go
character := splitter.NewCharacterSplitter(
	splitter.WithChunkSize(1000),
	splitter.WithChunkOverlap(200),
	splitter.WithSeparator("\n\n"),
)
recursive := splitter.NewRecursiveSplitter(
	splitter.WithRecursiveChunkSize(1000),
	splitter.WithRecursiveChunkOverlap(200),
)
markdown := splitter.NewMarkdownSplitter(
	splitter.WithMarkdownChunkSize(1000),
	splitter.WithMarkdownChunkOverlap(200),
	splitter.WithCodeBlockAware(true),
)

chunks, err := recursive.Split(ctx, docs)
```

### Embedder and the ai-core vector Store

`NewOpenAIEmbedder` takes a Provider and functional options. `Embed` accepts `[]string`; `EmbedOne` handles one text value:

```go
emb := embedder.NewOpenAIEmbedder(
	provider,
	embedder.WithModel("text-embedding-3-small"),
	embedder.WithDimension(1536),
	embedder.WithBatchSize(100),
)

vectors, err := emb.Embed(ctx, []string{"first chunk", "second chunk"})
queryVector, err := emb.EmbedOne(ctx, "search query")
```

Production paths use `ai-core/store/vector.Store`. It exposes `Add`, `Search`, `Get`, `Delete`, `Clear`, `Count`, and `Close`; search options are `vector.WithFilter`, `vector.WithMinScore`, `vector.WithEmbedding`, and `vector.WithMetadata`.

The Embedder's declared dimension, actual vector length, and Store collection dimension must match. Create a new collection and rebuild the index when changing the embedding model or dimension.

### Indexer and Retriever

`VectorIndexer` embeds and writes documents through `Index(ctx, docs)`:

```go
idx := indexer.NewVectorIndexer(store, emb, indexer.WithBatchSize(100))
if err := idx.Index(ctx, chunks); err != nil {
	return err
}
```

Retriever constructor options and per-call options come from different packages:

```go
ret := retriever.NewVectorRetriever(
	store,
	emb,
	retriever.WithTopK(5),
	retriever.WithMinScore(0.2),
)

hits, err := ret.Retrieve(
	ctx,
	query,
	rag.WithTopK(3),
	rag.WithMinScore(0.3),
	rag.WithFilter(map[string]any{"loader": "markdown"}),
)
```

These are the actual keyword and hybrid retrieval constructors. `HybridRetriever` already runs its two underlying Retrievers concurrently:

```go
keyword := retriever.NewKeywordRetriever(chunks, retriever.WithKeywordTopK(10))
hybrid := retriever.NewHybridRetriever(
	ret,
	keyword,
	retriever.WithVectorWeight(0.7),
	retriever.WithKeywordWeight(0.3),
	retriever.WithHybridTopK(5),
)

hits, err := hybrid.Retrieve(ctx, query, rag.WithTopK(5))
```

### Synthesizer and Engine

The available Synthesizer constructors are:

- `synthesizer.NewRefineSynthesizer`
- `synthesizer.NewCompactSynthesizer`
- `synthesizer.NewTreeSummarizeSynthesizer`
- `synthesizer.NewSimpleSummarizeSynthesizer`

Inject an `ai-core/llm.Provider` with `WithRefineSynthesizerLLM`, `WithCompactSynthesizerLLM`, `WithTreeSynthesizerLLM`, or `WithSimpleSynthesizerLLM`, respectively. Each call uses `Synthesize(ctx, query, docs, opts...)` and returns `*synthesizer.Response`.

The following is a fragment; it assumes `store`, `emb`, `source`, `chunker`, `syn`, `ctx`, and `query` have already been defined:

```go
engine := rag.NewEngine(
	rag.WithStore(store),
	rag.WithEngineEmbedder(emb),
	rag.WithLoader(source),
	rag.WithEngineSplitter(chunker),
	rag.WithEngineTopK(5),
	rag.WithEngineMinScore(0.2),
)

if err := engine.Ingest(ctx); err != nil {
	return err
}
docs, err := engine.Retrieve(ctx, query, rag.WithTopK(3))
if err != nil {
	return err
}
answer, err := syn.Synthesize(ctx, query, docs)
```

Call `engine.Query(ctx, query, ...)` when you only need concatenated retrieval context. Its return type is `(string, error)`, not a final LLM answer.

## Moving to Qdrant

### New collection

Replace the in-memory Store with this Qdrant Store. The remaining Indexer, Retriever, and Engine code stays unchanged. This fragment assumes `emb` is defined:

```go
store, err := qdrant.New(qdrant.Config{
	Host:             "localhost",
	Port:             6333,
	Collection:       "documents_v2",
	Dimension:        emb.Dimension(),
	Distance:         qdrant.DistanceCosine,
	CreateCollection: true,
})
if err != nil {
	return err
}
defer store.Close()
```

Use the default `qdrant.PointIDUUIDv8` for new collections. Qdrant rejects empty IDs, incorrect dimensions, non-finite vectors, and the reserved metadata keys `content`, `created_at`, and `_original_id`.

### Migrating the old point-ID strategy

`qdrant.PointIDLegacyHash31` exists only to read and migrate legacy collections. Do not switch strategies on the same collection: old numeric points and new UUID points can coexist as duplicates, and `Get`/`Delete` mapping for original IDs changes after the switch.

Migration sequence:

1. Open the old collection with `PointIDLegacyHash31`.
2. Create a differently named collection with the same dimension and distance. Omit `PointIDStrategy` to use the safe default.
3. Read the old collection with `Scroll` and write the new collection with `AddBatch`.
4. Compare `Count`, sample retrieval and by-ID reads, then switch application configuration. Keep the old collection through the rollback window.

This core migration fragment assumes that `ctx`, `legacy`, and `target` are initialized:

```go
err := legacy.Scroll(ctx, 100, func(docs []vector.Document) error {
	return target.AddBatch(
		ctx,
		docs,
		qdrant.WithBatchSize(100),
		qdrant.WithConcurrency(4),
		qdrant.WithRetry(3, time.Second),
	)
})
```

Data already overwritten by a legacy Hash31 collision cannot be recovered from that collection; rebuild the new collection from source documents in that case.

## Caching, concurrency, and incremental indexing

Use the existing LRU wrapper when only embedding results need caching:

```go
cached := embedder.NewCachedEmbedder(
	emb,
	embedder.WithMaxCacheSize(10_000),
)
```

Use `ConcurrentIndexer` for concurrent indexing. Do not wrap `Index` in a bare goroutine whose error cannot be collected:

```go
idx := indexer.NewConcurrentIndexer(
	store,
	cached,
	indexer.WithConcurrentBatchSize(100),
	indexer.WithConcurrency(4),
)
if err := idx.Index(ctx, chunks); err != nil {
	return err
}
```

To skip unchanged documents by content checksum within one process, use:

```go
idx := indexer.NewIncrementalIndexer(
	store,
	emb,
	indexer.WithIncrementalBatchSize(100),
)
if err := idx.Index(ctx, changedDocs); err != nil {
	return err
}
```

`IncrementalIndexer` keeps checksum state in memory; it is not persisted across processes. The current Engine has no query-result cache option.

## Evaluate

This fragment uses the existing `evaluate/rag.NewEvaluator`. Passing `nil` selects its built-in rule-based evaluation; pass an implementation of `evaluate/rag.LLMProvider` for LLM judging:

```go
contexts := make([]string, 0, len(hits))
for _, doc := range hits {
	contexts = append(contexts, doc.Content)
}

report, err := rageval.NewEvaluator(nil).Evaluate(ctx, &rageval.EvaluationInput{
	Question: query,
	Answer:   answer.Content,
	Contexts: contexts,
})
if err != nil {
	return err
}
fmt.Printf("faithfulness=%.2f relevancy=%.2f\n", report.Faithfulness, report.Relevancy)
```

The general evaluation framework also provides `metrics.NewRelevanceEvaluator` and `metrics.NewFaithfulnessEvaluator`; both accept `evaluate.LLMJudge`.

## Production checklist

- Pass a cancellable, bounded `context.Context` through every call and handle every error.
- Use the same embedding model and dimension for ingestion and querying; create a new collection and rebuild after a change.
- Keep document IDs stable and non-empty, and do not use Qdrant's reserved metadata keys.
- Before cutover, compare old and new collection counts and run retrieval and answer regression on a fixed query set.
- Call `Store.Close` on shutdown. Use Qdrant's own `AddBatch` only for migrations or direct vector writes.

## Related documentation

- [RAG guide](rag-guide.en.md)
