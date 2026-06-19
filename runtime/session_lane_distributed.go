package runtime

import (
	"context"
	"time"

	"github.com/hexagon-codes/toolkit/util/lease"
)

// 本文件为 SessionLane 补上跨副本串行能力：注入分布式租约（lease.Lease）后，
// 同一 sessionKey 的执行在多副本间也互斥串行，并通过 fencing token 拒绝过期持有者
// 的写入。未注入租约时所有方法行为与单进程版一致。

// SetDistributedLease 注入分布式租约后端与租约 TTL，启用跨副本会话串行。
//
// 传 nil 关闭分布式串行（回到单进程 sync.Mutex）。进程内可用 lease.NewMemoryLease，
// 跨副本用 Redis 等后端实现的 lease.Lease。
func (l *SessionLane) SetDistributedLease(backend lease.Lease, ttl time.Duration) {
	l.mu.Lock()
	l.lease = backend
	l.leaseTTL = ttl
	l.mu.Unlock()
}

// leasePollInterval 是等待他人释放租约时的轮询间隔。
const leasePollInterval = 20 * time.Millisecond

// AcquireSession 获取 sessionKey 的分布式租约，返回 fencing token 与释放函数。
//
// 若未注入分布式租约则返回 token=0、释放函数为空操作（退化为不跨副本串行）。
// 租约被他人持有时轮询等待，直到获取成功或 ctx 取消。fencing token 供下游受保护
// 资源比较、拒绝过期持有者写入。
func (l *SessionLane) AcquireSession(ctx context.Context, sessionKey string) (lease.FencingToken, func(), error) {
	l.mu.Lock()
	backend := l.lease
	ttl := l.leaseTTL
	l.mu.Unlock()

	if backend == nil || sessionKey == "" {
		return 0, func() {}, nil
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}

	for {
		token, acquired, err := backend.Acquire(ctx, sessionKey, ttl)
		if err != nil {
			return 0, func() {}, err
		}
		if acquired {
			release := func() { _ = backend.Release(context.WithoutCancel(ctx), sessionKey, token) }
			return token, release, nil
		}
		// 被他人持有，等待后重试
		select {
		case <-ctx.Done():
			return 0, func() {}, ctx.Err()
		case <-time.After(leasePollInterval):
		}
	}
}

// DoCtx 在跨副本会话串行 + 请求幂等保护下执行 fn。
//
// 与 Do 同义，但当注入了分布式租约时，sessionKey 的串行跨副本生效（先取分布式租约
// 再执行，结束释放）；未注入租约时退化为单进程 sync.Mutex（与 Do 一致）。
// requestID 幂等语义与 Do 相同。
func (l *SessionLane) DoCtx(ctx context.Context, sessionKey, requestID string, fn func() (*Result, error)) (*Result, error) {
	l.mu.Lock()
	hasLease := l.lease != nil
	l.mu.Unlock()

	if hasLease && sessionKey != "" {
		_, release, err := l.AcquireSession(ctx, sessionKey)
		if err != nil {
			return nil, err
		}
		defer release()
	} else if sessionKey != "" {
		sl := l.sessionLock(sessionKey)
		sl.Lock()
		defer sl.Unlock()
	}

	if requestID == "" {
		return fn()
	}
	e := l.entry(requestID)
	e.once.Do(func() { e.result, e.err = fn() })
	return e.result, e.err
}
