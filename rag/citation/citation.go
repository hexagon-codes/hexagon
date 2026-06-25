// Package citation 提供引用追踪功能
//
// Citation Engine 在生成回答时自动添加引用标注，
// 确保每个声明都能追溯到来源文档。
//
// 功能：
//   - 自动识别需要引用的内容
//   - 在回答中添加引用标记 [1], [2] 等
//   - 追踪引用与来源的对应关系
//   - 支持多种引用格式
//
// 使用示例：
//
//	engine := citation.New(
//	    retriever,
//	    llmProvider,
//	    citation.WithCitationStyle(citation.StyleNumeric),
//	)
//	response, err := engine.Query(ctx, "用户问题")
//	// response.Content 包含带引用的回答
//	// response.Citations 包含引用列表
package citation

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/toolkit/lang/stringx"
)

// CitationEngine 引用追踪引擎
type CitationEngine struct {
	// retriever 检索器
	retriever rag.Retriever

	// llm LLM 提供者
	llm llm.Provider

	// style 引用样式
	style CitationStyle

	// topK 检索文档数
	topK int

	// minSimilarity 最小相似度（用于引用匹配）
	minSimilarity float32
}

// CitationStyle 引用样式
type CitationStyle string

const (
	// StyleNumeric 数字样式 [1], [2], [3]
	StyleNumeric CitationStyle = "numeric"
	// StyleAuthorYear 作者年份样式 (Author, 2024)
	StyleAuthorYear CitationStyle = "author_year"
	// StyleFootnote 脚注样式 ¹, ², ³
	StyleFootnote CitationStyle = "footnote"
)

// Citation 引用信息
type Citation struct {
	// Index 引用编号（从 1 开始）
	Index int `json:"index"`

	// Marker 引用标记（如 "[1]"）
	Marker string `json:"marker"`

	// Text 被引用的文本片段
	Text string `json:"text"`

	// SourceID 来源文档 ID
	SourceID string `json:"source_id"`

	// SourceTitle 来源标题（如果有）
	SourceTitle string `json:"source_title,omitempty"`

	// SourceURL 来源 URL（如果有）
	SourceURL string `json:"source_url,omitempty"`

	// StartPosition 引用在回答中的起始位置
	StartPosition int `json:"start_position,omitempty"`

	// EndPosition 引用在回答中的结束位置
	EndPosition int `json:"end_position,omitempty"`
}

// CitedResponse 带引用的响应
type CitedResponse struct {
	// Content 带引用标记的回答内容
	Content string `json:"content"`

	// RawContent 无引用标记的原始内容
	RawContent string `json:"raw_content"`

	// Citations 引用列表
	Citations []Citation `json:"citations"`

	// Sources 来源文档列表
	Sources []rag.Document `json:"sources"`

	// Bibliography 参考文献列表（格式化）
	Bibliography string `json:"bibliography,omitempty"`
}

// Option 配置选项
type Option func(*CitationEngine)

// WithCitationStyle 设置引用样式
// 默认值: StyleNumeric
func WithCitationStyle(style CitationStyle) Option {
	return func(e *CitationEngine) {
		e.style = style
	}
}

// WithTopK 设置检索文档数
// 默认值: 5
func WithTopK(k int) Option {
	return func(e *CitationEngine) {
		if k > 0 {
			e.topK = k
		}
	}
}

// WithMinSimilarity 设置最小相似度
// 默认值: 0.5
func WithMinSimilarity(sim float32) Option {
	return func(e *CitationEngine) {
		if sim > 0 && sim <= 1.0 {
			e.minSimilarity = sim
		}
	}
}

// New 创建引用追踪引擎
func New(retriever rag.Retriever, provider llm.Provider, opts ...Option) *CitationEngine {
	e := &CitationEngine{
		retriever:     retriever,
		llm:           provider,
		style:         StyleNumeric,
		topK:          5,
		minSimilarity: 0.5,
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// Query 执行带引用的查询
func (e *CitationEngine) Query(ctx context.Context, query string) (*CitedResponse, error) {
	response := &CitedResponse{
		Citations: make([]Citation, 0),
		Sources:   make([]rag.Document, 0),
	}

	// 1. 检索相关文档
	docs, err := e.retriever.Retrieve(ctx, query, rag.WithTopK(e.topK))
	if err != nil {
		return nil, fmt.Errorf("检索失败: %w", err)
	}
	response.Sources = docs

	// 2. 生成带引用的回答
	citedContent, citations, err := e.generateWithCitations(ctx, query, docs)
	if err != nil {
		return nil, fmt.Errorf("生成失败: %w", err)
	}

	response.Content = citedContent
	response.Citations = citations

	// 3. 提取原始内容（去除引用标记）
	response.RawContent = e.stripCitations(citedContent)

	// 4. 生成参考文献
	response.Bibliography = e.generateBibliography(citations, docs)

	return response, nil
}

// generateWithCitations 生成带引用的回答
func (e *CitationEngine) generateWithCitations(ctx context.Context, query string, docs []rag.Document) (string, []Citation, error) {
	// 构建带编号的来源信息
	var sourcesBuilder strings.Builder
	for i, doc := range docs {
		title := e.extractTitle(doc)
		sourcesBuilder.WriteString(fmt.Sprintf("[%d] %s\n内容: %s\n\n",
			i+1, title, truncateText(doc.Content, 500)))
	}

	prompt := fmt.Sprintf(`基于以下来源回答问题。回答中必须使用引用标记标注信息来源。

规则：
1. 每个陈述或事实后面必须添加引用标记，如 [1], [2]
2. 一个陈述可以有多个引用，如 [1][2]
3. 只引用提供的来源，不要编造信息
4. 如果来源无法回答问题，请说明

来源：
%s

问题：%s

请用中文回答，并在每个陈述后添加引用标记：`, sourcesBuilder.String(), query)

	resp, err := e.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return "", nil, err
	}

	// 解析引用
	citations := e.parseCitations(resp.Content, docs)

	return resp.Content, citations, nil
}

// parseCitations 解析回答中的引用
func (e *CitationEngine) parseCitations(content string, docs []rag.Document) []Citation {
	citations := make([]Citation, 0)
	usedIndices := make(map[int]bool)

	// 匹配引用标记 [数字]
	re := regexp.MustCompile(`\[(\d+)\]`)
	matches := re.FindAllStringSubmatchIndex(content, -1)

	for _, match := range matches {
		if len(match) < 4 {
			continue
		}

		// 提取引用编号
		indexStr := content[match[2]:match[3]]
		var index int
		fmt.Sscanf(indexStr, "%d", &index)

		if index < 1 || index > len(docs) {
			continue
		}

		// 如果这个索引还没有被记录
		if !usedIndices[index] {
			usedIndices[index] = true
			doc := docs[index-1]

			citation := Citation{
				Index:         index,
				Marker:        e.formatMarker(index),
				SourceID:      doc.ID,
				SourceTitle:   e.extractTitle(doc),
				SourceURL:     e.extractURL(doc),
				StartPosition: match[0],
				EndPosition:   match[1],
			}

			// 提取引用周围的文本作为被引用内容
			citation.Text = e.extractCitedText(content, match[0])

			citations = append(citations, citation)
		}
	}

	return citations
}

// extractCitedText 提取引用标记前的文本
func (e *CitationEngine) extractCitedText(content string, citationPos int) string {
	// 转换为 rune 切片以正确处理中文
	runes := []rune(content)

	// 找到 citationPos 对应的 rune 位置
	bytePos := 0
	runePos := 0
	for runePos < len(runes) && bytePos < citationPos {
		bytePos += len(string(runes[runePos]))
		runePos++
	}

	// 向前查找句子开始
	start := runePos
	for start > 0 {
		prev := runes[start-1]
		if prev == '。' || prev == '.' || prev == '\n' {
			break
		}
		if runePos-start >= 100 { // 限制最大字符数
			break
		}
		start--
	}

	text := strings.TrimSpace(string(runes[start:runePos]))
	// 移除可能的引用标记
	re := regexp.MustCompile(`\[\d+\]`)
	text = re.ReplaceAllString(text, "")

	return strings.TrimSpace(text)
}

// formatMarker 根据样式格式化引用标记
func (e *CitationEngine) formatMarker(index int) string {
	switch e.style {
	case StyleNumeric:
		return fmt.Sprintf("[%d]", index)
	case StyleFootnote:
		superscripts := []rune{'⁰', '¹', '²', '³', '⁴', '⁵', '⁶', '⁷', '⁸', '⁹'}
		if index < 10 {
			return string(superscripts[index])
		}
		// 多位数处理
		var result strings.Builder
		for _, digit := range fmt.Sprintf("%d", index) {
			d := int(digit - '0')
			result.WriteRune(superscripts[d])
		}
		return result.String()
	case StyleAuthorYear:
		// 这种样式需要额外的元数据，这里返回简化版本
		return fmt.Sprintf("(Source %d)", index)
	default:
		return fmt.Sprintf("[%d]", index)
	}
}

// extractTitle 从文档中提取标题
func (e *CitationEngine) extractTitle(doc rag.Document) string {
	if doc.Metadata != nil {
		if title, ok := doc.Metadata["title"].(string); ok && title != "" {
			return title
		}
	}

	// 使用来源作为标题
	if doc.Source != "" {
		return doc.Source
	}

	// 使用内容的第一行
	lines := strings.SplitN(doc.Content, "\n", 2)
	if len(lines) > 0 && len(lines[0]) > 0 {
		title := lines[0]
		if len(title) > 50 {
			title = title[:50] + "..."
		}
		return title
	}

	return fmt.Sprintf("文档 %s", doc.ID)
}

// extractURL 从文档中提取 URL
func (e *CitationEngine) extractURL(doc rag.Document) string {
	if doc.Metadata != nil {
		if url, ok := doc.Metadata["url"].(string); ok {
			return url
		}
	}
	return ""
}

// stripCitations 去除引用标记
func (e *CitationEngine) stripCitations(content string) string {
	// 移除 [数字] 格式的引用
	re := regexp.MustCompile(`\s*\[\d+\]`)
	result := re.ReplaceAllString(content, "")

	// 移除多余的空格
	result = regexp.MustCompile(`\s+`).ReplaceAllString(result, " ")
	return strings.TrimSpace(result)
}

// generateBibliography 生成参考文献列表
func (e *CitationEngine) generateBibliography(citations []Citation, docs []rag.Document) string {
	if len(citations) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("参考文献：\n")

	usedDocs := make(map[int]bool)
	for _, citation := range citations {
		if !usedDocs[citation.Index] {
			usedDocs[citation.Index] = true

			builder.WriteString(fmt.Sprintf("%s ", citation.Marker))
			builder.WriteString(citation.SourceTitle)

			if citation.SourceURL != "" {
				builder.WriteString(fmt.Sprintf(" (%s)", citation.SourceURL))
			}
			builder.WriteString("\n")
		}
	}

	return builder.String()
}

// ============== 结构化引用生成 ==============

// GenerateStructuredCitation 生成结构化的引用响应
// 使用 LLM 生成带有明确引用的 JSON 格式响应
func (e *CitationEngine) GenerateStructuredCitation(ctx context.Context, query string, docs []rag.Document) (*CitedResponse, error) {
	// 构建来源信息
	var sourcesBuilder strings.Builder
	for i, doc := range docs {
		title := e.extractTitle(doc)
		sourcesBuilder.WriteString(fmt.Sprintf("[%d] %s: %s\n",
			i+1, title, truncateText(doc.Content, 300)))
	}

	prompt := fmt.Sprintf(`基于以下来源回答问题，并以 JSON 格式返回带引用的回答。

来源：
%s

问题：%s

返回 JSON 格式：
{
  "answer": "回答文本，包含引用标记如[1]",
  "citations": [
    {"index": 1, "text": "被引用的文本片段", "source_index": 1}
  ]
}

只返回 JSON：`, sourcesBuilder.String(), query)

	resp, err := e.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return nil, err
	}

	// 解析 JSON 响应
	return e.parseStructuredResponse(resp.Content, docs)
}

// parseStructuredResponse 解析结构化响应
func (e *CitationEngine) parseStructuredResponse(content string, docs []rag.Document) (*CitedResponse, error) {
	jsonContent := extractJSON(content)

	var parsed struct {
		Answer    string `json:"answer"`
		Citations []struct {
			Index       int    `json:"index"`
			Text        string `json:"text"`
			SourceIndex int    `json:"source_index"`
		} `json:"citations"`
	}

	if err := json.Unmarshal([]byte(jsonContent), &parsed); err != nil {
		// 解析失败时返回原始内容
		return &CitedResponse{
			Content:    content,
			RawContent: content,
			Sources:    docs,
		}, nil
	}

	// 构建引用列表
	citations := make([]Citation, len(parsed.Citations))
	for i, c := range parsed.Citations {
		sourceIndex := c.SourceIndex - 1
		if sourceIndex < 0 || sourceIndex >= len(docs) {
			sourceIndex = 0
		}

		doc := docs[sourceIndex]
		citations[i] = Citation{
			Index:       c.Index,
			Marker:      e.formatMarker(c.Index),
			Text:        c.Text,
			SourceID:    doc.ID,
			SourceTitle: e.extractTitle(doc),
			SourceURL:   e.extractURL(doc),
		}
	}

	return &CitedResponse{
		Content:      parsed.Answer,
		RawContent:   e.stripCitations(parsed.Answer),
		Citations:    citations,
		Sources:      docs,
		Bibliography: e.generateBibliography(citations, docs),
	}, nil
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
