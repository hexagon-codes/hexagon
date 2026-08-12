package devui

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/hexagon-codes/hexagon/hooks"
	"github.com/hexagon-codes/hexagon/observe/tracer"
)

// DevUI 开发调试界面服务器
//
// DevUI 提供了一个 Web 界面用于实时查看 Agent 执行过程，包括：
//   - 实时事件流（SSE 推送）
//   - REST API 查询历史事件和指标
//   - Span 追踪可视化
//   - 指标仪表板
//
// 使用示例：
//
//	ui := devui.New(devui.WithAddr("127.0.0.1:8080"))
//	defer func() {
//	    if err := ui.Stop(context.Background()); err != nil {
//	        log.Printf("Dev UI shutdown failed: %v", err)
//	    }
//	}()
//
//	ctx := hooks.ContextWithManager(context.Background(), ui.HookManager())
//	go func() {
//	    if err := ui.Start(); err != nil {
//	        log.Printf("Dev UI stopped: %v", err)
//	    }
//	}()
//	// 使用 ctx 执行 Agent，并访问 http://localhost:8080。
type DevUI struct {
	server        *http.Server
	collector     *Collector
	hookMgr       *hooks.Manager
	options       *Options
	graphStore    *GraphStore
	replayManager *ReplayManager
	running       bool
	mu            sync.Mutex
	startTime     time.Time
	sessionMu     sync.Mutex
	sessions      map[string]devUISession
}

// Options 配置选项
type Options struct {
	// Addr 监听地址，默认仅本机回环 "127.0.0.1:8080"
	Addr string

	// EnableSSE 是否启用 SSE 事件推送，默认 true
	EnableSSE bool

	// EnableMetrics 是否启用指标展示，默认 true
	EnableMetrics bool

	// StaticDir 自定义静态文件目录
	// 如果为空，使用内嵌的静态文件
	StaticDir string

	// MaxEvents 最大事件缓存数，默认 1000
	MaxEvents int

	// APIPrefix API 前缀，默认 "/api"
	APIPrefix string

	// CORSEnabled 是否启用 CORS，默认 false。启用时仍只允许 AllowedOrigins。
	CORSEnabled bool

	// AllowedOrigins 是允许跨域访问的精确 Origin 列表；不支持通配符。
	AllowedOrigins []string

	// AuthToken 是 Builder 写端点的 bearer/CSRF token。loopback 模式
	// 可为空并通过同源 bootstrap 建立短期 session；非 loopback 必须显式配置。
	AuthToken string

	// ReadTimeout HTTP 读取超时
	ReadTimeout time.Duration

	// WriteTimeout HTTP 写入超时
	WriteTimeout time.Duration
}

// DefaultOptions 返回默认配置
func DefaultOptions() *Options {
	return &Options{
		Addr:          "127.0.0.1:8080",
		EnableSSE:     true,
		EnableMetrics: true,
		MaxEvents:     1000,
		APIPrefix:     "/api",
		CORSEnabled:   false,
		ReadTimeout:   30 * time.Second,
		WriteTimeout:  30 * time.Second,
	}
}

// Option 配置选项函数
type Option func(*Options)

// WithAddr 设置监听地址
func WithAddr(addr string) Option {
	return func(o *Options) {
		o.Addr = addr
	}
}

// WithSSE 设置是否启用 SSE
func WithSSE(enabled bool) Option {
	return func(o *Options) {
		o.EnableSSE = enabled
	}
}

// WithMetrics 设置是否启用指标
func WithMetrics(enabled bool) Option {
	return func(o *Options) {
		o.EnableMetrics = enabled
	}
}

// WithStaticDir 设置静态文件目录
func WithStaticDir(dir string) Option {
	return func(o *Options) {
		o.StaticDir = dir
	}
}

// WithMaxEvents 设置最大事件缓存数
func WithMaxEvents(n int) Option {
	return func(o *Options) {
		if n > 0 {
			o.MaxEvents = n
		}
	}
}

// WithAPIPrefix 设置 API 前缀
func WithAPIPrefix(prefix string) Option {
	return func(o *Options) {
		o.APIPrefix = prefix
	}
}

// WithCORS 设置是否启用 CORS
func WithCORS(enabled bool) Option {
	return func(o *Options) {
		o.CORSEnabled = enabled
	}
}

// WithAllowedOrigins 设置允许跨域访问的精确 Origin。非法值和 "*" 会被忽略。
func WithAllowedOrigins(origins ...string) Option {
	return func(o *Options) {
		o.AllowedOrigins = normalizeOrigins(origins)
	}
}

// WithAuthToken 设置 Builder 写端点所需的 bearer/CSRF token（至少 32
// 个无空白字节）。空值不会关闭认证：loopback 仍要求短期 session，非 loopback
// 会拒绝启动。
func WithAuthToken(token string) Option {
	return func(o *Options) {
		o.AuthToken = token
	}
}

// WithTimeouts 设置超时时间
func WithTimeouts(read, write time.Duration) Option {
	return func(o *Options) {
		if read > 0 {
			o.ReadTimeout = read
		}
		if write > 0 {
			o.WriteTimeout = write
		}
	}
}

// New 创建 DevUI 实例
func New(opts ...Option) *DevUI {
	options := DefaultOptions()
	for _, opt := range opts {
		opt(options)
	}

	collector := NewCollector(options.MaxEvents)
	hookMgr := hooks.NewManager()

	// 将收集器注册到 Hook Manager
	hookMgr.RegisterRunHook(collector)
	hookMgr.RegisterToolHook(collector)
	hookMgr.RegisterLLMHook(collector)
	hookMgr.RegisterRetrieverHook(collector)

	return &DevUI{
		collector:     collector,
		hookMgr:       hookMgr,
		options:       options,
		graphStore:    NewGraphStore(),
		replayManager: NewReplayManager(100),
		sessions:      make(map[string]devUISession),
	}
}

// HookManager 返回 Hook Manager，用于注入到 Agent
func (d *DevUI) HookManager() *hooks.Manager {
	return d.hookMgr
}

// Tracer 返回内置的 Tracer，用于注入到 Agent
func (d *DevUI) Tracer() tracer.Tracer {
	return d.collector.Tracer()
}

// Collector 返回事件收集器
func (d *DevUI) Collector() *Collector {
	return d.collector
}

// Replay 返回调试回放管理器
func (d *DevUI) Replay() *ReplayManager {
	return d.replayManager
}

// Start 启动 DevUI 服务器
// 此方法会阻塞，建议在 goroutine 中调用
func (d *DevUI) Start() error {
	if err := d.validateSecurityConfig(); err != nil {
		return err
	}
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return fmt.Errorf("devui: already running")
	}
	d.running = true
	d.startTime = time.Now()
	d.mu.Unlock()

	// 创建路由
	mux := d.setupRoutes()

	// 创建服务器
	d.server = &http.Server{
		Addr:         d.options.Addr,
		Handler:      mux,
		ReadTimeout:  d.options.ReadTimeout,
		WriteTimeout: d.options.WriteTimeout,
	}

	fmt.Printf("🔮 Hexagon Dev UI starting at %s\n", d.URL())

	if err := d.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		d.mu.Lock()
		d.running = false
		d.mu.Unlock()
		return fmt.Errorf("devui: server error: %w", err)
	}

	return nil
}

// Stop 停止 DevUI 服务器
func (d *DevUI) Stop(ctx context.Context) error {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()

	if d.server != nil {
		if err := d.server.Shutdown(ctx); err != nil {
			return fmt.Errorf("devui: shutdown error: %w", err)
		}
	}

	d.mu.Lock()
	d.running = false
	d.mu.Unlock()

	return nil
}

// IsRunning 返回服务器是否正在运行
func (d *DevUI) IsRunning() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running
}

// Uptime 返回服务器运行时间
func (d *DevUI) Uptime() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.running {
		return 0
	}
	return time.Since(d.startTime)
}

// Addr 返回监听地址
func (d *DevUI) Addr() string {
	return d.options.Addr
}

// URL 返回完整的访问 URL
func (d *DevUI) URL() string {
	return devUIURL(d.options.Addr)
}

// setupRoutes 设置路由
func (d *DevUI) setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	handler := newHandler(d)
	bHandler := newBuilderHandler(d.graphStore, d.collector)
	rHandler := newReplayHandler(d.replayManager)

	// API 路由
	prefix := d.options.APIPrefix

	// 事件 API
	mux.HandleFunc(prefix+"/events", d.corsMiddleware(handler.handleEvents))
	mux.HandleFunc(prefix+"/events/", d.corsMiddleware(handler.handleEventByID))

	// Trace API
	mux.HandleFunc(prefix+"/traces", d.corsMiddleware(handler.handleTraces))
	mux.HandleFunc(prefix+"/traces/", d.corsMiddleware(handler.handleTraceByID))

	// 指标 API
	if d.options.EnableMetrics {
		mux.HandleFunc(prefix+"/metrics", d.corsMiddleware(handler.handleMetrics))
		mux.HandleFunc(prefix+"/stats", d.corsMiddleware(handler.handleStats))
	}

	// Builder API
	mux.HandleFunc(prefix+"/auth/bootstrap", d.corsMiddleware(d.handleAuthBootstrap))
	mux.HandleFunc(prefix+"/builder/graphs", d.corsMiddleware(d.protectBuilderWrites(prefix, bHandler.handleGraphs)))
	mux.HandleFunc(prefix+"/builder/graphs/", d.corsMiddleware(d.protectBuilderWrites(prefix, bHandler.handleGraph)))
	mux.HandleFunc(prefix+"/builder/node-types", d.corsMiddleware(bHandler.handleNodeTypes))

	// Replay API（调试回放）
	mux.HandleFunc(prefix+"/replay/sessions", d.corsMiddleware(rHandler.handleSessions))
	mux.HandleFunc(prefix+"/replay/sessions/", d.corsMiddleware(rHandler.handleSession))

	// SSE 事件流
	if d.options.EnableSSE {
		mux.HandleFunc("/events", d.corsMiddleware(handler.handleSSE))
	}

	// 健康检查
	mux.HandleFunc("/health", d.corsMiddleware(handler.handleHealth))

	// 静态文件
	mux.HandleFunc("/", d.corsMiddleware(handler.handleStatic))

	return mux
}
