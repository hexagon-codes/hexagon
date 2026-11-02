package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/template"
	"github.com/hexagon-codes/ai-core/tool"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

func TestChildPanicStopsRoundRobinAndPreservesPriorUsage(t *testing.T) {
	cause := errors.New("receipt unavailable after action")
	var goodCalls, panicCalls atomic.Int32
	good := &childResultFixture{BaseAgent: NewBaseAgent(WithName("good")), run: func(context.Context, Input) (Output, error) {
		goodCalls.Add(1)
		return Output{Content: "saved", Usage: llm.Usage{TotalTokens: 9}}, nil
	}}
	bad := &childResultFixture{BaseAgent: NewBaseAgent(WithName("panic")), run: func(context.Context, Input) (Output, error) {
		panicCalls.Add(1)
		panic(cause)
	}}
	out, err := NewTeam("panic-team", WithAgents(good, bad), WithMode(TeamModeRoundRobin), WithMaxRounds(2)).Run(t.Context(), Input{})
	var failure *AgentPanicError
	if !errors.As(err, &failure) || !errors.Is(err, cause) || !llm.IsUnsafeToReplay(err) || len(failure.Stack) == 0 || failure.AgentID != bad.ID() {
		t.Fatalf("panic lost control identity: %v", err)
	}
	if goodCalls.Load() != 1 || panicCalls.Load() != 1 || len(out.Children) != 2 || out.Usage.TotalTokens != 9 || out.Children[0].Output.Content != "saved" {
		t.Fatalf("panic repeated or lost prior facts: good=%d panic=%d output=%+v", goodCalls.Load(), panicCalls.Load(), out)
	}
}

func TestChildPanicSavedClassificationPreventsReplay(t *testing.T) {
	var calls atomic.Int32
	child := &childResultFixture{BaseAgent: NewBaseAgent(WithName("panic")), run: func(context.Context, Input) (Output, error) {
		calls.Add(1)
		panic("action receipt lost")
	}}
	ctx := agentruntime.ContextWithTaskExecutor(t.Context(), &childTaskCache{saved: map[string][]byte{}})
	ctx = agentruntime.ContextWithToolInvocation(ctx, agentruntime.ToolInvocation{RunID: "parent", TaskID: "original", Activation: "initial"})
	for i := 0; i < 2; i++ {
		result, err := NewAgentTool(child).Execute(ctx, map[string]any{"query": "work"})
		saved, ok := result.Output.(ChildRunResult)
		if err == nil || !llm.IsUnsafeToReplay(err) || !ok || saved.ControlError == nil || !*saved.ControlError || saved.Child.Status != "failed" || calls.Load() != 1 {
			t.Fatalf("panic classification or original identity lost: %+v %v calls=%d", result, err, calls.Load())
		}
	}
}

type childCopyPayload struct{ Values []int }

func TestCollaborativeChildInputsDoNotShareNestedValues(t *testing.T) {
	changed := make(chan struct{})
	input := Input{Context: map[string]any{"typed": &childCopyPayload{Values: []int{7}}, "nested": map[string]any{"value": "original"}}}
	mutator := &childResultFixture{BaseAgent: NewBaseAgent(WithName("mutator")), run: func(_ context.Context, in Input) (Output, error) {
		in.Context["typed"].(*childCopyPayload).Values[0] = 99
		in.Context["nested"].(map[string]any)["value"] = "mutated"
		close(changed)
		return Output{Content: "mutator finished"}, nil
	}}
	observer := &childResultFixture{BaseAgent: NewBaseAgent(WithName("observer")), run: func(ctx context.Context, in Input) (Output, error) {
		select {
		case <-changed:
		case <-ctx.Done():
			return Output{}, ctx.Err()
		}
		if in.Context["typed"].(*childCopyPayload).Values[0] != 7 || in.Context["nested"].(map[string]any)["value"] != "original" {
			return Output{}, errors.New("sibling input was modified")
		}
		return Output{Content: "original"}, nil
	}}
	out, err := NewTeam("isolated", WithMode(TeamModeCollaborative), WithAgents(mutator, observer)).Run(t.Context(), input)
	if err != nil || len(out.Children) != 2 || out.Children[1].Output.Content != "original" || input.Context["typed"].(*childCopyPayload).Values[0] != 7 || input.Context["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("parent or sibling input changed: %+v %+v %v", input, out, err)
	}
}

func TestSequentialChildOutputRemainsIndependentAfterHandoff(t *testing.T) {
	original := Output{Content: "first", Usage: llm.Usage{TotalTokens: 3}, Metadata: map[string]any{"typed": &childCopyPayload{Values: []int{7}}},
		ToolCalls: []ToolCallRecord{{Name: "artifact", Arguments: map[string]any{"typed": []int{2}}, Result: tool.NewResult(childCopyPayload{Values: []int{5}})}},
		Blocks:    template.Blocks{template.TextBlock("original block")}, Children: []ChildResult{{Output: Output{Metadata: map[string]any{"nested": []int{4}}}}}}
	first := &childResultFixture{BaseAgent: NewBaseAgent(WithName("first")), output: original}
	second := &childResultFixture{BaseAgent: NewBaseAgent(WithName("second")), run: func(_ context.Context, in Input) (Output, error) {
		in.Context["typed"].(*childCopyPayload).Values[0] = 99
		return Output{Content: "second", Usage: llm.Usage{TotalTokens: 2}}, nil
	}}
	out, err := NewTeam("handoff", WithAgents(first, second)).Run(t.Context(), Input{})
	if err != nil {
		t.Fatal(err)
	}
	original.Metadata["typed"].(*childCopyPayload).Values[0] = 77
	original.ToolCalls[0].Arguments["typed"].([]int)[0] = 77
	original.ToolCalls[0].Result.Output.(childCopyPayload).Values[0] = 77
	original.Blocks[0].Text = "changed block"
	original.Children[0].Output.Metadata["nested"].([]int)[0] = 77
	saved := out.Children[0].Output
	if out.Usage.TotalTokens != 5 || saved.Metadata["typed"].(*childCopyPayload).Values[0] != 7 || saved.ToolCalls[0].Arguments["typed"].([]int)[0] != 2 || saved.ToolCalls[0].Result.Output.(childCopyPayload).Values[0] != 5 || saved.Blocks[0].Text != "original block" || saved.Children[0].Output.Metadata["nested"].([]int)[0] != 4 {
		t.Fatalf("completed output changed or lost types/usage: %+v", saved)
	}
}

func TestChildOutputCopyFailurePreservesReturnedFacts(t *testing.T) {
	metadata := map[string]any{"opaque": make(chan struct{}), "value": "partial"}
	calls := 0
	child := &childResultFixture{BaseAgent: NewBaseAgent(WithName("opaque")), run: func(context.Context, Input) (Output, error) {
		calls++
		return Output{Content: "partial", Usage: llm.Usage{TotalTokens: 6}, Metadata: metadata}, nil
	}}
	out, err := NewTeam("copy-error", WithAgents(child), WithMode(TeamModeRoundRobin), WithMaxRounds(2)).Run(t.Context(), Input{})
	var failure *ChildOutputCopyError
	if !errors.As(err, &failure) || !llm.IsUnsafeToReplay(err) || calls != 1 || out.Usage.TotalTokens != 6 || len(out.Children) != 1 || out.Children[0].Output.Content != "partial" {
		t.Fatalf("copy failure lost result or retried: %+v %v calls=%d", out, err, calls)
	}
	if _, changed := metadata["run_id"]; changed {
		t.Fatal("failure metadata source was modified")
	}
}
