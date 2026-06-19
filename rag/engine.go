package rag

import (
	"context"
	"fmt"

	"github.com/hexagon-codes/ai-core/store/vector"
)

// Engine RAG 引擎
// 提供完整的 RAG 能力：文档加载、分割、索引、检索
type Engine struct {
	store    vector.Store
	embedder Embedder
	loader   Loader
	splitter Splitter
	indexer  Indexer

	// 配置
	topK     int
	minScore float32
}

// EngineOption Engine 配置选项
type EngineOption func(*Engine)

// WithStore 设置向量存储
func WithStore(store vector.Store) EngineOption {
	return func(e *Engine) {
		e.store = store
	}
}

// WithEngineEmbedder 设置向量生成器
func WithEngineEmbedder(embedder Embedder) EngineOption {
	return func(e *Engine) {
		e.embedder = embedder
	}
}

// WithLoader 设置文档加载器
func WithLoader(loader Loader) EngineOption {
	return func(e *Engine) {
		e.loader = loader
	}
}

// WithEngineSplitter 设置文档分割器
func WithEngineSplitter(splitter Splitter) EngineOption {
	return func(e *Engine) {
		e.splitter = splitter
	}
}

// WithEngineTopK 设置默认返回数量
func WithEngineTopK(k int) EngineOption {
	return func(e *Engine) {
		e.topK = k
	}
}

// WithEngineMinScore 设置默认最小分数
func WithEngineMinScore(score float32) EngineOption {
	return func(e *Engine) {
		e.minScore = score
	}
}

// NewEngine 创建 RAG 引擎
func NewEngine(opts ...EngineOption) *Engine {
	e := &Engine{
		topK:     5,
		minScore: 0.0,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Ingest 摄取文档
// 从 loader 加载文档，分割后索引到向量存储
func (e *Engine) Ingest(ctx context.Context) error {
	if e.loader == nil {
		return fmt.Errorf("loader is required for ingestion")
	}
	if e.store == nil {
		return fmt.Errorf("store is required for ingestion")
	}
	if e.embedder == nil {
		return fmt.Errorf("embedder is required for ingestion")
	}

	// 1. 加载文档
	docs, err := e.loader.Load(ctx)
	if err != nil {
		return fmt.Errorf("failed to load documents: %w", err)
	}

	// 2. 分割文档
	if e.splitter != nil {
		docs, err = e.splitter.Split(ctx, docs)
		if err != nil {
			return fmt.Errorf("failed to split documents: %w", err)
		}
	}

	// 3. 索引文档
	return e.IndexDocuments(ctx, docs)
}

// IndexDocuments 索引文档列表
func (e *Engine) IndexDocuments(ctx context.Context, docs []Document) error {
	if e.store == nil {
		return fmt.Errorf("store is required")
	}
	if e.embedder == nil {
		return fmt.Errorf("embedder is required")
	}

	// 提取文本
	texts := make([]string, len(docs))
	for i, doc := range docs {
		texts[i] = doc.Content
	}

	// 生成向量
	embeddings, err := e.embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("failed to embed documents: %w", err)
	}

	// 校验 embedder 返回的向量数量与文档数量一致。
	// embedder 是外部接口, 返回数量不可信; 若数量不匹配, 直接按下标
	// embeddings[i] 访问会越界 panic 或造成向量与文档错位, 因此在此显式拦截。
	if len(embeddings) != len(docs) {
		return fmt.Errorf("embedding count mismatch: embedder returned %d embeddings for %d documents", len(embeddings), len(docs))
	}

	// 维度校验: embedder.Dimension() 声明了期望维度, 逐一校验每个向量
	// 长度与之一致, 避免维度错配的向量写入存储 (store 侧维度交叉校验为后续项)。
	if dim := e.embedder.Dimension(); dim > 0 {
		for i, emb := range embeddings {
			if len(emb) != dim {
				return fmt.Errorf("embedding dimension mismatch at index %d: got %d, want %d", i, len(emb), dim)
			}
		}
	}

	// 转换并存储
	vectorDocs := make([]vector.Document, len(docs))
	for i, doc := range docs {
		vectorDocs[i] = vector.Document{
			ID:        doc.ID,
			Content:   doc.Content,
			Embedding: embeddings[i],
			Metadata:  doc.Metadata,
			CreatedAt: doc.CreatedAt,
		}
	}

	return e.store.Add(ctx, vectorDocs)
}

// Retrieve 检索相关文档
func (e *Engine) Retrieve(ctx context.Context, query string, opts ...RetrieveOption) ([]Document, error) {
	if e.store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if e.embedder == nil {
		return nil, fmt.Errorf("embedder is required")
	}

	cfg := &RetrieveConfig{
		TopK:     e.topK,
		MinScore: e.minScore,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// 负数 TopK 是非法输入。底层 store.Search 对负数 k 执行
	// make([]Document, k) 会 panic, 因此在 Engine 层做下限校验:
	// 将负数 clamp 为 0 (语义同 TopK=0, 即返回空结果), 不向下游透传非法值。
	if cfg.TopK < 0 {
		cfg.TopK = 0
	}

	// 生成查询向量
	embedding, err := e.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}
	if len(embedding) == 0 {
		return nil, fmt.Errorf("no embedding returned for query")
	}

	// 搜索
	searchOpts := []vector.SearchOption{
		vector.WithMinScore(cfg.MinScore),
		vector.WithMetadata(true),
	}
	if cfg.Filter != nil {
		searchOpts = append(searchOpts, vector.WithFilter(cfg.Filter))
	}

	vectorDocs, err := e.store.Search(ctx, embedding[0], cfg.TopK, searchOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to search: %w", err)
	}

	// 转换结果
	docs := make([]Document, len(vectorDocs))
	for i, vd := range vectorDocs {
		docs[i] = Document{
			ID:        vd.ID,
			Content:   vd.Content,
			Metadata:  vd.Metadata,
			Score:     vd.Score,
			CreatedAt: vd.CreatedAt,
		}
	}

	return docs, nil
}

// Query 检索并返回格式化的上下文
func (e *Engine) Query(ctx context.Context, query string, opts ...RetrieveOption) (string, error) {
	docs, err := e.Retrieve(ctx, query, opts...)
	if err != nil {
		return "", err
	}

	// 格式化上下文
	var context string
	for i, doc := range docs {
		context += fmt.Sprintf("[Document %d (score: %.2f)]\n%s\n\n", i+1, doc.Score, doc.Content)
	}

	return context, nil
}

// Delete 删除文档
func (e *Engine) Delete(ctx context.Context, ids []string) error {
	if e.store == nil {
		return fmt.Errorf("store is required")
	}
	return e.store.Delete(ctx, ids)
}

// Clear 清空所有文档
func (e *Engine) Clear(ctx context.Context) error {
	if e.store == nil {
		return fmt.Errorf("store is required")
	}
	return e.store.Clear(ctx)
}

// Count 返回文档数量
func (e *Engine) Count(ctx context.Context) (int, error) {
	if e.store == nil {
		return 0, fmt.Errorf("store is required")
	}
	return e.store.Count(ctx)
}

// Index 实现 Indexer 接口
func (e *Engine) Index(ctx context.Context, docs []Document) error {
	return e.IndexDocuments(ctx, docs)
}

// 确保 Engine 实现了 RAG 接口
var _ RAG = (*Engine)(nil)
