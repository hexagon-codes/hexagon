package middleware

import (
	"context"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/tokenizer"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

// TokenCounter 估算一段消息历史的 token 数。以依赖注入方式提供，使中间件不硬依赖
// 具体 tokenizer；nil 时 Compaction 退化为内置粗估（按字符数 /4 + 每条固定开销）。
type TokenCounter func(messages []llm.Message) int

// Compactor 把消息历史压缩为更短的等价历史。以依赖注入方式提供，使「摘要式压缩」
// （需 LLM）等策略可插拔；nil 时 Compaction 用内置策略「保留全部 system + 最近 N 条」。
type Compactor func(ctx context.Context, messages []llm.Message) []llm.Message

// Compaction 是上下文压缩中间件：在每次 LLM 调用前估算消息历史 token，超过阈值即压缩
// state.Messages，并发 ContextCompacted{before,after} 事件。
//
// 设计要点：
//   - 阈值 MaxTokens <= 0 时**完全关闭**（默认行为与无此中间件一致）。
//   - token 估算（Count）与压缩策略（Compact）均可注入；默认估算为粗估、默认压缩为
//     「保留 system + 最近 KeepRecent 条」截断（不需 LLM）；摘要式压缩由调用方注入 Compact。
//   - 压缩可能丢弃中间的 assistant/tool 配对，但 runner 在发送前会 sanitize 孤立 tool
//     message（见 runner.sanitizeToolCallSequence），故协议序列仍合规。
//
// 线程安全：Compaction 自身无可变状态，可被多个并发 run 复用。
type Compaction struct {
	// MaxTokens 触发压缩的 token 阈值；<= 0 表示关闭。
	MaxTokens int
	// KeepRecent 内置压缩保留的最近非 system 消息条数；<= 0 时默认 6。
	KeepRecent int
	// Count token 估算函数；nil 时用内置粗估。
	Count TokenCounter
	// Compact 压缩函数；nil 时用内置「保留 system + 最近 KeepRecent」。
	Compact Compactor
}

func (c Compaction) count(messages []llm.Message) int {
	if c.Count != nil {
		return c.Count(messages)
	}
	return estimateTokens(messages)
}

func (c Compaction) compact(ctx context.Context, messages []llm.Message) []llm.Message {
	if c.Compact != nil {
		return c.Compact(ctx, messages)
	}
	return keepRecent(messages, c.keepRecent())
}

func (c Compaction) keepRecent() int {
	if c.KeepRecent <= 0 {
		return 6
	}
	return c.KeepRecent
}

// BeforeLLM 估算当前历史 token，超阈值则压缩并发 ContextCompacted 事件。
func (c Compaction) BeforeLLM(ctx context.Context, state *hruntime.State) error {
	if c.MaxTokens <= 0 || state == nil {
		return nil
	}
	before := c.count(state.Messages)
	if before <= c.MaxTokens {
		return nil
	}
	compacted := c.compact(ctx, state.Messages)
	after := c.count(compacted)
	state.Messages = compacted
	_ = state.Emit(ctx, hruntime.Event{
		Type: hruntime.EventContextCompacted,
		Metadata: map[string]any{
			"before_tokens": before,
			"after_tokens":  after,
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

// keepRecent 内置压缩：保留全部 system 消息 + 最近 keep 条非 system 消息，保持原序。
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
	out := make([]llm.Message, 0, len(messages))
	dropped := 0
	for _, m := range messages {
		if m.Role == llm.RoleSystem {
			out = append(out, m) // system 始终保留
			continue
		}
		if dropped < drop {
			dropped++
			continue // 丢弃最早的非 system 消息
		}
		out = append(out, m)
	}
	return out
}
