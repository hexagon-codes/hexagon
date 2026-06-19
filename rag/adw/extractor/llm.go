// Package extractor 提供文档信息提取功能
//
// 本包实现多种提取器：
// - LLMExtractor: 基于 LLM 的结构化信息提取
// - RuleExtractor: 基于规则的提取
// - HybridExtractor: 混合提取策略
package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/rag/adw"
	"github.com/hexagon-codes/toolkit/util/idgen"
)

// LLMExtractor 基于 LLM 的提取器
// 使用 LLM 理解文档内容并提取结构化信息
type LLMExtractor struct {
	// provider LLM Provider
	provider llm.Provider

	// model 使用的模型
	model string

	// temperature 温度参数
	temperature float64

	// maxTokens 最大 token 数
	maxTokens int

	// systemPrompt 系统提示词
	systemPrompt string

	// entityPrompt 实体提取提示词
	entityPrompt string

	// relationPrompt 关系提取提示词
	relationPrompt string
}

// LLMExtractorOption LLMExtractor 配置选项
type LLMExtractorOption func(*LLMExtractor)

// WithModel 设置模型
func WithModel(model string) LLMExtractorOption {
	return func(e *LLMExtractor) {
		e.model = model
	}
}

// WithTemperature 设置温度
func WithTemperature(temp float64) LLMExtractorOption {
	return func(e *LLMExtractor) {
		e.temperature = temp
	}
}

// WithMaxTokens 设置最大 token 数
func WithMaxTokens(max int) LLMExtractorOption {
	return func(e *LLMExtractor) {
		e.maxTokens = max
	}
}

// WithSystemPrompt 设置系统提示词
func WithSystemPrompt(prompt string) LLMExtractorOption {
	return func(e *LLMExtractor) {
		e.systemPrompt = prompt
	}
}

// NewLLMExtractor 创建 LLM 提取器
func NewLLMExtractor(provider llm.Provider, opts ...LLMExtractorOption) *LLMExtractor {
	e := &LLMExtractor{
		provider:    provider,
		model:       "gpt-4o-mini",
		temperature: 0.0,
		maxTokens:   4096,
		systemPrompt: `你是一个专业的文档信息提取专家。你的任务是从给定的文档中提取结构化信息。

请遵循以下规则：
1. 严格按照提供的 Schema 提取信息
2. 如果某个字段在文档中找不到，返回 null
3. 确保提取的数据类型与 Schema 定义一致
4. 日期格式统一为 YYYY-MM-DD
5. 金额保留两位小数
6. 返回纯 JSON 格式，不要包含 markdown 代码块标记`,

		entityPrompt: `请从以下文档中识别实体（人名、组织名、地点、日期、金额等）。

返回 JSON 格式：
{
  "entities": [
    {"text": "实体文本", "type": "实体类型", "normalized_value": "标准化值", "confidence": 0.95}
  ]
}

实体类型包括：person, organization, location, date, time, money, percent, email, phone, url, product, event`,

		relationPrompt: `请分析以下实体之间的关系。

返回 JSON 格式：
{
  "relations": [
    {"source": "源实体", "target": "目标实体", "type": "关系类型", "confidence": 0.9}
  ]
}`,
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// Name 返回提取器名称
func (e *LLMExtractor) Name() string {
	return "llm_extractor"
}

// Extract 提取结构化数据
func (e *LLMExtractor) Extract(ctx context.Context, doc *adw.Document, schema *adw.ExtractionSchema) (map[string]any, error) {
	if schema == nil {
		return nil, adw.ErrSchemaRequired
	}

	// 构建提取提示词
	prompt := e.buildExtractionPrompt(doc.Document.Content, schema)

	// 调用 LLM
	req := llm.CompletionRequest{
		Model: e.model,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: e.systemPrompt},
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens:   e.maxTokens,
		Temperature: &e.temperature,
	}

	resp, err := e.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM 调用失败: %w", err)
	}

	// 解析响应
	result, err := e.parseExtractionResponse(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return result, nil
}

// ExtractEntities 提取实体
func (e *LLMExtractor) ExtractEntities(ctx context.Context, doc *adw.Document) ([]adw.Entity, error) {
	// 构建提示词
	prompt := e.entityPrompt + "\n\n文档内容：\n" + doc.Document.Content

	// 调用 LLM
	req := llm.CompletionRequest{
		Model: e.model,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens:   e.maxTokens,
		Temperature: &e.temperature,
	}

	resp, err := e.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM 调用失败: %w", err)
	}

	// 解析响应
	entities, err := e.parseEntityResponse(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("解析实体响应失败: %w", err)
	}

	return entities, nil
}

// ExtractRelations 提取关系
func (e *LLMExtractor) ExtractRelations(ctx context.Context, doc *adw.Document, entities []adw.Entity) ([]adw.Relation, error) {
	if len(entities) < 2 {
		return nil, nil // 至少需要两个实体才能建立关系
	}

	// 构建实体列表
	var entityList strings.Builder
	for _, entity := range entities {
		entityList.WriteString(fmt.Sprintf("- %s (%s)\n", entity.Text, entity.Type))
	}

	// 构建提示词
	prompt := e.relationPrompt + "\n\n实体列表：\n" + entityList.String() + "\n\n文档内容：\n" + doc.Document.Content

	// 调用 LLM
	req := llm.CompletionRequest{
		Model: e.model,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens:   e.maxTokens,
		Temperature: &e.temperature,
	}

	resp, err := e.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM 调用失败: %w", err)
	}

	// 解析响应
	relations, err := e.parseRelationResponse(resp.Content, entities)
	if err != nil {
		return nil, fmt.Errorf("解析关系响应失败: %w", err)
	}

	return relations, nil
}

// buildExtractionPrompt 构建提取提示词
func (e *LLMExtractor) buildExtractionPrompt(content string, schema *adw.ExtractionSchema) string {
	var sb strings.Builder

	sb.WriteString("请从以下文档中提取信息。\n\n")
	sb.WriteString("## Schema 定义\n")
	sb.WriteString(fmt.Sprintf("名称: %s\n", schema.Name))
	if schema.Description != "" {
		sb.WriteString(fmt.Sprintf("描述: %s\n", schema.Description))
	}
	sb.WriteString("\n字段定义：\n")

	for _, field := range schema.Fields {
		sb.WriteString(fmt.Sprintf("- %s (%s)", field.Name, field.Type))
		if field.Required {
			sb.WriteString(" [必需]")
		}
		if field.Description != "" {
			sb.WriteString(fmt.Sprintf(": %s", field.Description))
		}
		sb.WriteString("\n")

		if len(field.Examples) > 0 {
			sb.WriteString(fmt.Sprintf("  示例: %v\n", field.Examples))
		}
	}

	sb.WriteString("\n## 文档内容\n")
	sb.WriteString(content)
	sb.WriteString("\n\n## 输出\n")
	sb.WriteString("请以 JSON 格式返回提取结果，格式如下：\n")
	sb.WriteString("{\n")
	for i, field := range schema.Fields {
		sb.WriteString(fmt.Sprintf("  \"%s\": <值>", field.Name))
		if i < len(schema.Fields)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")

	return sb.String()
}

// parseExtractionResponse 解析提取响应
func (e *LLMExtractor) parseExtractionResponse(response string) (map[string]any, error) {
	// 清理响应（去除可能的 markdown 代码块标记）
	response = cleanJSONResponse(response)

	var result map[string]any
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}

	return result, nil
}

// parseEntityResponse 解析实体响应
func (e *LLMExtractor) parseEntityResponse(response string) ([]adw.Entity, error) {
	response = cleanJSONResponse(response)

	var result struct {
		Entities []struct {
			Text            string  `json:"text"`
			Type            string  `json:"type"`
			NormalizedValue string  `json:"normalized_value"`
			Confidence      float64 `json:"confidence"`
		} `json:"entities"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}

	entities := make([]adw.Entity, len(result.Entities))
	for i, e := range result.Entities {
		entities[i] = adw.Entity{
			ID:              idgen.NanoID(),
			Text:            e.Text,
			Type:            adw.EntityType(e.Type),
			NormalizedValue: e.NormalizedValue,
			Confidence:      e.Confidence,
		}
	}

	return entities, nil
}

// parseRelationResponse 解析关系响应
func (e *LLMExtractor) parseRelationResponse(response string, entities []adw.Entity) ([]adw.Relation, error) {
	response = cleanJSONResponse(response)

	var result struct {
		Relations []struct {
			Source     string  `json:"source"`
			Target     string  `json:"target"`
			Type       string  `json:"type"`
			Confidence float64 `json:"confidence"`
		} `json:"relations"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %w", err)
	}

	// 构建实体文本到 ID 的映射
	textToID := make(map[string]string)
	for _, entity := range entities {
		textToID[entity.Text] = entity.ID
	}

	relations := make([]adw.Relation, 0, len(result.Relations))
	for _, r := range result.Relations {
		sourceID, ok1 := textToID[r.Source]
		targetID, ok2 := textToID[r.Target]

		if ok1 && ok2 {
			relations = append(relations, adw.Relation{
				ID:             idgen.NanoID(),
				Type:           r.Type,
				SourceEntityID: sourceID,
				TargetEntityID: targetID,
				Confidence:     r.Confidence,
			})
		}
	}

	return relations, nil
}

// cleanJSONResponse 清理 JSON 响应
func cleanJSONResponse(response string) string {
	// 去除 markdown 代码块标记
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)
	return response
}

// ============== LLM 提取步骤 ==============

// LLMExtractionStep LLM 提取步骤
type LLMExtractionStep struct {
	extractor *LLMExtractor
	schema    *adw.ExtractionSchema
}

// NewLLMExtractionStep 创建 LLM 提取步骤
func NewLLMExtractionStep(provider llm.Provider, schema *adw.ExtractionSchema, opts ...LLMExtractorOption) *LLMExtractionStep {
	return &LLMExtractionStep{
		extractor: NewLLMExtractor(provider, opts...),
		schema:    schema,
	}
}

// Name 返回步骤名称
func (s *LLMExtractionStep) Name() string {
	return "llm_extraction"
}

// Description 返回步骤描述
func (s *LLMExtractionStep) Description() string {
	return "使用 LLM 提取结构化信息"
}

// Process 处理文档
func (s *LLMExtractionStep) Process(ctx context.Context, doc *adw.Document, opts adw.ProcessOptions) error {
	// 使用传入的 schema 或步骤自带的 schema
	schema := opts.Schema
	if schema == nil {
		schema = s.schema
	}

	if schema == nil {
		return adw.ErrSchemaRequired
	}

	// 提取结构化数据
	data, err := s.extractor.Extract(ctx, doc, schema)
	if err != nil {
		return fmt.Errorf("提取失败: %w", err)
	}

	// 保存到文档
	for k, v := range data {
		doc.SetStructuredValue(k, v)
	}

	return nil
}

// CanHandle 检查是否能处理
func (s *LLMExtractionStep) CanHandle(doc *adw.Document) bool {
	return len(doc.Document.Content) > 0
}

// ============== 实体提取步骤 ==============

// EntityExtractionStep 实体提取步骤
type EntityExtractionStep struct {
	extractor *LLMExtractor
}

// NewEntityExtractionStep 创建实体提取步骤
func NewEntityExtractionStep(provider llm.Provider, opts ...LLMExtractorOption) *EntityExtractionStep {
	return &EntityExtractionStep{
		extractor: NewLLMExtractor(provider, opts...),
	}
}

// Name 返回步骤名称
func (s *EntityExtractionStep) Name() string {
	return "entity_extraction"
}

// Description 返回步骤描述
func (s *EntityExtractionStep) Description() string {
	return "提取文档中的实体"
}

// Process 处理文档
func (s *EntityExtractionStep) Process(ctx context.Context, doc *adw.Document, opts adw.ProcessOptions) error {
	if !opts.EnableEntityRecognition {
		return nil
	}

	// 提取实体
	entities, err := s.extractor.ExtractEntities(ctx, doc)
	if err != nil {
		return fmt.Errorf("实体提取失败: %w", err)
	}

	// 添加到文档
	for _, entity := range entities {
		doc.AddEntity(entity)
	}

	return nil
}

// CanHandle 检查是否能处理
func (s *EntityExtractionStep) CanHandle(doc *adw.Document) bool {
	return len(doc.Document.Content) > 0
}

// ============== 关系提取步骤 ==============

// RelationExtractionStep 关系提取步骤
type RelationExtractionStep struct {
	extractor *LLMExtractor
}

// NewRelationExtractionStep 创建关系提取步骤
func NewRelationExtractionStep(provider llm.Provider, opts ...LLMExtractorOption) *RelationExtractionStep {
	return &RelationExtractionStep{
		extractor: NewLLMExtractor(provider, opts...),
	}
}

// Name 返回步骤名称
func (s *RelationExtractionStep) Name() string {
	return "relation_extraction"
}

// Description 返回步骤描述
func (s *RelationExtractionStep) Description() string {
	return "提取实体之间的关系"
}

// Process 处理文档
func (s *RelationExtractionStep) Process(ctx context.Context, doc *adw.Document, opts adw.ProcessOptions) error {
	if !opts.EnableRelationExtraction {
		return nil
	}

	// 需要先有实体
	if len(doc.Entities) < 2 {
		return nil
	}

	// 提取关系
	relations, err := s.extractor.ExtractRelations(ctx, doc, doc.Entities)
	if err != nil {
		return fmt.Errorf("关系提取失败: %w", err)
	}

	// 添加到文档
	for _, relation := range relations {
		doc.AddRelation(relation)
	}

	return nil
}

// CanHandle 检查是否能处理
func (s *RelationExtractionStep) CanHandle(doc *adw.Document) bool {
	return len(doc.Entities) >= 2
}
