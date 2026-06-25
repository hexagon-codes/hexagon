// Package agentic 提供代理式检索增强生成 (Agentic RAG) 实现
//
// Agentic RAG 通过智能代理进行多步推理和检索：
//   - 分析查询，生成检索计划
//   - 执行多步检索和推理
//   - 动态调整计划
//   - 支持多个检索源
//
// 使用示例：
//
//	arag := agentic.New(
//	    llmProvider,
//	    agentic.WithRetriever("knowledge", knowledgeRetriever),
//	    agentic.WithRetriever("web", webRetriever),
//	    agentic.WithMaxSteps(5),
//	)
//	response, err := arag.Query(ctx, "复杂问题")
package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/toolkit/lang/stringx"
)

// AgenticRAG 代理式检索增强生成
//
// 工作流程：
//  1. 分析查询，生成检索计划
//  2. 执行计划中的每个步骤
//  3. 根据步骤结果动态调整
//  4. 综合所有结果生成回答
type AgenticRAG struct {
	// retrievers 多个检索源
	retrievers map[string]rag.Retriever

	// llm LLM 提供者
	llm llm.Provider

	// maxSteps 最大步骤数
	maxSteps int

	// topK 每次检索的文档数
	topK int
}

// Step 执行步骤
type Step struct {
	// Type 步骤类型
	Type StepType `json:"type"`

	// Query 检索查询（对于检索步骤）
	Query string `json:"query,omitempty"`

	// Source 检索源（对于检索步骤）
	Source string `json:"source,omitempty"`

	// Reasoning 推理内容（对于推理步骤）
	Reasoning string `json:"reasoning,omitempty"`

	// Results 步骤结果
	Results []rag.Document `json:"results,omitempty"`

	// Conclusion 步骤结论
	Conclusion string `json:"conclusion,omitempty"`
}

// StepType 步骤类型
type StepType string

const (
	// StepTypeRetrieve 检索步骤
	StepTypeRetrieve StepType = "retrieve"
	// StepTypeReason 推理步骤
	StepTypeReason StepType = "reason"
	// StepTypeSynthesize 综合步骤
	StepTypeSynthesize StepType = "synthesize"
)

// Plan 执行计划
type Plan struct {
	// Query 原始查询
	Query string `json:"query"`

	// Steps 计划步骤
	Steps []Step `json:"steps"`

	// Analysis 查询分析
	Analysis string `json:"analysis,omitempty"`
}

// Response Agentic RAG 的响应
type Response struct {
	// Content 生成的回答
	Content string `json:"content"`

	// Sources 使用的来源文档
	Sources []rag.Document `json:"sources,omitempty"`

	// ExecutedSteps 执行的步骤
	ExecutedSteps []Step `json:"executed_steps"`

	// TotalSteps 总步骤数
	TotalSteps int `json:"total_steps"`
}

// Option AgenticRAG 配置选项
type Option func(*AgenticRAG)

// WithRetriever 添加检索器
func WithRetriever(name string, retriever rag.Retriever) Option {
	return func(a *AgenticRAG) {
		a.retrievers[name] = retriever
	}
}

// WithMaxSteps 设置最大步骤数
// 默认值: 5
func WithMaxSteps(n int) Option {
	return func(a *AgenticRAG) {
		if n > 0 {
			a.maxSteps = n
		}
	}
}

// WithTopK 设置每次检索的文档数
// 默认值: 3
func WithTopK(k int) Option {
	return func(a *AgenticRAG) {
		if k > 0 {
			a.topK = k
		}
	}
}

// New 创建 AgenticRAG 实例
func New(provider llm.Provider, opts ...Option) *AgenticRAG {
	a := &AgenticRAG{
		retrievers: make(map[string]rag.Retriever),
		llm:        provider,
		maxSteps:   5,
		topK:       3,
	}

	for _, opt := range opts {
		opt(a)
	}

	return a
}

// Query 执行 Agentic RAG 查询
func (a *AgenticRAG) Query(ctx context.Context, query string) (*Response, error) {
	response := &Response{
		ExecutedSteps: make([]Step, 0),
	}

	// 1. 生成检索计划
	plan, err := a.generatePlan(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("生成计划失败: %w", err)
	}

	// 2. 执行计划
	var allDocs []rag.Document
	stepCount := 0

	for _, step := range plan.Steps {
		if ctx.Err() != nil {
			break
		}
		if stepCount >= a.maxSteps {
			break
		}
		stepCount++

		executedStep, docs, err := a.executeStep(ctx, step, allDocs)
		if err != nil {
			continue
		}

		response.ExecutedSteps = append(response.ExecutedSteps, executedStep)
		allDocs = append(allDocs, docs...)

		// 检查是否需要动态调整计划
		if executedStep.Type == StepTypeReason && needsMoreInfo(executedStep.Conclusion) {
			// 生成额外的检索步骤
			additionalSteps, _ := a.generateAdditionalSteps(ctx, query, executedStep.Conclusion, allDocs)
			plan.Steps = append(plan.Steps, additionalSteps...)
		}
	}

	// 3. 综合生成回答
	content, err := a.synthesize(ctx, query, allDocs, response.ExecutedSteps)
	if err != nil {
		return nil, fmt.Errorf("综合生成失败: %w", err)
	}

	response.Content = content
	response.Sources = deduplicateDocs(allDocs)
	response.TotalSteps = stepCount

	return response, nil
}

// generatePlan 生成检索计划
func (a *AgenticRAG) generatePlan(ctx context.Context, query string) (*Plan, error) {
	// 构建可用检索源描述
	var sourcesDesc strings.Builder
	for name := range a.retrievers {
		sourcesDesc.WriteString(fmt.Sprintf("- %s\n", name))
	}

	prompt := fmt.Sprintf(`分析以下查询，并生成一个检索和推理计划。

查询: %s

可用检索源:
%s

要求:
1. 分解查询为可执行的步骤
2. 每个步骤可以是检索 (retrieve) 或推理 (reason)
3. 检索步骤需指定查询和来源
4. 最多 %d 个步骤

返回 JSON 格式:
{
  "analysis": "查询分析",
  "steps": [
    {"type": "retrieve", "query": "子查询", "source": "来源名"},
    {"type": "reason", "reasoning": "推理任务"}
  ]
}

只返回 JSON:`, query, sourcesDesc.String(), a.maxSteps)

	resp, err := a.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return nil, err
	}

	// 解析计划
	plan, err := a.parsePlan(resp.Content)
	if err != nil {
		// 解析失败时创建默认计划
		return a.createDefaultPlan(query), nil
	}
	plan.Query = query

	return plan, nil
}

// parsePlan 解析计划
func (a *AgenticRAG) parsePlan(content string) (*Plan, error) {
	jsonContent := extractJSON(content)
	if jsonContent == "" {
		return nil, fmt.Errorf("无法提取 JSON")
	}

	var plan Plan
	if err := json.Unmarshal([]byte(jsonContent), &plan); err != nil {
		return nil, err
	}

	return &plan, nil
}

// createDefaultPlan 创建默认计划
func (a *AgenticRAG) createDefaultPlan(query string) *Plan {
	steps := make([]Step, 0)

	// 为每个检索源创建一个检索步骤
	for source := range a.retrievers {
		steps = append(steps, Step{
			Type:   StepTypeRetrieve,
			Query:  query,
			Source: source,
		})
	}

	// 添加推理步骤
	steps = append(steps, Step{
		Type:      StepTypeReason,
		Reasoning: "根据检索结果进行推理",
	})

	return &Plan{
		Query:    query,
		Steps:    steps,
		Analysis: "默认计划：检索所有来源并推理",
	}
}

// executeStep 执行单个步骤
func (a *AgenticRAG) executeStep(ctx context.Context, step Step, previousDocs []rag.Document) (Step, []rag.Document, error) {
	executedStep := step

	switch step.Type {
	case StepTypeRetrieve:
		docs, err := a.executeRetrieveStep(ctx, step)
		if err != nil {
			return executedStep, nil, err
		}
		executedStep.Results = docs
		return executedStep, docs, nil

	case StepTypeReason:
		conclusion, err := a.executeReasonStep(ctx, step, previousDocs)
		if err != nil {
			return executedStep, nil, err
		}
		executedStep.Conclusion = conclusion
		return executedStep, nil, nil

	default:
		return executedStep, nil, fmt.Errorf("未知步骤类型: %s", step.Type)
	}
}

// executeRetrieveStep 执行检索步骤
func (a *AgenticRAG) executeRetrieveStep(ctx context.Context, step Step) ([]rag.Document, error) {
	retriever, ok := a.retrievers[step.Source]
	if !ok {
		// 如果指定的来源不存在，使用第一个可用的
		for _, r := range a.retrievers {
			retriever = r
			break
		}
	}

	if retriever == nil {
		return nil, fmt.Errorf("没有可用的检索器")
	}

	docs, err := retriever.Retrieve(ctx, step.Query, rag.WithTopK(a.topK))
	if err != nil {
		return nil, err
	}

	// 为每个文档添加来源信息
	for i := range docs {
		if docs[i].Metadata == nil {
			docs[i].Metadata = make(map[string]any)
		}
		docs[i].Metadata["retrieval_source"] = step.Source
		docs[i].Metadata["retrieval_query"] = step.Query
	}

	return docs, nil
}

// executeReasonStep 执行推理步骤
func (a *AgenticRAG) executeReasonStep(ctx context.Context, step Step, docs []rag.Document) (string, error) {
	// 构建上下文
	var contextBuilder strings.Builder
	for i, doc := range docs {
		contextBuilder.WriteString(fmt.Sprintf("[文档 %d]\n%s\n\n", i+1, truncateText(doc.Content, 500)))
	}

	prompt := fmt.Sprintf(`基于以下检索结果进行推理。

检索结果:
%s

推理任务: %s

请进行推理并给出结论:`, contextBuilder.String(), step.Reasoning)

	resp, err := a.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return "", err
	}

	return resp.Content, nil
}

// generateAdditionalSteps 生成额外步骤
func (a *AgenticRAG) generateAdditionalSteps(ctx context.Context, originalQuery, currentConclusion string, currentDocs []rag.Document) ([]Step, error) {
	prompt := fmt.Sprintf(`当前结论表明需要更多信息。请生成额外的检索步骤。

原始查询: %s
当前结论: %s

可用检索源:
%s

返回 JSON 格式的额外步骤:
{"steps": [{"type": "retrieve", "query": "新查询", "source": "来源"}]}

只返回 JSON:`, originalQuery, currentConclusion, a.listSources())

	resp, err := a.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return nil, err
	}

	jsonContent := extractJSON(resp.Content)
	var result struct {
		Steps []Step `json:"steps"`
	}
	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return nil, err
	}

	return result.Steps, nil
}

// synthesize 综合生成最终回答
func (a *AgenticRAG) synthesize(ctx context.Context, query string, docs []rag.Document, steps []Step) (string, error) {
	// 构建上下文
	var contextBuilder strings.Builder
	for i, doc := range docs {
		source := ""
		if doc.Metadata != nil {
			if s, ok := doc.Metadata["retrieval_source"].(string); ok {
				source = fmt.Sprintf(" [来源: %s]", s)
			}
		}
		contextBuilder.WriteString(fmt.Sprintf("[%d]%s\n%s\n\n", i+1, source, truncateText(doc.Content, 300)))
	}

	// 构建推理过程摘要
	var reasoningBuilder strings.Builder
	for _, step := range steps {
		if step.Type == StepTypeReason && step.Conclusion != "" {
			reasoningBuilder.WriteString(fmt.Sprintf("推理: %s\n", truncateText(step.Conclusion, 200)))
		}
	}

	prompt := fmt.Sprintf(`基于以下信息回答问题。

问题: %s

检索到的信息:
%s

推理过程:
%s

请综合以上信息，用中文给出完整、准确的回答:`, query, contextBuilder.String(), reasoningBuilder.String())

	resp, err := a.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return "", err
	}

	return resp.Content, nil
}

// listSources 列出所有来源
func (a *AgenticRAG) listSources() string {
	var builder strings.Builder
	for name := range a.retrievers {
		builder.WriteString(fmt.Sprintf("- %s\n", name))
	}
	return builder.String()
}

// 辅助函数

// needsMoreInfo 检查结论是否表明需要更多信息
func needsMoreInfo(conclusion string) bool {
	indicators := []string{
		"需要更多",
		"信息不足",
		"无法确定",
		"需要进一步",
		"不清楚",
	}

	lower := strings.ToLower(conclusion)
	for _, ind := range indicators {
		if strings.Contains(lower, ind) {
			return true
		}
	}
	return false
}

// deduplicateDocs 文档去重
func deduplicateDocs(docs []rag.Document) []rag.Document {
	seen := make(map[string]bool)
	var result []rag.Document

	for _, doc := range docs {
		if !seen[doc.ID] {
			seen[doc.ID] = true
			result = append(result, doc)
		}
	}

	return result
}

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
