package agent

import (
	"context"
	"testing"

	"github.com/hexagon-codes/hexagon/runtime/strategy"
	"github.com/hexagon-codes/hexagon/testing/mock"
)

// TestReActAgent_WithStrategy 验证 WithStrategy 把策略接到统一 agent loop：
// 选择 PlanExecute 策略后，ReActAgent 仍能在同一回合循环上正常产出结果
// （不引入独立 loop 实现）。策略前缀的实际注入由 runtime 的 runner 测试覆盖。
func TestReActAgent_WithStrategy(t *testing.T) {
	for _, s := range strategy.All() {
		ag := NewReAct(
			WithLLM(mock.FixedProvider("done")),
			WithStrategy(s),
		)
		out, err := ag.Run(context.Background(), Input{Query: "hello"})
		if err != nil {
			t.Fatalf("策略 %s 运行失败: %v", s.Name(), err)
		}
		if out.Content == "" {
			t.Errorf("策略 %s 应产出非空结果", s.Name())
		}
	}
}

// TestReActAgent_NoStrategy 未设置策略时默认行为不变（NoopStrategy = ReAct）。
func TestReActAgent_NoStrategy(t *testing.T) {
	ag := NewReAct(WithLLM(mock.FixedProvider("ok")))
	out, err := ag.Run(context.Background(), Input{Query: "hi"})
	if err != nil || out.Content == "" {
		t.Fatalf("默认策略应正常运行, got (%q,%v)", out.Content, err)
	}
}
