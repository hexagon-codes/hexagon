package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/lease"
)

// 本文件为 SessionLane 补上跨副本串行能力：注入分布式租约（lease.Lease）后，
// DoRun 续租并传播失租取消，fencing token 由下游受保护存储实际核对。
// 进程内请求缓存不提供跨副本幂等，也不能撤销已发出的外部动作。

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
// 此低阶入口不自动续租；持续执行使用 DoRun 获取续租及失租取消。
func (l *SessionLane) AcquireSession(ctx context.Context, sessionKey string) (lease.FencingToken, func(), error) {
	l.mu.Lock()
	backend := l.lease
	ttl := l.leaseTTL
	l.mu.Unlock()
	return acquireSession(ctx, sessionKey, backend, ttl)
}

func acquireSession(ctx context.Context, sessionKey string, backend lease.Lease, ttl time.Duration) (lease.FencingToken, func(), error) {

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
			var once sync.Once
			release := func() {
				once.Do(func() {
					cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cancel()
					_ = backend.Release(cleanup, sessionKey, token)
				})
			}
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
// 等待准入可取消；分布式租约按 DoRun 续租并在失租后返回错误。
// 无参 fn 不能接收派生 ctx，需要主动停止运行时改用 DoRun；请求缓存仅限当前进程。
func (l *SessionLane) DoCtx(ctx context.Context, sessionKey, requestID string, fn func() (*Result, error)) (*Result, error) {
	return l.DoRun(ctx, sessionKey, requestID, func(context.Context) (*Result, error) { return fn() })
}

type sessionFenceKey struct{}

// SessionFence 是会话资源的 fencing 身份，不替代 TaskStore 授予的 Run 执行权。
type SessionFence struct {
	SessionKey string
	Token      lease.FencingToken
}

func SessionFenceFromContext(ctx context.Context) (SessionFence, bool) {
	fence, ok := ctx.Value(sessionFenceKey{}).(SessionFence)
	return fence, ok
}

// DoRun 等待准入可取消，并将租约代次及失租取消传给实际执行回调。
// 旧 DoCtx 的无参回调无法接收派生 ctx；需要主动停止运行时使用此方法。
func (l *SessionLane) DoRun(ctx context.Context, sessionKey, requestID string, fn func(context.Context) (*Result, error)) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sessionKey != "" {
		gate := l.sessionLock(sessionKey)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case gate <- struct{}{}:
		}
		defer func() { <-gate }()
	}
	l.mu.Lock()
	backend, ttl := l.lease, l.leaseTTL
	l.mu.Unlock()
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	execution, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if backend != nil && sessionKey != "" {
		token, release, err := acquireSession(ctx, sessionKey, backend, ttl)
		if err != nil {
			return nil, err
		}
		defer release()
		execution = context.WithValue(execution, sessionFenceKey{}, SessionFence{SessionKey: sessionKey, Token: token})
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			interval := ttl / 3
			if interval <= 0 {
				interval = time.Nanosecond
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-execution.Done():
					return
				case <-ticker.C:
					if err := backend.Refresh(execution, sessionKey, token, ttl); err != nil {
						cancel(errors.Join(ErrExecutionLost, err))
						return
					}
				}
			}
		}()
		defer func() { cancel(nil); <-stopped }()
	}
	run := func() (*Result, error) {
		if err := execution.Err(); err != nil {
			return nil, context.Cause(execution)
		}
		result, err := fn(execution)
		if cause := context.Cause(execution); cause != nil {
			return result, errors.Join(err, cause)
		}
		return result, err
	}
	if requestID == "" {
		return run()
	}
	return l.doRequest(execution, sessionKey, requestID, run)
}
