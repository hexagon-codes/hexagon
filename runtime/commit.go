package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrCommitConflict   = errors.New("runtime: commit identity conflict")
	ErrRevisionConflict = errors.New("runtime: revision conflict")
	ErrExecutionLost    = errors.New("runtime: execution authority lost")
	ErrCommitUnknown    = errors.New("runtime: commit outcome is unknown")
	ErrRunNotFound      = errors.New("runtime: run not found")
	ErrRunTerminal      = errors.New("runtime: run is terminal")
)

// ExecutionAuthority 标识协调者的执行代次，与并行 TaskResult 的物理 attempt 分开。
// 存储必须同时核对三项及租约。
type ExecutionAuthority struct {
	Owner     string `json:"owner"`
	AttemptID string `json:"attempt_id"`
	Fence     uint64 `json:"fence"`
}

type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunWaiting   RunStatus = "waiting"
	RunSucceeded RunStatus = "succeeded"
	RunFailed    RunStatus = "failed"
	RunCancelled RunStatus = "cancelled"
)

func (s RunStatus) Terminal() bool { return s == RunSucceeded || s == RunFailed || s == RunCancelled }

// RunRecord 是唯一恢复权威的值投影，Snapshot 保存私有快照信封而非 Go 指针。
type RunRecord struct {
	Scope           string                       `json:"scope"`
	RunID           string                       `json:"run_id"`
	Spec            RunSpec                      `json:"spec"`
	Revision        uint64                       `json:"revision"`
	Status          RunStatus                    `json:"status"`
	Authority       ExecutionAuthority           `json:"authority"`
	LeaseUntil      time.Time                    `json:"lease_until"`
	WakeAt          time.Time                    `json:"wake_at,omitempty"`
	Snapshot        []byte                       `json:"snapshot"`
	Tasks           map[string]TaskResult        `json:"tasks,omitempty"`
	TaskActivations map[string]TaskActivation    `json:"task_activations,omitempty"`
	Inputs          map[string]InboundInput      `json:"inputs,omitempty"`
	Operations      map[string]Operation         `json:"operations,omitempty"`
	Artifacts       map[string]ArtifactReference `json:"artifacts,omitempty"`
	Events          []StoredEvent                `json:"events,omitempty"`
	NextSequence    int64                        `json:"next_sequence"`
	CancelReason    string                       `json:"cancel_reason,omitempty"`
	// WaitingTargets 固定当前可接续的目标；nil 为未声明，空切片为当前无等待。
	WaitingTargets *[]string `json:"waiting_targets,omitempty"`
}

// CommitRequest 在第一次提交前冻结，结果未知时必须复用同一 ID 与全部内容。
// Snapshot 为 nil 表示不改变快照；空但非 nil 表示清空快照。
type CommitRequest struct {
	Scope            string              `json:"scope"`
	RunID            string              `json:"run_id"`
	CommitID         string              `json:"commit_id"`
	Digest           string              `json:"-"`
	ExpectedRevision uint64              `json:"expected_revision"`
	Authority        ExecutionAuthority  `json:"authority"`
	Snapshot         []byte              `json:"snapshot"`
	Status           RunStatus           `json:"status,omitempty"`
	WakeAt           *time.Time          `json:"wake_at,omitempty"`
	PendingResults   []TaskResult        `json:"pending_results,omitempty"`
	ActivateTasks    []TaskActivation    `json:"activate_tasks,omitempty"`
	AdoptTaskIDs     []string            `json:"adopt_task_ids,omitempty"`
	ConsumeInputIDs  []string            `json:"consume_input_ids,omitempty"`
	Operations       []Operation         `json:"operations,omitempty"`
	Artifacts        []ArtifactReference `json:"artifacts,omitempty"`
	Events           []StoredEvent       `json:"events,omitempty"`
	// WaitingTargets 非 nil 时与快照一起替换当前等待目标，避免旧目标反复唤醒。
	WaitingTargets *[]string `json:"waiting_targets,omitempty"`
}

// SealCommit 计算包含执行条件和全部事实的摘要，并通过序列化隔离可变引用。
func SealCommit(request CommitRequest) (CommitRequest, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return CommitRequest{}, fmt.Errorf("runtime: encode commit: %w", err)
	}
	var frozen CommitRequest
	if err := json.Unmarshal(data, &frozen); err != nil {
		return CommitRequest{}, err
	}
	sum := sha256.Sum256(data)
	frozen.Digest = hex.EncodeToString(sum[:])
	return frozen, nil
}

// ValidateCommit 检查请求未在形成摘要后被修改，不进行执行权检查。
func ValidateCommit(request CommitRequest) error {
	if request.Scope == "" || request.RunID == "" || request.CommitID == "" || request.Digest == "" {
		return fmt.Errorf("runtime: commit requires scope, run ID, commit ID and digest")
	}
	frozen, err := SealCommit(request)
	if err != nil {
		return err
	}
	if frozen.Digest != request.Digest {
		return ErrCommitConflict
	}
	return nil
}

type CommitReceipt struct {
	Scope         string    `json:"scope"`
	RunID         string    `json:"run_id"`
	CommitID      string    `json:"commit_id"`
	Digest        string    `json:"digest"`
	Revision      uint64    `json:"revision"`
	FirstSequence int64     `json:"first_sequence,omitempty"`
	LastSequence  int64     `json:"last_sequence,omitempty"`
	CommittedAt   time.Time `json:"committed_at"`
}

// Committer 的实现先查原提交，再校验新写执行条件；历史查回不能被后来的取消或失租拒绝。
// Commit 返回 ErrCommitUnknown 时，只能查询或重试原请求，不能重跑外部动作。
type Committer interface {
	Commit(context.Context, CommitRequest) (CommitReceipt, error)
	LookupCommit(context.Context, string, string) (CommitReceipt, bool, error)
}

// ApplyCommit 在已锁定的权威记录副本上应用决议。调用者负责事务及先查提交回执。
// 它不调用 Provider/工具；结果保存与状态采用是不同操作。
func ApplyCommit(current RunRecord, request CommitRequest, now time.Time) (RunRecord, CommitReceipt, error) {
	if err := ValidateCommit(request); err != nil {
		return RunRecord{}, CommitReceipt{}, err
	}
	if current.Scope != request.Scope || current.RunID != request.RunID {
		return RunRecord{}, CommitReceipt{}, ErrRunNotFound
	}
	if current.Revision != request.ExpectedRevision {
		return RunRecord{}, CommitReceipt{}, ErrRevisionConflict
	}
	if current.Status.Terminal() {
		return RunRecord{}, CommitReceipt{}, ErrRunTerminal
	}
	if current.Authority != request.Authority || request.Authority.Owner == "" || request.Authority.AttemptID == "" || !current.LeaseUntil.After(now) {
		return RunRecord{}, CommitReceipt{}, ErrExecutionLost
	}
	data, err := json.Marshal(current)
	if err != nil {
		return RunRecord{}, CommitReceipt{}, err
	}
	var next RunRecord
	if err := json.Unmarshal(data, &next); err != nil {
		return RunRecord{}, CommitReceipt{}, err
	}
	if next.Tasks == nil {
		next.Tasks = make(map[string]TaskResult)
	}
	if next.TaskActivations == nil {
		next.TaskActivations = make(map[string]TaskActivation)
	}
	if next.Operations == nil {
		next.Operations = make(map[string]Operation)
	}
	if next.Artifacts == nil {
		next.Artifacts = make(map[string]ArtifactReference)
	}
	revision := current.Revision + 1
	for _, activation := range request.ActivateTasks {
		if activation.TaskID == "" || activation.Activation == "" || activation.AttemptID == "" || activation.InputRevision > current.Revision {
			return RunRecord{}, CommitReceipt{}, fmt.Errorf("runtime: invalid task activation")
		}
		if previous, exists := next.TaskActivations[activation.TaskID]; exists {
			if previous.Activation != activation.Activation || previous.InputRevision != activation.InputRevision {
				return RunRecord{}, CommitReceipt{}, ErrCommitConflict
			}
			if _, completed := next.Tasks[activation.TaskID]; completed && previous.AttemptID != activation.AttemptID {
				return RunRecord{}, CommitReceipt{}, fmt.Errorf("runtime: completed task cannot be reactivated")
			}
		}
		next.TaskActivations[activation.TaskID] = activation
	}
	for _, result := range request.PendingResults {
		if result.TaskID == "" || result.AttemptID == "" || result.Activation == "" || result.Status == "" || result.AdoptedRevision != 0 {
			return RunRecord{}, CommitReceipt{}, fmt.Errorf("runtime: invalid pending task result")
		}
		activation, exists := next.TaskActivations[result.TaskID]
		if !exists || activation.Activation != result.Activation || activation.AttemptID != result.AttemptID || activation.InputRevision != result.InputRevision {
			return RunRecord{}, CommitReceipt{}, fmt.Errorf("runtime: task result does not match its activation")
		}
		if previous, exists := next.Tasks[result.TaskID]; exists {
			if !sameJSON(previous.withoutAdoption(), result.withoutAdoption()) {
				return RunRecord{}, CommitReceipt{}, ErrCommitConflict
			}
			continue
		}
		next.Tasks[result.TaskID] = result
	}
	for _, id := range request.AdoptTaskIDs {
		result, exists := next.Tasks[id]
		if !exists || result.Status != TaskSucceeded {
			return RunRecord{}, CommitReceipt{}, fmt.Errorf("runtime: task result is not adoptable: %s", id)
		}
		if result.AdoptedRevision != 0 {
			return RunRecord{}, CommitReceipt{}, fmt.Errorf("runtime: task result was already adopted: %s", id)
		}
		result.AdoptedRevision = revision
		next.Tasks[id] = result
	}
	for _, id := range request.ConsumeInputIDs {
		input, exists := next.Inputs[id]
		if !exists || input.ConsumedRevision != 0 {
			return RunRecord{}, CommitReceipt{}, fmt.Errorf("runtime: input is missing or already consumed: %s", id)
		}
		input.ConsumedRevision = revision
		next.Inputs[id] = input
	}
	for _, operation := range request.Operations {
		if err := operation.Validate(); err != nil {
			return RunRecord{}, CommitReceipt{}, err
		}
		if old, exists := next.Operations[operation.ID]; exists {
			if old.InputDigest != operation.InputDigest || old.Provider != operation.Provider || old.IdempotencyKey != operation.IdempotencyKey {
				return RunRecord{}, CommitReceipt{}, ErrCommitConflict
			}
			if old.Status == OperationSucceeded && operation.Status != OperationSucceeded {
				return RunRecord{}, CommitReceipt{}, fmt.Errorf("runtime: confirmed operation success cannot regress")
			}
			if old.Status == OperationSucceeded && !sameJSON(old, operation) {
				return RunRecord{}, CommitReceipt{}, ErrCommitConflict
			}
		}
		next.Operations[operation.ID] = operation
	}
	for _, artifact := range request.Artifacts {
		if err := artifact.Validate(); err != nil {
			return RunRecord{}, CommitReceipt{}, err
		}
		if old, exists := next.Artifacts[artifact.ID]; exists {
			if old.Digest != artifact.Digest || old.ObjectKey != artifact.ObjectKey || old.Version != artifact.Version || old.OperationID != artifact.OperationID || old.TaskID != artifact.TaskID {
				return RunRecord{}, CommitReceipt{}, ErrCommitConflict
			}
			if artifactStatusOrder(artifact.Status) < artifactStatusOrder(old.Status) {
				return RunRecord{}, CommitReceipt{}, fmt.Errorf("runtime: artifact registration cannot regress")
			}
		}
		next.Artifacts[artifact.ID] = artifact
	}
	if request.Snapshot != nil {
		next.Snapshot = append([]byte{}, request.Snapshot...)
	}
	if request.Status != "" {
		switch request.Status {
		case RunQueued, RunRunning, RunWaiting, RunSucceeded, RunFailed, RunCancelled:
		default:
			return RunRecord{}, CommitReceipt{}, fmt.Errorf("runtime: invalid run status")
		}
		next.Status = request.Status
	}
	if request.WaitingTargets != nil {
		targets := append([]string{}, (*request.WaitingTargets)...)
		next.WaitingTargets = &targets
	}
	// 只有仍属于当前等待集的输入需要调度；过期目标保留原事实，不盲消费或反复唤醒。
	// 在同一权威行锁中的最新 Inputs 与等待集上判断，覆盖 Signal 与 finish 的交错窗口。
	if next.Status == RunWaiting {
		for _, input := range next.Inputs {
			if input.Target != "" && input.ConsumedRevision == 0 && next.IsWaitingTarget(input.Target) {
				next.Status = RunQueued
				break
			}
		}
	}
	if request.WakeAt != nil {
		next.WakeAt = *request.WakeAt
	}
	receipt := CommitReceipt{Scope: request.Scope, RunID: request.RunID, CommitID: request.CommitID, Digest: request.Digest, Revision: revision, CommittedAt: now}
	for _, event := range request.Events {
		if event.Type == "" || event.ID != "" || event.Sequence != 0 {
			return RunRecord{}, CommitReceipt{}, fmt.Errorf("runtime: event identity is allocated by the commit store")
		}
		next.NextSequence++
		event.Scope, event.RunID, event.CommitID = next.Scope, next.RunID, request.CommitID
		event.Sequence, event.Revision, event.Timestamp = next.NextSequence, revision, now
		event.ID = fmt.Sprintf("%s/%d", next.RunID, event.Sequence)
		if event.Version == "" {
			event.Version = EventVersionV1
		}
		next.Events = append(next.Events, event)
		if receipt.FirstSequence == 0 {
			receipt.FirstSequence = event.Sequence
		}
		receipt.LastSequence = event.Sequence
	}
	next.Revision = revision
	return next, receipt, nil
}

// IsWaitingTarget 只查询已声明的目标，不根据任意输入猜测新的等待项。
func (r RunRecord) IsWaitingTarget(target string) bool {
	if r.WaitingTargets == nil {
		return false
	}
	for _, id := range *r.WaitingTargets {
		if id == target {
			return true
		}
	}
	return false
}

func sameJSON(a, b any) bool {
	x, e1 := json.Marshal(a)
	y, e2 := json.Marshal(b)
	return e1 == nil && e2 == nil && string(x) == string(y)
}

func artifactStatusOrder(status ArtifactRegistrationStatus) int {
	switch status {
	case ArtifactBytesStored:
		return 1
	case ArtifactRegistered:
		return 2
	case ArtifactDelivered:
		return 3
	}
	return 0
}
