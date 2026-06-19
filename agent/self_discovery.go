package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/hexagon/core"
	"github.com/hexagon-codes/hexagon/hooks"
	"github.com/hexagon-codes/hexagon/internal/util"
)

// SelfDiscoveryAgent 自我发现 Agent
//
// Self-Discovery Agent 参考 Google 的 SELF-DISCOVER 论文实现，
// 通过以下四个阶段解决复杂推理任务：
//
//  1. SELECT（选择）: 从推理模块库中选择与任务相关的推理模块
//  2. ADAPT（适配）: 将选中的模块适配到具体任务
//  3. IMPLEMENT（实现）: 生成任务特定的推理结构
//  4. EXECUTE（执行）: 使用生成的结构执行推理
//
// 内置推理模块包括：
//   - 批判性思维: 评估论点和证据
//   - 逐步推理: 分解复杂问题
//   - 创造性思维: 生成新颖解决方案
//   - 系统分析: 理解组件间关系
//   - 类比推理: 利用相似性解决问题
//   - 归纳推理: 从具体到一般
//   - 演绎推理: 从一般到具体
//
// 使用示例：
//
//	agent := NewSelfDiscovery(
//	    WithLLM(llmProvider),
//	    WithSelfDiscoveryModules(
//	        CriticalThinkingModule,
//	        StepByStepModule,
//	    ),
//	)
//	output, err := agent.Run(ctx, Input{Query: "解决复杂推理问题"})
type SelfDiscoveryAgent struct {
	*BaseAgent

	// modules 可用的推理模块
	modules []ReasoningModule

	// maxModules 最多选择的模块数
	maxModules int
}

// ReasoningModule 推理模块
// 每个模块代表一种推理策略
type ReasoningModule struct {
	// Name 模块名称
	Name string `json:"name"`

	// Description 模块描述
	Description string `json:"description"`

	// Template 推理模板（用于 ADAPT 阶段）
	Template string `json:"template"`

	// Examples 示例（可选）
	Examples []string `json:"examples,omitempty"`
}

// 预定义的推理模块
var (
	// CriticalThinkingModule 批判性思维模块
	CriticalThinkingModule = ReasoningModule{
		Name:        "批判性思维",
		Description: "评估论点的有效性、识别假设和偏见、分析证据的可靠性",
		Template:    "1. 识别核心论点\n2. 找出隐含假设\n3. 评估证据质量\n4. 考虑反对意见\n5. 得出结论",
	}

	// StepByStepModule 逐步推理模块
	StepByStepModule = ReasoningModule{
		Name:        "逐步推理",
		Description: "将复杂问题分解为更小的可管理步骤，按顺序解决",
		Template:    "1. 理解问题\n2. 分解为子问题\n3. 逐个解决\n4. 整合结果",
	}

	// CreativeThinkingModule 创造性思维模块
	CreativeThinkingModule = ReasoningModule{
		Name:        "创造性思维",
		Description: "生成新颖的想法和解决方案，打破常规思维",
		Template:    "1. 重新定义问题\n2. 头脑风暴多种方案\n3. 组合不同想法\n4. 评估可行性",
	}

	// SystemAnalysisModule 系统分析模块
	SystemAnalysisModule = ReasoningModule{
		Name:        "系统分析",
		Description: "理解系统组件之间的关系、因果链和反馈循环",
		Template:    "1. 识别系统组件\n2. 分析组件关系\n3. 找出关键节点\n4. 预测系统行为",
	}

	// AnalogicalReasoningModule 类比推理模块
	AnalogicalReasoningModule = ReasoningModule{
		Name:        "类比推理",
		Description: "利用已知领域的知识来理解新领域",
		Template:    "1. 找到相似的已知情况\n2. 映射相似性\n3. 转移解决方案\n4. 验证适用性",
	}

	// InductiveReasoningModule 归纳推理模块
	InductiveReasoningModule = ReasoningModule{
		Name:        "归纳推理",
		Description: "从具体观察中推导一般规律",
		Template:    "1. 收集具体案例\n2. 识别模式\n3. 形成假设\n4. 验证推广",
	}

	// DeductiveReasoningModule 演绎推理模块
	DeductiveReasoningModule = ReasoningModule{
		Name:        "演绎推理",
		Description: "从一般原则推导具体结论",
		Template:    "1. 确定前提\n2. 应用逻辑规则\n3. 推导结论\n4. 验证有效性",
	}

	// DecompositionModule 问题分解模块
	DecompositionModule = ReasoningModule{
		Name:        "问题分解",
		Description: "将大问题分解为更小、更容易处理的子问题",
		Template:    "1. 识别问题边界\n2. 划分子问题\n3. 确定依赖关系\n4. 制定解决顺序",
	}

	// DefaultReasoningModules 默认推理模块集
	DefaultReasoningModules = []ReasoningModule{
		CriticalThinkingModule,
		StepByStepModule,
		CreativeThinkingModule,
		SystemAnalysisModule,
		AnalogicalReasoningModule,
		InductiveReasoningModule,
		DeductiveReasoningModule,
		DecompositionModule,
	}
)

// SelfDiscoveryOption Self-Discovery Agent 配置选项
type SelfDiscoveryOption func(*SelfDiscoveryAgent)

// WithSelfDiscoveryModules 设置可用的推理模块
func WithSelfDiscoveryModules(modules ...ReasoningModule) SelfDiscoveryOption {
	return func(a *SelfDiscoveryAgent) {
		a.modules = append(a.modules, modules...)
	}
}

// WithSelfDiscoveryMaxModules 设置最多选择的模块数
// 默认值: 3
func WithSelfDiscoveryMaxModules(n int) SelfDiscoveryOption {
	return func(a *SelfDiscoveryAgent) {
		if n > 0 {
			a.maxModules = n
		}
	}
}

// NewSelfDiscovery 创建 Self-Discovery Agent
//
// 参数：
//   - opts: Agent 基础配置选项
//   - sdOpts: Self-Discovery 特有配置选项
//
// 使用示例：
//
//	agent := NewSelfDiscovery(
//	    []Option{WithLLM(llm)},
//	    WithSelfDiscoveryMaxModules(4),
//	)
func NewSelfDiscovery(opts []Option, sdOpts ...SelfDiscoveryOption) *SelfDiscoveryAgent {
	base := NewBaseAgent(opts...)

	// 设置默认名称
	if base.config.Name == "Agent" {
		base.config.Name = "SelfDiscoveryAgent"
	}

	agent := &SelfDiscoveryAgent{
		BaseAgent:  base,
		modules:    make([]ReasoningModule, 0),
		maxModules: 3,
	}

	for _, opt := range sdOpts {
		opt(agent)
	}

	// 如果没有设置模块，使用默认模块
	if len(agent.modules) == 0 {
		agent.modules = DefaultReasoningModules
	}

	return agent
}

// Run 执行 Self-Discovery Agent
//
// 执行流程：
//  1. SELECT: 选择相关的推理模块
//  2. ADAPT: 将模块适配到当前任务
//  3. IMPLEMENT: 生成任务特定的推理结构
//  4. EXECUTE: 使用结构执行推理
func (a *SelfDiscoveryAgent) Run(ctx context.Context, input Input) (Output, error) {
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

	var totalUsage llm.Usage

	// 阶段 1: SELECT - 选择推理模块
	selectedModules, usage, err := a.selectModules(ctx, input)
	if err != nil {
		a.triggerError(ctx, hookManager, runID, err, "select")
		return Output{}, fmt.Errorf("SELECT phase failed: %w", err)
	}
	totalUsage = mergeUsage(totalUsage, usage)

	// 阶段 2: ADAPT - 适配模块
	adaptedModules, usage, err := a.adaptModules(ctx, input, selectedModules)
	if err != nil {
		a.triggerError(ctx, hookManager, runID, err, "adapt")
		return Output{}, fmt.Errorf("ADAPT phase failed: %w", err)
	}
	totalUsage = mergeUsage(totalUsage, usage)

	// 阶段 3: IMPLEMENT - 生成推理结构
	reasoningStructure, usage, err := a.implementStructure(ctx, input, adaptedModules)
	if err != nil {
		a.triggerError(ctx, hookManager, runID, err, "implement")
		return Output{}, fmt.Errorf("IMPLEMENT phase failed: %w", err)
	}
	totalUsage = mergeUsage(totalUsage, usage)

	// 阶段 4: EXECUTE - 执行推理
	result, usage, err := a.executeReasoning(ctx, input, reasoningStructure)
	if err != nil {
		a.triggerError(ctx, hookManager, runID, err, "execute")
		return Output{}, fmt.Errorf("EXECUTE phase failed: %w", err)
	}
	totalUsage = mergeUsage(totalUsage, usage)

	output := Output{
		Content: result,
		Usage:   totalUsage,
		Metadata: map[string]any{
			"selected_modules":    getModuleNames(selectedModules),
			"reasoning_structure": reasoningStructure,
		},
	}

	// 触发运行结束钩子
	if hookManager != nil {
		hookManager.TriggerRunEnd(ctx, &hooks.RunEndEvent{
			RunID:    runID,
			AgentID:  a.ID(),
			Output:   output,
			Duration: time.Since(startTime).Milliseconds(),
		})
	}

	return output, nil
}

// selectModules SELECT 阶段 - 选择相关的推理模块
func (a *SelfDiscoveryAgent) selectModules(ctx context.Context, input Input) ([]ReasoningModule, llm.Usage, error) {
	// 构建模块描述
	var modulesDesc strings.Builder
	for i, m := range a.modules {
		modulesDesc.WriteString(fmt.Sprintf("%d. %s: %s\n", i+1, m.Name, m.Description))
	}

	prompt := fmt.Sprintf(`你是一个推理专家。请从以下推理模块中选择最适合解决当前任务的模块。

任务: %s

可用的推理模块:
%s

要求:
1. 选择 1-%d 个最相关的模块
2. 解释选择原因

返回 JSON 格式:
{
  "selected_modules": [
    {"name": "模块名称", "reason": "选择原因"}
  ]
}

只返回 JSON，不要其他内容。`, input.Query, modulesDesc.String(), a.maxModules)

	resp, err := a.config.LLM.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return nil, llm.Usage{}, err
	}

	// 解析响应
	selected, err := a.parseSelectedModules(resp.Content)
	if err != nil {
		// 解析失败时返回默认模块
		return []ReasoningModule{StepByStepModule}, resp.Usage, nil
	}

	return selected, resp.Usage, nil
}

// parseSelectedModules 解析选中的模块
func (a *SelfDiscoveryAgent) parseSelectedModules(content string) ([]ReasoningModule, error) {
	jsonContent := extractJSONContent(content)
	if jsonContent == "" {
		return nil, fmt.Errorf("cannot extract JSON")
	}

	var result struct {
		SelectedModules []struct {
			Name   string `json:"name"`
			Reason string `json:"reason"`
		} `json:"selected_modules"`
	}

	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return nil, err
	}

	// 匹配模块
	var selected []ReasoningModule
	for _, s := range result.SelectedModules {
		for _, m := range a.modules {
			if m.Name == s.Name {
				selected = append(selected, m)
				break
			}
		}
	}

	if len(selected) == 0 {
		return nil, fmt.Errorf("no modules matched")
	}

	return selected, nil
}

// adaptModules ADAPT 阶段 - 将模块适配到具体任务
func (a *SelfDiscoveryAgent) adaptModules(ctx context.Context, input Input, modules []ReasoningModule) ([]AdaptedModule, llm.Usage, error) {
	// 构建模块信息
	var modulesInfo strings.Builder
	for i, m := range modules {
		modulesInfo.WriteString(fmt.Sprintf("%d. %s\n模板: %s\n\n", i+1, m.Name, m.Template))
	}

	prompt := fmt.Sprintf(`请将以下推理模块适配到具体任务。

任务: %s

选中的推理模块:
%s

要求:
1. 根据任务修改每个模块的推理步骤
2. 使步骤更具体、更适合当前任务

返回 JSON 格式:
{
  "adapted_modules": [
    {
      "name": "模块名称",
      "adapted_steps": ["具体化的步骤1", "具体化的步骤2", ...]
    }
  ]
}

只返回 JSON，不要其他内容。`, input.Query, modulesInfo.String())

	resp, err := a.config.LLM.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return nil, llm.Usage{}, err
	}

	// 解析响应
	adapted, err := a.parseAdaptedModules(resp.Content)
	if err != nil {
		// 解析失败时使用原始模块
		adapted = make([]AdaptedModule, len(modules))
		for i, m := range modules {
			adapted[i] = AdaptedModule{
				Name:  m.Name,
				Steps: strings.Split(m.Template, "\n"),
			}
		}
	}

	return adapted, resp.Usage, nil
}

// AdaptedModule 适配后的模块
type AdaptedModule struct {
	Name  string   `json:"name"`
	Steps []string `json:"adapted_steps"`
}

// parseAdaptedModules 解析适配后的模块
func (a *SelfDiscoveryAgent) parseAdaptedModules(content string) ([]AdaptedModule, error) {
	jsonContent := extractJSONContent(content)
	if jsonContent == "" {
		return nil, fmt.Errorf("cannot extract JSON")
	}

	var result struct {
		AdaptedModules []AdaptedModule `json:"adapted_modules"`
	}

	if err := json.Unmarshal([]byte(jsonContent), &result); err != nil {
		return nil, err
	}

	return result.AdaptedModules, nil
}

// implementStructure IMPLEMENT 阶段 - 生成任务特定的推理结构
func (a *SelfDiscoveryAgent) implementStructure(ctx context.Context, input Input, adaptedModules []AdaptedModule) (string, llm.Usage, error) {
	// 构建适配后的模块信息
	var modulesInfo strings.Builder
	for i, m := range adaptedModules {
		modulesInfo.WriteString(fmt.Sprintf("模块 %d: %s\n", i+1, m.Name))
		for j, step := range m.Steps {
			modulesInfo.WriteString(fmt.Sprintf("  %d. %s\n", j+1, step))
		}
		modulesInfo.WriteString("\n")
	}

	prompt := fmt.Sprintf(`请根据适配后的推理模块，生成一个完整的推理结构来解决任务。

任务: %s

适配后的推理模块:
%s

要求:
1. 整合所有模块的步骤
2. 形成一个连贯的推理流程
3. 确保每个步骤清晰可执行

返回一个结构化的推理计划（纯文本，不需要 JSON）。`, input.Query, modulesInfo.String())

	resp, err := a.config.LLM.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	})
	if err != nil {
		return "", llm.Usage{}, err
	}

	return resp.Content, resp.Usage, nil
}

// executeReasoning EXECUTE 阶段 - 使用推理结构执行推理
func (a *SelfDiscoveryAgent) executeReasoning(ctx context.Context, input Input, reasoningStructure string) (string, llm.Usage, error) {
	prompt := fmt.Sprintf(`请使用以下推理结构来解决任务。

任务: %s

推理结构:
%s

请按照推理结构逐步思考，然后给出最终答案。`, input.Query, reasoningStructure)

	messages := []llm.Message{}
	if a.config.SystemPrompt != "" {
		messages = append(messages, llm.Message{
			Role:    llm.RoleSystem,
			Content: a.config.SystemPrompt,
		})
	}
	messages = append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: prompt,
	})

	resp, err := a.config.LLM.Complete(ctx, llm.CompletionRequest{
		Messages: messages,
	})
	if err != nil {
		return "", llm.Usage{}, err
	}

	return resp.Content, resp.Usage, nil
}

// triggerError 触发错误钩子
func (a *SelfDiscoveryAgent) triggerError(ctx context.Context, hookManager *hooks.Manager, runID string, err error, phase string) {
	if hookManager != nil {
		hookManager.TriggerError(ctx, &hooks.ErrorEvent{
			RunID:   runID,
			AgentID: a.ID(),
			Error:   err,
			Phase:   phase,
		})
	}
}

// Invoke 执行 SelfDiscovery Agent（实现 Runnable 接口）
func (a *SelfDiscoveryAgent) Invoke(ctx context.Context, input Input, opts ...core.Option) (Output, error) {
	return a.Run(ctx, input)
}

// Stream 流式执行 Agent
func (a *SelfDiscoveryAgent) Stream(ctx context.Context, input Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	output, err := a.Run(ctx, input)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(output), nil
}

// Batch 批量执行 Agent
func (a *SelfDiscoveryAgent) Batch(ctx context.Context, inputs []Input, opts ...core.Option) ([]Output, error) {
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
func (a *SelfDiscoveryAgent) Collect(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (Output, error) {
	var zero Output
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		return zero, err
	}
	return a.Run(ctx, collected)
}

// Transform 转换流
func (a *SelfDiscoveryAgent) Transform(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (*stream.StreamReader[Output], error) {
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
func (a *SelfDiscoveryAgent) BatchStream(ctx context.Context, inputs []Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	results, err := a.Batch(ctx, inputs, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}

// 辅助函数

// mergeUsage 合并 Token 使用统计
func mergeUsage(a, b llm.Usage) llm.Usage {
	return llm.Usage{
		PromptTokens:        a.PromptTokens + b.PromptTokens,
		CompletionTokens:    a.CompletionTokens + b.CompletionTokens,
		TotalTokens:         a.TotalTokens + b.TotalTokens,
		CacheCreationTokens: a.CacheCreationTokens + b.CacheCreationTokens,
		CacheReadTokens:     a.CacheReadTokens + b.CacheReadTokens,
	}
}

// getModuleNames 获取模块名称列表
func getModuleNames(modules []ReasoningModule) []string {
	names := make([]string, len(modules))
	for i, m := range modules {
		names[i] = m.Name
	}
	return names
}

// 确保实现了 Agent 接口
var _ Agent = (*SelfDiscoveryAgent)(nil)
