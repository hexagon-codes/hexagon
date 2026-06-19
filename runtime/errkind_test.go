package runtime

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// TestClassify_Sentinels 验证各层 sentinel 被正确归类。
func TestClassify_Sentinels(t *testing.T) {
	cases := []struct {
		err  error
		want ErrorKind
	}{
		{context.Canceled, KindCanceled},
		{context.DeadlineExceeded, KindTimeout},
		{ErrBudgetExceeded, KindBudget},
		{ErrMaxTurns, KindMaxTurns},
		{ErrUnsafeReplay, KindUnsafeReplay},
		{ErrNoProvider, KindProvider},
		{errors.New("something else"), KindUnknown},
		{nil, KindUnknown},
	}
	for _, c := range cases {
		if got := Classify(c.err); got != c.want {
			t.Errorf("Classify(%v) = %s, want %s", c.err, got, c.want)
		}
	}
}

// TestClassify_WrappedBudget 验证包装后的预算错误仍被归类为 budget（errors.Is 透传）。
func TestClassify_WrappedBudget(t *testing.T) {
	wrapped := fmt.Errorf("%w: tokens=100 max=50", ErrBudgetExceeded)
	if got := Classify(wrapped); got != KindBudget {
		t.Errorf("包装的预算错误应归类为 budget, got %s", got)
	}
}

// TestError_TypedClassifyAndUnwrap 验证 *Error 的 Kind 与 Unwrap。
func TestError_TypedClassifyAndUnwrap(t *testing.T) {
	cause := errors.New("root cause")
	e := NewError(KindProvider, "provider down", cause, true)
	if Classify(e) != KindProvider {
		t.Errorf("typed Error 应按其 Kind 分类")
	}
	if !errors.Is(e, cause) {
		t.Error("Unwrap 应透传 cause")
	}
	if !Retryable(e) {
		t.Error("该 Error 标记为可重试")
	}
}

// TestRetryable_Defaults 验证按 Kind 的默认可重试判定。
func TestRetryable_Defaults(t *testing.T) {
	if !Retryable(context.DeadlineExceeded) {
		t.Error("超时应可重试")
	}
	if !Retryable(ErrNoProvider) {
		t.Error("provider 错误应可重试")
	}
	if Retryable(ErrBudgetExceeded) {
		t.Error("预算超限不应重试")
	}
	if Retryable(context.Canceled) {
		t.Error("取消不应重试")
	}
}

// TestToolError 验证工具失败包装为统一错误并可分类。
func TestToolError(t *testing.T) {
	te := ToolError("search", errors.New("connection reset"))
	if Classify(te) != KindToolFailure {
		t.Errorf("工具错误应归类为 tool_failure")
	}
	if te.Error() == "" {
		t.Error("应有非空错误信息")
	}
}

// TestBugRev0618_4_ErrorEmptyFallback 回归锁定 [BUG-REV0618-4]：
// 零值/无消息 Error 不返回空串，至少暴露分类（此前 Error() 在两者皆空时返回 ""）。
func TestBugRev0618_4_ErrorEmptyFallback(t *testing.T) {
	if got := (&Error{}).Error(); got == "" {
		t.Error("零值 Error 不应返回空串")
	}
	if got := (&Error{Kind: KindBudget}).Error(); got == "" {
		t.Error("无消息但有 Kind 的 Error 应暴露分类")
	}
}

// TestAsRuntimeError 验证转 RuntimeError 时 Code 取分类。
func TestAsRuntimeError(t *testing.T) {
	re := AsRuntimeError(ErrBudgetExceeded)
	if re == nil || re.Code != string(KindBudget) {
		t.Errorf("RuntimeError.Code 应为 budget, got %+v", re)
	}
	if AsRuntimeError(nil) != nil {
		t.Error("nil 错误应返回 nil")
	}
}
