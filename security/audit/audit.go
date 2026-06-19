// Package audit 提供 Hexagon AI Agent 框架的审计日志
//
// 支持操作审计记录、敏感操作监控、审计日志查询等功能。
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hexagon-codes/hexagon/internal/util"
)

// AuditLogger 审计日志记录器
type AuditLogger struct {
	// Store 存储后端
	store AuditStore

	// Writers 日志写入器
	writers []io.Writer

	// Filters 事件过滤器
	filters []EventFilter

	// Hooks 事件钩子
	hooks []EventHook

	// Config 配置
	config AuditConfig

	// Buffer 缓冲区
	buffer chan *AuditEvent

	// Running 运行状态
	running bool

	// bufferOverflowCount 缓冲区溢出计数（用于监控）
	bufferOverflowCount int64

	// storeFailCount 存储写入失败计数（用于监控审计日志丢失情况）
	storeFailCount int64

	mu sync.RWMutex
}

// AuditConfig 审计配置
type AuditConfig struct {
	// Enabled 是否启用
	Enabled bool

	// BufferSize 缓冲区大小
	BufferSize int

	// FlushInterval 刷新间隔
	FlushInterval time.Duration

	// RetentionDays 保留天数
	RetentionDays int

	// LogLevel 日志级别
	LogLevel AuditLevel

	// SensitiveFields 敏感字段（需要脱敏）
	SensitiveFields []string

	// IncludeDetails 是否包含详细信息
	IncludeDetails bool
}

// DefaultAuditConfig 默认配置
func DefaultAuditConfig() AuditConfig {
	return AuditConfig{
		Enabled:         true,
		BufferSize:      1000,
		FlushInterval:   5 * time.Second,
		RetentionDays:   90,
		LogLevel:        LevelInfo,
		SensitiveFields: []string{"password", "token", "secret", "api_key"},
		IncludeDetails:  true,
	}
}

// AuditOption 审计选项
type AuditOption func(*AuditConfig)

// NewAuditLogger 创建审计日志记录器
func NewAuditLogger(opts ...AuditOption) *AuditLogger {
	config := DefaultAuditConfig()
	for _, opt := range opts {
		opt(&config)
	}

	logger := &AuditLogger{
		store:   NewMemoryAuditStore(),
		writers: []io.Writer{os.Stdout},
		filters: make([]EventFilter, 0),
		hooks:   make([]EventHook, 0),
		config:  config,
		buffer:  make(chan *AuditEvent, config.BufferSize),
	}

	return logger
}

// WithAuditEnabled 设置启用状态
func WithAuditEnabled(enabled bool) AuditOption {
	return func(c *AuditConfig) {
		c.Enabled = enabled
	}
}

// WithAuditLevel 设置日志级别
func WithAuditLevel(level AuditLevel) AuditOption {
	return func(c *AuditConfig) {
		c.LogLevel = level
	}
}

// WithRetentionDays 设置保留天数
func WithRetentionDays(days int) AuditOption {
	return func(c *AuditConfig) {
		c.RetentionDays = days
	}
}

// SetStore 设置存储
func (l *AuditLogger) SetStore(store AuditStore) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.store = store
}

// AddWriter 添加写入器
func (l *AuditLogger) AddWriter(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writers = append(l.writers, w)
}

// AddFilter 添加过滤器
func (l *AuditLogger) AddFilter(filter EventFilter) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.filters = append(l.filters, filter)
}

// AddHook 添加钩子
func (l *AuditLogger) AddHook(hook EventHook) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hooks = append(l.hooks, hook)
}

// Start 启动审计日志
func (l *AuditLogger) Start(ctx context.Context) {
	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return
	}
	l.running = true
	l.mu.Unlock()

	go l.processLoop(ctx)
}

// Stop 停止审计日志
//
// 在锁内同时设置 running=false 和关闭 buffer，确保原子性。
// 这样 Log() 方法在检查 running 状态时不会向已关闭的 channel 发送。
func (l *AuditLogger) Stop() {
	l.mu.Lock()
	if !l.running {
		l.mu.Unlock()
		return
	}
	l.running = false
	close(l.buffer)
	l.mu.Unlock()
}

// processLoop 处理循环
func (l *AuditLogger) processLoop(ctx context.Context) {
	ticker := time.NewTicker(l.config.FlushInterval)
	defer ticker.Stop()

	batch := make([]*AuditEvent, 0, 100)

	for {
		select {
		case <-ctx.Done():
			// 刷新剩余事件
			if len(batch) > 0 {
				l.flush(batch)
			}
			return
		case event, ok := <-l.buffer:
			if !ok {
				if len(batch) > 0 {
					l.flush(batch)
				}
				return
			}
			batch = append(batch, event)
			if len(batch) >= 100 {
				l.flush(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				l.flush(batch)
				batch = batch[:0]
			}
		}
	}
}

// flush 刷新事件
func (l *AuditLogger) flush(events []*AuditEvent) {
	for _, event := range events {
		// 写入存储
		if l.store != nil {
			if err := l.store.Save(context.Background(), event); err != nil {
				fmt.Fprintf(os.Stderr, "[WARN] audit: failed to save event %s to store: %v\n", event.ID, err)
			}
		}

		// 写入 writers
		l.mu.RLock()
		writers := l.writers
		l.mu.RUnlock()

		data, err := json.Marshal(event)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] audit: failed to marshal event %s: %v\n", event.ID, err)
			continue
		}
		for _, w := range writers {
			if _, err := w.Write(data); err != nil {
				fmt.Fprintf(os.Stderr, "[WARN] audit: failed to write event %s: %v\n", event.ID, err)
			}
			w.Write([]byte("\n"))
		}
	}
}

// ============== Audit Event ==============

// AuditEvent 审计事件
type AuditEvent struct {
	// ID 事件 ID
	ID string `json:"id"`

	// Timestamp 时间戳
	Timestamp time.Time `json:"timestamp"`

	// Level 级别
	Level AuditLevel `json:"level"`

	// Category 类别
	Category EventCategory `json:"category"`

	// Action 操作
	Action string `json:"action"`

	// Actor 执行者
	Actor *Actor `json:"actor"`

	// Target 目标
	Target *Target `json:"target,omitempty"`

	// Result 结果
	Result EventResult `json:"result"`

	// Details 详细信息
	Details map[string]any `json:"details,omitempty"`

	// Request 请求信息
	Request *RequestInfo `json:"request,omitempty"`

	// Response 响应信息
	Response *ResponseInfo `json:"response,omitempty"`

	// Duration 耗时
	Duration time.Duration `json:"duration,omitempty"`

	// TraceID 追踪 ID
	TraceID string `json:"trace_id,omitempty"`

	// SpanID Span ID
	SpanID string `json:"span_id,omitempty"`

	// Tags 标签
	Tags []string `json:"tags,omitempty"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// AuditLevel 审计级别
type AuditLevel string

const (
	LevelDebug    AuditLevel = "debug"
	LevelInfo     AuditLevel = "info"
	LevelWarning  AuditLevel = "warning"
	LevelError    AuditLevel = "error"
	LevelCritical AuditLevel = "critical"
)

// EventCategory 事件类别
type EventCategory string

const (
	CategoryAuth     EventCategory = "auth"
	CategoryAgent    EventCategory = "agent"
	CategoryTool     EventCategory = "tool"
	CategoryLLM      EventCategory = "llm"
	CategoryMemory   EventCategory = "memory"
	CategoryWorkflow EventCategory = "workflow"
	CategoryNetwork  EventCategory = "network"
	CategorySecurity EventCategory = "security"
	CategoryConfig   EventCategory = "config"
	CategoryAdmin    EventCategory = "admin"
)

// EventResult 事件结果
type EventResult string

const (
	ResultSuccess EventResult = "success"
	ResultFailure EventResult = "failure"
	ResultDenied  EventResult = "denied"
	ResultError   EventResult = "error"
)

// Actor 执行者
type Actor struct {
	// Type 类型（user/agent/system）
	Type string `json:"type"`

	// ID 标识
	ID string `json:"id"`

	// Name 名称
	Name string `json:"name"`

	// IP IP 地址
	IP string `json:"ip,omitempty"`

	// UserAgent 用户代理
	UserAgent string `json:"user_agent,omitempty"`

	// Roles 角色
	Roles []string `json:"roles,omitempty"`
}

// Target 目标
type Target struct {
	// Type 类型
	Type string `json:"type"`

	// ID 标识
	ID string `json:"id"`

	// Name 名称
	Name string `json:"name,omitempty"`

	// Attributes 属性
	Attributes map[string]any `json:"attributes,omitempty"`
}

// RequestInfo 请求信息
type RequestInfo struct {
	// Method 方法
	Method string `json:"method,omitempty"`

	// Path 路径
	Path string `json:"path,omitempty"`

	// Query 查询参数
	Query map[string]string `json:"query,omitempty"`

	// Headers 请求头
	Headers map[string]string `json:"headers,omitempty"`

	// Body 请求体（脱敏后）
	Body any `json:"body,omitempty"`
}

// ResponseInfo 响应信息
type ResponseInfo struct {
	// StatusCode 状态码
	StatusCode int `json:"status_code,omitempty"`

	// Body 响应体（脱敏后）
	Body any `json:"body,omitempty"`

	// Error 错误信息
	Error string `json:"error,omitempty"`
}

// ============== Logging Methods ==============

// Log 记录审计事件
func (l *AuditLogger) Log(event *AuditEvent) {
	if !l.config.Enabled {
		return
	}

	// 检查级别
	if !l.shouldLog(event.Level) {
		return
	}

	// 应用过滤器
	l.mu.RLock()
	filters := l.filters
	l.mu.RUnlock()

	for _, filter := range filters {
		if !filter(event) {
			return
		}
	}

	// 设置默认值
	if event.ID == "" {
		event.ID = util.GenerateID("audit")
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// 脱敏处理
	l.sanitize(event)

	// 触发钩子
	l.mu.RLock()
	hooks := l.hooks
	l.mu.RUnlock()

	for _, hook := range hooks {
		hook(event)
	}

	// 发送到缓冲区
	// 在锁内检查 running 状态并发送，防止向已关闭的 channel 发送（TOCTOU 防护）
	l.mu.RLock()
	running := l.running
	if !running {
		l.mu.RUnlock()
		// 审计已停止，直接同步写入
		l.writeEventDirect(event)
		return
	}
	// 在持有读锁的情况下发送，确保 Stop() 不会在此期间关闭 buffer
	select {
	case l.buffer <- event:
		l.mu.RUnlock()
		// 成功加入缓冲区
	default:
		l.mu.RUnlock()
		// 缓冲区满，同步写入以确保审计事件不丢失
		// 审计日志的完整性比性能更重要
		l.mu.Lock()
		l.bufferOverflowCount++
		l.mu.Unlock()

		// 直接同步写入存储与 writers。
		// writeEventDirect 内部负责落库 store(失败时累加 storeFailCount 并写 stderr)
		// 与写入 writers, 与未启动路径保持一致的落库语义(W3-46)。
		l.writeEventDirect(event)
	}
}

// LogAuth 记录认证事件
func (l *AuditLogger) LogAuth(actor *Actor, action string, result EventResult, details map[string]any) {
	l.Log(&AuditEvent{
		Level:    LevelInfo,
		Category: CategoryAuth,
		Action:   action,
		Actor:    actor,
		Result:   result,
		Details:  details,
	})
}

// LogAgent 记录 Agent 事件
func (l *AuditLogger) LogAgent(actor *Actor, target *Target, action string, result EventResult, duration time.Duration, details map[string]any) {
	l.Log(&AuditEvent{
		Level:    LevelInfo,
		Category: CategoryAgent,
		Action:   action,
		Actor:    actor,
		Target:   target,
		Result:   result,
		Duration: duration,
		Details:  details,
	})
}

// LogTool 记录工具调用事件
func (l *AuditLogger) LogTool(actor *Actor, toolName string, result EventResult, duration time.Duration, details map[string]any) {
	l.Log(&AuditEvent{
		Level:    LevelInfo,
		Category: CategoryTool,
		Action:   "execute",
		Actor:    actor,
		Target:   &Target{Type: "tool", ID: toolName, Name: toolName},
		Result:   result,
		Duration: duration,
		Details:  details,
	})
}

// LogLLM 记录 LLM 调用事件
func (l *AuditLogger) LogLLM(actor *Actor, provider, model string, result EventResult, duration time.Duration, tokenUsage map[string]int) {
	l.Log(&AuditEvent{
		Level:    LevelInfo,
		Category: CategoryLLM,
		Action:   "call",
		Actor:    actor,
		Target:   &Target{Type: "llm", ID: provider, Name: model},
		Result:   result,
		Duration: duration,
		Details:  map[string]any{"token_usage": tokenUsage},
	})
}

// LogSecurity 记录安全事件
func (l *AuditLogger) LogSecurity(level AuditLevel, actor *Actor, action string, result EventResult, details map[string]any) {
	l.Log(&AuditEvent{
		Level:    level,
		Category: CategorySecurity,
		Action:   action,
		Actor:    actor,
		Result:   result,
		Details:  details,
	})
}

// LogError 记录错误事件
func (l *AuditLogger) LogError(category EventCategory, actor *Actor, action string, err error, details map[string]any) {
	if details == nil {
		details = make(map[string]any)
	}
	details["error"] = err.Error()

	l.Log(&AuditEvent{
		Level:    LevelError,
		Category: category,
		Action:   action,
		Actor:    actor,
		Result:   ResultError,
		Details:  details,
	})
}

// shouldLog 检查是否应该记录
func (l *AuditLogger) shouldLog(level AuditLevel) bool {
	levels := map[AuditLevel]int{
		LevelDebug:    0,
		LevelInfo:     1,
		LevelWarning:  2,
		LevelError:    3,
		LevelCritical: 4,
	}

	return levels[level] >= levels[l.config.LogLevel]
}

// writeEventDirect 直接写入事件（用于未启动或缓冲区溢出时的同步写入）
//
// 此方法绕过缓冲区，直接将事件同时写入存储 store 与所有配置的 writers。
// 用于确保即使在未启动 (running=false) 或高负载溢出时审计事件也不会丢失,
// 与溢出路径保持一致的落库语义(W3-46: 审计日志不可静默丢弃)。
func (l *AuditLogger) writeEventDirect(event *AuditEvent) {
	// 写入存储, 确保未启动时审计事件仍落库(与溢出路径一致)。
	// 写入失败时更新失败计数并输出到 stderr 确保可观测。
	l.mu.RLock()
	store := l.store
	l.mu.RUnlock()
	if store != nil {
		if err := store.Save(context.Background(), event); err != nil {
			l.mu.Lock()
			l.storeFailCount++
			l.mu.Unlock()
			fmt.Fprintf(os.Stderr, "hexagon/audit: store.Save failed: %v\n", err)
		}
	}

	l.mu.RLock()
	writers := l.writers
	l.mu.RUnlock()

	if len(writers) == 0 {
		return
	}

	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	for _, w := range writers {
		_, _ = w.Write(data)
		_, _ = w.Write([]byte("\n"))
	}
}

// GetBufferOverflowCount 获取缓冲区溢出次数
//
// 可用于监控审计系统的健康状态
func (l *AuditLogger) GetBufferOverflowCount() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.bufferOverflowCount
}

// GetStoreFailCount 获取存储写入失败次数
//
// 与 GetBufferOverflowCount 对称, 可用于监控审计日志落库丢失情况。
// storeFailCount 在溢出同步写入 store 失败时累加。
func (l *AuditLogger) GetStoreFailCount() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.storeFailCount
}

// sanitize 脱敏处理
//
// 对 Details、Metadata 以及 Request.Body / Response.Body 中的敏感字段统一脱敏。
// Metadata 与 Details 同样可能携带敏感数据, 必须一并脱敏(W3-47), 否则原样泄漏。
func (l *AuditLogger) sanitize(event *AuditEvent) {
	if event.Details != nil {
		event.Details = l.sanitizeMap(event.Details)
	}
	if event.Metadata != nil {
		event.Metadata = l.sanitizeMap(event.Metadata)
	}
	if event.Request != nil && event.Request.Body != nil {
		if m, ok := event.Request.Body.(map[string]any); ok {
			event.Request.Body = l.sanitizeMap(m)
		}
	}
	if event.Response != nil && event.Response.Body != nil {
		if m, ok := event.Response.Body.(map[string]any); ok {
			event.Response.Body = l.sanitizeMap(m)
		}
	}
}

// sanitizeMap 脱敏 map
//
// 对敏感 key 直接打码; 非敏感 key 的值递归脱敏(支持嵌套 map 与 slice),
// 确保 slice 内的 map 中的敏感字段同样被脱敏(W3-48)。
func (l *AuditLogger) sanitizeMap(m map[string]any) map[string]any {
	result := make(map[string]any)
	for k, v := range m {
		if l.isSensitive(k) {
			result[k] = "***REDACTED***"
		} else {
			result[k] = l.sanitizeValue(v)
		}
	}
	return result
}

// sanitizeValue 递归脱敏任意值
//
// 支持 map[string]any、[]any 与 []map[string]any 三类容器, 其余值原样返回。
// slice 内的元素逐个递归处理, 确保 []any / []map 中嵌套的敏感字段不被遗漏(W3-48)。
func (l *AuditLogger) sanitizeValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return l.sanitizeMap(val)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = l.sanitizeValue(item)
		}
		return out
	case []map[string]any:
		// 强类型 slice 也需递归; 统一转为 []any 以便嵌套元素被一致脱敏。
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = l.sanitizeMap(item)
		}
		return out
	default:
		return v
	}
}

// isSensitive 检查是否敏感字段
//
// 大小写不敏感, 并匹配常见变体(W3-49):
//   - 子串包含: "user_password" 包含 "password" -> 命中
//   - 分隔符归一化: 去除 '_'、'-' 后比较, 使 "apikey" 命中 "api_key"
//
// 这样既能识别 Password / PASSWORD 等大小写变体, 也能识别带前后缀或无分隔符的变体,
// 避免敏感字段因命名差异而泄漏。
func (l *AuditLogger) isSensitive(field string) bool {
	lf := strings.ToLower(field)
	// 归一化: 去除常见分隔符, 用于无下划线变体匹配(如 apikey vs api_key)
	nf := normalizeFieldName(lf)
	for _, sensitive := range l.config.SensitiveFields {
		ls := strings.ToLower(sensitive)
		if ls == "" {
			continue
		}
		// 子串包含匹配(大小写不敏感)
		if strings.Contains(lf, ls) {
			return true
		}
		// 分隔符归一化后的子串包含匹配
		ns := normalizeFieldName(ls)
		if ns != "" && strings.Contains(nf, ns) {
			return true
		}
	}
	return false
}

// normalizeFieldName 归一化字段名, 去除下划线与连字符。
// 用于匹配无分隔符的敏感字段变体(如 "apikey" 与 "api_key")。
func normalizeFieldName(s string) string {
	r := strings.ReplaceAll(s, "_", "")
	return strings.ReplaceAll(r, "-", "")
}

// ============== Event Filter & Hook ==============

// EventFilter 事件过滤器
type EventFilter func(*AuditEvent) bool

// EventHook 事件钩子
type EventHook func(*AuditEvent)

// FilterByCategory 按类别过滤
func FilterByCategory(categories ...EventCategory) EventFilter {
	categorySet := make(map[EventCategory]bool)
	for _, c := range categories {
		categorySet[c] = true
	}
	return func(event *AuditEvent) bool {
		return categorySet[event.Category]
	}
}

// FilterByLevel 按级别过滤
func FilterByLevel(minLevel AuditLevel) EventFilter {
	levels := map[AuditLevel]int{
		LevelDebug:    0,
		LevelInfo:     1,
		LevelWarning:  2,
		LevelError:    3,
		LevelCritical: 4,
	}
	return func(event *AuditEvent) bool {
		return levels[event.Level] >= levels[minLevel]
	}
}

// ============== Audit Store ==============

// AuditStore 审计存储接口
type AuditStore interface {
	// Save 保存事件
	Save(ctx context.Context, event *AuditEvent) error

	// Query 查询事件
	Query(ctx context.Context, query AuditQuery) ([]*AuditEvent, error)

	// Count 统计事件数量
	Count(ctx context.Context, query AuditQuery) (int64, error)

	// Delete 删除事件
	Delete(ctx context.Context, ids []string) error

	// Cleanup 清理过期事件
	Cleanup(ctx context.Context, before time.Time) (int64, error)
}

// AuditQuery 审计查询
type AuditQuery struct {
	// StartTime 开始时间
	StartTime time.Time

	// EndTime 结束时间
	EndTime time.Time

	// Categories 类别
	Categories []EventCategory

	// Levels 级别
	Levels []AuditLevel

	// ActorID 执行者 ID
	ActorID string

	// TargetID 目标 ID
	TargetID string

	// Action 操作
	Action string

	// Result 结果
	Result EventResult

	// TraceID 追踪 ID
	TraceID string

	// Tags 标签
	Tags []string

	// Limit 限制
	Limit int

	// Offset 偏移
	Offset int

	// OrderBy 排序
	OrderBy string

	// OrderDesc 降序
	OrderDesc bool
}

// MemoryAuditStore 内存审计存储
type MemoryAuditStore struct {
	events []*AuditEvent
	mu     sync.RWMutex
}

// NewMemoryAuditStore 创建内存存储
func NewMemoryAuditStore() *MemoryAuditStore {
	return &MemoryAuditStore{
		events: make([]*AuditEvent, 0),
	}
}

// Save 保存事件
func (s *MemoryAuditStore) Save(ctx context.Context, event *AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

// Query 查询事件
func (s *MemoryAuditStore) Query(ctx context.Context, query AuditQuery) ([]*AuditEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*AuditEvent

	for _, event := range s.events {
		if s.matchQuery(event, query) {
			results = append(results, event)
		}
	}

	// 应用分页
	// Offset 越界(>= 结果数)时返回空集合, 而非静默忽略 offset 返回全量。
	// 与同类分页语义统一(W3-33/W3-45): offset 超出范围视为已翻过所有页。
	if query.Offset > 0 {
		if query.Offset >= len(results) {
			results = results[:0]
		} else {
			results = results[query.Offset:]
		}
	}
	if query.Limit > 0 && query.Limit < len(results) {
		results = results[:query.Limit]
	}

	return results, nil
}

// Count 统计数量
func (s *MemoryAuditStore) Count(ctx context.Context, query AuditQuery) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int64
	for _, event := range s.events {
		if s.matchQuery(event, query) {
			count++
		}
	}

	return count, nil
}

// Delete 删除事件
func (s *MemoryAuditStore) Delete(ctx context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}

	newEvents := make([]*AuditEvent, 0)
	for _, event := range s.events {
		if !idSet[event.ID] {
			newEvents = append(newEvents, event)
		}
	}
	s.events = newEvents

	return nil
}

// Cleanup 清理过期事件
func (s *MemoryAuditStore) Cleanup(ctx context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var count int64
	newEvents := make([]*AuditEvent, 0)
	for _, event := range s.events {
		if event.Timestamp.After(before) {
			newEvents = append(newEvents, event)
		} else {
			count++
		}
	}
	s.events = newEvents

	return count, nil
}

// matchQuery 匹配查询条件
func (s *MemoryAuditStore) matchQuery(event *AuditEvent, query AuditQuery) bool {
	// 时间范围
	if !query.StartTime.IsZero() && event.Timestamp.Before(query.StartTime) {
		return false
	}
	if !query.EndTime.IsZero() && event.Timestamp.After(query.EndTime) {
		return false
	}

	// 类别
	if len(query.Categories) > 0 {
		found := false
		for _, cat := range query.Categories {
			if event.Category == cat {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// 级别
	if len(query.Levels) > 0 {
		found := false
		for _, level := range query.Levels {
			if event.Level == level {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// 执行者
	if query.ActorID != "" && (event.Actor == nil || event.Actor.ID != query.ActorID) {
		return false
	}

	// 目标
	if query.TargetID != "" && (event.Target == nil || event.Target.ID != query.TargetID) {
		return false
	}

	// 操作
	if query.Action != "" && event.Action != query.Action {
		return false
	}

	// 结果
	if query.Result != "" && event.Result != query.Result {
		return false
	}

	// TraceID
	if query.TraceID != "" && event.TraceID != query.TraceID {
		return false
	}

	return true
}

// ============== Context Helpers ==============

type auditContextKey struct{}

// ContextWithAuditLogger 将审计日志添加到 context
func ContextWithAuditLogger(ctx context.Context, logger *AuditLogger) context.Context {
	return context.WithValue(ctx, auditContextKey{}, logger)
}

// AuditLoggerFromContext 从 context 获取审计日志
func AuditLoggerFromContext(ctx context.Context) *AuditLogger {
	if logger, ok := ctx.Value(auditContextKey{}).(*AuditLogger); ok {
		return logger
	}
	return nil
}

// LogFromContext 从 context 记录审计事件
func LogFromContext(ctx context.Context, event *AuditEvent) {
	if logger := AuditLoggerFromContext(ctx); logger != nil {
		logger.Log(event)
	}
}

// ============== Predefined Actors ==============

// SystemActor 系统执行者
func SystemActor() *Actor {
	return &Actor{
		Type: "system",
		ID:   "system",
		Name: "System",
	}
}

// UserActor 用户执行者
func UserActor(id, name string, roles []string) *Actor {
	return &Actor{
		Type:  "user",
		ID:    id,
		Name:  name,
		Roles: roles,
	}
}

// AgentActor Agent 执行者
func AgentActor(id, name string) *Actor {
	return &Actor{
		Type: "agent",
		ID:   id,
		Name: name,
	}
}

// ============== Report ==============

// topReportN 审计报告中 TopActors / TopActions 的最大保留条数。
// 报告按计数降序排序后截断到该上限, 体现 "Top-N" 语义。
const topReportN = 10

// AuditReport 审计报告
type AuditReport struct {
	// Period 时间段
	Period TimeRange `json:"period"`

	// TotalEvents 总事件数
	TotalEvents int64 `json:"total_events"`

	// ByCategory 按类别统计
	ByCategory map[EventCategory]int64 `json:"by_category"`

	// ByLevel 按级别统计
	ByLevel map[AuditLevel]int64 `json:"by_level"`

	// ByResult 按结果统计
	ByResult map[EventResult]int64 `json:"by_result"`

	// TopActors 最活跃执行者
	TopActors []ActorStats `json:"top_actors"`

	// TopActions 最常见操作
	TopActions []ActionStats `json:"top_actions"`

	// Errors 错误列表
	Errors []*AuditEvent `json:"errors,omitempty"`

	// GeneratedAt 生成时间
	GeneratedAt time.Time `json:"generated_at"`
}

// TimeRange 时间范围
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// ActorStats 执行者统计
type ActorStats struct {
	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name"`
	Count     int64  `json:"count"`
}

// ActionStats 操作统计
type ActionStats struct {
	Action string `json:"action"`
	Count  int64  `json:"count"`
}

// GenerateReport 生成审计报告
func (l *AuditLogger) GenerateReport(ctx context.Context, start, end time.Time) (*AuditReport, error) {
	events, err := l.store.Query(ctx, AuditQuery{
		StartTime: start,
		EndTime:   end,
	})
	if err != nil {
		return nil, err
	}

	report := &AuditReport{
		Period:      TimeRange{Start: start, End: end},
		TotalEvents: int64(len(events)),
		ByCategory:  make(map[EventCategory]int64),
		ByLevel:     make(map[AuditLevel]int64),
		ByResult:    make(map[EventResult]int64),
		GeneratedAt: time.Now(),
	}

	actorCounts := make(map[string]*ActorStats)
	actionCounts := make(map[string]int64)

	for _, event := range events {
		report.ByCategory[event.Category]++
		report.ByLevel[event.Level]++
		report.ByResult[event.Result]++

		if event.Actor != nil {
			if _, ok := actorCounts[event.Actor.ID]; !ok {
				actorCounts[event.Actor.ID] = &ActorStats{
					ActorID:   event.Actor.ID,
					ActorName: event.Actor.Name,
				}
			}
			actorCounts[event.Actor.ID].Count++
		}

		actionCounts[event.Action]++

		if event.Level == LevelError || event.Level == LevelCritical {
			report.Errors = append(report.Errors, event)
		}
	}

	// 转换 top actors: 按计数降序排序后截断到 topReportN(W3-51)
	for _, stats := range actorCounts {
		report.TopActors = append(report.TopActors, *stats)
	}
	sort.Slice(report.TopActors, func(i, j int) bool {
		return report.TopActors[i].Count > report.TopActors[j].Count
	})
	if len(report.TopActors) > topReportN {
		report.TopActors = report.TopActors[:topReportN]
	}

	// 转换 top actions: 按计数降序排序后截断到 topReportN(W3-51)
	for action, count := range actionCounts {
		report.TopActions = append(report.TopActions, ActionStats{
			Action: action,
			Count:  count,
		})
	}
	sort.Slice(report.TopActions, func(i, j int) bool {
		return report.TopActions[i].Count > report.TopActions[j].Count
	})
	if len(report.TopActions) > topReportN {
		report.TopActions = report.TopActions[:topReportN]
	}

	return report, nil
}
