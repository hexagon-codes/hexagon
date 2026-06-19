package a2a

import (
	"context"
	"time"
)

// ============== TaskHandler 接口 ==============

// TaskHandler 任务处理器接口
// 实现此接口来处理 A2A 任务请求。
//
// 使用示例:
//
//	type MyHandler struct {
//	    llm llm.Provider
//	}
//
//	func (h *MyHandler) HandleTask(ctx context.Context, task *Task, msg *Message) (*TaskUpdate, error) {
//	    // 调用 LLM 处理消息
//	    resp, err := h.llm.Complete(ctx, ...)
//	    if err != nil {
//	        return &TaskUpdate{
//	            Status: TaskStatus{State: TaskStateFailed},
//	        }, nil
//	    }
//
//	    return &TaskUpdate{
//	        Status: TaskStatus{State: TaskStateCompleted},
//	        Message: &Message{Role: RoleAgent, Parts: []Part{&TextPart{Text: resp}}},
//	    }, nil
//	}
type TaskHandler interface {
	// HandleTask 处理任务消息
	//
	// 参数:
	//   - ctx: 上下文
	//   - task: 当前任务状态
	//   - msg: 收到的消息
	//
	// 返回:
	//   - TaskUpdate: 任务更新（状态、消息、产物等）
	//   - error: 处理错误
	HandleTask(ctx context.Context, task *Task, msg *Message) (*TaskUpdate, error)
}

// StreamingTaskHandler 流式任务处理器接口
// 支持流式输出的处理器应实现此接口。
type StreamingTaskHandler interface {
	TaskHandler

	// HandleTaskStream 流式处理任务消息
	//
	// 参数:
	//   - ctx: 上下文
	//   - task: 当前任务状态
	//   - msg: 收到的消息
	//
	// 返回:
	//   - <-chan *TaskUpdate: 任务更新流
	//   - error: 处理错误
	HandleTaskStream(ctx context.Context, task *Task, msg *Message) (<-chan *TaskUpdate, error)
}

// ============== TaskUpdate ==============

// TaskUpdate 任务更新
// 表示任务状态的一次更新。
type TaskUpdate struct {
	// Status 新状态（可选，为空表示不更新状态）
	Status *TaskStatus `json:"status,omitempty"`

	// Message Agent 回复消息（可选）
	Message *Message `json:"message,omitempty"`

	// Artifact 产物（可选）
	Artifact *Artifact `json:"artifact,omitempty"`

	// Metadata 元数据更新（可选）
	Metadata map[string]any `json:"metadata,omitempty"`

	// Final 是否为最终更新
	Final bool `json:"final,omitempty"`
}

// NewStatusUpdate 创建状态更新
func NewStatusUpdate(state TaskState) *TaskUpdate {
	return &TaskUpdate{
		Status: &TaskStatus{
			State:     state,
			Timestamp: time.Now(),
		},
	}
}

// NewMessageUpdate 创建消息更新
func NewMessageUpdate(msg *Message) *TaskUpdate {
	return &TaskUpdate{
		Message: msg,
	}
}

// NewArtifactUpdate 创建产物更新
func NewArtifactUpdate(artifact *Artifact) *TaskUpdate {
	return &TaskUpdate{
		Artifact: artifact,
	}
}

// NewCompletedUpdate 创建完成更新
func NewCompletedUpdate(msg *Message) *TaskUpdate {
	return &TaskUpdate{
		Status: &TaskStatus{
			State:     TaskStateCompleted,
			Message:   msg,
			Timestamp: time.Now(),
		},
		Message: msg,
		Final:   true,
	}
}

// NewFailedUpdate 创建失败更新
func NewFailedUpdate(errMsg string) *TaskUpdate {
	msg := &Message{
		Role: RoleAgent,
		Parts: []Part{
			&TextPart{Text: errMsg},
		},
	}
	return &TaskUpdate{
		Status: &TaskStatus{
			State:     TaskStateFailed,
			Message:   msg,
			Timestamp: time.Now(),
		},
		Message: msg,
		Final:   true,
	}
}

// NewInputRequiredUpdate 创建需要输入更新
func NewInputRequiredUpdate(prompt string) *TaskUpdate {
	msg := &Message{
		Role: RoleAgent,
		Parts: []Part{
			&TextPart{Text: prompt},
		},
	}
	return &TaskUpdate{
		Status: &TaskStatus{
			State:     TaskStateInputRequired,
			Message:   msg,
			Timestamp: time.Now(),
		},
		Message: msg,
	}
}

// ============== FuncHandler ==============

// TaskFunc 任务处理函数类型
type TaskFunc func(ctx context.Context, task *Task, msg *Message) (*TaskUpdate, error)

// FuncHandler 函数式任务处理器
// 将普通函数包装为 TaskHandler。
type FuncHandler struct {
	fn TaskFunc
}

// NewFuncHandler 创建函数式处理器
func NewFuncHandler(fn TaskFunc) *FuncHandler {
	return &FuncHandler{fn: fn}
}

// HandleTask 实现 TaskHandler 接口
func (h *FuncHandler) HandleTask(ctx context.Context, task *Task, msg *Message) (*TaskUpdate, error) {
	return h.fn(ctx, task, msg)
}

// ============== StreamingFuncHandler ==============

// StreamingTaskFunc 流式任务处理函数类型
type StreamingTaskFunc func(ctx context.Context, task *Task, msg *Message) (<-chan *TaskUpdate, error)

// StreamingFuncHandler 流式函数处理器
type StreamingFuncHandler struct {
	*FuncHandler
	streamFn StreamingTaskFunc
}

// NewStreamingFuncHandler 创建流式函数处理器
func NewStreamingFuncHandler(fn TaskFunc, streamFn StreamingTaskFunc) *StreamingFuncHandler {
	return &StreamingFuncHandler{
		FuncHandler: NewFuncHandler(fn),
		streamFn:    streamFn,
	}
}

// HandleTaskStream 实现 StreamingTaskHandler 接口
func (h *StreamingFuncHandler) HandleTaskStream(ctx context.Context, task *Task, msg *Message) (<-chan *TaskUpdate, error) {
	return h.streamFn(ctx, task, msg)
}

// ============== ChainHandler ==============

// ChainHandler 链式处理器
// 按顺序执行多个处理器，第一个返回非空结果的处理器生效。
type ChainHandler struct {
	handlers []TaskHandler
}

// NewChainHandler 创建链式处理器
func NewChainHandler(handlers ...TaskHandler) *ChainHandler {
	return &ChainHandler{handlers: handlers}
}

// HandleTask 实现 TaskHandler 接口
func (h *ChainHandler) HandleTask(ctx context.Context, task *Task, msg *Message) (*TaskUpdate, error) {
	for _, handler := range h.handlers {
		update, err := handler.HandleTask(ctx, task, msg)
		if err != nil {
			return nil, err
		}
		if update != nil {
			return update, nil
		}
	}
	return nil, nil
}

// ============== EchoHandler ==============

// EchoHandler 回显处理器
// 简单地将用户消息回显，用于测试。
type EchoHandler struct{}

// NewEchoHandler 创建回显处理器
func NewEchoHandler() *EchoHandler {
	return &EchoHandler{}
}

// HandleTask 实现 TaskHandler 接口
func (h *EchoHandler) HandleTask(_ context.Context, _ *Task, msg *Message) (*TaskUpdate, error) {
	// 获取用户消息文本
	text := msg.GetTextContent()
	if text == "" {
		text = "收到非文本消息"
	}

	// 返回回显消息
	return NewCompletedUpdate(&Message{
		Role: RoleAgent,
		Parts: []Part{
			&TextPart{Text: "Echo: " + text},
		},
	}), nil
}

// ============== SkillRouter ==============

// SkillMatcher 技能匹配器
type SkillMatcher func(msg *Message) bool

// SkillRoute 技能路由
type SkillRoute struct {
	// Matcher 匹配器
	Matcher SkillMatcher

	// Handler 处理器
	Handler TaskHandler
}

// SkillRouter 技能路由器
// 根据消息内容路由到不同的处理器。
type SkillRouter struct {
	routes         []SkillRoute
	defaultHandler TaskHandler
}

// NewSkillRouter 创建技能路由器
func NewSkillRouter() *SkillRouter {
	return &SkillRouter{
		routes: make([]SkillRoute, 0),
	}
}

// Route 添加路由
func (r *SkillRouter) Route(matcher SkillMatcher, handler TaskHandler) *SkillRouter {
	r.routes = append(r.routes, SkillRoute{
		Matcher: matcher,
		Handler: handler,
	})
	return r
}

// Default 设置默认处理器
func (r *SkillRouter) Default(handler TaskHandler) *SkillRouter {
	r.defaultHandler = handler
	return r
}

// HandleTask 实现 TaskHandler 接口
func (r *SkillRouter) HandleTask(ctx context.Context, task *Task, msg *Message) (*TaskUpdate, error) {
	// 尝试匹配路由
	for _, route := range r.routes {
		if route.Matcher(msg) {
			return route.Handler.HandleTask(ctx, task, msg)
		}
	}

	// 使用默认处理器
	if r.defaultHandler != nil {
		return r.defaultHandler.HandleTask(ctx, task, msg)
	}

	// 没有匹配的处理器
	return NewFailedUpdate("no handler found for message"), nil
}

// ============== 匹配器工厂函数 ==============

// PrefixMatcher 创建前缀匹配器
func PrefixMatcher(prefix string) SkillMatcher {
	return func(msg *Message) bool {
		text := msg.GetTextContent()
		return len(text) >= len(prefix) && text[:len(prefix)] == prefix
	}
}

// ContainsMatcher 创建包含匹配器
func ContainsMatcher(substr string) SkillMatcher {
	return func(msg *Message) bool {
		text := msg.GetTextContent()
		for i := 0; i <= len(text)-len(substr); i++ {
			if text[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	}
}

// AlwaysMatcher 创建始终匹配的匹配器
func AlwaysMatcher() SkillMatcher {
	return func(_ *Message) bool {
		return true
	}
}

// ============== 中间件 ==============

// Middleware 中间件类型
type Middleware func(TaskHandler) TaskHandler

// ApplyMiddleware 应用中间件
func ApplyMiddleware(handler TaskHandler, middlewares ...Middleware) TaskHandler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// LoggingMiddleware 日志中间件
func LoggingMiddleware(logger func(format string, args ...any)) Middleware {
	return func(next TaskHandler) TaskHandler {
		return NewFuncHandler(func(ctx context.Context, task *Task, msg *Message) (*TaskUpdate, error) {
			start := time.Now()
			logger("handling task %s, message: %s", task.ID, msg.GetTextContent())

			update, err := next.HandleTask(ctx, task, msg)

			duration := time.Since(start)
			if err != nil {
				logger("task %s failed after %v: %v", task.ID, duration, err)
			} else if update != nil && update.Status != nil {
				logger("task %s completed with status %s after %v", task.ID, update.Status.State, duration)
			}

			return update, err
		})
	}
}

// RecoveryMiddleware 恢复中间件
func RecoveryMiddleware() Middleware {
	return func(next TaskHandler) TaskHandler {
		return NewFuncHandler(func(ctx context.Context, task *Task, msg *Message) (update *TaskUpdate, err error) {
			defer func() {
				if r := recover(); r != nil {
					err = nil
					update = NewFailedUpdate("internal error: handler panicked")
				}
			}()
			return next.HandleTask(ctx, task, msg)
		})
	}
}

// TimeoutMiddleware 超时中间件
func TimeoutMiddleware(timeout time.Duration) Middleware {
	return func(next TaskHandler) TaskHandler {
		return NewFuncHandler(func(ctx context.Context, task *Task, msg *Message) (*TaskUpdate, error) {
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			done := make(chan struct{})
			var update *TaskUpdate
			var err error

			go func() {
				update, err = next.HandleTask(ctx, task, msg)
				close(done)
			}()

			select {
			case <-done:
				return update, err
			case <-ctx.Done():
				return NewFailedUpdate("handler timeout"), nil
			}
		})
	}
}
