package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================================================
// shouldLog 级别过滤逻辑
// ============================================================

// TestShouldLog_LevelMatrix 验证 shouldLog 在各级别阈值下的过滤行为。
// 表驱动覆盖: 阈值 = Info 时, debug 应被丢弃, info/warning/error/critical 应记录。
func TestShouldLog_LevelMatrix(t *testing.T) {
	cases := []struct {
		name     string
		minLevel AuditLevel
		evtLevel AuditLevel
		want     bool
	}{
		{"info阈值-debug丢弃", LevelInfo, LevelDebug, false},
		{"info阈值-info记录", LevelInfo, LevelInfo, true},
		{"info阈值-warning记录", LevelInfo, LevelWarning, true},
		{"info阈值-critical记录", LevelInfo, LevelCritical, true},
		{"debug阈值-debug记录", LevelDebug, LevelDebug, true},
		{"critical阈值-error丢弃", LevelCritical, LevelError, false},
		{"critical阈值-critical记录", LevelCritical, LevelCritical, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := NewAuditLogger(WithAuditLevel(tc.minLevel))
			if got := l.shouldLog(tc.evtLevel); got != tc.want {
				t.Errorf("shouldLog(%s) with min=%s = %v, want %v",
					tc.evtLevel, tc.minLevel, got, tc.want)
			}
		})
	}
}

// TestShouldLog_UnknownEventLevel 验证未知/空事件级别的处理。
// 未知级别在 map 中查不到会得到 0(=Debug 优先级), 当阈值为 Info(=1) 时应被丢弃。
// 这是一个潜在的边界陷阱: 未知级别被静默当作最低优先级。
func TestShouldLog_UnknownEventLevel(t *testing.T) {
	l := NewAuditLogger(WithAuditLevel(LevelInfo))
	// 空级别 -> map 取值 0 < 1 -> 丢弃
	if l.shouldLog(AuditLevel("")) {
		t.Error("空级别在 Info 阈值下应被丢弃 (映射为 0)")
	}
	if l.shouldLog(AuditLevel("不存在的级别")) {
		t.Error("未知级别在 Info 阈值下应被丢弃 (映射为 0)")
	}
}

// TestShouldLog_UnknownConfigLevel 验证当配置的 LogLevel 为未知值时的行为。
// 配置级别未知 -> 映射为 0 -> 所有事件(含 debug)都会被记录。
func TestShouldLog_UnknownConfigLevel(t *testing.T) {
	l := NewAuditLogger(WithAuditLevel(AuditLevel("garbage")))
	if !l.shouldLog(LevelDebug) {
		t.Error("配置级别未知时映射为 0, debug 事件应记录")
	}
}

// ============================================================
// 脱敏逻辑 sanitize / sanitizeMap / isSensitive
// ============================================================

// TestSanitize_TopLevelDetails 验证顶层 Details 中的敏感字段被脱敏。
func TestSanitize_TopLevelDetails(t *testing.T) {
	l := NewAuditLogger()
	evt := &AuditEvent{
		Details: map[string]any{
			"password": "secret123",
			"username": "alice",
		},
	}
	l.sanitize(evt)
	if evt.Details["password"] != "***REDACTED***" {
		t.Errorf("password 应被脱敏, 实际=%v", evt.Details["password"])
	}
	if evt.Details["username"] != "alice" {
		t.Errorf("非敏感字段不应改变, 实际=%v", evt.Details["username"])
	}
}

// TestSanitize_NestedMap 验证嵌套 map 中的敏感字段也被脱敏。
func TestSanitize_NestedMap(t *testing.T) {
	l := NewAuditLogger()
	evt := &AuditEvent{
		Details: map[string]any{
			"auth": map[string]any{
				"token": "abc",
				"user":  "bob",
			},
		},
	}
	l.sanitize(evt)
	nested := evt.Details["auth"].(map[string]any)
	if nested["token"] != "***REDACTED***" {
		t.Errorf("嵌套 token 应被脱敏, 实际=%v", nested["token"])
	}
}

// TestSanitize_MetadataNotSanitized 验证 Metadata 中的敏感字段被脱敏。
// 回归: W3-47 sanitize 必须同时处理 Metadata(与 Details 一致), 否则放入 Metadata 的敏感数据将原样泄漏。
func TestSanitize_MetadataNotSanitized(t *testing.T) {
	l := NewAuditLogger()
	evt := &AuditEvent{
		Metadata: map[string]any{
			"password": "leaked-secret",
			"user":     "alice",
		},
	}
	l.sanitize(evt)
	// 回归: W3-47 Metadata 中的敏感字段必须被脱敏
	if evt.Metadata["password"] != "***REDACTED***" {
		t.Errorf("Metadata 中的 password 应被脱敏, 实际=%v (潜在数据泄漏)", evt.Metadata["password"])
	}
	if evt.Metadata["user"] != "alice" {
		t.Errorf("Metadata 中的非敏感字段不应改变, 实际=%v", evt.Metadata["user"])
	}
}

// TestSanitize_SliceOfMapsNotSanitized 验证 slice 内 map 的敏感字段被脱敏。
// 回归: W3-48 sanitizeMap 必须递归处理 []any / []map[string]any 内的元素, 否则 slice 中的敏感字段泄漏。
func TestSanitize_SliceOfMapsNotSanitized(t *testing.T) {
	l := NewAuditLogger()
	evt := &AuditEvent{
		Details: map[string]any{
			"items": []any{
				map[string]any{"password": "in-slice"},
			},
			"creds": []map[string]any{
				{"token": "tok-in-typed-slice", "name": "keep"},
			},
		},
	}
	l.sanitize(evt)
	// 回归: W3-48 []any 内 map 的敏感字段必须被脱敏
	items := evt.Details["items"].([]any)
	first := items[0].(map[string]any)
	if first["password"] != "***REDACTED***" {
		t.Errorf("slice 内 map 的 password 应被脱敏, 实际=%v (潜在数据泄漏)", first["password"])
	}
	// 回归: W3-48 []map[string]any 内的敏感字段也必须被脱敏
	creds := evt.Details["creds"].([]any)
	cred0 := creds[0].(map[string]any)
	if cred0["token"] != "***REDACTED***" {
		t.Errorf("[]map 内 token 应被脱敏, 实际=%v", cred0["token"])
	}
	if cred0["name"] != "keep" {
		t.Errorf("[]map 内非敏感字段不应改变, 实际=%v", cred0["name"])
	}
}

// TestIsSensitive_CaseAndSubstring 验证 isSensitive 的大小写不敏感 + 包含匹配。
// 回归: W3-49 敏感字段匹配应大小写不敏感且匹配常见变体(子串包含), 而非小写精确匹配,
// 否则 Password / user_password / apikey 等变体会泄漏。
func TestIsSensitive_CaseAndSubstring(t *testing.T) {
	l := NewAuditLogger()
	cases := []struct {
		field     string
		sensitive bool
		note      string
	}{
		{"password", true, "精确小写匹配"},
		{"Password", true, "大小写不敏感匹配"},
		{"PASSWORD", true, "全大写匹配"},
		{"user_password", true, "子串包含匹配"},
		{"api_key", true, "精确匹配"},
		{"apikey", true, "无下划线变体匹配 (apikey 包含 api_key 变体)"},
		{"ApiKey", true, "驼峰变体匹配"},
		{"username", false, "非敏感字段不匹配"},
		{"id", false, "短非敏感字段不匹配"},
	}
	for _, c := range cases {
		got := l.isSensitive(c.field)
		if got != c.sensitive {
			t.Errorf("isSensitive(%q)=%v, want %v (%s)", c.field, got, c.sensitive, c.note)
		}
	}
}

// TestSanitize_RequestResponseBody 验证 Request.Body 与 Response.Body 的脱敏。
func TestSanitize_RequestResponseBody(t *testing.T) {
	l := NewAuditLogger()
	evt := &AuditEvent{
		Request:  &RequestInfo{Body: map[string]any{"secret": "s1", "ok": "v"}},
		Response: &ResponseInfo{Body: map[string]any{"token": "t1", "ok": "v"}},
	}
	l.sanitize(evt)
	rb := evt.Request.Body.(map[string]any)
	if rb["secret"] != "***REDACTED***" {
		t.Errorf("Request.Body secret 应脱敏, 实际=%v", rb["secret"])
	}
	rsp := evt.Response.Body.(map[string]any)
	if rsp["token"] != "***REDACTED***" {
		t.Errorf("Response.Body token 应脱敏, 实际=%v", rsp["token"])
	}
}

// ============================================================
// Log 主流程: 默认值、禁用、过滤器、钩子
// ============================================================

// TestLog_DisabledNoEffect 验证禁用时 Log 不产生任何记录。
func TestLog_DisabledNoEffect(t *testing.T) {
	store := NewMemoryAuditStore()
	l := NewAuditLogger(WithAuditEnabled(false))
	l.SetStore(store)
	var buf bytes.Buffer
	// 替换 writers 为 buffer (清除默认 stdout)
	l.writers = []io.Writer{&buf}

	l.Log(&AuditEvent{Level: LevelError, Action: "x"})

	if buf.Len() != 0 {
		t.Errorf("禁用时不应写入 writer, 实际写入=%q", buf.String())
	}
	cnt, _ := store.Count(context.Background(), AuditQuery{})
	if cnt != 0 {
		t.Errorf("禁用时不应写入 store, count=%d", cnt)
	}
}

// TestLog_FillsDefaults 验证 Log 自动填充 ID 与 Timestamp。
// 此处不 Start, 走"已停止 -> 直接同步写入"路径, 便于同步断言。
func TestLog_FillsDefaults(t *testing.T) {
	l := NewAuditLogger()
	var buf bytes.Buffer
	l.writers = []io.Writer{&buf}

	evt := &AuditEvent{Level: LevelInfo, Action: "act"}
	l.Log(evt)

	if evt.ID == "" {
		t.Error("Log 应自动填充 ID")
	}
	if !strings.HasPrefix(evt.ID, "audit-") {
		t.Errorf("ID 应带 audit- 前缀, 实际=%q", evt.ID)
	}
	if evt.Timestamp.IsZero() {
		t.Error("Log 应自动填充 Timestamp")
	}
}

// TestLog_FilterRejects 验证过滤器返回 false 时事件被丢弃。
func TestLog_FilterRejects(t *testing.T) {
	l := NewAuditLogger()
	var buf bytes.Buffer
	l.writers = []io.Writer{&buf}
	// 只允许 Tool 类别
	l.AddFilter(FilterByCategory(CategoryTool))

	l.Log(&AuditEvent{Level: LevelInfo, Category: CategoryAgent, Action: "x"})
	if buf.Len() != 0 {
		t.Errorf("被过滤事件不应写入, 实际=%q", buf.String())
	}

	l.Log(&AuditEvent{Level: LevelInfo, Category: CategoryTool, Action: "y"})
	if buf.Len() == 0 {
		t.Error("未被过滤事件应写入")
	}
}

// TestLog_HookInvoked 验证钩子被调用且能观察到事件。
func TestLog_HookInvoked(t *testing.T) {
	l := NewAuditLogger()
	var buf bytes.Buffer
	l.writers = []io.Writer{&buf}

	var got *AuditEvent
	l.AddHook(func(e *AuditEvent) { got = e })

	l.Log(&AuditEvent{Level: LevelInfo, Action: "hooked"})
	if got == nil || got.Action != "hooked" {
		t.Errorf("钩子应观察到事件, got=%v", got)
	}
}

// TestLog_HookSeesRedactedDetails 验证钩子在脱敏之后被调用(顺序一致性)。
// Log 中 sanitize 在 hook 之前执行, 钩子应看到已脱敏的数据。
func TestLog_HookSeesRedactedDetails(t *testing.T) {
	l := NewAuditLogger()
	var buf bytes.Buffer
	l.writers = []io.Writer{&buf}

	var seen any
	l.AddHook(func(e *AuditEvent) {
		if e.Details != nil {
			seen = e.Details["password"]
		}
	})
	l.Log(&AuditEvent{Level: LevelInfo, Action: "x", Details: map[string]any{"password": "p"}})

	if seen != "***REDACTED***" {
		t.Errorf("钩子应看到已脱敏的 password, 实际=%v", seen)
	}
}

// ============================================================
// 直接写入路径 (未 Start 或溢出) 的 JSON 正确性
// ============================================================

// TestWriteEventDirect_JSONValid 验证直接写入路径产生合法 JSON + 换行。
func TestWriteEventDirect_JSONValid(t *testing.T) {
	l := NewAuditLogger()
	var buf bytes.Buffer
	l.writers = []io.Writer{&buf}

	l.Log(&AuditEvent{Level: LevelInfo, Action: "json-test", Category: CategoryAgent})

	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Error("每条事件应以换行结尾")
	}
	var decoded AuditEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &decoded); err != nil {
		t.Fatalf("写入内容应为合法 JSON: %v\n原文=%q", err, out)
	}
	if decoded.Action != "json-test" {
		t.Errorf("解析后 Action 不符, 实际=%q", decoded.Action)
	}
}

// ============================================================
// 缓冲与异步处理: Start/Stop/processLoop
// ============================================================

// TestStartStop_FlushesBufferedEvents 验证异步路径下事件最终落入 store。
func TestStartStop_FlushesBufferedEvents(t *testing.T) {
	store := NewMemoryAuditStore()
	l := NewAuditLogger()
	l.SetStore(store)
	var buf bytes.Buffer
	l.writers = []io.Writer{&buf}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Start(ctx)

	for i := 0; i < 10; i++ {
		l.Log(&AuditEvent{Level: LevelInfo, Action: "async"})
	}

	// 轮询等待异步刷新(基于 FlushInterval 或 batch)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		cnt, _ := store.Count(context.Background(), AuditQuery{})
		if cnt >= 10 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cnt, _ := store.Count(context.Background(), AuditQuery{})
	if cnt != 10 {
		t.Errorf("异步事件应全部落入 store, 期望 10, 实际 %d", cnt)
	}
}

// TestStop_Idempotent 验证 Stop 重复调用不 panic (第二次应安全 no-op)。
func TestStop_Idempotent(t *testing.T) {
	l := NewAuditLogger()
	ctx := context.Background()
	l.Start(ctx)
	l.Stop()
	// 第二次 Stop 不应 panic / 重复 close
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("重复 Stop 不应 panic: %v", r)
		}
	}()
	l.Stop()
}

// TestStart_Idempotent 验证 Start 重复调用安全 (第二次应 no-op)。
func TestStart_Idempotent(t *testing.T) {
	l := NewAuditLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Start(ctx)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("重复 Start 不应 panic: %v", r)
		}
	}()
	l.Start(ctx)
	l.Stop()
}

// TestLog_AfterStop_DirectWrite 验证 Stop 后 Log 走直接写入路径(不向已关闭 channel 发送)。
// 这是 TOCTOU 防护的关键: 若实现错误会 panic("send on closed channel")。
func TestLog_AfterStop_DirectWrite(t *testing.T) {
	store := NewMemoryAuditStore()
	l := NewAuditLogger()
	l.SetStore(store)
	var buf bytes.Buffer
	l.writers = []io.Writer{&buf}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Start(ctx)
	l.Stop()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Stop 后 Log 不应 panic: %v", r)
		}
	}()
	l.Log(&AuditEvent{Level: LevelInfo, Action: "after-stop"})

	// 走直接写入路径, writer 应立即收到
	if buf.Len() == 0 {
		t.Error("Stop 后 Log 应同步写入 writer")
	}
}

// TestBufferOverflow_DirectStoreWrite 验证缓冲区满时事件不丢失(同步写 store)。
// 不 Start (无消费者), 缓冲区大小设为 2, 写入超过容量的事件,
// 超量事件应通过同步路径直接写 store, 且 GetBufferOverflowCount 计数增加。
func TestBufferOverflow_DirectStoreWrite(t *testing.T) {
	store := NewMemoryAuditStore()
	// 这里需要在 running=true 时才会进入缓冲区分支, 但不消费 -> 触发 default 溢出。
	l := NewAuditLogger()
	l.SetStore(store)
	var buf bytes.Buffer
	l.writers = []io.Writer{&buf}
	// 手动构造小缓冲并标记 running, 但不启动消费者 goroutine。
	l.buffer = make(chan *AuditEvent, 2)
	l.mu.Lock()
	l.running = true
	l.mu.Unlock()

	total := 6
	for i := 0; i < total; i++ {
		l.Log(&AuditEvent{Level: LevelInfo, Action: "overflow"})
	}

	overflow := l.GetBufferOverflowCount()
	if overflow == 0 {
		t.Error("缓冲区满时应记录溢出计数 > 0")
	}
	// 溢出的事件应被同步写入 store
	cnt, _ := store.Count(context.Background(), AuditQuery{})
	if int64(cnt) != overflow {
		t.Logf("溢出事件直接写 store: overflow=%d, store_count=%d", overflow, cnt)
	}
	if cnt == 0 {
		t.Error("溢出事件应同步写入 store, 不应丢失")
	}

	// 清理: 标记停止避免 close 已被替换的 channel 的混乱
	l.mu.Lock()
	l.running = false
	l.mu.Unlock()
}

// TestStoreFailCount_Getter 验证 storeFailCount 暴露 Getter(与 bufferOverflowCount 对称)。
// 回归: W3-50 store.Save 失败计数必须可读, 否则无法监控审计落库丢失。
func TestStoreFailCount_Getter(t *testing.T) {
	l := NewAuditLogger()
	l.SetStore(&failingAuditStore{})
	l.writers = []io.Writer{&safeWriter{}}
	// 标记 running 并使用零容量 channel, 强制走溢出同步写 store 路径(store.Save 会失败)。
	l.buffer = make(chan *AuditEvent, 1)
	l.mu.Lock()
	l.running = true
	l.mu.Unlock()

	// 初始计数应为 0
	if l.GetStoreFailCount() != 0 {
		t.Errorf("初始 storeFailCount 应为 0, 实际 %d", l.GetStoreFailCount())
	}

	total := 5
	for i := 0; i < total; i++ {
		l.Log(&AuditEvent{Level: LevelInfo, Action: "fail"})
	}

	// 至少有溢出事件触发 store.Save 失败, 计数应 > 0
	if l.GetStoreFailCount() == 0 {
		t.Error("store.Save 失败时 GetStoreFailCount 应 > 0")
	}

	l.mu.Lock()
	l.running = false
	l.mu.Unlock()
}

// failingAuditStore 是 Save 始终失败的存储, 用于测试 storeFailCount。
type failingAuditStore struct{}

func (s *failingAuditStore) Save(ctx context.Context, event *AuditEvent) error {
	return context.DeadlineExceeded
}
func (s *failingAuditStore) Query(ctx context.Context, query AuditQuery) ([]*AuditEvent, error) {
	return nil, nil
}
func (s *failingAuditStore) Count(ctx context.Context, query AuditQuery) (int64, error) {
	return 0, nil
}
func (s *failingAuditStore) Delete(ctx context.Context, ids []string) error { return nil }
func (s *failingAuditStore) Cleanup(ctx context.Context, before time.Time) (int64, error) {
	return 0, nil
}

// ============================================================
// 并发竞态
// ============================================================

// TestLog_ConcurrentDirectWrite 并发调用 Log(未 Start, 走直接写入)检测竞态。
// 配合 -race 运行可暴露 writers/store 上的数据竞争。
func TestLog_ConcurrentDirectWrite(t *testing.T) {
	store := NewMemoryAuditStore()
	l := NewAuditLogger()
	l.SetStore(store)
	l.writers = []io.Writer{&safeWriter{}}

	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			l.Log(&AuditEvent{Level: LevelInfo, Action: "concurrent"})
		}()
	}
	wg.Wait()
}

// TestConcurrent_AddAndLog 并发添加 filter/hook/writer 同时 Log, 检测锁保护是否完整。
func TestConcurrent_AddAndLog(t *testing.T) {
	l := NewAuditLogger()
	l.writers = []io.Writer{&safeWriter{}}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			l.AddHook(func(*AuditEvent) {})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			l.AddFilter(func(*AuditEvent) bool { return true })
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			l.Log(&AuditEvent{Level: LevelInfo, Action: "race"})
		}
	}()
	wg.Wait()
}

// TestMemoryStore_ConcurrentSaveQuery 并发 Save + Query 检测 store 锁。
func TestMemoryStore_ConcurrentSaveQuery(t *testing.T) {
	store := NewMemoryAuditStore()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			store.Save(context.Background(), &AuditEvent{ID: "x", Timestamp: time.Now()})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			store.Query(context.Background(), AuditQuery{})
		}
	}()
	wg.Wait()
}

// ============================================================
// MemoryAuditStore 查询 / 分页 / 时间边界
// ============================================================

// TestQuery_Pagination_OffsetLimit 验证分页 offset/limit 组合。
func TestQuery_Pagination_OffsetLimit(t *testing.T) {
	store := NewMemoryAuditStore()
	for i := 0; i < 10; i++ {
		store.Save(context.Background(), &AuditEvent{ID: string(rune('a' + i)), Timestamp: time.Now()})
	}
	// offset=2, limit=3 -> 期望返回 3 条
	res, _ := store.Query(context.Background(), AuditQuery{Offset: 2, Limit: 3})
	if len(res) != 3 {
		t.Errorf("offset=2 limit=3 应返回 3 条, 实际 %d", len(res))
	}
	if res[0].ID != "c" {
		t.Errorf("offset=2 首条应为 'c', 实际 %q", res[0].ID)
	}
}

// TestQuery_OffsetBeyondLen 暴露分页边界缺陷:
// 当 Offset >= len(results) 时, 条件 `query.Offset < len(results)` 为假, offset 被忽略,
// 导致返回全部结果而非空集合。这是分页越界时的反直觉行为。
func TestQuery_OffsetBeyondLen(t *testing.T) {
	store := NewMemoryAuditStore()
	for i := 0; i < 3; i++ {
		store.Save(context.Background(), &AuditEvent{ID: string(rune('a' + i)), Timestamp: time.Now()})
	}
	// 共 3 条, offset=5 越界
	res, _ := store.Query(context.Background(), AuditQuery{Offset: 5})
	if len(res) != 0 {
		t.Errorf("offset 越界应返回空集合, 实际返回 %d 条 (越界 offset 被静默忽略, 分页逻辑缺陷)", len(res))
	}
}

// TestQuery_OffsetEqualsLen 暴露分页边界:
// offset 恰好等于结果数时, 条件 `offset < len` 为假, offset 被忽略返回全部。
func TestQuery_OffsetEqualsLen(t *testing.T) {
	store := NewMemoryAuditStore()
	for i := 0; i < 3; i++ {
		store.Save(context.Background(), &AuditEvent{ID: string(rune('a' + i)), Timestamp: time.Now()})
	}
	res, _ := store.Query(context.Background(), AuditQuery{Offset: 3})
	if len(res) != 0 {
		t.Errorf("offset==len 应返回空, 实际 %d 条 (边界忽略缺陷)", len(res))
	}
}

// TestQuery_LimitLargerThanLen 验证 limit 大于结果数时返回全部。
func TestQuery_LimitLargerThanLen(t *testing.T) {
	store := NewMemoryAuditStore()
	for i := 0; i < 3; i++ {
		store.Save(context.Background(), &AuditEvent{ID: string(rune('a' + i)), Timestamp: time.Now()})
	}
	res, _ := store.Query(context.Background(), AuditQuery{Limit: 100})
	if len(res) != 3 {
		t.Errorf("limit 超过总数应返回全部 3 条, 实际 %d", len(res))
	}
}

// TestQuery_TimeBoundaries 验证时间范围边界(Before/After 为严格不等)。
// StartTime 用 Before 比较: event 恰好等于 StartTime 不会被过滤 -> 包含。
// EndTime 用 After 比较: event 恰好等于 EndTime 不会被过滤 -> 包含。
func TestQuery_TimeBoundaries(t *testing.T) {
	store := NewMemoryAuditStore()
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	store.Save(context.Background(), &AuditEvent{ID: "at-start", Timestamp: base})
	store.Save(context.Background(), &AuditEvent{ID: "middle", Timestamp: base.Add(time.Hour)})
	store.Save(context.Background(), &AuditEvent{ID: "at-end", Timestamp: base.Add(2 * time.Hour)})

	res, _ := store.Query(context.Background(), AuditQuery{
		StartTime: base,
		EndTime:   base.Add(2 * time.Hour),
	})
	// 三条均在 [base, base+2h] 闭区间内
	if len(res) != 3 {
		t.Errorf("闭区间应包含边界事件, 期望 3, 实际 %d", len(res))
	}
}

// TestQuery_ActorTargetTraceFilters 验证 actor/target/trace/result 过滤。
func TestQuery_ActorTargetTraceFilters(t *testing.T) {
	store := NewMemoryAuditStore()
	store.Save(context.Background(), &AuditEvent{
		ID: "1", Timestamp: time.Now(),
		Actor:  &Actor{ID: "actor-A"},
		Target: &Target{ID: "target-X"},
		Result: ResultSuccess, TraceID: "tr-1",
	})
	store.Save(context.Background(), &AuditEvent{
		ID: "2", Timestamp: time.Now(),
		Actor:  &Actor{ID: "actor-B"},
		Result: ResultFailure, TraceID: "tr-2",
	})

	if r, _ := store.Query(context.Background(), AuditQuery{ActorID: "actor-A"}); len(r) != 1 {
		t.Errorf("ActorID 过滤期望 1, 实际 %d", len(r))
	}
	if r, _ := store.Query(context.Background(), AuditQuery{TargetID: "target-X"}); len(r) != 1 {
		t.Errorf("TargetID 过滤期望 1, 实际 %d", len(r))
	}
	if r, _ := store.Query(context.Background(), AuditQuery{TraceID: "tr-2"}); len(r) != 1 {
		t.Errorf("TraceID 过滤期望 1, 实际 %d", len(r))
	}
	if r, _ := store.Query(context.Background(), AuditQuery{Result: ResultFailure}); len(r) != 1 {
		t.Errorf("Result 过滤期望 1, 实际 %d", len(r))
	}
	// nil Actor 但查询 ActorID 应被排除
	if r, _ := store.Query(context.Background(), AuditQuery{TargetID: "target-X", ActorID: "actor-A"}); len(r) != 1 {
		t.Errorf("组合过滤期望 1, 实际 %d", len(r))
	}
}

// TestCleanup_BoundaryEvent 暴露 Cleanup 边界:
// Cleanup 保留 Timestamp.After(before) 的事件; 恰好等于 before 的事件会被删除(After 为严格大于)。
func TestCleanup_BoundaryEvent(t *testing.T) {
	store := NewMemoryAuditStore()
	before := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	store.Save(context.Background(), &AuditEvent{ID: "exact", Timestamp: before})
	store.Save(context.Background(), &AuditEvent{ID: "after", Timestamp: before.Add(time.Second)})

	deleted, _ := store.Cleanup(context.Background(), before)
	// exact(==before) 不满足 After -> 被删除; after 保留
	if deleted != 1 {
		t.Errorf("边界事件(==before)应被清理, 期望删除 1, 实际 %d", deleted)
	}
	cnt, _ := store.Count(context.Background(), AuditQuery{})
	if cnt != 1 {
		t.Errorf("清理后应剩 1 条, 实际 %d", cnt)
	}
}

// TestDelete_NonexistentID 验证删除不存在的 ID 不影响其他事件。
func TestDelete_NonexistentID(t *testing.T) {
	store := NewMemoryAuditStore()
	store.Save(context.Background(), &AuditEvent{ID: "keep"})
	if err := store.Delete(context.Background(), []string{"ghost"}); err != nil {
		t.Fatalf("删除不存在 ID 不应报错: %v", err)
	}
	cnt, _ := store.Count(context.Background(), AuditQuery{})
	if cnt != 1 {
		t.Errorf("删除幽灵 ID 后应仍剩 1 条, 实际 %d", cnt)
	}
}

// TestDelete_EmptyAndNil 验证空/nil ID 列表是安全 no-op。
func TestDelete_EmptyAndNil(t *testing.T) {
	store := NewMemoryAuditStore()
	store.Save(context.Background(), &AuditEvent{ID: "a"})
	_ = store.Delete(context.Background(), nil)
	_ = store.Delete(context.Background(), []string{})
	cnt, _ := store.Count(context.Background(), AuditQuery{})
	if cnt != 1 {
		t.Errorf("空删除后应仍剩 1 条, 实际 %d", cnt)
	}
}

// ============================================================
// 便捷日志方法 LogAuth / LogTool / LogLLM / LogError 等
// ============================================================

// waitForCount 轮询等待 store 达到期望事件数(异步刷新), 超时则返回当前值。
func waitForCount(store *MemoryAuditStore, want int, timeout time.Duration) int64 {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cnt, _ := store.Count(context.Background(), AuditQuery{})
		if cnt >= int64(want) {
			return cnt
		}
		time.Sleep(20 * time.Millisecond)
	}
	cnt, _ := store.Count(context.Background(), AuditQuery{})
	return cnt
}

// TestConvenienceLoggers 验证各便捷方法正确构造事件并落入 store。
// 这些方法默认级别为 Info, 因此 logger 需用 Debug 阈值确保全部记录。
// 注意: 事件落入 store 依赖 Start() 后的异步 flush; 未 Start 时只写 writers。
func TestConvenienceLoggers(t *testing.T) {
	store := NewMemoryAuditStore()
	l := NewAuditLogger(WithAuditLevel(LevelDebug))
	l.SetStore(store)
	l.writers = []io.Writer{&safeWriter{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Start(ctx)
	defer l.Stop()

	actor := UserActor("u1", "User1", []string{"admin"})

	l.LogAuth(actor, "login", ResultSuccess, map[string]any{"method": "oauth"})
	l.LogTool(actor, "calculator", ResultSuccess, time.Millisecond, nil)
	l.LogLLM(actor, "openai", "gpt-4", ResultSuccess, time.Second, map[string]int{"total": 100})
	l.LogSecurity(LevelCritical, actor, "intrusion", ResultDenied, nil)
	l.LogAgent(actor, &Target{Type: "agent", ID: "a1"}, "run", ResultSuccess, time.Second, nil)

	cnt := waitForCount(store, 5, 8*time.Second)
	if cnt != 5 {
		t.Errorf("5 个便捷方法应产生 5 条记录, 实际 %d", cnt)
	}

	// 验证 LogTool 的 target 构造
	res, _ := store.Query(context.Background(), AuditQuery{Categories: []EventCategory{CategoryTool}})
	if len(res) != 1 || res[0].Target == nil || res[0].Target.ID != "calculator" {
		t.Errorf("LogTool 应构造 target.ID=calculator, 实际=%+v", res)
	}

	// 验证 LogLLM 的 details
	llmRes, _ := store.Query(context.Background(), AuditQuery{Categories: []EventCategory{CategoryLLM}})
	if len(llmRes) != 1 {
		t.Fatalf("应有 1 条 LLM 记录, 实际 %d", len(llmRes))
	}
	if llmRes[0].Target.Name != "gpt-4" {
		t.Errorf("LogLLM target.Name 应为 model=gpt-4, 实际=%q", llmRes[0].Target.Name)
	}
}

// TestLogError_NilDetails 验证 LogError 在 details 为 nil 时初始化并写入 error 字段。
func TestLogError_NilDetails(t *testing.T) {
	store := NewMemoryAuditStore()
	l := NewAuditLogger(WithAuditLevel(LevelDebug))
	l.SetStore(store)
	l.writers = []io.Writer{&safeWriter{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Start(ctx)
	defer l.Stop()

	l.LogError(CategoryTool, SystemActor(), "boom", context.DeadlineExceeded, nil)

	waitForCount(store, 1, 8*time.Second)
	res, _ := store.Query(context.Background(), AuditQuery{Categories: []EventCategory{CategoryTool}})
	if len(res) != 1 {
		t.Fatalf("应有 1 条错误记录, 实际 %d", len(res))
	}
	if res[0].Level != LevelError || res[0].Result != ResultError {
		t.Errorf("LogError 应为 Error 级别/结果, 实际 level=%s result=%s", res[0].Level, res[0].Result)
	}
	if res[0].Details["error"] == nil {
		t.Error("LogError 应写入 details[error]")
	}
}

// TestLogError_MutatesCallerMap 暴露副作用:
// LogError 直接向传入的 details map 写入 "error" 键, 会修改调用方的 map (而非拷贝)。
func TestLogError_MutatesCallerMap(t *testing.T) {
	l := NewAuditLogger(WithAuditLevel(LevelDebug))
	l.writers = []io.Writer{&safeWriter{}}

	caller := map[string]any{"foo": "bar"}
	l.LogError(CategoryTool, SystemActor(), "x", context.Canceled, caller)

	if _, ok := caller["error"]; ok {
		t.Logf("注意: LogError 修改了调用方传入的 details map (写入了 error 键), 存在副作用")
	}
}

// ============================================================
// Context helpers
// ============================================================

// TestLogFromContext_NoLogger 验证 context 中无 logger 时 LogFromContext 安全 no-op。
func TestLogFromContext_NoLogger(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("无 logger 时不应 panic: %v", r)
		}
	}()
	LogFromContext(context.Background(), &AuditEvent{Action: "x"})
}

// TestLogFromContext_WithLogger 验证 context 中有 logger 时事件被记录。
func TestLogFromContext_WithLogger(t *testing.T) {
	store := NewMemoryAuditStore()
	l := NewAuditLogger()
	l.SetStore(store)
	l.writers = []io.Writer{&safeWriter{}}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Start(runCtx)
	defer l.Stop()

	ctx := ContextWithAuditLogger(context.Background(), l)
	LogFromContext(ctx, &AuditEvent{Level: LevelInfo, Action: "ctx-log"})

	cnt := waitForCount(store, 1, 8*time.Second)
	if cnt != 1 {
		t.Errorf("LogFromContext 应记录 1 条, 实际 %d", cnt)
	}
}

// TestLog_StoppedSkipsStore 验证未 Start 时 Log 仍落库 store。
// 回归: W3-46 当 logger 未 Start (running=false) 时, Log 走 writeEventDirect,
// 该路径必须同时写 writers 与 store(与缓冲区溢出路径一致), 否则默认未启动的 logger
// 调用 Log 时审计事件不会进入持久化 store, 存在审计落库缺口。
func TestLog_StoppedSkipsStore(t *testing.T) {
	store := NewMemoryAuditStore()
	l := NewAuditLogger() // 未 Start, running=false
	l.SetStore(store)
	l.writers = []io.Writer{&safeWriter{}}

	l.Log(&AuditEvent{Level: LevelInfo, Action: "no-store"})

	// 回归: W3-46 未 Start 时事件必须落库 store
	cnt, _ := store.Count(context.Background(), AuditQuery{})
	if cnt != 1 {
		t.Errorf("未 Start (running=false) 时 Log 应同步写入 store, 期望 1, 实际 %d (审计落库缺口)", cnt)
	}
}

// ============================================================
// GenerateReport
// ============================================================

// TestGenerateReport_Aggregation 验证报告聚合统计正确。
func TestGenerateReport_Aggregation(t *testing.T) {
	store := NewMemoryAuditStore()
	l := NewAuditLogger()
	l.SetStore(store)

	now := time.Now()
	events := []*AuditEvent{
		{ID: "1", Timestamp: now, Category: CategoryAgent, Level: LevelInfo, Result: ResultSuccess, Action: "run", Actor: &Actor{ID: "u1", Name: "U1"}},
		{ID: "2", Timestamp: now, Category: CategoryAgent, Level: LevelError, Result: ResultError, Action: "run", Actor: &Actor{ID: "u1", Name: "U1"}},
		{ID: "3", Timestamp: now, Category: CategoryTool, Level: LevelCritical, Result: ResultFailure, Action: "execute", Actor: &Actor{ID: "u2", Name: "U2"}},
	}
	for _, e := range events {
		store.Save(context.Background(), e)
	}

	report, err := l.GenerateReport(context.Background(), now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("生成报告失败: %v", err)
	}
	if report.TotalEvents != 3 {
		t.Errorf("总事件数应为 3, 实际 %d", report.TotalEvents)
	}
	if report.ByCategory[CategoryAgent] != 2 {
		t.Errorf("Agent 类别应为 2, 实际 %d", report.ByCategory[CategoryAgent])
	}
	if report.ByResult[ResultSuccess] != 1 {
		t.Errorf("Success 结果应为 1, 实际 %d", report.ByResult[ResultSuccess])
	}
	// error + critical 应进入 Errors
	if len(report.Errors) != 2 {
		t.Errorf("Errors 应含 2 条(error+critical), 实际 %d", len(report.Errors))
	}
	// 两个不同 actor
	if len(report.TopActors) != 2 {
		t.Errorf("TopActors 应有 2 个, 实际 %d", len(report.TopActors))
	}
	// u1 的 count 应为 2
	for _, a := range report.TopActors {
		if a.ActorID == "u1" && a.Count != 2 {
			t.Errorf("u1 count 应为 2, 实际 %d", a.Count)
		}
	}
}

// TestGenerateReport_TopNotSortedOrLimited 验证 TopActions/TopActors 按频次降序排序并取 Top-N。
// 回归: W3-51 字段命名为 TopActors/TopActions, 必须按计数降序排列并截断到 TopN,
// 否则 "Top" 语义不闭环(实际无序全量)。
func TestGenerateReport_TopNotSortedOrLimited(t *testing.T) {
	store := NewMemoryAuditStore()
	l := NewAuditLogger()
	l.SetStore(store)

	now := time.Now()
	// 构造 5 个不同 action, 频次不同
	freq := map[string]int{"a": 1, "b": 5, "c": 3, "d": 2, "e": 4}
	id := 0
	for action, n := range freq {
		for i := 0; i < n; i++ {
			id++
			store.Save(context.Background(), &AuditEvent{
				ID: string(rune(id)), Timestamp: now, Action: action,
				Actor: &Actor{ID: "u" + action},
			})
		}
	}

	report, _ := l.GenerateReport(context.Background(), now.Add(-time.Hour), now.Add(time.Hour))

	// 5 个 action 未超过 TopN 上限, 应全部出现
	if len(report.TopActions) != 5 {
		t.Errorf("TopActions 应含全部 5 个 action, 实际 %d", len(report.TopActions))
	}
	// 回归: W3-51 TopActions 必须按 count 严格降序排列
	for i := 1; i < len(report.TopActions); i++ {
		if report.TopActions[i-1].Count < report.TopActions[i].Count {
			t.Errorf("TopActions 应按频次降序排列, 实际 [%d].Count=%d < [%d].Count=%d, 顺序=%v",
				i-1, report.TopActions[i-1].Count, i, report.TopActions[i].Count, report.TopActions)
		}
	}
	// 频次最高的 action b(5 次) 应排第一
	if len(report.TopActions) > 0 && report.TopActions[0].Action != "b" {
		t.Errorf("TopActions 首位应为频次最高的 b, 实际 %q", report.TopActions[0].Action)
	}
	// 回归: W3-51 TopActors 也必须按 count 降序排列
	for i := 1; i < len(report.TopActors); i++ {
		if report.TopActors[i-1].Count < report.TopActors[i].Count {
			t.Errorf("TopActors 应按频次降序排列, 实际 [%d].Count=%d < [%d].Count=%d",
				i-1, report.TopActors[i-1].Count, i, report.TopActors[i].Count)
		}
	}
}

// TestGenerateReport_TopNLimit 验证 TopActions/TopActors 截断到 TopN 上限。
// 回归: W3-51 当不同 action/actor 数量超过 TopN 时, 应只保留计数最高的前 N 个。
func TestGenerateReport_TopNLimit(t *testing.T) {
	store := NewMemoryAuditStore()
	l := NewAuditLogger()
	l.SetStore(store)

	now := time.Now()
	// 构造 20 个不同 action / actor, 频次递增, 超过 TopN(10) 上限
	for i := 0; i < 20; i++ {
		action := "act-" + string(rune('A'+i))
		actorID := "actor-" + string(rune('A'+i))
		for j := 0; j <= i; j++ { // 第 i 个 action 出现 i+1 次
			store.Save(context.Background(), &AuditEvent{
				ID: string(rune(i*100 + j)), Timestamp: now, Action: action,
				Actor: &Actor{ID: actorID},
			})
		}
	}

	report, _ := l.GenerateReport(context.Background(), now.Add(-time.Hour), now.Add(time.Hour))

	// 回归: W3-51 截断到 TopN(=10)
	if len(report.TopActions) != topReportN {
		t.Errorf("TopActions 应截断到 %d 个, 实际 %d", topReportN, len(report.TopActions))
	}
	if len(report.TopActors) != topReportN {
		t.Errorf("TopActors 应截断到 %d 个, 实际 %d", topReportN, len(report.TopActors))
	}
	// 截断后应保留频次最高的(第 20 个 action 出现 20 次), 排第一
	if len(report.TopActions) > 0 && report.TopActions[0].Count != 20 {
		t.Errorf("截断后首位应为频次最高(20), 实际 %d", report.TopActions[0].Count)
	}
}

// TestGenerateReport_EmptyStore 验证空 store 生成空报告(不 panic)。
func TestGenerateReport_EmptyStore(t *testing.T) {
	l := NewAuditLogger()
	report, err := l.GenerateReport(context.Background(), time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("空 store 不应报错: %v", err)
	}
	if report.TotalEvents != 0 {
		t.Errorf("空 store 总数应为 0, 实际 %d", report.TotalEvents)
	}
	if report.ByCategory == nil {
		t.Error("ByCategory map 应被初始化(非 nil)")
	}
}

// ============================================================
// 长字符串 / Unicode / 大量字段
// ============================================================

// TestLog_UnicodeAndLongStrings 验证 Unicode 与超长字符串能被正确序列化。
func TestLog_UnicodeAndLongStrings(t *testing.T) {
	l := NewAuditLogger()
	var buf bytes.Buffer
	l.writers = []io.Writer{&buf}

	longStr := strings.Repeat("中文😀", 5000)
	l.Log(&AuditEvent{
		Level:    LevelInfo,
		Action:   "动作-🚀",
		Category: CategoryAgent,
		Details:  map[string]any{"unicode": "héllo-世界-🎉", "long": longStr},
	})

	var decoded AuditEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &decoded); err != nil {
		t.Fatalf("Unicode/长字符串应正常序列化: %v", err)
	}
	if decoded.Action != "动作-🚀" {
		t.Errorf("Unicode action 解析错误, 实际=%q", decoded.Action)
	}
	if decoded.Details["unicode"] != "héllo-世界-🎉" {
		t.Errorf("Unicode details 解析错误, 实际=%v", decoded.Details["unicode"])
	}
}

// TestLog_NilActorTargetDetails 验证 nil Actor/Target/Details 不导致 panic。
func TestLog_NilActorTargetDetails(t *testing.T) {
	l := NewAuditLogger()
	l.writers = []io.Writer{&safeWriter{}}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil 字段不应 panic: %v", r)
		}
	}()
	l.Log(&AuditEvent{Level: LevelInfo, Action: "nil-fields"})
}

// ============================================================
// 辅助类型
// ============================================================

// safeWriter 是并发安全的 io.Writer, 用于并发测试避免 writer 自身竞态。
type safeWriter struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *safeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}
