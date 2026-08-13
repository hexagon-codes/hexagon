package agent

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/schema"
	"github.com/hexagon-codes/ai-core/tool"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

// safeTool 实现 tool.Tool + ReplaySafetyAware，声明重放安全性。
type safeTool struct {
	name   string
	safety ReplaySafety
}

func (t *safeTool) Name() string                  { return t.name }
func (t *safeTool) Description() string           { return "" }
func (t *safeTool) Schema() *schema.Schema        { return &schema.Schema{Type: "object"} }
func (t *safeTool) Validate(map[string]any) error { return nil }
func (t *safeTool) Execute(context.Context, map[string]any) (tool.Result, error) {
	return tool.Result{Success: true}, nil
}
func (t *safeTool) ReplaySafety() ReplaySafety { return t.safety }

// plainTool 实现 tool.Tool 但**不**实现 ReplaySafetyAware。
type plainTool struct{ name string }

func (t *plainTool) Name() string                  { return t.name }
func (t *plainTool) Description() string           { return "" }
func (t *plainTool) Schema() *schema.Schema        { return &schema.Schema{Type: "object"} }
func (t *plainTool) Validate(map[string]any) error { return nil }
func (t *plainTool) Execute(context.Context, map[string]any) (tool.Result, error) {
	return tool.Result{Success: true}, nil
}

// TestAgentToolExecutor_SideEffectOf 验证按工具声明分类，未声明/未找到默认 Unsafe。
func TestAgentToolExecutor_SideEffectOf(t *testing.T) {
	exec := &agentToolExecutor{tools: []tool.Tool{
		&safeTool{name: "search", safety: ReplayReadOnly},
		&safeTool{name: "upsert", safety: ReplayIdempotent},
		&plainTool{name: "charge"},
	}}

	cases := map[string]agentruntime.ToolSideEffect{
		"search":  ReplayReadOnly,
		"upsert":  ReplayIdempotent,
		"charge":  agentruntime.SideEffectUnsafe, // 未声明 → 默认 Unsafe
		"missing": agentruntime.SideEffectUnsafe, // 未找到 → 默认 Unsafe
	}
	for name, want := range cases {
		if got := exec.SideEffectOf(llm.ToolCall{Name: name}); got != want {
			t.Errorf("SideEffectOf(%q) = %v, want %v", name, got, want)
		}
	}
}
