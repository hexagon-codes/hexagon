package runtime

import (
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/template"
)

// TestBlocksFromRun_MultiStepInterleave 钉死 P2：runtime 从本次运行的消息序列重建
// **有序内容块流**，保真多步 text↔tool 交错；输入历史被排除；tool 状态透传。
func TestBlocksFromRun_MultiStepInterleave(t *testing.T) {
	state := &State{
		runStart: 2, // 前 2 条是输入（system + user），不应进块流
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "你是助手"},
			{Role: llm.RoleUser, Content: "查杭州天气和空气质量"},
			// ── 本次运行新增（runStart 起）──
			{Role: llm.RoleAssistant, Content: "先查天气。", ToolCalls: []llm.ToolCallRef{{ID: "t1", Name: "weather", Arguments: `{"city":"杭州"}`}}},
			{Role: llm.RoleTool, ToolCallID: "t1", Content: "27°C"},
			{Role: llm.RoleAssistant, Content: "27°C，再查空气质量。", ToolCalls: []llm.ToolCallRef{{ID: "t2", Name: "aqi", Arguments: "{}"}}},
			{Role: llm.RoleTool, ToolCallID: "t2", Content: "良"},
			{Role: llm.RoleAssistant, Content: "空气质量良，适合外出。"},
		},
		ToolCalls: []ToolCallRecord{
			{ID: "t1", Name: "weather", Result: ToolResult{Content: "27°C", Status: ToolStatusSuccess}},
			{ID: "t2", Name: "aqi", Result: ToolResult{Content: "良", Status: ToolStatusSuccess}},
		},
	}

	blocks := blocksFromRun(state)

	wantTypes := []template.BlockType{
		template.BlockText, template.BlockToolUse, template.BlockToolResult,
		template.BlockText, template.BlockToolUse, template.BlockToolResult,
		template.BlockText,
	}
	if len(blocks) != len(wantTypes) {
		t.Fatalf("块数 = %d, want %d: %+v", len(blocks), len(wantTypes), blocks)
	}
	for i, want := range wantTypes {
		if blocks[i].Type != want {
			t.Fatalf("block[%d].Type = %q, want %q（交错顺序未保真）", i, blocks[i].Type, want)
		}
	}
	// 输入历史（system/user）被排除：无任何块含输入文本。
	if blocks.Text() == "" || containsBlockText(blocks, "你是助手") || containsBlockText(blocks, "查杭州天气和空气质量") {
		t.Fatalf("输入历史泄漏进块流: %q", blocks.Text())
	}
	// 夹在两工具之间的第 2 段文本如实保留。
	if blocks[3].Type != template.BlockText || blocks[3].Text != "27°C，再查空气质量。" {
		t.Fatalf("工具间文本块丢失/错位: %+v", blocks[3])
	}
	// tool 执行状态透传到 tool_result 块。
	if blocks[2].Type != template.BlockToolResult || blocks[2].Status != "success" || blocks[2].ToolUseID != "t1" {
		t.Fatalf("tool_result 状态/关联错: %+v", blocks[2])
	}
	// 结构合法（每个 tool_result 有前置配对 tool_use）。
	if err := blocks.Validate(); err != nil {
		t.Fatalf("块序列非法: %v", err)
	}
}

// 单步无工具：仅一个 text 块。
func TestBlocksFromRun_SingleTextOnly(t *testing.T) {
	state := &State{
		runStart: 1,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "你好"},
			{Role: llm.RoleAssistant, Content: "你好！有什么可以帮你？"},
		},
	}
	blocks := blocksFromRun(state)
	if len(blocks) != 1 || blocks[0].Type != template.BlockText {
		t.Fatalf("单步应仅 1 个 text 块: %+v", blocks)
	}
}

func containsBlockText(bs template.Blocks, sub string) bool {
	for _, b := range bs {
		if b.Type == template.BlockText && b.Text == sub {
			return true
		}
	}
	return false
}
