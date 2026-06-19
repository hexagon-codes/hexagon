package runtime

import (
	"context"
	"fmt"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/checkpoint"
)

// Snapshot 是一次执行在某个步边界上的持久化状态。
//
// 它只包含恢复一次 Agent 执行所必需的最小状态：步号、消息历史、已完成工具调用记录、
// 累计用量与终止信息。Reasoning、Attributes 等运行期派生/易变数据不入快照，以免把
// 易变细节固化进持久化格式、并降低与具体执行实现的耦合。
type Snapshot struct {
	// RunID 标识一次执行，同时作为持久化命名空间。
	RunID string `json:"run_id"`
	// Step 快照所在的步边界（对应 State.Turn）。
	Step int `json:"step"`
	// Messages 截至本步的消息历史。
	Messages []llm.Message `json:"messages"`
	// ToolCalls 截至本步已完成的工具调用记录。
	ToolCalls []ToolCallRecord `json:"tool_calls,omitempty"`
	// Usage 截至本步的累计 token 用量（计量维度）。
	Usage llm.Usage `json:"usage"`
	// Final 标记执行是否已得出最终答案。
	Final bool `json:"final,omitempty"`
	// FinalText 最终答案文本（Final 为真时有效）。
	FinalText string `json:"final_text,omitempty"`
	// Pending 非空表示这是一个「步内意图快照」：该步的工具调用已发起但尚未确认全部
	// 完成（崩溃窗口）。它记录这些工具的重放安全性，供 Resume 判定能否安全续跑——
	// 含 Unsafe 工具时 Resume fail-closed（ErrUnsafeReplay），全部可重放安全时重跑该步。
	// 步正常完成后的完成快照不带 Pending（同步号覆盖意图快照）。
	Pending []PendingTool `json:"pending,omitempty"`
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
// doneIDs 是快照里已完成（结果已记录）的工具调用 ID 集合。已完成的工具即使是 Unsafe
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

// SnapshotState 从运行时 State 抽取当前步的 Snapshot。runID 标识本次执行。
//
// 切片做浅拷贝快照，避免与仍在演进的 State 共享底层数组。
func SnapshotState(state *State, runID string) Snapshot {
	if state == nil {
		return Snapshot{RunID: runID}
	}
	return Snapshot{
		RunID:     runID,
		Step:      state.Turn,
		Messages:  append([]llm.Message(nil), state.Messages...),
		ToolCalls: append([]ToolCallRecord(nil), state.ToolCalls...),
		Usage:     state.Usage,
		Final:     state.Final,
		FinalText: state.FinalText,
	}
}

// RestoreState 用 Snapshot 重建一个可继续执行的 State（resume 入口）。
//
// 返回的 State 从 Snapshot 的步号继续；Attributes 重新初始化为空（provider/model
// 等运行期属性在恢复后的首次调用时重新填充）。
func (s Snapshot) RestoreState() *State {
	return &State{
		Turn:       s.Step,
		Messages:   append([]llm.Message(nil), s.Messages...),
		ToolCalls:  append([]ToolCallRecord(nil), s.ToolCalls...),
		Usage:      s.Usage,
		Final:      s.Final,
		FinalText:  s.FinalText,
		Attributes: map[string]any{},
	}
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
	return checkpoint.PutValue(ctx, d.cp, checkpoint.Checkpoint{
		Namespace: snap.RunID,
		ID:        snapshotID(snap.Step),
		Metadata:  map[string]string{"step": snapshotID(snap.Step)},
	}, snap)
}

func (d checkpointDurable) Load(ctx context.Context, runID string) (Snapshot, bool, error) {
	snap, _, ok, err := checkpoint.LatestValue[Snapshot](ctx, d.cp, runID)
	return snap, ok, err
}
