// Package graph 提供 Hexagon AI Agent 框架的图编排引擎
//
// stream_mode.go 实现多种流式输出模式，参考 LangGraph 的设计：
//   - StreamModeValues: 每步输出完整状态快照
//   - StreamModeUpdates: 每步输出状态增量变化
//   - StreamModeMessages: 输出 LLM 生成的 token 流
//   - StreamModeCustom: 用户自定义事件流
//   - StreamModeDebug: 详细调试信息流
//
// 使用示例：
//
//	stream, err := graph.StreamRun(ctx, state,
//	    WithStreamMode(StreamModeUpdates),
//	)
//	for event := range stream.Events() {
//	    fmt.Println(event.Node, event.Data)
//	}
package graph

import (
	"context"
	"sync"
	"time"
)

// StreamMode 流式输出模式
type StreamMode int

const (
	// StreamModeValues 完整状态快照模式
	// 每个节点执行后输出完整的当前状态
	StreamModeValues StreamMode = iota

	// StreamModeUpdates 增量更新模式
	// 每个节点执行后只输出变化的部分
	StreamModeUpdates

	// StreamModeMessages LLM 消息流模式
	// 输出 LLM 生成的每个 token
	StreamModeMessages

	// StreamModeCustom 自定义事件模式
	// 节点可以发射自定义事件
	StreamModeCustom

	// StreamModeDebug 调试模式
	// 输出详细的执行信息（节点开始/结束/耗时/错误）
	StreamModeDebug
)

// StreamModeEvent 流式事件
type StreamModeEvent struct {
	// Mode 事件对应的流模式
	Mode StreamMode `json:"mode"`

	// Node 产生事件的节点名称
	Node string `json:"node"`

	// Type 事件类型
	Type StreamModeEventType `json:"type"`

	// Data 事件数据
	Data any `json:"data,omitempty"`

	// Timestamp 事件时间戳
	Timestamp time.Time `json:"timestamp"`

	// Metadata 额外元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// StreamModeEventType 流式事件类型
type StreamModeEventType string

const (
	// EventNodeStart 节点开始执行
	EventNodeStart StreamModeEventType = "node_start"

	// EventNodeEnd 节点执行结束
	EventNodeEnd StreamModeEventType = "node_end"

	// EventNodeError 节点执行错误
	EventNodeError StreamModeEventType = "node_error"

	// EventStateUpdate 状态更新
	EventStateUpdate StreamModeEventType = "state_update"

	// EventStateSnapshot 状态快照
	EventStateSnapshot StreamModeEventType = "state_snapshot"

	// EventMessage LLM 消息
	EventMessage StreamModeEventType = "message"

	// EventToken LLM Token
	EventToken StreamModeEventType = "token"

	// EventCustom 自定义事件
	EventCustom StreamModeEventType = "custom"

	// EventGraphStart 图开始执行
	EventGraphStart StreamModeEventType = "graph_start"

	// EventGraphEnd 图执行结束
	EventGraphEnd StreamModeEventType = "graph_end"
)

// StreamChannel 流式事件通道
// 提供对图执行过程中产生的事件的读取能力
type StreamChannel struct {
	events chan StreamModeEvent
	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

// NewStreamChannel 创建流式事件通道
func NewStreamChannel(bufferSize int) *StreamChannel {
	if bufferSize <= 0 {
		bufferSize = 100
	}
	return &StreamChannel{
		events: make(chan StreamModeEvent, bufferSize),
		done:   make(chan struct{}),
	}
}

// Events 返回事件读取通道
func (sc *StreamChannel) Events() <-chan StreamModeEvent {
	return sc.events
}

// Done 返回完成信号通道
func (sc *StreamChannel) Done() <-chan struct{} {
	return sc.done
}

// Emit 发射一个事件
// 如果通道已关闭或缓冲区已满，事件将被丢弃
func (sc *StreamChannel) Emit(event StreamModeEvent) {
	sc.mu.Lock()
	if sc.closed {
		sc.mu.Unlock()
		return
	}
	sc.mu.Unlock()

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	select {
	case sc.events <- event:
	default:
		// 缓冲区满，丢弃事件（避免阻塞节点执行）
	}
}

// Close 关闭事件通道
func (sc *StreamChannel) Close() {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if !sc.closed {
		sc.closed = true
		close(sc.events)
		close(sc.done)
	}
}

// StreamRunOption 流式执行选项
type StreamRunOption func(*streamRunConfig)

type streamRunConfig struct {
	modes      []StreamMode
	bufferSize int
	filter     func(StreamModeEvent) bool
}

// WithStreamMode 设置流式模式
func WithStreamMode(modes ...StreamMode) StreamRunOption {
	return func(c *streamRunConfig) {
		c.modes = append(c.modes, modes...)
	}
}

// WithStreamBufferSize 设置事件缓冲区大小
func WithStreamBufferSize(size int) StreamRunOption {
	return func(c *streamRunConfig) {
		c.bufferSize = size
	}
}

// WithStreamFilter 设置事件过滤器
func WithStreamFilter(filter func(StreamModeEvent) bool) StreamRunOption {
	return func(c *streamRunConfig) {
		c.filter = filter
	}
}

// StreamRun 以流式模式执行图
// 返回 StreamChannel 用于读取执行过程中的事件
func (g *Graph[S]) StreamRun(ctx context.Context, state S, opts ...StreamRunOption) (*StreamChannel, error) {
	cfg := &streamRunConfig{
		modes:      []StreamMode{StreamModeValues},
		bufferSize: 100,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	modeSet := make(map[StreamMode]bool)
	for _, m := range cfg.modes {
		modeSet[m] = true
	}

	ch := NewStreamChannel(cfg.bufferSize)

	go func() {
		defer ch.Close()

		// 发射图开始事件
		if modeSet[StreamModeDebug] {
			ch.Emit(StreamModeEvent{
				Mode: StreamModeDebug,
				Type: EventGraphStart,
				Node: g.Name,
				Data: map[string]any{"name": g.Name},
			})
		}

		streamCtx := WithStreamChannel(ctx, ch)
		events, err := g.Stream(streamCtx, state)
		if err != nil {
			ch.Emit(StreamModeEvent{Mode: StreamModeDebug, Type: EventNodeError, Data: err.Error()})
			return
		}
		for event := range events {
			if event.Type == EventTypeError {
				ch.Emit(StreamModeEvent{Mode: StreamModeDebug, Type: EventNodeError, Node: event.NodeName, Data: event.Error.Error()})
				return
			}
			if modeSet[StreamModeDebug] {
				kind := EventNodeStart
				if event.Type == EventTypeNodeEnd {
					kind = EventNodeEnd
				}
				if event.Type == EventTypeEnd {
					kind = EventGraphEnd
				}
				ch.Emit(StreamModeEvent{Mode: StreamModeDebug, Type: kind, Node: event.NodeName, Data: event.State})
			}
			if event.Type != EventTypeNodeEnd {
				continue
			}
			for _, mode := range []StreamMode{StreamModeValues, StreamModeUpdates} {
				if !modeSet[mode] {
					continue
				}
				kind := EventStateSnapshot
				if mode == StreamModeUpdates {
					kind = EventStateUpdate
				}
				item := StreamModeEvent{Mode: mode, Type: kind, Node: event.NodeName, Data: event.State}
				if cfg.filter == nil || cfg.filter(item) {
					ch.Emit(item)
				}
			}
		}

	}()

	return ch, nil
}

// findNextNode 查找下一个节点
func (g *Graph[S]) findNextNode(current string) string {
	// 先查邻接表
	if targets, ok := g.adjacency[current]; ok && len(targets) > 0 {
		return targets[0]
	}

	// 再查边列表
	for _, edge := range g.Edges {
		if edge.From == current {
			return edge.To
		}
	}

	return ""
}

// EmitCustomEvent 在节点处理函数中发射自定义事件
// 需要通过 context 传递 StreamChannel
func EmitCustomEvent(ctx context.Context, name string, data any) {
	ch, ok := ctx.Value(streamChannelKey{}).(*StreamChannel)
	if !ok || ch == nil {
		return
	}

	ch.Emit(StreamModeEvent{
		Mode: StreamModeCustom,
		Type: EventCustom,
		Data: map[string]any{
			"name": name,
			"data": data,
		},
	})
}

type streamChannelKey struct{}

// WithStreamChannel 将 StreamChannel 注入到 context 中
func WithStreamChannel(ctx context.Context, ch *StreamChannel) context.Context {
	return context.WithValue(ctx, streamChannelKey{}, ch)
}
