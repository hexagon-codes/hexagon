package graph

import (
	"context"
	"errors"
	"fmt"

	"github.com/hexagon-codes/ai-core/llm"
)

// ParallelResult 保留已派发分支的完整结果；错误伴随的状态不作为成功更新采用。
type ParallelResult[S State] struct {
	Name  string
	State S
	Error error
}

// ParallelExecutionError 保留所有分支结果和错误树，调用方可以核对成功及未知动作。
// 成功或未知分支不允许整组重放；全部明确失败仍按调用方重试策略处理。
type ParallelExecutionError[S State] struct {
	Name    string
	Results []ParallelResult[S]
	Cause   error
}

func (e *ParallelExecutionError[S]) Error() string {
	return fmt.Sprintf("parallel execution %q failed: %v", e.Name, errors.Join(e.Unwrap()...))
}
func (e *ParallelExecutionError[S]) Unwrap() []error {
	var failures []error
	if e.Cause != nil {
		failures = append(failures, e.Cause)
	}
	for _, result := range e.Results {
		if result.Error != nil {
			failures = append(failures, fmt.Errorf("branch %q: %w", result.Name, result.Error))
		}
	}
	return failures
}
func (e *ParallelExecutionError[S]) UnsafeToReplay() bool {
	for _, result := range e.Results {
		if result.Error == nil || llm.IsUnsafeToReplay(result.Error) || errors.Is(result.Error, context.Canceled) || errors.Is(result.Error, context.DeadlineExceeded) {
			return true
		}
	}
	return false
}

func parallelFailure[S State](name string, results []ParallelResult[S]) error {
	for _, result := range results {
		if result.Error != nil {
			return &ParallelExecutionError[S]{Name: name, Results: results}
		}
	}
	return nil
}
