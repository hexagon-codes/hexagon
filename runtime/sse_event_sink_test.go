package runtime

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSSEEventSink_BasicEmit(t *testing.T) {
	w := httptest.NewRecorder()
	sink, err := NewSSEEventSink(w)
	if err != nil {
		t.Fatalf("NewSSEEventSink: %v", err)
	}
	defer sink.Close()

	ev := Event{
		Type:      EventRunStarted,
		RunID:     "run-1",
		RequestID: "req-1",
		Turn:      0,
		Sequence:  1,
		Timestamp: time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC),
	}
	if err := sink.Emit(context.Background(), ev); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: run_started") {
		t.Errorf("body 缺 event line: %q", body)
	}
	if !strings.Contains(body, `"run_id":"run-1"`) {
		t.Errorf("body 缺 run_id: %q", body)
	}
	if !strings.HasSuffix(body, "\n\n") {
		t.Errorf("帧未以 \\n\\n 收尾: %q", body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type: %q", ct)
	}
}

func TestSSEEventSink_FilterSkipsEvent(t *testing.T) {
	w := httptest.NewRecorder()
	sink, _ := NewSSEEventSink(w)
	sink.Filter = func(e Event) bool { return e.Type != EventToolCallStarted }

	_ = sink.Emit(context.Background(), Event{Type: EventRunStarted, RunID: "r"})
	_ = sink.Emit(context.Background(), Event{Type: EventToolCallStarted, RunID: "r"})
	_ = sink.Emit(context.Background(), Event{Type: EventRunFinished, RunID: "r"})

	body := w.Body.String()
	if strings.Contains(body, "tool_call_started") {
		t.Errorf("tool_call_started 应被过滤: %s", body)
	}
	if !strings.Contains(body, "run_started") || !strings.Contains(body, "run_finished") {
		t.Errorf("非过滤事件应出现: %s", body)
	}
}

func TestSSEEventSink_ClosedNoop(t *testing.T) {
	w := httptest.NewRecorder()
	sink, _ := NewSSEEventSink(w)
	sink.Close()
	if err := sink.Emit(context.Background(), Event{Type: EventRunStarted}); err != nil {
		t.Errorf("已关闭的 sink Emit 不应返 err: %v", err)
	}
	if w.Body.Len() != 0 {
		t.Errorf("Close 后不应写入: %q", w.Body.String())
	}
}

func TestSSEEventSink_ContextCancelled(t *testing.T) {
	w := httptest.NewRecorder()
	sink, _ := NewSSEEventSink(w)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sink.Emit(ctx, Event{Type: EventRunStarted}); err == nil {
		t.Error("ctx 已取消应返 error")
	}
}

func TestSSEEventSink_ConcurrentEmit(t *testing.T) {
	w := httptest.NewRecorder()
	sink, _ := NewSSEEventSink(w)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = sink.Emit(context.Background(), Event{Type: EventRunStarted, Sequence: int64(i)})
		}(i)
	}
	wg.Wait()
	// 50 帧应全部完整（每帧以 \n\n 结尾）
	body := w.Body.String()
	if c := strings.Count(body, "event: run_started"); c != 50 {
		t.Errorf("并发 50 帧期望全发，实际 %d", c)
	}
}

func TestSSEEventSink_ErrorEvent(t *testing.T) {
	w := httptest.NewRecorder()
	sink, _ := NewSSEEventSink(w)
	_ = sink.Emit(context.Background(), Event{
		Type:  EventRunFinished,
		Error: errors.New("oops"),
	})
	body := w.Body.String()
	if !strings.Contains(body, `"error":"oops"`) {
		t.Errorf("error 应序列化为 string: %s", body)
	}
}

func TestSSEEventSink_EmitRaw(t *testing.T) {
	w := httptest.NewRecorder()
	sink, _ := NewSSEEventSink(w)
	defer sink.Close()

	payload := map[string]string{"stage": "calling_llm", "message": "调用 LLM…"}
	if err := sink.EmitRaw(context.Background(), "progress", payload); err != nil {
		t.Fatalf("EmitRaw: %v", err)
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: progress\n") {
		t.Errorf("缺自定义 event: %q", body)
	}
	if !strings.Contains(body, `"stage":"calling_llm"`) {
		t.Errorf("缺自定义 payload: %q", body)
	}
	if !strings.HasSuffix(body, "\n\n") {
		t.Errorf("帧未以 \\n\\n 收尾: %q", body)
	}
}

func TestSSEEventSink_EmitRawBypassesFilter(t *testing.T) {
	w := httptest.NewRecorder()
	sink, _ := NewSSEEventSink(w)
	sink.Filter = func(e Event) bool { return false }
	if err := sink.EmitRaw(context.Background(), "custom", map[string]int{"x": 1}); err != nil {
		t.Fatalf("EmitRaw: %v", err)
	}
	if !strings.Contains(w.Body.String(), "event: custom") {
		t.Errorf("EmitRaw 应绕过 Filter: %q", w.Body.String())
	}
}

func TestSSEEventSink_EmitRawAfterCloseNoop(t *testing.T) {
	w := httptest.NewRecorder()
	sink, _ := NewSSEEventSink(w)
	sink.Close()
	if err := sink.EmitRaw(context.Background(), "x", "y"); err != nil {
		t.Errorf("已关闭 sink EmitRaw 不应返 err: %v", err)
	}
	if w.Body.Len() != 0 {
		t.Errorf("Close 后 EmitRaw 不应写入: %q", w.Body.String())
	}
}

func TestSSEEventSink_EmitRawCtxCancelled(t *testing.T) {
	w := httptest.NewRecorder()
	sink, _ := NewSSEEventSink(w)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sink.EmitRaw(ctx, "x", "y"); err == nil {
		t.Error("ctx 取消应返 error")
	}
}

func TestSSEEventSink_WriteComment(t *testing.T) {
	w := httptest.NewRecorder()
	sink, _ := NewSSEEventSink(w)
	if err := sink.WriteComment("keepalive"); err != nil {
		t.Fatalf("WriteComment: %v", err)
	}
	if !strings.Contains(w.Body.String(), ": keepalive\n\n") {
		t.Errorf("缺 comment 帧: %q", w.Body.String())
	}
}
