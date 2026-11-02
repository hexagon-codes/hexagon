package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hexagon-codes/toolkit/net/sse"
)

// EmitStored 使用完整已提交事件信封及稳定 SSE id，适合 FollowEvents 的历史和实时追赶。
// 此接口与即时 token 流分开；临时 delta 不会冒充可恢复游标。
func (s *SSEEventSink) EmitStored(ctx context.Context, event StoredEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if event.ID == "" || event.Sequence <= 0 {
		return fmt.Errorf("runtime/sse: stored event identity is required")
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if err := s.sw.Write(&sse.Event{ID: event.ID, Event: string(event.Type), Data: string(payload)}); err != nil {
		s.closed = true
		return fmt.Errorf("runtime/sse: write stored event: %w", err)
	}
	return nil
}

func (s *SSEEventSink) Follow(ctx context.Context, store EventStore, scope, runID string, after int64, interval time.Duration) error {
	return FollowEvents(ctx, store, scope, runID, after, interval, func(event StoredEvent) error { return s.EmitStored(ctx, event) })
}
