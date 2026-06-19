package otel

import (
	"context"
	"testing"

	"github.com/hexagon-codes/hexagon/observe/tracer"
)

// TestApplyGenAI_SetsSpanAttributes 验证 ApplyGenAI 把 gen_ai.* 属性与 token 用量写入 span。
func TestApplyGenAI_SetsSpanAttributes(t *testing.T) {
	mt := tracer.NewMemoryTracer()
	_, span := mt.StartSpan(context.Background(), "llm-call")

	ApplyGenAI(span, GenAICall{
		System:       "openai",
		RequestModel: "gpt-4o",
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
	})
	span.End()

	spans := mt.Spans()
	if len(spans) != 1 {
		t.Fatalf("应记录 1 个 span, got %d", len(spans))
	}
	attrs := spans[0].Attributes()
	if attrs[AttrGenAISystem] != "openai" {
		t.Errorf("gen_ai.system 应为 openai, got %v", attrs[AttrGenAISystem])
	}
	if attrs[AttrGenAIRequestModel] != "gpt-4o" {
		t.Errorf("gen_ai.request.model 应为 gpt-4o, got %v", attrs[AttrGenAIRequestModel])
	}
	if attrs[AttrGenAIUsageTotalTokens] != 150 {
		t.Errorf("gen_ai.usage.total_tokens 应为 150, got %v", attrs[AttrGenAIUsageTotalTokens])
	}
}

// TestPipelineSpanTree 验证全管线 span 树：Agent 根 + LLM/Tool/Retriever 子 observation。
func TestPipelineSpanTree(t *testing.T) {
	mt := tracer.NewMemoryTracer()
	ctx := context.Background()

	ctx, root := StartAgentSpan(ctx, mt, "agent-run")
	_, llm := StartLLMSpan(ctx, mt, "llm-call")
	llm.End()
	_, tool := StartToolSpan(ctx, mt, "tool-exec")
	tool.End()
	_, ret := StartRetrieverSpan(ctx, mt, "vector-search")
	ret.End()
	root.End()

	if got := mt.Size(); got != 4 {
		t.Fatalf("应有 4 个 span（agent+llm+tool+retriever）, got %d", got)
	}

	// 坐实"树"而非扁平列表：三个子 span 的 ParentID 都应指向 agent 根 span。
	rootID := root.SpanID()
	children := 0
	for _, s := range mt.Spans() {
		if s.SpanID() == rootID {
			continue
		}
		if s.ParentID() != rootID {
			t.Errorf("子 span %q 的 ParentID=%q, 应为根 %q", s.SpanID(), s.ParentID(), rootID)
		}
		children++
	}
	if children != 3 {
		t.Errorf("应有 3 个子 span 挂在 agent 根下, got %d", children)
	}
}

// TestNewLangfuseExporter_EmptyEndpoint 验证缺端点报错。
func TestNewLangfuseExporter_EmptyEndpoint(t *testing.T) {
	if _, err := NewLangfuseExporter(LangfuseConfig{PublicKey: "pk", SecretKey: "sk"}); err == nil {
		t.Error("缺 endpoint 应报错")
	}
}

// TestBugRev0618_5_LangfuseHalfAuthRejected 回归锁定 [BUG-REV0618-5]：
// 只给 public/secret 之一时报错（此前会发 base64("pk:")，Langfuse 静默 401 难排查）。
func TestBugRev0618_5_LangfuseHalfAuthRejected(t *testing.T) {
	if _, err := NewLangfuseExporter(LangfuseConfig{Endpoint: "https://x/otel", PublicKey: "pk"}); err == nil {
		t.Error("只给 PublicKey 应报错")
	}
	if _, err := NewLangfuseExporter(LangfuseConfig{Endpoint: "https://x/otel", SecretKey: "sk"}); err == nil {
		t.Error("只给 SecretKey 应报错")
	}
}

// TestNewLangfuseExporter_Valid 验证有效配置构造出非空导出器。
func TestNewLangfuseExporter_Valid(t *testing.T) {
	exp, err := NewLangfuseExporter(LangfuseConfig{
		Endpoint:  "https://cloud.langfuse.com/api/public/otel",
		PublicKey: "pk-lf-xxx",
		SecretKey: "sk-lf-xxx",
	})
	if err != nil {
		t.Fatalf("有效配置应成功: %v", err)
	}
	if exp == nil {
		t.Error("应返回非空导出器")
	}
}
