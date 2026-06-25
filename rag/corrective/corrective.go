// Package corrective 提供纠错检索增强生成 (Corrective RAG) 实现
//
// Corrective RAG 是一种通过评估和纠错来提高检索质量的策略：
//   - 评估主检索器的结果质量
//   - 当质量不佳时触发备选检索器（如网络搜索）
//   - 融合多个来源的结果
//
// 参考论文: Corrective Retrieval Augmented Generation
//
// 使用示例：
//
//	crag := corrective.New(
//	    primaryRetriever,
//	    corrective.WithFallbackRetriever(webSearchRetriever),
//	    corrective.WithRelevanceThreshold(0.5),
//	)
//	docs, err := crag.Retrieve(ctx, "用户问题")
package corrective

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/toolkit/lang/stringx"
)

// CorrectiveRAG 纠错检索增强生成
//
// 工作流程：
//  1. 使用主检索器检索
//  2. 评估每个结果: Correct / Ambiguous / Incorrect
//  3. 质量不佳时触发备选检索器
//  4. 融合结果
type CorrectiveRAG struct {
	// primaryRetriever 主检索器
	primaryRetriever rag.Retriever

	// fallbackRetriever 备选检索器（如网络搜索）
	fallbackRetriever rag.Retriever

	// evaluator 相关性评估器
	evaluator RelevanceEvaluator

	// llm LLM 提供者（用于评估和优化查询）
	llm llm.Provider

	// relevanceThreshold 相关性阈值
	// 低于此阈值的结果被认为是 Incorrect
	relevanceThreshold float32

	// ambiguousThreshold 模糊阈值
	// 介于 relevanceThreshold 和 ambiguousThreshold 之间的结果是 Ambiguous
	ambiguousThreshold float32

	// topK 检索文档数量
	topK int

	// queryRewriter 查询重写器（用于优化查询）
	queryRewriter QueryRewriter
}

// RelevanceEvaluator 相关性评估器接口
type RelevanceEvaluator interface {
	// Evaluate 评估文档与查询的相关性
	// 返回评估结果和分数
	Evaluate(ctx context.Context, query string, doc rag.Document) (EvaluationResult, float32, error)
}

// EvaluationResult 评估结果类型
type EvaluationResult string

const (
	// ResultCorrect 正确/相关
	ResultCorrect EvaluationResult = "correct"
	// ResultAmbiguous 模糊/部分相关
	ResultAmbiguous EvaluationResult = "ambiguous"
	// ResultIncorrect 错误/不相关
	ResultIncorrect EvaluationResult = "incorrect"
)

// QueryRewriter 查询重写器接口
type QueryRewriter interface {
	// Rewrite 重写/优化查询
	Rewrite(ctx context.Context, query string) (string, error)
}

// EvaluatedDocument 带评估结果的文档
type EvaluatedDocument struct {
	rag.Document
	Evaluation EvaluationResult `json:"evaluation"`
}

// Option CorrectiveRAG 配置选项
type Option func(*CorrectiveRAG)

// WithFallbackRetriever 设置备选检索器
func WithFallbackRetriever(retriever rag.Retriever) Option {
	return func(c *CorrectiveRAG) {
		c.fallbackRetriever = retriever
	}
}

// WithEvaluator 设置相关性评估器
func WithEvaluator(evaluator RelevanceEvaluator) Option {
	return func(c *CorrectiveRAG) {
		c.evaluator = evaluator
	}
}

// WithLLM 设置 LLM 提供者
func WithLLM(provider llm.Provider) Option {
	return func(c *CorrectiveRAG) {
		c.llm = provider
	}
}

// WithRelevanceThreshold 设置相关性阈值
// 默认值: 0.5
func WithRelevanceThreshold(threshold float32) Option {
	return func(c *CorrectiveRAG) {
		if threshold > 0 && threshold <= 1.0 {
			c.relevanceThreshold = threshold
		}
	}
}

// WithAmbiguousThreshold 设置模糊阈值
// 默认值: 0.7
func WithAmbiguousThreshold(threshold float32) Option {
	return func(c *CorrectiveRAG) {
		if threshold > 0 && threshold <= 1.0 {
			c.ambiguousThreshold = threshold
		}
	}
}

// WithTopK 设置检索文档数量
// 默认值: 5
func WithTopK(k int) Option {
	return func(c *CorrectiveRAG) {
		if k > 0 {
			c.topK = k
		}
	}
}

// WithQueryRewriter 设置查询重写器
func WithQueryRewriter(rewriter QueryRewriter) Option {
	return func(c *CorrectiveRAG) {
		c.queryRewriter = rewriter
	}
}

// New 创建 CorrectiveRAG 实例
func New(primaryRetriever rag.Retriever, opts ...Option) *CorrectiveRAG {
	c := &CorrectiveRAG{
		primaryRetriever:   primaryRetriever,
		relevanceThreshold: 0.5,
		ambiguousThreshold: 0.7,
		topK:               5,
	}

	for _, opt := range opts {
		opt(c)
	}

	// 如果配置了 LLM 但没有评估器，创建 LLM 评估器
	if c.evaluator == nil && c.llm != nil {
		c.evaluator = NewLLMEvaluator(c.llm)
	}

	// 如果配置了 LLM 但没有查询重写器，创建 LLM 重写器
	if c.queryRewriter == nil && c.llm != nil {
		c.queryRewriter = NewLLMQueryRewriter(c.llm)
	}

	return c
}

// Retrieve 执行纠错检索
func (c *CorrectiveRAG) Retrieve(ctx context.Context, query string, opts ...rag.RetrieveOption) ([]rag.Document, error) {
	// 1. 使用主检索器检索
	docs, err := c.primaryRetriever.Retrieve(ctx, query, rag.WithTopK(c.topK))
	if err != nil {
		return nil, fmt.Errorf("主检索失败: %w", err)
	}

	if len(docs) == 0 {
		// 没有结果，尝试备选检索
		return c.fallbackRetrieve(ctx, query)
	}

	// 2. 评估每个文档
	evaluated, err := c.evaluateDocuments(ctx, query, docs)
	if err != nil {
		// 评估失败时直接返回原结果
		return docs, nil
	}

	// 3. 统计评估结果
	var correct, ambiguous, incorrect []rag.Document
	for _, ed := range evaluated {
		switch ed.Evaluation {
		case ResultCorrect:
			correct = append(correct, ed.Document)
		case ResultAmbiguous:
			ambiguous = append(ambiguous, ed.Document)
		case ResultIncorrect:
			incorrect = append(incorrect, ed.Document)
		}
	}

	// 4. 根据评估结果决定策略
	return c.decideStrategy(ctx, query, correct, ambiguous, incorrect)
}

// evaluateDocuments 评估所有文档
func (c *CorrectiveRAG) evaluateDocuments(ctx context.Context, query string, docs []rag.Document) ([]EvaluatedDocument, error) {
	if c.evaluator == nil {
		// 没有评估器，使用分数阈值判断
		return c.evaluateByScore(docs), nil
	}

	evaluated := make([]EvaluatedDocument, 0, len(docs))
	for _, doc := range docs {
		result, score, err := c.evaluator.Evaluate(ctx, query, doc)
		if err != nil {
			// 评估失败时根据分数判断
			result = c.evaluateResultByScore(doc.Score)
		} else {
			doc.Score = score
		}

		evaluated = append(evaluated, EvaluatedDocument{
			Document:   doc,
			Evaluation: result,
		})
	}

	return evaluated, nil
}

// evaluateByScore 根据分数评估文档
func (c *CorrectiveRAG) evaluateByScore(docs []rag.Document) []EvaluatedDocument {
	evaluated := make([]EvaluatedDocument, len(docs))
	for i, doc := range docs {
		evaluated[i] = EvaluatedDocument{
			Document:   doc,
			Evaluation: c.evaluateResultByScore(doc.Score),
		}
	}
	return evaluated
}

// evaluateResultByScore 根据分数返回评估结果
func (c *CorrectiveRAG) evaluateResultByScore(score float32) EvaluationResult {
	if score >= c.ambiguousThreshold {
		return ResultCorrect
	}
	if score >= c.relevanceThreshold {
		return ResultAmbiguous
	}
	return ResultIncorrect
}

// decideStrategy 根据评估结果决定策略
func (c *CorrectiveRAG) decideStrategy(ctx context.Context, query string, correct, ambiguous, incorrect []rag.Document) ([]rag.Document, error) {
	totalCorrect := len(correct)
	totalAmbiguous := len(ambiguous)
	total := totalCorrect + totalAmbiguous + len(incorrect)

	// 计算正确率
	correctRate := float32(totalCorrect) / float32(total)
	usableRate := float32(totalCorrect+totalAmbiguous) / float32(total)

	// 策略决定
	if correctRate >= 0.5 {
		// 大部分正确，直接返回正确和模糊的结果
		return append(correct, ambiguous...), nil
	}

	if usableRate >= 0.3 {
		// 有一些可用结果，结合备选检索
		fallbackDocs, err := c.fallbackRetrieve(ctx, query)
		if err == nil && len(fallbackDocs) > 0 {
			// 融合结果
			return c.mergeResults(correct, ambiguous, fallbackDocs), nil
		}
		// 备选失败，返回现有可用结果
		return append(correct, ambiguous...), nil
	}

	// 大部分不相关，完全依赖备选检索
	fallbackDocs, err := c.fallbackRetrieve(ctx, query)
	if err != nil || len(fallbackDocs) == 0 {
		// 备选也失败，返回最好的结果
		if len(correct) > 0 {
			return correct, nil
		}
		return ambiguous, nil
	}

	return fallbackDocs, nil
}

// fallbackRetrieve 使用备选检索器
func (c *CorrectiveRAG) fallbackRetrieve(ctx context.Context, query string) ([]rag.Document, error) {
	if c.fallbackRetriever == nil {
		return nil, fmt.Errorf("未配置备选检索器")
	}

	// 可选：重写查询以优化备选检索
	optimizedQuery := query
	if c.queryRewriter != nil {
		if rewritten, err := c.queryRewriter.Rewrite(ctx, query); err == nil {
			optimizedQuery = rewritten
		}
	}

	return c.fallbackRetriever.Retrieve(ctx, optimizedQuery, rag.WithTopK(c.topK))
}

// mergeResults 融合多个来源的结果
func (c *CorrectiveRAG) mergeResults(correct, ambiguous, fallback []rag.Document) []rag.Document {
	// 使用 map 去重
	seen := make(map[string]bool)
	var results []rag.Document

	// 优先添加正确的结果
	for _, doc := range correct {
		if !seen[doc.ID] {
			seen[doc.ID] = true
			if doc.Metadata == nil {
				doc.Metadata = make(map[string]any)
			}
			doc.Metadata["source_type"] = "primary_correct"
			results = append(results, doc)
		}
	}

	// 添加备选结果
	for _, doc := range fallback {
		if !seen[doc.ID] {
			seen[doc.ID] = true
			if doc.Metadata == nil {
				doc.Metadata = make(map[string]any)
			}
			doc.Metadata["source_type"] = "fallback"
			results = append(results, doc)
		}
	}

	// 最后添加模糊结果
	for _, doc := range ambiguous {
		if !seen[doc.ID] {
			seen[doc.ID] = true
			if doc.Metadata == nil {
				doc.Metadata = make(map[string]any)
			}
			doc.Metadata["source_type"] = "primary_ambiguous"
			results = append(results, doc)
		}
	}

	// 限制返回数量
	if len(results) > c.topK {
		results = results[:c.topK]
	}

	return results
}

// ============== LLM Evaluator ==============

// LLMEvaluator 基于 LLM 的相关性评估器
type LLMEvaluator struct {
	llm llm.Provider
}

// NewLLMEvaluator 创建 LLM 评估器
func NewLLMEvaluator(provider llm.Provider) *LLMEvaluator {
	return &LLMEvaluator{llm: provider}
}

// Evaluate 评估文档相关性
func (e *LLMEvaluator) Evaluate(ctx context.Context, query string, doc rag.Document) (EvaluationResult, float32, error) {
	prompt := fmt.Sprintf(`评估以下文档与查询的相关性。

查询: %s

文档:
%s

请评估这个文档是否能帮助回答查询：
- "correct": 文档直接相关，包含回答查询所需的关键信息
- "ambiguous": 文档部分相关，可能有帮助但不够直接
- "incorrect": 文档不相关，无法帮助回答查询

返回 JSON 格式:
{"evaluation": "correct/ambiguous/incorrect", "score": 0.0-1.0, "reason": "原因"}

只返回 JSON:`, query, truncateText(doc.Content, 1000))

	resp, err := e.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return ResultAmbiguous, 0.5, err
	}

	var result struct {
		Evaluation string  `json:"evaluation"`
		Score      float32 `json:"score"`
	}

	jsonContent := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return ResultAmbiguous, 0.5, nil
	}

	evaluation := EvaluationResult(result.Evaluation)
	if evaluation != ResultCorrect && evaluation != ResultAmbiguous && evaluation != ResultIncorrect {
		evaluation = ResultAmbiguous
	}

	return evaluation, result.Score, nil
}

// ============== LLM Query Rewriter ==============

// LLMQueryRewriter 基于 LLM 的查询重写器
type LLMQueryRewriter struct {
	llm llm.Provider
}

// NewLLMQueryRewriter 创建 LLM 查询重写器
func NewLLMQueryRewriter(provider llm.Provider) *LLMQueryRewriter {
	return &LLMQueryRewriter{llm: provider}
}

// Rewrite 重写查询
func (r *LLMQueryRewriter) Rewrite(ctx context.Context, query string) (string, error) {
	prompt := fmt.Sprintf(`请优化以下搜索查询，使其更适合网络搜索。

原始查询: %s

要求:
1. 提取关键词
2. 移除口语化表达
3. 添加必要的上下文

返回优化后的查询（只返回查询文本，不要其他内容）:`, query)

	resp, err := r.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return query, err
	}

	rewritten := strings.TrimSpace(resp.Content)
	if rewritten == "" {
		return query, nil
	}

	return rewritten, nil
}

// 辅助函数

// extractJSON 从文本中提取 JSON
func extractJSON(content string) string {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start == -1 || end == -1 || start >= end {
		return "{}"
	}
	return content[start : end+1]
}

// truncateText 截断文本
func truncateText(text string, maxLen int) string {
	// rune-safe 截断（委托 toolkit stringx.SubString），避免 CJK 字节切断产生乱码（BUG-20260625 F-4）。
	head := stringx.SubString(text, 0, maxLen)
	if head == text {
		return text
	}
	return head + "..."
}

// 确保实现了接口
var _ rag.Retriever = (*CorrectiveRAG)(nil)
var _ RelevanceEvaluator = (*LLMEvaluator)(nil)
var _ QueryRewriter = (*LLMQueryRewriter)(nil)
