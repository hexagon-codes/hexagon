// Package graph 提供 Hexagon AI Agent 框架的图编排引擎
//
// cache.go 实现节点级缓存功能，参考 LangGraph 的 Node-level Caching：
//   - 基于节点输入的哈希值缓存执行结果
//   - 避免重复执行相同输入的节点（如重复的 LLM 调用）
//   - 支持内存缓存和自定义缓存后端
//   - 支持 TTL 过期和容量限制
//
// 使用示例：
//
//	graph := NewGraph[MyState]("my-graph").
//	    AddNode("llm_call", handler).
//	    WithNodeCache("llm_call", NewMemoryNodeCache(
//	        WithCacheTTL(5 * time.Minute),
//	        WithCacheCapacity(100),
//	    )).
//	    Build()
package graph

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/idgen"
)

// NodeCache 节点缓存接口
// 用于缓存节点的执行结果，避免重复计算
type NodeCache interface {
	// Get 获取缓存的状态
	// key 是基于节点输入计算的哈希值
	// 返回缓存的状态和是否命中
	Get(key string) (any, bool)

	// Set 设置缓存
	Set(key string, value any)

	// Delete 删除缓存
	Delete(key string)

	// Clear 清空所有缓存
	Clear()

	// Stats 返回缓存统计信息
	Stats() CacheStats
}

// CacheStats 缓存统计信息
type CacheStats struct {
	// Hits 命中次数
	Hits int64 `json:"hits"`

	// Misses 未命中次数
	Misses int64 `json:"misses"`

	// Size 当前缓存条目数
	Size int `json:"size"`

	// Evictions 驱逐次数
	Evictions int64 `json:"evictions"`
}

// ============== MemoryNodeCache ==============

// MemoryNodeCache 内存节点缓存
// 使用 LRU 策略，支持 TTL 过期
type MemoryNodeCache struct {
	mu        sync.RWMutex
	entries   map[string]*cacheEntry
	order     []string // LRU 顺序
	capacity  int
	ttl       time.Duration
	hits      int64
	misses    int64
	evictions int64
}

type cacheEntry struct {
	value     any
	createdAt time.Time
}

// MemoryCacheOption 内存缓存选项
type MemoryCacheOption func(*MemoryNodeCache)

// WithCacheTTL 设置缓存过期时间
func WithCacheTTL(ttl time.Duration) MemoryCacheOption {
	return func(c *MemoryNodeCache) {
		c.ttl = ttl
	}
}

// WithCacheCapacity 设置缓存容量
func WithCacheCapacity(capacity int) MemoryCacheOption {
	return func(c *MemoryNodeCache) {
		c.capacity = capacity
	}
}

// NewMemoryNodeCache 创建内存节点缓存
func NewMemoryNodeCache(opts ...MemoryCacheOption) *MemoryNodeCache {
	c := &MemoryNodeCache{
		entries:  make(map[string]*cacheEntry),
		order:    make([]string, 0),
		capacity: 1000,
		ttl:      30 * time.Minute,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Get 获取缓存
func (c *MemoryNodeCache) Get(key string) (any, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok {
		c.mu.Lock()
		c.misses++
		c.mu.Unlock()
		return nil, false
	}

	// 检查 TTL
	if c.ttl > 0 && time.Since(entry.createdAt) > c.ttl {
		c.Delete(key)
		c.mu.Lock()
		c.misses++
		c.mu.Unlock()
		return nil, false
	}

	c.mu.Lock()
	c.hits++
	// 移到 LRU 最前面
	c.moveToFront(key)
	c.mu.Unlock()

	return entry.value, true
}

// Set 设置缓存
func (c *MemoryNodeCache) Set(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 如果已存在，更新并移到最前
	if _, exists := c.entries[key]; exists {
		c.entries[key] = &cacheEntry{
			value:     value,
			createdAt: time.Now(),
		}
		c.moveToFront(key)
		return
	}

	// 容量满时驱逐最旧的
	for len(c.entries) >= c.capacity && len(c.order) > 0 {
		oldest := c.order[len(c.order)-1]
		delete(c.entries, oldest)
		c.order = c.order[:len(c.order)-1]
		c.evictions++
	}

	c.entries[key] = &cacheEntry{
		value:     value,
		createdAt: time.Now(),
	}
	c.order = append([]string{key}, c.order...)
}

// Delete 删除缓存条目
func (c *MemoryNodeCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, key)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
}

// Clear 清空缓存
func (c *MemoryNodeCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*cacheEntry)
	c.order = c.order[:0]
}

// Stats 返回统计信息
func (c *MemoryNodeCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return CacheStats{
		Hits:      c.hits,
		Misses:    c.misses,
		Size:      len(c.entries),
		Evictions: c.evictions,
	}
}

// moveToFront 将 key 移到 LRU 最前面（调用者需持有锁）
func (c *MemoryNodeCache) moveToFront(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append([]string{key}, c.order...)
			return
		}
	}
}

// ============== 缓存集成到 Graph ==============

// nodeCacheConfig 节点缓存配置
type nodeCacheConfig struct {
	cache NodeCache
	// keyFunc 自定义缓存 key 生成函数
	keyFunc func(nodeName string, state any) string
}

// WithNodeCache 为图构建器添加节点缓存
func (b *GraphBuilder[S]) WithNodeCache(nodeName string, cache NodeCache) *GraphBuilder[S] {
	if b.graph.Metadata == nil {
		b.graph.Metadata = make(map[string]any)
	}

	caches, _ := b.graph.Metadata["__node_caches"].(map[string]*nodeCacheConfig)
	if caches == nil {
		caches = make(map[string]*nodeCacheConfig)
	}

	caches[nodeName] = &nodeCacheConfig{cache: cache}
	b.graph.Metadata["__node_caches"] = caches

	return b
}

// GetNodeCache 获取节点缓存
func (g *Graph[S]) GetNodeCache(nodeName string) NodeCache {
	caches, _ := g.Metadata["__node_caches"].(map[string]*nodeCacheConfig)
	if caches == nil {
		return nil
	}
	cfg, ok := caches[nodeName]
	if !ok {
		return nil
	}
	return cfg.cache
}

// ComputeCacheKey 计算节点缓存 key
// 基于节点名称和输入状态的 SHA256 哈希
func ComputeCacheKey(nodeName string, state any) string {
	data, err := json.Marshal(state)
	if err != nil {
		// 无法序列化，使用固定前缀避免缓存
		return fmt.Sprintf("uncacheable-%s-%s", nodeName, idgen.NanoID())
	}

	hash := sha256.Sum256(append([]byte(nodeName+":"), data...))
	return fmt.Sprintf("%x", hash[:16])
}

// CachedNodeHandler 创建带缓存的节点处理函数
// 包装原始 handler，自动检查和更新缓存
func CachedNodeHandler[S State](nodeName string, handler NodeHandler[S], cache NodeCache) NodeHandler[S] {
	return func(ctx context.Context, state S) (S, error) {
		// 计算缓存 key
		key := ComputeCacheKey(nodeName, state)

		// 尝试从缓存获取
		if cached, hit := cache.Get(key); hit {
			if cachedState, ok := cached.(S); ok {
				return cachedState, nil
			}
		}

		// 缓存未命中，执行原始 handler
		result, err := handler(ctx, state)
		if err != nil {
			return result, err
		}

		// 缓存结果
		cache.Set(key, result)

		return result, nil
	}
}
