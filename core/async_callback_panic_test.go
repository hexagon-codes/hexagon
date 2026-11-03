package core

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvokeWithCallbackPanicDoesNotKillHost(t *testing.T) {
	var called atomic.Bool
	InvokeWithCallback(context.Background(), NewRunnable[int, int]("value", "", func(context.Context, int, ...Option) (int, error) {
		return 1, nil
	}), 1, func(int, error) {
		called.Store(true)
		panic("completion callback failed")
	})
	deadline := time.Now().Add(time.Second)
	for !called.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !called.Load() {
		t.Fatal("callback was not invoked")
	}
	// 给 panic 恢复边界一个调度机会；若未被恢复，测试进程会直接终止。
	time.Sleep(20 * time.Millisecond)
}
