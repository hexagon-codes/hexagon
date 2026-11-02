package structured

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/llm/parser"
)

// OutputMode 指定结果所在的模型响应通道，不表示 Provider 已支持该能力。
type OutputMode string

const (
	// OutputModePrompt 保留提示模式从正文或代码块提取 JSON 的行为。
	OutputModePrompt OutputMode = "prompt"
	// OutputModeNative 要求正文直接包含 JSON，不从非 JSON 正文中猜测结果。
	OutputModeNative OutputMode = "native"
	// OutputModeTool 要求唯一的结果工具调用，其参数即结构化结果。
	OutputModeTool OutputMode = "tool"
)

var (
	// ErrRefused 表示 Provider 明确报告拒绝，格式修正不能将其变成成功。
	ErrRefused = errors.New("structured output: model refused the request")
	// ErrTruncated 表示 Provider 明确报告输出截断，即使已有合法 JSON 也不能成功。
	ErrTruncated = errors.New("structured output: model output was truncated")
)

// ExtractOutput 只提取结果并检查结束状态，不调用模型、不执行工具、不校验 Schema。
// 调用者必须继续用结果 Schema 验证返回的原始 JSON，再映射成 Go 对象。
// 空 mode 与 OutputModePrompt 等价；工具模式的空 toolName 使用 structured_output。
// 拒绝、截断可用 errors.Is 区分；未知结束原因不推断为拒绝，仍要求有效结果。
func ExtractOutput(resp *llm.CompletionResponse, mode OutputMode, toolName string) (json.RawMessage, error) {
	if resp == nil {
		return nil, fmt.Errorf("structured output: provider returned nil response")
	}
	switch strings.ToLower(strings.TrimSpace(resp.FinishReason)) {
	case "length", "max_tokens", "max_output_tokens", "incomplete":
		return nil, fmt.Errorf("%w: %s", ErrTruncated, resp.FinishReason)
	case "content_filter", "refusal", "safety", "recitation", "blocklist", "prohibited_content", "spii", "image_safety":
		return nil, fmt.Errorf("%w: %s", ErrRefused, resp.FinishReason)
	}
	output := resp.Content
	switch mode {
	case "", OutputModePrompt, OutputModeNative:
		if len(resp.ToolCalls) > 0 {
			return nil, fmt.Errorf("structured output: unexpected tool calls in text output mode")
		}
	case OutputModeTool:
		if toolName == "" {
			toolName = "structured_output"
		}
		if len(resp.ToolCalls) != 1 {
			return nil, fmt.Errorf("structured output: expected exactly one %q tool call, got %d", toolName, len(resp.ToolCalls))
		}
		call := resp.ToolCalls[0]
		if call.Name != toolName {
			return nil, fmt.Errorf("structured output: expected tool %q, got %q", toolName, call.Name)
		}
		if call.ID == "" {
			return nil, fmt.Errorf("structured output: result tool call has no ID")
		}
		output = call.Arguments
	default:
		return nil, fmt.Errorf("structured output: unknown output mode %q", mode)
	}
	return parser.NewJSONParser[json.RawMessage]().
		WithExtractJSON(mode == "" || mode == OutputModePrompt).
		Parse(context.Background(), output)
}

// RepairMessages 构造一次格式修正所需的消息，不执行任何工具或模型调用。
// 保留 assistant 工具引用、每个 tool_call_id 的失败结果以及 Provider 私有续传信息。
// 缺失或重复工具 ID 无法构成合法协议轮次，因此返回错误而不伪造 ID。
// 调用者负责修正次数、预算及 Schema 验证，且不应对拒绝或截断调用本函数。
func RepairMessages(resp *llm.CompletionResponse, mode OutputMode, toolName string, failure error) ([]llm.Message, error) {
	if resp == nil || failure == nil {
		return nil, fmt.Errorf("structured output: repair requires a response and validation error")
	}
	if errors.Is(failure, ErrRefused) || errors.Is(failure, ErrTruncated) {
		return nil, failure
	}
	assistant := llm.AssistantMessage(resp.Content)
	assistant.ProviderContinuation = resp.ProviderContinuation.Clone()
	seen := make(map[string]bool, len(resp.ToolCalls))
	for _, call := range resp.ToolCalls {
		if call.ID == "" || seen[call.ID] {
			return nil, fmt.Errorf("structured output: cannot repair missing or duplicate tool call ID")
		}
		seen[call.ID] = true
		assistant.ToolCalls = append(assistant.ToolCalls, llm.ToolCallRef{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	}
	feedback := "Structured output validation failed: " + failure.Error()
	messages := []llm.Message{assistant}
	for _, call := range resp.ToolCalls {
		message := llm.ToolResultMessage(call.ID, feedback)
		message.ToolResultError = true
		messages = append(messages, message)
	}
	instruction := "Return corrected JSON matching the provided JSON Schema, with all required fields and correct types. Do not include other text."
	if mode == OutputModeTool {
		if toolName == "" {
			toolName = "structured_output"
		}
		instruction = fmt.Sprintf("Call %q exactly once with corrected arguments matching its JSON Schema. Do not return the result as message text or call other tools.", toolName)
	}
	messages = append(messages, llm.UserMessage(feedback+"\n"+instruction))
	return messages, nil
}
