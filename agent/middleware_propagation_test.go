package agent

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

// countingMW 是计数用 runtime 中间件：每次 BeforeLLM 自增，用于验证中间件确实被注入并触发。
type countingMW struct{ before int }

func (m *countingMW) BeforeLLM(context.Context, *agentruntime.State) error { m.before++; return nil }
func (m *countingMW) AfterLLM(context.Context, *agentruntime.State, *llm.CompletionResponse) error {
	return nil
}
func (m *countingMW) BeforeTool(context.Context, *agentruntime.State, llm.ToolCall) error { return nil }
func (m *countingMW) AfterTool(context.Context, *agentruntime.State, llm.ToolCall, agentruntime.ToolResult) error {
	return nil
}
func (m *countingMW) Finalize(context.Context, *agentruntime.State) error { return nil }

// contentMockProvider 返回终态内容（无 tool call），使单轮 run 直接完成。
type contentMockProvider struct{ calls int }

func (p *contentMockProvider) Name() string            { return "content-mock" }
func (p *contentMockProvider) Models() []llm.ModelInfo { return nil }
func (p *contentMockProvider) CountTokens([]llm.Message) (int, error) {
	return 0, nil
}
func (p *contentMockProvider) Complete(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.calls++
	return &llm.CompletionResponse{Content: "ok", Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 10, TotalTokens: 20}}, nil
}
func (p *contentMockProvider) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, nil
}

// TestRunCompletionWithRuntimePropagatesMiddleware 直接验证 helper 把 mws 注入底层 run。
func TestRunCompletionWithRuntimePropagatesMiddleware(t *testing.T) {
	mw := &countingMW{}
	_, err := runCompletionWithRuntime(
		context.Background(), &contentMockProvider{}, "rid",
		[]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil, mw,
	)
	if err != nil {
		t.Fatalf("runCompletionWithRuntime error = %v", err)
	}
	if mw.before != 1 {
		t.Fatalf("中间件 BeforeLLM 应被调用 1 次；got %d", mw.before)
	}
}

// TestReflectionAgentHonorsWithMiddleware 验证 WithMiddleware 经 runCompletionWithRuntime
// 路径作用到 ReflectionAgent 的内部 LLM 调用（修复前对该 agent 静默失效）。
func TestReflectionAgentHonorsWithMiddleware(t *testing.T) {
	mw := &countingMW{}
	a := NewReflection([]Option{
		WithLLM(&contentMockProvider{}),
		WithMaxIterations(2),
		WithMiddleware(mw),
	})

	if _, err := a.Run(context.Background(), Input{Query: "hi"}); err != nil {
		t.Fatalf("ReflectionAgent.Run error = %v", err)
	}
	// 至少主补全这一次 LLM 调用应触发中间件（修复前为 0）。
	if mw.before < 1 {
		t.Fatalf("WithMiddleware 应作用于 ReflectionAgent 的 LLM 调用；BeforeLLM 计数 = %d", mw.before)
	}
}
