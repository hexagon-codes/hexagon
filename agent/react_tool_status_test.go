package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/schema"
	"github.com/hexagon-codes/ai-core/tool"

	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

// statusTestTool 是可定制执行行为的测试工具。
type statusTestTool struct {
	name string
	fn   func(context.Context, map[string]any) (tool.Result, error)
}

func (t *statusTestTool) Name() string                  { return t.name }
func (t *statusTestTool) Description() string           { return "" }
func (t *statusTestTool) Schema() *schema.Schema        { return &schema.Schema{Type: "object"} }
func (t *statusTestTool) Validate(map[string]any) error { return nil }
func (t *statusTestTool) Execute(ctx context.Context, a map[string]any) (tool.Result, error) {
	return t.fn(ctx, a)
}

// TestAgentToolExecutor_StatusAndDuration 钉死「执行真相一等化」下沉：
// 框架在执行点产出 Status / DurationMs，客户端无需嗅探结果正文。
func TestAgentToolExecutor_StatusAndDuration(t *testing.T) {
	exec := &agentToolExecutor{tools: []tool.Tool{
		&statusTestTool{name: "ok", fn: func(context.Context, map[string]any) (tool.Result, error) {
			time.Sleep(5 * time.Millisecond) // 让耗时可测
			return tool.NewResult("done"), nil
		}},
		&statusTestTool{name: "soft_fail", fn: func(context.Context, map[string]any) (tool.Result, error) {
			return tool.Result{Success: false, Error: "rate limited"}, nil // 无 Go error 的软失败
		}},
		&statusTestTool{name: "go_err", fn: func(context.Context, map[string]any) (tool.Result, error) {
			return tool.Result{}, errors.New("boom")
		}},
	}}

	t.Run("成功 → success + 耗时被测量", func(t *testing.T) {
		res, _ := exec.Execute(context.Background(), llm.ToolCall{Name: "ok", Arguments: "{}"})
		if res.Status != agentruntime.ToolStatusSuccess {
			t.Fatalf("Status = %q, want success", res.Status)
		}
		if res.Error != "" {
			t.Fatalf("Error = %q, want empty", res.Error)
		}
		if res.DurationMs < 1 {
			t.Fatalf("DurationMs = %d, want >= 1 (sleep 5ms)", res.DurationMs)
		}
	})

	t.Run("软失败（tool.Result.Success=false，无 Go error）→ error + 带 Error", func(t *testing.T) {
		res, _ := exec.Execute(context.Background(), llm.ToolCall{Name: "soft_fail", Arguments: "{}"})
		if res.Status != agentruntime.ToolStatusError {
			t.Fatalf("Status = %q, want error", res.Status)
		}
		if res.Error != "rate limited" {
			t.Fatalf("Error = %q, want 'rate limited'", res.Error)
		}
	})

	t.Run("Go 级 execErr → error", func(t *testing.T) {
		res, _ := exec.Execute(context.Background(), llm.ToolCall{Name: "go_err", Arguments: "{}"})
		if res.Status != agentruntime.ToolStatusError {
			t.Fatalf("Status = %q, want error", res.Status)
		}
	})

	t.Run("工具未找到 → error", func(t *testing.T) {
		res, _ := exec.Execute(context.Background(), llm.ToolCall{Name: "missing", Arguments: "{}"})
		if res.Status != agentruntime.ToolStatusError {
			t.Fatalf("Status = %q, want error", res.Status)
		}
	})

	t.Run("参数解析失败 → error", func(t *testing.T) {
		res, _ := exec.Execute(context.Background(), llm.ToolCall{Name: "ok", Arguments: "{bad json"})
		if res.Status != agentruntime.ToolStatusError {
			t.Fatalf("Status = %q, want error", res.Status)
		}
	})
}
