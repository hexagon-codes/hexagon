package replay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============== 测试辅助 ==============

// failingStorage 是一个总是返回错误的存储实现，用于测试错误路径。
type failingStorage struct {
	saveErr error
	loadErr error
}

func (s *failingStorage) Save(ctx context.Context, trace *Trace) error {
	return s.saveErr
}

func (s *failingStorage) Load(ctx context.Context, id string) (*Trace, error) {
	return nil, s.loadErr
}

func (s *failingStorage) List(ctx context.Context, filter TraceFilter) ([]*Trace, error) {
	return nil, nil
}

func (s *failingStorage) Delete(ctx context.Context, id string) error {
	return nil
}

// echoExecutor 原样返回 span 的预期输出，用于模拟"重放结果与原始一致"。
type echoExecutor struct{}

func (echoExecutor) Execute(ctx context.Context, span *Span) (any, error) {
	return span.Output, nil
}

// ============== generateID 唯一性 / 格式 ==============

// TestGenerateID_Format 验证 ID 前缀格式以及单调递增计数器。
func TestGenerateID_Format(t *testing.T) {
	id := generateID("trace")
	if !strings.HasPrefix(id, "trace_") {
		t.Fatalf("ID 应以 trace_ 开头, 实际: %s", id)
	}
	// 形如 trace_<nanoid>_<counter>，至少 3 段
	parts := strings.Split(id, "_")
	if len(parts) < 3 {
		t.Fatalf("ID 应包含至少 3 段 (prefix_nanoid_counter), 实际: %s", id)
	}
}

// TestGenerateID_UniqueConcurrent 并发生成 ID 不应重复（计数器受锁保护）。
func TestGenerateID_UniqueConcurrent(t *testing.T) {
	const n = 2000
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[string]struct{}, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			id := generateID("span")
			mu.Lock()
			seen[id] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != n {
		t.Fatalf("并发生成 %d 个 ID 出现重复, 唯一数: %d", n, len(seen))
	}
}

// ============== Recorder 录制基本流程 ==============

// TestRecorder_FullTraceLifecycle 验证完整的 trace+span 录制生命周期与状态一致性。
func TestRecorder_FullTraceLifecycle(t *testing.T) {
	storage := NewMemoryStorage()
	rec := NewRecorder(storage)

	traceID := rec.StartTrace("test-trace", map[string]any{"q": "hello"})
	if traceID == "" {
		t.Fatal("StartTrace 应返回非空 trace ID")
	}

	cur := rec.GetCurrentTrace()
	if cur == nil || cur.Status != StatusRunning {
		t.Fatalf("StartTrace 后状态应为 running, 实际: %+v", cur)
	}

	spanID := rec.StartSpan("llm-call", SpanTypeLLM, map[string]any{"prompt": "hi"})
	if spanID == "" {
		t.Fatal("StartSpan 应返回非空 span ID")
	}
	rec.AddAttribute("model", "gpt-4")
	rec.AddEvent("token", map[string]any{"n": 1})
	rec.EndSpan(map[string]any{"text": "world"}, nil)

	rec.EndTrace(map[string]any{"answer": "world"}, nil)

	// EndTrace 后 currentTrace 应被清空
	if rec.GetCurrentTrace() != nil {
		t.Fatal("EndTrace 后 currentTrace 应为 nil")
	}

	// 验证已持久化
	loaded, err := storage.Load(context.Background(), traceID)
	if err != nil {
		t.Fatalf("加载已录制 trace 失败: %v", err)
	}
	if loaded.Status != StatusCompleted {
		t.Errorf("trace 状态应为 completed, 实际: %s", loaded.Status)
	}
	if len(loaded.Spans) != 1 {
		t.Fatalf("应录制 1 个 span, 实际: %d", len(loaded.Spans))
	}
	sp := loaded.Spans[0]
	if sp.Status != SpanStatusOK {
		t.Errorf("span 状态应为 ok, 实际: %s", sp.Status)
	}
	if sp.Type != SpanTypeLLM {
		t.Errorf("span 类型应为 llm, 实际: %s", sp.Type)
	}
	if sp.Attributes["model"] != "gpt-4" {
		t.Errorf("span 属性 model 应为 gpt-4, 实际: %v", sp.Attributes["model"])
	}
	if len(sp.Events) != 1 || sp.Events[0].Name != "token" {
		t.Errorf("span 应包含 1 个 token 事件, 实际: %+v", sp.Events)
	}
	if loaded.Duration <= 0 {
		t.Errorf("trace Duration 应 > 0, 实际: %v", loaded.Duration)
	}
}

// TestRecorder_ErrorTrace 验证带错误结束 trace/span 时的状态与错误信息记录。
func TestRecorder_ErrorTrace(t *testing.T) {
	storage := NewMemoryStorage()
	rec := NewRecorder(storage)

	traceID := rec.StartTrace("err-trace", nil)
	rec.StartSpan("tool", SpanTypeTool, nil)
	rec.EndSpan(nil, errors.New("tool boom"))
	rec.EndTrace(nil, errors.New("trace boom"))

	loaded, err := storage.Load(context.Background(), traceID)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if loaded.Status != StatusFailed {
		t.Errorf("trace 状态应为 failed, 实际: %s", loaded.Status)
	}
	if loaded.Error != "trace boom" {
		t.Errorf("trace 错误应为 'trace boom', 实际: %s", loaded.Error)
	}
	if loaded.Spans[0].Status != SpanStatusError {
		t.Errorf("span 状态应为 error, 实际: %s", loaded.Spans[0].Status)
	}
	if loaded.Spans[0].Error != "tool boom" {
		t.Errorf("span 错误应为 'tool boom', 实际: %s", loaded.Spans[0].Error)
	}
}

// TestRecorder_NestedSpans 验证嵌套 span 的父子关系（ParentID）。
func TestRecorder_NestedSpans(t *testing.T) {
	rec := NewRecorder(NewMemoryStorage())
	rec.StartTrace("nested", nil)

	parentID := rec.StartSpan("parent", SpanTypeChain, nil)
	childID := rec.StartSpan("child", SpanTypeLLM, nil)
	rec.EndSpan(nil, nil) // 结束 child
	rec.EndSpan(nil, nil) // 结束 parent

	trace := rec.GetCurrentTrace()
	if len(trace.Spans) != 2 {
		t.Fatalf("应有 2 个 span, 实际: %d", len(trace.Spans))
	}
	var parent, child *Span
	for _, s := range trace.Spans {
		if s.ID == parentID {
			parent = s
		}
		if s.ID == childID {
			child = s
		}
	}
	if parent == nil || child == nil {
		t.Fatal("未找到 parent/child span")
	}
	if parent.ParentID != "" {
		t.Errorf("顶层 span 的 ParentID 应为空, 实际: %s", parent.ParentID)
	}
	if child.ParentID != parentID {
		t.Errorf("child 的 ParentID 应为 %s, 实际: %s", parentID, child.ParentID)
	}
}

// ============== Recorder 边界与未闭环 ==============

// TestRecorder_EndTraceWithoutStart 验证未开始 trace 时 EndTrace 不 panic 且无副作用。
func TestRecorder_EndTraceWithoutStart(t *testing.T) {
	storage := NewMemoryStorage()
	rec := NewRecorder(storage)
	// 直接结束，不应 panic
	rec.EndTrace("output", nil)
	list, _ := storage.List(context.Background(), TraceFilter{})
	if len(list) != 0 {
		t.Errorf("未开始 trace 不应保存任何内容, 实际数量: %d", len(list))
	}
}

// TestRecorder_StartSpanWithoutTrace 验证未开始 trace 时 StartSpan 返回空 ID。
func TestRecorder_StartSpanWithoutTrace(t *testing.T) {
	rec := NewRecorder(NewMemoryStorage())
	id := rec.StartSpan("orphan", SpanTypeCustom, nil)
	if id != "" {
		t.Errorf("无 trace 时 StartSpan 应返回空 ID, 实际: %s", id)
	}
}

// TestRecorder_EndSpanWithoutSpan 验证空 span 栈时 EndSpan 不 panic。
func TestRecorder_EndSpanWithoutSpan(t *testing.T) {
	rec := NewRecorder(NewMemoryStorage())
	rec.StartTrace("t", nil)
	rec.EndSpan("output", nil) // 栈为空，不应 panic
}

// TestRecorder_AddAttributeWithoutSpan 验证空栈时 AddAttribute/AddEvent 安全。
func TestRecorder_AddAttributeWithoutSpan(t *testing.T) {
	rec := NewRecorder(NewMemoryStorage())
	rec.StartTrace("t", nil)
	rec.AddAttribute("k", "v") // 无活动 span，不应 panic
	rec.AddEvent("e", nil)     // 无活动 span，不应 panic
}

// TestRecorder_MaxSpansLimit 验证 MaxSpans 上限：超出后 StartSpan 返回空。
func TestRecorder_MaxSpansLimit(t *testing.T) {
	cfg := DefaultRecorderConfig
	cfg.MaxSpans = 2
	rec := NewRecorder(NewMemoryStorage(), cfg)
	rec.StartTrace("t", nil)

	id1 := rec.StartSpan("s1", SpanTypeCustom, nil)
	rec.EndSpan(nil, nil)
	id2 := rec.StartSpan("s2", SpanTypeCustom, nil)
	rec.EndSpan(nil, nil)
	// 此时 trace.Spans 已有 2 个，达到上限
	id3 := rec.StartSpan("s3", SpanTypeCustom, nil)

	if id1 == "" || id2 == "" {
		t.Fatalf("前两个 span 应成功, id1=%q id2=%q", id1, id2)
	}
	if id3 != "" {
		t.Errorf("超出 MaxSpans 后 StartSpan 应返回空 ID, 实际: %s", id3)
	}
	if len(rec.GetCurrentTrace().Spans) != 2 {
		t.Errorf("span 数量应被限制为 2, 实际: %d", len(rec.GetCurrentTrace().Spans))
	}
}

// TestRecorder_StartTraceOverwritesPrevious 回归: 重复 StartTrace 不得静默丢失上一个
// 未结束的 trace；修复后应先 flush(保存) 旧 trace 再开启新 trace，避免整条调用链丢失。
func TestRecorder_StartTraceOverwritesPrevious(t *testing.T) {
	storage := NewMemoryStorage()
	rec := NewRecorder(storage)

	id1 := rec.StartTrace("first", nil)
	rec.StartSpan("s", SpanTypeCustom, nil)
	// 未 EndTrace 直接开始新的 trace
	id2 := rec.StartTrace("second", nil)

	if id1 == id2 {
		t.Fatal("两次 StartTrace 的 ID 应不同")
	}
	// 修复后：第一个未结束 trace 应在被覆盖前自动 flush 保存（不再静默丢失）
	saved, err := storage.Load(context.Background(), id1)
	if err != nil {
		t.Fatalf("第一个未结束 trace 应在 StartTrace 覆盖前被自动保存, Load 失败: %v", err)
	}
	if saved.Name != "first" {
		t.Errorf("被 flush 的 trace 名称应为 first, 实际: %s", saved.Name)
	}
	if len(saved.Spans) != 1 {
		t.Errorf("被 flush 的 trace 应保留其已录制的 1 个 span, 实际: %d", len(saved.Spans))
	}

	cur := rec.GetCurrentTrace()
	if cur.Name != "second" {
		t.Errorf("当前 trace 应被覆盖为 second, 实际: %s", cur.Name)
	}
	// 旧的 span 栈应已被清空（StartTrace 重置 spanStack），新 trace 只含自己的 span
	rec.StartSpan("s2", SpanTypeCustom, nil)
	if len(cur.Spans) != 1 {
		t.Errorf("新 trace 应只有 1 个 span, 实际: %d (旧 spanStack 可能未清理)", len(cur.Spans))
	}
}

// TestRecorder_StaleSpanStackAfterRestart 回归: StartTrace 必须重置 spanStack，
// 否则上一个未结束 trace 的孤儿 span 会残留在栈中，后续 AddAttribute/EndSpan 会
// 错误地写入到不属于当前 trace 的 span（脏栈 / 状态不一致 bug）。
func TestRecorder_StaleSpanStackAfterRestart(t *testing.T) {
	rec := NewRecorder(NewMemoryStorage())

	rec.StartTrace("first", nil)
	firstSpanID := rec.StartSpan("open-span", SpanTypeCustom, nil) // 故意不 EndSpan，留一个开放 span

	// 不调用 EndTrace 直接开始新 trace；修复后 StartTrace 也应清空 spanStack
	rec.StartTrace("second", nil)

	// 在新 trace 内开启一个真正属于它的 span。
	// 若脏栈未清，旧 open-span 仍是栈顶，real-span 会把它误当父节点（ParentID 错位）。
	spanID := rec.StartSpan("real-span", SpanTypeCustom, nil)
	if spanID == "" {
		t.Fatal("新 trace 内 StartSpan 应成功")
	}
	rec.AddAttribute("k", "v")
	rec.EndSpan("real-output", nil)

	cur := rec.GetCurrentTrace()
	// 新 trace 只应包含它自己的 1 个 span
	if len(cur.Spans) != 1 {
		t.Fatalf("新 trace 应只有 1 个 span, 实际: %d (StartTrace 未清理 spanStack)", len(cur.Spans))
	}
	realSpan := cur.Spans[0]
	if realSpan.Name != "real-span" {
		t.Errorf("唯一 span 应为 real-span, 实际: %s", realSpan.Name)
	}
	// 关键断言：real-span 是新 trace 的顶层 span，其 ParentID 必须为空。
	// 脏栈会让它错误地指向上一个 trace 的孤儿 open-span。
	if realSpan.ParentID != "" {
		t.Errorf("real-span 应为顶层 span (ParentID 为空), 实际指向了脏栈孤儿 span: %s (firstSpanID=%s)", realSpan.ParentID, firstSpanID)
	}
	// EndSpan 应把 real-output 写到 real-span，而非被脏栈顶 pop 偏移
	if realSpan.Output != "real-output" {
		t.Errorf("real-span 的输出应为 real-output, 实际: %v (脏栈导致 EndSpan 错位)", realSpan.Output)
	}
	if realSpan.Attributes["k"] != "v" {
		t.Errorf("real-span 应记录属性 k=v, 实际: %v", realSpan.Attributes["k"])
	}
}

// TestRecorder_RecordInputOutputDisabled 验证关闭录制输入/输出时不记录。
func TestRecorder_RecordInputOutputDisabled(t *testing.T) {
	cfg := RecorderConfig{MaxSpans: 100, RecordInput: false, RecordOutput: false}
	rec := NewRecorder(NewMemoryStorage(), cfg)
	rec.StartTrace("t", "secret-input")
	rec.StartSpan("s", SpanTypeCustom, "span-input")
	rec.EndSpan("span-output", nil)
	rec.EndTrace("trace-output", nil)

	// currentTrace 已 nil，需从一个新的角度验证：用一个会保存的 storage
	storage := NewMemoryStorage()
	rec2 := NewRecorder(storage, cfg)
	id := rec2.StartTrace("t2", "secret")
	rec2.StartSpan("s2", SpanTypeCustom, "in")
	rec2.EndSpan("out", nil)
	rec2.EndTrace("trace-out", nil)

	loaded, _ := storage.Load(context.Background(), id)
	if loaded.Input != nil {
		t.Errorf("RecordInput=false 时 trace.Input 应为 nil, 实际: %v", loaded.Input)
	}
	if loaded.Output != nil {
		t.Errorf("RecordOutput=false 时 trace.Output 应为 nil, 实际: %v", loaded.Output)
	}
	if loaded.Spans[0].Input != nil {
		t.Errorf("RecordInput=false 时 span.Input 应为 nil, 实际: %v", loaded.Spans[0].Input)
	}
	if loaded.Spans[0].Output != nil {
		t.Errorf("RecordOutput=false 时 span.Output 应为 nil, 实际: %v", loaded.Spans[0].Output)
	}
}

// ============== sanitize 脱敏 ==============

// TestRecorder_SanitizeTopLevel 验证顶层敏感字段被脱敏。
func TestRecorder_SanitizeTopLevel(t *testing.T) {
	storage := NewMemoryStorage()
	cfg := DefaultRecorderConfig
	cfg.SensitiveFields = []string{"password", "token"}
	rec := NewRecorder(storage, cfg)

	id := rec.StartTrace("t", map[string]any{
		"username": "alice",
		"password": "p@ss",
		"token":    "abc123",
	})
	rec.EndTrace(nil, nil)

	loaded, _ := storage.Load(context.Background(), id)
	m, ok := loaded.Input.(map[string]any)
	if !ok {
		t.Fatalf("Input 应为 map, 实际类型: %T", loaded.Input)
	}
	if m["password"] != "***REDACTED***" {
		t.Errorf("password 应被脱敏, 实际: %v", m["password"])
	}
	if m["token"] != "***REDACTED***" {
		t.Errorf("token 应被脱敏, 实际: %v", m["token"])
	}
	if m["username"] != "alice" {
		t.Errorf("非敏感字段不应被改动, 实际: %v", m["username"])
	}
}

// TestRecorder_SanitizeNestedNotRedacted 回归: sanitize 必须递归脱敏嵌套结构中的敏感字段，
// 否则嵌套 map/slice 里的 password 等敏感数据会以明文录制（安全问题）。
func TestRecorder_SanitizeNestedNotRedacted(t *testing.T) {
	storage := NewMemoryStorage()
	cfg := DefaultRecorderConfig
	cfg.SensitiveFields = []string{"password"}
	rec := NewRecorder(storage, cfg)

	id := rec.StartTrace("t", map[string]any{
		"user": map[string]any{"password": "secret"},
		// 嵌套在 slice 中的对象同样需要脱敏
		"accounts": []any{
			map[string]any{"name": "a", "password": "p1"},
			map[string]any{"name": "b", "password": "p2"},
		},
	})
	rec.EndTrace(nil, nil)

	loaded, _ := storage.Load(context.Background(), id)
	m := loaded.Input.(map[string]any)

	nested, _ := m["user"].(map[string]any)
	if nested == nil || nested["password"] != "***REDACTED***" {
		t.Errorf("嵌套 map 中的 password 应被脱敏, 实际: %v", nested["password"])
	}

	accounts, _ := m["accounts"].([]any)
	if len(accounts) != 2 {
		t.Fatalf("accounts 应保留 2 个元素, 实际: %d", len(accounts))
	}
	for i, raw := range accounts {
		acc, _ := raw.(map[string]any)
		if acc == nil || acc["password"] != "***REDACTED***" {
			t.Errorf("accounts[%d] 中的 password 应被脱敏, 实际: %v", i, acc["password"])
		}
		// 非敏感字段不应被改动
		if acc != nil && acc["name"] == "***REDACTED***" {
			t.Errorf("accounts[%d] 的 name 不应被脱敏", i)
		}
	}
}

// TestRecorder_SanitizeChangesType 暴露 sanitize 会把结构体输入转成 map，改变了原始类型。
func TestRecorder_SanitizeChangesType(t *testing.T) {
	storage := NewMemoryStorage()
	cfg := DefaultRecorderConfig
	cfg.SensitiveFields = []string{"Password"}
	rec := NewRecorder(storage, cfg)

	type Creds struct {
		User     string `json:"user"`
		Password string `json:"Password"`
	}
	id := rec.StartTrace("t", Creds{User: "bob", Password: "x"})
	rec.EndTrace(nil, nil)

	loaded, _ := storage.Load(context.Background(), id)
	// 由于先 Marshal 再 Unmarshal 到 map，类型从 struct 变成了 map
	if _, ok := loaded.Input.(map[string]any); !ok {
		t.Logf("注意：脱敏后类型为 %T (未变成 map)", loaded.Input)
	} else {
		t.Logf("已知行为：脱敏将 struct 输入转成 map[string]any (类型丢失)")
	}
}

// TestRecorder_SanitizeNonMappable 验证非 map 可序列化输入（如字符串）在有敏感字段时的处理。
func TestRecorder_SanitizeNonMappable(t *testing.T) {
	storage := NewMemoryStorage()
	cfg := DefaultRecorderConfig
	cfg.SensitiveFields = []string{"password"}
	rec := NewRecorder(storage, cfg)

	// 字符串无法 Unmarshal 到 map，应原样返回
	id := rec.StartTrace("t", "just-a-string")
	rec.EndTrace(nil, nil)

	loaded, _ := storage.Load(context.Background(), id)
	if loaded.Input != "just-a-string" {
		t.Errorf("非 map 输入应原样保留, 实际: %v", loaded.Input)
	}
}

// ============== ErrorHandler 存储失败处理 ==============

// TestRecorder_ErrorHandlerOnSaveFailure 验证存储 Save 失败时触发 ErrorHandler。
func TestRecorder_ErrorHandlerOnSaveFailure(t *testing.T) {
	var captured error
	cfg := DefaultRecorderConfig
	cfg.ErrorHandler = func(err error) { captured = err }
	rec := NewRecorder(&failingStorage{saveErr: errors.New("disk full")}, cfg)

	rec.StartTrace("t", nil)
	rec.EndTrace(nil, nil)

	if captured == nil {
		t.Fatal("Save 失败时应触发 ErrorHandler")
	}
	if !strings.Contains(captured.Error(), "disk full") {
		t.Errorf("错误应包含底层原因 'disk full', 实际: %v", captured)
	}
	if !strings.Contains(captured.Error(), "failed to save trace") {
		t.Errorf("错误应被包装, 实际: %v", captured)
	}
}

// TestRecorder_NilErrorHandlerSilent 验证 ErrorHandler 为 nil 时静默忽略保存失败（不 panic）。
func TestRecorder_NilErrorHandlerSilent(t *testing.T) {
	rec := NewRecorder(&failingStorage{saveErr: errors.New("boom")})
	rec.StartTrace("t", nil)
	rec.EndTrace(nil, nil) // 不应 panic
}

// TestRecorder_NilStorage 验证 storage 为 nil 时 EndTrace 不 panic。
func TestRecorder_NilStorage(t *testing.T) {
	rec := NewRecorder(nil)
	rec.StartTrace("t", nil)
	rec.EndTrace(nil, nil) // storage==nil 分支，不应 panic
}

// ============== Recorder 并发安全 ==============

// TestRecorder_ConcurrentSpans 并发录制 span 不应 panic 或 data race（需配合 -race）。
func TestRecorder_ConcurrentSpans(t *testing.T) {
	cfg := DefaultRecorderConfig
	cfg.MaxSpans = 100000
	rec := NewRecorder(NewMemoryStorage(), cfg)
	rec.StartTrace("concurrent", nil)

	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			rec.StartSpan(fmt.Sprintf("s-%d", i), SpanTypeCustom, i)
			rec.AddAttribute("idx", i)
			rec.AddEvent("evt", map[string]any{"i": i})
			rec.EndSpan(i, nil)
		}(i)
	}
	wg.Wait()
	rec.EndTrace(nil, nil)
	// 主要验证不 panic / 无 race
}

// ============== Replayer 重放一致性 ==============

// TestReplayer_ReplayConsistency 验证重放结果与原始输出一致（OutputMatch=true）。
func TestReplayer_ReplayConsistency(t *testing.T) {
	storage := NewMemoryStorage()
	trace := &Trace{
		ID: "tr-1",
		Spans: []*Span{
			{ID: "s1", Name: "a", Output: "out-a"},
			{ID: "s2", Name: "b", Output: map[string]any{"k": "v"}},
		},
	}
	_ = storage.Save(context.Background(), trace)

	rep := NewReplayer(storage, echoExecutor{})
	res, err := rep.Replay(context.Background(), "tr-1")
	if err != nil {
		t.Fatalf("Replay 失败: %v", err)
	}
	if !res.Success {
		t.Errorf("重放应成功, 实际 Error: %s", res.Error)
	}
	if len(res.SpanResults) != 2 {
		t.Fatalf("应有 2 个 span 结果, 实际: %d", len(res.SpanResults))
	}
	for _, sr := range res.SpanResults {
		if !sr.Success {
			t.Errorf("span %s 应成功", sr.Name)
		}
		if !sr.OutputMatch {
			t.Errorf("span %s 输出应匹配 (echo executor), expected=%v actual=%v", sr.Name, sr.ExpectedOutput, sr.ActualOutput)
		}
	}
}

// TestReplayer_OutputMismatch 验证重放输出与预期不一致时 OutputMatch=false。
func TestReplayer_OutputMismatch(t *testing.T) {
	storage := NewMemoryStorage()
	trace := &Trace{
		ID:    "tr-mismatch",
		Spans: []*Span{{ID: "s1", Name: "a", Output: "expected"}},
	}
	_ = storage.Save(context.Background(), trace)

	exec := ExecutorFunc(func(ctx context.Context, span *Span) (any, error) {
		return "actual-different", nil
	})
	rep := NewReplayer(storage, exec)
	res, _ := rep.Replay(context.Background(), "tr-mismatch")

	sr := res.SpanResults[0]
	if sr.OutputMatch {
		t.Error("输出不同, OutputMatch 应为 false")
	}
	// span 执行本身无错，仍算 Success
	if !sr.Success {
		t.Error("无执行错误时 span 应为 Success=true")
	}
}

// TestReplayer_ExecuteError_NoStop 验证执行出错但 StopOnError=false 时继续重放后续 span。
func TestReplayer_ExecuteError_NoStop(t *testing.T) {
	storage := NewMemoryStorage()
	trace := &Trace{
		ID: "tr-err",
		Spans: []*Span{
			{ID: "s1", Name: "fail", Output: "x"},
			{ID: "s2", Name: "ok", Output: "y"},
		},
	}
	_ = storage.Save(context.Background(), trace)

	exec := ExecutorFunc(func(ctx context.Context, span *Span) (any, error) {
		if span.Name == "fail" {
			return nil, errors.New("exec failed")
		}
		return span.Output, nil
	})
	rep := NewReplayer(storage, exec, ReplayerConfig{StopOnError: false})
	res, _ := rep.Replay(context.Background(), "tr-err")

	if res.Success {
		t.Error("有 span 失败时整体 Success 应为 false")
	}
	if len(res.SpanResults) != 2 {
		t.Fatalf("StopOnError=false 应执行全部 2 个 span, 实际: %d", len(res.SpanResults))
	}
	if res.SpanResults[0].Success {
		t.Error("第一个 span 应失败")
	}
	if !res.SpanResults[1].Success {
		t.Error("第二个 span 应成功")
	}
}

// TestReplayer_ExecuteError_Stop 验证 StopOnError=true 时遇错立即停止。
func TestReplayer_ExecuteError_Stop(t *testing.T) {
	storage := NewMemoryStorage()
	trace := &Trace{
		ID: "tr-stop",
		Spans: []*Span{
			{ID: "s1", Name: "fail", Output: "x"},
			{ID: "s2", Name: "never", Output: "y"},
		},
	}
	_ = storage.Save(context.Background(), trace)

	exec := ExecutorFunc(func(ctx context.Context, span *Span) (any, error) {
		return nil, errors.New("boom")
	})
	rep := NewReplayer(storage, exec, ReplayerConfig{StopOnError: true})
	res, _ := rep.Replay(context.Background(), "tr-stop")

	if res.Success {
		t.Error("StopOnError 遇错整体应失败")
	}
	if len(res.SpanResults) != 1 {
		t.Errorf("StopOnError 应在第一个 span 后停止, 实际结果数: %d", len(res.SpanResults))
	}
	if !strings.Contains(res.Error, "fail") {
		t.Errorf("结果 Error 应包含失败 span 名, 实际: %s", res.Error)
	}
}

// TestReplayer_ReplayLoadNotFound 验证加载不存在的 trace 时返回错误。
func TestReplayer_ReplayLoadNotFound(t *testing.T) {
	rep := NewReplayer(NewMemoryStorage(), echoExecutor{})
	_, err := rep.Replay(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("加载不存在的 trace 应返回错误")
	}
	if !strings.Contains(err.Error(), "failed to load trace") {
		t.Errorf("错误应被包装为 'failed to load trace', 实际: %v", err)
	}
}

// TestReplayer_EmptyTrace 验证重放空 span 的 trace 不出错（空录制边界）。
func TestReplayer_EmptyTrace(t *testing.T) {
	rep := NewReplayer(NewMemoryStorage(), echoExecutor{})
	res, err := rep.ReplayTrace(context.Background(), &Trace{ID: "empty", Spans: nil})
	if err != nil {
		t.Fatalf("空 trace 重放不应出错: %v", err)
	}
	if !res.Success {
		t.Error("空 trace 应视为成功")
	}
	if len(res.SpanResults) != 0 {
		t.Errorf("空 trace 不应有 span 结果, 实际: %d", len(res.SpanResults))
	}
}

// TestReplayer_ModifyInput 验证 ModifyInput 钩子能改变传给执行器的输入。
func TestReplayer_ModifyInput(t *testing.T) {
	storage := NewMemoryStorage()
	trace := &Trace{ID: "tr-mod", Spans: []*Span{{ID: "s1", Name: "a", Input: "orig", Output: "modified"}}}
	_ = storage.Save(context.Background(), trace)

	exec := ExecutorFunc(func(ctx context.Context, span *Span) (any, error) {
		// 执行器返回它实际收到的输入，验证已被修改
		return span.Input, nil
	})
	cfg := ReplayerConfig{
		ModifyInput: func(span *Span) any { return "modified" },
	}
	rep := NewReplayer(storage, exec, cfg)
	res, _ := rep.Replay(context.Background(), "tr-mod")

	if res.SpanResults[0].ActualOutput != "modified" {
		t.Errorf("ModifyInput 应改变执行器收到的输入, 实际输出: %v", res.SpanResults[0].ActualOutput)
	}
	// 原始 span 不应被修改（使用 spanCopy）
	if trace.Spans[0].Input != "orig" {
		t.Errorf("原始 span.Input 不应被修改, 实际: %v", trace.Spans[0].Input)
	}
}

// TestReplayer_CustomCompareOutput 验证自定义 CompareOutput 被使用。
func TestReplayer_CustomCompareOutput(t *testing.T) {
	storage := NewMemoryStorage()
	trace := &Trace{ID: "tr-cmp", Spans: []*Span{{ID: "s1", Name: "a", Output: "EXPECTED"}}}
	_ = storage.Save(context.Background(), trace)

	exec := ExecutorFunc(func(ctx context.Context, span *Span) (any, error) {
		return "expected", nil // 大小写不同
	})
	called := false
	cfg := ReplayerConfig{
		CompareOutput: func(expected, actual any) bool {
			called = true
			return strings.EqualFold(fmt.Sprint(expected), fmt.Sprint(actual))
		},
	}
	rep := NewReplayer(storage, exec, cfg)
	res, _ := rep.Replay(context.Background(), "tr-cmp")

	if !called {
		t.Error("自定义 CompareOutput 应被调用")
	}
	if !res.SpanResults[0].OutputMatch {
		t.Error("忽略大小写比较应匹配")
	}
}

// TestReplayer_Breakpoint_Continue 验证断点处理器返回 true 时继续重放。
func TestReplayer_Breakpoint_Continue(t *testing.T) {
	storage := NewMemoryStorage()
	trace := &Trace{
		ID: "tr-bp",
		Spans: []*Span{
			{ID: "s1", Name: "break-here", Output: "a"},
			{ID: "s2", Name: "next", Output: "b"},
		},
	}
	_ = storage.Save(context.Background(), trace)

	var breakHit string
	cfg := ReplayerConfig{
		Breakpoints: []string{"break-here"},
		BreakpointHandler: func(span *Span) bool {
			breakHit = span.Name
			return true // 继续
		},
	}
	rep := NewReplayer(storage, echoExecutor{}, cfg)
	res, _ := rep.Replay(context.Background(), "tr-bp")

	if breakHit != "break-here" {
		t.Errorf("断点处理器应被命中名为 break-here 的 span, 实际: %s", breakHit)
	}
	if len(res.SpanResults) != 2 {
		t.Errorf("处理器返回 true 应继续执行全部 span, 实际: %d", len(res.SpanResults))
	}
}

// TestReplayer_Breakpoint_Interrupt 验证断点处理器返回 false 时中断重放。
func TestReplayer_Breakpoint_Interrupt(t *testing.T) {
	storage := NewMemoryStorage()
	trace := &Trace{
		ID: "tr-bp2",
		Spans: []*Span{
			{ID: "s1", Name: "stop", Output: "a"},
			{ID: "s2", Name: "next", Output: "b"},
		},
	}
	_ = storage.Save(context.Background(), trace)

	cfg := ReplayerConfig{
		Breakpoints:       []string{"stop"},
		BreakpointHandler: func(span *Span) bool { return false }, // 中断
	}
	rep := NewReplayer(storage, echoExecutor{}, cfg)
	res, _ := rep.Replay(context.Background(), "tr-bp2")

	// break 发生在执行 s1 之前，所以 s1 都不会执行
	if len(res.SpanResults) != 0 {
		t.Errorf("断点返回 false 应在执行前中断, 期望 0 个结果, 实际: %d", len(res.SpanResults))
	}
}

// TestReplayer_BreakpointWithoutHandler 暴露：设置断点但 BreakpointHandler 为 nil 时断点形同虚设。
func TestReplayer_BreakpointWithoutHandler(t *testing.T) {
	storage := NewMemoryStorage()
	trace := &Trace{
		ID: "tr-bp3",
		Spans: []*Span{
			{ID: "s1", Name: "bp", Output: "a"},
			{ID: "s2", Name: "next", Output: "b"},
		},
	}
	_ = storage.Save(context.Background(), trace)

	cfg := ReplayerConfig{Breakpoints: []string{"bp"}} // 无 handler
	rep := NewReplayer(storage, echoExecutor{}, cfg)
	res, _ := rep.Replay(context.Background(), "tr-bp3")

	// 没有 handler 时断点不起任何作用，全部 span 正常执行
	if len(res.SpanResults) != 2 {
		t.Errorf("无 handler 的断点应被忽略, 全部执行, 实际: %d", len(res.SpanResults))
	}
	t.Log("已知行为：Breakpoints 设置了但 BreakpointHandler 为 nil 时，断点完全无效")
}

// ============== defaultCompare ==============

// TestDefaultCompare 验证默认 JSON 比较的各种情形。
func TestDefaultCompare(t *testing.T) {
	rep := &Replayer{}
	cases := []struct {
		name           string
		expected       any
		actual         any
		wantMatch      bool
	}{
		{"相同字符串", "x", "x", true},
		{"不同字符串", "x", "y", false},
		{"相同 map", map[string]any{"a": 1}, map[string]any{"a": 1}, true},
		{"both nil", nil, nil, true},
		{"nil vs 空字符串", nil, "", false},
		{"数字相等", 1, 1, true},
		// JSON 序列化后 int 和 float 表示可能不同
		{"int vs float 同值", 1, 1.0, true}, // json.Marshal(1)=="1", json.Marshal(1.0)=="1"
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rep.defaultCompare(c.expected, c.actual)
			if got != c.wantMatch {
				t.Errorf("defaultCompare(%v, %v) = %v, 期望 %v", c.expected, c.actual, got, c.wantMatch)
			}
		})
	}
}

// TestDefaultCompare_UnmarshalableNotFalseMatch 回归: defaultCompare 静默吞掉
// json.Marshal 错误会导致不可序列化的输入双双得到空串 "" 而被误判为"匹配"。
// 修复后：当任一侧序列化失败时不得返回 true（假阳性匹配）。
func TestDefaultCompare_UnmarshalableNotFalseMatch(t *testing.T) {
	rep := &Replayer{}

	// chan 无法被 json.Marshal，旧实现下 expectedJSON==actualJSON=="" -> 误判 true
	expected := make(chan int)
	actual := make(chan int)
	if rep.defaultCompare(expected, actual) {
		t.Error("两个不可序列化值不应被判定为输出匹配 (静默吞错导致的假阳性)")
	}

	// 一侧可序列化、一侧不可序列化：显然不应匹配
	if rep.defaultCompare("real-output", make(chan int)) {
		t.Error("可序列化值与不可序列化值不应匹配")
	}
}

// ============== MemoryStorage ==============

// TestMemoryStorage_CRUD 验证内存存储的增删查。
func TestMemoryStorage_CRUD(t *testing.T) {
	s := NewMemoryStorage()
	ctx := context.Background()

	tr := &Trace{ID: "x1", Name: "demo", Status: StatusCompleted}
	if err := s.Save(ctx, tr); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	got, err := s.Load(ctx, "x1")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if got.Name != "demo" {
		t.Errorf("Load 名称错误: %s", got.Name)
	}
	if err := s.Delete(ctx, "x1"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if _, err := s.Load(ctx, "x1"); err == nil {
		t.Error("删除后 Load 应失败")
	}
}

// TestMemoryStorage_LoadNotFound 验证加载不存在的 ID 返回错误。
func TestMemoryStorage_LoadNotFound(t *testing.T) {
	s := NewMemoryStorage()
	_, err := s.Load(context.Background(), "ghost")
	if err == nil {
		t.Fatal("加载不存在的 trace 应返回错误")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("错误应包含 'not found', 实际: %v", err)
	}
}

// TestMemoryStorage_DeleteNonExistent 验证删除不存在的 ID 不报错（幂等）。
func TestMemoryStorage_DeleteNonExistent(t *testing.T) {
	s := NewMemoryStorage()
	if err := s.Delete(context.Background(), "ghost"); err != nil {
		t.Errorf("删除不存在的 ID 应幂等无错, 实际: %v", err)
	}
}

// TestMemoryStorage_ListFilters 验证 List 过滤器（名称/状态/Limit/时间）。
func TestMemoryStorage_ListFilters(t *testing.T) {
	s := NewMemoryStorage()
	ctx := context.Background()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	_ = s.Save(ctx, &Trace{ID: "a", Name: "foo", Status: StatusCompleted, StartTime: base, EndTime: base.Add(time.Hour)})
	_ = s.Save(ctx, &Trace{ID: "b", Name: "bar", Status: StatusFailed, StartTime: base.Add(time.Hour), EndTime: base.Add(2 * time.Hour)})
	_ = s.Save(ctx, &Trace{ID: "c", Name: "foo", Status: StatusCompleted, StartTime: base.Add(2 * time.Hour), EndTime: base.Add(3 * time.Hour)})

	// 按名称
	r, _ := s.List(ctx, TraceFilter{Name: "foo"})
	if len(r) != 2 {
		t.Errorf("name=foo 应返回 2 个, 实际: %d", len(r))
	}
	// 按状态
	r, _ = s.List(ctx, TraceFilter{Status: StatusFailed})
	if len(r) != 1 || r[0].ID != "b" {
		t.Errorf("status=failed 应返回 [b], 实际: %+v", r)
	}
	// Limit
	r, _ = s.List(ctx, TraceFilter{Limit: 1})
	if len(r) != 1 {
		t.Errorf("Limit=1 应返回 1 个, 实际: %d", len(r))
	}
	// 全部
	r, _ = s.List(ctx, TraceFilter{})
	if len(r) != 3 {
		t.Errorf("无过滤应返回全部 3 个, 实际: %d", len(r))
	}
}

// TestMemoryStorage_ListStartEndTimeFilter 验证 MemoryStorage 时间范围过滤。
func TestMemoryStorage_ListStartEndTimeFilter(t *testing.T) {
	s := NewMemoryStorage()
	ctx := context.Background()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// StartTime 早于 cutoff 的会被过滤掉
	_ = s.Save(ctx, &Trace{ID: "old", StartTime: base, EndTime: base.Add(time.Hour)})
	_ = s.Save(ctx, &Trace{ID: "new", StartTime: base.Add(10 * time.Hour), EndTime: base.Add(11 * time.Hour)})

	cutoff := base.Add(5 * time.Hour)
	r, _ := s.List(ctx, TraceFilter{StartTime: &cutoff})
	// 过滤逻辑：trace.StartTime.Before(cutoff) 则跳过；old(base) before cutoff -> 跳过
	for _, tr := range r {
		if tr.ID == "old" {
			t.Errorf("StartTime 早于 cutoff 的 trace 应被过滤, 但 'old' 仍出现")
		}
	}
}

// TestMemoryStorage_Concurrent 并发读写不应 data race。
func TestMemoryStorage_Concurrent(t *testing.T) {
	s := NewMemoryStorage()
	ctx := context.Background()
	var wg sync.WaitGroup
	const n = 50
	wg.Add(n * 2)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_ = s.Save(ctx, &Trace{ID: fmt.Sprintf("t-%d", i)})
		}(i)
		go func(i int) {
			defer wg.Done()
			_, _ = s.List(ctx, TraceFilter{})
		}(i)
	}
	wg.Wait()
}

// ============== FileStorage ==============

// TestFileStorage_SaveLoadDelete 验证文件存储的保存/加载/删除往返一致性。
func TestFileStorage_SaveLoadDelete(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStorage(dir)
	ctx := context.Background()

	tr := &Trace{
		ID:     "file-1",
		Name:   "demo",
		Status: StatusCompleted,
		Spans:  []*Span{{ID: "sp", Name: "s", Type: SpanTypeLLM}},
		Input:  map[string]any{"q": "hello"},
	}
	if err := s.Save(ctx, tr); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	loaded, err := s.Load(ctx, "file-1")
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if loaded.Name != "demo" || len(loaded.Spans) != 1 {
		t.Errorf("加载内容不一致: %+v", loaded)
	}
	if err := s.Delete(ctx, "file-1"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if _, err := s.Load(ctx, "file-1"); err == nil {
		t.Error("删除后 Load 应失败")
	}
}

// TestFileStorage_LoadNonExistent 验证加载不存在文件返回错误。
func TestFileStorage_LoadNonExistent(t *testing.T) {
	s := NewFileStorage(t.TempDir())
	if _, err := s.Load(context.Background(), "ghost"); err == nil {
		t.Fatal("加载不存在文件应返回错误")
	}
}

// TestFileStorage_List 验证文件存储 List 及名称/状态过滤。
func TestFileStorage_List(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStorage(dir)
	ctx := context.Background()

	_ = s.Save(ctx, &Trace{ID: "a", Name: "foo", Status: StatusCompleted})
	_ = s.Save(ctx, &Trace{ID: "b", Name: "bar", Status: StatusFailed})

	all, err := s.List(ctx, TraceFilter{})
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("应列出 2 个, 实际: %d", len(all))
	}

	foo, _ := s.List(ctx, TraceFilter{Name: "foo"})
	if len(foo) != 1 || foo[0].ID != "a" {
		t.Errorf("name=foo 过滤应得 [a], 实际: %+v", foo)
	}
}

// TestFileStorage_ListIgnoresTimeFilter 回归: FileStorage.List 必须与 MemoryStorage 一致地
// 应用 StartTime/EndTime 过滤，否则时间窗口查询在两种存储下结果不一致。
func TestFileStorage_ListIgnoresTimeFilter(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStorage(dir)
	ctx := context.Background()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// old 的 StartTime 早于 cutoff，应被 StartTime 过滤掉
	_ = s.Save(ctx, &Trace{ID: "old", StartTime: base, EndTime: base.Add(time.Hour)})
	// recent 的 StartTime 晚于 cutoff，应保留
	_ = s.Save(ctx, &Trace{ID: "recent", StartTime: base.Add(200 * time.Hour), EndTime: base.Add(201 * time.Hour)})

	cutoff := base.Add(100 * time.Hour)
	r, _ := s.List(ctx, TraceFilter{StartTime: &cutoff})

	var ids []string
	for _, tr := range r {
		ids = append(ids, tr.ID)
		if tr.ID == "old" {
			t.Errorf("StartTime 早于 cutoff 的 'old' 应被过滤掉, 但仍出现 (FileStorage.List 未应用时间过滤)")
		}
	}
	// recent 应被保留
	foundRecent := false
	for _, id := range ids {
		if id == "recent" {
			foundRecent = true
		}
	}
	if !foundRecent {
		t.Errorf("StartTime 晚于 cutoff 的 'recent' 应保留, 实际结果: %v", ids)
	}

	// 同时验证 EndTime 过滤：EndTime 晚于 cutoff 的会被跳过（与 MemoryStorage 一致）
	endCutoff := base.Add(100 * time.Hour)
	r2, _ := s.List(ctx, TraceFilter{EndTime: &endCutoff})
	for _, tr := range r2 {
		if tr.ID == "recent" {
			t.Errorf("EndTime 晚于 cutoff 的 'recent' 应被 EndTime 过滤掉, 但仍出现")
		}
	}
}

// TestFileStorage_DeleteNonExistent 回归: FileStorage.Delete 对不存在的 ID 必须幂等无错，
// 与 MemoryStorage.Delete 行为一致（不应透传 os.Remove 的 not-exist 错误）。
func TestFileStorage_DeleteNonExistent(t *testing.T) {
	s := NewFileStorage(t.TempDir())
	if err := s.Delete(context.Background(), "ghost"); err != nil {
		t.Errorf("删除不存在的 ID 应幂等无错 (与 MemoryStorage 一致), 实际: %v", err)
	}
}

// ============== Export / Import ==============

// TestExportImport_RoundTrip 验证导出导入往返序列化一致性。
func TestExportImport_RoundTrip(t *testing.T) {
	orig := &Trace{
		ID:     "exp-1",
		Name:   "export-test",
		Status: StatusCompleted,
		Spans: []*Span{
			{ID: "sp1", Name: "a", Type: SpanTypeTool, Status: SpanStatusOK},
		},
		Input:    map[string]any{"x": "y"},
		Metadata: map[string]any{"env": "test"},
	}
	var buf bytes.Buffer
	if err := Export(orig, &buf); err != nil {
		t.Fatalf("Export 失败: %v", err)
	}
	imported, err := Import(&buf)
	if err != nil {
		t.Fatalf("Import 失败: %v", err)
	}
	if imported.ID != orig.ID || imported.Name != orig.Name {
		t.Errorf("往返后基本字段不一致: %+v", imported)
	}
	if len(imported.Spans) != 1 || imported.Spans[0].Name != "a" {
		t.Errorf("往返后 spans 不一致: %+v", imported.Spans)
	}
	if imported.Metadata["env"] != "test" {
		t.Errorf("往返后 metadata 不一致: %+v", imported.Metadata)
	}
}

// TestImport_InvalidJSON 验证导入非法 JSON 返回错误（边界）。
func TestImport_InvalidJSON(t *testing.T) {
	_, err := Import(strings.NewReader("{not-json"))
	if err == nil {
		t.Fatal("导入非法 JSON 应返回错误")
	}
}

// TestImport_EmptyInput 验证导入空输入返回错误（EOF）。
func TestImport_EmptyInput(t *testing.T) {
	_, err := Import(strings.NewReader(""))
	if err == nil {
		t.Fatal("导入空输入应返回错误 (EOF)")
	}
}

// ============== Unicode / 超长 / 特殊输入 ==============

// TestRecorder_UnicodeAndLongInput 验证 Unicode 和超长输入的录制与往返。
func TestRecorder_UnicodeAndLongInput(t *testing.T) {
	storage := NewMemoryStorage()
	rec := NewRecorder(storage)

	longStr := strings.Repeat("六边形战士🦾", 5000) // 超长 + Unicode + emoji
	id := rec.StartTrace("中文名称-名前-이름", map[string]any{
		"unicode": "你好世界🌍",
		"long":    longStr,
		"empty":   "",
	})
	rec.EndTrace(nil, nil)

	loaded, err := storage.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if loaded.Name != "中文名称-名前-이름" {
		t.Errorf("Unicode 名称不一致: %s", loaded.Name)
	}
	m := loaded.Input.(map[string]any)
	if m["unicode"] != "你好世界🌍" {
		t.Errorf("Unicode 输入不一致: %v", m["unicode"])
	}
	if m["long"] != longStr {
		t.Errorf("超长输入不一致 (长度: %d vs %d)", len(fmt.Sprint(m["long"])), len(longStr))
	}
}

// TestExportImport_UnicodeRoundTrip 验证 Unicode 内容的导出导入往返。
func TestExportImport_UnicodeRoundTrip(t *testing.T) {
	orig := &Trace{ID: "u1", Name: "测试🎯", Input: "数据📦"}
	var buf bytes.Buffer
	if err := Export(orig, &buf); err != nil {
		t.Fatalf("Export 失败: %v", err)
	}
	imported, err := Import(&buf)
	if err != nil {
		t.Fatalf("Import 失败: %v", err)
	}
	if imported.Name != "测试🎯" || imported.Input != "数据📦" {
		t.Errorf("Unicode 往返不一致: name=%s input=%v", imported.Name, imported.Input)
	}
}

// ============== ExecutorFunc 适配器 ==============

// TestExecutorFunc_Adapter 验证 ExecutorFunc 正确实现 Executor 接口。
func TestExecutorFunc_Adapter(t *testing.T) {
	var f Executor = ExecutorFunc(func(ctx context.Context, span *Span) (any, error) {
		return "v-" + span.Name, nil
	})
	out, err := f.Execute(context.Background(), &Span{Name: "x"})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if out != "v-x" {
		t.Errorf("输出应为 v-x, 实际: %v", out)
	}
}
