package a2a

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ============== Client ==============

// Client A2A 客户端
// 提供与 A2A Agent 服务器通信的能力。
//
// 使用示例:
//
//	client := a2a.NewClient("http://localhost:8080")
//
//	// 获取 Agent Card
//	card, _ := client.GetAgentCard(ctx)
//
//	// 发送消息
//	task, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
//	    Message: a2a.NewUserMessage("你好"),
//	})
type Client struct {
	// baseURL 基础 URL
	baseURL string

	// httpClient HTTP 客户端
	httpClient *http.Client

	// auth 认证器
	auth Authenticator

	// requestID 请求 ID 计数器
	requestID atomic.Int64

	// closed 客户端是否已关闭
	closed atomic.Bool

	// userAgent User-Agent 头
	userAgent string

	// timeout 默认超时时间
	timeout time.Duration

	mu sync.RWMutex
}

// ClientOption 客户端选项
type ClientOption func(*Client)

// NewClient 创建 A2A 客户端
func NewClient(baseURL string, opts ...ClientOption) *Client {
	// 移除末尾斜杠
	baseURL = strings.TrimRight(baseURL, "/")

	c := &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		userAgent: "Hexagon-A2A-Client/1.0",
		timeout:   30 * time.Second,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// WithHTTPClient 设置 HTTP 客户端
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// WithAuth 设置认证器
func WithAuth(auth Authenticator) ClientOption {
	return func(c *Client) {
		c.auth = auth
	}
}

// WithUserAgent 设置 User-Agent
func WithUserAgent(userAgent string) ClientOption {
	return func(c *Client) {
		c.userAgent = userAgent
	}
}

// WithTimeout 设置默认超时时间
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.timeout = timeout
		c.httpClient.Timeout = timeout
	}
}

// Close 关闭客户端
func (c *Client) Close() error {
	c.closed.Store(true)
	return nil
}

// IsClosed 检查客户端是否已关闭
func (c *Client) IsClosed() bool {
	return c.closed.Load()
}

// ============== API 方法 ==============

// GetAgentCard 获取 Agent Card
func (c *Client) GetAgentCard(ctx context.Context) (*AgentCard, error) {
	if c.IsClosed() {
		return nil, ErrClientClosed
	}

	url := c.baseURL + PathAgentCard

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	req.Header.Set("Accept", ContentTypeJSON)
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.handleHTTPError(resp)
	}

	var card AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}

	return &card, nil
}

// SendMessage 发送消息（创建或继续任务）
func (c *Client) SendMessage(ctx context.Context, req *SendMessageRequest) (*Task, error) {
	if c.IsClosed() {
		return nil, ErrClientClosed
	}

	var task Task
	if err := c.callRPC(ctx, MethodSendMessage, req, &task); err != nil {
		return nil, err
	}

	return &task, nil
}

// SendMessageStream 流式发送消息
// 返回一个事件通道，调用者需要消费所有事件直到通道关闭。
func (c *Client) SendMessageStream(ctx context.Context, req *SendMessageRequest) (<-chan StreamEvent, error) {
	if c.IsClosed() {
		return nil, ErrClientClosed
	}

	url := c.baseURL + PathTaskSendStream

	// 创建 JSON-RPC 请求
	rpcReq, err := NewJSONRPCRequest(c.nextRequestID(), MethodSendMessageStream, req)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create http request failed: %w", err)
	}

	httpReq.Header.Set("Content-Type", ContentTypeJSON)
	httpReq.Header.Set("Accept", ContentTypeSSE)
	if c.userAgent != "" {
		httpReq.Header.Set("User-Agent", c.userAgent)
	}

	// 应用认证
	if c.auth != nil {
		if err := c.auth.Authenticate(httpReq); err != nil {
			return nil, fmt.Errorf("authentication failed: %w", err)
		}
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, c.handleHTTPError(resp)
	}

	// 检查 Content-Type
	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, ContentTypeSSE) {
		defer resp.Body.Close()
		return nil, fmt.Errorf("unexpected content type: %s", contentType)
	}

	// 创建事件通道
	events := make(chan StreamEvent)

	// 启动 goroutine 读取 SSE 事件
	go c.readSSEEvents(ctx, resp.Body, events)

	return events, nil
}

// GetTask 获取任务状态
func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	if c.IsClosed() {
		return nil, ErrClientClosed
	}

	req := &GetTaskRequest{ID: taskID}
	var task Task
	if err := c.callRPC(ctx, MethodGetTask, req, &task); err != nil {
		return nil, err
	}

	return &task, nil
}

// ListTasks 列出任务
func (c *Client) ListTasks(ctx context.Context, opts *ListTasksRequest) (*ListTasksResponse, error) {
	if c.IsClosed() {
		return nil, ErrClientClosed
	}

	if opts == nil {
		opts = &ListTasksRequest{}
	}

	var resp ListTasksResponse
	if err := c.callRPC(ctx, MethodListTasks, opts, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// CancelTask 取消任务
func (c *Client) CancelTask(ctx context.Context, taskID string) (*Task, error) {
	if c.IsClosed() {
		return nil, ErrClientClosed
	}

	req := &CancelTaskRequest{ID: taskID}
	var task Task
	if err := c.callRPC(ctx, MethodCancelTask, req, &task); err != nil {
		return nil, err
	}

	return &task, nil
}

// SetPushNotification 设置推送通知
func (c *Client) SetPushNotification(ctx context.Context, taskID string, config *PushNotificationConfig) error {
	if c.IsClosed() {
		return ErrClientClosed
	}

	req := &SetPushNotificationRequest{
		TaskID: taskID,
		Config: *config,
	}

	var resp SetPushNotificationResponse
	return c.callRPC(ctx, MethodSetPushNotification, req, &resp)
}

// GetPushNotification 获取推送配置
func (c *Client) GetPushNotification(ctx context.Context, taskID string) (*PushNotificationConfig, error) {
	if c.IsClosed() {
		return nil, ErrClientClosed
	}

	req := &GetPushNotificationRequest{TaskID: taskID}
	var resp GetPushNotificationResponse
	if err := c.callRPC(ctx, MethodGetPushNotification, req, &resp); err != nil {
		return nil, err
	}

	return resp.Config, nil
}

// Resubscribe 重新订阅任务事件流
func (c *Client) Resubscribe(ctx context.Context, taskID string) (<-chan StreamEvent, error) {
	if c.IsClosed() {
		return nil, ErrClientClosed
	}

	url := c.baseURL + PathTaskResubscribe

	req := &ResubscribeRequest{TaskID: taskID}
	rpcReq, err := NewJSONRPCRequest(c.nextRequestID(), MethodResubscribe, req)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create http request failed: %w", err)
	}

	httpReq.Header.Set("Content-Type", ContentTypeJSON)
	httpReq.Header.Set("Accept", ContentTypeSSE)
	if c.userAgent != "" {
		httpReq.Header.Set("User-Agent", c.userAgent)
	}

	if c.auth != nil {
		if err := c.auth.Authenticate(httpReq); err != nil {
			return nil, fmt.Errorf("authentication failed: %w", err)
		}
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, c.handleHTTPError(resp)
	}

	events := make(chan StreamEvent)
	go c.readSSEEvents(ctx, resp.Body, events)

	return events, nil
}

// ============== 内部方法 ==============

// nextRequestID 生成下一个请求 ID
func (c *Client) nextRequestID() int64 {
	return c.requestID.Add(1)
}

// callRPC 调用 JSON-RPC 方法
func (c *Client) callRPC(ctx context.Context, method string, params, result any) error {
	url := c.baseURL + PathTasks

	// 创建 JSON-RPC 请求
	rpcReq, err := NewJSONRPCRequest(c.nextRequestID(), method, params)
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return fmt.Errorf("marshal request failed: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create http request failed: %w", err)
	}

	httpReq.Header.Set("Content-Type", ContentTypeJSON)
	httpReq.Header.Set("Accept", ContentTypeJSON)
	if c.userAgent != "" {
		httpReq.Header.Set("User-Agent", c.userAgent)
	}

	// 应用认证
	if c.auth != nil {
		if err := c.auth.Authenticate(httpReq); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.handleHTTPError(resp)
	}

	// 解析 JSON-RPC 响应
	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return fmt.Errorf("decode response failed: %w", err)
	}

	// 检查错误
	if rpcResp.Error != nil {
		return rpcResp.Error
	}

	// 解析结果
	if result != nil && rpcResp.Result != nil {
		if err := json.Unmarshal(rpcResp.Result, result); err != nil {
			return fmt.Errorf("decode result failed: %w", err)
		}
	}

	return nil
}

// handleHTTPError 处理 HTTP 错误
func (c *Client) handleHTTPError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)

	// 尝试解析为 JSON-RPC 错误响应
	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err == nil && rpcResp.Error != nil {
		return rpcResp.Error
	}

	// 返回 HTTP 错误
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
}

// readSSEEvents 读取 SSE 事件
func (c *Client) readSSEEvents(ctx context.Context, body io.ReadCloser, events chan<- StreamEvent) {
	defer body.Close()
	defer close(events)

	scanner := bufio.NewScanner(body)
	var eventType string
	var data strings.Builder

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			// context 取消时尝试发送错误事件，但不阻塞
			select {
			case events <- &ErrorEvent{Error: NewInternalError(ctx.Err().Error())}:
			default:
			}
			return
		default:
		}

		line := scanner.Text()

		// 空行表示事件结束
		if line == "" {
			if eventType != "" && data.Len() > 0 {
				event := c.parseSSEEvent(eventType, data.String())
				if event != nil {
					// 发送事件时检查 context 是否已取消，防止消费者停止消费后 goroutine 阻塞
					select {
					case <-ctx.Done():
						return
					case events <- event:
					}
				}

				// 如果是完成事件，退出
				if eventType == EventTypeDone {
					return
				}
			}
			eventType = ""
			data.Reset()
			continue
		}

		// 解析 SSE 字段
		if value, found := strings.CutPrefix(line, "event:"); found {
			eventType = strings.TrimSpace(value)
		} else if value, found := strings.CutPrefix(line, "data:"); found {
			if data.Len() > 0 {
				data.WriteString("\n")
			}
			data.WriteString(value)
		}
	}

	if err := scanner.Err(); err != nil {
		events <- &ErrorEvent{Error: NewInternalError(err.Error())}
	}
}

// parseSSEEvent 解析 SSE 事件
func (c *Client) parseSSEEvent(eventType, data string) StreamEvent {
	data = strings.TrimSpace(data)

	switch eventType {
	case EventTypeTaskStatus:
		var event TaskStatusEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return &ErrorEvent{Error: NewParseError(err.Error())}
		}
		return &event

	case EventTypeArtifact:
		var event ArtifactEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return &ErrorEvent{Error: NewParseError(err.Error())}
		}
		return &event

	case EventTypeError:
		var event ErrorEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return &ErrorEvent{Error: NewParseError(err.Error())}
		}
		return &event

	case EventTypeDone:
		var event DoneEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			// done 事件可能没有数据
			return &DoneEvent{}
		}
		return &event

	default:
		return nil
	}
}

// ============== Authenticator 接口 ==============

// Authenticator 认证器接口
type Authenticator interface {
	// Authenticate 对请求进行认证
	Authenticate(req *http.Request) error
}

// BearerAuth Bearer Token 认证
type BearerAuth struct {
	Token string
}

// Authenticate 实现认证
func (a *BearerAuth) Authenticate(req *http.Request) error {
	req.Header.Set("Authorization", "Bearer "+a.Token)
	return nil
}

// APIKeyAuth API Key 认证
type APIKeyAuth struct {
	// Key API Key 名称
	Key string

	// Value API Key 值
	Value string

	// In 位置 (header, query)
	In string
}

// Authenticate 实现认证
func (a *APIKeyAuth) Authenticate(req *http.Request) error {
	switch a.In {
	case "query":
		q := req.URL.Query()
		q.Set(a.Key, a.Value)
		req.URL.RawQuery = q.Encode()
	default: // header
		req.Header.Set(a.Key, a.Value)
	}
	return nil
}

// BasicAuth 基本认证
type BasicAuth struct {
	Username string
	Password string
}

// Authenticate 实现认证
func (a *BasicAuth) Authenticate(req *http.Request) error {
	req.SetBasicAuth(a.Username, a.Password)
	return nil
}
