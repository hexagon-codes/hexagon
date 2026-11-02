package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	hr "github.com/hexagon-codes/hexagon/runtime"
)

const WorkflowDefinitionKind = "workflow_definition"
const workflowSnapshotVersion = "workflow.execution.v1"
const workflowSchedulerVersion = "workflow.scheduler.v1"

type workflowRunSpec struct {
	WorkflowID      string           `json:"workflow_id"`
	WorkflowVersion string           `json:"workflow_version"`
	Steps           []StepDefinition `json:"steps"`
	Timeout         time.Duration    `json:"timeout"`
	RetryPolicy     *RetryPolicy     `json:"retry_policy,omitempty"`
	Input           WorkflowInput    `json:"input"`
}

type durableWorkflowSnapshot struct {
	Version   string             `json:"version"`
	Execution *WorkflowExecution `json:"execution"`
}

// FreezeRunSpec 固定可读输入、步骤定义和宿主实现引用；不序列化步骤闭包。
func (wf *Workflow) FreezeRunSpec(runID string, input WorkflowInput) (hr.RunSpec, error) {
	if wf == nil || wf.ID == "" || wf.DefinitionReference == nil || wf.DefinitionReference.Kind != WorkflowDefinitionKind {
		return hr.RunSpec{}, fmt.Errorf("%w: immutable workflow definition reference is required", hr.ErrUnrecoverableSpec)
	}
	reference := *wf.DefinitionReference
	if len(reference.Content) > 0 {
		digest, err := hr.ContentDigest(reference.Content)
		if err != nil {
			return hr.RunSpec{}, err
		}
		if reference.Digest != "" && reference.Digest != digest {
			return hr.RunSpec{}, hr.ErrUnrecoverableSpec
		}
		reference.Digest = digest
	}
	request, err := json.Marshal(workflowRunSpec{WorkflowID: wf.ID, WorkflowVersion: wf.Version, Steps: wf.StepDefs, Timeout: wf.Timeout, RetryPolicy: wf.RetryPolicy, Input: input})
	if err != nil {
		return hr.RunSpec{}, err
	}
	strategy := json.RawMessage(`{"name":"workflow","version":"workflow.scheduler.v1"}`)
	digest, err := hr.ContentDigest(strategy)
	if err != nil {
		return hr.RunSpec{}, err
	}
	spec := hr.RunSpec{Version: hr.RunSpecVersion, RunID: runID, Request: request, Strategy: "workflow", StrategyReference: hr.ContentReference{Kind: "strategy", ID: "workflow", Revision: workflowSchedulerVersion, Digest: digest, Content: strategy}, Codec: hr.StateCodec{Type: "workflow.Execution", Version: workflowSnapshotVersion}, Resources: []hr.ContentReference{reference}}
	return spec, spec.Validate()
}

type WorkflowRunReader interface {
	GetRun(context.Context, string, string) (hr.RunRecord, bool, error)
}

var ErrWorkflowPaused = errors.New("workflow execution is paused")

// DurableWorkflowRunner 由同一 Worker 调用，状态只提交到统一 Run 权威。
// 每次 Run 创建自己的轻量执行器，不共享可变执行权，不启动另一套持久 worker。
type DurableWorkflowRunner struct {
	reader    WorkflowRunReader
	committer hr.Committer
	options   []ExecutorOption
}

func NewDurableWorkflowRunner(reader WorkflowRunReader, committer hr.Committer, options ...ExecutorOption) *DurableWorkflowRunner {
	return &DurableWorkflowRunner{reader: reader, committer: committer, options: options}
}

// WorkflowCommitUnknownError 保留原不可变请求，宿主只能查回或重试该提交。
type WorkflowCommitUnknownError struct {
	Request hr.CommitRequest
	Cause   error
}

func (e *WorkflowCommitUnknownError) Error() string {
	return fmt.Sprintf("workflow commit %s outcome is unknown", e.Request.CommitID)
}
func (e *WorkflowCommitUnknownError) Unwrap() error { return errors.Join(hr.ErrCommitUnknown, e.Cause) }

// Run 继续原执行。consumedInputIDs 只列出宿主已实际应用的输入，未指定的 Signal 保留未消费。
func (r *DurableWorkflowRunner) Run(ctx context.Context, scope, runID string, wf *Workflow, consumedInputIDs ...string) (*WorkflowExecution, error) {
	return r.run(ctx, scope, runID, wf, false, consumedInputIDs...)
}

// Resume 是宿主针对原 Run 的明确继续决议，消费身份仍与实际采用输入对应。
func (r *DurableWorkflowRunner) Resume(ctx context.Context, scope, runID string, wf *Workflow, consumedInputIDs ...string) (*WorkflowExecution, error) {
	return r.run(ctx, scope, runID, wf, true, consumedInputIDs...)
}

func (r *DurableWorkflowRunner) run(ctx context.Context, scope, runID string, wf *Workflow, resume bool, consumedInputIDs ...string) (*WorkflowExecution, error) {
	if r.reader == nil || r.committer == nil || wf == nil {
		return nil, fmt.Errorf("workflow durable dependencies are incomplete")
	}
	authority, ok := hr.ExecutionAuthorityFromContext(ctx)
	if !ok || authority.Scope != scope {
		return nil, hr.ErrExecutionLost
	}
	record, found, err := r.reader.GetRun(ctx, scope, runID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, hr.ErrRunNotFound
	}
	if err := record.Spec.Validate(); err != nil {
		return nil, err
	}
	if record.Spec.RunID != runID || record.Spec.Strategy != "workflow" || record.Spec.StrategyReference.Revision != workflowSchedulerVersion || record.Spec.Codec != (hr.StateCodec{Type: "workflow.Execution", Version: workflowSnapshotVersion}) {
		return nil, hr.ErrUnrecoverableSpec
	}
	var frozen workflowRunSpec
	if err := decodeWorkflowJSON(record.Spec.Request, &frozen); err != nil {
		return nil, fmt.Errorf("%w: %w", hr.ErrUnrecoverableSpec, err)
	}
	current, err := wf.FreezeRunSpec(runID, frozen.Input)
	if err != nil {
		return nil, err
	}
	if current.StrategyReference.Digest != record.Spec.StrategyReference.Digest || current.StrategyReference.ObjectKey != record.Spec.StrategyReference.ObjectKey {
		return nil, hr.ErrUnrecoverableSpec
	}
	if string(current.Request) != string(record.Spec.Request) || len(current.Resources) != len(record.Spec.Resources) {
		return nil, hr.ErrUnrecoverableSpec
	}
	for index := range current.Resources {
		a, b := current.Resources[index], record.Spec.Resources[index]
		if a.Kind != b.Kind || a.ID != b.ID || a.Revision != b.Revision || a.Digest != b.Digest || a.ObjectKey != b.ObjectKey {
			return nil, hr.ErrUnrecoverableSpec
		}
	}
	var execution *WorkflowExecution
	if len(record.Snapshot) > 0 {
		var snapshot durableWorkflowSnapshot
		if err := decodeWorkflowJSON(record.Snapshot, &snapshot); err != nil {
			return nil, fmt.Errorf("%w: %w", hr.ErrUnrecoverableSpec, err)
		}
		if snapshot.Version != workflowSnapshotVersion || snapshot.Execution == nil || snapshot.Execution.ID != runID || snapshot.Execution.WorkflowID != wf.ID {
			return nil, hr.ErrUnrecoverableSpec
		}
		execution = snapshot.Execution
	} else {
		execution = NewExecution(wf)
		execution.ID = runID
		execution.Status = StatusRunning
		execution.Input, err = json.Marshal(frozen.Input)
		if err != nil {
			return nil, err
		}
		execution.InputFormat = workflowInputFormat
		execution.Context.Variables = frozen.Input.Variables
		execution.Context.Metadata = frozen.Input.Metadata
	}
	if record.Status.Terminal() {
		if record.Status == hr.RunCancelled {
			execution.Status = StatusCancelled
			execution.Error = record.CancelReason
			return execution, nil
		}
		if (record.Status == hr.RunSucceeded && execution.Status == StatusCompleted) || (record.Status == hr.RunFailed && execution.Status == StatusFailed) {
			return execution, nil
		}
		return execution, hr.ErrUnrecoverableSpec
	}
	if record.Authority != authority.Authority {
		return execution, hr.ErrExecutionLost
	}
	if execution.Status == StatusPaused {
		if !resume {
			return execution, ErrWorkflowPaused
		}
		execution.Status, execution.PausedAt = StatusRunning, nil
	}
	saver := &workflowRunSaver{record: record, committer: r.committer, consumedInputIDs: consumedInputIDs}
	options := append([]ExecutorOption(nil), r.options...)
	options = append(options, WithExecutionPersistence(saver))
	executor := NewExecutor(options...)
	id, err := executor.RecoverExecution(ctx, wf, execution)
	if err != nil {
		return execution, err
	}
	return executor.WaitForCompletion(ctx, id, 0)
}

type workflowRunSaver struct {
	record           hr.RunRecord
	committer        hr.Committer
	consumedInputIDs []string
}

func (s *workflowRunSaver) SaveExecution(ctx context.Context, execution *WorkflowExecution) error {
	data, err := json.Marshal(durableWorkflowSnapshot{workflowSnapshotVersion, execution})
	if err != nil {
		return err
	}
	status := hr.RunRunning
	switch execution.Status {
	case StatusPaused:
		status = hr.RunWaiting
	case StatusCompleted:
		status = hr.RunSucceeded
	case StatusFailed:
		status = hr.RunFailed
	case StatusCancelled:
		status = hr.RunCancelled
	}
	sum := sha256.Sum256(data)
	payload, err := json.Marshal(map[string]string{"status": string(execution.Status), "step_id": execution.Context.CurrentStepID})
	if err != nil {
		return err
	}
	request, err := hr.SealCommit(hr.CommitRequest{Scope: s.record.Scope, RunID: s.record.RunID, CommitID: fmt.Sprintf("%s/workflow/%d/%s", s.record.RunID, s.record.Revision, hex.EncodeToString(sum[:12])), ExpectedRevision: s.record.Revision, Authority: s.record.Authority, Snapshot: data, Status: status, ConsumeInputIDs: append([]string(nil), s.consumedInputIDs...), Events: []hr.StoredEvent{{Type: hr.EventType("workflow_checkpoint"), Payload: payload}}})
	if err != nil {
		return err
	}
	receipt, err := s.committer.Commit(ctx, request)
	if err != nil {
		if errors.Is(err, hr.ErrCommitUnknown) {
			return &WorkflowCommitUnknownError{Request: request, Cause: err}
		}
		return err
	}
	s.record.Revision = receipt.Revision
	s.consumedInputIDs = nil
	if execution.Status == StatusPaused {
		return ErrWorkflowPaused
	}
	return nil
}
