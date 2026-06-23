package runtime

import (
	"context"
	"errors"
	"fmt"
)

// 本文件提供贯穿运行时/Agent/工具各层的统一错误模型：把分散的错误（runtime typed
// error、工具 error-as-string、子链降级错误）归一到一个可分类的 Kind 上，使上层在
// 重试、降级、可观测上能做一致决策，而不必各层各自字符串匹配。

// ErrorKind 是跨层错误分类。
type ErrorKind string

const (
	// KindUnknown 未分类错误
	KindUnknown ErrorKind = "unknown"
	// KindCanceled 上下文取消
	KindCanceled ErrorKind = "canceled"
	// KindTimeout 超时（含 deadline 超时）
	KindTimeout ErrorKind = "timeout"
	// KindBudget 预算超限（token/时间/成本）
	KindBudget ErrorKind = "budget"
	// KindPermission 权限拒绝
	KindPermission ErrorKind = "permission"
	// KindProvider provider 不可用/未选择
	KindProvider ErrorKind = "provider"
	// KindUnsafeReplay 拒绝重放有副作用的在途工具
	KindUnsafeReplay ErrorKind = "unsafe_replay"
	// KindToolFailure 工具执行失败
	KindToolFailure ErrorKind = "tool_failure"
)

// Error 是带分类的统一错误：携带 Kind + 可重试标记 + 包装的底层错误。
type Error struct {
	Kind      ErrorKind
	Message   string
	Retryable bool
	cause     error
}

func (e *Error) Error() string {
	if e.cause != nil && e.Message != "" {
		return fmt.Sprintf("%s: %v", e.Message, e.cause)
	}
	if e.cause != nil {
		return e.cause.Error()
	}
	if e.Message != "" {
		return e.Message
	}
	// 兜底：避免空错误串，至少暴露分类。
	kind := e.Kind
	if kind == "" {
		kind = KindUnknown
	}
	return "runtime error: " + string(kind)
}

// Unwrap 支持 errors.Is/As 透传底层错误。
func (e *Error) Unwrap() error { return e.cause }

// NewError 构造带分类的错误；retryable 标记是否值得上层重试。
func NewError(kind ErrorKind, message string, cause error, retryable bool) *Error {
	return &Error{Kind: kind, Message: message, cause: cause, Retryable: retryable}
}

// Classify 把任意错误归类到 ErrorKind（按已知 sentinel 与 context 错误判定）。
//
// 已是 *Error 时直接返回其 Kind；否则按 errors.Is 匹配运行时各层 sentinel；
// 都不匹配返回 KindUnknown。
func Classify(err error) ErrorKind {
	if err == nil {
		return KindUnknown
	}
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Kind
	}
	switch {
	case errors.Is(err, context.Canceled):
		return KindCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return KindTimeout
	case errors.Is(err, ErrBudgetExceeded):
		return KindBudget
	case errors.Is(err, ErrUnsafeReplay):
		return KindUnsafeReplay
	case errors.Is(err, ErrNoProvider), errors.Is(err, ErrNoFallback):
		return KindProvider
	default:
		return KindUnknown
	}
}

// Retryable 报告错误是否值得重试。
//
// 显式 *Error 以其 Retryable 标记为准；其余按 Kind 给默认：超时/provider 可重试，
// 取消/预算/权限/拒绝重放不可重试，未知保守为不可重试。
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Retryable
	}
	switch Classify(err) {
	case KindTimeout, KindProvider:
		return true
	default:
		return false
	}
}

// ToolError 把一次工具失败归一为带分类的统一错误（Kind=tool_failure）。
//
// 用于打通「工具层 error-as-string（tool.Result.Error）」与统一错误模型：上层拿到
// 工具失败时用本函数包装，即可与其它层错误一起被 Classify/Retryable 一致处理。
func ToolError(toolName string, cause error) *Error {
	return &Error{
		Kind:    KindToolFailure,
		Message: fmt.Sprintf("tool %q failed", toolName),
		cause:   cause,
	}
}

// AsRuntimeError 把任意错误转为可上报的 RuntimeError 载荷，Code 取其分类。
func AsRuntimeError(err error) *RuntimeError {
	if err == nil {
		return nil
	}
	return runtimeError(string(Classify(err)), err)
}
