package runtime

import (
	"context"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
)

const EventVersionV1 = "runtime.event.v1"

// EventType identifies a runtime event.
type EventType string

const (
	EventRunStarted           EventType = "run_started"
	EventProviderSelected     EventType = "provider_selected"
	EventLLMStarted           EventType = "llm_started"
	EventLLMChunk             EventType = "llm_chunk"
	EventLLMCompleted         EventType = "llm_completed"
	EventModelAttemptStarted  EventType = "model_attempt_started"
	EventModelAttemptFinished EventType = "model_attempt_finished"
	EventToolCallStarted      EventType = "tool_call_started"
	EventToolCallCompleted    EventType = "tool_call_completed"
	EventToolCallFailed       EventType = "tool_call_failed"
	EventProviderFallback     EventType = "provider_fallback"
	EventBudgetChecked        EventType = "budget_checked"
	EventBudgetExceeded       EventType = "budget_exceeded"
	EventContextCompacted     EventType = "context_compacted"
	EventPermissionRequested  EventType = "permission_requested"
	EventPermissionApproved   EventType = "permission_approved"
	EventPermissionDenied     EventType = "permission_denied"
	EventReasoningSanitized   EventType = "reasoning_sanitized"
	EventCheckpointSaved      EventType = "checkpoint_saved"
	EventRunFinished          EventType = "run_finished"
	EventRunFailed            EventType = "run_failed"
	EventRunWaiting           EventType = "run_waiting"
	EventRunCancelled         EventType = "run_cancelled"

	// 长时任务进度事件（B-fw5）—— 供 hexeye 等"提交→轮询"长任务把进度作为一类
	// RuntimeEvent 贯通到 SSE。载荷置于 Event.Payload（*JobProgress），SSE sink 自动透传。
	EventJobQueued   EventType = "job_queued"
	EventJobRunning  EventType = "job_running"
	EventJobProgress EventType = "job_progress"
	EventJobPreview  EventType = "job_preview"
	EventJobDone     EventType = "job_done"
	EventJobFailed   EventType = "job_failed"
	// EventHeartbeat 心跳事件，保持长连接活跃（无业务载荷）。
	EventHeartbeat EventType = "heartbeat"
)

// JobProgress 是长时任务进度事件的载荷（置于 Event.Payload，随 SSE 透传给客户端）。
//
// 用于 hexeye 短剧/媒体等长耗时"提交→轮询"任务：把 queued/running/progress(x%)/preview/done
// 作为统一的一类 RuntimeEvent 上报，desktop 的 /cost、前端进度条等可直接消费。
type JobProgress struct {
	// JobID 任务标识。
	JobID string `json:"job_id,omitempty"`
	// Stage 阶段名（queued/running/progress/preview/done/failed）。
	Stage string `json:"stage,omitempty"`
	// Percent 进度百分比 [0,100]，progress 阶段有效。
	Percent float64 `json:"percent,omitempty"`
	// Message 人类可读进度描述。
	Message string `json:"message,omitempty"`
	// Preview 阶段性预览（缩略图 URL / 部分结果等，任意可序列化值）。
	Preview any `json:"preview,omitempty"`
}

// StateSummary is a stable, low-cardinality view of runtime state for event consumers.
type StateSummary struct {
	Turn      int  `json:"turn"`
	Final     bool `json:"final"`
	Messages  int  `json:"messages"`
	ToolCalls int  `json:"tool_calls"`
}

// Redaction describes whether sensitive event payload fields were redacted.
type Redaction struct {
	Applied bool     `json:"applied,omitempty"`
	Fields  []string `json:"fields,omitempty"`
}

// Event is emitted by the runtime state machine.
type Event struct {
	// ID 仅在必要事实已持久提交后分配；临时 token 不获得恢复游标。
	ID           string
	Version      string
	Type         EventType
	RunID        string
	RequestID    string
	SessionID    string
	Turn         int
	Sequence     int64
	Timestamp    time.Time
	TraceID      string
	SpanID       string
	ParentSpanID string
	AttemptID    string

	// State 是执行核在事件边界产生的只读投影，不传递活跃 RunState。
	// 数据容器独立，宿主资源句柄遵循 Snapshot 的只读借用合同；标量观察优先 StateSummary。
	State        *State
	StateSummary StateSummary
	Response     *llm.CompletionResponse
	Chunk        *llm.StreamChunk
	ToolCall     *llm.ToolCall
	ToolResult   *ToolResult
	Error        error
	RuntimeError *RuntimeError
	Metadata     map[string]any
	Payload      any
	Redaction    Redaction
}

// EventSink consumes runtime events.
type EventSink interface {
	Emit(ctx context.Context, event Event) error
}

// EventSinkFunc adapts a function to EventSink.
type EventSinkFunc func(ctx context.Context, event Event) error

// Emit implements EventSink.
func (f EventSinkFunc) Emit(ctx context.Context, event Event) error {
	if f == nil {
		return nil
	}
	return f(ctx, event)
}

func emit(ctx context.Context, sink EventSink, event Event) error {
	if sink == nil {
		return nil
	}
	return invokeExtension("event_sink", func() error { return sink.Emit(ctx, event) })
}
