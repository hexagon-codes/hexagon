package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/net/sse"
)

// SSEEventSink 把 runtime.Event 串行序列化为 W3C SSE 帧并 flush 到 HTTP response。
//
// 把 http.ResponseWriter 包成 SSEEventSink 即获得：
//   - 自动 Content-Type / Cache-Control header
//   - 标准 `event: <type>\ndata: <json>\n\n` 帧格式
//   - 线程安全 flush（多 goroutine 同时 emit 不竞争）
//   - context.Done() 自动断流（客户端 abort 时停止序列化）
//
// 使用：
//
//	sink := runtime.NewSSEEventSink(w)
//	defer sink.Close()
//	runner.Run(ctx, req, sink)
//
// host 也可在每个事件前后塞自己的应用层事件（cron compile progress 等）。
type SSEEventSink struct {
	// sw 委托 toolkit/net/sse.Writer：负责事件帧（event:/data:）格式 + 线程安全 flush。
	sw *sse.Writer
	// w/flusher 仅供 WriteComment 写注释帧：本 sink 既定契约是 `: <text>\n\n`，
	// 而 toolkit WriteComment 用单 `\n`（SSE 注释规范）—— 为保持 wire 字节不变，
	// 注释帧仍走手写路径。事件帧（主体）已委托 toolkit。
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
	closed  bool

	// 可选过滤器：返 false 跳过该事件不发给客户端。nil 表示全发。
	Filter func(event Event) bool
}

// NewSSEEventSink 在 ResponseWriter 上准备 SSE headers 并返回 sink。
// 若 ResponseWriter 不支持 http.Flusher，返 nil + error。
func NewSSEEventSink(w http.ResponseWriter) (*SSEEventSink, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("runtime/sse: ResponseWriter 不支持 http.Flusher")
	}
	// sse.NewWriter 设置 SSE 头部（Content-Type / Cache-Control / Connection /
	// X-Accel-Buffering，与原手写一致）并提供标准事件帧格式 + 线程安全 flush。
	sw, err := sse.NewWriter(w)
	if err != nil {
		return nil, fmt.Errorf("runtime/sse: create writer: %w", err)
	}
	return &SSEEventSink{sw: sw, w: w, flusher: flusher}, nil
}

// Emit 实现 EventSink。
//
// 行为：
//   - 已 Close → 静默返 nil（不向 detached 连接写）
//   - ctx.Done → 返 ctx.Err
//   - Filter 过滤为 false → 跳过
//   - JSON 序列化失败 → 返 error（不中断 runtime）
//   - Write 失败 → 标记 closed + 返 error（避免后续 emit 继续写挂掉的连接）
func (s *SSEEventSink) Emit(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.Filter != nil && !s.Filter(event) {
		return nil
	}

	payload, err := json.Marshal(buildSSEPayload(event))
	if err != nil {
		return fmt.Errorf("runtime/sse: marshal event: %w", err)
	}
	if err := s.sw.Write(&sse.Event{Event: string(event.Type), Data: string(payload)}); err != nil {
		s.closed = true
		return fmt.Errorf("runtime/sse: write frame: %w", err)
	}
	return nil
}

// Close 标记 sink 关闭（之后的 Emit 静默 noop）。可重复调用。
func (s *SSEEventSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

// EmitRaw 发送 host 自定义的 SSE 事件，不经 runtime.Event 包装。
//
// 用途：host（如 hexclaw cron handler）想复用 SSEEventSink 的 headers
// 设置、线程安全 flush、Close 语义和 ctx 取消，但需要发自己业务定义的
// event name + payload 形态（与 runtime.EventType 枚举无关）。
//
// 行为：
//   - 已 Close → 静默返 nil
//   - ctx.Done → 返 ctx.Err
//   - 不经 Filter（Filter 是 runtime.Event 语义）
//   - JSON 序列化失败 → 返 error
//   - Write 失败 → 标记 closed + 返 error
//
// wire 格式与 Emit 一致：`event: <eventName>\ndata: <json>\n\n`
func (s *SSEEventSink) EmitRaw(ctx context.Context, eventName string, payload any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("runtime/sse: marshal raw payload: %w", err)
	}
	if err := s.sw.Write(&sse.Event{Event: eventName, Data: string(b)}); err != nil {
		s.closed = true
		return fmt.Errorf("runtime/sse: write raw frame: %w", err)
	}
	return nil
}

// WriteComment 发送 SSE 注释帧（": keepalive\n\n"），保持长连接活跃 / 调试用。
// 不算事件，不经 Filter。
func (s *SSEEventSink) WriteComment(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", text); err != nil {
		s.closed = true
		return err
	}
	s.flusher.Flush()
	return nil
}

// ssePayload 是发给客户端的 wire JSON（avoid leaking internal pointers）。
type ssePayload struct {
	Type      EventType     `json:"type"`
	RunID     string        `json:"run_id,omitempty"`
	RequestID string        `json:"request_id,omitempty"`
	SessionID string        `json:"session_id,omitempty"`
	Turn      int           `json:"turn,omitempty"`
	Sequence  int64         `json:"sequence,omitempty"`
	Timestamp time.Time     `json:"ts"`
	Summary   StateSummary  `json:"summary,omitempty"`
	ToolCall  *toolCallView `json:"tool_call,omitempty"`
	Error     string        `json:"error,omitempty"`
	Payload   any           `json:"payload,omitempty"`
}

type toolCallView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func buildSSEPayload(e Event) ssePayload {
	p := ssePayload{
		Type:      e.Type,
		RunID:     e.RunID,
		RequestID: e.RequestID,
		SessionID: e.SessionID,
		Turn:      e.Turn,
		Sequence:  e.Sequence,
		Timestamp: e.Timestamp,
		Summary:   e.StateSummary,
		Payload:   e.Payload,
	}
	if e.Error != nil {
		p.Error = e.Error.Error()
	}
	if e.ToolCall != nil {
		p.ToolCall = &toolCallView{ID: e.ToolCall.ID, Name: e.ToolCall.Name}
	}
	return p
}

// Ensure SSEEventSink satisfies the EventSink interface and io.Closer ergonomics.
var (
	_ EventSink = (*SSEEventSink)(nil)
	_ io.Closer = (*sseCloseShim)(nil)
)

// sseCloseShim 保证 SSEEventSink 满足 io.Closer 接口（Close 不返 error 时的 adapter）。
type sseCloseShim struct{ *SSEEventSink }

func (s *sseCloseShim) Close() error { s.SSEEventSink.Close(); return nil }

// AsIOCloser 把 SSEEventSink 包装为 io.Closer 供 defer 使用。
func (s *SSEEventSink) AsIOCloser() io.Closer { return &sseCloseShim{s} }
