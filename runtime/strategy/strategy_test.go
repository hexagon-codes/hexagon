package strategy

import (
	"context"
	"strings"
	"testing"

	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

// TestAll_Names 验证内置策略名称稳定且唯一。
func TestAll_Names(t *testing.T) {
	got := make(map[string]bool)
	for _, s := range All() {
		got[s.Name()] = true
	}
	for _, want := range []string{"react", "plan-execute", "reflection"} {
		if !got[want] {
			t.Errorf("缺少策略 %q", want)
		}
	}
	if len(got) != 3 {
		t.Errorf("应有 3 个唯一策略, got %d", len(got))
	}
}

// TestByName 验证按名查找。
func TestByName(t *testing.T) {
	if s, ok := ByName("plan-execute"); !ok || s.Name() != "plan-execute" {
		t.Errorf("ByName(plan-execute) = (%v,%v)", s, ok)
	}
	if _, ok := ByName("nope"); ok {
		t.Error("未知策略名应返回 false")
	}
}

// TestSystemPrefixes 验证三种策略的提示词引导：
// ReAct 无前缀；PlanExecute 引导先规划；Reflection 引导自检。
func TestSystemPrefixes(t *testing.T) {
	ctx := context.Background()
	req := hruntime.Request{}

	if p := (ReAct{}).BuildSystemPrefix(ctx, req); p != "" {
		t.Errorf("ReAct 应无系统前缀, got %q", p)
	}

	pe := PlanExecute{}.BuildSystemPrefix(ctx, req)
	if !strings.Contains(strings.ToLower(pe), "plan") {
		t.Errorf("PlanExecute 前缀应引导规划, got %q", pe)
	}

	rf := Reflection{}.BuildSystemPrefix(ctx, req)
	if !strings.Contains(strings.ToLower(rf), "self-check") && !strings.Contains(strings.ToLower(rf), "check") {
		t.Errorf("Reflection 前缀应引导自检, got %q", rf)
	}
}

// TestStrategies_ShouldContinue 三种策略默认不额外阻断循环（由 runner 的工具调用终止控制）。
func TestStrategies_ShouldContinue(t *testing.T) {
	for _, s := range All() {
		if !s.ShouldContinue(context.Background(), &hruntime.State{}) {
			t.Errorf("%s.ShouldContinue 默认应为 true（交由 runner 终止）", s.Name())
		}
	}
}
