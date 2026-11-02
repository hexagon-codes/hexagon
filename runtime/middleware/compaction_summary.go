package middleware

import (
	"context"
	"fmt"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	hr "github.com/hexagon-codes/hexagon/runtime"
)

const CompactionPurpose = "context_compaction"

// ModelSummaryCompactor 使用指定 Provider 与完整请求配置生成历史摘要。
// request.Messages 作为调用方的摘要提示前缀，随后附加被覆盖的历史与摘要指令；其他字段保留。
// 运行时通过统一 InvokeComplete 保存调用事实与用量，本函数不另行累计。
func ModelSummaryCompactor(provider llm.CallProvider, request llm.CompletionRequest, keepRecent int) Compactor {
	if keepRecent <= 0 {
		keepRecent = 6
	}
	frozen, freezeErr := cloneCompactionRequest(request)
	return func(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
		if freezeErr != nil {
			return nil, freezeErr
		}
		systemEnd, keepStart := RecentMessageBounds(messages, keepRecent)
		if keepStart <= systemEnd {
			return hr.CloneOutputMessages(messages), nil
		}
		prepared, err := cloneCompactionRequest(frozen)
		if err != nil {
			return nil, err
		}
		prepared.Messages = append(prepared.Messages, hr.CloneOutputMessages(messages[:keepStart])...)
		prepared.Messages = append(prepared.Messages, llm.UserMessage("Summarize the conversation history. Preserve user constraints, decisions, completed tool results, and unresolved work. Do not perform the work or call tools. Return only the summary."))
		response, err := llm.InvokeComplete(ctx, provider, prepared, CompactionPurpose)
		if err != nil {
			return nil, err
		}
		if response == nil || strings.TrimSpace(response.Content) == "" || len(response.ToolCalls) > 0 {
			return nil, fmt.Errorf("summary response did not contain a complete text summary")
		}
		switch strings.ToLower(strings.TrimSpace(response.FinishReason)) {
		case "", "stop", "end_turn", "stop_sequence", "completed":
		default:
			return nil, fmt.Errorf("summary response ended without completion: %s", response.FinishReason)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result := hr.CloneOutputMessages(messages[:systemEnd])
		result = append(result, llm.AssistantMessage("Context summary:\n"+response.Content))
		result = append(result, hr.CloneOutputMessages(messages[keepStart:])...)
		return result, nil
	}
}
