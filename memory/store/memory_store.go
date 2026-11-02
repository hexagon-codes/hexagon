package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// InMemoryStore 基于内存的 MemoryStore 实现
//
// 适用于开发和测试场景。支持：
//   - 命名空间隔离
//   - TTL 自动过期清理
//   - 基础关键词搜索
//
// 线程安全：所有方法都是并发安全的。
//
// 使用示例：
//
//	store := NewInMemoryStore()
//	defer store.Close()
//
//	store.Put(ctx, []string{"users", "u1"}, "profile", map[string]any{
//	    "name": "张三",
//	    "age":  30,
//	})
type InMemoryStore struct {
	// items 存储所有记忆条目，键为 namespace:key 拼接
	items map[string]*Item

	mu sync.RWMutex

	// done 用于停止后台清理协程
	done chan struct{}

	// cleanupInterval TTL 清理间隔
	cleanupInterval time.Duration
}

// InMemoryOption 是 InMemoryStore 的配置选项
type InMemoryOption func(*InMemoryStore)

// WithCleanupInterval 设置 TTL 过期清理间隔
//
// 默认每分钟清理一次过期条目
func WithCleanupInterval(d time.Duration) InMemoryOption {
	return func(s *InMemoryStore) {
		s.cleanupInterval = d
	}
}

// NewInMemoryStore 创建内存存储实例
//
// 会启动一个后台协程定期清理过期条目，
// 使用完毕后应调用 Close() 释放资源。
func NewInMemoryStore(opts ...InMemoryOption) *InMemoryStore {
	s := &InMemoryStore{
		items:           make(map[string]*Item),
		done:            make(chan struct{}),
		cleanupInterval: time.Minute,
	}

	for _, opt := range opts {
		opt(s)
	}

	// 启动后台 TTL 清理协程
	go s.cleanupLoop()

	return s
}

// Put 存储一条记忆
func (s *InMemoryStore) Put(_ context.Context, namespace []string, key string, value map[string]any, opts ...PutOption) error {
	_, err := s.put(namespace, key, value, nil, opts...)
	return err
}

// PutIfRevision 在同一个实例锁内验证修订并更新，冲突不会留下写入。
func (s *InMemoryStore) PutIfRevision(ctx context.Context, namespace []string, key string, value map[string]any, expected uint64, opts ...PutOption) (*Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 条件写遵守 JSON 文档合同，输入、存储与返回值各自独立。
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var frozen map[string]any
	if err := json.Unmarshal(data, &frozen); err != nil {
		return nil, err
	}
	item, err := s.put(append([]string(nil), namespace...), key, frozen, &expected, opts...)
	if err != nil {
		return nil, err
	}
	item.Value = nil
	if err := json.Unmarshal(data, &item.Value); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *InMemoryStore) put(namespace []string, key string, value map[string]any, expected *uint64, opts ...PutOption) (*Item, error) {
	if key == "" {
		return nil, fmt.Errorf("key 不能为空")
	}

	options := applyPutOptions(opts)
	now := time.Now()

	item := &Item{
		Namespace: namespace,
		Key:       key,
		Value:     value,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// 设置过期时间
	if options.ttl > 0 {
		expiresAt := now.Add(options.ttl)
		item.ExpiresAt = &expiresAt
	}

	storeKey := namespaceKey(namespace, key)

	s.mu.Lock()
	defer s.mu.Unlock()

	existing := s.items[storeKey]
	if existing != nil && existing.IsExpired() {
		existing = nil
	}
	var current uint64
	if existing != nil {
		current = existing.Revision
	}
	if expected != nil && (*expected != current || *expected == 0 && existing != nil) {
		return nil, ErrRevisionConflict
	}
	if current == ^uint64(0) {
		return nil, fmt.Errorf("memory store: revision overflow")
	}
	item.Revision = current + 1
	if existing != nil {
		item.CreatedAt = existing.CreatedAt
	}

	s.items[storeKey] = item
	return copyItem(item), nil
}

// Get 获取一条记忆
func (s *InMemoryStore) Get(_ context.Context, namespace []string, key string) (*Item, error) {
	storeKey := namespaceKey(namespace, key)

	s.mu.RLock()
	item, ok := s.items[storeKey]
	s.mu.RUnlock()

	if !ok {
		return nil, nil
	}

	// 检查是否过期
	if item.IsExpired() {
		// 惰性清理过期条目
		s.mu.Lock()
		if s.items[storeKey] == item {
			delete(s.items, storeKey)
		}
		s.mu.Unlock()
		return nil, nil
	}

	// 返回副本，避免外部修改影响内部数据
	return copyItem(item), nil
}

// Search 搜索记忆
//
// 支持关键词搜索（在 Value 中查找包含 Query 的文本字段）
// 和元数据过滤（精确匹配 Filter 中的所有字段）
func (s *InMemoryStore) Search(_ context.Context, namespace []string, query *SearchQuery) ([]*SearchResult, error) {
	if query == nil {
		return nil, nil
	}

	prefix := namespacePrefix(namespace)
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*SearchResult

	for key, item := range s.items {
		// 命名空间前缀匹配
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			continue
		}

		// 跳过过期条目
		if item.IsExpired() {
			continue
		}

		// 元数据过滤
		if !matchFilter(item.Value, query.Filter) {
			continue
		}

		// 关键词搜索
		score := float64(1.0)
		if query.Query != "" {
			matched, s := keywordMatch(item.Value, query.Query)
			if !matched {
				continue
			}
			score = s
		}

		results = append(results, &SearchResult{
			Item:  copyItem(item),
			Score: score,
		})
	}

	// 按分数降序排序
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// 分页
	start := query.Offset
	if start < 0 {
		start = 0
	}
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
func (s *InMemoryStore) Delete(_ context.Context, namespace []string, key string) error {
	storeKey := namespaceKey(namespace, key)

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.items, storeKey)
	return nil
}

// List 列出命名空间下的所有记忆
func (s *InMemoryStore) List(_ context.Context, namespace []string, opts ...ListOption) ([]*Item, error) {
	options := applyListOptions(opts)
	prefix := namespacePrefix(namespace)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []*Item

	for key, item := range s.items {
		// 命名空间前缀匹配
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			continue
		}

		// 跳过过期条目
		if item.IsExpired() {
			continue
		}

		// 键前缀过滤
		if options.prefix != "" && !strings.HasPrefix(item.Key, options.prefix) {
			continue
		}

		items = append(items, copyItem(item))
	}

	// 按更新时间排序
	sort.Slice(items, func(i, j int) bool {
		if options.orderDesc {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].UpdatedAt.Before(items[j].UpdatedAt)
	})

	// 分页
	start := options.offset
	if start < 0 {
		start = 0
	}
	if start >= len(items) {
		return nil, nil
	}
	end := len(items)
	if options.limit > 0 && start+options.limit < end {
		end = start + options.limit
	}

	return items[start:end], nil
}

// DeleteNamespace 删除整个命名空间
func (s *InMemoryStore) DeleteNamespace(_ context.Context, namespace []string) error {
	prefix := namespacePrefix(namespace)

	s.mu.Lock()
	defer s.mu.Unlock()

	for key := range s.items {
		if prefix == "" || strings.HasPrefix(key, prefix) {
			delete(s.items, key)
		}
	}
	return nil
}

// Close 关闭存储，停止后台清理协程
func (s *InMemoryStore) Close() error {
	close(s.done)
	return nil
}

// cleanupLoop 后台定期清理过期条目
func (s *InMemoryStore) cleanupLoop() {
	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.cleanup()
		}
	}
}

// cleanup 清理所有过期条目
func (s *InMemoryStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, item := range s.items {
		if item.IsExpired() {
			delete(s.items, key)
		}
	}
}

// ============== 内部辅助函数 ==============

// copyItem 创建 Item 的深拷贝
func copyItem(item *Item) *Item {
	if item == nil {
		return nil
	}

	copied := &Item{
		Revision:  item.Revision,
		Key:       item.Key,
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
		ExpiresAt: item.ExpiresAt,
	}

	// 拷贝命名空间
	if item.Namespace != nil {
		copied.Namespace = make([]string, len(item.Namespace))
		copy(copied.Namespace, item.Namespace)
	}

	// 拷贝值（浅拷贝 map）
	if item.Value != nil {
		copied.Value = make(map[string]any, len(item.Value))
		for k, v := range item.Value {
			copied.Value[k] = v
		}
	}

	return copied
}

// matchFilter 检查 value 是否匹配所有过滤条件
// 使用 reflect.DeepEqual 进行类型安全的值比较，避免 int(1) != float64(1.0) 的问题
func matchFilter(value map[string]any, filter map[string]any) bool {
	if len(filter) == 0 {
		return true
	}
	if value == nil {
		return false
	}
	for k, v := range filter {
		mv, ok := value[k]
		if !ok {
			return false
		}
		if !valuesEqual(mv, v) {
			return false
		}
	}
	return true
}

// valuesEqual 比较两个值是否相等，支持跨数值类型比较
func valuesEqual(a, b any) bool {
	// 先尝试直接比较
	if fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b) {
		return true
	}

	// 尝试数值比较（处理 int vs float64 等情况）
	af, aOk := toFloat64(a)
	bf, bOk := toFloat64(b)
	if aOk && bOk {
		return af == bf
	}

	return false
}

// toFloat64 尝试将值转换为 float64
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

// keywordMatch 在 value 的字符串字段中搜索关键词
//
// 返回是否匹配以及匹配分数（0-1），分数基于匹配的字段数量
func keywordMatch(value map[string]any, query string) (bool, float64) {
	if value == nil || query == "" {
		return false, 0
	}

	queryLower := strings.ToLower(query)
	matchedFields := 0
	totalFields := 0

	for _, v := range value {
		str, ok := v.(string)
		if !ok {
			continue
		}
		totalFields++
		if strings.Contains(strings.ToLower(str), queryLower) {
			matchedFields++
		}
	}

	if matchedFields == 0 {
		return false, 0
	}

	// 分数 = 匹配字段数 / 总字符串字段数
	score := float64(matchedFields) / float64(totalFields)
	return true, score
}
