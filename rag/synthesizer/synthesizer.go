// Package synthesizer 提供 RAG 响应合成能力
//
// 响应合成器负责将检索到的文档与用户查询结合，生成最终的回答。
// 借鉴 LlamaIndex 的响应合成器设计，支持多种合成策略。
//
// 支持的合成策略：
//   - Refine: 迭代精炼，逐个文档优化答案
//   - Compact: 紧凑合成，将文档压缩后一次性生成
//   - TreeSummarize: 树状摘要，分层递归合成
//   - SimpleSummarize: 简单摘要，直接合并所有文档
package synthesizer

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/toolkit/lang/stringx"
)

// Synthesizer 响应合成器接口
// 负责将检索到的文档与用户查询结合，生成最终回答
type Synthesizer interface {
	// Name 返回合成器名称
	Name() string

	// Synthesize 合成响应
	// 接收查询和相关文档，返回生成的响应
	Synthesize(ctx context.Context, query string, docs []rag.Document, opts ...SynthesizeOption) (*Response, error)
}

// Response 合成响应
type Response struct {
	// Content 生成的回答内容
	Content string `json:"content"`

	// SourceDocuments 引用的源文档
	SourceDocuments []rag.Document `json:"source_documents,omitempty"`

	// Metadata 元数据（包含 token 消耗、延迟等信息）
	Metadata map[string]any `json:"metadata,omitempty"`
}

// SynthesizeOption 合成选项函数
type SynthesizeOption func(*synthesizeConfig)

// synthesizeConfig 合成配置
type synthesizeConfig struct {
	// maxTokens 最大生成 token 数
	maxTokens int
	// temperature 生成温度
	temperature float64
	// promptTemplate 提示词模板
	promptTemplate string
	// includeSource 是否包含源文档引用
	includeSource bool
	// timeout 超时时间
	timeout time.Duration
}

// WithMaxTokens 设置最大生成 token 数
func WithMaxTokens(n int) SynthesizeOption {
	return func(c *synthesizeConfig) {
		c.maxTokens = n
	}
}

// WithTemperature 设置生成温度
// 温度越高，生成的内容越随机；温度越低，生成的内容越确定
func WithTemperature(t float64) SynthesizeOption {
	return func(c *synthesizeConfig) {
		c.temperature = t
	}
}

// WithPromptTemplate 设置提示词模板
// 模板中可使用 {query} 和 {context} 占位符
func WithPromptTemplate(template string) SynthesizeOption {
	return func(c *synthesizeConfig) {
		c.promptTemplate = template
	}
}

// WithSourceDocuments 设置是否包含源文档引用
func WithSourceDocuments(include bool) SynthesizeOption {
	return func(c *synthesizeConfig) {
		c.includeSource = include
	}
}

// WithSynthesizeTimeout 设置合成超时时间
func WithSynthesizeTimeout(d time.Duration) SynthesizeOption {
	return func(c *synthesizeConfig) {
		c.timeout = d
	}
}

// ============== Refine Synthesizer ==============

// RefineSynthesizer 迭代精炼合成器
// 逐个处理文档，每次用新文档来精炼已有的答案
// 适合需要综合多个文档信息的场景
type RefineSynthesizer struct {
	// name 合成器名称
	name string
	// llm LLM 提供者
	llm llm.Provider
	// refinePrompt 精炼提示词模板
	refinePrompt string
	// initialPrompt 初始提示词模板
	initialPrompt string
}

// NewRefineSynthesizer 创建迭代精炼合成器
func NewRefineSynthesizer(opts ...RefineSynthesizerOption) *RefineSynthesizer {
	s := &RefineSynthesizer{
		name: "refine_synthesizer",
		initialPrompt: `基于以下上下文回答问题。
上下文: {context}
问题: {query}
回答:`,
		refinePrompt: `原始问题: {query}
已有回答: {existing_answer}
以下是新的上下文信息: {context}
如果新上下文有助于改进回答，请优化它；否则返回原回答。
优化后的回答:`,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// RefineSynthesizerOption 精炼合成器选项
type RefineSynthesizerOption func(*RefineSynthesizer)

// WithRefineSynthesizerName 设置合成器名称
func WithRefineSynthesizerName(name string) RefineSynthesizerOption {
	return func(s *RefineSynthesizer) {
		s.name = name
	}
}

// WithRefineSynthesizerLLM 设置 LLM 提供者
func WithRefineSynthesizerLLM(provider llm.Provider) RefineSynthesizerOption {
	return func(s *RefineSynthesizer) {
		s.llm = provider
	}
}

// WithRefinePrompt 设置精炼提示词
func WithRefinePrompt(prompt string) RefineSynthesizerOption {
	return func(s *RefineSynthesizer) {
		s.refinePrompt = prompt
	}
}

// Name 返回合成器名称
func (s *RefineSynthesizer) Name() string {
	return s.name
}

// Synthesize 合成响应
// 迭代处理每个文档，逐步精炼答案
func (s *RefineSynthesizer) Synthesize(ctx context.Context, query string, docs []rag.Document, opts ...SynthesizeOption) (*Response, error) {
	if len(docs) == 0 {
		return &Response{
			Content:  "没有找到相关信息来回答您的问题。",
			Metadata: map[string]any{"strategy": "refine", "doc_count": 0},
		}, nil
	}

	config := &synthesizeConfig{
		maxTokens:     1024,
		temperature:   0.7,
		includeSource: true,
	}
	for _, opt := range opts {
		opt(config)
	}

	// 如果没有 LLM 提供者，返回占位响应
	if s.llm == nil {
		var contents []string
		for _, doc := range docs {
			contents = append(contents, doc.Content)
		}
		return &Response{
			Content: fmt.Sprintf("基于 %d 个文档的回答（需要配置 LLM）:\n\n%s\n\n问题: %s",
				len(docs), strings.Join(contents, "\n---\n"), query),
			Metadata:        map[string]any{"strategy": "refine", "doc_count": len(docs)},
			SourceDocuments: docs,
		}, nil
	}

	// 迭代精炼：第一个文档生成初始答案，后续文档逐步精炼
	var currentAnswer string
	var totalTokens int

	for i, doc := range docs {
		var prompt string
		if i == 0 {
			// 初始提示
			prompt = strings.ReplaceAll(s.initialPrompt, "{context}", doc.Content)
			prompt = strings.ReplaceAll(prompt, "{query}", query)
		} else {
			// 精炼提示：同时注入原始问题，避免后续轮次丢失用户问题上下文
			prompt = strings.ReplaceAll(s.refinePrompt, "{existing_answer}", currentAnswer)
			prompt = strings.ReplaceAll(prompt, "{context}", doc.Content)
			prompt = strings.ReplaceAll(prompt, "{query}", query)
		}

		resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
			Messages: []llm.Message{{Role: llm.RoleUser, Content: prompt}},
		})
		if err != nil {
			return nil, fmt.Errorf("LLM 调用失败: %w", err)
		}

		currentAnswer = resp.Content
		totalTokens += resp.Usage.TotalTokens
	}

	response := &Response{
		Content: currentAnswer,
		Metadata: map[string]any{
			"strategy":     "refine",
			"doc_count":    len(docs),
			"total_tokens": totalTokens,
		},
	}

	if config.includeSource {
		response.SourceDocuments = docs
	}

	return response, nil
}

// ============== Compact Synthesizer ==============

// CompactSynthesizer 紧凑合成器
// 将所有文档压缩到上下文窗口内，一次性生成答案
// 适合文档较少或需要快速响应的场景
type CompactSynthesizer struct {
	// name 合成器名称
	name string
	// llm LLM 提供者
	llm llm.Provider
	// maxContextLength 最大上下文长度
	maxContextLength int
	// separator 文档分隔符
	separator string
}

// NewCompactSynthesizer 创建紧凑合成器
func NewCompactSynthesizer(opts ...CompactSynthesizerOption) *CompactSynthesizer {
	s := &CompactSynthesizer{
		name:             "compact_synthesizer",
		maxContextLength: 4096,
		separator:        "\n---\n",
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CompactSynthesizerOption 紧凑合成器选项
type CompactSynthesizerOption func(*CompactSynthesizer)

// WithCompactSynthesizerName 设置合成器名称
func WithCompactSynthesizerName(name string) CompactSynthesizerOption {
	return func(s *CompactSynthesizer) {
		s.name = name
	}
}

// WithCompactSynthesizerMaxContext 设置最大上下文长度
func WithCompactSynthesizerMaxContext(n int) CompactSynthesizerOption {
	return func(s *CompactSynthesizer) {
		s.maxContextLength = n
	}
}

// WithCompactSynthesizerLLM 设置 LLM 提供者
func WithCompactSynthesizerLLM(provider llm.Provider) CompactSynthesizerOption {
	return func(s *CompactSynthesizer) {
		s.llm = provider
	}
}

// Name 返回合成器名称
func (s *CompactSynthesizer) Name() string {
	return s.name
}

// Synthesize 合成响应
// 将所有文档合并后一次性生成答案
func (s *CompactSynthesizer) Synthesize(ctx context.Context, query string, docs []rag.Document, opts ...SynthesizeOption) (*Response, error) {
	if len(docs) == 0 {
		return &Response{
			Content:  "没有找到相关信息来回答您的问题。",
			Metadata: map[string]any{"strategy": "compact", "doc_count": 0},
		}, nil
	}

	config := &synthesizeConfig{
		maxTokens:     1024,
		temperature:   0.7,
		includeSource: true,
	}
	for _, opt := range opts {
		opt(config)
	}

	// 合并文档内容
	var contents []string
	for _, doc := range docs {
		contents = append(contents, doc.Content)
	}
	compactedContext := strings.Join(contents, s.separator)

	// 截断到最大长度：判定与截断动作统一用 rune（字符）度量，
	// 避免"字节判定 / rune 截断"混用导致中文等多字节内容实际超限。
	// 复用 toolkit/lang/stringx.SubString 做 UTF-8 安全截断。
	if utf8.RuneCountInString(compactedContext) > s.maxContextLength {
		compactedContext = stringx.SubString(compactedContext, 0, s.maxContextLength)
	}

	// 如果没有 LLM 提供者，返回占位响应
	if s.llm == nil {
		return &Response{
			Content: fmt.Sprintf("基于 %d 个文档的回答（需要配置 LLM）:\n\n%s\n\n问题: %s",
				len(docs), compactedContext, query),
			Metadata:        map[string]any{"strategy": "compact", "doc_count": len(docs)},
			SourceDocuments: docs,
		}, nil
	}

	// 构建提示词
	prompt := fmt.Sprintf(`基于以下上下文信息回答问题。如果上下文中没有相关信息，请如实说明。

上下文:
%s

问题: %s

回答:`, compactedContext, query)

	resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: prompt}},
	})
	if err != nil {
		return nil, fmt.Errorf("LLM 调用失败: %w", err)
	}

	response := &Response{
		Content: resp.Content,
		Metadata: map[string]any{
			"strategy":       "compact",
			"doc_count":      len(docs),
			"context_length": utf8.RuneCountInString(compactedContext),
			"total_tokens":   resp.Usage.TotalTokens,
		},
	}

	if config.includeSource {
		response.SourceDocuments = docs
	}

	return response, nil
}

// ============== Tree Summarize Synthesizer ==============

// TreeSummarizeSynthesizer 树状摘要合成器
// 分层递归处理文档，适合大量文档的场景
type TreeSummarizeSynthesizer struct {
	// name 合成器名称
	name string
	// llm LLM 提供者
	llm llm.Provider
	// chunkSize 每组文档数量
	chunkSize int
}

// NewTreeSummarizeSynthesizer 创建树状摘要合成器
func NewTreeSummarizeSynthesizer(opts ...TreeSynthesizerOption) *TreeSummarizeSynthesizer {
	s := &TreeSummarizeSynthesizer{
		name:      "tree_summarize_synthesizer",
		chunkSize: 5,
	}
	for _, opt := range opts {
		opt(s)
	}
	// 确保 chunkSize 至少为 2，否则会导致无限循环
	// 当 chunkSize=1 时，每个 chunk 产生一个摘要，总数不会减少
	if s.chunkSize < 2 {
		s.chunkSize = 2
	}
	return s
}

// TreeSynthesizerOption 树状合成器选项
type TreeSynthesizerOption func(*TreeSummarizeSynthesizer)

// WithTreeSynthesizerName 设置合成器名称
func WithTreeSynthesizerName(name string) TreeSynthesizerOption {
	return func(s *TreeSummarizeSynthesizer) {
		s.name = name
	}
}

// WithTreeSynthesizerChunkSize 设置每组文档数量
// 注意：chunkSize 最小为 2，否则会导致无限循环
func WithTreeSynthesizerChunkSize(n int) TreeSynthesizerOption {
	return func(s *TreeSummarizeSynthesizer) {
		if n < 2 {
			n = 2
		}
		s.chunkSize = n
	}
}

// WithTreeSynthesizerLLM 设置 LLM 提供者
func WithTreeSynthesizerLLM(provider llm.Provider) TreeSynthesizerOption {
	return func(s *TreeSummarizeSynthesizer) {
		s.llm = provider
	}
}

// Name 返回合成器名称
func (s *TreeSummarizeSynthesizer) Name() string {
	return s.name
}

// Synthesize 合成响应
// 分层递归摘要，最终合并为一个答案
func (s *TreeSummarizeSynthesizer) Synthesize(ctx context.Context, query string, docs []rag.Document, opts ...SynthesizeOption) (*Response, error) {
	if len(docs) == 0 {
		return &Response{
			Content:  "没有找到相关信息来回答您的问题。",
			Metadata: map[string]any{"strategy": "tree_summarize", "doc_count": 0},
		}, nil
	}

	config := &synthesizeConfig{
		maxTokens:     1024,
		temperature:   0.7,
		includeSource: true,
	}
	for _, opt := range opts {
		opt(config)
	}

	// 如果没有 LLM 提供者，返回占位响应
	if s.llm == nil {
		var contents []string
		for _, doc := range docs {
			contents = append(contents, doc.Content)
		}
		return &Response{
			Content: fmt.Sprintf("基于 %d 个文档的回答（需要配置 LLM）:\n\n%s\n\n问题: %s",
				len(docs), strings.Join(contents, "\n---\n"), query),
			Metadata:        map[string]any{"strategy": "tree_summarize", "doc_count": len(docs)},
			SourceDocuments: docs,
		}, nil
	}

	// 树状递归摘要：将文档分组摘要，然后递归合并
	summaries := make([]string, len(docs))
	for i, doc := range docs {
		summaries[i] = doc.Content
	}

	var totalTokens int
	level := 0

	// 递归合并直到只剩一个摘要
	for len(summaries) > 1 {
		level++
		var newSummaries []string

		for i := 0; i < len(summaries); i += s.chunkSize {
			end := i + s.chunkSize
			if end > len(summaries) {
				end = len(summaries)
			}
			chunk := summaries[i:end]

			prompt := fmt.Sprintf(`请将以下内容合并摘要：

%s

合并摘要:`, strings.Join(chunk, "\n---\n"))

			resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
				Messages: []llm.Message{{Role: llm.RoleUser, Content: prompt}},
			})
			if err != nil {
				return nil, fmt.Errorf("LLM 调用失败: %w", err)
			}

			newSummaries = append(newSummaries, resp.Content)
			totalTokens += resp.Usage.TotalTokens
		}

		summaries = newSummaries
	}

	// 最终用摘要回答问题
	finalPrompt := fmt.Sprintf(`基于以下摘要回答问题：

摘要: %s

问题: %s

回答:`, summaries[0], query)

	finalResp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: finalPrompt}},
	})
	if err != nil {
		return nil, fmt.Errorf("LLM 调用失败: %w", err)
	}
	totalTokens += finalResp.Usage.TotalTokens

	response := &Response{
		Content: finalResp.Content,
		Metadata: map[string]any{
			"strategy":     "tree_summarize",
			"doc_count":    len(docs),
			"chunk_size":   s.chunkSize,
			"tree_levels":  level,
			"total_tokens": totalTokens,
		},
	}

	if config.includeSource {
		response.SourceDocuments = docs
	}

	return response, nil
}

// ============== Simple Summarize Synthesizer ==============

// SimpleSummarizeSynthesizer 简单摘要合成器
// 最简单的策略，直接连接所有文档并生成摘要
type SimpleSummarizeSynthesizer struct {
	// name 合成器名称
	name string
	// llm LLM 提供者
	llm llm.Provider
}

// NewSimpleSummarizeSynthesizer 创建简单摘要合成器
func NewSimpleSummarizeSynthesizer(opts ...SimpleSynthesizerOption) *SimpleSummarizeSynthesizer {
	s := &SimpleSummarizeSynthesizer{
		name: "simple_summarize_synthesizer",
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// SimpleSynthesizerOption 简单合成器选项
type SimpleSynthesizerOption func(*SimpleSummarizeSynthesizer)

// WithSimpleSynthesizerName 设置合成器名称
func WithSimpleSynthesizerName(name string) SimpleSynthesizerOption {
	return func(s *SimpleSummarizeSynthesizer) {
		s.name = name
	}
}

// WithSimpleSynthesizerLLM 设置 LLM 提供者
func WithSimpleSynthesizerLLM(provider llm.Provider) SimpleSynthesizerOption {
	return func(s *SimpleSummarizeSynthesizer) {
		s.llm = provider
	}
}

// Name 返回合成器名称
func (s *SimpleSummarizeSynthesizer) Name() string {
	return s.name
}

// Synthesize 合成响应
// 直接合并所有文档内容生成回答
func (s *SimpleSummarizeSynthesizer) Synthesize(ctx context.Context, query string, docs []rag.Document, opts ...SynthesizeOption) (*Response, error) {
	if len(docs) == 0 {
		return &Response{
			Content:  "没有找到相关信息来回答您的问题。",
			Metadata: map[string]any{"strategy": "simple_summarize", "doc_count": 0},
		}, nil
	}

	config := &synthesizeConfig{
		maxTokens:     1024,
		temperature:   0.7,
		includeSource: true,
	}
	for _, opt := range opts {
		opt(config)
	}

	// 合并所有文档内容
	var contents []string
	for _, doc := range docs {
		contents = append(contents, doc.Content)
	}
	combinedContext := strings.Join(contents, "\n---\n")

	// 如果没有 LLM 提供者，返回占位响应
	if s.llm == nil {
		return &Response{
			Content: fmt.Sprintf("基于 %d 个文档的回答（需要配置 LLM）:\n\n%s\n\n问题: %s",
				len(docs), combinedContext, query),
			Metadata:        map[string]any{"strategy": "simple_summarize", "doc_count": len(docs)},
			SourceDocuments: docs,
		}, nil
	}

	// 构建提示词
	prompt := fmt.Sprintf(`基于以下文档内容回答问题：

文档内容:
%s

问题: %s

回答:`, combinedContext, query)

	resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: prompt}},
	})
	if err != nil {
		return nil, fmt.Errorf("LLM 调用失败: %w", err)
	}

	response := &Response{
		Content: resp.Content,
		Metadata: map[string]any{
			"strategy":     "simple_summarize",
			"doc_count":    len(docs),
			"total_tokens": resp.Usage.TotalTokens,
		},
	}

	if config.includeSource {
		response.SourceDocuments = docs
	}

	return response, nil
}

// ============== Factory ==============

// SynthesizerType 合成器类型
type SynthesizerType string

const (
	// TypeRefine 迭代精炼类型
	TypeRefine SynthesizerType = "refine"
	// TypeCompact 紧凑合成类型
	TypeCompact SynthesizerType = "compact"
	// TypeTreeSummarize 树状摘要类型
	TypeTreeSummarize SynthesizerType = "tree_summarize"
	// TypeSimpleSummarize 简单摘要类型
	TypeSimpleSummarize SynthesizerType = "simple_summarize"
)

// New 创建合成器
// 根据类型创建对应的合成器实例
func New(synthType SynthesizerType) Synthesizer {
	switch synthType {
	case TypeRefine:
		return NewRefineSynthesizer()
	case TypeCompact:
		return NewCompactSynthesizer()
	case TypeTreeSummarize:
		return NewTreeSummarizeSynthesizer()
	case TypeSimpleSummarize:
		return NewSimpleSummarizeSynthesizer()
	default:
		return NewCompactSynthesizer()
	}
}

// 确保所有合成器都实现了 Synthesizer 接口
var (
	_ Synthesizer = (*RefineSynthesizer)(nil)
	_ Synthesizer = (*CompactSynthesizer)(nil)
	_ Synthesizer = (*TreeSummarizeSynthesizer)(nil)
	_ Synthesizer = (*SimpleSummarizeSynthesizer)(nil)
)
