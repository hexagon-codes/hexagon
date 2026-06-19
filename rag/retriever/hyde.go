// Package retriever 提供 RAG 系统的文档检索器
//
// hyde.go 实现 HyDE (Hypothetical Document Embeddings) 假设文档检索：
//   - HyDERetriever: 通过 LLM 生成假设文档，用假设文档的向量检索真实文档
//   - VectorMergeStrategy: 多个假设文档向量的合并策略
//
// HyDE 核心思想：
//
//	用户查询和相关文档之间存在"语义鸿沟"。HyDE 先让 LLM 生成一个
//	假设的理想答案文档，然后用该文档的向量去检索真实文档。
//	因为假设文档与真实文档在语义空间中更接近，检索效果通常更好。
//
// 对标 LangChain HypotheticalDocumentEmbedder / LlamaIndex HyDEQueryTransform。
//
// 使用示例：
//
//	hyde := NewHyDERetriever(
//	    llmProvider,
//	    embedder,
//	    vectorStore,
//	    WithHyDENumHypothetical(3),
//	    WithHyDETopK(10),
//	)
//	docs, err := hyde.Retrieve(ctx, "Go 语言的并发模型有什么优势？")
package retriever

import (
	"context"
	"fmt"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/rag"
)

// VectorMergeStrategy 假设文档向量合并策略
type VectorMergeStrategy int

const (
	// MergeAverage 对所有假设文档的向量取平均值
	// 适用于假设文档质量较均匀的场景
	MergeAverage VectorMergeStrategy = iota

	// MergeSearchAll 分别用每个假设文档向量检索，然后聚合去重
	// 适用于需要更多样化结果的场景
	MergeSearchAll
)

// HyDERetriever HyDE 假设文档检索器
// 核心流程：
//  1. 用户查询 → LLM 生成 N 个假设文档
//  2. 假设文档 → 向量化
//  3. 假设文档向量 → 检索真实文档
//  4. 去重排序 → 返回结果
type HyDERetriever struct {
	// llmProvider LLM 提供者（用于生成假设文档）
	llmProvider llm.Provider

	// embedder 向量生成器（将假设文档转为向量）
	embedder vector.Embedder

	// store 向量存储（检索真实文档）
	store vector.Store

	// promptTemplate 生成假设文档的提示词模板
	// 模板中 %s 会被替换为用户查询
	promptTemplate string

	// numHypothetical 生成的假设文档数量
	numHypothetical int

	// topK 每次检索返回的文档数量
	topK int

	// mergeStrategy 多向量合并策略
	mergeStrategy VectorMergeStrategy

	// model 使用的 LLM 模型
	model string

	// temperature LLM 采样温度
	temperature float64
}

// HyDEOption HyDE 检索器选项
type HyDEOption func(*HyDERetriever)

// WithHyDEModel 设置 LLM 模型名称
func WithHyDEModel(model string) HyDEOption {
	return func(r *HyDERetriever) {
		r.model = model
	}
}

// WithHyDEPrompt 设置假设文档生成的提示词模板
// 模板中 %s 会被替换为用户查询
func WithHyDEPrompt(prompt string) HyDEOption {
	return func(r *HyDERetriever) {
		r.promptTemplate = prompt
	}
}

// WithHyDENumHypothetical 设置生成的假设文档数量
func WithHyDENumHypothetical(n int) HyDEOption {
	return func(r *HyDERetriever) {
		if n > 0 {
			r.numHypothetical = n
		}
	}
}

// WithHyDETopK 设置返回文档数量
func WithHyDETopK(k int) HyDEOption {
	return func(r *HyDERetriever) {
		if k > 0 {
			r.topK = k
		}
	}
}

// WithHyDEMergeStrategy 设置向量合并策略
func WithHyDEMergeStrategy(strategy VectorMergeStrategy) HyDEOption {
	return func(r *HyDERetriever) {
		r.mergeStrategy = strategy
	}
}

// WithHyDETemperature 设置 LLM 采样温度
// 较高温度（0.7-1.0）生成更多样化的假设文档
func WithHyDETemperature(temp float64) HyDEOption {
	return func(r *HyDERetriever) {
		r.temperature = temp
	}
}

// defaultHyDEPrompt 默认假设文档生成提示词
const defaultHyDEPrompt = `请根据以下问题，写出一段可能包含答案的文档内容。
不要解释，直接输出文档内容。

问题：%s

文档内容：`

// NewHyDERetriever 创建 HyDE 检索器
//
// 参数：
//   - llmProvider: LLM 提供者，用于生成假设文档
//   - embedder: 向量生成器，将假设文档转为向量
//   - store: 向量存储，用于检索真实文档
//   - opts: 可选配置
func NewHyDERetriever(llmProvider llm.Provider, embedder vector.Embedder, store vector.Store, opts ...HyDEOption) *HyDERetriever {
	r := &HyDERetriever{
		llmProvider:     llmProvider,
		embedder:        embedder,
		store:           store,
		promptTemplate:  defaultHyDEPrompt,
		numHypothetical: 1,
		topK:            5,
		mergeStrategy:   MergeAverage,
		temperature:     0.7,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Retrieve 执行 HyDE 检索
// 流程：查询 → LLM 生成假设文档 → 向量化 → 检索 → 去重排序
func (r *HyDERetriever) Retrieve(ctx context.Context, query string, opts ...rag.RetrieveOption) ([]rag.Document, error) {
	cfg := &rag.RetrieveConfig{
		TopK: r.topK,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// 1. 生成假设文档
	hypotheticalDocs, err := r.generateHypothetical(ctx, query)
	if err != nil {
		// 降级：LLM 调用失败时，直接用原始查询向量检索
		return r.fallbackRetrieve(ctx, query, cfg)
	}

	// 2. 根据合并策略检索
	switch r.mergeStrategy {
	case MergeSearchAll:
		return r.retrieveWithSearchAll(ctx, hypotheticalDocs, cfg)
	default: // MergeAverage
		return r.retrieveWithAverageVector(ctx, hypotheticalDocs, cfg)
	}
}

// generateHypothetical 使用 LLM 生成假设文档
func (r *HyDERetriever) generateHypothetical(ctx context.Context, query string) ([]string, error) {
	prompt := fmt.Sprintf(r.promptTemplate, query)

	var docs []string
	for i := 0; i < r.numHypothetical; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		req := llm.CompletionRequest{
			Model: r.model,
			Messages: []llm.Message{
				{Role: llm.RoleUser, Content: prompt},
			},
			MaxTokens: 500,
		}
		if r.temperature > 0 {
			temp := r.temperature
			req.Temperature = &temp
		}

		resp, err := r.llmProvider.Complete(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("生成假设文档失败: %w", err)
		}

		content := strings.TrimSpace(resp.Content)
		if content != "" {
			docs = append(docs, content)
		}
	}

	if len(docs) == 0 {
		return nil, fmt.Errorf("未生成有效的假设文档")
	}

	return docs, nil
}

// retrieveWithAverageVector 使用平均向量检索
// 将所有假设文档的向量取平均值，然后用平均向量检索
func (r *HyDERetriever) retrieveWithAverageVector(ctx context.Context, hypotheticalDocs []string, cfg *rag.RetrieveConfig) ([]rag.Document, error) {
	// 向量化所有假设文档
	var allEmbeddings [][]float32
	for _, doc := range hypotheticalDocs {
		embedding, err := r.embedder.EmbedOne(ctx, doc)
		if err != nil {
			continue // 跳过失败的
		}
		allEmbeddings = append(allEmbeddings, embedding)
	}

	if len(allEmbeddings) == 0 {
		return nil, fmt.Errorf("所有假设文档向量化失败")
	}

	// 计算平均向量
	avgEmbedding := averageVectors(allEmbeddings)

	// 检索
	searchOpts := []vector.SearchOption{
		vector.WithMetadata(true),
	}
	if cfg.MinScore > 0 {
		searchOpts = append(searchOpts, vector.WithMinScore(cfg.MinScore))
	}
	if cfg.Filter != nil {
		searchOpts = append(searchOpts, vector.WithFilter(cfg.Filter))
	}

	vectorDocs, err := r.store.Search(ctx, avgEmbedding, cfg.TopK, searchOpts...)
	if err != nil {
		return nil, fmt.Errorf("向量检索失败: %w", err)
	}

	return convertVectorDocs(vectorDocs), nil
}

// retrieveWithSearchAll 分别检索并聚合
// 用每个假设文档向量分别检索，然后去重聚合
func (r *HyDERetriever) retrieveWithSearchAll(ctx context.Context, hypotheticalDocs []string, cfg *rag.RetrieveConfig) ([]rag.Document, error) {
	seen := make(map[string]struct{})
	var allDocs []rag.Document

	searchOpts := []vector.SearchOption{
		vector.WithMetadata(true),
	}
	if cfg.MinScore > 0 {
		searchOpts = append(searchOpts, vector.WithMinScore(cfg.MinScore))
	}
	if cfg.Filter != nil {
		searchOpts = append(searchOpts, vector.WithFilter(cfg.Filter))
	}

	for _, doc := range hypotheticalDocs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		embedding, err := r.embedder.EmbedOne(ctx, doc)
		if err != nil {
			continue
		}

		vectorDocs, err := r.store.Search(ctx, embedding, cfg.TopK, searchOpts...)
		if err != nil {
			continue
		}

		for _, vd := range vectorDocs {
			if _, exists := seen[vd.ID]; !exists {
				seen[vd.ID] = struct{}{}
				allDocs = append(allDocs, rag.Document{
					ID:        vd.ID,
					Content:   vd.Content,
					Metadata:  vd.Metadata,
					Embedding: vd.Embedding,
					Score:     vd.Score,
					CreatedAt: vd.CreatedAt,
				})
			}
		}
	}

	// 按分数排序，取 TopK
	if len(allDocs) > cfg.TopK {
		// 按分数降序排序
		for i := 0; i < len(allDocs)-1; i++ {
			for j := i + 1; j < len(allDocs); j++ {
				if allDocs[j].Score > allDocs[i].Score {
					allDocs[i], allDocs[j] = allDocs[j], allDocs[i]
				}
			}
		}
		allDocs = allDocs[:cfg.TopK]
	}

	return allDocs, nil
}

// fallbackRetrieve 降级检索：直接用原始查询向量检索
func (r *HyDERetriever) fallbackRetrieve(ctx context.Context, query string, cfg *rag.RetrieveConfig) ([]rag.Document, error) {
	embedding, err := r.embedder.EmbedOne(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("降级检索失败（查询向量化失败）: %w", err)
	}

	searchOpts := []vector.SearchOption{
		vector.WithMetadata(true),
	}
	if cfg.MinScore > 0 {
		searchOpts = append(searchOpts, vector.WithMinScore(cfg.MinScore))
	}
	if cfg.Filter != nil {
		searchOpts = append(searchOpts, vector.WithFilter(cfg.Filter))
	}

	vectorDocs, err := r.store.Search(ctx, embedding, cfg.TopK, searchOpts...)
	if err != nil {
		return nil, fmt.Errorf("降级检索失败: %w", err)
	}

	return convertVectorDocs(vectorDocs), nil
}

// averageVectors 计算多个向量的平均值
func averageVectors(vectors [][]float32) []float32 {
	if len(vectors) == 0 {
		return nil
	}
	dim := len(vectors[0])
	avg := make([]float32, dim)
	for _, v := range vectors {
		for i := 0; i < dim && i < len(v); i++ {
			avg[i] += v[i]
		}
	}
	n := float32(len(vectors))
	for i := range avg {
		avg[i] /= n
	}
	return avg
}

// convertVectorDocs 将 vector.Document 转为 rag.Document
func convertVectorDocs(vectorDocs []vector.Document) []rag.Document {
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
	return docs
}

// 确保 HyDERetriever 实现 rag.Retriever 接口
var _ rag.Retriever = (*HyDERetriever)(nil)
