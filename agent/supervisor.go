// supervisor.go 实现监督者 Agent 模式
//
// SupervisorAgent 由一个 Manager Agent 动态决定将任务分派给哪个 Worker Agent。
// Supervisor 模式：主 Agent 调度子 Agent 并汇总结果。
//
// 执行流程：
//  1. 将所有 worker 通过 AgentAsTool 注册为 manager 的工具
//  2. manager 通过 tool call 选择 worker
//  3. 执行被选中的 worker
//  4. 将 worker 结果返回给 manager
//  5. manager 决定继续分派或返回最终结果
//
// 使用示例：
//
//	supervisor := NewSupervisor("coordinator",
//	    managerAgent,
//	    []Agent{researcher, writer, reviewer},
//	    WithSupervisorRounds(5),
//	)
//	output, err := supervisor.Run(ctx, Input{Query: "写一篇关于 AI 的文章"})
package agent

import (
	"context"
	"fmt"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/memory"
	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/hexagon/core"
	"github.com/hexagon-codes/hexagon/internal/util"
)

// SupervisorAgent 监督者 Agent
//
// 由一个 Manager Agent 动态决定将任务分派给哪个 Worker Agent。
// Manager 的工具列表中包含所有 Worker 的 AgentAsTool 包装，
// 通过 LLM 的 tool call 机制自动选择合适的 worker。
//
// 线程安全：SupervisorAgent 是不可变的，创建后可安全并发使用。
type SupervisorAgent struct {
	primitiveBase
	config    Config
	manager   Agent
	workers   map[string]Agent
	maxRounds int
}

// SupervisorOption SupervisorAgent 专用选项
type SupervisorOption func(*SupervisorAgent)

// WithSupervisorRounds 设置 Supervisor 最大轮次
//
// 每轮 manager 选择一个 worker 执行。超过最大轮次时
// 返回 manager 当前的最终回答。
// 默认值: 10
func WithSupervisorRounds(n int) SupervisorOption {
	return func(s *SupervisorAgent) {
		s.maxRounds = n
	}
}

// NewSupervisor 创建监督者 Agent
//
// 参数：
//   - name: Agent 名称
//   - manager: 管理者 Agent（需要配置 LLM，用于决策分派）
//   - workers: 工人 Agent 列表
//   - opts: 可选配置
func NewSupervisor(name string, manager Agent, workers []Agent, opts ...SupervisorOption) *SupervisorAgent {
	workerMap := make(map[string]Agent, len(workers))
	for _, w := range workers {
		workerMap[w.Name()] = w
	}

	s := &SupervisorAgent{
		config: Config{
			ID:          util.AgentID(),
			Name:        name,
			Description: fmt.Sprintf("Supervisor agent with %d workers", len(workers)),
		},
		manager:   manager,
		workers:   workerMap,
		maxRounds: 10,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Run 执行监督者流程
//
// 流程：
//  1. 构建包含所有 worker 工具的消息列表
//  2. 让 manager LLM 选择要调用的 worker
//  3. 执行选中的 worker，将结果添加到消息历史
//  4. 继续让 manager 决定下一步，直到 manager 不再调用工具
func (s *SupervisorAgent) Run(ctx context.Context, input Input) (Output, error) {
	if s.manager.LLM() == nil {
		return Output{}, fmt.Errorf("SupervisorAgent %q: manager LLM not configured", s.config.Name)
	}

	// 将所有 worker 包装为工具
	workerTools := make(map[string]tool.Tool, len(s.workers))
	for _, w := range s.workers {
		t := AgentAsTool(w)
		workerTools[t.Name()] = t
	}

	// 构建工具定义
	toolDefs := make([]llm.ToolDefinition, 0, len(workerTools))
	for _, t := range workerTools {
		toolDefs = append(toolDefs, llm.NewToolDefinition(t.Name(), t.Description(), t.Schema()))
	}

	// 构建初始消息
	messages := make([]llm.Message, 0, 4)

	// 系统提示词
	systemPrompt := s.buildSystemPrompt()
	if systemPrompt != "" {
		messages = append(messages, llm.Message{
			Role:    llm.RoleSystem,
			Content: systemPrompt,
		})
	}

	// 用户消息
	messages = append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: input.Query,
	})

	// 迭代执行
	var allToolCalls []ToolCallRecord
	for round := 0; round < s.maxRounds; round++ {
		if ctx.Err() != nil {
			return Output{}, ctx.Err()
		}

		// 调用 manager LLM
		resp, err := s.manager.LLM().Complete(ctx, llm.CompletionRequest{
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			return Output{}, fmt.Errorf("SupervisorAgent %q round %d: LLM failed: %w",
				s.config.Name, round+1, err)
		}

		// 没有 tool call，manager 给出了最终回答
		if len(resp.ToolCalls) == 0 {
			return Output{
				Content:   resp.Content,
				ToolCalls: allToolCalls,
				Usage:     resp.Usage,
				Metadata: map[string]any{
					"agent_type": "supervisor",
					"rounds":     round + 1,
				},
			}, nil
		}

		// 添加 assistant 消息
		messages = append(messages, llm.Message{
			Role:    llm.RoleAssistant,
			Content: resp.Content,
		})

		// 执行每个 tool call，将结果添加到消息历史
		for _, tc := range resp.ToolCalls {
			targetTool, ok := workerTools[tc.Name]

			var result string
			if !ok {
				result = fmt.Sprintf("unknown worker tool: %s", tc.Name)
			} else {
				// 解析参数
				args, parseErr := tool.ParseArgs(tc.Arguments)
				if parseErr != nil {
					result = fmt.Sprintf("argument parse error: %v", parseErr)
				} else {
					toolResult, execErr := targetTool.Execute(ctx, args)
					if execErr != nil {
						result = fmt.Sprintf("execution error: %v", execErr)
					} else if toolResult.Success {
						result = fmt.Sprintf("%v", toolResult.Output)
					} else {
						result = fmt.Sprintf("failed: %v", toolResult.Output)
					}

					allToolCalls = append(allToolCalls, ToolCallRecord{
						Name:      tc.Name,
						Arguments: args,
						Result:    toolResult,
					})
				}
			}

			// 添加 tool 结果消息
			messages = append(messages, llm.Message{
				Role:    llm.RoleTool,
				Content: result,
			})
		}
	}

	return Output{
		Content:   "max rounds reached",
		ToolCalls: allToolCalls,
		Metadata: map[string]any{
			"agent_type": "supervisor",
			"rounds":     s.maxRounds,
			"exhausted":  true,
		},
	}, nil
}

// buildSystemPrompt 构建 manager 的系统提示词
func (s *SupervisorAgent) buildSystemPrompt() string {
	prompt := "You are a supervisor agent coordinating multiple workers. "
	prompt += "Use the available tools to delegate tasks to the most appropriate worker. "
	prompt += "When you have enough information to answer, respond directly without calling tools.\n\n"
	prompt += "Available workers:\n"
	for name, w := range s.workers {
		desc := w.Description()
		if desc == "" {
			desc = "no description"
		}
		prompt += fmt.Sprintf("- %s: %s\n", name, desc)
	}
	return prompt
}

// === Agent 接口方法 ===

// ID 返回 Agent ID
func (s *SupervisorAgent) ID() string { return s.config.ID }

// Name 返回 Agent 名称
func (s *SupervisorAgent) Name() string { return s.config.Name }

// Description 返回描述
func (s *SupervisorAgent) Description() string { return s.config.Description }

// Role 返回角色
func (s *SupervisorAgent) Role() Role { return s.config.Role }

// Tools 返回工具列表（聚合 manager + 所有 worker 工具）
func (s *SupervisorAgent) Tools() []tool.Tool {
	agents := make([]Agent, 0, len(s.workers)+1)
	agents = append(agents, s.manager)
	for _, w := range s.workers {
		agents = append(agents, w)
	}
	return collectAgentTools(agents)
}

// Memory 返回记忆系统
func (s *SupervisorAgent) Memory() memory.Memory { return s.config.Memory }

// LLM 返回 LLM Provider（使用 manager 的 LLM）
func (s *SupervisorAgent) LLM() llm.Provider { return s.manager.LLM() }

// Invoke 执行 Agent
func (s *SupervisorAgent) Invoke(ctx context.Context, input Input, opts ...core.Option) (Output, error) {
	return s.Run(ctx, input)
}

// Stream 流式执行 Agent
func (s *SupervisorAgent) Stream(ctx context.Context, input Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	output, err := s.Run(ctx, input)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(output), nil
}

// Batch 批量执行 Agent
func (s *SupervisorAgent) Batch(ctx context.Context, inputs []Input, opts ...core.Option) ([]Output, error) {
	results := make([]Output, len(inputs))
	for i, input := range inputs {
		output, err := s.Run(ctx, input)
		if err != nil {
			return nil, err
		}
		results[i] = output
	}
	return results, nil
}

// Collect 收集流式输入并执行
func (s *SupervisorAgent) Collect(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (Output, error) {
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		return Output{}, err
	}
	return s.Run(ctx, collected)
}

// Transform 转换流
func (s *SupervisorAgent) Transform(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (*stream.StreamReader[Output], error) {
	reader, writer := stream.Pipe[Output](10)
	go func() {
		defer writer.Close()
		for {
			in, err := input.Recv()
			if err != nil {
				return
			}
			result, err := s.Run(ctx, in)
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
func (s *SupervisorAgent) BatchStream(ctx context.Context, inputs []Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	results, err := s.Batch(ctx, inputs, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}

// 确保实现了 Agent 接口
var _ Agent = (*SupervisorAgent)(nil)
