package runtime

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"
)

// WorkerStore 在任务宿主所需的能力上增加到期任务枚举，不把扫描职责放入 Runner。
type WorkerStore interface {
	TaskStore
	Runnable(context.Context, string, int) ([]string, error)
}

// Worker 显式由宿主调用 Run，返回时已等待当前执行及续租结束。
// Execute 负责调用同一 Runner/Graph；收到取消后仍需独立核对的供应商回执由宿主另行管理。
type Worker struct {
	Store        WorkerStore
	Scope        string
	Owner        string
	Lease        time.Duration
	PollInterval time.Duration
	Execute      func(context.Context, RunRecord) error
	OnError      func(string, error)
}

func (w *Worker) Run(ctx context.Context) error {
	if w.Store == nil || w.Scope == "" || w.Owner == "" || w.Execute == nil || w.Lease <= 0 {
		return fmt.Errorf("runtime: worker requires store, scope, owner, execution handler and positive lease")
	}
	interval := w.PollInterval
	if interval <= 0 {
		interval = time.Second
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ids, err := w.Store.Runnable(ctx, w.Scope, 32)
		if err != nil {
			return err
		}
		for _, runID := range ids {
			if err := w.Process(ctx, runID); err != nil && !errors.Is(err, context.Canceled) {
				if w.OnError != nil {
					w.OnError(runID, err)
				} else {
					return err
				}
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Process 仅处理一个指定 Run，便于宿主沿用已有队列/调度器。
func (w *Worker) Process(ctx context.Context, runID string) error {
	if w.Store == nil || w.Execute == nil || w.Lease <= 0 {
		return fmt.Errorf("runtime: worker is not configured")
	}
	record, claimed, err := w.Store.Claim(ctx, w.Scope, runID, ExecutionAuthority{Owner: w.Owner, AttemptID: rand.Text()}, w.Lease)
	if err != nil || !claimed {
		return err
	}
	execution, cancel := context.WithCancel(ContextWithRunAuthority(ctx, w.Scope, runID, record.Authority))
	defer cancel()
	renewed := make(chan error, 1)
	go func() {
		interval := w.Lease / 3
		if interval <= 0 {
			interval = time.Nanosecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-execution.Done():
				renewed <- nil
				return
			case <-ticker.C:
				if err := w.Store.Renew(execution, w.Scope, runID, record.Authority, w.Lease); err != nil {
					cancel()
					renewed <- err
					return
				}
			}
		}
	}()
	executeErr := w.Execute(execution, record)
	cancel()
	renewErr := <-renewed
	// 停机交接使用有界清理 context，不沿用已取消的订阅/任务 ctx。
	cleanup, finish := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finish()
	releaseErr := w.Store.Release(cleanup, w.Scope, runID, record.Authority)
	if errors.Is(releaseErr, ErrExecutionLost) {
		releaseErr = nil
	}
	return errors.Join(executeErr, renewErr, releaseErr)
}
