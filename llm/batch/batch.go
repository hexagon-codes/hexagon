// Package batch 提供 LLM 请求批处理功能
//
// 本包实现了 LLM 请求的批量处理和优化：
//   - 请求合并：将多个相似请求合并处理
//   - 请求队列：管理并发请求队列
//   - 速率限制：控制 API 调用频率
//   - 自动重试：处理临时错误
//
//   - OpenAI Batch API: 批量处理
//   - gRPC: 请求流和批处理
//
// 使用示例（Batcher 实例方式，需手动 Start/Stop）：
//
//	batcher := batch.NewBatcher(provider, batch.DefaultConfig())
//	batcher.Start()
//	defer batcher.Stop()
//	results := batcher.BatchSubmit(ctx, requests)
//
// 使用示例（包级辅助函数，自动管理生命周期）：
//
//	results := batch.BatchComplete(ctx, provider, requests)
package batch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hexagon-codes/toolkit/util/rate"
	"github.com/hexagon-codes/toolkit/util/retry"
)

// ============== 错误定义 ==============

var (
	// ErrBatchFailed 批处理失败
	ErrBatchFailed = errors.New("batch processing failed")

	// ErrQueueFull 队列已满
	ErrQueueFull = errors.New("request queue is full")

	// ErrRateLimited 被限流
	ErrRateLimited = errors.New("rate limited")

	// ErrTimeout 超时
	ErrTimeout = errors.New("timeout")

	// ErrProviderNotSet 未设置 Provider
	ErrProviderNotSet = errors.New("provider not set")
)

// ============== 请求和响应 ==============

// Request 批处理请求
type Request struct {
	// ID 请求 ID
	ID string

	// Messages 消息列表
	Messages []Message

	// Model 模型名称（可选，使用默认）
	Model string

	// MaxTokens 最大 token 数
	MaxTokens int

	// Temperature 温度
	Temperature float64

	// Metadata 元数据
	Metadata map[string]any
}

// Message 消息
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Response 批处理响应
type Response struct {
	// ID 请求 ID
	ID string

	// Content 响应内容
	Content string

	// TokensUsed 使用的 token 数
	TokensUsed int

	// FinishReason 完成原因
	FinishReason string

	// Error 错误（如果有）
	Error error

	// Latency 延迟（毫秒）
	Latency int64

	// Metadata 元数据
	Metadata map[string]any
}

// ============== Provider 接口 ==============

// Provider LLM 提供者接口（简化版）
type Provider interface {
	// Complete 执行补全
	Complete(ctx context.Context, req *Request) (*Response, error)
}

// ============== 批处理配置 ==============

// Config 批处理配置
type Config struct {
	// MaxBatchSize 最大批量大小
	MaxBatchSize int

	// MaxConcurrent 最大并发数
	MaxConcurrent int

	// QueueSize 队列大小
	QueueSize int

	// FlushInterval 刷新间隔
	FlushInterval time.Duration

	// Timeout 请求超时
	Timeout time.Duration

	// MaxRetries 最大重试次数
	MaxRetries int

	// RetryDelay 重试延迟
	RetryDelay time.Duration

	// RateLimit 速率限制（每秒请求数）
	RateLimit int

	// OnBatchStart 批次开始回调
	OnBatchStart func(batchID string, count int)

	// OnBatchComplete 批次完成回调
	OnBatchComplete func(batchID string, count int, duration time.Duration)

	// OnRequestComplete 请求完成回调
	OnRequestComplete func(req *Request, resp *Response)

	// OnError 错误回调
	OnError func(req *Request, err error)
}

// DefaultConfig 默认配置
func DefaultConfig() *Config {
	return &Config{
		MaxBatchSize:  10,
		MaxConcurrent: 5,
		QueueSize:     1000,
		FlushInterval: 100 * time.Millisecond,
		Timeout:       60 * time.Second,
		MaxRetries:    3,
		RetryDelay:    time.Second,
		RateLimit:     100, // 100 请求/秒
	}
}

// ============== 批处理器 ==============

// Batcher 批处理器
type Batcher struct {
	provider Provider
	config   *Config

	// 请求队列
	queue     chan *pendingRequest
	batchChan chan []*pendingRequest

	// 状态
	running  int32
	wg       sync.WaitGroup
	stopChan chan struct{}

	// 统计
	stats *Stats

	// 速率限制（使用 toolkit 令牌桶实现）
	limiter *rate.TokenBucket
}

// pendingRequest 待处理请求
type pendingRequest struct {
	request  *Request
	response chan *Response
}

// Stats 统计信息
type Stats struct {
	TotalRequests   int64         `json:"total_requests"`
	SuccessRequests int64         `json:"success_requests"`
	FailedRequests  int64         `json:"failed_requests"`
	TotalBatches    int64         `json:"total_batches"`
	TotalTokens     int64         `json:"total_tokens"`
	TotalLatency    time.Duration `json:"total_latency"`
	mu              sync.RWMutex
}

// NewBatcher 创建批处理器
func NewBatcher(provider Provider, config ...*Config) *Batcher {
	cfg := DefaultConfig()
	if len(config) > 0 && config[0] != nil {
		cfg = config[0]
	}

	// 速率限制规范化：
	// RateLimit<=0 表示"不限流"。此时必须将 limiter 置为 nil，
	// 而不能交给 rate.NewTokenBucket(0, 0)——后者 capacity=0、rate=0，
	// 令牌桶 Wait() 内部计算 waitTime = (1-0)/0*1000 → +Inf，
	// 会陷入 time.Sleep(+Inf) 死循环，导致 worker 永久卡死、
	// Stop 的 wg.Wait 永不返回（死锁）、goroutine 泄漏。
	// 限流场景下令牌桶容量与速率均取 RateLimit（每秒请求数）。
	var limiter *rate.TokenBucket
	if cfg.RateLimit > 0 {
		limiter = rate.NewTokenBucket(cfg.RateLimit, float64(cfg.RateLimit))
	}

	return &Batcher{
		provider:  provider,
		config:    cfg,
		queue:     make(chan *pendingRequest, cfg.QueueSize),
		batchChan: make(chan []*pendingRequest, cfg.MaxConcurrent),
		stopChan:  make(chan struct{}),
		stats:     &Stats{},
		limiter:   limiter,
	}
}

// Start 启动批处理器
func (b *Batcher) Start() {
	if !atomic.CompareAndSwapInt32(&b.running, 0, 1) {
		return
	}

	// 启动收集器
	b.wg.Add(1)
	go b.collector()

	// 启动工作者
	for i := 0; i < b.config.MaxConcurrent; i++ {
		b.wg.Add(1)
		go b.worker(i)
	}
}

// Stop 停止批处理器
//
// 停止流程：先关闭 stopChan 通知 collector 退出，collector 退出前会把
// 残留在 batch 缓冲里的请求做一次最终 flush（阻塞发送，确保不丢弃），
// 随后 close(batchChan) 让所有 worker 自然退出，最后 wg.Wait 等待收尾。
func (b *Batcher) Stop() {
	if !atomic.CompareAndSwapInt32(&b.running, 1, 0) {
		return
	}
	close(b.stopChan)
	b.wg.Wait()
}

// Submit 提交请求
func (b *Batcher) Submit(ctx context.Context, req *Request) (*Response, error) {
	if b.provider == nil {
		return nil, ErrProviderNotSet
	}

	// 创建待处理请求
	pending := &pendingRequest{
		request:  req,
		response: make(chan *Response, 1),
	}

	// 提交到队列
	select {
	case b.queue <- pending:
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return nil, ErrQueueFull
	}

	// 等待响应
	select {
	case resp := <-pending.response:
		return resp, resp.Error
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// BatchSubmit 批量提交请求
func (b *Batcher) BatchSubmit(ctx context.Context, requests []*Request) []*Response {
	results := make([]*Response, len(requests))
	var wg sync.WaitGroup

	for i, req := range requests {
		i, req := i, req
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := b.Submit(ctx, req)
			if err != nil && resp == nil {
				resp = &Response{
					ID:    req.ID,
					Error: err,
				}
			}
			results[i] = resp
		}()
	}

	wg.Wait()
	return results
}

// collector 收集请求并组成批次
func (b *Batcher) collector() {
	defer b.wg.Done()

	ticker := time.NewTicker(b.config.FlushInterval)
	defer ticker.Stop()

	batch := make([]*pendingRequest, 0, b.config.MaxBatchSize)

	// flush 把当前 batch 缓冲发往 batchChan 交给 worker 处理。
	//
	// final 区分两种语境：
	//   - 稳态 flush（final=false）：运行期间因数量/ticker 触发，
	//     发送时仍监听 stopChan，以便在停止信号到来时及时退出，避免阻塞。
	//   - 最终 flush（final=true）：Stop 路径下退出前的收尾 flush，
	//     此时 stopChan 已 close，若再把 <-stopChan 放进发送 select，
	//     则 close 后的 stopChan 与可发送的 batchChan 会"双就绪"，
	//     Go 在多个就绪 case 间随机选择，约 50% 概率走 stopChan 分支，
	//     导致残留批次被静默丢弃、对应请求的 response 通道永不写入、
	//     Submit 调用方永久挂起。因此最终 flush 必须做"无 stopChan 逃逸"的
	//     阻塞发送，确保残留请求一定被交付。batchChan 有 MaxConcurrent
	//     的缓冲且 worker 在 batchChan close 后才退出，故此发送不会死锁。
	flush := func(final bool) {
		if len(batch) == 0 {
			return
		}

		// 发送批次（拷贝快照，避免后续复用底层数组）
		batchCopy := make([]*pendingRequest, len(batch))
		copy(batchCopy, batch)

		if final {
			// 最终 flush：阻塞发送，保证残留请求不被丢弃
			b.batchChan <- batchCopy
		} else {
			select {
			case b.batchChan <- batchCopy:
			case <-b.stopChan:
				return
			}
		}

		batch = batch[:0]
	}

	for {
		select {
		case <-b.stopChan:
			// 收尾：先把队列中已入队但尚未被收集的请求一并纳入，
			// 再做最终阻塞 flush，确保所有已入队请求都被交付处理。
			for {
				select {
				case req := <-b.queue:
					batch = append(batch, req)
					if len(batch) >= b.config.MaxBatchSize {
						flush(true)
					}
					continue
				default:
				}
				break
			}
			flush(true)
			close(b.batchChan)
			return

		case req := <-b.queue:
			batch = append(batch, req)
			if len(batch) >= b.config.MaxBatchSize {
				flush(false)
			}

		case <-ticker.C:
			flush(false)
		}
	}
}

// worker 工作者处理批次
func (b *Batcher) worker(id int) {
	defer b.wg.Done()

	for batch := range b.batchChan {
		b.processBatch(batch)
	}
}

// processBatch 处理批次
func (b *Batcher) processBatch(batch []*pendingRequest) {
	startTime := time.Now()
	batchID := generateBatchID()

	atomic.AddInt64(&b.stats.TotalBatches, 1)

	if b.config.OnBatchStart != nil {
		b.config.OnBatchStart(batchID, len(batch))
	}

	// 并发处理每个请求
	var wg sync.WaitGroup
	for _, pending := range batch {
		pending := pending
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.processRequest(pending)
		}()
	}
	wg.Wait()

	if b.config.OnBatchComplete != nil {
		b.config.OnBatchComplete(batchID, len(batch), time.Since(startTime))
	}
}

// processRequest 处理单个请求
func (b *Batcher) processRequest(pending *pendingRequest) {
	atomic.AddInt64(&b.stats.TotalRequests, 1)
	startTime := time.Now()

	// 速率限制（使用 toolkit 令牌桶，在锁外等待避免持锁 sleep）。
	// limiter 为 nil 表示 RateLimit<=0 即不限流，直接跳过等待。
	if b.limiter != nil {
		b.limiter.Wait()
	}

	// 创建带超时的 context
	ctx, cancel := context.WithTimeout(context.Background(), b.config.Timeout)
	defer cancel()

	var resp *Response
	var err error

	// lastCallErr 记录 provider.Complete 最近一次返回的原始错误。
	//
	// 不能依赖 retry.DoWithContext 的返回值来还原原始错误：
	//   - 不可重试早退路径（RetryIf=false）返回的是【裸错误】（无 %w 包装），
	//     对它调用 errors.Unwrap 得到 nil，会把真实原因（如 "invalid api key"）误丢。
	//   - 重试耗尽路径返回 fmt.Errorf("%w: %v", ErrMaxAttemptsReached, lastErr)，
	//     Unwrap 只能拿到 ErrMaxAttemptsReached 哨兵，原始原因（如 timeout）从错误链中丢失。
	// 当前 toolkit v0.0.6 的 retry 尚未提供 WithUnwrapFinalError/WithReturnLastError
	// 补偿选项，因此在本包内通过闭包捕获最近一次回调错误，保证原始失败原因不丢。
	var lastCallErr error

	// 使用 toolkit/util/retry 实现重试逻辑
	err = retry.DoWithContext(ctx, func() error {
		var callErr error
		resp, callErr = b.provider.Complete(ctx, pending.request)
		lastCallErr = callErr
		return callErr
	},
		retry.Attempts(b.config.MaxRetries+1),
		retry.Delay(b.config.RetryDelay),
		retry.DelayType(retry.LinearBackoff),
		retry.RetryIf(func(err error) bool { return isRetryableError(err) }),
	)
	// 归一化错误：优先保留 provider 返回的原始失败原因，
	// 仅在确实拿不到任何原始错误时才回退到 ErrBatchFailed 哨兵。
	if err != nil && resp == nil {
		switch {
		case lastCallErr != nil:
			err = lastCallErr
		default:
			err = fmt.Errorf("%w: %v", ErrBatchFailed, err)
		}
	}

	latency := time.Since(startTime)

	if resp == nil {
		resp = &Response{
			ID:    pending.request.ID,
			Error: err,
		}
	}
	resp.Latency = latency.Milliseconds()

	// 更新统计
	if err != nil {
		atomic.AddInt64(&b.stats.FailedRequests, 1)
		if b.config.OnError != nil {
			b.config.OnError(pending.request, err)
		}
	} else {
		atomic.AddInt64(&b.stats.SuccessRequests, 1)
		atomic.AddInt64(&b.stats.TotalTokens, int64(resp.TokensUsed))
	}

	b.stats.mu.Lock()
	b.stats.TotalLatency += latency
	b.stats.mu.Unlock()

	if b.config.OnRequestComplete != nil {
		b.config.OnRequestComplete(pending.request, resp)
	}

	// 发送响应
	pending.response <- resp
}

// Stats 获取统计信息
func (b *Batcher) GetStats() Stats {
	b.stats.mu.RLock()
	defer b.stats.mu.RUnlock()

	return Stats{
		TotalRequests:   atomic.LoadInt64(&b.stats.TotalRequests),
		SuccessRequests: atomic.LoadInt64(&b.stats.SuccessRequests),
		FailedRequests:  atomic.LoadInt64(&b.stats.FailedRequests),
		TotalBatches:    atomic.LoadInt64(&b.stats.TotalBatches),
		TotalTokens:     atomic.LoadInt64(&b.stats.TotalTokens),
		TotalLatency:    b.stats.TotalLatency,
	}
}

// ============== 辅助函数 ==============

var batchCounter int64

func generateBatchID() string {
	id := atomic.AddInt64(&batchCounter, 1)
	return fmt.Sprintf("batch-%d", id)
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	// 可重试的错误类型
	errStr := err.Error()
	return errors.Is(err, ErrRateLimited) ||
		errors.Is(err, ErrTimeout) ||
		containsAny(errStr, "timeout", "rate limit", "429", "503", "temporarily")
}

func containsAny(s string, substrs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range substrs {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

// ============== 批量执行辅助函数 ==============

// BatchComplete 批量补全（简化接口）
func BatchComplete(ctx context.Context, provider Provider, requests []*Request, config ...*Config) []*Response {
	batcher := NewBatcher(provider, config...)
	batcher.Start()
	defer batcher.Stop()

	return batcher.BatchSubmit(ctx, requests)
}

// ParallelComplete 并行补全（不使用批处理器）
func ParallelComplete(ctx context.Context, provider Provider, requests []*Request, maxConcurrent int) []*Response {
	results := make([]*Response, len(requests))

	if maxConcurrent <= 0 {
		maxConcurrent = 10
	}

	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for i, req := range requests {
		i, req := i, req
		wg.Add(1)
		go func() {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			startTime := time.Now()
			resp, err := provider.Complete(ctx, req)
			if resp == nil {
				resp = &Response{
					ID:    req.ID,
					Error: err,
				}
			}
			resp.Latency = time.Since(startTime).Milliseconds()
			results[i] = resp
		}()
	}

	wg.Wait()
	return results
}

// ============== 请求构建器 ==============

// RequestBuilder 请求构建器
type RequestBuilder struct {
	request *Request
}

// NewRequestBuilder 创建请求构建器
func NewRequestBuilder() *RequestBuilder {
	return &RequestBuilder{
		request: &Request{
			Metadata: make(map[string]any),
		},
	}
}

// WithID 设置 ID
func (rb *RequestBuilder) WithID(id string) *RequestBuilder {
	rb.request.ID = id
	return rb
}

// WithModel 设置模型
func (rb *RequestBuilder) WithModel(model string) *RequestBuilder {
	rb.request.Model = model
	return rb
}

// WithMaxTokens 设置最大 token 数
func (rb *RequestBuilder) WithMaxTokens(max int) *RequestBuilder {
	rb.request.MaxTokens = max
	return rb
}

// WithTemperature 设置温度
func (rb *RequestBuilder) WithTemperature(temp float64) *RequestBuilder {
	rb.request.Temperature = temp
	return rb
}

// AddSystemMessage 添加系统消息
func (rb *RequestBuilder) AddSystemMessage(content string) *RequestBuilder {
	rb.request.Messages = append(rb.request.Messages, Message{
		Role:    "system",
		Content: content,
	})
	return rb
}

// AddUserMessage 添加用户消息
func (rb *RequestBuilder) AddUserMessage(content string) *RequestBuilder {
	rb.request.Messages = append(rb.request.Messages, Message{
		Role:    "user",
		Content: content,
	})
	return rb
}

// AddAssistantMessage 添加助手消息
func (rb *RequestBuilder) AddAssistantMessage(content string) *RequestBuilder {
	rb.request.Messages = append(rb.request.Messages, Message{
		Role:    "assistant",
		Content: content,
	})
	return rb
}

// WithMetadata 设置元数据
func (rb *RequestBuilder) WithMetadata(key string, value any) *RequestBuilder {
	rb.request.Metadata[key] = value
	return rb
}

// Build 构建请求
func (rb *RequestBuilder) Build() *Request {
	return rb.request
}
