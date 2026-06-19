package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/hexagon/core"
	"github.com/hexagon-codes/hexagon/hooks"
	"github.com/hexagon-codes/hexagon/internal/util"
	"github.com/hexagon-codes/hexagon/orchestration/planner"
)

// PlanExecuteAgent 计划执行分离的 Agent
//
// Plan-and-Execute 模式将任务规划和执行分离，让 Agent 能够：
//   - 先生成完整的执行计划
//   - 按步骤执行计划
//   - 根据执行结果动态调整计划
//   - 在失败时进行重计划
//
// 与 ReAct Agent 的区别：
//   - ReAct: 边思考边行动，适合简单任务
//   - Plan-Execute: 先规划后执行，适合复杂多步任务
//
// 使用示例：
//
//	agent := NewPlanExecute(
//	    WithLLM(llmProvider),
//	    WithTools(searchTool, calculatorTool),
//	    WithPlanExecutePlanner(planner.NewSequentialPlanner(...)),
//	)
//	output, err := agent.Run(ctx, Input{Query: "完成复杂的多步任务"})
type PlanExecuteAgent struct {
	*BaseAgent

	// planner 任务规划器
	planner planner.Planner

	// maxReplans 最大重计划次数
	maxReplans int

	// replanOnFailure 步骤失败时是否自动重计划
	replanOnFailure bool

	// summarizer 结果汇总器（可选）
	summarizer Summarizer
}

// Summarizer 结果汇总器接口
type Summarizer interface {
	// Summarize 汇总执行结果
	Summarize(ctx context.Context, goal string, steps []*planner.Step) (string, error)
}

// PlanExecuteOption Plan-Execute Agent 配置选项
type PlanExecuteOption func(*PlanExecuteAgent)

// WithPlanExecutePlanner 设置规划器
func WithPlanExecutePlanner(p planner.Planner) PlanExecuteOption {
	return func(a *PlanExecuteAgent) {
		a.planner = p
	}
}

// WithPlanExecuteMaxReplans 设置最大重计划次数
// 默认值: 3
func WithPlanExecuteMaxReplans(n int) PlanExecuteOption {
	return func(a *PlanExecuteAgent) {
		if n > 0 {
			a.maxReplans = n
		}
	}
}

// WithPlanExecuteReplanOnFailure 设置步骤失败时是否自动重计划
// 默认值: true
func WithPlanExecuteReplanOnFailure(enabled bool) PlanExecuteOption {
	return func(a *PlanExecuteAgent) {
		a.replanOnFailure = enabled
	}
}

// WithPlanExecuteSummarizer 设置结果汇总器
func WithPlanExecuteSummarizer(s Summarizer) PlanExecuteOption {
	return func(a *PlanExecuteAgent) {
		a.summarizer = s
	}
}

// NewPlanExecute 创建 Plan-Execute Agent
//
// 参数：
//   - opts: Agent 基础配置选项
//   - peOpts: Plan-Execute 特有配置选项
//
// 使用示例：
//
//	agent := NewPlanExecute(
//	    WithLLM(llm),
//	    WithTools(tools...),
//	    WithPlanExecutePlanner(planner),
//	)
func NewPlanExecute(opts []Option, peOpts ...PlanExecuteOption) *PlanExecuteAgent {
	base := NewBaseAgent(opts...)

	// 设置默认名称
	if base.config.Name == "Agent" {
		base.config.Name = "PlanExecuteAgent"
	}

	agent := &PlanExecuteAgent{
		BaseAgent:       base,
		maxReplans:      3,
		replanOnFailure: true,
	}

	for _, opt := range peOpts {
		opt(agent)
	}

	return agent
}

// Run 执行 Plan-Execute Agent
//
// 执行流程：
//  1. 使用 Planner 生成执行计划
//  2. 按顺序执行每个步骤
//  3. 收集执行结果
//  4. 步骤失败时进行重计划（如果启用）
//  5. 汇总所有结果生成最终回复
func (a *PlanExecuteAgent) Run(ctx context.Context, input Input) (Output, error) {
	if a.config.LLM == nil {
		return Output{}, fmt.Errorf("LLM provider not configured")
	}

	// 生成运行 ID
	runID := util.GenerateID("run")
	startTime := time.Now()

	// 获取钩子管理器
	hookManager := hooks.ManagerFromContext(ctx)

	// 触发运行开始钩子
	if hookManager != nil {
		if err := hookManager.TriggerRunStart(ctx, &hooks.RunStartEvent{
			RunID:   runID,
			AgentID: a.ID(),
			Input:   input,
		}); err != nil {
			return Output{}, fmt.Errorf("run start hook failed: %w", err)
		}
	}

	var output Output
	var runErr error

	// 1. 生成执行计划
	plan, err := a.createPlan(ctx, runID, input.Query)
	if err != nil {
		runErr = fmt.Errorf("planning failed: %w", err)
		a.triggerError(ctx, hookManager, runID, runErr, "planning")
		return Output{}, runErr
	}

	// 记录计划到元数据
	output.Metadata = map[string]any{
		"plan_id":     plan.ID,
		"plan_goal":   plan.Goal,
		"total_steps": len(plan.Steps),
	}

	// 2. 执行计划
	replanCount := 0
	for {
		// 执行所有待执行的步骤
		for _, step := range plan.Steps {
			if ctx.Err() != nil {
				runErr = ctx.Err()
				break
			}

			// 跳过已完成或已跳过的步骤
			if step.State == planner.StepStateCompleted || step.State == planner.StepStateSkipped {
				continue
			}

			// 检查依赖是否满足
			if !a.checkDependencies(plan, step) {
				continue
			}

			// 执行步骤
			step.State = planner.StepStateRunning
			result, err := a.executeStep(ctx, runID, step, hookManager)
			step.Result = result

			if err != nil || (result != nil && !result.Success) {
				step.State = planner.StepStateFailed

				// 如果启用了失败时重计划
				if a.replanOnFailure && replanCount < a.maxReplans && a.planner != nil {
					replanCount++
					feedback := a.buildFailureFeedback(step)
					newPlan, replanErr := a.planner.Replan(ctx, plan, feedback)
					if replanErr == nil {
						plan = newPlan
						break // 重新开始执行循环
					}
				}

				// 不重计划则记录错误
				if err != nil {
					runErr = fmt.Errorf("step %d failed: %w", step.Index+1, err)
				} else if result != nil {
					runErr = fmt.Errorf("step %d failed: %s", step.Index+1, result.Error)
				}
				break
			}

			step.State = planner.StepStateCompleted

			// 记录工具调用
			if step.Action != nil && step.Action.Type == planner.ActionTypeTool {
				output.ToolCalls = append(output.ToolCalls, ToolCallRecord{
					Name:      step.Action.Name,
					Arguments: step.Action.Parameters,
					Result: tool.Result{
						Success: result.Success,
						Output:  result.Output,
						Error:   result.Error,
					},
				})
			}
		}

		// 检查是否需要继续重计划循环
		if runErr != nil || a.allStepsCompleted(plan) {
			break
		}
	}

	// 更新计划状态
	if runErr != nil {
		plan.State = planner.PlanStateFailed
	} else {
		plan.State = planner.PlanStateCompleted
	}

	// 3. 汇总执行结果
	if runErr == nil {
		summary, err := a.summarizeResults(ctx, runID, plan)
		if err != nil {
			// 汇总失败不影响主流程，使用简单汇总
			summary = a.buildSimpleSummary(plan)
		}
		output.Content = summary
	}

	// 更新元数据
	output.Metadata["completed_steps"] = a.countCompletedSteps(plan)
	output.Metadata["replan_count"] = replanCount

	// 触发运行结束钩子
	if hookManager != nil {
		if runErr != nil {
			hookManager.TriggerError(ctx, &hooks.ErrorEvent{
				RunID:   runID,
				AgentID: a.ID(),
				Error:   runErr,
				Phase:   "run",
			})
		} else {
			hookManager.TriggerRunEnd(ctx, &hooks.RunEndEvent{
				RunID:    runID,
				AgentID:  a.ID(),
				Output:   output,
				Duration: time.Since(startTime).Milliseconds(),
			})
		}
	}

	if runErr != nil {
		return Output{}, runErr
	}

	return output, nil
}

// createPlan 创建执行计划
func (a *PlanExecuteAgent) createPlan(ctx context.Context, runID, goal string) (*planner.Plan, error) {
	// 如果配置了规划器，使用它
	if a.planner != nil {
		return a.planner.Plan(ctx, goal)
	}

	// 没有规划器时，使用 LLM 直接生成计划
	return a.planWithLLM(ctx, runID, goal)
}

// planWithLLM 使用 LLM 直接生成计划
func (a *PlanExecuteAgent) planWithLLM(ctx context.Context, runID, goal string) (*planner.Plan, error) {
	// 构建工具描述
	toolsDesc := a.buildToolsDescription()

	prompt := fmt.Sprintf(`你是一个任务规划专家。请将以下目标分解为可执行的步骤序列。

目标: %s

可用工具:
%s

要求:
1. 每个步骤应该是原子操作
2. 步骤之间有明确的执行顺序
3. 最多 10 个步骤

返回 JSON 格式:
{
  "steps": [
    {
      "description": "步骤描述",
      "action": {
        "type": "tool",
        "name": "工具名称",
        "parameters": {}
      }
    }
  ]
}

只返回 JSON，不要其他内容。`, goal, toolsDesc)

	resp, err := a.completeWithRuntime(ctx, runID, []llm.Message{
		{Role: llm.RoleUser, Content: prompt},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("LLM planning failed: %w", err)
	}

	// 解析响应
	steps, err := a.parseStepsFromResponse(resp.Content)
	if err != nil {
		// 解析失败时返回包含单一步骤的计划
		return &planner.Plan{
			ID:        util.GenerateID("plan"),
			Goal:      goal,
			Steps:     []*planner.Step{},
			State:     planner.PlanStatePending,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}, nil
	}

	return &planner.Plan{
		ID:        util.GenerateID("plan"),
		Goal:      goal,
		Steps:     steps,
		State:     planner.PlanStatePending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

// buildToolsDescription 构建工具描述
func (a *PlanExecuteAgent) buildToolsDescription() string {
	if len(a.config.Tools) == 0 {
		return "无可用工具"
	}

	var builder strings.Builder
	for i, t := range a.config.Tools {
		builder.WriteString(fmt.Sprintf("%d. %s: %s\n", i+1, t.Name(), t.Description()))
	}
	return builder.String()
}

// parseStepsFromResponse 从 LLM 响应解析步骤
func (a *PlanExecuteAgent) parseStepsFromResponse(content string) ([]*planner.Step, error) {
	// 提取 JSON
	jsonContent := extractJSONContent(content)
	if jsonContent == "" {
		return nil, fmt.Errorf("cannot extract JSON from response")
	}

	var result struct {
		Steps []struct {
			Description string `json:"description"`
			Action      *struct {
				Type       string         `json:"type"`
				Name       string         `json:"name"`
				Parameters map[string]any `json:"parameters"`
			} `json:"action"`
		} `json:"steps"`
	}

	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return nil, fmt.Errorf("JSON parse failed: %w", err)
	}

	steps := make([]*planner.Step, len(result.Steps))
	for i, s := range result.Steps {
		var action *planner.Action
		if s.Action != nil {
			action = &planner.Action{
				Type:       planner.ActionType(s.Action.Type),
				Name:       s.Action.Name,
				Parameters: s.Action.Parameters,
			}
		}
		steps[i] = &planner.Step{
			ID:          fmt.Sprintf("step-%d", i+1),
			Index:       i,
			Description: s.Description,
			Action:      action,
			State:       planner.StepStatePending,
		}
	}

	return steps, nil
}

// extractJSONContent 从文本中提取 JSON
func extractJSONContent(content string) string {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start == -1 || end == -1 || start >= end {
		return ""
	}
	return content[start : end+1]
}

// executeStep 执行单个步骤
func (a *PlanExecuteAgent) executeStep(ctx context.Context, runID string, step *planner.Step, hookManager *hooks.Manager) (*planner.StepResult, error) {
	// 如果没有动作，使用 LLM 执行
	if step.Action == nil {
		return a.executeWithLLM(ctx, runID, step, hookManager)
	}

	switch step.Action.Type {
	case planner.ActionTypeTool:
		return a.executeTool(ctx, runID, step, hookManager)
	case planner.ActionTypeLLM:
		return a.executeWithLLM(ctx, runID, step, hookManager)
	case planner.ActionTypeAgent:
		return a.executeAgent(ctx, runID, step, hookManager)
	default:
		// 默认使用 LLM 执行
		return a.executeWithLLM(ctx, runID, step, hookManager)
	}
}

// executeTool 执行工具调用
func (a *PlanExecuteAgent) executeTool(ctx context.Context, runID string, step *planner.Step, hookManager *hooks.Manager) (*planner.StepResult, error) {
	startTime := time.Now()

	// 查找工具
	var targetTool tool.Tool
	for _, t := range a.config.Tools {
		if t.Name() == step.Action.Name {
			targetTool = t
			break
		}
	}

	if targetTool == nil {
		return &planner.StepResult{
			Success:  false,
			Error:    fmt.Sprintf("tool '%s' not found", step.Action.Name),
			Duration: time.Since(startTime).Milliseconds(),
		}, nil
	}

	toolID := util.GenerateID("tool")

	// 触发工具开始钩子
	if hookManager != nil {
		hookManager.TriggerToolStart(ctx, &hooks.ToolStartEvent{
			RunID:    runID,
			ToolName: step.Action.Name,
			ToolID:   toolID,
			Input:    step.Action.Parameters,
		})
	}

	// 执行工具
	result, err := targetTool.Execute(ctx, step.Action.Parameters)
	duration := time.Since(startTime).Milliseconds()

	// 触发工具结束钩子
	if hookManager != nil {
		hookManager.TriggerToolEnd(ctx, &hooks.ToolEndEvent{
			RunID:    runID,
			ToolName: step.Action.Name,
			ToolID:   toolID,
			Output:   result,
			Duration: duration,
			Error:    err,
		})
	}

	if err != nil {
		return &planner.StepResult{
			Success:  false,
			Error:    err.Error(),
			Duration: duration,
		}, err
	}

	return &planner.StepResult{
		Success:  result.Success,
		Output:   result.Output,
		Error:    result.Error,
		Duration: duration,
	}, nil
}

// executeWithLLM 使用 LLM 执行步骤
func (a *PlanExecuteAgent) executeWithLLM(ctx context.Context, runID string, step *planner.Step, hookManager *hooks.Manager) (*planner.StepResult, error) {
	startTime := time.Now()

	prompt := fmt.Sprintf("请执行以下任务并返回结果:\n\n%s", step.Description)

	resp, err := a.completeWithRuntime(ctx, runID, []llm.Message{
		{Role: llm.RoleUser, Content: prompt},
	}, runtimeLLMHookSink(runID, a.config.LLM.Name(), hookManager))

	if err != nil {
		return &planner.StepResult{
			Success:  false,
			Error:    err.Error(),
			Duration: time.Since(startTime).Milliseconds(),
		}, err
	}

	return &planner.StepResult{
		Success:  true,
		Output:   resp.Content,
		Duration: time.Since(startTime).Milliseconds(),
		Tokens:   resp.Usage.TotalTokens,
	}, nil
}

// executeAgent 执行 Agent 调用
func (a *PlanExecuteAgent) executeAgent(ctx context.Context, runID string, step *planner.Step, hookManager *hooks.Manager) (*planner.StepResult, error) {
	// Agent 调用暂时使用 LLM 代替
	return a.executeWithLLM(ctx, runID, step, hookManager)
}

// checkDependencies 检查步骤依赖是否满足
func (a *PlanExecuteAgent) checkDependencies(plan *planner.Plan, step *planner.Step) bool {
	if len(step.Dependencies) == 0 {
		return true
	}

	for _, depID := range step.Dependencies {
		for _, s := range plan.Steps {
			if s.ID == depID && s.State != planner.StepStateCompleted {
				return false
			}
		}
	}
	return true
}

// allStepsCompleted 检查所有步骤是否完成
func (a *PlanExecuteAgent) allStepsCompleted(plan *planner.Plan) bool {
	for _, step := range plan.Steps {
		if step.State != planner.StepStateCompleted && step.State != planner.StepStateSkipped {
			return false
		}
	}
	return true
}

// countCompletedSteps 统计完成的步骤数
func (a *PlanExecuteAgent) countCompletedSteps(plan *planner.Plan) int {
	count := 0
	for _, step := range plan.Steps {
		if step.State == planner.StepStateCompleted {
			count++
		}
	}
	return count
}

// buildFailureFeedback 构建失败反馈
func (a *PlanExecuteAgent) buildFailureFeedback(step *planner.Step) string {
	feedback := fmt.Sprintf("步骤 %d (%s) 执行失败", step.Index+1, step.Description)
	if step.Result != nil && step.Result.Error != "" {
		feedback += fmt.Sprintf(": %s", step.Result.Error)
	}
	return feedback
}

// summarizeResults 汇总执行结果
func (a *PlanExecuteAgent) summarizeResults(ctx context.Context, runID string, plan *planner.Plan) (string, error) {
	// 如果配置了汇总器，使用它
	if a.summarizer != nil {
		return a.summarizer.Summarize(ctx, plan.Goal, plan.Steps)
	}

	// 使用 LLM 汇总
	return a.summarizeWithLLM(ctx, runID, plan)
}

// summarizeWithLLM 使用 LLM 汇总结果
func (a *PlanExecuteAgent) summarizeWithLLM(ctx context.Context, runID string, plan *planner.Plan) (string, error) {
	// 构建步骤执行摘要
	var stepsBuilder strings.Builder
	for _, step := range plan.Steps {
		status := "待执行"
		result := ""
		switch step.State {
		case planner.StepStateCompleted:
			status = "✓ 完成"
			if step.Result != nil && step.Result.Output != nil {
				result = fmt.Sprintf(" - 结果: %v", step.Result.Output)
			}
		case planner.StepStateFailed:
			status = "✗ 失败"
			if step.Result != nil {
				result = fmt.Sprintf(" - 错误: %s", step.Result.Error)
			}
		case planner.StepStateSkipped:
			status = "○ 跳过"
		}
		stepsBuilder.WriteString(fmt.Sprintf("%d. %s [%s]%s\n", step.Index+1, step.Description, status, result))
	}

	prompt := fmt.Sprintf(`请根据以下任务执行情况生成一个简洁的总结回复。

原始目标: %s

执行步骤:
%s

要求:
1. 简洁明了地说明任务完成情况
2. 包含关键结果和数据
3. 如果有失败的步骤，说明原因
4. 用自然语言回复，不要使用 JSON`, plan.Goal, stepsBuilder.String())

	resp, err := a.completeWithRuntime(ctx, runID, []llm.Message{
		{Role: llm.RoleUser, Content: prompt},
	}, nil)
	if err != nil {
		return a.buildSimpleSummary(plan), nil
	}

	return resp.Content, nil
}

// buildSimpleSummary 构建简单汇总
func (a *PlanExecuteAgent) buildSimpleSummary(plan *planner.Plan) string {
	completed := a.countCompletedSteps(plan)
	total := len(plan.Steps)

	if completed == total {
		return fmt.Sprintf("任务完成。成功执行了 %d 个步骤。", total)
	}
	return fmt.Sprintf("任务部分完成。执行了 %d/%d 个步骤。", completed, total)
}

// triggerError 触发错误钩子
func (a *PlanExecuteAgent) triggerError(ctx context.Context, hookManager *hooks.Manager, runID string, err error, phase string) {
	if hookManager != nil {
		hookManager.TriggerError(ctx, &hooks.ErrorEvent{
			RunID:   runID,
			AgentID: a.ID(),
			Error:   err,
			Phase:   phase,
		})
	}
}

// Invoke 执行 PlanExecute Agent（实现 Runnable 接口）
func (a *PlanExecuteAgent) Invoke(ctx context.Context, input Input, opts ...core.Option) (Output, error) {
	return a.Run(ctx, input)
}

// Stream 流式执行 Agent
func (a *PlanExecuteAgent) Stream(ctx context.Context, input Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	output, err := a.Run(ctx, input)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(output), nil
}

// Batch 批量执行 Agent
func (a *PlanExecuteAgent) Batch(ctx context.Context, inputs []Input, opts ...core.Option) ([]Output, error) {
	results := make([]Output, len(inputs))
	for i, input := range inputs {
		output, err := a.Run(ctx, input)
		if err != nil {
			return nil, err
		}
		results[i] = output
	}
	return results, nil
}

// Collect 收集流式输入并执行
func (a *PlanExecuteAgent) Collect(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (Output, error) {
	var zero Output
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		return zero, err
	}
	return a.Run(ctx, collected)
}

// Transform 转换流
func (a *PlanExecuteAgent) Transform(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (*stream.StreamReader[Output], error) {
	reader, writer := stream.Pipe[Output](10)
	go func() {
		defer writer.Close()
		for {
			in, err := input.Recv()
			if err != nil {
				return
			}
			result, err := a.Run(ctx, in)
			if err != nil {
				writer.CloseWithError(err)
				return
			}
			writer.Send(result)
		}
	}()
	return reader, nil
}

// BatchStream 批量流式执行
func (a *PlanExecuteAgent) BatchStream(ctx context.Context, inputs []Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	results, err := a.Batch(ctx, inputs, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}

// 确保实现了 Agent 接口
var _ Agent = (*PlanExecuteAgent)(nil)
