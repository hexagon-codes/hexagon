package otel

// 本文件为 observe/otel 包的全场景审计测试（wave8）。
//
// 覆盖目标：
//   - GenAI 属性构造的边界（空/0/负/Unicode/超长）与基数分流不变量
//   - ApplyGenAI 写入 span 的属性与 token 用量一致性
//   - Langfuse 导出器配置校验（成对认证、空端点、Header 注入）
//   - OTelHexagonTracer / OTelHexagonSpan 的生命周期、并发、关闭后行为
//   - 各 Start*Span 便捷函数的 span 类型正确性
//   - 各 Tracing*Hook 的状态机闭环（start→end / start→error）与漏配清理
//   - noopSpan 的全方法安全空实现
//
// 注：本文件只测试公开行为，不修改源码。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/hexagon-codes/hexagon/hooks"
	"github.com/hexagon-codes/hexagon/observe/tracer"
)

// ============== GenAI 属性边界 ==============

// TestNewGenAIAttributes_NegativeTokensOmitted 验证负数 token / max_tokens 被过滤。
// 源码用 `> 0` 判断，因此负值与 0 一样被丢弃，这是合理的健壮性处理。
func TestNewGenAIAttributes_NegativeTokensOmitted(t *testing.T) {
	a := NewGenAIAttributes(GenAICall{
		System:       "openai",
		RequestModel: "gpt-4o",
		MaxTokens:    -1,
		InputTokens:  -100,
		OutputTokens: -50,
		TotalTokens:  -150,
	})
	if len(a.High) != 0 {
		t.Errorf("负数 token/max_tokens 应全部被丢弃，High 应为空；got %+v", a.High)
	}
	// 低基数仍应正常包含 system / operation / request.model
	if a.Low[AttrGenAISystem] != "openai" {
		t.Errorf("system 仍应在 Low；got %v", a.Low[AttrGenAISystem])
	}
}

// TestNewGenAIAttributes_EmptyCall 验证完全空的调用：仅有默认 operation。
func TestNewGenAIAttributes_EmptyCall(t *testing.T) {
	a := NewGenAIAttributes(GenAICall{})
	// operation 默认 chat
	if a.Low[AttrGenAIOperationName] != OperationChat {
		t.Errorf("空调用 operation 应默认 %q；got %v", OperationChat, a.Low[AttrGenAIOperationName])
	}
	// Low 只应有 operation 一项
	if len(a.Low) != 1 {
		t.Errorf("空调用 Low 应仅含 operation 一项；got %+v", a.Low)
	}
	if len(a.High) != 0 {
		t.Errorf("空调用 High 应为空；got %+v", a.High)
	}
	// All() 应等于 Low（High 为空）
	all := a.All()
	if len(all) != 1 || all[AttrGenAIOperationName] != OperationChat {
		t.Errorf("空调用 All() 应仅含 operation；got %+v", all)
	}
}

// TestNewGenAIAttributes_CustomOperation 验证显式 operation 不被默认值覆盖。
func TestNewGenAIAttributes_CustomOperation(t *testing.T) {
	a := NewGenAIAttributes(GenAICall{Operation: "text_completion"})
	if a.Low[AttrGenAIOperationName] != "text_completion" {
		t.Errorf("自定义 operation 应保留；got %v", a.Low[AttrGenAIOperationName])
	}
}

// TestNewGenAIAttributes_UnicodeAndLong 验证 Unicode 与超长字符串原样透传（不截断、不转义）。
func TestNewGenAIAttributes_UnicodeAndLong(t *testing.T) {
	longModel := strings.Repeat("模型-A", 5000) // 超长 Unicode
	a := NewGenAIAttributes(GenAICall{
		System:        "通义千问",
		RequestModel:  longModel,
		ResponseModel: "🤖-v2",
	})
	if a.Low[AttrGenAISystem] != "通义千问" {
		t.Errorf("Unicode system 应原样保留；got %v", a.Low[AttrGenAISystem])
	}
	if a.Low[AttrGenAIRequestModel] != longModel {
		t.Error("超长 request.model 应原样保留，不截断")
	}
	if a.High[AttrGenAIResponseModel] != "🤖-v2" {
		t.Errorf("emoji response.model 应在 High；got %v", a.High[AttrGenAIResponseModel])
	}
}

// TestGenAIAttributes_AllIsCopy 验证 All() 返回的是合并后的新 map，
// 修改返回值不应污染原始 Low/High（状态一致性）。
func TestGenAIAttributes_AllIsCopy(t *testing.T) {
	a := NewGenAIAttributes(GenAICall{System: "openai", InputTokens: 10})
	all := a.All()
	all[AttrGenAISystem] = "tampered"
	all["injected"] = true
	if a.Low[AttrGenAISystem] != "openai" {
		t.Error("修改 All() 返回值污染了原始 Low；All() 应返回独立副本")
	}
	if _, ok := a.Low["injected"]; ok {
		t.Error("向 All() 注入的键泄漏回了 Low")
	}
}

// TestNewGenAIAttributes_NilSafety 验证 NewGenAIAttributes 永不返回 nil map。
func TestNewGenAIAttributes_NilSafety(t *testing.T) {
	a := NewGenAIAttributes(GenAICall{})
	if a.Low == nil {
		t.Error("Low 不应为 nil")
	}
	if a.High == nil {
		t.Error("High 不应为 nil（空调用也应是非 nil 空 map）")
	}
}

// ============== ApplyGenAI ==============

// TestApplyGenAI_NoTokensSkipsUsage 验证无 token 时不调用 SetTokenUsage（不写入 0 用量）。
func TestApplyGenAI_NoTokensSkipsUsage(t *testing.T) {
	mt := tracer.NewMemoryTracer()
	_, span := mt.StartSpan(context.Background(), "llm-call")
	ApplyGenAI(span, GenAICall{System: "openai", RequestModel: "gpt-4o"})
	span.End()

	attrs := mt.Spans()[0].Attributes()
	// 无 token：不应出现 llm.total_tokens（SetTokenUsage 才会写）
	if _, ok := attrs[tracer.AttrLLMTotalTokens]; ok {
		t.Error("无 token 时不应写入 llm.total_tokens（避免误报 0 用量）")
	}
	if attrs[AttrGenAISystem] != "openai" {
		t.Error("gen_ai.system 仍应写入 span")
	}
}

// TestApplyGenAI_ReturnsSplitAttributes 验证返回值就是分流后的属性集合（供 metrics 复用）。
func TestApplyGenAI_ReturnsSplitAttributes(t *testing.T) {
	mt := tracer.NewMemoryTracer()
	_, span := mt.StartSpan(context.Background(), "llm")
	got := ApplyGenAI(span, GenAICall{System: "openai", InputTokens: 7, TotalTokens: 7})
	span.End()

	if got.Low[AttrGenAISystem] != "openai" {
		t.Error("返回的 Low 应含 system")
	}
	if got.High[AttrGenAIUsageInputTokens] != 7 {
		t.Error("返回的 High 应含 input_tokens")
	}
}

// TestApplyGenAI_OnlyPartialTokens 验证仅有 InputTokens（无 total）时也写入 token 用量。
func TestApplyGenAI_OnlyPartialTokens(t *testing.T) {
	mt := tracer.NewMemoryTracer()
	_, span := mt.StartSpan(context.Background(), "llm")
	ApplyGenAI(span, GenAICall{InputTokens: 42})
	span.End()

	attrs := mt.Spans()[0].Attributes()
	// SetTokenUsage 被触发，prompt_tokens 应为 42
	if attrs[tracer.AttrLLMPromptTokens] != 42 {
		t.Errorf("llm.prompt_tokens 应为 42；got %v", attrs[tracer.AttrLLMPromptTokens])
	}
}

// ============== Langfuse 导出器 ==============

// TestNewLangfuseExporter_NoAuthIsValid 验证不提供任何密钥（自托管无认证）是合法配置。
func TestNewLangfuseExporter_NoAuthIsValid(t *testing.T) {
	exp, err := NewLangfuseExporter(LangfuseConfig{Endpoint: "http://localhost:3000/otel"})
	if err != nil {
		t.Fatalf("无认证应合法；got err=%v", err)
	}
	if exp == nil {
		t.Error("应返回非空导出器")
	}
}

// TestNewLangfuseExporter_CustomHeadersPreserved 验证额外 Headers 被保留（与 Auth 头共存）。
// 由于 Headers 注入 OTLPExporter 内部不可直接读取，这里至少验证不报错且构造成功。
func TestNewLangfuseExporter_CustomHeadersPreserved(t *testing.T) {
	exp, err := NewLangfuseExporter(LangfuseConfig{
		Endpoint:  "https://x/otel",
		PublicKey: "pk",
		SecretKey: "sk",
		Headers:   map[string]string{"X-Custom": "v"},
	})
	if err != nil {
		t.Fatalf("合法配置应成功；got %v", err)
	}
	if exp == nil {
		t.Error("应返回非空导出器")
	}
}

// TestNewLangfuseExporter_EmptyEndpointWithAuth 验证空端点即便带认证也报错（端点优先校验）。
func TestNewLangfuseExporter_EmptyEndpointWithAuth(t *testing.T) {
	if _, err := NewLangfuseExporter(LangfuseConfig{PublicKey: "pk", SecretKey: "sk"}); err == nil {
		t.Error("空端点应报错")
	}
}

// ============== Start*Span 便捷函数 ==============

// TestStartSpans_SpanKindsCorrect 验证各便捷函数为 span 打上正确的 hexagon.span.kind。
// 用 OTelHexagonTracer 才能拿到 kind 属性（MemoryTracer 不写 hexagon.span.kind）。
func TestStartSpans_SpanKindsCorrect(t *testing.T) {
	tr := NewOTelHexagonTracer(WithTracerServiceName("test"))
	defer func() { _ = tr.Shutdown(context.Background()) }()
	ctx := context.Background()

	cases := []struct {
		name     string
		start    func(context.Context) (context.Context, tracer.Span)
		wantKind string
	}{
		{"agent", func(c context.Context) (context.Context, tracer.Span) { return StartAgentSpan(c, tr, "a") }, "agent"},
		{"llm", func(c context.Context) (context.Context, tracer.Span) { return StartLLMSpan(c, tr, "l") }, "llm"},
		{"tool", func(c context.Context) (context.Context, tracer.Span) { return StartToolSpan(c, tr, "t") }, "tool"},
		{"retriever", func(c context.Context) (context.Context, tracer.Span) { return StartRetrieverSpan(c, tr, "r") }, "retrieval"},
		{"embedding", func(c context.Context) (context.Context, tracer.Span) { return StartEmbeddingSpan(c, tr, "e") }, "embedding"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, span := tc.start(ctx)
			os, ok := span.(*OTelHexagonSpan)
			if !ok {
				t.Fatalf("应返回 *OTelHexagonSpan；got %T", span)
			}
			if got := os.Attributes()["hexagon.span.kind"]; got != tc.wantKind {
				t.Errorf("span kind 应为 %q；got %v", tc.wantKind, got)
			}
			span.End()
		})
	}
}

// 回归: storeSpan 对同一 traceID 存第二个 span 不再 panic（CAS 不可比较切片）
//
// 历史 BUG：OTelHexagonTracer.storeSpan 用 sync.Map.CompareAndSwap 比较
// []*OTelHexagonSpan 切片值。Go 中切片不可比较，CompareAndSwap 内部用 == 比较旧值时
// panic("comparing uncomparable type")。该路径在「同一 traceID 下存第二个 span」时触发，
// 即任何 父→子 span 树（最常见的 Agent→LLM 场景）都会崩溃。
//
// 修复后：storeSpan 改为锁保护的读改写，不再依赖 CAS。本测试断言「不 panic」且
// 两个 span 都被正确收集到同一 traceID 下，永久锁定该不变量。
func TestBugStoreSpanPanicsOnSecondSpanSameTrace(t *testing.T) {
	tr := NewOTelHexagonTracer(WithTracerServiceName("test"))
	defer func() { _ = tr.Shutdown(context.Background()) }()

	// 固定 traceID，使第二次 StartSpan 走 storeSpan 的「已存在」分支。
	ctx := tr.InjectTraceID(context.Background(), "same-trace")

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, s1 := tr.StartSpan(ctx, "first")  // 首个 span，初始化切片
		_, s2 := tr.StartSpan(ctx, "second") // 第二个 span，追加到已存在切片
		_ = s1
		_ = s2
	}()

	if recovered != nil {
		t.Fatalf("同一 traceID 存第二个 span 不应 panic；got %v", recovered)
	}
	// 两个 span 都应被收集到同一 traceID 下
	spans := tr.GetSpans("same-trace")
	if len(spans) != 2 {
		t.Errorf("同一 traceID 下应收集到 2 个 span；got %d", len(spans))
	}
}

// ============== OTelHexagonTracer 生命周期 ==============

// TestOTelHexagonTracer_StartSpanBasics 验证基本 span 字段与 context 注入。
func TestOTelHexagonTracer_StartSpanBasics(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()

	ctx, span := tr.StartSpan(context.Background(), "root")
	if span.SpanID() == "" || span.TraceID() == "" {
		t.Error("span 应有非空 SpanID 与 TraceID")
	}
	if !span.IsRecording() {
		t.Error("新 span 应处于 recording 状态")
	}
	// context 中应能提取到 traceID 与当前 span
	if tr.ExtractTraceID(ctx) != span.TraceID() {
		t.Error("ExtractTraceID 应返回 span 的 traceID")
	}
	if tracer.SpanFromContext(ctx) == nil {
		t.Error("StartSpan 应把 span 注入 context")
	}
}

// TestOTelHexagonTracer_ChildSharesTraceID 验证父子 span 共享 traceID。
//
// 回归: storeSpan 修复后，创建同 trace 的子 span 不再 panic，可直接断言父子关系。
func TestOTelHexagonTracer_ChildSharesTraceID(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()

	ctx, parent := tr.StartSpan(context.Background(), "parent")

	_, child := tr.StartSpan(ctx, "child")
	// 断言父子共享 traceID 且子有独立 SpanID
	if child.TraceID() != parent.TraceID() {
		t.Errorf("子 span 应继承父 traceID；parent=%q child=%q", parent.TraceID(), child.TraceID())
	}
	if child.SpanID() == parent.SpanID() {
		t.Error("子 span 应有独立的 SpanID")
	}
}

// TestOTelHexagonTracer_ShutdownIdempotent 验证 Shutdown 幂等且关闭后 StartSpan 返回 noopSpan。
func TestOTelHexagonTracer_ShutdownIdempotent(t *testing.T) {
	tr := NewOTelHexagonTracer()
	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("首次 Shutdown 不应报错；got %v", err)
	}
	if err := tr.Shutdown(context.Background()); err != nil {
		t.Fatalf("重复 Shutdown 应幂等返回 nil；got %v", err)
	}
	// 关闭后 StartSpan 返回不可记录的 noopSpan
	_, span := tr.StartSpan(context.Background(), "after-close")
	if span.IsRecording() {
		t.Error("关闭后 StartSpan 应返回 noopSpan（IsRecording=false）")
	}
	if span.SpanID() != "" {
		t.Error("noopSpan 的 SpanID 应为空")
	}
}

// TestOTelHexagonTracer_GetSpansMissingTrace 验证查询不存在的 traceID 返回 nil。
func TestOTelHexagonTracer_GetSpansMissingTrace(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	if got := tr.GetSpans("nonexistent"); got != nil {
		t.Errorf("不存在的 traceID 应返回 nil；got %v", got)
	}
}

// TestOTelHexagonTracer_ConcurrentDistinctTraces 验证并发 StartSpan（各自独立 traceID）安全。
//
// 注意：故意让每个 goroutine 用「不同」traceID，从而只走 storeSpan 的 LoadOrStore 分支，
// 避开会 panic 的 CompareAndSwap 分支（goroutine 内 panic 无法被外层 recover，会崩溃整个测试）。
// 这样可在不触发 BUG 的前提下验证 sync.Map 并发写入与计数一致性。
func TestOTelHexagonTracer_ConcurrentDistinctTraces(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()

	const n = 200
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			ctx := tr.InjectTraceID(context.Background(), fmt.Sprintf("trace-%d", i))
			_, span := tr.StartSpan(ctx, fmt.Sprintf("span-%d", i))
			span.End()
		}(i)
	}
	wg.Wait()

	// 每个独立 trace 应恰好 1 个 span，无丢失
	missing := 0
	for i := 0; i < n; i++ {
		if got := tr.GetSpans(fmt.Sprintf("trace-%d", i)); len(got) != 1 {
			missing++
		}
	}
	if missing != 0 {
		t.Errorf("并发独立 trace 写入应每个恰 1 span；缺失/异常 %d 个", missing)
	}
}

// ============== OTelHexagonSpan 行为 ==============

// TestOTelHexagonSpan_RecordErrorSetsStatus 验证 RecordError 写入错误属性并置 error 状态。
func TestOTelHexagonSpan_RecordErrorSetsStatus(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	_, span := tr.StartSpan(context.Background(), "s")
	os := span.(*OTelHexagonSpan)

	span.RecordError(errors.New("boom"))
	attrs := os.Attributes()
	if attrs[tracer.AttrErrorMessage] != "boom" {
		t.Errorf("error.message 应为 boom；got %v", attrs[tracer.AttrErrorMessage])
	}
	if attrs[tracer.AttrErrorType] != "*errors.errorString" {
		t.Errorf("error.type 应为具体类型；got %v", attrs[tracer.AttrErrorType])
	}
}

// TestOTelHexagonSpan_RecordNilErrorNoop 验证 RecordError(nil) 不写任何错误属性。
func TestOTelHexagonSpan_RecordNilErrorNoop(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	_, span := tr.StartSpan(context.Background(), "s")
	os := span.(*OTelHexagonSpan)

	span.RecordError(nil)
	if _, ok := os.Attributes()[tracer.AttrErrorMessage]; ok {
		t.Error("RecordError(nil) 不应写入 error.message")
	}
}

// TestOTelHexagonSpan_EndIdempotent 验证重复 End 不改变已结束状态（recording 仅翻转一次）。
func TestOTelHexagonSpan_EndIdempotent(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	_, span := tr.StartSpan(context.Background(), "s")
	os := span.(*OTelHexagonSpan)

	span.End()
	d1 := os.Duration()
	if span.IsRecording() {
		t.Error("End 后不应再 recording")
	}
	span.End() // 第二次 End 应是 no-op（recording 已 false，不更新 endTime）
	d2 := os.Duration()
	if d1 != d2 {
		t.Errorf("重复 End 应固定 endTime，Duration 不应变化；d1=%v d2=%v", d1, d2)
	}
}

// TestOTelHexagonSpan_SetTokenUsageWritesAttrs 验证 SetTokenUsage 同步写入 llm.* token 属性。
func TestOTelHexagonSpan_SetTokenUsageWritesAttrs(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	_, span := tr.StartSpan(context.Background(), "s")
	os := span.(*OTelHexagonSpan)

	span.SetTokenUsage(tracer.TokenUsage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30})
	attrs := os.Attributes()
	if attrs[tracer.AttrLLMPromptTokens] != 10 || attrs[tracer.AttrLLMCompletionTokens] != 20 || attrs[tracer.AttrLLMTotalTokens] != 30 {
		t.Errorf("token 用量应同步写入 llm.* 属性；got %+v", attrs)
	}
}

// TestOTelHexagonSpan_AddEventOddArgs 验证 AddEvent 奇数参数时丢弃最后一个未配对的键，不 panic。
func TestOTelHexagonSpan_AddEventOddArgs(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	_, span := tr.StartSpan(context.Background(), "s")

	// 奇数个参数：("k1", "v1", "dangling") —— 不应 panic，"dangling" 被忽略
	span.AddEvent("evt", "k1", "v1", "dangling")
	// 空参数也不应 panic
	span.AddEvent("evt-empty")
}

// TestOTelHexagonSpan_AttributesIsCopy 验证 Attributes() 返回副本，外部修改不污染内部。
func TestOTelHexagonSpan_AttributesIsCopy(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	_, span := tr.StartSpan(context.Background(), "s")
	os := span.(*OTelHexagonSpan)

	span.SetAttribute("k", "v")
	got := os.Attributes()
	got["k"] = "tampered"
	got["new"] = 1
	fresh := os.Attributes()
	if fresh["k"] != "v" {
		t.Error("修改 Attributes() 返回值污染了内部状态；应返回副本")
	}
	if _, ok := fresh["new"]; ok {
		t.Error("注入键泄漏回内部属性")
	}
}

// TestOTelHexagonSpan_ConcurrentSetAttributes 验证并发写属性不触发 race（需 -race 才能完整暴露）。
func TestOTelHexagonSpan_ConcurrentSetAttributes(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	_, span := tr.StartSpan(context.Background(), "s")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			span.SetAttribute(fmt.Sprintf("k%d", i), i)
			span.AddEvent("e", "i", i)
			_ = span.(*OTelHexagonSpan).Attributes()
		}(i)
	}
	wg.Wait()
}

// ============== noopSpan ==============

// TestNoopSpan_AllMethodsSafe 验证关闭后返回的 noopSpan 所有方法都安全可调用且语义为空。
func TestNoopSpan_AllMethodsSafe(t *testing.T) {
	tr := NewOTelHexagonTracer()
	_ = tr.Shutdown(context.Background())
	_, span := tr.StartSpan(context.Background(), "x")

	// 调用全部方法不应 panic
	span.SetName("n")
	span.SetInput("in")
	span.SetOutput("out")
	span.SetTokenUsage(tracer.TokenUsage{TotalTokens: 1})
	span.SetAttribute("k", "v")
	span.SetAttributes(map[string]any{"k": "v"})
	span.AddEvent("e", "k", "v")
	span.RecordError(errors.New("e"))
	span.SetStatus(tracer.StatusCodeError, "msg")
	span.EndWithError(errors.New("e"))
	span.End()
	if span.SpanID() != "" || span.TraceID() != "" {
		t.Error("noopSpan 的 ID 应为空")
	}
	if span.IsRecording() {
		t.Error("noopSpan 不应 recording")
	}
}

// ============== Tracing Hooks 闭环 ==============

// TestTracingRunHook_StartEndClosesSpan 验证 Run 钩子 start→end 闭环并清理 span 映射。
func TestTracingRunHook_StartEndClosesSpan(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	h := NewTracingRunHook(tr)
	ctx := context.Background()

	if err := h.OnStart(ctx, &hooks.RunStartEvent{RunID: "r1", AgentID: "agent-x", Input: "hi"}); err != nil {
		t.Fatalf("OnStart 不应报错；got %v", err)
	}
	// 此时 span 应被记录且 recording
	spanI, ok := h.spans.Load("r1")
	if !ok {
		t.Fatal("OnStart 应把 span 存入映射")
	}
	if !spanI.(tracer.Span).IsRecording() {
		t.Error("OnStart 后 span 应 recording")
	}

	if err := h.OnEnd(ctx, &hooks.RunEndEvent{RunID: "r1", Output: "done"}); err != nil {
		t.Fatalf("OnEnd 不应报错；got %v", err)
	}
	// 闭环后映射应被删除
	if _, ok := h.spans.Load("r1"); ok {
		t.Error("OnEnd 后应从映射删除 span（避免泄漏）")
	}
	if spanI.(tracer.Span).IsRecording() {
		t.Error("OnEnd 后 span 应已结束")
	}
}

// 回归: Run 钩子不再把 AgentID 当作 agent.name 写入（丢失真实名称）
//
// 历史 BUG：OnStart 把 event.AgentID 同时写进 agent.id 和 agent.name。
// 由于 RunStartEvent 不携带 Agent 名称，写入的 agent.name 实为 ID 的复制品，
// 是误导性的「假名称」。修复后：agent.id 正常写入；agent.name 不再被写成 ID。
func TestTracingRunHook_DoesNotFakeAgentName(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	h := NewTracingRunHook(tr)

	if err := h.OnStart(context.Background(), &hooks.RunStartEvent{RunID: "rn", AgentID: "agent-uuid-123"}); err != nil {
		t.Fatalf("OnStart 不应报错；got %v", err)
	}
	spanI, _ := h.spans.Load("rn")
	attrs := spanI.(*OTelHexagonSpan).Attributes()

	// agent.id 应正常写入真实 ID
	if attrs[tracer.AttrAgentID] != "agent-uuid-123" {
		t.Errorf("agent.id 应为真实 AgentID；got %v", attrs[tracer.AttrAgentID])
	}
	// agent.name 不应被写成 AgentID 的复制品（无真实名称时应缺省，而非伪造）
	if name, ok := attrs[tracer.AttrAgentName]; ok && name == "agent-uuid-123" {
		t.Errorf("agent.name 不应等于 AgentID（丢失真实名称的伪造值）；got %v", name)
	}
}

// TestTracingRunHook_OnEndUnknownRunID 验证对未知 RunID 调 OnEnd 不 panic、安全 no-op。
func TestTracingRunHook_OnEndUnknownRunID(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	h := NewTracingRunHook(tr)
	if err := h.OnEnd(context.Background(), &hooks.RunEndEvent{RunID: "ghost"}); err != nil {
		t.Errorf("未知 RunID 的 OnEnd 应安全返回 nil；got %v", err)
	}
}

// TestTracingRunHook_OnErrorClosesSpanWithError 验证 start→error 路径以错误结束 span 并清理。
func TestTracingRunHook_OnErrorClosesSpanWithError(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	h := NewTracingRunHook(tr)
	ctx := context.Background()

	_ = h.OnStart(ctx, &hooks.RunStartEvent{RunID: "r2", AgentID: "a"})
	spanI, _ := h.spans.Load("r2")
	if err := h.OnError(ctx, &hooks.ErrorEvent{RunID: "r2", Error: errors.New("fail")}); err != nil {
		t.Fatalf("OnError 不应报错；got %v", err)
	}
	if _, ok := h.spans.Load("r2"); ok {
		t.Error("OnError 后应清理 span 映射")
	}
	os := spanI.(*OTelHexagonSpan)
	if os.Attributes()[tracer.AttrErrorMessage] != "fail" {
		t.Error("OnError 应记录错误到 span")
	}
}

// TestTracingRunHook_Metadata 验证钩子元信息（名称/启用/时机）。
func TestTracingRunHook_Metadata(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	h := NewTracingRunHook(tr)
	if h.Name() != "otel-tracing-run" {
		t.Errorf("钩子名应为 otel-tracing-run；got %q", h.Name())
	}
	if !h.Enabled() {
		t.Error("钩子默认应启用")
	}
	want := hooks.TimingRunStart | hooks.TimingRunEnd | hooks.TimingRunError
	if h.Timings() != want {
		t.Errorf("Timings 不匹配；got %v want %v", h.Timings(), want)
	}
}

// TestTracingToolHook_SuccessAndError 验证工具钩子的成功与错误两条结束路径都清理映射。
func TestTracingToolHook_SuccessAndError(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	h := NewTracingToolHook(tr)
	ctx := context.Background()

	// 成功路径
	_ = h.OnToolStart(ctx, &hooks.ToolStartEvent{ToolID: "t1", ToolName: "calc", Input: map[string]any{"a": 1}})
	if err := h.OnToolEnd(ctx, &hooks.ToolEndEvent{ToolID: "t1", Output: "ok"}); err != nil {
		t.Fatalf("OnToolEnd 不应报错；got %v", err)
	}
	if _, ok := h.spans.Load("t1"); ok {
		t.Error("成功结束后应清理映射")
	}

	// 错误路径
	_ = h.OnToolStart(ctx, &hooks.ToolStartEvent{ToolID: "t2", ToolName: "calc"})
	spanI, _ := h.spans.Load("t2")
	_ = h.OnToolEnd(ctx, &hooks.ToolEndEvent{ToolID: "t2", Error: errors.New("tool fail")})
	if _, ok := h.spans.Load("t2"); ok {
		t.Error("错误结束后也应清理映射")
	}
	if spanI.(*OTelHexagonSpan).Attributes()[tracer.AttrErrorMessage] != "tool fail" {
		t.Error("错误应记录到工具 span")
	}
}

// TestTracingLLMHook_EndWritesTokenUsage 验证 LLM 钩子结束时把 token 写入 span。
func TestTracingLLMHook_EndWritesTokenUsage(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	h := NewTracingLLMHook(tr)
	ctx := context.Background()

	_ = h.OnLLMStart(ctx, &hooks.LLMStartEvent{RequestID: "q1", Provider: "openai", Model: "gpt-4o"})
	spanI, _ := h.spans.Load("q1")
	_ = h.OnLLMEnd(ctx, &hooks.LLMEndEvent{RequestID: "q1", PromptTokens: 100, CompletionTokens: 40})

	os := spanI.(*OTelHexagonSpan)
	attrs := os.Attributes()
	// total = prompt + completion = 140
	if attrs[tracer.AttrLLMTotalTokens] != 140 {
		t.Errorf("LLM 结束应写入 total_tokens=140；got %v", attrs[tracer.AttrLLMTotalTokens])
	}
	if _, ok := h.spans.Load("q1"); ok {
		t.Error("OnLLMEnd 后应清理映射")
	}
}

// TestTracingLLMHook_StreamAddsEvents 验证流式 chunk 事件被加到 span 上且不删除映射（仍 recording）。
func TestTracingLLMHook_StreamAddsEvents(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	h := NewTracingLLMHook(tr)
	ctx := context.Background()

	_ = h.OnLLMStart(ctx, &hooks.LLMStartEvent{RequestID: "q2", Provider: "openai", Model: "gpt-4o"})
	// 流式事件不应清理映射
	if err := h.OnLLMStream(ctx, &hooks.LLMStreamEvent{RequestID: "q2", ChunkIndex: 0, Content: "he"}); err != nil {
		t.Fatalf("OnLLMStream 不应报错；got %v", err)
	}
	if _, ok := h.spans.Load("q2"); !ok {
		t.Error("流式中途不应删除 span 映射")
	}
	// 对未知 requestID 的 stream 应安全 no-op
	if err := h.OnLLMStream(ctx, &hooks.LLMStreamEvent{RequestID: "unknown"}); err != nil {
		t.Errorf("未知 requestID 的 stream 应安全；got %v", err)
	}
}

// TestTracingRetrieverHook_StartEnd 验证检索钩子记录 doc_count 并闭环。
func TestTracingRetrieverHook_StartEnd(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	h := NewTracingRetrieverHook(tr)
	ctx := context.Background()

	_ = h.OnRetrieverStart(ctx, &hooks.RetrieverStartEvent{QueryID: "qr1", Query: "向量检索", TopK: 5})
	spanI, _ := h.spans.Load("qr1")
	_ = h.OnRetrieverEnd(ctx, &hooks.RetrieverEndEvent{QueryID: "qr1", DocCount: 3, Documents: []any{"d1", "d2", "d3"}})

	os := spanI.(*OTelHexagonSpan)
	if os.Attributes()[tracer.AttrRetrievalDocCount] != 3 {
		t.Errorf("应记录 doc_count=3；got %v", os.Attributes()[tracer.AttrRetrievalDocCount])
	}
	if _, ok := h.spans.Load("qr1"); ok {
		t.Error("OnRetrieverEnd 后应清理映射")
	}
}

// TestTracingRetrieverHook_ErrorPath 验证检索错误路径以错误结束。
func TestTracingRetrieverHook_ErrorPath(t *testing.T) {
	tr := NewOTelHexagonTracer()
	defer func() { _ = tr.Shutdown(context.Background()) }()
	h := NewTracingRetrieverHook(tr)
	ctx := context.Background()

	_ = h.OnRetrieverStart(ctx, &hooks.RetrieverStartEvent{QueryID: "qr2", Query: "q"})
	spanI, _ := h.spans.Load("qr2")
	_ = h.OnRetrieverEnd(ctx, &hooks.RetrieverEndEvent{QueryID: "qr2", Error: errors.New("index down")})
	if spanI.(*OTelHexagonSpan).Attributes()[tracer.AttrErrorMessage] != "index down" {
		t.Error("检索错误应记录到 span")
	}
}

// ============== SetupTracing ==============

// TestSetupTracing_RegistersHooks 验证 SetupTracing 创建 tracer 并注册四类钩子。
func TestSetupTracing_RegistersHooks(t *testing.T) {
	mgr := hooks.NewManager()
	tr, err := SetupTracing(mgr, WithTracerServiceName("svc"))
	if err != nil {
		t.Fatalf("SetupTracing 不应报错；got %v", err)
	}
	if tr == nil {
		t.Fatal("应返回非空 tracer")
	}
	defer func() { _ = tr.Shutdown(context.Background()) }()

	// 通过 manager 触发一次完整 run 生命周期，验证注册的钩子确实生效（端到端）。
	ctx := context.Background()
	if err := mgr.TriggerRunStart(ctx, &hooks.RunStartEvent{RunID: "e2e", AgentID: "a", Input: "x"}); err != nil {
		t.Fatalf("TriggerRunStart 不应报错；got %v", err)
	}
	if err := mgr.TriggerRunEnd(ctx, &hooks.RunEndEvent{RunID: "e2e", Output: "y"}); err != nil {
		t.Fatalf("TriggerRunEnd 不应报错；got %v", err)
	}
	// 钩子在内部用自身 ctx 创建 span，落到 tracer 后应能查询到至少一个 span。
	// 钩子 OnStart 用传入 ctx（无既有 traceID）生成新 traceID，无法从外部 ctx 直接取，
	// 因此这里通过遍历内部 spans 映射间接确认：钩子注册成功且生命周期被触发。
	allTraces := 0
	tr.spans.Range(func(_, v any) bool {
		allTraces += len(v.([]*OTelHexagonSpan))
		return true
	})
	if allTraces == 0 {
		t.Error("SetupTracing 注册的钩子触发后应有 span 落到 tracer")
	}
}
