// Package mock 提供 Hexagon AI Agent 框架测试的 Mock 实现
//
// 本文件实现向量存储和向量生成器的 Mock：
//   - MockVectorStore: 模拟 vector.Store 接口
//   - MockEmbedder: 模拟 vector.Embedder 接口
package mock

import (
	"context"
	"fmt"
	"sync"

	"github.com/hexagon-codes/ai-core/store/vector"
)

// ============== MockVectorStore ==============

// MockVectorStore 模拟向量存储
// 支持内存存储、搜索结果注入、错误注入和调用追踪
type MockVectorStore struct {
	docs         map[string]vector.Document
	mu           sync.RWMutex
	searchCalls  []searchCall
	searchResult []vector.Document
	searchErr    error

	// 自定义搜索函数
	searchFn func(ctx context.Context, query []float32, k int, opts ...vector.SearchOption) ([]vector.Document, error)
}

// searchCall 记录搜索调用
type searchCall struct {
	Query []float32
	K     int
}

// VectorStoreOption MockVectorStore 选项
type VectorStoreOption func(*MockVectorStore)

// WithSearchResults 预设搜索结果
func WithSearchResults(docs []vector.Document) VectorStoreOption {
	return func(s *MockVectorStore) {
		s.searchResult = docs
	}
}

// WithSearchError 预设搜索错误
func WithSearchError(err error) VectorStoreOption {
	return func(s *MockVectorStore) {
		s.searchErr = err
	}
}

// WithSearchFn 自定义搜索函数
func WithSearchFn(fn func(ctx context.Context, query []float32, k int, opts ...vector.SearchOption) ([]vector.Document, error)) VectorStoreOption {
	return func(s *MockVectorStore) {
		s.searchFn = fn
	}
}

// NewMockVectorStore 创建 Mock 向量存储
func NewMockVectorStore(opts ...VectorStoreOption) *MockVectorStore {
	s := &MockVectorStore{
		docs:        make(map[string]vector.Document),
		searchCalls: make([]searchCall, 0),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Add 添加文档
func (s *MockVectorStore) Add(ctx context.Context, docs []vector.Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, doc := range docs {
		s.docs[doc.ID] = doc
	}
	return nil
}

// Search 搜索文档
func (s *MockVectorStore) Search(ctx context.Context, query []float32, k int, opts ...vector.SearchOption) ([]vector.Document, error) {
	s.mu.Lock()
	s.searchCalls = append(s.searchCalls, searchCall{Query: query, K: k})
	s.mu.Unlock()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	if s.searchFn != nil {
		return s.searchFn(ctx, query, k, opts...)
	}

	if s.searchErr != nil {
		return nil, s.searchErr
	}

	if s.searchResult != nil {
		result := s.searchResult
		if k < len(result) {
			result = result[:k]
		}
		return result, nil
	}

	// 返回所有存储的文档
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []vector.Document
	for _, doc := range s.docs {
		result = append(result, doc)
		if len(result) >= k {
			break
		}
	}
	return result, nil
}

// Get 获取文档
func (s *MockVectorStore) Get(ctx context.Context, id string) (*vector.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if doc, ok := s.docs[id]; ok {
		return &doc, nil
	}
	return nil, nil
}

// Delete 删除文档
func (s *MockVectorStore) Delete(ctx context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		delete(s.docs, id)
	}
	return nil
}

// Clear 清空存储
func (s *MockVectorStore) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs = make(map[string]vector.Document)
	return nil
}

// Count 返回文档数量
func (s *MockVectorStore) Count(ctx context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.docs), nil
}

// Close 关闭存储
func (s *MockVectorStore) Close() error {
	return nil
}

// SearchCalls 返回搜索调用记录
func (s *MockVectorStore) SearchCalls() []searchCall {
	s.mu.RLock()
	defer s.mu.RUnlock()
	calls := make([]searchCall, len(s.searchCalls))
	copy(calls, s.searchCalls)
	return calls
}

// SearchCallCount 返回搜索调用次数
func (s *MockVectorStore) SearchCallCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.searchCalls)
}

var _ vector.Store = (*MockVectorStore)(nil)

// ============== MockEmbedder ==============

// MockEmbedder 模拟向量生成器
// 支持固定维度向量生成、自定义 embed 函数和错误注入
type MockEmbedder struct {
	dim     int
	mu      sync.RWMutex
	calls   [][]string
	embedFn func(ctx context.Context, texts []string) ([][]float32, error)
	err     error
}

// EmbedderOption MockEmbedder 选项
type EmbedderOption func(*MockEmbedder)

// WithEmbedFn 自定义 embed 函数
func WithEmbedFn(fn func(ctx context.Context, texts []string) ([][]float32, error)) EmbedderOption {
	return func(e *MockEmbedder) {
		e.embedFn = fn
	}
}

// WithEmbedError 预设 embed 错误
func WithEmbedError(err error) EmbedderOption {
	return func(e *MockEmbedder) {
		e.err = err
	}
}

// NewMockEmbedder 创建 Mock 向量生成器
func NewMockEmbedder(dim int, opts ...EmbedderOption) *MockEmbedder {
	e := &MockEmbedder{
		dim:   dim,
		calls: make([][]string, 0),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// FixedEmbedder 创建返回固定值向量的生成器
func FixedEmbedder(dim int) *MockEmbedder {
	return NewMockEmbedder(dim)
}

// Embed 将文本转为向量
func (e *MockEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.calls = append(e.calls, texts)
	e.mu.Unlock()

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	if e.err != nil {
		return nil, e.err
	}

	if e.embedFn != nil {
		return e.embedFn(ctx, texts)
	}

	// 默认：为每个文本生成基于长度的固定向量
	result := make([][]float32, len(texts))
	for i, text := range texts {
		vec := make([]float32, e.dim)
		for j := range vec {
			vec[j] = float32(len(text)+i+j) * 0.01
		}
		result[i] = vec
	}
	return result, nil
}

// EmbedOne 将单个文本转为向量
func (e *MockEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := e.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("embed 返回空结果")
	}
	return embeddings[0], nil
}

// Dimension 返回向量维度
func (e *MockEmbedder) Dimension() int {
	return e.dim
}

// EmbedCalls 返回所有调用记录
func (e *MockEmbedder) EmbedCalls() [][]string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	calls := make([][]string, len(e.calls))
	copy(calls, e.calls)
	return calls
}

// EmbedCallCount 返回调用次数
func (e *MockEmbedder) EmbedCallCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.calls)
}

var _ vector.Embedder = (*MockEmbedder)(nil)
