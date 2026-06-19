package agent

import (
	"context"
	"testing"

	"github.com/hexagon-codes/hexagon/checkpoint"
)

// TestReActAgentDurableRunAndResume 验证 agent 层可恢复执行闭环：WithCheckpointer 开启持久化后，
// Run 返回 run_id 并落终态快照；用该 run_id Resume 命中终态 → 直接返回结果、不重跑 Provider。
func TestReActAgentDurableRunAndResume(t *testing.T) {
	provider := &contentMockProvider{}
	cp := checkpoint.NewMemory()
	a := NewReAct(WithLLM(provider), WithMaxIterations(3), WithCheckpointer(cp))

	out, err := a.Run(context.Background(), Input{Query: "hi"})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if out.Content != "ok" {
		t.Fatalf("Content = %q, want ok", out.Content)
	}
	runID, _ := out.Metadata["run_id"].(string)
	if runID == "" {
		t.Fatal("Output.Metadata[\"run_id\"] 应非空，供 Resume 使用")
	}
	callsAfterRun := provider.calls
	if callsAfterRun == 0 {
		t.Fatal("首跑 Provider 未被调用")
	}

	// Resume 命中终态快照 → 返回结果、不再调 Provider。
	resumed, err := a.Resume(context.Background(), runID, Input{Query: "hi"})
	if err != nil {
		t.Fatalf("Resume error = %v", err)
	}
	if resumed.Content != "ok" {
		t.Fatalf("Resume Content = %q, want ok", resumed.Content)
	}
	if resumed.Metadata["run_id"] != runID {
		t.Fatalf("Resume 应回填同一 run_id；got %v", resumed.Metadata["run_id"])
	}
	if provider.calls != callsAfterRun {
		t.Fatalf("Resume 终态不应重跑 Provider；calls %d → %d", callsAfterRun, provider.calls)
	}
}

// TestReActAgentResumeWithoutCheckpointerErrors 验证未配置 WithCheckpointer 时 Resume 报错。
func TestReActAgentResumeWithoutCheckpointerErrors(t *testing.T) {
	a := NewReAct(WithLLM(&contentMockProvider{}))
	if _, err := a.Resume(context.Background(), "whatever", Input{Query: "hi"}); err == nil {
		t.Fatal("未配置 WithCheckpointer 的 Resume 应报错")
	}
}

// TestReActAgentNoCheckpointerRunsNormally 验证行为保持：不配置 WithCheckpointer 时，
// Run 正常执行、不触碰持久化（与重构前一致），且仍回填 run_id。
func TestReActAgentNoCheckpointerRunsNormally(t *testing.T) {
	provider := &contentMockProvider{}
	a := NewReAct(WithLLM(provider), WithMaxIterations(3))

	out, err := a.Run(context.Background(), Input{Query: "hi"})
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if out.Content != "ok" {
		t.Fatalf("Content = %q, want ok", out.Content)
	}
	if out.Metadata["run_id"] == "" || out.Metadata["run_id"] == nil {
		t.Fatal("run_id 应始终回填（即使未开持久化）")
	}
}
