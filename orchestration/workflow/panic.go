package workflow

import (
	"fmt"
	"runtime/debug"
)

// StepPanicError 保留用户步骤的身份及当前协程栈；未知副作用不得自动重放。
type StepPanicError struct {
	StepID string
	Value  any
	Stack  []byte
}

func (e *StepPanicError) Error() string {
	return fmt.Sprintf("workflow step %s panicked: %v", e.StepID, e.Value)
}

func (*StepPanicError) UnsafeToReplay() bool { return true }

// invokeStep 捕获执行该步骤的同一协程中的 panic，结果仍交给原失败保存路径。
func invokeStep(stepID string, execute func() (*StepOutput, error)) (output *StepOutput, err error) {
	defer func() {
		if value := recover(); value != nil {
			output = nil
			err = &StepPanicError{StepID: stepID, Value: value, Stack: debug.Stack()}
		}
	}()
	return execute()
}
