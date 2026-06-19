package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

// TestBudgetControl_PerRunTokenLimit 验证统一抽象的 per-run token 上限在 BeforeLLM fail-closed。
func TestBudgetControl_PerRunTokenLimit(t *testing.T) {
	bc := NewBudgetControl(BudgetControlConfig{
		Limits: BudgetLimits{MaxTokens: 10},
	})
	st := &hruntime.State{Usage: llm.Usage{TotalTokens: 1 << 20}} // 远超上限
	if err := bc.BeforeLLM(context.Background(), st); err == nil {
		t.Error("超 token 上限应 fail-closed")
	}
}

// TestBudgetControl_CrossRunCumulative 验证跨 run 累计记账经统一抽象在 AfterLLM 触发 fail-closed。
func TestBudgetControl_CrossRunCumulative(t *testing.T) {
	overBudget := errors.New("cumulative budget exceeded")
	bc := NewBudgetControl(BudgetControlConfig{
		Record: func(_ string, _ llm.Usage) error { return overBudget },
	})
	st := &hruntime.State{}
	resp := &llm.CompletionResponse{Usage: llm.Usage{TotalTokens: 100}}
	if err := bc.AfterLLM(context.Background(), st, resp); !errors.Is(err, overBudget) {
		t.Errorf("跨 run 累计超限应 fail-closed, got %v", err)
	}
}

// TestBudgetControl_AllDisabledNoop 验证未配置任何维度时各 hook 均无操作（行为不变）。
func TestBudgetControl_AllDisabledNoop(t *testing.T) {
	bc := NewBudgetControl(BudgetControlConfig{})
	st := &hruntime.State{}
	ctx := context.Background()
	if err := bc.BeforeLLM(ctx, st); err != nil {
		t.Errorf("BeforeLLM 应 no-op: %v", err)
	}
	if err := bc.AfterLLM(ctx, st, &llm.CompletionResponse{}); err != nil {
		t.Errorf("AfterLLM 应 no-op: %v", err)
	}
	if err := bc.Finalize(ctx, st); err != nil {
		t.Errorf("Finalize 应 no-op: %v", err)
	}
}
