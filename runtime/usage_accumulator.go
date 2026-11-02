package runtime

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// UsageAccumulator 由同一运行及其嵌套调用共享，只累计使用量，不决定预算政策。
// 零值可用；首次记录token时开始计时。实例开始使用后不得复制。
type UsageAccumulator struct {
	tokens        atomic.Int64
	microDollars  atomic.Int64
	costMu        sync.Mutex
	costRemainder float64
	started       atomic.Int64
	startOnce     sync.Once
}

// UsageSnapshot 是一组无共享可变引用的计量读数，不承诺跨字段事务快照。
type UsageSnapshot struct {
	Tokens       int64
	CostMicroUSD int64
	StartedAt    time.Time
}

// RecordTokens 累加已知使用量，首次记录（含零值）开始计时。
func (u *UsageAccumulator) RecordTokens(tokens int) {
	u.startOnce.Do(func() { u.started.Store(time.Now().UnixNano()) })
	u.tokens.Add(int64(tokens))
}

// RecordCost 以微美元精度累计调用方提供的费用，不包含定价或持久化。
func (u *UsageAccumulator) RecordCost(costUSD float64) {
	u.costMu.Lock()
	defer u.costMu.Unlock()
	u.recordCost(costUSD)
}

// recordCost 保留小于记账单位的余量，不因逐笔舍入永久丢失费用。
func (u *UsageAccumulator) recordCost(costUSD float64) {
	exact := costUSD*1_000_000 + u.costRemainder
	whole := math.Round(exact)
	u.costRemainder = exact - whole
	u.microDollars.Add(int64(whole))
}

// Snapshot 返回当前计量；未开始时StartedAt为零值。
func (u *UsageAccumulator) Snapshot() UsageSnapshot {
	s := UsageSnapshot{Tokens: u.tokens.Load(), CostMicroUSD: u.microDollars.Load()}
	if start := u.started.Load(); start != 0 {
		s.StartedAt = time.Unix(0, start)
	}
	return s
}

// CheckLimits 比较调用方显式提供的共享预算，不设置默认值或主动取消。
// 非正限额禁用相应维度；token／成本达到限额即耗尽，时长超过限额时耗尽。
func (u *UsageAccumulator) CheckLimits(maxTokens int64, maxDuration time.Duration, maxCostUSD float64) error {
	usage := u.Snapshot()
	if maxTokens > 0 && usage.Tokens >= maxTokens {
		return fmt.Errorf("token budget exhausted: %d/%d", usage.Tokens, maxTokens)
	}
	if maxDuration > 0 && !usage.StartedAt.IsZero() && time.Since(usage.StartedAt) > maxDuration {
		return fmt.Errorf("duration budget exhausted: %v/%v", time.Since(usage.StartedAt).Round(time.Second), maxDuration)
	}
	maxCostMicroUSD := math.Ceil(maxCostUSD * 1_000_000)
	if maxCostUSD > 0 && float64(usage.CostMicroUSD) >= maxCostMicroUSD {
		return fmt.Errorf("cost budget exhausted: $%.4f/$%.2f", float64(usage.CostMicroUSD)/1_000_000, maxCostUSD)
	}
	return nil
}

// RefundUnused 归还调用方计算的未用额度，原子限定累计值不小于零，不改变计时。
// 本方法不建立分配关系；分配／归还政策由持有者负责。
func (u *UsageAccumulator) RefundUnused(tokens int64, costUSD float64) {
	refundUsage(&u.tokens, tokens)
	if costUSD > 0 {
		u.costMu.Lock()
		defer u.costMu.Unlock()
		u.recordCost(-costUSD)
		if value := u.microDollars.Load(); value < 0 || (value == 0 && u.costRemainder < 0) {
			u.microDollars.Store(0)
			u.costRemainder = 0
		}
	}
}

func refundUsage(counter *atomic.Int64, amount int64) {
	if amount <= 0 {
		return
	}
	for {
		current := counter.Load()
		next := current - min(current, amount)
		if counter.CompareAndSwap(current, next) {
			return
		}
	}
}
