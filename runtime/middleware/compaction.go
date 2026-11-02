package middleware

import (
	"context"
	"fmt"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/tokenizer"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
	"github.com/hexagon-codes/toolkit/lang/stringx"
)

// TokenCounter 估算一段消息历史的 token 数。以依赖注入方式提供，使中间件不硬依赖
// 具体 tokenizer；nil 时复用 ai-core 的中文感知估算。
type TokenCounter func(messages []llm.Message) int

// Compactor 把消息历史压缩为更短的等价历史。以依赖注入方式提供，使「摘要式压缩」
// （需 LLM）等策略可插拔；nil 时 Compaction 用内置策略「保留全部 system + 最近 N 条」。
// 自定义压缩须完整保留待完成调用组；改写消息语义时应清除该消息的 ProviderContinuation，
// 不可同时回放改写后的正文与旧协议输出。
// 返回错误时不采用候选消息，调用方保留原消息并处理失败。
type Compactor func(ctx context.Context, messages []llm.Message) ([]llm.Message, error)

// Compaction 是上下文压缩中间件：在每次 LLM 调用前估算消息历史 token，超过阈值即压缩
// state.Messages，并发 ContextCompacted{before,after} 事件。
//
// 设计要点：
//   - 未提供 ShouldCompact 且 MaxTokens <= 0 时完全关闭。
//   - token 估算（Count）与压缩策略（Compact）均可注入；默认估算为中文感知估算、默认压缩为
//     「保留 system + 最近 KeepRecent 条」截断（不需 LLM）；摘要式压缩由调用方注入 Compact。
//   - 内置策略以完整 assistant/tool 调用组为截断边界，保留协议续传与工具返回的对应关系。
//
// 线程安全：Compaction 自身无可变状态；注入回调的并发安全由调用方负责。
type Compaction struct {
	// MaxTokens 触发压缩的 token 阈值；未提供 ShouldCompact 时，<= 0 表示关闭。
	MaxTokens int
	// ShouldCompact 可选的业务触发条件，优先于 MaxTokens；nil 时沿用 token 阈值。
	// 可用于保留调用方既有的字节阈值。回调不得修改消息，token 统计仍用于事件观测。
	ShouldCompact func(messages []llm.Message) bool
	// KeepRecent 内置压缩保留的最近非 system 消息条数；<= 0 时默认 6。
	KeepRecent int
	// Count token 估算函数；nil 时用内置中文感知估算。
	Count TokenCounter
	// CountRequest 覆盖完整请求；优先于 Count。Count 仅覆盖消息部分，工具/输出 Schema 仍计入。
	CountRequest RequestTokenCounter
	// Compact 压缩函数；nil 时用内置「保留 system + 最近 KeepRecent」。
	Compact Compactor
}

func (c Compaction) count(messages []llm.Message) int {
	if c.Count != nil {
		return c.Count(messages)
	}
	return estimateTokens(messages)
}

func (c Compaction) compact(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
	if c.Compact != nil {
		return c.Compact(ctx, hruntime.CloneOutputMessages(messages))
	}
	return keepRecent(messages, c.keepRecent()), nil
}

func (c Compaction) keepRecent() int {
	if c.KeepRecent <= 0 {
		return 6
	}
	return c.KeepRecent
}

// BeforeLLM 估算当前历史 token，超阈值则压缩并发 ContextCompacted 事件。
func (c Compaction) BeforeLLM(ctx context.Context, state *hruntime.State) error {
	if state == nil {
		return nil
	}
	if c.ShouldCompact == nil && c.MaxTokens <= 0 {
		return nil
	}
	request, err := state.Request.Generation.ApplyTo(llm.CompletionRequest{Model: state.Request.ModelName, Messages: state.Messages, Tools: state.Request.Tools, Metadata: state.Request.Metadata})
	if err != nil {
		return fmt.Errorf("prepare compaction request: %w", err)
	}
	// 压缩只覆盖原历史；计量使用与实际派发相同的请求派生，注入提示不写回历史。
	frozenRequest := state.Request
	model := frozenRequest.ModelName
	if selected, ok := state.Attributes["model"].(string); ok {
		model = selected
	}
	metered := c
	metered.CountRequest = func(history llm.CompletionRequest) (int, error) {
		prepared, err := hruntime.PrepareModelRequest(frozenRequest, history.Messages, model)
		if err != nil {
			return 0, err
		}
		return c.countRequest(prepared)
	}
	work, err := metered.Prepare(request, state.MessageRevision)
	if err != nil {
		return err
	}
	if work == nil {
		return nil
	}
	candidate, err := work.Run(ctx)
	if err != nil {
		return err
	}
	compacted, err := AdoptCompaction(candidate, state.MessageRevision, state.Messages)
	if err != nil {
		return err
	}
	state.Messages = compacted
	_ = state.Emit(ctx, hruntime.Event{
		Type: hruntime.EventContextCompacted,
		Metadata: map[string]any{
			"before_tokens": candidate.BeforeTokens,
			"after_tokens":  candidate.AfterTokens,
			"max_tokens":    c.MaxTokens,
		},
	})
	return nil
}

// AfterLLM 无操作。
func (Compaction) AfterLLM(context.Context, *hruntime.State, *llm.CompletionResponse) error {
	return nil
}

// BeforeTool 无操作。
func (Compaction) BeforeTool(context.Context, *hruntime.State, llm.ToolCall) error { return nil }

// AfterTool 无操作。
func (Compaction) AfterTool(context.Context, *hruntime.State, llm.ToolCall, hruntime.ToolResult) error {
	return nil
}

// Finalize 无操作。
func (Compaction) Finalize(context.Context, *hruntime.State) error { return nil }

// estimateTokens 内置默认估算：逐条消息委托 ai-core tokenizer.CountGPT4（GPT-4 参数，
// 中文感知），与全生态统一口径，避免原 len(content)/4+4 字节口径对 CJK/英文漂移（RU-8）。
// 仅作默认值，精确计量请注入 TokenCounter。
func estimateTokens(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		n := tokenizer.CountGPT4(m.Content)
		if n == 0 && m.Content != "" {
			n = 1
		}
		total += n
	}
	return total
}

// keepRecent 保留全部 system 与最近 keep 条非 system 消息，窗口向前扩展到完整调用组。
func keepRecent(messages []llm.Message, keep int) []llm.Message {
	if len(messages) == 0 {
		return messages
	}
	// 统计非 system 总数，算出从第几条非 system 起开始保留。
	nonSystem := 0
	for _, m := range messages {
		if m.Role != llm.RoleSystem {
			nonSystem++
		}
	}
	drop := nonSystem - keep
	if drop <= 0 {
		return messages // 无需压缩
	}
	keepStart := len(messages)
	for i, message := range messages {
		if message.Role != llm.RoleSystem {
			if drop == 0 {
				keepStart = i
				break
			}
			drop--
		}
	}
	_, keepStart = RecentMessageBounds(messages, len(messages)-keepStart)
	out := make([]llm.Message, 0, len(messages))
	for i, message := range messages {
		if message.Role == llm.RoleSystem || i >= keepStart {
			out = append(out, message)
		}
	}
	return out
}

// HeuristicCompressOptions 控制启发式压缩参数。零值时使用合理默认。
type HeuristicCompressOptions struct {
	// KeepRecentToolPairs 保留最近 N 组 assistant 工具调用及全部返回（默认 3）。
	KeepRecentToolPairs int
	// MaxAssistantChars assistant 回复超过该长度截断（默认 4000）
	MaxAssistantChars int
	// PreservedRoles 永不压缩的 role（如 "system"）
	PreservedRoles map[string]bool
}

// HeuristicCompress 保留最近完整工具组并缩短超长 assistant 正文，返回压缩后的副本。
// 用户各轮原文和多模态内容完整保留，不依据正文相同或文本长度推断重复消息和媒体。
// cfg 为零值时使用默认参数。
func HeuristicCompress(history []llm.Message, cfg HeuristicCompressOptions) []llm.Message {
	history = hruntime.CloneOutputMessages(history)
	if cfg.KeepRecentToolPairs <= 0 {
		cfg.KeepRecentToolPairs = 3
	}
	if cfg.MaxAssistantChars <= 0 {
		cfg.MaxAssistantChars = 4000
	}
	if cfg.PreservedRoles == nil {
		cfg.PreservedRoles = map[string]bool{"system": true}
	}

	// 规则 1：以 assistant 调用组为边界，避免并列工具返回被按消息条数截断。
	toolKeepStart := heuristicToolKeepStart(history, cfg.KeepRecentToolPairs)

	out := make([]llm.Message, 0, len(history))
	for i, m := range history {
		role := strings.ToLower(string(m.Role))

		// 永不压缩的 role
		if cfg.PreservedRoles[role] {
			out = append(out, m)
			continue
		}

		// 规则 1: 老 tool_call/tool_result 跳过
		if (role == "tool" || (role == "assistant" && len(m.ToolCalls) > 0) || m.Name == hruntime.ToolMediaMessageName) && i < toolKeepStart {
			continue
		}

		// assistant 超长截断按码点保留头尾，避免切裂中文或 emoji。
		if role == "assistant" {
			runes := []rune(m.Content)
			if len(runes) > cfg.MaxAssistantChars {
				// 调用组的原始协议条目与正文保持一致，不能只缩短显示正文。
				if m.ProviderContinuation != nil && len(m.ToolCalls) > 0 {
					out = append(out, m)
					continue
				}
				head := cfg.MaxAssistantChars * 60 / 100
				tail := cfg.MaxAssistantChars * 20 / 100
				m.Content = string(runes[:head]) + "\n...[truncated]...\n" + string(runes[len(runes)-tail:])
				m.ProviderContinuation = nil
			}
		}

		out = append(out, m)
	}

	return out
}

func heuristicToolKeepStart(messages []llm.Message, keep int) int {
	if keep <= 0 {
		keep = 3
	}
	start, groups := len(messages), 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleAssistant && len(messages[i].ToolCalls) > 0 {
			start, groups = i, groups+1
			if groups == keep {
				break
			}
		}
	}
	return start
}

// HasCompactableToolMedia 检查是否存在应随旧调用组释放的媒体，不依赖正文长度。
func HasCompactableToolMedia(messages []llm.Message, keep int) bool {
	start := heuristicToolKeepStart(messages, keep)
	for _, message := range messages[:start] {
		if message.Name == hruntime.ToolMediaMessageName {
			return true
		}
	}
	return false
}

// RecentMessageBounds 返回前置 system 段末尾及最近消息窗口起点。
// 最近窗口内的 tool 返回按调用 ID 找回 assistant，保留夹在结果之间的多模态消息。
// keepRecent 小于零时按零处理；函数不修改输入。
func RecentMessageBounds(messages []llm.Message, keepRecent int) (systemEnd, keepStart int) {
	// system 消息通常在开头
	systemEnd = 0
	for i, m := range messages {
		if m.Role == "system" {
			systemEnd = i + 1
		} else {
			break
		}
	}

	// 保留最近 N 条
	keepStart = len(messages) - max(keepRecent, 0)
	if keepStart < systemEnd {
		keepStart = systemEnd
	}

	if keepStart == len(messages) || keepStart == systemEnd {
		return
	}

	// 记录每个结果实际对应的调用位置；同一 ID 在新轮次复用时只匹配前方最近一次调用。
	callByID := make(map[string]int)
	callForResult := make([]int, len(messages))
	lastCall := -1
	for i, message := range messages {
		callForResult[i] = -1
		if message.Role == llm.RoleAssistant && len(message.ToolCalls) > 0 {
			lastCall = i
			for _, call := range message.ToolCalls {
				if call.ID != "" {
					callByID[call.ID] = i
				}
			}
		} else if message.Role == llm.RoleTool {
			if message.ToolCallID == "" {
				callForResult[i] = lastCall // 无 ID 的旧格式沿用最近调用组。
			} else if start, ok := callByID[message.ToolCallID]; ok {
				callForResult[i] = start
			}
		} else if message.Name == hruntime.ToolMediaMessageName {
			callForResult[i] = lastCall
		}
	}

	// 从后向前扩展窗口，新增保留区内的结果也参与检查，不受中间消息 role 影响。
	for i := len(messages) - 1; i >= keepStart; i-- {
		if start := callForResult[i]; start >= systemEnd && start < keepStart {
			keepStart = start
		}
	}

	return
}

// HeuristicToolSummary 提取用户约束、工具名称和简短结果，不调用模型。
// 优先按 ToolCallID 配对；没有 ID 的旧返回按尚未消费的调用顺序回退。
func HeuristicToolSummary(msgs []llm.Message) string {
	var userConstraints strings.Builder
	var toolSummary strings.Builder
	toolCount := 0

	// ID 索引与顺序游标共用消费状态，避免混合新旧返回重复匹配同一调用。
	type pendingCall struct {
		id, name string
		used     bool
	}
	var pending []pendingCall
	byID := make(map[string]int)
	nextLegacy := 0

	for _, m := range msgs {
		if m.Role == "user" && m.Content != "" {
			// 保留用户消息中的约束和指令
			userConstraints.WriteString(fmt.Sprintf("User: %s\n", stringx.TruncateWithSuffix(m.Content, 200, "...")))
		} else if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				toolCount++
				if tc.ID != "" {
					byID[tc.ID] = len(pending)
				}
				pending = append(pending, pendingCall{id: tc.ID, name: tc.Name})
			}
		} else if m.Role == "tool" {
			name := "(unknown)"
			index := -1
			if m.ToolCallID != "" {
				if found, ok := byID[m.ToolCallID]; ok {
					index = found
				}
			} else {
				for nextLegacy < len(pending) && pending[nextLegacy].used {
					nextLegacy++
				}
				if nextLegacy < len(pending) {
					index = nextLegacy
					nextLegacy++
				}
			}
			if index >= 0 && !pending[index].used {
				call := &pending[index]
				name = call.name
				call.used = true
				if current, ok := byID[call.id]; ok && current == index {
					delete(byID, call.id)
				}
			}
			content := m.Content
			if strings.HasPrefix(content, "Error") {
				toolSummary.WriteString(fmt.Sprintf("- %s: %s\n", name, stringx.TruncateWithSuffix(content, 150, "...")))
			} else {
				toolSummary.WriteString(fmt.Sprintf("- %s: %s\n", name, stringx.TruncateWithSuffix(content, 100, "...")))
			}
		}
	}

	if toolCount == 0 && userConstraints.Len() == 0 {
		return "(no interactions to summarize)"
	}

	var result strings.Builder
	if userConstraints.Len() > 0 {
		result.WriteString("User context:\n")
		result.WriteString(userConstraints.String())
		result.WriteByte('\n')
	}
	if toolCount > 0 {
		result.WriteString(fmt.Sprintf("Executed %d tool calls:\n", toolCount))
		result.WriteString(toolSummary.String())
	}
	return result.String()
}
