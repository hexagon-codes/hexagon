package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Executor 工作流执行器
type Executor struct {
	// 执行中的工作流
	executions sync.Map // executionID -> *executionState

	// 持久化存储
	store       WorkflowStore
	persistence ExecutionPersistence
	reconciler  StepReconciler

	// 事件处理器
	eventHandlers []WorkflowEventHandler

	// 钩子
	hooks *WorkflowHooks

	// 配置
	config ExecutorConfig

	mu sync.RWMutex
}

// ExecutorConfig 执行器配置
type ExecutorConfig struct {
	// DefaultTimeout 默认超时时间
	DefaultTimeout time.Duration

	// MaxConcurrentExecutions 最大并发执行数
	MaxConcurrentExecutions int

	// EnablePersistence 启用持久化
	EnablePersistence bool
}

// DefaultExecutorConfig 返回默认配置
func DefaultExecutorConfig() ExecutorConfig {
	return ExecutorConfig{
		DefaultTimeout:          time.Hour,
		MaxConcurrentExecutions: 100,
		EnablePersistence:       false,
	}
}

// executionState 执行状态
type executionState struct {
	execution      *WorkflowExecution
	workflow       *Workflow
	cancel         context.CancelFunc
	pauseCh        chan struct{}
	resumeCh       chan struct{}
	doneCh         chan struct{}
	mu             sync.Mutex
	ctx            context.Context
	persistenceErr error
	executionErr   error
}

// ExecutorOption 执行器选项
type ExecutorOption func(*Executor)

// WithStore 设置存储
func WithStore(store WorkflowStore) ExecutorOption {
	return func(e *Executor) {
		e.store = store
		e.config.EnablePersistence = true
	}
}

// WithHooks 设置钩子
func WithHooks(hooks *WorkflowHooks) ExecutorOption {
	return func(e *Executor) {
		e.hooks = hooks
	}
}

// WithExecutorConfig 设置配置
func WithExecutorConfig(config ExecutorConfig) ExecutorOption {
	return func(e *Executor) {
		e.config = config
	}
}

// NewExecutor 创建执行器
func NewExecutor(opts ...ExecutorOption) *Executor {
	e := &Executor{
		config:        DefaultExecutorConfig(),
		eventHandlers: make([]WorkflowEventHandler, 0),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// OnEvent 注册事件处理器
// 每个观察者取得独立载荷；通知失败会返回错误，不撤销已经提交的终态。
func (e *Executor) OnEvent(handler WorkflowEventHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.eventHandlers = append(e.eventHandlers, handler)
}

// emitEvent 发送事件
func (e *Executor) emitEvent(event *WorkflowEvent) error {
	e.mu.RLock()
	handlers := make([]WorkflowEventHandler, len(e.eventHandlers))
	copy(handlers, e.eventHandlers)
	e.mu.RUnlock()

	for _, handler := range handlers {
		if err := observeWorkflowValue("event."+string(event.Type), event.Data, func(data any) error {
			copy := *event
			copy.Data = data
			handler(&copy)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// Run 同步运行工作流
func (e *Executor) Run(ctx context.Context, wf *Workflow, input WorkflowInput) (*WorkflowOutput, error) {
	executionID, err := e.RunAsync(ctx, wf, input)
	if err != nil {
		return nil, err
	}

	timeout := wf.Timeout
	if timeout == 0 {
		timeout = e.config.DefaultTimeout
	}

	execution, err := e.WaitForCompletion(ctx, executionID, timeout)
	if err != nil {
		return nil, err
	}

	if execution.Status == StatusFailed {
		return nil, fmt.Errorf("workflow failed: %s", execution.Error)
	}

	if execution.Status == StatusCancelled {
		return nil, fmt.Errorf("workflow cancelled")
	}

	var output WorkflowOutput
	if len(execution.Output) > 0 {
		if err := json.Unmarshal(execution.Output, &output.Data); err != nil {
			return nil, fmt.Errorf("decode workflow output: %w", err)
		}
	}
	output.Variables = execution.Context.Variables
	output.StepOutputs = make(map[string]any)
	for stepID, result := range execution.StepResults {
		output.StepOutputs[stepID] = result.Output
	}

	return &output, nil
}

// RunAsync 异步运行工作流
func (e *Executor) RunAsync(ctx context.Context, wf *Workflow, input WorkflowInput) (string, error) {
	// 创建执行实例
	execution := NewExecution(wf)
	execution.StartedAt = time.Now()
	execution.Status = StatusRunning

	// 序列化输入
	inputData, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode workflow input: %w", err)
	}
	execution.Input, execution.InputFormat = inputData, workflowInputFormat

	// 初始化上下文
	if input.Variables != nil {
		execution.Context.Variables = input.Variables
	}
	if input.Metadata != nil {
		execution.Context.Metadata = input.Metadata
	}

	// 触发开始钩子
	if e.hooks != nil && e.hooks.OnStart != nil {
		if err := observeWorkflowValue("on_start", input, func(copy WorkflowInput) error { return e.hooks.OnStart(ctx, wf, copy) }); err != nil {
			return "", fmt.Errorf("start hook failed: %w", err)
		}
	}

	return e.startExecution(ctx, wf, execution, input, false)
}

func (e *Executor) startExecution(ctx context.Context, wf *Workflow, execution *WorkflowExecution, input WorkflowInput, recovered bool) (string, error) {
	execCtx, cancel := context.WithCancel(ctx)
	state := &executionState{execution: execution, workflow: wf, cancel: cancel, pauseCh: make(chan struct{}, 1), resumeCh: make(chan struct{}, 1), doneCh: make(chan struct{}), ctx: execCtx}
	if previous, loaded := e.executions.LoadOrStore(execution.ID, state); loaded {
		old := previous.(*executionState)
		select {
		case <-old.doneCh:
			if !e.executions.CompareAndSwap(execution.ID, old, state) {
				cancel()
				return "", fmt.Errorf("execution %s attachment changed", execution.ID)
			}
		default:
			cancel()
			return "", fmt.Errorf("execution %s is already attached", execution.ID)
		}
	}
	if err := e.persistExecution(ctx, execution); err != nil {
		e.executions.Delete(execution.ID)
		cancel()
		return "", fmt.Errorf("save execution: %w", err)
	}
	if recovered && execution.Status == StatusPaused {
		state.pauseCh <- struct{}{}
	}
	eventType := EventWorkflowStarted
	if recovered {
		eventType = EventWorkflowResumed
	}
	if err := e.emitEvent(&WorkflowEvent{
		Type:        eventType,
		ExecutionID: execution.ID,
		Status:      StatusRunning,
		Timestamp:   time.Now(),
	}); err != nil {
		e.failExecution(state, err)
		cancel()
		close(state.doneCh)
		return "", err
	}

	// 异步执行
	go e.executeWorkflow(execCtx, state, input)

	return execution.ID, nil
}

// executeWorkflow 执行工作流
func (e *Executor) executeWorkflow(ctx context.Context, state *executionState, input WorkflowInput) {
	defer close(state.doneCh)
	defer state.cancel()

	wf := state.workflow
	execution := state.execution

	// 准备步骤输入
	stepInput := StepInput{
		Data:            input.Data,
		Variables:       execution.Context.Variables,
		PreviousOutputs: make(map[string]any),
		Metadata:        execution.Context.Metadata,
	}

	// 顺序执行步骤
	for _, step := range wf.Steps {
		select {
		case <-ctx.Done():
			e.setExecutionStatus(state, StatusCancelled, ctx.Err().Error())
			return
		case <-state.pauseCh:
			if err := e.setExecutionStatus(state, StatusPaused, ""); err != nil {
				return
			}
			// 等待恢复
			select {
			case <-ctx.Done():
				e.setExecutionStatus(state, StatusCancelled, ctx.Err().Error())
				return
			case <-state.resumeCh:
				if err := e.setExecutionStatus(state, StatusRunning, ""); err != nil {
					return
				}
			}
		default:
		}
		if previous, exists := execution.StepResults[step.ID()]; exists && previous != nil && previous.Status == StatusCompleted {
			if previous.HasOutput || previous.Output != nil {
				stepInput.Data = previous.Output
			}
			stepInput.PreviousOutputs[step.ID()] = previous.Output
			continue
		}

		// 检查依赖
		if baseStep, ok := step.(*BaseStep); ok {
			for _, dep := range baseStep.Dependencies() {
				if result, completed := execution.StepResults[dep]; !completed || result == nil || result.Status != StatusCompleted {
					e.setExecutionStatus(state, StatusFailed, fmt.Sprintf("dependency %s not completed", dep))
					return
				}
			}
		}

		// 执行步骤
		state.mu.Lock()
		execution.Context.CurrentStepID = step.ID()
		stepResult := &StepResult{StepID: step.ID(), Status: StatusRunning, StartedAt: time.Now()}
		execution.StepResults[step.ID()] = stepResult
		if err := e.persistExecution(ctx, execution); err != nil {
			state.persistenceErr = err
			state.mu.Unlock()
			return
		}
		state.mu.Unlock()

		// 触发步骤开始钩子
		if e.hooks != nil && e.hooks.OnStepStart != nil {
			if err := observeWorkflowValue(step.ID()+".on_step_start", stepInput.Data, func(copy any) error { return e.hooks.OnStepStart(ctx, step, copy) }); err != nil {
				e.failExecution(state, err)
				return
			}
		}

		if err := e.emitEvent(&WorkflowEvent{
			Type:        EventStepStarted,
			ExecutionID: execution.ID,
			StepID:      step.ID(),
			Status:      StatusRunning,
			Timestamp:   time.Now(),
		}); err != nil {
			e.failExecution(state, err)
			return
		}

		output, err := invokeStep(step.ID(), func() (*StepOutput, error) { return step.Execute(ctx, stepInput) })

		state.mu.Lock()
		completedAt := time.Now()
		stepResult.CompletedAt = &completedAt
		stepResult.Duration = completedAt.Sub(stepResult.StartedAt)

		if err != nil {
			stepResult.Status = StatusFailed
			stepResult.Error = err.Error()
			if output != nil {
				stepResult.Output, stepResult.HasOutput = output.Data, true
			}
			state.mu.Unlock()

			// 触发步骤错误钩子
			var callbackErr error
			if e.hooks != nil && e.hooks.OnStepError != nil {
				callbackErr = invokeWorkflowCallback(step.ID()+".on_step_error", func() error { return e.hooks.OnStepError(ctx, step, err) })
			}

			callbackErr = errors.Join(callbackErr, e.emitEvent(&WorkflowEvent{
				Type:        EventStepFailed,
				ExecutionID: execution.ID,
				StepID:      step.ID(),
				Status:      StatusFailed,
				Error:       err.Error(),
				Timestamp:   time.Now(),
			}))

			if callbackErr != nil {
				e.failExecution(state, errors.Join(err, callbackErr))
				return
			}
			status := StatusFailed
			if ctx.Err() != nil {
				status = StatusCancelled
			}
			// 普通步骤失败保留原等待合同：返回终态记录，由 Run 转成业务执行错误。
			_ = e.setExecutionStatus(state, status, fmt.Sprintf("step %s failed: %s", step.ID(), err))
			return
		}

		stepResult.Status = StatusCompleted
		if output != nil {
			stepResult.Output = output.Data
			stepResult.HasOutput = true

			// 更新输入
			stepInput.Data = output.Data
			stepInput.PreviousOutputs[step.ID()] = output.Data

			// 合并变量
			for k, v := range output.Variables {
				stepInput.Variables[k] = v
				execution.Context.Variables[k] = v
			}
		}

		execution.Context.CompletedSteps = append(execution.Context.CompletedSteps, step.ID())
		if err := e.persistExecution(ctx, execution); err != nil {
			state.persistenceErr = err
			state.mu.Unlock()
			return
		}
		state.mu.Unlock()

		// 触发步骤完成钩子
		if e.hooks != nil && e.hooks.OnStepComplete != nil {
			if err := observeWorkflowValue(step.ID()+".on_step_complete", output, func(copy *StepOutput) error { return e.hooks.OnStepComplete(ctx, step, copy) }); err != nil {
				e.failExecution(state, err)
				return
			}
		}

		if err := e.emitEvent(&WorkflowEvent{
			Type:        EventStepCompleted,
			ExecutionID: execution.ID,
			StepID:      step.ID(),
			Status:      StatusCompleted,
			Data:        output,
			Timestamp:   time.Now(),
		}); err != nil {
			e.failExecution(state, err)
			return
		}

	}

	// 工作流完成
	if ctx.Err() != nil {
		e.setExecutionStatus(state, StatusCancelled, ctx.Err().Error())
		return
	}
	outputData, err := json.Marshal(stepInput.Data)
	if err != nil {
		e.setExecutionStatus(state, StatusFailed, err.Error())
		return
	}
	state.mu.Lock()
	execution.Output = outputData
	state.mu.Unlock()
	// 触发完成钩子
	if e.hooks != nil && e.hooks.OnComplete != nil {
		output := &WorkflowOutput{
			Data:      stepInput.Data,
			Variables: stepInput.Variables,
		}
		if err := observeWorkflowValue("on_complete", output, func(copy *WorkflowOutput) error { return e.hooks.OnComplete(ctx, wf, copy) }); err != nil {
			e.failExecution(state, err)
			return
		}
	}
	_ = e.setExecutionStatus(state, StatusCompleted, "")
}

// setExecutionStatus 设置执行状态
func (e *Executor) setExecutionStatus(state *executionState, status WorkflowStatus, errMsg string) error {
	state.mu.Lock()

	execution := state.execution
	execution.Status = status
	if errMsg != "" {
		execution.Error = errMsg
	}

	now := time.Now()
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		execution.CompletedAt = &now
		execution.Duration = now.Sub(execution.StartedAt)
	case StatusPaused:
		execution.PausedAt = &now
	}

	// 持久化
	if err := e.persistExecution(state.ctx, execution); err != nil {
		state.persistenceErr = err
		state.mu.Unlock()
		return err
	}
	state.mu.Unlock()

	// 发送事件
	var eventType WorkflowEventType
	switch status {
	case StatusCompleted:
		eventType = EventWorkflowCompleted
	case StatusFailed:
		eventType = EventWorkflowFailed
	case StatusPaused:
		eventType = EventWorkflowPaused
	case StatusCancelled:
		eventType = EventWorkflowCancelled
	case StatusRunning:
		eventType = EventWorkflowResumed
	}

	if eventType != "" {
		err := e.emitEvent(&WorkflowEvent{
			Type:        eventType,
			ExecutionID: execution.ID,
			Status:      status,
			Error:       errMsg,
			Timestamp:   now,
		})
		if err != nil {
			switch status {
			case StatusPaused, StatusRunning:
				e.failExecution(state, err)
			case StatusCompleted, StatusCancelled:
				// 终态通知失败只报告调用错误，不撤销已经提交的业务事实。
				state.mu.Lock()
				state.executionErr = errors.Join(state.executionErr, err)
				state.mu.Unlock()
			}
		}
		return err
	}
	return nil
}

// Pause 暂停执行
func (e *Executor) Pause(ctx context.Context, executionID string) error {
	stateVal, ok := e.executions.Load(executionID)
	if !ok {
		return fmt.Errorf("execution %s not found", executionID)
	}

	state := stateVal.(*executionState)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.execution.Status != StatusRunning {
		return fmt.Errorf("execution is not running")
	}

	select {
	case state.pauseCh <- struct{}{}:
		return nil
	default:
		return fmt.Errorf("already pausing")
	}
}

// Resume 恢复执行
func (e *Executor) Resume(ctx context.Context, executionID string) error {
	stateVal, ok := e.executions.Load(executionID)
	if !ok {
		return fmt.Errorf("execution %s not found", executionID)
	}

	state := stateVal.(*executionState)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.execution.Status != StatusPaused {
		return fmt.Errorf("execution is not paused")
	}

	select {
	case state.resumeCh <- struct{}{}:
		return nil
	default:
		return fmt.Errorf("already resuming")
	}
}

// Cancel 取消执行
func (e *Executor) Cancel(ctx context.Context, executionID string) error {
	stateVal, ok := e.executions.Load(executionID)
	if !ok {
		return fmt.Errorf("execution %s not found", executionID)
	}

	state := stateVal.(*executionState)
	state.cancel()
	return nil
}

// GetExecution 获取执行实例
func (e *Executor) GetExecution(ctx context.Context, executionID string) (*WorkflowExecution, error) {
	// 先从内存查找
	if stateVal, ok := e.executions.Load(executionID); ok {
		state := stateVal.(*executionState)
		state.mu.Lock()
		defer state.mu.Unlock()
		return cloneExecution(state.execution)
	}

	// 从存储查找
	if e.config.EnablePersistence && e.store != nil {
		return e.store.GetExecution(ctx, executionID)
	}

	return nil, fmt.Errorf("execution %s not found", executionID)
}

// WaitForCompletion 等待执行完成
func (e *Executor) WaitForCompletion(ctx context.Context, executionID string, timeout time.Duration) (*WorkflowExecution, error) {
	stateVal, ok := e.executions.Load(executionID)
	if !ok {
		return nil, fmt.Errorf("execution %s not found", executionID)
	}

	state := stateVal.(*executionState)

	if timeout > 0 {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		select {
		case <-state.doneCh:
			return completedExecution(state)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	select {
	case <-state.doneCh:
		return completedExecution(state)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ListExecutions 列出执行实例
func (e *Executor) ListExecutions(ctx context.Context, workflowID string, status WorkflowStatus, limit int) ([]*WorkflowExecution, error) {
	if e.config.EnablePersistence && e.store != nil {
		return e.store.ListExecutions(ctx, workflowID, status, limit)
	}

	// 从内存获取
	var executions []*WorkflowExecution
	e.executions.Range(func(_, value any) bool {
		state := value.(*executionState)
		if workflowID != "" && state.workflow.ID != workflowID {
			return true
		}
		if status != "" && state.execution.Status != status {
			return true
		}
		executions = append(executions, state.execution)
		if limit > 0 && len(executions) >= limit {
			return false
		}
		return true
	})

	return executions, nil
}

// CleanupCompleted 清理已完成的执行
func (e *Executor) CleanupCompleted(olderThan time.Duration) int {
	cutoff := time.Now().Add(-olderThan)
	cleaned := 0

	e.executions.Range(func(key, value any) bool {
		state := value.(*executionState)
		execution := state.execution

		if execution.Status == StatusCompleted || execution.Status == StatusFailed || execution.Status == StatusCancelled {
			if execution.CompletedAt != nil && execution.CompletedAt.Before(cutoff) {
				e.executions.Delete(key)
				cleaned++
			}
		}
		return true
	})

	return cleaned
}

// 确保实现了接口
var _ WorkflowRunner = (*Executor)(nil)
