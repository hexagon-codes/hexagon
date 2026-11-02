package runtime

import (
	"context"
	"errors"
	"reflect"

	"github.com/hexagon-codes/ai-core/llm"
)

// Middleware 接收暂存副本。成功时仅采用各阶段允许的变换；错误/ panic 不提交候选状态。
// Before/AfterLLM 可变换消息、工具、生成参数及元数据；AfterLLM 另可变换响应正文与工具调用。
// 所有阶段可更新扩展属性，计量事实不受其覆盖；Finalize 另可变换最终正文/推理展示。
// Turn、Final、Run 身份及已完成工具由执行核所有，修改副本不会改变这些事实。
type Middleware interface {
	BeforeLLM(ctx context.Context, state *State) error
	AfterLLM(ctx context.Context, state *State, resp *llm.CompletionResponse) error
	BeforeTool(ctx context.Context, state *State, call llm.ToolCall) error
	AfterTool(ctx context.Context, state *State, call llm.ToolCall, result ToolResult) error
	Finalize(ctx context.Context, state *State) error
}

// MiddlewareFuncSet is a convenience middleware implementation.
type MiddlewareFuncSet struct {
	BeforeLLMFunc          func(context.Context, *State) error
	AfterLLMFunc           func(context.Context, *State, *llm.CompletionResponse) error
	BeforeToolFunc         func(context.Context, *State, llm.ToolCall) error
	AfterToolFunc          func(context.Context, *State, llm.ToolCall, ToolResult) error
	FinalizeFunc           func(context.Context, *State) error
	WrapModelCallFunc      func(context.Context, *State, llm.CompletionRequest, ModelCall) (*llm.CompletionResponse, error)
	BeforeModelAttemptFunc func(context.Context, *State, ModelAttempt) error
	AfterModelAttemptFunc  func(context.Context, *State, ModelAttempt) error
}

func (m MiddlewareFuncSet) BeforeModelAttempt(ctx context.Context, state *State, attempt ModelAttempt) error {
	if m.BeforeModelAttemptFunc != nil {
		return m.BeforeModelAttemptFunc(ctx, state, attempt)
	}
	return nil
}

func (m MiddlewareFuncSet) AfterModelAttempt(ctx context.Context, state *State, attempt ModelAttempt) error {
	if m.AfterModelAttemptFunc != nil {
		return m.AfterModelAttemptFunc(ctx, state, attempt)
	}
	return nil
}

// WrapModelCall 将每次 next 委托到实际调用边界；短路不产生虚构用量。
func (m MiddlewareFuncSet) WrapModelCall(ctx context.Context, state *State, req llm.CompletionRequest, next ModelCall) (*llm.CompletionResponse, error) {
	if m.WrapModelCallFunc != nil {
		return m.WrapModelCallFunc(ctx, state, req, next)
	}
	return next(ctx, req)
}

func (m MiddlewareFuncSet) BeforeLLM(ctx context.Context, s *State) error {
	if m.BeforeLLMFunc != nil {
		return m.BeforeLLMFunc(ctx, s)
	}
	return nil
}
func (m MiddlewareFuncSet) AfterLLM(ctx context.Context, s *State, r *llm.CompletionResponse) error {
	if m.AfterLLMFunc != nil {
		return m.AfterLLMFunc(ctx, s, r)
	}
	return nil
}
func (m MiddlewareFuncSet) BeforeTool(ctx context.Context, s *State, c llm.ToolCall) error {
	if m.BeforeToolFunc != nil {
		return m.BeforeToolFunc(ctx, s, c)
	}
	return nil
}
func (m MiddlewareFuncSet) AfterTool(ctx context.Context, s *State, c llm.ToolCall, r ToolResult) error {
	if m.AfterToolFunc != nil {
		return m.AfterToolFunc(ctx, s, c, r)
	}
	return nil
}
func (m MiddlewareFuncSet) Finalize(ctx context.Context, s *State) error {
	if m.FinalizeFunc != nil {
		return m.FinalizeFunc(ctx, s)
	}
	return nil
}

func runBeforeLLM(ctx context.Context, mw []Middleware, state *State) error {
	for _, m := range mw {
		if err := stageMiddleware(ctx, state, "before_llm", func(candidate *State) error { return m.BeforeLLM(ctx, candidate) }); err != nil {
			return err
		}
	}
	return nil
}

func runAfterLLM(ctx context.Context, mw []Middleware, state *State, resp *llm.CompletionResponse) error {
	for _, m := range mw {
		candidateResponse := snapshotEventPayload(Event{Response: resp}).Response
		if err := stageMiddleware(ctx, state, "after_llm", func(candidate *State) error { return m.AfterLLM(ctx, candidate, candidateResponse) }); err != nil {
			return err
		}
		if resp != nil && candidateResponse != nil {
			resp.Content = candidateResponse.Content
			resp.ToolCalls = append([]llm.ToolCall(nil), candidateResponse.ToolCalls...)
		}
	}
	return nil
}

func runBeforeTool(ctx context.Context, mw []Middleware, state *State, call llm.ToolCall) error {
	for _, m := range mw {
		if err := stageMiddleware(ctx, state, "before_tool", func(candidate *State) error { return m.BeforeTool(ctx, candidate, call) }); err != nil {
			return err
		}
	}
	return nil
}

func runAfterTool(ctx context.Context, mw []Middleware, state *State, call llm.ToolCall, result ToolResult) error {
	for _, m := range mw {
		candidateResult := result
		candidateResult.Raw = cloneRuntimeValue(result.Raw)
		if err := stageMiddleware(ctx, state, "after_tool", func(candidate *State) error { return m.AfterTool(ctx, candidate, call, candidateResult) }); err != nil {
			return err
		}
	}
	return nil
}

func runFinalize(ctx context.Context, mw []Middleware, state *State) error {
	for _, m := range mw {
		if err := stageMiddleware(ctx, state, "finalize", func(candidate *State) error { return m.Finalize(ctx, candidate) }); err != nil {
			return err
		}
	}
	return nil
}

func stageMiddleware(ctx context.Context, state *State, phase string, fn func(*State) error) error {
	candidate := state.Snapshot()
	var events []Event
	candidate.emit = func(_ context.Context, event Event) error {
		event.State = nil
		events = append(events, snapshotEventPayload(event))
		return nil
	}
	err := invokeExtension("middleware."+phase, func() error { return fn(candidate) })
	if err == nil {
		if phase == "before_llm" || phase == "after_llm" {
			prepared, prepareErr := CloneRequest(candidate.Request)
			if prepareErr != nil {
				err = prepareErr
			} else {
				if !reflect.DeepEqual(state.Messages, candidate.Messages) {
					state.MessageRevision++
				}
				state.Messages = CloneOutputMessages(candidate.Messages)
				state.Request.Tools, state.Request.Generation, state.Request.Metadata = prepared.Tools, prepared.Generation, prepared.Metadata
			}
		}
		if err == nil {
			reported, missing := usageCount(state.Attributes, UsageReportedCallsMetadataKey), usageCount(state.Attributes, UsageMissingCallsMetadataKey)
			partial, lowerBound := usageCount(state.Attributes, UsagePartialCallsMetadataKey), usageCount(state.Attributes, UsageKnownTokenLowerBoundMetadataKey)
			state.Attributes = cloneRuntimeMetadata(candidate.Attributes)
			if state.Attributes == nil {
				state.Attributes = make(map[string]any)
			}
			state.setUsageEvidence(reported, missing)
			state.Attributes[UsagePartialCallsMetadataKey], state.Attributes[UsageKnownTokenLowerBoundMetadataKey] = partial, lowerBound
			if partial > 0 && missing == 0 {
				state.Attributes[UsageSourceMetadataKey] = "partial"
			}
			if phase == "finalize" {
				state.FinalText, state.Reasoning = candidate.FinalText, candidate.Reasoning
			}
		}
	}
	for _, event := range events {
		event.State = state
		if emitErr := state.Emit(ctx, event); emitErr != nil {
			err = errors.Join(err, emitErr)
		}
	}
	return err
}
