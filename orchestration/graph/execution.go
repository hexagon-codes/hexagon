package graph

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/interrupt"
)

// NodePanicError 保留用户节点或复制回调的 panic 及任务定位信息。
// Stack 供诊断使用，不直接拼进用户可见的错误消息。
type NodePanicError struct {
	Node  string
	Value any
	Stack []byte
}

func nodeControlOrUnknown(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if _, ok := interrupt.IsInterruptSignal(err); ok {
		return true
	}
	if _, ok := IsInterruptError(err); ok {
		return true
	}
	return llm.IsUnsafeToReplay(err)
}

func (e *NodePanicError) Error() string {
	return fmt.Sprintf("node %q panicked: %v", e.Node, e.Value)
}

// UnsafeToReplay 表明 panic 之前可能已经发生外部动作，不能自动重新执行。
func (*NodePanicError) UnsafeToReplay() bool { return true }

// cloneState 捕获所有执行入口中的状态复制异常，保留未知副作用与定位信息。
func cloneState(state State, node string) (copy State, err error) {
	defer func() {
		if value := recover(); value != nil {
			copy = nil
			err = &NodePanicError{Node: node, Value: value, Stack: debug.Stack()}
		}
	}()
	return state.Clone()
}

// executeNodeHandler 统一节点策略，节点显式配置覆盖执行器默认配置。
// 超时为合作式取消；忽略 context 的处理器返回后也不能采用其过期结果。
func executeNodeHandler[S State](ctx context.Context, node *Node[S], input S, fallback *RetryPolicy) (output S, err error) {
	defer func() {
		if value := recover(); value != nil {
			output = input
			err = &NodePanicError{Node: node.Name, Value: value, Stack: debug.Stack()}
		}
	}()
	policy := node.RetryPolicy
	if policy == nil {
		policy = fallback
	}
	if node.Timeout < 0 || node.Timeout > int64((1<<63-1)/time.Millisecond) {
		return input, fmt.Errorf("node %q has invalid timeout", node.Name)
	}
	retries, delay := 0, time.Duration(0)
	if policy != nil {
		if policy.MaxRetries < 0 || policy.InitialDelay < 0 || policy.MaxDelay < 0 || policy.Multiplier < 0 || policy.Multiplier != policy.Multiplier {
			return input, fmt.Errorf("node %q has invalid retry policy", node.Name)
		}
		retries = policy.MaxRetries
		if policy.InitialDelay > int64((1<<63-1)/time.Millisecond) || policy.MaxDelay > int64((1<<63-1)/time.Millisecond) {
			return input, fmt.Errorf("node %q retry delay overflows duration", node.Name)
		}
		delay = time.Duration(policy.InitialDelay) * time.Millisecond
		if policy.MaxDelay > 0 && delay > time.Duration(policy.MaxDelay)*time.Millisecond {
			delay = time.Duration(policy.MaxDelay) * time.Millisecond
		}
	}
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return input, err
		}
		attemptCtx, cancel := context.WithCancel(ctx)
		if node.Timeout > 0 {
			cancel()
			attemptCtx, cancel = context.WithTimeout(ctx, time.Duration(node.Timeout)*time.Millisecond)
		}
		output, err := runIsolatedNode(attemptCtx, node.Name, node.Handler, input)
		if cause := attemptCtx.Err(); cause != nil {
			err = errors.Join(err, cause)
		}
		cancel()
		if err == nil {
			return output, nil
		}
		if attempt >= retries || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return output, err
		}
		if _, ok := interrupt.IsInterruptSignal(err); ok {
			return output, err
		}
		if _, ok := IsInterruptError(err); ok {
			return output, err
		}
		if llm.IsUnsafeToReplay(err) {
			return output, err
		}
		if policy.RetryOn != nil && !policy.RetryOn(err) {
			return output, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return output, ctx.Err()
		case <-timer.C:
		}
		if policy.Multiplier > 1 {
			limit := time.Duration(1<<63 - 1)
			if policy.MaxDelay > 0 {
				limit = time.Duration(policy.MaxDelay) * time.Millisecond
			}
			next := float64(delay) * policy.Multiplier
			if next >= float64(limit) {
				delay = limit
			} else {
				delay = time.Duration(next)
			}
		}
	}
}

// runIsolatedNode 在同一执行边界内复制输入并运行用户节点。
// 不修改原输入；返回值是否采用由执行器决定，错误伴随的部分结果不能当作成功提交。
func runIsolatedNode[S State](ctx context.Context, name string, handler NodeHandler[S], input S) (output S, err error) {
	output = input
	defer func() {
		if value := recover(); value != nil {
			output = input
			err = &NodePanicError{Node: name, Value: value, Stack: debug.Stack()}
		}
	}()
	cloned, err := cloneState(input, name)
	if err != nil {
		return input, fmt.Errorf("clone input for node %q: %w", name, err)
	}
	typed, ok := cloned.(S)
	if !ok {
		return input, fmt.Errorf("clone input for node %q: expected %T, got %T", name, input, cloned)
	}
	if err := ctx.Err(); err != nil {
		return input, err
	}
	output, err = handler(ctx, typed)
	return output, err
}
