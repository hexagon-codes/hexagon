package runtime

import (
	"context"
	"encoding/json"
	"fmt"
)

const TaskWaiting TaskStatus = "waiting"

// TaskCall 标识父运行中的不可变任务激活。显式恢复等待项应使用新 Activation，
// 成功兄弟任务保持原身份；Input 为固定版本数据，不能放连接或任意 Go 对象。
type TaskCall struct {
	ID         string
	Activation string
	Input      json.RawMessage
	ReplaySafe bool
	// ResumeTargets 只列该任务结果真正采用的原中断目标，不能列尚未执行的兄弟目标。
	ResumeTargets []string
}

type TaskOutcome struct {
	Status TaskStatus      `json:"status"`
	Data   json.RawMessage `json:"data,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type TaskExecutor interface {
	ExecuteTask(context.Context, TaskCall, func(context.Context) (TaskOutcome, error)) (TaskOutcome, error)
}

type taskExecutorKey struct{}

func ContextWithTaskExecutor(ctx context.Context, executor TaskExecutor) context.Context {
	return context.WithValue(ctx, taskExecutorKey{}, executor)
}

func TaskExecutorFromContext(ctx context.Context) (TaskExecutor, bool) {
	executor, ok := ctx.Value(taskExecutorKey{}).(TaskExecutor)
	return executor, ok && executor != nil
}

// ExecuteTask 在轻量运行中直接执行，在持久宿主中由同一恢复权威保存/复用结果。
// 回调的结构化中断应放入 Data 并显式解码，不能依赖持久化后的 error 文本恢复类型。
func ExecuteTask(ctx context.Context, call TaskCall, execute func(context.Context) (TaskOutcome, error)) (TaskOutcome, error) {
	if execute == nil {
		return TaskOutcome{}, fmt.Errorf("runtime: task execution callback is missing")
	}
	if executor, ok := TaskExecutorFromContext(ctx); ok {
		return executor.ExecuteTask(ctx, call, execute)
	}
	return execute(ctx)
}

// TaskOutcomeUnknownError 保留原任务身份，恢复者须核对而非另建物理动作。
type TaskOutcomeUnknownError struct{ TaskID, Activation string }

func (e *TaskOutcomeUnknownError) Error() string {
	return fmt.Sprintf("runtime: task outcome is unknown: %s (%s)", e.TaskID, e.Activation)
}
func (*TaskOutcomeUnknownError) UnsafeToReplay() bool { return true }

type TaskExecutionError struct{ TaskID, Message string }

func (e *TaskExecutionError) Error() string {
	return fmt.Sprintf("runtime: task %s failed: %s", e.TaskID, e.Message)
}
func (*TaskExecutionError) UnsafeToReplay() bool { return true }
