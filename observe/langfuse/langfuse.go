// Package langfuse 提供 Langfuse 可观测平台集成
//
// Langfuse 是开源的 LLM 可观测平台，用于追踪和监控 LLM 应用。
// 本包提供 Langfuse 旧版事件摄取 API 的手工客户端，支持 Trace、Span、Generation
// 事件与异步批量上报。新接入优先使用 observe/otel.NewLangfuseExporter，通过
// OTLP 与 Hook Manager 统一采集；本包不会自动注册 Hexagon Hook。
//
// 使用示例：
//
//	client := NewClient(
//	    WithPublicKey("pk-xxx"),
//	    WithSecretKey("sk-xxx"),
//	    WithHost("https://cloud.langfuse.com"),
//	)
//	defer client.Close()
//
//	client.TraceStart("trace-1", "agent.run", input, []string{"demo"})
//	client.GenerationStart("generation-1", "trace-1", "", "llm.call", "gpt-4o", input)
//	client.GenerationEnd("generation-1", "trace-1", output, &UsageBody{
//	    Input:  100,
//	    Output: 50,
//	    Total:  150,
//	})
package langfuse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/idgen"
)

// Client Langfuse 客户端
// 负责收集和上报追踪数据到 Langfuse 平台
type Client struct {
	publicKey     string
	secretKey     string
	host          string
	httpClient    *http.Client
	events        []Event
	mu            sync.Mutex
	flushSize     int           // 批量上报大小
	flushInterval time.Duration // 上报间隔
	done          chan struct{}
}

// ClientOption 客户端配置选项
type ClientOption func(*Client)

// WithPublicKey 设置公钥
func WithPublicKey(key string) ClientOption {
	return func(c *Client) {
		c.publicKey = key
	}
}

// WithSecretKey 设置密钥
func WithSecretKey(key string) ClientOption {
	return func(c *Client) {
		c.secretKey = key
	}
}

// WithHost 设置 Langfuse 服务地址
func WithHost(host string) ClientOption {
	return func(c *Client) {
		c.host = host
	}
}

// WithFlushSize 设置批量上报大小
func WithFlushSize(size int) ClientOption {
	return func(c *Client) {
		c.flushSize = size
	}
}

// WithFlushInterval 设置上报间隔
func WithFlushInterval(interval time.Duration) ClientOption {
	return func(c *Client) {
		c.flushInterval = interval
	}
}

// NewClient 创建 Langfuse 客户端
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		host:          "https://cloud.langfuse.com",
		httpClient:    &http.Client{Timeout: 10 * time.Second},
		events:        make([]Event, 0, 100),
		flushSize:     50,
		flushInterval: 5 * time.Second,
		done:          make(chan struct{}),
	}
	for _, opt := range opts {
		opt(c)
	}

	// 启动后台上报协程
	go c.backgroundFlush()

	return c
}

// Event Langfuse 事件
type Event struct {
	// Type 事件类型
	Type EventType `json:"type"`

	// Body 事件数据
	Body any `json:"body"`

	// ID 事件 ID
	ID string `json:"id"`

	// Timestamp 时间戳
	Timestamp time.Time `json:"timestamp"`
}

// EventType 事件类型
type EventType string

const (
	// EventTypeTraceCreate 创建追踪
	EventTypeTraceCreate EventType = "trace-create"

	// EventTypeSpanCreate 创建 Span
	EventTypeSpanCreate EventType = "span-create"

	// EventTypeSpanUpdate 更新 Span
	EventTypeSpanUpdate EventType = "span-update"

	// EventTypeGenerationCreate 创建 LLM 调用记录
	EventTypeGenerationCreate EventType = "generation-create"

	// EventTypeGenerationUpdate 更新 LLM 调用记录
	EventTypeGenerationUpdate EventType = "generation-update"
)

// TraceBody 追踪数据
type TraceBody struct {
	ID       string         `json:"id"`
	Name     string         `json:"name,omitempty"`
	Input    any            `json:"input,omitempty"`
	Output   any            `json:"output,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Tags     []string       `json:"tags,omitempty"`
}

// SpanBody Span 数据
type SpanBody struct {
	ID        string         `json:"id"`
	TraceID   string         `json:"traceId"`
	ParentID  string         `json:"parentObservationId,omitempty"`
	Name      string         `json:"name"`
	StartTime time.Time      `json:"startTime"`
	EndTime   *time.Time     `json:"endTime,omitempty"`
	Input     any            `json:"input,omitempty"`
	Output    any            `json:"output,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Level     string         `json:"level,omitempty"`
	StatusMsg string         `json:"statusMessage,omitempty"`
}

// GenerationBody LLM 调用数据
type GenerationBody struct {
	ID              string         `json:"id"`
	TraceID         string         `json:"traceId"`
	ParentID        string         `json:"parentObservationId,omitempty"`
	Name            string         `json:"name"`
	StartTime       time.Time      `json:"startTime"`
	EndTime         *time.Time     `json:"endTime,omitempty"`
	Model           string         `json:"model,omitempty"`
	ModelParameters map[string]any `json:"modelParameters,omitempty"`
	Input           any            `json:"input,omitempty"`
	Output          any            `json:"output,omitempty"`
	Usage           *UsageBody     `json:"usage,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	CompletionStart *time.Time     `json:"completionStartTime,omitempty"`
	Level           string         `json:"level,omitempty"`
}

// UsageBody Token 使用数据
type UsageBody struct {
	Input  int `json:"input,omitempty"`
	Output int `json:"output,omitempty"`
	Total  int `json:"total,omitempty"`
}

// ============== 追踪方法 ==============

// TraceStart 开始追踪
func (c *Client) TraceStart(id, name string, input any, tags []string) {
	c.addEvent(Event{
		Type: EventTypeTraceCreate,
		ID:   id,
		Body: TraceBody{
			ID:    id,
			Name:  name,
			Input: input,
			Tags:  tags,
		},
		Timestamp: time.Now(),
	})
}

// SpanStart 开始 Span
func (c *Client) SpanStart(id, traceID, parentID, name string, input any) {
	c.addEvent(Event{
		Type: EventTypeSpanCreate,
		ID:   id,
		Body: SpanBody{
			ID:        id,
			TraceID:   traceID,
			ParentID:  parentID,
			Name:      name,
			StartTime: time.Now(),
			Input:     input,
		},
		Timestamp: time.Now(),
	})
}

// SpanEnd 结束 Span
func (c *Client) SpanEnd(id, traceID string, output any, level string) {
	now := time.Now()
	c.addEvent(Event{
		Type: EventTypeSpanUpdate,
		ID:   id,
		Body: SpanBody{
			ID:      id,
			TraceID: traceID,
			EndTime: &now,
			Output:  output,
			Level:   level,
		},
		Timestamp: now,
	})
}

// GenerationStart 开始 LLM 调用记录
func (c *Client) GenerationStart(id, traceID, parentID, name, model string, input any) {
	c.addEvent(Event{
		Type: EventTypeGenerationCreate,
		ID:   id,
		Body: GenerationBody{
			ID:        id,
			TraceID:   traceID,
			ParentID:  parentID,
			Name:      name,
			Model:     model,
			StartTime: time.Now(),
			Input:     input,
		},
		Timestamp: time.Now(),
	})
}

// GenerationEnd 结束 LLM 调用记录
func (c *Client) GenerationEnd(id, traceID string, output any, usage *UsageBody) {
	now := time.Now()
	c.addEvent(Event{
		Type: EventTypeGenerationUpdate,
		ID:   id,
		Body: GenerationBody{
			ID:      id,
			TraceID: traceID,
			EndTime: &now,
			Output:  output,
			Usage:   usage,
		},
		Timestamp: now,
	})
}

// ============== 内部方法 ==============

// addEvent 添加事件到缓冲区
func (c *Client) addEvent(event Event) {
	c.mu.Lock()
	c.events = append(c.events, event)
	shouldFlush := len(c.events) >= c.flushSize
	c.mu.Unlock()

	if shouldFlush {
		go c.Flush()
	}
}

// Flush 发送所有缓冲的事件到 Langfuse
func (c *Client) Flush() {
	c.mu.Lock()
	if len(c.events) == 0 {
		c.mu.Unlock()
		return
	}
	events := c.events
	c.events = make([]Event, 0, c.flushSize)
	c.mu.Unlock()

	c.sendBatch(events)
}

// sendBatch 批量发送事件
func (c *Client) sendBatch(events []Event) {
	body := map[string]any{
		"batch": events,
	}

	data, err := json.Marshal(body)
	if err != nil {
		fmt.Printf("[langfuse] 序列化事件失败: %v\n", err)
		return
	}

	url := c.host + "/api/public/ingestion"
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		fmt.Printf("[langfuse] 创建请求失败: %v\n", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.publicKey, c.secretKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		fmt.Printf("[langfuse] 发送事件失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		fmt.Printf("[langfuse] 上报失败，状态码: %d\n", resp.StatusCode)
	}
}

// backgroundFlush 后台定时上报
func (c *Client) backgroundFlush() {
	ticker := time.NewTicker(c.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.Flush()
		case <-c.done:
			c.Flush() // 最后一次上报
			return
		}
	}
}

// Close 关闭客户端，发送剩余事件
func (c *Client) Close() {
	close(c.done)
}

// ============== Hooks 集成 ==============

// LLMHookFunc 返回可注册到 Hooks 系统的 LLM 钩子函数
// 返回 (onStart, onEnd) 两个函数
func (c *Client) LLMHookFunc() (func(ctx context.Context, traceID, model string, input any), func(ctx context.Context, traceID string, output any, inputTokens, outputTokens int)) {
	onStart := func(ctx context.Context, traceID, model string, input any) {
		c.GenerationStart(
			fmt.Sprintf("gen-%s", idgen.NanoID()),
			traceID, "", "llm-call", model, input,
		)
	}

	onEnd := func(ctx context.Context, traceID string, output any, inputTokens, outputTokens int) {
		c.GenerationEnd(
			fmt.Sprintf("gen-%s", idgen.NanoID()),
			traceID, output,
			&UsageBody{
				Input:  inputTokens,
				Output: outputTokens,
				Total:  inputTokens + outputTokens,
			},
		)
	}

	return onStart, onEnd
}
