package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// PutIfRevision 使用实际 Redis WATCH/MULTI 事务验证并更新同一键。
// 竞争冲突明确返回 ErrRevisionConflict，不在库内以读取最新版后重试来覆盖其他写入。
func (s *RedisStore) PutIfRevision(ctx context.Context, namespace []string, key string, value map[string]any, expected uint64, opts ...PutOption) (*Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.client == nil {
		return nil, fmt.Errorf("memory store: redis client is nil")
	}
	if key == "" {
		return nil, fmt.Errorf("memory store: key is required")
	}
	options := applyPutOptions(opts)
	ttl := options.ttl
	if ttl == 0 {
		ttl = s.defaultTTL
	}
	redisKey := s.dataKey(namespace, key)
	var saved *Item
	err := s.client.Watch(ctx, func(tx *redis.Tx) error {
		data, err := tx.Get(ctx, redisKey).Bytes()
		var existing *redisItem
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		if err == nil {
			existing = &redisItem{}
			if err := json.Unmarshal(data, existing); err != nil {
				return fmt.Errorf("memory store: failed to decode current revision: %w", err)
			}
		}
		var current uint64
		if existing != nil {
			current = existing.Revision
		}
		if current != expected || expected == 0 && existing != nil {
			return ErrRevisionConflict
		}
		if current == ^uint64(0) {
			return fmt.Errorf("memory store: revision overflow")
		}
		now := time.Now()
		item := &redisItem{Revision: current + 1, Namespace: append([]string(nil), namespace...), Key: key, Value: value, CreatedAt: now, UpdatedAt: now}
		if existing != nil {
			item.CreatedAt = existing.CreatedAt
		}
		if ttl > 0 {
			expires := now.Add(ttl)
			item.ExpiresAt = &expires
		}
		data, err = json.Marshal(item)
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, redisKey, data, ttl)
			pipe.SAdd(ctx, s.nsIndexKey(namespace), key)
			return nil
		})
		if err == nil {
			saved = item.toItem()
		}
		return err
	}, redisKey)
	if errors.Is(err, redis.TxFailedErr) {
		return nil, ErrRevisionConflict
	}
	return saved, err
}
