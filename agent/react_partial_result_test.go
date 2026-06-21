package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

// TestFinishRun_SurfacesPartialResultOnMaxTurns 证明：当运行时以 ErrMaxTurns 返回一个
// **非 nil 部分结果**（携带已计费用量与工具记录）时，ReAct 门面 finishRun 会把它转成
// Output 一并返回，而不是丢弃为空 Output——便于调用方恢复部分工作。
//
// 修复前：finishRun 在 err!=nil 时无条件 `return Output{}, err`，部分结果被丢弃。
func TestFinishRun_SurfacesPartialResultOnMaxTurns(t *testing.T) {
	a := NewReAct(WithName("partial"))

	partial := &agentruntime.Result{
		Content: "thinking...",
		Usage:   llm.Usage{PromptTokens: 3, CompletionTokens: 3, TotalTokens: 6},
		ToolCalls: []agentruntime.ToolCallRecord{
			{ID: "t1", Name: "noop"},
		},
	}

	out, err := a.finishRun(context.Background(), Input{Query: "hi"}, "run-partial", partial, agentruntime.ErrMaxTurns, nil)
	if !errors.Is(err, agentruntime.ErrMaxTurns) {
		t.Fatalf("want ErrMaxTurns preserved, got %v", err)
	}
	if out.Usage.TotalTokens != 6 {
		t.Fatalf("partial usage TotalTokens = %d, want 6 (billed work must survive)", out.Usage.TotalTokens)
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("partial ToolCalls = %d, want 1", len(out.ToolCalls))
	}
	if out.Metadata["run_id"] != "run-partial" {
		t.Fatalf("run_id metadata = %v, want run-partial", out.Metadata["run_id"])
	}
}

// TestFinishRun_NilResultKeepsEmptyOutputContract 证明：当没有部分结果（result==nil，
// 如 ctx 取消 / provider 错误）时，finishRun 维持返回空 Output 的旧契约。
func TestFinishRun_NilResultKeepsEmptyOutputContract(t *testing.T) {
	a := NewReAct(WithName("nil-result"))

	out, err := a.finishRun(context.Background(), Input{Query: "hi"}, "run-nil", nil, context.Canceled, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled preserved, got %v", err)
	}
	if out.Content != "" || out.ToolCalls != nil || out.Usage.TotalTokens != 0 {
		t.Fatalf("nil result must yield empty Output, got %+v", out)
	}
}
