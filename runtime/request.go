package runtime

import (
	"context"
	"encoding/json"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/template"
	"github.com/hexagon-codes/hexagon/interrupt"
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
	// Generation 由 ai-core 表示模型参数，运行时负责冻结并透传到实际调用。
	Generation llm.GenerationOptions
	// Purpose 标识推理、摘要或结构化修正等调用用途，不进入模型协议参数。
	Purpose string
	// StructuredOutput 是本次最终结果的显式结构化约束；中间业务工具仍由执行核调度。
	StructuredOutput *StructuredOutputSpec
}

type StructuredOutputMode string

const (
	StructuredModePrompt StructuredOutputMode = "prompt"
	StructuredModeNative StructuredOutputMode = "native"
	StructuredModeTool   StructuredOutputMode = "tool"
)

type StructuredOutputSpec struct {
	Schema     *llm.Schema
	Mode       StructuredOutputMode
	Name       string
	ToolName   string
	MaxRepairs int
}

// Limits constrains a runtime run.
type Limits struct {
	MaxTurns int
}

const (
	// BudgetStartedAtMetadataKey 保存本 Run 首次预算检查的墙钟起点。
	BudgetStartedAtMetadataKey = "runtime.budget.started_at"
	// UsageSourceMetadataKey 标识实报完整性：reported、mixed 或 unknown。
	UsageSourceMetadataKey = "usage_source"
	// UsageReportedCallsMetadataKey 记录取得实际用量的模型调用数，明确零用量也计入。
	UsageReportedCallsMetadataKey = "usage_reported_calls"
	// UsageMissingCallsMetadataKey 记录没有实际用量回执的模型调用数。
	UsageMissingCallsMetadataKey = "usage_missing_calls"
)

// StopReason 是运行终止的一等原因，对齐 Anthropic/OpenAI 的 stop_reason 语义：到达 limit
// 是正常终止而非错误。它是表达正常结果「为什么停」的唯一机制，调用方据此
// 决定如何呈现（如 max_turns 时提示「可继续」），无需 errors.Is 反查。达到轮次上限不再产生
// 任何 error（Run/Stream 返回 nil error + StopReason=max_turns）。
// 错误同时携带的部分 Result 不填写 StopReason；取消／失败原因仍由原 error 传播。
type StopReason string

const (
	// StopReasonEndTurn 模型给出了最终答案，运行正常结束。
	StopReasonEndTurn StopReason = "end_turn"
	// StopReasonMaxTurns 达到工具循环轮次上限仍无终态——结果携带已产出的部分内容/用量，
	// 调用方可据此呈现部分结果并提示用户继续。
	StopReasonMaxTurns       StopReason = "max_turns"
	StopReasonOutputLimit    StopReason = "output_limit"
	StopReasonRefusal        StopReason = "refusal"
	StopReasonContentFilter  StopReason = "content_filter"
	StopReasonPauseTurn      StopReason = "pause_turn"
	StopReasonModelCancelled StopReason = "model_cancelled"
	StopReasonModelStopped   StopReason = "model_stopped"
)

// Result is the product-neutral output of an agent runtime run.
type Result struct {
	Content   string
	Reasoning string
	ToolCalls []ToolCallRecord
	// Blocks 是本次运行的**有序内容块流**（text/tool_use/tool_result 按执行序交错），
	// 修复 Content 单串 + ToolCalls 扁平数组无法表达多步 text↔tool 交错的缺陷。
	// 客户端有此字段时按序渲染，否则回退 Content + ToolCalls。
	Blocks template.Blocks
	Usage  llm.Usage
	// Metadata 的 usage_source/usage_reported_calls/usage_missing_calls 保留用量来源；
	// Usage 只累计已知原始用量，不在 Runtime 中估算未上报部分。
	Metadata   map[string]any
	StopReason StopReason
	// ModelFinishReason 保真供应商结束原因，不能当作业务任务完成的证据。
	ModelFinishReason string
	Structured        json.RawMessage
	// OutputMessages 按执行顺序保存本轮消息及私有续传状态，不进入公开结果 JSON。
	// 调用方如需跨请求持久化，必须使用自己的私有消息封装。
	OutputMessages []llm.Message `json:"-"`
}

// State is the mutable state owned by the runtime state machine.
type State struct {
	Request Request

	Messages []llm.Message
	// MessageRevision 由执行核推进，候选采用还须验证对应前缀内容及水位。
	MessageRevision uint64
	ToolCalls       []ToolCallRecord
	Usage           llm.Usage

	Turn       int
	Final      bool
	FinalText  string
	Reasoning  string
	Attributes map[string]any

	// runStart 是本次运行在 Messages 中的起始下标（init + 系统前缀之后）。
	// 用于从 Messages 切出"本次运行新增的 assistant/tool 消息"重建有序块，
	// 排除输入历史。
	runStart int
	// outputMessages 只记录本次运行的实际产出，不能随模型输入压缩而丢失或混入历史。
	// nil 保留旧快照和直接构造 State 的兼容路径。
	outputMessages []llm.Message
	// partialText 是已向调用方输出、但该次模型流未完成的正文前缀。
	partialText            string
	lastAttemptID          string
	stopReason             StopReason
	modelFinishReason      string
	structured             json.RawMessage
	structuredFinishReason string
	phase                  ExecutionPhase
	appliedContextUpdates  map[string]bool
	toolSnapshot           *ToolSnapshot
	waitingSignal          *interrupt.InterruptSignal
	waitingSaved           *interrupt.PersistedSignal
	// draft 边界只覆盖未确认模型正文，fallback 可丢弃它而保留已完成工具事实。
	hasPartialDraft   bool
	draftMessageStart int
	draftOutputStart  int
	// pendingToolStart 限定当前工具批在累计记录中的起点，不跨轮复用完成身份。
	pendingToolStart int

	emit func(context.Context, Event) error
}

// ToolCallRecord records a completed tool call.
type ToolCallRecord struct {
	ID        string
	Name      string
	Arguments string
	Result    ToolResult
}

// Snapshot 复制消息、工具、参数和可隔离的数据容器；修改投影不提交到运行状态。
// Strategy 与不可复制的宿主资源按只读借用合同保留，由装配者管理其生命周期，
// 不得通过投影操作共享句柄。emit 不复制，异步观察不能用快照反向发布控制事件。
func (s *State) Snapshot() *State {
	if s == nil {
		return nil
	}
	attrs := cloneRuntimeMetadata(s.Attributes)
	request, err := CloneRequest(s.Request)
	if err != nil {
		request = s.Request
		request.Messages = CloneOutputMessages(s.Request.Messages)
		request.Metadata = cloneRuntimeMetadata(s.Request.Metadata)
		request.Tools = nil
		request.Generation = llm.GenerationOptions{}
		if attrs == nil {
			attrs = make(map[string]any)
		}
		attrs["snapshot_request_error"] = err.Error()
	}
	toolCalls := append([]ToolCallRecord(nil), s.ToolCalls...)
	for i := range toolCalls {
		toolCalls[i].Result.Raw = cloneRuntimeValue(toolCalls[i].Result.Raw)
		toolCalls[i].Result.ContentParts = cloneContentParts(toolCalls[i].Result.ContentParts)
		toolCalls[i].Result.ToolSnapshot = cloneRuntimeToolSnapshot(toolCalls[i].Result.ToolSnapshot)
		toolCalls[i].Result.Structured = cloneStructuredPayload(toolCalls[i].Result.Structured)
	}
	return &State{
		Request:                request,
		Messages:               CloneOutputMessages(s.Messages),
		MessageRevision:        s.MessageRevision,
		ToolCalls:              toolCalls,
		Usage:                  s.Usage,
		Turn:                   s.Turn,
		Final:                  s.Final,
		FinalText:              s.FinalText,
		Reasoning:              s.Reasoning,
		Attributes:             attrs,
		runStart:               s.runStart,
		outputMessages:         cloneOutputMessages(s.outputMessages),
		partialText:            s.partialText,
		stopReason:             s.stopReason,
		modelFinishReason:      s.modelFinishReason,
		structured:             append(json.RawMessage(nil), s.structured...),
		structuredFinishReason: s.structuredFinishReason,
		appliedContextUpdates:  cloneUpdateIDs(s.appliedContextUpdates),
		toolSnapshot:           cloneRuntimeToolSnapshot(s.toolSnapshot),
		hasPartialDraft:        s.hasPartialDraft,
		draftMessageStart:      s.draftMessageStart,
		draftOutputStart:       s.draftOutputStart,
		pendingToolStart:       s.pendingToolStart,
	}
}

func cloneOutputMessages(messages []llm.Message) []llm.Message {
	return CloneOutputMessages(messages)
}

// CloneOutputMessages 复制消息、工具引用、多模态内容及协议续传字节。
// nil 与非 nil 空切片分别保留，副本不共享消息内部的可变存储。
func CloneOutputMessages(messages []llm.Message) []llm.Message {
	if messages == nil {
		return nil
	}
	cloned := make([]llm.Message, len(messages))
	for i, message := range messages {
		cloned[i] = cloneMessage(message)
	}
	return cloned
}

func cloneMessage(message llm.Message) llm.Message {
	message.ProviderContinuation = message.ProviderContinuation.Clone()
	if message.ToolCalls != nil {
		message.ToolCalls = append([]llm.ToolCallRef{}, message.ToolCalls...)
	}
	if message.MultiContent != nil {
		message.MultiContent = append([]llm.ContentPart{}, message.MultiContent...)
		for i := range message.MultiContent {
			if original := message.MultiContent[i].ImageURL; original != nil {
				image := *original
				message.MultiContent[i].ImageURL = &image
			}
		}
	}
	return message
}

func (s *State) appendOutputMessage(message llm.Message) {
	s.Messages = append(s.Messages, cloneMessage(message))
	s.MessageRevision++
	if s.outputMessages != nil {
		s.outputMessages = append(s.outputMessages, cloneMessage(message))
	}
}

func cloneUpdateIDs(ids map[string]bool) map[string]bool {
	if ids == nil {
		return nil
	}
	out := make(map[string]bool, len(ids))
	for id, applied := range ids {
		out[id] = applied
	}
	return out
}

// AddUsage accumulates token usage.
func (s *State) AddUsage(u llm.Usage) {
	s.Usage.PromptTokens += u.PromptTokens
	s.Usage.CompletionTokens += u.CompletionTokens
	s.Usage.TotalTokens += u.TotalTokens
	s.Usage.CacheCreationTokens += u.CacheCreationTokens
	s.Usage.CacheReadTokens += u.CacheReadTokens
}

func (s *State) recordProviderUsage(usage llm.Usage, receipt *llm.Usage) {
	reported := receipt != nil || usage != (llm.Usage{})
	if receipt != nil {
		usage = *receipt
	}
	s.AddUsage(usage)
	reportedCalls := usageCount(s.Attributes, UsageReportedCallsMetadataKey)
	missingCalls := usageCount(s.Attributes, UsageMissingCallsMetadataKey)
	if reported {
		reportedCalls++
	} else {
		missingCalls++
	}
	s.setUsageEvidence(reportedCalls, missingCalls)
}

func (s *State) setUsageEvidence(reportedCalls, missingCalls int) {
	if s.Attributes == nil {
		s.Attributes = make(map[string]any)
	}
	source := "unknown"
	if reportedCalls > 0 {
		source = "reported"
		if missingCalls > 0 {
			source = "mixed"
		}
	}
	s.Attributes[UsageSourceMetadataKey] = source
	s.Attributes[UsageReportedCallsMetadataKey] = reportedCalls
	s.Attributes[UsageMissingCallsMetadataKey] = missingCalls
}

// Emit lets middleware publish runtime events without knowing the runner internals.
func (s *State) Emit(ctx context.Context, event Event) error {
	if s == nil || s.emit == nil {
		return nil
	}
	event.State = s
	return s.emit(ctx, event)
}

// usageCount读取旧快照中可缺省的计量元数据，未声明时沿原合同视为零。
func usageCount(attributes map[string]any, key string) int {
	if count, ok := attributes[key].(int); ok {
		return count
	}
	return 0
}
