package agent

import (
	"context"
	"fmt"
	"os"

	"github.com/hexagon-codes/ai-core/memory"
	runmemory "github.com/hexagon-codes/hexagon/memory"
	"github.com/hexagon-codes/toolkit/util/reflectx"
)

// SharedMemory 团队级共享记忆容器
//
// 提供三层共享记忆：
//   - shortTerm: 短期记忆（BufferMemory），存储最近的任务结果，始终启用
//   - longTerm: 长期记忆（VectorMemory），支持语义检索，可选
//   - entity: 实体记忆（EntityMemory），构建实体知识库，可选
//
// 所有 Agent 通过 SharedMemoryProxy 读写共享记忆，实现跨 Agent 的记忆自动共享。
//
// 线程安全：所有方法都是并发安全的（底层 memory.Memory 实现自身保证线程安全）
type SharedMemory struct {
	// shortTerm 短期共享记忆（始终存在）
	shortTerm memory.Memory

	// longTerm 长期共享记忆（可选，支持语义搜索）
	longTerm memory.Memory

	// entity 实体共享记忆（可选，构建实体知识图）
	entity *memory.EntityMemory
}

// SharedMemoryConfig 共享记忆配置
type SharedMemoryConfig struct {
	// ShortTermCapacity 短期记忆容量（默认 200）
	ShortTermCapacity int
}

// SharedMemoryOption 共享记忆配置选项
type SharedMemoryOption func(*SharedMemory)

// WithShortTermCapacity 设置短期记忆容量
func WithShortTermCapacity(capacity int) SharedMemoryOption {
	return func(sm *SharedMemory) {
		sm.shortTerm = memory.NewBuffer(capacity)
	}
}

// WithLongTermMemory 启用长期记忆（语义检索）
//
// 传入已配置好 Embedder 的 VectorMemory 实例
func WithLongTermMemory(longTerm memory.Memory) SharedMemoryOption {
	return func(sm *SharedMemory) {
		sm.longTerm = longTerm
	}
}

// WithEntityMemory 启用实体记忆（实体知识库）
//
// 传入已配置好 EntityExtractor 的 EntityMemory 实例
func WithEntityMemory(entity *memory.EntityMemory) SharedMemoryOption {
	return func(sm *SharedMemory) {
		sm.entity = entity
	}
}

// NewSharedMemory 创建共享记忆
//
// 默认只启用短期记忆（BufferMemory，容量 200）。
// 通过 Option 可启用长期记忆和实体记忆。
//
// 示例：
//
//	sm := NewSharedMemory()                              // 仅短期记忆
//	sm := NewSharedMemory(WithShortTermCapacity(500))    // 自定义容量
//	sm := NewSharedMemory(WithLongTermMemory(vecMem))    // 启用长期记忆
func NewSharedMemory(opts ...SharedMemoryOption) *SharedMemory {
	sm := &SharedMemory{
		shortTerm: memory.NewBuffer(200),
	}
	for _, opt := range opts {
		opt(sm)
	}
	return sm
}

// Save 保存条目到共享记忆
//
// 写入 shortTerm；如启用则同时写入 longTerm 和 entity。
// longTerm/entity 写入失败仅输出 stderr 警告，不影响 shortTerm。
func (sm *SharedMemory) Save(ctx context.Context, entry memory.Entry) error {
	entry = cloneSharedEntry(entry)
	if err := sm.shortTerm.Save(ctx, entry); err != nil {
		return fmt.Errorf("shared memory short-term save failed: %w", err)
	}

	// 异步写入可选的长期和实体记忆
	if sm.longTerm != nil {
		if err := sm.longTerm.Save(ctx, cloneSharedEntry(entry)); err != nil {
			fmt.Fprintf(os.Stderr, "hexagon: shared memory long-term save warning: %v\n", err)
		}
	}
	if sm.entity != nil {
		if err := sm.entity.Save(ctx, cloneSharedEntry(entry)); err != nil {
			fmt.Fprintf(os.Stderr, "hexagon: shared memory entity save warning: %v\n", err)
		}
	}

	return nil
}

// SaveBatch 批量保存条目到共享记忆
func (sm *SharedMemory) SaveBatch(ctx context.Context, entries []memory.Entry) error {
	for _, entry := range entries {
		if err := sm.Save(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

// Get 根据 ID 获取条目（从 shortTerm 查找）
func (sm *SharedMemory) Get(ctx context.Context, id string) (*memory.Entry, error) {
	entry, err := sm.shortTerm.Get(ctx, id)
	if err != nil || entry == nil {
		return entry, err
	}
	copy := cloneSharedEntry(*entry)
	return &copy, nil
}

// Search 搜索共享记忆
//
// 合并 shortTerm 和 longTerm 的搜索结果（去重）。
// longTerm 搜索失败仅输出 stderr 警告。
func (sm *SharedMemory) Search(ctx context.Context, query memory.SearchQuery) ([]memory.Entry, error) {
	results, err := sm.shortTerm.Search(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("shared memory short-term search failed: %w", err)
	}

	if sm.longTerm != nil {
		longResults, err := sm.longTerm.Search(ctx, query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hexagon: shared memory long-term search warning: %v\n", err)
		} else if len(longResults) > 0 {
			results = mergeEntries(results, longResults, query.Limit)
		}
	}

	return cloneSharedResults(results, query.ExcludeStale), nil
}

// Capture 冻结宿主声明的独占 Scope 内选中输入，后续共享更新不改变此 Run。
// 原 Memory 端口不提供持久 CAS 修订，记录明确标记未版本化；版本条件写使用 ScopedMemory。
func (sm *SharedMemory) Capture(ctx context.Context, scope runmemory.Scope, source string, query memory.SearchQuery) (*runmemory.Snapshot, error) {
	query.ExcludeStale = true
	entries, err := sm.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	return runmemory.FreezeEntries(scope, source, entries)
}

// Delete 删除条目（从 shortTerm 删除）
func (sm *SharedMemory) Delete(ctx context.Context, id string) error {
	return sm.shortTerm.Delete(ctx, id)
}

// Clear 清空所有共享记忆
func (sm *SharedMemory) Clear(ctx context.Context) error {
	if err := sm.shortTerm.Clear(ctx); err != nil {
		return err
	}
	if sm.longTerm != nil {
		if err := sm.longTerm.Clear(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "hexagon: shared memory long-term clear warning: %v\n", err)
		}
	}
	if sm.entity != nil {
		if err := sm.entity.Clear(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "hexagon: shared memory entity clear warning: %v\n", err)
		}
	}
	return nil
}

// Stats 返回共享记忆统计信息
func (sm *SharedMemory) Stats() memory.MemoryStats {
	return sm.shortTerm.Stats()
}

// 确保实现了 Memory 接口
var _ memory.Memory = (*SharedMemory)(nil)

// ============== SharedMemoryProxy ==============

// SharedMemoryProxy 共享记忆代理
//
// 每个 Agent 持有一个 Proxy 实例，拦截 Memory 的读写操作：
//   - Save: 写入本地记忆 + 同步到共享记忆（标记 _agent_id/_agent_name）
//   - Search: 合并本地记忆和共享记忆的搜索结果
//   - Get/Delete/Clear/Stats: 代理到本地记忆
//
// 对 Agent 完全透明，无需修改 Agent 代码。
//
// 线程安全：本身无状态，底层 local 和 shared 自身保证线程安全
type SharedMemoryProxy struct {
	// local Agent 原始记忆
	local memory.Memory

	// shared 团队共享记忆
	shared *SharedMemory

	// agentID 所属 Agent 的 ID
	agentID string

	// agentName 所属 Agent 的名称
	agentName string

	// config 代理配置
	config ProxyConfig
}

// ProxyConfig 代理配置
type ProxyConfig struct {
	// WriteToShared 是否将写入同步到共享记忆（默认 true）
	WriteToShared bool

	// ReadFromShared 是否从共享记忆读取（默认 true）
	ReadFromShared bool

	// SharedSearchLimit 从共享记忆搜索的结果数量限制（默认 5）
	SharedSearchLimit int
}

// DefaultProxyConfig 返回默认代理配置
func DefaultProxyConfig() ProxyConfig {
	return ProxyConfig{
		WriteToShared:     true,
		ReadFromShared:    true,
		SharedSearchLimit: 5,
	}
}

// ProxyOption 代理配置选项
type ProxyOption func(*ProxyConfig)

// WithWriteToShared 设置是否同步写入到共享记忆
func WithWriteToShared(enabled bool) ProxyOption {
	return func(c *ProxyConfig) {
		c.WriteToShared = enabled
	}
}

// WithReadFromShared 设置是否从共享记忆读取
func WithReadFromShared(enabled bool) ProxyOption {
	return func(c *ProxyConfig) {
		c.ReadFromShared = enabled
	}
}

// WithSharedSearchLimit 设置共享记忆搜索结果限制
func WithSharedSearchLimit(limit int) ProxyOption {
	return func(c *ProxyConfig) {
		c.SharedSearchLimit = limit
	}
}

// NewSharedMemoryProxy 创建共享记忆代理
//
// 参数：
//   - local: Agent 原始记忆
//   - shared: 团队共享记忆
//   - agentID: 所属 Agent 的 ID
//   - agentName: 所属 Agent 的名称
//   - opts: 代理配置选项
func NewSharedMemoryProxy(local memory.Memory, shared *SharedMemory, agentID, agentName string, opts ...ProxyOption) *SharedMemoryProxy {
	config := DefaultProxyConfig()
	for _, opt := range opts {
		opt(&config)
	}
	return &SharedMemoryProxy{
		local:     local,
		shared:    shared,
		agentID:   agentID,
		agentName: agentName,
		config:    config,
	}
}

// Save 保存条目
//
// 标记 _agent_id 和 _agent_name 元数据，写入本地记忆，
// 如果启用 WriteToShared 则同步写入共享记忆（失败仅输出 stderr 警告）。
func (p *SharedMemoryProxy) Save(ctx context.Context, entry memory.Entry) error {
	// 标记来源 Agent 信息
	entry = p.tagEntry(entry)

	// 写入本地记忆
	if err := p.local.Save(ctx, entry); err != nil {
		return err
	}

	// 同步到共享记忆
	if p.config.WriteToShared {
		if err := p.shared.Save(ctx, entry); err != nil {
			fmt.Fprintf(os.Stderr, "hexagon: shared memory proxy sync warning (agent=%s): %v\n", p.agentName, err)
		}
	}

	return nil
}

// SaveBatch 批量保存条目
//
// 逐条调用 Save 以保证每条都同步到共享记忆
func (p *SharedMemoryProxy) SaveBatch(ctx context.Context, entries []memory.Entry) error {
	for _, entry := range entries {
		if err := p.Save(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

// Get 根据 ID 获取条目（代理到本地记忆）
func (p *SharedMemoryProxy) Get(ctx context.Context, id string) (*memory.Entry, error) {
	return p.local.Get(ctx, id)
}

// Search 搜索记忆
//
// 合并本地记忆和共享记忆的搜索结果，按 ID 去重，本地结果优先。
func (p *SharedMemoryProxy) Search(ctx context.Context, query memory.SearchQuery) ([]memory.Entry, error) {
	localResults, err := p.local.Search(ctx, query)
	if err != nil {
		return nil, err
	}

	if !p.config.ReadFromShared {
		return localResults, nil
	}

	// 从共享记忆搜索
	sharedQuery := query
	if p.config.SharedSearchLimit > 0 {
		sharedQuery.Limit = p.config.SharedSearchLimit
	}

	sharedResults, err := p.shared.Search(ctx, sharedQuery)
	if err != nil {
		// 共享搜索失败时退化为仅本地结果
		fmt.Fprintf(os.Stderr, "hexagon: shared memory proxy search warning (agent=%s): %v\n", p.agentName, err)
		return localResults, nil
	}

	if len(sharedResults) == 0 {
		return localResults, nil
	}

	return mergeEntries(localResults, sharedResults, query.Limit), nil
}

// Delete 删除条目（代理到本地记忆）
func (p *SharedMemoryProxy) Delete(ctx context.Context, id string) error {
	return p.local.Delete(ctx, id)
}

// Clear 清空本地记忆（不清空共享记忆）
func (p *SharedMemoryProxy) Clear(ctx context.Context) error {
	return p.local.Clear(ctx)
}

// Stats 返回本地记忆统计信息
func (p *SharedMemoryProxy) Stats() memory.MemoryStats {
	return p.local.Stats()
}

// Local 返回本地原始记忆（用于测试和调试）
func (p *SharedMemoryProxy) Local() memory.Memory {
	return p.local
}

// tagEntry 为条目添加来源 Agent 标记
func (p *SharedMemoryProxy) tagEntry(entry memory.Entry) memory.Entry {
	entry = cloneSharedEntry(entry)
	if entry.Metadata == nil {
		entry.Metadata = make(map[string]any)
	}
	entry.Metadata["_agent_id"] = p.agentID
	entry.Metadata["_agent_name"] = p.agentName
	return entry
}

// cloneSharedEntry 保类型复制记忆数据；Handler/连接等对象不属于 Entry 的数据合同。
func cloneSharedEntry(entry memory.Entry) memory.Entry {
	entry.Metadata = reflectx.DeepCopy(entry.Metadata)
	entry.Embedding = append([]float32(nil), entry.Embedding...)
	entry.Media = append([]memory.MediaRef(nil), entry.Media...)
	for i := range entry.Media {
		entry.Media[i].Metadata = reflectx.DeepCopy(entry.Media[i].Metadata)
	}
	return entry
}

func cloneSharedResults(entries []memory.Entry, excludeStale bool) []memory.Entry {
	result := make([]memory.Entry, 0, len(entries))
	for _, entry := range entries {
		if !excludeStale || !entry.Stale {
			result = append(result, cloneSharedEntry(entry))
		}
	}
	return result
}

// 确保实现了 Memory 接口
var _ memory.Memory = (*SharedMemoryProxy)(nil)

// ============== 工具函数 ==============

// mergeEntries 合并两组记忆条目
//
// 按 ID 去重，primary 中的条目优先保留。
// 如果 limit > 0，最多返回 limit 条结果。
func mergeEntries(primary, secondary []memory.Entry, limit int) []memory.Entry {
	if len(secondary) == 0 {
		return primary
	}

	// 收集 primary 中已有的 ID
	seen := make(map[string]struct{}, len(primary))
	for _, e := range primary {
		if e.ID != "" {
			seen[e.ID] = struct{}{}
		}
	}

	// 合并 secondary 中不重复的条目
	merged := make([]memory.Entry, len(primary), len(primary)+len(secondary))
	copy(merged, primary)
	for _, e := range secondary {
		if e.ID != "" {
			if _, ok := seen[e.ID]; ok {
				continue
			}
			seen[e.ID] = struct{}{}
		}
		merged = append(merged, e)
	}

	// 应用 limit
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}

	return merged
}

// ============== Team 集成辅助 ==============

// installSharedMemory 为团队中所有 Agent 安装共享记忆代理
//
// 遍历所有 agent，如果 agent 实现了 MemorySetter 接口，
// 则用 SharedMemoryProxy 包装其原始记忆。
// 已经包装过的 agent 不会重复包装。
//
// 调用者必须持有 t.mu 的写锁
func (t *Team) installSharedMemory() {
	for _, ag := range t.agents {
		t.wrapAgentMemory(ag)
	}
}

// wrapAgentMemory 为单个 Agent 包装共享记忆代理
//
// 如果 Agent 未实现 MemorySetter，则跳过。
// 如果 Agent 的 Memory 已经是 SharedMemoryProxy，则不重复包装。
func (t *Team) wrapAgentMemory(ag Agent) {
	setter, ok := ag.(MemorySetter)
	if !ok {
		return
	}

	// 检查是否已包装
	if _, already := ag.Memory().(*SharedMemoryProxy); already {
		return
	}

	proxy := NewSharedMemoryProxy(
		ag.Memory(),
		t.sharedMemory,
		ag.ID(),
		ag.Name(),
	)
	setter.SetMemory(proxy)
}
