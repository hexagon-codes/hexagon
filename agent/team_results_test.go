package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/interrupt"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

type childResultFixture struct {
	*BaseAgent
	output     Output
	failure    error
	panicOnRun bool
	run        func(context.Context, Input) (Output, error)
}

func (a *childResultFixture) Run(ctx context.Context, input Input) (Output, error) {
	if a.run != nil {
		return a.run(ctx, input)
	}
	if a.panicOnRun {
		panic("child extension failed")
	}
	return a.output, a.failure
}

// TaskExecutor 的替身只验证 Agent 的激活/结果合同，不声称覆盖存储事务或租约。
type childTaskCache struct {
	mu      sync.Mutex
	saved   map[string][]byte
	targets []string
}

func (c *childTaskCache) ExecuteTask(ctx context.Context, call agentruntime.TaskCall, run func(context.Context) (agentruntime.TaskOutcome, error)) (agentruntime.TaskOutcome, error) {
	key := call.ID + "/" + call.Activation
	c.mu.Lock()
	data, exists := c.saved[key]
	c.mu.Unlock()
	if exists {
		var outcome agentruntime.TaskOutcome
		err := json.Unmarshal(data, &outcome)
		return outcome, err
	}
	outcome, err := run(ctx)
	if err != nil {
		return outcome, err
	}
	data, err = json.Marshal(outcome)
	if err != nil {
		return outcome, err
	}
	c.mu.Lock()
	c.saved[key] = data
	c.targets = append(c.targets, call.ResumeTargets...)
	c.mu.Unlock()
	return outcome, nil
}

func TestCollaborativeTeamTargetedResumeRetainsSuccessfulChild(t *testing.T) {
	var goodCalls, waitingCalls atomic.Int32
	good := &childResultFixture{BaseAgent: NewBaseAgent(WithName("good")), run: func(context.Context, Input) (Output, error) {
		goodCalls.Add(1)
		return Output{Content: "original artifact", Usage: llm.Usage{TotalTokens: 3, CacheReadTokens: 2}}, nil
	}}
	waiting := &childResultFixture{BaseAgent: NewBaseAgent(WithName("waiting")), run: func(ctx context.Context, _ Input) (Output, error) {
		call := waitingCalls.Add(1)
		if resumed, _, _ := interrupt.GetResumeContext[any](ctx); resumed && call == 3 {
			return Output{Content: "approved", Usage: llm.Usage{TotalTokens: 7}}, nil
		}
		return Output{Content: "partial", Usage: llm.Usage{TotalTokens: 4}}, interrupt.InterruptSignalFunc(ctx, "approval required")
	}}
	team := NewTeam("resume", WithMode(TeamModeCollaborative), WithAgents(good, waiting))
	cache := &childTaskCache{saved: map[string][]byte{}}
	ctx := agentruntime.ContextWithTaskExecutor(t.Context(), cache)
	ctx = agentruntime.ContextWithToolInvocation(ctx, agentruntime.ToolInvocation{RunID: "parent", TaskID: "team-call", AttemptID: "attempt-1", Activation: "initial"})
	first, err := team.Run(ctx, Input{Query: "work"})
	signal, ok := interrupt.IsInterruptSignal(err)
	if !ok || len(signal.Subs) != 1 || first.Usage.TotalTokens != 7 {
		t.Fatalf("missing waiting facts: %+v %v", first, err)
	}
	leaf := signal.Subs[0]
	firstTarget := leaf.ID
	ctx = interrupt.PopulateResumeInfo(ctx, map[string]interrupt.Address{leaf.ID: leaf.Address}, nil)
	ctx = interrupt.Resume(ctx, leaf.ID)
	ctx = agentruntime.ContextWithToolInvocation(ctx, agentruntime.ToolInvocation{RunID: "parent", TaskID: "team-call", AttemptID: "attempt-2", Activation: "resume/" + leaf.ID})
	second, err := team.Run(ctx, Input{Query: "work"})
	signal, ok = interrupt.IsInterruptSignal(err)
	if !ok || len(signal.Subs) != 1 || second.Usage.TotalTokens != 7 || goodCalls.Load() != 1 || waitingCalls.Load() != 2 {
		t.Fatalf("first resume lost new wait: %+v %v", second, err)
	}
	leaf = signal.Subs[0]
	secondTarget := leaf.ID
	ctx = interrupt.PopulateResumeInfo(ctx, map[string]interrupt.Address{leaf.ID: leaf.Address}, nil)
	ctx = interrupt.Resume(ctx, leaf.ID)
	ctx = agentruntime.ContextWithToolInvocation(ctx, agentruntime.ToolInvocation{RunID: "parent", TaskID: "team-call", AttemptID: "attempt-3", Activation: "resume/" + leaf.ID})
	second, err = team.Run(ctx, Input{Query: "work"})
	if err != nil || len(second.Children) != 2 || second.Children[0].Output.Content != "original artifact" || second.Children[1].Output.Content != "approved" || second.Usage.TotalTokens != 10 || second.Usage.CacheReadTokens != 2 || goodCalls.Load() != 1 || waitingCalls.Load() != 3 {
		t.Fatalf("resume replayed or lost child facts: %+v err=%v calls=%d/%d", second, err, goodCalls.Load(), waitingCalls.Load())
	}
	if len(cache.targets) != 2 || cache.targets[0] != firstTarget || cache.targets[1] != secondTarget {
		t.Fatalf("resume adoption included an unexecuted or wrong target: %v", cache.targets)
	}
}

func TestCollaborativeTeamCancellationDoesNotWaitForUncooperativeChild(t *testing.T) {
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	child := &childResultFixture{BaseAgent: NewBaseAgent(WithName("uncooperative")), run: func(context.Context, Input) (Output, error) {
		close(started)
		<-release
		close(finished)
		return Output{Content: "late"}, nil
	}}
	team := NewTeam("cancel", WithMode(TeamModeCollaborative), WithAgents(child))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	t.Cleanup(func() { close(release); <-finished })
	type result struct {
		out Output
		err error
	}
	done := make(chan result, 1)
	go func() { out, err := team.Run(ctx, Input{}); done <- result{out, err} }()
	<-started
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.err, context.Canceled) || len(result.out.Children) != 1 || result.out.Children[0].Status != "cancel_requested" {
			t.Fatalf("incorrect cancellation facts: %+v %v", result.out, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("team waited for a child that ignored cancellation")
	}
}

func TestCollaborativeTeamPreservesFailedChildFacts(t *testing.T) {
	good := &childResultFixture{BaseAgent: NewBaseAgent(WithName("good")), output: Output{Content: "complete", Usage: llm.Usage{TotalTokens: 3}}}
	bad := &childResultFixture{BaseAgent: NewBaseAgent(WithName("bad")), output: Output{Content: "partial", Usage: llm.Usage{TotalTokens: 4}}, failure: errors.New("child failed")}
	team := NewTeam("partial", WithMode(TeamModeCollaborative), WithAgents(good, bad))
	out, err := team.Run(t.Context(), Input{Query: "work"})
	var failure *TeamExecutionError
	if !errors.As(err, &failure) || !failure.UnsafeToReplay() {
		t.Fatalf("error=%v", err)
	}
	if len(out.Children) != 2 || out.Children[0].AgentName != "good" || out.Children[1].AgentName != "bad" || out.Children[1].Output.Content != "partial" || out.Children[1].Error == "" || out.Usage.TotalTokens != 7 {
		t.Fatalf("child facts were lost: %+v", out)
	}
}

func TestCollaborativeTeamPanicBecomesChildFailure(t *testing.T) {
	bad := &childResultFixture{BaseAgent: NewBaseAgent(WithName("panic")), panicOnRun: true}
	team := NewTeam("panic", WithMode(TeamModeCollaborative), WithAgents(bad))
	out, err := team.Run(t.Context(), Input{})
	if err == nil || len(out.Children) != 1 || out.Children[0].Status != "failed" || !strings.Contains(out.Children[0].Error, "panicked") {
		t.Fatalf("panic became success or lost child result: output=%+v error=%v", out, err)
	}
}
