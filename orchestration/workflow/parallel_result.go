package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/interrupt"
)

// ParallelStepResult 按声明顺序保留分支完整输出和执行错误，不把失败输出作为成功采用。
type ParallelStepResult struct {
	StepID string
	Output *StepOutput
	Error  error
}

// ParallelStepError 保留并行步骤的完整错误树；成功或未知分支禁止整组盲重放。
// 全部明确普通失败仍服从外层步骤的重试策略。
type ParallelStepError struct {
	StepID  string
	Results []ParallelStepResult
}

func (e *ParallelStepError) Error() string {
	return fmt.Sprintf("parallel step %q failed: %v", e.StepID, errors.Join(e.Unwrap()...))
}

func (e *ParallelStepError) Unwrap() []error {
	var causes []error
	for _, result := range e.Results {
		if result.Error != nil {
			causes = append(causes, fmt.Errorf("step %q: %w", result.StepID, result.Error))
		}
	}
	return causes
}

func (e *ParallelStepError) UnsafeToReplay() bool {
	for _, result := range e.Results {
		_, waiting := interrupt.IsInterruptSignal(result.Error)
		if result.Error == nil || waiting || errors.Is(result.Error, interrupt.ErrInterrupted) || llm.IsUnsafeToReplay(result.Error) || errors.Is(result.Error, context.Canceled) || errors.Is(result.Error, context.DeadlineExceeded) {
			return true
		}
	}
	return false
}
