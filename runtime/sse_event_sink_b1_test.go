package runtime

import (
	"context"
	"net/http/httptest"
	"testing"
)

// 特征测试 — B-1（2026-06-22 hex-test 审计 / 复用分层）：
// SSEEventSink 的手写 SSE 帧改为委托 toolkit/net/sse.Writer。
// 本测试钉死三个写入路径的 *wire 字节* 逐字节不变，先在重构前跑通（锁定现 wire），
// 重构后须仍通过 —— 保证 byte 级兼容（桌面 / cron handler 的 SSE 消费方不受影响）。
func TestSSEEventSink_B1_WireFormatPreserved(t *testing.T) {
	rec := httptest.NewRecorder()
	sink, err := NewSSEEventSink(rec)
	if err != nil {
		t.Fatalf("NewSSEEventSink: %v", err)
	}

	// EmitRaw：自定义事件名 + 确定性 payload
	if err := sink.EmitRaw(context.Background(), "custom", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("EmitRaw: %v", err)
	}
	// WriteComment：注释帧
	if err := sink.WriteComment("keepalive"); err != nil {
		t.Fatalf("WriteComment: %v", err)
	}

	got := rec.Body.String()
	const wantRaw = "event: custom\ndata: {\"k\":\"v\"}\n\n"
	const wantComment = ": keepalive\n\n"
	want := wantRaw + wantComment
	if got != want {
		t.Errorf("SSE wire 字节不一致：\n got=%q\nwant=%q", got, want)
	}

	// 头部也须保持 SSE 约定
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type=%q，期望 text/event-stream", ct)
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Errorf("缺 X-Accel-Buffering: no 头")
	}
}
