package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/checkpoint"
)

// TestRunnerDurableSavesFinalAndResumeReturnsWithoutRerun 验证：开启 Durable 后，
// 一次完整执行会在终态持久化快照；随后用同一 RunID Resume 命中终态快照时，直接
// 返回最终结果、不再调用 Provider（不重跑）。
func TestRunnerDurableSavesFinalAndResumeReturnsWithoutRerun(t *testing.T) {
	provider := &fakeProvider{name: "fake", responses: []*llm.CompletionResponse{{Content: "hello"}}}
	cp := checkpoint.NewMemory()
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fake", Model: "fake-model"},
		Durable:          NewDurableExecution(cp),
	})

	req := Request{ID: "run-dur", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}
	result, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "hello" {
		t.Fatalf("Content = %q, want hello", result.Content)
	}
	callsAfterRun := provider.calls
	if callsAfterRun == 0 {
		t.Fatal("provider 未被调用，首跑异常")
	}

	// 终态快照应已持久化
	snap, ok, err := NewDurableExecution(cp).Load(context.Background(), "run-dur")
	if err != nil || !ok {
		t.Fatalf("Load 终态快照失败 ok=%v err=%v", ok, err)
	}
	if !snap.Final || snap.FinalText != "hello" {
		t.Fatalf("快照非预期终态: %+v", snap)
	}

	// Resume 命中终态快照 → 直接返回结果、不再调 Provider
	resumed, err := runner.Resume(context.Background(), "run-dur", req, nil)
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed.Content != "hello" {
		t.Fatalf("Resume Content = %q, want hello", resumed.Content)
	}
	if provider.calls != callsAfterRun {
		t.Fatalf("Resume 终态不应重跑 Provider；calls %d → %d", callsAfterRun, provider.calls)
	}
}

// TestRunnerResumeContinuesIncomplete 验证：命中未终态（中间步）快照时，从"快照步号+1"
// 继续状态机，调用 Provider 续跑直至终态。
func TestRunnerResumeContinuesIncomplete(t *testing.T) {
	provider := &fakeProvider{name: "fake", responses: []*llm.CompletionResponse{{Content: "resumed-final"}}}
	cp := checkpoint.NewMemory()
	durable := NewDurableExecution(cp)
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fake", Model: "fake-model"},
		Durable:          durable,
	})

	// 模拟"进程在第 0 步后中断"：直接持久化一个未终态中间快照。
	if err := durable.Save(context.Background(), Snapshot{
		RunID:    "run-inc",
		Step:     0,
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Final:    false,
	}); err != nil {
		t.Fatalf("seed 中间快照失败: %v", err)
	}

	req := Request{ID: "run-inc", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}
	result, err := runner.Resume(context.Background(), "run-inc", req, nil)
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if result.Content != "resumed-final" {
		t.Fatalf("Content = %q, want resumed-final", result.Content)
	}
	if provider.calls != 1 {
		t.Fatalf("续跑应恰好调用 Provider 一次；got %d", provider.calls)
	}

	// 续跑后应落终态快照
	snap, ok, _ := durable.Load(context.Background(), "run-inc")
	if !ok || !snap.Final {
		t.Fatalf("续跑后应有终态快照；ok=%v snap=%+v", ok, snap)
	}
}

// TestRunnerResumeErrors 验证 Resume 的错误路径。
func TestRunnerResumeErrors(t *testing.T) {
	provider := &fakeProvider{name: "fake", responses: []*llm.CompletionResponse{{Content: "x"}}}

	// 未配置 Durable → ErrNoDurable
	plain := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fake", Model: "fake-model"}})
	if _, err := plain.Resume(context.Background(), "whatever", Request{ID: "whatever"}, nil); !errors.Is(err, ErrNoDurable) {
		t.Fatalf("无 Durable 应返回 ErrNoDurable；got %v", err)
	}

	// 配置 Durable 但 runID 无快照 → ErrNoSnapshot
	durRunner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fake", Model: "fake-model"},
		Durable:          NewDurableExecution(checkpoint.NewMemory()),
	})
	if _, err := durRunner.Resume(context.Background(), "missing", Request{ID: "missing"}, nil); !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("未知 runID 应返回 ErrNoSnapshot；got %v", err)
	}
}

// TestRunnerDurableNoIDSkipsPersistence 验证：开启 Durable 但 Request.ID 为空时，
// Save 静默跳过（无可恢复标识符），执行本身仍正常完成、行为不变。
func TestRunnerDurableNoIDSkipsPersistence(t *testing.T) {
	provider := &fakeProvider{name: "fake", responses: []*llm.CompletionResponse{{Content: "ok"}}}
	cp := checkpoint.NewMemory()
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fake", Model: "fake-model"},
		Durable:          NewDurableExecution(cp),
	})

	result, err := runner.Run(context.Background(), Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "ok" {
		t.Fatalf("Content = %q, want ok", result.Content)
	}
	// 无 ID → 无任何命名空间快照
	if _, ok, _ := NewDurableExecution(cp).Load(context.Background(), ""); ok {
		t.Fatal("Request.ID 为空时不应持久化任何快照")
	}
}
