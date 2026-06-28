// Package agent 提供 AI Agent 核心接口和实现
//
// 本包实现了多种 Agent 类型，包括：
//   - BaseAgent: 基础 Agent，提供通用能力
//   - ReActAgent: 实现 ReAct (Reasoning + Acting) 推理模式
//   - Team: 多 Agent 协作团队，支持顺序/层级/协作/轮询四种模式
//   - SwarmRunner: 模仿 OpenAI Swarm 的 Agent 交接运行器
//
// 状态管理：
//   - TurnState: 单轮对话状态
//   - SessionState: 会话级状态
//   - AgentState: Agent 持久状态
//   - GlobalState: 跨 Agent 共享状态
//
// 使用示例：
//
//	agent := NewReAct(
//	    WithName("assistant"),
//	    WithLLM(llmProvider),
//	    WithTools(searchTool, calculatorTool),
//	)
//	output, err := agent.Run(ctx, Input{Query: "Hello"})
package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/memory"
	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/ai-core/template"
	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/hexagon/checkpoint"
	"github.com/hexagon-codes/hexagon/core"
	"github.com/hexagon-codes/hexagon/internal/util"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

// Input 是 Agent 的输入
type Input struct {
	// Query 用户查询
	Query string `json:"query"`

	// Context 额外上下文
	Context map[string]any `json:"context,omitempty"`
}

// Output 是 Agent 的输出
type Output struct {
	// Content 最终回复内容
	Content string `json:"content"`

	// ToolCalls 执行的工具调用记录
	ToolCalls []ToolCallRecord `json:"tool_calls,omitempty"`

	// Blocks 本次运行的有序内容块流（text↔tool 交错序），与 runtime.Result.Blocks 对齐。
	// SDK 消费者（如 hexeye）据此保真展示多步 ReAct，而非把 Content 压平。
	Blocks template.Blocks `json:"blocks,omitempty"`

	// Usage Token 使用统计
	Usage llm.Usage `json:"usage,omitempty"`

	// StopReason 运行终止原因（end_turn / max_turns），与运行时 Result.StopReason 对齐，
	// 让调用方据此决定如何呈现（如轮次耗尽时提示「可继续」），无需 errors.Is 反查。
	StopReason agentruntime.StopReason `json:"stop_reason,omitempty"`

	// Metadata 额外元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ToolCallRecord 记录工具调用
type ToolCallRecord struct {
	// Name 工具名称
	Name string `json:"name"`

	// Arguments 工具参数
	Arguments map[string]any `json:"arguments"`

	// Result 工具结果
	Result tool.Result `json:"result"`
}

// Agent 是 AI Agent 的核心接口
// 继承 Runnable 接口，添加 Agent 特有的方法
type Agent interface {
	core.Runnable[Input, Output]

	// ID 返回 Agent 唯一标识
	ID() string

	// Role 返回 Agent 的角色定义
	Role() Role

	// Tools 返回 Agent 可用的工具列表
	Tools() []tool.Tool

	// Memory 返回 Agent 的记忆系统
	Memory() memory.Memory

	// LLM 返回 Agent 使用的 LLM Provider
	LLM() llm.Provider

	// Run 执行 Agent（向后兼容方法）
	// Deprecated: 请使用 Invoke
	Run(ctx context.Context, input Input) (Output, error)
}

// Config 是 Agent 的配置
type Config struct {
	// ID Agent 唯一标识
	ID string

	// Name Agent 名称
	Name string

	// Description Agent 描述
	Description string

	// Role Agent 角色定义
	Role Role

	// SystemPrompt 系统提示词
	SystemPrompt string

	// LLM LLM 提供者
	LLM llm.Provider

	// Tools 可用工具列表
	Tools []tool.Tool

	// Memory 记忆系统
	Memory memory.Memory

	// MaxIterations 最大迭代次数（防止无限循环）
	MaxIterations int

	// Verbose 是否输出详细日志
	Verbose bool

	// Middleware 注入到底层 runtime 的中间件链（按序在每步 BeforeLLM/AfterLLM/
	// BeforeTool/AfterTool/Finalize 触发）。这是 runtime 中间件扩展点在 Agent 层的出口。
	//
	// 典型用途——接入运行时预算（token+墙钟+成本三维 fail-closed 单一强制点）：
	//
	//	ctrl := cost.NewController(cost.WithBudget(10.0))
	//	agent := NewReAct(WithLLM(p), WithMiddleware(middleware.Budget{
	//	    Limits: middleware.BudgetLimits{MaxCostUSD: 10.0, MaxTokens: 100000},
	//	    Cost:   ctrl.BudgetCostFunc(), // 成本估算所有权仍在 cost.Controller
	//	}))
	//
	// 为空时（默认）底层 runner 不挂任何中间件，行为不变。
	//
	// ⚠️ Budget 语义按 agent 类型不同：中间件按"每次 runtime run"作用。
	//   - ReActAgent：单次多轮 run → Budget 是**整次执行的累计上限**。
	//   - PlanExecute / Reflection：planner / step / replan / critique 各是一次独立 run
	//     → Budget 退化为**每次 LLM 调用各自封顶**，非 agent 全程累计。
	// logging / metrics / 自定义等无状态中间件不受此影响（按调用作用语义一致）。
	// 需要"多 run agent 全程累计预算"时，须另行设计跨 run 共享累加器。
	Middleware []agentruntime.Middleware

	// Durable 可选：开启可持久化/可恢复执行（经 WithCheckpointer 设置）。
	//
	// 非 nil 时，Agent 的底层 runtime run 会在每步边界持久化快照，并支持经 Resume
	// 从最近快照续跑。nil 时（默认）不触碰持久化、行为不变。
	//
	// 语义按 agent 类型：仅 ReActAgent（单次多轮 run）有干净的"整次执行可恢复"语义；
	// 多 run agent（PlanExecute/Reflection）每次内部调用是独立 run，Durable 不适用其整体恢复。
	Durable agentruntime.DurableExecution

	// Strategy 可选：选择统一 agent loop 的执行策略（经 WithStrategy 设置）。
	//
	// 统一 runtime 的回合循环由 Strategy 定制（系统前缀 / 是否继续 / 收尾）。nil 时
	// 默认 NoopStrategy（等价 ReAct）。借助 runtime/strategy 包可让同一个 ReActAgent
	// 以 ReAct / PlanExecute / Reflection 三种策略在**同一个统一回合循环**上运行
	// （提示词引导式），无需各自独立的 loop 实现。
	//
	// 注：独立的 PlanExecuteAgent / ReflectionAgent 是功能更丰富的多调用编排实现，
	// 与本"统一 loop + 策略"轻量路径并存，互不影响。
	Strategy agentruntime.Strategy
}

// Option 是 Agent 配置选项
type Option func(*Config)

// WithID 设置 Agent ID
func WithID(id string) Option {
	return func(c *Config) {
		c.ID = id
	}
}

// WithName 设置 Agent 名称
func WithName(name string) Option {
	return func(c *Config) {
		c.Name = name
	}
}

// WithDescription 设置 Agent 描述
func WithDescription(desc string) Option {
	return func(c *Config) {
		c.Description = desc
	}
}

// WithSystemPrompt 设置系统提示词
func WithSystemPrompt(prompt string) Option {
	return func(c *Config) {
		c.SystemPrompt = prompt
	}
}

// WithLLM 设置 LLM 提供者
func WithLLM(provider llm.Provider) Option {
	return func(c *Config) {
		c.LLM = provider
	}
}

// WithTools 设置工具列表
func WithTools(tools ...tool.Tool) Option {
	return func(c *Config) {
		c.Tools = append(c.Tools, tools...)
	}
}

// WithMemory 设置记忆系统
func WithMemory(mem memory.Memory) Option {
	return func(c *Config) {
		c.Memory = mem
	}
}

// WithMaxIterations 设置最大迭代次数
func WithMaxIterations(n int) Option {
	return func(c *Config) {
		c.MaxIterations = n
	}
}

// WithVerbose 设置详细输出模式
func WithVerbose(v bool) Option {
	return func(c *Config) {
		c.Verbose = v
	}
}

// WithRole 设置 Agent 角色
func WithRole(role Role) Option {
	return func(c *Config) {
		c.Role = role
	}
}

// WithMiddleware 追加注入到底层 runtime 的中间件（runtime 中间件扩展点在 Agent 层的出口）。
//
// 最常见用途是接入运行时预算 middleware.Budget（token+墙钟+成本三维单一 fail-closed 强制点）；
// 成本维度通过 cost.Controller.BudgetCostFunc() 注入，成本估算所有权仍在 security/cost，
// Agent 不引入对 security/cost 的硬依赖。
func WithMiddleware(mws ...agentruntime.Middleware) Option {
	return func(c *Config) {
		c.Middleware = append(c.Middleware, mws...)
	}
}

// WithStrategy 选择统一 agent loop 的执行策略。
//
// 传入 runtime/strategy 包提供的策略（strategy.ReAct{} / strategy.PlanExecute{} /
// strategy.Reflection{}），即可让 ReActAgent 以对应策略（提示词引导式）在同一个
// 统一回合循环上运行。nil 时默认 ReAct 行为。
func WithStrategy(s agentruntime.Strategy) Option {
	return func(c *Config) {
		c.Strategy = s
	}
}

// WithCheckpointer 开启 Agent 的可持久化/可恢复执行，以给定 Checkpointer 作为存储后端。
//
// 设置后，ReActAgent.Run 会在每步边界持久化执行快照（命名空间为本次 run 的 run_id，
// 经 Output.Metadata["run_id"] 返回），并可用该 run_id 调 ReActAgent.Resume 续跑或
// 取回已完成结果。cp 为 nil 时本选项 no-op。
//
// 仅 ReActAgent（单次多轮 run）有干净的整次可恢复语义；多 run agent 不适用。
func WithCheckpointer(cp checkpoint.Checkpointer) Option {
	return func(c *Config) {
		if cp != nil {
			c.Durable = agentruntime.NewDurableExecution(cp)
		}
	}
}

// MemorySetter 允许外部替换 Agent 的记忆系统
//
// 用于共享记忆场景：Team 通过此接口将 Agent 原始记忆包装为 SharedMemoryProxy，
// 实现跨 Agent 记忆自动共享。BaseAgent 和 ReActAgent 均实现此接口。
type MemorySetter interface {
	SetMemory(mem memory.Memory)
}

// BaseAgent 提供 Agent 的基础实现
type BaseAgent struct {
	config Config

	// 单轮补全 runtime runner 在 agent 生命周期内复用（避免每次 LLM 调用重新构造）。
	// 由 PlanExecute/Reflection 的内部补全经 completeWithRuntime 使用；惰性构造、并发安全。
	runnerOnce sync.Once
	runner     *agentruntime.DefaultRunner
}

// SetMemory 替换 Agent 的记忆系统
//
// 此方法用于共享记忆代理注入，不应在常规业务代码中调用。
func (a *BaseAgent) SetMemory(mem memory.Memory) {
	a.config.Memory = mem
}

// NewBaseAgent 创建基础 Agent
func NewBaseAgent(opts ...Option) *BaseAgent {
	cfg := Config{
		ID:            generateID(),
		Name:          "Agent",
		Description:   "Base AI Agent",
		MaxIterations: 10,
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.Memory == nil {
		cfg.Memory = memory.NewBuffer(100)
	}

	return &BaseAgent{config: cfg}
}

// ID 返回 Agent ID
func (a *BaseAgent) ID() string {
	return a.config.ID
}

// Name 返回 Agent 名称
func (a *BaseAgent) Name() string {
	return a.config.Name
}

// Role 返回 Agent 角色
func (a *BaseAgent) Role() Role {
	return a.config.Role
}

// Description 返回 Agent 描述
func (a *BaseAgent) Description() string {
	return a.config.Description
}

// Tools 返回工具列表
func (a *BaseAgent) Tools() []tool.Tool {
	return a.config.Tools
}

// Memory 返回记忆系统
func (a *BaseAgent) Memory() memory.Memory {
	return a.config.Memory
}

// LLM 返回 LLM 提供者
func (a *BaseAgent) LLM() llm.Provider {
	return a.config.LLM
}

// Config 返回配置（用于子类访问）
func (a *BaseAgent) Config() Config {
	return a.config
}

// Invoke 执行 Agent
// BaseAgent 提供简单的 LLM 对话实现，子类可以覆盖此方法实现更复杂的逻辑
func (a *BaseAgent) Invoke(ctx context.Context, input Input, opts ...core.Option) (Output, error) {
	if a.config.LLM == nil {
		return Output{}, fmt.Errorf("LLM provider not configured")
	}

	// 构建消息
	messages := make([]llm.Message, 0, 2)
	if a.config.SystemPrompt != "" {
		messages = append(messages, llm.Message{
			Role:    llm.RoleSystem,
			Content: a.config.SystemPrompt,
		})
	}
	messages = append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: input.Query,
	})

	// 调用 LLM
	resp, err := a.config.LLM.Complete(ctx, llm.CompletionRequest{
		Messages: messages,
	})
	if err != nil {
		return Output{}, fmt.Errorf("LLM completion failed: %w", err)
	}

	return Output{
		Content: resp.Content,
		Usage:   resp.Usage,
	}, nil
}

// Run 是 Invoke 的别名（向后兼容）
// Deprecated: 请使用 Invoke
func (a *BaseAgent) Run(ctx context.Context, input Input) (Output, error) {
	return a.Invoke(ctx, input)
}

// Stream 流式执行 Agent
func (a *BaseAgent) Stream(ctx context.Context, input Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	output, err := a.Invoke(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(output), nil
}

// Batch 批量执行 Agent
func (a *BaseAgent) Batch(ctx context.Context, inputs []Input, opts ...core.Option) ([]Output, error) {
	results := make([]Output, len(inputs))
	for i, input := range inputs {
		output, err := a.Invoke(ctx, input, opts...)
		if err != nil {
			return nil, err
		}
		results[i] = output
	}
	return results, nil
}

// Collect 收集流式输入并执行
func (a *BaseAgent) Collect(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (Output, error) {
	var zero Output
	// 收集所有输入
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		return zero, err
	}
	return a.Invoke(ctx, collected, opts...)
}

// Transform 转换流
func (a *BaseAgent) Transform(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (*stream.StreamReader[Output], error) {
	reader, writer := stream.Pipe[Output](10)
	go func() {
		defer writer.Close()
		for {
			in, err := input.Recv()
			if err != nil {
				return
			}
			result, err := a.Invoke(ctx, in, opts...)
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
func (a *BaseAgent) BatchStream(ctx context.Context, inputs []Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	results, err := a.Batch(ctx, inputs, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}

// InputSchema 返回输入 Schema
func (a *BaseAgent) InputSchema() *core.Schema {
	return core.SchemaOf[Input]()
}

// OutputSchema 返回输出 Schema
func (a *BaseAgent) OutputSchema() *core.Schema {
	return core.SchemaOf[Output]()
}

// generateID 生成唯一 ID
func generateID() string {
	return util.AgentID()
}
