package workflow

import (
	"errors"
	"fmt"
	"time"

	"github.com/hexagon-codes/toolkit/util/reflectx"
)

// invokeWorkflowCallback 在回调所在协程处理 panic，不让完成通知越过失败落盘。
func invokeWorkflowCallback(name string, callback func() error) error {
	_, err := invokeStep(name, func() (*StepOutput, error) { return nil, callback() })
	return err
}

// observeWorkflowValue 给回调独立载荷；返回错误不会采用回调对载荷的修改。
func observeWorkflowValue[T any](name string, value T, callback func(T) error) error {
	return invokeWorkflowCallback(name, func() error {
		copy, err := reflectx.DeepCopyChecked(value)
		if err != nil {
			return fmt.Errorf("copy workflow callback %s payload: %w", name, err)
		}
		return callback(copy)
	})
}

func (e *Executor) failExecution(state *executionState, cause error) {
	state.mu.Lock()
	state.executionErr = errors.Join(state.executionErr, cause)
	if result := state.execution.StepResults[state.execution.Context.CurrentStepID]; result != nil && result.Status == StatusRunning {
		now := time.Now()
		result.Status, result.Error, result.CompletedAt = StatusFailed, cause.Error(), &now
		result.Duration = now.Sub(result.StartedAt)
	}
	state.mu.Unlock()
	status := StatusFailed
	if state.ctx.Err() != nil {
		status = StatusCancelled
	}
	if err := e.setExecutionStatus(state, status, cause.Error()); err != nil {
		// 失败通知自身再失败时只保留错误，不递归发送同一失败事件。
		state.mu.Lock()
		state.executionErr = errors.Join(state.executionErr, err)
		state.mu.Unlock()
	}
}

func completedExecution(state *executionState) (*WorkflowExecution, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	execution := state.execution
	err := errors.Join(state.persistenceErr, state.executionErr)
	if err == nil {
		switch state.execution.Status {
		case StatusCompleted, StatusFailed, StatusCancelled:
		default:
			err = fmt.Errorf("workflow execution stopped before reaching a terminal status")
		}
	}
	return execution, err
}
