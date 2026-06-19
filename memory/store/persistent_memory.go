package store

import (
	"context"
	"maps"
	"slices"
	"sort"
	"time"

	coremem "github.com/hexagon-codes/ai-core/memory"

	"github.com/hexagon-codes/hexagon/internal/util"
)

// PersistentMemory 将 MemoryStore 适配为 ai-core memory.Memory 接口
//
// 通过桥接模式，让现有的 Agent、WindowMemory、SummaryMemory 等上层组件
// 无缝使用持久化存储后端（FileStore、RedisStore 等）。
//
// 分层架构：
//
//	Agent / Application
//	       ↓ (memory.Memory 接口)
//	PersistentMemory 适配器
//	       ↓ (MemoryStore 接口)
//	InMemoryStore / FileStore / RedisStore
//
// 使用示例：
//
//	// 使用文件存储作为后端
//	fileStore, _ := NewFileStore("/data/memory")
//	mem := NewPersistentMemory(fileStore, []string{"users", "u123"})
//
//	// 现在可以像普通 Memory 一样使用
//	mem.Save(ctx, memory.NewUserEntry("你好"))
//	entries, _ := mem.Search(ctx, memory.SearchQuery{Limit: 10})
//
// 线程安全：所有方法都是并发安全的（由底层 MemoryStore 保证）。
type PersistentMemory struct {
	// store 底层持久化存储
	store MemoryStore

	// namespace 固定的命名空间路径，用于隔离不同用户/会话的记忆
	namespace []string
}

// PersistentMemoryOption 是 PersistentMemory 的配置选项
type PersistentMemoryOption func(*PersistentMemory)

// NewPersistentMemory 创建持久化记忆适配器
//
// store: 底层存储（InMemoryStore / FileStore / RedisStore）
// namespace: 命名空间路径，用于隔离记忆，如 ["users", "u123"]
//
// 示例：
//
//	mem := NewPersistentMemory(store, []string{"users", "u123"})
func NewPersistentMemory(store MemoryStore, namespace []string, opts ...PersistentMemoryOption) *PersistentMemory {
	pm := &PersistentMemory{
		store:     store,
		namespace: namespace,
	}
	for _, opt := range opts {
		opt(pm)
	}
	return pm
}

// Save 保存单条记忆条目
//
// 将 ai-core Entry 转换为 MemoryStore Item 并持久化。
// 如果 Entry.ID 为空，会自动生成唯一 ID。
func (pm *PersistentMemory) Save(ctx context.Context, entry coremem.Entry) error {
	if entry.ID == "" {
		entry.ID = generateMemoryID()
	}
	now := time.Now()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = now
	}

	value := entryToMap(entry)
	return pm.store.Put(ctx, pm.namespace, entry.ID, value)
}

// SaveBatch 批量保存记忆条目
func (pm *PersistentMemory) SaveBatch(ctx context.Context, entries []coremem.Entry) error {
	for _, entry := range entries {
		if err := pm.Save(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

// Get 根据 ID 获取记忆条目
//
// 返回 nil, nil 表示记忆不存在。
func (pm *PersistentMemory) Get(ctx context.Context, id string) (*coremem.Entry, error) {
	item, err := pm.store.Get(ctx, pm.namespace, id)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}
	entry := mapToEntry(item)
	return &entry, nil
}

// Search 搜索记忆条目
//
// 将 ai-core SearchQuery 转换为 MemoryStore SearchQuery 执行搜索，
// 再对结果应用 ai-core 特有的过滤条件（Roles、Since、Until）。
func (pm *PersistentMemory) Search(ctx context.Context, query coremem.SearchQuery) ([]coremem.Entry, error) {
	// 构建 MemoryStore 搜索查询
	storeQuery := &SearchQuery{
		Query:  query.Query,
		Filter: query.Metadata,
		Limit:  0, // 先不限制，后续在 Entry 层做过滤和分页
		Offset: 0,
	}

	results, err := pm.store.Search(ctx, pm.namespace, storeQuery)
	if err != nil {
		return nil, err
	}

	// 转换并过滤结果
	var entries []coremem.Entry
	for _, r := range results {
		entry := mapToEntry(r.Item)

		// 角色过滤
		if len(query.Roles) > 0 && !slices.Contains(query.Roles, entry.Role) {
			continue
		}

		// 时间范围过滤
		if query.Since != nil && entry.CreatedAt.Before(*query.Since) {
			continue
		}
		if query.Until != nil && entry.CreatedAt.After(*query.Until) {
			continue
		}

		entries = append(entries, entry)
	}

	// 排序
	if query.OrderDesc {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].CreatedAt.After(entries[j].CreatedAt)
		})
	} else {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].CreatedAt.Before(entries[j].CreatedAt)
		})
	}

	// 分页
	start := max(query.Offset, 0)
	if start >= len(entries) {
		return nil, nil
	}
	end := len(entries)
	if query.Limit > 0 && start+query.Limit < end {
		end = start + query.Limit
	}

	return entries[start:end], nil
}

// Delete 删除指定 ID 的记忆条目
func (pm *PersistentMemory) Delete(ctx context.Context, id string) error {
	return pm.store.Delete(ctx, pm.namespace, id)
}

// Clear 清空当前命名空间下的所有记忆
func (pm *PersistentMemory) Clear(ctx context.Context) error {
	return pm.store.DeleteNamespace(ctx, pm.namespace)
}

// Stats 返回记忆统计信息
//
// 通过 List 获取当前命名空间下的所有条目来计算统计信息。
func (pm *PersistentMemory) Stats() coremem.MemoryStats {
	ctx := context.Background()
	items, err := pm.store.List(ctx, pm.namespace)
	if err != nil {
		return coremem.MemoryStats{}
	}

	stats := coremem.MemoryStats{
		EntryCount: len(items),
	}

	// 计算最早和最新条目时间
	for _, item := range items {
		t := item.CreatedAt
		if stats.OldestEntry == nil || t.Before(*stats.OldestEntry) {
			copied := t
			stats.OldestEntry = &copied
		}
		if stats.NewestEntry == nil || t.After(*stats.NewestEntry) {
			copied := t
			stats.NewestEntry = &copied
		}
	}

	return stats
}

// Namespace 返回当前命名空间
func (pm *PersistentMemory) Namespace() []string {
	ns := make([]string, len(pm.namespace))
	copy(ns, pm.namespace)
	return ns
}

// Store 返回底层 MemoryStore
func (pm *PersistentMemory) Store() MemoryStore {
	return pm.store
}

// 确保实现了 ai-core Memory 接口
var _ coremem.Memory = (*PersistentMemory)(nil)

// ============== Entry ↔ Map 转换 ==============

// entryToMap 将 ai-core Entry 转换为 MemoryStore 的 map 格式
func entryToMap(entry coremem.Entry) map[string]any {
	value := map[string]any{
		"id":         entry.ID,
		"role":       entry.Role,
		"content":    entry.Content,
		"created_at": entry.CreatedAt.Format(time.RFC3339Nano),
		"updated_at": entry.UpdatedAt.Format(time.RFC3339Nano),
	}

	if entry.Metadata != nil {
		value["metadata"] = entry.Metadata
	}

	if len(entry.Embedding) > 0 {
		// 将 []float32 转换为 []any 以便 JSON 序列化
		embedding := make([]any, len(entry.Embedding))
		for i, v := range entry.Embedding {
			embedding[i] = float64(v)
		}
		value["embedding"] = embedding
	}

	return value
}

// mapToEntry 将 MemoryStore Item 转换回 ai-core Entry
func mapToEntry(item *Item) coremem.Entry {
	if item == nil {
		return coremem.Entry{}
	}

	entry := coremem.Entry{
		ID:        item.Key,
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}

	if v, ok := item.Value["role"].(string); ok {
		entry.Role = v
	}
	if v, ok := item.Value["content"].(string); ok {
		entry.Content = v
	}

	// 还原 Metadata
	if v, ok := item.Value["metadata"].(map[string]any); ok {
		entry.Metadata = make(map[string]any, len(v))
		maps.Copy(entry.Metadata, v)
	}

	// 还原 Embedding
	if v, ok := item.Value["embedding"].([]any); ok {
		entry.Embedding = make([]float32, 0, len(v))
		for _, val := range v {
			if f, ok := val.(float64); ok {
				entry.Embedding = append(entry.Embedding, float32(f))
			}
		}
	}

	// 优先从 Value 中的时间覆盖 Item 时间（更精确）
	if v, ok := item.Value["created_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			entry.CreatedAt = t
		}
	}
	if v, ok := item.Value["updated_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			entry.UpdatedAt = t
		}
	}

	return entry
}

// ============== ID 生成 ==============

// generateMemoryID 生成唯一的记忆 ID
//
// 复用本仓 internal/util.GenerateID（底层为 toolkit/util/idgen），
// 生成 "pmem-{nanoid}" 形式的唯一标识。ID 仅作唯一性标识使用，
// 时序由 CreatedAt 保证，不依赖 ID 内部结构。
func generateMemoryID() string {
	return util.GenerateID("pmem")
}
