// Package graph 提供图编排引擎
//
// 本文件实现增强的 Human-in-the-Loop (HITL) 功能：
//   - 人工审批：关键操作前等待人工确认
//   - 人工输入：请求人工提供额外输入
//   - 人工校验：人工校验 LLM 输出
//   - 人工接管：在特定条件下切换为人工操作
//
// 设计借鉴：
//   - LangGraph: Human-in-the-loop
//   - Mastra: Human 节点
//   - CrewAI: 人工协作模式
package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/idgen"
)

// ============== HITL 核心类型 ==============

// HITLType 人工介入类型
type HITLType string

const (
	// HITLApproval 审批模式：需要人工批准才能继续
	HITLApproval HITLType = "approval"

	// HITLInput 输入模式：需要人工提供额外输入
	HITLInput HITLType = "input"

	// HITLReview 审查模式：人工审查 LLM 输出
	HITLReview HITLType = "review"

	// HITLTakeover 接管模式：人工完全接管执行
	HITLTakeover HITLType = "takeover"

	// HITLCorrection 纠正模式：人工纠正错误
	HITLCorrection HITLType = "correction"
)

// HITLRequest 人工介入请求
type HITLRequest struct {
	// ID 请求唯一标识
	ID string `json:"id"`

	// Type 介入类型
	Type HITLType `json:"type"`

	// NodeID 触发节点
	NodeID string `json:"node_id"`

	// Title 请求标题
	Title string `json:"title"`

	// Description 请求描述
	Description string `json:"description"`

	// Context 上下文信息
	Context map[string]any `json:"context,omitempty"`

	// Options 可选项（用于审批）
	Options []HITLOption `json:"options,omitempty"`

	// InputSchema 输入 Schema（用于输入模式）
	InputSchema map[string]any `json:"input_schema,omitempty"`

	// CurrentOutput 当前输出（用于审查模式）
	CurrentOutput any `json:"current_output,omitempty"`

	// Timeout 超时时间
	Timeout time.Duration `json:"timeout,omitempty"`

	// Priority 优先级
	Priority HITLPriority `json:"priority"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`

	// ExpiresAt 过期时间
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// HITLOption 选项
type HITLOption struct {
	// ID 选项 ID
	ID string `json:"id"`

	// Label 显示标签
	Label string `json:"label"`

	// Description 选项描述
	Description string `json:"description,omitempty"`

	// Recommended 是否推荐
	Recommended bool `json:"recommended,omitempty"`

	// Dangerous 是否危险操作
	Dangerous bool `json:"dangerous,omitempty"`
}

// HITLPriority 优先级
type HITLPriority string

const (
	// PriorityLow 低优先级
	PriorityLow HITLPriority = "low"

	// PriorityNormal 普通优先级
	PriorityNormal HITLPriority = "normal"

	// PriorityHigh 高优先级
	PriorityHigh HITLPriority = "high"

	// PriorityUrgent 紧急优先级
	PriorityUrgent HITLPriority = "urgent"
)

// HITLResponse 人工响应
type HITLResponse struct {
	// RequestID 对应请求 ID
	RequestID string `json:"request_id"`

	// Approved 是否批准（用于审批模式）
	Approved bool `json:"approved,omitempty"`

	// SelectedOption 选择的选项 ID
	SelectedOption string `json:"selected_option,omitempty"`

	// Input 人工输入数据
	Input map[string]any `json:"input,omitempty"`

	// CorrectedOutput 纠正后的输出
	CorrectedOutput any `json:"corrected_output,omitempty"`

	// Feedback 反馈信息
	Feedback string `json:"feedback,omitempty"`

	// RespondedBy 响应者标识
	RespondedBy string `json:"responded_by,omitempty"`

	// RespondedAt 响应时间
	RespondedAt time.Time `json:"responded_at"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ============== HITL 处理器 ==============

// HITLHandler 人工介入处理器接口
type HITLHandler interface {
	// Handle 处理人工介入请求
	// 阻塞直到收到响应或超时
	Handle(ctx context.Context, request *HITLRequest) (*HITLResponse, error)
}

// HITLCallback 回调式处理器
type HITLCallback func(ctx context.Context, request *HITLRequest) (*HITLResponse, error)

// Handle 实现 HITLHandler 接口
func (c HITLCallback) Handle(ctx context.Context, request *HITLRequest) (*HITLResponse, error) {
	return c(ctx, request)
}

// ChannelHITLHandler 基于 channel 的处理器
type ChannelHITLHandler struct {
	requests  chan *HITLRequest
	responses chan *HITLResponse

	pending map[string]chan *HITLResponse
	mu      sync.Mutex
}

// NewChannelHITLHandler 创建 channel 处理器
func NewChannelHITLHandler(bufferSize int) *ChannelHITLHandler {
	return &ChannelHITLHandler{
		requests:  make(chan *HITLRequest, bufferSize),
		responses: make(chan *HITLResponse, bufferSize),
		pending:   make(map[string]chan *HITLResponse),
	}
}

// Handle 实现 HITLHandler 接口
func (h *ChannelHITLHandler) Handle(ctx context.Context, request *HITLRequest) (*HITLResponse, error) {
	// 创建响应 channel
	respCh := make(chan *HITLResponse, 1)

	h.mu.Lock()
	h.pending[request.ID] = respCh
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.pending, request.ID)
		h.mu.Unlock()
	}()

	// 发送请求
	select {
	case h.requests <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// 等待响应
	var timeoutCh <-chan time.Time
	if request.Timeout > 0 {
		timeoutCh = time.After(request.Timeout)
	}

	select {
	case resp := <-respCh:
		return resp, nil
	case <-timeoutCh:
		return nil, fmt.Errorf("HITL request timeout: %s", request.ID)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// GetRequests 获取请求 channel（供 UI 消费）
func (h *ChannelHITLHandler) GetRequests() <-chan *HITLRequest {
	return h.requests
}

// SubmitResponse 提交响应
func (h *ChannelHITLHandler) SubmitResponse(response *HITLResponse) error {
	h.mu.Lock()
	respCh, exists := h.pending[response.RequestID]
	h.mu.Unlock()

	if !exists {
		return fmt.Errorf("no pending request: %s", response.RequestID)
	}

	response.RespondedAt = time.Now()
	respCh <- response
	return nil
}

// ============== HITL 节点 ==============

// HITLNode 人工介入节点
type HITLNode[S State] struct {
	// ID 节点 ID
	ID string

	// Name 节点名称
	Name string

	// Type 介入类型
	Type HITLType

	// Handler 处理器
	Handler HITLHandler

	// RequestBuilder 请求构建器
	RequestBuilder func(state S) *HITLRequest

	// ResponseHandler 响应处理器
	ResponseHandler func(state S, response *HITLResponse) S

	// Condition 触发条件（可选）
	Condition func(state S) bool
}

// Execute 执行节点
func (n *HITLNode[S]) Execute(ctx context.Context, state S) (S, error) {
	// 检查触发条件
	if n.Condition != nil && !n.Condition(state) {
		return state, nil // 条件不满足，跳过
	}

	// 构建请求
	request := n.RequestBuilder(state)
	if request.ID == "" {
		request.ID = fmt.Sprintf("hitl_%s_%s", n.ID, idgen.NanoID())
	}
	request.NodeID = n.ID
	request.Type = n.Type
	request.CreatedAt = time.Now()

	// 设置过期时间
	if request.Timeout > 0 {
		expires := request.CreatedAt.Add(request.Timeout)
		request.ExpiresAt = &expires
	}

	// 等待人工响应
	response, err := n.Handler.Handle(ctx, request)
	if err != nil {
		return state, fmt.Errorf("HITL failed: %w", err)
	}

	// 处理响应
	if n.ResponseHandler != nil {
		state = n.ResponseHandler(state, response)
	}

	return state, nil
}

// ============== HITL 便捷函数 ==============

// NewApprovalNode 创建审批节点
func NewApprovalNode[S State](id string, handler HITLHandler, opts ...HITLNodeOption[S]) *HITLNode[S] {
	node := &HITLNode[S]{
		ID:      id,
		Name:    "Approval: " + id,
		Type:    HITLApproval,
		Handler: handler,
		RequestBuilder: func(state S) *HITLRequest {
			return &HITLRequest{
				Type:     HITLApproval,
				Title:    "需要审批",
				Priority: PriorityNormal,
				Options: []HITLOption{
					{ID: "approve", Label: "批准", Recommended: true},
					{ID: "reject", Label: "拒绝", Dangerous: true},
				},
			}
		},
		ResponseHandler: func(state S, response *HITLResponse) S {
			// 默认实现：将审批结果存入状态
			if stateMap, ok := any(state).(map[string]any); ok {
				stateMap["_hitl_approved"] = response.Approved
				stateMap["_hitl_feedback"] = response.Feedback
			}
			return state
		},
	}

	for _, opt := range opts {
		opt(node)
	}

	return node
}

// NewInputNode 创建输入节点
func NewInputNode[S State](id string, handler HITLHandler, schema map[string]any, opts ...HITLNodeOption[S]) *HITLNode[S] {
	node := &HITLNode[S]{
		ID:      id,
		Name:    "Input: " + id,
		Type:    HITLInput,
		Handler: handler,
		RequestBuilder: func(state S) *HITLRequest {
			return &HITLRequest{
				Type:        HITLInput,
				Title:       "需要输入",
				InputSchema: schema,
				Priority:    PriorityNormal,
			}
		},
		ResponseHandler: func(state S, response *HITLResponse) S {
			// 默认实现：将输入合并到状态
			if stateMap, ok := any(state).(map[string]any); ok {
				for k, v := range response.Input {
					stateMap[k] = v
				}
			}
			return state
		},
	}

	for _, opt := range opts {
		opt(node)
	}

	return node
}

// NewReviewNode 创建审查节点
func NewReviewNode[S State](id string, handler HITLHandler, opts ...HITLNodeOption[S]) *HITLNode[S] {
	node := &HITLNode[S]{
		ID:      id,
		Name:    "Review: " + id,
		Type:    HITLReview,
		Handler: handler,
		RequestBuilder: func(state S) *HITLRequest {
			var output any
			if stateMap, ok := any(state).(map[string]any); ok {
				output = stateMap["output"]
			}
			return &HITLRequest{
				Type:          HITLReview,
				Title:         "需要审查",
				CurrentOutput: output,
				Priority:      PriorityNormal,
				Options: []HITLOption{
					{ID: "accept", Label: "接受", Recommended: true},
					{ID: "modify", Label: "修改"},
					{ID: "reject", Label: "拒绝", Dangerous: true},
				},
			}
		},
		ResponseHandler: func(state S, response *HITLResponse) S {
			if stateMap, ok := any(state).(map[string]any); ok {
				if response.CorrectedOutput != nil {
					stateMap["output"] = response.CorrectedOutput
				}
				stateMap["_hitl_reviewed"] = true
				stateMap["_hitl_feedback"] = response.Feedback
			}
			return state
		},
	}

	for _, opt := range opts {
		opt(node)
	}

	return node
}

// HITLNodeOption HITL 节点选项
type HITLNodeOption[S State] func(*HITLNode[S])

// WithHITLTitle 设置标题
func WithHITLTitle[S State](title string) HITLNodeOption[S] {
	return func(n *HITLNode[S]) {
		orig := n.RequestBuilder
		n.RequestBuilder = func(state S) *HITLRequest {
			req := orig(state)
			req.Title = title
			return req
		}
	}
}

// WithHITLDescription 设置描述
func WithHITLDescription[S State](desc string) HITLNodeOption[S] {
	return func(n *HITLNode[S]) {
		orig := n.RequestBuilder
		n.RequestBuilder = func(state S) *HITLRequest {
			req := orig(state)
			req.Description = desc
			return req
		}
	}
}

// WithHITLTimeout 设置超时
func WithHITLTimeout[S State](timeout time.Duration) HITLNodeOption[S] {
	return func(n *HITLNode[S]) {
		orig := n.RequestBuilder
		n.RequestBuilder = func(state S) *HITLRequest {
			req := orig(state)
			req.Timeout = timeout
			return req
		}
	}
}

// WithHITLPriority 设置优先级
func WithHITLPriority[S State](priority HITLPriority) HITLNodeOption[S] {
	return func(n *HITLNode[S]) {
		orig := n.RequestBuilder
		n.RequestBuilder = func(state S) *HITLRequest {
			req := orig(state)
			req.Priority = priority
			return req
		}
	}
}

// WithHITLCondition 设置触发条件
func WithHITLCondition[S State](condition func(state S) bool) HITLNodeOption[S] {
	return func(n *HITLNode[S]) {
		n.Condition = condition
	}
}

// ============== HITL 管理器 ==============

// HITLManager 人工介入管理器
type HITLManager struct {
	// 活跃请求
	activeRequests map[string]*HITLRequest

	// 历史记录
	history []*HITLRecord

	// 处理器
	handler HITLHandler

	// 配置
	config HITLManagerConfig

	// 统计
	stats HITLStats

	mu sync.RWMutex
}

// HITLRecord HITL 记录
type HITLRecord struct {
	Request   *HITLRequest  `json:"request"`
	Response  *HITLResponse `json:"response,omitempty"`
	Status    string        `json:"status"` // pending, completed, timeout, cancelled
	StartedAt time.Time     `json:"started_at"`
	EndedAt   *time.Time    `json:"ended_at,omitempty"`
}

// HITLStats HITL 统计
type HITLStats struct {
	TotalRequests   int64                  `json:"total_requests"`
	CompletedCount  int64                  `json:"completed_count"`
	TimeoutCount    int64                  `json:"timeout_count"`
	CancelledCount  int64                  `json:"cancelled_count"`
	AverageWaitTime time.Duration          `json:"average_wait_time"`
	ApprovalRate    float64                `json:"approval_rate"`
	ByType          map[HITLType]int64     `json:"by_type"`
	ByPriority      map[HITLPriority]int64 `json:"by_priority"`
}

// HITLManagerConfig 管理器配置
type HITLManagerConfig struct {
	// MaxPendingRequests 最大待处理请求数
	MaxPendingRequests int

	// DefaultTimeout 默认超时
	DefaultTimeout time.Duration

	// HistoryLimit 历史记录上限
	HistoryLimit int
}

// NewHITLManager 创建管理器
func NewHITLManager(handler HITLHandler, config ...HITLManagerConfig) *HITLManager {
	cfg := HITLManagerConfig{
		MaxPendingRequests: 100,
		DefaultTimeout:     30 * time.Minute,
		HistoryLimit:       1000,
	}
	if len(config) > 0 {
		cfg = config[0]
	}

	return &HITLManager{
		activeRequests: make(map[string]*HITLRequest),
		history:        make([]*HITLRecord, 0),
		handler:        handler,
		config:         cfg,
		stats: HITLStats{
			ByType:     make(map[HITLType]int64),
			ByPriority: make(map[HITLPriority]int64),
		},
	}
}

// Submit 提交请求
func (m *HITLManager) Submit(ctx context.Context, request *HITLRequest) (*HITLResponse, error) {
	m.mu.Lock()

	// 检查待处理数量
	if len(m.activeRequests) >= m.config.MaxPendingRequests {
		m.mu.Unlock()
		return nil, fmt.Errorf("too many pending requests")
	}

	// 设置默认值
	if request.Timeout == 0 {
		request.Timeout = m.config.DefaultTimeout
	}

	// 记录
	record := &HITLRecord{
		Request:   request,
		Status:    "pending",
		StartedAt: time.Now(),
	}
	m.activeRequests[request.ID] = request
	m.history = append(m.history, record)
	m.stats.TotalRequests++
	m.stats.ByType[request.Type]++
	m.stats.ByPriority[request.Priority]++

	// 限制历史记录
	if len(m.history) > m.config.HistoryLimit {
		m.history = m.history[1:]
	}

	m.mu.Unlock()

	// 处理请求
	response, err := m.handler.Handle(ctx, request)

	// 更新记录
	m.mu.Lock()
	delete(m.activeRequests, request.ID)

	now := time.Now()
	record.EndedAt = &now

	if err != nil {
		if ctx.Err() != nil {
			record.Status = "cancelled"
			m.stats.CancelledCount++
		} else {
			record.Status = "timeout"
			m.stats.TimeoutCount++
		}
	} else {
		record.Status = "completed"
		record.Response = response
		m.stats.CompletedCount++
		if response.Approved {
			// 更新审批率
			m.stats.ApprovalRate = float64(m.countApproved()) / float64(m.stats.CompletedCount)
		}
	}

	// 更新平均等待时间
	totalWait := time.Duration(0)
	completedCount := int64(0)
	for _, r := range m.history {
		if r.EndedAt != nil {
			totalWait += r.EndedAt.Sub(r.StartedAt)
			completedCount++
		}
	}
	if completedCount > 0 {
		m.stats.AverageWaitTime = totalWait / time.Duration(completedCount)
	}

	m.mu.Unlock()

	return response, err
}

// countApproved 计算批准数量
func (m *HITLManager) countApproved() int64 {
	var count int64
	for _, r := range m.history {
		if r.Response != nil && r.Response.Approved {
			count++
		}
	}
	return count
}

// GetActiveRequests 获取活跃请求
func (m *HITLManager) GetActiveRequests() []*HITLRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*HITLRequest, 0, len(m.activeRequests))
	for _, req := range m.activeRequests {
		result = append(result, req)
	}
	return result
}

// GetHistory 获取历史记录
func (m *HITLManager) GetHistory(limit int) []*HITLRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 || limit > len(m.history) {
		limit = len(m.history)
	}

	// 返回最近的记录
	start := len(m.history) - limit
	if start < 0 {
		start = 0
	}

	result := make([]*HITLRecord, limit)
	copy(result, m.history[start:])
	return result
}

// GetStats 获取统计信息
func (m *HITLManager) GetStats() HITLStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats
}

// Cancel 取消请求
func (m *HITLManager) Cancel(requestID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.activeRequests[requestID]; !exists {
		return fmt.Errorf("request not found: %s", requestID)
	}

	delete(m.activeRequests, requestID)

	// 更新记录
	for _, r := range m.history {
		if r.Request.ID == requestID {
			now := time.Now()
			r.Status = "cancelled"
			r.EndedAt = &now
			m.stats.CancelledCount++
			break
		}
	}

	return nil
}

// Export 导出历史
func (m *HITLManager) Export() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return json.MarshalIndent(m.history, "", "  ")
}
