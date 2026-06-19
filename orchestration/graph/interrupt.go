package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/idgen"
)

// InterruptType 中断类型
type InterruptType string

const (
	// InterruptTypeApproval 需要人工审批
	InterruptTypeApproval InterruptType = "approval"
	// InterruptTypeInput 需要人工输入
	InterruptTypeInput InterruptType = "input"
	// InterruptTypeReview 需要人工审核
	InterruptTypeReview InterruptType = "review"
	// InterruptTypeCustom 自定义中断
	InterruptTypeCustom InterruptType = "custom"
)

// InterruptStatus 中断状态
type InterruptStatus string

const (
	// InterruptStatusPending 等待处理
	InterruptStatusPending InterruptStatus = "pending"
	// InterruptStatusApproved 已批准
	InterruptStatusApproved InterruptStatus = "approved"
	// InterruptStatusRejected 已拒绝
	InterruptStatusRejected InterruptStatus = "rejected"
	// InterruptStatusCompleted 已完成（带输入）
	InterruptStatusCompleted InterruptStatus = "completed"
	// InterruptStatusTimeout 超时
	InterruptStatusTimeout InterruptStatus = "timeout"
	// InterruptStatusCancelled 已取消
	InterruptStatusCancelled InterruptStatus = "cancelled"
)

// Interrupt 中断定义
type Interrupt struct {
	// ID 唯一标识符
	ID string `json:"id"`

	// ThreadID 线程 ID
	ThreadID string `json:"thread_id"`

	// GraphName 图名称
	GraphName string `json:"graph_name"`

	// NodeName 触发中断的节点
	NodeName string `json:"node_name"`

	// Type 中断类型
	Type InterruptType `json:"type"`

	// Status 中断状态
	Status InterruptStatus `json:"status"`

	// Title 标题（用于显示）
	Title string `json:"title"`

	// Message 消息内容
	Message string `json:"message"`

	// Data 附加数据
	Data map[string]any `json:"data,omitempty"`

	// Options 选项（用于 Approval 类型）
	Options []InterruptOption `json:"options,omitempty"`

	// InputSchema 输入模式（用于 Input 类型）
	InputSchema *InputSchema `json:"input_schema,omitempty"`

	// Response 响应数据
	Response *InterruptResponse `json:"response,omitempty"`

	// Timeout 超时时间
	Timeout time.Duration `json:"timeout,omitempty"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt 更新时间
	UpdatedAt time.Time `json:"updated_at"`

	// ResolvedAt 解决时间
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`

	// ResolvedBy 解决者
	ResolvedBy string `json:"resolved_by,omitempty"`
}

// InterruptOption 中断选项
type InterruptOption struct {
	// Value 选项值
	Value string `json:"value"`
	// Label 显示标签
	Label string `json:"label"`
	// Description 描述
	Description string `json:"description,omitempty"`
	// Style 样式（primary, danger, default）
	Style string `json:"style,omitempty"`
}

// InputSchema 输入模式
type InputSchema struct {
	// Fields 输入字段
	Fields []InputField `json:"fields"`
}

// InputField 输入字段
type InputField struct {
	// Name 字段名
	Name string `json:"name"`
	// Type 字段类型（text, number, boolean, select, textarea）
	Type string `json:"type"`
	// Label 显示标签
	Label string `json:"label"`
	// Description 描述
	Description string `json:"description,omitempty"`
	// Required 是否必填
	Required bool `json:"required,omitempty"`
	// Default 默认值
	Default any `json:"default,omitempty"`
	// Options 选项（用于 select 类型）
	Options []string `json:"options,omitempty"`
	// Validation 验证规则
	Validation *FieldValidation `json:"validation,omitempty"`
}

// FieldValidation 字段验证规则
type FieldValidation struct {
	// Min 最小值（用于 number）
	Min *float64 `json:"min,omitempty"`
	// Max 最大值（用于 number）
	Max *float64 `json:"max,omitempty"`
	// MinLength 最小长度（用于 text）
	MinLength *int `json:"min_length,omitempty"`
	// MaxLength 最大长度（用于 text）
	MaxLength *int `json:"max_length,omitempty"`
	// Pattern 正则表达式
	Pattern string `json:"pattern,omitempty"`
}

// InterruptResponse 中断响应
type InterruptResponse struct {
	// Action 动作（approve, reject, submit, cancel）
	Action string `json:"action"`
	// Data 响应数据
	Data map[string]any `json:"data,omitempty"`
	// Comment 备注
	Comment string `json:"comment,omitempty"`
}

// InterruptHandler 中断处理器接口
type InterruptHandler interface {
	// Create 创建中断
	Create(ctx context.Context, interrupt *Interrupt) error

	// Get 获取中断
	Get(ctx context.Context, id string) (*Interrupt, error)

	// Resolve 解决中断
	Resolve(ctx context.Context, id string, response *InterruptResponse, resolvedBy string) error

	// Cancel 取消中断
	Cancel(ctx context.Context, id string) error

	// List 列出中断
	List(ctx context.Context, threadID string) ([]*Interrupt, error)

	// ListPending 列出待处理的中断
	ListPending(ctx context.Context) ([]*Interrupt, error)

	// Wait 等待中断解决
	Wait(ctx context.Context, id string) (*Interrupt, error)

	// WaitWithTimeout 带超时等待中断解决
	WaitWithTimeout(ctx context.Context, id string, timeout time.Duration) (*Interrupt, error)
}

// MemoryInterruptHandler 内存中断处理器
type MemoryInterruptHandler struct {
	interrupts map[string]*Interrupt
	waiters    map[string][]chan *Interrupt
	mu         sync.RWMutex
}

// NewMemoryInterruptHandler 创建内存中断处理器
func NewMemoryInterruptHandler() *MemoryInterruptHandler {
	return &MemoryInterruptHandler{
		interrupts: make(map[string]*Interrupt),
		waiters:    make(map[string][]chan *Interrupt),
	}
}

// Create 创建中断
func (h *MemoryInterruptHandler) Create(ctx context.Context, interrupt *Interrupt) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if interrupt.ID == "" {
		interrupt.ID = generateInterruptID()
	}
	interrupt.Status = InterruptStatusPending
	interrupt.CreatedAt = time.Now()
	interrupt.UpdatedAt = interrupt.CreatedAt

	h.interrupts[interrupt.ID] = interrupt
	return nil
}

// Get 获取中断
func (h *MemoryInterruptHandler) Get(ctx context.Context, id string) (*Interrupt, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	interrupt, ok := h.interrupts[id]
	if !ok {
		return nil, fmt.Errorf("interrupt %s not found", id)
	}
	return interrupt, nil
}

// Resolve 解决中断
func (h *MemoryInterruptHandler) Resolve(ctx context.Context, id string, response *InterruptResponse, resolvedBy string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	interrupt, ok := h.interrupts[id]
	if !ok {
		return fmt.Errorf("interrupt %s not found", id)
	}

	if interrupt.Status != InterruptStatusPending {
		return fmt.Errorf("interrupt %s is not pending", id)
	}

	now := time.Now()
	interrupt.Response = response
	interrupt.ResolvedAt = &now
	interrupt.ResolvedBy = resolvedBy
	interrupt.UpdatedAt = now

	switch response.Action {
	case "approve":
		interrupt.Status = InterruptStatusApproved
	case "reject":
		interrupt.Status = InterruptStatusRejected
	case "submit":
		interrupt.Status = InterruptStatusCompleted
	case "cancel":
		interrupt.Status = InterruptStatusCancelled
	default:
		interrupt.Status = InterruptStatusCompleted
	}

	// 通知等待者
	if waiters, ok := h.waiters[id]; ok {
		for _, ch := range waiters {
			select {
			case ch <- interrupt:
			default:
			}
			close(ch)
		}
		delete(h.waiters, id)
	}

	return nil
}

// Cancel 取消中断
func (h *MemoryInterruptHandler) Cancel(ctx context.Context, id string) error {
	return h.Resolve(ctx, id, &InterruptResponse{Action: "cancel"}, "system")
}

// List 列出中断
func (h *MemoryInterruptHandler) List(ctx context.Context, threadID string) ([]*Interrupt, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var result []*Interrupt
	for _, interrupt := range h.interrupts {
		if interrupt.ThreadID == threadID {
			result = append(result, interrupt)
		}
	}
	return result, nil
}

// ListPending 列出待处理的中断
func (h *MemoryInterruptHandler) ListPending(ctx context.Context) ([]*Interrupt, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var result []*Interrupt
	for _, interrupt := range h.interrupts {
		if interrupt.Status == InterruptStatusPending {
			result = append(result, interrupt)
		}
	}
	return result, nil
}

// Wait 等待中断解决
func (h *MemoryInterruptHandler) Wait(ctx context.Context, id string) (*Interrupt, error) {
	return h.WaitWithTimeout(ctx, id, 0)
}

// WaitWithTimeout 带超时等待中断解决
func (h *MemoryInterruptHandler) WaitWithTimeout(ctx context.Context, id string, timeout time.Duration) (*Interrupt, error) {
	h.mu.Lock()

	interrupt, ok := h.interrupts[id]
	if !ok {
		h.mu.Unlock()
		return nil, fmt.Errorf("interrupt %s not found", id)
	}

	// 如果已经解决，直接返回
	if interrupt.Status != InterruptStatusPending {
		h.mu.Unlock()
		return interrupt, nil
	}

	// 创建等待通道
	ch := make(chan *Interrupt, 1)
	h.waiters[id] = append(h.waiters[id], ch)
	h.mu.Unlock()

	// 等待
	if timeout > 0 {
		select {
		case result := <-ch:
			return result, nil
		case <-time.After(timeout):
			return nil, fmt.Errorf("timeout waiting for interrupt %s", id)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	select {
	case result := <-ch:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// generateInterruptID 生成中断 ID
func generateInterruptID() string {
	return fmt.Sprintf("int-%s", idgen.NanoID())
}

// InterruptError 中断错误
type InterruptError struct {
	Interrupt *Interrupt
}

func (e *InterruptError) Error() string {
	return fmt.Sprintf("graph interrupted at node %s: %s", e.Interrupt.NodeName, e.Interrupt.Title)
}

// IsInterruptError 判断是否是中断错误
func IsInterruptError(err error) (*Interrupt, bool) {
	if ie, ok := err.(*InterruptError); ok {
		return ie.Interrupt, true
	}
	return nil, false
}

// InterruptConfig 中断配置
type InterruptConfig struct {
	// Handler 中断处理器
	Handler InterruptHandler

	// DefaultTimeout 默认超时时间
	DefaultTimeout time.Duration

	// AutoResume 自动恢复（当中断解决后自动继续执行）
	AutoResume bool
}

// NewInterruptConfig 创建中断配置
func NewInterruptConfig(handler InterruptHandler) *InterruptConfig {
	return &InterruptConfig{
		Handler:        handler,
		DefaultTimeout: 24 * time.Hour,
		AutoResume:     false,
	}
}

// WithDefaultTimeout 设置默认超时时间
func (c *InterruptConfig) WithDefaultTimeout(timeout time.Duration) *InterruptConfig {
	c.DefaultTimeout = timeout
	return c
}

// WithAutoResume 设置自动恢复
func (c *InterruptConfig) WithAutoResume(autoResume bool) *InterruptConfig {
	c.AutoResume = autoResume
	return c
}

// InterruptBuilder 中断构建器
type InterruptBuilder struct {
	interrupt *Interrupt
}

// NewInterrupt 创建中断构建器
func NewInterrupt(nodeID string) *InterruptBuilder {
	return &InterruptBuilder{
		interrupt: &Interrupt{
			NodeName: nodeID,
			Type:     InterruptTypeApproval,
			Status:   InterruptStatusPending,
			Data:     make(map[string]any),
		},
	}
}

// WithType 设置中断类型
func (b *InterruptBuilder) WithType(t InterruptType) *InterruptBuilder {
	b.interrupt.Type = t
	return b
}

// WithTitle 设置标题
func (b *InterruptBuilder) WithTitle(title string) *InterruptBuilder {
	b.interrupt.Title = title
	return b
}

// WithMessage 设置消息
func (b *InterruptBuilder) WithMessage(message string) *InterruptBuilder {
	b.interrupt.Message = message
	return b
}

// WithData 设置数据
func (b *InterruptBuilder) WithData(key string, value any) *InterruptBuilder {
	b.interrupt.Data[key] = value
	return b
}

// WithOptions 设置选项
func (b *InterruptBuilder) WithOptions(options ...InterruptOption) *InterruptBuilder {
	b.interrupt.Options = options
	return b
}

// WithInputSchema 设置输入模式
func (b *InterruptBuilder) WithInputSchema(schema *InputSchema) *InterruptBuilder {
	b.interrupt.InputSchema = schema
	return b
}

// WithTimeout 设置超时时间
func (b *InterruptBuilder) WithTimeout(timeout time.Duration) *InterruptBuilder {
	b.interrupt.Timeout = timeout
	return b
}

// Build 构建中断
func (b *InterruptBuilder) Build() *Interrupt {
	return b.interrupt
}

// ApprovalInterrupt 创建审批类型中断
func ApprovalInterrupt(nodeID, title, message string) *Interrupt {
	return NewInterrupt(nodeID).
		WithType(InterruptTypeApproval).
		WithTitle(title).
		WithMessage(message).
		WithOptions(
			InterruptOption{Value: "approve", Label: "批准", Style: "primary"},
			InterruptOption{Value: "reject", Label: "拒绝", Style: "danger"},
		).
		Build()
}

// InputInterrupt 创建输入类型中断
func InputInterrupt(nodeID, title, message string, fields ...InputField) *Interrupt {
	return NewInterrupt(nodeID).
		WithType(InterruptTypeInput).
		WithTitle(title).
		WithMessage(message).
		WithInputSchema(&InputSchema{Fields: fields}).
		Build()
}

// ReviewInterrupt 创建审核类型中断
func ReviewInterrupt(nodeID, title, message string, data map[string]any) *Interrupt {
	builder := NewInterrupt(nodeID).
		WithType(InterruptTypeReview).
		WithTitle(title).
		WithMessage(message)

	for k, v := range data {
		builder.WithData(k, v)
	}

	return builder.
		WithOptions(
			InterruptOption{Value: "approve", Label: "通过", Style: "primary"},
			InterruptOption{Value: "reject", Label: "驳回", Style: "danger"},
			InterruptOption{Value: "modify", Label: "修改", Style: "default"},
		).
		Build()
}

// HumanInTheLoop Human-in-the-loop 执行器
// 用于在图执行过程中处理人工干预
type HumanInTheLoop[S State] struct {
	graph     *Graph[S]
	handler   InterruptHandler
	saver     CheckpointSaver
	mu        sync.Mutex
	pendingID string // 当前待处理的中断 ID
}

// NewHumanInTheLoop 创建 Human-in-the-loop 执行器
func NewHumanInTheLoop[S State](graph *Graph[S], handler InterruptHandler, saver CheckpointSaver) *HumanInTheLoop[S] {
	return &HumanInTheLoop[S]{
		graph:   graph,
		handler: handler,
		saver:   saver,
	}
}

// RunWithInterrupt 运行图，支持中断和恢复
func (h *HumanInTheLoop[S]) RunWithInterrupt(ctx context.Context, threadID string, initialState S) (S, *Interrupt, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// 尝试从检查点恢复
	var state S
	var startNode string
	var checkpoint *Checkpoint

	if h.saver != nil {
		var err error
		checkpoint, err = h.saver.Load(ctx, threadID)
		if err == nil && checkpoint != nil {
			// 从检查点恢复状态
			if err := json.Unmarshal(checkpoint.State, &state); err != nil {
				return initialState, nil, fmt.Errorf("unmarshal checkpoint state: %w", err)
			}
			startNode = checkpoint.CurrentNode
		} else {
			state = initialState
			startNode = h.graph.EntryPoint
		}
	} else {
		state = initialState
		startNode = h.graph.EntryPoint
	}

	if startNode == "" {
		startNode = START
	}

	// 执行图
	currentNode := startNode
	for {
		select {
		case <-ctx.Done():
			return state, nil, ctx.Err()
		default:
		}

		// 检查是否到达终点
		if currentNode == END {
			break
		}

		// 获取节点
		node, ok := h.graph.Nodes[currentNode]
		if !ok {
			return state, nil, fmt.Errorf("node %s not found", currentNode)
		}

		// 检查节点是否需要中断
		if node.Metadata != nil {
			if interrupt, ok := node.Metadata["interrupt"].(*Interrupt); ok {
				// 创建检查点
				if h.saver != nil {
					stateData, _ := json.Marshal(state)
					cp := &Checkpoint{
						ThreadID:    threadID,
						GraphName:   h.graph.Name,
						CurrentNode: currentNode,
						State:       stateData,
					}
					if checkpoint != nil {
						cp.ParentID = checkpoint.ID
					}
					if err := h.saver.Save(ctx, cp); err != nil {
						return state, nil, fmt.Errorf("save checkpoint: %w", err)
					}
				}

				// 创建中断
				interrupt.ThreadID = threadID
				interrupt.GraphName = h.graph.Name
				if err := h.handler.Create(ctx, interrupt); err != nil {
					return state, nil, fmt.Errorf("create interrupt: %w", err)
				}

				h.pendingID = interrupt.ID
				return state, interrupt, &InterruptError{Interrupt: interrupt}
			}
		}

		// 执行节点
		newState, err := node.Handler(ctx, state)
		if err != nil {
			return state, nil, fmt.Errorf("node %s failed: %w", currentNode, err)
		}
		state = newState

		// 确定下一个节点
		executor := &graphExecutor[S]{graph: h.graph, state: state, config: &runConfig{}}
		nextNode, err := executor.getNextNode(currentNode)
		if err != nil {
			return state, nil, err
		}

		currentNode = nextNode

		// 保存检查点
		if h.saver != nil {
			stateData, _ := json.Marshal(state)
			cp := &Checkpoint{
				ThreadID:    threadID,
				GraphName:   h.graph.Name,
				CurrentNode: currentNode,
				State:       stateData,
			}
			if checkpoint != nil {
				cp.ParentID = checkpoint.ID
			}
			checkpoint = cp
			if err := h.saver.Save(ctx, cp); err != nil {
				return state, nil, fmt.Errorf("save checkpoint: %w", err)
			}
		}
	}

	return state, nil, nil
}

// Resume 恢复执行（在中断解决后）
func (h *HumanInTheLoop[S]) Resume(ctx context.Context, threadID string, response *InterruptResponse) (S, *Interrupt, error) {
	// 解决当前中断
	if h.pendingID != "" {
		if err := h.handler.Resolve(ctx, h.pendingID, response, "user"); err != nil {
			var zero S
			return zero, nil, fmt.Errorf("resolve interrupt: %w", err)
		}
		h.pendingID = ""
	}

	// 从检查点恢复并继续执行
	checkpoint, err := h.saver.Load(ctx, threadID)
	if err != nil {
		var zero S
		return zero, nil, fmt.Errorf("load checkpoint: %w", err)
	}

	var state S
	if err := json.Unmarshal(checkpoint.State, &state); err != nil {
		return state, nil, fmt.Errorf("unmarshal checkpoint state: %w", err)
	}

	return h.RunWithInterrupt(ctx, threadID, state)
}

// WaitAndResume 等待中断解决并自动恢复
func (h *HumanInTheLoop[S]) WaitAndResume(ctx context.Context, threadID string, interruptID string) (S, error) {
	interrupt, err := h.handler.WaitWithTimeout(ctx, interruptID, 0)
	if err != nil {
		var zero S
		return zero, err
	}

	if interrupt.Status == InterruptStatusRejected || interrupt.Status == InterruptStatusCancelled {
		var zero S
		return zero, fmt.Errorf("interrupt %s was rejected or cancelled", interruptID)
	}

	state, _, err := h.Resume(ctx, threadID, interrupt.Response)
	return state, err
}

// 确保实现了接口
var _ InterruptHandler = (*MemoryInterruptHandler)(nil)
