// Package extractor 提供 RAG 系统的文档元数据提取器
//
// MetadataExtractor 从文档中自动提取结构化元数据，如：
//   - 标题、摘要
//   - 关键词、主题
//   - 日期、作者
//   - 实体信息
//
// 参考 LlamaIndex 的 MetadataExtractor 设计
package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/toolkit/lang/stringx"
)

// MetadataExtractor 元数据提取器接口
type MetadataExtractor interface {
	// Extract 从文档中提取元数据
	Extract(ctx context.Context, doc rag.Document) (map[string]any, error)

	// Name 返回提取器名称
	Name() string
}

// ============== 标题提取器 ==============

// TitleExtractor 标题提取器
// 从文档内容中提取或生成标题
type TitleExtractor struct {
	// llmComplete LLM 完成函数（可选，用于生成标题）
	llmComplete func(ctx context.Context, prompt string) (string, error)

	// maxTitleLen 最大标题长度
	maxTitleLen int

	// fromFirstLine 是否从首行提取标题
	fromFirstLine bool
}

// TitleExtractorOption 配置选项
type TitleExtractorOption func(*TitleExtractor)

// WithTitleLLM 设置 LLM 用于生成标题
func WithTitleLLM(fn func(ctx context.Context, prompt string) (string, error)) TitleExtractorOption {
	return func(e *TitleExtractor) {
		e.llmComplete = fn
	}
}

// WithMaxTitleLen 设置最大标题长度
func WithMaxTitleLen(length int) TitleExtractorOption {
	return func(e *TitleExtractor) {
		e.maxTitleLen = length
	}
}

// WithTitleFromFirstLine 设置从首行提取标题
func WithTitleFromFirstLine(enabled bool) TitleExtractorOption {
	return func(e *TitleExtractor) {
		e.fromFirstLine = enabled
	}
}

// NewTitleExtractor 创建标题提取器
func NewTitleExtractor(opts ...TitleExtractorOption) *TitleExtractor {
	e := &TitleExtractor{
		maxTitleLen:   100,
		fromFirstLine: true,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Name 返回提取器名称
func (e *TitleExtractor) Name() string {
	return "title_extractor"
}

// Extract 提取标题
func (e *TitleExtractor) Extract(ctx context.Context, doc rag.Document) (map[string]any, error) {
	// 如果已有标题，直接返回
	if title, ok := doc.Metadata["title"].(string); ok && title != "" {
		return map[string]any{"title": title}, nil
	}

	var title string

	// 尝试从首行提取
	if e.fromFirstLine {
		title = extractFirstLine(doc.Content)
	}

	// 如果首行太长或没有找到，使用 LLM 生成
	if (title == "" || len(title) > e.maxTitleLen) && e.llmComplete != nil {
		prompt := fmt.Sprintf(`请为以下文档生成一个简洁的标题（不超过%d个字符）：

%s

只输出标题，不要其他内容。`, e.maxTitleLen, truncateText(doc.Content, 2000))

		generated, err := e.llmComplete(ctx, prompt)
		if err == nil && generated != "" {
			title = strings.TrimSpace(generated)
		}
	}

	// 如果还是没有标题，使用前几个词
	if title == "" {
		words := strings.Fields(doc.Content)
		if len(words) > 10 {
			words = words[:10]
		}
		title = strings.Join(words, " ") + "..."
	}

	// 截断过长的标题
	if len(title) > e.maxTitleLen {
		title = title[:e.maxTitleLen-3] + "..."
	}

	return map[string]any{"title": title}, nil
}

// ============== 摘要提取器 ==============

// SummaryExtractor 摘要提取器
// 使用 LLM 生成文档摘要
type SummaryExtractor struct {
	// llmComplete LLM 完成函数
	llmComplete func(ctx context.Context, prompt string) (string, error)

	// maxSummaryLen 最大摘要长度
	maxSummaryLen int

	// promptTemplate 提示词模板
	promptTemplate string
}

// SummaryExtractorOption 配置选项
type SummaryExtractorOption func(*SummaryExtractor)

// WithSummaryMaxLen 设置最大摘要长度
func WithSummaryMaxLen(length int) SummaryExtractorOption {
	return func(e *SummaryExtractor) {
		e.maxSummaryLen = length
	}
}

// WithSummaryPrompt 设置自定义提示词
func WithSummaryPrompt(prompt string) SummaryExtractorOption {
	return func(e *SummaryExtractor) {
		e.promptTemplate = prompt
	}
}

const defaultSummaryPrompt = `请为以下文档生成一个简洁的摘要（不超过%d个字符）：

%s

要求：
1. 概括文档的主要内容
2. 保留关键信息和结论
3. 使用简洁的语言

只输出摘要，不要其他内容。`

// NewSummaryExtractor 创建摘要提取器
//
// 参数：
//   - llmComplete: LLM 完成函数
//   - opts: 配置选项
func NewSummaryExtractor(llmComplete func(ctx context.Context, prompt string) (string, error), opts ...SummaryExtractorOption) *SummaryExtractor {
	e := &SummaryExtractor{
		llmComplete:    llmComplete,
		maxSummaryLen:  200,
		promptTemplate: defaultSummaryPrompt,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Name 返回提取器名称
func (e *SummaryExtractor) Name() string {
	return "summary_extractor"
}

// Extract 提取摘要
func (e *SummaryExtractor) Extract(ctx context.Context, doc rag.Document) (map[string]any, error) {
	if e.llmComplete == nil {
		return nil, fmt.Errorf("LLM complete function not set")
	}

	// 如果已有摘要，直接返回
	if summary, ok := doc.Metadata["summary"].(string); ok && summary != "" {
		return map[string]any{"summary": summary}, nil
	}

	prompt := fmt.Sprintf(e.promptTemplate, e.maxSummaryLen, truncateText(doc.Content, 3000))
	summary, err := e.llmComplete(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("生成摘要失败: %w", err)
	}

	return map[string]any{"summary": strings.TrimSpace(summary)}, nil
}

// ============== 关键词提取器 ==============

// KeywordExtractor 关键词提取器
type KeywordExtractor struct {
	// llmComplete LLM 完成函数（可选）
	llmComplete func(ctx context.Context, prompt string) (string, error)

	// maxKeywords 最大关键词数量
	maxKeywords int

	// useSimpleExtraction 是否使用简单提取（不依赖 LLM）
	useSimpleExtraction bool
}

// KeywordExtractorOption 配置选项
type KeywordExtractorOption func(*KeywordExtractor)

// WithKeywordLLM 设置 LLM
func WithKeywordLLM(fn func(ctx context.Context, prompt string) (string, error)) KeywordExtractorOption {
	return func(e *KeywordExtractor) {
		e.llmComplete = fn
	}
}

// WithMaxKeywords 设置最大关键词数量
func WithMaxKeywords(n int) KeywordExtractorOption {
	return func(e *KeywordExtractor) {
		e.maxKeywords = n
	}
}

// WithSimpleExtraction 使用简单提取（基于词频）
func WithSimpleExtraction(enabled bool) KeywordExtractorOption {
	return func(e *KeywordExtractor) {
		e.useSimpleExtraction = enabled
	}
}

// NewKeywordExtractor 创建关键词提取器
func NewKeywordExtractor(opts ...KeywordExtractorOption) *KeywordExtractor {
	e := &KeywordExtractor{
		maxKeywords:         10,
		useSimpleExtraction: false,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Name 返回提取器名称
func (e *KeywordExtractor) Name() string {
	return "keyword_extractor"
}

// Extract 提取关键词
func (e *KeywordExtractor) Extract(ctx context.Context, doc rag.Document) (map[string]any, error) {
	// 如果已有关键词，直接返回
	if keywords, ok := doc.Metadata["keywords"].([]string); ok && len(keywords) > 0 {
		return map[string]any{"keywords": keywords}, nil
	}

	var keywords []string

	if e.llmComplete != nil && !e.useSimpleExtraction {
		// 使用 LLM 提取
		prompt := fmt.Sprintf(`请从以下文档中提取最多%d个关键词或关键短语：

%s

以 JSON 数组格式输出，如：["关键词1", "关键词2"]`, e.maxKeywords, truncateText(doc.Content, 2000))

		response, err := e.llmComplete(ctx, prompt)
		if err == nil {
			keywords = parseKeywordsJSON(response)
		}
	}

	// 如果 LLM 提取失败或使用简单提取
	if len(keywords) == 0 {
		keywords = extractKeywordsSimple(doc.Content, e.maxKeywords)
	}

	return map[string]any{"keywords": keywords}, nil
}

// ============== 问题生成器 ==============

// QuestionsExtractor 问题生成器
// 生成可以由文档回答的问题，用于提高检索质量
type QuestionsExtractor struct {
	// llmComplete LLM 完成函数
	llmComplete func(ctx context.Context, prompt string) (string, error)

	// numQuestions 生成的问题数量
	numQuestions int
}

// QuestionsExtractorOption 配置选项
type QuestionsExtractorOption func(*QuestionsExtractor)

// WithNumQuestions 设置问题数量
func WithNumQuestions(n int) QuestionsExtractorOption {
	return func(e *QuestionsExtractor) {
		e.numQuestions = n
	}
}

// NewQuestionsExtractor 创建问题生成器
func NewQuestionsExtractor(llmComplete func(ctx context.Context, prompt string) (string, error), opts ...QuestionsExtractorOption) *QuestionsExtractor {
	e := &QuestionsExtractor{
		llmComplete:  llmComplete,
		numQuestions: 5,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Name 返回提取器名称
func (e *QuestionsExtractor) Name() string {
	return "questions_extractor"
}

// Extract 生成问题
func (e *QuestionsExtractor) Extract(ctx context.Context, doc rag.Document) (map[string]any, error) {
	if e.llmComplete == nil {
		return nil, fmt.Errorf("LLM complete function not set")
	}

	prompt := fmt.Sprintf(`请根据以下文档内容，生成%d个可以由该文档回答的问题：

%s

以 JSON 数组格式输出，如：["问题1？", "问题2？"]`, e.numQuestions, truncateText(doc.Content, 2000))

	response, err := e.llmComplete(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("生成问题失败: %w", err)
	}

	questions := parseQuestionsJSON(response)
	return map[string]any{"questions": questions}, nil
}

// ============== 实体提取器 ==============

// EntityExtractor 实体提取器
type EntityExtractor struct {
	// llmComplete LLM 完成函数
	llmComplete func(ctx context.Context, prompt string) (string, error)

	// entityTypes 要提取的实体类型
	entityTypes []string
}

// EntityExtractorOption 配置选项
type EntityExtractorOption func(*EntityExtractor)

// WithEntityTypes 设置要提取的实体类型
func WithEntityTypes(types ...string) EntityExtractorOption {
	return func(e *EntityExtractor) {
		e.entityTypes = types
	}
}

// NewEntityExtractor 创建实体提取器
func NewEntityExtractor(llmComplete func(ctx context.Context, prompt string) (string, error), opts ...EntityExtractorOption) *EntityExtractor {
	e := &EntityExtractor{
		llmComplete: llmComplete,
		entityTypes: []string{"person", "organization", "location", "date", "product"},
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Name 返回提取器名称
func (e *EntityExtractor) Name() string {
	return "entity_extractor"
}

// Extract 提取实体
func (e *EntityExtractor) Extract(ctx context.Context, doc rag.Document) (map[string]any, error) {
	if e.llmComplete == nil {
		return nil, fmt.Errorf("LLM complete function not set")
	}

	typesStr := strings.Join(e.entityTypes, ", ")
	prompt := fmt.Sprintf(`请从以下文档中提取实体信息，实体类型包括：%s

文档内容：
%s

以 JSON 格式输出，格式如下：
{
  "entities": {
    "person": ["人名1", "人名2"],
    "organization": ["组织1"],
    "location": ["地点1"],
    "date": ["日期1"],
    "product": ["产品1"]
  }
}

只输出 JSON，不要其他内容。`, typesStr, truncateText(doc.Content, 2000))

	response, err := e.llmComplete(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("提取实体失败: %w", err)
	}

	entities := parseEntitiesJSON(response)
	return map[string]any{"entities": entities}, nil
}

// ============== 复合提取器 ==============

// CompositeExtractor 复合提取器
// 组合多个提取器
type CompositeExtractor struct {
	extractors []MetadataExtractor
}

// NewCompositeExtractor 创建复合提取器
func NewCompositeExtractor(extractors ...MetadataExtractor) *CompositeExtractor {
	return &CompositeExtractor{
		extractors: extractors,
	}
}

// Name 返回提取器名称
func (e *CompositeExtractor) Name() string {
	return "composite_extractor"
}

// Extract 使用所有提取器提取元数据
func (e *CompositeExtractor) Extract(ctx context.Context, doc rag.Document) (map[string]any, error) {
	result := make(map[string]any)

	for _, extractor := range e.extractors {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		metadata, err := extractor.Extract(ctx, doc)
		if err != nil {
			// 单个提取器失败不影响其他
			continue
		}

		// 合并结果
		for k, v := range metadata {
			result[k] = v
		}
	}

	return result, nil
}

// AddExtractor 添加提取器
func (e *CompositeExtractor) AddExtractor(extractor MetadataExtractor) {
	e.extractors = append(e.extractors, extractor)
}

// ============== 简单元数据提取器 ==============

// SimpleExtractor 简单元数据提取器
// 不依赖 LLM，从文档中提取基本信息
type SimpleExtractor struct{}

// NewSimpleExtractor 创建简单提取器
func NewSimpleExtractor() *SimpleExtractor {
	return &SimpleExtractor{}
}

// Name 返回提取器名称
func (e *SimpleExtractor) Name() string {
	return "simple_extractor"
}

// Extract 提取基本元数据
func (e *SimpleExtractor) Extract(ctx context.Context, doc rag.Document) (map[string]any, error) {
	result := make(map[string]any)

	// 文档长度
	result["char_count"] = len(doc.Content)
	result["word_count"] = len(strings.Fields(doc.Content))

	// 尝试提取日期
	if dates := extractDates(doc.Content); len(dates) > 0 {
		result["dates"] = dates
	}

	// 尝试提取 URL
	if urls := extractURLs(doc.Content); len(urls) > 0 {
		result["urls"] = urls
	}

	// 尝试提取 email
	if emails := extractEmails(doc.Content); len(emails) > 0 {
		result["emails"] = emails
	}

	// 语言检测（简单启发式）
	result["language"] = detectLanguage(doc.Content)

	// 添加处理时间戳
	result["extracted_at"] = time.Now().Format(time.RFC3339)

	return result, nil
}

// ============== 辅助函数 ==============

// extractFirstLine 提取第一行
func extractFirstLine(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 跳过空行和过短的行
		if len(line) > 5 {
			return line
		}
	}
	return ""
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

// parseKeywordsJSON 解析关键词 JSON
func parseKeywordsJSON(response string) []string {
	// 尝试提取 JSON 数组
	start := strings.Index(response, "[")
	end := strings.LastIndex(response, "]")
	if start == -1 || end == -1 || end <= start {
		return nil
	}

	var keywords []string
	if err := json.Unmarshal([]byte(response[start:end+1]), &keywords); err != nil {
		return nil
	}
	return keywords
}

// parseQuestionsJSON 解析问题 JSON
func parseQuestionsJSON(response string) []string {
	return parseKeywordsJSON(response)
}

// parseEntitiesJSON 解析实体 JSON
func parseEntitiesJSON(response string) map[string][]string {
	// 尝试提取 JSON
	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start == -1 || end == -1 || end <= start {
		return nil
	}

	var result struct {
		Entities map[string][]string `json:"entities"`
	}
	if err := json.Unmarshal([]byte(response[start:end+1]), &result); err != nil {
		return nil
	}
	return result.Entities
}

// extractKeywordsSimple 简单关键词提取（基于词频）
func extractKeywordsSimple(content string, maxKeywords int) []string {
	// 分词
	words := strings.Fields(strings.ToLower(content))

	// 统计词频
	freq := make(map[string]int)
	for _, word := range words {
		// 过滤短词和停用词
		if len(word) < 3 || isStopWord(word) {
			continue
		}
		// 清理标点
		word = cleanWord(word)
		if word != "" {
			freq[word]++
		}
	}

	// 按频率排序
	type wordFreq struct {
		word  string
		count int
	}
	var sorted []wordFreq
	for w, c := range freq {
		sorted = append(sorted, wordFreq{w, c})
	}

	// 简单排序
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].count > sorted[i].count {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// 返回前 N 个
	n := maxKeywords
	if n > len(sorted) {
		n = len(sorted)
	}

	result := make([]string, n)
	for i := 0; i < n; i++ {
		result[i] = sorted[i].word
	}
	return result
}

// isStopWord 检查是否为停用词
func isStopWord(word string) bool {
	// 常见英文停用词
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "must": true, "shall": true,
		"this": true, "that": true, "these": true, "those": true,
		"it": true, "its": true, "of": true, "to": true, "for": true,
		"in": true, "on": true, "at": true, "by": true, "with": true,
		"and": true, "or": true, "but": true, "if": true, "then": true,
		"else": true, "when": true, "where": true, "why": true, "how": true,
		"all": true, "each": true, "every": true, "both": true, "few": true,
		"more": true, "most": true, "other": true, "some": true, "such": true,
		"no": true, "not": true, "only": true, "same": true, "so": true,
		"than": true, "too": true, "very": true, "just": true,
		// 常见中文停用词
		"的": true, "是": true, "在": true, "了": true, "和": true,
		"与": true, "也": true, "都": true, "不": true, "有": true,
		"这": true, "那": true, "我": true, "你": true, "他": true,
	}
	return stopWords[word]
}

// cleanWord 清理单词中的标点
func cleanWord(word string) string {
	// 移除常见标点
	word = strings.Trim(word, ".,;:!?\"'()[]{}«»")
	return word
}

// extractDates 提取日期
func extractDates(content string) []string {
	var dates []string

	// 匹配常见日期格式
	patterns := []string{
		`\d{4}-\d{2}-\d{2}`,      // 2024-01-15
		`\d{4}/\d{2}/\d{2}`,      // 2024/01/15
		`\d{2}/\d{2}/\d{4}`,      // 01/15/2024
		`\d{4}年\d{1,2}月\d{1,2}日`, // 2024年1月15日
	}

	seen := make(map[string]bool)
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllString(content, -1)
		for _, match := range matches {
			if !seen[match] {
				dates = append(dates, match)
				seen[match] = true
			}
		}
	}

	return dates
}

// extractURLs 提取 URL
func extractURLs(content string) []string {
	re := regexp.MustCompile(`https?://[^\s<>"{}|\\^` + "`" + `\[\]]+`)
	return re.FindAllString(content, -1)
}

// extractEmails 提取 email
func extractEmails(content string) []string {
	re := regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	return re.FindAllString(content, -1)
}

// detectLanguage 检测语言（简单启发式）
func detectLanguage(content string) string {
	// 统计中文字符
	var chineseCount, asciiCount int
	for _, r := range content {
		if r >= '\u4e00' && r <= '\u9fff' {
			chineseCount++
		} else if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			asciiCount++
		}
	}

	if chineseCount > asciiCount {
		return "zh"
	}
	return "en"
}

// 确保实现了接口
var _ MetadataExtractor = (*TitleExtractor)(nil)
var _ MetadataExtractor = (*SummaryExtractor)(nil)
var _ MetadataExtractor = (*KeywordExtractor)(nil)
var _ MetadataExtractor = (*QuestionsExtractor)(nil)
var _ MetadataExtractor = (*EntityExtractor)(nil)
var _ MetadataExtractor = (*CompositeExtractor)(nil)
var _ MetadataExtractor = (*SimpleExtractor)(nil)
