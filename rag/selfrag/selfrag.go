// Package selfrag 提供自我反思检索增强生成 (Self-RAG) 实现
//
// Self-RAG 是一种先进的 RAG 策略，通过自我反思机制来提高生成质量：
//   - 判断是否需要检索 (Retrieval Decision)
//   - 评估检索结果的相关性 (Relevance Assessment)
//   - 验证生成内容的忠实度 (Faithfulness Verification)
//   - 检查回答的完整性 (Completeness Check)
//
// 参考论文: Self-RAG: Learning to Retrieve, Generate, and Critique through Self-Reflection
//
// 使用示例：
//
//	selfRAG := selfrag.New(
//	    retriever,
//	    llmProvider,
//	    selfrag.WithMaxRetries(3),
//	    selfrag.WithRelevanceThreshold(0.7),
//	)
//	response, err := selfRAG.Query(ctx, "用户问题")
package selfrag

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/toolkit/lang/stringx"
)

// SelfRAG 自我反思检索增强生成
//
// 工作流程：
//  1. 接收用户查询
//  2. 使用 Critic 判断是否需要检索
//  3. 如需检索，执行检索并评估相关性
//  4. 过滤低相关性文档
//  5. 生成回答
//  6. 验证回答的忠实度
//  7. 检查回答的完整性
//  8. 如不满足要求，重试或返回最佳结果
type SelfRAG struct {
	// retriever 底层检索器
	retriever rag.Retriever

	// llm LLM 提供者
	llm llm.Provider

	// critic 批评器（用于各种评估）
	critic Critic

	// maxRetries 最大重试次数
	maxRetries int

	// relevanceThreshold 相关性阈值
	relevanceThreshold float32

	// faithfulnessThreshold 忠实度阈值
	faithfulnessThreshold float32

	// topK 检索文档数量
	topK int
}

// Critic 批评器接口
// 负责评估检索和生成的各个方面
type Critic interface {
	// NeedsRetrieval 判断是否需要检索
	// 返回是否需要检索以及置信度
	NeedsRetrieval(ctx context.Context, query string) (bool, float32, error)

	// IsRelevant 评估文档与查询的相关性
	// 返回是否相关以及相关性分数
	IsRelevant(ctx context.Context, query string, doc rag.Document) (bool, float32, error)

	// IsFaithful 验证回答是否忠实于来源文档
	// 返回是否忠实以及忠实度分数
	IsFaithful(ctx context.Context, response string, sources []rag.Document) (bool, float32, error)

	// IsComplete 检查回答是否完整回答了问题
	// 返回是否完整以及完整度分数
	IsComplete(ctx context.Context, query string, response string) (bool, float32, error)
}

// Response Self-RAG 的响应
type Response struct {
	// Content 生成的回答
	Content string `json:"content"`

	// Sources 使用的来源文档
	Sources []rag.Document `json:"sources,omitempty"`

	// NeedRetrieval 是否需要检索
	NeedRetrieval bool `json:"need_retrieval"`

	// RelevanceScores 各文档的相关性分数
	RelevanceScores map[string]float32 `json:"relevance_scores,omitempty"`

	// FaithfulnessScore 忠实度分数
	FaithfulnessScore float32 `json:"faithfulness_score"`

	// CompletenessScore 完整度分数
	CompletenessScore float32 `json:"completeness_score"`

	// Retries 重试次数
	Retries int `json:"retries"`
}

// Option SelfRAG 配置选项
type Option func(*SelfRAG)

// WithCritic 设置批评器
func WithCritic(critic Critic) Option {
	return func(s *SelfRAG) {
		s.critic = critic
	}
}

// WithMaxRetries 设置最大重试次数
// 默认值: 3
func WithMaxRetries(n int) Option {
	return func(s *SelfRAG) {
		if n > 0 {
			s.maxRetries = n
		}
	}
}

// WithRelevanceThreshold 设置相关性阈值
// 默认值: 0.7
func WithRelevanceThreshold(threshold float32) Option {
	return func(s *SelfRAG) {
		if threshold > 0 && threshold <= 1.0 {
			s.relevanceThreshold = threshold
		}
	}
}

// WithFaithfulnessThreshold 设置忠实度阈值
// 默认值: 0.7
func WithFaithfulnessThreshold(threshold float32) Option {
	return func(s *SelfRAG) {
		if threshold > 0 && threshold <= 1.0 {
			s.faithfulnessThreshold = threshold
		}
	}
}

// WithTopK 设置检索文档数量
// 默认值: 5
func WithTopK(k int) Option {
	return func(s *SelfRAG) {
		if k > 0 {
			s.topK = k
		}
	}
}

// New 创建 SelfRAG 实例
func New(retriever rag.Retriever, provider llm.Provider, opts ...Option) *SelfRAG {
	s := &SelfRAG{
		retriever:             retriever,
		llm:                   provider,
		maxRetries:            3,
		relevanceThreshold:    0.7,
		faithfulnessThreshold: 0.7,
		topK:                  5,
	}

	for _, opt := range opts {
		opt(s)
	}

	// 如果没有设置 Critic，使用 LLM 批评器
	if s.critic == nil {
		s.critic = NewLLMCritic(provider)
	}

	return s
}

// Query 执行 Self-RAG 查询
func (s *SelfRAG) Query(ctx context.Context, query string) (*Response, error) {
	var bestResponse *Response
	var bestScore float32

	for retry := 0; retry <= s.maxRetries; retry++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		response, err := s.executeQuery(ctx, query)
		if err != nil {
			continue
		}
		response.Retries = retry

		// 计算综合评分
		score := (response.FaithfulnessScore + response.CompletenessScore) / 2

		// 更新最佳响应
		if bestResponse == nil || score > bestScore {
			bestResponse = response
			bestScore = score
		}

		// 如果达到阈值，提前返回
		if response.FaithfulnessScore >= s.faithfulnessThreshold &&
			response.CompletenessScore >= s.faithfulnessThreshold {
			return response, nil
		}
	}

	if bestResponse == nil {
		return nil, fmt.Errorf("未能生成满意的回答")
	}

	return bestResponse, nil
}

// executeQuery 执行单次查询
func (s *SelfRAG) executeQuery(ctx context.Context, query string) (*Response, error) {
	response := &Response{
		RelevanceScores: make(map[string]float32),
	}

	// 1. 判断是否需要检索
	needRetrieval, _, err := s.critic.NeedsRetrieval(ctx, query)
	if err != nil {
		// 出错时默认进行检索
		needRetrieval = true
	}
	response.NeedRetrieval = needRetrieval

	var relevantDocs []rag.Document

	if needRetrieval {
		// 2. 执行检索
		docs, err := s.retriever.Retrieve(ctx, query, rag.WithTopK(s.topK))
		if err != nil {
			return nil, fmt.Errorf("检索失败: %w", err)
		}

		// 3. 评估相关性并过滤
		for _, doc := range docs {
			isRelevant, score, err := s.critic.IsRelevant(ctx, query, doc)
			if err != nil {
				continue
			}
			response.RelevanceScores[doc.ID] = score

			if isRelevant && score >= s.relevanceThreshold {
				doc.Score = score
				relevantDocs = append(relevantDocs, doc)
			}
		}
	}

	response.Sources = relevantDocs

	// 4. 生成回答
	content, err := s.generateResponse(ctx, query, relevantDocs)
	if err != nil {
		return nil, fmt.Errorf("生成回答失败: %w", err)
	}
	response.Content = content

	// 5. 验证忠实度
	if len(relevantDocs) > 0 {
		_, faithfulnessScore, err := s.critic.IsFaithful(ctx, content, relevantDocs)
		if err == nil {
			response.FaithfulnessScore = faithfulnessScore
		}
	} else {
		// 没有来源时忠实度设为 1
		response.FaithfulnessScore = 1.0
	}

	// 6. 检查完整性
	_, completenessScore, err := s.critic.IsComplete(ctx, query, content)
	if err == nil {
		response.CompletenessScore = completenessScore
	}

	return response, nil
}

// generateResponse 生成回答
func (s *SelfRAG) generateResponse(ctx context.Context, query string, sources []rag.Document) (string, error) {
	var prompt string

	if len(sources) > 0 {
		// 构建上下文
		var contextBuilder strings.Builder
		for i, doc := range sources {
			contextBuilder.WriteString(fmt.Sprintf("[来源 %d]\n%s\n\n", i+1, doc.Content))
		}

		prompt = fmt.Sprintf(`基于以下参考资料回答问题。请确保回答准确、完整，并忠实于提供的资料。

参考资料:
%s

问题: %s

请用中文回答:`, contextBuilder.String(), query)
	} else {
		prompt = fmt.Sprintf(`请回答以下问题。如果你不确定，请说明。

问题: %s

请用中文回答:`, query)
	}

	resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return "", err
	}

	return resp.Content, nil
}

// ============== LLM Critic ==============

// LLMCritic 基于 LLM 的批评器
type LLMCritic struct {
	llm llm.Provider
}

// NewLLMCritic 创建 LLM 批评器
func NewLLMCritic(provider llm.Provider) *LLMCritic {
	return &LLMCritic{llm: provider}
}

// NeedsRetrieval 判断是否需要检索
func (c *LLMCritic) NeedsRetrieval(ctx context.Context, query string) (bool, float32, error) {
	prompt := fmt.Sprintf(`分析以下问题，判断是否需要从外部知识库检索信息来回答。

问题: %s

判断标准:
- 如果问题涉及特定事实、数据、或专业知识，需要检索
- 如果是一般性问题、闲聊或简单的逻辑推理，不需要检索

返回 JSON 格式:
{"need_retrieval": true/false, "confidence": 0.0-1.0, "reason": "原因"}

只返回 JSON:`, query)

	resp, err := c.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return true, 0.5, err
	}

	// 解析响应
	var result struct {
		NeedRetrieval bool    `json:"need_retrieval"`
		Confidence    float32 `json:"confidence"`
	}

	jsonContent := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return true, 0.5, nil // 默认需要检索
	}

	return result.NeedRetrieval, result.Confidence, nil
}

// IsRelevant 评估文档相关性
func (c *LLMCritic) IsRelevant(ctx context.Context, query string, doc rag.Document) (bool, float32, error) {
	prompt := fmt.Sprintf(`评估以下文档与问题的相关性。

问题: %s

文档内容:
%s

评估标准:
- 文档是否包含回答问题所需的信息
- 信息的相关程度

返回 JSON 格式:
{"is_relevant": true/false, "score": 0.0-1.0, "reason": "原因"}

只返回 JSON:`, query, truncateText(doc.Content, 1000))

	resp, err := c.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return false, 0, err
	}

	var result struct {
		IsRelevant bool    `json:"is_relevant"`
		Score      float32 `json:"score"`
	}

	jsonContent := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return false, 0, nil
	}

	return result.IsRelevant, result.Score, nil
}

// IsFaithful 验证回答忠实度
func (c *LLMCritic) IsFaithful(ctx context.Context, response string, sources []rag.Document) (bool, float32, error) {
	// 构建来源摘要
	var sourcesBuilder strings.Builder
	for i, doc := range sources {
		sourcesBuilder.WriteString(fmt.Sprintf("[来源 %d]: %s\n", i+1, truncateText(doc.Content, 500)))
	}

	prompt := fmt.Sprintf(`评估以下回答是否忠实于提供的来源文档。

来源文档:
%s

回答:
%s

评估标准:
- 回答中的每个声明是否都能在来源中找到依据
- 是否有编造或歪曲信息

返回 JSON 格式:
{"is_faithful": true/false, "score": 0.0-1.0, "issues": ["问题1", "问题2"]}

只返回 JSON:`, sourcesBuilder.String(), response)

	resp, err := c.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return false, 0, err
	}

	var result struct {
		IsFaithful bool    `json:"is_faithful"`
		Score      float32 `json:"score"`
	}

	jsonContent := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return false, 0, nil
	}

	return result.IsFaithful, result.Score, nil
}

// IsComplete 检查回答完整性
func (c *LLMCritic) IsComplete(ctx context.Context, query string, response string) (bool, float32, error) {
	prompt := fmt.Sprintf(`评估以下回答是否完整地回答了问题。

问题: %s

回答: %s

评估标准:
- 是否回答了问题的所有方面
- 是否缺少关键信息

返回 JSON 格式:
{"is_complete": true/false, "score": 0.0-1.0, "missing": ["缺失1", "缺失2"]}

只返回 JSON:`, query, response)

	resp, err := c.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return false, 0, err
	}

	var result struct {
		IsComplete bool    `json:"is_complete"`
		Score      float32 `json:"score"`
	}

	jsonContent := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return false, 0, nil
	}

	return result.IsComplete, result.Score, nil
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

// 确保实现了 Critic 接口
var _ Critic = (*LLMCritic)(nil)
