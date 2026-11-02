// 产品元数据不会隐式改变公共执行核的可见工具，工具策略由宿主装配。

package runtime

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

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
func (p *guardedToolsProvider) Models() []llm.ModelInfo                  { return nil }
func (p *guardedToolsProvider) CountTokens(_ []llm.Message) (int, error) { return 0, nil }

// 用 staticSelector（同 runner_test.go 风格）模拟 provider 选择
type cronGuardSelector struct{ p *guardedToolsProvider }

func (s *cronGuardSelector) Select(_ context.Context, _ Request) (ProviderSelection, error) {
	return ProviderSelection{Provider: s.p, Name: "guarded", Model: "test"}, nil
}
func (s *cronGuardSelector) Fallback(_ context.Context, _ ProviderSelection, _ error) (ProviderSelection, error) {
	return ProviderSelection{}, nil
}

func TestProductMetadataKeepsExplicitTools(t *testing.T) {
	p := &guardedToolsProvider{}
	runner := NewRunner(Config{ProviderSelector: &cronGuardSelector{p: p}})

	tools := []llm.ToolDefinition{
		{Type: "function", Function: llm.ToolFunctionDef{Name: "fake_tool"}},
	}

	// 宿主提供了工具，即使存在产品字段也须按原合同下发。
	_, err := runner.Run(context.Background(), Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "解析定时任务"}},
		Tools:    tools,
		Metadata: map[string]any{"cron_context": true},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(p.lastTools) != 1 {
		t.Errorf("explicit tools were changed by product metadata: got %d", len(p.lastTools))
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
