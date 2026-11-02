package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/lease"
)

// SessionLane 提供两条执行纪律（A8）：
//   - **同 session 准入串行化**：默认仅限当前进程；注入分布式租约后，DoRun 续租并
//     传递 fencing 身份。拒绝过期写入依赖受保护存储实际校验，不能撤销在途外部动作。
//   - **同 RequestID 幂等**：同一 requestID 的重试不重复执行 fn（返回首次结果），避免重复
//     执行/重复计费。
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
	done   chan struct{}
	result *Result
	err    error
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
//   - requestID 非空 → 幂等（同 ID 重试返回首次结果，不重复执行 fn）；为空 → 每次执行。
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
	return l.doRequest(context.Background(), requestID, fn)
}

// Forget 移除某 requestID 的幂等缓存（释放内存；下次同 ID 将重新执行）。
func (l *SessionLane) Forget(requestID string) {
	l.mu.Lock()
	delete(l.requests, requestID)
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

func (l *SessionLane) doRequest(ctx context.Context, requestID string, fn func() (*Result, error)) (*Result, error) {
	l.mu.Lock()
	if e, ok := l.requests[requestID]; ok {
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-e.done:
			return e.result, e.err
		}
	}
	e := &laneEntry{done: make(chan struct{})}
	l.requests[requestID] = e
	l.mu.Unlock()
	defer close(e.done)
	e.result, e.err = fn()
	return e.result, e.err
}
