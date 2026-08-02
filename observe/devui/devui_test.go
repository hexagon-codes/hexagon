package devui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hexagon-codes/hexagon/hooks"
)

// TestNewDevUI 测试创建 DevUI 实例
func TestNewDevUI(t *testing.T) {
	ui := New(
		WithAddr(":0"),
		WithMaxEvents(500),
		WithSSE(true),
		WithMetrics(true),
	)

	if ui == nil {
		t.Fatal("expected non-nil DevUI")
	}

	if ui.Addr() != ":0" {
		t.Errorf("expected addr :0, got %s", ui.Addr())
	}

	if ui.HookManager() == nil {
		t.Error("expected non-nil HookManager")
	}

	if ui.Tracer() == nil {
		t.Error("expected non-nil Tracer")
	}

	if ui.Collector() == nil {
		t.Error("expected non-nil Collector")
	}
}

// TestCollector 测试事件收集器
func TestCollector(t *testing.T) {
	collector := NewCollector(100)

	// 测试订阅
	eventCh, unsubscribe := collector.Subscribe()
	defer unsubscribe()

	if collector.SubscriberCount() != 1 {
		t.Errorf("expected 1 subscriber, got %d", collector.SubscriberCount())
	}

	// 测试 RunHook
	ctx := context.Background()
	runStartEvt := &hooks.RunStartEvent{
		RunID:   "run-1",
		AgentID: "agent-1",
		Input:   "test input",
	}

	_ = collector.OnStart(ctx, runStartEvt)

	// 验证事件被收集
	select {
	case event := <-eventCh:
		if event.Type != EventAgentStart {
			t.Errorf("expected event type %s, got %s", EventAgentStart, event.Type)
		}
		if event.Data["run_id"] != "run-1" {
			t.Errorf("expected run_id run-1, got %v", event.Data["run_id"])
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for event")
	}

	// 验证统计
	stats := collector.Stats()
	if stats.TotalEvents != 1 {
		t.Errorf("expected 1 total event, got %d", stats.TotalEvents)
	}
	if stats.AgentRuns != 1 {
		t.Errorf("expected 1 agent run, got %d", stats.AgentRuns)
	}
}

// TestRingBuffer 测试环形缓冲区
func TestRingBuffer(t *testing.T) {
	rb := NewRingBuffer(5)

	// 测试空缓冲区
	if rb.Size() != 0 {
		t.Errorf("expected size 0, got %d", rb.Size())
	}

	all := rb.GetAll()
	if len(all) != 0 {
		t.Errorf("expected 0 events, got %d", len(all))
	}

	// 添加事件
	for i := 0; i < 3; i++ {
		e := &Event{
			ID:   string(rune('a' + i)),
			Type: EventAgentStart,
		}
		rb.Push(e)
	}

	if rb.Size() != 3 {
		t.Errorf("expected size 3, got %d", rb.Size())
	}

	// 测试 GetRecent
	recent := rb.GetRecent(2)
	if len(recent) != 2 {
		t.Errorf("expected 2 recent events, got %d", len(recent))
	}

	// 测试溢出
	for i := 0; i < 5; i++ {
		e := &Event{
			ID:   string(rune('1' + i)),
			Type: EventLLMRequest,
		}
		rb.Push(e)
	}

	// 缓冲区容量为 5，应该只保留最新的 5 个
	if rb.Size() != 5 {
		t.Errorf("expected size 5 (capacity), got %d", rb.Size())
	}

	// 测试清空
	rb.Clear()
	if rb.Size() != 0 {
		t.Errorf("expected size 0 after clear, got %d", rb.Size())
	}
}

// TestEventPool 测试事件对象池
func TestEventPool(t *testing.T) {
	// 获取事件
	e := AcquireEvent()
	if e == nil {
		t.Fatal("expected non-nil event")
	}

	if e.Data == nil {
		t.Error("expected non-nil Data map")
	}

	// 设置数据
	e.ID = "test-id"
	e.Type = EventToolCall
	e.Data["key"] = "value"

	// 归还事件
	ReleaseEvent(e)

	// 再次获取，应该是重置后的
	e2 := AcquireEvent()
	if e2.ID != "" {
		t.Errorf("expected empty ID after reset, got %s", e2.ID)
	}
	if e2.Type != "" {
		t.Errorf("expected empty Type after reset, got %s", e2.Type)
	}
	if len(e2.Data) != 0 {
		t.Errorf("expected empty Data after reset, got %v", e2.Data)
	}

	ReleaseEvent(e2)
}

// TestHandler 测试 HTTP 处理器
func TestHandler(t *testing.T) {
	ui := New(WithMaxEvents(100))
	h := newHandler(ui)

	// 添加一些测试事件
	collector := ui.Collector()
	ctx := context.Background()

	_ = collector.OnStart(ctx, &hooks.RunStartEvent{
		RunID:   "run-1",
		AgentID: "agent-1",
		Input:   "test",
	})

	_ = collector.OnToolStart(ctx, &hooks.ToolStartEvent{
		RunID:    "run-1",
		ToolName: "calculator",
		ToolID:   "tool-1",
		Input:    map[string]any{"a": 1, "b": 2},
	})

	// 测试 /api/events
	t.Run("GetEvents", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
		w := httptest.NewRecorder()

		h.handleEvents(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		var resp response
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if !resp.Success {
			t.Error("expected success=true")
		}
	})

	// 测试 /api/metrics
	t.Run("GetMetrics", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
		w := httptest.NewRecorder()

		h.handleMetrics(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		var resp response
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if !resp.Success {
			t.Error("expected success=true")
		}

		data := resp.Data.(map[string]any)
		if data["agent_runs"].(float64) != 1 {
			t.Errorf("expected 1 agent run, got %v", data["agent_runs"])
		}
		if data["tool_calls"].(float64) != 1 {
			t.Errorf("expected 1 tool call, got %v", data["tool_calls"])
		}
	})

	// 测试 /health
	t.Run("Health", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()

		h.handleHealth(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
	})

	// 测试事件类型过滤
	t.Run("EventFilter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/events?type=agent.start", nil)
		w := httptest.NewRecorder()

		h.handleEvents(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}

		var resp response
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		data := resp.Data.(map[string]any)
		total := int(data["total"].(float64))
		if total != 1 {
			t.Errorf("expected 1 filtered event, got %d", total)
		}
	})
}

// TestAllHooks 测试所有 Hook 接口
func TestAllHooks(t *testing.T) {
	collector := NewCollector(100)
	ctx := context.Background()

	// 测试 RunHook
	t.Run("RunHook", func(t *testing.T) {
		_ = collector.OnStart(ctx, &hooks.RunStartEvent{
			RunID:   "run-1",
			AgentID: "agent-1",
		})
		_ = collector.OnEnd(ctx, &hooks.RunEndEvent{
			RunID:    "run-1",
			AgentID:  "agent-1",
			Duration: 100,
		})
		_ = collector.OnError(ctx, &hooks.ErrorEvent{
			RunID:   "run-1",
			AgentID: "agent-1",
			Error:   context.Canceled,
		})
	})

	// 测试 ToolHook
	t.Run("ToolHook", func(t *testing.T) {
		_ = collector.OnToolStart(ctx, &hooks.ToolStartEvent{
			RunID:    "run-1",
			ToolName: "test-tool",
		})
		_ = collector.OnToolEnd(ctx, &hooks.ToolEndEvent{
			RunID:    "run-1",
			ToolName: "test-tool",
			Duration: 50,
		})
	})

	// 测试 LLMHook
	t.Run("LLMHook", func(t *testing.T) {
		_ = collector.OnLLMStart(ctx, &hooks.LLMStartEvent{
			RunID:    "run-1",
			Provider: "openai",
			Model:    "gpt-4",
		})
		_ = collector.OnLLMStream(ctx, &hooks.LLMStreamEvent{
			RunID:      "run-1",
			Model:      "gpt-4",
			Content:    "Hello",
			ChunkIndex: 0,
		})
		_ = collector.OnLLMEnd(ctx, &hooks.LLMEndEvent{
			RunID:            "run-1",
			Model:            "gpt-4",
			PromptTokens:     100,
			CompletionTokens: 50,
			Duration:         200,
		})
	})

	// 测试 RetrieverHook
	t.Run("RetrieverHook", func(t *testing.T) {
		_ = collector.OnRetrieverStart(ctx, &hooks.RetrieverStartEvent{
			RunID: "run-1",
			Query: "test query",
			TopK:  5,
		})
		_ = collector.OnRetrieverEnd(ctx, &hooks.RetrieverEndEvent{
			RunID:     "run-1",
			Query:     "test query",
			Documents: []any{"doc1", "doc2"},
			Duration:  30,
		})
	})

	// 验证统计
	stats := collector.Stats()
	if stats.AgentRuns != 1 {
		t.Errorf("expected 1 agent run, got %d", stats.AgentRuns)
	}
	if stats.ToolCalls != 1 {
		t.Errorf("expected 1 tool call, got %d", stats.ToolCalls)
	}
	if stats.LLMCalls != 1 {
		t.Errorf("expected 1 LLM call, got %d", stats.LLMCalls)
	}
	if stats.RetrieverRuns != 1 {
		t.Errorf("expected 1 retriever run, got %d", stats.RetrieverRuns)
	}
	if stats.Errors != 1 {
		t.Errorf("expected 1 error, got %d", stats.Errors)
	}

	// 验证事件总数
	// RunStart, RunEnd, RunError, ToolStart, ToolEnd, LLMStart, LLMStream, LLMEnd, RetrieverStart, RetrieverEnd = 10
	if stats.TotalEvents != 10 {
		t.Errorf("expected 10 total events, got %d", stats.TotalEvents)
	}
}

// TestEmitMethods 测试手动发送事件
func TestEmitMethods(t *testing.T) {
	collector := NewCollector(100)

	// 测试图事件
	collector.EmitGraphStart("run-1", "graph-1", "test-graph", map[string]any{"key": "value"})
	collector.EmitGraphNode("run-1", "graph-1", "node-1", "process", nil, 10)
	collector.EmitGraphEnd("run-1", "graph-1", nil, 100)

	// 测试状态变更
	collector.EmitStateChange("agent-1", "status", "idle", "running")

	// 测试错误
	collector.EmitError("run-1", "graph", "node failed", "stack trace...")

	stats := collector.Stats()
	if stats.TotalEvents != 5 {
		t.Errorf("expected 5 events, got %d", stats.TotalEvents)
	}
	if stats.Errors != 1 {
		t.Errorf("expected 1 error, got %d", stats.Errors)
	}
}

// TestStaticHandler 测试静态文件处理
func TestStaticHandler(t *testing.T) {
	ui := New()
	h := newHandler(ui)

	// 测试根路径
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	h.handleStatic(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Hexagon Dev UI") {
		t.Error("expected HTML to contain 'Hexagon Dev UI'")
	}
}

// TestMiddleware 测试中间件
func TestMiddleware(t *testing.T) {
	// 测试 Recovery 中间件
	t.Run("Recovery", func(t *testing.T) {
		handler := RecoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("test panic")
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		// 不应该 panic
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected status 500, got %d", w.Code)
		}
	})

	// 测试 CORS 中间件
	t.Run("CORS", func(t *testing.T) {
		const allowedOrigin = "https://trusted.example"
		handler := CORSMiddleware(allowedOrigin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		// 测试 OPTIONS 请求
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		req.Header.Set("Origin", allowedOrigin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Header().Get("Access-Control-Allow-Origin") != allowedOrigin {
			t.Error("expected CORS header")
		}
	})

	// 测试链式中间件
	t.Run("Chain", func(t *testing.T) {
		var order []int
		m1 := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, 1)
				next.ServeHTTP(w, r)
			})
		}
		m2 := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, 2)
				next.ServeHTTP(w, r)
			})
		}

		handler := ChainMiddleware(m1, m2)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, 3)
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
			t.Errorf("expected order [1,2,3], got %v", order)
		}
	})
}

// BenchmarkCollector 基准测试收集器
func BenchmarkCollector(b *testing.B) {
	collector := NewCollector(1000)
	ctx := context.Background()

	b.Run("OnStart", func(b *testing.B) {
		evt := &hooks.RunStartEvent{
			RunID:   "run-1",
			AgentID: "agent-1",
			Input:   "test input",
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = collector.OnStart(ctx, evt)
		}
	})

	b.Run("WithSubscriber", func(b *testing.B) {
		eventCh, unsubscribe := collector.Subscribe()
		defer unsubscribe()

		// 启动消费者
		go func() {
			for range eventCh {
				// 消费事件
			}
		}()

		evt := &hooks.RunStartEvent{
			RunID:   "run-1",
			AgentID: "agent-1",
			Input:   "test input",
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = collector.OnStart(ctx, evt)
		}
	})
}

// BenchmarkRingBuffer 基准测试环形缓冲区
func BenchmarkRingBuffer(b *testing.B) {
	rb := NewRingBuffer(1000)

	b.Run("Push", func(b *testing.B) {
		e := &Event{
			ID:   "test",
			Type: EventAgentStart,
			Data: map[string]any{"key": "value"},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rb.Push(e)
		}
	})

	b.Run("GetRecent", func(b *testing.B) {
		// 先填充数据
		for i := 0; i < 1000; i++ {
			rb.Push(&Event{ID: "test"})
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rb.GetRecent(100)
		}
	})
}
