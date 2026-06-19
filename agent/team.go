package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/hexagon-codes/ai-core/llm"
	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/hexagon/core"
	"github.com/hexagon-codes/hexagon/internal/util"
)

// TeamMode 团队工作模式
type TeamMode int

const (
	// TeamModeSequential 顺序执行模式
	// Agent 按顺序依次执行，前一个的输出作为后一个的输入
	TeamModeSequential TeamMode = iota

	// TeamModeHierarchical 层级模式
	// 由 Manager Agent 协调和分配任务给其他 Agent
	TeamModeHierarchical

	// TeamModeCollaborative 协作模式
	// 所有 Agent 并行工作，通过消息传递协作
	TeamModeCollaborative

	// TeamModeRoundRobin 轮询模式
	// Agent 轮流执行，直到达到目标
	TeamModeRoundRobin
)

// String 返回模式名称
func (m TeamMode) String() string {
	switch m {
	case TeamModeSequential:
		return "sequential"
	case TeamModeHierarchical:
		return "hierarchical"
	case TeamModeCollaborative:
		return "collaborative"
	case TeamModeRoundRobin:
		return "round_robin"
	default:
		return "unknown"
	}
}

// Team 团队
// 多个 Agent 组成的协作团队
//
// 线程安全：所有方法都是并发安全的
type Team struct {
	// ID 团队 ID
	id string

	// Name 团队名称
	name string

	// Description 团队描述
	description string

	// agents 团队成员（受 mu 保护）
	agents []Agent

	// Mode 工作模式
	mode TeamMode

	// Manager 管理者 Agent（仅用于 Hierarchical 模式）
	manager Agent

	// MaxRounds 最大轮次（用于 RoundRobin 和 Collaborative 模式）
	maxRounds int

	// GlobalState 全局状态
	globalState GlobalState

	// sharedMemory 团队共享记忆（可选）
	// 设置后，所有 Agent 的 Memory 会被自动包装为 SharedMemoryProxy
	sharedMemory *SharedMemory

	// Verbose 详细输出
	verbose bool

	// mu 保护 agents 切片的并发访问
	mu sync.RWMutex
}

// TeamOption 团队配置选项
type TeamOption func(*Team)

// NewTeam 创建团队
func NewTeam(name string, opts ...TeamOption) *Team {
	t := &Team{
		id:          util.GenerateID("team"),
		name:        name,
		mode:        TeamModeSequential,
		maxRounds:   10,
		globalState: NewGlobalState(),
	}

	for _, opt := range opts {
		opt(t)
	}

	// 注册所有 Agent 到全局状态
	for _, agent := range t.agents {
		t.globalState.RegisterAgent(agent.ID(), agent)
	}

	// 如果设置了共享记忆，为所有 Agent 安装共享记忆代理
	if t.sharedMemory != nil {
		t.installSharedMemory()
	}

	return t
}

// WithDescription 设置描述
func WithTeamDescription(desc string) TeamOption {
	return func(t *Team) {
		t.description = desc
	}
}

// WithAgents 设置团队成员
func WithAgents(agents ...Agent) TeamOption {
	return func(t *Team) {
		t.agents = append(t.agents, agents...)
	}
}

// WithMode 设置工作模式
func WithMode(mode TeamMode) TeamOption {
	return func(t *Team) {
		t.mode = mode
	}
}

// WithManager 设置管理者（Hierarchical 模式）
func WithManager(manager Agent) TeamOption {
	return func(t *Team) {
		t.manager = manager
		t.mode = TeamModeHierarchical
	}
}

// WithMaxRounds 设置最大轮次
func WithMaxRounds(rounds int) TeamOption {
	return func(t *Team) {
		t.maxRounds = rounds
	}
}

// WithGlobalState 设置全局状态
func WithGlobalState(state GlobalState) TeamOption {
	return func(t *Team) {
		t.globalState = state
	}
}

// WithSharedMemory 设置团队共享记忆
//
// 启用后，所有 Agent 的 Memory 会被自动包装为 SharedMemoryProxy，
// Agent 的写入会同步到共享记忆，搜索会合并共享记忆的结果。
// 后续通过 AddAgent 添加的 Agent 也会自动包装。
func WithSharedMemory(sm *SharedMemory) TeamOption {
	return func(t *Team) {
		t.sharedMemory = sm
	}
}

// WithTeamVerbose 设置详细输出
func WithTeamVerbose(verbose bool) TeamOption {
	return func(t *Team) {
		t.verbose = verbose
	}
}

// ID 返回团队 ID
func (t *Team) ID() string {
	return t.id
}

// Name 返回团队名称
func (t *Team) Name() string {
	return t.name
}

// Description 返回团队描述
func (t *Team) Description() string {
	return t.description
}

// Agents 返回团队成员（返回副本以确保安全）
func (t *Team) Agents() []Agent {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]Agent, len(t.agents))
	copy(result, t.agents)
	return result
}

// Mode 返回工作模式
func (t *Team) Mode() TeamMode {
	return t.mode
}

// SharedMemory 返回团队共享记忆（如未设置则返回 nil）
func (t *Team) SharedMemory() *SharedMemory {
	return t.sharedMemory
}

// AddAgent 添加 Agent 到团队
//
// 如果团队启用了共享记忆，新添加的 Agent 会自动包装 SharedMemoryProxy。
//
// 线程安全：此方法是并发安全的
func (t *Team) AddAgent(agent Agent) {
	if t.sharedMemory != nil {
		t.wrapAgentMemory(agent)
	}
	t.mu.Lock()
	t.agents = append(t.agents, agent)
	t.globalState.RegisterAgent(agent.ID(), agent)
	t.mu.Unlock()
}

// RemoveAgent 从团队移除 Agent
//
// 线程安全：此方法是并发安全的
func (t *Team) RemoveAgent(agentID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	newAgents := make([]Agent, 0, len(t.agents))
	for _, a := range t.agents {
		if a.ID() != agentID {
			newAgents = append(newAgents, a)
		}
	}
	t.agents = newAgents
}

// Run 执行团队任务
func (t *Team) Run(ctx context.Context, input Input) (Output, error) {
	switch t.mode {
	case TeamModeSequential:
		return t.runSequential(ctx, input)
	case TeamModeHierarchical:
		return t.runHierarchical(ctx, input)
	case TeamModeCollaborative:
		return t.runCollaborative(ctx, input)
	case TeamModeRoundRobin:
		return t.runRoundRobin(ctx, input)
	default:
		return Output{}, fmt.Errorf("unknown team mode: %d", t.mode)
	}
}

// runSequential 顺序执行
func (t *Team) runSequential(ctx context.Context, input Input) (Output, error) {
	// 获取 agents 的快照
	t.mu.RLock()
	agents := make([]Agent, len(t.agents))
	copy(agents, t.agents)
	t.mu.RUnlock()

	if len(agents) == 0 {
		return Output{}, fmt.Errorf("team has no agents")
	}

	currentInput := input
	var lastOutput Output

	for _, agent := range agents {
		select {
		case <-ctx.Done():
			return Output{}, ctx.Err()
		default:
		}

		output, err := agent.Run(ctx, currentInput)
		if err != nil {
			return Output{}, fmt.Errorf("agent %s failed: %w", agent.Name(), err)
		}

		lastOutput = output

		// 将当前输出转换为下一个 Agent 的输入
		currentInput = Input{
			Query:   output.Content,
			Context: output.Metadata,
		}
	}

	return lastOutput, nil
}

// runHierarchical 层级执行
//
// 由 Manager Agent 协调和分配任务给其他 Agent。
// 线程安全：在执行前获取 agents 的快照
func (t *Team) runHierarchical(ctx context.Context, input Input) (Output, error) {
	if t.manager == nil {
		return Output{}, fmt.Errorf("hierarchical mode requires a manager")
	}

	// 获取 agents 的快照（线程安全）
	t.mu.RLock()
	agents := make([]Agent, len(t.agents))
	copy(agents, t.agents)
	t.mu.RUnlock()

	if len(agents) == 0 {
		return Output{}, fmt.Errorf("team has no agents")
	}

	// Manager 分析任务并决定如何分配
	// 这里简化实现：Manager 决定执行顺序
	managerOutput, err := t.manager.Run(ctx, Input{
		Query: fmt.Sprintf("As team manager, analyze this task and coordinate team: %s\nTeam members: %s",
			input.Query, t.getAgentNamesFromSlice(agents)),
		Context: input.Context,
	})
	if err != nil {
		return Output{}, fmt.Errorf("manager failed: %w", err)
	}

	// 让所有 Agent 处理任务
	results := make([]string, 0, len(agents))
	var agentErrors []string
	for _, agent := range agents {
		select {
		case <-ctx.Done():
			return Output{}, ctx.Err()
		default:
		}

		output, err := agent.Run(ctx, Input{
			Query:   input.Query,
			Context: map[string]any{"manager_guidance": managerOutput.Content},
		})
		if err != nil {
			// 记录失败的 Agent 及错误信息，继续执行其他 Agent
			agentErrors = append(agentErrors, fmt.Sprintf("[%s]: %v", agent.Name(), err))
			continue
		}
		results = append(results, fmt.Sprintf("[%s]: %s", agent.Name(), output.Content))
	}

	// 如果所有 Agent 都失败了，返回错误
	if len(results) == 0 && len(agentErrors) > 0 {
		return Output{}, fmt.Errorf("all agents failed: %s", strings.Join(agentErrors, "; "))
	}

	// Manager 汇总结果
	summaryOutput, err := t.manager.Run(ctx, Input{
		Query: fmt.Sprintf("Summarize team results:\n%s", results),
	})
	if err != nil {
		return Output{Content: fmt.Sprintf("Team results: %v", results)}, nil
	}

	return summaryOutput, nil
}

// runCollaborative 协作执行
//
// 所有 Agent 并行工作，通过消息传递协作。
// 线程安全：在执行前获取 agents 的快照
func (t *Team) runCollaborative(ctx context.Context, input Input) (Output, error) {
	// 获取 agents 的快照（线程安全）
	t.mu.RLock()
	agents := make([]Agent, len(t.agents))
	copy(agents, t.agents)
	t.mu.RUnlock()

	if len(agents) == 0 {
		return Output{}, fmt.Errorf("team has no agents")
	}

	// 并行执行所有 Agent
	type result struct {
		agent  Agent
		output Output
		err    error
	}

	results := make(chan result, len(agents))
	var wg sync.WaitGroup

	for _, agent := range agents {
		wg.Add(1)
		go func(a Agent) {
			defer wg.Done()
			output, err := a.Run(ctx, input)
			results <- result{agent: a, output: output, err: err}
		}(agent)
	}

	// 等待所有完成
	go func() {
		wg.Wait()
		close(results)
	}()

	// 收集结果
	var outputs []string
	var allToolCalls []ToolCallRecord
	var totalUsage llm.Usage

	for r := range results {
		if r.err != nil {
			continue
		}
		outputs = append(outputs, fmt.Sprintf("[%s]:\n%s", r.agent.Name(), r.output.Content))
		allToolCalls = append(allToolCalls, r.output.ToolCalls...)
		totalUsage.PromptTokens += r.output.Usage.PromptTokens
		totalUsage.CompletionTokens += r.output.Usage.CompletionTokens
		totalUsage.TotalTokens += r.output.Usage.TotalTokens
	}

	// 格式化输出
	var contentBuilder strings.Builder
	contentBuilder.WriteString("=== Collaborative Results ===\n\n")
	for i, output := range outputs {
		contentBuilder.WriteString(output)
		if i < len(outputs)-1 {
			contentBuilder.WriteString("\n\n---\n\n")
		}
	}

	return Output{
		Content:   contentBuilder.String(),
		ToolCalls: allToolCalls,
		Usage:     totalUsage,
		Metadata: map[string]any{
			"mode":        "collaborative",
			"agent_count": len(agents),
		},
	}, nil
}

// runRoundRobin 轮询执行
//
// Agent 轮流执行，直到达到目标或达到最大轮次。
// 线程安全：在执行前获取 agents 的快照
func (t *Team) runRoundRobin(ctx context.Context, input Input) (Output, error) {
	// 获取 agents 的快照（线程安全）
	t.mu.RLock()
	agents := make([]Agent, len(t.agents))
	copy(agents, t.agents)
	t.mu.RUnlock()

	if len(agents) == 0 {
		return Output{}, fmt.Errorf("team has no agents")
	}

	currentInput := input
	var lastOutput Output

	for round := 0; round < t.maxRounds; round++ {
		for _, agent := range agents {
			select {
			case <-ctx.Done():
				return Output{}, ctx.Err()
			default:
			}

			output, err := agent.Run(ctx, currentInput)
			if err != nil {
				continue
			}

			lastOutput = output

			// 检查是否完成（简化：检查输出中是否包含完成标记）
			if output.Metadata != nil {
				if done, ok := output.Metadata["done"].(bool); ok && done {
					return output, nil
				}
			}

			// 准备下一轮输入
			currentInput = Input{
				Query: output.Content,
				Context: map[string]any{
					"round":     round,
					"agent":     agent.Name(),
					"iteration": round*len(agents) + 1,
				},
			}
		}
	}

	return lastOutput, nil
}

// getAgentNamesFromSlice 从 Agent 切片获取名称列表
func (t *Team) getAgentNamesFromSlice(agents []Agent) string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name()
	}
	return fmt.Sprintf("%v", names)
}

// Invoke 执行 Team（实现 Runnable 接口）
func (t *Team) Invoke(ctx context.Context, input Input, opts ...core.Option) (Output, error) {
	return t.Run(ctx, input)
}

// Stream 流式执行
func (t *Team) Stream(ctx context.Context, input Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	output, err := t.Run(ctx, input)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(output), nil
}

// Batch 批量执行
func (t *Team) Batch(ctx context.Context, inputs []Input, opts ...core.Option) ([]Output, error) {
	results := make([]Output, len(inputs))
	for i, input := range inputs {
		output, err := t.Run(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("batch item %d failed: %w", i, err)
		}
		results[i] = output
	}
	return results, nil
}

// Collect 收集流式输入并执行
func (t *Team) Collect(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (Output, error) {
	var zero Output
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		return zero, err
	}
	return t.Run(ctx, collected)
}

// Transform 转换流
func (t *Team) Transform(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (*stream.StreamReader[Output], error) {
	reader, writer := stream.Pipe[Output](10)
	go func() {
		defer writer.Close()
		for {
			in, err := input.Recv()
			if err != nil {
				return
			}
			result, err := t.Run(ctx, in)
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
func (t *Team) BatchStream(ctx context.Context, inputs []Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	results, err := t.Batch(ctx, inputs, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}

// InputSchema 返回输入 Schema
func (t *Team) InputSchema() *core.Schema {
	return core.SchemaOf[Input]()
}

// OutputSchema 返回输出 Schema
func (t *Team) OutputSchema() *core.Schema {
	return core.SchemaOf[Output]()
}

// 确保实现了 Runnable 接口
var _ core.Runnable[Input, Output] = (*Team)(nil)
