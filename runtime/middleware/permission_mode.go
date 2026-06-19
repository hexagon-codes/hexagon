package middleware

import (
	"context"
	"errors"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

// ErrPermissionDenied 是权限拒绝的标准错误。
var ErrPermissionDenied = errors.New("runtime: tool call denied by permission policy")

// PermissionMode 是五级权限模式。
//
// 零值 PermissionModeDefault = 旧行为（委托 Checker，无则放行），保证默认与 0.4.8 等价。
type PermissionMode int

const (
	// PermissionModeDefault 旧行为：委托 Checker（无 Checker 则放行）。向后兼容默认。
	PermissionModeDefault PermissionMode = iota
	// PermissionModeReadOnly 仅放行只读类工具，写/危险一律拒绝。
	PermissionModeReadOnly
	// PermissionModeWorkspaceWrite 放行只读 + 工作区写，危险类（shell/exec/删除等）拒绝。
	PermissionModeWorkspaceWrite
	// PermissionModeDangerFullAccess 放行一切（含危险类）。
	PermissionModeDangerFullAccess
	// PermissionModePrompt 每次工具调用征询 Prompt 回调批准；无回调则 fail-closed 拒绝。
	PermissionModePrompt
	// PermissionModeAllow 放行一切（语义同 full，但不做危险分类）。
	PermissionModeAllow
)

func (m PermissionMode) String() string {
	switch m {
	case PermissionModeReadOnly:
		return "read_only"
	case PermissionModeWorkspaceWrite:
		return "workspace_write"
	case PermissionModeDangerFullAccess:
		return "danger_full_access"
	case PermissionModePrompt:
		return "prompt"
	case PermissionModeAllow:
		return "allow"
	default:
		return "default"
	}
}

// ToolClass 是工具的危险等级分类。
type ToolClass int

const (
	ToolRead      ToolClass = iota // 只读
	ToolWrite                      // 写（工作区内）
	ToolDangerous                  // 危险（shell/exec/删除/网络等）
)

// ToolClassifier 把一次工具调用分类为危险等级；nil 时用内置按工具名启发式分类。
type ToolClassifier func(call llm.ToolCall) ToolClass

// PermissionDecision 是 Hook 的判定结果。
type PermissionDecision int

const (
	DecisionDefer PermissionDecision = iota // 交给 mode 策略决定
	DecisionAllow                           // 覆盖：放行
	DecisionDeny                            // 覆盖：拒绝
)

// PermissionHook 在工具执行前调用，可覆盖判定（Allow/Deny）或 Defer 给 mode 策略。
//
// 注：受 Middleware.BeforeTool 接口（只返 error）所限，Hook 暂不能改写将被执行的 call
// input（input 变更需 runner 侧支持）；本版仅支持判定覆盖。
type PermissionHook func(ctx context.Context, state *hruntime.State, call llm.ToolCall) PermissionDecision

// PromptFunc 在 Prompt 模式下征询批准（返回 true=批准）。
type PromptFunc func(ctx context.Context, call llm.ToolCall) bool

// PolicyPermission 是五级权限模式中间件，实现「请求→Hook 覆盖→mode 策略→分类→判定」
// 五步决策链，并发 PermissionRequested/Approved/Denied 事件。
//
// 默认（Mode=Default）行为与旧 Permission 等价（委托 Checker / 放行），可叠加/替换旧中间件。
type PolicyPermission struct {
	Mode     PermissionMode      // 权限模式
	Classify ToolClassifier      // nil→内置启发式
	Hook     PermissionHook      // nil→无 Hook
	Prompt   PromptFunc          // Prompt 模式征询；nil→fail-closed deny
	Checker  hruntime.Permission // Default 模式委托（向后兼容）
}

func (PolicyPermission) BeforeLLM(context.Context, *hruntime.State) error { return nil }
func (PolicyPermission) AfterLLM(context.Context, *hruntime.State, *llm.CompletionResponse) error {
	return nil
}

func (p PolicyPermission) BeforeTool(ctx context.Context, state *hruntime.State, call llm.ToolCall) error {
	_ = state.Emit(ctx, hruntime.Event{Type: hruntime.EventPermissionRequested, ToolCall: &call})

	// 步骤 2：Hook 覆盖
	if p.Hook != nil {
		switch p.Hook(ctx, state, call) {
		case DecisionAllow:
			return p.allow(ctx, state, call, "hook")
		case DecisionDeny:
			return p.deny(ctx, state, call, "hook")
		}
		// DecisionDefer → 继续 mode 策略
	}

	// 步骤 3-5：mode 策略 + 分类 + 判定
	switch p.Mode {
	case PermissionModeAllow, PermissionModeDangerFullAccess:
		return p.allow(ctx, state, call, p.Mode.String())
	case PermissionModeReadOnly:
		if p.classify(call) == ToolRead {
			return p.allow(ctx, state, call, "read_only")
		}
		return p.deny(ctx, state, call, "read_only")
	case PermissionModeWorkspaceWrite:
		if p.classify(call) != ToolDangerous {
			return p.allow(ctx, state, call, "workspace_write")
		}
		return p.deny(ctx, state, call, "workspace_write")
	case PermissionModePrompt:
		if p.Prompt != nil && p.Prompt(ctx, call) {
			return p.allow(ctx, state, call, "prompt")
		}
		return p.deny(ctx, state, call, "prompt")
	default: // PermissionModeDefault：旧行为
		if p.Checker == nil {
			return p.allow(ctx, state, call, "none")
		}
		if err := p.Checker.CheckTool(ctx, call); err != nil {
			_ = state.Emit(ctx, hruntime.Event{Type: hruntime.EventPermissionDenied, ToolCall: &call, Error: err})
			return err
		}
		return p.allow(ctx, state, call, "checker")
	}
}

func (PolicyPermission) AfterTool(context.Context, *hruntime.State, llm.ToolCall, hruntime.ToolResult) error {
	return nil
}
func (PolicyPermission) Finalize(context.Context, *hruntime.State) error { return nil }

func (p PolicyPermission) classify(call llm.ToolCall) ToolClass {
	if p.Classify != nil {
		return p.Classify(call)
	}
	return classifyByName(call.Name)
}

func (p PolicyPermission) allow(ctx context.Context, state *hruntime.State, call llm.ToolCall, by string) error {
	return state.Emit(ctx, hruntime.Event{
		Type:     hruntime.EventPermissionApproved,
		ToolCall: &call,
		Metadata: map[string]any{"by": by, "mode": p.Mode.String()},
	})
}

func (p PolicyPermission) deny(ctx context.Context, state *hruntime.State, call llm.ToolCall, by string) error {
	_ = state.Emit(ctx, hruntime.Event{
		Type:     hruntime.EventPermissionDenied,
		ToolCall: &call,
		Error:    ErrPermissionDenied,
		Metadata: map[string]any{"by": by, "mode": p.Mode.String()},
	})
	// 包装为带分类的统一错误：Classify 据此归类为 KindPermission，
	// 同时保留 ErrPermissionDenied 在 Unwrap 链上（errors.Is 仍成立）。不可重试。
	return hruntime.NewError(hruntime.KindPermission, ErrPermissionDenied.Error(), ErrPermissionDenied, false)
}

// classifyByName 按工具名启发式分类（默认分类器）。未知归 ToolWrite（保守，不当只读放行）。
func classifyByName(name string) ToolClass {
	n := strings.ToLower(name)
	dangerous := []string{"shell", "exec", "run", "command", "bash", "delete", "remove", "rm", "kill", "sudo", "http", "fetch_url", "network"}
	for _, k := range dangerous {
		if strings.Contains(n, k) {
			return ToolDangerous
		}
	}
	read := []string{"read", "list", "get", "search", "view", "find", "stat", "cat", "grep", "query", "fetch"}
	for _, k := range read {
		if strings.Contains(n, k) {
			return ToolRead
		}
	}
	return ToolWrite
}
