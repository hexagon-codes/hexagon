package runtime

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestSessionLane_Idempotent 同 requestID 重试只执行一次 fn，返回首次结果。
func TestSessionLane_Idempotent(t *testing.T) {
	lane := NewSessionLane()
	var calls int32
	fn := func() (*Result, error) {
		atomic.AddInt32(&calls, 1)
		return &Result{Content: "ok"}, nil
	}

	r1, _ := lane.Do("sess", "req-1", fn)
	r2, _ := lane.Do("sess", "req-1", fn) // 重试同 ID
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("同 requestID 应只执行一次 fn，实际 %d", calls)
	}
	if r1.Content != "ok" || r2.Content != "ok" {
		t.Fatalf("两次应返回同一结果")
	}
}

// TestSessionLane_DistinctRequestsExecute 不同 requestID 各自执行。
func TestSessionLane_DistinctRequestsExecute(t *testing.T) {
	lane := NewSessionLane()
	var calls int32
	fn := func() (*Result, error) { atomic.AddInt32(&calls, 1); return &Result{}, nil }

	lane.Do("sess", "a", fn)
	lane.Do("sess", "b", fn)
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("不同 requestID 应各执行一次，实际 %d", calls)
	}
}

// TestSessionLane_EmptyRequestIDAlwaysRuns 空 requestID 不幂等，每次执行。
func TestSessionLane_EmptyRequestIDAlwaysRuns(t *testing.T) {
	lane := NewSessionLane()
	var calls int32
	fn := func() (*Result, error) { atomic.AddInt32(&calls, 1); return &Result{}, nil }
	lane.Do("sess", "", fn)
	lane.Do("sess", "", fn)
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("空 requestID 应每次执行，实际 %d", calls)
	}
}

// TestSessionLane_SerializesSameSession 同 session 并发执行串行化（无重叠）。
func TestSessionLane_SerializesSameSession(t *testing.T) {
	lane := NewSessionLane()
	var concurrent, maxConcurrent int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// 同 session、不同 requestID（避免幂等短路），验证串行
			_, _ = lane.Do("sess", "", func() (*Result, error) {
				c := atomic.AddInt32(&concurrent, 1)
				for {
					m := atomic.LoadInt32(&maxConcurrent)
					if c <= m || atomic.CompareAndSwapInt32(&maxConcurrent, m, c) {
						break
					}
				}
				atomic.AddInt32(&concurrent, -1)
				return &Result{}, nil
			})
		}(i)
	}
	wg.Wait()
	if atomic.LoadInt32(&maxConcurrent) != 1 {
		t.Fatalf("同 session 应串行（max concurrent=1），实际 %d", maxConcurrent)
	}
}

// TestSessionLane_Forget 清除幂等缓存后同 ID 重新执行。
func TestSessionLane_Forget(t *testing.T) {
	lane := NewSessionLane()
	var calls int32
	fn := func() (*Result, error) { atomic.AddInt32(&calls, 1); return &Result{}, nil }
	lane.Do("s", "req", fn)
	lane.Forget("req")
	lane.Do("s", "req", fn)
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("Forget 后应重新执行，实际 %d", calls)
	}
}
