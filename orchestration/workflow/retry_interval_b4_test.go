package workflow

import (
	"testing"
	"time"
)

// 特征测试 — B-4（2026-06-22 hex-test 审计 / 复用分层）：
// calculateRetryInterval 由手写指数循环改为复用 toolkit/util/retry.ExponentialBackoff。
// 本测试钉死退避序列**逐项不变**，先在重构前跑通（锁定现行为），重构后须仍通过。
func TestCalculateRetryInterval_B4_SequencePreserved(t *testing.T) {
	s := &BaseStep{retryPolicy: &RetryPolicy{
		InitialInterval: time.Second,
		Multiplier:      2,
		MaxInterval:     10 * time.Second,
	}}
	want := []time.Duration{
		1 * time.Second,  // attempt 0：InitialInterval
		2 * time.Second,  // attempt 1
		4 * time.Second,  // attempt 2
		8 * time.Second,  // attempt 3
		10 * time.Second, // attempt 4：16s 封顶到 MaxInterval
		10 * time.Second, // attempt 5：持续封顶
	}
	for attempt, w := range want {
		if got := s.calculateRetryInterval(attempt); got != w {
			t.Errorf("calculateRetryInterval(%d)=%v，期望=%v（须与原手写指数循环逐项一致）", attempt, got, w)
		}
	}

	// nil 策略 → 默认 1s（保留原边界行为）
	var s2 BaseStep
	if got := s2.calculateRetryInterval(0); got != time.Second {
		t.Errorf("nil retryPolicy 应返回 1s，实际=%v", got)
	}
}
