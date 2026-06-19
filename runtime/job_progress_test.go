package runtime

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestJobProgressEvents 验证长任务进度事件构造 + 载荷正确。
func TestJobProgressEvents(t *testing.T) {
	ev := NewJobProgressEvent("job-1", 42, "rendering")
	if ev.Type != EventJobProgress {
		t.Fatalf("Type = %s, want %s", ev.Type, EventJobProgress)
	}
	p, ok := ev.Payload.(*JobProgress)
	if !ok {
		t.Fatalf("Payload 类型应为 *JobProgress，实际 %T", ev.Payload)
	}
	if p.JobID != "job-1" || p.Percent != 42 || p.Message != "rendering" || p.Stage != "progress" {
		t.Fatalf("JobProgress 载荷不对: %+v", p)
	}

	done := NewJobDoneEvent("job-1")
	if dp := done.Payload.(*JobProgress); dp.Percent != 100 || dp.Stage != "done" {
		t.Fatalf("done 事件应 100%%/done，实际 %+v", dp)
	}

	failed := NewJobFailedEvent("job-1", context.DeadlineExceeded)
	if failed.Error == nil || failed.RuntimeError == nil {
		t.Fatal("failed 事件应带 Error/RuntimeError")
	}
}

// TestJobProgressOverSSE 验证进度事件经 SSEEventSink 透传到 wire（事件名 + 载荷）。
func TestJobProgressOverSSE(t *testing.T) {
	rec := httptest.NewRecorder()
	sink, err := NewSSEEventSink(rec)
	if err != nil {
		t.Fatalf("NewSSEEventSink error = %v", err)
	}

	seq := []Event{
		NewJobQueuedEvent("job-1"),
		NewJobRunningEvent("job-1"),
		NewJobProgressEvent("job-1", 50, "half"),
		NewJobDoneEvent("job-1"),
	}
	for _, ev := range seq {
		if err := sink.Emit(context.Background(), ev); err != nil {
			t.Fatalf("Emit(%s) error = %v", ev.Type, err)
		}
	}

	body := rec.Body.String()
	for _, want := range []string{
		"event: job_queued",
		"event: job_running",
		"event: job_progress",
		"event: job_done",
		`"percent":50`,
		`"job_id":"job-1"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE wire 缺 %q\n---\n%s", want, body)
		}
	}
}

// TestHeartbeatEvent 验证心跳事件。
func TestHeartbeatEvent(t *testing.T) {
	hb := NewHeartbeatEvent()
	if hb.Type != EventHeartbeat {
		t.Fatalf("Type = %s, want %s", hb.Type, EventHeartbeat)
	}
}
