package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/internal/util"
	"github.com/hexagon-codes/hexagon/interrupt"
)

// RunStore 是持久宿主的同一恢复权威，不并列写入轻量 Checkpointer。
type RunStore interface {
	TaskStore
	Committer
}

// DurableRunner 由宿主显式提交、由 Worker 显式执行；不隐式启动 goroutine 或迁移。
// Runner 的引用/PrepareRun 负责装配原资源版本；连接、worker 和作用域均归宿主。
type DurableRunner struct {
	Runner          *DefaultRunner
	Store           RunStore
	ResolveStrategy func(ContentReference) (Strategy, error)
	Codecs          *SnapshotCodecs
}

// Submit 在任何模型/工具派发前冻结并持久接受。重复 InputID 交由同一 Store 去重。
func (d *DurableRunner) Submit(ctx context.Context, scope, inputID string, request Request) (record RunRecord, submitErr error) {
	if d.Runner == nil || d.Store == nil {
		return RunRecord{}, fmt.Errorf("runtime: durable runner dependencies are missing")
	}
	if request.ID == "" {
		request.ID = requestID(request)
	}
	if request.ID == "" {
		return RunRecord{}, fmt.Errorf("runtime: durable submit requires an explicit run identity")
	}
	ingressDigest, err := originalIngressDigest(request)
	if err != nil {
		return RunRecord{}, err
	}
	// 已接受的入站先查原记录；当前路由或资源版本变化不改变原接收事实。
	legacyAcceptance := false
	if previous, found, err := d.Store.GetRun(ctx, scope, request.ID); err != nil {
		return RunRecord{}, err
	} else if found {
		accepted, ok := previous.Inputs[inputID]
		expected, digestErr := SubmissionDigest(SubmitRequest{Scope: scope, InputID: inputID, RunID: request.ID, Spec: previous.Spec, Input: previous.Spec.Request})
		if digestErr != nil {
			return RunRecord{}, digestErr
		}
		if !ok || accepted.Target != "" || accepted.Digest != expected {
			return RunRecord{}, ErrCommitConflict
		}
		if previous.Spec.IngressDigest != "" {
			if previous.Spec.IngressDigest != ingressDigest {
				return RunRecord{}, ErrCommitConflict
			}
			return previous, nil
		}
		legacyAcceptance = true
	}
	active, prepared, references, release, err := d.Runner.prepareExecution(ctx, request)
	if release != nil {
		defer func() {
			submitErr = errors.Join(submitErr, invokeExtension("release_submit", func() error { release(); return nil }))
		}()
	}
	if err != nil {
		return RunRecord{}, err
	}
	if active.selector == nil {
		return RunRecord{}, ErrNoProvider
	}
	var selected ProviderSelection
	if err := invokeExtension("provider.select", func() error { var err error; selected, err = active.selector.Select(ctx, prepared); return err }); err != nil {
		return RunRecord{}, err
	}
	if selected.Provider == nil {
		return RunRecord{}, ErrNoProvider
	}
	references = append(append([]ContentReference(nil), d.Runner.references...), references...)
	spec, err := FreezeRunSpec(prepared, references)
	if err != nil {
		return RunRecord{}, err
	}
	spec.SelectedProvider = &ProviderRoute{Name: selected.Name, Model: selected.Model}
	if !legacyAcceptance {
		spec.IngressDigest = ingressDigest
	}
	return d.Store.Submit(ctx, SubmitRequest{Scope: scope, InputID: inputID, RunID: request.ID, Spec: spec, Input: spec.Request})
}

// originalIngressDigest 排除 Go 资源句柄，只保留调用方输入及策略名称。
func originalIngressDigest(request Request) (string, error) {
	request, err := CloneRequest(request)
	if err != nil {
		return "", err
	}
	strategy := strategyOrDefault(request.Strategy)
	name := ""
	if err := invokeExtension("strategy.name", func() error { name = strategy.Name(); return nil }); err != nil {
		return "", err
	}
	request.Strategy = nil
	data, err := json.Marshal(struct {
		Input    frozenRequest `json:"input"`
		Strategy string        `json:"strategy"`
	}{frozenRequest{Request: request, Continuations: snapshotContinuations(request.Messages)}, name})
	if err != nil {
		return "", err
	}
	return ContentDigest(data)
}

// Execute 只消费 Worker 已取得的执行权；未找到/坏快照不会退回首次执行。
func (d *DurableRunner) Execute(ctx context.Context, record RunRecord, sink EventSink) (*Result, error) {
	return d.ExecuteWithInputs(ctx, record, nil, sink)
}

func (d *DurableRunner) ExecuteWithInputs(ctx context.Context, record RunRecord, inputs []ResumeInput, sink EventSink) (*Result, error) {
	if d.Runner == nil || d.Store == nil {
		return nil, fmt.Errorf("runtime: durable runner dependencies are missing")
	}
	execution, ok := ExecutionAuthorityFromContext(ctx)
	if !ok || execution.Scope != record.Scope || execution.Authority != record.Authority || (execution.RunID != "" && execution.RunID != record.RunID) {
		return nil, ErrExecutionLost
	}
	current, found, err := d.Store.GetRun(ctx, record.Scope, record.RunID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrRunNotFound
	}
	if current.Authority != execution.Authority {
		return nil, ErrExecutionLost
	}
	var restoredSnapshot *Snapshot
	if len(current.Snapshot) > 0 {
		var decoded Snapshot
		if d.Codecs == nil {
			decoded, err = DecodeSnapshot(current.Snapshot)
		} else {
			decoded, err = d.Codecs.Decode(current.Snapshot, current.Spec.Codec, current.RunID)
		}
		if err != nil {
			return nil, err
		}
		if decoded.RunID != current.RunID || decoded.Spec == nil || !sameJSON(*decoded.Spec, current.Spec) {
			return nil, ErrUnrecoverableSpec
		}
		restoredSnapshot = &decoded
	}
	var request Request
	if restoredSnapshot != nil {
		request, err = current.Spec.restoreRequestData(d.ResolveStrategy)
	} else {
		request, err = current.Spec.RestoreRequest(d.ResolveStrategy)
	}
	if err != nil {
		return nil, err
	}
	var snapshotCache []byte
	if restoredSnapshot != nil {
		// 后续预算等读取复用已核验的迁移结果，不再把原旧版本字节交给默认解码器。
		snapshotCache, err = EncodeSnapshot(*restoredSnapshot)
		if err != nil {
			return nil, err
		}
	}
	session := &runSession{store: d.Store, record: current, authority: execution.Authority, sink: sink, pendingAdopt: make(map[string]bool), modelOrdinals: make(map[string]int), interruptCodecs: d.Runner.interruptCodecs, snapshotSource: append([]byte(nil), current.Snapshot...), snapshotCache: snapshotCache}
	active := *d.Runner
	active.durable, active.session = session, session
	ctx = ContextWithTaskExecutor(context.WithValue(ctx, frozenRunSpecKey{}, current.Spec), session)
	var seed *State
	var pending []PendingTool
	start := 0
	if len(current.Snapshot) > 0 {
		snapshot := *restoredSnapshot
		if snapshot.Final || snapshot.StopReason != "" {
			return snapshotResult(snapshot), nil
		}
		if current.Status.Terminal() {
			return canceledStateResult(snapshot.RestoreState()), ErrRunTerminal
		}
		seed, start, pending = snapshot.RestoreState(), snapshotNextTurn(snapshot), snapshot.Pending
		if snapshot.Waiting != nil {
			signal, decodeErr := interrupt.DecodeSignal(snapshot.Waiting, d.Runner.interruptCodecs)
			if decodeErr != nil {
				return nil, decodeErr
			}
			if len(inputs) == 0 {
				return canceledStateResult(snapshot.RestoreState()), signal
			}
			targets := make(map[string]any)
			for _, chosen := range inputs {
				accepted, ok := current.Inputs[chosen.InputID]
				if !ok || accepted.ConsumedRevision != 0 || accepted.Target == "" {
					return nil, fmt.Errorf("runtime: resume input was not accepted or is already consumed")
				}
				data, encodeErr := json.Marshal(chosen.Data)
				if encodeErr != nil || !sameJSON(accepted.Payload, json.RawMessage(data)) {
					return nil, fmt.Errorf("runtime: resume data differs from accepted input")
				}
				if _, duplicate := targets[accepted.Target]; duplicate {
					return nil, fmt.Errorf("runtime: duplicate resume target")
				}
				targets[accepted.Target] = chosen.Data
				if session.resumeInputs == nil {
					session.resumeInputs = make(map[string]string)
				}
				session.resumeInputs[accepted.Target] = chosen.InputID
			}
			ctx, err = prepareResumeContext(ctx, signal, targets)
			if err != nil {
				return nil, err
			}
			seed.waitingSignal = signal
		} else if len(inputs) > 0 {
			return nil, fmt.Errorf("runtime: run has no waiting targets")
		}
	} else if current.Status.Terminal() {
		return nil, fmt.Errorf("runtime: terminal run has no readable snapshot")
	}
	result, err := active.execute(ctx, request, session, seed, start, pending)
	if err != nil {
		// 原提交未知时不能再用一份新决议覆盖它，调用方需先核对原 Commit。
		var unknown *RunCommitUnknownError
		var persistence *RunPersistenceError
		var delivery *EventDeliveryError
		var unknownTask *TaskOutcomeUnknownError
		if errors.As(err, &unknownTask) {
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			return result, errors.Join(err, session.finish(finishCtx, RunWaiting))
		}
		if errors.As(err, &unknown) || errors.As(err, &persistence) || errors.As(err, &delivery) || errors.Is(err, ErrCommitUnknown) || errors.Is(err, ErrExecutionLost) {
			return result, err
		}
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		status := RunFailed
		_, interrupted := interrupt.IsInterruptSignal(err)
		if errors.Is(err, ErrTurnStopped) || interrupted || errors.Is(err, interrupt.ErrInterrupted) {
			status = RunWaiting
		}
		if needsToolReconciliation(err) {
			status = RunWaiting
		}
		if errors.Is(err, context.Canceled) {
			status = RunCancelled
		}
		if finishErr := session.finish(finishCtx, status); finishErr != nil {
			err = errors.Join(err, finishErr)
		}
	}
	return result, err
}

// RunCommitUnknownError 保存已经冻结的原请求；核对时必须复用其身份和摘要。
type RunCommitUnknownError struct {
	Request CommitRequest
	Err     error
}

func (e *RunCommitUnknownError) Error() string {
	return fmt.Sprintf("runtime: commit %s outcome is unknown: %v", e.Request.CommitID, e.Err)
}
func (e *RunCommitUnknownError) Unwrap() error      { return e.Err }
func (*RunCommitUnknownError) UnsafeToReplay() bool { return true }

type RunPersistenceError struct {
	Phase string
	Err   error
}

func (e *RunPersistenceError) Error() string {
	return fmt.Sprintf("runtime: persist %s: %v", e.Phase, e.Err)
}
func (e *RunPersistenceError) Unwrap() error      { return e.Err }
func (*RunPersistenceError) UnsafeToReplay() bool { return true }

type EventDeliveryError struct{ Err error }

func (e *EventDeliveryError) Error() string      { return fmt.Sprintf("runtime: event delivery: %v", e.Err) }
func (e *EventDeliveryError) Unwrap() error      { return e.Err }
func (*EventDeliveryError) UnsafeToReplay() bool { return true }

type runSession struct {
	mu              sync.Mutex
	store           RunStore
	record          RunRecord
	authority       ExecutionAuthority
	sink            EventSink
	pendingAdopt    map[string]bool
	modelOrdinals   map[string]int
	interruptCodecs *interrupt.StateCodecs
	consumeInputs   []string
	resumeInputs    map[string]string
	// 缓存只匹配同一份持久字节；新快照提交后不能复用旧迁移结果。
	snapshotSource []byte
	snapshotCache  []byte
}

// commitLocked 不包含模型/工具/事件消费者回调；仅调用同一 Store 的提交及查回。
func (s *runSession) commitLocked(ctx context.Context, request CommitRequest) (CommitReceipt, error) {
	request.Scope, request.RunID, request.Authority, request.ExpectedRevision = s.record.Scope, s.record.RunID, s.authority, s.record.Revision
	request.CommitID = util.GenerateID("commit")
	frozen, err := SealCommit(request)
	if err != nil {
		return CommitReceipt{}, &RunPersistenceError{Phase: "commit", Err: err}
	}
	receipt, err := s.store.Commit(ctx, frozen)
	if err != nil && errors.Is(err, ErrCommitUnknown) {
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		original, found, lookupErr := s.store.LookupCommit(lookupCtx, frozen.Scope, frozen.CommitID)
		if lookupErr != nil || !found {
			return CommitReceipt{}, &RunCommitUnknownError{Request: frozen, Err: errors.Join(err, lookupErr)}
		}
		if original.Digest != frozen.Digest || original.RunID != frozen.RunID {
			return CommitReceipt{}, ErrCommitConflict
		}
		receipt, err = original, nil
	}
	if err != nil {
		return CommitReceipt{}, &RunPersistenceError{Phase: "commit", Err: err}
	}
	// 只按已确认原回执推进本地 revision；派生投影由同一事务纯函数重建。
	next, found, readErr := s.store.GetRun(ctx, frozen.Scope, frozen.RunID)
	if readErr != nil {
		return receipt, &RunPersistenceError{Phase: "read committed run", Err: readErr}
	}
	if !found {
		return receipt, ErrRunNotFound
	}
	if next.Revision < receipt.Revision {
		return receipt, ErrRevisionConflict
	}
	s.record = next
	return receipt, nil
}

func (s *runSession) Save(ctx context.Context, snapshot Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	spec := s.record.Spec
	snapshot.Spec, snapshot.Codec = &spec, StateCodec{Type: "runtime.Snapshot", Version: SnapshotCodecVersion}
	data, err := EncodeSnapshot(snapshot)
	if err != nil {
		return err
	}
	var adopt, consume []string
	consume = append(consume, s.consumeInputs...)
	for id := range s.pendingAdopt {
		if result, ok := s.record.Tasks[id]; ok && result.Status == TaskSucceeded && result.AdoptedRevision == 0 {
			adopt = append(adopt, id)
		}
	}
	if len(s.record.Snapshot) == 0 {
		for id, input := range s.record.Inputs {
			if input.Target == "" && input.ConsumedRevision == 0 && sameJSON(input.Payload, s.record.Spec.Request) {
				consume = append(consume, id)
			}
		}
	}
	sort.Strings(adopt)
	sort.Strings(consume)
	status := RunRunning
	if snapshot.Waiting != nil {
		status = RunWaiting
		// 已选但尚未处理的入站决定仍在队列中；不能随另一目标的等待快照一起休眠。
		signal, decodeErr := interrupt.DecodeSignal(snapshot.Waiting, s.interruptCodecs)
		if decodeErr != nil {
			return decodeErr
		}
		addresses, _ := interrupt.SignalToPersistenceMaps(signal)
		for target, id := range s.resumeInputs {
			if _, waiting := addresses[target]; !waiting {
				continue
			}
			consuming := false
			for _, consumedID := range consume {
				if consumedID == id {
					consuming = true
					break
				}
			}
			if !consuming && s.record.Inputs[id].ConsumedRevision == 0 {
				status = RunQueued
				break
			}
		}
	}
	if snapshot.Final || snapshot.StopReason != "" {
		status = RunSucceeded
	}
	events := []StoredEvent{{Type: "state_committed"}}
	if snapshot.Final || snapshot.StopReason != "" {
		payload, encodeErr := json.Marshal(snapshotResult(snapshot))
		if encodeErr != nil {
			return encodeErr
		}
		events = append(events, StoredEvent{Type: EventRunFinished, Payload: payload})
	}
	targetSet := make(map[string]bool)
	var visit func(*interrupt.PersistedSignal)
	visit = func(signal *interrupt.PersistedSignal) {
		if signal == nil {
			return
		}
		targetSet[signal.ID] = true
		for _, sub := range signal.Subs {
			visit(sub)
		}
	}
	visit(snapshot.Waiting)
	targets := make([]string, 0, len(targetSet))
	for id := range targetSet {
		targets = append(targets, id)
	}
	sort.Strings(targets)
	_, err = s.commitLocked(ctx, CommitRequest{Snapshot: data, Status: status, AdoptTaskIDs: adopt, ConsumeInputIDs: consume, Events: events, WaitingTargets: &targets})
	if err == nil {
		clear(s.pendingAdopt)
		s.consumeInputs = nil
	}
	return err
}

func (s *runSession) Load(_ context.Context, runID string) (Snapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if runID != s.record.RunID {
		return Snapshot{}, false, ErrRunNotFound
	}
	if len(s.record.Snapshot) == 0 {
		return Snapshot{}, false, nil
	}
	data := s.record.Snapshot
	if s.snapshotCache != nil && bytes.Equal(data, s.snapshotSource) {
		data = s.snapshotCache
	}
	// 每次解码产生独立值，调用者修改返回快照不会污染迁移缓存。
	snapshot, err := DecodeSnapshot(data)
	return snapshot, err == nil, err
}

func (s *runSession) Emit(ctx context.Context, event Event) error {
	s.mu.Lock()
	terminal := s.record.Status.Terminal()
	lastSequence := s.record.NextSequence
	storageRunID := s.record.RunID
	s.mu.Unlock()
	if event.RunID != "" && event.RunID != storageRunID {
		event.Metadata = cloneRuntimeMetadata(event.Metadata)
		if event.Metadata == nil {
			event.Metadata = make(map[string]any)
		}
		event.Metadata["source_run_id"] = event.RunID
	}
	event.RunID = storageRunID
	if event.Type == EventRunFinished && terminal {
		event.Sequence = lastSequence
		event.ID = fmt.Sprintf("%s/%d", event.RunID, lastSequence)
		return emit(ctx, s.sink, event)
	}
	if event.Type != EventLLMChunk && event.Type != EventHeartbeat {
		// 公开事件不包含协议私有 continuation，也不序列化任意 State/资源句柄。
		payload, err := json.Marshal(struct {
			AttemptID  string         `json:"attempt_id,omitempty"`
			Turn       int            `json:"turn"`
			Summary    StateSummary   `json:"state"`
			Response   any            `json:"response,omitempty"`
			ToolCall   any            `json:"tool_call,omitempty"`
			ToolResult any            `json:"tool_result,omitempty"`
			Metadata   map[string]any `json:"metadata,omitempty"`
			Payload    any            `json:"payload,omitempty"`
			Error      string         `json:"error,omitempty"`
		}{event.AttemptID, event.Turn, event.StateSummary, event.Response, event.ToolCall, event.ToolResult, event.Metadata, event.Payload, errorText(event.Error)})
		if err != nil {
			return fmt.Errorf("runtime: encode durable event: %w", err)
		}
		s.mu.Lock()
		receipt, err := s.commitLocked(ctx, CommitRequest{Events: []StoredEvent{{Type: event.Type, Payload: payload}}})
		s.mu.Unlock()
		if err != nil {
			return err
		}
		event.Sequence = receipt.LastSequence
		event.ID = fmt.Sprintf("%s/%d", event.RunID, receipt.LastSequence)
	} else {
		event.Sequence = 0
		event.ID = ""
	}
	if err := emit(ctx, s.sink, event); err != nil {
		return &EventDeliveryError{Err: err}
	}
	return nil
}

func errorText(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func (s *runSession) finish(ctx context.Context, status RunStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.Status.Terminal() {
		return nil
	}
	typeOf := EventRunFailed
	if status == RunWaiting {
		typeOf = EventRunWaiting
	} else if status == RunCancelled {
		typeOf = EventRunCancelled
	}
	_, err := s.commitLocked(ctx, CommitRequest{Status: status, Events: []StoredEvent{{Type: typeOf, Payload: json.RawMessage(strconv.Quote(string(status)))}}})
	return err
}

func taskStorageID(call TaskCall) string {
	sum := sha256.Sum256([]byte(call.ID + "\x00" + call.Activation))
	return "task/" + hex.EncodeToString(sum[:])
}

func (s *runSession) ExecuteTask(ctx context.Context, call TaskCall, execute func(context.Context) (TaskOutcome, error)) (TaskOutcome, error) {
	outcome, err, _ := s.executeTask(ctx, call, execute)
	return outcome, err
}

func (s *runSession) executeTask(ctx context.Context, call TaskCall, execute func(context.Context) (TaskOutcome, error)) (TaskOutcome, error, bool) {
	if call.ID == "" || call.Activation == "" || !json.Valid(call.Input) {
		return TaskOutcome{}, fmt.Errorf("runtime: task requires fixed identity and JSON input"), false
	}
	id := taskStorageID(call)
	digest, err := ContentDigest(call.Input)
	if err != nil {
		return TaskOutcome{}, err, false
	}
	s.mu.Lock()
	activation, activated := s.record.TaskActivations[id]
	if activated && activation.Activation != digest {
		s.mu.Unlock()
		return TaskOutcome{}, ErrCommitConflict, false
	}
	if activated {
		if _, saved := s.record.Tasks[id]; !saved {
			recovered, lookupErr := s.lookupTaskReceiptLocked(ctx, id, activation)
			if lookupErr != nil {
				s.mu.Unlock()
				return TaskOutcome{}, &RunPersistenceError{Phase: "lookup original task receipt", Err: lookupErr}, false
			}
			if recovered != nil {
				if _, err := s.commitLocked(ctx, CommitRequest{PendingResults: []TaskResult{*recovered}, Events: []StoredEvent{{Type: "task_receipt_recovered"}}}); err != nil {
					s.mu.Unlock()
					return TaskOutcome{}, err, false
				}
			}
		}
	}
	if saved, ok := s.record.Tasks[id]; ok {
		var outcome TaskOutcome
		err := json.Unmarshal(saved.Result, &outcome)
		if saved.Status == TaskSucceeded {
			s.pendingAdopt[id] = true
		}
		s.markResumeInputsLocked(call.ResumeTargets)
		s.mu.Unlock()
		if err != nil {
			return TaskOutcome{}, err, true
		}
		if outcome.Error != "" {
			return outcome, &TaskExecutionError{TaskID: call.ID, Message: outcome.Error}, true
		}
		return outcome, nil, true
	}
	if activated && !call.ReplaySafe {
		s.mu.Unlock()
		return TaskOutcome{}, &TaskOutcomeUnknownError{TaskID: call.ID, Activation: call.Activation}, false
	}
	if !activated {
		activation = TaskActivation{TaskID: id, Activation: digest, InputRevision: s.record.Revision}
	}
	activation.AttemptID = util.GenerateID("task_attempt")
	_, err = s.commitLocked(ctx, CommitRequest{ActivateTasks: []TaskActivation{activation}})
	s.mu.Unlock()
	if err != nil {
		return TaskOutcome{}, err, false
	}
	var outcome TaskOutcome
	execErr := invokeExtension("task."+call.ID, func() error { var err error; outcome, err = execute(ctx); return err })
	if outcome.Status == "" {
		outcome.Status = TaskSucceeded
		if execErr != nil {
			outcome.Status = TaskFailed
		}
	}
	if execErr != nil {
		outcome.Error = execErr.Error()
	}
	data, err := json.Marshal(outcome)
	if err != nil {
		return outcome, errors.Join(execErr, err), false
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result := TaskResult{TaskID: id, Activation: activation.Activation, AttemptID: activation.AttemptID, InputRevision: activation.InputRevision, Status: outcome.Status, Result: data, Error: outcome.Error}
	if archiveErr := s.archiveTaskResult(finishCtx, result, llm.IsUnsafeToReplay(execErr)); archiveErr != nil {
		return outcome, errors.Join(execErr, &RunPersistenceError{Phase: "archive task receipt", Err: archiveErr}), false
	}
	s.mu.Lock()
	_, saveErr := s.commitLocked(finishCtx, CommitRequest{PendingResults: []TaskResult{result}, Events: []StoredEvent{{Type: "task_completed", Payload: data}}})
	if saveErr == nil && outcome.Status == TaskSucceeded {
		s.pendingAdopt[id] = true
	}
	if saveErr == nil {
		s.markResumeInputsLocked(call.ResumeTargets)
	}
	s.mu.Unlock()
	return outcome, errors.Join(execErr, saveErr), false
}

func (s *runSession) markResumeInputsLocked(targets []string) {
	for _, target := range targets {
		id, ok := s.resumeInputs[target]
		if !ok {
			continue
		}
		found := false
		for _, pending := range s.consumeInputs {
			if pending == id {
				found = true
				break
			}
		}
		if !found {
			s.consumeInputs = append(s.consumeInputs, id)
		}
	}
}
