package runtime

import (
	"context"

	"github.com/hexagon-codes/ai-core/llm"
)

// StreamMode controls how the runtime invokes the provider and projects output.
type StreamMode string

const (
	// StreamModeOff uses provider.Complete and only emits lifecycle events.
	StreamModeOff StreamMode = "off"
	// StreamModeEvents uses provider.Complete while still emitting runtime events.
	StreamModeEvents StreamMode = "events"
	// StreamModeTokens uses provider.Stream and emits LLMChunk events.
	StreamModeTokens StreamMode = "tokens"
)

// Request is the product-neutral input for an agent runtime run.
type Request struct {
	ID           string
	Messages     []llm.Message
	Tools        []llm.ToolDefinition
	ProviderName string
	ModelName    string
	Metadata     map[string]any
	Limits       Limits
	Strategy     Strategy
	StreamMode   StreamMode
}

// Limits constrains a runtime run.
type Limits struct {
	MaxTurns int
}

// Result is the product-neutral output of an agent runtime run.
type Result struct {
	Content   string
	Reasoning string
	ToolCalls []ToolCallRecord
	Usage     llm.Usage
	Metadata  map[string]any
}

// State is the mutable state owned by the runtime state machine.
type State struct {
	Request Request

	Messages  []llm.Message
	ToolCalls []ToolCallRecord
	Usage     llm.Usage

	Turn       int
	Final      bool
	FinalText  string
	Reasoning  string
	Attributes map[string]any

	emit func(context.Context, Event) error
}

// ToolCallRecord records a completed tool call.
type ToolCallRecord struct {
	ID        string
	Name      string
	Arguments string
	Result    ToolResult
}

// Snapshot 返回 State 的独立只读副本，安全用于跨 goroutine 保留与异步读取。
//
// 并发契约：EventSink 收到的 Event.State 是运行时**仍在演进的活指针**——它由运行
// 循环单线程持有并在步边界 append Messages / 写 Attributes。emit 为同步调用，故 sink
// 在回调**内**（运行循环阻塞等待期间）读取或 Snapshot 是安全的；但若 sink 想在回调
// 返回后保留 State、或另起 goroutine 异步读取，**必须先在回调内 Snapshot 取独立副本**，
// 否则与后续步边界的写入构成 data race（Messages 切片读写 / Attributes map 并发读写 panic）。
//
// 复制 Messages/ToolCalls/Attributes 这三处可变部分到全新底层存储；emit 句柄不复制
// （快照是只读视图，不应触发事件）。
func (s *State) Snapshot() *State {
	if s == nil {
		return nil
	}
	var attrs map[string]any
	if s.Attributes != nil {
		attrs = make(map[string]any, len(s.Attributes))
		for k, v := range s.Attributes {
			attrs[k] = v
		}
	}
	return &State{
		Request:    s.Request,
		Messages:   append([]llm.Message(nil), s.Messages...),
		ToolCalls:  append([]ToolCallRecord(nil), s.ToolCalls...),
		Usage:      s.Usage,
		Turn:       s.Turn,
		Final:      s.Final,
		FinalText:  s.FinalText,
		Reasoning:  s.Reasoning,
		Attributes: attrs,
	}
}

// AddUsage accumulates token usage.
func (s *State) AddUsage(u llm.Usage) {
	s.Usage.PromptTokens += u.PromptTokens
	s.Usage.CompletionTokens += u.CompletionTokens
	s.Usage.TotalTokens += u.TotalTokens
	s.Usage.CacheCreationTokens += u.CacheCreationTokens
	s.Usage.CacheReadTokens += u.CacheReadTokens
}

// Emit lets middleware publish runtime events without knowing the runner internals.
func (s *State) Emit(ctx context.Context, event Event) error {
	if s == nil || s.emit == nil {
		return nil
	}
	event.State = s
	return s.emit(ctx, event)
}
