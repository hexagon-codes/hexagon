package interrupt

import (
	"context"
	"strings"
)

// ============== 地址段类型 ==============

// AddressSegmentType 地址段类型，用于区分不同层级的组件
type AddressSegmentType string

const (
	// SegmentNode 图节点
	SegmentNode AddressSegmentType = "node"

	// SegmentTool 工具调用
	SegmentTool AddressSegmentType = "tool"

	// SegmentSubgraph 子图
	SegmentSubgraph AddressSegmentType = "subgraph"

	// SegmentAgent Agent
	SegmentAgent AddressSegmentType = "agent"
)

// ============== 地址段 ==============

// AddressSegment 地址中的一个段，代表层级结构中的一级
//
// 每个段包含：
//   - Type: 段类型（node/tool/subgraph/agent）
//   - ID: 主标识符（节点名、工具名等）
//   - SubID: 辅助标识符（区分同名组件的不同调用实例，如并行工具调用）
type AddressSegment struct {
	Type  AddressSegmentType
	ID    string
	SubID string
}

// String 返回段的字符串表示
// 格式: "type:id" 或 "type:id:subID"（当 SubID 非空时）
func (s AddressSegment) String() string {
	if s.SubID != "" {
		return string(s.Type) + ":" + s.ID + ":" + s.SubID
	}
	return string(s.Type) + ":" + s.ID
}

// Equals 判断两个段是否相等
func (s AddressSegment) Equals(other AddressSegment) bool {
	return s.Type == other.Type && s.ID == other.ID && s.SubID == other.SubID
}

// ============== 层级地址 ==============

// Address 层级地址，由多个段组成
// 例如: [node:step1, tool:search, tool:search:call_1]
// 表示在 step1 节点中，search 工具的 call_1 调用
type Address []AddressSegment

// String 返回地址的字符串表示
// 格式: "node:step1;tool:search:call_1"
func (a Address) String() string {
	if len(a) == 0 {
		return ""
	}
	parts := make([]string, len(a))
	for i, seg := range a {
		parts[i] = seg.String()
	}
	return strings.Join(parts, ";")
}

// Equals 判断两个地址是否完全相等
func (a Address) Equals(other Address) bool {
	if len(a) != len(other) {
		return false
	}
	for i := range a {
		if !a[i].Equals(other[i]) {
			return false
		}
	}
	return true
}

// IsDescendantOf 判断当前地址是否是 ancestor 的后代
// 即 ancestor 是当前地址的前缀
func (a Address) IsDescendantOf(ancestor Address) bool {
	if len(ancestor) >= len(a) {
		return false
	}
	for i := range ancestor {
		if !a[i].Equals(ancestor[i]) {
			return false
		}
	}
	return true
}

// Append 追加一个段，返回新地址（不修改原地址）
func (a Address) Append(seg AddressSegment) Address {
	newAddr := make(Address, len(a)+1)
	copy(newAddr, a)
	newAddr[len(a)] = seg
	return newAddr
}

// ============== Context 操作 ==============

type addressContextKey struct{}

// AppendAddressSegment 在 context 中追加一个地址段
//
// 同时检查 globalResumeInfo：
//   - 如果新地址匹配某个中断点地址 → 注入 InterruptState 到 context
//   - 如果新地址匹配某个恢复目标 → 注入 resumeData 和 isResumeTarget=true
//   - 如果新地址的后代是恢复目标 → 标记 isResumeTarget=true
func AppendAddressSegment(ctx context.Context, segType AddressSegmentType, id, subID string) context.Context {
	seg := AddressSegment{Type: segType, ID: id, SubID: subID}
	current := GetCurrentAddress(ctx)
	newAddr := current.Append(seg)

	ac := &addrCtx{
		addr: newAddr,
	}

	// 检查 globalResumeInfo 并注入中断/恢复状态
	if gri := getGlobalResumeInfo(ctx); gri != nil {
		gri.mu.Lock()
		defer gri.mu.Unlock()

		// 查找当前地址匹配的中断点，注入中断状态
		for iID, addr := range gri.id2Addr {
			if newAddr.Equals(addr) {
				// 精确匹配 → 注入 interruptState
				if state, ok := gri.id2State[iID]; ok && !gri.id2StateUsed[iID] {
					ac.interruptState = state
					ac.hasInterruptState = true
					gri.id2StateUsed[iID] = true
				}
				// 注入 resumeData（如果有）
				if data, ok := gri.id2ResumeData[iID]; ok && !gri.id2DataUsed[iID] {
					ac.resumeData = data
					ac.hasResumeData = true
					ac.isResumeTarget = true
					ac.resumeTargetID = iID
					gri.id2DataUsed[iID] = true
				}
			}

			// 后代匹配 → 标记 isResumeTarget
			if _, hasResume := gri.id2ResumeData[iID]; hasResume && !gri.id2DataUsed[iID] {
				if addr.IsDescendantOf(newAddr) {
					ac.isResumeTarget = true
				}
			}
		}
	}

	return context.WithValue(ctx, addressContextKey{}, ac)
}

// GetCurrentAddress 从 context 获取当前地址
func GetCurrentAddress(ctx context.Context) Address {
	if ac, ok := ctx.Value(addressContextKey{}).(*addrCtx); ok {
		return ac.addr
	}
	return nil
}

// addrCtx 当前地址上下文（存储在 context 中）
type addrCtx struct {
	addr              Address
	interruptState    any    // 从 checkpoint 恢复的中断状态
	hasInterruptState bool   // 是否有中断状态
	isResumeTarget    bool   // 是否为恢复目标
	resumeTargetID    string // 精确地址匹配的恢复身份，不包含仅被选中的祖先
	resumeData        any    // 恢复数据
	hasResumeData     bool   // 是否有恢复数据
}

// getAddrCtx 从 context 获取 addrCtx
func getAddrCtx(ctx context.Context) *addrCtx {
	if ac, ok := ctx.Value(addressContextKey{}).(*addrCtx); ok {
		return ac
	}
	return nil
}
