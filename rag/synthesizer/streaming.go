// Package synthesizer 提供 RAG 响应合成能力
//
// streaming.go 实现流式合成（边检索边生成）：
//   - StreamingSynthesizer: 流式合成器接口
//   - IncrementalSynthesizer: 增量合成，每到达一批文档就更新答案
//   - PipelineSynthesizer: 管道式合成，检索和生成并行执行
//
// 对标 LangChain/LlamaIndex 的 Streaming Synthesis 能力。
//
// 使用示例：
//
//	synth := NewIncrementalSynthesizer(llmProvider,
//	    WithIncrementalBatchSize(3),
//	)
//
//	// 流式使用
//	stream, err := synth.SynthesizeStream(ctx, "查询", docChan)
//	for chunk := range stream {
//	    fmt.Print(chunk.Text)
//	}
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

// ============== 流式合成接口 ==============

// StreamChunk 流式输出块
type StreamChunk struct {
	// Text 文本内容
	Text string

	// IsPartial 是否为部分结果（true 表示后续可能有更新）
	IsPartial bool

	// SourceDocs 当前块使用的源文档数量
	SourceDocs int

	// Metadata 元数据
	Metadata map[string]any
}

// StreamingSynthesizer 流式合成器接口
// 支持边检索边生成的流式合成
type StreamingSynthesizer interface {
	Synthesizer

	// SynthesizeStream 流式合成
	// docChan 是一个接收文档的通道，文档会在检索过程中陆续到达
	// 返回一个输出流通道
	SynthesizeStream(ctx context.Context, query string, docChan <-chan rag.Document) (<-chan StreamChunk, error)
}

// ============== 增量合成器 ==============

// IncrementalSynthesizer 增量合成器
// 每到达一批文档就增量更新答案
type IncrementalSynthesizer struct {
	name      string
	llm       llm.Provider
	model     string
	batchSize int // 每批文档数量
	maxDocs   int // 最大使用文档数
}

// IncrementalOption 增量合成器选项
type IncrementalOption func(*IncrementalSynthesizer)

// WithIncrementalLLM 设置 LLM
func WithIncrementalLLM(provider llm.Provider) IncrementalOption {
	return func(s *IncrementalSynthesizer) {
		s.llm = provider
	}
}

// WithIncrementalModel 设置模型
func WithIncrementalModel(model string) IncrementalOption {
	return func(s *IncrementalSynthesizer) {
		s.model = model
	}
}

// WithIncrementalBatchSize 设置每批文档数量
func WithIncrementalBatchSize(size int) IncrementalOption {
	return func(s *IncrementalSynthesizer) {
		s.batchSize = size
	}
}

// WithIncrementalMaxDocs 设置最大文档数
func WithIncrementalMaxDocs(max int) IncrementalOption {
	return func(s *IncrementalSynthesizer) {
		s.maxDocs = max
	}
}

// NewIncrementalSynthesizer 创建增量合成器
func NewIncrementalSynthesizer(opts ...IncrementalOption) *IncrementalSynthesizer {
	s := &IncrementalSynthesizer{
		name:      "incremental_synthesizer",
		batchSize: 3,
		maxDocs:   20,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name 返回名称
func (s *IncrementalSynthesizer) Name() string { return s.name }

// Synthesize 同步合成（实现 Synthesizer 接口）
func (s *IncrementalSynthesizer) Synthesize(ctx context.Context, query string, docs []rag.Document) (string, error) {
	if s.llm == nil {
		return "", fmt.Errorf("LLM 未配置")
	}

	// 构建上下文
	var contextBuilder strings.Builder
	for i, doc := range docs {
		if i >= s.maxDocs {
			break
		}
		fmt.Fprintf(&contextBuilder, "[文档 %d] %s\n\n", i+1, doc.Content)
	}

	req := llm.CompletionRequest{
		Model: s.model,
		Messages: []llm.Message{
			{Role: "system", Content: "基于提供的文档回答用户问题。如果文档不足以回答，说明需要更多信息。"},
			{Role: "user", Content: fmt.Sprintf("文档:\n%s\n\n问题: %s", contextBuilder.String(), query)},
		},
		MaxTokens: 2000,
	}

	resp, err := s.llm.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// SynthesizeStream 流式增量合成
func (s *IncrementalSynthesizer) SynthesizeStream(ctx context.Context, query string, docChan <-chan rag.Document) (<-chan StreamChunk, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("LLM 未配置")
	}

	output := make(chan StreamChunk, 10)

	go func() {
		defer close(output)

		var allDocs []rag.Document
		var batch []rag.Document
		currentAnswer := ""
		iteration := 0

		for {
			select {
			case <-ctx.Done():
				return
			case doc, ok := <-docChan:
				if !ok {
					// 文档通道关闭，做最终合成
					if len(allDocs) > 0 && iteration == 0 {
						// 还没有做过合成，做一次完整合成
						answer, err := s.Synthesize(ctx, query, allDocs)
						if err == nil {
							sendChunk(ctx, output, StreamChunk{
								Text:       answer,
								IsPartial:  false,
								SourceDocs: len(allDocs),
							})
						}
					} else if currentAnswer != "" {
						// 发送最终结果
						sendChunk(ctx, output, StreamChunk{
							Text:       currentAnswer,
							IsPartial:  false,
							SourceDocs: len(allDocs),
						})
					}
					return
				}

				// 越界时停止收集：已收集文档达到 maxDocs 上限后，
				// 不再把新文档追加进 allDocs/batch。否则新文档会泄漏进
				// allDocs（导致最终 SourceDocs 计数虚高）并在 batch 中残留，
				// 却既不触发增量合成、也不会反映到最终输出，造成信息丢失。
				if len(allDocs) >= s.maxDocs {
					continue
				}

				allDocs = append(allDocs, doc)
				batch = append(batch, doc)

				// 批次满，执行增量合成
				if len(batch) >= s.batchSize {
					iteration++
					answer, err := s.synthesizeIncremental(ctx, query, allDocs, currentAnswer)
					if err != nil {
						continue
					}
					currentAnswer = answer

					sendChunk(ctx, output, StreamChunk{
						Text:       currentAnswer,
						IsPartial:  true,
						SourceDocs: len(allDocs),
						Metadata: map[string]any{
							"iteration": iteration,
						},
					})
					batch = batch[:0]
				}
			}
		}
	}()

	return output, nil
}

// synthesizeIncremental 增量合成
func (s *IncrementalSynthesizer) synthesizeIncremental(ctx context.Context, query string, docs []rag.Document, previousAnswer string) (string, error) {
	var contextBuilder strings.Builder
	for i, doc := range docs {
		fmt.Fprintf(&contextBuilder, "[文档 %d] %s\n\n", i+1, doc.Content)
	}

	prompt := fmt.Sprintf("文档:\n%s\n\n问题: %s", contextBuilder.String(), query)
	if previousAnswer != "" {
		prompt = fmt.Sprintf("之前的回答:\n%s\n\n现在有了更多文档:\n%s\n\n请根据所有文档更新回答。\n\n问题: %s",
			previousAnswer, contextBuilder.String(), query)
	}

	req := llm.CompletionRequest{
		Model: s.model,
		Messages: []llm.Message{
			{Role: "system", Content: "基于提供的文档回答用户问题。如果是更新回答，请综合新旧信息给出更完整的答案。"},
			{Role: "user", Content: prompt},
		},
		MaxTokens: 2000,
	}

	resp, err := s.llm.Complete(ctx, req)
	if err != nil {
		return previousAnswer, err
	}
	return resp.Content, nil
}

// ============== 管道式合成器 ==============

// PipelineSynthesizer 管道式合成器
// 检索和生成在不同 goroutine 中并行执行
// 检索结果通过内部缓冲传递给生成侧
type PipelineSynthesizer struct {
	name       string
	llm        llm.Provider
	model      string
	retriever  rag.Retriever
	bufferSize int
}

// PipelineOption 管道合成器选项
type PipelineOption func(*PipelineSynthesizer)

// WithPipelineLLM 设置 LLM
func WithPipelineLLM(provider llm.Provider) PipelineOption {
	return func(s *PipelineSynthesizer) {
		s.llm = provider
	}
}

// WithPipelineModel 设置模型
func WithPipelineModel(model string) PipelineOption {
	return func(s *PipelineSynthesizer) {
		s.model = model
	}
}

// WithPipelineRetriever 设置检索器
func WithPipelineRetriever(r rag.Retriever) PipelineOption {
	return func(s *PipelineSynthesizer) {
		s.retriever = r
	}
}

// WithPipelineBufferSize 设置缓冲大小
func WithPipelineBufferSize(size int) PipelineOption {
	return func(s *PipelineSynthesizer) {
		s.bufferSize = size
	}
}

// NewPipelineSynthesizer 创建管道合成器
func NewPipelineSynthesizer(opts ...PipelineOption) *PipelineSynthesizer {
	s := &PipelineSynthesizer{
		name:       "pipeline_synthesizer",
		bufferSize: 5,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Name 返回名称
func (s *PipelineSynthesizer) Name() string { return s.name }

// Synthesize 同步合成
func (s *PipelineSynthesizer) Synthesize(ctx context.Context, query string, docs []rag.Document) (string, error) {
	if s.llm == nil {
		return "", fmt.Errorf("LLM 未配置")
	}

	var contextBuilder strings.Builder
	for i, doc := range docs {
		fmt.Fprintf(&contextBuilder, "[文档 %d] %s\n\n", i+1, doc.Content)
	}

	req := llm.CompletionRequest{
		Model: s.model,
		Messages: []llm.Message{
			{Role: "system", Content: "基于提供的文档回答用户问题。"},
			{Role: "user", Content: fmt.Sprintf("文档:\n%s\n\n问题: %s", contextBuilder.String(), query)},
		},
		MaxTokens: 2000,
	}

	resp, err := s.llm.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// RetrieveAndSynthesize 管道式检索+合成
// 启动检索和合成两个并行管道：
//   - 检索管道：异步执行检索，文档到达后立即推送
//   - 合成管道：收到足够文档后开始生成，后续文档用于增量更新
func (s *PipelineSynthesizer) RetrieveAndSynthesize(ctx context.Context, query string, opts ...rag.RetrieveOption) (<-chan StreamChunk, error) {
	if s.retriever == nil {
		return nil, fmt.Errorf("检索器未配置")
	}
	if s.llm == nil {
		return nil, fmt.Errorf("LLM 未配置")
	}

	docChan := make(chan rag.Document, s.bufferSize)
	output := make(chan StreamChunk, 10)

	// 检索管道
	go func() {
		defer close(docChan)

		docs, err := s.retriever.Retrieve(ctx, query, opts...)
		if err != nil {
			return
		}

		for _, doc := range docs {
			select {
			case <-ctx.Done():
				return
			case docChan <- doc:
			}
		}
	}()

	// 合成管道
	go func() {
		defer close(output)

		var allDocs []rag.Document
		var mu sync.Mutex
		firstBatchDone := make(chan struct{})
		firstBatch := true

		// 收集文档
		go func() {
			for doc := range docChan {
				mu.Lock()
				allDocs = append(allDocs, doc)
				count := len(allDocs)
				mu.Unlock()

				// 第一批文档到达
				if firstBatch && count >= s.bufferSize {
					firstBatch = false
					close(firstBatchDone)
				}
			}
			// 如果文档不足 bufferSize 也要触发合成
			if firstBatch {
				close(firstBatchDone)
			}
		}()

		// 等待第一批文档
		select {
		case <-ctx.Done():
			return
		case <-firstBatchDone:
		}

		// 等待一小段时间让更多文档到达
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
		timer.Stop()

		// 获取所有已收集的文档
		mu.Lock()
		docs := make([]rag.Document, len(allDocs))
		copy(docs, allDocs)
		mu.Unlock()

		// 执行合成
		answer, err := s.Synthesize(ctx, query, docs)
		if err != nil {
			return
		}

		sendChunk(ctx, output, StreamChunk{
			Text:       answer,
			IsPartial:  false,
			SourceDocs: len(docs),
		})
	}()

	return output, nil
}

// ============== 辅助函数 ==============

// sendChunk 安全发送 chunk 到通道
func sendChunk(ctx context.Context, ch chan<- StreamChunk, chunk StreamChunk) {
	select {
	case <-ctx.Done():
	case ch <- chunk:
	}
}
