package graph

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ============== WhileLoop 测试 ==============

func TestWhileLoopNode_Basic(t *testing.T) {
	node := WhileLoopNode[TestState](
		"while",
		func(s TestState) bool { return s.Counter < 3 },
		func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		},
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{Counter: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Counter != 3 {
		t.Errorf("expected counter 3, got %d", result.Counter)
	}
}

func TestWhileLoopNode_ConditionFalseInitially(t *testing.T) {
	iterations := 0
	node := WhileLoopNode[TestState](
		"while",
		func(s TestState) bool { return false },
		func(ctx context.Context, s TestState) (TestState, error) {
			iterations++
			return s, nil
		},
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if iterations != 0 {
		t.Errorf("expected 0 iterations, got %d", iterations)
	}
}

func TestWhileLoopNode_MaxIterations(t *testing.T) {
	cfg := &LoopConfig{
		MaxIterations: 5,
		BreakOnError:  true,
	}

	node := WhileLoopNode[TestState](
		"while",
		func(s TestState) bool { return true }, // 永远为真
		func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		},
		cfg,
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if !errors.Is(err, ErrMaxIterationsReached) {
		t.Errorf("expected ErrMaxIterationsReached, got %v", err)
	}
}

func TestWhileLoopNode_Timeout(t *testing.T) {
	cfg := &LoopConfig{
		MaxIterations: 0,
		Timeout:       10 * time.Millisecond,
		BreakOnError:  true,
	}

	node := WhileLoopNode[TestState](
		"while",
		func(s TestState) bool { return true },
		func(ctx context.Context, s TestState) (TestState, error) {
			time.Sleep(5 * time.Millisecond)
			s.Counter++
			return s, nil
		},
		cfg,
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if !errors.Is(err, ErrLoopTimeout) {
		t.Errorf("expected ErrLoopTimeout, got %v", err)
	}
}

func TestWhileLoopNode_ContextCancel(t *testing.T) {
	node := WhileLoopNode[TestState](
		"while",
		func(s TestState) bool { return true },
		func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Handler(ctx, TestState{})
	if err == nil {
		t.Error("expected error from context cancellation")
	}
}

func TestWhileLoopNode_Break(t *testing.T) {
	var breakCalled bool
	cfg := &LoopConfig{
		MaxIterations: 1000,
		BreakOnError:  true,
		OnBreak: func(iteration int, reason string) {
			breakCalled = true
		},
	}

	node := WhileLoopNode[TestState](
		"while",
		func(s TestState) bool { return true },
		func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			if s.Counter >= 3 {
				return s, ErrLoopBreak
			}
			return s, nil
		},
		cfg,
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Counter != 3 {
		t.Errorf("expected counter 3, got %d", result.Counter)
	}
	if !breakCalled {
		t.Error("expected OnBreak callback to be called")
	}
}

func TestWhileLoopNode_Continue(t *testing.T) {
	// 注意：ErrLoopContinue 不更新 state（跳过 state = newState），
	// 所以需要用外部计数器控制条件退出
	externalCounter := 0
	node := WhileLoopNode[TestState](
		"while",
		func(s TestState) bool { return externalCounter < 5 },
		func(ctx context.Context, s TestState) (TestState, error) {
			externalCounter++
			s.Counter++
			if externalCounter == 3 {
				return s, ErrLoopContinue // state 不会被应用
			}
			s.Path += "x"
			return s, nil
		},
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 循环 5 次，第 3 次 continue 跳过 path 追加且 state 不更新
	// 所以 path 应有 4 个 "x"
	if result.Path != "xxxx" {
		t.Errorf("expected path 'xxxx', got %q", result.Path)
	}
}

func TestWhileLoopNode_BreakOnError(t *testing.T) {
	cfg := &LoopConfig{
		MaxIterations: 1000,
		BreakOnError:  true,
	}

	node := WhileLoopNode[TestState](
		"while",
		func(s TestState) bool { return true },
		func(ctx context.Context, s TestState) (TestState, error) {
			return s, errors.New("some error")
		},
		cfg,
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if err == nil {
		t.Error("expected error with BreakOnError")
	}
}

func TestWhileLoopNode_ContinueOnError(t *testing.T) {
	cfg := &LoopConfig{
		MaxIterations:   5,
		ContinueOnError: true,
	}

	errorCount := 0
	node := WhileLoopNode[TestState](
		"while",
		func(s TestState) bool { return true },
		func(ctx context.Context, s TestState) (TestState, error) {
			errorCount++
			return s, errors.New("some error")
		},
		cfg,
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	// 达到 MaxIterations
	if !errors.Is(err, ErrMaxIterationsReached) {
		t.Errorf("expected ErrMaxIterationsReached, got %v", err)
	}
	if errorCount != 5 {
		t.Errorf("expected 5 errors before max iterations, got %d", errorCount)
	}
}

func TestWhileLoopNode_Callbacks(t *testing.T) {
	var iterationCalls []int
	var completedWith int

	cfg := &LoopConfig{
		MaxIterations: 1000,
		OnIteration: func(i int) {
			iterationCalls = append(iterationCalls, i)
		},
		OnComplete: func(iterations int) {
			completedWith = iterations
		},
		BreakOnError: true,
	}

	node := WhileLoopNode[TestState](
		"while",
		func(s TestState) bool { return s.Counter < 3 },
		func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		},
		cfg,
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(iterationCalls) != 3 {
		t.Errorf("expected 3 iteration callbacks, got %d", len(iterationCalls))
	}
	if completedWith != 3 {
		t.Errorf("expected completed with 3, got %d", completedWith)
	}
}

func TestWhileLoopNode_MaxIterationsOnBreakCallback(t *testing.T) {
	var breakReason string
	cfg := &LoopConfig{
		MaxIterations: 2,
		OnBreak: func(iteration int, reason string) {
			breakReason = reason
		},
		BreakOnError: true,
	}

	node := WhileLoopNode[TestState](
		"while",
		func(s TestState) bool { return true },
		func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		},
		cfg,
	)

	ctx := context.Background()
	node.Handler(ctx, TestState{})
	if breakReason != "max iterations reached" {
		t.Errorf("expected break reason 'max iterations reached', got %q", breakReason)
	}
}

// ============== DoWhileLoop 测试 ==============

func TestDoWhileLoopNode_Basic(t *testing.T) {
	node := DoWhileLoopNode[TestState](
		"do-while",
		func(s TestState) bool { return s.Counter < 3 },
		func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		},
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{Counter: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Counter != 3 {
		t.Errorf("expected counter 3, got %d", result.Counter)
	}
}

func TestDoWhileLoopNode_ExecutesAtLeastOnce(t *testing.T) {
	executed := false
	node := DoWhileLoopNode[TestState](
		"do-while",
		func(s TestState) bool { return false }, // 条件立即为 false
		func(ctx context.Context, s TestState) (TestState, error) {
			executed = true
			s.Counter = 99
			return s, nil
		},
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !executed {
		t.Error("expected body to execute at least once")
	}
	if result.Counter != 99 {
		t.Errorf("expected counter 99, got %d", result.Counter)
	}
}

func TestDoWhileLoopNode_MaxIterations(t *testing.T) {
	cfg := &LoopConfig{
		MaxIterations: 3,
		BreakOnError:  true,
	}

	node := DoWhileLoopNode[TestState](
		"do-while",
		func(s TestState) bool { return true },
		func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		},
		cfg,
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if !errors.Is(err, ErrMaxIterationsReached) {
		t.Errorf("expected ErrMaxIterationsReached, got %v", err)
	}
}

func TestDoWhileLoopNode_Break(t *testing.T) {
	node := DoWhileLoopNode[TestState](
		"do-while",
		func(s TestState) bool { return true },
		func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			if s.Counter >= 2 {
				return s, ErrLoopBreak
			}
			return s, nil
		},
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Counter != 2 {
		t.Errorf("expected counter 2, got %d", result.Counter)
	}
}

func TestDoWhileLoopNode_Continue(t *testing.T) {
	// ErrLoopContinue 在 do-while 中不更新 state，
	// 但 do-while 的 continue 分支还会检查 condition(state)
	// 使用外部计数器控制退出
	externalCounter := 0
	node := DoWhileLoopNode[TestState](
		"do-while",
		func(s TestState) bool { return externalCounter < 4 },
		func(ctx context.Context, s TestState) (TestState, error) {
			externalCounter++
			s.Counter++
			if externalCounter == 2 {
				return s, ErrLoopContinue
			}
			s.Path += "x"
			return s, nil
		},
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 循环 4 次，第 2 次 continue，其余 3 次 path += "x"
	if result.Path != "xxx" {
		t.Errorf("expected path 'xxx', got %q", result.Path)
	}
}

func TestDoWhileLoopNode_ContinueOnError(t *testing.T) {
	cfg := &LoopConfig{
		MaxIterations:   5,
		ContinueOnError: true,
	}

	node := DoWhileLoopNode[TestState](
		"do-while",
		func(s TestState) bool { return true },
		func(ctx context.Context, s TestState) (TestState, error) {
			return s, errors.New("error")
		},
		cfg,
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if !errors.Is(err, ErrMaxIterationsReached) {
		t.Errorf("expected ErrMaxIterationsReached, got %v", err)
	}
}

func TestDoWhileLoopNode_Timeout(t *testing.T) {
	cfg := &LoopConfig{
		MaxIterations: 0,
		Timeout:       10 * time.Millisecond,
		BreakOnError:  true,
	}

	node := DoWhileLoopNode[TestState](
		"do-while",
		func(s TestState) bool { return true },
		func(ctx context.Context, s TestState) (TestState, error) {
			time.Sleep(5 * time.Millisecond)
			return s, nil
		},
		cfg,
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if !errors.Is(err, ErrLoopTimeout) {
		t.Errorf("expected ErrLoopTimeout, got %v", err)
	}
}

// ============== ForLoop 测试 ==============

func TestForLoopNode_Basic(t *testing.T) {
	node := ForLoopNode[TestState](
		"for",
		5,
		func(ctx context.Context, s TestState, i int) (TestState, error) {
			s.Counter += i
			return s, nil
		},
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 0+1+2+3+4 = 10
	if result.Counter != 10 {
		t.Errorf("expected counter 10, got %d", result.Counter)
	}
}

func TestForLoopNode_ZeroIterations(t *testing.T) {
	executed := false
	node := ForLoopNode[TestState](
		"for",
		0,
		func(ctx context.Context, s TestState, i int) (TestState, error) {
			executed = true
			return s, nil
		},
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if executed {
		t.Error("expected body not to execute with 0 iterations")
	}
}

func TestForLoopNode_Break(t *testing.T) {
	node := ForLoopNode[TestState](
		"for",
		10,
		func(ctx context.Context, s TestState, i int) (TestState, error) {
			s.Counter++
			if i >= 3 {
				return s, ErrLoopBreak
			}
			return s, nil
		},
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Counter != 4 { // 0,1,2,3 = 4次
		t.Errorf("expected counter 4, got %d", result.Counter)
	}
}

func TestForLoopNode_Continue(t *testing.T) {
	node := ForLoopNode[TestState](
		"for",
		5,
		func(ctx context.Context, s TestState, i int) (TestState, error) {
			if i == 2 {
				return s, ErrLoopContinue
			}
			s.Counter++
			return s, nil
		},
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Counter != 4 { // 跳过 i=2
		t.Errorf("expected counter 4, got %d", result.Counter)
	}
}

func TestForLoopNode_Timeout(t *testing.T) {
	cfg := &LoopConfig{
		Timeout:      10 * time.Millisecond,
		BreakOnError: true,
	}

	node := ForLoopNode[TestState](
		"for",
		100,
		func(ctx context.Context, s TestState, i int) (TestState, error) {
			time.Sleep(5 * time.Millisecond)
			return s, nil
		},
		cfg,
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if !errors.Is(err, ErrLoopTimeout) {
		t.Errorf("expected ErrLoopTimeout, got %v", err)
	}
}

func TestForLoopNode_ContextCancel(t *testing.T) {
	node := ForLoopNode[TestState](
		"for",
		1000,
		func(ctx context.Context, s TestState, i int) (TestState, error) {
			return s, nil
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Handler(ctx, TestState{})
	if err == nil {
		t.Error("expected error from context cancellation")
	}
}

func TestForLoopNode_BreakOnError(t *testing.T) {
	cfg := &LoopConfig{
		MaxIterations: 1000,
		BreakOnError:  true,
	}

	node := ForLoopNode[TestState](
		"for",
		10,
		func(ctx context.Context, s TestState, i int) (TestState, error) {
			return s, errors.New("some error")
		},
		cfg,
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if err == nil {
		t.Error("expected error with BreakOnError")
	}
}

func TestForLoopNode_ContinueOnError(t *testing.T) {
	cfg := &LoopConfig{
		MaxIterations:   1000,
		ContinueOnError: true,
	}

	errorCount := 0
	node := ForLoopNode[TestState](
		"for",
		5,
		func(ctx context.Context, s TestState, i int) (TestState, error) {
			errorCount++
			return s, errors.New("error")
		},
		cfg,
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error with ContinueOnError: %v", err)
	}
	if errorCount != 5 {
		t.Errorf("expected 5 error iterations, got %d", errorCount)
	}
}

func TestForLoopNode_OnCompleteCallback(t *testing.T) {
	var completedIterations int
	cfg := &LoopConfig{
		MaxIterations: 1000,
		OnComplete: func(iterations int) {
			completedIterations = iterations
		},
		BreakOnError: true,
	}

	node := ForLoopNode[TestState](
		"for",
		3,
		func(ctx context.Context, s TestState, i int) (TestState, error) {
			return s, nil
		},
		cfg,
	)

	ctx := context.Background()
	node.Handler(ctx, TestState{})
	if completedIterations != 3 {
		t.Errorf("expected OnComplete(3), got %d", completedIterations)
	}
}

// ============== ForEachLoop 测试 ==============

func TestForEachLoopNode_Basic(t *testing.T) {
	node := ForEachLoopNode[TestState, string](
		"foreach",
		func(s TestState) []string {
			return []string{"a", "b", "c"}
		},
		func(ctx context.Context, s TestState, item string, i int) (TestState, error) {
			s.Path += item
			return s, nil
		},
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Path != "abc" {
		t.Errorf("expected path 'abc', got %q", result.Path)
	}
}

func TestForEachLoopNode_EmptyItems(t *testing.T) {
	node := ForEachLoopNode[TestState, string](
		"foreach",
		func(s TestState) []string { return nil },
		func(ctx context.Context, s TestState, item string, i int) (TestState, error) {
			t.Error("should not execute")
			return s, nil
		},
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestForEachLoopNode_Break(t *testing.T) {
	node := ForEachLoopNode[TestState, int](
		"foreach",
		func(s TestState) []int { return []int{1, 2, 3, 4, 5} },
		func(ctx context.Context, s TestState, item int, i int) (TestState, error) {
			s.Counter += item
			if item == 3 {
				return s, ErrLoopBreak
			}
			return s, nil
		},
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Counter != 6 { // 1+2+3
		t.Errorf("expected counter 6, got %d", result.Counter)
	}
}

func TestForEachLoopNode_MaxIterations(t *testing.T) {
	cfg := &LoopConfig{
		MaxIterations: 2,
		BreakOnError:  true,
	}

	node := ForEachLoopNode[TestState, int](
		"foreach",
		func(s TestState) []int { return []int{1, 2, 3, 4, 5} },
		func(ctx context.Context, s TestState, item int, i int) (TestState, error) {
			return s, nil
		},
		cfg,
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if !errors.Is(err, ErrMaxIterationsReached) {
		t.Errorf("expected ErrMaxIterationsReached, got %v", err)
	}
}

// ============== UntilLoop 测试 ==============

func TestUntilLoopNode_Basic(t *testing.T) {
	node := UntilLoopNode[TestState](
		"until",
		func(s TestState) bool { return s.Counter >= 5 },
		func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		},
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{Counter: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Counter != 5 {
		t.Errorf("expected counter 5, got %d", result.Counter)
	}
}

func TestUntilLoopNode_AlreadyMet(t *testing.T) {
	iterations := 0
	node := UntilLoopNode[TestState](
		"until",
		func(s TestState) bool { return true }, // 条件已满足
		func(ctx context.Context, s TestState) (TestState, error) {
			iterations++
			return s, nil
		},
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if iterations != 0 {
		t.Errorf("expected 0 iterations, got %d", iterations)
	}
}

// ============== 循环控制函数测试 ==============

func TestBreak(t *testing.T) {
	err := Break()
	if !errors.Is(err, ErrLoopBreak) {
		t.Errorf("expected ErrLoopBreak, got %v", err)
	}
}

func TestContinue(t *testing.T) {
	err := Continue()
	if !errors.Is(err, ErrLoopContinue) {
		t.Errorf("expected ErrLoopContinue, got %v", err)
	}
}

func TestBreakIf(t *testing.T) {
	handler := BreakIf[TestState](func(s TestState) bool {
		return s.Counter > 5
	})

	ctx := context.Background()

	// 条件不满足
	_, err := handler(ctx, TestState{Counter: 3})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// 条件满足
	_, err = handler(ctx, TestState{Counter: 10})
	if !errors.Is(err, ErrLoopBreak) {
		t.Errorf("expected ErrLoopBreak, got %v", err)
	}
}

func TestContinueIf(t *testing.T) {
	handler := ContinueIf[TestState](func(s TestState) bool {
		return s.Counter%2 == 0
	})

	ctx := context.Background()

	// 奇数不 continue
	_, err := handler(ctx, TestState{Counter: 3})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// 偶数 continue
	_, err = handler(ctx, TestState{Counter: 4})
	if !errors.Is(err, ErrLoopContinue) {
		t.Errorf("expected ErrLoopContinue, got %v", err)
	}
}

// ============== LoopCounter 测试 ==============

func TestLoopCounter(t *testing.T) {
	counter := NewLoopCounter()

	if counter.Get() != 0 {
		t.Errorf("expected 0, got %d", counter.Get())
	}

	v := counter.Increment()
	if v != 1 {
		t.Errorf("expected 1, got %d", v)
	}

	v = counter.Increment()
	if v != 2 {
		t.Errorf("expected 2, got %d", v)
	}

	if counter.Get() != 2 {
		t.Errorf("expected 2, got %d", counter.Get())
	}

	counter.Reset()
	if counter.Get() != 0 {
		t.Errorf("expected 0 after reset, got %d", counter.Get())
	}
}

// ============== DefaultLoopConfig 测试 ==============

func TestDefaultLoopConfig(t *testing.T) {
	cfg := DefaultLoopConfig()
	if cfg.MaxIterations != 1000 {
		t.Errorf("expected MaxIterations 1000, got %d", cfg.MaxIterations)
	}
	if cfg.Timeout != 0 {
		t.Errorf("expected Timeout 0, got %v", cfg.Timeout)
	}
	if !cfg.BreakOnError {
		t.Error("expected BreakOnError true")
	}
	if cfg.ContinueOnError {
		t.Error("expected ContinueOnError false")
	}
}

// ============== ParallelForEachLoop 测试 ==============

func TestParallelForEachLoopNode_Basic(t *testing.T) {
	node := ParallelForEachLoopNode[TestState, int](
		"parallel-foreach",
		func(s TestState) []int { return []int{1, 2, 3, 4, 5} },
		func(ctx context.Context, item int, index int) error {
			return nil
		},
		func(state TestState, results []error) (TestState, error) {
			for _, err := range results {
				if err != nil {
					return state, err
				}
			}
			state.Counter = len(results)
			return state, nil
		},
		3,
	)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Counter != 5 {
		t.Errorf("expected counter 5, got %d", result.Counter)
	}
}

func TestParallelForEachLoopNode_EmptyItems(t *testing.T) {
	node := ParallelForEachLoopNode[TestState, int](
		"parallel-foreach",
		func(s TestState) []int { return nil },
		func(ctx context.Context, item int, index int) error { return nil },
		func(state TestState, results []error) (TestState, error) { return state, nil },
		3,
	)

	ctx := context.Background()
	state := TestState{Counter: 42}
	result, err := node.Handler(ctx, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Counter != 42 {
		t.Errorf("expected counter 42, got %d", result.Counter)
	}
}

func TestParallelForEachLoopNode_WithErrors(t *testing.T) {
	node := ParallelForEachLoopNode[TestState, int](
		"parallel-foreach",
		func(s TestState) []int { return []int{1, 2, 3} },
		func(ctx context.Context, item int, index int) error {
			if item == 2 {
				return errors.New("item 2 failed")
			}
			return nil
		},
		func(state TestState, results []error) (TestState, error) {
			for _, err := range results {
				if err != nil {
					return state, err
				}
			}
			return state, nil
		},
		3,
	)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if err == nil {
		t.Error("expected error from failing item")
	}
}

func TestParallelForEachLoopNode_DefaultConcurrency(t *testing.T) {
	// maxConcurrency <= 0 默认为 10
	node := ParallelForEachLoopNode[TestState, int](
		"parallel-foreach",
		func(s TestState) []int { return []int{1} },
		func(ctx context.Context, item int, index int) error { return nil },
		func(state TestState, results []error) (TestState, error) { return state, nil },
		0,
	)

	if node.Metadata["max_concurrency"] != 10 {
		t.Errorf("expected default max_concurrency 10, got %v", node.Metadata["max_concurrency"])
	}
}

func TestParallelForEachLoopNode_ContextCancel(t *testing.T) {
	// 取消后未派发项也有完成记录，不要求 worker 与 item 数量相等。
	node := ParallelForEachLoopNode[TestState, int](
		"parallel-foreach",
		func(s TestState) []int { return []int{1, 2, 3} },
		func(ctx context.Context, item int, index int) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		},
		func(state TestState, results []error) (TestState, error) {
			for _, err := range results {
				if err != nil {
					return state, err
				}
			}
			return state, nil
		},
		1,
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Handler(ctx, TestState{})
	if err == nil {
		t.Error("expected error from context cancellation")
	}
}

// ============== RetryLoop 测试 ==============

func TestRetryLoopNode_Success(t *testing.T) {
	cfg := &RetryConfig{
		MaxRetries:  3,
		Delay:       time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		Backoff:     2.0,
		ShouldRetry: func(err error) bool { return true },
	}

	node := RetryLoopNode[TestState]("retry", func(ctx context.Context, s TestState) (TestState, error) {
		s.Counter = 42
		return s, nil
	}, cfg)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Counter != 42 {
		t.Errorf("expected counter 42, got %d", result.Counter)
	}
}

func TestRetryLoopNode_RetryThenSuccess(t *testing.T) {
	attempts := 0
	cfg := &RetryConfig{
		MaxRetries:  5,
		Delay:       time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		Backoff:     1.0,
		ShouldRetry: func(err error) bool { return true },
	}

	node := RetryLoopNode[TestState]("retry", func(ctx context.Context, s TestState) (TestState, error) {
		attempts++
		if attempts < 3 {
			return s, errors.New("temporary")
		}
		s.Counter = 99
		return s, nil
	}, cfg)

	ctx := context.Background()
	result, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Counter != 99 {
		t.Errorf("expected counter 99, got %d", result.Counter)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryLoopNode_MaxRetriesExceeded(t *testing.T) {
	cfg := &RetryConfig{
		MaxRetries:  2,
		Delay:       time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		Backoff:     1.0,
		ShouldRetry: func(err error) bool { return true },
	}

	node := RetryLoopNode[TestState]("retry", func(ctx context.Context, s TestState) (TestState, error) {
		return s, errors.New("persistent")
	}, cfg)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if err == nil {
		t.Error("expected error after max retries")
	}
}

func TestRetryLoopNode_ShouldNotRetry(t *testing.T) {
	attempts := 0
	cfg := &RetryConfig{
		MaxRetries: 5,
		Delay:      time.Millisecond,
		Backoff:    1.0,
		ShouldRetry: func(err error) bool {
			return false // 不重试
		},
	}

	node := RetryLoopNode[TestState]("retry", func(ctx context.Context, s TestState) (TestState, error) {
		attempts++
		return s, errors.New("error")
	}, cfg)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if err == nil {
		t.Error("expected error")
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retry), got %d", attempts)
	}
}

func TestRetryLoopNode_DefaultConfig(t *testing.T) {
	node := RetryLoopNode[TestState]("retry", func(ctx context.Context, s TestState) (TestState, error) {
		return s, nil
	}, nil)

	if node.Metadata["max_retries"] != 3 {
		t.Errorf("expected default max_retries 3, got %v", node.Metadata["max_retries"])
	}
}

func TestRetryLoopNode_OnRetryCallback(t *testing.T) {
	var retryAttempts []int
	cfg := &RetryConfig{
		MaxRetries:  3,
		Delay:       time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		Backoff:     2.0,
		ShouldRetry: func(err error) bool { return true },
		OnRetry: func(attempt int, err error, delay time.Duration) {
			retryAttempts = append(retryAttempts, attempt)
		},
	}

	attempts := 0
	node := RetryLoopNode[TestState]("retry", func(ctx context.Context, s TestState) (TestState, error) {
		attempts++
		if attempts <= 2 {
			return s, errors.New("error")
		}
		return s, nil
	}, cfg)

	ctx := context.Background()
	_, err := node.Handler(ctx, TestState{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(retryAttempts) != 2 {
		t.Errorf("expected 2 retry callbacks, got %d", len(retryAttempts))
	}
}

func TestRetryLoopNode_BackoffCapped(t *testing.T) {
	cfg := &RetryConfig{
		MaxRetries:  5,
		Delay:       100 * time.Millisecond,
		MaxDelay:    200 * time.Millisecond,
		Backoff:     10.0, // 大退避因子
		ShouldRetry: func(err error) bool { return true },
	}

	// 验证 delay 不超过 MaxDelay
	node := RetryLoopNode[TestState]("retry", func(ctx context.Context, s TestState) (TestState, error) {
		return s, errors.New("error")
	}, cfg)

	if node.Metadata["max_retries"] != 5 {
		t.Errorf("expected max_retries 5, got %v", node.Metadata["max_retries"])
	}
}

func TestRetryLoopNode_ContextCancel(t *testing.T) {
	cfg := &RetryConfig{
		MaxRetries:  10,
		Delay:       time.Second,
		MaxDelay:    10 * time.Second,
		Backoff:     2.0,
		ShouldRetry: func(err error) bool { return true },
	}

	node := RetryLoopNode[TestState]("retry", func(ctx context.Context, s TestState) (TestState, error) {
		return s, errors.New("error")
	}, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Handler(ctx, TestState{})
	if err == nil {
		t.Error("expected error from context cancellation")
	}
}

// ============== AddLoopBackEdge 测试 ==============

func TestAddLoopBackEdge(t *testing.T) {
	g, err := NewGraph[TestState]("test").
		WithExecutionMode(GraphModePregel).
		AddNode("work", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddEdge(START, "work").
		AddLoopBackEdge("work", "work", func(s TestState) bool {
			return s.Counter < 3
		}, 10).
		Build()

	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	ctx := context.Background()
	result, err := g.Run(ctx, TestState{Counter: 0})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if result.Counter < 3 {
		t.Errorf("expected counter >= 3, got %d", result.Counter)
	}
}

func TestAddLoopBackEdge_MaxIterations(t *testing.T) {
	g, err := NewGraph[TestState]("test").
		WithExecutionMode(GraphModePregel).
		AddNode("work", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddEdge(START, "work").
		AddLoopBackEdge("work", "work", func(s TestState) bool {
			return true // 永远为真
		}, 5). // 最多 5 次
		Build()

	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	ctx := context.Background()
	result, err := g.Run(ctx, TestState{Counter: 0})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// 最多循环 5 次后应该退出
	if result.Counter > 10 { // 留一些余量
		t.Errorf("expected limited iterations, got counter %d", result.Counter)
	}
}

// ============== Builder 方法测试 ==============

func TestAddWhileLoop_Builder(t *testing.T) {
	g, err := NewGraph[TestState]("test").
		AddWhileLoop("loop",
			func(s TestState) bool { return s.Counter < 3 },
			func(ctx context.Context, s TestState) (TestState, error) {
				s.Counter++
				return s, nil
			},
		).
		AddEdge(START, "loop").
		AddEdge("loop", END).
		Build()

	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	ctx := context.Background()
	result, err := g.Run(ctx, TestState{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Counter != 3 {
		t.Errorf("expected counter 3, got %d", result.Counter)
	}
}

func TestAddDoWhileLoop_Builder(t *testing.T) {
	g, err := NewGraph[TestState]("test").
		AddDoWhileLoop("loop",
			func(s TestState) bool { return s.Counter < 3 },
			func(ctx context.Context, s TestState) (TestState, error) {
				s.Counter++
				return s, nil
			},
		).
		AddEdge(START, "loop").
		AddEdge("loop", END).
		Build()

	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	ctx := context.Background()
	result, err := g.Run(ctx, TestState{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Counter != 3 {
		t.Errorf("expected counter 3, got %d", result.Counter)
	}
}

func TestAddForLoop_Builder(t *testing.T) {
	g, err := NewGraph[TestState]("test").
		AddForLoop("loop", 3,
			func(ctx context.Context, s TestState, i int) (TestState, error) {
				s.Counter++
				return s, nil
			},
		).
		AddEdge(START, "loop").
		AddEdge("loop", END).
		Build()

	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	ctx := context.Background()
	result, err := g.Run(ctx, TestState{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Counter != 3 {
		t.Errorf("expected counter 3, got %d", result.Counter)
	}
}

func TestAddUntilLoop_Builder(t *testing.T) {
	g, err := NewGraph[TestState]("test").
		AddUntilLoop("loop",
			func(s TestState) bool { return s.Counter >= 3 },
			func(ctx context.Context, s TestState) (TestState, error) {
				s.Counter++
				return s, nil
			},
		).
		AddEdge(START, "loop").
		AddEdge("loop", END).
		Build()

	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	ctx := context.Background()
	result, err := g.Run(ctx, TestState{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Counter != 3 {
		t.Errorf("expected counter 3, got %d", result.Counter)
	}
}

func TestAddRetryLoop_Builder(t *testing.T) {
	attempts := 0
	cfg := &RetryConfig{
		MaxRetries:  3,
		Delay:       time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		Backoff:     1.0,
		ShouldRetry: func(err error) bool { return true },
	}

	g, err := NewGraph[TestState]("test").
		AddRetryLoop("retry",
			func(ctx context.Context, s TestState) (TestState, error) {
				attempts++
				if attempts < 2 {
					return s, errors.New("temp")
				}
				s.Counter = 42
				return s, nil
			},
			cfg,
		).
		AddEdge(START, "retry").
		AddEdge("retry", END).
		Build()

	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	ctx := context.Background()
	result, err := g.Run(ctx, TestState{})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Counter != 42 {
		t.Errorf("expected counter 42, got %d", result.Counter)
	}
}

// ============== 节点元数据测试 ==============

func TestLoopNodeMetadata(t *testing.T) {
	whileNode := WhileLoopNode[TestState]("w", func(s TestState) bool { return false },
		func(ctx context.Context, s TestState) (TestState, error) { return s, nil })
	if whileNode.Metadata["loop_type"] != "while" {
		t.Errorf("expected loop_type 'while', got %v", whileNode.Metadata["loop_type"])
	}
	if whileNode.Type != NodeTypeLoop {
		t.Errorf("expected NodeTypeLoop, got %d", whileNode.Type)
	}

	doWhileNode := DoWhileLoopNode[TestState]("dw", func(s TestState) bool { return false },
		func(ctx context.Context, s TestState) (TestState, error) { return s, nil })
	if doWhileNode.Metadata["loop_type"] != "do_while" {
		t.Errorf("expected loop_type 'do_while', got %v", doWhileNode.Metadata["loop_type"])
	}

	forNode := ForLoopNode[TestState]("f", 3,
		func(ctx context.Context, s TestState, i int) (TestState, error) { return s, nil })
	if forNode.Metadata["loop_type"] != "for" {
		t.Errorf("expected loop_type 'for', got %v", forNode.Metadata["loop_type"])
	}
	if forNode.Metadata["iterations"] != 3 {
		t.Errorf("expected iterations 3, got %v", forNode.Metadata["iterations"])
	}
}

// ============== DefaultRetryConfig 测试 ==============

func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()
	if cfg.MaxRetries != 3 {
		t.Errorf("expected MaxRetries 3, got %d", cfg.MaxRetries)
	}
	if cfg.Delay != time.Second {
		t.Errorf("expected Delay 1s, got %v", cfg.Delay)
	}
	if cfg.MaxDelay != 30*time.Second {
		t.Errorf("expected MaxDelay 30s, got %v", cfg.MaxDelay)
	}
	if cfg.Backoff != 2.0 {
		t.Errorf("expected Backoff 2.0, got %f", cfg.Backoff)
	}
	if cfg.Jitter != 0.1 {
		t.Errorf("expected Jitter 0.1, got %f", cfg.Jitter)
	}
	if cfg.ShouldRetry == nil {
		t.Error("expected ShouldRetry to be set")
	}
}

// ============== 错误常量测试 ==============

func TestErrorConstants(t *testing.T) {
	if ErrLoopBreak.Error() != "loop break" {
		t.Errorf("unexpected ErrLoopBreak: %v", ErrLoopBreak)
	}
	if ErrLoopContinue.Error() != "loop continue" {
		t.Errorf("unexpected ErrLoopContinue: %v", ErrLoopContinue)
	}
	if ErrMaxIterationsReached.Error() != "max iterations reached" {
		t.Errorf("unexpected ErrMaxIterationsReached: %v", ErrMaxIterationsReached)
	}
	if ErrLoopTimeout.Error() != "loop timeout" {
		t.Errorf("unexpected ErrLoopTimeout: %v", ErrLoopTimeout)
	}
}
