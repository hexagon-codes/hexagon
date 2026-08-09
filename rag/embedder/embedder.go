// Package embedder 提供 RAG 系统的文本嵌入生成器
//
// Embedder 用于将文本转换为向量：
//   - OpenAIEmbedder: 使用 OpenAI Embedding API
//   - CachedEmbedder: 带缓存的 Embedder 包装器（带 LRU 淘汰和防击穿）
//   - BatchEmbedder: 批量处理的 Embedder 包装器
package embedder

import (
	"container/list"
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/toolkit/util/hash"
	"golang.org/x/sync/singleflight"
)

// ============== OpenAIEmbedder ==============

// EmbeddingProvider 嵌入提供者接口
// 通常由 ai-core 的 llm.Provider 实现
type EmbeddingProvider interface {
	// Embed 将文本转换为向量
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// ModelEmbeddingProvider 支持指定模型的嵌入提供者（可选接口）
type ModelEmbeddingProvider interface {
	EmbeddingProvider
	EmbedWithModel(ctx context.Context, model string, texts []string) ([][]float32, error)
}

// OpenAIEmbedder OpenAI Embedding 生成器
type OpenAIEmbedder struct {
	provider  EmbeddingProvider
	model     string
	dimension int
	batchSize int
}

// OpenAIOption OpenAIEmbedder 选项
type OpenAIOption func(*OpenAIEmbedder)

// WithModel 设置模型
func WithModel(model string) OpenAIOption {
	return func(e *OpenAIEmbedder) {
		e.model = model
	}
}

// WithDimension 设置向量维度
func WithDimension(dim int) OpenAIOption {
	return func(e *OpenAIEmbedder) {
		e.dimension = dim
	}
}

// WithBatchSize 设置批量大小
func WithBatchSize(size int) OpenAIOption {
	return func(e *OpenAIEmbedder) {
		e.batchSize = size
	}
}

// NewOpenAIEmbedder 创建 OpenAI Embedder
func NewOpenAIEmbedder(provider EmbeddingProvider, opts ...OpenAIOption) *OpenAIEmbedder {
	e := &OpenAIEmbedder{
		provider:  provider,
		model:     "text-embedding-3-small",
		dimension: 1536,
		batchSize: 100,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Embed 将文本列表转换为向量
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// 分批处理
	var allEmbeddings [][]float32

	for i := 0; i < len(texts); i += e.batchSize {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		end := i + e.batchSize
		if end > len(texts) {
			end = len(texts)
		}

		batch := texts[i:end]
		// 优先使用指定模型（支持 Ollama nomic-embed-text 等非默认模型）
		var embeddings [][]float32
		var err error
		if mp, ok := e.provider.(ModelEmbeddingProvider); ok {
			embeddings, err = mp.EmbedWithModel(ctx, e.model, batch)
		} else {
			embeddings, err = e.provider.Embed(ctx, batch)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to embed batch %d-%d: %w", i, end, err)
		}
		if len(embeddings) != len(batch) {
			return nil, fmt.Errorf("embedding count mismatch: got %d, expected %d (batch %d-%d)", len(embeddings), len(batch), i, end)
		}

		allEmbeddings = append(allEmbeddings, embeddings...)
	}

	return allEmbeddings, nil
}

// EmbedOne 将单个文本转换为向量
func (e *OpenAIEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := e.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return embeddings[0], nil
}

// Dimension 返回向量维度
func (e *OpenAIEmbedder) Dimension() int {
	return e.dimension
}

var _ vector.Embedder = (*OpenAIEmbedder)(nil)

// ============== CachedEmbedder ==============

// lruEntry LRU 缓存条目
type lruEntry struct {
	key   string
	value []float32
}

// CachedEmbedder 带 LRU 缓存的 Embedder
//
// 特性：
//   - LRU 淘汰策略：当缓存满时自动淘汰最久未使用的条目
//   - 防缓存击穿：使用 singleflight 确保相同文本并发请求只调用一次底层 Embedder
//   - 线程安全：所有方法都是并发安全的
type CachedEmbedder struct {
	embedder vector.Embedder
	cache    map[string]*list.Element // key -> LRU list element
	lru      *list.List               // LRU 双向链表，最近使用的在前
	mu       sync.RWMutex
	maxSize  int
	sf       singleflight.Group // 防止缓存击穿
}

// CacheOption CachedEmbedder 选项
type CacheOption func(*CachedEmbedder)

// WithMaxCacheSize 设置最大缓存大小
func WithMaxCacheSize(size int) CacheOption {
	return func(e *CachedEmbedder) {
		e.maxSize = size
	}
}

// NewCachedEmbedder 创建带缓存的 Embedder
func NewCachedEmbedder(embedder vector.Embedder, opts ...CacheOption) *CachedEmbedder {
	e := &CachedEmbedder{
		embedder: embedder,
		cache:    make(map[string]*list.Element),
		lru:      list.New(),
		maxSize:  10000,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Embed 将文本列表转换为向量（带 LRU 缓存和防击穿）
func (e *CachedEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	result := make([][]float32, len(texts))
	var toEmbed []string
	var toEmbedIdx []int

	// 第一遍：检查缓存
	e.mu.Lock()
	for i, text := range texts {
		key := hashText(text)
		if elem, ok := e.cache[key]; ok {
			// 缓存命中，移动到 LRU 链表头部
			e.lru.MoveToFront(elem)
			result[i] = elem.Value.(*lruEntry).value
		} else {
			toEmbed = append(toEmbed, text)
			toEmbedIdx = append(toEmbedIdx, i)
		}
	}
	e.mu.Unlock()

	if len(toEmbed) == 0 {
		return result, nil
	}

	// 使用 singleflight 防止并发请求相同文本时多次调用底层 Embedder
	// 为整个批次创建聚合 hash key，避免大批量文本产生超长键
	var batchKeyMaterial strings.Builder
	batchKeyMaterial.Grow(len(toEmbed) * 64)
	for _, text := range toEmbed {
		batchKeyMaterial.WriteString(hashText(text))
	}
	batchKey := hash.SHA256(batchKeyMaterial.String())

	embedResult, err, _ := e.sf.Do(batchKey, func() (interface{}, error) {
		return e.embedder.Embed(ctx, toEmbed)
	})

	if err != nil {
		return nil, err
	}

	embeddings := embedResult.([][]float32)

	// 将结果添加到缓存
	e.mu.Lock()
	for i, embedding := range embeddings {
		idx := toEmbedIdx[i]
		result[idx] = embedding
		key := hashText(toEmbed[i])

		// 如果已存在，先删除旧的
		if elem, ok := e.cache[key]; ok {
			e.lru.Remove(elem)
			delete(e.cache, key)
		}

		// 添加到缓存
		entry := &lruEntry{key: key, value: embedding}
		elem := e.lru.PushFront(entry)
		e.cache[key] = elem

		// LRU 淘汰：如果超过最大容量，删除最久未使用的
		for e.lru.Len() > e.maxSize {
			oldest := e.lru.Back()
			if oldest != nil {
				e.lru.Remove(oldest)
				delete(e.cache, oldest.Value.(*lruEntry).key)
			}
		}
	}
	e.mu.Unlock()

	return result, nil
}

// EmbedOne 将单个文本转换为向量
func (e *CachedEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := e.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return embeddings[0], nil
}

// Dimension 返回向量维度
func (e *CachedEmbedder) Dimension() int {
	return e.embedder.Dimension()
}

// CacheSize 返回缓存大小
func (e *CachedEmbedder) CacheSize() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.cache)
}

// ClearCache 清空缓存
func (e *CachedEmbedder) ClearCache() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cache = make(map[string]*list.Element)
	e.lru.Init()
}

// CacheHitRate 返回缓存命中率（调试用，需要额外跟踪）
// 注意：当前实现不跟踪命中率，如需要可添加 hits/misses 计数器
func (e *CachedEmbedder) CacheHitRate() float64 {
	return 0 // 预留接口
}

var _ vector.Embedder = (*CachedEmbedder)(nil)

// ============== MockEmbedder ==============

// MockEmbedder 模拟 Embedder（用于测试）
type MockEmbedder struct {
	dimension int
	fixedVec  []float32
}

// NewMockEmbedder 创建模拟 Embedder
func NewMockEmbedder(dimension int) *MockEmbedder {
	// 创建一个固定向量
	vec := make([]float32, dimension)
	for i := range vec {
		vec[i] = float32(i) / float32(dimension)
	}
	return &MockEmbedder{
		dimension: dimension,
		fixedVec:  vec,
	}
}

// Embed 模拟嵌入
func (e *MockEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		// 基于文本内容生成不同的向量
		vec := make([]float32, e.dimension)
		hash := hashText(text)
		for j := 0; j < e.dimension && j < len(hash); j++ {
			vec[j] = float32(hash[j]) / 255.0
		}
		// 填充剩余维度
		for j := len(hash); j < e.dimension; j++ {
			vec[j] = e.fixedVec[j]
		}
		result[i] = vec
	}
	return result, nil
}

// EmbedOne 模拟嵌入单个文本
func (e *MockEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := e.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return embeddings[0], nil
}

// Dimension 返回向量维度
func (e *MockEmbedder) Dimension() int {
	return e.dimension
}

var _ vector.Embedder = (*MockEmbedder)(nil)

// ============== FuncEmbedder ==============

// FuncEmbedder 函数式 Embedder
type FuncEmbedder struct {
	embedFn   func(ctx context.Context, texts []string) ([][]float32, error)
	dimension int
}

// NewFuncEmbedder 创建函数式 Embedder
func NewFuncEmbedder(dimension int, fn func(ctx context.Context, texts []string) ([][]float32, error)) *FuncEmbedder {
	return &FuncEmbedder{
		embedFn:   fn,
		dimension: dimension,
	}
}

// Embed 调用函数生成向量
func (e *FuncEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.embedFn(ctx, texts)
}

// EmbedOne 嵌入单个文本
func (e *FuncEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := e.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return embeddings[0], nil
}

// Dimension 返回向量维度
func (e *FuncEmbedder) Dimension() int {
	return e.dimension
}

var _ vector.Embedder = (*FuncEmbedder)(nil)

// ============== 辅助函数 ==============

// hashText 计算文本的 SHA-256 指纹（复用 toolkit 的唯一实现）
func hashText(text string) string {
	return hash.SHA256(text)
}
