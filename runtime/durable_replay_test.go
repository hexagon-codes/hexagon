package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/checkpoint"
)

// TestSideEffect_ReplaySafe 验证三态重放安全性判定。
func TestSideEffect_ReplaySafe(t *testing.T) {
	if SideEffectUnsafe.ReplaySafe() {
		t.Error("Unsafe 不应可重放")
	}
	if !SideEffectIdempotent.ReplaySafe() {
		t.Error("Idempotent 应可重放")
	}
	if !SideEffectReadOnly.ReplaySafe() {
		t.Error("ReadOnly 应可重放")
	}
}

// TestUnsafeNotDone 验证「在途未完成的 unsafe」检测：已完成的 unsafe 工具按 ID 跳过。
func TestUnsafeNotDone(t *testing.T) {
	allSafe := []PendingTool{
		{Call: llm.ToolCall{ID: "a"}, SideEffect: SideEffectReadOnly},
		{Call: llm.ToolCall{ID: "b"}, SideEffect: SideEffectIdempotent},
	}
	if UnsafeNotDone(allSafe, map[string]bool{}) {
		t.Error("全安全不应判 unsafe")
	}
	mixed := []PendingTool{
		{Call: llm.ToolCall{ID: "a"}, SideEffect: SideEffectReadOnly},
		{Call: llm.ToolCall{ID: "b"}, SideEffect: SideEffectUnsafe},
	}
	if !UnsafeNotDone(mixed, map[string]bool{}) {
		t.Error("含未完成 unsafe 应判 unsafe")
	}
	// 已完成的 unsafe（b 在 done 集）应跳过 → 不判 unsafe
	if UnsafeNotDone(mixed, map[string]bool{"b": true}) {
		t.Error("已完成的 unsafe 应跳过，不应判 unsafe")
	}
}

// classifyingExec 是实现 SideEffectClassifier 的测试执行器。
type classifyingExec struct{}

func (classifyingExec) Execute(context.Context, llm.ToolCall) (ToolResult, error) {
	return ToolResult{}, nil
}

func (classifyingExec) SideEffectOf(call llm.ToolCall) ToolSideEffect {
	if call.Name == "read" {
		return SideEffectReadOnly
	}
	return SideEffectUnsafe
}

// TestClassifyPending_DefaultUnsafe 执行器未实现 SideEffectClassifier → 一律 Unsafe（保守默认）。
func TestClassifyPending_DefaultUnsafe(t *testing.T) {
	pending := classifyPending(nil, []llm.ToolCall{{Name: "x"}})
	if len(pending) != 1 || pending[0].SideEffect != SideEffectUnsafe {
		t.Errorf("未声明工具应默认 Unsafe, got %+v", pending)
	}
}

// TestClassifyPending_Classifier 执行器声明时按声明分类。
func TestClassifyPending_Classifier(t *testing.T) {
	pending := classifyPending(classifyingExec{}, []llm.ToolCall{{Name: "read"}, {Name: "write"}})
	if pending[0].SideEffect != SideEffectReadOnly || pending[1].SideEffect != SideEffectUnsafe {
		t.Errorf("应按 classifier 声明分类, got %+v", pending)
	}
}

// TestResume_FailsClosedOnUnsafePendingReplay 含 Unsafe 工具的步内中断（意图快照）
// Resume 应 fail-closed，拒绝重放、不重复副作用。
func TestResume_FailsClosedOnUnsafePendingReplay(t *testing.T) {
	cp := checkpoint.NewMemory()
	durable := NewDurableExecution(cp)
	runID := "run-unsafe"

	// 预置「步内意图快照」：工具已发起、含 Unsafe，模拟崩溃在工具执行中途。
	if err := durable.Save(context.Background(), Snapshot{
		RunID:    runID,
		Step:     0,
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Pending:  []PendingTool{{Call: llm.ToolCall{ID: "c1", Name: "charge"}, SideEffect: SideEffectUnsafe}},
	}); err != nil {
		t.Fatal(err)
	}

	provider := &fakeProvider{name: "fake", responses: []*llm.CompletionResponse{{Content: "should-not-run"}}}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fake", Model: "fake-model"},
		Durable:          durable,
	})

	_, err := runner.Resume(context.Background(), runID, Request{ID: runID}, nil)
	if !errors.Is(err, ErrUnsafeReplay) {
		t.Errorf("含 Unsafe 工具的步内中断 Resume 应 fail-closed ErrUnsafeReplay, got %v", err)
	}
}

// TestResume_SafePendingReplayReruns 全部可重放安全（只读/幂等）的步内中断可重跑续跑。
func TestResume_SafePendingReplayReruns(t *testing.T) {
	cp := checkpoint.NewMemory()
	durable := NewDurableExecution(cp)
	runID := "run-safe"

	if err := durable.Save(context.Background(), Snapshot{
		RunID:    runID,
		Step:     0,
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Pending:  []PendingTool{{Call: llm.ToolCall{ID: "s1", Name: "search"}, SideEffect: SideEffectReadOnly}},
	}); err != nil {
		t.Fatal(err)
	}

	provider := &fakeProvider{name: "fake", responses: []*llm.CompletionResponse{{Content: "resumed-ok"}}}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fake", Model: "fake-model"},
		Durable:          durable,
	})

	res, err := runner.Resume(context.Background(), runID, Request{ID: runID}, nil)
	if err != nil {
		t.Fatalf("可重放安全的步内中断应能重跑续跑, got %v", err)
	}
	if res == nil || res.Content != "resumed-ok" {
		t.Errorf("重跑应产出结果, got %+v", res)
	}
}

// countingExec 记录每个工具调用 ID 的执行次数。
type countingExec struct{ runs map[string]int }

func (c *countingExec) Execute(_ context.Context, call llm.ToolCall) (ToolResult, error) {
	c.runs[call.ID]++
	return ToolResult{Content: "done-" + call.ID}, nil
}

// TestResume_SkipsCompletedTool 验证 per-tool 幂等：续跑时已完成的工具按 ID 跳过、
// 只补跑未完成的（exactly-once，不重复副作用）。
func TestResume_SkipsCompletedTool(t *testing.T) {
	cp := checkpoint.NewMemory()
	durable := NewDurableExecution(cp)
	runID := "run-dedup"
	exec := &countingExec{runs: map[string]int{}}

	// 进度快照：t1 已完成（在 ToolCalls），t2 未完成；两者均 ReadOnly（安全）。
	if err := durable.Save(context.Background(), Snapshot{
		RunID:     runID,
		Step:      0,
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		ToolCalls: []ToolCallRecord{{ID: "t1", Name: "a"}},
		Pending: []PendingTool{
			{Call: llm.ToolCall{ID: "t1", Name: "a"}, SideEffect: SideEffectReadOnly},
			{Call: llm.ToolCall{ID: "t2", Name: "b"}, SideEffect: SideEffectReadOnly},
		},
	}); err != nil {
		t.Fatal(err)
	}

	provider := &fakeProvider{name: "fake", responses: []*llm.CompletionResponse{{Content: "final"}}}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fake", Model: "fake-model"},
		Durable:          durable,
		ToolExecutor:     exec,
	})

	if _, err := runner.Resume(context.Background(), runID, Request{ID: runID}, nil); err != nil {
		t.Fatal(err)
	}
	if exec.runs["t1"] != 0 {
		t.Errorf("已完成的 t1 不应被重新执行, got %d 次", exec.runs["t1"])
	}
	if exec.runs["t2"] != 1 {
		t.Errorf("未完成的 t2 应恰好执行一次, got %d 次", exec.runs["t2"])
	}
}
