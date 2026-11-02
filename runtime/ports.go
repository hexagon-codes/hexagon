package runtime

import (
	"context"
	"encoding/json"

	"github.com/hexagon-codes/ai-core/llm"
)

// Provider is the LLM port used by the runtime.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error)
	Stream(ctx context.Context, req llm.CompletionRequest) (*llm.Stream, error)
}

// ProviderSelection describes the selected model backend.
type ProviderSelection struct {
	Provider Provider
	Name     string
	Model    string
}

// ProviderSelector selects the primary provider and optional fallback.
type ProviderSelector interface {
	Select(ctx context.Context, req Request) (ProviderSelection, error)
	Fallback(ctx context.Context, failed ProviderSelection, err error) (ProviderSelection, error)
}

// StaticProviderSelector is a single-provider selector.
type StaticProviderSelector struct {
	Provider Provider
	Name     string
	Model    string
}

// Select returns the configured provider.
func (s StaticProviderSelector) Select(context.Context, Request) (ProviderSelection, error) {
	name := s.Name
	if name == "" && s.Provider != nil {
		name = s.Provider.Name()
	}
	return ProviderSelection{Provider: s.Provider, Name: name, Model: s.Model}, nil
}

// Fallback returns no fallback.
func (s StaticProviderSelector) Fallback(context.Context, ProviderSelection, error) (ProviderSelection, error) {
	return ProviderSelection{}, ErrNoFallback
}

// ToolExecutor executes tool calls.
type ToolExecutor interface {
	Execute(ctx context.Context, call llm.ToolCall) (ToolResult, error)
}

// SideEffectClassifier 是 ToolExecutor 的可选附加能力：声明某次工具调用在「崩溃后
// 重放」语义下的安全性（只读 / 幂等 / 不安全）。
//
// 仅用于 Durable 执行的 exactly-once 保护：执行器若实现本接口，Runner 在工具执行前
// 的意图快照里记录各工具的重放安全性，Resume 据此判定能否安全续跑。**未实现时所有
// 工具按最保守的 SideEffectUnsafe 处理**——即崩溃在步内时 Resume 会 fail-closed，
// 宁可显式失败也不静默重复副作用。
type SideEffectClassifier interface {
	SideEffectOf(call llm.ToolCall) ToolSideEffect
}

// ToolStatus 是工具单次调用的执行结果状态——随 ToolResult 一等返回，客户端据此渲染
// （成功 / 失败），无需对结果正文做字符串嗅探。对齐 StopReason 的「执行真相一等化」范式：
// 框架在执行点拥有完整上下文，应直接产出状态，而非让上层反查。仅表达**已完成**的两态
// （执行中是流式概念，不属批量结果）。
type ToolStatus string

// 工具执行结果状态常量：成功 / 失败两态。
const (
	// ToolStatusSuccess 表示工具执行成功（无 Go 级 execErr 且 tool.Result.Success 为真）。
	ToolStatusSuccess ToolStatus = "success"
	// ToolStatusError 表示工具执行失败（Go 级 execErr，或 tool.Result.Success 为假的软失败）。
	ToolStatusError ToolStatus = "error"
)

// ToolResult is a product-neutral tool result.
type StructuredPayload struct {
	Kind    string          `json:"kind"`
	Version int             `json:"version"`
	Data    json.RawMessage `json:"data"`
}

type ToolResult struct {
	Content string
	// ContentParts 保存可持久化的完整多模态投影，不依赖 Raw 恢复后仍实现 Go 接口。
	ContentParts []llm.ContentPart `json:"content_parts,omitempty"`
	// ToolSnapshot 让已完成的发现任务在状态采用前中断后仍能重建相同执行绑定。
	ToolSnapshot *ToolSnapshot      `json:"tool_snapshot,omitempty"`
	Structured   *StructuredPayload `json:"structured,omitempty"`
	// NeedsReconciliation 不把已发生但尚需补登记的动作当成可重新执行的失败。
	NeedsReconciliation bool `json:"needs_reconciliation,omitempty"`
	Raw                 any
	Error               string
	// Stop 表示记录本次结果后停止本轮运行，Content作为终稿，不派发剩余工具。
	// 由执行器按自身政策设置；零值保持普通工具结果行为。
	Stop bool `json:"stop,omitempty"`
	// Status 是执行结果状态（success / error），由框架在执行点据 execErr 与
	// tool.Result.Success 判定。零值（空串）表示未填充——老快照/老路径向后兼容。
	Status ToolStatus `json:"status,omitempty"`
	// DurationMs 是工具执行耗时（毫秒），由框架在执行点 time.Since 测量。
	DurationMs int64 `json:"duration_ms,omitempty"`
}

// ToolResultReconciler 仅核对原结果或幂等补登记，不得重新调用原业务执行器。
// handled=false 表示该结果不属于此核对器；查询失败保留原事实，不能转为成功文本。
type ToolResultReconciler interface {
	ReconcileToolResult(context.Context, llm.ToolCall, ToolResult) (ToolResult, bool, error)
}

// Permission checks whether a tool call can execute.
type Permission interface {
	CheckTool(ctx context.Context, call llm.ToolCall) error
}

// Memory is the optional long-term memory port.
type Memory interface {
	Search(ctx context.Context, query string, limit int) ([]MemoryEntry, error)
	Save(ctx context.Context, entry MemoryEntry) error
}

// MemoryEntry is a product-neutral memory record.
type MemoryEntry struct {
	Role    string
	Content string
}
