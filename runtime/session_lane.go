package runtime

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/hexagon-codes/hexagon/core"
	"github.com/hexagon-codes/toolkit/util/lease"
	"github.com/hexagon-codes/toolkit/util/reflectx"
)

// SessionLane 提供两条执行纪律（A8）：
//   - **同 session 准入串行化**：默认仅限当前进程；注入分布式租约后，DoRun 续租并
//     传递 fencing 身份。拒绝过期写入依赖受保护存储实际校验，不能撤销在途外部动作。
//   - **同 RequestID 幂等**：同一 sessionKey + requestID 的重试不重复执行 fn
//     （返回首次结果的独立副本），避免重复执行/重复计费；不同 sessionKey 不共享缓存。
//
// 跨副本串行 + fencing token 见 session_lane_distributed.go。
//
// 线程安全：所有方法可并发调用。
type SessionLane struct {
	mu       sync.Mutex
	sessions map[string]chan struct{}
	requests map[string]*laneEntry

	lease    lease.Lease   // 可选：分布式租约后端（nil=单进程串行）
	leaseTTL time.Duration // 分布式租约 TTL
}

type laneEntry struct {
	done      chan struct{}
	requestID string
	result    *Result
	err       error
}

// NewSessionLane 创建会话泳道。
func NewSessionLane() *SessionLane {
	return &SessionLane{
		sessions: make(map[string]chan struct{}),
		requests: make(map[string]*laneEntry),
	}
}

// Do 在会话串行 + 请求幂等保护下执行 fn。
//
//   - sessionKey 非空 → 同 session 串行（互斥）；为空 → 不串行。
//   - requestID 非空 → 幂等（同 sessionKey + ID 重试返回首次结果的独立副本，不重复执行 fn）；为空 → 每次执行。
//
// Do 只用进程内准入门串行，不感知 SetDistributedLease 注入的分布式租约。
// 若配置了分布式租约以求跨副本串行，所有调用方必须统一用 DoCtx——混用 Do 与 DoCtx 会让
// Do 的调用绕过租约、与 DoCtx 的调用在跨副本下并发。需要跨副本串行时一律走 DoCtx。
func (l *SessionLane) Do(sessionKey, requestID string, fn func() (*Result, error)) (*Result, error) {
	if sessionKey != "" {
		sl := l.sessionLock(sessionKey)
		sl <- struct{}{}
		defer func() { <-sl }()
	}
	if requestID == "" {
		return fn()
	}
	return l.doRequest(context.Background(), sessionKey, requestID, fn)
}

// Forget 移除某 requestID 的幂等缓存（释放内存；下次同 ID 将重新执行）。
// 缓存键包含 session 作用域，因此按 requestID 清理所有匹配作用域。
func (l *SessionLane) Forget(requestID string) {
	l.mu.Lock()
	for key, entry := range l.requests {
		if entry.requestID == requestID {
			delete(l.requests, key)
		}
	}
	l.mu.Unlock()
}

func (l *SessionLane) sessionLock(key string) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if m, ok := l.sessions[key]; ok {
		return m
	}
	m := make(chan struct{}, 1)
	l.sessions[key] = m
	return m
}

func laneRequestKey(sessionKey, requestID string) string {
	return sessionKey + "\x00" + requestID
}

func (l *SessionLane) doRequest(ctx context.Context, sessionKey, requestID string, fn func() (*Result, error)) (out *Result, outErr error) {
	key := laneRequestKey(sessionKey, requestID)
	l.mu.Lock()
	if e, ok := l.requests[key]; ok {
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-e.done:
			cached, cacheErr := cloneLaneResult(e.result)
			if cacheErr != nil {
				return nil, cacheErr
			}
			return cached, e.err
		}
	}
	e := &laneEntry{done: make(chan struct{}), requestID: requestID}
	l.requests[key] = e
	l.mu.Unlock()
	defer func() {
		if value := recover(); value != nil {
			e.result, e.err = nil, &core.AsyncPanicError{Value: value, Stack: debug.Stack()}
		}
		// 缓存和返回值都使用独立副本；调用者修改返回值不能污染幂等事实。
		cached, cacheErr := cloneLaneResult(e.result)
		if cacheErr != nil {
			e.result, e.err = nil, errors.Join(e.err, cacheErr)
		} else {
			e.result = cached
		}
		close(e.done)
		cloned, cloneErr := cloneLaneResult(e.result)
		if cloneErr != nil {
			out, outErr = nil, cloneErr
		} else {
			out, outErr = cloned, e.err
		}
	}()
	e.result, e.err = fn()
	return e.result, e.err
}

// cloneLaneResult 复制会话泳道结果的公开与内部可变字段。
// 协议/工具结果按现有 runtime 快照辅助复制；无法隔离时显式返回错误。
func cloneLaneResult(result *Result) (*Result, error) {
	if result == nil {
		return nil, nil
	}
	copy := *result
	copy.Metadata = cloneRuntimeMetadata(result.Metadata)
	copy.Structured = append([]byte(nil), result.Structured...)
	copy.OutputMessages = CloneOutputMessages(result.OutputMessages)
	copy.ToolCalls = append([]ToolCallRecord(nil), result.ToolCalls...)
	for index := range copy.ToolCalls {
		copy.ToolCalls[index].Result = cloneToolResult(copy.ToolCalls[index].Result)
	}
	if result.Blocks != nil {
		blocks, err := reflectx.DeepCopyChecked(result.Blocks)
		if err != nil {
			return nil, fmt.Errorf("snapshot session lane blocks: %w", err)
		}
		copy.Blocks = blocks
	}
	return &copy, nil
}
