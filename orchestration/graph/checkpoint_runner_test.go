package graph

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// ============== DefaultCheckpointRunnerConfig 测试 ==============

func TestDefaultCheckpointRunnerConfig(t *testing.T) {
	cfg := DefaultCheckpointRunnerConfig()
	if !cfg.AutoSave {
		t.Error("expected AutoSave true")
	}
	if cfg.SaveInterval != 1 {
		t.Errorf("expected SaveInterval 1, got %d", cfg.SaveInterval)
	}
	if !cfg.SaveOnError {
		t.Error("expected SaveOnError true")
	}
	if !cfg.SaveOnInterrupt {
		t.Error("expected SaveOnInterrupt true")
	}
	if cfg.MaxRetries != 0 {
		t.Errorf("expected MaxRetries 0, got %d", cfg.MaxRetries)
	}
	if cfg.RetryDelay != time.Second {
		t.Errorf("expected RetryDelay 1s, got %v", cfg.RetryDelay)
	}
}

// ============== NewCheckpointRunner 测试 ==============

func TestNewCheckpointRunner(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()

	runner := NewCheckpointRunner[TestState](g, saver, nil)
	if runner.config == nil {
		t.Fatal("expected default config")
	}
	if !runner.config.AutoSave {
		t.Error("expected default config AutoSave true")
	}
}

func TestNewCheckpointRunner_WithConfig(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()

	cfg := &CheckpointRunnerConfig{
		AutoSave:    false,
		MaxRetries:  1,
		RetryDelay:  100 * time.Millisecond,
		SaveOnError: false,
	}
	runner := NewCheckpointRunner[TestState](g, saver, cfg)
	if runner.config.AutoSave {
		t.Error("expected AutoSave false")
	}
	if runner.config.MaxRetries != 1 {
		t.Errorf("expected MaxRetries 1, got %d", runner.config.MaxRetries)
	}
}

// ============== Run 测试 ==============

func TestCheckpointRunner_Run_Basic(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()
	runner := NewCheckpointRunner[TestState](g, saver, nil)

	ctx := context.Background()
	result, err := runner.Run(ctx, "thread-1", TestState{Counter: 0, Path: ""})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.Counter != 2 {
		t.Errorf("expected counter 2, got %d", result.Counter)
	}
	if result.Path != "12" {
		t.Errorf("expected path '12', got %q", result.Path)
	}

	// 验证检查点状态
	cp := runner.GetCurrentCheckpoint()
	if cp == nil {
		t.Fatal("expected current checkpoint")
	}
	if cp.Status != CheckpointStatusCompleted {
		t.Errorf("expected completed status, got %s", cp.Status)
	}
	if cp.ThreadID != "thread-1" {
		t.Errorf("expected thread ID 'thread-1', got %q", cp.ThreadID)
	}
}

func TestCheckpointRunner_Run_WithError(t *testing.T) {
	g, err := NewGraph[TestState]("test").
		AddNode("fail", func(ctx context.Context, s TestState) (TestState, error) {
			return s, errors.New("node failed")
		}).
		AddEdge(START, "fail").
		AddEdge("fail", END).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	saver := NewMemoryEnhancedCheckpointSaver()
	cfg := &CheckpointRunnerConfig{
		AutoSave:     true,
		SaveInterval: 1,
		SaveOnError:  true,
		MaxRetries:   0, // 不重试
		RetryDelay:   time.Millisecond,
	}
	runner := NewCheckpointRunner[TestState](g, saver, cfg)

	ctx := context.Background()
	_, err = runner.Run(ctx, "thread-err", TestState{})
	if err == nil {
		t.Error("expected error from failing node")
	}
}

func TestCheckpointRunner_Run_ContextCancel(t *testing.T) {
	g, err := NewGraph[TestState]("test").
		AddNode("slow", func(ctx context.Context, s TestState) (TestState, error) {
			select {
			case <-ctx.Done():
				return s, ctx.Err()
			case <-time.After(10 * time.Second):
				return s, nil
			}
		}).
		AddEdge(START, "slow").
		AddEdge("slow", END).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	saver := NewMemoryEnhancedCheckpointSaver()
	cfg := &CheckpointRunnerConfig{
		AutoSave:        true,
		SaveInterval:    1,
		SaveOnError:     true,
		SaveOnInterrupt: true,
		MaxRetries:      0,
		RetryDelay:      time.Millisecond,
	}
	runner := NewCheckpointRunner[TestState](g, saver, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err = runner.Run(ctx, "thread-cancel", TestState{})
	if err == nil {
		t.Error("expected error from context cancellation")
	}
}

// ============== Resume 测试 ==============

func TestCheckpointRunner_Resume(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()

	// 先运行一次创建检查点
	runner1 := NewCheckpointRunner[TestState](g, saver, nil)
	ctx := context.Background()
	_, err := runner1.Run(ctx, "thread-resume", TestState{Counter: 0, Path: ""})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	cp := runner1.GetCurrentCheckpoint()
	if cp == nil {
		t.Fatal("expected checkpoint")
	}

	// 从检查点恢复（已完成的图不会再执行节点）
	runner2 := NewCheckpointRunner[TestState](g, saver, nil)
	result, err := runner2.Resume(ctx, cp.ID)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	// 已完成的检查点恢复后直接返回
	if result.Counter != 2 {
		t.Errorf("expected counter 2, got %d", result.Counter)
	}
}

func TestCheckpointRunner_Resume_NotFound(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()
	runner := NewCheckpointRunner[TestState](g, saver, nil)

	ctx := context.Background()
	_, err := runner.Resume(ctx, "nonexistent-id")
	if err == nil {
		t.Error("expected error for nonexistent checkpoint")
	}
}

// ============== ResumeFromLatest 测试 ==============

func TestCheckpointRunner_ResumeFromLatest(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()

	// 运行创建检查点
	runner1 := NewCheckpointRunner[TestState](g, saver, nil)
	ctx := context.Background()
	_, err := runner1.Run(ctx, "thread-latest", TestState{Counter: 0, Path: ""})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// 从最新检查点恢复
	runner2 := NewCheckpointRunner[TestState](g, saver, nil)
	result, err := runner2.ResumeFromLatest(ctx, "thread-latest")
	if err != nil {
		t.Fatalf("ResumeFromLatest failed: %v", err)
	}

	if result.Counter != 2 {
		t.Errorf("expected counter 2, got %d", result.Counter)
	}
}

func TestCheckpointRunner_ResumeFromLatest_NotFound(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()
	runner := NewCheckpointRunner[TestState](g, saver, nil)

	ctx := context.Background()
	_, err := runner.ResumeFromLatest(ctx, "nonexistent-thread")
	if err == nil {
		t.Error("expected error for nonexistent thread")
	}
}

// ============== Fork 测试 ==============

func TestCheckpointRunner_Fork(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()

	// 运行创建检查点
	runner1 := NewCheckpointRunner[TestState](g, saver, nil)
	ctx := context.Background()
	_, err := runner1.Run(ctx, "thread-fork", TestState{Counter: 0, Path: ""})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	cp := runner1.GetCurrentCheckpoint()

	// 从检查点分支，修改状态
	runner2 := NewCheckpointRunner[TestState](g, saver, nil)
	result, err := runner2.Fork(ctx, cp.ID, "experiment", func(s TestState) TestState {
		s.Counter = 100
		return s
	})
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}

	// 分支应以修改后的状态运行
	if result.Counter < 100 {
		t.Errorf("expected counter >= 100, got %d", result.Counter)
	}
}

func TestCheckpointRunner_Fork_NilModifier(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()

	runner1 := NewCheckpointRunner[TestState](g, saver, nil)
	ctx := context.Background()
	_, err := runner1.Run(ctx, "thread-fork2", TestState{Counter: 0, Path: ""})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	cp := runner1.GetCurrentCheckpoint()

	// nil modifier 不修改状态
	runner2 := NewCheckpointRunner[TestState](g, saver, nil)
	_, err = runner2.Fork(ctx, cp.ID, "no-modify", nil)
	if err != nil {
		t.Fatalf("Fork with nil modifier failed: %v", err)
	}
}

func TestCheckpointRunner_Fork_NotFound(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()
	runner := NewCheckpointRunner[TestState](g, saver, nil)

	ctx := context.Background()
	_, err := runner.Fork(ctx, "nonexistent", "branch", nil)
	if err == nil {
		t.Error("expected error for nonexistent checkpoint")
	}
}

// ============== GetHistory 测试 ==============

func TestCheckpointRunner_GetHistory(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()
	runner := NewCheckpointRunner[TestState](g, saver, nil)

	ctx := context.Background()
	_, err := runner.Run(ctx, "thread-history", TestState{Counter: 0, Path: ""})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	_, err = runner.GetHistory(ctx, 10)
	if err != nil {
		t.Fatalf("GetHistory failed: %v", err)
	}
}

func TestCheckpointRunner_GetHistory_NoCheckpoint(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()
	runner := NewCheckpointRunner[TestState](g, saver, nil)

	ctx := context.Background()
	history, err := runner.GetHistory(ctx, 10)
	if err != nil {
		t.Fatalf("GetHistory failed: %v", err)
	}
	if history != nil {
		t.Errorf("expected nil history, got %v", history)
	}
}

// ============== GetCurrentCheckpoint 测试 ==============

func TestCheckpointRunner_GetCurrentCheckpoint_BeforeRun(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()
	runner := NewCheckpointRunner[TestState](g, saver, nil)

	cp := runner.GetCurrentCheckpoint()
	if cp != nil {
		t.Error("expected nil checkpoint before run")
	}
}

// ============== executeNode 重试测试 ==============

func TestCheckpointRunner_RetryOnNodeError(t *testing.T) {
	attempts := 0
	g, err := NewGraph[TestState]("test").
		AddNode("flaky", func(ctx context.Context, s TestState) (TestState, error) {
			attempts++
			if attempts < 3 {
				return s, errors.New("temporary failure")
			}
			s.Counter = 42
			return s, nil
		}).
		AddEdge(START, "flaky").
		AddEdge("flaky", END).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	saver := NewMemoryEnhancedCheckpointSaver()
	cfg := &CheckpointRunnerConfig{
		AutoSave:     true,
		SaveInterval: 1,
		SaveOnError:  true,
		MaxRetries:   5,
		RetryDelay:   time.Millisecond,
	}
	runner := NewCheckpointRunner[TestState](g, saver, cfg)

	ctx := context.Background()
	result, err := runner.Run(ctx, "thread-retry", TestState{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Counter != 42 {
		t.Errorf("expected counter 42, got %d", result.Counter)
	}
	if attempts < 3 {
		t.Errorf("expected at least 3 attempts, got %d", attempts)
	}
}

func TestCheckpointRunner_RetryExhausted(t *testing.T) {
	g, err := NewGraph[TestState]("test").
		AddNode("always-fail", func(ctx context.Context, s TestState) (TestState, error) {
			return s, errors.New("persistent failure")
		}).
		AddEdge(START, "always-fail").
		AddEdge("always-fail", END).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	saver := NewMemoryEnhancedCheckpointSaver()
	cfg := &CheckpointRunnerConfig{
		AutoSave:     true,
		SaveInterval: 1,
		SaveOnError:  true,
		MaxRetries:   2,
		RetryDelay:   time.Millisecond,
	}
	runner := NewCheckpointRunner[TestState](g, saver, cfg)

	ctx := context.Background()
	_, err = runner.Run(ctx, "thread-exhaust", TestState{})
	if err == nil {
		t.Error("expected error after retry exhaustion")
	}
}

// ============== 条件路由测试 ==============

func TestCheckpointRunner_ConditionalEdge(t *testing.T) {
	g, err := NewGraph[TestState]("test").
		AddNode("check", func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		}).
		AddNode("high", func(ctx context.Context, s TestState) (TestState, error) {
			s.Path += "H"
			return s, nil
		}).
		AddNode("low", func(ctx context.Context, s TestState) (TestState, error) {
			s.Path += "L"
			return s, nil
		}).
		AddEdge(START, "check").
		AddConditionalEdge("check", func(s TestState) string {
			if s.Counter > 5 {
				return "high"
			}
			return "low"
		}, map[string]string{
			"high": "high",
			"low":  "low",
		}).
		AddEdge("high", END).
		AddEdge("low", END).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	saver := NewMemoryEnhancedCheckpointSaver()
	runner := NewCheckpointRunner[TestState](g, saver, nil)

	ctx := context.Background()

	// Counter > 5 走 high 路径
	result, err := runner.Run(ctx, "thread-cond-high", TestState{Counter: 10})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Path != "H" {
		t.Errorf("expected path 'H', got %q", result.Path)
	}
}

// ============== executeNode 节点未找到测试 ==============

func TestCheckpointRunner_NodeNotFound(t *testing.T) {
	g := buildSimpleGraphForRunner(t)
	saver := NewMemoryEnhancedCheckpointSaver()
	cfg := &CheckpointRunnerConfig{
		AutoSave:     true,
		SaveInterval: 1,
		SaveOnError:  true,
		MaxRetries:   0,
		RetryDelay:   time.Millisecond,
	}
	runner := NewCheckpointRunner[TestState](g, saver, cfg)

	// 手动设置一个指向不存在节点的检查点
	stateBytes, _ := json.Marshal(TestState{})
	runner.currentCheckpoint = &EnhancedCheckpoint{
		ThreadID:       "test",
		GraphName:      g.Name,
		Status:         CheckpointStatusRunning,
		CurrentNode:    START,
		PendingNodes:   []string{"nonexistent"},
		CompletedNodes: []string{},
		State:          stateBytes,
		Stats:          &CheckpointStats{},
		Metadata:       make(map[string]any),
	}

	ctx := context.Background()
	_, err := runner.executeFromCheckpoint(ctx, TestState{})
	if err == nil {
		t.Error("expected error for nonexistent node")
	}
}

// ============== 辅助函数 ==============

func buildSimpleGraphForRunner(t *testing.T) *Graph[TestState] {
	t.Helper()
	g, err := NewGraph[TestState]("test-graph").
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			s.Path += "1"
			return s, nil
		}).
		AddNode("step2", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			s.Path += "2"
			return s, nil
		}).
		AddEdge(START, "step1").
		AddEdge("step1", "step2").
		AddEdge("step2", END).
		Build()
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	return g
}
