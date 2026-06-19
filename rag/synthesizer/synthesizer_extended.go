// Package synthesizer 提供 RAG 响应合成能力
//
// 本文件实现更多合成策略，对标 LlamaIndex：
//   - Accumulate: 累积合成，将每个文档的答案累积
//   - Generation: 直接生成，不使用检索内容
//   - NoText: 无文本合成，仅返回源文档
//   - CompactAndRefine: 紧凑后精炼
//   - AsyncTreeSummarize: 异步树状摘要
//   - CustomPrompt: 自定义提示词合成
package synthesizer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/rag"
)

// ============== Accumulate Synthesizer ==============

// AccumulateSynthesizer 累积合成器
// 为每个文档单独生成答案，然后累积所有答案
type AccumulateSynthesizer struct {
	name       string
	llm        llm.Provider
	separator  string
	dedup      bool // 是否去重
	maxAnswers int  // 最大答案数量
}

// AccumulateOption 累积合成器选项
type AccumulateOption func(*AccumulateSynthesizer)

// WithAccumulateSeparator 设置答案分隔符
func WithAccumulateSeparator(sep string) AccumulateOption {
	return func(s *AccumulateSynthesizer) {
		s.separator = sep
	}
}

// WithAccumulateDedup 设置是否去重
func WithAccumulateDedup(dedup bool) AccumulateOption {
	return func(s *AccumulateSynthesizer) {
		s.dedup = dedup
	}
}

// WithAccumulateMaxAnswers 设置最大答案数量
func WithAccumulateMaxAnswers(n int) AccumulateOption {
	return func(s *AccumulateSynthesizer) {
		s.maxAnswers = n
	}
}

// WithAccumulateLLM 设置 LLM 提供者
func WithAccumulateLLM(provider llm.Provider) AccumulateOption {
	return func(s *AccumulateSynthesizer) {
		s.llm = provider
	}
}

// NewAccumulateSynthesizer 创建累积合成器
func NewAccumulateSynthesizer(opts ...AccumulateOption) *AccumulateSynthesizer {
	s := &AccumulateSynthesizer{
		name:       "accumulate_synthesizer",
		separator:  "\n\n---\n\n",
		dedup:      true,
		maxAnswers: 10,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name 返回合成器名称
func (s *AccumulateSynthesizer) Name() string {
	return s.name
}

// Synthesize 合成响应
func (s *AccumulateSynthesizer) Synthesize(ctx context.Context, query string, docs []rag.Document, opts ...SynthesizeOption) (*Response, error) {
	if len(docs) == 0 {
		return &Response{
			Content:  "没有找到相关信息来回答您的问题。",
			Metadata: map[string]any{"strategy": "accumulate", "doc_count": 0},
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

	// 限制文档数量
	processDocs := docs
	if s.maxAnswers > 0 && len(docs) > s.maxAnswers {
		processDocs = docs[:s.maxAnswers]
	}

	// 如果没有 LLM，返回文档内容
	if s.llm == nil {
		var contents []string
		for i, doc := range processDocs {
			contents = append(contents, fmt.Sprintf("【答案 %d】\n%s", i+1, doc.Content))
		}
		return &Response{
			Content:         strings.Join(contents, s.separator),
			Metadata:        map[string]any{"strategy": "accumulate", "doc_count": len(processDocs)},
			SourceDocuments: docs,
		}, nil
	}

	// 为每个文档生成答案
	var answers []string
	var totalTokens int
	seen := make(map[string]bool)

	for i, doc := range processDocs {
		prompt := fmt.Sprintf(`基于以下上下文回答问题：

上下文:
%s

问题: %s

回答:`, doc.Content, query)

		resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
			Messages: []llm.Message{{Role: llm.RoleUser, Content: prompt}},
		})
		if err != nil {
			continue // 跳过失败的
		}

		answer := resp.Content
		totalTokens += resp.Usage.TotalTokens

		// 去重
		if s.dedup {
			normalized := strings.TrimSpace(strings.ToLower(answer))
			if seen[normalized] {
				continue
			}
			seen[normalized] = true
		}

		answers = append(answers, fmt.Sprintf("【答案 %d】（来源: %s）\n%s", i+1, doc.Source, answer))
	}

	response := &Response{
		Content: strings.Join(answers, s.separator),
		Metadata: map[string]any{
			"strategy":     "accumulate",
			"doc_count":    len(processDocs),
			"answer_count": len(answers),
			"total_tokens": totalTokens,
		},
	}

	if config.includeSource {
		response.SourceDocuments = docs
	}

	return response, nil
}

var _ Synthesizer = (*AccumulateSynthesizer)(nil)

// ============== Generation Synthesizer ==============

// GenerationSynthesizer 直接生成合成器
// 不使用检索内容，仅基于 LLM 知识生成答案
type GenerationSynthesizer struct {
	name string
	llm  llm.Provider
}

// GenerationOption 直接生成合成器选项
type GenerationOption func(*GenerationSynthesizer)

// WithGenerationLLM 设置 LLM 提供者
func WithGenerationLLM(provider llm.Provider) GenerationOption {
	return func(s *GenerationSynthesizer) {
		s.llm = provider
	}
}

// NewGenerationSynthesizer 创建直接生成合成器
func NewGenerationSynthesizer(opts ...GenerationOption) *GenerationSynthesizer {
	s := &GenerationSynthesizer{
		name: "generation_synthesizer",
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name 返回合成器名称
func (s *GenerationSynthesizer) Name() string {
	return s.name
}

// Synthesize 合成响应
func (s *GenerationSynthesizer) Synthesize(ctx context.Context, query string, docs []rag.Document, opts ...SynthesizeOption) (*Response, error) {
	config := &synthesizeConfig{
		maxTokens:   1024,
		temperature: 0.7,
	}
	for _, opt := range opts {
		opt(config)
	}

	if s.llm == nil {
		return &Response{
			Content:  fmt.Sprintf("（直接生成模式，需要配置 LLM）\n\n问题: %s", query),
			Metadata: map[string]any{"strategy": "generation"},
		}, nil
	}

	resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: query}},
	})
	if err != nil {
		return nil, fmt.Errorf("LLM 调用失败: %w", err)
	}

	return &Response{
		Content: resp.Content,
		Metadata: map[string]any{
			"strategy":     "generation",
			"total_tokens": resp.Usage.TotalTokens,
		},
	}, nil
}

var _ Synthesizer = (*GenerationSynthesizer)(nil)

// ============== NoText Synthesizer ==============

// NoTextSynthesizer 无文本合成器
// 不生成文本，仅返回源文档
type NoTextSynthesizer struct {
	name string
}

// NewNoTextSynthesizer 创建无文本合成器
func NewNoTextSynthesizer() *NoTextSynthesizer {
	return &NoTextSynthesizer{
		name: "no_text_synthesizer",
	}
}

// Name 返回合成器名称
func (s *NoTextSynthesizer) Name() string {
	return s.name
}

// Synthesize 合成响应
func (s *NoTextSynthesizer) Synthesize(ctx context.Context, query string, docs []rag.Document, opts ...SynthesizeOption) (*Response, error) {
	return &Response{
		Content:         "",
		SourceDocuments: docs,
		Metadata: map[string]any{
			"strategy":  "no_text",
			"doc_count": len(docs),
		},
	}, nil
}

var _ Synthesizer = (*NoTextSynthesizer)(nil)

// ============== CompactAndRefine Synthesizer ==============

// CompactAndRefineSynthesizer 紧凑后精炼合成器
// 先紧凑文档，如果超出上下文限制则精炼
type CompactAndRefineSynthesizer struct {
	name             string
	llm              llm.Provider
	maxContextLength int
}

// CompactAndRefineOption 紧凑后精炼选项
type CompactAndRefineOption func(*CompactAndRefineSynthesizer)

// WithCompactAndRefineLLM 设置 LLM
func WithCompactAndRefineLLM(provider llm.Provider) CompactAndRefineOption {
	return func(s *CompactAndRefineSynthesizer) {
		s.llm = provider
	}
}

// WithCompactAndRefineMaxContext 设置最大上下文长度
func WithCompactAndRefineMaxContext(n int) CompactAndRefineOption {
	return func(s *CompactAndRefineSynthesizer) {
		s.maxContextLength = n
	}
}

// NewCompactAndRefineSynthesizer 创建紧凑后精炼合成器
func NewCompactAndRefineSynthesizer(opts ...CompactAndRefineOption) *CompactAndRefineSynthesizer {
	s := &CompactAndRefineSynthesizer{
		name:             "compact_and_refine_synthesizer",
		maxContextLength: 4096,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name 返回合成器名称
func (s *CompactAndRefineSynthesizer) Name() string {
	return s.name
}

// Synthesize 合成响应
func (s *CompactAndRefineSynthesizer) Synthesize(ctx context.Context, query string, docs []rag.Document, opts ...SynthesizeOption) (*Response, error) {
	if len(docs) == 0 {
		return &Response{
			Content:  "没有找到相关信息来回答您的问题。",
			Metadata: map[string]any{"strategy": "compact_and_refine", "doc_count": 0},
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

	// 计算总上下文长度（用于在 compact / refine 策略间选择）
	totalLength := 0
	for _, doc := range docs {
		totalLength += len(doc.Content)
	}

	var response *Response
	var err error

	if totalLength <= s.maxContextLength {
		// 使用 Compact 策略
		compact := NewCompactSynthesizer(
			WithCompactSynthesizerLLM(s.llm),
			WithCompactSynthesizerMaxContext(s.maxContextLength),
		)
		response, err = compact.Synthesize(ctx, query, docs, opts...)
		if response != nil && response.Metadata != nil {
			response.Metadata["actual_strategy"] = "compact"
		}
	} else {
		// 使用 Refine 策略
		refine := NewRefineSynthesizer(
			WithRefineSynthesizerLLM(s.llm),
		)
		response, err = refine.Synthesize(ctx, query, docs, opts...)
		if response != nil && response.Metadata != nil {
			response.Metadata["actual_strategy"] = "refine"
		}
	}

	if err != nil {
		return nil, err
	}

	response.Metadata["strategy"] = "compact_and_refine"
	return response, nil
}

var _ Synthesizer = (*CompactAndRefineSynthesizer)(nil)

// ============== AsyncTreeSummarize Synthesizer ==============

// AsyncTreeSummarizeSynthesizer 异步树状摘要合成器
// 并行处理文档分组，提高性能
type AsyncTreeSummarizeSynthesizer struct {
	name        string
	llm         llm.Provider
	chunkSize   int
	concurrency int
}

// AsyncTreeOption 异步树状摘要选项
type AsyncTreeOption func(*AsyncTreeSummarizeSynthesizer)

// WithAsyncTreeLLM 设置 LLM
func WithAsyncTreeLLM(provider llm.Provider) AsyncTreeOption {
	return func(s *AsyncTreeSummarizeSynthesizer) {
		s.llm = provider
	}
}

// WithAsyncTreeChunkSize 设置分组大小
func WithAsyncTreeChunkSize(n int) AsyncTreeOption {
	return func(s *AsyncTreeSummarizeSynthesizer) {
		s.chunkSize = n
	}
}

// WithAsyncTreeConcurrency 设置并发数
func WithAsyncTreeConcurrency(n int) AsyncTreeOption {
	return func(s *AsyncTreeSummarizeSynthesizer) {
		s.concurrency = n
	}
}

// NewAsyncTreeSummarizeSynthesizer 创建异步树状摘要合成器
func NewAsyncTreeSummarizeSynthesizer(opts ...AsyncTreeOption) *AsyncTreeSummarizeSynthesizer {
	s := &AsyncTreeSummarizeSynthesizer{
		name:        "async_tree_summarize_synthesizer",
		chunkSize:   5,
		concurrency: 3,
	}
	for _, opt := range opts {
		opt(s)
	}
	// chunkSize 最小值守卫：合并阶段每轮必须至少把 2 个摘要合成 1 个，
	// 才能保证 summaries 长度严格递减并最终收敛到 1。若 chunkSize<2
	// （含 0、负数），分组结果与输入等长，合并循环将无法收敛甚至死循环，
	// 故统一矫正为 2，与 TreeSummarizeSynthesizer 行为保持一致。
	if s.chunkSize < 2 {
		s.chunkSize = 2
	}
	return s
}

// Name 返回合成器名称
func (s *AsyncTreeSummarizeSynthesizer) Name() string {
	return s.name
}

// Synthesize 合成响应
func (s *AsyncTreeSummarizeSynthesizer) Synthesize(ctx context.Context, query string, docs []rag.Document, opts ...SynthesizeOption) (*Response, error) {
	if len(docs) == 0 {
		return &Response{
			Content:  "没有找到相关信息来回答您的问题。",
			Metadata: map[string]any{"strategy": "async_tree_summarize", "doc_count": 0},
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

	if s.llm == nil {
		var contents []string
		for _, doc := range docs {
			contents = append(contents, doc.Content)
		}
		return &Response{
			Content: fmt.Sprintf("基于 %d 个文档的回答（需要配置 LLM）:\n\n%s\n\n问题: %s",
				len(docs), strings.Join(contents, "\n---\n"), query),
			Metadata:        map[string]any{"strategy": "async_tree_summarize", "doc_count": len(docs)},
			SourceDocuments: docs,
		}, nil
	}

	// 并行树状摘要
	summaries := make([]string, len(docs))
	for i, doc := range docs {
		summaries[i] = doc.Content
	}

	var totalTokens int64
	level := 0
	startTime := time.Now()

	// 递归并行合并
	for len(summaries) > 1 {
		// 合并循环每轮检查 ctx.Done()：不依赖 LLM 自行感知取消，
		// 上下文取消后及时返回，避免在多层合并中继续无谓的 LLM 调用。
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		level++
		var newSummaries []string
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, s.concurrency) // 信号量控制并发

		// 分组
		var chunks [][]string
		for i := 0; i < len(summaries); i += s.chunkSize {
			end := i + s.chunkSize
			if end > len(summaries) {
				end = len(summaries)
			}
			chunks = append(chunks, summaries[i:end])
		}

		results := make([]string, len(chunks))
		errors := make([]error, len(chunks))

		for i, chunk := range chunks {
			wg.Add(1)
			go func(idx int, c []string) {
				defer wg.Done()
				sem <- struct{}{}        // 获取信号量
				defer func() { <-sem }() // 释放信号量

				prompt := fmt.Sprintf(`请将以下内容合并摘要：

%s

合并摘要:`, strings.Join(c, "\n---\n"))

				resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
					Messages: []llm.Message{{Role: llm.RoleUser, Content: prompt}},
				})
				if err != nil {
					errors[idx] = err
					return
				}

				mu.Lock()
				totalTokens += int64(resp.Usage.TotalTokens)
				mu.Unlock()

				results[idx] = resp.Content
			}(i, chunk)
		}

		wg.Wait()

		// 检查错误
		for i, err := range errors {
			if err != nil {
				return nil, fmt.Errorf("chunk %d 处理失败: %w", i, err)
			}
		}

		newSummaries = results
		summaries = newSummaries
	}

	// 最终回答
	finalPrompt := fmt.Sprintf(`基于以下摘要回答问题：

摘要: %s

问题: %s

回答:`, summaries[0], query)

	finalResp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: finalPrompt}},
	})
	if err != nil {
		return nil, fmt.Errorf("最终 LLM 调用失败: %w", err)
	}
	totalTokens += int64(finalResp.Usage.TotalTokens)

	response := &Response{
		Content: finalResp.Content,
		Metadata: map[string]any{
			"strategy":     "async_tree_summarize",
			"doc_count":    len(docs),
			"chunk_size":   s.chunkSize,
			"concurrency":  s.concurrency,
			"tree_levels":  level,
			"total_tokens": totalTokens,
			"duration_ms":  time.Since(startTime).Milliseconds(),
		},
	}

	if config.includeSource {
		response.SourceDocuments = docs
	}

	return response, nil
}

var _ Synthesizer = (*AsyncTreeSummarizeSynthesizer)(nil)

// ============== Custom Prompt Synthesizer ==============

// CustomPromptSynthesizer 自定义提示词合成器
// 支持完全自定义的提示词模板
type CustomPromptSynthesizer struct {
	name           string
	llm            llm.Provider
	promptTemplate string
	systemPrompt   string
}

// CustomPromptOption 自定义提示词选项
type CustomPromptOption func(*CustomPromptSynthesizer)

// WithCustomPromptLLM 设置 LLM
func WithCustomPromptLLM(provider llm.Provider) CustomPromptOption {
	return func(s *CustomPromptSynthesizer) {
		s.llm = provider
	}
}

// WithCustomPromptTemplate 设置提示词模板
// 支持占位符: {query}, {context}, {doc_count}
func WithCustomPromptTemplate(template string) CustomPromptOption {
	return func(s *CustomPromptSynthesizer) {
		s.promptTemplate = template
	}
}

// WithCustomSystemPrompt 设置系统提示词
func WithCustomSystemPrompt(prompt string) CustomPromptOption {
	return func(s *CustomPromptSynthesizer) {
		s.systemPrompt = prompt
	}
}

// NewCustomPromptSynthesizer 创建自定义提示词合成器
func NewCustomPromptSynthesizer(opts ...CustomPromptOption) *CustomPromptSynthesizer {
	s := &CustomPromptSynthesizer{
		name: "custom_prompt_synthesizer",
		promptTemplate: `基于以下上下文回答问题。

上下文:
{context}

问题: {query}

回答:`,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name 返回合成器名称
func (s *CustomPromptSynthesizer) Name() string {
	return s.name
}

// Synthesize 合成响应
func (s *CustomPromptSynthesizer) Synthesize(ctx context.Context, query string, docs []rag.Document, opts ...SynthesizeOption) (*Response, error) {
	config := &synthesizeConfig{
		maxTokens:     1024,
		temperature:   0.7,
		includeSource: true,
	}
	for _, opt := range opts {
		opt(config)
	}

	// 使用自定义模板（如果提供）
	template := s.promptTemplate
	if config.promptTemplate != "" {
		template = config.promptTemplate
	}

	// 合并文档内容
	var contents []string
	for _, doc := range docs {
		contents = append(contents, doc.Content)
	}
	context := strings.Join(contents, "\n---\n")

	// 替换占位符
	prompt := template
	prompt = strings.ReplaceAll(prompt, "{query}", query)
	prompt = strings.ReplaceAll(prompt, "{context}", context)
	prompt = strings.ReplaceAll(prompt, "{doc_count}", fmt.Sprintf("%d", len(docs)))

	if s.llm == nil {
		return &Response{
			Content:         prompt,
			Metadata:        map[string]any{"strategy": "custom_prompt", "doc_count": len(docs)},
			SourceDocuments: docs,
		}, nil
	}

	// 构建消息
	var messages []llm.Message
	if s.systemPrompt != "" {
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: s.systemPrompt})
	}
	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: prompt})

	resp, err := s.llm.Complete(ctx, llm.CompletionRequest{
		Messages: messages,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM 调用失败: %w", err)
	}

	response := &Response{
		Content: resp.Content,
		Metadata: map[string]any{
			"strategy":     "custom_prompt",
			"doc_count":    len(docs),
			"total_tokens": resp.Usage.TotalTokens,
		},
	}

	if config.includeSource {
		response.SourceDocuments = docs
	}

	return response, nil
}

var _ Synthesizer = (*CustomPromptSynthesizer)(nil)

// ============== 扩展工厂 ==============

// 扩展合成器类型
const (
	// TypeAccumulate 累积类型
	TypeAccumulate SynthesizerType = "accumulate"
	// TypeGeneration 直接生成类型
	TypeGeneration SynthesizerType = "generation"
	// TypeNoText 无文本类型
	TypeNoText SynthesizerType = "no_text"
	// TypeCompactAndRefine 紧凑后精炼类型
	TypeCompactAndRefine SynthesizerType = "compact_and_refine"
	// TypeAsyncTreeSummarize 异步树状摘要类型
	TypeAsyncTreeSummarize SynthesizerType = "async_tree_summarize"
	// TypeCustomPrompt 自定义提示词类型
	TypeCustomPrompt SynthesizerType = "custom_prompt"
)

// NewExtended 创建扩展合成器
func NewExtended(synthType SynthesizerType) Synthesizer {
	switch synthType {
	case TypeAccumulate:
		return NewAccumulateSynthesizer()
	case TypeGeneration:
		return NewGenerationSynthesizer()
	case TypeNoText:
		return NewNoTextSynthesizer()
	case TypeCompactAndRefine:
		return NewCompactAndRefineSynthesizer()
	case TypeAsyncTreeSummarize:
		return NewAsyncTreeSummarizeSynthesizer()
	case TypeCustomPrompt:
		return NewCustomPromptSynthesizer()
	default:
		return New(synthType)
	}
}

// AvailableStrategies 返回所有可用的合成策略
func AvailableStrategies() []SynthesizerType {
	return []SynthesizerType{
		TypeRefine,
		TypeCompact,
		TypeTreeSummarize,
		TypeSimpleSummarize,
		TypeAccumulate,
		TypeGeneration,
		TypeNoText,
		TypeCompactAndRefine,
		TypeAsyncTreeSummarize,
		TypeCustomPrompt,
	}
}
