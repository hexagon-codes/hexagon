package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/rate"
	"github.com/hexagon-codes/toolkit/util/retry"
)

// ============== Push 通知 ==============

// PushNotification 推送通知内容
type PushNotification struct {
	// TaskID 任务 ID
	TaskID string `json:"taskId"`

	// Event 事件类型
	Event string `json:"event"`

	// Task 任务状态（完整状态）
	Task *Task `json:"task,omitempty"`

	// Status 任务状态（仅状态）
	Status *TaskStatus `json:"status,omitempty"`

	// Artifact 产物（仅产物事件）
	Artifact *Artifact `json:"artifact,omitempty"`

	// Timestamp 时间戳
	Timestamp time.Time `json:"timestamp"`
}

// NewTaskStatusNotification 创建任务状态通知
func NewTaskStatusNotification(task *Task) *PushNotification {
	return &PushNotification{
		TaskID:    task.ID,
		Event:     EventTypeTaskStatus,
		Task:      task,
		Status:    &task.Status,
		Timestamp: time.Now(),
	}
}

// NewArtifactNotification 创建产物通知
func NewArtifactNotification(taskID string, artifact *Artifact) *PushNotification {
	return &PushNotification{
		TaskID:    taskID,
		Event:     EventTypeArtifact,
		Artifact:  artifact,
		Timestamp: time.Now(),
	}
}

// ============== PushManager ==============

// PushManager 推送管理器
// 管理任务的推送配置和发送推送通知。
type PushManager struct {
	// service 推送服务
	service PushService

	// configs 推送配置 (taskID -> config)
	configs map[string]*PushNotificationConfig

	// retryConfig 重试配置
	retryConfig RetryConfig

	// rateLimiter 速率限制器（使用 toolkit 令牌桶实现）
	rateLimiter *rate.TokenBucket

	mu sync.RWMutex
}

// RetryConfig 重试配置
type RetryConfig struct {
	// MaxRetries 最大重试次数
	MaxRetries int

	// InitialDelay 初始延迟
	InitialDelay time.Duration

	// MaxDelay 最大延迟
	MaxDelay time.Duration

	// Multiplier 延迟乘数
	Multiplier float64
}

// DefaultRetryConfig 默认重试配置
var DefaultRetryConfig = RetryConfig{
	MaxRetries:   3,
	InitialDelay: 100 * time.Millisecond,
	MaxDelay:     5 * time.Second,
	Multiplier:   2.0,
}

// PushManagerOption 推送管理器选项
type PushManagerOption func(*PushManager)

// NewPushManager 创建推送管理器
func NewPushManager(service PushService, opts ...PushManagerOption) *PushManager {
	m := &PushManager{
		service:     service,
		configs:     make(map[string]*PushNotificationConfig),
		retryConfig: DefaultRetryConfig,
		rateLimiter: rate.NewTokenBucket(100, 100), // 默认 100 qps
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// WithRetryConfig 设置重试配置
func WithRetryConfig(config RetryConfig) PushManagerOption {
	return func(m *PushManager) {
		m.retryConfig = config
	}
}

// WithRateLimit 设置速率限制
// limit: 窗口内允许的最大请求数
// window: 时间窗口
func WithRateLimit(limit int, window time.Duration) PushManagerOption {
	return func(m *PushManager) {
		// 计算每秒令牌生成速率
		ratePerSec := float64(limit) / window.Seconds()
		m.rateLimiter = rate.NewTokenBucket(limit, ratePerSec)
	}
}

// SetConfig 设置推送配置
func (m *PushManager) SetConfig(taskID string, config *PushNotificationConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[taskID] = config
}

// GetConfig 获取推送配置
func (m *PushManager) GetConfig(taskID string) (*PushNotificationConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	config, exists := m.configs[taskID]
	return config, exists
}

// RemoveConfig 移除推送配置
func (m *PushManager) RemoveConfig(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.configs, taskID)
}

// Push 发送推送通知
func (m *PushManager) Push(ctx context.Context, taskID string, notification *PushNotification) error {
	m.mu.RLock()
	config, exists := m.configs[taskID]
	m.mu.RUnlock()

	if !exists || config == nil {
		return nil // 没有配置，跳过
	}

	// 速率限制
	if !m.rateLimiter.Allow() {
		return fmt.Errorf("rate limit exceeded")
	}

	// 带重试的推送
	return m.pushWithRetry(ctx, config, notification)
}

// PushTask 发送任务状态推送
func (m *PushManager) PushTask(ctx context.Context, task *Task) error {
	notification := NewTaskStatusNotification(task)
	return m.Push(ctx, task.ID, notification)
}

// PushArtifact 发送产物推送
func (m *PushManager) PushArtifact(ctx context.Context, taskID string, artifact *Artifact) error {
	notification := NewArtifactNotification(taskID, artifact)
	return m.Push(ctx, taskID, notification)
}

// pushWithRetry 带重试的推送
// 使用 toolkit/util/retry 实现指数退避重试
func (m *PushManager) pushWithRetry(ctx context.Context, config *PushNotificationConfig, notification *PushNotification) error {
	return retry.DoWithContext(ctx, func() error {
		return m.service.Push(ctx, config, notificationToTask(notification))
	},
		retry.Attempts(m.retryConfig.MaxRetries+1),
		retry.Delay(m.retryConfig.InitialDelay),
		retry.MaxDelay(m.retryConfig.MaxDelay),
		retry.Multiplier(m.retryConfig.Multiplier),
		retry.DelayType(retry.ExponentialBackoff),
	)
}

// notificationToTask 将通知转换为任务（用于推送服务）
func notificationToTask(n *PushNotification) *Task {
	if n.Task != nil {
		return n.Task
	}

	return &Task{
		ID:     n.TaskID,
		Status: *n.Status,
	}
}

// ============== WebhookPushService ==============

// WebhookPushService Webhook 推送服务
// 通过 HTTP POST 发送推送通知到配置的 URL。
type WebhookPushService struct {
	// httpClient HTTP 客户端
	httpClient *http.Client

	// defaultHeaders 默认请求头
	defaultHeaders map[string]string

	// signKey 签名密钥（用于请求签名）
	signKey string
}

// WebhookPushOption Webhook 推送选项
type WebhookPushOption func(*WebhookPushService)

// NewWebhookPushService 创建 Webhook 推送服务
func NewWebhookPushService(opts ...WebhookPushOption) *WebhookPushService {
	s := &WebhookPushService{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		defaultHeaders: map[string]string{
			"Content-Type": ContentTypeJSON,
			"User-Agent":   "Hexagon-A2A-Push/1.0",
		},
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// WithPushHTTPClient 设置 HTTP 客户端
func WithPushHTTPClient(client *http.Client) WebhookPushOption {
	return func(s *WebhookPushService) {
		s.httpClient = client
	}
}

// WithPushHeaders 设置默认请求头
func WithPushHeaders(headers map[string]string) WebhookPushOption {
	return func(s *WebhookPushService) {
		maps.Copy(s.defaultHeaders, headers)
	}
}

// WithPushSignKey 设置签名密钥
func WithPushSignKey(key string) WebhookPushOption {
	return func(s *WebhookPushService) {
		s.signKey = key
	}
}

// Push 发送推送通知
func (s *WebhookPushService) Push(ctx context.Context, config *PushNotificationConfig, task *Task) error {
	if config.URL == "" {
		return nil
	}

	// 序列化任务
	body, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal task failed: %w", err)
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}

	// 设置默认头
	for k, v := range s.defaultHeaders {
		req.Header.Set(k, v)
	}

	// 设置认证
	if config.Token != "" {
		req.Header.Set("Authorization", "Bearer "+config.Token)
	}

	// 如果有认证配置，使用它
	if config.Authentication != nil && config.Authentication.Credentials != "" {
		req.Header.Set("Authorization", config.Authentication.Credentials)
	}

	// 发送请求
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("push failed: %d %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// ============== 异步推送 ==============

// AsyncPushService 异步推送服务
// 在后台异步发送推送通知，不阻塞主流程。
type AsyncPushService struct {
	// underlying 底层推送服务
	underlying PushService

	// queue 推送队列
	queue chan *pushRequest

	// workers 工作协程数
	workers int

	// wg 等待组
	wg sync.WaitGroup

	// ctx 上下文
	ctx context.Context

	// cancel 取消函数
	cancel context.CancelFunc
}

// pushRequest 推送请求
type pushRequest struct {
	config *PushNotificationConfig
	task   *Task
}

// NewAsyncPushService 创建异步推送服务
func NewAsyncPushService(underlying PushService, queueSize, workers int) *AsyncPushService {
	ctx, cancel := context.WithCancel(context.Background())

	s := &AsyncPushService{
		underlying: underlying,
		queue:      make(chan *pushRequest, queueSize),
		workers:    workers,
		ctx:        ctx,
		cancel:     cancel,
	}

	// 启动工作协程
	for range workers {
		s.wg.Add(1)
		go s.worker()
	}

	return s
}

// Push 异步发送推送
func (s *AsyncPushService) Push(_ context.Context, config *PushNotificationConfig, task *Task) error {
	select {
	case s.queue <- &pushRequest{config: config, task: task}:
		return nil
	default:
		return fmt.Errorf("push queue full")
	}
}

// worker 工作协程
func (s *AsyncPushService) worker() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		case req, ok := <-s.queue:
			if !ok {
				return
			}
			// 忽略错误，异步推送失败不影响主流程
			_ = s.underlying.Push(s.ctx, req.config, req.task)
		}
	}
}

// Close 关闭异步推送服务
func (s *AsyncPushService) Close() {
	s.cancel()
	close(s.queue)
	s.wg.Wait()
}

// ============== 便捷函数 ==============

// NewDefaultPushService 创建默认推送服务
func NewDefaultPushService() PushService {
	return NewAsyncPushService(
		NewWebhookPushService(),
		1000, // 队列大小
		10,   // 工作协程数
	)
}
