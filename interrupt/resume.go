package interrupt

import (
	"context"
	"sync"
)

// ============== 全局恢复信息 ==============

type globalResumeInfoKey struct{}

// globalResumeInfo 全局恢复信息，存储在 context 中
//
// 在恢复执行前，通过 PopulateResumeInfo 将持久化的中断信息注入 context。
// 随后通过 Resume/ResumeWithData 标记要恢复的中断点。
// 执行过程中，AppendAddressSegment 会自动匹配并注入对应的状态/数据。
type globalResumeInfo struct {
	mu            sync.Mutex
	id2Addr       map[string]Address // interruptID -> 地址
	id2State      map[string]any     // interruptID -> 中断状态
	id2ResumeData map[string]any     // interruptID -> 恢复数据
	id2StateUsed  map[string]bool    // 防止状态被重复消费
	id2DataUsed   map[string]bool    // 防止数据被重复消费
}

// getGlobalResumeInfo 从 context 获取全局恢复信息
func getGlobalResumeInfo(ctx context.Context) *globalResumeInfo {
	if gri, ok := ctx.Value(globalResumeInfoKey{}).(*globalResumeInfo); ok {
		return gri
	}
	return nil
}

// setGlobalResumeInfo 设置全局恢复信息到 context
func setGlobalResumeInfo(ctx context.Context, gri *globalResumeInfo) context.Context {
	return context.WithValue(ctx, globalResumeInfoKey{}, gri)
}

// ============== 恢复函数 ==============

// Resume 标记中断点为已恢复（不携带数据）
//
// 对指定的中断 ID，将恢复数据设为 nil 并标记为恢复目标。
// 执行时 AppendAddressSegment 会检测到恢复标记，
// 使 GetResumeContext 返回 isResumeTarget=true。
func Resume(ctx context.Context, interruptIDs ...string) context.Context {
	gri := getGlobalResumeInfo(ctx)
	if gri == nil {
		gri = &globalResumeInfo{
			id2Addr:       make(map[string]Address),
			id2State:      make(map[string]any),
			id2ResumeData: make(map[string]any),
			id2StateUsed:  make(map[string]bool),
			id2DataUsed:   make(map[string]bool),
		}
		ctx = setGlobalResumeInfo(ctx, gri)
	}

	gri.mu.Lock()
	defer gri.mu.Unlock()

	for _, id := range interruptIDs {
		// 使用空 struct 作为标记，区分 "无数据" 和 "未设置"
		gri.id2ResumeData[id] = nil
	}

	return ctx
}

// ResumeWithData 恢复单个中断点并携带数据
//
// data 会通过 GetResumeContext[T] 传递给中断组件。
// 组件可以利用此数据执行不同的恢复逻辑。
func ResumeWithData(ctx context.Context, interruptID string, data any) context.Context {
	gri := getGlobalResumeInfo(ctx)
	if gri == nil {
		gri = &globalResumeInfo{
			id2Addr:       make(map[string]Address),
			id2State:      make(map[string]any),
			id2ResumeData: make(map[string]any),
			id2StateUsed:  make(map[string]bool),
			id2DataUsed:   make(map[string]bool),
		}
		ctx = setGlobalResumeInfo(ctx, gri)
	}

	gri.mu.Lock()
	defer gri.mu.Unlock()

	gri.id2ResumeData[interruptID] = data

	return ctx
}

// BatchResumeWithData 批量恢复多个中断点
//
// 适用于 CompositeInterrupt 场景：多个子中断需要同时恢复。
// resumeData: map[interruptID]data
func BatchResumeWithData(ctx context.Context, resumeData map[string]any) context.Context {
	gri := getGlobalResumeInfo(ctx)
	if gri == nil {
		gri = &globalResumeInfo{
			id2Addr:       make(map[string]Address),
			id2State:      make(map[string]any),
			id2ResumeData: make(map[string]any),
			id2StateUsed:  make(map[string]bool),
			id2DataUsed:   make(map[string]bool),
		}
		ctx = setGlobalResumeInfo(ctx, gri)
	}

	gri.mu.Lock()
	defer gri.mu.Unlock()

	for id, data := range resumeData {
		gri.id2ResumeData[id] = data
	}

	return ctx
}

// IsResumeRequested 查询原始中断身份是否被本次定向恢复选中，不消费恢复数据。
func IsResumeRequested(ctx context.Context, interruptID string) bool {
	gri := getGlobalResumeInfo(ctx)
	if gri == nil {
		return false
	}
	gri.mu.Lock()
	defer gri.mu.Unlock()
	_, ok := gri.id2ResumeData[interruptID]
	return ok
}

// ResumeTargetID 返回当前地址精确命中的目标，包括无载荷的恢复。
// 仅为后代目标充当祖先的上下文返回空；命中不等于持久消费，任务采用后才能消费。
func ResumeTargetID(ctx context.Context) (string, bool) {
	address := getAddrCtx(ctx)
	if address == nil || address.resumeTargetID == "" {
		return "", false
	}
	return address.resumeTargetID, true
}

// ============== 泛型状态访问 ==============

// GetInterruptState 获取中断时保存的组件状态
//
// 用于 StatefulInterrupt 场景：组件在中断前保存了内部状态（如处理进度），
// 恢复时通过此函数获取，跳过已完成的工作。
//
// 返回值：
//   - wasInterrupted: 当前组件是否曾参与中断（地址匹配到了中断记录）
//   - hasState: 是否有保存的状态且类型匹配
//   - state: 类型安全的状态对象
func GetInterruptState[T any](ctx context.Context) (wasInterrupted bool, hasState bool, state T) {
	var zero T
	ac := getAddrCtx(ctx)
	if ac == nil {
		return false, false, zero
	}

	if !ac.hasInterruptState {
		return false, false, zero
	}

	// 有中断状态，尝试类型断言
	if ac.interruptState == nil {
		return true, false, zero
	}

	typed, ok := ac.interruptState.(T)
	if !ok {
		return true, false, zero
	}

	return true, true, typed
}

// GetResumeContext 获取恢复上下文
//
// 用于组件内部判断当前是否为恢复目标，以及获取恢复时携带的数据。
//
// 返回值：
//   - isResumeTarget: 当前组件（或其后代）是否为恢复目标
//   - hasData: 是否携带了恢复数据且类型匹配
//   - data: 类型安全的恢复数据
func GetResumeContext[T any](ctx context.Context) (isResumeTarget bool, hasData bool, data T) {
	var zero T
	ac := getAddrCtx(ctx)
	if ac == nil {
		return false, false, zero
	}

	if !ac.isResumeTarget {
		return false, false, zero
	}

	if !ac.hasResumeData {
		return true, false, zero
	}

	if ac.resumeData == nil {
		return true, false, zero
	}

	typed, ok := ac.resumeData.(T)
	if !ok {
		return true, false, zero
	}

	return true, true, typed
}
