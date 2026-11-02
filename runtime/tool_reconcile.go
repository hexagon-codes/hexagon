package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hexagon-codes/ai-core/llm"
)

// ToolReconciliationError 区分已发生动作的核对需求和普通工具失败。
// 它不允许重做原业务动作；宿主可安排原身份查询或幂等补登记。
type ToolReconciliationError struct{ Err error }

func (e *ToolReconciliationError) Error() string {
	return fmt.Sprintf("runtime: tool result requires reconciliation: %v", e.Err)
}
func (e *ToolReconciliationError) Unwrap() error           { return e.Err }
func (*ToolReconciliationError) UnsafeToReplay() bool      { return true }
func (*ToolReconciliationError) NeedsReconciliation() bool { return true }

func needsToolReconciliation(err error) bool {
	if err == nil {
		return false
	}
	if marker, ok := err.(interface{ NeedsReconciliation() bool }); ok && marker.NeedsReconciliation() {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if needsToolReconciliation(child) {
				return true
			}
		}
	} else if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return needsToolReconciliation(wrapped.Unwrap())
	}
	return false
}

func (s *runSession) reconcileTool(ctx context.Context, executor ToolExecutor, call llm.ToolCall, previous ToolResult, original error) (ToolResult, error) {
	previous.NeedsReconciliation = true
	reconciler, ok := executor.(ToolResultReconciler)
	if !ok {
		return previous, &ToolReconciliationError{Err: original}
	}
	invocation, ok := ToolInvocationFromContext(ctx)
	if !ok {
		return previous, &ToolReconciliationError{Err: original}
	}
	input, err := json.Marshal(struct {
		Call     llm.ToolCall `json:"call"`
		Previous ToolResult   `json:"previous"`
	}{call, previous})
	if err != nil {
		return previous, &ToolReconciliationError{Err: errors.Join(original, err)}
	}
	// 核对器只允许原结果查询/幂等登记；不同 Worker 可重复核对，不能重调业务执行器。
	activation := invocation.Activation + "/lookup/" + s.authority.AttemptID
	outcome, lookupErr := s.ExecuteTask(ctx, TaskCall{ID: invocation.TaskID + "/reconcile", Activation: activation, Input: input, ReplaySafe: true}, func(ctx context.Context) (TaskOutcome, error) {
		result, handled, err := reconciler.ReconcileToolResult(ctx, call, previous)
		data, encodeErr := json.Marshal(struct {
			Handled bool       `json:"handled"`
			Result  ToolResult `json:"result"`
		}{handled, result})
		return TaskOutcome{Data: data}, errors.Join(err, encodeErr)
	})
	if lookupErr != nil {
		return previous, &ToolReconciliationError{Err: errors.Join(original, lookupErr)}
	}
	var repaired struct {
		Handled bool       `json:"handled"`
		Result  ToolResult `json:"result"`
	}
	if err := decodeRuntimeJSON(outcome.Data, &repaired); err != nil {
		return previous, &ToolReconciliationError{Err: errors.Join(original, err)}
	}
	if !repaired.Handled {
		return previous, &ToolReconciliationError{Err: original}
	}
	repaired.Result.NeedsReconciliation = false
	if repaired.Result.DurationMs == 0 {
		repaired.Result.DurationMs = previous.DurationMs
	}
	return repaired.Result, nil
}

// replaceReconciledTool 只更新当前轮聊天投影；原失败与核对结果在 Task/Receipt/Event 中保留。
func replaceReconciledTool(state *State, call llm.ToolCall, result ToolResult) bool {
	for index := state.pendingToolStart; index < len(state.ToolCalls); index++ {
		record := &state.ToolCalls[index]
		if record.ID != call.ID || !record.Result.NeedsReconciliation {
			continue
		}
		record.Result = result
		replace := func(messages []llm.Message) []llm.Message {
			start := 0
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Role == llm.RoleAssistant && len(messages[i].ToolCalls) > 0 {
					start = i + 1
					break
				}
			}
			for i := start; i < len(messages); i++ {
				if messages[i].Role != llm.RoleTool || messages[i].ToolCallID != call.ID {
					continue
				}
				message := llm.Message{Role: llm.RoleTool, Content: result.Content, ToolCallID: call.ID, ToolResultError: result.Error != "" || result.Status == ToolStatusError}
				end := i + 1
				if end < len(messages) && messages[end].Name == ToolMediaMessageName {
					end++
				}
				replacement := appendToolMedia([]llm.Message{message}, call.Name, call.ID, result)
				updated := append(CloneOutputMessages(messages[:i]), replacement...)
				updated = append(updated, CloneOutputMessages(messages[end:])...)
				return updated
			}
			return messages
		}
		state.Messages = replace(state.Messages)
		state.outputMessages = replace(state.outputMessages)
		state.MessageRevision++
		return true
	}
	return false
}
