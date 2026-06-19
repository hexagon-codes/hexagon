// Package workflow 提供 Hexagon AI Agent 框架的工作流引擎
//
// 工作流引擎支持定义和执行复杂的多步骤任务，具有以下特性：
// - 顺序执行、并行执行、条件分支
// - 暂停/恢复执行
// - 持久化和恢复
// - 错误处理和重试
//
// 基本用法：
//
//	wf := workflow.New("my-workflow").
//	    Add(step1).
//	    Add(step2).
//	    Parallel(step3, step4).
//	    Build()
//
//	result, err := wf.Run(ctx, input)
package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/idgen"
)

// WorkflowStatus 工作流状态
type WorkflowStatus string

const (
	// StatusPending 等待执行
	StatusPending WorkflowStatus = "pending"
	// StatusRunning 执行中
	StatusRunning WorkflowStatus = "running"
	// StatusPaused 已暂停
	StatusPaused WorkflowStatus = "paused"
	// StatusCompleted 已完成
	StatusCompleted WorkflowStatus = "completed"
	// StatusFailed 执行失败
	StatusFailed WorkflowStatus = "failed"
	// StatusCancelled 已取消
	StatusCancelled WorkflowStatus = "cancelled"
)

// Workflow 工作流定义
type Workflow struct {
	// ID 唯一标识符
	ID string `json:"id"`

	// Name 工作流名称
	Name string `json:"name"`

	// Description 描述
	Description string `json:"description,omitempty"`

	// Version 版本
	Version string `json:"version,omitempty"`

	// Steps 步骤列表
	Steps []Step `json:"-"`

	// StepDefs 步骤定义（用于序列化）
	StepDefs []StepDefinition `json:"steps"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// Timeout 总超时时间
	Timeout time.Duration `json:"timeout,omitempty"`

	// RetryPolicy 默认重试策略
	RetryPolicy *RetryPolicy `json:"retry_policy,omitempty"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`
}

// StepDefinition 步骤定义（用于序列化）
type StepDefinition struct {
	// ID 步骤 ID
	ID string `json:"id"`

	// Name 步骤名称
	Name string `json:"name"`

	// Type 步骤类型
	Type StepType `json:"type"`

	// Description 描述
	Description string `json:"description,omitempty"`

	// Config 配置
	Config map[string]any `json:"config,omitempty"`

	// Dependencies 依赖的步骤 ID
	Dependencies []string `json:"dependencies,omitempty"`
}

// WorkflowExecution 工作流执行实例
type WorkflowExecution struct {
	// ID 执行实例 ID
	ID string `json:"id"`

	// WorkflowID 工作流 ID
	WorkflowID string `json:"workflow_id"`

	// Status 状态
	Status WorkflowStatus `json:"status"`

	// Input 输入数据
	Input json.RawMessage `json:"input,omitempty"`

	// Output 输出数据
	Output json.RawMessage `json:"output,omitempty"`

	// Error 错误信息
	Error string `json:"error,omitempty"`

	// Context 执行上下文
	Context *ExecutionContext `json:"context"`

	// StepResults 步骤执行结果
	StepResults map[string]*StepResult `json:"step_results"`

	// StartedAt 开始时间
	StartedAt time.Time `json:"started_at"`

	// CompletedAt 完成时间
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// PausedAt 暂停时间
	PausedAt *time.Time `json:"paused_at,omitempty"`

	// Duration 执行时长
	Duration time.Duration `json:"duration,omitempty"`
}

// ExecutionContext 执行上下文
type ExecutionContext struct {
	// Variables 变量
	Variables map[string]any `json:"variables,omitempty"`

	// CurrentStepID 当前步骤 ID
	CurrentStepID string `json:"current_step_id,omitempty"`

	// CompletedSteps 已完成的步骤
	CompletedSteps []string `json:"completed_steps,omitempty"`

	// PendingSteps 待执行的步骤
	PendingSteps []string `json:"pending_steps,omitempty"`

	// SkippedSteps 跳过的步骤
	SkippedSteps []string `json:"skipped_steps,omitempty"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// StepResult 步骤执行结果
type StepResult struct {
	// StepID 步骤 ID
	StepID string `json:"step_id"`

	// Status 状态
	Status WorkflowStatus `json:"status"`

	// Output 输出数据
	Output any `json:"output,omitempty"`

	// Error 错误信息
	Error string `json:"error,omitempty"`

	// StartedAt 开始时间
	StartedAt time.Time `json:"started_at"`

	// CompletedAt 完成时间
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// Duration 执行时长
	Duration time.Duration `json:"duration,omitempty"`

	// RetryCount 重试次数
	RetryCount int `json:"retry_count,omitempty"`
}

// RetryPolicy 重试策略
type RetryPolicy struct {
	// MaxRetries 最大重试次数
	MaxRetries int `json:"max_retries"`

	// InitialInterval 初始间隔
	InitialInterval time.Duration `json:"initial_interval"`

	// MaxInterval 最大间隔
	MaxInterval time.Duration `json:"max_interval"`

	// Multiplier 间隔倍数
	Multiplier float64 `json:"multiplier"`

	// RetryableErrors 可重试的错误类型
	RetryableErrors []string `json:"retryable_errors,omitempty"`
}

// DefaultRetryPolicy 返回默认重试策略
func DefaultRetryPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxRetries:      3,
		InitialInterval: time.Second,
		MaxInterval:     time.Minute,
		Multiplier:      2.0,
	}
}

// WorkflowInput 工作流输入
type WorkflowInput struct {
	// Data 输入数据
	Data any `json:"data"`

	// Variables 初始变量
	Variables map[string]any `json:"variables,omitempty"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// WorkflowOutput 工作流输出
type WorkflowOutput struct {
	// Data 输出数据
	Data any `json:"data"`

	// Variables 最终变量
	Variables map[string]any `json:"variables,omitempty"`

	// StepOutputs 各步骤输出
	StepOutputs map[string]any `json:"step_outputs,omitempty"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// WorkflowRunner 工作流运行器接口
type WorkflowRunner interface {
	// Run 运行工作流
	Run(ctx context.Context, wf *Workflow, input WorkflowInput) (*WorkflowOutput, error)

	// RunAsync 异步运行工作流
	RunAsync(ctx context.Context, wf *Workflow, input WorkflowInput) (string, error)

	// Pause 暂停执行
	Pause(ctx context.Context, executionID string) error

	// Resume 恢复执行
	Resume(ctx context.Context, executionID string) error

	// Cancel 取消执行
	Cancel(ctx context.Context, executionID string) error

	// GetExecution 获取执行实例
	GetExecution(ctx context.Context, executionID string) (*WorkflowExecution, error)

	// WaitForCompletion 等待执行完成
	WaitForCompletion(ctx context.Context, executionID string, timeout time.Duration) (*WorkflowExecution, error)
}

// WorkflowEvent 工作流事件
type WorkflowEvent struct {
	// Type 事件类型
	Type WorkflowEventType `json:"type"`

	// ExecutionID 执行实例 ID
	ExecutionID string `json:"execution_id"`

	// StepID 步骤 ID（如果适用）
	StepID string `json:"step_id,omitempty"`

	// Status 状态
	Status WorkflowStatus `json:"status"`

	// Data 事件数据
	Data any `json:"data,omitempty"`

	// Error 错误信息
	Error string `json:"error,omitempty"`

	// Timestamp 时间戳
	Timestamp time.Time `json:"timestamp"`
}

// WorkflowEventType 工作流事件类型
type WorkflowEventType string

const (
	// EventWorkflowStarted 工作流开始
	EventWorkflowStarted WorkflowEventType = "workflow_started"
	// EventWorkflowCompleted 工作流完成
	EventWorkflowCompleted WorkflowEventType = "workflow_completed"
	// EventWorkflowFailed 工作流失败
	EventWorkflowFailed WorkflowEventType = "workflow_failed"
	// EventWorkflowPaused 工作流暂停
	EventWorkflowPaused WorkflowEventType = "workflow_paused"
	// EventWorkflowResumed 工作流恢复
	EventWorkflowResumed WorkflowEventType = "workflow_resumed"
	// EventWorkflowCancelled 工作流取消
	EventWorkflowCancelled WorkflowEventType = "workflow_cancelled"
	// EventStepStarted 步骤开始
	EventStepStarted WorkflowEventType = "step_started"
	// EventStepCompleted 步骤完成
	EventStepCompleted WorkflowEventType = "step_completed"
	// EventStepFailed 步骤失败
	EventStepFailed WorkflowEventType = "step_failed"
	// EventStepSkipped 步骤跳过
	EventStepSkipped WorkflowEventType = "step_skipped"
	// EventStepRetrying 步骤重试
	EventStepRetrying WorkflowEventType = "step_retrying"
)

// WorkflowEventHandler 工作流事件处理器
type WorkflowEventHandler func(event *WorkflowEvent)

// WorkflowHooks 工作流钩子
type WorkflowHooks struct {
	// OnStart 工作流开始时触发
	OnStart func(ctx context.Context, wf *Workflow, input WorkflowInput) error

	// OnComplete 工作流完成时触发
	OnComplete func(ctx context.Context, wf *Workflow, output *WorkflowOutput) error

	// OnError 工作流出错时触发
	OnError func(ctx context.Context, wf *Workflow, err error) error

	// OnStepStart 步骤开始时触发
	OnStepStart func(ctx context.Context, step Step, input any) error

	// OnStepComplete 步骤完成时触发
	OnStepComplete func(ctx context.Context, step Step, output any) error

	// OnStepError 步骤出错时触发
	OnStepError func(ctx context.Context, step Step, err error) error
}

// WorkflowRegistry 工作流注册中心
type WorkflowRegistry struct {
	workflows map[string]*Workflow
	mu        sync.RWMutex
}

// NewWorkflowRegistry 创建工作流注册中心
func NewWorkflowRegistry() *WorkflowRegistry {
	return &WorkflowRegistry{
		workflows: make(map[string]*Workflow),
	}
}

// Register 注册工作流
func (r *WorkflowRegistry) Register(wf *Workflow) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if wf.ID == "" {
		return fmt.Errorf("workflow id cannot be empty")
	}

	r.workflows[wf.ID] = wf
	return nil
}

// Get 获取工作流
func (r *WorkflowRegistry) Get(id string) (*Workflow, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	wf, ok := r.workflows[id]
	return wf, ok
}

// List 列出所有工作流
func (r *WorkflowRegistry) List() []*Workflow {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Workflow, 0, len(r.workflows))
	for _, wf := range r.workflows {
		result = append(result, wf)
	}
	return result
}

// Remove 移除工作流
func (r *WorkflowRegistry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.workflows, id)
}

// generateExecutionID 生成执行实例 ID
func generateExecutionID() string {
	return fmt.Sprintf("exec-%s", idgen.NanoID())
}

// NewExecution 创建执行实例
func NewExecution(wf *Workflow) *WorkflowExecution {
	return &WorkflowExecution{
		ID:          generateExecutionID(),
		WorkflowID:  wf.ID,
		Status:      StatusPending,
		StepResults: make(map[string]*StepResult),
		Context: &ExecutionContext{
			Variables:      make(map[string]any),
			CompletedSteps: make([]string, 0),
			PendingSteps:   make([]string, 0),
			Metadata:       make(map[string]any),
		},
	}
}
