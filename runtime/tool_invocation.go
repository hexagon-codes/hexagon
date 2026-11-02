package runtime

import "context"

// ToolInvocation 由执行核注入工具上下文，不从模型参数或共享 Input.Context 读取身份。
// TaskID 在同一 Run/turn/位置稳定，AttemptID 是本次实际派发；它们不授予业务权限。
type ToolInvocation struct {
	Scope       string
	RunID       string
	ParentRunID string
	Turn        int
	TaskID      string
	Activation  string
	AttemptID   string
	ToolCallID  string
	SideEffect  ToolSideEffect
}

type toolInvocationKey struct{}

func ContextWithToolInvocation(ctx context.Context, invocation ToolInvocation) context.Context {
	return context.WithValue(ctx, toolInvocationKey{}, invocation)
}

func ToolInvocationFromContext(ctx context.Context) (ToolInvocation, bool) {
	invocation, ok := ctx.Value(toolInvocationKey{}).(ToolInvocation)
	return invocation, ok && invocation.RunID != "" && invocation.TaskID != "" && invocation.AttemptID != ""
}
