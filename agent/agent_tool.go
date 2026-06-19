package agent

import (
	"context"
	"fmt"

	"github.com/hexagon-codes/ai-core/schema"
	"github.com/hexagon-codes/ai-core/tool"
)

// AgentTool 把一个 Agent 适配为 tool.Tool，使任意 agent loop（ReAct / PlanExecute /
// Reflection 等）都能把另一个 Agent 当作工具来调用，从而构成「递归子链」——
// agent 调 agent、loop 套 loop。
//
// 设计意图（A10 统一 agent loop 的组合落点）：与其把三种 agent 强行合并进同一个
// 回合循环（会丢失 PlanExecute 的规划/重计划、Reflection 的自检等各自特性），不如
// 让它们各自保留循环、通过统一的「agent 即工具」基元相互嵌套。这样：
//   - DeepAgent 的递归分解、Swarm 的 handoff 等专用组合可统一表达为「子 agent 作工具」；
//   - 任意 agent 都能作为子链节点被另一 agent 调度，无需为每种组合各写一套适配。
//
// 入参约定为单字段对象 {"query": string}，即交给子 Agent 的子任务文本。
type AgentTool struct {
	agent       Agent
	name        string
	description string
	queryKey    string
}

// AgentToolOption 配置 AgentTool。
type AgentToolOption func(*AgentTool)

// WithAgentToolName 覆盖工具名（默认取 agent.Name()）。
func WithAgentToolName(name string) AgentToolOption {
	return func(t *AgentTool) {
		if name != "" {
			t.name = name
		}
	}
}

// WithAgentToolDescription 覆盖工具描述（默认取 agent.Description() 或通用说明）。
func WithAgentToolDescription(desc string) AgentToolOption {
	return func(t *AgentTool) {
		if desc != "" {
			t.description = desc
		}
	}
}

// WithAgentToolQueryKey 覆盖入参字段名（默认 "query"）。
func WithAgentToolQueryKey(key string) AgentToolOption {
	return func(t *AgentTool) {
		if key != "" {
			t.queryKey = key
		}
	}
}

// NewAgentTool 把 agent 包装为可被其它 agent 调用的工具。
//
// 默认工具名取 agent.Name()，描述取 agent.Description()（为空则用通用说明），
// 入参字段为 "query"。
func NewAgentTool(a Agent, opts ...AgentToolOption) *AgentTool {
	t := &AgentTool{
		agent:    a,
		name:     a.Name(),
		queryKey: "query",
	}
	if d := a.Description(); d != "" {
		t.description = d
	} else {
		t.description = fmt.Sprintf("将子任务委派给子 Agent %q 处理并返回其结果", t.name)
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Name 返回工具名。
func (t *AgentTool) Name() string { return t.name }

// Description 返回工具描述。
func (t *AgentTool) Description() string { return t.description }

// Schema 返回单字段入参 Schema：{"query": string}。
func (t *AgentTool) Schema() *schema.Schema {
	return &schema.Schema{
		Type: "object",
		Properties: map[string]*schema.Schema{
			t.queryKey: {
				Type:        "string",
				Description: "交给子 Agent 处理的子任务或查询",
			},
		},
		Required: []string{t.queryKey},
	}
}

// Validate 校验入参含非空字符串 query。
func (t *AgentTool) Validate(args map[string]any) error {
	q, ok := args[t.queryKey].(string)
	if !ok || q == "" {
		return fmt.Errorf("agent tool %q: 缺少字符串参数 %q", t.name, t.queryKey)
	}
	return nil
}

// Execute 运行子 Agent 并把其最终回复作为工具结果返回。
//
// 子 Agent 出错时以失败 Result（而非 error）返回，避免单个子链节点失败直接中断
// 上层 agent 的循环——上层可据此决定重试/重计划/换路。
func (t *AgentTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	q, _ := args[t.queryKey].(string)
	out, err := t.agent.Run(ctx, Input{Query: q})
	if err != nil {
		return tool.NewErrorResult(err), nil
	}
	return tool.NewResult(out.Content), nil
}

// 编译期断言：*AgentTool 满足 tool.Tool。
var _ tool.Tool = (*AgentTool)(nil)
