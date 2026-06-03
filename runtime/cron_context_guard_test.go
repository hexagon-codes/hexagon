// cron_context_guard_test 守卫 D1.1：
//   cron 上下文（解析/创建/编辑 cron）调 LLM 时必须强制 tools=nil，
//   从协议层根除 hexclaw 踩过的 tool_use_id 链路 400 bug。

package runtime

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

func TestIsCronContext_BooleanTrue(t *testing.T) {
	if !isCronContext(map[string]any{"cron_context": true}) {
		t.Error("bool true 应识别为 cron context")
	}
}

func TestIsCronContext_StringTrue(t *testing.T) {
	if !isCronContext(map[string]any{"cron_context": "true"}) {
		t.Error("string \"true\" 应识别为 cron context")
	}
	if !isCronContext(map[string]any{"cron_context": "1"}) {
		t.Error("string \"1\" 应识别为 cron context")
	}
}

func TestIsCronContext_AbsentOrFalse(t *testing.T) {
	if isCronContext(nil) {
		t.Error("nil metadata 不应是 cron context")
	}
	if isCronContext(map[string]any{}) {
		t.Error("空 metadata 不应是 cron context")
	}
	if isCronContext(map[string]any{"cron_context": false}) {
		t.Error("bool false 不应是 cron context")
	}
	if isCronContext(map[string]any{"other_key": "value"}) {
		t.Error("其它 key 不应是 cron context")
	}
}

// guardedToolsProvider 记录 callProvider 时实际收到的 Tools 长度
type guardedToolsProvider struct {
	lastTools []llm.ToolDefinition
}

func (p *guardedToolsProvider) Name() string { return "guarded" }
func (p *guardedToolsProvider) Complete(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.lastTools = req.Tools
	return &llm.CompletionResponse{Content: "ok"}, nil
}
func (p *guardedToolsProvider) Stream(_ context.Context, _ llm.CompletionRequest) (*llm.Stream, error) {
	return nil, nil
}
func (p *guardedToolsProvider) Models() []llm.ModelInfo               { return nil }
func (p *guardedToolsProvider) CountTokens(_ []llm.Message) (int, error) { return 0, nil }

// 用 staticSelector（同 runner_test.go 风格）模拟 provider 选择
type cronGuardSelector struct{ p *guardedToolsProvider }

func (s *cronGuardSelector) Select(_ context.Context, _ Request) (ProviderSelection, error) {
	return ProviderSelection{Provider: s.p, Name: "guarded", Model: "test"}, nil
}
func (s *cronGuardSelector) Fallback(_ context.Context, _ ProviderSelection, _ error) (ProviderSelection, error) {
	return ProviderSelection{}, nil
}

func TestCronContextGuard_ToolsForcedToNil(t *testing.T) {
	p := &guardedToolsProvider{}
	runner := NewRunner(Config{ProviderSelector: &cronGuardSelector{p: p}})

	tools := []llm.ToolDefinition{
		{Type: "function", Function: llm.ToolFunctionDef{Name: "fake_tool"}},
	}

	// 带 cron_context: true 调用，期望 Tools 被强制清空
	_, err := runner.Run(context.Background(), Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "解析定时任务"}},
		Tools:    tools,
		Metadata: map[string]any{"cron_context": true},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(p.lastTools) != 0 {
		t.Errorf("cron_context=true 时 Tools 必须被强制清空，实际收到 %d 个", len(p.lastTools))
	}
}

func TestCronContextGuard_NonCronContextKeepsTools(t *testing.T) {
	p := &guardedToolsProvider{}
	runner := NewRunner(Config{ProviderSelector: &cronGuardSelector{p: p}})

	tools := []llm.ToolDefinition{
		{Type: "function", Function: llm.ToolFunctionDef{Name: "fake_tool"}},
	}

	// 不带 cron_context 时 Tools 必须保留
	_, err := runner.Run(context.Background(), Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "普通 chat"}},
		Tools:    tools,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(p.lastTools) != 1 {
		t.Errorf("非 cron context 应保留 1 个 tool，实际 %d 个", len(p.lastTools))
	}
}
