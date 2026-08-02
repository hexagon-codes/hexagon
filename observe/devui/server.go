package devui

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Server HTTP 服务器包装器
// 提供更精细的服务器生命周期控制
type Server struct {
	*http.Server
	listener net.Listener
	running  bool
	mu       sync.Mutex
}

// NewServer 创建 HTTP 服务器
func NewServer(addr string, handler http.Handler) *Server {
	return &Server{
		Server: &http.Server{
			Addr:         addr,
			Handler:      handler,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
	}
}

// Start 启动服务器
// 返回实际监听的地址（当使用 :0 端口时有用）
func (s *Server) Start() (string, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return "", fmt.Errorf("server already running")
	}

	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		s.mu.Unlock()
		return "", err
	}

	s.listener = ln
	s.running = true
	actualAddr := ln.Addr().String()
	s.mu.Unlock()

	go func() {
		if err := s.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}
	}()

	return actualAddr, nil
}

// Stop 停止服务器
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	if err := s.Shutdown(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	s.running = false
	s.mu.Unlock()

	return nil
}

// IsRunning 返回服务器是否正在运行
func (s *Server) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// ActualAddr 返回实际监听地址
func (s *Server) ActualAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.Addr
}

// RecoveryMiddleware 恢复中间件
// 捕获 panic 并返回 500 错误
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, `{"success":false,"error":"internal server error"}`)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware 日志中间件
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// 包装 ResponseWriter 以捕获状态码
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(lrw, r)

		duration := time.Since(start)

		// 简单的请求日志
		fmt.Printf("[%s] %s %s %d %v\n",
			start.Format("15:04:05"),
			r.Method,
			r.URL.Path,
			lrw.statusCode,
			duration,
		)
	})
}

// loggingResponseWriter 包装 ResponseWriter 以捕获状态码
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

// CORSMiddleware CORS 中间件
func CORSMiddleware(allowOrigin string) func(http.Handler) http.Handler {
	allowed, configured := normalizeOrigin(allowOrigin)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin, valid := normalizeOrigin(r.Header.Get("Origin"))
			if configured && valid && origin == allowed {
				w.Header().Add("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Origin", allowed)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With, "+csrfHeader)
				w.Header().Set("Access-Control-Max-Age", "86400")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
			} else if r.Method == http.MethodOptions && r.Header.Get("Origin") != "" {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitMiddleware 简单的速率限制中间件
type RateLimitMiddleware struct {
	requests    map[string][]time.Time
	limit       int           // 限制请求数
	window      time.Duration // 时间窗口
	lastCleanup time.Time     // 上次全局清理时间
	mu          sync.Mutex
}

// NewRateLimitMiddleware 创建速率限制中间件
func NewRateLimitMiddleware(limit int, window time.Duration) *RateLimitMiddleware {
	return &RateLimitMiddleware{
		requests:    make(map[string][]time.Time),
		limit:       limit,
		window:      window,
		lastCleanup: time.Now(),
	}
}

// Handler 返回中间件处理器
func (m *RateLimitMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr

		m.mu.Lock()

		now := time.Now()

		// 定期全局清理过期 IP 记录，防止大量唯一 IP 导致内存泄漏
		if now.Sub(m.lastCleanup) > m.window*2 {
			for k, times := range m.requests {
				var valid []time.Time
				for _, t := range times {
					if now.Sub(t) < m.window {
						valid = append(valid, t)
					}
				}
				if len(valid) == 0 {
					delete(m.requests, k)
				} else {
					m.requests[k] = valid
				}
			}
			m.lastCleanup = now
		}

		// 清理当前 IP 的过期请求记录
		if times, ok := m.requests[ip]; ok {
			var valid []time.Time
			for _, t := range times {
				if now.Sub(t) < m.window {
					valid = append(valid, t)
				}
			}
			m.requests[ip] = valid
		}

		// 检查是否超过限制
		if len(m.requests[ip]) >= m.limit {
			m.mu.Unlock()
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"success":false,"error":"rate limit exceeded"}`)
			return
		}

		// 记录此次请求
		m.requests[ip] = append(m.requests[ip], now)
		m.mu.Unlock()

		next.ServeHTTP(w, r)
	})
}

// ChainMiddleware 链接多个中间件
func ChainMiddleware(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(final http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}
