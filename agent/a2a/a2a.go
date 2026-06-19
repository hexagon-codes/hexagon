// Package a2a 实现 Google A2A (Agent-to-Agent) 协议
//
// A2A 是一个开放协议，使 AI Agent 能够安全地相互通信、协调任务、共享上下文。
// 本包提供了完整的 A2A 协议实现，包括：
//   - AgentCard: Agent 元数据描述
//   - Task: 任务生命周期管理
//   - Message: Agent 间消息通信
//   - Artifact: 任务输出产物
//   - 推送通知: 服务端主动推送
//
// 规范参考:
//   - Google A2A Protocol: https://google.github.io/A2A/
//
// 使用示例:
//
//	// 创建 A2A 服务器
//	card := &a2a.AgentCard{
//	    Name:        "assistant",
//	    Description: "通用助手 Agent",
//	    URL:         "http://localhost:8080",
//	    Version:     "1.0.0",
//	}
//	server := a2a.NewServer(card, handler)
//	server.Start(":8080")
//
//	// 创建 A2A 客户端
//	client := a2a.NewClient("http://localhost:8080")
//	card, _ := client.GetAgentCard(ctx)
package a2a

import (
	"errors"
	"fmt"
)

// ============== 协议版本 ==============

const (
	// ProtocolVersion A2A 协议版本
	ProtocolVersion = "1.0"

	// ContentTypeJSON JSON 内容类型
	ContentTypeJSON = "application/json"

	// ContentTypeSSE SSE 内容类型
	ContentTypeSSE = "text/event-stream"
)

// ============== 路由路径 ==============

const (
	// PathAgentCard Agent Card 路径
	PathAgentCard = "/.well-known/agent-card.json"

	// PathTasks 任务列表路径
	PathTasks = "/tasks"

	// PathTaskSend 发送消息路径
	PathTaskSend = "/tasks/send"

	// PathTaskSendStream 流式发送消息路径
	PathTaskSendStream = "/tasks/sendSubscribe"

	// PathTaskGet 获取任务路径（带 taskId 参数）
	PathTaskGet = "/tasks/get"

	// PathTaskCancel 取消任务路径
	PathTaskCancel = "/tasks/cancel"

	// PathTaskResubscribe 重新订阅任务路径
	PathTaskResubscribe = "/tasks/resubscribe"

	// PathPushNotification 推送通知配置路径前缀
	PathPushNotification = "/tasks/pushNotification"

	// PathPushNotificationGet 获取推送配置路径
	PathPushNotificationGet = "/tasks/pushNotification/get"

	// PathPushNotificationSet 设置推送配置路径
	PathPushNotificationSet = "/tasks/pushNotification/set"
)

// ============== JSON-RPC 方法 ==============

const (
	// MethodSendMessage 发送消息方法
	MethodSendMessage = "tasks/send"

	// MethodSendMessageStream 流式发送消息方法
	MethodSendMessageStream = "tasks/sendSubscribe"

	// MethodGetTask 获取任务方法
	MethodGetTask = "tasks/get"

	// MethodListTasks 列出任务方法
	MethodListTasks = "tasks/list"

	// MethodCancelTask 取消任务方法
	MethodCancelTask = "tasks/cancel"

	// MethodResubscribe 重新订阅方法
	MethodResubscribe = "tasks/resubscribe"

	// MethodSetPushNotification 设置推送通知方法
	MethodSetPushNotification = "tasks/pushNotification/set"

	// MethodGetPushNotification 获取推送通知方法
	MethodGetPushNotification = "tasks/pushNotification/get"
)

// ============== SSE 事件类型 ==============

const (
	// EventTypeTaskStatus 任务状态事件
	EventTypeTaskStatus = "task-status"

	// EventTypeArtifact 产物事件
	EventTypeArtifact = "artifact"

	// EventTypeError 错误事件
	EventTypeError = "error"

	// EventTypeDone 完成事件
	EventTypeDone = "done"
)

// ============== 错误码 ==============

// A2A 标准错误码
// 参考 JSON-RPC 2.0 规范和 A2A 协议规范
const (
	// CodeParseError JSON 解析错误 (-32700)
	CodeParseError = -32700

	// CodeInvalidRequest 无效请求 (-32600)
	CodeInvalidRequest = -32600

	// CodeMethodNotFound 方法不存在 (-32601)
	CodeMethodNotFound = -32601

	// CodeInvalidParams 无效参数 (-32602)
	CodeInvalidParams = -32602

	// CodeInternalError 内部错误 (-32603)
	CodeInternalError = -32603

	// CodeTaskNotFound 任务不存在 (-32001)
	CodeTaskNotFound = -32001

	// CodeTaskNotCancelable 任务不可取消 (-32002)
	CodeTaskNotCancelable = -32002

	// CodePushNotificationNotSupported 不支持推送通知 (-32003)
	CodePushNotificationNotSupported = -32003

	// CodeUnsupportedOperation 不支持的操作 (-32004)
	CodeUnsupportedOperation = -32004

	// CodeContentTypeNotSupported 不支持的内容类型 (-32005)
	CodeContentTypeNotSupported = -32005

	// CodeAuthenticationRequired 需要认证 (-32010)
	CodeAuthenticationRequired = -32010

	// CodeAuthenticationFailed 认证失败 (-32011)
	CodeAuthenticationFailed = -32011

	// CodePermissionDenied 权限不足 (-32012)
	CodePermissionDenied = -32012
)

// ============== 预定义错误 ==============

var (
	// ErrTaskNotFound 任务不存在
	ErrTaskNotFound = errors.New("task not found")

	// ErrTaskNotCancelable 任务不可取消
	ErrTaskNotCancelable = errors.New("task is not cancelable")

	// ErrPushNotSupported 不支持推送通知
	ErrPushNotSupported = errors.New("push notification not supported")

	// ErrUnsupportedOperation 不支持的操作
	ErrUnsupportedOperation = errors.New("unsupported operation")

	// ErrInvalidMessage 无效消息
	ErrInvalidMessage = errors.New("invalid message")

	// ErrInvalidTask 无效任务
	ErrInvalidTask = errors.New("invalid task")

	// ErrAuthRequired 需要认证
	ErrAuthRequired = errors.New("authentication required")

	// ErrAuthFailed 认证失败
	ErrAuthFailed = errors.New("authentication failed")

	// ErrPermissionDenied 权限不足
	ErrPermissionDenied = errors.New("permission denied")

	// ErrStreamingNotSupported 不支持流式响应
	ErrStreamingNotSupported = errors.New("streaming not supported")

	// ErrServerStopped 服务器已停止
	ErrServerStopped = errors.New("server stopped")

	// ErrClientClosed 客户端已关闭
	ErrClientClosed = errors.New("client closed")
)

// ============== A2A 错误类型 ==============

// Error A2A 错误
// 实现 JSON-RPC 2.0 错误格式
type Error struct {
	// Code 错误码
	Code int `json:"code"`

	// Message 错误消息
	Message string `json:"message"`

	// Data 附加数据（可选）
	Data any `json:"data,omitempty"`
}

// Error 实现 error 接口
func (e *Error) Error() string {
	if e.Data != nil {
		return fmt.Sprintf("[%d] %s: %v", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

// NewError 创建 A2A 错误
func NewError(code int, message string, data ...any) *Error {
	e := &Error{
		Code:    code,
		Message: message,
	}
	if len(data) > 0 {
		e.Data = data[0]
	}
	return e
}

// NewParseError 创建解析错误
func NewParseError(message string) *Error {
	return NewError(CodeParseError, message)
}

// NewInvalidRequestError 创建无效请求错误
func NewInvalidRequestError(message string) *Error {
	return NewError(CodeInvalidRequest, message)
}

// NewMethodNotFoundError 创建方法不存在错误
func NewMethodNotFoundError(method string) *Error {
	return NewError(CodeMethodNotFound, fmt.Sprintf("method not found: %s", method))
}

// NewInvalidParamsError 创建无效参数错误
func NewInvalidParamsError(message string) *Error {
	return NewError(CodeInvalidParams, message)
}

// NewInternalError 创建内部错误
func NewInternalError(message string) *Error {
	return NewError(CodeInternalError, message)
}

// NewTaskNotFoundError 创建任务不存在错误
func NewTaskNotFoundError(taskID string) *Error {
	return NewError(CodeTaskNotFound, fmt.Sprintf("task not found: %s", taskID))
}

// NewTaskNotCancelableError 创建任务不可取消错误
func NewTaskNotCancelableError(taskID string) *Error {
	return NewError(CodeTaskNotCancelable, fmt.Sprintf("task is not cancelable: %s", taskID))
}

// NewPushNotSupportedError 创建不支持推送通知错误
func NewPushNotSupportedError() *Error {
	return NewError(CodePushNotificationNotSupported, "push notification not supported")
}

// NewUnsupportedOperationError 创建不支持操作错误
func NewUnsupportedOperationError(op string) *Error {
	return NewError(CodeUnsupportedOperation, fmt.Sprintf("unsupported operation: %s", op))
}

// NewAuthRequiredError 创建需要认证错误
func NewAuthRequiredError() *Error {
	return NewError(CodeAuthenticationRequired, "authentication required")
}

// NewAuthFailedError 创建认证失败错误
func NewAuthFailedError(reason string) *Error {
	return NewError(CodeAuthenticationFailed, reason)
}

// NewPermissionDeniedError 创建权限不足错误
func NewPermissionDeniedError(reason string) *Error {
	return NewError(CodePermissionDenied, reason)
}

// ============== 辅助函数 ==============

// IsA2AError 检查是否为 A2A 错误
func IsA2AError(err error) bool {
	var a2aErr *Error
	return errors.As(err, &a2aErr)
}

// GetA2AError 获取 A2A 错误
func GetA2AError(err error) *Error {
	var a2aErr *Error
	if errors.As(err, &a2aErr) {
		return a2aErr
	}
	return nil
}

// ToA2AError 将普通错误转换为 A2A 错误
func ToA2AError(err error) *Error {
	if err == nil {
		return nil
	}

	// 如果已经是 A2A 错误，直接返回
	if a2aErr := GetA2AError(err); a2aErr != nil {
		return a2aErr
	}

	// 检查已知错误类型
	switch {
	case errors.Is(err, ErrTaskNotFound):
		return NewError(CodeTaskNotFound, err.Error())
	case errors.Is(err, ErrTaskNotCancelable):
		return NewError(CodeTaskNotCancelable, err.Error())
	case errors.Is(err, ErrPushNotSupported):
		return NewError(CodePushNotificationNotSupported, err.Error())
	case errors.Is(err, ErrUnsupportedOperation):
		return NewError(CodeUnsupportedOperation, err.Error())
	case errors.Is(err, ErrAuthRequired):
		return NewError(CodeAuthenticationRequired, err.Error())
	case errors.Is(err, ErrAuthFailed):
		return NewError(CodeAuthenticationFailed, err.Error())
	case errors.Is(err, ErrPermissionDenied):
		return NewError(CodePermissionDenied, err.Error())
	default:
		return NewInternalError(err.Error())
	}
}
