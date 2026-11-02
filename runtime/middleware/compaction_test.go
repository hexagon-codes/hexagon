package middleware

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/template"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
)

func msgs(n int) []llm.Message {
	out := make([]llm.Message, 0, n+1)
	out = append(out, llm.Message{Role: llm.RoleSystem, Content: "sys"})
	for i := 0; i < n; i++ {
		out = append(out, llm.Message{Role: llm.RoleUser, Content: "0123456789012345678901234567890123456789"}) // 40 chars ≈ 14 tok
	}
	return out
}

// TestCompaction_TriggersAboveThreshold 验证超阈值触发压缩：保留 system + 最近 N，发事件。
func TestCompaction_TriggersAboveThreshold(t *testing.T) {
	c := Compaction{MaxTokens: 50, KeepRecent: 3}
	st := &hruntime.State{Messages: msgs(10), Attributes: map[string]any{}}

	if err := c.BeforeLLM(context.Background(), st); err != nil {
		t.Fatalf("BeforeLLM error = %v", err)
	}
	// 应保留 1 条 system + 最近 3 条 user = 4 条
	if len(st.Messages) != 4 {
		t.Fatalf("压缩后应剩 4 条（system+最近3），实际 %d", len(st.Messages))
	}
	if st.Messages[0].Role != llm.RoleSystem {
		t.Errorf("system 消息应保留在首位")
	}
}

// TestCompaction_NotTriggeredBelowThreshold 验证未超阈值不压缩。
func TestCompaction_NotTriggeredBelowThreshold(t *testing.T) {
	c := Compaction{MaxTokens: 100000, KeepRecent: 3}
	st := &hruntime.State{Messages: msgs(5), Attributes: map[string]any{}}
	before := len(st.Messages)

	if err := c.BeforeLLM(context.Background(), st); err != nil {
		t.Fatalf("BeforeLLM error = %v", err)
	}
	if len(st.Messages) != before {
		t.Fatalf("未超阈值不应压缩；%d → %d", before, len(st.Messages))
	}
}

// TestCompaction_DisabledWhenMaxTokensZero 验证阈值<=0 完全关闭（行为不变）。
func TestCompaction_DisabledWhenMaxTokensZero(t *testing.T) {
	c := Compaction{MaxTokens: 0}
	st := &hruntime.State{Messages: msgs(100), Attributes: map[string]any{}}
	before := len(st.Messages)
	if err := c.BeforeLLM(context.Background(), st); err != nil {
		t.Fatalf("BeforeLLM error = %v", err)
	}
	if len(st.Messages) != before {
		t.Fatalf("MaxTokens<=0 应关闭压缩；%d → %d", before, len(st.Messages))
	}
}

// TestCompaction_CustomCompactor 验证可注入压缩器（如摘要式）。
func TestCompaction_CustomCompactor(t *testing.T) {
	called := false
	c := Compaction{
		MaxTokens: 1,
		Count:     func([]llm.Message) int { return 999 }, // 强制超阈值
		Compact: func(_ context.Context, m []llm.Message) ([]llm.Message, error) {
			called = true
			return m[:1], nil // 自定义：只留第一条
		},
	}
	st := &hruntime.State{Messages: msgs(10), Attributes: map[string]any{}}
	if err := c.BeforeLLM(context.Background(), st); err != nil {
		t.Fatalf("BeforeLLM error = %v", err)
	}
	if !called {
		t.Fatal("自定义 Compactor 应被调用")
	}
	if len(st.Messages) != 1 {
		t.Fatalf("自定义压缩后应剩 1 条，实际 %d", len(st.Messages))
	}
}

// 压缩失败或取消时，候选消息及其中的修改不得覆盖原始历史。
func TestCompaction_FailurePreservesHistory(t *testing.T) {
	failure := errors.New("summary unavailable")
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled_%v", cancel), func(t *testing.T) {
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			original := msgs(3)
			state := &hruntime.State{Messages: original}
			c := Compaction{
				MaxTokens: 1,
				Compact: func(_ context.Context, candidate []llm.Message) ([]llm.Message, error) {
					candidate[0].Content = "modified candidate"
					if cancel {
						stop()
						return candidate[:1], nil
					}
					return nil, failure
				},
			}
			wantErr := failure
			if cancel {
				wantErr = context.Canceled
			}
			if err := c.BeforeLLM(ctx, state); !errors.Is(err, wantErr) {
				t.Fatalf("error = %v, want %v", err, wantErr)
			}
			if !reflect.DeepEqual(state.Messages, msgs(3)) {
				t.Fatal("failed compaction changed original history")
			}
		})
	}
}

// 自定义触发条件保留应用既有阈值口径，不启用额外 token 预算。
func TestCompaction_CustomTrigger(t *testing.T) {
	for _, trigger := range []bool{false, true} {
		t.Run(map[bool]string{false: "below_existing_threshold", true: "at_existing_threshold"}[trigger], func(t *testing.T) {
			called := false
			c := Compaction{
				ShouldCompact: func([]llm.Message) bool { return trigger },
				Compact: func(_ context.Context, messages []llm.Message) ([]llm.Message, error) {
					called = true
					return messages[:1], nil
				},
			}
			st := &hruntime.State{Messages: msgs(3)}
			if err := c.BeforeLLM(t.Context(), st); err != nil {
				t.Fatal(err)
			}
			if called != trigger {
				t.Fatalf("compactor called = %v, want %v", called, trigger)
			}
			if trigger && len(st.Messages) != 1 || !trigger && len(st.Messages) != 4 {
				t.Fatalf("unexpected retained messages: %d", len(st.Messages))
			}
			if err := c.BeforeLLM(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// 多个并列工具返回必须与其 assistant 调用一起保留。
func TestHeuristicCompressPreservesToolGroups(t *testing.T) {
	history := []llm.Message{
		llm.SystemMessage("system"),
		llm.AssistantToolCallMessage("", []llm.ToolCallRef{{ID: "old", Name: "lookup"}}),
		llm.ToolResultMessage("old", "old result"),
		llm.AssistantToolCallMessage("", []llm.ToolCallRef{{ID: "a", Name: "read"}, {ID: "b", Name: "read"}, {ID: "c", Name: "read"}}),
		llm.ToolResultMessage("a", "first result"),
		llm.ToolResultMessage("b", "second result"),
		llm.ToolResultMessage("c", "third result"),
		llm.UserMessage("解释刚才的结果"),
	}
	before := append([]llm.Message(nil), history...)
	got := HeuristicCompress(history, HeuristicCompressOptions{KeepRecentToolPairs: 1})
	want := append([]llm.Message{history[0]}, history[3:]...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool call group was split or old results retained: %+v", got)
	}
	if !reflect.DeepEqual(history, before) {
		t.Fatal("compression mutated the original history")
	}
}

// 图片不同的同文请求不能按纯文本去重。
func TestHeuristicCompressPreservesMultimodalRequests(t *testing.T) {
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "解题", MultiContent: []llm.ContentPart{template.NewImageURLPart("asset://page-1", "auto")}},
		{Role: llm.RoleUser, Content: "解题", MultiContent: []llm.ContentPart{template.NewImageURLPart("asset://page-2", "auto")}},
	}
	got := HeuristicCompress(history, HeuristicCompressOptions{})
	if !reflect.DeepEqual(got, history) {
		t.Fatalf("distinct image requests were merged: %+v", got)
	}
}

func TestRecentMessageBoundsKeepsToolGroup(t *testing.T) {
	history := []llm.Message{
		llm.SystemMessage("system"),
		llm.UserMessage("old"),
		llm.AssistantToolCallMessage("", []llm.ToolCallRef{{ID: "a", Name: "read"}, {ID: "b", Name: "read"}}),
		llm.ToolResultMessage("a", "a result"),
		llm.ToolResultMessage("b", "b result"),
		llm.UserMessage("current"),
	}
	systemEnd, keepStart := RecentMessageBounds(history, 3)
	if systemEnd != 1 || keepStart != 2 {
		t.Fatalf("bounds = (%d, %d), want (1, 2)", systemEnd, keepStart)
	}
	for _, keep := range []int{0, -1} {
		_, start := RecentMessageBounds(history, keep)
		if start != len(history) {
			t.Fatalf("empty recent window starts at %d", start)
		}
	}
}

// 工具返回之间的多模态消息不能成为拆开调用组的边界。
func TestRecentMessageBoundsKeepsInterleavedToolGroup(t *testing.T) {
	history := []llm.Message{
		llm.SystemMessage("system"),
		llm.AssistantToolCallMessage("", []llm.ToolCallRef{{ID: "page-1", Name: "old_read"}}),
		llm.ToolResultMessage("page-1", "old result"),
		llm.UserMessage("read the current pages"),
	}
	calls := []llm.ToolCallRef{}
	for i := 1; i <= 5; i++ {
		calls = append(calls, llm.ToolCallRef{ID: fmt.Sprintf("page-%d", i), Name: "read_page"})
	}
	history = append(history, llm.AssistantToolCallMessage("", calls))
	for _, call := range calls {
		history = append(history, llm.ToolResultMessage(call.ID, "current result"), llm.Message{
			Role: llm.RoleUser, MultiContent: []llm.ContentPart{template.NewImageURLPart("asset://"+call.ID, "auto")},
		})
	}
	before := append([]llm.Message(nil), history...)
	for _, keep := range []int{7, 8} {
		t.Run(fmt.Sprintf("recent_%d", keep), func(t *testing.T) {
			systemEnd, keepStart := RecentMessageBounds(history, keep)
			if systemEnd != 1 || keepStart != 4 {
				t.Fatalf("bounds = (%d, %d), want (1, 4); current tool group was split or old reused ID restored", systemEnd, keepStart)
			}
			if !reflect.DeepEqual(history, before) {
				t.Fatal("boundary calculation mutated the original history")
			}
		})
	}

	// 已经离开最近窗口的完整工具组不应被拉回。
	for i := 0; i < 6; i++ {
		history = append(history, llm.UserMessage("later request"), llm.AssistantMessage("later answer"))
	}
	_, keepStart := RecentMessageBounds(history, 8)
	if keepStart != 19 {
		t.Fatalf("expired tool group was restored: start = %d, want 19", keepStart)
	}
}

func TestHeuristicCompactionPreservesConstraintsAndUTF8(t *testing.T) {
	history := []llm.Message{
		llm.SystemMessage("system"),
		llm.UserMessage("不要修改，只读排查"),
		llm.AssistantMessage(strings.Repeat("中文🦀", 100)),
		llm.AssistantToolCallMessage("", []llm.ToolCallRef{{ID: "a", Name: "lookup"}}),
		llm.ToolResultMessage("a", "Error: unavailable"),
	}
	got := HeuristicCompress(history, HeuristicCompressOptions{MaxAssistantChars: 30})
	if !reflect.DeepEqual(got[0], history[0]) || !utf8.ValidString(got[2].Content) || len([]rune(got[2].Content)) >= 300 {
		t.Fatalf("system or UTF-8 text was damaged: %+v", got)
	}
	summary := HeuristicToolSummary(history)
	for _, wanted := range []string{"不要修改，只读排查", "lookup", "Error: unavailable"} {
		if !strings.Contains(summary, wanted) {
			t.Fatalf("summary lost %q: %s", wanted, summary)
		}
	}
}

// 同一句话在不同轮次中具有不同含义，不能仅凭正文去重。
func TestHeuristicCompressPreservesUserTurns(t *testing.T) {
	history := []llm.Message{
		llm.UserMessage("先查杭州"),
		llm.UserMessage("下雨吗？"),
		llm.AssistantMessage("杭州小雨"),
		llm.UserMessage("改查上海"),
		llm.UserMessage("下雨吗？"),
	}
	got := HeuristicCompress(history, HeuristicCompressOptions{})
	if !reflect.DeepEqual(got, history) {
		t.Fatalf("distinct user turns were removed: got %d messages, want %d", len(got), len(history))
	}
}

// 正文长度不表示媒体类型，用户长文及末尾指令必须完整保留。
func TestHeuristicCompressPreservesLongUserText(t *testing.T) {
	text := strings.Repeat("原文内容。", 9000) + "最后指令：只读核对，不修改。"
	history := []llm.Message{llm.UserMessage(text)}
	got := HeuristicCompress(history, HeuristicCompressOptions{})
	if !reflect.DeepEqual(got, history) {
		t.Fatalf("plain text changed: got %d bytes, want %d", len(got[0].Content), len(text))
	}
}

func TestHeuristicToolSummaryMatchesCallID(t *testing.T) {
	calls := llm.AssistantToolCallMessage("", []llm.ToolCallRef{{ID: "weather-a", Name: "weather"}, {ID: "kb-b", Name: "knowledge_search"}})
	for _, tc := range []struct {
		name     string
		messages []llm.Message
		want     []string
	}{
		{"reversed_results", []llm.Message{calls, llm.ToolResultMessage("kb-b", "教材结果"), llm.ToolResultMessage("weather-a", "天气结果")}, []string{"- knowledge_search: 教材结果", "- weather: 天气结果"}},
		{"id_then_legacy", []llm.Message{calls, llm.ToolResultMessage("kb-b", "教材结果"), llm.ToolResultMessage("", "天气结果")}, []string{"- knowledge_search: 教材结果", "- weather: 天气结果"}},
		{"legacy_then_id", []llm.Message{calls, llm.ToolResultMessage("", "天气结果"), llm.ToolResultMessage("kb-b", "教材结果")}, []string{"- knowledge_search: 教材结果", "- weather: 天气结果"}},
		{"unknown_id", []llm.Message{calls, llm.ToolResultMessage("missing", "未知结果"), llm.ToolResultMessage("weather-a", "天气结果")}, []string{"- (unknown): 未知结果", "- weather: 天气结果"}},
		{"reused_id_new_turn", []llm.Message{calls, llm.ToolResultMessage("weather-a", "天气结果"), llm.AssistantToolCallMessage("", []llm.ToolCallRef{{ID: "weather-a", Name: "browser"}}), llm.ToolResultMessage("weather-a", "网页结果")}, []string{"- weather: 天气结果", "- browser: 网页结果"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := HeuristicToolSummary(tc.messages)
			for _, want := range tc.want {
				if !strings.Contains(summary, want) {
					t.Fatalf("missing pairing %q in summary: %s", want, summary)
				}
			}
		})
	}
}
