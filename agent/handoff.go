package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/toolkit/lang/mapx"
)

// Handoff 交接结果
// 当 Agent 需要将任务交接给另一个 Agent 时使用
type Handoff struct {
	// TargetAgent 目标 Agent
	TargetAgent Agent

	// Message 交接消息
	Message string

	// Context 交接上下文
	Context map[string]any

	// Reason 交接原因
	Reason string
}

// TransferToInput 转交工具的输入
type TransferToInput struct {
	// Message 传递给目标 Agent 的消息
	Message string `json:"message" desc:"Message to pass to the target agent" required:"true"`

	// Reason 转交原因
	Reason string `json:"reason" desc:"Reason for transferring to this agent"`

	// Context 额外上下文
	Context map[string]any `json:"context" desc:"Additional context to pass"`
}

// TransferTool 创建转交工具
// 通过工具调用触发的 Agent 交接（handoff）机制。
func TransferTo(target Agent) tool.Tool {
	return &transferTool{
		target: target,
	}
}

// transferTool 转交工具实现
type transferTool struct {
	target Agent
}

func (t *transferTool) Name() string {
	return fmt.Sprintf("transfer_to_%s", t.target.Name())
}

func (t *transferTool) Description() string {
	return fmt.Sprintf("Transfer the conversation to %s. %s", t.target.Name(), t.target.Description())
}

func (t *transferTool) Schema() *llm.Schema {
	return llm.SchemaOf[TransferToInput]()
}

func (t *transferTool) Validate(args map[string]any) error {
	if _, ok := args["message"]; !ok {
		return fmt.Errorf("message is required")
	}
	return nil
}

func (t *transferTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	message, _ := args["message"].(string)
	reason, _ := args["reason"].(string)
	context, _ := args["context"].(map[string]any)

	// 创建交接信息
	handoff := Handoff{
		TargetAgent: t.target,
		Message:     message,
		Context:     context,
		Reason:      reason,
	}

	// 将交接信息存储到 context 中，供外层处理
	return tool.Result{
		Success: true,
		Output:  handoff,
	}, nil
}

// 确保实现了 Tool 接口
var _ tool.Tool = (*transferTool)(nil)

// HandoffHandler 交接处理器
// 用于在外层处理 Agent 交接
type HandoffHandler struct {
	// OnHandoff 交接回调
	OnHandoff func(ctx context.Context, handoff Handoff) error
}

// ProcessToolResult 处理工具结果，检测是否有交接
func (h *HandoffHandler) ProcessToolResult(ctx context.Context, result tool.Result) (*Handoff, error) {
	if !result.Success {
		return nil, nil
	}

	handoff, ok := result.Output.(Handoff)
	if !ok {
		return nil, nil
	}

	if h.OnHandoff != nil {
		if err := h.OnHandoff(ctx, handoff); err != nil {
			return nil, err
		}
	}

	return &handoff, nil
}

// SwarmRunner 多 Agent 交接（handoff）运行器
// 自动处理 Agent 之间的交接
type SwarmRunner struct {
	// InitialAgent 初始 Agent
	InitialAgent Agent

	// MaxHandoffs 最大交接次数
	MaxHandoffs int

	// GlobalState 全局状态
	GlobalState GlobalState

	// Verbose 详细输出
	Verbose bool
}

// NewSwarmRunner 创建 Swarm 运行器
func NewSwarmRunner(initialAgent Agent) *SwarmRunner {
	return &SwarmRunner{
		InitialAgent: initialAgent,
		MaxHandoffs:  10,
		GlobalState:  NewGlobalState(),
	}
}

// Run 运行 Swarm
func (s *SwarmRunner) Run(ctx context.Context, input Input) (Output, error) {
	currentAgent := s.InitialAgent
	currentInput := input
	handoffCount := 0

	for handoffCount < s.MaxHandoffs {
		select {
		case <-ctx.Done():
			return Output{}, ctx.Err()
		default:
		}

		// 执行当前 Agent
		output, err := currentAgent.Run(ctx, currentInput)
		if err != nil {
			return Output{}, fmt.Errorf("agent %s failed: %w", currentAgent.Name(), err)
		}

		// 检查是否有交接
		handoff := s.extractHandoff(output)
		if handoff == nil {
			// 没有交接，返回结果
			return output, nil
		}

		// 处理交接
		handoffCount++
		if s.Verbose {
			fmt.Printf("Handoff %d: %s -> %s (reason: %s)\n",
				handoffCount, currentAgent.Name(), handoff.TargetAgent.Name(), handoff.Reason)
		}

		// 切换到目标 Agent
		currentAgent = handoff.TargetAgent
		currentInput = Input{
			Query:   handoff.Message,
			Context: handoff.Context,
		}
	}

	return Output{}, fmt.Errorf("max handoffs (%d) exceeded", s.MaxHandoffs)
}

// extractHandoff 从输出中提取交接信息
func (s *SwarmRunner) extractHandoff(output Output) *Handoff {
	for _, tc := range output.ToolCalls {
		if handoff, ok := tc.Result.Output.(Handoff); ok {
			return &handoff
		}
	}
	return nil
}

// ContextVariables 上下文变量
// 用于在 Agent 之间传递状态
//
// 注意：此类型（普通 map）不是线程安全的。
// 如果需要并发访问，请使用 SafeContextVariables。
type ContextVariables map[string]any

// Get 获取值
func (c ContextVariables) Get(key string) (any, bool) {
	v, ok := c[key]
	return v, ok
}

// Set 设置值
func (c ContextVariables) Set(key string, value any) {
	c[key] = value
}

// Merge 合并变量
func (c ContextVariables) Merge(other ContextVariables) {
	for k, v := range other {
		c[k] = v
	}
}

// Clone 克隆变量
func (c ContextVariables) Clone() ContextVariables {
	clone := make(ContextVariables, len(c))
	for k, v := range c {
		clone[k] = v
	}
	return clone
}

// contextVariablesKey context key
type contextVariablesKey struct{}

// ContextWithVariables 将上下文变量添加到 context
func ContextWithVariables(ctx context.Context, vars ContextVariables) context.Context {
	return context.WithValue(ctx, contextVariablesKey{}, vars)
}

// VariablesFromContext 从 context 中获取上下文变量
func VariablesFromContext(ctx context.Context) ContextVariables {
	if v, ok := ctx.Value(contextVariablesKey{}).(ContextVariables); ok {
		return v
	}
	return nil
}

// UpdateContextVariables 更新 context 中的变量
func UpdateContextVariables(ctx context.Context, updates ContextVariables) context.Context {
	existing := VariablesFromContext(ctx)
	if existing == nil {
		existing = make(ContextVariables)
	}
	for k, v := range updates {
		existing[k] = v
	}
	return ContextWithVariables(ctx, existing)
}

// ============== 线程安全的上下文变量 ==============

// SafeContextVariables 线程安全的上下文变量
// 用于在多个 goroutine 之间安全地传递和修改状态
type SafeContextVariables struct {
	data map[string]any
	mu   sync.RWMutex
}

// NewSafeContextVariables 创建线程安全的上下文变量
func NewSafeContextVariables() *SafeContextVariables {
	return &SafeContextVariables{
		data: make(map[string]any),
	}
}

// Get 获取值（线程安全）
func (s *SafeContextVariables) Get(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// Set 设置值（线程安全）
func (s *SafeContextVariables) Set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

// Delete 删除值（线程安全）
func (s *SafeContextVariables) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
}

// Merge 合并变量（线程安全）
func (s *SafeContextVariables) Merge(other ContextVariables) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range other {
		s.data[k] = v
	}
}

// MergeSafe 合并另一个 SafeContextVariables（线程安全）
func (s *SafeContextVariables) MergeSafe(other *SafeContextVariables) {
	// 获取 other 的快照
	other.mu.RLock()
	snapshot := make(map[string]any, len(other.data))
	for k, v := range other.data {
		snapshot[k] = v
	}
	other.mu.RUnlock()

	// 合并到 s
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range snapshot {
		s.data[k] = v
	}
}

// Clone 克隆变量（线程安全，返回普通 ContextVariables）
func (s *SafeContextVariables) Clone() ContextVariables {
	s.mu.RLock()
	defer s.mu.RUnlock()
	clone := make(ContextVariables, len(s.data))
	for k, v := range s.data {
		clone[k] = v
	}
	return clone
}

// CloneSafe 克隆为新的 SafeContextVariables（线程安全）
func (s *SafeContextVariables) CloneSafe() *SafeContextVariables {
	s.mu.RLock()
	defer s.mu.RUnlock()
	clone := &SafeContextVariables{
		data: make(map[string]any, len(s.data)),
	}
	for k, v := range s.data {
		clone.data[k] = v
	}
	return clone
}

// Len 返回变量数量（线程安全）
func (s *SafeContextVariables) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// Keys 返回所有键（线程安全）
func (s *SafeContextVariables) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return mapx.Keys(s.data)
}

// safeContextVariablesKey context key for SafeContextVariables
type safeContextVariablesKey struct{}

// ContextWithSafeVariables 将线程安全的上下文变量添加到 context
func ContextWithSafeVariables(ctx context.Context, vars *SafeContextVariables) context.Context {
	return context.WithValue(ctx, safeContextVariablesKey{}, vars)
}

// SafeVariablesFromContext 从 context 中获取线程安全的上下文变量
func SafeVariablesFromContext(ctx context.Context) *SafeContextVariables {
	if v, ok := ctx.Value(safeContextVariablesKey{}).(*SafeContextVariables); ok {
		return v
	}
	return nil
}

// ============== AgentAsTool 适配器 ==============

// AgentAsTool 将 Agent 包装为 Tool
//
// 使 Agent 可以被其他 Agent 作为工具调用。与 TransferTo 不同，
// AgentAsTool 直接执行目标 Agent 并返回结果，而不是创建交接。
//
// 使用场景：
//   - SupervisorAgent 将 worker Agent 注册为工具
//   - 让一个 Agent 调用另一个 Agent 的能力
//
// 工具名称：agent_<name>
// 输入参数：message (必填) + context (可选)
func AgentAsTool(ag Agent) tool.Tool {
	return &agentTool{agent: ag}
}

// agentTool 将 Agent 包装为 tool.Tool 的内部实现
type agentTool struct {
	agent Agent
}

func (t *agentTool) Name() string {
	return fmt.Sprintf("agent_%s", t.agent.Name())
}

func (t *agentTool) Description() string {
	desc := t.agent.Description()
	if desc == "" {
		return fmt.Sprintf("Execute agent %q and return its response", t.agent.Name())
	}
	return fmt.Sprintf("Agent %s: %s", t.agent.Name(), desc)
}

func (t *agentTool) Schema() *llm.Schema {
	return llm.SchemaOf[AgentToolInput]()
}

func (t *agentTool) Validate(args map[string]any) error {
	if _, ok := args["message"]; !ok {
		return fmt.Errorf("message is required")
	}
	return nil
}

func (t *agentTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	message, _ := args["message"].(string)
	agentCtx, _ := args["context"].(map[string]any)

	input := Input{
		Query:   message,
		Context: agentCtx,
	}

	output, err := t.agent.Run(ctx, input)
	if err != nil {
		return tool.Result{
			Success: false,
			Output:  fmt.Sprintf("agent %q execution failed: %v", t.agent.Name(), err),
		}, nil
	}

	return tool.Result{
		Success: true,
		Output:  output.Content,
	}, nil
}

// AgentToolInput AgentAsTool 的输入参数
type AgentToolInput struct {
	// Message 传递给 Agent 的消息
	Message string `json:"message" desc:"Message to send to the agent" required:"true"`

	// Context 额外上下文
	Context map[string]any `json:"context,omitempty" desc:"Additional context to pass to the agent"`
}

// 确保实现了 Tool 接口
var _ tool.Tool = (*agentTool)(nil)
