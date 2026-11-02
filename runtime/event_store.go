package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrEventCursor = errors.New("runtime: event cursor is unavailable")

// StoredEvent 保存已提交事实，不持有活 State 或 error 指针。临时 token delta 不默认入库。
type StoredEvent struct {
	Scope     string          `json:"scope,omitempty"`
	RunID     string          `json:"run_id,omitempty"`
	ID        string          `json:"id,omitempty"`
	CommitID  string          `json:"commit_id,omitempty"`
	Sequence  int64           `json:"sequence,omitempty"`
	Revision  uint64          `json:"revision,omitempty"`
	Version   string          `json:"version,omitempty"`
	Type      EventType       `json:"type"`
	Timestamp time.Time       `json:"timestamp,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// EventStore 的游标为最后已消费序号；返回严格递增且大于 after 的事实。
type EventStore interface {
	ReadEvents(context.Context, string, string, int64, int) ([]StoredEvent, error)
}

// FollowEvents 用权威记录轮询追赶，通知丢失也不会丢必要事件。
// 不拥有后台 goroutine；调用者负责 ctx，sink 失败后可沿原游标重新连接并去重。
func FollowEvents(ctx context.Context, store EventStore, scope, runID string, after int64, interval time.Duration, sink func(StoredEvent) error) error {
	if store == nil || sink == nil {
		return fmt.Errorf("runtime: event store and sink are required")
	}
	if interval <= 0 {
		interval = time.Second
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		events, err := store.ReadEvents(ctx, scope, runID, after, 128)
		if err != nil {
			return err
		}
		for _, event := range events {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := invokeExtension("stored_event_sink", func() error { return sink(event) }); err != nil {
				return err
			}
			after = event.Sequence
		}
		if len(events) > 0 {
			continue
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
