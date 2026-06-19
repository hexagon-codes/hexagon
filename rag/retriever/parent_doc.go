// Package retriever 提供 RAG 系统的文档检索器
package retriever

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/rag"
)

// ParentDocRetriever 父子文档检索器
// 子文档用于精确匹配，返回父文档提供完整上下文
//
// 工作原理：
//  1. 索引时：将原始文档保存为父文档，分割成子块后存入向量存储
//  2. 检索时：用子块进行向量检索，找到相关子块后返回对应的父文档
//  3. 优势：子块用于精确语义匹配，父文档提供更完整的上下文
//
// 参考 LlamaIndex 的 ParentDocumentRetriever 设计
//
// 使用示例：
//
//	retriever := NewParentDocRetriever(
//	    vectorStore, embedder,
//	    WithChildSplitter(splitter.NewRecursiveSplitter(200, 50)),
//	    WithParentTopK(5),
//	)
//	// 索引文档
//	retriever.Index(ctx, docs)
//	// 检索
//	parentDocs, err := retriever.Retrieve(ctx, "query")
type ParentDocRetriever struct {
	// childStore 子文档向量存储
	childStore vector.Store

	// parentStore 父文档存储（ID -> Document）
	parentStore *DocumentStore

	// embedder 向量嵌入器
	embedder vector.Embedder

	// childSplitter 子文档分割器
	childSplitter rag.Splitter

	// childTopK 检索子文档数量
	childTopK int

	// parentTopK 返回父文档数量
	parentTopK int

	// minScore 最小相关性分数
	minScore float32

	// mu 保护并发访问
	mu sync.RWMutex
}

// DocumentStore 简单的文档存储
// 用于存储父文档
type DocumentStore struct {
	docs map[string]rag.Document
	mu   sync.RWMutex
}

// NewDocumentStore 创建文档存储
func NewDocumentStore() *DocumentStore {
	return &DocumentStore{
		docs: make(map[string]rag.Document),
	}
}

// Save 保存文档
func (s *DocumentStore) Save(doc rag.Document) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[doc.ID] = doc
}

// Get 获取文档
func (s *DocumentStore) Get(id string) (rag.Document, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, ok := s.docs[id]
	return doc, ok
}

// Delete 删除文档
func (s *DocumentStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.docs, id)
}

// Clear 清空存储
func (s *DocumentStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs = make(map[string]rag.Document)
}

// Count 返回文档数量
func (s *DocumentStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.docs)
}

// ParentDocOption ParentDocRetriever 配置选项
type ParentDocOption func(*ParentDocRetriever)

// WithChildSplitter 设置子文档分割器
func WithChildSplitter(splitter rag.Splitter) ParentDocOption {
	return func(r *ParentDocRetriever) {
		r.childSplitter = splitter
	}
}

// WithChildTopK 设置检索子文档数量
// 默认值: 10
func WithChildTopK(k int) ParentDocOption {
	return func(r *ParentDocRetriever) {
		if k > 0 {
			r.childTopK = k
		}
	}
}

// WithParentTopK 设置返回父文档数量
// 默认值: 5
func WithParentTopK(k int) ParentDocOption {
	return func(r *ParentDocRetriever) {
		if k > 0 {
			r.parentTopK = k
		}
	}
}

// WithParentMinScore 设置最小相关性分数
func WithParentMinScore(score float32) ParentDocOption {
	return func(r *ParentDocRetriever) {
		r.minScore = score
	}
}

// WithParentStore 设置父文档存储（可用于持久化）
func WithParentStore(store *DocumentStore) ParentDocOption {
	return func(r *ParentDocRetriever) {
		r.parentStore = store
	}
}

// NewParentDocRetriever 创建父子文档检索器
//
// 参数：
//   - childStore: 子文档向量存储
//   - embedder: 向量嵌入器
//   - opts: 配置选项
func NewParentDocRetriever(childStore vector.Store, embedder vector.Embedder, opts ...ParentDocOption) *ParentDocRetriever {
	r := &ParentDocRetriever{
		childStore:  childStore,
		parentStore: NewDocumentStore(),
		embedder:    embedder,
		childTopK:   10,
		parentTopK:  5,
		minScore:    0.0,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Index 索引文档
// 将原始文档保存为父文档，分割成子块后存入向量存储。
// 仅在访问内存状态时短暂持锁，Embed 等耗时操作在锁外执行，避免阻塞 Retrieve。
func (r *ParentDocRetriever) Index(ctx context.Context, docs []rag.Document) error {
	for _, doc := range docs {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// 生成父文档 ID（如果没有）
		if doc.ID == "" {
			doc.ID = generateDocID(doc.Content)
		}
		if doc.CreatedAt.IsZero() {
			doc.CreatedAt = time.Now()
		}

		// 短暂持锁保存父文档
		r.mu.Lock()
		r.parentStore.Save(doc)
		r.mu.Unlock()

		// 分割成子块（在锁外执行）
		var childDocs []rag.Document
		if r.childSplitter != nil {
			var err error
			childDocs, err = r.childSplitter.Split(ctx, []rag.Document{doc})
			if err != nil {
				return fmt.Errorf("分割文档 %s 失败: %w", doc.ID, err)
			}
		} else {
			// 没有分割器，直接使用原文档作为子文档
			childDocs = []rag.Document{doc}
		}

		// 为每个子文档设置 parent_id 元数据
		for i := range childDocs {
			if childDocs[i].ID == "" {
				childDocs[i].ID = fmt.Sprintf("%s_chunk_%d", doc.ID, i)
			}
			if childDocs[i].Metadata == nil {
				childDocs[i].Metadata = make(map[string]any)
			}
			childDocs[i].Metadata["parent_id"] = doc.ID
			childDocs[i].Metadata["chunk_index"] = i
		}

		// 向量化子文档（在锁外执行，此操作可能耗时数秒）
		texts := make([]string, len(childDocs))
		for i, cd := range childDocs {
			texts[i] = cd.Content
		}

		embeddings, err := r.embedder.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("向量化文档 %s 的子块失败: %w", doc.ID, err)
		}

		// 设置向量并转换为 vector.Document
		vectorDocs := make([]vector.Document, len(childDocs))
		for i := range childDocs {
			if i < len(embeddings) {
				childDocs[i].Embedding = embeddings[i]
			}
			vectorDocs[i] = ragDocToVectorDoc(childDocs[i])
		}

		// 存入向量存储（在锁外执行）
		if err := r.childStore.Add(ctx, vectorDocs); err != nil {
			return fmt.Errorf("存储文档 %s 的子块失败: %w", doc.ID, err)
		}
	}

	return nil
}

// Retrieve 检索相关的父文档
// 先检索子块，然后返回对应的父文档
func (r *ParentDocRetriever) Retrieve(ctx context.Context, query string, opts ...rag.RetrieveOption) ([]rag.Document, error) {
	cfg := &rag.RetrieveConfig{
		TopK:     r.parentTopK,
		MinScore: r.minScore,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	// 向量化查询
	embedding, err := r.embedder.EmbedOne(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("向量化查询失败: %w", err)
	}

	// 检索子文档
	searchOpts := []vector.SearchOption{
		vector.WithMinScore(cfg.MinScore),
		vector.WithMetadata(true),
	}
	if cfg.Filter != nil {
		searchOpts = append(searchOpts, vector.WithFilter(cfg.Filter))
	}

	childDocs, err := r.childStore.Search(ctx, embedding, r.childTopK, searchOpts...)
	if err != nil {
		return nil, fmt.Errorf("检索子文档失败: %w", err)
	}

	// 收集父文档 ID 和最高分数
	parentScores := make(map[string]float32)
	for _, child := range childDocs {
		parentID, ok := child.Metadata["parent_id"].(string)
		if !ok {
			continue
		}
		// 记录每个父文档的最高子文档分数
		if child.Score > parentScores[parentID] {
			parentScores[parentID] = child.Score
		}
	}

	// 按分数排序父文档 ID
	type scoredParent struct {
		id    string
		score float32
	}
	var scored []scoredParent
	for id, score := range parentScores {
		scored = append(scored, scoredParent{id: id, score: score})
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// 获取父文档
	k := cfg.TopK
	if k > len(scored) {
		k = len(scored)
	}

	parentDocs := make([]rag.Document, 0, k)
	for i := 0; i < k; i++ {
		parent, ok := r.parentStore.Get(scored[i].id)
		if ok {
			parent.Score = scored[i].score
			// 添加检索元数据
			if parent.Metadata == nil {
				parent.Metadata = make(map[string]any)
			}
			parent.Metadata["retrieval_type"] = "parent_doc"
			parentDocs = append(parentDocs, parent)
		}
	}

	return parentDocs, nil
}

// Delete 删除文档（包括父文档和所有子块）
func (r *ParentDocRetriever) Delete(ctx context.Context, ids []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, id := range ids {
		// 删除父文档
		r.parentStore.Delete(id)

		// 删除子块（通过 parent_id 过滤）
		// 注意：这需要向量存储支持按元数据删除
		// 如果不支持，可以记录子块 ID 后逐个删除
	}

	return nil
}

// Clear 清空所有文档
func (r *ParentDocRetriever) Clear(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.parentStore.Clear()
	return r.childStore.Clear(ctx)
}

// Count 返回父文档数量
func (r *ParentDocRetriever) Count(ctx context.Context) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.parentStore.Count(), nil
}

// GetParentStore 获取父文档存储（用于序列化/持久化）
func (r *ParentDocRetriever) GetParentStore() *DocumentStore {
	return r.parentStore
}

// generateDocID 生成文档 ID
func generateDocID(content string) string {
	hash := sha256.Sum256([]byte(content))
	return "doc_" + hex.EncodeToString(hash[:8])
}

// ragDocToVectorDoc 将 rag.Document 转换为 vector.Document
func ragDocToVectorDoc(doc rag.Document) vector.Document {
	return vector.Document{
		ID:        doc.ID,
		Content:   doc.Content,
		Embedding: doc.Embedding,
		Metadata:  doc.Metadata,
		Score:     doc.Score,
		CreatedAt: doc.CreatedAt,
	}
}

// vectorDocToRagDoc 将 vector.Document 转换为 rag.Document
func vectorDocToRagDoc(doc vector.Document) rag.Document {
	return rag.Document{
		ID:        doc.ID,
		Content:   doc.Content,
		Embedding: doc.Embedding,
		Metadata:  doc.Metadata,
		Score:     doc.Score,
		CreatedAt: doc.CreatedAt,
	}
}

// 确保实现了 Retriever 接口
var _ rag.Retriever = (*ParentDocRetriever)(nil)
