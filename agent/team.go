package agent

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/hexagon-codes/ai-core/llm"
	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/hexagon/core"
	"github.com/hexagon-codes/hexagon/internal/util"
	"github.com/hexagon-codes/hexagon/interrupt"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
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
	if _, ok := ctx.Value(childRunKey{}).(ChildRunResult); !ok {
		ctx = context.WithValue(ctx, childRunKey{}, ChildRunResult{RunID: childRunID(ctx), TaskID: "team"})
	}
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
	var result Output

	for index, agent := range agents {
		select {
		case <-ctx.Done():
			return result, &TeamExecutionError{Causes: []error{ctx.Err()}}
		default:
		}

		child, err := runChildTask(ctx, agent, currentInput, fmt.Sprintf("sequential/member/%d", index), agent.Run)
		appendTeamChild(&result, child)
		if err != nil {
			return result, &TeamExecutionError{Causes: []error{fmt.Errorf("agent %s failed: %w", agent.Name(), err)}}
		}
		output := child.Child.Output

		// 将当前输出转换为下一个 Agent 的输入
		currentInput = Input{
			Query:   output.Content,
			Context: output.Metadata,
		}
	}

	return result, nil
}

// appendTeamChild 保留每次子任务的完整事实，顶层用量只累计一次该子任务的聚合值。
func appendTeamChild(result *Output, child ChildRunResult) {
	out := child.Child.Output
	result.Children = append(result.Children, child.Child)
	result.ToolCalls = append(result.ToolCalls, out.ToolCalls...)
	result.Blocks = append(result.Blocks, out.Blocks...)
	result.Usage.PromptTokens += out.Usage.PromptTokens
	result.Usage.CompletionTokens += out.Usage.CompletionTokens
	result.Usage.TotalTokens += out.Usage.TotalTokens
	result.Usage.CacheCreationTokens += out.Usage.CacheCreationTokens
	result.Usage.CacheReadTokens += out.Usage.CacheReadTokens
	if out.Content != "" {
		result.Content = out.Content
	}
	result.StopReason = out.StopReason
	result.Metadata = out.Metadata
}

// 已保存的普通失败禁止重派原任务，但不阻断读取组合内其他位置的已存事实。
// 只对明确保存的分类和直接恢复错误采用该语义，旧记录及新的未知错误仍保守处理。
func teamChildControlError(child ChildRunResult, err error) bool {
	if err == nil {
		return false
	}
	if child.ControlError != nil {
		if *child.ControlError {
			return true
		}
		if saved, ok := err.(*agentruntime.TaskExecutionError); ok && saved.TaskID == child.TaskID {
			return false
		}
	}
	return childControlError(err)
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
	var result Output
	manager, err := runChildTask(ctx, t.manager, Input{
		Query: fmt.Sprintf("As team manager, analyze this task and coordinate team: %s\nTeam members: %s",
			input.Query, t.getAgentNamesFromSlice(agents)),
		Context: input.Context,
	}, "hierarchical/manager/plan", t.manager.Run)
	appendTeamChild(&result, manager)
	if err != nil {
		return result, &TeamExecutionError{Causes: []error{fmt.Errorf("manager failed: %w", err)}}
	}
	managerOutput := manager.Child.Output

	// 让所有 Agent 处理任务
	results := make([]string, 0, len(agents))
	var failures []error
	for index, agent := range agents {
		select {
		case <-ctx.Done():
			return result, &TeamExecutionError{Causes: append(failures, ctx.Err())}
		default:
		}

		child, err := runChildTask(ctx, agent, Input{
			Query:   input.Query,
			Context: map[string]any{"manager_guidance": managerOutput.Content},
		}, fmt.Sprintf("hierarchical/member/%d", index), agent.Run)
		appendTeamChild(&result, child)
		if err != nil {
			failures = append(failures, fmt.Errorf("agent %s failed: %w", agent.Name(), err))
			if teamChildControlError(child, err) {
				return result, &TeamExecutionError{Causes: failures}
			}
			continue
		}
		results = append(results, fmt.Sprintf("[%s]: %s", agent.Name(), child.Child.Output.Content))
	}

	// 如果所有 Agent 都失败了，返回错误
	if len(results) == 0 && len(failures) > 0 {
		return result, &TeamExecutionError{Causes: failures}
	}

	// Manager 汇总结果
	summary, err := runChildTask(ctx, t.manager, Input{
		Query: fmt.Sprintf("Summarize team results:\n%s", results),
	}, "hierarchical/manager/summary", t.manager.Run)
	appendTeamChild(&result, summary)
	if err != nil {
		failures = append(failures, fmt.Errorf("manager summary failed: %w", err))
	}
	if len(failures) > 0 {
		return result, &TeamExecutionError{Causes: failures}
	}
	return result, nil
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
		index  int
		agent  Agent
		output Output
		err    error
	}

	results := make(chan result, len(agents))
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for index, agent := range agents {
		go func(index int, a Agent) {
			r := result{index: index, agent: a}
			defer func() {
				if value := recover(); value != nil {
					r.err = &AgentPanicError{AgentID: a.ID(), Value: value, Stack: debug.Stack()}
				}
				results <- r
			}()
			child, err := runChildTask(childCtx, a, input, fmt.Sprintf("member/%d", index), a.Run)
			r.output, r.err = child.Child.Output, err
		}(index, agent)
	}

	// 收集结果
	var outputs []string
	var allToolCalls []ToolCallRecord
	var totalUsage llm.Usage
	ordered := make([]result, len(agents))
	for i, a := range agents {
		ordered[i] = result{index: i, agent: a}
	}
	received := make([]bool, len(agents))
	remaining := len(agents)
collect:
	for remaining > 0 {
		select {
		case r := <-results:
			ordered[r.index], received[r.index] = r, true
			remaining--
		case <-ctx.Done():
			// 已返回的事实先收齐；尚未响应取消的成员不谎称已停止。
			for {
				select {
				case r := <-results:
					ordered[r.index], received[r.index] = r, true
				default:
					break collect
				}
			}
		}
	}
	children := make([]ChildResult, 0, len(agents))
	var failures, waiting []error

	for _, r := range ordered {
		child := ChildResult{AgentID: r.agent.ID(), AgentName: r.agent.Name(), Status: "returned", Output: r.output}
		if !received[r.index] {
			child.Status, child.Error = "cancel_requested", ctx.Err().Error()
			failures = append(failures, fmt.Errorf("agent %q cancellation pending: %w", r.agent.Name(), ctx.Err()))
		}
		if r.err != nil {
			child.Status, child.Error = "failed", r.err.Error()
			if _, ok := interrupt.IsInterruptSignal(r.err); ok {
				child.Status = "waiting"
				waiting = append(waiting, r.err)
			} else {
				failures = append(failures, fmt.Errorf("agent %q: %w", r.agent.Name(), r.err))
			}
		}
		children = append(children, child)
		outputs = append(outputs, fmt.Sprintf("[%s] (%s):\n%s", r.agent.Name(), child.Status, r.output.Content))
		allToolCalls = append(allToolCalls, r.output.ToolCalls...)
		totalUsage.PromptTokens += r.output.Usage.PromptTokens
		totalUsage.CompletionTokens += r.output.Usage.CompletionTokens
		totalUsage.TotalTokens += r.output.Usage.TotalTokens
		totalUsage.CacheCreationTokens += r.output.Usage.CacheCreationTokens
		totalUsage.CacheReadTokens += r.output.Usage.CacheReadTokens
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

	output := Output{
		Content:   contentBuilder.String(),
		ToolCalls: allToolCalls,
		Usage:     totalUsage,
		Children:  children,
		Metadata: map[string]any{
			"mode":        "collaborative",
			"agent_count": len(agents),
		},
	}
	if len(waiting) > 0 {
		failures = append(failures, interrupt.CompositeInterrupt(ctx, "child agents are waiting", nil, waiting...))
	}
	if len(failures) > 0 {
		return output, &TeamExecutionError{Causes: failures}
	}
	return output, nil
}

// TeamExecutionError 保留各成员失败/等待；组合中可能已有成功副作用，不能自动整组重跑。
type TeamExecutionError struct{ Causes []error }

func (e *TeamExecutionError) Error() string {
	if e == nil {
		return "team execution incomplete"
	}
	if cause := errors.Join(e.Causes...); cause != nil {
		return "team execution incomplete: " + cause.Error()
	}
	return "team execution incomplete"
}
func (e *TeamExecutionError) Unwrap() []error    { return e.Causes }
func (*TeamExecutionError) UnsafeToReplay() bool { return true }

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
	var result Output
	var failures []error

	for round := 0; round < t.maxRounds; round++ {
		for index, agent := range agents {
			select {
			case <-ctx.Done():
				return result, &TeamExecutionError{Causes: append(failures, ctx.Err())}
			default:
			}

			child, err := runChildTask(ctx, agent, currentInput, fmt.Sprintf("round/%d/member/%d", round, index), agent.Run)
			appendTeamChild(&result, child)
			if err != nil {
				failures = append(failures, fmt.Errorf("agent %s failed: %w", agent.Name(), err))
				if teamChildControlError(child, err) {
					return result, &TeamExecutionError{Causes: failures}
				}
				continue
			}
			output := child.Child.Output

			// 检查是否完成（简化：检查输出中是否包含完成标记）
			if output.Metadata != nil {
				if done, ok := output.Metadata["done"].(bool); ok && done {
					if len(failures) > 0 {
						return result, &TeamExecutionError{Causes: failures}
					}
					return result, nil
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

	if len(failures) > 0 {
		return result, &TeamExecutionError{Causes: failures}
	}
	return result, nil
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
