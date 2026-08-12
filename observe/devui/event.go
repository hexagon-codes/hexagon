// Package devui 提供 Hexagon 开发调试界面
//
// DevUI 是一个轻量级的 Web 调试界面，用于实时查看 Agent 执行过程、
// LLM 调用、工具执行、RAG 检索等信息。
//
// 特性：
//   - 实时事件流推送（SSE）
//   - Span 追踪可视化
//   - 指标仪表板
//   - 零侵入集成（利用现有 Hooks + Tracer）
//
// 快速使用：
//
//	ui := devui.New(devui.WithAddr("127.0.0.1:8080"))
//	defer func() {
//	    if err := ui.Stop(context.Background()); err != nil {
//	        log.Printf("Dev UI shutdown failed: %v", err)
//	    }
//	}()
//
//	ctx := hooks.ContextWithManager(context.Background(), ui.HookManager())
//	go func() {
//	    if err := ui.Start(); err != nil {
//	        log.Printf("Dev UI stopped: %v", err)
//	    }
//	}()
//	// 使用 ctx 执行 Agent，并访问 http://localhost:8080。
package devui

import (
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/poolx"
)

// EventType 事件类型
type EventType string

const (
	// Agent 事件
	EventAgentStart EventType = "agent.start" // Agent 开始执行
	EventAgentEnd   EventType = "agent.end"   // Agent 执行结束

	// LLM 事件
	EventLLMRequest  EventType = "llm.request"  // LLM 请求开始
	EventLLMStream   EventType = "llm.stream"   // LLM 流式输出
	EventLLMResponse EventType = "llm.response" // LLM 响应完成

	// 工具事件
	EventToolCall   EventType = "tool.call"   // 工具调用开始
	EventToolResult EventType = "tool.result" // 工具返回结果

	// RAG 事件
	EventRetrieverStart EventType = "retriever.start" // 检索开始
	EventRetrieverEnd   EventType = "retriever.end"   // 检索结束

	// 图编排事件
	EventGraphStart EventType = "graph.start" // 图执行开始
	EventGraphNode  EventType = "graph.node"  // 图节点执行
	EventGraphEnd   EventType = "graph.end"   // 图执行结束

	// 状态事件
	EventStateChange EventType = "state.change" // 状态变更

	// 错误事件
	EventError EventType = "error" // 错误发生
)

// Event 统一事件结构
//
// 所有事件都使用此结构进行传输，通过 Type 字段区分事件类型，
// Data 字段存储事件特定数据。
type Event struct {
	// ID 事件唯一标识
	ID string `json:"id"`

	// Type 事件类型
	Type EventType `json:"type"`

	// Timestamp 事件发生时间
	Timestamp time.Time `json:"timestamp"`

	// TraceID 链路追踪 ID（可选）
	TraceID string `json:"trace_id,omitempty"`

	// SpanID Span ID（可选）
	SpanID string `json:"span_id,omitempty"`

	// ParentID 父 Span ID（可选）
	ParentID string `json:"parent_id,omitempty"`

	// AgentID Agent 标识（可选）
	AgentID string `json:"agent_id,omitempty"`

	// AgentName Agent 名称（可选）
	AgentName string `json:"agent_name,omitempty"`

	// Data 事件数据
	// 根据事件类型存储不同的数据结构
	Data map[string]any `json:"data"`
}

// Reset 重置事件对象到初始状态
// 用于对象池复用
func (e *Event) Reset() {
	e.ID = ""
	e.Type = ""
	e.Timestamp = time.Time{}
	e.TraceID = ""
	e.SpanID = ""
	e.ParentID = ""
	e.AgentID = ""
	e.AgentName = ""
	// 清空 map 但保留容量
	clear(e.Data)
}

// Clone 克隆事件对象
// 用于需要保留事件副本的场景
func (e *Event) Clone() *Event {
	clone := &Event{
		ID:        e.ID,
		Type:      e.Type,
		Timestamp: e.Timestamp,
		TraceID:   e.TraceID,
		SpanID:    e.SpanID,
		ParentID:  e.ParentID,
		AgentID:   e.AgentID,
		AgentName: e.AgentName,
		Data:      make(map[string]any, len(e.Data)),
	}
	for k, v := range e.Data {
		clone.Data[k] = v
	}
	return clone
}

// eventPool 事件对象池
// 使用 toolkit 的泛型对象池减少 GC 压力
var eventPool = mustNewEventPool()

// mustNewEventPool 使用静态已知合法的配置创建事件对象池。
func mustNewEventPool() *poolx.ObjectPool[*Event] {
	p, err := poolx.NewObjectPool(
		func() *Event {
			return &Event{
				Data: make(map[string]any, 8), // 预分配常用容量
			}
		},
		func(e **Event) {
			(*e).Reset()
		},
	)
	if err != nil {
		panic(fmt.Errorf("create event pool: %w", err))
	}

	return p
}

// AcquireEvent 从对象池获取事件对象
func AcquireEvent() *Event {
	return eventPool.Get()
}

// ReleaseEvent 将事件对象归还到对象池
func ReleaseEvent(e *Event) {
	if e != nil {
		eventPool.Put(e)
	}
}

// AgentStartData Agent 开始事件数据
type AgentStartData struct {
	RunID    string         `json:"run_id"`
	Input    any            `json:"input"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// AgentEndData Agent 结束事件数据
type AgentEndData struct {
	RunID    string         `json:"run_id"`
	Output   any            `json:"output"`
	Duration int64          `json:"duration_ms"` // 毫秒
	Metadata map[string]any `json:"metadata,omitempty"`
}

// LLMRequestData LLM 请求事件数据
type LLMRequestData struct {
	RunID       string  `json:"run_id"`
	Provider    string  `json:"provider"`
	Model       string  `json:"model"`
	Messages    any     `json:"messages"`
	Temperature float64 `json:"temperature,omitempty"`
}

// LLMStreamData LLM 流式输出事件数据
type LLMStreamData struct {
	RunID   string `json:"run_id"`
	Model   string `json:"model"`
	Content string `json:"content"`
	Index   int    `json:"index"`
}

// LLMResponseData LLM 响应事件数据
type LLMResponseData struct {
	RunID            string `json:"run_id"`
	Model            string `json:"model"`
	Response         any    `json:"response"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	Duration         int64  `json:"duration_ms"` // 毫秒
}

// ToolCallData 工具调用事件数据
type ToolCallData struct {
	RunID    string         `json:"run_id"`
	ToolID   string         `json:"tool_id"`
	ToolName string         `json:"tool_name"`
	Input    map[string]any `json:"input"`
}

// ToolResultData 工具结果事件数据
type ToolResultData struct {
	RunID    string `json:"run_id"`
	ToolID   string `json:"tool_id"`
	ToolName string `json:"tool_name"`
	Output   any    `json:"output"`
	Duration int64  `json:"duration_ms"` // 毫秒
	Error    string `json:"error,omitempty"`
}

// RetrieverStartData 检索开始事件数据
type RetrieverStartData struct {
	RunID string `json:"run_id"`
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

// RetrieverEndData 检索结束事件数据
type RetrieverEndData struct {
	RunID     string `json:"run_id"`
	Query     string `json:"query"`
	DocCount  int    `json:"doc_count"`
	Documents any    `json:"documents,omitempty"`
	Duration  int64  `json:"duration_ms"` // 毫秒
}

// GraphNodeData 图节点事件数据
type GraphNodeData struct {
	RunID    string         `json:"run_id"`
	GraphID  string         `json:"graph_id"`
	NodeID   string         `json:"node_id"`
	NodeName string         `json:"node_name"`
	State    map[string]any `json:"state,omitempty"`
	Duration int64          `json:"duration_ms,omitempty"` // 毫秒
}

// ErrorData 错误事件数据
type ErrorData struct {
	RunID   string `json:"run_id"`
	Source  string `json:"source"` // agent, llm, tool, retriever, graph
	Message string `json:"message"`
	Stack   string `json:"stack,omitempty"`
}

// RingBuffer 环形缓冲区
// 用于存储固定数量的最近事件
type RingBuffer struct {
	events []*Event
	head   int // 下一个写入位置
	size   int // 当前元素数量
	cap    int // 容量
	mu     sync.RWMutex
}

// NewRingBuffer 创建环形缓冲区
//
// 参数：
//   - capacity: 缓冲区容量
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 1000
	}
	return &RingBuffer{
		events: make([]*Event, capacity),
		cap:    capacity,
	}
}

// Push 添加事件到缓冲区
// 如果缓冲区已满，会覆盖最旧的事件
func (rb *RingBuffer) Push(e *Event) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// 克隆事件，避免外部修改影响缓冲区
	clone := e.Clone()

	// 如果有旧事件，归还到池
	if rb.events[rb.head] != nil {
		ReleaseEvent(rb.events[rb.head])
	}

	rb.events[rb.head] = clone
	rb.head = (rb.head + 1) % rb.cap

	if rb.size < rb.cap {
		rb.size++
	}
}

// GetAll 获取所有事件（从最旧到最新）
func (rb *RingBuffer) GetAll() []*Event {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	result := make([]*Event, 0, rb.size)

	if rb.size == 0 {
		return result
	}

	// 计算起始位置
	start := 0
	if rb.size == rb.cap {
		start = rb.head // 如果已满，从 head 开始是最旧的
	}

	for i := 0; i < rb.size; i++ {
		idx := (start + i) % rb.cap
		if rb.events[idx] != nil {
			result = append(result, rb.events[idx].Clone())
		}
	}

	return result
}

// GetRecent 获取最近 n 个事件（从最新到最旧）
func (rb *RingBuffer) GetRecent(n int) []*Event {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if n <= 0 || rb.size == 0 {
		return nil
	}

	if n > rb.size {
		n = rb.size
	}

	result := make([]*Event, n)

	for i := 0; i < n; i++ {
		// 从 head-1 开始往回取
		idx := (rb.head - 1 - i + rb.cap) % rb.cap
		if rb.events[idx] != nil {
			result[i] = rb.events[idx].Clone()
		}
	}

	return result
}

// Size 返回当前事件数量
func (rb *RingBuffer) Size() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.size
}

// Clear 清空缓冲区
func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	for i := 0; i < rb.cap; i++ {
		if rb.events[i] != nil {
			ReleaseEvent(rb.events[i])
			rb.events[i] = nil
		}
	}
	rb.head = 0
	rb.size = 0
}
