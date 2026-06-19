package bench

import (
	"context"
	"fmt"
	"testing"

	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/hexagon/rag/embedder"
	"github.com/hexagon-codes/hexagon/rag/retriever"
	"github.com/hexagon-codes/hexagon/rag/splitter"
)

// BenchmarkDocumentCreation 测试文档创建性能
func BenchmarkDocumentCreation(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = rag.Document{
			ID:      fmt.Sprintf("doc-%d", i),
			Content: "This is a test document content for benchmarking purposes.",
			Metadata: map[string]any{
				"source": "benchmark",
				"index":  i,
			},
		}
	}
}

// BenchmarkCharacterSplitter 测试字符分割器性能
func BenchmarkCharacterSplitter(b *testing.B) {
	s := splitter.NewCharacterSplitter(
		splitter.WithChunkSize(100),
		splitter.WithChunkOverlap(20),
	)
	docs := []rag.Document{
		{
			ID:      "doc-1",
			Content: generateLongContent(1000),
		},
	}

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = s.Split(ctx, docs)
	}
}

// BenchmarkRecursiveSplitter 测试递归分割器性能
func BenchmarkRecursiveSplitter(b *testing.B) {
	s := splitter.NewRecursiveSplitter(
		splitter.WithRecursiveChunkSize(100),
		splitter.WithRecursiveChunkOverlap(20),
	)
	docs := []rag.Document{
		{
			ID:      "doc-1",
			Content: generateLongContent(1000),
		},
	}

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = s.Split(ctx, docs)
	}
}

// BenchmarkMemoryVectorStore 测试内存向量存储性能
func BenchmarkMemoryVectorStore(b *testing.B) {
	const dimension = 128
	store := vector.NewMemoryStore(dimension)
	ctx := context.Background()

	// 准备测试数据
	docs := make([]vector.Document, 100)
	for i := range docs {
		embedding := make([]float32, dimension)
		for j := range embedding {
			embedding[j] = float32(i*dimension+j) / 10000.0
		}
		docs[i] = vector.Document{
			ID:        fmt.Sprintf("doc-%d", i),
			Content:   fmt.Sprintf("Document content %d", i),
			Embedding: embedding,
		}
	}

	b.Run("Add", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			_ = store.Add(ctx, docs[:10])
		}
	})

	// 预先添加数据用于搜索测试
	_ = store.Add(ctx, docs)

	b.Run("Search", func(b *testing.B) {
		query := make([]float32, dimension)
		for j := range query {
			query[j] = float32(j) / 10000.0
		}

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			_, _ = store.Search(ctx, query, 10)
		}
	})
}

// BenchmarkMockEmbedder 测试 Mock Embedder 性能
func BenchmarkMockEmbedder(b *testing.B) {
	emb := embedder.NewMockEmbedder(128)
	texts := []string{
		"This is a test document",
		"Another test document",
		"Third test document",
	}

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = emb.Embed(ctx, texts)
	}
}

// BenchmarkVectorRetriever 测试向量检索器性能
func BenchmarkVectorRetriever(b *testing.B) {
	const dimension = 128
	store := vector.NewMemoryStore(dimension)
	emb := embedder.NewMockEmbedder(dimension)
	ret := retriever.NewVectorRetriever(store, emb)

	ctx := context.Background()

	// 预先添加数据
	docs := make([]vector.Document, 100)
	contents := make([]string, 100)
	for i := range docs {
		contents[i] = fmt.Sprintf("Document content %d with some additional text", i)
		docs[i] = vector.Document{
			ID:      fmt.Sprintf("doc-%d", i),
			Content: contents[i],
		}
	}

	// 生成 embedding 并添加
	embeddings, _ := emb.Embed(ctx, contents)
	for i := range docs {
		docs[i].Embedding = embeddings[i]
	}
	_ = store.Add(ctx, docs)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = ret.Retrieve(ctx, "test query", rag.WithTopK(10))
	}
}

// BenchmarkKeywordRetriever 测试关键词检索器性能
func BenchmarkKeywordRetriever(b *testing.B) {
	docs := make([]rag.Document, 100)
	for i := range docs {
		docs[i] = rag.Document{
			ID:      fmt.Sprintf("doc-%d", i),
			Content: fmt.Sprintf("Document content %d with keywords like test benchmark performance", i),
		}
	}

	ret := retriever.NewKeywordRetriever(docs)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = ret.Retrieve(ctx, "test benchmark", rag.WithTopK(10))
	}
}

// BenchmarkHybridRetriever 测试混合检索器性能
func BenchmarkHybridRetriever(b *testing.B) {
	const dimension = 128
	store := vector.NewMemoryStore(dimension)
	emb := embedder.NewMockEmbedder(dimension)
	vectorRet := retriever.NewVectorRetriever(store, emb)

	ragDocs := make([]rag.Document, 100)
	vectorDocs := make([]vector.Document, 100)
	contents := make([]string, 100)

	for i := range ragDocs {
		contents[i] = fmt.Sprintf("Document content %d with keywords", i)
		ragDocs[i] = rag.Document{
			ID:      fmt.Sprintf("doc-%d", i),
			Content: contents[i],
		}
		vectorDocs[i] = vector.Document{
			ID:      fmt.Sprintf("doc-%d", i),
			Content: contents[i],
		}
	}

	ctx := context.Background()

	// 生成 embedding 并添加
	embeddings, _ := emb.Embed(ctx, contents)
	for i := range vectorDocs {
		vectorDocs[i].Embedding = embeddings[i]
	}
	_ = store.Add(ctx, vectorDocs)

	keywordRet := retriever.NewKeywordRetriever(ragDocs)
	hybridRet := retriever.NewHybridRetriever(vectorRet, keywordRet,
		retriever.WithVectorWeight(0.7),
		retriever.WithKeywordWeight(0.3),
	)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = hybridRet.Retrieve(ctx, "test content", rag.WithTopK(10))
	}
}

// 辅助函数

func generateLongContent(length int) string {
	content := ""
	words := []string{"The", "quick", "brown", "fox", "jumps", "over", "the", "lazy", "dog."}
	for len(content) < length {
		for _, word := range words {
			if len(content)+len(word)+1 > length {
				break
			}
			if len(content) > 0 {
				content += " "
			}
			content += word
		}
	}
	return content
}
