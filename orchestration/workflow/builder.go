package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/hexagon-codes/toolkit/util/idgen"
)

// WorkflowBuilder 工作流构建器
type WorkflowBuilder struct {
	workflow *Workflow
	err      error
}

// New 创建工作流构建器
func New(name string) *WorkflowBuilder {
	return &WorkflowBuilder{
		workflow: &Workflow{
			ID:       generateWorkflowID(),
			Name:     name,
			Steps:    make([]Step, 0),
			StepDefs: make([]StepDefinition, 0),
			Metadata: make(map[string]any),
		},
	}
}

// WithID 设置工作流 ID
func (b *WorkflowBuilder) WithID(id string) *WorkflowBuilder {
	if b.err != nil {
		return b
	}
	b.workflow.ID = id
	return b
}

// WithDescription 设置描述
func (b *WorkflowBuilder) WithDescription(desc string) *WorkflowBuilder {
	if b.err != nil {
		return b
	}
	b.workflow.Description = desc
	return b
}

// WithVersion 设置版本
func (b *WorkflowBuilder) WithVersion(version string) *WorkflowBuilder {
	if b.err != nil {
		return b
	}
	b.workflow.Version = version
	return b
}

// WithTimeout 设置超时时间
func (b *WorkflowBuilder) WithTimeout(timeout time.Duration) *WorkflowBuilder {
	if b.err != nil {
		return b
	}
	b.workflow.Timeout = timeout
	return b
}

// WithRetryPolicy 设置重试策略
func (b *WorkflowBuilder) WithRetryPolicy(policy *RetryPolicy) *WorkflowBuilder {
	if b.err != nil {
		return b
	}
	b.workflow.RetryPolicy = policy
	return b
}

// WithMetadata 设置元数据
func (b *WorkflowBuilder) WithMetadata(key string, value any) *WorkflowBuilder {
	if b.err != nil {
		return b
	}
	b.workflow.Metadata[key] = value
	return b
}

// Add 添加步骤
func (b *WorkflowBuilder) Add(step Step) *WorkflowBuilder {
	if b.err != nil {
		return b
	}

	if step == nil {
		b.err = fmt.Errorf("step cannot be nil")
		return b
	}

	b.workflow.Steps = append(b.workflow.Steps, step)
	b.workflow.StepDefs = append(b.workflow.StepDefs, StepDefinition{
		ID:   step.ID(),
		Name: step.Name(),
		Type: step.Type(),
	})
	return b
}

// AddFunc 添加函数步骤
func (b *WorkflowBuilder) AddFunc(id, name string, fn StepFunc, opts ...BaseStepOption) *WorkflowBuilder {
	return b.Add(NewStep(id, name, fn, opts...))
}

// Sequential 顺序执行多个步骤
func (b *WorkflowBuilder) Sequential(steps ...Step) *WorkflowBuilder {
	if b.err != nil {
		return b
	}

	for _, step := range steps {
		b.Add(step)
	}
	return b
}

// Parallel 并行执行多个步骤
func (b *WorkflowBuilder) Parallel(id, name string, steps ...Step) *WorkflowBuilder {
	return b.Add(NewParallelStep(id, name, steps))
}

// ParallelFuncs 并行执行多个函数
func (b *WorkflowBuilder) ParallelFuncs(id, name string, funcs map[string]StepFunc) *WorkflowBuilder {
	steps := make([]Step, 0, len(funcs))
	for stepID, fn := range funcs {
		steps = append(steps, NewStep(stepID, stepID, fn))
	}
	return b.Parallel(id, name, steps...)
}

// Conditional 条件分支
func (b *WorkflowBuilder) Conditional(id, name string, condition ConditionFunc) *ConditionalBuilder {
	return &ConditionalBuilder{
		parent: b,
		step:   NewConditionalStep(id, name, condition),
	}
}

// Loop 循环执行
func (b *WorkflowBuilder) Loop(id, name string, step Step, condition ConditionFunc, opts ...LoopStepOption) *WorkflowBuilder {
	return b.Add(NewLoopStep(id, name, step, condition, opts...))
}

// Wait 等待固定时间
func (b *WorkflowBuilder) Wait(id, name string, duration time.Duration) *WorkflowBuilder {
	return b.Add(NewWaitStep(id, name, duration))
}

// WaitUntil 等待条件满足
func (b *WorkflowBuilder) WaitUntil(id, name string, condition func(ctx context.Context, input StepInput) (bool, error)) *WorkflowBuilder {
	return b.Add(NewWaitUntilStep(id, name, condition))
}

// SubWorkflow 子工作流
func (b *WorkflowBuilder) SubWorkflow(id, name string, workflow *Workflow, runner WorkflowRunner) *WorkflowBuilder {
	return b.Add(NewSubWorkflowStep(id, name, workflow, runner))
}

// Build 构建工作流
func (b *WorkflowBuilder) Build() (*Workflow, error) {
	if b.err != nil {
		return nil, b.err
	}

	// 验证所有步骤
	for _, step := range b.workflow.Steps {
		if err := step.Validate(); err != nil {
			return nil, fmt.Errorf("step %s validation failed: %w", step.ID(), err)
		}
	}

	return b.workflow, nil
}

// MustBuild 构建工作流，失败时 panic
//
// ⚠️ 警告：构建失败时会 panic。
// 仅在初始化时使用，不要在运行时调用。
// 推荐使用 Build() 方法并正确处理错误。
//
// 使用场景：
//   - 程序启动时的全局初始化
//   - 测试代码中
func (b *WorkflowBuilder) MustBuild() *Workflow {
	wf, err := b.Build()
	if err != nil {
		panic(fmt.Sprintf("workflow build failed: %v", err))
	}
	return wf
}

// generateWorkflowID 生成工作流 ID
func generateWorkflowID() string {
	return fmt.Sprintf("wf-%s", idgen.NanoID())
}

// ============== ConditionalBuilder ==============

// ConditionalBuilder 条件构建器
type ConditionalBuilder struct {
	parent *WorkflowBuilder
	step   *ConditionalStep
}

// Then 设置条件为真时执行的步骤
func (b *ConditionalBuilder) Then(step Step) *ConditionalBuilder {
	b.step.Then(step)
	return b
}

// ThenFunc 设置条件为真时执行的函数
func (b *ConditionalBuilder) ThenFunc(id, name string, fn StepFunc) *ConditionalBuilder {
	return b.Then(NewStep(id, name, fn))
}

// Else 设置条件为假时执行的步骤
func (b *ConditionalBuilder) Else(step Step) *ConditionalBuilder {
	b.step.Else(step)
	return b
}

// ElseFunc 设置条件为假时执行的函数
func (b *ConditionalBuilder) ElseFunc(id, name string, fn StepFunc) *ConditionalBuilder {
	return b.Else(NewStep(id, name, fn))
}

// Branch 添加分支
func (b *ConditionalBuilder) Branch(name string, step Step) *ConditionalBuilder {
	b.step.Branch(name, step)
	return b
}

// End 结束条件构建
func (b *ConditionalBuilder) End() *WorkflowBuilder {
	b.parent.Add(b.step)
	return b.parent
}

// ============== Pipeline Builder ==============

// Pipeline 创建流水线（简化的顺序工作流）
func Pipeline(name string, steps ...Step) (*Workflow, error) {
	builder := New(name)
	for _, step := range steps {
		builder.Add(step)
	}
	return builder.Build()
}

// PipelineFuncs 从函数列表创建流水线
func PipelineFuncs(name string, funcs []struct {
	ID   string
	Name string
	Fn   StepFunc
}) (*Workflow, error) {
	builder := New(name)
	for _, f := range funcs {
		builder.AddFunc(f.ID, f.Name, f.Fn)
	}
	return builder.Build()
}

// ============== Common Patterns ==============

// MapStep 创建 Map 步骤（对数组中的每个元素执行操作）
func MapStep(id, name string, fn func(ctx context.Context, item any) (any, error)) Step {
	return NewStep(id, name, func(ctx context.Context, input StepInput) (*StepOutput, error) {
		items, ok := input.Data.([]any)
		if !ok {
			return nil, fmt.Errorf("input must be an array")
		}

		results := make([]any, len(items))
		for i, item := range items {
			result, err := fn(ctx, item)
			if err != nil {
				return nil, fmt.Errorf("map failed at index %d: %w", i, err)
			}
			results[i] = result
		}

		return &StepOutput{Data: results}, nil
	})
}

// FilterStep 创建 Filter 步骤（过滤数组元素）
func FilterStep(id, name string, predicate func(item any) bool) Step {
	return NewStep(id, name, func(ctx context.Context, input StepInput) (*StepOutput, error) {
		items, ok := input.Data.([]any)
		if !ok {
			return nil, fmt.Errorf("input must be an array")
		}

		var results []any
		for _, item := range items {
			if predicate(item) {
				results = append(results, item)
			}
		}

		return &StepOutput{Data: results}, nil
	})
}

// ReduceStep 创建 Reduce 步骤（聚合数组元素）
func ReduceStep(id, name string, initial any, reducer func(acc, item any) any) Step {
	return NewStep(id, name, func(ctx context.Context, input StepInput) (*StepOutput, error) {
		items, ok := input.Data.([]any)
		if !ok {
			return nil, fmt.Errorf("input must be an array")
		}

		result := initial
		for _, item := range items {
			result = reducer(result, item)
		}

		return &StepOutput{Data: result}, nil
	})
}

// RetryWrapper 包装步骤添加重试逻辑
func RetryWrapper(step Step, policy *RetryPolicy) Step {
	base, ok := step.(*BaseStep)
	if ok {
		base.retryPolicy = policy
		return base
	}

	// 对于其他类型的步骤，创建包装器
	return NewStep(step.ID(), step.Name(), func(ctx context.Context, input StepInput) (*StepOutput, error) {
		return step.Execute(ctx, input)
	}, WithStepRetryPolicy(policy))
}

// TimeoutWrapper 包装步骤添加超时
func TimeoutWrapper(step Step, timeout time.Duration) Step {
	base, ok := step.(*BaseStep)
	if ok {
		base.timeout = timeout
		return base
	}

	return NewStep(step.ID(), step.Name(), func(ctx context.Context, input StepInput) (*StepOutput, error) {
		return step.Execute(ctx, input)
	}, WithStepTimeout(timeout))
}
