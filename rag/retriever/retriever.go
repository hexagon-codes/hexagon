// Package retriever 提供 RAG 系统的文档检索器
//
// Retriever 用于从向量存储中检索相关文档：
//   - VectorRetriever: 基于向量相似度检索
//   - KeywordRetriever: 基于关键词检索
//   - HybridRetriever: 混合检索（向量 + 关键词）
//   - MultiRetriever: 多源检索聚合
package retriever

import (
	"context"
	"sort"
	"strings"

	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/rag"
)

// ============== VectorRetriever ==============

// VectorRetriever 向量检索器
type VectorRetriever struct {
	store    vector.Store
	embedder vector.Embedder
	topK     int
	minScore float32
}

// VectorOption VectorRetriever 选项
type VectorOption func(*VectorRetriever)

// WithTopK 设置返回数量
func WithTopK(k int) VectorOption {
	return func(r *VectorRetriever) {
		r.topK = k
	}
}

// WithMinScore 设置最小分数
func WithMinScore(score float32) VectorOption {
	return func(r *VectorRetriever) {
		r.minScore = score
	}
}

// NewVectorRetriever 创建向量检索器
func NewVectorRetriever(store vector.Store, embedder vector.Embedder, opts ...VectorOption) *VectorRetriever {
	r := &VectorRetriever{
		store:    store,
		embedder: embedder,
		topK:     5,
		minScore: 0.0,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Retrieve 检索相关文档
func (r *VectorRetriever) Retrieve(ctx context.Context, query string, opts ...rag.RetrieveOption) ([]rag.Document, error) {
	cfg := &rag.RetrieveConfig{
		TopK:     r.topK,
		MinScore: r.minScore,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// 生成查询向量
	embedding, err := r.embedder.EmbedOne(ctx, query)
	if err != nil {
		return nil, err
	}

	// 搜索向量存储
	searchOpts := []vector.SearchOption{
		vector.WithMinScore(cfg.MinScore),
		vector.WithMetadata(true),
	}
	if cfg.Filter != nil {
		searchOpts = append(searchOpts, vector.WithFilter(cfg.Filter))
	}

	vectorDocs, err := r.store.Search(ctx, embedding, cfg.TopK, searchOpts...)
	if err != nil {
		return nil, err
	}

	// 转换为 rag.Document
	docs := make([]rag.Document, len(vectorDocs))
	for i, vd := range vectorDocs {
		docs[i] = rag.Document{
			ID:        vd.ID,
			Content:   vd.Content,
			Metadata:  vd.Metadata,
			Embedding: vd.Embedding,
			Score:     vd.Score,
			CreatedAt: vd.CreatedAt,
		}
	}

	return docs, nil
}

var _ rag.Retriever = (*VectorRetriever)(nil)

// ============== KeywordRetriever ==============

// KeywordRetriever 关键词检索器
type KeywordRetriever struct {
	documents []rag.Document
	topK      int
}

// KeywordOption KeywordRetriever 选项
type KeywordOption func(*KeywordRetriever)

// WithKeywordTopK 设置返回数量
func WithKeywordTopK(k int) KeywordOption {
	return func(r *KeywordRetriever) {
		r.topK = k
	}
}

// NewKeywordRetriever 创建关键词检索器
func NewKeywordRetriever(docs []rag.Document, opts ...KeywordOption) *KeywordRetriever {
	r := &KeywordRetriever{
		documents: docs,
		topK:      5,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// AddDocuments 添加文档
func (r *KeywordRetriever) AddDocuments(docs []rag.Document) {
	r.documents = append(r.documents, docs...)
}

// Retrieve 检索相关文档
func (r *KeywordRetriever) Retrieve(ctx context.Context, query string, opts ...rag.RetrieveOption) ([]rag.Document, error) {
	cfg := &rag.RetrieveConfig{
		TopK: r.topK,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// 分词（简单空格分词）
	queryTerms := tokenize(query)

	// 计算 BM25 分数
	type scoredDoc struct {
		doc   rag.Document
		score float32
	}

	var scored []scoredDoc
	for _, doc := range r.documents {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// 检查过滤条件
		if cfg.Filter != nil && !matchFilter(doc.Metadata, cfg.Filter) {
			continue
		}

		score := bm25Score(queryTerms, doc.Content)
		if score > cfg.MinScore {
			doc.Score = score
			scored = append(scored, scoredDoc{doc: doc, score: score})
		}
	}

	// 排序
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// 返回 TopK
	k := cfg.TopK
	if k > len(scored) {
		k = len(scored)
	}

	result := make([]rag.Document, k)
	for i := 0; i < k; i++ {
		result[i] = scored[i].doc
	}

	return result, nil
}

var _ rag.Retriever = (*KeywordRetriever)(nil)

// ============== HybridRetriever ==============

// HybridRetriever 混合检索器
// 结合向量检索和关键词检索的结果
type HybridRetriever struct {
	vectorRetriever  rag.Retriever
	keywordRetriever rag.Retriever
	vectorWeight     float32
	keywordWeight    float32
	topK             int
}

// HybridOption HybridRetriever 选项
type HybridOption func(*HybridRetriever)

// WithVectorWeight 设置向量检索权重
func WithVectorWeight(w float32) HybridOption {
	return func(r *HybridRetriever) {
		r.vectorWeight = w
	}
}

// WithKeywordWeight 设置关键词检索权重
func WithKeywordWeight(w float32) HybridOption {
	return func(r *HybridRetriever) {
		r.keywordWeight = w
	}
}

// WithHybridTopK 设置返回数量
func WithHybridTopK(k int) HybridOption {
	return func(r *HybridRetriever) {
		r.topK = k
	}
}

// NewHybridRetriever 创建混合检索器
func NewHybridRetriever(vectorRet, keywordRet rag.Retriever, opts ...HybridOption) *HybridRetriever {
	r := &HybridRetriever{
		vectorRetriever:  vectorRet,
		keywordRetriever: keywordRet,
		vectorWeight:     0.7,
		keywordWeight:    0.3,
		topK:             5,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Retrieve 混合检索
func (r *HybridRetriever) Retrieve(ctx context.Context, query string, opts ...rag.RetrieveOption) ([]rag.Document, error) {
	cfg := &rag.RetrieveConfig{
		TopK: r.topK,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// 并行执行两种检索
	type result struct {
		docs []rag.Document
		err  error
	}

	vectorCh := make(chan result, 1)
	keywordCh := make(chan result, 1)

	go func() {
		docs, err := r.vectorRetriever.Retrieve(ctx, query, rag.WithTopK(cfg.TopK*2))
		vectorCh <- result{docs: docs, err: err}
	}()

	go func() {
		docs, err := r.keywordRetriever.Retrieve(ctx, query, rag.WithTopK(cfg.TopK*2))
		keywordCh <- result{docs: docs, err: err}
	}()

	// 收集结果
	vectorRes := <-vectorCh
	keywordRes := <-keywordCh

	if vectorRes.err != nil {
		return nil, vectorRes.err
	}
	if keywordRes.err != nil {
		return nil, keywordRes.err
	}

	// 融合分数 (Reciprocal Rank Fusion)
	scoreMap := make(map[string]float32)
	docMap := make(map[string]rag.Document)

	for i, doc := range vectorRes.docs {
		rank := float32(i + 1)
		rrf := r.vectorWeight / (60 + rank) // RRF 公式
		scoreMap[doc.ID] += rrf
		docMap[doc.ID] = doc
	}

	for i, doc := range keywordRes.docs {
		rank := float32(i + 1)
		rrf := r.keywordWeight / (60 + rank)
		scoreMap[doc.ID] += rrf
		if _, exists := docMap[doc.ID]; !exists {
			docMap[doc.ID] = doc
		}
	}

	// 排序
	type scoredDoc struct {
		id    string
		score float32
	}
	var scored []scoredDoc
	for id, score := range scoreMap {
		scored = append(scored, scoredDoc{id: id, score: score})
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// 返回 TopK
	k := cfg.TopK
	if k > len(scored) {
		k = len(scored)
	}

	finalDocs := make([]rag.Document, k)
	for i := 0; i < k; i++ {
		doc := docMap[scored[i].id]
		doc.Score = scored[i].score
		finalDocs[i] = doc
	}

	return finalDocs, nil
}

var _ rag.Retriever = (*HybridRetriever)(nil)

// ============== MultiRetriever ==============

// MultiRetriever 多源检索器
// 从多个检索器获取结果并聚合
type MultiRetriever struct {
	retrievers []rag.Retriever
	topK       int
	dedupe     bool // 是否去重
}

// MultiOption MultiRetriever 选项
type MultiOption func(*MultiRetriever)

// WithMultiTopK 设置返回数量
func WithMultiTopK(k int) MultiOption {
	return func(r *MultiRetriever) {
		r.topK = k
	}
}

// WithDedupe 设置是否去重
func WithDedupe(dedupe bool) MultiOption {
	return func(r *MultiRetriever) {
		r.dedupe = dedupe
	}
}

// NewMultiRetriever 创建多源检索器
func NewMultiRetriever(retrievers []rag.Retriever, opts ...MultiOption) *MultiRetriever {
	r := &MultiRetriever{
		retrievers: retrievers,
		topK:       5,
		dedupe:     true,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Retrieve 多源检索
func (r *MultiRetriever) Retrieve(ctx context.Context, query string, opts ...rag.RetrieveOption) ([]rag.Document, error) {
	cfg := &rag.RetrieveConfig{
		TopK: r.topK,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// 并行从所有检索器获取结果
	type result struct {
		docs []rag.Document
		err  error
	}

	results := make(chan result, len(r.retrievers))

	for _, ret := range r.retrievers {
		go func(retriever rag.Retriever) {
			docs, err := retriever.Retrieve(ctx, query, rag.WithTopK(cfg.TopK))
			results <- result{docs: docs, err: err}
		}(ret)
	}

	// 收集结果
	var allDocs []rag.Document
	seen := make(map[string]bool)

	for i := 0; i < len(r.retrievers); i++ {
		res := <-results
		if res.err != nil {
			continue // 跳过失败的检索器
		}

		for _, doc := range res.docs {
			if r.dedupe {
				if seen[doc.ID] {
					continue
				}
				seen[doc.ID] = true
			}
			allDocs = append(allDocs, doc)
		}
	}

	// 按分数排序
	sort.Slice(allDocs, func(i, j int) bool {
		return allDocs[i].Score > allDocs[j].Score
	})

	// 返回 TopK
	k := cfg.TopK
	if k > len(allDocs) {
		k = len(allDocs)
	}

	return allDocs[:k], nil
}

var _ rag.Retriever = (*MultiRetriever)(nil)

// ============== RerankerRetriever ==============

// Reranker 重排序器接口
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []rag.Document) ([]rag.Document, error)
}

// RerankerRetriever 带重排序的检索器
type RerankerRetriever struct {
	retriever rag.Retriever
	reranker  Reranker
	topK      int
	fetchK    int // 初始获取数量
}

// RerankerOption RerankerRetriever 选项
type RerankerOption func(*RerankerRetriever)

// WithRerankerTopK 设置最终返回数量
func WithRerankerTopK(k int) RerankerOption {
	return func(r *RerankerRetriever) {
		r.topK = k
	}
}

// WithFetchK 设置初始获取数量
func WithFetchK(k int) RerankerOption {
	return func(r *RerankerRetriever) {
		r.fetchK = k
	}
}

// NewRerankerRetriever 创建带重排序的检索器
func NewRerankerRetriever(retriever rag.Retriever, reranker Reranker, opts ...RerankerOption) *RerankerRetriever {
	r := &RerankerRetriever{
		retriever: retriever,
		reranker:  reranker,
		topK:      5,
		fetchK:    20,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Retrieve 检索并重排序
func (r *RerankerRetriever) Retrieve(ctx context.Context, query string, opts ...rag.RetrieveOption) ([]rag.Document, error) {
	cfg := &rag.RetrieveConfig{
		TopK: r.topK,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// 初始检索
	docs, err := r.retriever.Retrieve(ctx, query, rag.WithTopK(r.fetchK))
	if err != nil {
		return nil, err
	}

	// 重排序
	reranked, err := r.reranker.Rerank(ctx, query, docs)
	if err != nil {
		return nil, err
	}

	// 返回 TopK
	k := cfg.TopK
	if k > len(reranked) {
		k = len(reranked)
	}

	return reranked[:k], nil
}

var _ rag.Retriever = (*RerankerRetriever)(nil)

// ============== 辅助函数 ==============

// tokenize 简单分词
func tokenize(text string) []string {
	// 简单按空格和标点分词
	text = strings.ToLower(text)
	var tokens []string
	var current strings.Builder

	for _, r := range text {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= '\u4e00' && r <= '\u9fff' {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

// bm25Score 计算简化版 BM25 分数
func bm25Score(queryTerms []string, content string) float32 {
	contentLower := strings.ToLower(content)
	contentTerms := tokenize(content)

	// 计算词频
	tf := make(map[string]int)
	for _, term := range contentTerms {
		tf[term]++
	}

	// 计算分数
	var score float32
	k1 := float32(1.2)
	b := float32(0.75)
	avgDl := float32(500) // 假设平均文档长度
	dl := float32(len(contentTerms))

	// 计算简化 IDF：使用查询词在文档中出现的比例来近似
	// 匹配越少的词权重越高（更有区分度）
	numQueryTerms := float32(len(queryTerms))
	if numQueryTerms == 0 {
		numQueryTerms = 1
	}

	for _, term := range queryTerms {
		termFreq := float32(tf[term])
		if termFreq == 0 {
			// 检查是否包含该词（中文兼容）
			if strings.Contains(contentLower, term) {
				termFreq = 1
			}
		}
		if termFreq > 0 {
			// 简化 IDF：假设 10 篇文档中有 df 篇包含该词
			// IDF = ln(N/df)，这里用 ln(10/1) ≈ 2.3 作为基础权重
			// 匹配到的词越多，单个词权重相对降低
			idf := float32(2.3) / numQueryTerms * float32(len(queryTerms))
			numerator := termFreq * (k1 + 1)
			denominator := termFreq + k1*(1-b+b*dl/avgDl)
			score += idf * numerator / denominator
		}
	}

	return score
}

// matchFilter 检查元数据是否匹配过滤条件
func matchFilter(metadata, filter map[string]any) bool {
	if metadata == nil {
		return len(filter) == 0
	}

	for k, v := range filter {
		if mv, ok := metadata[k]; !ok || mv != v {
			return false
		}
	}
	return true
}
