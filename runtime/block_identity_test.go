package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

func TestBlocksRepeatedToolCallIDUsesChronologicalResults(t *testing.T) {
	state := &State{ToolCalls: []ToolCallRecord{
		{ID: "reused", Result: ToolResult{Content: "first result", Error: "first failed", Status: ToolStatusError}},
		{ID: "reused", Result: ToolResult{Content: "second result", Status: ToolStatusSuccess}},
	}, outputMessages: []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCallRef{{ID: "reused", Name: "first"}}},
		{Role: llm.RoleTool, ToolCallID: "reused", Content: "first result"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCallRef{{ID: "reused", Name: "second"}}},
		{Role: llm.RoleTool, ToolCallID: "reused", Content: "second result"},
	}}
	data, err := json.Marshal(blocksFromRun(state))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, "first result") != 1 || strings.Count(text, "second result") != 1 || strings.Index(text, "first result") > strings.Index(text, "second result") {
		t.Fatalf("tool results were assigned to another turn: %s", text)
	}
}
