package agent

import (
	"github.com/hexagon-codes/ai-core/llm"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

// 重放安全性等级（用于工具声明 ReplaySafety；语义见 runtime.ToolSideEffect）。
//
// 用于 Durable exactly-once：当一个工具步在崩溃后被 Resume 时，框架据此判定能否安全
// 重放——只读 / 幂等可重放，否则 fail-closed 拒绝重放、避免重复副作用。
const (
	// ReplayUnsafe 默认：重放可能重复副作用（发邮件 / 扣款 / 写库等）。
	ReplayUnsafe = agentruntime.SideEffectUnsafe
	// ReplayIdempotent 幂等：重放产生相同结果、无额外副作用。
	ReplayIdempotent = agentruntime.SideEffectIdempotent
	// ReplayReadOnly 只读：无任何副作用。
	ReplayReadOnly = agentruntime.SideEffectReadOnly
)

// ReplaySafetyAware 是工具**可选**实现的接口，用于声明自身在 Durable 崩溃重放下的
// 安全性。未实现的工具一律按最保守的 ReplayUnsafe 处理（Durable 续跑时对其 fail-closed）。
//
// 示例：一个只读的检索工具可声明可安全重放，从而崩溃后自动续跑而非 fail-closed：
//
//	func (t *SearchTool) ReplaySafety() agent.ReplaySafety { return agent.ReplayReadOnly }
type ReplaySafetyAware interface {
	ReplaySafety() ReplaySafety
}

// ReplaySafety 是工具声明的重放安全性等级类型别名（= runtime.ToolSideEffect）。
type ReplaySafety = agentruntime.ToolSideEffect

// SideEffectOf 让 agentToolExecutor 满足 runtime.SideEffectClassifier：按底层工具的
// ReplaySafetyAware 声明返回其重放安全性；工具未声明或未找到时返回最保守的 Unsafe。
//
// 这把"工具是否可安全重放"的判定下放到工具作者（最了解副作用的人），同时保持安全默认。
func (e *agentToolExecutor) SideEffectOf(call llm.ToolCall) agentruntime.ToolSideEffect {
	for _, t := range e.tools {
		if t.Name() != call.Name {
			continue
		}
		if aware, ok := t.(ReplaySafetyAware); ok {
			return aware.ReplaySafety()
		}
		return agentruntime.SideEffectUnsafe
	}
	return agentruntime.SideEffectUnsafe
}

// 编译期断言：*agentToolExecutor 满足 runtime.SideEffectClassifier。
var _ agentruntime.SideEffectClassifier = (*agentToolExecutor)(nil)
