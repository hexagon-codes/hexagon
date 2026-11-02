package graph

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

	"github.com/hexagon-codes/hexagon/interrupt"
	hr "github.com/hexagon-codes/hexagon/runtime"
)

const graphSnapshotVersion = "graph.snapshot.v1"
const GraphDefinitionKind = "graph_definition"

// WithDefinitionReference 固定宿主已装配的节点实现版本，仅持久图强制要求。
// 使用 ObjectKey 时，宿主负责读取该稳定内容并装配匹配版本的处理器。
func (b *GraphBuilder[S]) WithDefinitionReference(reference hr.ContentReference) *GraphBuilder[S] {
	copy := reference
	copy.Content = append(json.RawMessage(nil), reference.Content...)
	if copy.Digest == "" && len(copy.Content) > 0 {
		digest, err := hr.ContentDigest(copy.Content)
		if err != nil {
			b.err = err
			return b
		}
		copy.Digest = digest
	}
	b.graph.definitionReference = &copy
	return b
}

// TaskOutcomeUnknownError 保留已登记但未确认的激活，交给宿主核对原操作。
type TaskOutcomeUnknownError struct{ TaskID string }

func (e *TaskOutcomeUnknownError) Error() string {
	return fmt.Sprintf("task %q outcome is unknown; reconcile the original activation before replay", e.TaskID)
}
func (*TaskOutcomeUnknownError) UnsafeToReplay() bool { return true }

type graphSpec struct {
	PlanID  string          `json:"plan_id"`
	Name    string          `json:"name"`
	Mode    GraphMode       `json:"mode"`
	Trigger TriggerMode     `json:"trigger"`
	Input   json.RawMessage `json:"input"`
}

type durableGraphSnapshot struct {
	Version  string          `json:"version"`
	RunID    string          `json:"run_id"`
	State    json.RawMessage `json:"state"`
	Frontier json.RawMessage `json:"frontier"`
}

// FreezeRunSpec 固定图计划与可读输入，交给宿主的 TaskStore.Submit 持久接收。
func (g *Graph[S]) FreezeRunSpec(runID string, input S) (hr.RunSpec, error) {
	if g.frozen != nil {
		g = g.frozen
	}
	if !g.compiled || g.plan == nil {
		return hr.RunSpec{}, fmt.Errorf("graph not compiled")
	}
	if g.definitionReference == nil || g.definitionReference.Kind != GraphDefinitionKind || g.definitionReference.ID == "" || g.definitionReference.Revision == "" || (len(g.definitionReference.Content) == 0 && g.definitionReference.ObjectKey == "") {
		return hr.RunSpec{}, fmt.Errorf("%w: immutable graph definition reference is required", hr.ErrUnrecoverableSpec)
	}
	state, err := g.codec().Encode(input)
	if err != nil {
		return hr.RunSpec{}, err
	}
	request, err := json.Marshal(graphSpec{g.plan.Identity, g.Name, g.plan.Mode, g.plan.TriggerMode, state})
	if err != nil {
		return hr.RunSpec{}, err
	}
	strategy := json.RawMessage(`{"name":"graph","version":"graph.scheduler.v1"}`)
	strategyDigest, err := hr.ContentDigest(strategy)
	if err != nil {
		return hr.RunSpec{}, err
	}
	plan, err := json.Marshal(g.plan)
	if err != nil {
		return hr.RunSpec{}, err
	}
	planDigest, err := hr.ContentDigest(plan)
	if err != nil {
		return hr.RunSpec{}, err
	}
	spec := hr.RunSpec{Version: hr.RunSpecVersion, RunID: runID, Request: request, Strategy: "graph", StrategyReference: hr.ContentReference{Kind: "strategy", ID: "graph", Revision: "graph.scheduler.v1", Digest: strategyDigest, Content: strategy}, Codec: hr.StateCodec{Type: g.codec().Type(), Version: g.codec().Version()}, Resources: []hr.ContentReference{{Kind: "graph_plan", ID: g.Name, Revision: g.plan.Identity, Digest: planDigest, Content: plan}, *g.definitionReference}}
	return spec, spec.Validate()
}

// GraphRunReader 只读取统一恢复权威；具体 SQL、连接和任务宿主仍由应用装配。
type GraphRunReader interface {
	GetRun(context.Context, string, string) (hr.RunRecord, bool, error)
}

// DurableGraphRunner 将图决议提交到共同运行端口，不启动 worker 或持有数据库事务。
type DurableGraphRunner[S State] struct {
	graph     *Graph[S]
	reader    GraphRunReader
	committer hr.Committer
}

// GraphResumeInput 引用宿主已接受的入站身份，Data 是该持久内容的具体类型解码结果。
type GraphResumeInput struct {
	InputID string
	Data    any
}

func NewDurableGraphRunner[S State](graph *Graph[S], reader GraphRunReader, committer hr.Committer) *DurableGraphRunner[S] {
	if graph != nil && graph.frozen != nil {
		graph = graph.frozen
	}
	return &DurableGraphRunner[S]{graph, reader, committer}
}

func (r *DurableGraphRunner[S]) load(ctx context.Context, scope, runID string) (hr.RunRecord, *planExecutor[S], error) {
	if r.graph == nil || r.graph.plan == nil || r.reader == nil {
		return hr.RunRecord{}, nil, fmt.Errorf("graph durable dependencies are incomplete")
	}
	record, found, err := r.reader.GetRun(ctx, scope, runID)
	if err != nil {
		return record, nil, err
	}
	if !found {
		return record, nil, hr.ErrRunNotFound
	}
	if err := record.Spec.Validate(); err != nil {
		return record, nil, err
	}
	if record.Spec.RunID != runID || record.Spec.Strategy != "graph" || record.Spec.Codec != (hr.StateCodec{Type: r.graph.codec().Type(), Version: r.graph.codec().Version()}) {
		return record, nil, fmt.Errorf("%w: graph specification mismatch", hr.ErrUnrecoverableSpec)
	}
	if record.Spec.StrategyReference.Revision != "graph.scheduler.v1" {
		return record, nil, fmt.Errorf("%w: graph scheduler version mismatch", hr.ErrUnrecoverableSpec)
	}
	matchedPlan := false
	matchedDefinition := false
	plan, err := json.Marshal(r.graph.plan)
	if err != nil {
		return record, nil, err
	}
	digest, err := hr.ContentDigest(plan)
	if err != nil {
		return record, nil, err
	}
	for _, resource := range record.Spec.Resources {
		if resource.Kind == "graph_plan" && resource.ID == r.graph.Name && resource.Revision == r.graph.plan.Identity && resource.Digest == digest {
			matchedPlan = true
		}
		if definition := r.graph.definitionReference; definition != nil && resource.Kind == GraphDefinitionKind && resource.ID == definition.ID && resource.Revision == definition.Revision && resource.Digest == definition.Digest && resource.ObjectKey == definition.ObjectKey {
			matchedDefinition = true
		}
	}
	if !matchedDefinition {
		return record, nil, fmt.Errorf("%w: immutable graph definition is unavailable", hr.ErrUnrecoverableSpec)
	}
	if !matchedPlan {
		return record, nil, fmt.Errorf("%w: frozen graph plan is unavailable", hr.ErrUnrecoverableSpec)
	}
	var spec graphSpec
	if err := json.Unmarshal(record.Spec.Request, &spec); err != nil {
		return record, nil, fmt.Errorf("%w: %w", hr.ErrUnrecoverableSpec, err)
	}
	if spec.PlanID != r.graph.plan.Identity || spec.Name != r.graph.Name || spec.Mode != r.graph.plan.Mode || spec.Trigger != r.graph.plan.TriggerMode || len(spec.Input) == 0 {
		return record, nil, fmt.Errorf("%w: graph plan mismatch", hr.ErrUnrecoverableSpec)
	}
	state, err := r.graph.codec().Decode(spec.Input)
	if err != nil {
		return record, nil, fmt.Errorf("%w: graph input: %w", hr.ErrUnrecoverableSpec, err)
	}
	x := newPlanExecutor(r.graph, state)
	if len(record.Snapshot) > 0 {
		var saved durableGraphSnapshot
		if err := json.Unmarshal(record.Snapshot, &saved); err != nil {
			return record, nil, fmt.Errorf("%w: %w", hr.ErrUnrecoverableSpec, err)
		}
		if saved.Version != graphSnapshotVersion || saved.RunID != runID {
			return record, nil, fmt.Errorf("%w: graph snapshot identity mismatch", hr.ErrUnrecoverableSpec)
		}
		state, err = r.graph.codec().Decode(saved.State)
		if err != nil {
			return record, nil, fmt.Errorf("%w: %w", hr.ErrUnrecoverableSpec, err)
		}
		x.state = state
		x.frontier, err = r.graph.decodeFrontier(saved.Frontier)
		if err != nil {
			return record, nil, fmt.Errorf("%w: %w", hr.ErrUnrecoverableSpec, err)
		}
	} else if len(record.Tasks) > 0 || len(record.TaskActivations) > 0 {
		return record, nil, fmt.Errorf("%w: graph snapshot is missing", hr.ErrUnrecoverableSpec)
	}
	return record, x, nil
}

// Inspect 只解码已提交状态，不派发节点、不申请执行权、不改写持久记录。
func (r *DurableGraphRunner[S]) Inspect(ctx context.Context, scope, runID string) (S, error) {
	var zero S
	_, x, err := r.load(ctx, scope, runID)
	if err != nil {
		return zero, err
	}
	copy, err := x.state.Clone()
	if err != nil {
		return zero, err
	}
	state, ok := copy.(S)
	if !ok {
		return zero, fmt.Errorf("graph state clone type mismatch")
	}
	return state, nil
}

// Run 消费宿主已取得的执行权；恢复只采用原结果，不把提交冲突当作重新执行许可。
func (r *DurableGraphRunner[S]) Run(ctx context.Context, scope, runID string, authority hr.ExecutionAuthority, targets map[string]any) (S, error) {
	if len(targets) > 0 {
		var zero S
		return zero, fmt.Errorf("durable graph resume requires accepted input identities; use RunSignals")
	}
	return r.run(ctx, scope, runID, authority, nil)
}

func (r *DurableGraphRunner[S]) RunSignals(ctx context.Context, scope, runID string, authority hr.ExecutionAuthority, inputs []GraphResumeInput) (S, error) {
	return r.run(ctx, scope, runID, authority, inputs)
}

func (r *DurableGraphRunner[S]) run(ctx context.Context, scope, runID string, authority hr.ExecutionAuthority, inputs []GraphResumeInput) (S, error) {
	var zero S
	record, x, err := r.load(ctx, scope, runID)
	if err != nil {
		return zero, err
	}
	if record.Status.Terminal() {
		if record.Status == hr.RunSucceeded {
			return x.state, nil
		}
		return x.state, fmt.Errorf("graph run is %s", record.Status)
	}
	if record.Authority != authority || authority.Owner == "" || r.committer == nil {
		return x.state, hr.ErrExecutionLost
	}
	restoredActivations := map[string]bool{}
	for id := range record.TaskActivations {
		restoredActivations[id] = true
	}
	addresses := map[string]interrupt.Address{}
	states := map[string]any{}
	for _, signal := range x.frontier.Waiting {
		a, s := interrupt.SignalToPersistenceMaps(signal)
		for id, value := range a {
			addresses[id] = value
		}
		for id, value := range s {
			states[id] = value
		}
	}
	targets := map[string]any{}
	inputTargets := map[string]string{}
	for _, input := range inputs {
		accepted, ok := record.Inputs[input.InputID]
		if !ok {
			return x.state, fmt.Errorf("graph input %q has not been accepted", input.InputID)
		}
		encoded, err := json.Marshal(input.Data)
		if err != nil {
			return x.state, err
		}
		var original, decoded bytes.Buffer
		if err := json.Compact(&original, accepted.Payload); err != nil {
			return x.state, err
		}
		if err := json.Compact(&decoded, encoded); err != nil {
			return x.state, err
		}
		if !bytes.Equal(original.Bytes(), decoded.Bytes()) {
			return x.state, hr.ErrCommitConflict
		}
		if accepted.ConsumedRevision != 0 {
			continue
		}
		inputTargets[input.InputID] = accepted.Target
		if accepted.Target != "" {
			targets[accepted.Target] = input.Data
		}
	}
	for id := range targets {
		if _, ok := addresses[id]; !ok {
			return x.state, fmt.Errorf("resume target %q does not belong to graph run", id)
		}
	}
	ctx = interrupt.PopulateResumeInfo(ctx, addresses, states)
	ctx = interrupt.BatchResumeWithData(ctx, targets)
	commit := func(request hr.CommitRequest) error {
		request.Scope = scope
		request.RunID = runID
		request.Authority = authority
		request.ExpectedRevision = record.Revision
		if request.Snapshot != nil {
			// 等待身份与同一次前沿快照提交；无等待时显式清空旧目标。
			ids := make(map[string]bool)
			for _, signal := range x.frontier.Waiting {
				addresses, _ := interrupt.SignalToPersistenceMaps(signal)
				for id := range addresses {
					ids[id] = true
				}
			}
			targets := make([]string, 0, len(ids))
			for id := range ids {
				targets = append(targets, id)
			}
			sort.Strings(targets)
			request.WaitingTargets = &targets
		}
		encoded, err := json.Marshal(request)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(encoded)
		request.CommitID = runID + "/graph/" + hex.EncodeToString(sum[:])
		request, err = hr.SealCommit(request)
		if err != nil {
			return err
		}
		receipt, err := r.committer.Commit(ctx, request)
		if err != nil {
			if !errors.Is(err, hr.ErrCommitUnknown) {
				return err
			}
			original, found, lookupErr := r.committer.LookupCommit(ctx, scope, request.CommitID)
			if lookupErr != nil || !found {
				return errors.Join(hr.ErrCommitUnknown, err, lookupErr)
			}
			if original.Digest != request.Digest {
				return hr.ErrCommitConflict
			}
			receipt = original
		}
		latest, found, err := r.reader.GetRun(ctx, scope, runID)
		if err != nil {
			return err
		}
		if !found {
			return hr.ErrRunNotFound
		}
		if latest.Revision != receipt.Revision {
			return hr.ErrRevisionConflict
		}
		record = latest
		return nil
	}
	snapshot := func(state S, frontier *graphFrontier) ([]byte, error) {
		stateBytes, err := r.graph.codec().Encode(state)
		if err != nil {
			return nil, err
		}
		data, err := r.graph.encodeFrontier(frontier)
		if err != nil {
			return nil, err
		}
		return json.Marshal(durableGraphSnapshot{graphSnapshotVersion, runID, stateBytes, data})
	}
	x.beforeDispatch = func(callCtx context.Context, state S, frontier *graphFrontier, names []string) error {
		request := hr.CommitRequest{}
		for _, name := range names {
			id := x.taskID(name)
			if _, ok := record.TaskActivations[id]; ok {
				if restoredActivations[id] && frontier.Results[id] == nil && frontier.Waiting[name] == nil {
					return &TaskOutcomeUnknownError{TaskID: id}
				}
				continue
			}
			request.ActivateTasks = append(request.ActivateTasks, hr.TaskActivation{TaskID: id, Activation: strconv.FormatUint(x.taskGeneration(name), 10), AttemptID: authority.AttemptID + "/" + id, InputRevision: record.Revision})
		}
		if len(request.ActivateTasks) == 0 {
			return nil
		}
		for id, target := range inputTargets {
			if target == "" && record.Inputs[id].ConsumedRevision == 0 {
				request.ConsumeInputIDs = append(request.ConsumeInputIDs, id)
			}
		}
		sort.Strings(request.ConsumeInputIDs)
		payload, _ := json.Marshal(request.ActivateTasks)
		request.Events = append(request.Events, hr.StoredEvent{Type: "graph.tasks_activated", Payload: payload})
		var err error
		request.Snapshot, err = snapshot(state, frontier)
		if err != nil {
			return err
		}
		return commit(request)
	}
	x.persist = func(callCtx context.Context, state S, frontier *graphFrontier) error {
		request := hr.CommitRequest{}
		ids := make([]string, 0, len(frontier.Results))
		for id := range frontier.Results {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			result := frontier.Results[id]
			activation, exists := record.TaskActivations[id]
			if !exists {
				return fmt.Errorf("task activation is missing: %s", id)
			}
			if _, exists := record.Tasks[id]; !exists {
				writes, err := json.Marshal(result.Writes)
				if err != nil {
					return err
				}
				request.PendingResults = append(request.PendingResults, hr.TaskResult{TaskID: id, Activation: activation.Activation, AttemptID: activation.AttemptID, InputRevision: activation.InputRevision, Status: hr.TaskSucceeded, Result: result.Output, Writes: writes})
				payload, _ := json.Marshal(map[string]string{"task_id": id, "node": result.Node})
				request.Events = append(request.Events, hr.StoredEvent{Type: "graph.task_completed", Payload: payload})
			}
			if result.Adopted && record.Tasks[id].AdoptedRevision == 0 {
				request.AdoptTaskIDs = append(request.AdoptTaskIDs, id)
			}
		}
		for id, failure := range x.failed {
			if _, exists := record.Tasks[id]; exists {
				continue
			}
			activation, exists := record.TaskActivations[id]
			if !exists {
				return fmt.Errorf("failed task activation is missing: %s", id)
			}
			request.PendingResults = append(request.PendingResults, hr.TaskResult{TaskID: id, Activation: activation.Activation, AttemptID: activation.AttemptID, InputRevision: activation.InputRevision, Status: hr.TaskFailed, Error: failure.Error()})
			payload, _ := json.Marshal(map[string]string{"task_id": id, "error": failure.Error()})
			request.Events = append(request.Events, hr.StoredEvent{Type: "graph.task_failed", Payload: payload})
		}
		if len(request.AdoptTaskIDs) > 0 {
			payload, _ := json.Marshal(request.AdoptTaskIDs)
			request.Events = append(request.Events, hr.StoredEvent{Type: "graph.tasks_adopted", Payload: payload})
		}
		remaining := map[string]bool{}
		for _, signal := range frontier.Waiting {
			for _, point := range interrupt.ToInterruptContexts(signal) {
				remaining[point.ID] = true
			}
		}
		for id, target := range inputTargets {
			if target != "" && !remaining[target] && record.Inputs[id].ConsumedRevision == 0 {
				request.ConsumeInputIDs = append(request.ConsumeInputIDs, id)
			}
		}
		sort.Strings(request.ConsumeInputIDs)
		var err error
		request.Snapshot, err = snapshot(state, frontier)
		if err != nil {
			return err
		}
		return commit(request)
	}
	state, runErr := x.run(ctx)
	if runErr != nil {
		var persistence *GraphPersistenceError
		if errors.As(runErr, &persistence) {
			return state, runErr
		}
		if errors.Is(runErr, hr.ErrCommitUnknown) || errors.Is(runErr, hr.ErrRevisionConflict) || errors.Is(runErr, hr.ErrExecutionLost) {
			return state, runErr
		}
		var unknown *TaskOutcomeUnknownError
		if errors.As(runErr, &unknown) {
			return state, runErr
		}
		if _, waiting := graphWaitingSignal(ctx, runErr); waiting {
			if err := x.save(ctx); err != nil {
				return state, errors.Join(runErr, err)
			}
			data, err := snapshot(state, x.frontier)
			if err != nil {
				return state, errors.Join(runErr, err)
			}
			if err := commit(hr.CommitRequest{Snapshot: data, Status: hr.RunWaiting, Events: []hr.StoredEvent{{Type: "graph.waiting"}}}); err != nil {
				return state, errors.Join(runErr, err)
			}
		} else if ctx.Err() == nil {
			if err := x.save(ctx); err != nil {
				return state, errors.Join(runErr, err)
			}
			data, err := snapshot(state, x.frontier)
			if err != nil {
				return state, errors.Join(runErr, err)
			}
			if err := commit(hr.CommitRequest{Snapshot: data, Status: hr.RunFailed, Events: []hr.StoredEvent{{Type: "graph.failed"}}}); err != nil {
				return state, errors.Join(runErr, err)
			}
		}
		return state, runErr
	}
	data, err := snapshot(state, x.frontier)
	if err != nil {
		return state, err
	}
	if err := commit(hr.CommitRequest{Snapshot: data, Status: hr.RunSucceeded, Events: []hr.StoredEvent{{Type: "graph.completed"}}}); err != nil {
		return state, err
	}
	return state, nil
}
