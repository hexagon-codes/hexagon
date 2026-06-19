// Package indexer 提供 RAG 系统的文档索引器
//
// 本文件实现知识图谱索引器：
//   - GraphIndexer: 基于知识图谱的索引器
//   - Entity: 实体节点
//   - Relation: 实体关系
//   - GraphStore: 图存储接口
//   - MemoryGraphStore: 内存图存储实现
//
// 知识图谱索引可以：
//   - 提取文档中的实体和关系
//   - 构建知识图谱
//   - 支持图遍历查询
//   - 与向量检索结合使用
//
// 设计参考：
//   - LlamaIndex KnowledgeGraphIndex
//   - Neo4j GraphRAG
//   - Microsoft GraphRAG
package indexer

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/internal/util"
	"github.com/hexagon-codes/hexagon/rag"
)

// ============== 基础类型定义 ==============

// EntityType 实体类型
type EntityType string

const (
	// 常见实体类型
	EntityTypePerson       EntityType = "PERSON"       // 人物
	EntityTypeOrganization EntityType = "ORGANIZATION" // 组织
	EntityTypeLocation     EntityType = "LOCATION"     // 地点
	EntityTypeEvent        EntityType = "EVENT"        // 事件
	EntityTypeConcept      EntityType = "CONCEPT"      // 概念
	EntityTypeProduct      EntityType = "PRODUCT"      // 产品
	EntityTypeTechnology   EntityType = "TECHNOLOGY"   // 技术
	EntityTypeDate         EntityType = "DATE"         // 日期
	EntityTypeOther        EntityType = "OTHER"        // 其他
)

// 索引器默认配置常量
const (
	// DefaultEntityQueryLimit 默认实体查询限制
	// 用于批量删除等操作时的最大查询数量
	DefaultEntityQueryLimit = 10000

	// DefaultBatchSize 默认批处理大小
	DefaultBatchSize = 1000
)

// RelationType 关系类型
type RelationType string

const (
	// 常见关系类型
	RelationTypeIsA        RelationType = "IS_A"        // 是一种
	RelationTypePartOf     RelationType = "PART_OF"     // 是...的一部分
	RelationTypeHasA       RelationType = "HAS_A"       // 拥有
	RelationTypeRelatedTo  RelationType = "RELATED_TO"  // 相关
	RelationTypeCreatedBy  RelationType = "CREATED_BY"  // 由...创建
	RelationTypeLocatedIn  RelationType = "LOCATED_IN"  // 位于
	RelationTypeWorksFor   RelationType = "WORKS_FOR"   // 为...工作
	RelationTypeOccurredOn RelationType = "OCCURRED_ON" // 发生于
	RelationTypeUsedFor    RelationType = "USED_FOR"    // 用于
	RelationTypeCausedBy   RelationType = "CAUSED_BY"   // 由...引起
)

// Entity 实体节点
type Entity struct {
	// ID 实体唯一标识
	ID string `json:"id"`

	// Name 实体名称
	Name string `json:"name"`

	// Type 实体类型
	Type EntityType `json:"type"`

	// Description 实体描述
	Description string `json:"description,omitempty"`

	// Properties 实体属性
	Properties map[string]any `json:"properties,omitempty"`

	// SourceDocIDs 来源文档ID列表
	SourceDocIDs []string `json:"source_doc_ids,omitempty"`

	// Embedding 实体向量 (可选)
	Embedding []float32 `json:"embedding,omitempty"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt 更新时间
	UpdatedAt time.Time `json:"updated_at"`
}

// Relation 关系边
type Relation struct {
	// ID 关系唯一标识
	ID string `json:"id"`

	// SourceID 源实体ID
	SourceID string `json:"source_id"`

	// TargetID 目标实体ID
	TargetID string `json:"target_id"`

	// Type 关系类型
	Type RelationType `json:"type"`

	// Description 关系描述
	Description string `json:"description,omitempty"`

	// Weight 关系权重 (0-1)
	Weight float64 `json:"weight"`

	// Properties 关系属性
	Properties map[string]any `json:"properties,omitempty"`

	// SourceDocIDs 来源文档ID列表
	SourceDocIDs []string `json:"source_doc_ids,omitempty"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`
}

// Triple 三元组 (主语-谓语-宾语)
type Triple struct {
	Subject   string       `json:"subject"`
	Predicate RelationType `json:"predicate"`
	Object    string       `json:"object"`
}

// ============== GraphStore 接口 ==============

// GraphStore 图存储接口
type GraphStore interface {
	// AddEntity 添加实体
	AddEntity(ctx context.Context, entity *Entity) error

	// AddEntities 批量添加实体
	AddEntities(ctx context.Context, entities []*Entity) error

	// GetEntity 获取实体
	GetEntity(ctx context.Context, id string) (*Entity, error)

	// GetEntityByName 按名称获取实体
	GetEntityByName(ctx context.Context, name string) (*Entity, error)

	// SearchEntities 搜索实体
	SearchEntities(ctx context.Context, query EntityQuery) ([]*Entity, error)

	// UpdateEntity 更新实体
	UpdateEntity(ctx context.Context, entity *Entity) error

	// DeleteEntity 删除实体
	DeleteEntity(ctx context.Context, id string) error

	// AddRelation 添加关系
	AddRelation(ctx context.Context, relation *Relation) error

	// AddRelations 批量添加关系
	AddRelations(ctx context.Context, relations []*Relation) error

	// GetRelation 获取关系
	GetRelation(ctx context.Context, id string) (*Relation, error)

	// GetRelations 获取实体的关系
	GetRelations(ctx context.Context, entityID string, direction RelationDirection) ([]*Relation, error)

	// SearchRelations 搜索关系
	SearchRelations(ctx context.Context, query RelationQuery) ([]*Relation, error)

	// DeleteRelation 删除关系
	DeleteRelation(ctx context.Context, id string) error

	// GetNeighbors 获取邻居实体
	GetNeighbors(ctx context.Context, entityID string, depth int) ([]*Entity, error)

	// GetSubgraph 获取子图
	GetSubgraph(ctx context.Context, entityIDs []string, depth int) (*Subgraph, error)

	// Clear 清空图
	Clear(ctx context.Context) error

	// Stats 获取统计信息
	Stats(ctx context.Context) (*GraphStats, error)
}

// RelationDirection 关系方向
type RelationDirection string

const (
	RelationDirectionOutgoing RelationDirection = "outgoing" // 出边
	RelationDirectionIncoming RelationDirection = "incoming" // 入边
	RelationDirectionBoth     RelationDirection = "both"     // 双向
)

// EntityQuery 实体查询条件
type EntityQuery struct {
	// Types 实体类型过滤
	Types []EntityType

	// NamePattern 名称模式 (支持通配符)
	NamePattern string

	// Properties 属性过滤
	Properties map[string]any

	// Limit 返回数量限制
	Limit int

	// Offset 偏移量
	Offset int
}

// RelationQuery 关系查询条件
type RelationQuery struct {
	// SourceID 源实体ID
	SourceID string

	// TargetID 目标实体ID
	TargetID string

	// Types 关系类型过滤
	Types []RelationType

	// MinWeight 最小权重
	MinWeight float64

	// Limit 返回数量限制
	Limit int
}

// Subgraph 子图
type Subgraph struct {
	// Entities 实体列表
	Entities []*Entity

	// Relations 关系列表
	Relations []*Relation
}

// GraphStats 图统计信息
type GraphStats struct {
	// EntityCount 实体数量
	EntityCount int

	// RelationCount 关系数量
	RelationCount int

	// EntityTypeCounts 各类型实体数量
	EntityTypeCounts map[EntityType]int

	// RelationTypeCounts 各类型关系数量
	RelationTypeCounts map[RelationType]int
}

// ============== MemoryGraphStore 实现 ==============

// MemoryGraphStore 内存图存储
type MemoryGraphStore struct {
	entities    map[string]*Entity   // ID -> Entity
	entityNames map[string]string    // Name -> ID
	relations   map[string]*Relation // ID -> Relation
	outEdges    map[string][]string  // EntityID -> []RelationID (出边)
	inEdges     map[string][]string  // EntityID -> []RelationID (入边)
	mu          sync.RWMutex
}

// NewMemoryGraphStore 创建内存图存储
func NewMemoryGraphStore() *MemoryGraphStore {
	return &MemoryGraphStore{
		entities:    make(map[string]*Entity),
		entityNames: make(map[string]string),
		relations:   make(map[string]*Relation),
		outEdges:    make(map[string][]string),
		inEdges:     make(map[string][]string),
	}
}

// AddEntity 添加实体
func (s *MemoryGraphStore) AddEntity(ctx context.Context, entity *Entity) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entity.ID == "" {
		entity.ID = util.GenerateID("entity")
	}
	if entity.CreatedAt.IsZero() {
		entity.CreatedAt = time.Now()
	}
	entity.UpdatedAt = time.Now()

	s.entities[entity.ID] = entity
	s.entityNames[strings.ToLower(entity.Name)] = entity.ID

	return nil
}

// AddEntities 批量添加实体
func (s *MemoryGraphStore) AddEntities(ctx context.Context, entities []*Entity) error {
	for _, entity := range entities {
		if err := s.AddEntity(ctx, entity); err != nil {
			return err
		}
	}
	return nil
}

// GetEntity 获取实体
func (s *MemoryGraphStore) GetEntity(ctx context.Context, id string) (*Entity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entity, ok := s.entities[id]
	if !ok {
		return nil, fmt.Errorf("entity not found: %s", id)
	}
	return entity, nil
}

// GetEntityByName 按名称获取实体
func (s *MemoryGraphStore) GetEntityByName(ctx context.Context, name string) (*Entity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.entityNames[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("entity not found by name: %s", name)
	}
	return s.entities[id], nil
}

// SearchEntities 搜索实体
func (s *MemoryGraphStore) SearchEntities(ctx context.Context, query EntityQuery) ([]*Entity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*Entity

	typeSet := make(map[EntityType]bool)
	for _, t := range query.Types {
		typeSet[t] = true
	}

	var pattern *regexp.Regexp
	if query.NamePattern != "" {
		// 将通配符转换为正则
		regexPattern := strings.ReplaceAll(query.NamePattern, "*", ".*")
		regexPattern = strings.ReplaceAll(regexPattern, "?", ".")
		var err error
		pattern, err = regexp.Compile("(?i)" + regexPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid name pattern: %w", err)
		}
	}

	for _, entity := range s.entities {
		// 类型过滤
		if len(typeSet) > 0 && !typeSet[entity.Type] {
			continue
		}

		// 名称过滤
		if pattern != nil && !pattern.MatchString(entity.Name) {
			continue
		}

		// 属性过滤
		if len(query.Properties) > 0 {
			match := true
			for k, v := range query.Properties {
				if entity.Properties[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		results = append(results, entity)
	}

	// 分页
	// Offset 越界 (>= 结果数) 时应返回空集而非全量数据:
	// 原条件 `Offset > 0 && Offset < len` 在 Offset 越界时不成立, 切片被跳过,
	// 错误地返回了全部结果, 违反分页语义。
	if query.Offset > 0 {
		if query.Offset >= len(results) {
			results = nil
		} else {
			results = results[query.Offset:]
		}
	}
	if query.Limit > 0 && query.Limit < len(results) {
		results = results[:query.Limit]
	}

	return results, nil
}

// UpdateEntity 更新实体
func (s *MemoryGraphStore) UpdateEntity(ctx context.Context, entity *Entity) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.entities[entity.ID]; !ok {
		return fmt.Errorf("entity not found: %s", entity.ID)
	}

	// 实体改名时清理旧名索引, 否则 entityNames[旧名] 仍指向该实体,
	// GetEntityByName(旧名) 会查到脏数据。
	//
	// 注意: 不能依赖 s.entities[entity.ID].Name 推断旧名 —— AddEntity 直接保存了
	// 调用方传入的指针, 若调用方在 UpdateEntity 前就地修改了同一对象的 Name,
	// 则旧实体的 Name 已等于新名, 无法判断改名。这里以 entityNames 反向索引为准:
	// 删除所有指向本实体 ID 但 key 不等于新名的脏条目, 再写入新名索引。
	newName := strings.ToLower(entity.Name)
	for name, id := range s.entityNames {
		if id == entity.ID && name != newName {
			delete(s.entityNames, name)
		}
	}

	entity.UpdatedAt = time.Now()
	s.entities[entity.ID] = entity
	s.entityNames[newName] = entity.ID

	return nil
}

// DeleteEntity 删除实体
func (s *MemoryGraphStore) DeleteEntity(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entity, ok := s.entities[id]
	if !ok {
		return fmt.Errorf("entity not found: %s", id)
	}

	// 删除相关关系, 并清理另一端实体上残留的边引用,
	// 否则邻居实体的 in/outEdges 会残留已删关系的 relID (悬空边引用),
	// 导致 GetNeighbors / GetSubgraph 遍历时持续累积无效 relID。
	for _, relID := range s.outEdges[id] {
		// 出边: 另一端是关系的 TargetID, 其 inEdges 持有该 relID。
		if rel, ok := s.relations[relID]; ok && rel.TargetID != id {
			s.inEdges[rel.TargetID] = removeFromSlice(s.inEdges[rel.TargetID], relID)
		}
		delete(s.relations, relID)
	}
	for _, relID := range s.inEdges[id] {
		// 入边: 另一端是关系的 SourceID, 其 outEdges 持有该 relID。
		if rel, ok := s.relations[relID]; ok && rel.SourceID != id {
			s.outEdges[rel.SourceID] = removeFromSlice(s.outEdges[rel.SourceID], relID)
		}
		delete(s.relations, relID)
	}

	delete(s.outEdges, id)
	delete(s.inEdges, id)
	delete(s.entityNames, strings.ToLower(entity.Name))
	delete(s.entities, id)

	return nil
}

// AddRelation 添加关系
func (s *MemoryGraphStore) AddRelation(ctx context.Context, relation *Relation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 验证实体存在
	if _, ok := s.entities[relation.SourceID]; !ok {
		return fmt.Errorf("source entity not found: %s", relation.SourceID)
	}
	if _, ok := s.entities[relation.TargetID]; !ok {
		return fmt.Errorf("target entity not found: %s", relation.TargetID)
	}

	if relation.ID == "" {
		relation.ID = util.GenerateID("rel")
	}
	if relation.CreatedAt.IsZero() {
		relation.CreatedAt = time.Now()
	}
	if relation.Weight == 0 {
		relation.Weight = 1.0
	}

	// 重复添加同 ID 关系时, relations 表会被覆盖 (仍为 1 条), 但若无条件 append,
	// outEdges/inEdges 会膨胀为多条相同 relID → 边列表与关系表不一致;
	// 且 DeleteRelation 用 removeFromSlice 只删首个匹配, 会残留悬空 relID。
	// 因此对已存在的关系 ID 仅更新关系本体, 不再追加边引用。
	_, exists := s.relations[relation.ID]
	s.relations[relation.ID] = relation
	if !exists {
		s.outEdges[relation.SourceID] = append(s.outEdges[relation.SourceID], relation.ID)
		s.inEdges[relation.TargetID] = append(s.inEdges[relation.TargetID], relation.ID)
	}

	return nil
}

// AddRelations 批量添加关系
func (s *MemoryGraphStore) AddRelations(ctx context.Context, relations []*Relation) error {
	for _, rel := range relations {
		if err := s.AddRelation(ctx, rel); err != nil {
			return err
		}
	}
	return nil
}

// GetRelation 获取关系
func (s *MemoryGraphStore) GetRelation(ctx context.Context, id string) (*Relation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rel, ok := s.relations[id]
	if !ok {
		return nil, fmt.Errorf("relation not found: %s", id)
	}
	return rel, nil
}

// GetRelations 获取实体的关系
func (s *MemoryGraphStore) GetRelations(ctx context.Context, entityID string, direction RelationDirection) ([]*Relation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*Relation

	if direction == RelationDirectionOutgoing || direction == RelationDirectionBoth {
		for _, relID := range s.outEdges[entityID] {
			if rel, ok := s.relations[relID]; ok {
				results = append(results, rel)
			}
		}
	}

	if direction == RelationDirectionIncoming || direction == RelationDirectionBoth {
		for _, relID := range s.inEdges[entityID] {
			if rel, ok := s.relations[relID]; ok {
				results = append(results, rel)
			}
		}
	}

	return results, nil
}

// SearchRelations 搜索关系
func (s *MemoryGraphStore) SearchRelations(ctx context.Context, query RelationQuery) ([]*Relation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*Relation

	typeSet := make(map[RelationType]bool)
	for _, t := range query.Types {
		typeSet[t] = true
	}

	for _, rel := range s.relations {
		// 源实体过滤
		if query.SourceID != "" && rel.SourceID != query.SourceID {
			continue
		}

		// 目标实体过滤
		if query.TargetID != "" && rel.TargetID != query.TargetID {
			continue
		}

		// 类型过滤
		if len(typeSet) > 0 && !typeSet[rel.Type] {
			continue
		}

		// 权重过滤
		if rel.Weight < query.MinWeight {
			continue
		}

		results = append(results, rel)
	}

	if query.Limit > 0 && query.Limit < len(results) {
		results = results[:query.Limit]
	}

	return results, nil
}

// DeleteRelation 删除关系
func (s *MemoryGraphStore) DeleteRelation(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rel, ok := s.relations[id]
	if !ok {
		return fmt.Errorf("relation not found: %s", id)
	}

	// 从边列表中移除
	s.outEdges[rel.SourceID] = removeFromSlice(s.outEdges[rel.SourceID], id)
	s.inEdges[rel.TargetID] = removeFromSlice(s.inEdges[rel.TargetID], id)

	delete(s.relations, id)
	return nil
}

// GetNeighbors 获取邻居实体
func (s *MemoryGraphStore) GetNeighbors(ctx context.Context, entityID string, depth int) ([]*Entity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	visited := make(map[string]bool)
	var results []*Entity

	queue := []string{entityID}
	visited[entityID] = true

	for d := 0; d < depth && len(queue) > 0; d++ {
		var nextQueue []string
		for _, id := range queue {
			// 获取出边邻居
			for _, relID := range s.outEdges[id] {
				if rel, ok := s.relations[relID]; ok {
					if !visited[rel.TargetID] {
						visited[rel.TargetID] = true
						nextQueue = append(nextQueue, rel.TargetID)
						if entity, ok := s.entities[rel.TargetID]; ok {
							results = append(results, entity)
						}
					}
				}
			}
			// 获取入边邻居
			for _, relID := range s.inEdges[id] {
				if rel, ok := s.relations[relID]; ok {
					if !visited[rel.SourceID] {
						visited[rel.SourceID] = true
						nextQueue = append(nextQueue, rel.SourceID)
						if entity, ok := s.entities[rel.SourceID]; ok {
							results = append(results, entity)
						}
					}
				}
			}
		}
		queue = nextQueue
	}

	return results, nil
}

// GetSubgraph 获取子图
func (s *MemoryGraphStore) GetSubgraph(ctx context.Context, entityIDs []string, depth int) (*Subgraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entitySet := make(map[string]bool)
	relationSet := make(map[string]bool)

	// 先添加种子实体
	for _, id := range entityIDs {
		entitySet[id] = true
	}

	// BFS 扩展
	queue := make([]string, len(entityIDs))
	copy(queue, entityIDs)

	for d := 0; d < depth && len(queue) > 0; d++ {
		var nextQueue []string
		for _, id := range queue {
			for _, relID := range s.outEdges[id] {
				if rel, ok := s.relations[relID]; ok {
					relationSet[relID] = true
					if !entitySet[rel.TargetID] {
						entitySet[rel.TargetID] = true
						nextQueue = append(nextQueue, rel.TargetID)
					}
				}
			}
			for _, relID := range s.inEdges[id] {
				if rel, ok := s.relations[relID]; ok {
					relationSet[relID] = true
					if !entitySet[rel.SourceID] {
						entitySet[rel.SourceID] = true
						nextQueue = append(nextQueue, rel.SourceID)
					}
				}
			}
		}
		queue = nextQueue
	}

	// 构建子图
	subgraph := &Subgraph{}
	for id := range entitySet {
		if entity, ok := s.entities[id]; ok {
			subgraph.Entities = append(subgraph.Entities, entity)
		}
	}
	for id := range relationSet {
		if rel, ok := s.relations[id]; ok {
			subgraph.Relations = append(subgraph.Relations, rel)
		}
	}

	return subgraph, nil
}

// Clear 清空图
func (s *MemoryGraphStore) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entities = make(map[string]*Entity)
	s.entityNames = make(map[string]string)
	s.relations = make(map[string]*Relation)
	s.outEdges = make(map[string][]string)
	s.inEdges = make(map[string][]string)

	return nil
}

// Stats 获取统计信息
func (s *MemoryGraphStore) Stats(ctx context.Context) (*GraphStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &GraphStats{
		EntityCount:        len(s.entities),
		RelationCount:      len(s.relations),
		EntityTypeCounts:   make(map[EntityType]int),
		RelationTypeCounts: make(map[RelationType]int),
	}

	for _, entity := range s.entities {
		stats.EntityTypeCounts[entity.Type]++
	}

	for _, rel := range s.relations {
		stats.RelationTypeCounts[rel.Type]++
	}

	return stats, nil
}

var _ GraphStore = (*MemoryGraphStore)(nil)

// ============== EntityExtractor 实体抽取器 ==============

// EntityExtractor 实体抽取器接口
type EntityExtractor interface {
	// Extract 从文本中抽取实体和关系
	Extract(ctx context.Context, text string) ([]*Entity, []*Relation, error)
}

// LLMEntityExtractor 基于 LLM 的实体抽取器
type LLMEntityExtractor struct {
	provider    llm.Provider
	model       string
	entityTypes []EntityType
}

// LLMExtractorOption LLM 抽取器选项
type LLMExtractorOption func(*LLMEntityExtractor)

// WithExtractorModel 设置模型
func WithExtractorModel(model string) LLMExtractorOption {
	return func(e *LLMEntityExtractor) {
		e.model = model
	}
}

// WithEntityTypes 设置要抽取的实体类型
func WithEntityTypes(types ...EntityType) LLMExtractorOption {
	return func(e *LLMEntityExtractor) {
		e.entityTypes = types
	}
}

// NewLLMEntityExtractor 创建 LLM 实体抽取器
func NewLLMEntityExtractor(provider llm.Provider, opts ...LLMExtractorOption) *LLMEntityExtractor {
	e := &LLMEntityExtractor{
		provider: provider,
		entityTypes: []EntityType{
			EntityTypePerson,
			EntityTypeOrganization,
			EntityTypeLocation,
			EntityTypeConcept,
		},
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Extract 从文本中抽取实体和关系
func (e *LLMEntityExtractor) Extract(ctx context.Context, text string) ([]*Entity, []*Relation, error) {
	// 构建提示词
	prompt := e.buildExtractionPrompt(text)

	req := llm.CompletionRequest{
		Model: e.model,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
	}

	resp, err := e.provider.Complete(ctx, req)
	if err != nil {
		return nil, nil, fmt.Errorf("LLM extraction failed: %w", err)
	}

	// 解析响应
	return e.parseExtractionResponse(resp.Content)
}

// buildExtractionPrompt 构建抽取提示词
func (e *LLMEntityExtractor) buildExtractionPrompt(text string) string {
	typeList := make([]string, len(e.entityTypes))
	for i, t := range e.entityTypes {
		typeList[i] = string(t)
	}

	return fmt.Sprintf(`从以下文本中提取实体和关系。

实体类型: %s

输出格式 (每行一个):
ENTITY: <名称> | <类型> | <描述>
RELATION: <源实体> | <关系类型> | <目标实体>

关系类型包括: IS_A, PART_OF, HAS_A, RELATED_TO, CREATED_BY, LOCATED_IN, WORKS_FOR, OCCURRED_ON, USED_FOR, CAUSED_BY

文本:
%s

请提取所有实体和关系:`, strings.Join(typeList, ", "), text)
}

// parseExtractionResponse 解析抽取响应
func (e *LLMEntityExtractor) parseExtractionResponse(content string) ([]*Entity, []*Relation, error) {
	var entities []*Entity
	var relations []*Relation
	entityMap := make(map[string]*Entity)

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ENTITY:") {
			parts := strings.Split(strings.TrimPrefix(line, "ENTITY:"), "|")
			if len(parts) >= 2 {
				name := strings.TrimSpace(parts[0])
				typeStr := strings.TrimSpace(parts[1])
				var desc string
				if len(parts) >= 3 {
					desc = strings.TrimSpace(parts[2])
				}

				entity := &Entity{
					ID:          util.GenerateID("entity"),
					Name:        name,
					Type:        EntityType(typeStr),
					Description: desc,
					CreatedAt:   time.Now(),
					UpdatedAt:   time.Now(),
				}
				entities = append(entities, entity)
				entityMap[strings.ToLower(name)] = entity
			}
		} else if strings.HasPrefix(line, "RELATION:") {
			parts := strings.Split(strings.TrimPrefix(line, "RELATION:"), "|")
			if len(parts) >= 3 {
				sourceName := strings.TrimSpace(parts[0])
				relType := strings.TrimSpace(parts[1])
				targetName := strings.TrimSpace(parts[2])

				sourceEntity := entityMap[strings.ToLower(sourceName)]
				targetEntity := entityMap[strings.ToLower(targetName)]

				if sourceEntity != nil && targetEntity != nil {
					relation := &Relation{
						ID:        util.GenerateID("rel"),
						SourceID:  sourceEntity.ID,
						TargetID:  targetEntity.ID,
						Type:      RelationType(relType),
						Weight:    1.0,
						CreatedAt: time.Now(),
					}
					relations = append(relations, relation)
				}
			}
		}
	}

	return entities, relations, nil
}

// RuleBasedExtractor 基于规则的实体抽取器
type RuleBasedExtractor struct {
	patterns map[EntityType][]*regexp.Regexp
}

// NewRuleBasedExtractor 创建基于规则的抽取器
func NewRuleBasedExtractor() *RuleBasedExtractor {
	e := &RuleBasedExtractor{
		patterns: make(map[EntityType][]*regexp.Regexp),
	}

	// 添加常见模式
	e.AddPattern(EntityTypeDate, `\d{4}[-/]\d{1,2}[-/]\d{1,2}`)
	e.AddPattern(EntityTypeDate, `\d{4}年\d{1,2}月\d{1,2}日`)

	return e
}

// AddPattern 添加抽取模式
func (e *RuleBasedExtractor) AddPattern(entityType EntityType, pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}
	e.patterns[entityType] = append(e.patterns[entityType], re)
	return nil
}

// Extract 从文本中抽取实体
func (e *RuleBasedExtractor) Extract(ctx context.Context, text string) ([]*Entity, []*Relation, error) {
	var entities []*Entity
	seen := make(map[string]bool)

	for entityType, patterns := range e.patterns {
		for _, pattern := range patterns {
			matches := pattern.FindAllString(text, -1)
			for _, match := range matches {
				key := string(entityType) + ":" + match
				if !seen[key] {
					seen[key] = true
					entities = append(entities, &Entity{
						ID:        util.GenerateID("entity"),
						Name:      match,
						Type:      entityType,
						CreatedAt: time.Now(),
						UpdatedAt: time.Now(),
					})
				}
			}
		}
	}

	return entities, nil, nil
}

// ============== GraphIndexer ==============

// GraphIndexer 知识图谱索引器
type GraphIndexer struct {
	store     GraphStore
	extractor EntityExtractor
	batchSize int
}

// GraphIndexerOption GraphIndexer 选项
type GraphIndexerOption func(*GraphIndexer)

// WithGraphBatchSize 设置批量大小
func WithGraphBatchSize(size int) GraphIndexerOption {
	return func(i *GraphIndexer) {
		i.batchSize = size
	}
}

// WithExtractor 设置实体抽取器
func WithExtractor(extractor EntityExtractor) GraphIndexerOption {
	return func(i *GraphIndexer) {
		i.extractor = extractor
	}
}

// NewGraphIndexer 创建知识图谱索引器
func NewGraphIndexer(store GraphStore, opts ...GraphIndexerOption) *GraphIndexer {
	idx := &GraphIndexer{
		store:     store,
		batchSize: 10,
	}
	for _, opt := range opts {
		opt(idx)
	}
	// 默认使用规则抽取器
	if idx.extractor == nil {
		idx.extractor = NewRuleBasedExtractor()
	}
	return idx
}

// Index 索引文档到知识图谱
func (i *GraphIndexer) Index(ctx context.Context, docs []rag.Document) error {
	for _, doc := range docs {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// 抽取实体和关系
		entities, relations, err := i.extractor.Extract(ctx, doc.Content)
		if err != nil {
			return fmt.Errorf("extraction failed for doc %s: %w", doc.ID, err)
		}

		// 为实体添加文档来源
		for _, entity := range entities {
			entity.SourceDocIDs = append(entity.SourceDocIDs, doc.ID)
		}

		// 为关系添加文档来源
		for _, rel := range relations {
			rel.SourceDocIDs = append(rel.SourceDocIDs, doc.ID)
		}

		// 存储实体
		if err := i.store.AddEntities(ctx, entities); err != nil {
			return fmt.Errorf("failed to add entities: %w", err)
		}

		// 存储关系
		if err := i.store.AddRelations(ctx, relations); err != nil {
			return fmt.Errorf("failed to add relations: %w", err)
		}
	}

	return nil
}

// Delete 删除文档相关实体
func (i *GraphIndexer) Delete(ctx context.Context, ids []string) error {
	// 搜索来源于这些文档的实体
	for _, docID := range ids {
		entities, err := i.store.SearchEntities(ctx, EntityQuery{
			Limit: DefaultEntityQueryLimit,
		})
		if err != nil {
			return err
		}

		for _, entity := range entities {
			for _, sourceID := range entity.SourceDocIDs {
				if sourceID == docID {
					if err := i.store.DeleteEntity(ctx, entity.ID); err != nil {
						return err
					}
					break
				}
			}
		}
	}
	return nil
}

// Clear 清空索引
func (i *GraphIndexer) Clear(ctx context.Context) error {
	return i.store.Clear(ctx)
}

// Count 返回实体数量
func (i *GraphIndexer) Count(ctx context.Context) (int, error) {
	stats, err := i.store.Stats(ctx)
	if err != nil {
		return 0, err
	}
	return stats.EntityCount, nil
}

// Store 获取底层图存储
func (i *GraphIndexer) Store() GraphStore {
	return i.store
}

var _ rag.Indexer = (*GraphIndexer)(nil)

// ============== GraphRetriever ==============

// GraphRetriever 基于知识图谱的检索器
type GraphRetriever struct {
	store      GraphStore
	maxDepth   int
	maxResults int
}

// GraphRetrieverOption GraphRetriever 选项
type GraphRetrieverOption func(*GraphRetriever)

// WithMaxDepth 设置最大遍历深度
func WithMaxDepth(depth int) GraphRetrieverOption {
	return func(r *GraphRetriever) {
		r.maxDepth = depth
	}
}

// WithMaxResults 设置最大结果数
func WithMaxResults(max int) GraphRetrieverOption {
	return func(r *GraphRetriever) {
		r.maxResults = max
	}
}

// NewGraphRetriever 创建图检索器
func NewGraphRetriever(store GraphStore, opts ...GraphRetrieverOption) *GraphRetriever {
	r := &GraphRetriever{
		store:      store,
		maxDepth:   2,
		maxResults: 10,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Retrieve 基于查询检索相关实体和上下文
func (r *GraphRetriever) Retrieve(ctx context.Context, query string) (*GraphContext, error) {
	// 1. 在图中搜索匹配的实体。
	//    用户查询是任意文本, 直接拼进 NamePattern 会被 SearchEntities 当作正则编译,
	//    若含 '(' / '[' 等正则元字符 (如 "C++"、"foo(bar") 会导致 regexp.Compile 失败报错。
	//    这里对用户输入做 regexp.QuoteMeta 转义, 使其作为字面量参与子串匹配,
	//    外层的 '*' 通配符仍由 SearchEntities 解释为 ".*"。
	entities, err := r.store.SearchEntities(ctx, EntityQuery{
		NamePattern: "*" + regexp.QuoteMeta(query) + "*",
		Limit:       r.maxResults,
	})
	if err != nil {
		return nil, err
	}

	if len(entities) == 0 {
		return &GraphContext{}, nil
	}

	// 2. 获取子图
	entityIDs := make([]string, len(entities))
	for i, e := range entities {
		entityIDs[i] = e.ID
	}

	subgraph, err := r.store.GetSubgraph(ctx, entityIDs, r.maxDepth)
	if err != nil {
		return nil, err
	}

	// 3. 构建上下文
	return &GraphContext{
		Entities:  subgraph.Entities,
		Relations: subgraph.Relations,
		Triples:   r.buildTriples(subgraph),
	}, nil
}

// buildTriples 从子图构建三元组
func (r *GraphRetriever) buildTriples(subgraph *Subgraph) []Triple {
	entityMap := make(map[string]*Entity)
	for _, e := range subgraph.Entities {
		entityMap[e.ID] = e
	}

	var triples []Triple
	for _, rel := range subgraph.Relations {
		source := entityMap[rel.SourceID]
		target := entityMap[rel.TargetID]
		if source != nil && target != nil {
			triples = append(triples, Triple{
				Subject:   source.Name,
				Predicate: rel.Type,
				Object:    target.Name,
			})
		}
	}

	return triples
}

// GraphContext 图检索上下文
type GraphContext struct {
	// Entities 相关实体
	Entities []*Entity

	// Relations 相关关系
	Relations []*Relation

	// Triples 三元组列表
	Triples []Triple
}

// ToText 转换为文本描述
func (gc *GraphContext) ToText() string {
	var sb strings.Builder

	if len(gc.Entities) > 0 {
		sb.WriteString("实体:\n")
		for _, e := range gc.Entities {
			sb.WriteString(fmt.Sprintf("- %s (%s)", e.Name, e.Type))
			if e.Description != "" {
				sb.WriteString(": " + e.Description)
			}
			sb.WriteString("\n")
		}
	}

	if len(gc.Triples) > 0 {
		sb.WriteString("\n关系:\n")
		for _, t := range gc.Triples {
			sb.WriteString(fmt.Sprintf("- %s %s %s\n", t.Subject, t.Predicate, t.Object))
		}
	}

	return sb.String()
}

// ============== 辅助函数 ==============

// removeFromSlice 从切片中移除元素
func removeFromSlice(slice []string, elem string) []string {
	for i, v := range slice {
		if v == elem {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}
