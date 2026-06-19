package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ============== Server ==============

// Server A2A 服务器
// 提供 A2A 协议的服务端实现。
//
// 使用示例:
//
//	card := &a2a.AgentCard{
//	    Name:    "assistant",
//	    URL:     "http://localhost:8080",
//	    Version: "1.0.0",
//	}
//
//	handler := &MyTaskHandler{}
//	server := a2a.NewServer(card, handler)
//	server.Start(":8080")
type Server struct {
	// card Agent Card
	card *AgentCard

	// handler 任务处理器
	handler TaskHandler

	// store 任务存储
	store TaskStore

	// pushStore 推送配置存储
	pushStore PushConfigStore

	// pushService 推送服务
	pushService PushService

	// subscribers 任务订阅者 (taskID -> []chan)
	subscribers map[string][]chan StreamEvent

	// httpServer HTTP 服务器
	httpServer *http.Server

	// running 运行状态
	running atomic.Bool

	// wg 等待组
	wg sync.WaitGroup

	// cors CORS 配置
	corsEnabled bool
	corsOrigins []string

	mu sync.RWMutex
}

// ServerOption 服务器选项
type ServerOption func(*Server)

// NewServer 创建 A2A 服务器
func NewServer(card *AgentCard, handler TaskHandler, opts ...ServerOption) *Server {
	memStore := NewMemoryTaskStore()

	s := &Server{
		card:        card,
		handler:     handler,
		store:       memStore,
		pushStore:   memStore,
		subscribers: make(map[string][]chan StreamEvent),
		corsEnabled: true,
		corsOrigins: []string{"*"},
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// WithStore 设置任务存储
func WithStore(store TaskStore) ServerOption {
	return func(s *Server) {
		s.store = store
		// 如果存储也实现了 PushConfigStore，使用它
		if pushStore, ok := store.(PushConfigStore); ok {
			s.pushStore = pushStore
		}
	}
}

// WithPushService 设置推送服务
func WithPushService(pushService PushService) ServerOption {
	return func(s *Server) {
		s.pushService = pushService
	}
}

// WithCORS 设置 CORS
func WithCORS(enabled bool, origins ...string) ServerOption {
	return func(s *Server) {
		s.corsEnabled = enabled
		if len(origins) > 0 {
			s.corsOrigins = origins
		}
	}
}

// Start 启动服务器
func (s *Server) Start(addr string) error {
	if s.running.Load() {
		return fmt.Errorf("server already running")
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      s.corsMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // 无超时，支持 SSE
		IdleTimeout:  120 * time.Second,
	}

	s.running.Store(true)

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		s.running.Store(false)
		return err
	}

	return nil
}

// Stop 停止服务器
func (s *Server) Stop(ctx context.Context) error {
	if !s.running.Load() {
		return nil
	}

	s.running.Store(false)

	// 关闭所有订阅
	s.mu.Lock()
	for taskID, subs := range s.subscribers {
		for _, ch := range subs {
			close(ch)
		}
		delete(s.subscribers, taskID)
	}
	s.mu.Unlock()

	// 等待所有处理完成
	s.wg.Wait()

	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}

	return nil
}

// ServeHTTP 实现 http.Handler 接口
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.corsMiddleware(mux).ServeHTTP(w, r)
}

// Handler 返回 HTTP Handler
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return s.corsMiddleware(mux)
}

// registerRoutes 注册路由
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Agent Card
	mux.HandleFunc(PathAgentCard, s.handleAgentCard)

	// 任务 API (JSON-RPC)
	mux.HandleFunc(PathTasks, s.handleTasks)
	mux.HandleFunc(PathTaskSend, s.handleTasks)
	mux.HandleFunc(PathTaskSendStream, s.handleTasksStream)
	mux.HandleFunc(PathTaskGet, s.handleTasks)
	mux.HandleFunc(PathTaskCancel, s.handleTasks)
	mux.HandleFunc(PathTaskResubscribe, s.handleTasksStream)
	mux.HandleFunc(PathPushNotificationGet, s.handleTasks)
	mux.HandleFunc(PathPushNotificationSet, s.handleTasks)
}

// corsMiddleware CORS 中间件
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.corsEnabled {
			origin := r.Header.Get("Origin")
			if origin == "" {
				origin = "*"
			}

			// 检查是否允许的来源
			allowed := false
			for _, o := range s.corsOrigins {
				if o == "*" || o == origin {
					allowed = true
					break
				}
			}

			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}

			// 处理预检请求
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// ============== HTTP 处理器 ==============

// handleAgentCard 处理 Agent Card 请求
func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", ContentTypeJSON)
	if err := json.NewEncoder(w).Encode(s.card); err != nil {
		s.writeError(w, NewInternalError(err.Error()), nil)
	}
}

// handleTasks 处理任务 API 请求
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析 JSON-RPC 请求
	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, NewParseError(err.Error()), nil)
		return
	}

	// 验证 JSON-RPC 版本
	if req.JSONRPC != "2.0" {
		s.writeError(w, NewInvalidRequestError("invalid JSON-RPC version"), req.ID)
		return
	}

	// 路由到具体方法
	ctx := r.Context()
	var result any
	var rpcErr *Error

	switch req.Method {
	case MethodSendMessage:
		result, rpcErr = s.handleSendMessage(ctx, req.Params)
	case MethodGetTask:
		result, rpcErr = s.handleGetTask(ctx, req.Params)
	case MethodListTasks:
		result, rpcErr = s.handleListTasks(ctx, req.Params)
	case MethodCancelTask:
		result, rpcErr = s.handleCancelTask(ctx, req.Params)
	case MethodSetPushNotification:
		result, rpcErr = s.handleSetPushNotification(ctx, req.Params)
	case MethodGetPushNotification:
		result, rpcErr = s.handleGetPushNotification(ctx, req.Params)
	default:
		rpcErr = NewMethodNotFoundError(req.Method)
	}

	// 返回响应
	if rpcErr != nil {
		s.writeError(w, rpcErr, req.ID)
		return
	}

	s.writeResult(w, result, req.ID)
}

// handleTasksStream 处理流式任务 API 请求
func (s *Server) handleTasksStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 检查是否支持流式
	if !s.card.Capabilities.Streaming {
		s.writeError(w, NewUnsupportedOperationError("streaming"), nil)
		return
	}

	// 解析 JSON-RPC 请求
	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, NewParseError(err.Error()), nil)
		return
	}

	ctx := r.Context()

	switch req.Method {
	case MethodSendMessageStream:
		s.handleSendMessageStream(ctx, w, req.Params)
	case MethodResubscribe:
		s.handleResubscribe(ctx, w, req.Params)
	default:
		s.writeError(w, NewMethodNotFoundError(req.Method), req.ID)
	}
}

// ============== 方法处理器 ==============

// handleSendMessage 处理发送消息
func (s *Server) handleSendMessage(ctx context.Context, params json.RawMessage) (*Task, *Error) {
	var req SendMessageRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewInvalidParamsError(err.Error())
	}

	// 获取或创建任务
	task, err := s.getOrCreateTask(ctx, &req)
	if err != nil {
		return nil, ToA2AError(err)
	}

	// 添加消息到历史
	task.History = append(task.History, req.Message)
	task.Status = TaskStatus{
		State:     TaskStateWorking,
		Timestamp: time.Now(),
	}
	task.UpdatedAt = time.Now()

	if err := s.store.Update(ctx, task); err != nil {
		return nil, ToA2AError(err)
	}

	// 处理任务
	update, err := s.handler.HandleTask(ctx, task, &req.Message)
	if err != nil {
		// 处理失败
		task.Status = TaskStatus{
			State:     TaskStateFailed,
			Timestamp: time.Now(),
		}
		task.UpdatedAt = time.Now()
		s.store.Update(ctx, task)
		return nil, ToA2AError(err)
	}

	// 应用更新
	s.applyUpdate(task, update)
	if err := s.store.Update(ctx, task); err != nil {
		return nil, ToA2AError(err)
	}

	// 发送推送通知
	s.sendPushNotification(ctx, task)

	return task, nil
}

// handleSendMessageStream 处理流式发送消息
func (s *Server) handleSendMessageStream(ctx context.Context, w http.ResponseWriter, params json.RawMessage) {
	var req SendMessageRequest
	if err := json.Unmarshal(params, &req); err != nil {
		s.writeSSEError(w, NewInvalidParamsError(err.Error()))
		return
	}

	// 设置 SSE 头
	w.Header().Set("Content-Type", ContentTypeSSE)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeSSEError(w, NewInternalError("streaming not supported"))
		return
	}

	// 获取或创建任务
	task, err := s.getOrCreateTask(ctx, &req)
	if err != nil {
		s.writeSSEError(w, ToA2AError(err))
		return
	}

	// 添加消息到历史
	task.History = append(task.History, req.Message)
	task.Status = TaskStatus{
		State:     TaskStateWorking,
		Timestamp: time.Now(),
	}
	task.UpdatedAt = time.Now()

	if err := s.store.Update(ctx, task); err != nil {
		s.writeSSEError(w, ToA2AError(err))
		return
	}

	// 发送初始状态
	s.writeSSEEvent(w, EventTypeTaskStatus, &TaskStatusEvent{
		ID:     task.ID,
		Status: task.Status,
	})
	flusher.Flush()

	// 检查是否支持流式处理
	streamHandler, isStreaming := s.handler.(StreamingTaskHandler)
	if !isStreaming {
		// 非流式处理
		update, err := s.handler.HandleTask(ctx, task, &req.Message)
		if err != nil {
			task.Status = TaskStatus{
				State:     TaskStateFailed,
				Timestamp: time.Now(),
			}
			task.UpdatedAt = time.Now()
			s.store.Update(ctx, task)
			s.writeSSEError(w, ToA2AError(err))
			return
		}

		// 应用更新
		s.applyUpdate(task, update)
		s.store.Update(ctx, task)

		// 发送最终状态
		s.writeSSEEvent(w, EventTypeTaskStatus, &TaskStatusEvent{
			ID:     task.ID,
			Status: task.Status,
			Final:  true,
		})

		// 发送完成事件
		s.writeSSEEvent(w, EventTypeDone, &DoneEvent{Task: task})
		flusher.Flush()
		return
	}

	// 流式处理
	updates, err := streamHandler.HandleTaskStream(ctx, task, &req.Message)
	if err != nil {
		s.writeSSEError(w, ToA2AError(err))
		return
	}

	// 订阅任务更新
	subChan := make(chan StreamEvent, 100)
	s.subscribe(task.ID, subChan)
	defer s.unsubscribe(task.ID, subChan)

	// 处理更新流
	for {
		select {
		case <-ctx.Done():
			return

		case update, ok := <-updates:
			if !ok {
				// 更新流结束
				s.writeSSEEvent(w, EventTypeDone, &DoneEvent{Task: task})
				flusher.Flush()
				return
			}

			// 应用更新
			s.applyUpdate(task, update)
			s.store.Update(ctx, task)

			// 发送事件
			if update.Status != nil {
				s.writeSSEEvent(w, EventTypeTaskStatus, &TaskStatusEvent{
					ID:     task.ID,
					Status: task.Status,
					Final:  update.Final,
				})
			}
			if update.Artifact != nil {
				s.writeSSEEvent(w, EventTypeArtifact, &ArtifactEvent{
					ID:       task.ID,
					Artifact: *update.Artifact,
				})
			}
			flusher.Flush()

			// 如果是最终更新，结束
			if update.Final {
				s.writeSSEEvent(w, EventTypeDone, &DoneEvent{Task: task})
				flusher.Flush()
				return
			}
		}
	}
}

// handleGetTask 处理获取任务
func (s *Server) handleGetTask(ctx context.Context, params json.RawMessage) (*Task, *Error) {
	var req GetTaskRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewInvalidParamsError(err.Error())
	}

	task, err := s.store.Get(ctx, req.ID)
	if err != nil {
		return nil, ToA2AError(err)
	}

	// 截断历史
	if req.HistoryLength > 0 && len(task.History) > req.HistoryLength {
		task.History = task.History[len(task.History)-req.HistoryLength:]
	}

	return task, nil
}

// handleListTasks 处理列出任务
func (s *Server) handleListTasks(ctx context.Context, params json.RawMessage) (*ListTasksResponse, *Error) {
	var req ListTasksRequest
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, NewInvalidParamsError(err.Error())
		}
	}

	resp, err := s.store.List(ctx, &req)
	if err != nil {
		return nil, ToA2AError(err)
	}

	return resp, nil
}

// handleCancelTask 处理取消任务
func (s *Server) handleCancelTask(ctx context.Context, params json.RawMessage) (*Task, *Error) {
	var req CancelTaskRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewInvalidParamsError(err.Error())
	}

	task, err := s.store.Get(ctx, req.ID)
	if err != nil {
		return nil, ToA2AError(err)
	}

	// 检查任务是否可取消
	if task.Status.State.IsTerminal() {
		return nil, NewTaskNotCancelableError(req.ID)
	}

	// 更新状态
	task.Status = TaskStatus{
		State:     TaskStateCanceled,
		Timestamp: time.Now(),
	}
	task.UpdatedAt = time.Now()

	if err := s.store.Update(ctx, task); err != nil {
		return nil, ToA2AError(err)
	}

	// 通知订阅者
	s.notifySubscribers(task.ID, &TaskStatusEvent{
		ID:     task.ID,
		Status: task.Status,
		Final:  true,
	})

	return task, nil
}

// handleSetPushNotification 处理设置推送通知
func (s *Server) handleSetPushNotification(ctx context.Context, params json.RawMessage) (*SetPushNotificationResponse, *Error) {
	if !s.card.Capabilities.PushNotifications {
		return nil, NewPushNotSupportedError()
	}

	var req SetPushNotificationRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewInvalidParamsError(err.Error())
	}

	if err := s.pushStore.SetPushConfig(ctx, req.TaskID, &req.Config); err != nil {
		return nil, ToA2AError(err)
	}

	return &SetPushNotificationResponse{Config: req.Config}, nil
}

// handleGetPushNotification 处理获取推送通知
func (s *Server) handleGetPushNotification(ctx context.Context, params json.RawMessage) (*GetPushNotificationResponse, *Error) {
	if !s.card.Capabilities.PushNotifications {
		return nil, NewPushNotSupportedError()
	}

	var req GetPushNotificationRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewInvalidParamsError(err.Error())
	}

	config, err := s.pushStore.GetPushConfig(ctx, req.TaskID)
	if err != nil {
		return nil, ToA2AError(err)
	}

	return &GetPushNotificationResponse{Config: config}, nil
}

// handleResubscribe 处理重新订阅
func (s *Server) handleResubscribe(ctx context.Context, w http.ResponseWriter, params json.RawMessage) {
	var req ResubscribeRequest
	if err := json.Unmarshal(params, &req); err != nil {
		s.writeSSEError(w, NewInvalidParamsError(err.Error()))
		return
	}

	// 获取任务
	task, err := s.store.Get(ctx, req.TaskID)
	if err != nil {
		s.writeSSEError(w, ToA2AError(err))
		return
	}

	// 设置 SSE 头
	w.Header().Set("Content-Type", ContentTypeSSE)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeSSEError(w, NewInternalError("streaming not supported"))
		return
	}

	// 如果任务已完成，直接返回最终状态
	if task.Status.State.IsTerminal() {
		s.writeSSEEvent(w, EventTypeTaskStatus, &TaskStatusEvent{
			ID:     task.ID,
			Status: task.Status,
			Final:  true,
		})
		s.writeSSEEvent(w, EventTypeDone, &DoneEvent{Task: task})
		flusher.Flush()
		return
	}

	// 发送当前状态
	s.writeSSEEvent(w, EventTypeTaskStatus, &TaskStatusEvent{
		ID:     task.ID,
		Status: task.Status,
	})
	flusher.Flush()

	// 订阅更新
	subChan := make(chan StreamEvent, 100)
	s.subscribe(task.ID, subChan)
	defer s.unsubscribe(task.ID, subChan)

	// 等待更新
	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-subChan:
			if !ok {
				return
			}

			// 发送事件
			switch e := event.(type) {
			case *TaskStatusEvent:
				s.writeSSEEvent(w, EventTypeTaskStatus, e)
				flusher.Flush()
				if e.Final {
					s.writeSSEEvent(w, EventTypeDone, &DoneEvent{})
					flusher.Flush()
					return
				}
			case *ArtifactEvent:
				s.writeSSEEvent(w, EventTypeArtifact, e)
				flusher.Flush()
			}
		}
	}
}

// ============== 辅助方法 ==============

// getOrCreateTask 获取或创建任务
func (s *Server) getOrCreateTask(ctx context.Context, req *SendMessageRequest) (*Task, error) {
	if req.TaskID != "" {
		// 获取已有任务
		task, err := s.store.Get(ctx, req.TaskID)
		if err != nil {
			return nil, err
		}
		return task, nil
	}

	// 创建新任务
	task := NewTask(s.store.GenerateID())
	task.SessionID = req.SessionID
	if req.Metadata != nil {
		task.Metadata = req.Metadata
	}

	if err := s.store.Create(ctx, task); err != nil {
		return nil, err
	}

	// 设置推送通知
	if req.PushNotification != nil && s.card.Capabilities.PushNotifications {
		s.pushStore.SetPushConfig(ctx, task.ID, req.PushNotification)
	}

	return task, nil
}

// applyUpdate 应用任务更新
func (s *Server) applyUpdate(task *Task, update *TaskUpdate) {
	if update == nil {
		return
	}

	// 更新状态
	if update.Status != nil {
		task.Status = *update.Status
	}

	// 添加消息到历史
	if update.Message != nil {
		task.History = append(task.History, *update.Message)
	}

	// 添加产物
	if update.Artifact != nil {
		if update.Artifact.Append && len(task.Artifacts) > 0 {
			// 追加到最后一个产物
			lastArtifact := &task.Artifacts[len(task.Artifacts)-1]
			lastArtifact.Parts = append(lastArtifact.Parts, update.Artifact.Parts...)
			lastArtifact.LastChunk = update.Artifact.LastChunk
		} else {
			update.Artifact.Index = len(task.Artifacts)
			task.Artifacts = append(task.Artifacts, *update.Artifact)
		}
	}

	// 更新元数据
	if update.Metadata != nil {
		if task.Metadata == nil {
			task.Metadata = make(map[string]any)
		}
		maps.Copy(task.Metadata, update.Metadata)
	}

	task.UpdatedAt = time.Now()
}

// subscribe 订阅任务更新
func (s *Server) subscribe(taskID string, ch chan StreamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribers[taskID] = append(s.subscribers[taskID], ch)
}

// unsubscribe 取消订阅
func (s *Server) unsubscribe(taskID string, ch chan StreamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	subs := s.subscribers[taskID]
	for i, sub := range subs {
		if sub == ch {
			s.subscribers[taskID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}

	if len(s.subscribers[taskID]) == 0 {
		delete(s.subscribers, taskID)
	}
}

// notifySubscribers 通知订阅者
//
// 安全说明：使用 recover 防护 send-on-closed-channel panic。
// 当 Server.Stop() 并发关闭 channel 时，此处的发送可能触发 panic，
// 通过 recover 将其转为静默跳过。
func (s *Server) notifySubscribers(taskID string, event StreamEvent) {
	s.mu.RLock()
	// 复制一份切片引用，避免长时间持锁
	subs := make([]chan StreamEvent, len(s.subscribers[taskID]))
	copy(subs, s.subscribers[taskID])
	s.mu.RUnlock()

	for _, ch := range subs {
		func() {
			defer func() {
				// 防止向已关闭 channel 发送时 panic
				recover()
			}()
			select {
			case ch <- event:
			default:
				// 通道已满，跳过
			}
		}()
	}
}

// sendPushNotification 发送推送通知
func (s *Server) sendPushNotification(ctx context.Context, task *Task) {
	if s.pushService == nil || !s.card.Capabilities.PushNotifications {
		return
	}

	config, err := s.pushStore.GetPushConfig(ctx, task.ID)
	if err != nil || config == nil {
		return
	}

	// 异步发送推送通知
	go s.pushService.Push(context.Background(), config, task)
}

// writeResult 写入成功响应
func (s *Server) writeResult(w http.ResponseWriter, result any, id any) {
	resp, err := NewJSONRPCResponse(id, result)
	if err != nil {
		s.writeError(w, NewInternalError(err.Error()), id)
		return
	}

	w.Header().Set("Content-Type", ContentTypeJSON)
	json.NewEncoder(w).Encode(resp)
}

// writeError 写入错误响应
func (s *Server) writeError(w http.ResponseWriter, err *Error, id any) {
	resp := NewJSONRPCErrorResponse(id, err)
	w.Header().Set("Content-Type", ContentTypeJSON)
	json.NewEncoder(w).Encode(resp)
}

// writeSSEEvent 写入 SSE 事件
func (s *Server) writeSSEEvent(w http.ResponseWriter, eventType string, data any) {
	dataBytes, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(dataBytes))
}

// writeSSEError 写入 SSE 错误
func (s *Server) writeSSEError(w http.ResponseWriter, err *Error) {
	s.writeSSEEvent(w, EventTypeError, &ErrorEvent{Error: err})
}

// ============== PushService 接口 ==============

// PushService 推送服务接口
type PushService interface {
	// Push 发送推送通知
	Push(ctx context.Context, config *PushNotificationConfig, task *Task) error
}

// ============== HTTPPushService ==============

// HTTPPushService HTTP 推送服务
type HTTPPushService struct {
	httpClient *http.Client
}

// NewHTTPPushService 创建 HTTP 推送服务
func NewHTTPPushService() *HTTPPushService {
	return &HTTPPushService{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Push 发送推送通知
func (s *HTTPPushService) Push(ctx context.Context, config *PushNotificationConfig, task *Task) error {
	if config.URL == "" {
		return nil
	}

	// 构造推送数据
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.URL, strings.NewReader(string(data)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", ContentTypeJSON)
	if config.Token != "" {
		req.Header.Set("Authorization", "Bearer "+config.Token)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("push failed: %d %s", resp.StatusCode, string(body))
	}

	return nil
}
