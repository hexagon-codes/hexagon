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

// ReflectionAgent 自我反思 Agent
//
// Reflection Agent 在执行任务后进行自我反思，评估输出质量，
// 并在质量不满足要求时自动重试。
//
// 反思流程：
//  1. 执行任务生成初始输出
//  2. 使用 Reflector 评估输出质量
//  3. 识别优缺点和改进建议
//  4. 如果质量未达标，根据反馈重新执行
//  5. 重复直到达标或达到最大迭代次数
//
// 使用示例：
//
//	agent := NewReflection(
//	    WithLLM(llmProvider),
//	    WithReflectionMaxIterations(5),
//	    WithReflectionQualityTarget(0.8),
//	)
//	output, err := agent.Run(ctx, Input{Query: "撰写一篇高质量文章"})
type ReflectionAgent struct {
	*BaseAgent

	// reflector 反思器
	reflector Reflector

	// maxIterations 最大反思迭代次数
	maxIterations int

	// qualityTarget 目标质量分数 (0.0 - 1.0)
	qualityTarget float32

	// minIterations 最小迭代次数（即使质量达标也至少执行）
	minIterations int
}

// Reflector 反思器接口
// 负责评估输出质量并提供改进建议
type Reflector interface {
	// Reflect 对输出进行反思
	// 返回反思结果，包括质量评分、优缺点和改进建议
	Reflect(ctx context.Context, input Input, output Output) (*Reflection, error)

	// ScoreQuality 仅评估输出质量分数
	// 返回 0.0-1.0 之间的分数
	ScoreQuality(ctx context.Context, input Input, output Output) (float32, error)
}

// Reflection 反思结果
type Reflection struct {
	// Quality 质量评分 (0.0 - 1.0)
	Quality float32 `json:"quality"`

	// Strengths 优点列表
	Strengths []string `json:"strengths,omitempty"`

	// Weaknesses 缺点列表
	Weaknesses []string `json:"weaknesses,omitempty"`

	// Suggestions 改进建议列表
	Suggestions []string `json:"suggestions,omitempty"`

	// ShouldRetry 是否需要重试
	ShouldRetry bool `json:"should_retry"`

	// Feedback 给下次执行的反馈（用于改进）
	Feedback string `json:"feedback,omitempty"`
}

// ReflectionOption Reflection Agent 配置选项
type ReflectionOption func(*ReflectionAgent)

// WithReflector 设置反思器
func WithReflector(r Reflector) ReflectionOption {
	return func(a *ReflectionAgent) {
		a.reflector = r
	}
}

// WithReflectionMaxIterations 设置最大反思迭代次数
// 默认值: 3
func WithReflectionMaxIterations(n int) ReflectionOption {
	return func(a *ReflectionAgent) {
		if n > 0 {
			a.maxIterations = n
		}
	}
}

// WithReflectionQualityTarget 设置目标质量分数
// 默认值: 0.8
func WithReflectionQualityTarget(target float32) ReflectionOption {
	return func(a *ReflectionAgent) {
		if target > 0 && target <= 1.0 {
			a.qualityTarget = target
		}
	}
}

// WithReflectionMinIterations 设置最小迭代次数
// 默认值: 1
func WithReflectionMinIterations(n int) ReflectionOption {
	return func(a *ReflectionAgent) {
		if n > 0 {
			a.minIterations = n
		}
	}
}

// NewReflection 创建 Reflection Agent
//
// 参数：
//   - opts: Agent 基础配置选项
//   - rOpts: Reflection 特有配置选项
//
// 使用示例：
//
//	agent := NewReflection(
//	    []Option{WithLLM(llm)},
//	    WithReflectionQualityTarget(0.85),
//	)
func NewReflection(opts []Option, rOpts ...ReflectionOption) *ReflectionAgent {
	base := NewBaseAgent(opts...)

	// 设置默认名称
	if base.config.Name == "Agent" {
		base.config.Name = "ReflectionAgent"
	}

	agent := &ReflectionAgent{
		BaseAgent:     base,
		maxIterations: 3,
		qualityTarget: 0.8,
		minIterations: 1,
	}

	for _, opt := range rOpts {
		opt(agent)
	}

	return agent
}

// Run 执行 Reflection Agent
//
// 执行流程：
//  1. 执行任务生成初始输出
//  2. 反思评估输出质量
//  3. 如果质量未达标且未达到最大迭代次数，重试
//  4. 返回最终输出
func (a *ReflectionAgent) Run(ctx context.Context, input Input) (Output, error) {
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

	var bestOutput Output
	var bestQuality float32
	var reflections []*Reflection
	var feedback string

	// 迭代执行和反思
	for iteration := 0; iteration < a.maxIterations; iteration++ {
		if ctx.Err() != nil {
			return Output{}, ctx.Err()
		}

		// 执行任务
		output, err := a.executeTask(ctx, runID, input, feedback, iteration, hookManager)
		if err != nil {
			a.triggerError(ctx, hookManager, runID, err, "execution")
			return Output{}, err
		}

		// 反思评估
		reflection, err := a.reflect(ctx, input, output)
		if err != nil {
			// 反思失败不中断，使用默认反思结果
			reflection = &Reflection{
				Quality:     0.5,
				ShouldRetry: iteration < a.minIterations-1,
			}
		}
		reflections = append(reflections, reflection)

		// 更新最佳输出
		if reflection.Quality > bestQuality {
			bestQuality = reflection.Quality
			bestOutput = output
		}

		// 检查是否满足目标质量
		if iteration >= a.minIterations-1 && reflection.Quality >= a.qualityTarget {
			break
		}

		// 检查是否需要重试
		if !reflection.ShouldRetry && iteration >= a.minIterations-1 {
			break
		}

		// 准备下次迭代的反馈
		feedback = a.buildFeedback(reflection)
	}

	// 添加反思元数据
	bestOutput.Metadata = map[string]any{
		"iterations":    len(reflections),
		"final_quality": bestQuality,
		"reflections":   a.summarizeReflections(reflections),
	}

	// 触发运行结束钩子
	if hookManager != nil {
		hookManager.TriggerRunEnd(ctx, &hooks.RunEndEvent{
			RunID:    runID,
			AgentID:  a.ID(),
			Output:   bestOutput,
			Duration: time.Since(startTime).Milliseconds(),
		})
	}

	return bestOutput, nil
}

// executeTask 执行任务
func (a *ReflectionAgent) executeTask(ctx context.Context, runID string, input Input, feedback string, iteration int, hookManager *hooks.Manager) (Output, error) {
	// 构建提示
	var promptBuilder strings.Builder
	promptBuilder.WriteString(input.Query)

	// 如果有反馈，添加到提示中
	if feedback != "" && iteration > 0 {
		promptBuilder.WriteString(fmt.Sprintf("\n\n[改进反馈 - 第 %d 次迭代]\n%s", iteration+1, feedback))
	}

	messages := []llm.Message{}
	if a.config.SystemPrompt != "" {
		messages = append(messages, llm.Message{
			Role:    llm.RoleSystem,
			Content: a.config.SystemPrompt,
		})
	}
	messages = append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: promptBuilder.String(),
	})

	resp, err := a.completeWithRuntime(ctx, runID, messages, runtimeLLMHookSink(runID, a.config.LLM.Name(), hookManager))
	if err != nil {
		return Output{}, fmt.Errorf("LLM completion failed: %w", err)
	}

	return Output{
		Content: resp.Content,
		Usage:   resp.Usage,
	}, nil
}

// reflect 执行反思
func (a *ReflectionAgent) reflect(ctx context.Context, input Input, output Output) (*Reflection, error) {
	// 如果配置了反思器，使用它
	if a.reflector != nil {
		return a.reflector.Reflect(ctx, input, output)
	}

	// 使用 LLM 进行反思
	return a.reflectWithLLM(ctx, input, output)
}

// reflectWithLLM 使用 LLM 进行反思
func (a *ReflectionAgent) reflectWithLLM(ctx context.Context, input Input, output Output) (*Reflection, error) {
	prompt := fmt.Sprintf(`你是一个严格的质量评估专家。请对以下回复进行评估。

原始问题:
%s

生成的回复:
%s

请从以下维度评估回复质量:
1. 准确性: 回复是否正确、无错误
2. 完整性: 是否完整回答了问题
3. 清晰度: 表达是否清晰易懂
4. 相关性: 是否紧扣问题

返回 JSON 格式:
{
  "quality": 0.0-1.0 之间的分数,
  "strengths": ["优点1", "优点2"],
  "weaknesses": ["缺点1", "缺点2"],
  "suggestions": ["改进建议1", "改进建议2"],
  "should_retry": true/false,
  "feedback": "给下次执行的具体改进建议"
}

只返回 JSON，不要其他内容。`, input.Query, output.Content)

	resp, err := a.completeWithRuntime(ctx, util.GenerateID("run"), []llm.Message{
		{Role: llm.RoleUser, Content: prompt},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("reflection LLM call failed: %w", err)
	}

	// 解析响应
	return a.parseReflection(resp.Content)
}

// parseReflection 解析反思结果
func (a *ReflectionAgent) parseReflection(content string) (*Reflection, error) {
	// 提取 JSON
	jsonContent := extractJSONContent(content)
	if jsonContent == "" {
		return nil, fmt.Errorf("cannot extract JSON from reflection response")
	}

	var reflection Reflection
	if err := json.Unmarshal([]byte(jsonContent), &reflection); err != nil {
		return nil, fmt.Errorf("JSON parse failed: %w", err)
	}

	return &reflection, nil
}

// buildFeedback 构建给下次执行的反馈
func (a *ReflectionAgent) buildFeedback(reflection *Reflection) string {
	if reflection.Feedback != "" {
		return reflection.Feedback
	}

	// 从缺点和建议构建反馈
	var builder strings.Builder
	if len(reflection.Weaknesses) > 0 {
		builder.WriteString("需要改进的问题:\n")
		for _, w := range reflection.Weaknesses {
			builder.WriteString(fmt.Sprintf("- %s\n", w))
		}
	}
	if len(reflection.Suggestions) > 0 {
		builder.WriteString("\n改进建议:\n")
		for _, s := range reflection.Suggestions {
			builder.WriteString(fmt.Sprintf("- %s\n", s))
		}
	}
	return builder.String()
}

// summarizeReflections 汇总反思历史
func (a *ReflectionAgent) summarizeReflections(reflections []*Reflection) []map[string]any {
	summary := make([]map[string]any, len(reflections))
	for i, r := range reflections {
		summary[i] = map[string]any{
			"iteration":    i + 1,
			"quality":      r.Quality,
			"should_retry": r.ShouldRetry,
		}
	}
	return summary
}

// triggerError 触发错误钩子
func (a *ReflectionAgent) triggerError(ctx context.Context, hookManager *hooks.Manager, runID string, err error, phase string) {
	if hookManager != nil {
		hookManager.TriggerError(ctx, &hooks.ErrorEvent{
			RunID:   runID,
			AgentID: a.ID(),
			Error:   err,
			Phase:   phase,
		})
	}
}

// Invoke 执行 Reflection Agent（实现 Runnable 接口）
func (a *ReflectionAgent) Invoke(ctx context.Context, input Input, opts ...core.Option) (Output, error) {
	return a.Run(ctx, input)
}

// Stream 流式执行 Agent
func (a *ReflectionAgent) Stream(ctx context.Context, input Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	output, err := a.Run(ctx, input)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(output), nil
}

// Batch 批量执行 Agent
func (a *ReflectionAgent) Batch(ctx context.Context, inputs []Input, opts ...core.Option) ([]Output, error) {
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
func (a *ReflectionAgent) Collect(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (Output, error) {
	var zero Output
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		return zero, err
	}
	return a.Run(ctx, collected)
}

// Transform 转换流
func (a *ReflectionAgent) Transform(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (*stream.StreamReader[Output], error) {
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
func (a *ReflectionAgent) BatchStream(ctx context.Context, inputs []Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	results, err := a.Batch(ctx, inputs, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}

// ============== 默认 LLM 反思器 ==============

// LLMReflector 基于 LLM 的反思器
type LLMReflector struct {
	llm llm.Provider
}

// NewLLMReflector 创建 LLM 反思器
func NewLLMReflector(provider llm.Provider) *LLMReflector {
	return &LLMReflector{llm: provider}
}

// Reflect 执行反思
func (r *LLMReflector) Reflect(ctx context.Context, input Input, output Output) (*Reflection, error) {
	prompt := fmt.Sprintf(`你是一个严格的质量评估专家。请对以下回复进行评估。

原始问题:
%s

生成的回复:
%s

请从以下维度评估回复质量:
1. 准确性: 回复是否正确、无错误
2. 完整性: 是否完整回答了问题
3. 清晰度: 表达是否清晰易懂
4. 相关性: 是否紧扣问题

返回 JSON 格式:
{
  "quality": 0.0-1.0 之间的分数,
  "strengths": ["优点1", "优点2"],
  "weaknesses": ["缺点1", "缺点2"],
  "suggestions": ["改进建议1", "改进建议2"],
  "should_retry": true/false,
  "feedback": "给下次执行的具体改进建议"
}

只返回 JSON，不要其他内容。`, input.Query, output.Content)

	resp, err := runCompletionWithRuntime(ctx, r.llm, util.GenerateID("run"), []llm.Message{
		{Role: llm.RoleUser, Content: prompt},
	}, nil)
	if err != nil {
		return nil, err
	}

	// 解析响应
	jsonContent := extractJSONContent(resp.Content)
	if jsonContent == "" {
		return nil, fmt.Errorf("cannot extract JSON from reflection response")
	}

	var reflection Reflection
	if err := json.Unmarshal([]byte(jsonContent), &reflection); err != nil {
		return nil, err
	}

	return &reflection, nil
}

// ScoreQuality 评估质量分数
func (r *LLMReflector) ScoreQuality(ctx context.Context, input Input, output Output) (float32, error) {
	reflection, err := r.Reflect(ctx, input, output)
	if err != nil {
		return 0, err
	}
	return reflection.Quality, nil
}

// 确保实现了 Agent 接口
var _ Agent = (*ReflectionAgent)(nil)

// 确保实现了 Reflector 接口
var _ Reflector = (*LLMReflector)(nil)
