package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/rate"
)

// ErrInvalidPushManagerConfig 表示推送管理器配置无效。
var ErrInvalidPushManagerConfig = errors.New("invalid push manager config")

// ErrInvalidPushNotification 表示推送通知无效。
var ErrInvalidPushNotification = errors.New("invalid push notification")

// ============== Push 通知 ==============

// PushNotification 表示构造后不可变的推送通知快照。
type PushNotification struct {
	taskID       string
	eventType    string
	taskPayload  json.RawMessage
	artifactData json.RawMessage
	timestamp    time.Time
}

// NewTaskStatusNotification 创建任务状态通知
func NewTaskStatusNotification(task *Task) (*PushNotification, error) {
	if task == nil {
		return nil, fmt.Errorf("%w: task must not be nil", ErrInvalidPushNotification)
	}
	if task.ID == "" {
		return nil, fmt.Errorf("%w: task ID must not be empty", ErrInvalidPushNotification)
	}

	payload, err := json.Marshal(task)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal task: %w", ErrInvalidPushNotification, err)
	}

	return &PushNotification{
		taskID:      task.ID,
		eventType:   EventTypeTaskStatus,
		taskPayload: payload,
		timestamp:   time.Now().UTC(),
	}, nil
}

// NewArtifactNotification 创建产物通知
func NewArtifactNotification(taskID string, artifact *Artifact) (*PushNotification, error) {
	if taskID == "" {
		return nil, fmt.Errorf("%w: task ID must not be empty", ErrInvalidPushNotification)
	}
	if artifact == nil {
		return nil, fmt.Errorf("%w: artifact must not be nil", ErrInvalidPushNotification)
	}

	payload, err := json.Marshal(artifact)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal artifact: %w", ErrInvalidPushNotification, err)
	}

	return &PushNotification{
		taskID:       taskID,
		eventType:    EventTypeArtifact,
		artifactData: payload,
		timestamp:    time.Now().UTC(),
	}, nil
}

// MarshalJSON 输出唯一的规范化通知结构。
func (n PushNotification) MarshalJSON() ([]byte, error) {
	type notificationWire struct {
		TaskID    string          `json:"taskId"`
		Event     string          `json:"event"`
		Task      json.RawMessage `json:"task,omitempty"`
		Artifact  json.RawMessage `json:"artifact,omitempty"`
		Timestamp time.Time       `json:"timestamp"`
	}

	return json.Marshal(notificationWire{
		TaskID:    n.taskID,
		Event:     n.eventType,
		Task:      append(json.RawMessage(nil), n.taskPayload...),
		Artifact:  append(json.RawMessage(nil), n.artifactData...),
		Timestamp: n.timestamp,
	})
}

// TaskID 返回通知所属任务 ID。
func (n *PushNotification) TaskID() string {
	if n == nil {
		return ""
	}
	return n.taskID
}

// EventType 返回通知事件类型。
func (n *PushNotification) EventType() string {
	if n == nil {
		return ""
	}
	return n.eventType
}

// Timestamp 返回通知创建时间。
func (n *PushNotification) Timestamp() time.Time {
	if n == nil {
		return time.Time{}
	}
	return n.timestamp
}

// Task 返回任务快照副本；非任务事件返回 nil。
func (n *PushNotification) Task() (*Task, error) {
	if n == nil {
		return nil, fmt.Errorf("%w: notification must not be nil", ErrInvalidPushNotification)
	}
	if len(n.taskPayload) == 0 {
		return nil, nil
	}

	var task Task
	if err := json.Unmarshal(n.taskPayload, &task); err != nil {
		return nil, fmt.Errorf("decode task notification: %w", err)
	}
	return &task, nil
}

// Artifact 返回产物快照副本；非产物事件返回 nil。
func (n *PushNotification) Artifact() (*Artifact, error) {
	if n == nil {
		return nil, fmt.Errorf("%w: notification must not be nil", ErrInvalidPushNotification)
	}
	if len(n.artifactData) == 0 {
		return nil, nil
	}

	var artifact Artifact
	if err := json.Unmarshal(n.artifactData, &artifact); err != nil {
		return nil, fmt.Errorf("decode artifact notification: %w", err)
	}
	return &artifact, nil
}

// PushService 发送规范化推送通知。
type PushService interface {
	Push(ctx context.Context, config *PushNotificationConfig, notification *PushNotification) error
}

// ============== PushManager ==============

// PushManager 推送管理器
// 管理任务的推送配置和发送推送通知。
type PushManager struct {
	// service 推送服务
	service PushService

	// configs 推送配置 (taskID -> config)
	configs map[string]*PushNotificationConfig

	// rateLimiter 速率限制器（使用 toolkit 令牌桶实现）
	rateLimiter *rate.TokenBucket

	mu sync.RWMutex
}

type pushManagerConfig struct {
	rateLimit  int
	rateWindow time.Duration
}

// PushManagerOption 推送管理器选项
type PushManagerOption func(*pushManagerConfig)

// NewPushManager 创建推送管理器，并在返回前集中校验最终配置。
func NewPushManager(service PushService, opts ...PushManagerOption) (*PushManager, error) {
	if isNilValue(service) {
		return nil, fmt.Errorf("%w: push service must not be nil", ErrInvalidPushManagerConfig)
	}

	config := pushManagerConfig{
		rateLimit:  100,
		rateWindow: time.Second,
	}

	for index, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("%w: option %d must not be nil", ErrInvalidPushManagerConfig, index)
		}
		opt(&config)
	}

	if err := config.validate(); err != nil {
		return nil, err
	}

	ratePerSec := float64(config.rateLimit) / config.rateWindow.Seconds()
	limiter, err := rate.NewTokenBucket(config.rateLimit, ratePerSec)
	if err != nil {
		return nil, fmt.Errorf("%w: create rate limiter: %w", ErrInvalidPushManagerConfig, err)
	}

	return &PushManager{
		service:     service,
		configs:     make(map[string]*PushNotificationConfig),
		rateLimiter: limiter,
	}, nil
}

// validate 校验所有选项应用后的最终配置。
func (c pushManagerConfig) validate() error {
	if c.rateLimit <= 0 {
		return fmt.Errorf("%w: rate limit %d: %w", ErrInvalidPushManagerConfig, c.rateLimit, rate.ErrInvalidCapacity)
	}
	if c.rateWindow <= 0 {
		return fmt.Errorf("%w: rate limit window %s: %w", ErrInvalidPushManagerConfig, c.rateWindow, rate.ErrInvalidWindow)
	}
	return nil
}

// isNilValue 仅对支持 nil 的动态类型调用 IsNil，避免反射 panic。
func isNilValue(value any) bool {
	if value == nil {
		return true
	}

	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// WithRateLimit 设置速率限制
// limit: 窗口内允许的最大请求数
// window: 时间窗口
func WithRateLimit(limit int, window time.Duration) PushManagerOption {
	return func(c *pushManagerConfig) {
		c.rateLimit = limit
		c.rateWindow = window
	}
}

// SetConfig 设置推送配置
func (m *PushManager) SetConfig(taskID string, config *PushNotificationConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[taskID] = clonePushNotificationConfig(config)
}

// GetConfig 获取推送配置
func (m *PushManager) GetConfig(taskID string) (*PushNotificationConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	config, exists := m.configs[taskID]
	return clonePushNotificationConfig(config), exists
}

// RemoveConfig 移除推送配置
func (m *PushManager) RemoveConfig(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.configs, taskID)
}

// Push 发送推送通知
func (m *PushManager) Push(ctx context.Context, notification *PushNotification) error {
	if ctx == nil {
		return fmt.Errorf("push context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if notification == nil {
		return fmt.Errorf("push notification must not be nil")
	}

	taskID := notification.TaskID()
	if taskID == "" {
		return fmt.Errorf("push notification task ID must not be empty")
	}

	m.mu.RLock()
	config, exists := m.configs[taskID]
	config = clonePushNotificationConfig(config)
	m.mu.RUnlock()

	if !exists || config == nil {
		return nil // 没有配置，跳过
	}

	// 速率限制
	if !m.rateLimiter.Allow() {
		return fmt.Errorf("rate limit exceeded")
	}

	return m.service.Push(ctx, clonePushNotificationConfig(config), notification)
}

// PushTask 发送任务状态推送
func (m *PushManager) PushTask(ctx context.Context, task *Task) error {
	notification, err := NewTaskStatusNotification(task)
	if err != nil {
		return err
	}
	return m.Push(ctx, notification)
}

// PushArtifact 发送产物推送
func (m *PushManager) PushArtifact(ctx context.Context, taskID string, artifact *Artifact) error {
	notification, err := NewArtifactNotification(taskID, artifact)
	if err != nil {
		return err
	}
	return m.Push(ctx, notification)
}

// clonePushNotificationConfig 复制配置及其嵌套可变字段。
func clonePushNotificationConfig(config *PushNotificationConfig) *PushNotificationConfig {
	if config == nil {
		return nil
	}

	cloned := *config
	if config.Authentication != nil {
		authentication := *config.Authentication
		authentication.Schemes = append([]string(nil), config.Authentication.Schemes...)
		cloned.Authentication = &authentication
	}
	return &cloned
}

// ============== WebhookPushService ==============

const maxWebhookRetries = 10

var (
	// ErrInvalidWebhookPushConfig 表示 Webhook 推送配置无效。
	ErrInvalidWebhookPushConfig = errors.New("invalid webhook push config")
	// ErrInvalidAsyncPushConfig 表示异步推送配置无效。
	ErrInvalidAsyncPushConfig = errors.New("invalid async push config")
	// ErrPushServiceClosed 表示异步推送服务已关闭。
	ErrPushServiceClosed = errors.New("push service closed")
	// ErrPushQueueFull 表示异步推送队列已满。
	ErrPushQueueFull = errors.New("push queue full")
)

// WebhookRetryConfig 定义 Webhook 的有限重试策略。
type WebhookRetryConfig struct {
	// MaxRetries 最大重试次数，不含首次请求。
	MaxRetries int
	// InitialDelay 首次重试前的等待时间。
	InitialDelay time.Duration
	// MaxDelay 单次退避等待上限。
	MaxDelay time.Duration
	// Multiplier 指数退避乘数。
	Multiplier float64
}

// DefaultWebhookRetryConfig 返回默认 Webhook 重试配置快照。
func DefaultWebhookRetryConfig() WebhookRetryConfig {
	return WebhookRetryConfig{
		MaxRetries:   3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		Multiplier:   2,
	}
}

// validate 校验 Webhook 重试配置及其资源上界。
func (c WebhookRetryConfig) validate() error {
	switch {
	case c.MaxRetries < 0:
		return fmt.Errorf("max retries must not be negative")
	case c.MaxRetries > maxWebhookRetries:
		return fmt.Errorf("max retries must not exceed %d", maxWebhookRetries)
	case c.InitialDelay < 0:
		return fmt.Errorf("initial delay must not be negative")
	case c.MaxDelay <= 0:
		return fmt.Errorf("max delay must be positive")
	case c.MaxDelay < c.InitialDelay:
		return fmt.Errorf("max delay must not be less than initial delay")
	case c.Multiplier <= 0 || math.IsNaN(c.Multiplier) || math.IsInf(c.Multiplier, 0):
		return fmt.Errorf("multiplier must be finite and positive")
	default:
		return nil
	}
}

type webhookPushConfig struct {
	httpClient     *http.Client
	defaultHeaders map[string]string
	retryConfig    WebhookRetryConfig
}

// WebhookPushService Webhook 推送服务
// 通过 HTTP POST 发送推送通知到配置的 URL。
type WebhookPushService struct {
	// httpClient HTTP 客户端
	httpClient *http.Client

	// defaultHeaders 默认请求头
	defaultHeaders map[string]string

	// retryConfig 有限重试配置
	retryConfig WebhookRetryConfig
}

// WebhookPushOption Webhook 推送选项
type WebhookPushOption func(*webhookPushConfig)

// NewWebhookPushService 创建 Webhook 推送服务
func NewWebhookPushService(opts ...WebhookPushOption) (*WebhookPushService, error) {
	config := webhookPushConfig{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		defaultHeaders: map[string]string{
			"Content-Type": ContentTypeJSON,
			"User-Agent":   "Hexagon-A2A-Push/1.0",
		},
		retryConfig: DefaultWebhookRetryConfig(),
	}

	for index, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("%w: option %d must not be nil", ErrInvalidWebhookPushConfig, index)
		}
		opt(&config)
	}
	if config.httpClient == nil {
		return nil, fmt.Errorf("%w: HTTP client must not be nil", ErrInvalidWebhookPushConfig)
	}
	if err := config.retryConfig.validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidWebhookPushConfig, err)
	}

	return &WebhookPushService{
		httpClient:     config.httpClient,
		defaultHeaders: maps.Clone(config.defaultHeaders),
		retryConfig:    config.retryConfig,
	}, nil
}

// WithPushHTTPClient 设置 HTTP 客户端
func WithPushHTTPClient(client *http.Client) WebhookPushOption {
	return func(config *webhookPushConfig) {
		config.httpClient = client
	}
}

// WithPushHeaders 设置默认请求头
func WithPushHeaders(headers map[string]string) WebhookPushOption {
	return func(config *webhookPushConfig) {
		maps.Copy(config.defaultHeaders, headers)
	}
}

// WithWebhookRetry 设置 Webhook 有限重试策略。
func WithWebhookRetry(retryConfig WebhookRetryConfig) WebhookPushOption {
	return func(config *webhookPushConfig) {
		config.retryConfig = retryConfig
	}
}

// Push 发送推送通知
func (s *WebhookPushService) Push(ctx context.Context, config *PushNotificationConfig, notification *PushNotification) error {
	if ctx == nil {
		return fmt.Errorf("push context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if config == nil {
		return fmt.Errorf("push config must not be nil")
	}
	if notification == nil {
		return fmt.Errorf("push notification must not be nil")
	}
	if config.URL == "" {
		return nil
	}

	// 序列化规范化通知
	body, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("marshal push notification: %w", err)
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

	// wg 等待组
	wg sync.WaitGroup

	// ctx 上下文
	ctx context.Context

	// cancel 取消函数
	cancel context.CancelFunc

	// stateMu 保护生命周期状态与入队线性化边界
	stateMu sync.RWMutex

	// state 当前生命周期状态
	state asyncPushState

	// closeOnce 保证关闭协议只执行一次
	closeOnce sync.Once

	// closed 在工作协程全部退出后关闭
	closed chan struct{}
}

type asyncPushState uint8

const (
	asyncPushStateRunning asyncPushState = iota
	asyncPushStateClosing
	asyncPushStateClosed
)

// pushRequest 推送请求
type pushRequest struct {
	config       *PushNotificationConfig
	notification *PushNotification
}

// NewAsyncPushService 创建异步推送服务
func NewAsyncPushService(underlying PushService, queueSize, workers int) (*AsyncPushService, error) {
	if isNilValue(underlying) {
		return nil, fmt.Errorf("%w: underlying service must not be nil", ErrInvalidAsyncPushConfig)
	}
	if queueSize <= 0 {
		return nil, fmt.Errorf("%w: queue size must be positive", ErrInvalidAsyncPushConfig)
	}
	if workers <= 0 {
		return nil, fmt.Errorf("%w: workers must be positive", ErrInvalidAsyncPushConfig)
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &AsyncPushService{
		underlying: underlying,
		queue:      make(chan *pushRequest, queueSize),
		ctx:        ctx,
		cancel:     cancel,
		state:      asyncPushStateRunning,
		closed:     make(chan struct{}),
	}

	// 启动工作协程
	s.wg.Add(workers)
	for range workers {
		go s.worker()
	}

	return s, nil
}

// Push 异步发送推送
func (s *AsyncPushService) Push(ctx context.Context, config *PushNotificationConfig, notification *PushNotification) error {
	if ctx == nil {
		return fmt.Errorf("push context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if config == nil {
		return fmt.Errorf("push config must not be nil")
	}
	if notification == nil {
		return fmt.Errorf("push notification must not be nil")
	}

	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.state != asyncPushStateRunning {
		return ErrPushServiceClosed
	}

	request := &pushRequest{
		config:       clonePushNotificationConfig(config),
		notification: notification,
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.queue <- request:
		return nil
	default:
		return ErrPushQueueFull
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
			_ = s.underlying.Push(s.ctx, req.config, req.notification)
		}
	}
}

// Close 关闭异步推送服务
func (s *AsyncPushService) Close() {
	if s == nil {
		return
	}

	s.closeOnce.Do(func() {
		s.stateMu.Lock()
		s.state = asyncPushStateClosing
		s.cancel()
		s.stateMu.Unlock()

		s.wg.Wait()

		// 服务关闭后清空未投递快照，及时释放其引用。
		for {
			select {
			case <-s.queue:
				continue
			default:
				s.stateMu.Lock()
				s.state = asyncPushStateClosed
				close(s.closed)
				s.stateMu.Unlock()
				return
			}
		}
	})

	<-s.closed
}

// ============== 便捷函数 ==============

// NewDefaultPushService 创建默认推送服务
func NewDefaultPushService() (*AsyncPushService, error) {
	webhook, err := NewWebhookPushService()
	if err != nil {
		return nil, fmt.Errorf("create default webhook push service: %w", err)
	}
	return NewAsyncPushService(
		webhook,
		1000, // 队列大小
		10,   // 工作协程数
	)
}
