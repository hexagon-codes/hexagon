// Package interrupt 提供 Hexagon 框架的中断恢复能力
//
// 本包实现 Human-in-the-Loop 模式，支持在任意节点中断执行并等待人工输入。
//
// 核心功能：
//   - Interrupt: 在节点中触发中断
//   - Resume: 恢复执行
//   - Checkpoint: 检查点持久化
//
// 设计借鉴：
//   - LangGraph: interrupt() 函数
//   - LangGraph: Command 恢复机制
//   - LangGraph: Checkpointer 持久化
//
// 使用示例：
//
//	func reviewNode(ctx context.Context, state *State) error {
//	    // 中断等待人工审核
//	    result, err := interrupt.Interrupt(ctx, ReviewRequest{
//	        Content: state.Content,
//	    })
//	    if err != nil {
//	        return err
//	    }
//	    state.Approved = result.Approved
//	    return nil
//	}
//
//	// 恢复执行
//	graph.Resume(ctx, threadID, interrupt.Command{
//	    Resume: ApprovalResult{Approved: true},
//	})
package interrupt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/hexagon/checkpoint"
)

// ============== 错误定义 ==============

var (
	// ErrInterrupted 表示执行被中断
	ErrInterrupted = errors.New("execution interrupted")

	// ErrNoCheckpoint 表示没有找到检查点
	ErrNoCheckpoint = errors.New("no checkpoint found")

	// ErrInvalidResume 表示恢复数据无效
	ErrInvalidResume = errors.New("invalid resume data")

	// ErrTimeout 表示等待超时
	ErrTimeout = errors.New("interrupt timeout")

	// ErrCanceled 表示被取消
	ErrCanceled = errors.New("interrupt canceled")
)

// InterruptError 中断错误，携带中断信息
type InterruptError struct {
	ThreadID  string
	NodeID    string
	Payload   any
	Timestamp time.Time

	// Signal 关联的中断信号（新系统）
	// 当通过新的 InterruptSignalFunc/StatefulInterrupt/CompositeInterrupt 触发中断时，
	// 此字段会被填充，用于桥接新旧两套中断系统
	Signal *InterruptSignal
}

func (e *InterruptError) Error() string {
	return fmt.Sprintf("interrupted at node %s (thread: %s)", e.NodeID, e.ThreadID)
}

func (e *InterruptError) Is(target error) bool {
	return target == ErrInterrupted
}

// ============== Context Keys ==============

type interruptContextKey struct{}
type threadIDKey struct{}
type nodeIDKey struct{}

// ContextWithInterruptHandler 添加中断处理器到 context
func ContextWithInterruptHandler(ctx context.Context, handler *Handler) context.Context {
	return context.WithValue(ctx, interruptContextKey{}, handler)
}

// HandlerFromContext 从 context 获取中断处理器
func HandlerFromContext(ctx context.Context) *Handler {
	if h, ok := ctx.Value(interruptContextKey{}).(*Handler); ok {
		return h
	}
	return nil
}

// ContextWithThreadID 添加线程 ID 到 context
func ContextWithThreadID(ctx context.Context, threadID string) context.Context {
	return context.WithValue(ctx, threadIDKey{}, threadID)
}

// ThreadIDFromContext 从 context 获取线程 ID
func ThreadIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(threadIDKey{}).(string); ok {
		return id
	}
	return ""
}

// ContextWithNodeID 添加节点 ID 到 context
func ContextWithNodeID(ctx context.Context, nodeID string) context.Context {
	return context.WithValue(ctx, nodeIDKey{}, nodeID)
}

// NodeIDFromContext 从 context 获取节点 ID
func NodeIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(nodeIDKey{}).(string); ok {
		return id
	}
	return ""
}

// ============== Interrupt 函数 ==============

// Interrupt 在当前节点触发中断，等待人工输入
//
// 调用此函数会：
// 1. 保存当前状态到检查点
// 2. 返回 InterruptError
// 3. 等待 Resume 恢复
//
// 参数：
//   - ctx: 上下文，必须包含 Handler
//   - payload: 中断时携带的数据，会传递给恢复方
//
// 返回：
//   - T: 恢复时传入的数据
//   - error: 错误
func Interrupt[T any](ctx context.Context, payload any) (T, error) {
	var zero T

	handler := HandlerFromContext(ctx)
	if handler == nil {
		return zero, errors.New("no interrupt handler in context")
	}

	threadID := ThreadIDFromContext(ctx)
	nodeID := NodeIDFromContext(ctx)

	result, err := handler.interrupt(ctx, threadID, nodeID, payload, zero)
	if err != nil {
		return zero, err
	}

	// 类型断言
	if typed, ok := result.(T); ok {
		return typed, nil
	}
	return zero, fmt.Errorf("type assertion failed: expected %T, got %T", zero, result)
}

// InterruptWithOptions 带选项的中断
func InterruptWithOptions[T any](ctx context.Context, payload any, opts ...InterruptOption) (T, error) {
	var zero T

	config := &interruptConfig{
		timeout: 0, // 无超时
	}
	for _, opt := range opts {
		opt(config)
	}

	handler := HandlerFromContext(ctx)
	if handler == nil {
		return zero, errors.New("no interrupt handler in context")
	}

	threadID := ThreadIDFromContext(ctx)
	nodeID := NodeIDFromContext(ctx)

	// 应用超时
	if config.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, config.timeout)
		defer cancel()
	}

	result, err := handler.interrupt(ctx, threadID, nodeID, payload, zero)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && config.hasDefault {
			// 选项是类型擦除的，WithDefault[U] 的 U 可能与本调用的 T 不一致；
			// 用受检断言把类型不匹配转成可处理的错误，而不是裸断言 panic。
			def, ok := config.defaultValue.(T)
			if !ok {
				return zero, fmt.Errorf("%w: default value type %T does not match expected %T",
					ErrInvalidResume, config.defaultValue, zero)
			}
			return def, nil
		}
		return zero, err
	}

	// 类型断言
	typed, ok := result.(T)
	if !ok {
		return zero, fmt.Errorf("type assertion failed: expected %T, got %T", zero, result)
	}

	// 验证
	if config.validator != nil {
		if err := config.validator(typed); err != nil {
			return zero, fmt.Errorf("validation failed: %w", err)
		}
	}

	return typed, nil
}

// ============== InterruptOption ==============

type interruptConfig struct {
	timeout      time.Duration
	hasDefault   bool
	defaultValue any
	validator    func(any) error
}

// InterruptOption 中断选项
type InterruptOption func(*interruptConfig)

// WithTimeout 设置等待超时
func WithTimeout(d time.Duration) InterruptOption {
	return func(c *interruptConfig) {
		c.timeout = d
	}
}

// WithDefault 设置默认值（超时时使用）
func WithDefault[T any](v T) InterruptOption {
	return func(c *interruptConfig) {
		c.hasDefault = true
		c.defaultValue = v
	}
}

// WithValidator 设置输入验证器
func WithValidator[T any](fn func(T) error) InterruptOption {
	return func(c *interruptConfig) {
		c.validator = func(v any) error {
			// 选项是类型擦除的，恢复值的运行时类型可能与 WithValidator[T] 的 T 不一致；
			// 用受检断言把类型不匹配转成错误，而不是裸断言 panic。
			typed, ok := v.(T)
			if !ok {
				return fmt.Errorf("%w: resume value type %T does not match validator type %T",
					ErrInvalidResume, v, *new(T))
			}
			return fn(typed)
		}
	}
}

// ============== Command 恢复命令 ==============

// Command 恢复命令
type Command struct {
	// Resume 恢复值，会作为 Interrupt 的返回值
	Resume any `json:"resume,omitempty"`

	// Goto 跳转到指定节点（可选）
	Goto string `json:"goto,omitempty"`

	// Update 更新状态（可选）
	Update map[string]any `json:"update,omitempty"`
}

// ============== Handler 中断处理器 ==============

// Handler 中断处理器
type Handler struct {
	checkpointer checkpoint.Checkpointer
	pending      sync.Map // threadID -> *pendingInterrupt
}

type pendingInterrupt struct {
	threadID  string
	nodeID    string
	payload   any
	timestamp time.Time
	resumeCh  chan any
	errCh     chan error
}

// NewHandler 创建中断处理器
func NewHandler(checkpointer checkpoint.Checkpointer) *Handler {
	if checkpointer == nil {
		checkpointer = checkpoint.NewMemory()
	}
	return &Handler{
		checkpointer: checkpointer,
	}
}

// interrupt 内部中断实现
func (h *Handler) interrupt(ctx context.Context, threadID, nodeID string, payload any, zero any) (any, error) {
	// 创建 pending
	pending := &pendingInterrupt{
		threadID:  threadID,
		nodeID:    nodeID,
		payload:   payload,
		timestamp: time.Now(),
		resumeCh:  make(chan any, 1),
		errCh:     make(chan error, 1),
	}

	// 保存到 pending map
	h.pending.Store(threadID, pending)
	defer h.pending.Delete(threadID)

	// 保存检查点（领域 Checkpoint 作为 payload，经规范 checkpoint 包持久化）
	cp := Checkpoint{
		ThreadID:  threadID,
		NodeID:    nodeID,
		Payload:   payload,
		Status:    StatusInterrupted,
		Timestamp: pending.timestamp,
	}
	if err := checkpoint.PutValue(ctx, h.checkpointer, checkpoint.Checkpoint{Namespace: threadID, ID: threadID}, cp); err != nil {
		return zero, fmt.Errorf("failed to save checkpoint: %w", err)
	}

	// 等待恢复
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case err := <-pending.errCh:
		return zero, err
	case result := <-pending.resumeCh:
		return result, nil
	}
}

// Resume 恢复执行
func (h *Handler) Resume(ctx context.Context, threadID string, cmd Command) error {
	// 查找 pending
	if p, ok := h.pending.Load(threadID); ok {
		pending := p.(*pendingInterrupt)
		pending.resumeCh <- cmd.Resume
		return nil
	}

	// 从检查点恢复（领域 Checkpoint 作为 payload）
	cp, _, ok, err := checkpoint.GetValue[Checkpoint](ctx, h.checkpointer, threadID, threadID)
	if err != nil {
		return fmt.Errorf("failed to load checkpoint: %w", err)
	}
	if !ok {
		return ErrNoCheckpoint
	}

	// 更新检查点状态
	cp.Status = StatusResumed
	cp.ResumeData = cmd.Resume
	if err := checkpoint.PutValue(ctx, h.checkpointer, checkpoint.Checkpoint{Namespace: threadID, ID: threadID}, cp); err != nil {
		return fmt.Errorf("failed to update checkpoint: %w", err)
	}

	return nil
}

// GetPending 获取待处理的中断
func (h *Handler) GetPending(threadID string) *PendingInfo {
	if p, ok := h.pending.Load(threadID); ok {
		pending := p.(*pendingInterrupt)
		return &PendingInfo{
			ThreadID:  pending.threadID,
			NodeID:    pending.nodeID,
			Payload:   pending.payload,
			Timestamp: pending.timestamp,
		}
	}
	return nil
}

// ListPending 列出所有待处理的中断
func (h *Handler) ListPending() []*PendingInfo {
	var result []*PendingInfo
	h.pending.Range(func(key, value any) bool {
		pending := value.(*pendingInterrupt)
		result = append(result, &PendingInfo{
			ThreadID:  pending.threadID,
			NodeID:    pending.nodeID,
			Payload:   pending.payload,
			Timestamp: pending.timestamp,
		})
		return true
	})
	return result
}

// Cancel 取消中断
func (h *Handler) Cancel(threadID string, reason string) error {
	if p, ok := h.pending.Load(threadID); ok {
		pending := p.(*pendingInterrupt)
		pending.errCh <- fmt.Errorf("%w: %s", ErrCanceled, reason)
		return nil
	}
	return ErrNoCheckpoint
}

// PendingInfo 待处理中断信息
type PendingInfo struct {
	ThreadID  string
	NodeID    string
	Payload   any
	Timestamp time.Time
}

// ============== Checkpoint 检查点 ==============

// Status 检查点状态
type Status string

const (
	StatusRunning     Status = "running"
	StatusInterrupted Status = "interrupted"
	StatusResumed     Status = "resumed"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
)

// Checkpoint 检查点数据
type Checkpoint struct {
	ThreadID   string         `json:"thread_id"`
	NodeID     string         `json:"node_id"`
	Payload    any            `json:"payload,omitempty"`
	Status     Status         `json:"status"`
	State      any            `json:"state,omitempty"`
	ResumeData any            `json:"resume_data,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Timestamp  time.Time      `json:"timestamp"`
	Version    int            `json:"version"`
}

// ============== 便捷函数 ==============

// MustInterrupt 简化版中断，panic on error
//
// ⚠️ 警告：中断失败时会 panic。
// 仅在确定中断一定成功时使用。
// 推荐使用 Interrupt[T]() 方法并正确处理错误。
//
// 使用场景：
//   - 测试代码中
//   - 确定上下文已正确设置的场景
func MustInterrupt[T any](ctx context.Context, payload any) T {
	result, err := Interrupt[T](ctx, payload)
	if err != nil {
		panic(fmt.Sprintf("interrupt failed: %v", err))
	}
	return result
}

// InterruptForApproval 等待审批
func InterruptForApproval(ctx context.Context, content string) (bool, error) {
	return Interrupt[bool](ctx, map[string]any{
		"type":    "approval",
		"content": content,
	})
}

// InterruptForInput 等待用户输入
func InterruptForInput(ctx context.Context, prompt string) (string, error) {
	return Interrupt[string](ctx, map[string]any{
		"type":   "input",
		"prompt": prompt,
	})
}

// InterruptForChoice 等待用户选择
func InterruptForChoice(ctx context.Context, question string, options []string) (string, error) {
	return Interrupt[string](ctx, map[string]any{
		"type":     "choice",
		"question": question,
		"options":  options,
	})
}
