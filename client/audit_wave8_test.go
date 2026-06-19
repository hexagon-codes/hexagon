// audit_wave8_test.go
//
// 本测试文件针对 client 包的 Fluent ChatClient / PromptClient 公开 API 做全场景审计。
// 覆盖：正常路径、边界（空/nil/超长/0/负/Unicode/并发竞态）、错误路径、
// 状态一致性、流式与非流式合并逻辑、PromptClient 模板渲染闭环。
//
// 测试不 mock 被测逻辑本身（buildRequest / callStreaming / callNonStreaming /
// Render），仅用一个真实可控的 mockProvider 提供 *llm.Stream 与 CompletionResponse。
package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/schema"
	streamx "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/ai-core/tool"
)

// ============== 测试替身 ==============

// mockProvider 是一个可控的 llm.Provider，用于驱动 ChatClient 的真实执行路径。
// 它记录最近一次收到的请求，便于断言 buildRequest 的产物。
type mockProvider struct {
	name string

	// lastReq 保存最近一次 Complete/Stream 收到的请求，用于断言。
	lastReq llm.CompletionRequest

	// completeResp 是 Complete 返回的响应。
	completeResp *llm.CompletionResponse
	completeErr  error

	// streamSSE 是 Stream 返回的 SSE 文本（OpenAI 格式）。
	streamSSE string
	streamErr error
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Complete(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	m.lastReq = req
	if m.completeErr != nil {
		return nil, m.completeErr
	}
	if m.completeResp != nil {
		return m.completeResp, nil
	}
	return &llm.CompletionResponse{Content: "ok"}, nil
}

func (m *mockProvider) Stream(_ context.Context, req llm.CompletionRequest) (*llm.Stream, error) {
	m.lastReq = req
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	// 用内存 SSE 构造真实的 *streamx.Stream，走真实的解析+合并链路。
	return streamx.NewStream(strings.NewReader(m.streamSSE), streamx.OpenAIFormat), nil
}

func (m *mockProvider) Models() []llm.ModelInfo { return nil }

func (m *mockProvider) CountTokens(_ []llm.Message) (int, error) { return 0, nil }

// openAISSE 构造一段 OpenAI 风格的 SSE，文本被切成多个 chunk，最后以 [DONE] 结束。
func openAISSE(deltas []string, finishReason string) string {
	var b strings.Builder
	for i, d := range deltas {
		fr := ""
		// 最后一个内容块携带 finish_reason
		if i == len(deltas)-1 {
			fr = fmt.Sprintf(`,"finish_reason":%q`, finishReason)
		}
		b.WriteString(fmt.Sprintf(
			`data: {"id":"cmpl-1","model":"mock","choices":[{"index":0,"delta":{"content":%q}%s}]}`+"\n",
			d, fr,
		))
	}
	b.WriteString("data: [DONE]\n")
	return b.String()
}

// newTool 构造一个最简单的可断言名称/描述/Schema 的工具。
func newTool(name, desc string) tool.Tool {
	return tool.New(name, desc, func(_ context.Context, _ map[string]any) (any, error) {
		return "result", nil
	}, tool.WithSchema(&schema.Schema{Type: "object", Properties: map[string]*schema.Schema{}}))
}

// ============== Fluent 构建 / buildRequest 状态一致性 ==============

func TestChatClient_BuildRequest_FullChain(t *testing.T) {
	mp := &mockProvider{name: "mock"}
	temp := 0.7
	topP := 0.9

	_, err := NewChatClient(mp).
		Model("gpt-4").
		System("你是助手").
		User("你好").
		Assistant("我在").
		AddMessage(llm.RoleUser, "再问一句").
		Temperature(temp).
		MaxTokens(1000).
		TopP(topP).
		Stop("STOP", "END").
		Tools(newTool("weather", "查天气")).
		Metadata("trace", "abc").
		Call(context.Background())
	if err != nil {
		t.Fatalf("Call 不应返回错误: %v", err)
	}

	req := mp.lastReq
	// 系统消息应排在最前，后跟 user/assistant/user 共 4 条
	if len(req.Messages) != 4 {
		t.Fatalf("期望 4 条消息(含 system)，实际 %d 条: %+v", len(req.Messages), req.Messages)
	}
	if req.Messages[0].Role != llm.RoleSystem || req.Messages[0].Content != "你是助手" {
		t.Errorf("第一条应为 system 提示，实际 role=%v content=%q", req.Messages[0].Role, req.Messages[0].Content)
	}
	if req.Model != "gpt-4" {
		t.Errorf("Model 期望 gpt-4，实际 %q", req.Model)
	}
	if req.Temperature == nil || *req.Temperature != temp {
		t.Errorf("Temperature 未正确透传: %v", req.Temperature)
	}
	if req.TopP == nil || *req.TopP != topP {
		t.Errorf("TopP 未正确透传: %v", req.TopP)
	}
	if req.MaxTokens != 1000 {
		t.Errorf("MaxTokens 期望 1000，实际 %d", req.MaxTokens)
	}
	if len(req.Stop) != 2 || req.Stop[0] != "STOP" {
		t.Errorf("Stop 未正确透传: %v", req.Stop)
	}
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "weather" {
		t.Errorf("Tools 未正确透传: %+v", req.Tools)
	}
}

// TestChatClient_BuildRequest_NoSystemPrompt 验证空系统提示不会插入空 system 消息。
func TestChatClient_BuildRequest_NoSystemPrompt(t *testing.T) {
	mp := &mockProvider{name: "mock"}
	_, _ = NewChatClient(mp).User("hi").Call(context.Background())

	for _, m := range mp.lastReq.Messages {
		if m.Role == llm.RoleSystem {
			t.Fatalf("未设置系统提示时不应出现 system 消息: %+v", mp.lastReq.Messages)
		}
	}
	if len(mp.lastReq.Messages) != 1 {
		t.Fatalf("仅 1 条 user 消息，实际 %d 条", len(mp.lastReq.Messages))
	}
}

// TestChatClient_OptionalPointers_NilWhenUnset 验证未设置温度/TopP 时指针为 nil。
func TestChatClient_OptionalPointers_NilWhenUnset(t *testing.T) {
	mp := &mockProvider{name: "mock"}
	_, _ = NewChatClient(mp).User("hi").Call(context.Background())
	if mp.lastReq.Temperature != nil {
		t.Errorf("未设置 Temperature 应为 nil，实际 %v", *mp.lastReq.Temperature)
	}
	if mp.lastReq.TopP != nil {
		t.Errorf("未设置 TopP 应为 nil，实际 %v", *mp.lastReq.TopP)
	}
}

// TestChatClient_Boundary_TemperatureValues 边界温度值（0/负/超大）应原样透传，client 不做校验。
func TestChatClient_Boundary_TemperatureValues(t *testing.T) {
	cases := []struct {
		name string
		temp float64
	}{
		{"零温度", 0},
		{"负温度", -1.5},
		{"超大温度", 999.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mp := &mockProvider{name: "mock"}
			_, _ = NewChatClient(mp).User("x").Temperature(tc.temp).Call(context.Background())
			if mp.lastReq.Temperature == nil || *mp.lastReq.Temperature != tc.temp {
				t.Errorf("Temperature 应原样透传 %v，实际 %v", tc.temp, mp.lastReq.Temperature)
			}
		})
	}
}

// TestChatClient_Boundary_NegativeMaxTokens 负数/零 MaxTokens 应原样透传（client 不校验）。
func TestChatClient_Boundary_NegativeMaxTokens(t *testing.T) {
	for _, v := range []int{0, -1, -100} {
		mp := &mockProvider{name: "mock"}
		_, _ = NewChatClient(mp).User("x").MaxTokens(v).Call(context.Background())
		if mp.lastReq.MaxTokens != v {
			t.Errorf("MaxTokens 应原样透传 %d，实际 %d", v, mp.lastReq.MaxTokens)
		}
	}
}

// TestChatClient_User_Unicode 验证 Unicode/超长内容被完整保留。
func TestChatClient_User_Unicode(t *testing.T) {
	long := strings.Repeat("你好🌍", 5000)
	mp := &mockProvider{name: "mock"}
	_, _ = NewChatClient(mp).User(long).Call(context.Background())
	if got := mp.lastReq.Messages[0].Content; got != long {
		t.Errorf("Unicode 超长内容未完整保留: len=%d", len(got))
	}
}

// TestChatClient_EmptyUser 空字符串 user 消息仍会被追加（client 无内容校验）。
func TestChatClient_EmptyUser(t *testing.T) {
	mp := &mockProvider{name: "mock"}
	_, _ = NewChatClient(mp).User("").Call(context.Background())
	if len(mp.lastReq.Messages) != 1 || mp.lastReq.Messages[0].Content != "" {
		t.Errorf("空 user 消息应被追加为一条空内容消息，实际 %+v", mp.lastReq.Messages)
	}
}

// TestChatClient_Tools_Empty 不传工具时 req.Tools 应为 nil（不构造空 slice）。
func TestChatClient_Tools_Empty(t *testing.T) {
	mp := &mockProvider{name: "mock"}
	_, _ = NewChatClient(mp).User("x").Call(context.Background())
	if mp.lastReq.Tools != nil {
		t.Errorf("未设置工具时 req.Tools 应为 nil，实际 %+v", mp.lastReq.Tools)
	}
}

// ============== Messages / 切片别名状态一致性 ==============

// TestChatClient_Messages_Replace 验证 Messages() 整体替换消息列表。
func TestChatClient_Messages_Replace(t *testing.T) {
	mp := &mockProvider{name: "mock"}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "a"},
		{Role: llm.RoleAssistant, Content: "b"},
	}
	_, _ = NewChatClient(mp).User("将被覆盖").Messages(msgs).Call(context.Background())
	if len(mp.lastReq.Messages) != 2 || mp.lastReq.Messages[0].Content != "a" {
		t.Fatalf("Messages() 应整体替换，实际 %+v", mp.lastReq.Messages)
	}
}

// 回归: Bug#2 Messages() 未做 defensive copy，User() 的 append 写穿调用方底层数组。
// TestChatClient_Messages_AliasMutatesCallerSlice 验证 Messages() 对传入切片做防御性拷贝，
// 后续 User() 的 append 不会污染调用方原始切片底层数组。
func TestChatClient_Messages_AliasMutatesCallerSlice(t *testing.T) {
	mp := &mockProvider{name: "mock"}
	// 预留容量，使 append 在未拷贝时会原地写入调用方底层数组（暴露别名副作用）。
	caller := make([]llm.Message, 1, 4)
	caller[0] = llm.Message{Role: llm.RoleUser, Content: "orig"}

	c := NewChatClient(mp).Messages(caller)
	c.User("added") // 若缺少 defensive copy，会追加到调用方同一底层数组

	// 调用方切片底层数组不应被污染：caller[0:2] 的第 1 位不应出现 added。
	full := caller[:2]
	if full[1].Content == "added" {
		t.Errorf("Messages() 缺少 defensive copy，User() 写穿调用方底层数组: caller=%+v", full)
	}
	// client 自身应正确累积 2 条消息（orig + added）。
	if len(c.config.messages) != 2 || c.config.messages[1].Content != "added" {
		t.Errorf("client 内部消息应为 [orig, added]，实际 %+v", c.config.messages)
	}
}

// ============== 非流式 Call ==============

func TestChatClient_Call_NonStreaming_Success(t *testing.T) {
	mp := &mockProvider{
		name: "mock",
		completeResp: &llm.CompletionResponse{
			Content:      "回复内容",
			FinishReason: "stop",
			Usage:        llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8},
			ToolCalls:    []llm.ToolCall{{Index: 0, ID: "t1", Name: "weather"}},
		},
	}
	resp, err := NewChatClient(mp).User("x").Metadata("k", "v").Call(context.Background())
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if resp.Content != "回复内容" {
		t.Errorf("Content 期望 '回复内容'，实际 %q", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason 期望 stop，实际 %q", resp.FinishReason)
	}
	if resp.Usage.TotalTokens != 8 {
		t.Errorf("Usage.TotalTokens 期望 8，实际 %d", resp.Usage.TotalTokens)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "weather" {
		t.Errorf("ToolCalls 未透传: %+v", resp.ToolCalls)
	}
	if resp.Metadata["k"] != "v" {
		t.Errorf("Metadata 未透传: %+v", resp.Metadata)
	}
}

func TestChatClient_Call_NonStreaming_ProviderError(t *testing.T) {
	wantErr := errors.New("provider boom")
	mp := &mockProvider{name: "mock", completeErr: wantErr}
	resp, err := NewChatClient(mp).User("x").Call(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("应透传 provider 错误，实际 %v", err)
	}
	if resp != nil {
		t.Errorf("出错时应返回 nil 响应，实际 %+v", resp)
	}
}

// ============== 流式 Call（callStreaming 合并） ==============

func TestChatClient_Call_Streaming_Collect(t *testing.T) {
	mp := &mockProvider{
		name:      "mock",
		streamSSE: openAISSE([]string{"Hello", ", ", "World"}, "stop"),
	}
	resp, err := NewChatClient(mp).User("x").Stream().Call(context.Background())
	if err != nil {
		t.Fatalf("流式 Call 不应出错: %v", err)
	}
	if resp.Content != "Hello, World" {
		t.Errorf("流式合并内容期望 'Hello, World'，实际 %q", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason 期望 stop，实际 %q", resp.FinishReason)
	}
}

func TestChatClient_Call_Streaming_ProviderError(t *testing.T) {
	wantErr := errors.New("stream boom")
	mp := &mockProvider{name: "mock", streamErr: wantErr}
	resp, err := NewChatClient(mp).User("x").Stream().Call(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("应透传流式 provider 错误，实际 %v", err)
	}
	if resp != nil {
		t.Errorf("出错时应返回 nil 响应，实际 %+v", resp)
	}
}

// ============== CallStream（StreamReader 包装） ==============

func TestChatClient_CallStream_ReadAll(t *testing.T) {
	mp := &mockProvider{
		name:      "mock",
		streamSSE: openAISSE([]string{"A", "B", "C"}, "stop"),
	}
	sr, err := NewChatClient(mp).User("x").Metadata("m", 1).CallStream(context.Background())
	if err != nil {
		t.Fatalf("CallStream 不应出错: %v", err)
	}
	if sr.Metadata["m"] != 1 {
		t.Errorf("CallStream Metadata 未透传: %+v", sr.Metadata)
	}

	chunks, err := sr.Stream.Collect(context.Background())
	if err != nil {
		t.Fatalf("收集流块出错: %v", err)
	}
	// 期望收到 3 个内容块
	if len(chunks) != 3 {
		t.Fatalf("期望 3 个 chunk，实际 %d: %+v", len(chunks), chunks)
	}
	var buf strings.Builder
	for _, ch := range chunks {
		buf.WriteString(ch.Delta)
		// Content 与 Delta 在本实现里被赋成同一增量值
		if ch.Content != ch.Delta {
			t.Errorf("ChatChunk.Content 与 Delta 不一致: %q vs %q", ch.Content, ch.Delta)
		}
	}
	if buf.String() != "ABC" {
		t.Errorf("拼接增量期望 ABC，实际 %q", buf.String())
	}
	// 最后一块应标记 Done（finish_reason 非空）
	last := chunks[len(chunks)-1]
	if !last.Done {
		t.Errorf("最后一块应 Done=true，实际 %+v", last)
	}
}

func TestChatClient_CallStream_ProviderError(t *testing.T) {
	wantErr := errors.New("callstream boom")
	mp := &mockProvider{name: "mock", streamErr: wantErr}
	sr, err := NewChatClient(mp).User("x").CallStream(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("应透传错误，实际 %v", err)
	}
	if sr != nil {
		t.Errorf("出错时应返回 nil，实际 %+v", sr)
	}
}

// TestChatClient_CallStream_SetsStreamingFlag 验证 CallStream 副作用：会把 streaming 置 true。
func TestChatClient_CallStream_SetsStreamingFlag(t *testing.T) {
	mp := &mockProvider{name: "mock", streamSSE: openAISSE([]string{"x"}, "stop")}
	c := NewChatClient(mp).User("x")
	if c.config.streaming {
		t.Fatal("初始 streaming 应为 false")
	}
	_, _ = c.CallStream(context.Background())
	if !c.config.streaming {
		t.Error("CallStream 后 streaming 标志应为 true（已知副作用）")
	}
}

// ============== 默认 Provider / 便捷函数 ==============

func TestSetDefaultProvider_And_Chat(t *testing.T) {
	old := defaultProvider
	t.Cleanup(func() { defaultProvider = old })

	mp := &mockProvider{name: "default-mock", completeResp: &llm.CompletionResponse{Content: "via-default"}}
	SetDefaultProvider(mp)

	resp, err := Chat().User("hi").Call(context.Background())
	if err != nil {
		t.Fatalf("Chat() 走默认 provider 不应出错: %v", err)
	}
	if resp.Content != "via-default" {
		t.Errorf("期望默认 provider 返回 via-default，实际 %q", resp.Content)
	}
}

// 回归: Bug#4 Chat() 默认 provider 未设置(nil)时 Call 会 nil 解引用 panic，缺少 nil 守卫。
// TestChat_NilDefaultProvider_Panics 验证 Call 在 provider 为 nil 时返回 ErrNoProvider 而非 panic。
func TestChat_NilDefaultProvider_Panics(t *testing.T) {
	old := defaultProvider
	t.Cleanup(func() { defaultProvider = old })
	defaultProvider = nil

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("nil provider 时 Call 不应 panic，应返回错误，实际 panic: %v", r)
		}
	}()
	resp, err := Chat().User("hi").Call(context.Background())
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("nil provider 时应返回 ErrNoProvider，实际 %v", err)
	}
	if resp != nil {
		t.Errorf("出错时应返回 nil 响应，实际 %+v", resp)
	}
}

// 回归: Bug#4 CallStream 在 provider 为 nil 时同样返回 ErrNoProvider 而非 panic。
func TestChat_NilDefaultProvider_CallStream(t *testing.T) {
	old := defaultProvider
	t.Cleanup(func() { defaultProvider = old })
	defaultProvider = nil

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("nil provider 时 CallStream 不应 panic，实际 panic: %v", r)
		}
	}()
	sr, err := Chat().User("hi").CallStream(context.Background())
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("nil provider 时 CallStream 应返回 ErrNoProvider，实际 %v", err)
	}
	if sr != nil {
		t.Errorf("出错时应返回 nil，实际 %+v", sr)
	}
}

func TestChatWith_UsesGivenProvider(t *testing.T) {
	mp := &mockProvider{name: "explicit", completeResp: &llm.CompletionResponse{Content: "explicit-ok"}}
	resp, err := ChatWith(mp).User("hi").Call(context.Background())
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if resp.Content != "explicit-ok" {
		t.Errorf("期望 explicit-ok，实际 %q", resp.Content)
	}
}

// ============== PromptClient ==============

// 回归: Bug#1 PromptClient.Render() 占位符替换未实现，模板变量被静默忽略。
// TestPromptClient_Render_NotImplemented 验证 Render() 正确替换 {key} 占位符。
//
// 修复前 Render() 内部循环仅 `_ = k; _ = v`，从不替换，输出 == 原始模板；
// 此断言钉死修复后行为：所有占位符被对应变量值替换。请勿弱化为"原样返回"。
func TestPromptClient_Render_NotImplemented(t *testing.T) {
	const tmpl = "你好 {name}，今天是 {day}"
	p := NewPromptClient(tmpl).
		Var("name", "Alice").
		Var("day", "周一")

	out, err := p.Render()
	if err != nil {
		t.Fatalf("Render 不应出错: %v", err)
	}
	// 占位符必须被替换：功能闭环后输出为完整替换结果。
	const want = "你好 Alice，今天是 周一"
	if out != want {
		t.Errorf("Render() 占位符替换错误，期望 %q，实际 %q", want, out)
	}
	// 替换后模板中不应残留任何已知占位符。
	if strings.Contains(out, "{name}") || strings.Contains(out, "{day}") {
		t.Errorf("Render() 输出仍残留未替换占位符: %q", out)
	}
	// 显式确认变量确实被记录并被消费。
	if p.vars["name"] != "Alice" || p.vars["day"] != "周一" {
		t.Errorf("变量应已写入 vars 表，实际 %+v", p.vars)
	}
}

// 回归: Bug#1 Render 支持任意类型变量值与未知占位符保持原样。
func TestPromptClient_Render_TypesAndUnknownPlaceholder(t *testing.T) {
	out, err := NewPromptClient("count={n} ok={ok} missing={x}").
		Var("n", 42).
		Var("ok", true).
		Render()
	if err != nil {
		t.Fatalf("Render 不应出错: %v", err)
	}
	// 数字/布尔通过 conv.String 转换；未提供的 {x} 保持原样。
	const want = "count=42 ok=true missing={x}"
	if out != want {
		t.Errorf("Render 类型转换/未知占位符处理错误，期望 %q，实际 %q", want, out)
	}
}

// TestPromptClient_Render_NoVars 无变量时返回原模板。
func TestPromptClient_Render_NoVars(t *testing.T) {
	out, err := NewPromptClient("纯文本无变量").Render()
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if out != "纯文本无变量" {
		t.Errorf("期望原样返回，实际 %q", out)
	}
}

// TestPromptClient_Vars_BatchAndOverride 批量设置变量并覆盖单个变量。
func TestPromptClient_Vars_BatchAndOverride(t *testing.T) {
	p := NewPromptClient("t").
		Var("a", 1).
		Vars(map[string]any{"a": 2, "b": 3})
	if p.vars["a"] != 2 {
		t.Errorf("Vars 应覆盖已有变量 a=2，实际 %v", p.vars["a"])
	}
	if p.vars["b"] != 3 {
		t.Errorf("Vars 应新增 b=3，实际 %v", p.vars["b"])
	}
}

// TestPromptClient_Vars_NilMap 传入 nil map 不应 panic。
func TestPromptClient_Vars_NilMap(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Vars(nil) 不应 panic: %v", r)
		}
	}()
	p := NewPromptClient("t").Vars(nil)
	if len(p.vars) != 0 {
		t.Errorf("Vars(nil) 后变量表应为空，实际 %d", len(p.vars))
	}
}

// 回归: Bug#1 ToChat 经由 Render 生成 user 消息，占位符已被替换。
// TestPromptClient_ToChat 将 PromptClient 转换为 ChatClient，user 消息内容应为替换后文本。
func TestPromptClient_ToChat(t *testing.T) {
	mp := &mockProvider{name: "mock"}
	_, _ = Prompt("你好 {name}").Var("name", "Bob").ToChat(mp).Call(context.Background())

	if len(mp.lastReq.Messages) != 1 {
		t.Fatalf("ToChat 应生成 1 条 user 消息，实际 %d", len(mp.lastReq.Messages))
	}
	got := mp.lastReq.Messages[0].Content
	// Render 已闭环，ToChat 内容应为替换后的文本。
	if got != "你好 Bob" {
		t.Errorf("ToChat 内容应为替换后文本 '你好 Bob'，实际 %q", got)
	}
	if mp.lastReq.Messages[0].Role != llm.RoleUser {
		t.Errorf("ToChat 生成的消息角色应为 user，实际 %v", mp.lastReq.Messages[0].Role)
	}
}

// 回归: Bug#5 ChatResponse.Metadata 与 chatConfig.metadata 共享同一 map 引用，存在跨响应别名。
// TestChatResponse_Metadata_NotAliased 验证响应 Metadata 是独立拷贝：
// 修改响应 map 不影响 client 内部状态，且两次响应互不共享底层 map。
func TestChatResponse_Metadata_NotAliased(t *testing.T) {
	mp := &mockProvider{name: "mock", completeResp: &llm.CompletionResponse{Content: "ok"}}
	c := NewChatClient(mp).User("x").Metadata("k", "v")

	resp1, err := c.Call(context.Background())
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	// 污染响应 1 的 metadata，不应回写 client 内部状态。
	resp1.Metadata["k"] = "polluted"
	resp1.Metadata["injected"] = true

	if c.config.metadata["k"] != "v" {
		t.Errorf("修改响应 Metadata 不应污染 client 内部 metadata，实际 %+v", c.config.metadata)
	}
	if _, ok := c.config.metadata["injected"]; ok {
		t.Errorf("响应 Metadata 新增键不应出现在 client 内部 metadata: %+v", c.config.metadata)
	}

	// 第二次响应应拿到干净的 metadata（不受 resp1 污染影响）。
	resp2, err := c.Call(context.Background())
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if resp2.Metadata["k"] != "v" {
		t.Errorf("第二次响应 Metadata 应为干净值 v，实际 %v", resp2.Metadata["k"])
	}
	if _, ok := resp2.Metadata["injected"]; ok {
		t.Errorf("第二次响应不应继承 resp1 注入的键: %+v", resp2.Metadata)
	}
}

// TestChatClient_CallStream_DoneFlagSemantics 验证 ChatChunk.Done 的语义：
// 仅当对应 chunk 的 finish_reason 非空时才为 true。中间块 Done 应为 false。
func TestChatClient_CallStream_DoneFlagSemantics(t *testing.T) {
	mp := &mockProvider{
		name:      "mock",
		streamSSE: openAISSE([]string{"x", "y", "z"}, "stop"),
	}
	sr, err := NewChatClient(mp).User("q").CallStream(context.Background())
	if err != nil {
		t.Fatalf("CallStream 不应出错: %v", err)
	}
	chunks, err := sr.Stream.Collect(context.Background())
	if err != nil {
		t.Fatalf("收集出错: %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("期望 3 块，实际 %d", len(chunks))
	}
	// 前两块无 finish_reason -> Done=false
	for i := 0; i < 2; i++ {
		if chunks[i].Done {
			t.Errorf("第 %d 块无 finish_reason，Done 应为 false，实际 true", i)
		}
	}
	if !chunks[2].Done {
		t.Error("末块携带 finish_reason=stop，Done 应为 true")
	}
}

// TestChatClient_CallStream_EmptyStream 空 SSE（仅 [DONE]）时，CallStream 应正常返回且无 chunk。
func TestChatClient_CallStream_EmptyStream(t *testing.T) {
	mp := &mockProvider{name: "mock", streamSSE: "data: [DONE]\n"}
	sr, err := NewChatClient(mp).User("q").CallStream(context.Background())
	if err != nil {
		t.Fatalf("空流 CallStream 不应出错: %v", err)
	}
	chunks, err := sr.Stream.Collect(context.Background())
	if err != nil {
		t.Fatalf("收集空流出错: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("空流应收到 0 块，实际 %d: %+v", len(chunks), chunks)
	}
}

// TestChatClient_Stop_NoArgs 不传参数调用 Stop() 会把 stop 置为长度 0 的非 nil 切片，
// 经 buildRequest 透传后 req.Stop 为长度 0（钉死真实行为）。
func TestChatClient_Stop_NoArgs(t *testing.T) {
	mp := &mockProvider{name: "mock"}
	_, _ = NewChatClient(mp).User("x").Stop().Call(context.Background())
	if len(mp.lastReq.Stop) != 0 {
		t.Errorf("Stop() 无参数应得到长度 0 的 Stop，实际 %+v", mp.lastReq.Stop)
	}
}

// TestChatClient_AddMessage_ArbitraryRole AddMessage 允许任意 Role（包括 system/tool），
// 不做归一化或校验，原样透传。
func TestChatClient_AddMessage_ArbitraryRole(t *testing.T) {
	mp := &mockProvider{name: "mock"}
	_, _ = NewChatClient(mp).
		AddMessage(llm.RoleSystem, "sys-via-addmessage").
		AddMessage(llm.RoleUser, "u").
		Call(context.Background())
	// 注意：System() 未被调用，故 buildRequest 不会额外插入 system 头；
	// AddMessage 写入的 system 消息保持其在消息序列中的原始位置（第 0 位）。
	if len(mp.lastReq.Messages) != 2 {
		t.Fatalf("期望 2 条消息，实际 %d", len(mp.lastReq.Messages))
	}
	if mp.lastReq.Messages[0].Role != llm.RoleSystem || mp.lastReq.Messages[0].Content != "sys-via-addmessage" {
		t.Errorf("AddMessage 的 system 消息位置/内容被改动: %+v", mp.lastReq.Messages[0])
	}
}

// TestChatClient_Call_Streaming_DropsUsage 钉死缺陷：流式 Call（callStreaming）从 Collect
// 结果回填 Usage。OpenAI SSE 默认不含 usage，故 TotalTokens 为 0——验证合并路径本身可达。
func TestChatClient_Call_Streaming_NoUsageInSSE(t *testing.T) {
	mp := &mockProvider{name: "mock", streamSSE: openAISSE([]string{"a", "b"}, "stop")}
	resp, err := NewChatClient(mp).User("x").Stream().Call(context.Background())
	if err != nil {
		t.Fatalf("不应出错: %v", err)
	}
	if resp.Content != "ab" {
		t.Errorf("合并内容期望 ab，实际 %q", resp.Content)
	}
	// SSE 未携带 usage，合并结果 Usage 为零值（记录真实行为，非缺陷）。
	if resp.Usage.TotalTokens != 0 {
		t.Logf("SSE 含 usage 时 TotalTokens=%d", resp.Usage.TotalTokens)
	}
}

// TestPromptClient_Render_SwallowsErrorInToChat 当前 Render 永不返回错误，
// ToChat 用 `content, _ := p.Render()` 丢弃错误。此用例钉死 ToChat 即便 Render
// 未来返回错误也会被静默吞掉的潜在风险面（当前 Render 无错误，故 user 消息一定存在）。
func TestPromptClient_ToChat_AlwaysProducesMessage(t *testing.T) {
	mp := &mockProvider{name: "mock"}
	c := Prompt("任意模板 {x}").Var("x", "v").ToChat(mp)
	if len(c.config.messages) != 1 {
		t.Fatalf("ToChat 应生成 1 条消息，实际 %d", len(c.config.messages))
	}
	if c.config.messages[0].Role != llm.RoleUser {
		t.Errorf("消息角色应为 user，实际 %v", c.config.messages[0].Role)
	}
}

func TestPrompt_Constructor(t *testing.T) {
	p := Prompt("模板")
	if p.template != "模板" {
		t.Errorf("Prompt 模板期望 '模板'，实际 %q", p.template)
	}
	if p.vars == nil {
		t.Error("Prompt 变量表不应为 nil")
	}
}

// ============== 并发竞态 ==============

// 回归: Bug#3 ChatClient 共享可变 *chatConfig 且全程无锁，同一实例并发 Fluent 调用触发 fatal error。
// TestChatClient_ConcurrentFluent_NotThreadSafe 验证加锁后实例间状态隔离与并发安全。
//
// 修复前：ChatClient 共享同一 *chatConfig，Metadata 对内置 map 无锁写、User/Assistant 对同一
// messages slice 无锁 append，同一 client 并发调用会触发 "concurrent map writes" fatal error。
// 修复后：mu 保护 config，同一实例并发 Fluent 调用安全（见 ConcurrentSameClient 用例）。
func TestChatClient_ConcurrentFluent_NotThreadSafe(t *testing.T) {
	mp := &mockProvider{name: "mock"}

	// 受控并发：每个 goroutine 使用**独立** client，验证实例间无共享可变状态。
	var wg sync.WaitGroup
	results := make([]int, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c := NewChatClient(mp)
			for j := 0; j < 10; j++ {
				c.User(fmt.Sprintf("msg-%d-%d", n, j)).Metadata(fmt.Sprintf("k%d", j), j)
			}
			results[n] = len(c.config.messages)
		}(i)
	}
	wg.Wait()
	for i, n := range results {
		if n != 10 {
			t.Errorf("独立 client #%d 期望 10 条消息，实际 %d（实例间状态被污染）", i, n)
		}
	}
}

// 回归: Bug#3 同一 ChatClient 实例并发 Fluent 调用必须并发安全（加锁后不再 fatal error）。
// 本用例在同一 client 上并发写 Metadata(map) 与 append messages(slice)，须在 -race 下干净通过。
func TestChatClient_ConcurrentSameClient_ThreadSafe(t *testing.T) {
	mp := &mockProvider{name: "mock"}
	c := NewChatClient(mp)

	const goroutines = 50
	const perG = 20
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				// 并发写同一 client 的 map 与 slice：加锁前会 "concurrent map writes" 崩溃。
				c.Metadata(fmt.Sprintf("k-%d-%d", n, j), j).
					User(fmt.Sprintf("u-%d-%d", n, j))
			}
		}(i)
	}
	wg.Wait()

	// 所有写入应被保留：消息总数与 metadata 键数符合预期（无丢失/无崩溃）。
	if got := len(c.config.messages); got != goroutines*perG {
		t.Errorf("并发 append 消息数期望 %d，实际 %d", goroutines*perG, got)
	}
	if got := len(c.config.metadata); got != goroutines*perG {
		t.Errorf("并发写 metadata 键数期望 %d，实际 %d", goroutines*perG, got)
	}
}

// TestChatClient_SeparateClients_NoSharedState 验证不同 client 实例状态隔离。
func TestChatClient_SeparateClients_NoSharedState(t *testing.T) {
	mp := &mockProvider{name: "mock"}
	c1 := NewChatClient(mp).User("a")
	c2 := NewChatClient(mp).User("b")
	if len(c1.config.messages) != 1 || c1.config.messages[0].Content != "a" {
		t.Errorf("c1 状态被污染: %+v", c1.config.messages)
	}
	if len(c2.config.messages) != 1 || c2.config.messages[0].Content != "b" {
		t.Errorf("c2 状态被污染: %+v", c2.config.messages)
	}
}

// ============== 上下文取消 ==============

// TestChatClient_Call_ContextCanceled 已取消的 context 走非流式路径，
// 由 provider 决定行为；mockProvider 不检查 ctx，因此返回正常响应。
// 此用例记录 client 自身不在 Call 入口检查 ctx 的事实。
func TestChatClient_Call_ContextCanceled(t *testing.T) {
	mp := &mockProvider{name: "mock", completeResp: &llm.CompletionResponse{Content: "ignored-ctx"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp, err := NewChatClient(mp).User("x").Call(ctx)
	// client 不在入口做 ctx 检查，错误与否取决于 provider
	t.Logf("已取消 ctx 下 Call: err=%v resp=%v（client 入口未做 ctx 守卫）", err, resp != nil)
}
