package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/interrupt"
)

type durableModelResult struct {
	Response          *llm.CompletionResponse   `json:"response,omitempty"`
	Continuation      *llm.ProviderContinuation `json:"continuation,omitempty"`
	Receipt           *llm.Usage                `json:"usage_receipt,omitempty"`
	Usage             llm.Usage                 `json:"usage"`
	Reported          int                       `json:"reported"`
	Missing           int                       `json:"missing"`
	Partial           string                    `json:"partial,omitempty"`
	AttemptID         string                    `json:"attempt_id,omitempty"`
	Unsafe            bool                      `json:"unsafe_to_replay,omitempty"`
	HookFailure       bool                      `json:"hook_failure,omitempty"`
	PartialUsageCalls int                       `json:"partial_usage_calls,omitempty"`
	TokenLowerBound   int                       `json:"token_lower_bound,omitempty"`
}

func (s *runSession) callModel(ctx context.Context, runner *DefaultRunner, req Request, selection ProviderSelection, callReq llm.CompletionRequest, state *State, emitter *runEmitter) (*llm.CompletionResponse, error) {
	input, err := json.Marshal(struct {
		Request       llm.CompletionRequest        `json:"request"`
		Continuations []durableMessageContinuation `json:"continuations,omitempty"`
	}{callReq, snapshotContinuations(callReq.Messages)})
	if err != nil {
		return nil, err
	}
	routeData, err := json.Marshal([]string{selection.Name, selection.Model, req.Purpose})
	if err != nil {
		return nil, err
	}
	route, err := ContentDigest(routeData)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("model/%s/%d/%s", req.ID, state.Turn, route)
	s.mu.Lock()
	ordinal := s.modelOrdinals[key]
	s.modelOrdinals[key]++
	s.mu.Unlock()
	var live *llm.CompletionResponse
	outcome, callErr, replayed := s.executeTask(ctx, TaskCall{ID: fmt.Sprintf("%s/%d", key, ordinal), Activation: "1", Input: input}, func(ctx context.Context) (TaskOutcome, error) {
		before := state.Usage
		reported, missing := usageCount(state.Attributes, UsageReportedCallsMetadataKey), usageCount(state.Attributes, UsageMissingCallsMetadataKey)
		partial := state.partialText
		partialCalls := usageCount(state.Attributes, UsagePartialCallsMetadataKey)
		lowerBound := usageCount(state.Attributes, UsageKnownTokenLowerBoundMetadataKey)
		if known := knownUsageLowerBound(before); known > lowerBound {
			lowerBound = known
		}
		var err error
		live, err = runner.callProvider(ctx, req, selection, callReq, state, emitter)
		stored := durableModelResult{Response: live, Usage: usageDifference(state.Usage, before), Reported: usageCount(state.Attributes, UsageReportedCallsMetadataKey) - reported, Missing: usageCount(state.Attributes, UsageMissingCallsMetadataKey) - missing, Partial: state.partialText[len(partial):], AttemptID: state.lastAttemptID}
		stored.PartialUsageCalls = usageCount(state.Attributes, UsagePartialCallsMetadataKey) - partialCalls
		if current := usageCount(state.Attributes, UsageKnownTokenLowerBoundMetadataKey); current > lowerBound {
			stored.TokenLowerBound = current - lowerBound
		}
		stored.Unsafe = llm.IsUnsafeToReplay(err)
		var hook *AttemptHookError
		stored.HookFailure = errors.As(err, &hook)
		if live != nil {
			stored.Continuation, stored.Receipt = live.ProviderContinuation.Clone(), live.UsageReceipt
		}
		data, encodeErr := json.Marshal(stored)
		if encodeErr != nil {
			return TaskOutcome{}, encodeErr
		}
		return TaskOutcome{Data: data}, err
	})
	if !replayed {
		return live, callErr
	}
	var stored durableModelResult
	if err := json.Unmarshal(outcome.Data, &stored); err != nil {
		return nil, err
	}
	previousLowerBound := usageCount(state.Attributes, UsageKnownTokenLowerBoundMetadataKey)
	if known := knownUsageLowerBound(state.Usage); known > previousLowerBound {
		previousLowerBound = known
	}
	state.Usage = addUsage(state.Usage, stored.Usage)
	state.setUsageEvidence(usageCount(state.Attributes, UsageReportedCallsMetadataKey)+stored.Reported, usageCount(state.Attributes, UsageMissingCallsMetadataKey)+stored.Missing)
	state.Attributes[UsagePartialCallsMetadataKey] = usageCount(state.Attributes, UsagePartialCallsMetadataKey) + stored.PartialUsageCalls
	state.Attributes[UsageKnownTokenLowerBoundMetadataKey] = previousLowerBound + stored.TokenLowerBound
	if usageCount(state.Attributes, UsagePartialCallsMetadataKey) > 0 && usageCount(state.Attributes, UsageMissingCallsMetadataKey) == 0 {
		state.Attributes[UsageSourceMetadataKey] = "partial"
	}
	if stored.Partial != "" {
		state.appendPartialDraft(stored.Partial)
	}
	if stored.Response != nil {
		stored.Response.ProviderContinuation, stored.Response.UsageReceipt = stored.Continuation, stored.Receipt
	}
	if outcome.Error != "" {
		callErr = &StoredModelFailure{Message: outcome.Error, Unsafe: stored.Unsafe}
		if stored.HookFailure {
			callErr = &AttemptHookError{Phase: "restored", Err: callErr}
		}
	}
	if err := emitter.emit(ctx, Event{Type: "model_result_reused", State: state, Metadata: map[string]any{"original_attempt_id": stored.AttemptID}}); err != nil {
		return stored.Response, err
	}
	return stored.Response, callErr
}

type StoredModelFailure struct {
	Message string
	Unsafe  bool
}

func (e *StoredModelFailure) Error() string        { return e.Message }
func (e *StoredModelFailure) UnsafeToReplay() bool { return e.Unsafe }

func addUsage(a, b llm.Usage) llm.Usage {
	a.PromptTokens += b.PromptTokens
	a.CompletionTokens += b.CompletionTokens
	a.TotalTokens += b.TotalTokens
	a.CacheCreationTokens += b.CacheCreationTokens
	a.CacheReadTokens += b.CacheReadTokens
	return a
}

func (s *runSession) callTool(ctx context.Context, executor ToolExecutor, call llm.ToolCall) (ToolResult, error) {
	invocation, ok := ToolInvocationFromContext(ctx)
	if !ok {
		return ToolResult{}, fmt.Errorf("runtime: durable tool invocation identity is missing")
	}
	input, err := json.Marshal(call)
	if err != nil {
		return ToolResult{}, err
	}
	replaySafe := invocation.SideEffect.ReplaySafe()
	var live ToolResult
	var waitingSignal *interrupt.InterruptSignal
	var resumeTargets []string
	if target, matched := interrupt.ResumeTargetID(ctx); matched {
		resumeTargets = []string{target}
	}
	outcome, taskErr, replayed := s.executeTask(ctx, TaskCall{ID: invocation.TaskID, Activation: invocation.Activation, Input: input, ReplaySafe: replaySafe, ResumeTargets: resumeTargets}, func(ctx context.Context) (TaskOutcome, error) {
		var err error
		started := time.Now()
		live, err = executor.Execute(ctx, call)
		if !replaySafe && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			err = &ToolReconciliationError{Err: err}
		}
		live.NeedsReconciliation = live.NeedsReconciliation || needsToolReconciliation(err)
		if live.NeedsReconciliation && err == nil {
			err = &ToolReconciliationError{Err: fmt.Errorf("tool result is pending reconciliation")}
		}
		if live.DurationMs == 0 {
			live.DurationMs = time.Since(started).Milliseconds()
		}
		if snapshots, ok := executor.(ToolSnapshotter); ok {
			snapshot, snapshotErr := snapshots.SnapshotTools(ctx)
			if snapshotErr != nil {
				return TaskOutcome{}, &RunPersistenceError{Phase: "tool binding snapshot", Err: snapshotErr}
			}
			live.ToolSnapshot = cloneRuntimeToolSnapshot(&snapshot)
		}
		if len(live.ContentParts) == 0 {
			if media, ok := live.Raw.(interface{ ToolMessageContent() []llm.ContentPart }); ok {
				live.ContentParts = cloneContentParts(media.ToolMessageContent())
			}
		}
		stored := durableToolResult{Result: live}
		stored.Unsafe = llm.IsUnsafeToReplay(err)
		stored.Reconcile = live.NeedsReconciliation
		status := TaskSucceeded
		if signal, waiting := interrupt.IsInterruptSignal(err); waiting {
			waitingSignal = signal
			encoded, encodeErr := interrupt.EncodeSignal(signal, s.interruptCodecs)
			if encodeErr != nil {
				return TaskOutcome{}, encodeErr
			}
			stored.Waiting, status, err = encoded, TaskWaiting, nil
		} else if err != nil {
			status = TaskFailed
		}
		data, encodeErr := json.Marshal(stored)
		if encodeErr != nil {
			return TaskOutcome{}, encodeErr
		}
		return TaskOutcome{Status: status, Data: data}, err
	})
	if !replayed {
		if taskErr == nil && waitingSignal != nil {
			return live, waitingSignal
		}
		if live.NeedsReconciliation && taskErr != nil {
			return live, &ToolReconciliationError{Err: taskErr}
		}
		return live, taskErr
	}
	// 默认持久投影按 JSON 表示恢复 Raw，协议内容使用一等 ContentParts，
	// 不声称任意宿主 Go 对象或方法可以跨进程恢复。
	var restored durableToolResult
	if len(outcome.Data) > 0 {
		if err := decodeRuntimeJSON(outcome.Data, &restored); err != nil {
			return ToolResult{}, err
		}
	}
	if taskErr == nil && restored.Waiting != nil {
		signal, err := interrupt.DecodeSignal(restored.Waiting, s.interruptCodecs)
		if err != nil {
			return restored.Result, err
		}
		return restored.Result, signal
	}
	if outcome.Error != "" {
		taskErr = &StoredToolFailure{Message: outcome.Error, Unsafe: restored.Unsafe}
	}
	if restored.Reconcile {
		return s.reconcileTool(ctx, executor, call, restored.Result, taskErr)
	}
	return restored.Result, taskErr
}

type StoredToolFailure struct {
	Message string
	Unsafe  bool
}

func (e *StoredToolFailure) Error() string        { return e.Message }
func (e *StoredToolFailure) UnsafeToReplay() bool { return e.Unsafe }
