package runtime

import (
	"context"
	"encoding/json"
	"time"
)

type TaskStatus string

const (
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
	TaskSkipped   TaskStatus = "skipped"
)

// TaskActivation 固定稳定任务实例、本次物理 attempt 及其读取的输入版本。
// 新 attempt 可替换未完成的旧 attempt，不能借 Run revision 变化重做已保存结果。
type TaskActivation struct {
	TaskID        string `json:"task_id"`
	Activation    string `json:"activation"`
	AttemptID     string `json:"attempt_id"`
	InputRevision uint64 `json:"input_revision"`
}

// TaskResult 保存一次激活的完成事实，空 Result 仍可表示合法成功。
// InputRevision 是任务读取的基准版本，不要求等于结果保存时的 Run revision。
type TaskResult struct {
	TaskID          string          `json:"task_id"`
	Activation      string          `json:"activation"`
	AttemptID       string          `json:"attempt_id"`
	InputRevision   uint64          `json:"input_revision"`
	Status          TaskStatus      `json:"status"`
	Result          json.RawMessage `json:"result,omitempty"`
	Writes          json.RawMessage `json:"writes,omitempty"`
	Error           string          `json:"error,omitempty"`
	AdoptedRevision uint64          `json:"adopted_revision,omitempty"`
}

func (r TaskResult) withoutAdoption() TaskResult { r.AdoptedRevision = 0; return r }

// InboundInput 的接受与消费分开持久化，重复 Signal 不等于再次消费。
type InboundInput struct {
	ID               string          `json:"id"`
	Digest           string          `json:"digest"`
	Target           string          `json:"target,omitempty"`
	Payload          json.RawMessage `json:"payload"`
	ConsumedRevision uint64          `json:"consumed_revision,omitempty"`
}

type SubmitRequest struct {
	Scope   string
	InputID string
	RunID   string
	Spec    RunSpec
	Input   json.RawMessage
}

// TaskStore 由宿主消费；构造函数不启动 worker，连接与 worker ctx 均归宿主所有。
type TaskStore interface {
	Submit(context.Context, SubmitRequest) (RunRecord, error)
	GetRun(context.Context, string, string) (RunRecord, bool, error)
	Signal(context.Context, string, string, InboundInput) (InboundInput, error)
	Claim(context.Context, string, string, ExecutionAuthority, time.Duration) (RunRecord, bool, error)
	Renew(context.Context, string, string, ExecutionAuthority, time.Duration) error
	Release(context.Context, string, string, ExecutionAuthority) error
	Cancel(context.Context, string, string, string) error
}

type executionContextKey struct{}

// ExecutionContext 由 worker 按 Run 注入，不将执行权放入可共享 Runner 配置。
type ExecutionContext struct {
	Scope     string
	RunID     string
	Authority ExecutionAuthority
}

func ContextWithExecutionAuthority(ctx context.Context, scope string, authority ExecutionAuthority) context.Context {
	return context.WithValue(ctx, executionContextKey{}, ExecutionContext{Scope: scope, Authority: authority})
}

// ContextWithRunAuthority 将执行权绑定到指定 Run；嵌套调用不能借父权威写独立子 Run。
func ContextWithRunAuthority(ctx context.Context, scope, runID string, authority ExecutionAuthority) context.Context {
	return context.WithValue(ctx, executionContextKey{}, ExecutionContext{Scope: scope, RunID: runID, Authority: authority})
}

func ExecutionAuthorityFromContext(ctx context.Context) (ExecutionContext, bool) {
	execution, ok := ctx.Value(executionContextKey{}).(ExecutionContext)
	return execution, ok && execution.Scope != "" && execution.Authority.Owner != "" && execution.Authority.AttemptID != ""
}
