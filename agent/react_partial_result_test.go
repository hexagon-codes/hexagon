package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

// TestFinishRun_SurfacesStopReasonOnMaxTurns 证明：达到轮次上限时运行时返回 (result, nil)
// 且 result.StopReason=max_turns（不是错误，对齐 Anthropic/OpenAI stop_reason）。finishRun
// 把它转成 Output 一并返回——含 StopReason 与已计费用量/工具记录，调用方据 Output.StopReason
// 决定如何呈现（如提示「可继续」），无需 errors.Is 反查。
func TestFinishRun_SurfacesStopReasonOnMaxTurns(t *testing.T) {
	a := NewReAct(WithName("partial"))

	partial := &agentruntime.Result{
		Content: "thinking...",
		Usage:   llm.Usage{PromptTokens: 3, CompletionTokens: 3, TotalTokens: 6},
		ToolCalls: []agentruntime.ToolCallRecord{
			{ID: "t1", Name: "noop"},
		},
		StopReason: agentruntime.StopReasonMaxTurns,
	}

	out, err := a.finishRun(context.Background(), Input{Query: "hi"}, "run-partial", partial, nil, nil)
	if err != nil {
		t.Fatalf("max-turns is not an error, want nil, got %v", err)
	}
	if out.StopReason != agentruntime.StopReasonMaxTurns {
		t.Fatalf("Output.StopReason = %q, want %q", out.StopReason, agentruntime.StopReasonMaxTurns)
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

// TestFinishRun_SurfacesPartialResultOnError 证明：真错误（provider 故障等，非 max-turns）
// 携带非 nil 部分结果时，finishRun 把部分结果转成 Output 一并返回（恢复已计费工作），同时
// 保留 err。修复前：finishRun 在 err!=nil 时无条件 `return Output{}, err`，部分结果被丢弃。
func TestFinishRun_SurfacesPartialResultOnError(t *testing.T) {
	a := NewReAct(WithName("partial-err"))
	boom := errors.New("provider blew up mid-run")

	partial := &agentruntime.Result{
		Usage:     llm.Usage{TotalTokens: 6},
		ToolCalls: []agentruntime.ToolCallRecord{{ID: "t1", Name: "noop"}},
	}

	out, err := a.finishRun(context.Background(), Input{Query: "hi"}, "run-err", partial, boom, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("want underlying error preserved, got %v", err)
	}
	if out.Usage.TotalTokens != 6 || len(out.ToolCalls) != 1 {
		t.Fatalf("partial work must survive an error, got %+v", out)
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
