<div align="right">语言: 中文 | <a href="rag-integration.en.md">English</a></div>

# RAG 端到端集成

本文按 Hexagon 当前源码及其 `ai-core` 向量存储依赖编写，覆盖一条可运行的 RAG 主链路。更完整的组件说明见 [RAG 指南](rag-guide.md)。

## 真实边界

```text
Loader -> Splitter -> Indexer -> Embedder -> ai-core vector.Store
                                      ^                 |
                                      |                 v
                                  Retriever <-----------+
                                      |
                                      v
                                 Synthesizer -> Response
```

- `Loader` 和 `Splitter` 处理 `rag.Document`。
- `Indexer` 调用 Embedder 生成向量，再写入 `github.com/hexagon-codes/ai-core/store/vector.Store`。
- `Retriever` 使用同一个 Embedder 和 Store 检索 `[]rag.Document`。
- `Synthesizer` 独立调用 `ai-core/llm.Provider` 生成最终回答。
- `rag.Engine` 只封装加载、分块、索引和检索。`Engine.Query` 返回格式化的检索上下文，不调用 LLM，也不替代 Synthesizer。

## 可编译的完整示例

运行前设置 `OPENAI_API_KEY`，并确保 `./docs` 下存在 Markdown 文件。`VectorIndexer.Index` 内部完成批量 Embed，无需先手动调用 `Embed`。

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
	const query = "Hexagon 的 RAG Engine 负责什么？"
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

## 组件 API

除上面的完整程序外，下文 Go 代码均为集成片段，并假定其中提到的 `ctx`、Provider、文档或组件变量已在调用方定义；所列函数和选项名均来自当前源码。

### Loader 和 Splitter

常用 Loader 均实现 `rag.Loader`，入口都是 `Load(ctx)`：

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

`NewReaderLoader(reader, source)` 和 `NewStringLoader(content, source)` 可用于内存输入。`URLLoader` 返回响应正文，不负责把 HTML 清洗成纯文本。

Splitter 实现 `Split(ctx, docs)`；不同类型使用不同的选项命名空间：

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

### Embedder 和 ai-core vector Store

`NewOpenAIEmbedder` 接受 Provider 和函数选项。`Embed` 的输入是 `[]string`，`EmbedOne` 处理单段文本：

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

生产链路使用 `ai-core/store/vector.Store`。它提供 `Add`、`Search`、`Get`、`Delete`、`Clear`、`Count` 和 `Close`；搜索选项为 `vector.WithFilter`、`vector.WithMinScore`、`vector.WithEmbedding` 和 `vector.WithMetadata`。

Embedder 声明的维度、实际返回向量的长度和 Store 的集合维度必须一致。更换 embedding 模型或维度时，应使用新集合重建索引。

### Indexer 和 Retriever

`VectorIndexer` 负责 Embed 和写入，公开入口是 `Index(ctx, docs)`：

```go
idx := indexer.NewVectorIndexer(store, emb, indexer.WithBatchSize(100))
if err := idx.Index(ctx, chunks); err != nil {
	return err
}
```

Retriever 的构造选项与每次检索的选项来自不同包：

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

关键词和混合检索的真实构造方式如下。`HybridRetriever` 已并行执行两个底层 Retriever：

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

### Synthesizer 和 Engine

现有合成器构造器是：

- `synthesizer.NewRefineSynthesizer`
- `synthesizer.NewCompactSynthesizer`
- `synthesizer.NewTreeSummarizeSynthesizer`
- `synthesizer.NewSimpleSummarizeSynthesizer`

它们分别通过 `WithRefineSynthesizerLLM`、`WithCompactSynthesizerLLM`、`WithTreeSynthesizerLLM`、`WithSimpleSynthesizerLLM` 注入 `ai-core/llm.Provider`。每次调用使用 `Synthesize(ctx, query, docs, opts...)`，返回 `*synthesizer.Response`。

下面是 Engine 片段；假定 `store`、`emb`、`source`、`chunker`、`syn`、`ctx` 和 `query` 已定义：

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

若只需要拼接后的检索上下文，可调用 `engine.Query(ctx, query, ...)`。它的返回类型是 `(string, error)`，不是最终 LLM 回答。

## 切换到 Qdrant

### 新集合

把内存 Store 替换为下面的 Qdrant Store；其余 Indexer、Retriever 和 Engine 代码不变。此片段假定 `emb` 已定义：

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

新集合应使用默认的 `qdrant.PointIDUUIDv8`。Qdrant 会拒绝空 ID、错误维度、非有限向量，以及 metadata 中的保留键 `content`、`created_at`、`_original_id`。

### 从旧 Point ID 策略迁移

`qdrant.PointIDLegacyHash31` 仅用于读取和迁移旧集合。不要在同一集合上直接切换策略：旧 numeric point 与新 UUID point 可能并存为重复数据，而且切换后按原 ID 的 `Get`/`Delete` 映射也会变化。

迁移步骤：

1. 以 `PointIDLegacyHash31` 打开旧集合。
2. 创建一个不同名称、相同维度和距离的新集合；省略 `PointIDStrategy`，使用安全默认值。
3. 用 `Scroll` 读取旧集合，并用 `AddBatch` 写入新集合。
4. 比较 `Count`，抽样检索和按 ID 读取，再切换应用配置；旧集合保留到回滚窗口结束。

核心迁移片段如下，假定 `ctx`、`legacy` 和 `target` 均已初始化：

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

旧 Hash31 冲突已经覆盖的数据无法从旧集合恢复；此时必须从原始文档重新构建新集合。

## 缓存、并发和增量索引

只缓存 embedding 时，使用现有的 LRU 包装器：

```go
cached := embedder.NewCachedEmbedder(
	emb,
	embedder.WithMaxCacheSize(10_000),
)
```

并发索引使用 `ConcurrentIndexer`，不要再在 `Index` 外包一层无法收集错误的裸 goroutine：

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

需要同一进程内按内容校验和跳过未变化文档时，可使用：

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

`IncrementalIndexer` 的校验和状态在内存中，不跨进程持久化。当前 Engine 没有查询结果缓存选项。

## Evaluate

下面的评估片段使用现有 `evaluate/rag.NewEvaluator`。传入 `nil` 时使用包内规则评估；如需 LLM 评审，传入实现 `evaluate/rag.LLMProvider` 的对象：

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

另一套通用评估框架可使用 `metrics.NewRelevanceEvaluator` 和 `metrics.NewFaithfulnessEvaluator`，二者接收 `evaluate.LLMJudge`。

## 上线检查

- 所有调用都透传可取消、可超时的 `context.Context`，并检查错误。
- 摄取和查询必须复用同一 embedding 模型及维度；变更后新建集合并重建索引。
- 文档 ID 必须稳定且非空，metadata 不使用 Qdrant 保留键。
- 在切流前比较旧、新集合数量，并对固定查询集做检索和回答回归。
- 退出时调用 `Store.Close`；只有 Qdrant 迁移/直接写入场景才使用 Qdrant 自身的 `AddBatch`。

## 相关文档

- [RAG 指南](rag-guide.md)
