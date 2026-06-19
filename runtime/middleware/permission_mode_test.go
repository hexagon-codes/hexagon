package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

func denied(err error) bool { return errors.Is(err, ErrPermissionDenied) }

func callNamed(name string) llm.ToolCall { return llm.ToolCall{ID: "c1", Name: name} }

func TestPolicyPermission_Modes(t *testing.T) {
	ctx := context.Background()
	st := func() *hruntime.State { return &hruntime.State{Attributes: map[string]any{}} }

	tests := []struct {
		name   string
		mode   PermissionMode
		tool   string
		denied bool
	}{
		{"readonly allows read", PermissionModeReadOnly, "read_file", false},
		{"readonly denies write", PermissionModeReadOnly, "write_file", true},
		{"readonly denies shell", PermissionModeReadOnly, "shell", true},
		{"workspace allows write", PermissionModeWorkspaceWrite, "write_file", false},
		{"workspace allows read", PermissionModeWorkspaceWrite, "read_file", false},
		{"workspace denies shell", PermissionModeWorkspaceWrite, "shell_exec", true},
		{"full allows shell", PermissionModeDangerFullAccess, "shell", false},
		{"allow allows shell", PermissionModeAllow, "delete_all", false},
		{"default no checker allows", PermissionModeDefault, "shell", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PolicyPermission{Mode: tt.mode}
			err := p.BeforeTool(ctx, st(), callNamed(tt.tool))
			if denied(err) != tt.denied {
				t.Fatalf("mode=%s tool=%s err=%v, wantDenied=%v", tt.mode, tt.tool, err, tt.denied)
			}
		})
	}
}

func TestPolicyPermission_PromptMode(t *testing.T) {
	ctx := context.Background()
	st := &hruntime.State{Attributes: map[string]any{}}

	// 批准
	approve := PolicyPermission{Mode: PermissionModePrompt, Prompt: func(context.Context, llm.ToolCall) bool { return true }}
	if err := approve.BeforeTool(ctx, st, callNamed("shell")); err != nil {
		t.Fatalf("prompt 批准应放行: %v", err)
	}
	// 拒绝
	reject := PolicyPermission{Mode: PermissionModePrompt, Prompt: func(context.Context, llm.ToolCall) bool { return false }}
	if err := reject.BeforeTool(ctx, st, callNamed("shell")); !denied(err) {
		t.Fatalf("prompt 拒绝应 deny: %v", err)
	}
	// 无回调 → fail-closed deny
	none := PolicyPermission{Mode: PermissionModePrompt}
	if err := none.BeforeTool(ctx, st, callNamed("shell")); !denied(err) {
		t.Fatalf("prompt 无回调应 fail-closed deny: %v", err)
	}
}

func TestPolicyPermission_HookOverride(t *testing.T) {
	ctx := context.Background()
	st := &hruntime.State{Attributes: map[string]any{}}

	// Hook Deny 覆盖一个本应放行的（ReadOnly + read 工具）
	denyHook := PolicyPermission{
		Mode: PermissionModeReadOnly,
		Hook: func(context.Context, *hruntime.State, llm.ToolCall) PermissionDecision { return DecisionDeny },
	}
	if err := denyHook.BeforeTool(ctx, st, callNamed("read_file")); !denied(err) {
		t.Fatalf("Hook Deny 应覆盖放行: %v", err)
	}

	// Hook Allow 覆盖一个本应拒绝的（ReadOnly + write 工具）
	allowHook := PolicyPermission{
		Mode: PermissionModeReadOnly,
		Hook: func(context.Context, *hruntime.State, llm.ToolCall) PermissionDecision { return DecisionAllow },
	}
	if err := allowHook.BeforeTool(ctx, st, callNamed("write_file")); err != nil {
		t.Fatalf("Hook Allow 应覆盖拒绝: %v", err)
	}

	// Hook Defer → 落回 mode 策略（ReadOnly 拒绝 write）
	deferHook := PolicyPermission{
		Mode: PermissionModeReadOnly,
		Hook: func(context.Context, *hruntime.State, llm.ToolCall) PermissionDecision { return DecisionDefer },
	}
	if err := deferHook.BeforeTool(ctx, st, callNamed("write_file")); !denied(err) {
		t.Fatalf("Hook Defer 应落回 mode 策略(deny): %v", err)
	}
}
