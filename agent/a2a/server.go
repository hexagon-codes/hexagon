package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	wire "github.com/a2aproject/a2a-go/v2/a2a"
	"maps"
	"net/http"
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
	executions  map[string]context.CancelFunc

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
	s := &Server{
		card:        card,
		handler:     handler,
		subscribers: make(map[string][]chan StreamEvent),
		executions:  make(map[string]context.CancelFunc),
		corsEnabled: true,
		corsOrigins: []string{"*"},
	}

	for _, opt := range opts {
		opt(s)
	}
	if s.store == nil || s.pushStore == nil {
		memStore := NewMemoryTaskStore()
		if s.store == nil {
			s.store = memStore
		}
		if s.pushStore == nil {
			s.pushStore = memStore
		}
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
	mux.HandleFunc(PathTasks, s.handleProtocolRPC)
	mux.HandleFunc(PathTaskSend, s.handleProtocolRPC)
	mux.HandleFunc(PathTaskSendStream, s.handleProtocolRPC)
	mux.HandleFunc(PathTaskGet, s.handleProtocolRPC)
	mux.HandleFunc(PathTaskCancel, s.handleProtocolRPC)
	mux.HandleFunc(PathTaskResubscribe, s.handleProtocolRPC)
	mux.HandleFunc(PathPushNotificationGet, s.handleProtocolRPC)
	mux.HandleFunc(PathPushNotificationSet, s.handleProtocolRPC)
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
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, A2A-Version, A2A-Extensions")
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
	if err := json.NewEncoder(w).Encode(s.protocolCard()); err != nil {
		s.writeError(w, NewInternalError(err.Error()), nil)
	}
}

// ============== 方法处理器 ==============

// handleSendMessage 处理发送消息
func (s *Server) handleSendMessage(ctx context.Context, params json.RawMessage) (*Task, *Error) {
	var req SendMessageRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewInvalidParamsError(err.Error())
	}

	if req.ReturnImmediately {
		return nil, NewUnsupportedOperationError("returnImmediately")
	}
	// 获取或创建任务
	task, err := s.getOrCreateTask(ctx, &req)
	if err != nil {
		return nil, ToA2AError(err)
	}

	runCtx, currentTask, release, beginErr := s.beginExecution(ctx, task.ID)
	if beginErr != nil {
		return nil, beginErr
	}
	defer release()
	ctx = runCtx
	task = currentTask
	req.Message.TaskID, req.Message.ContextID = task.ID, task.SessionID
	// 添加消息到历史
	task.History = append(task.History, req.Message)
	task.Status = TaskStatus{
		State:     TaskStateWorking,
		Timestamp: time.Now(),
	}
	task.UpdatedAt = time.Now()

	if err := s.commitTask(ctx, task); err != nil {
		return nil, ToA2AError(err)
	}
	if task.Status.State.IsTerminal() {
		return task, nil
	}

	// 处理任务
	update, err := s.handler.HandleTask(ctx, task, &req.Message)
	// 执行错误是任务结果；有效消息和附件必须随失败任务返回。
	s.applyUpdate(task, update)
	var invalidResponse *Error
	if err == nil && !task.Status.State.IsTerminal() && task.Status.State != TaskStateInputRequired && task.Status.State != TaskStateAuthRequired {
		// 阻塞处理器返回后没有后台完成通路，不能把中间状态作为成功响应。
		invalidResponse = NewError(CodeInvalidAgentResponse, "blocking task handler returned without a terminal or interrupted state", map[string]any{"taskId": task.ID})
		err = invalidResponse
	}
	if err != nil {
		task.Status.State = TaskStateFailed
		task.Status.Timestamp = time.Now()
		if task.Status.Message == nil {
			message := NewAgentMessage(err.Error())
			normalizeMessage(&message, task)
			task.Status.Message = &message
		}
	}
	if err := s.commitTask(ctx, task); err != nil {
		return nil, ToA2AError(err)
	}

	s.notifySubscribers(task.ID, &TaskStatusEvent{ID: task.ID, ContextID: task.SessionID, Status: task.Status, Final: task.Status.State.IsTerminal() || task.Status.State == TaskStateInputRequired || task.Status.State == TaskStateAuthRequired})
	if invalidResponse != nil && task.Status.State == TaskStateFailed {
		return nil, invalidResponse
	}
	projected, err := projectHistory(task, req.HistoryLimit)
	if err != nil {
		return nil, ToA2AError(err)
	}
	return projected, nil
}

// handleSendMessageStream 处理流式发送消息
func (s *Server) handleSendMessageStream(ctx context.Context, w http.ResponseWriter, params json.RawMessage) {
	var req SendMessageRequest
	if err := json.Unmarshal(params, &req); err != nil {
		s.writeSSEError(w, NewInvalidParamsError(err.Error()))
		return
	}
	if req.ReturnImmediately {
		s.writeSSEError(w, NewUnsupportedOperationError("returnImmediately"))
		return
	}
	task, err := s.getOrCreateTask(ctx, &req)
	if err != nil {
		s.writeSSEError(w, ToA2AError(err))
		return
	}
	runCtx, currentTask, release, beginErr := s.beginExecution(ctx, task.ID)
	if beginErr != nil {
		s.writeSSEError(w, beginErr)
		return
	}
	defer release()
	ctx = runCtx
	task = currentTask
	normalizeMessage(&req.Message, task)
	task.History = append(task.History, req.Message)
	task.Status = TaskStatus{State: TaskStateWorking, Timestamp: time.Now()}
	if err := s.commitTask(ctx, task); err != nil {
		s.writeSSEError(w, ToA2AError(err))
		return
	}
	w.Header().Set("Content-Type", ContentTypeSSE)
	w.Header().Set("Cache-Control", "no-cache")
	flusher := w.(http.Flusher)
	projected, err := projectHistory(task, req.HistoryLimit)
	if err != nil {
		s.writeSSEError(w, ToA2AError(err))
		flusher.Flush()
		return
	}
	s.writeSSEEvent(w, "task", &TaskEvent{Task: projected})
	flusher.Flush()
	if task.Status.State.IsTerminal() {
		return
	}
	emit := func(update *TaskUpdate, runErr error) bool {
		if update == nil && runErr == nil {
			return false
		}
		s.applyUpdate(task, update)
		if runErr != nil {
			message := NewAgentMessage(runErr.Error())
			normalizeMessage(&message, task)
			if task.Status.Message == nil {
				task.Status.Message = &message
			}
			task.Status.State = TaskStateFailed
			task.Status.Timestamp = time.Now()
		} else if update.Final && !task.Status.State.IsTerminal() && task.Status.State != TaskStateInputRequired && task.Status.State != TaskStateAuthRequired {
			task.Status.State = TaskStateCompleted
			task.Status.Timestamp = time.Now()
		}
		if update != nil && update.Message != nil && update.Status == nil {
			task.Status.Message = update.Message
		}
		if err := s.commitTask(ctx, task); err != nil {
			s.writeSSEError(w, ToA2AError(err))
			flusher.Flush()
			return true
		}
		if task.Status.State != TaskStateCanceled && update != nil && update.Artifact != nil {
			event := &ArtifactEvent{ID: task.ID, ContextID: task.SessionID, Artifact: *update.Artifact}
			s.writeSSEEvent(w, EventTypeArtifact, event)
			s.notifySubscribers(task.ID, event)
		}
		final := task.Status.State.IsTerminal() || task.Status.State == TaskStateInputRequired || task.Status.State == TaskStateAuthRequired
		status := &TaskStatusEvent{ID: task.ID, ContextID: task.SessionID, Status: task.Status, Final: final}
		s.writeSSEEvent(w, EventTypeTaskStatus, status)
		s.notifySubscribers(task.ID, status)
		flusher.Flush()
		return final
	}
	stream, ok := s.handler.(StreamingTaskHandler)
	if !ok {
		update, err := s.handler.HandleTask(ctx, task, &req.Message)
		if err == nil && (update == nil || !update.Final) &&
			(update == nil || update.Status == nil || (!update.Status.State.IsTerminal() && update.Status.State != TaskStateInputRequired && update.Status.State != TaskStateAuthRequired)) {
			err = NewError(CodeInvalidAgentResponse, "blocking task handler returned without a terminal or interrupted state", map[string]any{"taskId": task.ID})
		}
		emit(update, err)
		return
	}
	updates, err := stream.HandleTaskStream(ctx, task, &req.Message)
	if err != nil {
		emit(nil, err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			if current, err := s.store.Get(context.WithoutCancel(ctx), task.ID); err == nil && current.Status.State == TaskStateCanceled {
				s.writeSSEEvent(w, EventTypeTaskStatus, &TaskStatusEvent{ID: task.ID, ContextID: task.SessionID, Status: current.Status, Final: true})
				flusher.Flush()
			}
			return
		case update, open := <-updates:
			if !open {
				emit(nil, fmt.Errorf("task stream ended before final state"))
				return
			}
			if emit(update, nil) {
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
	limit := historyLimit(req.HistoryLimit, req.HistoryLength)
	if limit != nil && *limit < 0 {
		return nil, NewInvalidParamsError("historyLength must be non-negative")
	}
	task, err = projectHistory(task, limit)
	if err != nil {
		return nil, ToA2AError(err)
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
	return s.protocolList(ctx, &req)
}

// handleCancelTask 处理取消任务
func (s *Server) handleCancelTask(ctx context.Context, params json.RawMessage) (*Task, *Error) {
	var req CancelTaskRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, NewInvalidParamsError(err.Error())
	}
	s.mu.Lock()
	task, err := s.store.Get(ctx, req.ID)
	if err != nil {
		s.mu.Unlock()
		return nil, ToA2AError(err)
	}
	if task.Status.State.IsTerminal() {
		s.mu.Unlock()
		return nil, NewTaskNotCancelableError(req.ID)
	}
	task.Status = TaskStatus{State: TaskStateCanceled, Timestamp: time.Now()}
	task.UpdatedAt = time.Now()
	if err := s.store.Update(ctx, task); err != nil {
		s.mu.Unlock()
		return nil, ToA2AError(err)
	}
	if cancel := s.executions[task.ID]; cancel != nil {
		cancel()
	}
	s.mu.Unlock()
	s.notifySubscribers(task.ID, &TaskStatusEvent{ID: task.ID, ContextID: task.SessionID, Status: task.Status, Final: true})
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
	events := make(chan StreamEvent, 100)
	s.subscribe(req.TaskID, events)
	defer s.unsubscribe(req.TaskID, events)
	task, err := s.store.Get(ctx, req.TaskID)
	if err != nil {
		s.writeSSEError(w, ToA2AError(err))
		return
	}
	if task.Status.State.IsTerminal() {
		s.writeSSEError(w, NewUnsupportedOperationError("cannot subscribe to terminal task"))
		return
	}
	w.Header().Set("Content-Type", ContentTypeSSE)
	w.Header().Set("Cache-Control", "no-cache")
	flusher := w.(http.Flusher)
	s.writeSSEEvent(w, "task", &TaskEvent{Task: task})
	flusher.Flush()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			s.writeProtocolEvent(w, event)
			flusher.Flush()
			if _, failed := event.(*ErrorEvent); failed {
				return
			}
			if status, ok := event.(*TaskStatusEvent); ok && status.Final {
				return
			}
		}
	}
}

// ============== 辅助方法 ==============

// getOrCreateTask 获取或创建任务
func (s *Server) getOrCreateTask(ctx context.Context, req *SendMessageRequest) (*Task, error) {
	if req.ReturnImmediately {
		return nil, NewUnsupportedOperationError("returnImmediately")
	}
	if req.PushNotification != nil {
		return nil, NewPushNotSupportedError()
	}
	if req.TaskID != "" {
		// 获取已有任务
		task, err := s.store.Get(ctx, req.TaskID)
		if err != nil {
			return nil, err
		}
		if task.Status.State.IsTerminal() {
			return nil, NewUnsupportedOperationError("terminal tasks are immutable")
		}
		return task, nil
	}

	// 创建新任务
	task := NewTask(s.store.GenerateID())
	if req.SessionID != "" {
		task.SessionID = req.SessionID
	}
	if req.Metadata != nil {
		task.Metadata = req.Metadata
	}

	if err := s.store.Create(ctx, task); err != nil {
		return nil, err
	}

	// 设置推送通知
	if req.PushNotification != nil && s.card.Capabilities.PushNotifications {
		if err := s.pushStore.SetPushConfig(ctx, task.ID, req.PushNotification); err != nil {
			return nil, err
		}
	}

	return task, nil
}

// applyUpdate 应用任务更新
func (s *Server) applyUpdate(task *Task, update *TaskUpdate) {
	if update == nil {
		return
	}

	if update.Message != nil {
		normalizeMessage(update.Message, task)
	}
	if update.Status != nil && update.Status.Message != nil {
		normalizeMessage(update.Status.Message, task)
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
		if update.Artifact.ID == "" {
			if update.Artifact.Append && len(task.Artifacts) > 0 {
				update.Artifact.ID = task.Artifacts[len(task.Artifacts)-1].ID
			}
			if update.Artifact.ID == "" {
				update.Artifact.ID = string(wire.NewArtifactID())
			}
		}
		index := -1
		for i := range task.Artifacts {
			if task.Artifacts[i].ID == update.Artifact.ID {
				index = i
				break
			}
		}
		if index >= 0 && update.Artifact.Append {
			task.Artifacts[index].Parts = append(task.Artifacts[index].Parts, update.Artifact.Parts...)
			task.Artifacts[index].LastChunk = update.Artifact.LastChunk
		} else if index >= 0 {
			task.Artifacts[index] = *update.Artifact
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

// notifySubscribers 保持事件顺序；慢订阅者收到显式错误并结束，不静默丢弃终态。
func (s *Server) notifySubscribers(taskID string, event StreamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subscribers := s.subscribers[taskID]
	remaining := subscribers[:0]
	for _, ch := range subscribers {
		select {
		case ch <- event:
			remaining = append(remaining, ch)
		default:
			// 通道由服务创建并持有，已满时腾出一个位置交付明确的流失败。
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- &ErrorEvent{Error: NewInternalError("task subscription buffer exhausted")}:
			default:
			}
		}
	}
	if len(remaining) == 0 {
		delete(s.subscribers, taskID)
	} else {
		s.subscribers[taskID] = remaining
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
	notification, err := NewTaskStatusNotification(task)
	if err != nil {
		return
	}

	// 异步发送推送通知
	go s.pushService.Push(context.Background(), clonePushNotificationConfig(config), notification)
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
	s.writeProtocolEvent(w, data)
}

// writeSSEError 写入 SSE 错误
func (s *Server) writeSSEError(w http.ResponseWriter, err *Error) {
	s.writeSSEEvent(w, EventTypeError, &ErrorEvent{Error: err})
}
