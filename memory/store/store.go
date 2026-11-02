// Package store 提供跨会话持久记忆存储
//
// 本包实现对标 LangGraph Memory Store 的跨会话持久记忆系统，提供：
//   - 命名空间隔离：按 user/org/agent/topic 层级组织记忆
//   - JSON 文档存储：每条记忆是一个 JSON 文档
//   - 语义检索：可选的向量搜索能力
//   - TTL 过期：记忆自动过期清理
//
// 使用示例：
//
//	// 创建内存存储（用于开发和测试）
//	store := store.NewInMemoryStore()
//
//	// 存储一条记忆
//	store.Put(ctx, []string{"users", "u123"}, "preferences", map[string]any{
//	    "theme": "dark",
//	    "language": "zh-CN",
//	})
//
//	// 检索记忆
//	item, _ := store.Get(ctx, []string{"users", "u123"}, "preferences")
//
//	// 语义搜索
//	results, _ := store.Search(ctx, []string{"users", "u123"}, &store.SearchQuery{
//	    Query: "用户偏好",
//	    Limit: 10,
//	})
package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

// MemoryStore 跨会话持久记忆存储接口
//
// 对标 LangGraph Memory Store，提供命名空间隔离的 KV 存储。
// 命名空间使用 []string 表示层级路径，如 ["users", "u123", "preferences"]。
//
// 所有方法都是并发安全的。
type MemoryStore interface {
	// Put 存储一条记忆
	//
	// namespace: 命名空间路径，如 ["users", "u123"]
	// key: 记忆键名，在命名空间内唯一
	// value: JSON 文档（任意 map 数据）
	// opts: 可选配置（TTL、索引字段等）
	//
	// 如果 key 已存在，则覆盖更新
	Put(ctx Context, namespace []string, key string, value map[string]any, opts ...PutOption) error

	// Get 获取一条记忆
	//
	// 返回 nil, nil 表示记忆不存在
	Get(ctx Context, namespace []string, key string) (*Item, error)

	// Search 搜索记忆
	//
	// 支持语义搜索（需配置 Embedder）和元数据过滤
	// 如果 query.Query 非空且存储支持语义搜索，则执行向量相似度搜索
	// 否则回退到关键词匹配
	Search(ctx Context, namespace []string, query *SearchQuery) ([]*SearchResult, error)

	// Delete 删除一条记忆
	//
	// 如果记忆不存在，不返回错误
	Delete(ctx Context, namespace []string, key string) error

	// List 列出命名空间下的所有记忆
	//
	// 支持分页和排序
	List(ctx Context, namespace []string, opts ...ListOption) ([]*Item, error)

	// DeleteNamespace 删除整个命名空间及其下所有记忆
	//
	// 递归删除所有子命名空间
	DeleteNamespace(ctx Context, namespace []string) error
}

// ErrRevisionConflict 表示条件更新的预期修订与当前持久记录不一致。
var ErrRevisionConflict = errors.New("memory store: revision conflict")

// ConditionalStore 是需要防止覆盖他人修改的消费侧条件写能力。
// expected=0 仅创建不存在的键；否则只更新该修订。成功返回实际保存的独立条目。
// 内存与文件实现只保证同一 Store 实例内原子性；Redis 实现使用服务端 WATCH 事务。
type ConditionalStore interface {
	MemoryStore
	PutIfRevision(ctx Context, namespace []string, key string, value map[string]any, expected uint64, opts ...PutOption) (*Item, error)
}

// Item 记忆条目
//
// 每条记忆是一个带命名空间的 JSON 文档
type Item struct {
	// Revision 是存储记录的条件写修订，与 Entry 的语义谱系版本分开。
	Revision uint64 `json:"revision"`
	// Namespace 命名空间路径
	Namespace []string `json:"namespace"`

	// Key 唯一键（在命名空间内唯一）
	Key string `json:"key"`

	// Value JSON 文档内容
	Value map[string]any `json:"value"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt 最后更新时间
	UpdatedAt time.Time `json:"updated_at"`

	// ExpiresAt 过期时间（nil 表示永不过期）
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// IsExpired 检查记忆是否已过期
func (item *Item) IsExpired() bool {
	if item.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*item.ExpiresAt)
}

// SearchQuery 搜索查询参数
type SearchQuery struct {
	// Query 语义搜索文本（如果存储支持向量搜索）
	Query string `json:"query,omitempty"`

	// Filter 元数据过滤条件
	// 支持精确匹配，如 {"type": "preference", "active": true}
	Filter map[string]any `json:"filter,omitempty"`

	// Limit 返回结果数量上限（默认 10）
	Limit int `json:"limit,omitempty"`

	// Offset 分页偏移量
	Offset int `json:"offset,omitempty"`
}

// SearchResult 搜索结果
type SearchResult struct {
	// Item 匹配的记忆条目
	Item *Item `json:"item"`

	// Score 相似度分数（语义搜索时有值，范围 0-1）
	Score float64 `json:"score,omitempty"`
}

// ============== 选项定义 ==============

// PutOption 是 Put 操作的可选配置
type PutOption func(*putOptions)

type putOptions struct {
	ttl         time.Duration
	indexFields []string
}

// WithTTL 设置记忆的过期时间
//
// 示例：
//
//	store.Put(ctx, ns, key, value, store.WithTTL(24 * time.Hour))
func WithTTL(ttl time.Duration) PutOption {
	return func(o *putOptions) {
		o.ttl = ttl
	}
}

// WithIndex 标记需要建立索引的字段（用于加速检索）
//
// 示例：
//
//	store.Put(ctx, ns, key, value, store.WithIndex("type", "category"))
func WithIndex(fields ...string) PutOption {
	return func(o *putOptions) {
		o.indexFields = fields
	}
}

// ListOption 是 List 操作的可选配置
type ListOption func(*listOptions)

type listOptions struct {
	limit     int
	offset    int
	prefix    string
	orderDesc bool
}

// WithListLimit 设置列表返回数量上限
func WithListLimit(limit int) ListOption {
	return func(o *listOptions) {
		o.limit = limit
	}
}

// WithListOffset 设置列表分页偏移量
func WithListOffset(offset int) ListOption {
	return func(o *listOptions) {
		o.offset = offset
	}
}

// WithKeyPrefix 按键前缀过滤
func WithKeyPrefix(prefix string) ListOption {
	return func(o *listOptions) {
		o.prefix = prefix
	}
}

// WithOrderDesc 设置降序排列（按更新时间）
func WithOrderDesc() ListOption {
	return func(o *listOptions) {
		o.orderDesc = true
	}
}

// ============== 辅助函数 ==============

// namespaceKey 将命名空间和键拼接为内部存储键
//
// 例如 (["users", "u123"], "prefs") → "users:u123:prefs"
func namespaceKey(namespace []string, key string) string {
	parts := make([]string, len(namespace)+1)
	copy(parts, namespace)
	parts[len(namespace)] = key
	return strings.Join(parts, ":")
}

// namespacePrefix 将命名空间拼接为前缀
//
// 例如 ["users", "u123"] → "users:u123:"
func namespacePrefix(namespace []string) string {
	if len(namespace) == 0 {
		return ""
	}
	return strings.Join(namespace, ":") + ":"
}

// applyPutOptions 应用 Put 选项
func applyPutOptions(opts []PutOption) *putOptions {
	o := &putOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// applyListOptions 应用 List 选项
func applyListOptions(opts []ListOption) *listOptions {
	o := &listOptions{
		limit: 100, // 默认最多返回 100 条
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Context 是 context.Context 的别名
type Context = context.Context
