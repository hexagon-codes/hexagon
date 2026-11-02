package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrTurnStopped = errors.New("runtime: turn stop requested")

// TurnIdentity 绑定控制请求的完整目标；迟到请求不能指向之后的另一轮。
type TurnIdentity struct {
	SessionID string
	RunID     string
	TurnID    string
}

type StopBoundary string

const (
	StopImmediate  StopBoundary = "immediate"
	StopAfterModel StopBoundary = "after_model"
	StopAfterTools StopBoundary = "after_tools"
)

type TurnController struct {
	mu     sync.Mutex
	active map[TurnIdentity]*TurnHandle
}
type TurnHandle struct {
	controller *TurnController
	identity   TurnIdentity
	ctx        context.Context
	cancel     context.CancelCauseFunc
	stopped    StopBoundary
	done       chan struct{}
	once       sync.Once
}
type turnHandleKey struct{}

func NewTurnController() *TurnController {
	return &TurnController{active: make(map[TurnIdentity]*TurnHandle)}
}

func (c *TurnController) Begin(parent context.Context, identity TurnIdentity) (*TurnHandle, error) {
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if identity.SessionID == "" || identity.RunID == "" || identity.TurnID == "" {
		return nil, fmt.Errorf("runtime: turn identity is incomplete")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		c.active = make(map[TurnIdentity]*TurnHandle)
	}
	if _, exists := c.active[identity]; exists {
		return nil, fmt.Errorf("runtime: turn is already active")
	}
	ctx, cancel := context.WithCancelCause(parent)
	handle := &TurnHandle{controller: c, identity: identity, cancel: cancel, done: make(chan struct{})}
	handle.ctx = context.WithValue(ctx, turnHandleKey{}, handle)
	c.active[identity] = handle
	return handle, nil
}

func (h *TurnHandle) Context() context.Context { return h.ctx }
func (h *TurnHandle) Done() <-chan struct{}    { return h.done }

// Finish 由运行所有者在保存部分结果后调用，Done 表示执行已退出而非仅请求取消。
func (h *TurnHandle) Finish() {
	h.once.Do(func() {
		h.controller.mu.Lock()
		if h.controller.active[h.identity] == h {
			delete(h.controller.active, h.identity)
		}
		h.controller.mu.Unlock()
		h.cancel(nil)
		close(h.done)
	})
}

func (c *TurnController) RequestStop(identity TurnIdentity, boundary StopBoundary) bool {
	if boundary != StopImmediate && boundary != StopAfterModel && boundary != StopAfterTools {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	handle, exists := c.active[identity]
	if !exists {
		return false
	}
	if handle.stopped == StopImmediate {
		return true
	}
	handle.stopped = boundary
	if boundary == StopImmediate {
		handle.cancel(ErrTurnStopped)
	}
	return true
}

// ReachTurnBoundary 由执行核在保存当前调用事实后、下一次派发前调用。
// 子 Run 不替父 Run 触发边界，父 ctx 的实际取消仍会传播给子调用。
func ReachTurnBoundary(ctx context.Context, runID string, boundary StopBoundary) error {
	handle, ok := ctx.Value(turnHandleKey{}).(*TurnHandle)
	if !ok || handle.identity.RunID != runID {
		return ctx.Err()
	}
	handle.controller.mu.Lock()
	if handle.stopped == boundary || handle.stopped == StopImmediate {
		handle.cancel(ErrTurnStopped)
	}
	handle.controller.mu.Unlock()
	return context.Cause(ctx)
}
