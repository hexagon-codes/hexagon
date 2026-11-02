package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/interrupt"
	hr "github.com/hexagon-codes/hexagon/runtime"
)

const workflowInputFormat = "workflow.input.v1"

// ExecutionPersistence 是执行器消费的单一保存端口，可绑定统一 Run 提交适配器。
type ExecutionPersistence interface {
	SaveExecution(context.Context, *WorkflowExecution) error
}

func WithExecutionPersistence(persistence ExecutionPersistence) ExecutorOption {
	return func(e *Executor) { e.persistence = persistence }
}

// StepOutcomeUnknownError 保留原步骤身份，恢复前必须核对真实外部结果。
type StepOutcomeUnknownError struct {
	ExecutionID string
	StepID      string
}

func (e *StepOutcomeUnknownError) Error() string {
	return fmt.Sprintf("workflow step %s/%s outcome is unknown; reconcile original operation", e.ExecutionID, e.StepID)
}
func (*StepOutcomeUnknownError) UnsafeToReplay() bool { return true }

// StepReconciler 仅查原操作事实，ok=false 不表示外部失败或可安全重发。
type StepReconciler interface {
	ReconcileStep(context.Context, string, Step, *StepResult) (*StepOutput, bool, error)
}

func WithStepReconciler(reconciler StepReconciler) ExecutorOption {
	return func(e *Executor) { e.reconciler = reconciler }
}

func (e *Executor) persistExecution(ctx context.Context, execution *WorkflowExecution) error {
	if e.persistence == nil && (!e.config.EnablePersistence || e.store == nil) {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("workflow execution context is missing")
	}
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
	}
	if e.persistence != nil {
		return e.persistence.SaveExecution(ctx, execution)
	}
	return e.store.SaveExecution(ctx, execution)
}

func cloneExecution(execution *WorkflowExecution) (*WorkflowExecution, error) {
	data, err := json.Marshal(execution)
	if err != nil {
		return nil, err
	}
	var cloned WorkflowExecution
	if err := decodeWorkflowJSON(data, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}

func restoredInput(execution *WorkflowExecution) (WorkflowInput, error) {
	var input WorkflowInput
	if len(execution.Input) > 0 {
		switch execution.InputFormat {
		case workflowInputFormat:
			if err := decodeWorkflowJSON(execution.Input, &input); err != nil {
				return input, err
			}
		case "":
			if err := decodeWorkflowJSON(execution.Input, &input.Data); err != nil {
				return input, err
			}
		default:
			return input, fmt.Errorf("unsupported workflow input format")
		}
	}
	if execution.Context != nil {
		input.Variables = execution.Context.Variables
		input.Metadata = execution.Context.Metadata
	}
	return input, nil
}

func decodeWorkflowJSON(data []byte, target any) error {
	if !json.Valid(data) {
		return fmt.Errorf("invalid workflow JSON state")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

// RecoverExecution 接续原 Execution ID、输入、上下文及完成步骤，不重新调用 RunAsync。
// 未确认的步骤默认只核对，只有显式声明 ReplaySafe 的步骤可以重新执行。
func (e *Executor) RecoverExecution(ctx context.Context, wf *Workflow, stored *WorkflowExecution) (string, error) {
	if wf == nil || stored == nil || stored.ID == "" || stored.WorkflowID != wf.ID {
		return "", fmt.Errorf("workflow recovery identity mismatch")
	}
	if stored.WorkflowVersion != "" && stored.WorkflowVersion != wf.Version {
		return "", fmt.Errorf("workflow definition version mismatch")
	}
	if stored.Status != StatusRunning && stored.Status != StatusPaused && stored.Status != StatusPending {
		return "", fmt.Errorf("workflow execution is not resumable")
	}
	execution, err := cloneExecution(stored)
	if err != nil {
		return "", err
	}
	if execution.Context == nil {
		execution.Context = &ExecutionContext{}
	}
	if execution.Context.Variables == nil {
		execution.Context.Variables = make(map[string]any)
	}
	if execution.Context.Metadata == nil {
		execution.Context.Metadata = make(map[string]any)
	}
	if execution.StepResults == nil {
		execution.StepResults = make(map[string]*StepResult)
	}
	if execution.StartedAt.IsZero() {
		execution.StartedAt = time.Now()
	}
	if execution.CompletedAt != nil {
		return "", fmt.Errorf("terminal workflow execution cannot be resumed")
	}
	if execution.Status == StatusPending {
		execution.Status = StatusRunning
	}
	for _, id := range execution.Context.CompletedSteps {
		if result := execution.StepResults[id]; result == nil || result.Status != StatusCompleted {
			return "", fmt.Errorf("completed workflow step result is missing: %s", id)
		}
	}
	for _, step := range wf.Steps {
		result, exists := execution.StepResults[step.ID()]
		if !exists && execution.Context.CurrentStepID != step.ID() {
			continue
		}
		if result != nil && result.Status == StatusCompleted {
			continue
		}
		if safe, ok := step.(interface{ ReplaySafe() bool }); ok && safe.ReplaySafe() {
			continue
		}
		if e.reconciler == nil {
			return "", &StepOutcomeUnknownError{execution.ID, step.ID()}
		}
		output, known, err := e.reconciler.ReconcileStep(ctx, execution.ID, step, result)
		if err != nil {
			return "", err
		}
		if !known {
			return "", &StepOutcomeUnknownError{execution.ID, step.ID()}
		}
		if result == nil {
			result = &StepResult{StepID: step.ID(), StartedAt: time.Now()}
			execution.StepResults[step.ID()] = result
		}
		now := time.Now()
		result.Status, result.Error, result.CompletedAt = StatusCompleted, "", &now
		if output != nil {
			result.Output, result.HasOutput = output.Data, true
			for key, value := range output.Variables {
				execution.Context.Variables[key] = value
			}
		}
		found := false
		for _, id := range execution.Context.CompletedSteps {
			if id == step.ID() {
				found = true
				break
			}
		}
		if !found {
			execution.Context.CompletedSteps = append(execution.Context.CompletedSteps, step.ID())
		}
	}
	input, err := restoredInput(execution)
	if err != nil {
		return "", fmt.Errorf("decode original workflow input: %w", err)
	}
	return e.startExecution(ctx, wf, execution, input, true)
}

func joinRecoveryError(current error, executionID string, err error) error {
	return errors.Join(current, fmt.Errorf("recover execution %s: %w", executionID, err))
}

func workflowRetryStopped(ctx context.Context, err error) bool {
	if _, interrupted := interrupt.IsInterruptSignal(err); interrupted || errors.Is(err, interrupt.ErrInterrupted) {
		return true
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, hr.ErrOperationUnresolved) || errors.Is(err, hr.ErrCommitUnknown) {
		return true
	}
	return llm.IsUnsafeToReplay(err)
}
