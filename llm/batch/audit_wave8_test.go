package batch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ============== 测试用 Provider 桩 ==============

// stubProvider 是可配置的 Provider 桩实现
//
// 通过 fn 字段完全控制每次 Complete 调用的行为，
// 用于模拟成功、失败、部分失败、可重试错误、慢响应等场景。
type stubProvider struct {
	// fn 自定义补全逻辑，必填
	fn func(ctx context.Context, req *Request) (*Response, error)
	// calls 记录调用次数（原子计数，并发安全）
	calls int64
}

func (p *stubProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	atomic.AddInt64(&p.calls, 1)
	return p.fn(ctx, req)
}

func (p *stubProvider) callCount() int64 { return atomic.LoadInt64(&p.calls) }

// okProvider 返回固定成功响应的 Provider，回显请求 ID 并设置 token 数。
func okProvider(tokens int) *stubProvider {
	return &stubProvider{
		fn: func(_ context.Context, req *Request) (*Response, error) {
			return &Response{
				ID:           req.ID,
				Content:      "ok:" + req.ID,
				TokensUsed:   tokens,
				FinishReason: "stop",
			}, nil
		},
	}
}

// fastConfig 返回适合测试的配置：极短刷新间隔、短超时、零重试延迟，
// 避免测试因默认 100ms 刷新 + 1s 重试延迟而过慢。
func fastConfig() *Config {
	return &Config{
		MaxBatchSize:  4,
		MaxConcurrent: 3,
		QueueSize:     100,
		FlushInterval: 5 * time.Millisecond,
		Timeout:       2 * time.Second,
		MaxRetries:    2,
		RetryDelay:    time.Millisecond,
		RateLimit:     1000,
	}
}

// ============== DefaultConfig 测试 ==============

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig 返回 nil")
	}
	// 验证默认值符合文档/注释承诺
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"MaxBatchSize", cfg.MaxBatchSize, 10},
		{"MaxConcurrent", cfg.MaxConcurrent, 5},
		{"QueueSize", cfg.QueueSize, 1000},
		{"FlushInterval", cfg.FlushInterval, 100 * time.Millisecond},
		{"Timeout", cfg.Timeout, 60 * time.Second},
		{"MaxRetries", cfg.MaxRetries, 3},
		{"RetryDelay", cfg.RetryDelay, time.Second},
		{"RateLimit", cfg.RateLimit, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("默认 %s = %v, 期望 %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

// ============== NewBatcher 测试 ==============

func TestNewBatcher_NilConfigUsesDefault(t *testing.T) {
	b := NewBatcher(okProvider(1))
	if b.config.MaxBatchSize != 10 {
		t.Errorf("nil 配置应使用默认值, MaxBatchSize=%d", b.config.MaxBatchSize)
	}

	// 显式传 nil 配置也应回退默认
	b2 := NewBatcher(okProvider(1), nil)
	if b2.config.QueueSize != 1000 {
		t.Errorf("显式 nil 配置应使用默认值, QueueSize=%d", b2.config.QueueSize)
	}
}

func TestNewBatcher_CustomConfig(t *testing.T) {
	cfg := fastConfig()
	b := NewBatcher(okProvider(1), cfg)
	if b.config != cfg {
		t.Error("自定义配置未被采用")
	}
	if cap(b.queue) != cfg.QueueSize {
		t.Errorf("队列容量 = %d, 期望 %d", cap(b.queue), cfg.QueueSize)
	}
}

// 回归: RateLimit<=0 死循环挂起（W8 bug #2）。
//
// TestSubmit_ZeroRateLimit_Hangs 验证 RateLimit=0 的边界。
//
// 修复前缺陷：NewBatcher 用 rate.NewTokenBucket(0, 0.0) 创建限流器（capacity=0, rate=0）。
// processRequest 中 b.limiter.Wait() 永远拿不到令牌（0 容量 + 0 速率），
// 内部计算 waitTime = (1-0)/0*1000 → +Inf，陷入 time.Sleep(+Inf) 死循环，
// worker 永久卡死、Stop 的 wg.Wait 死锁、goroutine 泄漏。
//
// 修复后契约：RateLimit<=0 视为"不限流"（limiter 置 nil，processRequest 跳过 Wait）。
// 因此 Submit 应在限定时间内成功返回有效响应，且 Stop 不死锁、能正常退出。
//
// 本测试用带超时的 goroutine 同时守护 Submit 与 Stop 两个挂起点，
// 一旦任一处在超时窗口内未完成即判失败。【断言不得弱化】。
func TestSubmit_ZeroRateLimit_Hangs(t *testing.T) {
	cfg := fastConfig()
	cfg.RateLimit = 0 // 边界：0 速率，应被规范化为"不限流"
	b := NewBatcher(cfg2okProvider(), cfg)
	b.Start()

	// 守护点一：Submit 不应挂起，且应返回成功响应。
	type submitResult struct {
		resp *Response
		err  error
	}
	subDone := make(chan submitResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		resp, err := b.Submit(ctx, &Request{ID: "z"})
		subDone <- submitResult{resp, err}
	}()

	select {
	case r := <-subDone:
		if r.err != nil {
			t.Fatalf("RateLimit=0 应视为不限流并成功，却返回错误: %v", r.err)
		}
		if r.resp == nil || r.resp.ID != "z" {
			t.Fatalf("RateLimit=0 下响应异常: %+v", r.resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RateLimit=0 导致 Submit 永久挂起（limiter.Wait 死循环）")
	}

	// 守护点二：Stop 不应因 worker 卡在 limiter.Wait() 而死锁。
	stopDone := make(chan struct{})
	go func() {
		b.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
	case <-time.After(2 * time.Second):
		t.Fatal("RateLimit=0 导致 Stop 死锁（wg.Wait 永不返回，worker 卡在 limiter.Wait）")
	}
}

// cfg2okProvider 返回固定成功响应的 Provider，回显请求 ID。
func cfg2okProvider() *stubProvider {
	return &stubProvider{
		fn: func(_ context.Context, req *Request) (*Response, error) {
			return &Response{ID: req.ID, Content: "ok:" + req.ID, FinishReason: "stop"}, nil
		},
	}
}

// ============== Submit 基本路径测试 ==============

func TestSubmit_ProviderNotSet(t *testing.T) {
	// provider 为 nil 时 Submit 应直接返回 ErrProviderNotSet
	b := NewBatcher(nil, fastConfig())
	b.Start()
	defer b.Stop()

	resp, err := b.Submit(context.Background(), &Request{ID: "x"})
	if !errors.Is(err, ErrProviderNotSet) {
		t.Errorf("err = %v, 期望 ErrProviderNotSet", err)
	}
	if resp != nil {
		t.Errorf("resp = %v, 期望 nil", resp)
	}
}

func TestSubmit_Success(t *testing.T) {
	b := NewBatcher(okProvider(7), fastConfig())
	b.Start()
	defer b.Stop()

	resp, err := b.Submit(context.Background(), &Request{ID: "req-1"})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if resp == nil {
		t.Fatal("resp 为 nil")
	}
	if resp.ID != "req-1" {
		t.Errorf("resp.ID = %q, 期望 req-1", resp.ID)
	}
	if resp.Content != "ok:req-1" {
		t.Errorf("resp.Content = %q", resp.Content)
	}
	if resp.TokensUsed != 7 {
		t.Errorf("TokensUsed = %d, 期望 7", resp.TokensUsed)
	}
}

func TestSubmit_QueueFull(t *testing.T) {
	// QueueSize=1 且不 Start（无 collector 消费），队列填满后再次 Submit 应返回 ErrQueueFull。
	// Submit 入队用了带 default 的 select，队列满时立即走 default 返回 ErrQueueFull（不阻塞）。
	cfg := fastConfig()
	cfg.QueueSize = 1
	b := NewBatcher(okProvider(1), cfg)
	// 故意不 Start，使 queue 无人消费

	// 后台一个 Submit：成功入队（占满缓冲区 1），随后阻塞在等待响应，
	// 用一个长 ctx 让它持续占住队列，直到测试结束才取消。
	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	enqueued := make(chan struct{})
	go func() {
		// 这次 Submit 会把 pending 写入 queue（成功），然后在 <-pending.response 处阻塞
		close(enqueued)
		_, _ = b.Submit(bgCtx, &Request{ID: "a"})
	}()
	<-enqueued

	// 等待 queue 缓冲被占满（len==cap）
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(b.queue) < cap(b.queue) {
		time.Sleep(time.Millisecond)
	}
	if len(b.queue) != cap(b.queue) {
		t.Fatalf("前置条件失败：queue 未被占满 len=%d cap=%d", len(b.queue), cap(b.queue))
	}

	// 队列已满，此次 Submit 应立即返回 ErrQueueFull
	resp, err := b.Submit(context.Background(), &Request{ID: "b"})
	if !errors.Is(err, ErrQueueFull) {
		t.Errorf("err = %v, 期望 ErrQueueFull", err)
	}
	if resp != nil {
		t.Errorf("resp = %v, 期望 nil", resp)
	}
}

func TestSubmit_ContextCanceledWhileWaiting(t *testing.T) {
	// 提交成功进入队列，但 provider 慢，等待响应期间 context 取消
	slow := &stubProvider{
		fn: func(ctx context.Context, req *Request) (*Response, error) {
			select {
			case <-time.After(2 * time.Second):
				return &Response{ID: req.ID}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}
	b := NewBatcher(slow, fastConfig())
	b.Start()
	defer b.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	resp, err := b.Submit(ctx, &Request{ID: "slow"})
	if err == nil {
		t.Error("期望 context 取消错误，但 err 为 nil")
	}
	if resp != nil {
		t.Errorf("resp = %v, 期望 nil", resp)
	}
}

// ============== 错误路径：错误信息丢失（疑似 bug）==============

// 回归: 非可重试错误原始信息被 errors.Unwrap 误丢为 ErrBatchFailed（W8 bug #3）。
//
// TestSubmit_NonRetryableError_PreservesError 验证：
// 当 provider 返回不可重试错误时，调用方应能拿到"原始错误"。
//
// 数据流分析：
//   - retry.DoWithContext 在 RetryIf 返回 false 时，直接返回原始错误（未包装）。
//   - processRequest 中：err != nil && resp == nil →
//       err = errors.Unwrap(err)  // 对未包装的错误返回 nil！
//       if err == nil { err = ErrBatchFailed }  // 原始错误被丢弃
//   - 结果：调用方拿到 ErrBatchFailed，真实错误（如 "invalid api key"）丢失。
//
// 期望：调用方应能从返回错误中识别出原始错误内容。
func TestSubmit_NonRetryableError_PreservesError(t *testing.T) {
	sentinel := errors.New("invalid api key: 401 unauthorized")
	p := &stubProvider{
		fn: func(_ context.Context, _ *Request) (*Response, error) {
			return nil, sentinel
		},
	}
	b := NewBatcher(p, fastConfig())
	b.Start()
	defer b.Stop()

	resp, err := b.Submit(context.Background(), &Request{ID: "e1"})
	if err == nil {
		t.Fatal("期望返回错误，得到 nil")
	}
	// 核心断言：原始错误信息不应丢失
	if !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("原始错误信息丢失: 返回 err=%q, 期望包含 'invalid api key'", err.Error())
	}
	// 不可重试错误只应调用 provider 一次
	if got := p.callCount(); got != 1 {
		t.Errorf("不可重试错误 provider 调用 %d 次, 期望 1 次", got)
	}
	// resp 不应为 nil（processRequest 兜底会构造一个）
	if resp != nil && resp.ID != "e1" {
		t.Errorf("resp.ID = %q, 期望 e1", resp.ID)
	}
}

// 回归: 重试耗尽后原始失败原因被 Unwrap 成 ErrMaxAttemptsReached 而丢失（W8 bug #4）。
//
// TestSubmit_RetryableError_ExhaustsRetries 验证可重试错误会重试 MaxRetries+1 次，
// 并最终在所有重试耗尽后仍返回错误，且原始错误信息可识别。
func TestSubmit_RetryableError_ExhaustsRetries(t *testing.T) {
	// 使用包含 "timeout" 的错误，isRetryableError 判定为可重试
	retryable := errors.New("upstream timeout occurred")
	p := &stubProvider{
		fn: func(_ context.Context, _ *Request) (*Response, error) {
			return nil, retryable
		},
	}
	cfg := fastConfig()
	cfg.MaxRetries = 2 // 期望总共尝试 3 次
	b := NewBatcher(p, cfg)
	b.Start()
	defer b.Stop()

	resp, err := b.Submit(context.Background(), &Request{ID: "r1"})
	if err == nil {
		t.Fatal("重试耗尽后应返回错误")
	}
	if got := p.callCount(); got != int64(cfg.MaxRetries+1) {
		t.Errorf("provider 调用 %d 次, 期望 %d 次 (MaxRetries+1)", got, cfg.MaxRetries+1)
	}
	// 重试耗尽时 DoWithContext 返回 ErrMaxAttemptsReached 包装的错误，
	// processRequest 会 Unwrap 它。验证最终错误能反映出原始原因。
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("重试耗尽错误未保留原始原因: %q", err.Error())
	}
	_ = resp
}

// TestSubmit_RetryThenSucceed 验证：前几次失败、最后成功时，最终返回成功响应。
func TestSubmit_RetryThenSucceed(t *testing.T) {
	var n int64
	p := &stubProvider{
		fn: func(_ context.Context, req *Request) (*Response, error) {
			c := atomic.AddInt64(&n, 1)
			if c < 3 {
				return nil, errors.New("temporarily unavailable")
			}
			return &Response{ID: req.ID, Content: "recovered", TokensUsed: 5}, nil
		},
	}
	cfg := fastConfig()
	cfg.MaxRetries = 3
	b := NewBatcher(p, cfg)
	b.Start()
	defer b.Stop()

	resp, err := b.Submit(context.Background(), &Request{ID: "s1"})
	if err != nil {
		t.Fatalf("第三次应成功，但返回错误: %v", err)
	}
	if resp == nil || resp.Content != "recovered" {
		t.Fatalf("期望恢复成功响应, resp=%+v", resp)
	}
	if got := p.callCount(); got != 3 {
		t.Errorf("provider 调用 %d 次, 期望 3 次", got)
	}
}

// ============== BatchSubmit 测试：分批/聚合/顺序/部分失败 ==============

func TestBatchSubmit_OrderAndAggregation(t *testing.T) {
	// 验证结果顺序与输入顺序一一对应（即使乱序完成）
	p := &stubProvider{
		fn: func(_ context.Context, req *Request) (*Response, error) {
			return &Response{ID: req.ID, Content: req.ID}, nil
		},
	}
	b := NewBatcher(p, fastConfig())
	b.Start()
	defer b.Stop()

	const n = 20
	reqs := make([]*Request, n)
	for i := 0; i < n; i++ {
		reqs[i] = &Request{ID: fmt.Sprintf("id-%02d", i)}
	}

	results := b.BatchSubmit(context.Background(), reqs)
	if len(results) != n {
		t.Fatalf("结果数 = %d, 期望 %d", len(results), n)
	}
	for i, r := range results {
		if r == nil {
			t.Fatalf("results[%d] 为 nil", i)
		}
		want := fmt.Sprintf("id-%02d", i)
		if r.ID != want {
			t.Errorf("results[%d].ID = %q, 期望 %q（顺序错乱）", i, r.ID, want)
		}
	}
}

func TestBatchSubmit_PartialFailure(t *testing.T) {
	// 偶数 ID 成功，奇数 ID 失败（不可重试），验证部分失败聚合正确
	p := &stubProvider{
		fn: func(_ context.Context, req *Request) (*Response, error) {
			if strings.HasSuffix(req.ID, "1") || strings.HasSuffix(req.ID, "3") {
				return nil, errors.New("permanent failure for " + req.ID)
			}
			return &Response{ID: req.ID, Content: "ok", TokensUsed: 2}, nil
		},
	}
	b := NewBatcher(p, fastConfig())
	b.Start()
	defer b.Stop()

	reqs := []*Request{
		{ID: "k0"}, {ID: "k1"}, {ID: "k2"}, {ID: "k3"}, {ID: "k4"},
	}
	results := b.BatchSubmit(context.Background(), reqs)

	if len(results) != len(reqs) {
		t.Fatalf("结果数 = %d, 期望 %d", len(results), len(reqs))
	}
	var success, failed int
	for i, r := range results {
		if r == nil {
			t.Fatalf("results[%d] 为 nil（部分失败时不应有 nil 槽位）", i)
		}
		if r.ID != reqs[i].ID {
			t.Errorf("results[%d].ID = %q, 期望 %q", i, r.ID, reqs[i].ID)
		}
		if r.Error != nil {
			failed++
		} else {
			success++
		}
	}
	if success != 3 || failed != 2 {
		t.Errorf("成功=%d 失败=%d, 期望 成功=3 失败=2", success, failed)
	}
}

func TestBatchSubmit_Empty(t *testing.T) {
	b := NewBatcher(okProvider(1), fastConfig())
	b.Start()
	defer b.Stop()

	results := b.BatchSubmit(context.Background(), []*Request{})
	if results == nil {
		t.Error("空输入应返回非 nil 空切片")
	}
	if len(results) != 0 {
		t.Errorf("空输入结果数 = %d, 期望 0", len(results))
	}
}

func TestBatchSubmit_ProviderNotSet(t *testing.T) {
	// provider 为 nil：每个请求 Submit 返回 ErrProviderNotSet，
	// BatchSubmit 应为每个槽位构造带 Error 的 Response（不应有 nil）
	b := NewBatcher(nil, fastConfig())
	b.Start()
	defer b.Stop()

	reqs := []*Request{{ID: "a"}, {ID: "b"}}
	results := b.BatchSubmit(context.Background(), reqs)
	if len(results) != 2 {
		t.Fatalf("结果数 = %d, 期望 2", len(results))
	}
	for i, r := range results {
		if r == nil {
			t.Fatalf("results[%d] 为 nil", i)
		}
		if !errors.Is(r.Error, ErrProviderNotSet) {
			t.Errorf("results[%d].Error = %v, 期望 ErrProviderNotSet", i, r.Error)
		}
		if r.ID != reqs[i].ID {
			t.Errorf("results[%d].ID = %q, 期望 %q", i, r.ID, reqs[i].ID)
		}
	}
}

// ============== 批处理分批逻辑测试 ==============

func TestBatching_RespectsMaxBatchSize(t *testing.T) {
	// 通过 OnBatchStart 回调记录每个批次的大小，
	// 验证没有任何批次超过 MaxBatchSize。
	var mu sync.Mutex
	var batchSizes []int

	cfg := fastConfig()
	cfg.MaxBatchSize = 3
	cfg.FlushInterval = 500 * time.Millisecond // 较长，避免 ticker 提前切批
	cfg.OnBatchStart = func(_ string, count int) {
		mu.Lock()
		batchSizes = append(batchSizes, count)
		mu.Unlock()
	}

	// provider 稍微慢一点，确保请求能堆积成批
	p := &stubProvider{
		fn: func(_ context.Context, req *Request) (*Response, error) {
			return &Response{ID: req.ID}, nil
		},
	}
	b := NewBatcher(p, cfg)
	b.Start()
	defer b.Stop()

	const n = 9
	reqs := make([]*Request, n)
	for i := range reqs {
		reqs[i] = &Request{ID: fmt.Sprintf("b%d", i)}
	}
	b.BatchSubmit(context.Background(), reqs)

	mu.Lock()
	defer mu.Unlock()
	total := 0
	for _, sz := range batchSizes {
		if sz > cfg.MaxBatchSize {
			t.Errorf("批次大小 %d 超过 MaxBatchSize=%d", sz, cfg.MaxBatchSize)
		}
		total += sz
	}
	if total != n {
		t.Errorf("批次请求总数 = %d, 期望 %d（请求丢失或重复）", total, n)
	}
}

// ============== 回调测试 ==============

func TestCallbacks_Invoked(t *testing.T) {
	var (
		batchStart    int64
		batchComplete int64
		reqComplete   int64
		errCalls      int64
	)
	cfg := fastConfig()
	cfg.OnBatchStart = func(_ string, _ int) { atomic.AddInt64(&batchStart, 1) }
	cfg.OnBatchComplete = func(_ string, _ int, _ time.Duration) { atomic.AddInt64(&batchComplete, 1) }
	cfg.OnRequestComplete = func(_ *Request, _ *Response) { atomic.AddInt64(&reqComplete, 1) }
	cfg.OnError = func(_ *Request, _ error) { atomic.AddInt64(&errCalls, 1) }

	// 一半成功一半失败
	p := &stubProvider{
		fn: func(_ context.Context, req *Request) (*Response, error) {
			if strings.HasPrefix(req.ID, "bad") {
				return nil, errors.New("boom")
			}
			return &Response{ID: req.ID, TokensUsed: 1}, nil
		},
	}
	b := NewBatcher(p, cfg)
	b.Start()
	defer b.Stop()

	reqs := []*Request{{ID: "good1"}, {ID: "bad1"}, {ID: "good2"}, {ID: "bad2"}}
	b.BatchSubmit(context.Background(), reqs)

	if atomic.LoadInt64(&reqComplete) != 4 {
		t.Errorf("OnRequestComplete 调用 %d 次, 期望 4", reqComplete)
	}
	if atomic.LoadInt64(&errCalls) != 2 {
		t.Errorf("OnError 调用 %d 次, 期望 2", errCalls)
	}
	if atomic.LoadInt64(&batchStart) < 1 {
		t.Error("OnBatchStart 未被调用")
	}
	if atomic.LoadInt64(&batchComplete) < 1 {
		t.Error("OnBatchComplete 未被调用")
	}
	// 批次开始与完成次数应相等
	if atomic.LoadInt64(&batchStart) != atomic.LoadInt64(&batchComplete) {
		t.Errorf("OnBatchStart(%d) != OnBatchComplete(%d)", batchStart, batchComplete)
	}
}

// ============== 统计一致性测试 ==============

func TestStats_Consistency(t *testing.T) {
	// 3 成功 + 2 失败，验证统计恒等：
	//   TotalRequests == Success + Failed
	//   TotalTokens == 成功请求 token 之和
	const tokensPer = 4
	p := &stubProvider{
		fn: func(_ context.Context, req *Request) (*Response, error) {
			if strings.HasPrefix(req.ID, "f") {
				return nil, errors.New("fail")
			}
			return &Response{ID: req.ID, TokensUsed: tokensPer}, nil
		},
	}
	b := NewBatcher(p, fastConfig())
	b.Start()
	defer b.Stop()

	reqs := []*Request{{ID: "s1"}, {ID: "s2"}, {ID: "s3"}, {ID: "f1"}, {ID: "f2"}}
	b.BatchSubmit(context.Background(), reqs)

	st := b.GetStats()
	if st.TotalRequests != 5 {
		t.Errorf("TotalRequests = %d, 期望 5", st.TotalRequests)
	}
	if st.SuccessRequests != 3 {
		t.Errorf("SuccessRequests = %d, 期望 3", st.SuccessRequests)
	}
	if st.FailedRequests != 2 {
		t.Errorf("FailedRequests = %d, 期望 2", st.FailedRequests)
	}
	if st.SuccessRequests+st.FailedRequests != st.TotalRequests {
		t.Errorf("统计恒等失败: %d + %d != %d", st.SuccessRequests, st.FailedRequests, st.TotalRequests)
	}
	if st.TotalTokens != int64(3*tokensPer) {
		t.Errorf("TotalTokens = %d, 期望 %d", st.TotalTokens, 3*tokensPer)
	}
	if st.TotalBatches < 1 {
		t.Errorf("TotalBatches = %d, 期望 >=1", st.TotalBatches)
	}
	if st.TotalLatency <= 0 {
		t.Errorf("TotalLatency = %v, 期望 >0", st.TotalLatency)
	}
}

// ============== 并发竞态测试 ==============

func TestConcurrent_NoRaceAndAllProcessed(t *testing.T) {
	// 多个 goroutine 并发 Submit，验证无竞态（-race）、无丢失、统计一致。
	p := &stubProvider{
		fn: func(_ context.Context, req *Request) (*Response, error) {
			return &Response{ID: req.ID, TokensUsed: 1}, nil
		},
	}
	cfg := fastConfig()
	cfg.QueueSize = 2000
	b := NewBatcher(p, cfg)
	b.Start()
	defer b.Stop()

	const goroutines = 20
	const perG = 25
	var wg sync.WaitGroup
	var okCount int64
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				resp, err := b.Submit(context.Background(), &Request{ID: fmt.Sprintf("g%d-i%d", g, i)})
				if err == nil && resp != nil {
					atomic.AddInt64(&okCount, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	total := int64(goroutines * perG)
	if okCount != total {
		t.Errorf("成功提交 %d, 期望 %d", okCount, total)
	}
	st := b.GetStats()
	if st.TotalRequests != total {
		t.Errorf("stats.TotalRequests = %d, 期望 %d", st.TotalRequests, total)
	}
	if st.SuccessRequests != total {
		t.Errorf("stats.SuccessRequests = %d, 期望 %d", st.SuccessRequests, total)
	}
}

// ============== Start / Stop 幂等性与状态机测试 ==============

func TestStartStop_Idempotent(t *testing.T) {
	b := NewBatcher(okProvider(1), fastConfig())
	// 多次 Start 应只启动一次（CAS 保护）
	b.Start()
	b.Start()
	b.Start()

	resp, err := b.Submit(context.Background(), &Request{ID: "x"})
	if err != nil || resp == nil {
		t.Fatalf("Start 后应能正常提交: resp=%v err=%v", resp, err)
	}

	// 多次 Stop 应安全（不重复 close channel 导致 panic）
	b.Stop()
	b.Stop()
	b.Stop()
}

func TestStop_WithoutStart(t *testing.T) {
	// 未 Start 直接 Stop：running 为 0，CAS(1,0) 失败，应直接返回不 panic
	b := NewBatcher(okProvider(1), fastConfig())
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("未 Start 时 Stop 发生 panic: %v", r)
		}
	}()
	b.Stop()
}

// 回归: Stop 最终 flush 与 stopChan 双就绪竞态静默丢弃残留批次（W8 bug #1）。
//
// TestStop_FlushesPending 验证 Stop 时 collector 会 flush 残留在 batch 缓冲里的请求。
//
// 数据流/竞态分析（疑似 bug）：
//   - Stop() 调用 close(b.stopChan)。
//   - collector 的 for-select 命中 <-b.stopChan 分支，调用 flush()。
//   - flush() 内部 select 同时就绪两个 case：
//       case b.batchChan <- batchCopy:   // 把残留批次交给 worker
//       case <-b.stopChan:               // stopChan 已 close，永远就绪 → return（丢弃批次！）
//   - Go select 在多个就绪 case 间随机选择，约 50% 概率走 <-b.stopChan，
//     残留批次被静默丢弃，对应请求的 response channel 永不写入，
//     Submit 调用方（若 ctx 无超时）将永久阻塞。
//
// 期望：Stop 后所有已入队但未发车的请求都应被处理并返回响应。
//
// 为稳定触发随机丢弃，这里做多轮 Stop，只要任意一轮请求被挂起即判失败。
func TestStop_FlushesPending(t *testing.T) {
	const rounds = 12
	for round := 0; round < rounds; round++ {
		p := &stubProvider{
			fn: func(_ context.Context, req *Request) (*Response, error) {
				return &Response{ID: req.ID}, nil
			},
		}
		cfg := fastConfig()
		cfg.MaxBatchSize = 100               // 不会因数量触发 flush
		cfg.FlushInterval = 10 * time.Second // 不会因 ticker 触发 flush
		b := NewBatcher(p, cfg)
		b.Start()

		// 提交请求并在另一 goroutine 等待响应（带超时，避免测试自身挂死）
		done := make(chan *Response, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			defer cancel()
			resp, _ := b.Submit(ctx, &Request{ID: "pending"})
			done <- resp
		}()

		// 确保请求已离开 queue、进入 collector 的 batch 缓冲
		time.Sleep(50 * time.Millisecond)

		// Stop 触发 flush
		b.Stop()

		select {
		case resp := <-done:
			if resp == nil {
				t.Fatalf("第 %d 轮: Stop 未 flush 残留请求（batchChan<- 与 <-stopChan 竞态导致批次被丢弃），请求被挂起", round)
			}
			if resp.ID != "pending" {
				t.Fatalf("第 %d 轮: Stop flush 后残留请求处理错误: %+v", round, resp)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("第 %d 轮: 等待响应超时，请求被永久挂起", round)
		}
	}
}

// ============== BatchComplete / ParallelComplete 辅助函数测试 ==============

func TestBatchComplete_Helper(t *testing.T) {
	p := okProvider(3)
	reqs := []*Request{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	results := BatchComplete(context.Background(), p, reqs, fastConfig())
	if len(results) != 3 {
		t.Fatalf("结果数 = %d, 期望 3", len(results))
	}
	for i, r := range results {
		if r == nil || r.ID != reqs[i].ID {
			t.Errorf("results[%d] = %+v, 期望 ID=%s", i, r, reqs[i].ID)
		}
	}
}

func TestParallelComplete_Success(t *testing.T) {
	p := okProvider(2)
	reqs := []*Request{{ID: "p0"}, {ID: "p1"}, {ID: "p2"}, {ID: "p3"}}
	results := ParallelComplete(context.Background(), p, reqs, 2)
	if len(results) != 4 {
		t.Fatalf("结果数 = %d, 期望 4", len(results))
	}
	for i, r := range results {
		if r == nil {
			t.Fatalf("results[%d] 为 nil", i)
		}
		if r.ID != reqs[i].ID {
			t.Errorf("results[%d].ID = %q, 期望 %q（顺序错乱）", i, r.ID, reqs[i].ID)
		}
		if r.Content != "ok:"+reqs[i].ID {
			t.Errorf("results[%d].Content = %q", i, r.Content)
		}
	}
}

func TestParallelComplete_PartialFailure(t *testing.T) {
	p := &stubProvider{
		fn: func(_ context.Context, req *Request) (*Response, error) {
			if req.ID == "fail" {
				return nil, errors.New("provider error")
			}
			return &Response{ID: req.ID, Content: "good"}, nil
		},
	}
	reqs := []*Request{{ID: "ok1"}, {ID: "fail"}, {ID: "ok2"}}
	results := ParallelComplete(context.Background(), p, reqs, 3)

	if results[1] == nil {
		t.Fatal("失败请求结果不应为 nil")
	}
	if results[1].Error == nil {
		t.Error("失败请求 Error 应非 nil")
	}
	if results[1].ID != "fail" {
		t.Errorf("失败请求 ID = %q, 期望 fail", results[1].ID)
	}
	if results[0].Error != nil || results[2].Error != nil {
		t.Error("成功请求不应有 Error")
	}
}

func TestParallelComplete_ZeroOrNegativeConcurrency(t *testing.T) {
	// maxConcurrent <= 0 时应回退默认 10，不应死锁或 panic
	p := okProvider(1)
	reqs := []*Request{{ID: "a"}, {ID: "b"}}
	for _, mc := range []int{0, -5} {
		results := ParallelComplete(context.Background(), p, reqs, mc)
		if len(results) != 2 {
			t.Errorf("maxConcurrent=%d: 结果数 = %d, 期望 2", mc, len(results))
		}
		for i, r := range results {
			if r == nil {
				t.Errorf("maxConcurrent=%d: results[%d] 为 nil", mc, i)
			}
		}
	}
}

func TestParallelComplete_Empty(t *testing.T) {
	results := ParallelComplete(context.Background(), okProvider(1), []*Request{}, 5)
	if len(results) != 0 {
		t.Errorf("空输入结果数 = %d, 期望 0", len(results))
	}
}

// TestParallelComplete_SetsLatency 验证 Latency 被设置（>=0）。
func TestParallelComplete_SetsLatency(t *testing.T) {
	p := &stubProvider{
		fn: func(_ context.Context, req *Request) (*Response, error) {
			return &Response{ID: req.ID}, nil
		},
	}
	results := ParallelComplete(context.Background(), p, []*Request{{ID: "a"}}, 1)
	if results[0].Latency < 0 {
		t.Errorf("Latency = %d, 期望 >=0", results[0].Latency)
	}
}

// ============== RequestBuilder 测试 ==============

func TestRequestBuilder_FullChain(t *testing.T) {
	req := NewRequestBuilder().
		WithID("rb-1").
		WithModel("gpt-4o").
		WithMaxTokens(256).
		WithTemperature(0.7).
		AddSystemMessage("you are helpful").
		AddUserMessage("hello").
		AddAssistantMessage("hi").
		WithMetadata("k", "v").
		Build()

	if req.ID != "rb-1" {
		t.Errorf("ID = %q", req.ID)
	}
	if req.Model != "gpt-4o" {
		t.Errorf("Model = %q", req.Model)
	}
	if req.MaxTokens != 256 {
		t.Errorf("MaxTokens = %d", req.MaxTokens)
	}
	if req.Temperature != 0.7 {
		t.Errorf("Temperature = %v", req.Temperature)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("Messages 数 = %d, 期望 3", len(req.Messages))
	}
	// 验证消息顺序与角色
	wantRoles := []string{"system", "user", "assistant"}
	for i, role := range wantRoles {
		if req.Messages[i].Role != role {
			t.Errorf("Messages[%d].Role = %q, 期望 %q", i, req.Messages[i].Role, role)
		}
	}
	if req.Metadata["k"] != "v" {
		t.Errorf("Metadata[k] = %v, 期望 v", req.Metadata["k"])
	}
}

func TestRequestBuilder_EmptyBuild(t *testing.T) {
	req := NewRequestBuilder().Build()
	if req == nil {
		t.Fatal("Build 返回 nil")
	}
	if req.Metadata == nil {
		t.Error("Metadata 应被初始化为非 nil map")
	}
	if len(req.Messages) != 0 {
		t.Errorf("空构建器 Messages 数 = %d, 期望 0", len(req.Messages))
	}
}

func TestRequestBuilder_UnicodeAndLongContent(t *testing.T) {
	// Unicode + 超长内容边界
	long := strings.Repeat("你好🌍", 5000)
	req := NewRequestBuilder().
		WithID("u-1").
		AddUserMessage(long).
		AddUserMessage("emoji 测试 😀🎉").
		Build()

	if req.Messages[0].Content != long {
		t.Error("超长 Unicode 内容被截断或损坏")
	}
	if !strings.Contains(req.Messages[1].Content, "😀") {
		t.Error("emoji 内容损坏")
	}
}

// ============== isRetryableError / containsAny 辅助函数测试 ==============

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil 错误", nil, false},
		{"哨兵 ErrRateLimited", ErrRateLimited, true},
		{"哨兵 ErrTimeout", ErrTimeout, true},
		{"包装 ErrTimeout", fmt.Errorf("call failed: %w", ErrTimeout), true},
		{"含 timeout 字样", errors.New("connection timeout"), true},
		{"含 rate limit 字样", errors.New("you hit the rate limit"), true},
		{"含 429", errors.New("HTTP 429 too many requests"), true},
		{"含 503", errors.New("503 service unavailable"), true},
		{"含 temporarily", errors.New("service temporarily down"), true},
		{"大小写不敏感 TIMEOUT", errors.New("TIMEOUT ERROR"), true},
		{"普通错误不可重试", errors.New("invalid argument"), false},
		{"401 不可重试", errors.New("401 unauthorized"), false},
		{"ErrProviderNotSet 不可重试", ErrProviderNotSet, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableError(tt.err); got != tt.want {
				t.Errorf("isRetryableError(%v) = %v, 期望 %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestContainsAny(t *testing.T) {
	tests := []struct {
		s       string
		substrs []string
		want    bool
	}{
		{"hello world", []string{"world"}, true},
		{"HELLO WORLD", []string{"world"}, true}, // 大小写不敏感
		{"hello", []string{"WORLD"}, false},
		{"foobar", []string{"x", "y", "bar"}, true},
		{"", []string{"a"}, false},
		{"anything", []string{}, false}, // 无子串
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			if got := containsAny(tt.s, tt.substrs...); got != tt.want {
				t.Errorf("containsAny(%q, %v) = %v, 期望 %v", tt.s, tt.substrs, got, tt.want)
			}
		})
	}
}

// ============== generateBatchID 唯一性测试 ==============

func TestGenerateBatchID_Unique(t *testing.T) {
	const n = 1000
	seen := make(map[string]bool, n)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := generateBatchID()
			mu.Lock()
			if seen[id] {
				t.Errorf("批次 ID 重复: %s", id)
			}
			seen[id] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != n {
		t.Errorf("唯一 ID 数 = %d, 期望 %d", len(seen), n)
	}
	for id := range seen {
		if !strings.HasPrefix(id, "batch-") {
			t.Errorf("ID 前缀错误: %s", id)
			break
		}
	}
}

// ============== GetStats 并发安全测试 ==============

func TestGetStats_ConcurrentReadDuringProcessing(t *testing.T) {
	// 处理过程中并发读取统计，验证 -race 下无数据竞争
	p := &stubProvider{
		fn: func(_ context.Context, req *Request) (*Response, error) {
			return &Response{ID: req.ID, TokensUsed: 1}, nil
		},
	}
	b := NewBatcher(p, fastConfig())
	b.Start()
	defer b.Stop()

	stop := make(chan struct{})
	var readerWg sync.WaitGroup
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = b.GetStats()
			}
		}
	}()

	reqs := make([]*Request, 50)
	for i := range reqs {
		reqs[i] = &Request{ID: fmt.Sprintf("c%d", i)}
	}
	b.BatchSubmit(context.Background(), reqs)

	close(stop)
	readerWg.Wait()

	st := b.GetStats()
	if st.TotalRequests != 50 {
		t.Errorf("TotalRequests = %d, 期望 50", st.TotalRequests)
	}
}
