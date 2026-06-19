package otel

// 本文件把 gen_ai 语义约定属性接进 tracer 的 Span，并提供全管线 Span 树的便捷构造：
// 一次 Agent 执行可展开为「Agent → LLM / Tool / Retriever」的子 Span 树，每类作为
// 独立 observation，便于在 Langfuse / Tempo 等后端分别分析各环节。

import (
	"context"

	"github.com/hexagon-codes/hexagon/observe/tracer"
)

// ApplyGenAI 把一次 LLM 调用的 gen_ai.* 属性写入 span（含 token 用量），
// 返回按基数分流的属性集合，便于上层把 Low 属性同时用作 metrics 标签。
//
// 遵循 C1 分流纪律：所有 gen_ai 属性都进 span（traces）；其中 Low（system/operation/
// request.model）可安全外溢到 metrics 标签，High（token 计数等）不应进标签。
func ApplyGenAI(span tracer.Span, c GenAICall) GenAIAttributes {
	attrs := NewGenAIAttributes(c)
	span.SetAttributes(attrs.All())
	if c.InputTokens > 0 || c.OutputTokens > 0 || c.TotalTokens > 0 {
		span.SetTokenUsage(tracer.TokenUsage{
			PromptTokens:     c.InputTokens,
			CompletionTokens: c.OutputTokens,
			TotalTokens:      c.TotalTokens,
		})
	}
	return attrs
}

// StartAgentSpan 在 ctx 下开启 Agent 执行根 span，作为全管线 span 树的根。
func StartAgentSpan(ctx context.Context, t tracer.Tracer, name string) (context.Context, tracer.Span) {
	return t.StartSpan(ctx, name, tracer.WithSpanKind(tracer.SpanKindAgent))
}

// StartLLMSpan 在 ctx 下开启 LLM 调用子 span（独立 observation）。
func StartLLMSpan(ctx context.Context, t tracer.Tracer, name string) (context.Context, tracer.Span) {
	return t.StartSpan(ctx, name, tracer.WithSpanKind(tracer.SpanKindLLM))
}

// StartToolSpan 在 ctx 下开启工具执行子 span（独立 observation）。
func StartToolSpan(ctx context.Context, t tracer.Tracer, name string) (context.Context, tracer.Span) {
	return t.StartSpan(ctx, name, tracer.WithSpanKind(tracer.SpanKindTool))
}

// StartRetrieverSpan 在 ctx 下开启检索/向量库子 span（独立 observation）。
func StartRetrieverSpan(ctx context.Context, t tracer.Tracer, name string) (context.Context, tracer.Span) {
	return t.StartSpan(ctx, name, tracer.WithSpanKind(tracer.SpanKindRetrieval))
}

// StartEmbeddingSpan 在 ctx 下开启嵌入子 span（独立 observation）。
func StartEmbeddingSpan(ctx context.Context, t tracer.Tracer, name string) (context.Context, tracer.Span) {
	return t.StartSpan(ctx, name, tracer.WithSpanKind(tracer.SpanKindEmbedding))
}
