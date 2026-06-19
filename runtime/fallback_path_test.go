package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// failOrOKProvider：fail=true 时 Complete 报错，否则返回固定内容。
type failOrOKProvider struct {
	name    string
	fail    bool
	content string
}

func (p *failOrOKProvider) Name() string { return p.name }
func (p *failOrOKProvider) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	if p.fail {
		return nil, errors.New(p.name + " down")
	}
	return &llm.CompletionResponse{Content: p.content}, nil
}
func (p *failOrOKProvider) Stream(_ context.Context, _ llm.CompletionRequest) (*llm.Stream, error) {
	if p.fail {
		return nil, errors.New(p.name + " down")
	}
	return llm.NewStream(strings.NewReader(""), llm.StreamOpenAIFormat), nil
}

// fallbackSelector：Select 给主 provider，Fallback 给备用 provider。
type fallbackSelector struct {
	primary Provider
	backup  Provider
}

func (s fallbackSelector) Select(context.Context, Request) (ProviderSelection, error) {
	return ProviderSelection{Provider: s.primary, Name: "primary", Model: "m1"}, nil
}
func (s fallbackSelector) Fallback(context.Context, ProviderSelection, error) (ProviderSelection, error) {
	return ProviderSelection{Provider: s.backup, Name: "backup", Model: "m2"}, nil
}

// TestRunnerProviderFallback_SwitchesOnPrimaryFailure 覆盖此前全套测试都进不去的 fallback 分支：
// 主 provider 调用失败 → 切到备用 → 返回备用结果，并发 EventProviderFallback 事件。
func TestRunnerProviderFallback_SwitchesOnPrimaryFailure(t *testing.T) {
	primary := &failOrOKProvider{name: "primary", fail: true}
	backup := &failOrOKProvider{name: "backup", fail: false, content: "from-fallback"}

	runner := NewRunner(Config{
		ProviderSelector: fallbackSelector{primary: primary, backup: backup},
		DefaultMaxTurns:  1,
	})

	var sawFallback bool
	var fromTo [2]string
	sink := EventSinkFunc(func(_ context.Context, e Event) error {
		if e.Type == EventProviderFallback {
			sawFallback = true
			fromTo[0], _ = e.Metadata["from"].(string)
			fromTo[1], _ = e.Metadata["to"].(string)
		}
		return nil
	})

	result, err := runner.RunWithSink(context.Background(), Request{
		ID:         "fb-1",
		Messages:   []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Limits:     Limits{MaxTurns: 1},
		StreamMode: StreamModeEvents,
	}, sink)
	if err != nil {
		t.Fatalf("主失败后应 fallback 成功，却返回错误: %v", err)
	}
	if result == nil || result.Content != "from-fallback" {
		t.Fatalf("应返回备用 provider 结果 from-fallback, got %+v", result)
	}
	if !sawFallback {
		t.Error("应发出 EventProviderFallback 事件")
	}
	if fromTo[0] != "primary" || fromTo[1] != "backup" {
		t.Errorf("fallback 事件 from/to 应为 primary→backup, got %v", fromTo)
	}
}

// TestRunnerProviderFallback_BothFail 边界：主备都失败时返回错误（不静默吞错）。
func TestRunnerProviderFallback_BothFail(t *testing.T) {
	runner := NewRunner(Config{
		ProviderSelector: fallbackSelector{
			primary: &failOrOKProvider{name: "primary", fail: true},
			backup:  &failOrOKProvider{name: "backup", fail: true},
		},
		DefaultMaxTurns: 1,
	})
	_, err := runner.RunWithSink(context.Background(), Request{
		ID:         "fb-2",
		Messages:   []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Limits:     Limits{MaxTurns: 1},
		StreamMode: StreamModeEvents,
	}, nil)
	if err == nil {
		t.Error("主备都失败应返回错误，而非静默成功")
	}
}
