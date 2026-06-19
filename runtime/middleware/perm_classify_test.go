package middleware

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

// TestBugRev0618_2_DenyClassifiesAsPermission 回归锁定 [BUG-REV0618-2]：
// 权限拒绝错误既满足 errors.Is(ErrPermissionDenied) 又被 Classify 归为 KindPermission
// （此前 Classify 因跨包环无法识别权限 sentinel，KindPermission 是死分类）。
func TestBugRev0618_2_DenyClassifiesAsPermission(t *testing.T) {
	p := PolicyPermission{Mode: PermissionModeReadOnly}
	err := p.deny(context.Background(), &hruntime.State{}, llm.ToolCall{}, "test")
	if err == nil {
		t.Fatal("deny 应返回错误")
	}
	if !denied(err) {
		t.Error("应仍满足 errors.Is(ErrPermissionDenied)")
	}
	if hruntime.Classify(err) != hruntime.KindPermission {
		t.Errorf("应归类为 KindPermission, got %s", hruntime.Classify(err))
	}
}
