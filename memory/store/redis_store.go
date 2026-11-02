package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore 基于 Redis 的持久化 MemoryStore 实现
//
// 将记忆存储在 Redis 中，适合生产环境和分布式部署。
//
// Redis 键结构：
//   - 记忆数据：{prefix}{ns1}:{ns2}:{key} → JSON 序列化的 redisItem
//   - 命名空间索引：{prefix}ns:{ns1}:{ns2} → Set 类型，存储该命名空间下的所有 key
//
// 特性：
//   - 利用 Redis TTL 实现记忆过期
//   - 利用 Redis Set 实现命名空间索引
//   - 基础关键词搜索（遍历匹配）
//   - Pipeline 批量操作优化
//
// 线程安全：Redis 客户端本身是并发安全的。
//
// 使用示例：
//
//	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
//	store := NewRedisStore(client)
//
//	store.Put(ctx, []string{"users", "u1"}, "prefs", map[string]any{
//	    "theme": "dark",
//	})
type RedisStore struct {
	// client Redis 客户端
	client *redis.Client

	// prefix Redis 键前缀
	prefix string

	// defaultTTL 默认 TTL，0 表示永不过期
	defaultTTL time.Duration
}

// RedisStoreOption 是 RedisStore 的配置选项
type RedisStoreOption func(*RedisStore)

// WithRedisPrefix 设置 Redis 键前缀
//
// 默认前缀为 "hexagon:mem:"
func WithRedisPrefix(prefix string) RedisStoreOption {
	return func(s *RedisStore) {
		s.prefix = prefix
	}
}

// WithDefaultTTL 设置默认 TTL
//
// 当 Put 未显式指定 TTL 时使用此默认值，0 表示永不过期
func WithDefaultTTL(ttl time.Duration) RedisStoreOption {
	return func(s *RedisStore) {
		s.defaultTTL = ttl
	}
}

// NewRedisStore 创建 Redis 存储实例
//
// client: Redis 客户端（调用方负责创建和关闭）
func NewRedisStore(client *redis.Client, opts ...RedisStoreOption) *RedisStore {
	s := &RedisStore{
		client: client,
		prefix: "hexagon:mem:",
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Put 存储一条记忆
func (s *RedisStore) Put(ctx context.Context, namespace []string, key string, value map[string]any, opts ...PutOption) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.client == nil {
		return errors.New("redis client is nil")
	}
	if key == "" {
		return fmt.Errorf("key 不能为空")
	}

	options := applyPutOptions(opts)
	now := time.Now()

	item := &redisItem{
		Namespace: namespace,
		Key:       key,
		Value:     value,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// 确定 TTL
	ttl := options.ttl
	if ttl == 0 {
		ttl = s.defaultTTL
	}
	if ttl > 0 {
		expiresAt := now.Add(ttl)
		item.ExpiresAt = &expiresAt
	}

	redisKey := s.dataKey(namespace, key)

	// 如果已存在，保留原始创建时间
	existing, err := s.getItem(ctx, redisKey)
	if err == nil && existing != nil {
		item.CreatedAt = existing.CreatedAt
		item.Revision = existing.Revision
	}
	if item.Revision == ^uint64(0) {
		return fmt.Errorf("memory store: revision overflow")
	}
	item.Revision++

	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("序列化记忆失败: %w", err)
	}

	// 使用 Pipeline 批量操作：写入数据 + 更新命名空间索引
	// 注意：Redis Pipeline 不是事务，部分失败时可能导致数据不一致
	// 这里使用 TxPipeline 确保原子性
	pipe := s.client.TxPipeline()
	if ttl > 0 {
		pipe.Set(ctx, redisKey, data, ttl)
	} else {
		pipe.Set(ctx, redisKey, data, 0)
	}
	pipe.SAdd(ctx, s.nsIndexKey(namespace), key)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("Redis Pipeline 执行失败: %w", err)
	}

	return nil
}

// Get 获取一条记忆
func (s *RedisStore) Get(ctx context.Context, namespace []string, key string) (*Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.client == nil {
		return nil, errors.New("redis client is nil")
	}

	redisKey := s.dataKey(namespace, key)
	item, err := s.getItem(ctx, redisKey)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}

	return item.toItem(), nil
}

// Search 搜索记忆
func (s *RedisStore) Search(ctx context.Context, namespace []string, query *SearchQuery) ([]*SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.client == nil {
		return nil, errors.New("redis client is nil")
	}
	if query == nil {
		return nil, nil
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}

	// 获取命名空间下所有 key
	items, err := s.listItems(ctx, namespace)
	if err != nil {
		return nil, err
	}

	var results []*SearchResult
	for _, item := range items {
		// 元数据过滤
		if !matchFilter(item.Value, query.Filter) {
			continue
		}

		// 关键词搜索
		score := float64(1.0)
		if query.Query != "" {
			matched, matchScore := keywordMatch(item.Value, query.Query)
			if !matched {
				continue
			}
			score = matchScore
		}

		results = append(results, &SearchResult{
			Item:  item.toItem(),
			Score: score,
		})
	}

	// 按分数降序排序
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// 分页
	start := max(query.Offset, 0)
	if start >= len(results) {
		return nil, nil
	}
	end := len(results)
	if start+limit < end {
		end = start + limit
	}

	return results[start:end], nil
}

// Delete 删除一条记忆
func (s *RedisStore) Delete(ctx context.Context, namespace []string, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.client == nil {
		return errors.New("redis client is nil")
	}

	pipe := s.client.Pipeline()
	pipe.Del(ctx, s.dataKey(namespace, key))
	pipe.SRem(ctx, s.nsIndexKey(namespace), key)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("Redis 删除失败: %w", err)
	}
	return nil
}

// List 列出命名空间下的所有记忆
func (s *RedisStore) List(ctx context.Context, namespace []string, opts ...ListOption) ([]*Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.client == nil {
		return nil, errors.New("redis client is nil")
	}

	options := applyListOptions(opts)

	redisItems, err := s.listItems(ctx, namespace)
	if err != nil {
		return nil, err
	}

	var items []*Item
	for _, ri := range redisItems {
		// 键前缀过滤
		if options.prefix != "" && !strings.HasPrefix(ri.Key, options.prefix) {
			continue
		}
		items = append(items, ri.toItem())
	}

	// 按更新时间排序
	sort.Slice(items, func(i, j int) bool {
		if options.orderDesc {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].UpdatedAt.Before(items[j].UpdatedAt)
	})

	// 分页
	start := max(options.offset, 0)
	if start >= len(items) {
		return nil, nil
	}
	end := len(items)
	if options.limit > 0 && start+options.limit < end {
		end = start + options.limit
	}

	return items[start:end], nil
}

// DeleteNamespace 删除整个命名空间及其下所有记忆
func (s *RedisStore) DeleteNamespace(ctx context.Context, namespace []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.client == nil {
		return errors.New("redis client is nil")
	}

	indexKey := s.nsIndexKey(namespace)

	// 获取命名空间下所有 key
	keys, err := s.client.SMembers(ctx, indexKey).Result()
	if err != nil {
		return fmt.Errorf("获取命名空间索引失败: %w", err)
	}

	if len(keys) == 0 {
		// 仍然删除索引 key 本身
		return s.client.Del(ctx, indexKey).Err()
	}

	// 构建要删除的所有 Redis key
	redisKeys := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		redisKeys = append(redisKeys, s.dataKey(namespace, key))
	}
	redisKeys = append(redisKeys, indexKey)

	if err := s.client.Del(ctx, redisKeys...).Err(); err != nil {
		return fmt.Errorf("批量删除失败: %w", err)
	}
	return nil
}

// Close 关闭存储
//
// 注意：不会关闭 Redis 客户端，客户端的生命周期由调用方管理
func (s *RedisStore) Close() error {
	return nil
}

// ============== 内部类型 ==============

// redisItem Redis 存储的记忆条目
type redisItem struct {
	Revision  uint64         `json:"revision"`
	Namespace []string       `json:"namespace"`
	Key       string         `json:"key"`
	Value     map[string]any `json:"value"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	ExpiresAt *time.Time     `json:"expires_at,omitempty"`
}

func (ri *redisItem) toItem() *Item {
	item := &Item{
		Revision:  ri.Revision,
		Key:       ri.Key,
		CreatedAt: ri.CreatedAt,
		UpdatedAt: ri.UpdatedAt,
		ExpiresAt: ri.ExpiresAt,
	}

	if ri.Namespace != nil {
		item.Namespace = make([]string, len(ri.Namespace))
		copy(item.Namespace, ri.Namespace)
	}

	if ri.Value != nil {
		item.Value = make(map[string]any, len(ri.Value))
		for k, v := range ri.Value {
			item.Value[k] = v
		}
	}

	return item
}

// ============== 内部方法 ==============

// dataKey 构建记忆数据的 Redis 键
//
// 格式: {prefix}{ns1}:{ns2}:{key}
func (s *RedisStore) dataKey(namespace []string, key string) string {
	parts := make([]string, 0, len(namespace)+1)
	parts = append(parts, namespace...)
	parts = append(parts, key)
	return s.prefix + strings.Join(parts, ":")
}

// nsIndexKey 构建命名空间索引的 Redis 键
//
// 格式: {prefix}ns:{ns1}:{ns2}
func (s *RedisStore) nsIndexKey(namespace []string) string {
	return s.prefix + "ns:" + strings.Join(namespace, ":")
}

// getItem 从 Redis 获取单条记忆
func (s *RedisStore) getItem(ctx context.Context, redisKey string) (*redisItem, error) {
	data, err := s.client.Get(ctx, redisKey).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("Redis Get 失败: %w", err)
	}

	var item redisItem
	if err := json.Unmarshal(data, &item); err != nil {
		return nil, fmt.Errorf("解析记忆数据失败: %w", err)
	}
	return &item, nil
}

// listItems 获取命名空间下所有记忆条目
func (s *RedisStore) listItems(ctx context.Context, namespace []string) ([]*redisItem, error) {
	indexKey := s.nsIndexKey(namespace)

	keys, err := s.client.SMembers(ctx, indexKey).Result()
	if err != nil {
		return nil, fmt.Errorf("获取命名空间索引失败: %w", err)
	}

	if len(keys) == 0 {
		return nil, nil
	}

	// 批量获取数据
	redisKeys := make([]string, len(keys))
	for i, key := range keys {
		redisKeys[i] = s.dataKey(namespace, key)
	}

	values, err := s.client.MGet(ctx, redisKeys...).Result()
	if err != nil {
		return nil, fmt.Errorf("Redis MGet 失败: %w", err)
	}

	items := make([]*redisItem, 0, len(values))
	for _, v := range values {
		if v == nil {
			continue
		}
		str, ok := v.(string)
		if !ok {
			continue
		}
		var item redisItem
		if err := json.Unmarshal([]byte(str), &item); err != nil {
			continue
		}
		items = append(items, &item)
	}

	return items, nil
}

// 确保实现了 MemoryStore 接口
var _ MemoryStore = (*RedisStore)(nil)
