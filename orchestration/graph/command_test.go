package graph

import (
	"context"
	"testing"
	"time"
)

// TestCommandGoto 测试 Goto 命令
func TestCommandGoto(t *testing.T) {
	cmd := Goto[MapState]("target_node")
	if cmd.Target() != "target_node" {
		t.Errorf("期望目标 target_node，实际 %s", cmd.Target())
	}
}

// TestCommandGotoEnd 测试 GotoEnd 命令
func TestCommandGotoEnd(t *testing.T) {
	cmd := GotoEnd[MapState]()
	if cmd.Target() != END {
		t.Errorf("期望目标 %s，实际 %s", END, cmd.Target())
	}
}

// TestCommandWithUpdate 测试 WithUpdate 方法
func TestCommandWithUpdate(t *testing.T) {
	cmd := Goto[MapState]("next").WithUpdate(func(s MapState) MapState {
		s.Set("key", "value")
		return s
	})

	state := MapState{}
	result := cmd.ApplyUpdates(state)
	val, ok := result.Get("key")
	if !ok || val != "value" {
		t.Errorf("ApplyUpdates 后期望 key=value")
	}
}

// TestCommandWithState 测试 WithState 方法
func TestCommandWithState(t *testing.T) {
	newState := MapState{}
	newState.Set("count", 42)

	cmd := Goto[MapState]("next").WithState(newState)

	state := MapState{}
	state.Set("old", "data")

	result := cmd.ApplyUpdates(state)
	val, ok := result.Get("count")
	if !ok || val != 42 {
		t.Errorf("WithState 后期望 count=42")
	}
}

// TestUpdateAndGoto 测试 UpdateAndGoto 便捷函数
func TestUpdateAndGoto(t *testing.T) {
	cmd := UpdateAndGoto[MapState]("next_step", func(s MapState) MapState {
		s.Set("status", "done")
		return s
	})
	if cmd.Target() != "next_step" {
		t.Errorf("期望目标 next_step，实际 %s", cmd.Target())
	}

	state := MapState{}
	result := cmd.ApplyUpdates(state)
	val, _ := result.Get("status")
	if val != "done" {
		t.Errorf("期望 status=done")
	}
}

// TestGotoIf 测试 GotoIf 便捷函数
func TestGotoIf(t *testing.T) {
	t.Run("条件为真", func(t *testing.T) {
		cmd := GotoIf[MapState](true, "branch_true", "branch_false")
		if cmd.Target() != "branch_true" {
			t.Errorf("期望 branch_true，实际 %s", cmd.Target())
		}
	})

	t.Run("条件为假", func(t *testing.T) {
		cmd := GotoIf[MapState](false, "branch_true", "branch_false")
		if cmd.Target() != "branch_false" {
			t.Errorf("期望 branch_false，实际 %s", cmd.Target())
		}
	})
}

// TestGotoSwitch 测试 GotoSwitch 便捷函数
func TestGotoSwitch(t *testing.T) {
	cases := map[string]string{
		"error":   "handle_error",
		"success": "handle_success",
	}

	t.Run("匹配到分支", func(t *testing.T) {
		cmd := GotoSwitch[MapState]("success", cases)
		if cmd.Target() != "handle_success" {
			t.Errorf("期望 handle_success，实际 %s", cmd.Target())
		}
	})

	t.Run("未匹配到使用 END", func(t *testing.T) {
		cmd := GotoSwitch[MapState]("unknown", cases)
		if cmd.Target() != END {
			t.Errorf("期望 %s，实际 %s", END, cmd.Target())
		}
	})
}

// TestAddCommandNode 测试在图中使用 Command 节点
func TestAddCommandNode(t *testing.T) {
	ctx := context.Background()

	callCount := 0

	commandHandler := func(_ context.Context, s MapState) (*Command[MapState], error) {
		callCount++
		count, _ := s.Get("count")
		c, _ := count.(int)

		if c >= 3 {
			return GotoEnd[MapState]().WithUpdate(func(s MapState) MapState {
				s.Set("result", "done")
				return s
			}), nil
		}

		return UpdateAndGoto[MapState]("process", func(s MapState) MapState {
			s.Set("count", c+1)
			return s
		}), nil
	}

	g, err := NewGraph[MapState]("command-graph").
		WithExecutionMode(GraphModePregel).
		AddCommandNode("process", commandHandler).
		AddEdge(START, "process").
		Build()
	if err != nil {
		t.Fatal(err)
	}

	initial := MapState{}
	initial.Set("count", 0)

	result, err := g.Run(ctx, initial)
	if err != nil {
		t.Fatal(err)
	}

	finalResult, ok := result.Get("result")
	if !ok || finalResult != "done" {
		t.Errorf("期望 result=done，实际 %v", finalResult)
	}
}

func TestCommandRoutesRemainIsolatedAcrossConcurrentRuns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	enteredA := make(chan struct{})
	releaseA := make(chan struct{})
	g, err := NewGraph[MapState]("isolated-command-runs").WithExecutionMode(GraphModePregel).AddCommandNode("route", func(callCtx context.Context, s MapState) (*Command[MapState], error) {
		if s["run"] == "A" {
			return Goto[MapState]("left").WithUpdate(func(state MapState) MapState {
				close(enteredA)
				select {
				case <-releaseA:
				case <-callCtx.Done():
				}
				return state
			}), nil
		}
		return Goto[MapState]("right"), nil
	}).AddNode("left", func(_ context.Context, s MapState) (MapState, error) { s["route"] = "left"; return s, nil }).AddNode("right", func(_ context.Context, s MapState) (MapState, error) { s["route"] = "right"; return s, nil }).AddEdge(START, "route").AddEdge("left", END).AddEdge("right", END).Build()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	type outcome struct {
		state MapState
		err   error
	}
	resultA := make(chan outcome, 1)
	finishedA := make(chan struct{})
	go func() {
		defer close(finishedA)
		state, err := g.Run(ctx, MapState{"run": "A"})
		resultA <- outcome{state, err}
	}()
	t.Cleanup(func() { cancel(); <-finishedA })
	select {
	case <-enteredA:
	case <-ctx.Done():
		t.Fatal("run A did not reach its command update")
	}
	// A 已取得自己的命令后，让 B 完整结束，固定两个 Run 的交错次序。
	stateB, err := g.Run(ctx, MapState{"run": "B"})
	if err != nil {
		t.Fatal(err)
	}
	if stateB["run"] != "B" || stateB["route"] != "right" {
		t.Fatalf("run B used another command result: %v", stateB)
	}
	close(releaseA)
	var a outcome
	select {
	case a = <-resultA:
	case <-ctx.Done():
		t.Fatal("run A did not complete after release")
	}
	if a.err != nil {
		t.Fatal(a.err)
	}
	if a.state["run"] != "A" || a.state["route"] != "left" {
		t.Fatalf("run A used run B's command result: %v", a.state)
	}
}

// TestCommandWithMetadata 测试 WithMetadata 方法
func TestCommandWithMetadata(t *testing.T) {
	cmd := Goto[MapState]("next").WithMetadata("reason", "test")
	if cmd.Target() != "next" {
		t.Errorf("期望目标 next")
	}
}
