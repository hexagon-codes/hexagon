package runtime

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/toolkit/util/lease"
)

// TestDoCtx_NoLeaseBehavesLikeDo 验证未注入租约时 DoCtx 行为等同 Do（串行 + 幂等）。
func TestDoCtx_NoLeaseBehavesLikeDo(t *testing.T) {
	l := NewSessionLane()
	calls := 0
	fn := func() (*Result, error) { calls++; return &Result{}, nil }

	_, _ = l.DoCtx(context.Background(), "s", "req-1", fn)
	_, _ = l.DoCtx(context.Background(), "s", "req-1", fn) // 同 requestID 幂等
	if calls != 1 {
		t.Errorf("同 requestID 应只执行一次, got %d", calls)
	}
}

// TestDoCtx_DistributedSerialization 验证注入分布式租约后，同 session 跨"持有者"串行（无重叠）。
func TestDoCtx_DistributedSerialization(t *testing.T) {
	l := NewSessionLane()
	l.SetDistributedLease(lease.NewMemoryLease(), time.Minute)

	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	fn := func() (*Result, error) {
		c := concurrent.Add(1)
		for {
			m := maxConcurrent.Load()
			if c <= m || maxConcurrent.CompareAndSwap(m, c) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		concurrent.Add(-1)
		return &Result{}, nil
	}

	var wg sync.WaitGroup
	for range 8 {
		// 不同 requestID（避免幂等短路），同 sessionKey（应串行）
		wg.Go(func() {
			_, _ = l.DoCtx(context.Background(), "session-X", "", fn)
		})
	}
	wg.Wait()

	if maxConcurrent.Load() != 1 {
		t.Errorf("同 session 应串行，最大并发应为 1, got %d", maxConcurrent.Load())
	}
}

// TestAcquireSession_FencingTokenMonotonic 验证 AcquireSession 返回单调 fencing token，释放后续号递增。
func TestAcquireSession_FencingTokenMonotonic(t *testing.T) {
	l := NewSessionLane()
	l.SetDistributedLease(lease.NewMemoryLease(), time.Minute)
	ctx := context.Background()

	tok1, release1, err := l.AcquireSession(ctx, "s")
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}
	release1()
	tok2, release2, err := l.AcquireSession(ctx, "s")
	if err != nil {
		t.Fatalf("AcquireSession: %v", err)
	}
	defer release2()

	if tok2 <= tok1 {
		t.Errorf("fencing token 应单调递增: tok1=%d tok2=%d", tok1, tok2)
	}
}

// TestAcquireSession_NoLeaseNoop 验证未注入租约时 AcquireSession 返回空操作释放函数、不阻塞。
func TestAcquireSession_NoLeaseNoop(t *testing.T) {
	l := NewSessionLane()
	tok, release, err := l.AcquireSession(context.Background(), "s")
	if err != nil {
		t.Fatalf("无租约不应报错: %v", err)
	}
	if tok != 0 {
		t.Errorf("无租约 token 应为 0, got %d", tok)
	}
	release() // 不应 panic
}
