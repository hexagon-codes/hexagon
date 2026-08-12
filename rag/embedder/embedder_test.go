package embedder

import (
	"context"
	"errors"
	"testing"
)

// mockEmbeddingProvider 模拟的嵌入提供者
type mockEmbeddingProvider struct {
	embedFn func(ctx context.Context, texts []string) ([][]float32, error)
}

func (m *mockEmbeddingProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if m.embedFn != nil {
		return m.embedFn(ctx, texts)
	}
	// 默认实现：返回固定维度的向量
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = make([]float32, 1536)
		for j := range result[i] {
			result[i][j] = float32(j) / 1536.0
		}
	}
	return result, nil
}

func TestOpenAIEmbedderCreation(t *testing.T) {
	provider := &mockEmbeddingProvider{}
	embedder := NewOpenAIEmbedder(provider)

	if embedder.Dimension() != 1536 {
		t.Errorf("expected dimension 1536, got %d", embedder.Dimension())
	}
}

func TestOpenAIEmbedderWithOptions(t *testing.T) {
	provider := &mockEmbeddingProvider{}
	embedder := NewOpenAIEmbedder(provider,
		WithModel("text-embedding-3-large"),
		WithDimension(3072),
		WithBatchSize(50),
	)

	if embedder.model != "text-embedding-3-large" {
		t.Errorf("expected model 'text-embedding-3-large', got '%s'", embedder.model)
	}

	if embedder.dimension != 3072 {
		t.Errorf("expected dimension 3072, got %d", embedder.dimension)
	}

	if embedder.batchSize != 50 {
		t.Errorf("expected batchSize 50, got %d", embedder.batchSize)
	}
}

func TestOpenAIEmbedderEmbed(t *testing.T) {
	provider := &mockEmbeddingProvider{}
	embedder := NewOpenAIEmbedder(provider)

	texts := []string{"Hello", "World"}
	embeddings, err := embedder.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embeddings) != 2 {
		t.Errorf("expected 2 embeddings, got %d", len(embeddings))
	}

	if len(embeddings[0]) != 1536 {
		t.Errorf("expected embedding dimension 1536, got %d", len(embeddings[0]))
	}
}

func TestOpenAIEmbedderEmbedEmpty(t *testing.T) {
	provider := &mockEmbeddingProvider{}
	embedder := NewOpenAIEmbedder(provider)

	embeddings, err := embedder.Embed(context.Background(), []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if embeddings != nil {
		t.Errorf("expected nil embeddings for empty input, got %v", embeddings)
	}
}

func TestOpenAIEmbedderEmbedOne(t *testing.T) {
	provider := &mockEmbeddingProvider{}
	embedder := NewOpenAIEmbedder(provider)

	embedding, err := embedder.EmbedOne(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embedding) != 1536 {
		t.Errorf("expected embedding dimension 1536, got %d", len(embedding))
	}
}

func TestOpenAIEmbedderBatching(t *testing.T) {
	callCount := 0
	provider := &mockEmbeddingProvider{
		embedFn: func(ctx context.Context, texts []string) ([][]float32, error) {
			callCount++
			result := make([][]float32, len(texts))
			for i := range texts {
				result[i] = make([]float32, 1536)
			}
			return result, nil
		},
	}

	embedder := NewOpenAIEmbedder(provider, WithBatchSize(2))

	// 5 个文本，batch size 2，应该调用 3 次
	texts := []string{"a", "b", "c", "d", "e"}
	_, err := embedder.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 3 {
		t.Errorf("expected 3 batch calls, got %d", callCount)
	}
}

func TestOpenAIEmbedderContextCancellation(t *testing.T) {
	provider := &mockEmbeddingProvider{}
	embedder := NewOpenAIEmbedder(provider, WithBatchSize(1))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	texts := []string{"a", "b", "c"}
	_, err := embedder.Embed(ctx, texts)
	if err == nil {
		t.Error("expected error due to cancelled context")
	}
}

func TestCachedEmbedderCreation(t *testing.T) {
	inner := NewMockEmbedder(128)
	cached := NewCachedEmbedder(inner)

	if cached.Dimension() != 128 {
		t.Errorf("expected dimension 128, got %d", cached.Dimension())
	}
}

func TestCachedEmbedderWithOptions(t *testing.T) {
	inner := NewMockEmbedder(128)
	cached := NewCachedEmbedder(inner, WithMaxCacheSize(100))

	if cached.maxSize != 100 {
		t.Errorf("expected maxSize 100, got %d", cached.maxSize)
	}
}

func TestCachedEmbedderCaching(t *testing.T) {
	callCount := 0
	inner := NewFuncEmbedder(128, func(ctx context.Context, texts []string) ([][]float32, error) {
		callCount++
		result := make([][]float32, len(texts))
		for i := range texts {
			result[i] = make([]float32, 128)
		}
		return result, nil
	})

	cached := NewCachedEmbedder(inner)

	// 第一次调用
	_, err := cached.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}

	// 第二次调用相同文本，应该命中缓存
	_, err = cached.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected 1 call (cached), got %d", callCount)
	}

	// 新文本应该触发新调用
	_, err = cached.Embed(context.Background(), []string{"new text"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
}

func TestCachedEmbedderCacheSize(t *testing.T) {
	inner := NewMockEmbedder(128)
	cached := NewCachedEmbedder(inner)

	_, _ = cached.Embed(context.Background(), []string{"a", "b", "c"})

	if cached.CacheSize() != 3 {
		t.Errorf("expected cache size 3, got %d", cached.CacheSize())
	}
}

func TestCachedEmbedderClearCache(t *testing.T) {
	inner := NewMockEmbedder(128)
	cached := NewCachedEmbedder(inner)

	_, _ = cached.Embed(context.Background(), []string{"a", "b", "c"})
	cached.ClearCache()

	if cached.CacheSize() != 0 {
		t.Errorf("expected cache size 0 after clear, got %d", cached.CacheSize())
	}
}

func TestCachedEmbedderEmbedOne(t *testing.T) {
	inner := NewMockEmbedder(128)
	cached := NewCachedEmbedder(inner)

	embedding, err := cached.EmbedOne(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embedding) != 128 {
		t.Errorf("expected embedding dimension 128, got %d", len(embedding))
	}
}

func TestMockEmbedderCreation(t *testing.T) {
	embedder := NewMockEmbedder(256)

	if embedder.Dimension() != 256 {
		t.Errorf("expected dimension 256, got %d", embedder.Dimension())
	}
}

func TestMockEmbedderEmbed(t *testing.T) {
	embedder := NewMockEmbedder(128)

	texts := []string{"hello", "world"}
	embeddings, err := embedder.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embeddings) != 2 {
		t.Errorf("expected 2 embeddings, got %d", len(embeddings))
	}

	// 不同文本应该产生不同向量
	if embeddings[0][0] == embeddings[1][0] {
		t.Error("expected different embeddings for different texts")
	}
}

func TestMockEmbedderEmbedOne(t *testing.T) {
	embedder := NewMockEmbedder(64)

	embedding, err := embedder.EmbedOne(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embedding) != 64 {
		t.Errorf("expected embedding dimension 64, got %d", len(embedding))
	}
}

func TestFuncEmbedderCreation(t *testing.T) {
	embedder := NewFuncEmbedder(512, func(ctx context.Context, texts []string) ([][]float32, error) {
		return nil, nil
	})

	if embedder.Dimension() != 512 {
		t.Errorf("expected dimension 512, got %d", embedder.Dimension())
	}
}

func TestFuncEmbedderEmbed(t *testing.T) {
	embedder := NewFuncEmbedder(128, func(ctx context.Context, texts []string) ([][]float32, error) {
		result := make([][]float32, len(texts))
		for i := range texts {
			result[i] = make([]float32, 128)
			result[i][0] = float32(i)
		}
		return result, nil
	})

	embeddings, err := embedder.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if embeddings[0][0] != 0 || embeddings[1][0] != 1 {
		t.Error("unexpected embedding values")
	}
}

func TestFuncEmbedderError(t *testing.T) {
	expectedErr := errors.New("embedding failed")
	embedder := NewFuncEmbedder(128, func(ctx context.Context, texts []string) ([][]float32, error) {
		return nil, expectedErr
	})

	_, err := embedder.Embed(context.Background(), []string{"test"})
	if err != expectedErr {
		t.Errorf("expected error '%v', got '%v'", expectedErr, err)
	}
}

func TestFuncEmbedderEmbedOne(t *testing.T) {
	embedder := NewFuncEmbedder(64, func(ctx context.Context, texts []string) ([][]float32, error) {
		result := make([][]float32, len(texts))
		for i := range texts {
			result[i] = make([]float32, 64)
		}
		return result, nil
	})

	embedding, err := embedder.EmbedOne(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embedding) != 64 {
		t.Errorf("expected embedding dimension 64, got %d", len(embedding))
	}
}

func TestFuncEmbedderEmbedOneEmpty(t *testing.T) {
	embedder := NewFuncEmbedder(64, func(ctx context.Context, texts []string) ([][]float32, error) {
		return [][]float32{}, nil // 返回空结果
	})

	_, err := embedder.EmbedOne(context.Background(), "test")
	if err == nil {
		t.Error("expected error for empty result")
	}
}

func TestHashText(t *testing.T) {
	hash1 := hashText("hello")
	hash2 := hashText("hello")
	hash3 := hashText("world")
	const wantHelloSHA256 = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

	if hash1 != hash2 {
		t.Error("same text should produce same hash")
	}

	if hash1 == hash3 {
		t.Error("different text should produce different hash")
	}

	if len(hash1) != 64 { // toolkit SHA-256 生成 64 位小写十六进制字符串。
		t.Errorf("expected hash length 64, got %d", len(hash1))
	}

	if hash1 != wantHelloSHA256 {
		t.Errorf("hashText(hello) = %q, want %q", hash1, wantHelloSHA256)
	}
}

func TestHashTexts(t *testing.T) {
	const want = "15e178b71fae8849ee562c9cc0d7ea322fba6cd495411329d47234479167cc8b"

	if got := hashTexts([]string{"hello", "world"}); got != want {
		t.Errorf("hashTexts() = %q, want %q", got, want)
	}
}
