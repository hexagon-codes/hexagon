// deep.go 实现深度 Agent 模式
//
// DeepAgent 支持递归子任务分解：当遇到复杂任务时，通过内置的
// "create_subtask" 工具自动创建子 Agent 处理子任务，将结果汇总返回。
//
// 实现递归子任务分解与层级执行。
//
// 使用示例：
//
//	deep := NewDeepAgent("deep-researcher",
//	    baseAgent,
//	    WithSubAgentFactory(func(task string) Agent {
//	        return NewBaseAgent(
//	            WithName("sub-"+task[:10]),
//	            WithLLM(myLLM),
//	            WithSystemPrompt("Focus on: "+task),
//	        )
//	    }),
//	    WithMaxDepth(3),
//	)
//	output, err := deep.Run(ctx, Input{Query: "对比分析三大云厂商"})
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/memory"
	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/hexagon/core"
	"github.com/hexagon-codes/hexagon/internal/util"
)

// DeepAgent 深度 Agent
//
// 支持递归子任务分解。当主 Agent 判断任务需要拆分时，
// 通过 "create_subtask" 工具创建子任务，由子 Agent 递归处理。
//
// 关键参数：
//   - agent: 基础 Agent（需要配置 LLM）
//   - subAgentFn: 子 Agent 工厂函数
//   - maxDepth: 最大递归深度（防止无限递归）
//
// 线程安全：DeepAgent 是不可变的，创建后可安全并发使用。
type DeepAgent struct {
	primitiveBase
	config     Config
	agent      Agent
	subAgentFn func(task string) Agent
	maxDepth   int
}

// DeepOption DeepAgent 专用选项
type DeepOption func(*DeepAgent)

// WithSubAgentFactory 设置子 Agent 工厂函数
//
// 工厂函数接收子任务描述，返回处理该子任务的 Agent。
// 如果未设置，DeepAgent 将使用主 Agent 处理子任务。
func WithSubAgentFactory(fn func(task string) Agent) DeepOption {
	return func(d *DeepAgent) {
		d.subAgentFn = fn
	}
}

// WithMaxDepth 设置最大递归深度
//
// 超过最大深度时，子任务将直接由当前 Agent 处理而不再递归分解。
// 默认值: 3
func WithMaxDepth(n int) DeepOption {
	return func(d *DeepAgent) {
		d.maxDepth = n
	}
}

// NewDeepAgent 创建深度 Agent
//
// 参数：
//   - name: Agent 名称
//   - agent: 基础 Agent（需要配置 LLM 用于任务分解决策）
//   - opts: 可选配置（子 Agent 工厂、最大深度等）
func NewDeepAgent(name string, agent Agent, opts ...DeepOption) *DeepAgent {
	d := &DeepAgent{
		config: Config{
			ID:          util.AgentID(),
			Name:        name,
			Description: "Deep agent with recursive subtask decomposition",
		},
		agent:    agent,
		maxDepth: 3,
	}

	for _, opt := range opts {
		opt(d)
	}

	return d
}

// Run 执行深度 Agent
//
// 在指定深度内递归执行。如果 Agent 通过工具调用 create_subtask，
// 将创建子 Agent 处理子任务并汇总结果。
func (d *DeepAgent) Run(ctx context.Context, input Input) (Output, error) {
	return d.runAtDepth(ctx, input, 0)
}

// runAtDepth 在指定深度执行
func (d *DeepAgent) runAtDepth(ctx context.Context, input Input, depth int) (Output, error) {
	if ctx.Err() != nil {
		return Output{}, ctx.Err()
	}

	agentLLM := d.agent.LLM()
	if agentLLM == nil {
		return Output{}, fmt.Errorf("DeepAgent %q: LLM not configured", d.config.Name)
	}

	// 构建工具定义
	toolDefs := d.buildToolDefinitions(depth)
	agentTools := d.buildToolMap()

	// 构建消息
	messages := d.buildMessages(input, depth)

	// 迭代执行（类似 ReAct 循环）
	maxIterations := 10
	var allToolCalls []ToolCallRecord

	for i := 0; i < maxIterations; i++ {
		if ctx.Err() != nil {
			return Output{}, ctx.Err()
		}

		resp, err := agentLLM.Complete(ctx, llm.CompletionRequest{
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			return Output{}, fmt.Errorf("DeepAgent %q depth %d: LLM failed: %w",
				d.config.Name, depth, err)
		}

		// 没有 tool call，返回最终回答
		if len(resp.ToolCalls) == 0 {
			return Output{
				Content:   resp.Content,
				ToolCalls: allToolCalls,
				Usage:     resp.Usage,
				Metadata: map[string]any{
					"agent_type": "deep",
					"depth":      depth,
					"iterations": i + 1,
				},
			}, nil
		}

		// 添加 assistant 消息
		messages = append(messages, llm.Message{
			Role:    llm.RoleAssistant,
			Content: resp.Content,
		})

		// 处理每个 tool call
		for _, tc := range resp.ToolCalls {
			var result string

			if tc.Name == "create_subtask" {
				// 解析子任务参数
				args, parseErr := tool.ParseArgs(tc.Arguments)
				if parseErr != nil {
					result = fmt.Sprintf("subtask parse error: %v", parseErr)
				} else {
					subtaskResult, subtaskErr := d.handleSubtask(ctx, args, depth)
					if subtaskErr != nil {
						result = fmt.Sprintf("subtask error: %v", subtaskErr)
					} else {
						result = subtaskResult
					}
					allToolCalls = append(allToolCalls, ToolCallRecord{
						Name:      "create_subtask",
						Arguments: args,
						Result: tool.Result{
							Success: subtaskErr == nil,
							Output:  result,
						},
					})
				}
			} else if t, ok := agentTools[tc.Name]; ok {
				// 解析并执行普通工具
				args, parseErr := tool.ParseArgs(tc.Arguments)
				if parseErr != nil {
					result = fmt.Sprintf("argument parse error: %v", parseErr)
				} else {
					toolResult, execErr := t.Execute(ctx, args)
					if execErr != nil {
						result = fmt.Sprintf("tool error: %v", execErr)
					} else {
						result = fmt.Sprintf("%v", toolResult.Output)
					}
					allToolCalls = append(allToolCalls, ToolCallRecord{
						Name:      tc.Name,
						Arguments: args,
						Result:    toolResult,
					})
				}
			} else {
				result = fmt.Sprintf("unknown tool: %s", tc.Name)
			}

			messages = append(messages, llm.Message{
				Role:    llm.RoleTool,
				Content: result,
			})
		}
	}

	return Output{
		Content:   "max iterations reached",
		ToolCalls: allToolCalls,
		Metadata: map[string]any{
			"agent_type": "deep",
			"depth":      depth,
			"exhausted":  true,
		},
	}, nil
}

// handleSubtask 处理子任务
func (d *DeepAgent) handleSubtask(ctx context.Context, args map[string]any, currentDepth int) (string, error) {
	taskDesc, _ := args["task"].(string)
	if taskDesc == "" {
		return "", fmt.Errorf("task description is required")
	}

	// 创建子 Agent
	var subAgent Agent
	if d.subAgentFn != nil {
		subAgent = d.subAgentFn(taskDesc)
	} else {
		subAgent = d.agent
	}

	// 递归执行子任务
	subInput := Input{
		Query: taskDesc,
		Context: map[string]any{
			"parent_agent": d.config.Name,
			"depth":        currentDepth + 1,
		},
	}

	// 如果还有递归空间，用 DeepAgent 包装
	if currentDepth+1 < d.maxDepth {
		subDeep := &DeepAgent{
			config:     d.config,
			agent:      subAgent,
			subAgentFn: d.subAgentFn,
			maxDepth:   d.maxDepth,
		}
		output, err := subDeep.runAtDepth(ctx, subInput, currentDepth+1)
		if err != nil {
			return "", err
		}
		return output.Content, nil
	}

	// 到达最大深度，直接执行
	output, err := subAgent.Run(ctx, subInput)
	if err != nil {
		return "", err
	}
	return output.Content, nil
}

// buildToolDefinitions 构建工具定义
func (d *DeepAgent) buildToolDefinitions(depth int) []llm.ToolDefinition {
	var defs []llm.ToolDefinition

	// 添加 Agent 原有工具
	for _, t := range d.agent.Tools() {
		defs = append(defs, llm.NewToolDefinition(t.Name(), t.Description(), t.Schema()))
	}

	// 如果未到最大深度，添加 create_subtask 工具
	if depth < d.maxDepth-1 {
		defs = append(defs, llm.NewToolDefinition(
			"create_subtask",
			"Create a subtask for a sub-agent to handle. Use this when the task is complex and can be broken into smaller, independent parts.",
			llm.SchemaOf[SubtaskInput](),
		))
	}

	return defs
}

// buildToolMap 构建工具名称到工具的映射
func (d *DeepAgent) buildToolMap() map[string]tool.Tool {
	tools := make(map[string]tool.Tool)
	for _, t := range d.agent.Tools() {
		tools[t.Name()] = t
	}
	return tools
}

// buildMessages 构建初始消息
func (d *DeepAgent) buildMessages(input Input, depth int) []llm.Message {
	messages := make([]llm.Message, 0, 3)

	// 系统提示词
	systemPrompt := d.buildSystemPrompt(depth)
	messages = append(messages, llm.Message{
		Role:    llm.RoleSystem,
		Content: systemPrompt,
	})

	// 用户消息
	query := input.Query
	if depth > 0 {
		query = fmt.Sprintf("[Subtask at depth %d] %s", depth, input.Query)
	}
	messages = append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: query,
	})

	return messages
}

// buildSystemPrompt 构建系统提示词
func (d *DeepAgent) buildSystemPrompt(depth int) string {
	var sb strings.Builder
	sb.WriteString("You are a deep-thinking agent that can decompose complex tasks into subtasks.\n\n")

	if depth < d.maxDepth-1 {
		sb.WriteString("When a task is too complex to handle directly, use the 'create_subtask' tool ")
		sb.WriteString("to delegate parts of it to sub-agents. Each subtask should be independent ")
		sb.WriteString("and well-defined.\n\n")
	}

	sb.WriteString(fmt.Sprintf("Current depth: %d / max: %d\n", depth, d.maxDepth))

	if depth >= d.maxDepth-1 {
		sb.WriteString("You are at the maximum depth - handle the task directly without creating subtasks.\n")
	}

	return sb.String()
}

// SubtaskInput 子任务工具的输入参数
type SubtaskInput struct {
	// Task 子任务描述
	Task string `json:"task" desc:"Description of the subtask to delegate" required:"true"`
}

// === Agent 接口方法 ===

// ID 返回 Agent ID
func (d *DeepAgent) ID() string { return d.config.ID }

// Name 返回 Agent 名称
func (d *DeepAgent) Name() string { return d.config.Name }

// Description 返回描述
func (d *DeepAgent) Description() string { return d.config.Description }

// Role 返回角色
func (d *DeepAgent) Role() Role { return d.config.Role }

// Tools 返回工具列表
func (d *DeepAgent) Tools() []tool.Tool { return d.agent.Tools() }

// Memory 返回记忆系统
func (d *DeepAgent) Memory() memory.Memory { return d.config.Memory }

// LLM 返回 LLM Provider
func (d *DeepAgent) LLM() llm.Provider { return d.agent.LLM() }

// Invoke 执行 Agent
func (d *DeepAgent) Invoke(ctx context.Context, input Input, opts ...core.Option) (Output, error) {
	return d.Run(ctx, input)
}

// Stream 流式执行 Agent
func (d *DeepAgent) Stream(ctx context.Context, input Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	output, err := d.Run(ctx, input)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(output), nil
}

// Batch 批量执行 Agent
func (d *DeepAgent) Batch(ctx context.Context, inputs []Input, opts ...core.Option) ([]Output, error) {
	results := make([]Output, len(inputs))
	for i, input := range inputs {
		output, err := d.Run(ctx, input)
		if err != nil {
			return nil, err
		}
		results[i] = output
	}
	return results, nil
}

// Collect 收集流式输入并执行
func (d *DeepAgent) Collect(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (Output, error) {
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		return Output{}, err
	}
	return d.Run(ctx, collected)
}

// Transform 转换流
func (d *DeepAgent) Transform(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (*stream.StreamReader[Output], error) {
	reader, writer := stream.Pipe[Output](10)
	go func() {
		defer writer.Close()
		for {
			in, err := input.Recv()
			if err != nil {
				return
			}
			result, err := d.Run(ctx, in)
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
func (d *DeepAgent) BatchStream(ctx context.Context, inputs []Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	results, err := d.Batch(ctx, inputs, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}

// 确保实现了 Agent 接口
var _ Agent = (*DeepAgent)(nil)
