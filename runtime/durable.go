package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/checkpoint"
	"github.com/hexagon-codes/hexagon/interrupt"
)

// Snapshot 是一次执行在某个步边界上的持久化状态。
//
// 它只包含恢复一次 Agent 执行所必需的最小状态：步号、消息历史、已完成工具调用记录、
// 累计用量与终止信息。Reasoning、Attributes 等运行期派生/易变数据不入快照，以免把
// 易变细节固化进持久化格式、并降低与具体执行实现的耦合。
type Snapshot struct {
	Phase ExecutionPhase `json:"phase,omitempty"`
	// Spec 与 Codec 固定在途配置及状态编码；nil/零值保留旧检查点读取路径。
	Spec  *RunSpec   `json:"spec,omitempty"`
	Codec StateCodec `json:"codec,omitempty"`
	// RunID 标识一次执行，同时作为持久化命名空间。
	RunID string `json:"run_id"`
	// Step 快照所在的步边界（对应 State.Turn）。
	Step int `json:"step"`
	// Messages 截至本步的消息历史。
	Messages []llm.Message `json:"messages"`
	// OutputMessages 保存本轮实际产出；nil 表示旧快照，空数组表示尚无产出。
	// 它与可压缩的模型输入分离，恢复时不重放输入历史作为新回复。
	OutputMessages *[]llm.Message `json:"output_messages,omitempty"`
	// ToolCalls 截至本步已完成的工具调用记录。
	ToolCalls []ToolCallRecord `json:"tool_calls,omitempty"`
	// Usage 截至本步的累计 token 用量（计量维度）。
	Usage llm.Usage `json:"usage"`
	// UsageReportedCalls 与 UsageMissingCalls 保留实际模型调用的回执完整性，恢复后不重新计数。
	UsageReportedCalls        int `json:"usage_reported_calls,omitempty"`
	UsageMissingCalls         int `json:"usage_missing_calls,omitempty"`
	UsagePartialCalls         int `json:"usage_partial_calls,omitempty"`
	UsageKnownTokenLowerBound int `json:"usage_known_token_lower_bound,omitempty"`
	// Final 标记执行是否已得出最终答案。
	Final bool `json:"final,omitempty"`
	// FinalText 最终答案文本（Final 为真时有效）。
	FinalText             string          `json:"final_text,omitempty"`
	StopReason            StopReason      `json:"stop_reason,omitempty"`
	ModelFinishReason     string          `json:"model_finish_reason,omitempty"`
	MessageRevision       uint64          `json:"message_revision,omitempty"`
	AppliedContextUpdates map[string]bool `json:"applied_context_updates,omitempty"`
	Structured            json.RawMessage `json:"structured,omitempty"`
	Tools                 *ToolSnapshot   `json:"tools,omitempty"`
	// Pending 非空表示这是一个「步内意图快照」：该步的工具调用已发起但尚未确认全部
	// 完成（崩溃窗口）。它记录这些工具的重放安全性，供 Resume 判定能否安全续跑——
	// 含 Unsafe 工具时 Resume fail-closed（ErrUnsafeReplay），全部可重放安全时重跑该步。
	// 步正常完成后的完成快照不带 Pending（同步号覆盖意图快照）。
	Pending []PendingTool `json:"pending,omitempty"`
	// PendingToolCallStart 是本步工具在累计 ToolCalls 中的起点；nil 表示旧快照。
	PendingToolCallStart *int                       `json:"pending_tool_call_start,omitempty"`
	Waiting              *interrupt.PersistedSignal `json:"waiting,omitempty"`
}

// ToolSideEffect 声明一次工具调用在「崩溃后重放」语义下的安全性。
//
// 用于 Durable 执行的 exactly-once 保护：当一步的工具已发起但崩溃在快照落盘前，
// Resume 需据此判定能否安全重跑——只读/幂等可重放，否则 fail-closed。
type ToolSideEffect int

const (
	// SideEffectUnsafe 默认：重放可能重复副作用（发邮件 / 扣款 / 写库等）。最保守默认，
	// 未显式声明的工具一律按此处理。
	SideEffectUnsafe ToolSideEffect = iota
	// SideEffectIdempotent 幂等：重放产生相同结果、不产生额外副作用。
	SideEffectIdempotent
	// SideEffectReadOnly 只读：无任何副作用。
	SideEffectReadOnly
)

// ReplaySafe 报告该副作用级别是否可安全重放（幂等或只读）。
func (s ToolSideEffect) ReplaySafe() bool {
	return s == SideEffectIdempotent || s == SideEffectReadOnly
}

// PendingTool 是步内意图快照里一条「已发起、待确认完成」的工具调用及其重放安全性。
//
// 保存完整 Call（含 ID/Name/Arguments），使 Resume 能**精确续跑**——按 ID 跳过已
// 完成的工具（其结果已在快照的 ToolCalls 里），只补跑未完成的，且无需重调 LLM。
type PendingTool struct {
	Call       llm.ToolCall   `json:"call"`
	SideEffect ToolSideEffect `json:"side_effect"`
}

// UnsafeNotDone 报告一组待确认工具中，是否存在「尚未完成且不可安全重放」的工具。
//
// doneIDs 是当前 Pending 所属步内已完成的工具调用 ID 集合，不包含此前轮次。已完成的工具即使是 Unsafe
// 也可安全跳过（副作用已发生且已记录）；只有尚未完成的 Unsafe 工具才需 fail-closed
// （无法确定其副作用是否已发生）。这把 fail-closed 窗口从「整步含任一 Unsafe」收窄到
// 「真正在途的那个 Unsafe」。
func UnsafeNotDone(pending []PendingTool, doneIDs map[string]bool) bool {
	for _, p := range pending {
		if doneIDs[p.Call.ID] {
			continue
		}
		if !p.SideEffect.ReplaySafe() {
			return true
		}
	}
	return false
}

// pendingToolRecordStart 为旧快照从最近工具消息组恢复本步范围，不能用历史同 ID 代替本步结果。
func pendingToolRecordStart(s Snapshot) int {
	if s.PendingToolCallStart != nil && *s.PendingToolCallStart >= 0 && *s.PendingToolCallStart <= len(s.ToolCalls) {
		return *s.PendingToolCallStart
	}
	if s.Step == 0 {
		return 0
	}
	for i := len(s.Messages) - 1; i >= 0; i-- {
		message := s.Messages[i]
		if message.Role != llm.RoleAssistant {
			continue
		}
		if len(message.ToolCalls) != len(s.Pending) {
			return len(s.ToolCalls)
		}
		ids := make(map[string]bool, len(s.Pending))
		for index, pending := range s.Pending {
			ref := message.ToolCalls[index]
			if ref.ID != pending.Call.ID || ref.Name != pending.Call.Name || ref.Arguments != pending.Call.Arguments {
				return len(s.ToolCalls)
			}
			ids[ref.ID] = true
		}
		completed := make(map[string]bool)
		for _, result := range s.Messages[i+1:] {
			if result.Role == llm.RoleTool && ids[result.ToolCallID] {
				completed[result.ToolCallID] = true
			}
		}
		if len(completed) <= len(s.ToolCalls) {
			return len(s.ToolCalls) - len(completed)
		}
		break
	}
	return len(s.ToolCalls)
}

func pendingCompletedToolIDs(s Snapshot) map[string]bool {
	done := make(map[string]bool)
	for index := pendingToolRecordStart(s); index < len(s.ToolCalls); index++ {
		record := &s.ToolCalls[index]
		if record.ID != "" {
			if !record.Result.NeedsReconciliation {
				done[record.ID] = true
			}
		}
	}
	return done
}

// SnapshotState 从运行时 State 抽取当前步的 Snapshot。runID 标识本次执行。
//
// 消息及协议续传字节独立复制，避免与仍在演进的 State 共享可变存储。
func SnapshotState(state *State, runID string) Snapshot {
	if state == nil {
		return Snapshot{RunID: runID}
	}
	snapshot := Snapshot{
		Phase:                 state.phase,
		RunID:                 runID,
		Step:                  state.Turn,
		Messages:              CloneOutputMessages(state.Messages),
		ToolCalls:             append([]ToolCallRecord(nil), state.ToolCalls...),
		Usage:                 state.Usage,
		Final:                 state.Final,
		FinalText:             state.FinalText,
		StopReason:            state.stopReason,
		ModelFinishReason:     state.modelFinishReason,
		MessageRevision:       state.MessageRevision,
		AppliedContextUpdates: cloneUpdateIDs(state.appliedContextUpdates),
		Structured:            append(json.RawMessage(nil), state.structured...),
		Tools:                 cloneRuntimeToolSnapshot(state.toolSnapshot),
		Waiting:               clonePersistedSignal(state.waitingSaved),
	}
	snapshot.UsageReportedCalls = usageCount(state.Attributes, UsageReportedCallsMetadataKey)
	snapshot.UsageMissingCalls = usageCount(state.Attributes, UsageMissingCallsMetadataKey)
	snapshot.UsagePartialCalls = usageCount(state.Attributes, UsagePartialCallsMetadataKey)
	snapshot.UsageKnownTokenLowerBound = usageCount(state.Attributes, UsageKnownTokenLowerBoundMetadataKey)
	if state.outputMessages != nil {
		messages := cloneOutputMessages(state.outputMessages)
		snapshot.OutputMessages = &messages
	}
	return snapshot
}

// RestoreState 用 Snapshot 重建一个可继续执行的 State（resume 入口）。
//
// 返回的 State 从 Snapshot 的步号继续；Attributes 重新初始化为空（provider/model
// 等运行期属性在恢复后的首次调用时重新填充）。
func (s Snapshot) RestoreState() *State {
	state := &State{
		phase:                 s.Phase,
		Turn:                  s.Step,
		Messages:              CloneOutputMessages(s.Messages),
		ToolCalls:             append([]ToolCallRecord(nil), s.ToolCalls...),
		Usage:                 s.Usage,
		Final:                 s.Final,
		FinalText:             s.FinalText,
		stopReason:            s.StopReason,
		modelFinishReason:     s.ModelFinishReason,
		MessageRevision:       s.MessageRevision,
		appliedContextUpdates: cloneUpdateIDs(s.AppliedContextUpdates),
		structured:            append(json.RawMessage(nil), s.Structured...),
		toolSnapshot:          cloneRuntimeToolSnapshot(s.Tools),
		Attributes:            map[string]any{},
		waitingSaved:          clonePersistedSignal(s.Waiting),
	}
	if len(s.Pending) > 0 {
		state.pendingToolStart = pendingToolRecordStart(s)
	}
	if s.OutputMessages != nil {
		state.outputMessages = cloneOutputMessages(*s.OutputMessages)
	}
	if s.UsageReportedCalls > 0 || s.UsageMissingCalls > 0 {
		state.setUsageEvidence(s.UsageReportedCalls, s.UsageMissingCalls)
	}
	state.Attributes[UsagePartialCallsMetadataKey] = s.UsagePartialCalls
	state.Attributes[UsageKnownTokenLowerBoundMetadataKey] = s.UsageKnownTokenLowerBound
	if s.UsagePartialCalls > 0 && s.UsageMissingCalls == 0 {
		state.Attributes[UsageSourceMetadataKey] = "partial"
	}
	return state
}

// DurableExecution 把"让一次长时 Agent 执行可持久化、可恢复"收敛为一个最小接口：
//   - 调用方决定何时/是否持久化（在步边界 Save、在恢复时 Load）；
//   - 接口只规定状态模型（Snapshot）与存储抽象（checkpoint.Checkpointer）的关系，
//     不绑定具体后端（后端由 Checkpointer 实现替换）。
//
// 契约：
//   - Save 前置：snap.RunID 非空，否则返回错误且不写入。
//   - Save 后置：随后对同一 RunID 的 Load 返回该 RunID 上最近一次成功 Save 的快照。
//   - 幂等：以 (RunID, Step) 为持久化键，同一步重复 Save 覆盖该步快照。
//   - Load 后置：命名空间无任何快照时 ok=false、err=nil。
//   - 消息中的 ProviderContinuation 只用于私有恢复，标准消息 JSON 不包含它；自定义
//     实现需显式保存该字段并保持消息位置及调用归属，不能把它写入公开响应或日志。
//
// DurableExecution 面向执行、checkpoint.Checkpointer 面向存储，前者用后者落地，
// 二者职责分离、各自可替换。
type DurableExecution interface {
	// Save 在步边界持久化一个执行快照。
	Save(ctx context.Context, snap Snapshot) error
	// Load 取某次执行最近的快照；不存在时 ok=false。
	Load(ctx context.Context, runID string) (Snapshot, bool, error)
}

// checkpointDurable 是 DurableExecution 的默认实现，委托给框架唯一的 checkpoint.Checkpointer。
type checkpointDurable struct {
	cp checkpoint.Checkpointer
}

// 检查点内部封装显式保存续传内容，公开 Snapshot/Message JSON 仍不暴露该字段。
// 内嵌 Snapshot 保持已有检查点的扁平字段，旧记录可直接读取。
type durableSnapshotEnvelope struct {
	Snapshot
	MessageContinuations []durableMessageContinuation `json:"message_continuations,omitempty"`
	OutputContinuations  []durableMessageContinuation `json:"output_continuations,omitempty"`
}

type durableMessageContinuation struct {
	Index        int                       `json:"index"`
	Role         llm.Role                  `json:"role"`
	ToolCallID   string                    `json:"tool_call_id,omitempty"`
	ToolCallIDs  []string                  `json:"tool_call_ids,omitempty"`
	Continuation *llm.ProviderContinuation `json:"continuation"`
}

func snapshotContinuations(messages []llm.Message) []durableMessageContinuation {
	var continuations []durableMessageContinuation
	for i, message := range messages {
		if message.ProviderContinuation == nil {
			continue
		}
		entry := durableMessageContinuation{
			Index:        i,
			Role:         message.Role,
			ToolCallID:   message.ToolCallID,
			Continuation: message.ProviderContinuation.Clone(),
		}
		for _, call := range message.ToolCalls {
			entry.ToolCallIDs = append(entry.ToolCallIDs, call.ID)
		}
		continuations = append(continuations, entry)
	}
	return continuations
}

func restoreContinuations(messages []llm.Message, continuations []durableMessageContinuation) error {
	seen := make(map[int]bool, len(continuations))
	for _, entry := range continuations {
		if entry.Index < 0 || entry.Index >= len(messages) || seen[entry.Index] || entry.Continuation == nil {
			return fmt.Errorf("runtime: invalid checkpoint continuation position")
		}
		message := &messages[entry.Index]
		if message.Role != llm.RoleAssistant || message.Role != entry.Role || message.ToolCallID != entry.ToolCallID || len(message.ToolCalls) != len(entry.ToolCallIDs) {
			return fmt.Errorf("runtime: checkpoint continuation message identity differs")
		}
		for i, call := range message.ToolCalls {
			if call.ID != entry.ToolCallIDs[i] {
				return fmt.Errorf("runtime: checkpoint continuation tool identity differs")
			}
		}
		seen[entry.Index] = true
		message.ProviderContinuation = entry.Continuation.Clone()
	}
	return nil
}

// NewDurableExecution 用给定的 Checkpointer 构造默认 DurableExecution。
//
// cp 为 nil 时 panic（属调用方编程错误：没有存储后端无法构造持久化执行）。
func NewDurableExecution(cp checkpoint.Checkpointer) DurableExecution {
	if cp == nil {
		panic("runtime: NewDurableExecution requires a non-nil Checkpointer")
	}
	return checkpointDurable{cp: cp}
}

// snapshotID 把步号映射为检查点 ID。
func snapshotID(step int) string { return fmt.Sprintf("step-%d", step) }

func (d checkpointDurable) Save(ctx context.Context, snap Snapshot) error {
	if snap.RunID == "" {
		return fmt.Errorf("runtime: DurableExecution.Save requires a non-empty RunID")
	}
	data, err := EncodeSnapshot(snap)
	if err != nil {
		return err
	}
	return d.cp.Put(ctx, checkpoint.Checkpoint{
		Namespace: snap.RunID,
		ID:        snapshotID(snap.Step),
		Metadata:  map[string]string{"step": snapshotID(snap.Step)},
		Payload:   data,
	})
}

func (d checkpointDurable) Load(ctx context.Context, runID string) (Snapshot, bool, error) {
	stored, ok, err := d.cp.Latest(ctx, runID)
	if err != nil || !ok {
		return Snapshot{}, ok, err
	}
	snapshot, err := DecodeSnapshot(stored.Payload)
	if err != nil {
		return Snapshot{}, false, err
	}
	if snapshot.RunID != runID {
		return Snapshot{}, false, fmt.Errorf("runtime: checkpoint run identity mismatch")
	}
	return snapshot, true, nil
}

// EncodeSnapshot 是检查点与事务适配共同使用的私有状态编码，显式保留 continuation。
func EncodeSnapshot(snap Snapshot) ([]byte, error) {
	if snap.RunID == "" {
		return nil, fmt.Errorf("runtime: snapshot run ID is required")
	}
	if snap.Spec != nil {
		if err := snap.Spec.Validate(); err != nil {
			return nil, err
		}
		if snap.Spec.RunID != snap.RunID {
			return nil, ErrUnrecoverableSpec
		}
	}
	envelope := durableSnapshotEnvelope{Snapshot: snap, MessageContinuations: snapshotContinuations(snap.Messages)}
	if snap.OutputMessages != nil {
		envelope.OutputContinuations = snapshotContinuations(*snap.OutputMessages)
	}
	return json.Marshal(envelope)
}

// DecodeSnapshot 只读取快照，不把损坏或不支持的格式当作首次运行。
func DecodeSnapshot(data []byte) (Snapshot, error) {
	if !json.Valid(data) {
		return Snapshot{}, fmt.Errorf("runtime: invalid snapshot JSON")
	}
	var envelope durableSnapshotEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return Snapshot{}, err
	}
	if envelope.RunID == "" {
		return Snapshot{}, fmt.Errorf("runtime: snapshot run ID is missing")
	}
	switch envelope.Phase {
	case "", PhaseBeforeModel, PhasePendingTools, PhaseAfterTurn, PhaseFinal:
	default:
		return Snapshot{}, fmt.Errorf("%w: unknown execution phase", ErrUnrecoverableSpec)
	}
	if envelope.Codec != (StateCodec{}) && envelope.Codec != (StateCodec{Type: "runtime.Snapshot", Version: SnapshotCodecVersion}) {
		return Snapshot{}, ErrUnrecoverableSpec
	}
	if envelope.Spec != nil {
		if err := envelope.Spec.Validate(); err != nil {
			return Snapshot{}, err
		}
		if envelope.Spec.RunID != envelope.RunID {
			return Snapshot{}, ErrUnrecoverableSpec
		}
	}
	if err := restoreContinuations(envelope.Messages, envelope.MessageContinuations); err != nil {
		return Snapshot{}, err
	}
	if envelope.OutputMessages == nil && len(envelope.OutputContinuations) != 0 {
		return Snapshot{}, fmt.Errorf("runtime: checkpoint continuation output is missing")
	}
	if envelope.OutputMessages != nil {
		if err := restoreContinuations(*envelope.OutputMessages, envelope.OutputContinuations); err != nil {
			return Snapshot{}, err
		}
	}
	return envelope.Snapshot, nil
}
