package retriever

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/rag"
)

// mockSplitter 模拟分割器
type mockSplitter struct {
	chunkSize int
}

func (s *mockSplitter) Name() string { return "mock_splitter" }

func (s *mockSplitter) Split(ctx context.Context, docs []rag.Document) ([]rag.Document, error) {
	var result []rag.Document
	for _, doc := range docs {
		// 简单分割：每 chunkSize 个字符一块
		content := doc.Content
		for i := 0; i < len(content); i += s.chunkSize {
			end := i + s.chunkSize
			if end > len(content) {
				end = len(content)
			}
			chunk := rag.Document{
				Content:  content[i:end],
				Metadata: map[string]any{"source": doc.Source},
			}
			result = append(result, chunk)
		}
	}
	return result, nil
}

// mockEmbedder 模拟嵌入器
type mockEmbedder struct {
	dimension int
}

func (e *mockEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		// 生成简单的模拟向量
		vec := make([]float32, e.dimension)
		for j := 0; j < e.dimension && j < len(texts[i]); j++ {
			vec[j] = float32(texts[i][j]) / 255.0
		}
		result[i] = vec
	}
	return result, nil
}

func (e *mockEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := e.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return embeddings[0], nil
}

func (e *mockEmbedder) Dimension() int {
	return e.dimension
}

func TestNewParentDocRetriever(t *testing.T) {
	store := vector.NewMemoryStore(128)
	embedder := &mockEmbedder{dimension: 128}

	r := NewParentDocRetriever(store, embedder)
	if r == nil {
		t.Fatal("NewParentDocRetriever returned nil")
	}

	if r.childTopK != 10 {
		t.Errorf("expected childTopK=10, got %d", r.childTopK)
	}
	if r.parentTopK != 5 {
		t.Errorf("expected parentTopK=5, got %d", r.parentTopK)
	}
}

func TestNewParentDocRetriever_WithOptions(t *testing.T) {
	store := vector.NewMemoryStore(128)
	embedder := &mockEmbedder{dimension: 128}
	splitter := &mockSplitter{chunkSize: 100}

	r := NewParentDocRetriever(store, embedder,
		WithChildSplitter(splitter),
		WithChildTopK(20),
		WithParentTopK(10),
		WithParentMinScore(0.5),
	)

	if r.childTopK != 20 {
		t.Errorf("expected childTopK=20, got %d", r.childTopK)
	}
	if r.parentTopK != 10 {
		t.Errorf("expected parentTopK=10, got %d", r.parentTopK)
	}
	if r.minScore != 0.5 {
		t.Errorf("expected minScore=0.5, got %f", r.minScore)
	}
}

func TestParentDocRetriever_Index(t *testing.T) {
	store := vector.NewMemoryStore(128)
	embedder := &mockEmbedder{dimension: 128}
	splitter := &mockSplitter{chunkSize: 50}

	r := NewParentDocRetriever(store, embedder, WithChildSplitter(splitter))

	ctx := context.Background()
	docs := []rag.Document{
		{
			ID:      "doc1",
			Content: "This is the first document. It contains some important information about Go programming.",
			Source:  "test.txt",
		},
		{
			ID:      "doc2",
			Content: "This is the second document. It talks about Python and machine learning.",
			Source:  "test2.txt",
		},
	}

	err := r.Index(ctx, docs)
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	// 验证父文档已保存
	count, err := r.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 parent docs, got %d", count)
	}

	// 验证父文档可以获取
	parent, ok := r.GetParentStore().Get("doc1")
	if !ok {
		t.Error("expected to find doc1 in parent store")
	}
	if parent.Content != docs[0].Content {
		t.Error("parent document content mismatch")
	}
}

func TestParentDocRetriever_Retrieve(t *testing.T) {
	store := vector.NewMemoryStore(128)
	embedder := &mockEmbedder{dimension: 128}
	splitter := &mockSplitter{chunkSize: 50}

	r := NewParentDocRetriever(store, embedder,
		WithChildSplitter(splitter),
		WithParentTopK(2),
	)

	ctx := context.Background()
	docs := []rag.Document{
		{
			ID:      "go-doc",
			Content: "Go is a programming language designed at Google. It is statically typed and compiled.",
		},
		{
			ID:      "python-doc",
			Content: "Python is a high-level programming language. It is known for its readability.",
		},
	}

	if err := r.Index(ctx, docs); err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	// 检索
	results, err := r.Retrieve(ctx, "Go programming language")
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	if len(results) == 0 {
		t.Error("expected at least one result")
	}

	// 检查是否返回的是父文档（完整内容）
	for _, doc := range results {
		if doc.Metadata["retrieval_type"] != "parent_doc" {
			t.Errorf("expected retrieval_type=parent_doc, got %v", doc.Metadata["retrieval_type"])
		}
	}
}

func TestParentDocRetriever_Clear(t *testing.T) {
	store := vector.NewMemoryStore(128)
	embedder := &mockEmbedder{dimension: 128}

	r := NewParentDocRetriever(store, embedder)

	ctx := context.Background()
	docs := []rag.Document{
		{ID: "doc1", Content: "Test content"},
	}

	if err := r.Index(ctx, docs); err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	count, _ := r.Count(ctx)
	if count != 1 {
		t.Errorf("expected 1 doc after index, got %d", count)
	}

	if err := r.Clear(ctx); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	count, _ = r.Count(ctx)
	if count != 0 {
		t.Errorf("expected 0 docs after clear, got %d", count)
	}
}

func TestDocumentStore(t *testing.T) {
	store := NewDocumentStore()

	// Save
	doc := rag.Document{ID: "test", Content: "content"}
	store.Save(doc)

	// Get
	got, ok := store.Get("test")
	if !ok {
		t.Error("expected to find document")
	}
	if got.Content != "content" {
		t.Errorf("expected content 'content', got %q", got.Content)
	}

	// Count
	if store.Count() != 1 {
		t.Errorf("expected count 1, got %d", store.Count())
	}

	// Delete
	store.Delete("test")
	_, ok = store.Get("test")
	if ok {
		t.Error("expected document to be deleted")
	}

	// Clear
	store.Save(rag.Document{ID: "a", Content: "a"})
	store.Save(rag.Document{ID: "b", Content: "b"})
	store.Clear()
	if store.Count() != 0 {
		t.Error("expected empty store after clear")
	}
}

func TestGenerateDocID(t *testing.T) {
	id1 := generateDocID("content1")
	id2 := generateDocID("content2")
	id3 := generateDocID("content1")

	if id1 == id2 {
		t.Error("different content should generate different IDs")
	}
	if id1 != id3 {
		t.Error("same content should generate same ID")
	}
	if id1[:4] != "doc_" {
		t.Errorf("ID should start with 'doc_', got %q", id1)
	}
}
