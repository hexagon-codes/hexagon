package runtime

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/template"
)

// BUG-20260703 B5：工具轮之后的最终回答必须以 text 块进入有序内容块流。
//
// 真机现象：web_search 跑完、正文流式显示后在 finalize 一刻消失，只剩工具卡。
// 根因：runner 终态分支（resp.ToolCalls 为空）只写 state.FinalText 即 break，
// 最终 assistant 消息从不进 state.Messages —— blocksFromRun 按 Messages 重建块流，
// 于是凡用过工具的运行，Blocks 恒为 [text?, tool_use, tool_result, ...] 而无最终
// text 块；客户端以 blocks 优先渲染时正文蒸发。
// （blocks_test.go 手工构造了含最终消息的 state，测试全绿但生产组装从未产出该形状。）
//
// 本测试走**生产组装路径**（Run → 工具轮 → 终态轮 → stateResult），钉死契约：
// 最终回答文本必须出现在 Blocks 尾部的 text 块中。
func TestBug20260703_B5_FinalAnswerTextBlockAfterToolUse(t *testing.T) {
	const finalAnswer = "杭州今天 27°C，空气质量良，适合外出。"
	provider := &fakeProvider{name: "fake", responses: []*llm.CompletionResponse{
		{ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "web_search", Arguments: `{"q":"杭州天气"}`}}},
		{Content: finalAnswer},
	}}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fake", Model: "fake-model"},
		ToolExecutor:     &fakeToolExecutor{},
	})

	result, err := runner.Run(context.Background(), Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "杭州天气如何"}},
		Tools:    []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunctionDef{Name: "web_search"}}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != finalAnswer {
		t.Fatalf("Content = %q, want %q", result.Content, finalAnswer)
	}

	// 命脉断言：最终回答在块流里有 text 块（客户端 blocks 优先渲染，缺了正文即蒸发）。
	var lastText string
	for _, b := range result.Blocks {
		if b.Type == template.BlockText {
			lastText = b.Text
		}
	}
	if lastText != finalAnswer {
		t.Fatalf("B5: 最终回答未进块流（正文蒸发）。Blocks = %+v, want 尾部 text 块 = %q", result.Blocks, finalAnswer)
	}
	// 顺序保真：text 块必须在 tool_result 之后（回答在工具之后产生）。
	lastIdx := len(result.Blocks) - 1
	if result.Blocks[lastIdx].Type != template.BlockText {
		t.Fatalf("B5: 块流末位应为最终回答 text 块，got %+v", result.Blocks[lastIdx])
	}
	if err := result.Blocks.Validate(); err != nil {
		t.Fatalf("块序列非法: %v", err)
	}
}

// 无工具的单轮运行同样应产出最终 text 块（组装路径统一，不依赖客户端 content 回退）。
func TestBug20260703_B5_FinalTextBlockOnNoToolRun(t *testing.T) {
	provider := &fakeProvider{name: "fake", responses: []*llm.CompletionResponse{{Content: "你好！"}}}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fake", Model: "fake-model"},
	})
	result, err := runner.Run(context.Background(), Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Blocks) != 1 || result.Blocks[0].Type != template.BlockText || result.Blocks[0].Text != "你好！" {
		t.Fatalf("B5: 单轮运行块流应为单个最终 text 块, got %+v", result.Blocks)
	}
}
