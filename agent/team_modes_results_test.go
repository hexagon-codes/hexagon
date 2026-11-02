package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

func TestSequentialTeamRetainsCompletedAndFailedChildren(t *testing.T) {
	failure := errors.New("second child failed")
	good := &childResultFixture{BaseAgent: NewBaseAgent(WithName("good")), output: Output{Content: "original artifact", Usage: llm.Usage{TotalTokens: 3}}}
	bad := &childResultFixture{BaseAgent: NewBaseAgent(WithName("bad")), output: Output{Content: "partial artifact", Usage: llm.Usage{TotalTokens: 4}}, failure: failure}
	out, err := NewTeam("sequential", WithAgents(good, bad)).Run(t.Context(), Input{})
	if !errors.Is(err, failure) || !llm.IsUnsafeToReplay(err) || len(out.Children) != 2 || out.Usage.TotalTokens != 7 {
		t.Fatalf("lost partial results: %+v %v", out, err)
	}
	if out.Children[0].Output.Content != "original artifact" || out.Children[1].Status != "failed" {
		t.Fatalf("lost child facts: %+v", out.Children)
	}
}

func TestHierarchicalTeamRetainsMembersAndManagerFailures(t *testing.T) {
	memberFailure, summaryFailure := errors.New("member failed"), errors.New("summary failed")
	calls := 0
	manager := &childResultFixture{BaseAgent: NewBaseAgent(WithName("manager")), run: func(context.Context, Input) (Output, error) {
		calls++
		out := Output{Content: "manager partial", Usage: llm.Usage{TotalTokens: 2}}
		if calls == 2 {
			return out, summaryFailure
		}
		return out, nil
	}}
	good := &childResultFixture{BaseAgent: NewBaseAgent(WithName("good")), output: Output{Content: "artifact", Usage: llm.Usage{TotalTokens: 3}}}
	bad := &childResultFixture{BaseAgent: NewBaseAgent(WithName("bad")), output: Output{Content: "partial", Usage: llm.Usage{TotalTokens: 4}}, failure: memberFailure}
	out, err := NewTeam("hierarchical", WithAgents(good, bad), WithManager(manager)).Run(t.Context(), Input{})
	if !errors.Is(err, memberFailure) || !errors.Is(err, summaryFailure) || out.Usage.TotalTokens != 11 || len(out.Children) != 4 || calls != 2 {
		t.Fatalf("lost member or manager facts: %+v %v", out, err)
	}
	if out.Children[2].Status != "failed" || out.Children[3].Status != "failed" {
		t.Fatalf("failure statuses were hidden: %+v", out.Children)
	}
}

func TestRoundRobinTeamStopsUnknownErrorTree(t *testing.T) {
	unknown := &agentruntime.TaskOutcomeUnknownError{TaskID: "original", Activation: "initial"}
	calls := 0
	child := &childResultFixture{BaseAgent: NewBaseAgent(WithName("unknown")), run: func(context.Context, Input) (Output, error) {
		calls++
		return Output{Usage: llm.Usage{TotalTokens: 4}}, errors.Join(falseReplayMarker{}, unknown)
	}}
	out, err := NewTeam("unknown", WithMode(TeamModeRoundRobin), WithMaxRounds(2), WithAgents(child)).Run(t.Context(), Input{})
	if calls != 1 || !errors.Is(err, unknown) || out.Usage.TotalTokens != 4 || len(out.Children) != 1 {
		t.Fatalf("unknown result replayed or lost: calls=%d output=%+v err=%v", calls, out, err)
	}
}

func TestRoundRobinTeamAllFailuresRemainFailures(t *testing.T) {
	a := &childResultFixture{BaseAgent: NewBaseAgent(WithName("a")), output: Output{Usage: llm.Usage{TotalTokens: 3}}, failure: errors.New("a failed")}
	b := &childResultFixture{BaseAgent: NewBaseAgent(WithName("b")), output: Output{Usage: llm.Usage{TotalTokens: 4}}, failure: errors.New("b failed")}
	out, err := NewTeam("failed", WithMode(TeamModeRoundRobin), WithMaxRounds(1), WithAgents(a, b)).Run(t.Context(), Input{})
	if !errors.Is(err, a.failure) || !errors.Is(err, b.failure) || out.Usage.TotalTokens != 7 || len(out.Children) != 2 {
		t.Fatalf("all failures were hidden: %+v %v", out, err)
	}
}

func TestSerialTeamModesReuseSavedChildren(t *testing.T) {
	for _, mode := range []TeamMode{TeamModeSequential, TeamModeHierarchical, TeamModeRoundRobin} {
		t.Run(mode.String(), func(t *testing.T) {
			calls, managerCalls := 0, 0
			member := &childResultFixture{BaseAgent: NewBaseAgent(WithName("member")), run: func(context.Context, Input) (Output, error) {
				calls++
				return Output{Content: "original artifact", Usage: llm.Usage{TotalTokens: 3}}, nil
			}}
			manager := &childResultFixture{BaseAgent: NewBaseAgent(WithName("manager")), run: func(context.Context, Input) (Output, error) {
				managerCalls++
				return Output{Content: "manager output", Usage: llm.Usage{TotalTokens: 2}}, nil
			}}
			opts := []TeamOption{WithAgents(member), WithMode(mode), WithMaxRounds(2)}
			if mode == TeamModeHierarchical {
				opts = append(opts, WithManager(manager))
			}
			team := NewTeam("persistent", opts...)
			cache := &childTaskCache{saved: map[string][]byte{}}
			ctx := agentruntime.ContextWithTaskExecutor(t.Context(), cache)
			ctx = agentruntime.ContextWithToolInvocation(ctx, agentruntime.ToolInvocation{RunID: "parent", TaskID: "team", Activation: "initial"})
			first, err := team.Run(ctx, Input{Query: "work"})
			if err != nil {
				t.Fatal(err)
			}
			firstCalls, firstManagerCalls := calls, managerCalls
			second, err := team.Run(ctx, Input{Query: "work"})
			if err != nil || calls != firstCalls || managerCalls != firstManagerCalls || second.Usage != first.Usage || len(second.Children) != len(first.Children) {
				t.Fatalf("saved children replayed: %d/%d manager=%d/%d first=%+v second=%+v err=%v", firstCalls, calls, firstManagerCalls, managerCalls, first, second, err)
			}
		})
	}
}

func TestSerialTeamModesRestorePartialFacts(t *testing.T) {
	for _, mode := range []TeamMode{TeamModeHierarchical, TeamModeRoundRobin} {
		t.Run(mode.String(), func(t *testing.T) {
			calls := 0
			member := func(name string, tokens int, failure error) *childResultFixture {
				return &childResultFixture{BaseAgent: NewBaseAgent(WithName(name)), run: func(context.Context, Input) (Output, error) {
					calls++
					return Output{Content: name, Usage: llm.Usage{TotalTokens: tokens}}, failure
				}}
			}
			bad, good := member("bad", 4, errors.New("ordinary failure")), member("good", 3, nil)
			opts := []TeamOption{WithAgents(bad, good), WithMode(mode), WithMaxRounds(1)}
			if mode == TeamModeHierarchical {
				opts = append(opts, WithManager(member("manager", 2, nil)))
			}
			team := NewTeam("partial", opts...)
			ctx := agentruntime.ContextWithTaskExecutor(t.Context(), &childTaskCache{saved: map[string][]byte{}})
			ctx = agentruntime.ContextWithToolInvocation(ctx, agentruntime.ToolInvocation{RunID: "parent", TaskID: "partial-team", Activation: "initial"})
			first, err := team.Run(ctx, Input{Query: "work"})
			if err == nil {
				t.Fatal("partial failure was hidden")
			}
			firstCalls := calls
			second, err := team.Run(ctx, Input{Query: "work"})
			if err == nil || calls != firstCalls || second.Usage != first.Usage || len(second.Children) != len(first.Children) {
				t.Fatalf("restore lost saved partial facts: calls=%d/%d first=%+v second=%+v err=%v", firstCalls, calls, first, second, err)
			}
		})
	}
}

func TestRoundRobinTeamRestoredUnknownDoesNotContinue(t *testing.T) {
	calls, followingCalls := 0, 0
	unknown := &childResultFixture{BaseAgent: NewBaseAgent(WithName("unknown")), run: func(context.Context, Input) (Output, error) {
		calls++
		return Output{Usage: llm.Usage{TotalTokens: 4}}, &agentruntime.TaskOutcomeUnknownError{TaskID: "original", Activation: "initial"}
	}}
	following := &childResultFixture{BaseAgent: NewBaseAgent(WithName("following")), run: func(context.Context, Input) (Output, error) {
		followingCalls++
		return Output{}, nil
	}}
	team := NewTeam("unknown", WithMode(TeamModeRoundRobin), WithMaxRounds(2), WithAgents(unknown, following))
	ctx := agentruntime.ContextWithTaskExecutor(t.Context(), &childTaskCache{saved: map[string][]byte{}})
	ctx = agentruntime.ContextWithToolInvocation(ctx, agentruntime.ToolInvocation{RunID: "parent", TaskID: "unknown-team", Activation: "initial"})
	for i := 0; i < 2; i++ {
		out, err := team.Run(ctx, Input{})
		if err == nil || !llm.IsUnsafeToReplay(err) || calls != 1 || followingCalls != 0 || out.Usage.TotalTokens != 4 || len(out.Children) != 1 {
			t.Fatalf("unknown result was resumed as ordinary failure: calls=%d following=%d output=%+v err=%v", calls, followingCalls, out, err)
		}
	}
}
