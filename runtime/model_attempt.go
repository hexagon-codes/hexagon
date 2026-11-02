package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/internal/util"
)

// ModelAttempt 记录一次 Provider 方法调用，不将包装器内部的 HTTP 重试次数猜成一。
// ProviderCalled 表示方法已调用，不代表上游已受理；UsageKnown 区分实报零与未知。
type ModelAttempt struct {
	ID                    string    `json:"id"`
	ParentAttemptID       string    `json:"parent_attempt_id,omitempty"`
	Granularity           string    `json:"granularity"`
	RunID                 string    `json:"run_id,omitempty"`
	Turn                  int       `json:"turn"`
	Purpose               string    `json:"purpose"`
	Provider              string    `json:"provider"`
	RequestedModel        string    `json:"requested_model,omitempty"`
	Model                 string    `json:"model,omitempty"`
	ModelSource           string    `json:"model_source,omitempty"`
	StartedAt             time.Time `json:"started_at"`
	FinishedAt            time.Time `json:"finished_at,omitempty"`
	ProviderCalled        bool      `json:"provider_called"`
	ProviderSucceeded     bool      `json:"provider_succeeded"`
	CancellationRequested bool      `json:"cancellation_requested,omitempty"`
	Outcome               string    `json:"outcome"`
	Usage                 llm.Usage `json:"usage"`
	UsageKnown            bool      `json:"usage_known"`
	UsagePartial          bool      `json:"usage_partial,omitempty"`
	UsageFields           []string  `json:"usage_fields,omitempty"`
	RequestID             string    `json:"request_id,omitempty"`
	ResponseID            string    `json:"response_id,omitempty"`
	Error                 error     `json:"-"`
}

// BeforeModelAttempt 在每次调用前检查已有策略；投影上的修改不提交到运行状态。
type BeforeModelAttempt interface {
	BeforeModelAttempt(context.Context, *State, ModelAttempt) error
}

// AfterModelAttempt 接收所有尝试的结果，包括前置处理失败和取消。
// 只有 ProviderCalled 为真才调用过 Provider；未知用量不得当零计账。
type AfterModelAttempt interface {
	AfterModelAttempt(context.Context, *State, ModelAttempt) error
}

// AttemptHookError 将策略/归档错误与 Provider 错误分开，不能因此触发模型 fallback。
type AttemptHookError struct {
	Phase string
	Err   error
}

func (e *AttemptHookError) Error() string      { return fmt.Sprintf("model attempt %s: %v", e.Phase, e.Err) }
func (e *AttemptHookError) Unwrap() error      { return e.Err }
func (*AttemptHookError) UnsafeToReplay() bool { return true }

func (r *DefaultRunner) callProvider(ctx context.Context, req Request, selection ProviderSelection, callReq llm.CompletionRequest, state *State, emitter *runEmitter) (response *llm.CompletionResponse, err error) {
	attempt := ModelAttempt{ID: util.GenerateID("attempt"), Granularity: "provider_method", RunID: req.ID, Turn: state.Turn, Purpose: req.Purpose, Provider: selection.Name, RequestedModel: selection.Model, Model: selection.Model, ModelSource: "requested", StartedAt: time.Now(), Outcome: "prepared"}
	if attempt.Purpose == "" {
		attempt.Purpose = "agent"
	}
	usageBefore := state.Usage
	knownBefore := usageCount(state.Attributes, UsageReportedCallsMetadataKey)
	missingBefore := usageCount(state.Attributes, UsageMissingCallsMetadataKey)
	partialBefore := usageCount(state.Attributes, UsagePartialCallsMetadataKey)
	lowerBoundBefore := usageCount(state.Attributes, UsageKnownTokenLowerBoundMetadataKey)
	if known := knownUsageLowerBound(usageBefore); known > lowerBoundBefore {
		lowerBoundBefore = known
	}
	physicalContext, physical := r.observePhysicalCalls(ctx, state, attempt)
	previousAttempt := emitter.attemptID
	emitter.attemptID = attempt.ID
	defer func() {
		attempt.ProviderSucceeded = attempt.ProviderCalled && err == nil
		attempt.CancellationRequested = ctx.Err() != nil
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		attempt.FinishedAt, attempt.Error = time.Now(), err
		receipts := physical.receipts()
		if len(receipts) > 0 {
			summary := physical.ledger.Summary()
			state.Usage = addUsage(usageBefore, summary.Usage)
			state.setUsageEvidence(knownBefore+summary.ReportedAttempts, missingBefore+summary.UnknownAttempts)
			state.Attributes[UsagePartialCallsMetadataKey] = partialBefore + summary.PartialAttempts
			state.Attributes[UsageKnownTokenLowerBoundMetadataKey] = lowerBoundBefore + physical.knownTokenLowerBound()
			if summary.PartialAttempts+partialBefore > 0 && summary.UnknownAttempts+missingBefore == 0 {
				state.Attributes[UsageSourceMetadataKey] = "partial"
			}
		}
		if failure := physical.failure(); failure != nil {
			err = errors.Join(err, &AttemptHookError{Phase: "receipt", Err: failure})
		}
		if attempt.ProviderCalled {
			state.lastAttemptID = attempt.ID
		}
		attempt.Usage = usageDifference(state.Usage, usageBefore)
		attempt.UsageKnown = usageCount(state.Attributes, UsageReportedCallsMetadataKey) > knownBefore
		attempt.Outcome = "not_called"
		if attempt.ProviderCalled {
			attempt.Outcome = "succeeded"
			if err != nil {
				attempt.Outcome = "failed"
			}
		}
		if response != nil {
			attempt.RequestID, attempt.ResponseID = response.RequestID, response.ID
			if response.Model != "" {
				attempt.Model = response.Model
				attempt.ModelSource = "reported"
				state.Attributes["model"] = response.Model
			}
		}
		// 取消后仍给已发生的用量/回执一个有界归档窗口，不继续执行模型或工具。
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.attemptFinalizeTimeout)
		defer cancel()
		accounting := []ModelAttempt{attempt}
		if len(receipts) > 0 {
			accounting = nil
			for _, receipt := range receipts {
				accounting = append(accounting, physicalModelAttempt(attempt, receipt))
			}
		}
		for _, fact := range accounting {
			if r.session != nil {
				if failure := r.session.archiveModelFact(finishCtx, fact); failure != nil {
					err = errors.Join(err, &AttemptHookError{Phase: "archive_receipt", Err: failure})
				}
			}
			for _, middleware := range r.middleware {
				if observer, ok := middleware.(AfterModelAttempt); ok {
					if failure := invokeExtension("after_model_attempt", func() error { return observer.AfterModelAttempt(finishCtx, state.Snapshot(), fact) }); failure != nil {
						err = errors.Join(err, &AttemptHookError{Phase: "after", Err: failure})
						if errors.Is(failure, ErrBudgetExceeded) {
							_ = emitter.emit(finishCtx, Event{Type: EventBudgetExceeded, State: state, Error: failure})
						}
					}
				}
			}
			if fact.Granularity == "http_round_trip" {
				if failure := emitter.emit(finishCtx, Event{Type: "http_attempt_finished", State: state, Payload: fact, Error: fact.Error}); failure != nil {
					err = errors.Join(err, &AttemptHookError{Phase: "event", Err: failure})
				}
			}
		}
		if failure := emitter.emit(finishCtx, Event{Type: EventModelAttemptFinished, State: state, Payload: attempt, Error: err, RuntimeError: runtimeError("model_attempt", err)}); failure != nil {
			err = errors.Join(err, &AttemptHookError{Phase: "event", Err: failure})
		}
		emitter.attemptID = previousAttempt
	}()
	for _, middleware := range r.middleware {
		if policy, ok := middleware.(BeforeModelAttempt); ok {
			if failure := invokeExtension("before_model_attempt", func() error { return policy.BeforeModelAttempt(ctx, state.Snapshot(), attempt) }); failure != nil {
				return nil, &AttemptHookError{Phase: "before", Err: failure}
			}
		}
	}
	if failure := emitter.emit(ctx, Event{Type: EventModelAttemptStarted, State: state, Payload: attempt}); failure != nil {
		return nil, &AttemptHookError{Phase: "event", Err: failure}
	}
	if failure := ctx.Err(); failure != nil {
		return nil, failure
	}
	attempt.ProviderCalled = true
	err = invokeExtension("provider.call", func() error {
		response, err = r.performProviderCall(physicalContext, req, selection, callReq, state, emitter)
		return err
	})
	return response, err
}

// ModelCall 的每次调用都形成独立 attempt，不能按 wrapper 外层次数计量。
type ModelCall func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error)

// ModelCallWrapper 返回派生请求或受控结果，不持有可写的内核运行状态。
type ModelCallWrapper interface {
	WrapModelCall(context.Context, *State, llm.CompletionRequest, ModelCall) (*llm.CompletionResponse, error)
}

// EventModelDraftReplaced 表示同一次模型包装调用放弃上一尝试的显示草稿。
const EventModelDraftReplaced EventType = "model_draft_replaced"

func (r *DefaultRunner) invokeProvider(ctx context.Context, req Request, selection ProviderSelection, callReq llm.CompletionRequest, state *State, emitter *runEmitter) (*llm.CompletionResponse, error) {
	// 入口以前的部分回答属于显式 Continue；这里只撤回本包装调用产生的新草稿。
	basePartial, baseReasoning := state.partialText, state.Reasoning
	baseMessages, baseOutput := len(state.Messages), len(state.outputMessages)
	baseDraft, baseDraftMessage, baseDraftOutput := state.hasPartialDraft, state.draftMessageStart, state.draftOutputStart
	calls := 0
	previousText := ""
	var previousErr error
	replaceDraft := func(ctx context.Context) error {
		discarded := strings.TrimPrefix(state.partialText, basePartial)
		if discarded == "" {
			discarded = previousText
		}
		state.partialText, state.Reasoning = basePartial, baseReasoning
		state.Messages, state.outputMessages = state.Messages[:baseMessages], state.outputMessages[:baseOutput]
		state.hasPartialDraft, state.draftMessageStart, state.draftOutputStart = baseDraft, baseDraftMessage, baseDraftOutput
		if req.StreamMode != StreamModeTokens {
			return nil
		}
		return emitter.emit(ctx, Event{Type: EventModelDraftReplaced, State: state, Metadata: map[string]any{"output_action": "replace_draft", "replacement_text": basePartial, "discarded_text": discarded}})
	}
	var next ModelCall = func(ctx context.Context, prepared llm.CompletionRequest) (*llm.CompletionResponse, error) {
		if calls > 0 {
			if err := replaceDraft(ctx); err != nil {
				return nil, err
			}
		}
		calls++
		frozen, err := CloneModelRequest(prepared)
		if err != nil {
			return nil, err
		}
		prepared = frozen
		active := selection
		active.Model = prepared.Model
		state.Attributes["model"] = active.Model
		state.Attributes["provider"] = active.Name
		invocation := req
		invocation.Purpose = modelPurpose(ctx, req.Purpose)
		var response *llm.CompletionResponse
		if r.session != nil {
			response, err = r.session.callModel(ctx, r, invocation, active, prepared, state, emitter)
		} else {
			response, err = r.callProvider(ctx, invocation, active, prepared, state, emitter)
		}
		previousErr, previousText = err, ""
		if response != nil {
			previousText = response.Content
		}
		return response, err
	}
	for i := len(r.middleware) - 1; i >= 0; i-- {
		if wrapper, ok := r.middleware[i].(ModelCallWrapper); ok {
			inner := next
			next = func(ctx context.Context, prepared llm.CompletionRequest) (*llm.CompletionResponse, error) {
				var response *llm.CompletionResponse
				err := invokeExtension("wrap_model_call", func() error {
					var err error
					response, err = wrapper.WrapModelCall(ctx, state.Snapshot(), prepared, inner)
					return err
				})
				return response, err
			}
		}
	}
	var response *llm.CompletionResponse
	var err error
	if req.StructuredOutput != nil {
		response, err = r.invokeStructured(ctx, req, callReq, state, emitter, next)
	} else {
		response, err = next(ctx, callReq)
	}
	if err == nil && previousErr != nil {
		// 包装器也可能直接提供替代结果，而不再次调用 next。
		if err := replaceDraft(ctx); err != nil {
			return nil, err
		}
	}
	return response, err
}

func usageDifference(after, before llm.Usage) llm.Usage {
	return llm.Usage{PromptTokens: after.PromptTokens - before.PromptTokens, CompletionTokens: after.CompletionTokens - before.CompletionTokens, TotalTokens: after.TotalTokens - before.TotalTokens, CacheCreationTokens: after.CacheCreationTokens - before.CacheCreationTokens, CacheReadTokens: after.CacheReadTokens - before.CacheReadTokens}
}

func buildModelRequest(state *State, selection ProviderSelection) (llm.CompletionRequest, error) {
	return PrepareModelRequest(state.Request, state.Messages, selection.Model)
}

// PrepareModelRequest 从当前历史和冻结的请求合同构造实际模型输入。
// 返回值包含结构化输出的提示、结果工具和 Schema，不修改输入；调用方负责资源绑定和派发。
func PrepareModelRequest(req Request, messages []llm.Message, model string) (llm.CompletionRequest, error) {
	tools, err := llm.CloneToolDefinitions(req.Tools)
	if err != nil {
		return llm.CompletionRequest{}, fmt.Errorf("prepare tools: %w", err)
	}
	prepared, err := req.Generation.ApplyTo(llm.CompletionRequest{Model: model, Messages: sanitizeToolCallSequence(CloneOutputMessages(messages)), Tools: tools, Metadata: cloneRuntimeMetadata(req.Metadata)})
	if err != nil {
		return llm.CompletionRequest{}, err
	}
	spec, err := cloneStructuredOutputSpec(req.StructuredOutput)
	if err != nil {
		return llm.CompletionRequest{}, err
	}
	return prepareStructuredModelRequest(prepared, spec)
}
