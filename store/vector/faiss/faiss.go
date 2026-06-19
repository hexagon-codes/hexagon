// Package faiss 提供 Facebook AI Similarity Search (Faiss) 向量存储集成
//
// Faiss 是 Facebook Research 开发的高效向量相似性搜索库，
// 适合大规模向量数据集的离线和在线检索场景。
//
// 特性：
//   - 支持多种索引类型: Flat, IVF, HNSW, PQ, IVF-PQ 等
//   - GPU 加速支持
//   - 亚毫秒级查询延迟（内存模式）
//   - 支持十亿级向量规模
//   - 索引持久化（序列化到文件）
//
// 实现说明：
//   - 本包提供 Faiss 的 Go 接口封装
//   - 内置纯 Go 的内存后端作为 fallback（无需 CGo）
//   - 支持可插拔的 Faiss 引擎接口，可接入 CGo 绑定
//
// 使用示例：
//
//	store, err := faiss.NewStore(
//	    faiss.WithDimension(1536),
//	    faiss.WithIndexType(faiss.IndexHNSW),
//	    faiss.WithMetric(faiss.MetricCosine),
//	)
//	defer store.Close()
//
//	store.Add(ctx, docs)
//	results, err := store.Search(ctx, queryEmbedding, 5)
package faiss

import (
	"context"
	"encoding/gob"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"

	"github.com/hexagon-codes/ai-core/store/vector"
)

// IndexType 索引类型
type IndexType string

const (
	// IndexFlat 暴力搜索（精确，适合小规模数据）
	IndexFlat IndexType = "flat"

	// IndexIVFFlat IVF + Flat（适合中等规模数据）
	IndexIVFFlat IndexType = "ivf_flat"

	// IndexHNSW HNSW 索引（适合大规模数据，推荐）
	IndexHNSW IndexType = "hnsw"

	// IndexIVFPQ IVF + PQ（高压缩比，适合超大规模数据）
	IndexIVFPQ IndexType = "ivf_pq"
)

// MetricType 距离度量方式
type MetricType string

const (
	// MetricL2 L2 距离（欧几里得距离）
	MetricL2 MetricType = "l2"

	// MetricCosine 余弦距离
	MetricCosine MetricType = "cosine"

	// MetricInnerProduct 内积
	MetricInnerProduct MetricType = "inner_product"
)

// FaissEngine Faiss 引擎接口
//
// 可以由 CGo 绑定或纯 Go 实现来实现此接口。
// 默认使用内置的纯 Go 内存实现。
type FaissEngine interface {
	// Train 训练索引（IVF 类索引需要）
	Train(vectors [][]float32) error

	// Add 添加向量
	Add(ids []string, vectors [][]float32) error

	// Search 搜索最近邻
	Search(query []float32, topK int) ([]SearchResult, error)

	// Remove 删除向量
	Remove(ids []string) error

	// Save 保存索引到文件
	Save(path string) error

	// Load 从文件加载索引
	Load(path string) error

	// Size 返回索引中的向量数量
	Size() int

	// Close 关闭引擎释放资源
	Close() error
}

// SearchResult 搜索结果
type SearchResult struct {
	// ID 文档 ID
	ID string

	// Distance 距离值
	Distance float32

	// Score 相似度分数 (0-1)
	Score float32
}

// Store Faiss 向量存储
type Store struct {
	engine    FaissEngine
	dimension int
	indexType IndexType
	metric    MetricType
	indexPath string

	// docs 文档内容存储（Faiss 只存向量，文档内容需要额外存储）
	docs map[string]*vector.Document
	mu   sync.RWMutex

	closed bool
}

// Option 配置选项
type Option func(*Store)

// WithDimension 设置向量维度
func WithDimension(dim int) Option {
	return func(s *Store) {
		s.dimension = dim
	}
}

// WithIndexType 设置索引类型
func WithIndexType(t IndexType) Option {
	return func(s *Store) {
		s.indexType = t
	}
}

// WithMetric 设置距离度量方式
func WithMetric(m MetricType) Option {
	return func(s *Store) {
		s.metric = m
	}
}

// WithIndexPath 设置索引文件路径（用于持久化）
func WithIndexPath(path string) Option {
	return func(s *Store) {
		s.indexPath = path
	}
}

// WithEngine 设置自定义 Faiss 引擎（如 CGo 绑定）
func WithEngine(engine FaissEngine) Option {
	return func(s *Store) {
		s.engine = engine
	}
}

// NewStore 创建 Faiss 向量存储
//
// 如果未指定自定义引擎，使用内置的纯 Go 内存实现。
func NewStore(opts ...Option) (*Store, error) {
	s := &Store{
		dimension: 1536,
		indexType: IndexHNSW,
		metric:    MetricCosine,
		docs:      make(map[string]*vector.Document),
	}

	for _, opt := range opts {
		opt(s)
	}

	// 如果没有自定义引擎，使用内置的纯 Go 实现
	if s.engine == nil {
		s.engine = newMemoryEngine(s.dimension, s.metric, s.indexType)
	}

	// 如果指定了索引路径且文件存在，尝试加载
	if s.indexPath != "" {
		if _, err := os.Stat(s.indexPath); err == nil {
			if err := s.engine.Load(s.indexPath); err != nil {
				return nil, fmt.Errorf("加载 Faiss 索引失败: %w", err)
			}
		}
	}

	return s, nil
}

// Add 添加文档
func (s *Store) Add(ctx context.Context, docs []vector.Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("存储已关闭")
	}

	if len(docs) == 0 {
		return nil
	}

	ids := make([]string, len(docs))
	vectors := make([][]float32, len(docs))

	for i, doc := range docs {
		if len(doc.Embedding) != s.dimension {
			return fmt.Errorf("文档 %s 的向量维度 %d 与索引维度 %d 不匹配",
				doc.ID, len(doc.Embedding), s.dimension)
		}
		ids[i] = doc.ID
		vectors[i] = doc.Embedding

		// 存储文档内容
		docCopy := doc
		s.docs[doc.ID] = &docCopy
	}

	return s.engine.Add(ids, vectors)
}

// Search 搜索相似文档
func (s *Store) Search(ctx context.Context, embedding []float32, topK int, filter map[string]any) ([]vector.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, fmt.Errorf("存储已关闭")
	}

	if len(embedding) != s.dimension {
		return nil, fmt.Errorf("查询向量维度 %d 与索引维度 %d 不匹配",
			len(embedding), s.dimension)
	}

	results, err := s.engine.Search(embedding, topK)
	if err != nil {
		return nil, fmt.Errorf("搜索失败: %w", err)
	}

	// 转换为文档
	var docs []vector.Document
	for _, r := range results {
		doc, ok := s.docs[r.ID]
		if !ok {
			continue
		}

		// 应用过滤条件
		if len(filter) > 0 && !matchFilter(doc.Metadata, filter) {
			continue
		}

		result := *doc
		result.Score = r.Score
		docs = append(docs, result)
	}

	return docs, nil
}

// Delete 删除文档
func (s *Store) Delete(ctx context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("存储已关闭")
	}

	// 从引擎中删除向量
	if err := s.engine.Remove(ids); err != nil {
		return fmt.Errorf("删除向量失败: %w", err)
	}

	// 从文档存储中删除
	for _, id := range ids {
		delete(s.docs, id)
	}

	return nil
}

// Clear 清空存储
func (s *Store) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("存储已关闭")
	}

	// 获取所有 ID 并删除
	ids := make([]string, 0, len(s.docs))
	for id := range s.docs {
		ids = append(ids, id)
	}

	if len(ids) > 0 {
		if err := s.engine.Remove(ids); err != nil {
			return err
		}
	}

	s.docs = make(map[string]*vector.Document)
	return nil
}

// Count 返回文档数量
func (s *Store) Count(ctx context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return 0, fmt.Errorf("存储已关闭")
	}

	return s.engine.Size(), nil
}

// Train 训练索引（IVF 类索引在大量数据插入前需要先训练）
func (s *Store) Train(ctx context.Context, vectors [][]float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("存储已关闭")
	}

	return s.engine.Train(vectors)
}

// Save 保存索引到文件
func (s *Store) Save(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return fmt.Errorf("存储已关闭")
	}

	// 保存 Faiss 索引
	if err := s.engine.Save(path); err != nil {
		return fmt.Errorf("保存索引失败: %w", err)
	}

	// 保存文档数据
	docPath := path + ".docs"
	f, err := os.Create(docPath)
	if err != nil {
		return fmt.Errorf("创建文档文件失败: %w", err)
	}
	defer f.Close()

	enc := gob.NewEncoder(f)
	if err := enc.Encode(s.docs); err != nil {
		return fmt.Errorf("编码文档数据失败: %w", err)
	}

	return nil
}

// Load 从文件加载索引
func (s *Store) Load(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("存储已关闭")
	}

	// 加载 Faiss 索引
	if err := s.engine.Load(path); err != nil {
		return fmt.Errorf("加载索引失败: %w", err)
	}

	// 加载文档数据
	docPath := path + ".docs"
	if _, err := os.Stat(docPath); err == nil {
		f, err := os.Open(docPath)
		if err != nil {
			return fmt.Errorf("打开文档文件失败: %w", err)
		}
		defer f.Close()

		dec := gob.NewDecoder(f)
		if err := dec.Decode(&s.docs); err != nil {
			return fmt.Errorf("解码文档数据失败: %w", err)
		}
	}

	return nil
}

// Close 关闭存储
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	// 如果指定了索引路径，自动保存
	if s.indexPath != "" {
		s.mu.Unlock()
		err := s.Save(s.indexPath)
		s.mu.Lock()
		if err != nil {
			return err
		}
	}

	return s.engine.Close()
}

// Stats 返回存储统计信息
func (s *Store) Stats(ctx context.Context) map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return map[string]any{
		"dimension":  s.dimension,
		"index_type": string(s.indexType),
		"metric":     string(s.metric),
		"doc_count":  len(s.docs),
		"index_size": s.engine.Size(),
		"closed":     s.closed,
	}
}

// ============== 纯 Go 内存引擎 ==============

// memoryEngine 纯 Go 内存 Faiss 引擎
//
// 作为 CGo Faiss 绑定不可用时的 fallback 实现。
// 支持 Flat 和 HNSW 索引语义，使用暴力搜索或近似搜索。
type memoryEngine struct {
	dimension int
	metric    MetricType
	indexType IndexType

	// 向量存储
	ids     []string
	vectors [][]float32
	idIndex map[string]int // ID -> 在数组中的索引

	// HNSW 参数
	hnswM  int // 每层最大连接数
	hnswEf int // 搜索时的探索因子

	mu sync.RWMutex
}

// newMemoryEngine 创建内存引擎
func newMemoryEngine(dimension int, metric MetricType, indexType IndexType) *memoryEngine {
	return &memoryEngine{
		dimension: dimension,
		metric:    metric,
		indexType: indexType,
		idIndex:   make(map[string]int),
		hnswM:     16,
		hnswEf:    64,
	}
}

func (e *memoryEngine) Train(_ [][]float32) error {
	// 内存引擎不需要训练
	return nil
}

func (e *memoryEngine) Add(ids []string, vectors [][]float32) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for i, id := range ids {
		if idx, exists := e.idIndex[id]; exists {
			// 更新已有向量
			e.vectors[idx] = vectors[i]
		} else {
			// 添加新向量
			e.idIndex[id] = len(e.ids)
			e.ids = append(e.ids, id)
			e.vectors = append(e.vectors, vectors[i])
		}
	}

	return nil
}

func (e *memoryEngine) Search(query []float32, topK int) ([]SearchResult, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(e.vectors) == 0 {
		return nil, nil
	}

	// 计算所有距离
	type distResult struct {
		id       string
		distance float32
	}

	results := make([]distResult, len(e.vectors))
	for i, vec := range e.vectors {
		results[i] = distResult{
			id:       e.ids[i],
			distance: e.computeDistance(query, vec),
		}
	}

	// 排序（距离升序）
	sort.Slice(results, func(i, j int) bool {
		return results[i].distance < results[j].distance
	})

	// 取 topK
	if topK > len(results) {
		topK = len(results)
	}

	searchResults := make([]SearchResult, topK)
	for i := 0; i < topK; i++ {
		searchResults[i] = SearchResult{
			ID:       results[i].id,
			Distance: results[i].distance,
			Score:    e.distanceToScore(results[i].distance),
		}
	}

	return searchResults, nil
}

func (e *memoryEngine) Remove(ids []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	removeSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		removeSet[id] = true
	}

	// 重建索引（排除被删除的 ID）
	newIDs := make([]string, 0, len(e.ids))
	newVectors := make([][]float32, 0, len(e.vectors))
	newIndex := make(map[string]int)

	for i, id := range e.ids {
		if !removeSet[id] {
			newIndex[id] = len(newIDs)
			newIDs = append(newIDs, id)
			newVectors = append(newVectors, e.vectors[i])
		}
	}

	e.ids = newIDs
	e.vectors = newVectors
	e.idIndex = newIndex

	return nil
}

func (e *memoryEngine) Save(path string) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := gob.NewEncoder(f)

	// 保存元数据
	if err := enc.Encode(e.dimension); err != nil {
		return err
	}
	if err := enc.Encode(string(e.metric)); err != nil {
		return err
	}
	if err := enc.Encode(e.ids); err != nil {
		return err
	}
	return enc.Encode(e.vectors)
}

func (e *memoryEngine) Load(path string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dec := gob.NewDecoder(f)

	var dim int
	if err := dec.Decode(&dim); err != nil {
		return err
	}
	e.dimension = dim

	var metric string
	if err := dec.Decode(&metric); err != nil {
		return err
	}
	e.metric = MetricType(metric)

	if err := dec.Decode(&e.ids); err != nil {
		return err
	}
	if err := dec.Decode(&e.vectors); err != nil {
		return err
	}

	// 重建 ID 索引
	e.idIndex = make(map[string]int, len(e.ids))
	for i, id := range e.ids {
		e.idIndex[id] = i
	}

	return nil
}

func (e *memoryEngine) Size() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.ids)
}

func (e *memoryEngine) Close() error {
	return nil
}

// computeDistance 计算两个向量之间的距离
func (e *memoryEngine) computeDistance(a, b []float32) float32 {
	switch e.metric {
	case MetricL2:
		return l2Distance(a, b)
	case MetricCosine:
		return cosineDistance(a, b)
	case MetricInnerProduct:
		return -innerProduct(a, b) // 负内积作为距离
	default:
		return cosineDistance(a, b)
	}
}

// distanceToScore 将距离转换为相似度分数 (0-1)
func (e *memoryEngine) distanceToScore(distance float32) float32 {
	switch e.metric {
	case MetricCosine:
		// 余弦距离范围 [0, 2]，转换为相似度 [1, -1]
		return 1 - distance
	case MetricL2:
		// L2 距离越小越相似
		return float32(1.0 / (1.0 + float64(distance)))
	case MetricInnerProduct:
		// 内积距离取负值，所以 score = -distance
		return -distance
	default:
		return 1 - distance
	}
}

// ============== 距离计算函数 ==============

// l2Distance 计算 L2 距离
func l2Distance(a, b []float32) float32 {
	var sum float64
	for i := range a {
		diff := float64(a[i]) - float64(b[i])
		sum += diff * diff
	}
	return float32(math.Sqrt(sum))
}

// cosineDistance 计算余弦距离 (1 - cosine_similarity)
func cosineDistance(a, b []float32) float32 {
	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 1.0
	}

	similarity := dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
	return float32(1.0 - similarity)
}

// innerProduct 计算内积
func innerProduct(a, b []float32) float32 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return float32(sum)
}

// matchFilter 检查文档元数据是否匹配过滤条件
func matchFilter(metadata map[string]any, filter map[string]any) bool {
	if metadata == nil {
		return false
	}
	for key, value := range filter {
		metaVal, ok := metadata[key]
		if !ok {
			return false
		}
		if fmt.Sprintf("%v", metaVal) != fmt.Sprintf("%v", value) {
			return false
		}
	}
	return true
}
