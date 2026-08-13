// Package conversation 的审计测试（Wave 1）
//
// 本测试文件针对 conversation 包的全部公开 API 进行全场景覆盖：
//   - 正常路径：消息添加 / 获取 / 计数 / 清空 / 历史
//   - 边界：空内容 / nil / 超长 / 0 / 负数 / Unicode / 并发竞态
//   - 错误路径：Summarize 的 Provider 失败
//   - 状态一致性：maxMessages 截断、maxTurns 限制、Token 预算、Summarize 替换
//
// 设计原则：不 mock 被测逻辑本身；仅对外部 LLM Provider 用接口 mock，
// 验证本地的请求构造 / 响应处理 / 错误传播。
package conversation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// ============== 测试辅助：mock Provider ==============

// mockProvider 是 llm.Provider 的最小 mock 实现，
// 仅用于驱动 Summarize 的本地逻辑（请求构造 / 响应替换 / 错误传播）。
type mockProvider struct {
	// completeFn 允许测试自定义 Complete 行为
	completeFn func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error)
	// lastReq 记录最近一次收到的请求，用于断言请求构造正确性
	mu      sync.Mutex
	lastReq llm.CompletionRequest
	calls   int
}

func (p *mockProvider) Name() string { return "mock" }

func (p *mockProvider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.mu.Lock()
	p.lastReq = req
	p.calls++
	p.mu.Unlock()
	if p.completeFn != nil {
		return p.completeFn(ctx, req)
	}
	return &llm.CompletionResponse{Content: "默认摘要"}, nil
}

func (p *mockProvider) Stream(ctx context.Context, req llm.CompletionRequest) (*llm.Stream, error) {
	return nil, errors.New("not implemented")
}

func (p *mockProvider) Models() []llm.ModelInfo { return nil }

func (p *mockProvider) CountTokens(messages []llm.Message) (int, error) { return 0, nil }

// 编译期断言：mockProvider 实现 llm.Provider
var _ llm.Provider = (*mockProvider)(nil)

// ============== New / Option 默认值 ==============

func TestNew_DefaultsAndOptions(t *testing.T) {
	t.Run("默认值", func(t *testing.T) {
		m := New()
		if m.maxTokens != 4096 {
			t.Errorf("默认 maxTokens = %d, want 4096", m.maxTokens)
		}
		if m.maxMessages != 1000 {
			t.Errorf("默认 maxMessages = %d, want 1000", m.maxMessages)
		}
		if m.maxTurns != 0 {
			t.Errorf("默认 maxTurns = %d, want 0", m.maxTurns)
		}
		if m.systemPrompt != "" {
			t.Errorf("默认 systemPrompt = %q, want 空", m.systemPrompt)
		}
	})

	t.Run("所有选项生效", func(t *testing.T) {
		m := New(
			WithMaxTokens(100),
			WithMaxTurns(3),
			WithMaxMessages(50),
			WithSystemPrompt("你是助手"),
		)
		if m.maxTokens != 100 || m.maxTurns != 3 || m.maxMessages != 50 || m.systemPrompt != "你是助手" {
			t.Errorf("选项未全部生效: %+v", m)
		}
	})
}

// ============== 消息添加与计数 ==============

func TestAddAndCount(t *testing.T) {
	m := New(WithSystemPrompt(""))
	m.AddUserMessage("hi")
	m.AddAssistantMessage("hello")
	m.AddSystemMessage("sys")
	m.AddUserMessage("再问")

	if got := m.MessageCount(); got != 4 {
		t.Errorf("MessageCount = %d, want 4", got)
	}
	if got := m.TurnCount(); got != 2 {
		t.Errorf("TurnCount = %d, want 2 (两条 user)", got)
	}
}

func TestAddToolResult_Formatting(t *testing.T) {
	m := New()
	m.AddToolResult("search", "结果内容")
	hist := m.History()
	if len(hist) != 1 {
		t.Fatalf("History len = %d, want 1", len(hist))
	}
	if hist[0].Role != "tool" {
		t.Errorf("Role = %q, want tool", hist[0].Role)
	}
	want := "[search] 结果内容"
	if hist[0].Content != want {
		t.Errorf("Content = %q, want %q", hist[0].Content, want)
	}
}

func TestAddMessage_EmptyContent(t *testing.T) {
	// 边界：空内容也应被记录（库不做内容校验）
	m := New()
	m.AddUserMessage("")
	if m.MessageCount() != 1 {
		t.Errorf("空内容消息应被记录, MessageCount = %d, want 1", m.MessageCount())
	}
}

// ============== History 是副本（隔离性） ==============

func TestHistory_IsCopy(t *testing.T) {
	m := New()
	m.AddUserMessage("原始")
	hist := m.History()
	hist[0].Content = "被篡改"
	// 再取一次，内部不应被外部修改污染
	hist2 := m.History()
	if hist2[0].Content != "原始" {
		t.Errorf("History 应返回副本, 内部被篡改为 %q", hist2[0].Content)
	}
}

// ============== Clear ==============

func TestClear(t *testing.T) {
	m := New()
	m.AddUserMessage("a")
	m.AddAssistantMessage("b")
	m.Clear()
	if m.MessageCount() != 0 {
		t.Errorf("Clear 后 MessageCount = %d, want 0", m.MessageCount())
	}
	if len(m.History()) != 0 {
		t.Errorf("Clear 后 History 应为空")
	}
	// Clear 后仍可继续添加
	m.AddUserMessage("c")
	if m.MessageCount() != 1 {
		t.Errorf("Clear 后再添加失败")
	}
}

// ============== maxMessages 截断状态一致性 ==============

func TestMaxMessages_Trimming(t *testing.T) {
	m := New(WithMaxMessages(3), WithSystemPrompt(""))
	for i := 0; i < 10; i++ {
		m.AddUserMessage(string(rune('0' + i)))
	}
	if got := m.MessageCount(); got != 3 {
		t.Fatalf("MessageCount = %d, want 3", got)
	}
	hist := m.History()
	// 应保留最后 3 条："7","8","9"
	wantContents := []string{"7", "8", "9"}
	for i, w := range wantContents {
		if hist[i].Content != w {
			t.Errorf("trim 后 hist[%d].Content = %q, want %q", i, hist[i].Content, w)
		}
	}
}

func TestMaxMessages_Zero_NoLimit(t *testing.T) {
	// maxMessages=0 表示不限制
	m := New(WithMaxMessages(0))
	for i := 0; i < 100; i++ {
		m.AddUserMessage("x")
	}
	if m.MessageCount() != 100 {
		t.Errorf("maxMessages=0 应不限制, MessageCount = %d, want 100", m.MessageCount())
	}
}

func TestMaxMessages_Negative(t *testing.T) {
	// 边界：负数 maxMessages。条件是 `> 0` 才截断，故负数等价于不限制。
	m := New(WithMaxMessages(-5))
	for i := 0; i < 20; i++ {
		m.AddUserMessage("x")
	}
	if m.MessageCount() != 20 {
		t.Errorf("负数 maxMessages 应等价不限制, MessageCount = %d, want 20", m.MessageCount())
	}
}

// ============== GetMessages：系统提示词 ==============

func TestGetMessages_SystemPromptAlwaysFirst(t *testing.T) {
	m := New(WithSystemPrompt("你是助手"), WithMaxTokens(0))
	m.AddUserMessage("问题")
	msgs := m.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("len = %d, want 2", len(msgs))
	}
	if msgs[0].Role != llm.RoleSystem || msgs[0].Content != "你是助手" {
		t.Errorf("第一条应为系统提示词, got role=%q content=%q", msgs[0].Role, msgs[0].Content)
	}
	if msgs[1].Role != llm.RoleUser || msgs[1].Content != "问题" {
		t.Errorf("第二条应为用户消息, got role=%q content=%q", msgs[1].Role, msgs[1].Content)
	}
}

func TestGetMessages_NoSystemPrompt(t *testing.T) {
	m := New(WithSystemPrompt(""), WithMaxTokens(0))
	m.AddUserMessage("问题")
	msgs := m.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("无系统提示词时 len = %d, want 1", len(msgs))
	}
	if msgs[0].Role != llm.RoleUser {
		t.Errorf("第一条应为用户消息, got %q", msgs[0].Role)
	}
}

func TestGetMessages_Empty(t *testing.T) {
	// 边界：无任何消息，且无系统提示词
	m := New(WithSystemPrompt(""))
	if got := m.GetMessages(); len(got) != 0 {
		t.Errorf("空管理器 GetMessages 应返回空, got len=%d", len(got))
	}
	// 仅系统提示词
	m2 := New(WithSystemPrompt("sys"))
	if got := m2.GetMessages(); len(got) != 1 {
		t.Errorf("仅系统提示词时应返回 1 条, got len=%d", len(got))
	}
}

func TestGetMessages_TokenOrderPreserved(t *testing.T) {
	// 验证 Token 预算内消息保持时间顺序（buildMessages 有反转逻辑）
	m := New(WithSystemPrompt(""), WithMaxTokens(0))
	m.AddUserMessage("第一")
	m.AddAssistantMessage("第二")
	m.AddUserMessage("第三")
	msgs := m.GetMessages()
	wantOrder := []string{"第一", "第二", "第三"}
	if len(msgs) != 3 {
		t.Fatalf("len = %d, want 3", len(msgs))
	}
	for i, w := range wantOrder {
		if msgs[i].Content != w {
			t.Errorf("顺序错误 msgs[%d] = %q, want %q", i, msgs[i].Content, w)
		}
	}
}

// ============== Token 预算截断 ==============

func TestGetContext_TokenBudgetTruncation(t *testing.T) {
	m := New(WithSystemPrompt(""))
	// 规范口径 tokenizer.CountGPT4("aaaa") = 1 token/条（RU-8 修复后）
	for i := 0; i < 5; i++ {
		m.AddUserMessage("aaaa")
	}
	// 预算 4 token → 最多 4 条（4*1=4），第 5 条会使 5>4 break
	msgs := m.GetContext(4)
	if len(msgs) != 4 {
		t.Errorf("预算=4 时应保留 4 条, got %d", len(msgs))
	}
	// 应保留最近的 4 条（发生截断，最早 1 条被丢弃）
}

func TestGetContext_KeepsMostRecent(t *testing.T) {
	m := New(WithSystemPrompt(""))
	m.AddUserMessage("old")   // CountGPT4 -> 1 token
	m.AddUserMessage("newer") // CountGPT4 -> 1 token
	// 预算 1：从最近向前填充，"newer"(1) 用满，"old"(1) 加上为 2>1 break
	msgs := m.GetContext(1)
	if len(msgs) != 1 {
		t.Fatalf("len = %d, want 1", len(msgs))
	}
	if msgs[0].Content != "newer" {
		t.Errorf("应保留最新消息, got %q", msgs[0].Content)
	}
}

func TestGetContext_SystemPromptConsumesBudget(t *testing.T) {
	// 系统提示词先占用预算；若系统提示词已耗尽预算，则后续消息一条都进不来
	m := New(WithSystemPrompt("aaaaaaaa")) // CountGPT4 -> 2 token
	m.AddUserMessage("bb")                 // CountGPT4 -> 1 token
	msgs := m.GetContext(2)
	// usedTokens=2 已等于预算，user 消息 1 token 使 3>2 break
	if len(msgs) != 1 {
		t.Errorf("系统提示词占满预算后应只有系统提示词, got len=%d: %+v", len(msgs), msgs)
	}
	if len(msgs) == 1 && msgs[0].Role != llm.RoleSystem {
		t.Errorf("剩下的应为系统提示词, got %q", msgs[0].Role)
	}
}

func TestGetContext_SingleHugeMessageDropped(t *testing.T) {
	// 边界/疑似未闭环：单条超长消息超过预算时，break 会丢弃它及所有更早消息，
	// 导致最近一轮内容完全丢失（只剩系统提示词）。
	// 这里断言"现状行为"，以暴露该截断策略可能不符合直觉。
	m := New(WithSystemPrompt("sys")) // sys 3 runes -> 1 token
	long := strings.Repeat("x", 1000) // 1000 runes -> 500 token
	m.AddUserMessage(long)
	msgs := m.GetContext(10)
	// 超长消息被丢弃，仅剩系统提示词
	if len(msgs) != 1 {
		t.Errorf("现状: 超长消息被丢弃应仅剩系统提示词, got len=%d", len(msgs))
	}
}

func TestGetContext_NegativeBudget(t *testing.T) {
	// 边界：负数预算。tokenBudget>0 为假，故不做截断 -> 返回全部消息。
	m := New(WithSystemPrompt(""))
	m.AddUserMessage("a")
	m.AddUserMessage("b")
	msgs := m.GetContext(-100)
	if len(msgs) != 2 {
		t.Errorf("负预算应不截断（现状）, got len=%d, want 2", len(msgs))
	}
}

func TestGetContext_ZeroBudget(t *testing.T) {
	// 边界：0 预算表示不限制（与负数同一分支）
	m := New(WithSystemPrompt(""))
	for i := 0; i < 5; i++ {
		m.AddUserMessage("aaaa")
	}
	msgs := m.GetContext(0)
	if len(msgs) != 5 {
		t.Errorf("0 预算应不限制, got len=%d, want 5", len(msgs))
	}
}

// ============== maxTurns 限制 ==============

func TestMaxTurns(t *testing.T) {
	tests := []struct {
		name     string
		maxTurns int
		wantLen  int // 不含系统提示词
		wantLast string
	}{
		{"保留最近1轮", 1, 2, "回复3"},
		{"保留最近2轮", 2, 4, "回复3"},
		{"轮次超过总数", 10, 6, "回复3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(WithSystemPrompt(""), WithMaxTokens(0), WithMaxTurns(tt.maxTurns))
			m.AddUserMessage("问1")
			m.AddAssistantMessage("回复1")
			m.AddUserMessage("问2")
			m.AddAssistantMessage("回复2")
			m.AddUserMessage("问3")
			m.AddAssistantMessage("回复3")

			msgs := m.GetMessages()
			if len(msgs) != tt.wantLen {
				t.Fatalf("maxTurns=%d: len = %d, want %d, msgs=%+v", tt.maxTurns, len(msgs), tt.wantLen, msgs)
			}
			if msgs[len(msgs)-1].Content != tt.wantLast {
				t.Errorf("最后一条 = %q, want %q", msgs[len(msgs)-1].Content, tt.wantLast)
			}
		})
	}
}

func TestMaxTurns_FirstMessageIsUser(t *testing.T) {
	// maxTurns=1 且第一条就是 user，验证起始定位正确
	m := New(WithSystemPrompt(""), WithMaxTokens(0), WithMaxTurns(1))
	m.AddUserMessage("唯一问")
	m.AddAssistantMessage("唯一答")
	msgs := m.GetMessages()
	if len(msgs) != 2 || msgs[0].Content != "唯一问" {
		t.Errorf("maxTurns=1 单轮应保留全部, got %+v", msgs)
	}
}

func TestMaxTurns_TrailingAssistantOnly(t *testing.T) {
	// 末尾有 user 之后的多条 assistant，验证它们被纳入最后一轮
	m := New(WithSystemPrompt(""), WithMaxTokens(0), WithMaxTurns(1))
	m.AddUserMessage("问")
	m.AddAssistantMessage("答1")
	m.AddAssistantMessage("答2")
	msgs := m.GetMessages()
	if len(msgs) != 3 {
		t.Errorf("最后一轮含多条 assistant 应全保留, got len=%d", len(msgs))
	}
}

// ============== estimateTokens（通过 GetContext 间接 + 直接） ==============

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		// 规范口径：委托 ai-core tokenizer.CountGPT4（RU-8 修复后）
		{"空字符串", "", 0},
		{"单字符", "a", 1},
		{"两字符", "ab", 1},
		{"中文四字", "你好世界", 3}, // CountGPT4 中文感知
		{"emoji", "😀😀", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := estimateTokens(tt.in); got != tt.want {
				t.Errorf("estimateTokens(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestEstimateTokens_UnicodeNotByteBased(t *testing.T) {
	// 验证按 rune 而非 byte 计数：中文 1 字 3 byte，若按 byte 会严重高估
	cn := "你" // 1 rune, 3 bytes
	if got := estimateTokens(cn); got != 1 {
		t.Errorf("单中文 estimateTokens = %d, want 1（按 rune 而非 byte）", got)
	}
}

// ============== Summarize 正常路径 ==============

func TestSummarize_Success(t *testing.T) {
	m := New(WithSystemPrompt(""))
	m.AddUserMessage("问1")
	m.AddAssistantMessage("答1")
	m.AddUserMessage("问2")
	m.AddAssistantMessage("答2")
	m.AddUserMessage("最近问")
	m.AddAssistantMessage("最近答")

	prov := &mockProvider{
		completeFn: func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
			return &llm.CompletionResponse{Content: "这是摘要"}, nil
		},
	}

	if err := m.Summarize(context.Background(), prov, 2); err != nil {
		t.Fatalf("Summarize err = %v", err)
	}

	hist := m.History()
	// 期望：1 条摘要 + 最近 2 条 = 3 条
	if len(hist) != 3 {
		t.Fatalf("Summarize 后 len = %d, want 3, hist=%+v", len(hist), hist)
	}
	if hist[0].Role != "system" || !strings.Contains(hist[0].Content, "这是摘要") {
		t.Errorf("第一条应为摘要, got role=%q content=%q", hist[0].Role, hist[0].Content)
	}
	if !strings.HasPrefix(hist[0].Content, "[对话摘要]") {
		t.Errorf("摘要应带 [对话摘要] 前缀, got %q", hist[0].Content)
	}
	// 最近两条应原样保留
	if hist[1].Content != "最近问" || hist[2].Content != "最近答" {
		t.Errorf("最近消息应保留, got %q, %q", hist[1].Content, hist[2].Content)
	}
}

func TestSummarize_RequestConstruction(t *testing.T) {
	// 验证发给 Provider 的请求构造：含 system 指令 + 拼接的旧对话
	m := New()
	m.AddUserMessage("历史问")
	m.AddAssistantMessage("历史答")
	m.AddUserMessage("保留问")

	prov := &mockProvider{}
	if err := m.Summarize(context.Background(), prov, 1); err != nil {
		t.Fatalf("err = %v", err)
	}
	prov.mu.Lock()
	req := prov.lastReq
	calls := prov.calls
	prov.mu.Unlock()

	if calls != 1 {
		t.Fatalf("Provider 应被调用 1 次, got %d", calls)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("请求应有 2 条消息(system+user), got %d", len(req.Messages))
	}
	if req.Messages[0].Role != llm.RoleSystem {
		t.Errorf("第一条请求消息应为 system, got %q", req.Messages[0].Role)
	}
	if req.MaxTokens != 500 {
		t.Errorf("MaxTokens = %d, want 500", req.MaxTokens)
	}
	// user 内容应包含被摘要的旧消息
	body := req.Messages[1].Content
	if !strings.Contains(body, "历史问") || !strings.Contains(body, "历史答") {
		t.Errorf("摘要请求体应包含旧消息, got %q", body)
	}
	if strings.Contains(body, "保留问") {
		t.Errorf("保留的消息不应被发去摘要, got %q", body)
	}
}

func TestSummarize_NoopWhenFewMessages(t *testing.T) {
	// keepRecent >= 消息数时，应直接返回 nil 且不调用 Provider
	m := New()
	m.AddUserMessage("a")
	m.AddAssistantMessage("b")

	prov := &mockProvider{}
	if err := m.Summarize(context.Background(), prov, 5); err != nil {
		t.Fatalf("err = %v", err)
	}
	prov.mu.Lock()
	calls := prov.calls
	prov.mu.Unlock()
	if calls != 0 {
		t.Errorf("消息数 <= keepRecent 时不应调用 Provider, got calls=%d", calls)
	}
	if m.MessageCount() != 2 {
		t.Errorf("不应修改消息, MessageCount = %d, want 2", m.MessageCount())
	}
}

func TestSummarize_ProviderError(t *testing.T) {
	m := New()
	m.AddUserMessage("a")
	m.AddAssistantMessage("b")
	m.AddUserMessage("c")

	prov := &mockProvider{
		completeFn: func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
			return nil, errors.New("上游故障")
		},
	}
	err := m.Summarize(context.Background(), prov, 1)
	if err == nil {
		t.Fatal("Provider 失败时应返回错误")
	}
	if !strings.Contains(err.Error(), "对话摘要生成失败") {
		t.Errorf("错误应被包装, got %v", err)
	}
	// 失败后消息不应被修改
	if m.MessageCount() != 3 {
		t.Errorf("失败后消息不应改变, MessageCount = %d, want 3", m.MessageCount())
	}
}

func TestSummarize_ConcurrentClear(t *testing.T) {
	// 状态一致性：LLM 调用期间被 Clear，应不再替换（snapshot 守卫）
	m := New()
	for i := 0; i < 6; i++ {
		m.AddUserMessage("x")
	}

	cleared := make(chan struct{})
	prov := &mockProvider{
		completeFn: func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
			// 在 LLM 调用期间清空
			m.Clear()
			close(cleared)
			return &llm.CompletionResponse{Content: "摘要"}, nil
		},
	}
	if err := m.Summarize(context.Background(), prov, 2); err != nil {
		t.Fatalf("err = %v", err)
	}
	<-cleared
	// 被 Clear 后，Summarize 应感知到 len < snapshotLen 并放弃替换
	if m.MessageCount() != 0 {
		t.Errorf("Clear 后 Summarize 不应注入摘要, MessageCount = %d, want 0", m.MessageCount())
	}
}

// keepRecent 的边界探测：负数 / 0
func TestSummarize_KeepRecentBoundary(t *testing.T) {
	// 回归: B1 Summarize(keepRecent<0) 切片越界 panic（已对负值做 clamp 视为 0）
	t.Run("keepRecent为0", func(t *testing.T) {
		// keepRecent=0：保留 0 条最近消息，全部摘要
		m := New()
		m.AddUserMessage("a")
		m.AddAssistantMessage("b")
		prov := &mockProvider{
			completeFn: func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
				return &llm.CompletionResponse{Content: "全摘要"}, nil
			},
		}
		if err := m.Summarize(context.Background(), prov, 0); err != nil {
			t.Fatalf("err = %v", err)
		}
		hist := m.History()
		// 期望只剩 1 条摘要
		if len(hist) != 1 {
			t.Errorf("keepRecent=0 应只剩 1 条摘要, got len=%d: %+v", len(hist), hist)
		}
	})

	t.Run("keepRecent为负数", func(t *testing.T) {
		// 边界/疑似 bug：keepRecent 负数。len(messages)<=keepRecent 永远为假，
		// 进入摘要流程；oldMessages 长度 = len - keepRecent > len，切片越界风险。
		// 这里探测是否 panic 或行为异常。
		m := New()
		m.AddUserMessage("a")
		m.AddAssistantMessage("b")
		prov := &mockProvider{
			completeFn: func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
				return &llm.CompletionResponse{Content: "摘要"}, nil
			},
		}
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("keepRecent 为负数导致 panic（未做防御）: %v", r)
			}
		}()
		err := m.Summarize(context.Background(), prov, -1)
		// 若未 panic，记录其行为是否合理
		if err != nil {
			t.Logf("keepRecent=-1 返回 err = %v", err)
		}
		t.Logf("keepRecent=-1 后 MessageCount = %d", m.MessageCount())
	})
}

// ============== 并发竞态（-race 下运行） ==============

func TestConcurrentAccess(t *testing.T) {
	m := New(WithMaxMessages(100))
	var wg sync.WaitGroup
	// 并发写
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				m.AddUserMessage("u")
				m.AddAssistantMessage("a")
			}
		}()
	}
	// 并发读
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = m.GetMessages()
				_ = m.TurnCount()
				_ = m.MessageCount()
				_ = m.History()
				_ = m.GetContext(100)
			}
		}()
	}
	// 并发清空
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 20; j++ {
			m.Clear()
		}
	}()
	wg.Wait()
	// 仅验证不 panic / 不 data race；最终状态受 maxMessages 限制
	if m.MessageCount() > 100 {
		t.Errorf("并发后 MessageCount = %d 超过 maxMessages=100", m.MessageCount())
	}
}

func TestConcurrentSummarize(t *testing.T) {
	// 并发 Summarize + 写入，验证锁外 LLM 调用不破坏状态一致性
	m := New()
	for i := 0; i < 10; i++ {
		m.AddUserMessage("x")
	}
	prov := &mockProvider{
		completeFn: func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
			return &llm.CompletionResponse{Content: "摘要"}, nil
		},
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Summarize(context.Background(), prov, 3)
		}()
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.AddUserMessage("y")
			_ = m.History()
		}()
	}
	wg.Wait()
	// 不 panic 即视为通过该项（race 由 -race 检测）
	_ = m.MessageCount()
}
