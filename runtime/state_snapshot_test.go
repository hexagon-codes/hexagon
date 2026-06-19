package runtime

import (
	"context"
	"sync"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// TestState_Snapshot_Independent 验证快照与原 State 完全独立：原 State 后续被修改
// 不影响已取的快照。
func TestState_Snapshot_Independent(t *testing.T) {
	s := &State{
		Messages:   []llm.Message{{Content: "a"}},
		ToolCalls:  []ToolCallRecord{{ID: "t1"}},
		Attributes: map[string]any{"k": "v"},
		Turn:       1,
	}
	snap := s.Snapshot()

	// 修改原 State
	s.Messages = append(s.Messages, llm.Message{Content: "b"})
	s.ToolCalls = append(s.ToolCalls, ToolCallRecord{ID: "t2"})
	s.Attributes["k"] = "changed"
	s.Turn = 2

	if len(snap.Messages) != 1 {
		t.Errorf("Messages 应独立, got %d", len(snap.Messages))
	}
	if len(snap.ToolCalls) != 1 {
		t.Errorf("ToolCalls 应独立, got %d", len(snap.ToolCalls))
	}
	if snap.Attributes["k"] != "v" {
		t.Errorf("Attributes 应独立, got %v", snap.Attributes["k"])
	}
	if snap.Turn != 1 {
		t.Errorf("Turn 应为快照时值 1, got %d", snap.Turn)
	}
}

// TestState_Snapshot_AsyncSinkRaceFree 验证安全模式：sink 在 emit 回调内 Snapshot，
// 再另起 goroutine 异步读取副本——即使运行循环继续推进并发写 live State，也无 race。
// 用 -race 跑此用例可证明「回调内快照 + 异步读副本」是契约安全的。
func TestState_Snapshot_AsyncSinkRaceFree(t *testing.T) {
	var wg sync.WaitGroup
	sink := EventSinkFunc(func(_ context.Context, e Event) error {
		if e.State == nil {
			return nil
		}
		snap := e.State.Snapshot() // 回调内取快照（此刻运行循环阻塞在 emit，安全）
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = len(snap.Messages)
			_ = snap.Attributes["provider"]
			_ = snap.Turn
		}()
		return nil
	})

	provider := &fakeProvider{name: "fake", responses: []*llm.CompletionResponse{{Content: "hi"}}}
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fake", Model: "fake-model"},
	})
	if _, err := runner.RunWithSink(context.Background(), Request{
		ID:       "r1",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
	}, sink); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}
