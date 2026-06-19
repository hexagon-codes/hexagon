package indexer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/rag"
)

// ============== Mock 实现 ==============

// mockVectorStore 模拟向量存储
type mockVectorStore struct {
	docs   []vector.Document
	mu     sync.Mutex
	addErr error
	delErr error
}

func newMockVectorStore() *mockVectorStore {
	return &mockVectorStore{
		docs: make([]vector.Document, 0),
	}
}

func (s *mockVectorStore) Add(ctx context.Context, docs []vector.Document) error {
	if s.addErr != nil {
		return s.addErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs = append(s.docs, docs...)
	return nil
}

func (s *mockVectorStore) Search(ctx context.Context, query []float32, topK int, opts ...vector.SearchOption) ([]vector.Document, error) {
	return nil, nil
}

func (s *mockVectorStore) Get(ctx context.Context, id string) (*vector.Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.docs {
		if s.docs[i].ID == id {
			return &s.docs[i], nil
		}
	}
	return nil, nil
}

func (s *mockVectorStore) Delete(ctx context.Context, ids []string) error {
	if s.delErr != nil {
		return s.delErr
	}
	return nil
}

func (s *mockVectorStore) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs = make([]vector.Document, 0)
	return nil
}

func (s *mockVectorStore) Count(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.docs), nil
}

func (s *mockVectorStore) Close() error {
	return nil
}

// mockEmbedder 模拟向量生成器
type mockEmbedder struct {
	dim      int
	embedErr error
}

func newMockEmbedder(dim int) *mockEmbedder {
	return &mockEmbedder{dim: dim}
}

func (e *mockEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if e.embedErr != nil {
		return nil, e.embedErr
	}
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = make([]float32, e.dim)
		for j := 0; j < e.dim; j++ {
			result[i][j] = float32(i*e.dim + j)
		}
	}
	return result, nil
}

func (e *mockEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	if e.embedErr != nil {
		return nil, e.embedErr
	}
	result := make([]float32, e.dim)
	for j := 0; j < e.dim; j++ {
		result[j] = float32(j)
	}
	return result, nil
}

func (e *mockEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	result := make([]float32, e.dim)
	return result, nil
}

func (e *mockEmbedder) Dimension() int {
	return e.dim
}

// ============== VectorIndexer 测试 ==============

func TestNewVectorIndexer(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)

	idx := NewVectorIndexer(store, embedder)
	if idx == nil {
		t.Fatal("NewVectorIndexer returned nil")
	}

	if idx.batchSize != 100 {
		t.Errorf("expected batchSize=100, got %d", idx.batchSize)
	}
}

func TestNewVectorIndexer_WithOptions(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)

	idx := NewVectorIndexer(store, embedder, WithBatchSize(50))

	if idx.batchSize != 50 {
		t.Errorf("expected batchSize=50, got %d", idx.batchSize)
	}
}

func TestVectorIndexer_Index(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewVectorIndexer(store, embedder, WithBatchSize(10))

	ctx := context.Background()
	docs := []rag.Document{
		{ID: "doc1", Content: "First document"},
		{ID: "doc2", Content: "Second document"},
		{ID: "doc3", Content: "Third document"},
	}

	err := idx.Index(ctx, docs)
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	count, _ := store.Count(ctx)
	if count != 3 {
		t.Errorf("expected 3 documents in store, got %d", count)
	}
}

func TestVectorIndexer_Index_Empty(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewVectorIndexer(store, embedder)

	ctx := context.Background()
	err := idx.Index(ctx, []rag.Document{})
	if err != nil {
		t.Fatalf("Index empty should not fail: %v", err)
	}
}

func TestVectorIndexer_Index_Batching(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewVectorIndexer(store, embedder, WithBatchSize(5))

	ctx := context.Background()

	// 创建 12 个文档（需要 3 批）
	docs := make([]rag.Document, 12)
	for i := range docs {
		docs[i] = rag.Document{ID: string(rune('a' + i)), Content: "Content"}
	}

	err := idx.Index(ctx, docs)
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	count, _ := store.Count(ctx)
	if count != 12 {
		t.Errorf("expected 12 documents, got %d", count)
	}
}

func TestVectorIndexer_Index_ContextCancelled(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewVectorIndexer(store, embedder, WithBatchSize(5))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	docs := []rag.Document{{ID: "1", Content: "test"}}
	err := idx.Index(ctx, docs)
	if err == nil {
		t.Error("expected context cancelled error")
	}
}

func TestVectorIndexer_Delete(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewVectorIndexer(store, embedder)

	ctx := context.Background()
	err := idx.Delete(ctx, []string{"doc1", "doc2"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestVectorIndexer_Clear(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewVectorIndexer(store, embedder)

	ctx := context.Background()

	// 先添加一些文档
	idx.Index(ctx, []rag.Document{{ID: "1", Content: "test"}})

	// 清空
	err := idx.Clear(ctx)
	if err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	count, _ := idx.Count(ctx)
	if count != 0 {
		t.Errorf("expected 0 documents after clear, got %d", count)
	}
}

func TestVectorIndexer_Count(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewVectorIndexer(store, embedder)

	ctx := context.Background()
	idx.Index(ctx, []rag.Document{
		{ID: "1", Content: "a"},
		{ID: "2", Content: "b"},
	})

	count, err := idx.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected count=2, got %d", count)
	}
}

// ============== ConcurrentIndexer 测试 ==============

func TestNewConcurrentIndexer(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)

	idx := NewConcurrentIndexer(store, embedder)
	if idx == nil {
		t.Fatal("NewConcurrentIndexer returned nil")
	}

	if idx.batchSize != 100 {
		t.Errorf("expected batchSize=100, got %d", idx.batchSize)
	}
	if idx.concurrency != 4 {
		t.Errorf("expected concurrency=4, got %d", idx.concurrency)
	}
}

func TestNewConcurrentIndexer_WithOptions(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)

	idx := NewConcurrentIndexer(store, embedder,
		WithConcurrentBatchSize(25),
		WithConcurrency(8),
	)

	if idx.batchSize != 25 {
		t.Errorf("expected batchSize=25, got %d", idx.batchSize)
	}
	if idx.concurrency != 8 {
		t.Errorf("expected concurrency=8, got %d", idx.concurrency)
	}
}

func TestConcurrentIndexer_Index(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewConcurrentIndexer(store, embedder,
		WithConcurrentBatchSize(5),
		WithConcurrency(2),
	)

	ctx := context.Background()

	// 创建 20 个文档
	docs := make([]rag.Document, 20)
	for i := range docs {
		docs[i] = rag.Document{ID: string(rune('a' + i)), Content: "Content"}
	}

	err := idx.Index(ctx, docs)
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	count, _ := store.Count(ctx)
	if count != 20 {
		t.Errorf("expected 20 documents, got %d", count)
	}
}

func TestConcurrentIndexer_Index_Empty(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewConcurrentIndexer(store, embedder)

	ctx := context.Background()
	err := idx.Index(ctx, []rag.Document{})
	if err != nil {
		t.Fatalf("Index empty should not fail: %v", err)
	}
}

func TestConcurrentIndexer_Delete(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewConcurrentIndexer(store, embedder)

	ctx := context.Background()
	err := idx.Delete(ctx, []string{"doc1"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestConcurrentIndexer_Clear(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewConcurrentIndexer(store, embedder)

	ctx := context.Background()
	err := idx.Clear(ctx)
	if err != nil {
		t.Fatalf("Clear failed: %v", err)
	}
}

func TestConcurrentIndexer_Count(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewConcurrentIndexer(store, embedder)

	ctx := context.Background()
	count, err := idx.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count=0, got %d", count)
	}
}

// ============== IncrementalIndexer 测试 ==============

func TestNewIncrementalIndexer(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)

	idx := NewIncrementalIndexer(store, embedder)
	if idx == nil {
		t.Fatal("NewIncrementalIndexer returned nil")
	}

	if idx.batchSize != 100 {
		t.Errorf("expected batchSize=100, got %d", idx.batchSize)
	}
	if idx.checksums == nil {
		t.Error("checksums map should be initialized")
	}
}

func TestNewIncrementalIndexer_WithOptions(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)

	idx := NewIncrementalIndexer(store, embedder, WithIncrementalBatchSize(50))

	if idx.batchSize != 50 {
		t.Errorf("expected batchSize=50, got %d", idx.batchSize)
	}
}

func TestIncrementalIndexer_Index(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewIncrementalIndexer(store, embedder)

	ctx := context.Background()
	docs := []rag.Document{
		{ID: "doc1", Content: "First document"},
		{ID: "doc2", Content: "Second document"},
	}

	// 首次索引
	err := idx.Index(ctx, docs)
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	count, _ := store.Count(ctx)
	if count != 2 {
		t.Errorf("expected 2 documents, got %d", count)
	}
}

func TestIncrementalIndexer_Index_NoChange(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewIncrementalIndexer(store, embedder)

	ctx := context.Background()
	docs := []rag.Document{
		{ID: "doc1", Content: "First document"},
	}

	// 首次索引
	idx.Index(ctx, docs)
	firstCount, _ := store.Count(ctx)

	// 再次索引相同的文档
	idx.Index(ctx, docs)
	secondCount, _ := store.Count(ctx)

	if secondCount != firstCount {
		t.Errorf("expected no change, got %d -> %d", firstCount, secondCount)
	}
}

func TestIncrementalIndexer_Index_Update(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewIncrementalIndexer(store, embedder)

	ctx := context.Background()

	// 首次索引
	docs := []rag.Document{{ID: "doc1", Content: "Original content"}}
	idx.Index(ctx, docs)
	firstCount, _ := store.Count(ctx)

	// 更新内容后索引
	docs = []rag.Document{{ID: "doc1", Content: "Updated content"}}
	idx.Index(ctx, docs)
	secondCount, _ := store.Count(ctx)

	// 应该有新的文档被添加（因为内容变化了）
	if secondCount <= firstCount {
		t.Errorf("expected new document added for updated content")
	}
}

func TestIncrementalIndexer_Delete(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewIncrementalIndexer(store, embedder)

	ctx := context.Background()

	// 索引文档
	docs := []rag.Document{{ID: "doc1", Content: "Test"}}
	idx.Index(ctx, docs)

	// 删除
	err := idx.Delete(ctx, []string{"doc1"})
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// 检查 checksums 是否被删除
	if _, ok := idx.checksums["doc1"]; ok {
		t.Error("checksum should be removed after delete")
	}
}

func TestIncrementalIndexer_Clear(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewIncrementalIndexer(store, embedder)

	ctx := context.Background()

	// 索引文档
	docs := []rag.Document{
		{ID: "doc1", Content: "Test1"},
		{ID: "doc2", Content: "Test2"},
	}
	idx.Index(ctx, docs)

	// 清空
	err := idx.Clear(ctx)
	if err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	count, _ := idx.Count(ctx)
	if count != 0 {
		t.Errorf("expected 0 documents after clear, got %d", count)
	}

	if len(idx.checksums) != 0 {
		t.Errorf("expected 0 checksums after clear, got %d", len(idx.checksums))
	}
}

func TestIncrementalIndexer_Count(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewIncrementalIndexer(store, embedder)

	ctx := context.Background()
	count, err := idx.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count=0, got %d", count)
	}
}

// ============== 辅助函数测试 ==============

func TestComputeChecksum(t *testing.T) {
	tests := []struct {
		content  string
		expected string
	}{
		{"", "empty"},
		{"a", "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"},
		{"hello", "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
		{"world!", "711e9609339e92b03ddc0a211827dba421f38f9ed8b9d806e1ffdd8c15ffa03d"},
	}

	for _, tt := range tests {
		result := computeChecksum(tt.content)
		if result != tt.expected {
			t.Errorf("computeChecksum(%q) = %q, want %q", tt.content, result, tt.expected)
		}
	}
}

func TestComputeChecksum_DifferentContent(t *testing.T) {
	cs1 := computeChecksum("hello")
	cs2 := computeChecksum("world")

	if cs1 == cs2 {
		t.Error("different content should have different checksums")
	}
}

// ============== 接口实现测试 ==============

func TestInterfaceImplementation(t *testing.T) {
	var _ rag.Indexer = (*VectorIndexer)(nil)
	var _ rag.Indexer = (*ConcurrentIndexer)(nil)
	var _ rag.Indexer = (*IncrementalIndexer)(nil)
}

// ============== 并发安全测试 ==============

func TestIncrementalIndexer_Concurrent(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewIncrementalIndexer(store, embedder)

	ctx := context.Background()
	var wg sync.WaitGroup

	// 并发索引
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			docs := []rag.Document{
				{ID: string(rune('a' + n)), Content: "Content"},
			}
			idx.Index(ctx, docs)
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// 成功完成
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent indexing timed out")
	}
}

// ============== 文档 ID 生成测试 ==============

func TestVectorIndexer_GeneratesIDIfEmpty(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(128)
	idx := NewVectorIndexer(store, embedder)

	ctx := context.Background()
	docs := []rag.Document{
		{Content: "No ID provided"}, // ID 为空
	}

	err := idx.Index(ctx, docs)
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	// 检查存储中的文档是否有 ID
	if len(store.docs) == 0 {
		t.Fatal("expected at least one document in store")
	}

	if store.docs[0].ID == "" {
		t.Error("document should have a generated ID")
	}
}
