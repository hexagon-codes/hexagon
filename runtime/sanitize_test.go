// runtime 层 root-level sanitize 守护 task #29 / BUG-20260523 的根因防御。
//
// 这些测试存在的意义：openai/anthropic 等 provider 各自实现 sanitize 太脆弱
// （anthropic 直连根本没做），runtime 在 callProvider 前统一 sanitize 一次，
// 任何 host / provider / 网关组合都能确保 outbound 序列契约级合规。
package runtime

import (
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

func TestSanitizeToolCallSequence_EmptyNoop(t *testing.T) {
	if out := sanitizeToolCallSequence(nil); len(out) != 0 {
		t.Errorf("nil 应返空，得到 %d", len(out))
	}
	if out := sanitizeToolCallSequence([]llm.Message{}); len(out) != 0 {
		t.Errorf("空 slice 应保持空，得到 %d", len(out))
	}
}

func TestSanitizeToolCallSequence_LegalPairKept(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCallRef{{ID: "X", Name: "f", Arguments: "{}"}}},
		{Role: llm.RoleTool, Content: "ok", ToolCallID: "X"},
	}
	out := sanitizeToolCallSequence(in)
	if len(out) != 4 {
		t.Fatalf("合法序列应保留全部 4 条，得到 %d", len(out))
	}
	if out[3].ToolCallID != "X" {
		t.Errorf("tool message 应保留 ToolCallID=X，得到 %q", out[3].ToolCallID)
	}
}

func TestSanitizeToolCallSequence_OrphanToolDropped(t *testing.T) {
	// 复现 task #29 / BUG-20260523：assistant.ToolCalls 丢失只剩 tool message。
	// 模拟 session 反序列化丢字段的场景。
	in := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "创建定时任务"},
		// 没有前序 assistant + ToolCalls
		{Role: llm.RoleTool, Content: "Error executing tool", ToolCallID: "toolu_orphan"},
	}
	out := sanitizeToolCallSequence(in)
	if len(out) != 2 {
		t.Fatalf("孤立 tool 应被剥离，期望 2 条，得到 %d", len(out))
	}
	for _, m := range out {
		if m.Role == llm.RoleTool {
			t.Errorf("仍含孤立 tool message: %+v", m)
		}
	}
}

func TestSanitizeToolCallSequence_ToolWithoutIDDropped(t *testing.T) {
	// 没有 ToolCallID 的 tool message 无法对账，必丢
	in := []llm.Message{
		{Role: llm.RoleUser, Content: "x"},
		{Role: llm.RoleTool, Content: "no id here"},
	}
	out := sanitizeToolCallSequence(in)
	if len(out) != 1 {
		t.Errorf("无 ID 的 tool 应被剥离，期望 1 条，得到 %d", len(out))
	}
}

func TestSanitizeToolCallSequence_PartialMatchInMultiToolCall(t *testing.T) {
	// assistant 一次发 2 个 tool_call，其中 1 个 tool result 用了未声明的 ID
	in := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCallRef{
			{ID: "A", Name: "f1"},
			{ID: "B", Name: "f2"},
		}},
		{Role: llm.RoleTool, Content: "ok-A", ToolCallID: "A"},
		{Role: llm.RoleTool, Content: "ghost", ToolCallID: "GHOST"},
		{Role: llm.RoleTool, Content: "ok-B", ToolCallID: "B"},
	}
	out := sanitizeToolCallSequence(in)
	if len(out) != 3 {
		t.Fatalf("期望 assistant + 2 个 legit tool，得到 %d", len(out))
	}
	for _, m := range out {
		if m.Role == llm.RoleTool && m.ToolCallID == "GHOST" {
			t.Errorf("GHOST 应被剥离: %+v", m)
		}
	}
}

func TestSanitizeToolCallSequence_AssistantToolCallsAccumulateAcrossTurns(t *testing.T) {
	// 多轮场景：tool_use 在 turn0 声明，turn2 的 user/assistant 不应该重置集合
	in := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCallRef{{ID: "A"}}},
		{Role: llm.RoleTool, Content: "ok", ToolCallID: "A"},
		{Role: llm.RoleAssistant, Content: "再来一下"},
		{Role: llm.RoleUser, Content: "确认"},
		// turn 2 重新引用 A 的 tool（罕见但合法）
		{Role: llm.RoleTool, Content: "again", ToolCallID: "A"},
	}
	out := sanitizeToolCallSequence(in)
	if len(out) != 5 {
		t.Errorf("跨轮 ID 应保留可见性，期望 5 条全保留，得到 %d", len(out))
	}
}

func TestSanitizeToolCallSequence_AssistantPreservedEvenWhenToolCallEmpty(t *testing.T) {
	// 普通 assistant 文本消息（无 ToolCalls）必须保留
	in := []llm.Message{
		{Role: llm.RoleAssistant, Content: "hi there"},
		{Role: llm.RoleUser, Content: "ok"},
	}
	out := sanitizeToolCallSequence(in)
	if len(out) != 2 {
		t.Errorf("普通 assistant 不应被影响，期望 2 条，得到 %d", len(out))
	}
}

func TestSanitizeToolCallSequence_PureFunctionNoMutation(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleTool, Content: "ghost", ToolCallID: "X"},
	}
	original := append([]llm.Message(nil), in...)
	_ = sanitizeToolCallSequence(in)
	if len(in) != len(original) || in[0].ToolCallID != original[0].ToolCallID {
		t.Errorf("sanitize 不应改原 slice")
	}
}
