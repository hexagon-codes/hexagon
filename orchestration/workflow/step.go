package workflow

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/retry"
)

// StepType 步骤类型
type StepType string

const (
	// StepTypeNormal 普通步骤
	StepTypeNormal StepType = "normal"
	// StepTypeParallel 并行步骤
	StepTypeParallel StepType = "parallel"
	// StepTypeConditional 条件步骤
	StepTypeConditional StepType = "conditional"
	// StepTypeLoop 循环步骤
	StepTypeLoop StepType = "loop"
	// StepTypeSubWorkflow 子工作流步骤
	StepTypeSubWorkflow StepType = "sub_workflow"
	// StepTypeWait 等待步骤
	StepTypeWait StepType = "wait"
)

// Step 步骤接口
type Step interface {
	// ID 返回步骤 ID
	ID() string

	// Name 返回步骤名称
	Name() string

	// Type 返回步骤类型
	Type() StepType

	// Execute 执行步骤
	Execute(ctx context.Context, input StepInput) (*StepOutput, error)

	// Validate 验证步骤配置
	Validate() error
}

// StepInput 步骤输入
type StepInput struct {
	// Data 输入数据
	Data any

	// Variables 上下文变量
	Variables map[string]any

	// PreviousOutputs 前置步骤的输出
	PreviousOutputs map[string]any

	// Metadata 元数据
	Metadata map[string]any
}

// StepOutput 步骤输出
type StepOutput struct {
	// Data 输出数据
	Data any

	// Variables 更新的变量
	Variables map[string]any

	// Metadata 元数据
	Metadata map[string]any

	// NextStepID 下一步骤 ID（用于条件步骤）
	NextStepID string
}

// StepFunc 步骤执行函数
type StepFunc func(ctx context.Context, input StepInput) (*StepOutput, error)

// ============== BaseStep ==============

// BaseStep 基础步骤实现
type BaseStep struct {
	id           string
	name         string
	description  string
	executeFn    StepFunc
	retryPolicy  *RetryPolicy
	timeout      time.Duration
	dependencies []string
	metadata     map[string]any
}

// BaseStepOption 基础步骤选项
type BaseStepOption func(*BaseStep)

// WithStepDescription 设置步骤描述
func WithStepDescription(desc string) BaseStepOption {
	return func(s *BaseStep) {
		s.description = desc
	}
}

// WithStepRetryPolicy 设置步骤重试策略
func WithStepRetryPolicy(policy *RetryPolicy) BaseStepOption {
	return func(s *BaseStep) {
		s.retryPolicy = policy
	}
}

// WithStepTimeout 设置步骤超时时间
func WithStepTimeout(timeout time.Duration) BaseStepOption {
	return func(s *BaseStep) {
		s.timeout = timeout
	}
}

// WithStepDependencies 设置步骤依赖
func WithStepDependencies(deps ...string) BaseStepOption {
	return func(s *BaseStep) {
		s.dependencies = deps
	}
}

// WithStepMetadata 设置步骤元数据
func WithStepMetadata(key string, value any) BaseStepOption {
	return func(s *BaseStep) {
		if s.metadata == nil {
			s.metadata = make(map[string]any)
		}
		s.metadata[key] = value
	}
}

// NewStep 创建基础步骤
func NewStep(id, name string, fn StepFunc, opts ...BaseStepOption) *BaseStep {
	s := &BaseStep{
		id:        id,
		name:      name,
		executeFn: fn,
		metadata:  make(map[string]any),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ID 返回步骤 ID
func (s *BaseStep) ID() string {
	return s.id
}

// Name 返回步骤名称
func (s *BaseStep) Name() string {
	return s.name
}

// Type 返回步骤类型
func (s *BaseStep) Type() StepType {
	return StepTypeNormal
}

// Execute 执行步骤
func (s *BaseStep) Execute(ctx context.Context, input StepInput) (*StepOutput, error) {
	// 应用超时
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	// 执行（带重试）
	var lastErr error
	maxRetries := 0
	if s.retryPolicy != nil {
		maxRetries = s.retryPolicy.MaxRetries
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		output, err := s.executeFn(ctx, input)
		if err == nil {
			return output, nil
		}

		lastErr = err

		// 检查是否需要重试
		if attempt < maxRetries && s.retryPolicy != nil {
			interval := s.calculateRetryInterval(attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(interval):
				continue
			}
		}
	}

	return nil, lastErr
}

// Validate 验证步骤配置
func (s *BaseStep) Validate() error {
	if s.id == "" {
		return fmt.Errorf("step id cannot be empty")
	}
	if s.name == "" {
		return fmt.Errorf("step name cannot be empty")
	}
	if s.executeFn == nil {
		return fmt.Errorf("step execute function cannot be nil")
	}
	return nil
}

// calculateRetryInterval 计算重试间隔
func (s *BaseStep) calculateRetryInterval(attempt int) time.Duration {
	if s.retryPolicy == nil {
		return time.Second
	}

	// 复用 toolkit/util/retry 指数退避，退避序列与原手写实现逐项一致：
	// InitialInterval, ×Multiplier 递增，封顶 MaxInterval。
	// ExponentialBackoff 的 multiplier 为 Multiplier^(n-1)，故 attempt 偏移 +1
	// （calculateRetryInterval(0)=InitialInterval=ExponentialBackoff(1)）。
	cfg := retry.DefaultConfig()
	cfg.Delay = s.retryPolicy.InitialInterval
	cfg.MaxDelay = s.retryPolicy.MaxInterval
	cfg.Multiplier = s.retryPolicy.Multiplier
	return retry.ExponentialBackoff(attempt+1, cfg)
}

// Dependencies 返回依赖的步骤 ID
func (s *BaseStep) Dependencies() []string {
	return s.dependencies
}

// ============== ParallelStep ==============

// ParallelStep 并行步骤
type ParallelStep struct {
	id          string
	name        string
	steps       []Step
	failFast    bool
	maxParallel int
}

// ParallelStepOption 并行步骤选项
type ParallelStepOption func(*ParallelStep)

// WithFailFast 设置快速失败
func WithFailFast(failFast bool) ParallelStepOption {
	return func(s *ParallelStep) {
		s.failFast = failFast
	}
}

// WithMaxParallel 设置最大并行数
func WithMaxParallel(max int) ParallelStepOption {
	return func(s *ParallelStep) {
		s.maxParallel = max
	}
}

// NewParallelStep 创建并行步骤
func NewParallelStep(id, name string, steps []Step, opts ...ParallelStepOption) *ParallelStep {
	s := &ParallelStep{
		id:          id,
		name:        name,
		steps:       steps,
		failFast:    true,
		maxParallel: 0, // 无限制
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ID 返回步骤 ID
func (s *ParallelStep) ID() string {
	return s.id
}

// Name 返回步骤名称
func (s *ParallelStep) Name() string {
	return s.name
}

// Type 返回步骤类型
func (s *ParallelStep) Type() StepType {
	return StepTypeParallel
}

// Execute 执行并行步骤
func (s *ParallelStep) Execute(ctx context.Context, input StepInput) (*StepOutput, error) {
	if len(s.steps) == 0 {
		return &StepOutput{Data: nil}, nil
	}

	// 创建可取消的上下文
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 结果收集
	type result struct {
		stepID string
		output *StepOutput
		err    error
	}

	results := make(chan result, len(s.steps))
	var wg sync.WaitGroup

	// 控制并行数
	sem := make(chan struct{}, s.maxParallelCount())

	for _, step := range s.steps {
		wg.Add(1)
		go func(step Step) {
			defer wg.Done()

			// 获取信号量
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results <- result{stepID: step.ID(), err: ctx.Err()}
				return
			}

			output, err := step.Execute(ctx, input)
			results <- result{stepID: step.ID(), output: output, err: err}

			if err != nil && s.failFast {
				cancel()
			}
		}(step)
	}

	// 等待所有步骤完成
	go func() {
		wg.Wait()
		close(results)
	}()

	// 收集结果
	outputs := make(map[string]any)
	var firstErr error

	for res := range results {
		if res.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("step %s failed: %w", res.stepID, res.err)
		}
		if res.output != nil {
			outputs[res.stepID] = res.output.Data
		}
	}

	if firstErr != nil && s.failFast {
		return nil, firstErr
	}

	return &StepOutput{
		Data: outputs,
	}, firstErr
}

// Validate 验证步骤配置
func (s *ParallelStep) Validate() error {
	if s.id == "" {
		return fmt.Errorf("parallel step id cannot be empty")
	}
	if len(s.steps) == 0 {
		return fmt.Errorf("parallel step must have at least one sub-step")
	}
	for _, step := range s.steps {
		if err := step.Validate(); err != nil {
			return fmt.Errorf("sub-step %s validation failed: %w", step.ID(), err)
		}
	}
	return nil
}

func (s *ParallelStep) maxParallelCount() int {
	if s.maxParallel <= 0 {
		return len(s.steps)
	}
	return s.maxParallel
}

// ============== ConditionalStep ==============

// ConditionalStep 条件步骤
type ConditionalStep struct {
	id        string
	name      string
	condition ConditionFunc
	thenStep  Step
	elseStep  Step
	branches  map[string]Step
}

// ConditionFunc 条件函数
type ConditionFunc func(ctx context.Context, input StepInput) (string, error)

// NewConditionalStep 创建条件步骤
func NewConditionalStep(id, name string, condition ConditionFunc) *ConditionalStep {
	return &ConditionalStep{
		id:        id,
		name:      name,
		condition: condition,
		branches:  make(map[string]Step),
	}
}

// Then 设置条件为真时执行的步骤
func (s *ConditionalStep) Then(step Step) *ConditionalStep {
	s.thenStep = step
	s.branches["true"] = step
	return s
}

// Else 设置条件为假时执行的步骤
func (s *ConditionalStep) Else(step Step) *ConditionalStep {
	s.elseStep = step
	s.branches["false"] = step
	return s
}

// Branch 添加分支
func (s *ConditionalStep) Branch(name string, step Step) *ConditionalStep {
	s.branches[name] = step
	return s
}

// ID 返回步骤 ID
func (s *ConditionalStep) ID() string {
	return s.id
}

// Name 返回步骤名称
func (s *ConditionalStep) Name() string {
	return s.name
}

// Type 返回步骤类型
func (s *ConditionalStep) Type() StepType {
	return StepTypeConditional
}

// Execute 执行条件步骤
func (s *ConditionalStep) Execute(ctx context.Context, input StepInput) (*StepOutput, error) {
	// 评估条件
	branchName, err := s.condition(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("condition evaluation failed: %w", err)
	}

	// 找到对应的分支
	branch, ok := s.branches[branchName]
	if !ok {
		return &StepOutput{
			Data:       nil,
			NextStepID: branchName,
		}, nil
	}

	// 执行分支
	output, err := branch.Execute(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("branch %s execution failed: %w", branchName, err)
	}

	return output, nil
}

// Validate 验证步骤配置
func (s *ConditionalStep) Validate() error {
	if s.id == "" {
		return fmt.Errorf("conditional step id cannot be empty")
	}
	if s.condition == nil {
		return fmt.Errorf("condition function cannot be nil")
	}
	if len(s.branches) == 0 {
		return fmt.Errorf("conditional step must have at least one branch")
	}
	for name, branch := range s.branches {
		if err := branch.Validate(); err != nil {
			return fmt.Errorf("branch %s validation failed: %w", name, err)
		}
	}
	return nil
}

// ============== LoopStep ==============

// LoopStep 循环步骤
type LoopStep struct {
	id            string
	name          string
	step          Step
	condition     ConditionFunc
	maxIterations int
	collectOutput bool
}

// LoopStepOption 循环步骤选项
type LoopStepOption func(*LoopStep)

// WithMaxIterations 设置最大迭代次数
func WithMaxIterations(max int) LoopStepOption {
	return func(s *LoopStep) {
		s.maxIterations = max
	}
}

// WithCollectOutput 设置是否收集输出
func WithCollectOutput(collect bool) LoopStepOption {
	return func(s *LoopStep) {
		s.collectOutput = collect
	}
}

// NewLoopStep 创建循环步骤
// condition 返回 "continue" 继续循环，返回 "break" 退出
func NewLoopStep(id, name string, step Step, condition ConditionFunc, opts ...LoopStepOption) *LoopStep {
	s := &LoopStep{
		id:            id,
		name:          name,
		step:          step,
		condition:     condition,
		maxIterations: 100,
		collectOutput: true,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ID 返回步骤 ID
func (s *LoopStep) ID() string {
	return s.id
}

// Name 返回步骤名称
func (s *LoopStep) Name() string {
	return s.name
}

// Type 返回步骤类型
func (s *LoopStep) Type() StepType {
	return StepTypeLoop
}

// Execute 执行循环步骤
func (s *LoopStep) Execute(ctx context.Context, input StepInput) (*StepOutput, error) {
	var outputs []any
	currentInput := input

	for i := 0; i < s.maxIterations; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// 检查条件
		action, err := s.condition(ctx, currentInput)
		if err != nil {
			return nil, fmt.Errorf("loop condition failed at iteration %d: %w", i, err)
		}
		if action == "break" {
			break
		}

		// 执行步骤
		output, err := s.step.Execute(ctx, currentInput)
		if err != nil {
			return nil, fmt.Errorf("loop step failed at iteration %d: %w", i, err)
		}

		if s.collectOutput && output != nil {
			outputs = append(outputs, output.Data)
		}

		// 更新输入
		if output != nil {
			currentInput.Data = output.Data
			for k, v := range output.Variables {
				currentInput.Variables[k] = v
			}
		}
	}

	if s.collectOutput {
		return &StepOutput{Data: outputs}, nil
	}
	return &StepOutput{Data: currentInput.Data}, nil
}

// Validate 验证步骤配置
func (s *LoopStep) Validate() error {
	if s.id == "" {
		return fmt.Errorf("loop step id cannot be empty")
	}
	if s.step == nil {
		return fmt.Errorf("loop step body cannot be nil")
	}
	if s.condition == nil {
		return fmt.Errorf("loop condition cannot be nil")
	}
	return s.step.Validate()
}

// ============== WaitStep ==============

// WaitStep 等待步骤
type WaitStep struct {
	id       string
	name     string
	duration time.Duration
	until    func(ctx context.Context, input StepInput) (bool, error)
}

// NewWaitStep 创建等待步骤（固定时间）
func NewWaitStep(id, name string, duration time.Duration) *WaitStep {
	return &WaitStep{
		id:       id,
		name:     name,
		duration: duration,
	}
}

// NewWaitUntilStep 创建等待步骤（等待条件满足）
func NewWaitUntilStep(id, name string, until func(ctx context.Context, input StepInput) (bool, error)) *WaitStep {
	return &WaitStep{
		id:    id,
		name:  name,
		until: until,
	}
}

// ID 返回步骤 ID
func (s *WaitStep) ID() string {
	return s.id
}

// Name 返回步骤名称
func (s *WaitStep) Name() string {
	return s.name
}

// Type 返回步骤类型
func (s *WaitStep) Type() StepType {
	return StepTypeWait
}

// Execute 执行等待步骤
func (s *WaitStep) Execute(ctx context.Context, input StepInput) (*StepOutput, error) {
	if s.duration > 0 {
		// 固定时间等待
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.duration):
			return &StepOutput{Data: input.Data}, nil
		}
	}

	if s.until != nil {
		// 等待条件满足
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
				satisfied, err := s.until(ctx, input)
				if err != nil {
					return nil, fmt.Errorf("wait condition check failed: %w", err)
				}
				if satisfied {
					return &StepOutput{Data: input.Data}, nil
				}
			}
		}
	}

	return &StepOutput{Data: input.Data}, nil
}

// Validate 验证步骤配置
func (s *WaitStep) Validate() error {
	if s.id == "" {
		return fmt.Errorf("wait step id cannot be empty")
	}
	if s.duration <= 0 && s.until == nil {
		return fmt.Errorf("wait step must have either duration or until condition")
	}
	return nil
}

// ============== SubWorkflowStep ==============

// SubWorkflowStep 子工作流步骤
type SubWorkflowStep struct {
	id       string
	name     string
	workflow *Workflow
	runner   WorkflowRunner
}

// NewSubWorkflowStep 创建子工作流步骤
func NewSubWorkflowStep(id, name string, workflow *Workflow, runner WorkflowRunner) *SubWorkflowStep {
	return &SubWorkflowStep{
		id:       id,
		name:     name,
		workflow: workflow,
		runner:   runner,
	}
}

// ID 返回步骤 ID
func (s *SubWorkflowStep) ID() string {
	return s.id
}

// Name 返回步骤名称
func (s *SubWorkflowStep) Name() string {
	return s.name
}

// Type 返回步骤类型
func (s *SubWorkflowStep) Type() StepType {
	return StepTypeSubWorkflow
}

// Execute 执行子工作流步骤
func (s *SubWorkflowStep) Execute(ctx context.Context, input StepInput) (*StepOutput, error) {
	workflowInput := WorkflowInput{
		Data:      input.Data,
		Variables: input.Variables,
		Metadata:  input.Metadata,
	}

	output, err := s.runner.Run(ctx, s.workflow, workflowInput)
	if err != nil {
		return nil, fmt.Errorf("sub-workflow execution failed: %w", err)
	}

	return &StepOutput{
		Data:      output.Data,
		Variables: output.Variables,
		Metadata:  output.Metadata,
	}, nil
}

// Validate 验证步骤配置
func (s *SubWorkflowStep) Validate() error {
	if s.id == "" {
		return fmt.Errorf("sub-workflow step id cannot be empty")
	}
	if s.workflow == nil {
		return fmt.Errorf("sub-workflow cannot be nil")
	}
	if s.runner == nil {
		return fmt.Errorf("sub-workflow runner cannot be nil")
	}
	return nil
}
