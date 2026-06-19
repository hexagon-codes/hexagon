package runtime

import "time"

// 长时任务进度事件构造助手（B-fw5）。
//
// 用法：长任务（如 hexeye 媒体生产）在自己的执行循环里构造这些事件并经任意 EventSink
// （含 SSEEventSink）上报；载荷置于 Event.Payload，SSE wire 自动透传。
//
//	sink.Emit(ctx, runtime.NewJobQueuedEvent(jobID))
//	sink.Emit(ctx, runtime.NewJobProgressEvent(jobID, 42, "rendering"))
//	sink.Emit(ctx, runtime.NewJobDoneEvent(jobID))

func jobEvent(t EventType, p JobProgress) Event {
	return Event{
		Version:   EventVersionV1,
		Type:      t,
		Timestamp: time.Now(),
		Payload:   &p,
	}
}

// NewJobQueuedEvent 任务入队。
func NewJobQueuedEvent(jobID string) Event {
	return jobEvent(EventJobQueued, JobProgress{JobID: jobID, Stage: "queued"})
}

// NewJobRunningEvent 任务开始执行。
func NewJobRunningEvent(jobID string) Event {
	return jobEvent(EventJobRunning, JobProgress{JobID: jobID, Stage: "running"})
}

// NewJobProgressEvent 进度更新（percent 取值 [0,100]）。
func NewJobProgressEvent(jobID string, percent float64, message string) Event {
	return jobEvent(EventJobProgress, JobProgress{JobID: jobID, Stage: "progress", Percent: percent, Message: message})
}

// NewJobPreviewEvent 阶段性预览（缩略图 / 部分结果等）。
func NewJobPreviewEvent(jobID string, preview any) Event {
	return jobEvent(EventJobPreview, JobProgress{JobID: jobID, Stage: "preview", Preview: preview})
}

// NewJobDoneEvent 任务完成。
func NewJobDoneEvent(jobID string) Event {
	return jobEvent(EventJobDone, JobProgress{JobID: jobID, Stage: "done", Percent: 100})
}

// NewJobFailedEvent 任务失败。
func NewJobFailedEvent(jobID string, err error) Event {
	ev := jobEvent(EventJobFailed, JobProgress{JobID: jobID, Stage: "failed"})
	if err != nil {
		ev.Error = err
		ev.RuntimeError = runtimeError("job_failed", err)
		if p, ok := ev.Payload.(*JobProgress); ok {
			p.Message = err.Error()
		}
	}
	return ev
}

// NewHeartbeatEvent 心跳事件，保持长连接活跃。
func NewHeartbeatEvent() Event {
	return Event{Version: EventVersionV1, Type: EventHeartbeat, Timestamp: time.Now()}
}
