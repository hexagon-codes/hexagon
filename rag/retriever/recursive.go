package retriever

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/rag"
)

// NodeType 索引节点类型
type NodeType string

const (
	// NodeTypeChunk 文本块节点（叶子节点）
	NodeTypeChunk NodeType = "chunk"

	// NodeTypeIndex 索引节点（中间节点，指向其他节点）
	NodeTypeIndex NodeType = "index"

	// NodeTypeSummary 摘要节点（包含子节点内容的摘要）
	NodeTypeSummary NodeType = "summary"

	// NodeTypeTable 表格节点（结构化数据）
	NodeTypeTable NodeType = "table"

	// NodeTypeImage 图片节点
	NodeTypeImage NodeType = "image"
)

// IndexNode 索引节点
// 用于构建层级文档结构，支持多跳检索
type IndexNode struct {
	// ID 节点 ID
	ID string `json:"id"`

	// Type 节点类型
	Type NodeType `json:"type"`

	// Content 节点内容
	Content string `json:"content"`

	// Children 子节点 ID 列表
	Children []string `json:"children,omitempty"`

	// Parent 父节点 ID
	Parent string `json:"parent,omitempty"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// Embedding 节点向量（如果有）
	Embedding []float32 `json:"embedding,omitempty"`

	// Score 检索分数（仅在检索结果中有效）
	Score float32 `json:"score,omitempty"`
}

// IsLeaf 判断是否为叶子节点
func (n *IndexNode) IsLeaf() bool {
	return len(n.Children) == 0
}

// NodeIndex 节点索引存储
type NodeIndex struct {
	nodes map[string]*IndexNode
	mu    sync.RWMutex
}

// NewNodeIndex 创建节点索引
func NewNodeIndex() *NodeIndex {
	return &NodeIndex{
		nodes: make(map[string]*IndexNode),
	}
}

// Add 添加节点
func (idx *NodeIndex) Add(node *IndexNode) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.nodes[node.ID] = node
}

// Get 获取节点
func (idx *NodeIndex) Get(id string) (*IndexNode, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	node, ok := idx.nodes[id]
	return node, ok
}

// GetChildren 获取子节点
func (idx *NodeIndex) GetChildren(id string) []*IndexNode {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	parent, ok := idx.nodes[id]
	if !ok {
		return nil
	}

	children := make([]*IndexNode, 0, len(parent.Children))
	for _, childID := range parent.Children {
		if child, ok := idx.nodes[childID]; ok {
			children = append(children, child)
		}
	}
	return children
}

// Delete 删除节点
func (idx *NodeIndex) Delete(id string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.nodes, id)
}

// Clear 清空索引
func (idx *NodeIndex) Clear() {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.nodes = make(map[string]*IndexNode)
}

// Count 返回节点数量
func (idx *NodeIndex) Count() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.nodes)
}

// AllNodes 返回所有节点
func (idx *NodeIndex) AllNodes() []*IndexNode {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	nodes := make([]*IndexNode, 0, len(idx.nodes))
	for _, node := range idx.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

// GetLeafNodes 获取所有叶子节点
func (idx *NodeIndex) GetLeafNodes() []*IndexNode {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var leaves []*IndexNode
	for _, node := range idx.nodes {
		if node.IsLeaf() {
			leaves = append(leaves, node)
		}
	}
	return leaves
}

// RecursiveRetriever 递归/多跳检索器
// 支持层级文档结构的递归检索
//
// 工作原理：
//  1. 从根索引节点开始搜索
//  2. 根据查询选择相关的子节点
//  3. 递归深入直到到达叶子节点（实际内容）
//  4. 可选使用 LLM 判断是否需要继续深入
//
// 适用场景：
//   - 层级文档（如书籍章节、目录结构）
//   - 多步推理检索
//   - 复杂问题分解
//
// 使用示例：
//
//	retriever := NewRecursiveRetriever(
//	    vectorStore, embedder, nodeIndex,
//	    WithMaxDepth(3),
//	    WithRecursiveTopK(5),
//	)
//	docs, err := retriever.Retrieve(ctx, "query")
type RecursiveRetriever struct {
	// store 向量存储（用于语义搜索）
	store vector.Store

	// embedder 向量嵌入器
	embedder vector.Embedder

	// nodeIndex 节点索引
	nodeIndex *NodeIndex

	// maxDepth 最大检索深度
	maxDepth int

	// topK 每层返回的节点数量
	topK int

	// minScore 最小相关性分数
	minScore float32

	// expandAll 是否展开所有子节点（而不是选择性展开）
	expandAll bool

	// includeIntermediateNodes 是否包含中间节点在结果中
	includeIntermediateNodes bool

	// mu 保护并发访问
	mu sync.RWMutex
}

// RecursiveOption RecursiveRetriever 配置选项
type RecursiveOption func(*RecursiveRetriever)

// WithMaxDepth 设置最大检索深度
// 默认值: 3
func WithMaxDepth(depth int) RecursiveOption {
	return func(r *RecursiveRetriever) {
		if depth > 0 {
			r.maxDepth = depth
		}
	}
}

// WithRecursiveTopK 设置每层返回的节点数量
// 默认值: 5
func WithRecursiveTopK(k int) RecursiveOption {
	return func(r *RecursiveRetriever) {
		if k > 0 {
			r.topK = k
		}
	}
}

// WithRecursiveMinScore 设置最小相关性分数
func WithRecursiveMinScore(score float32) RecursiveOption {
	return func(r *RecursiveRetriever) {
		r.minScore = score
	}
}

// WithExpandAll 设置是否展开所有子节点
// 默认: false（选择性展开）
func WithExpandAll(expand bool) RecursiveOption {
	return func(r *RecursiveRetriever) {
		r.expandAll = expand
	}
}

// WithIncludeIntermediateNodes 设置是否包含中间节点在结果中
// 默认: false（只返回叶子节点）
func WithIncludeIntermediateNodes(include bool) RecursiveOption {
	return func(r *RecursiveRetriever) {
		r.includeIntermediateNodes = include
	}
}

// NewRecursiveRetriever 创建递归检索器
//
// 参数：
//   - store: 向量存储
//   - embedder: 向量嵌入器
//   - nodeIndex: 节点索引
//   - opts: 配置选项
func NewRecursiveRetriever(store vector.Store, embedder vector.Embedder, nodeIndex *NodeIndex, opts ...RecursiveOption) *RecursiveRetriever {
	r := &RecursiveRetriever{
		store:                    store,
		embedder:                 embedder,
		nodeIndex:                nodeIndex,
		maxDepth:                 3,
		topK:                     5,
		minScore:                 0.0,
		expandAll:                false,
		includeIntermediateNodes: false,
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Retrieve 递归检索相关文档
func (r *RecursiveRetriever) Retrieve(ctx context.Context, query string, opts ...rag.RetrieveOption) ([]rag.Document, error) {
	cfg := &rag.RetrieveConfig{
		TopK:     r.topK,
		MinScore: r.minScore,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	// 向量化查询
	queryEmbedding, err := r.embedder.EmbedOne(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("向量化查询失败: %w", err)
	}

	// 收集结果
	var results []*IndexNode
	visited := make(map[string]bool)

	// 从向量存储中检索初始节点
	searchOpts := []vector.SearchOption{
		vector.WithMinScore(cfg.MinScore),
		vector.WithMetadata(true),
	}

	initialDocs, err := r.store.Search(ctx, queryEmbedding, r.topK*2, searchOpts...)
	if err != nil {
		return nil, fmt.Errorf("初始检索失败: %w", err)
	}

	// 转换为节点并开始递归
	for _, doc := range initialDocs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		node, ok := r.nodeIndex.Get(doc.ID)
		if !ok {
			// 如果节点索引中没有，创建一个叶子节点
			node = &IndexNode{
				ID:        doc.ID,
				Type:      NodeTypeChunk,
				Content:   doc.Content,
				Metadata:  doc.Metadata,
				Embedding: doc.Embedding,
				Score:     doc.Score,
			}
		} else {
			node.Score = doc.Score
		}

		// 递归检索
		r.recursiveRetrieve(ctx, queryEmbedding, node, 0, visited, &results)
	}

	// 按分数排序
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// 转换为 Document 并返回 TopK
	k := cfg.TopK
	if k > len(results) {
		k = len(results)
	}

	docs := make([]rag.Document, 0, k)
	for i := 0; i < k; i++ {
		node := results[i]
		doc := rag.Document{
			ID:        node.ID,
			Content:   node.Content,
			Metadata:  node.Metadata,
			Embedding: node.Embedding,
			Score:     node.Score,
		}
		if doc.Metadata == nil {
			doc.Metadata = make(map[string]any)
		}
		doc.Metadata["node_type"] = string(node.Type)
		doc.Metadata["retrieval_type"] = "recursive"
		docs = append(docs, doc)
	}

	return docs, nil
}

// recursiveRetrieve 递归检索
func (r *RecursiveRetriever) recursiveRetrieve(
	ctx context.Context,
	queryEmbedding []float32,
	node *IndexNode,
	depth int,
	visited map[string]bool,
	results *[]*IndexNode,
) {
	if ctx.Err() != nil {
		return
	}

	// 检查是否已访问
	if visited[node.ID] {
		return
	}
	visited[node.ID] = true

	// 检查深度限制
	if depth > r.maxDepth {
		return
	}

	// 如果是叶子节点，添加到结果
	if node.IsLeaf() {
		*results = append(*results, node)
		return
	}

	// 如果需要包含中间节点
	if r.includeIntermediateNodes {
		*results = append(*results, node)
	}

	// 获取子节点
	children := r.nodeIndex.GetChildren(node.ID)
	if len(children) == 0 {
		// 没有子节点，当作叶子节点处理
		*results = append(*results, node)
		return
	}

	// 选择要展开的子节点
	var nodesToExpand []*IndexNode
	if r.expandAll {
		nodesToExpand = children
	} else {
		// 选择性展开：计算子节点与查询的相关性
		nodesToExpand = r.selectRelevantChildren(queryEmbedding, children)
	}

	// 递归处理选中的子节点
	for _, child := range nodesToExpand {
		r.recursiveRetrieve(ctx, queryEmbedding, child, depth+1, visited, results)
	}
}

// selectRelevantChildren 选择相关的子节点
func (r *RecursiveRetriever) selectRelevantChildren(queryEmbedding []float32, children []*IndexNode) []*IndexNode {
	// 计算每个子节点的相关性分数
	type scoredNode struct {
		node  *IndexNode
		score float32
	}

	var scored []scoredNode
	for _, child := range children {
		var score float32
		if len(child.Embedding) > 0 && len(queryEmbedding) > 0 {
			score = cosineSimilarity32(queryEmbedding, child.Embedding)
		} else {
			// 没有向量，使用原有分数或默认值
			score = child.Score
		}

		if score >= r.minScore {
			scored = append(scored, scoredNode{node: child, score: score})
		}
	}

	// 按分数排序
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// 返回 TopK
	k := r.topK
	if k > len(scored) {
		k = len(scored)
	}

	result := make([]*IndexNode, k)
	for i := 0; i < k; i++ {
		scored[i].node.Score = scored[i].score
		result[i] = scored[i].node
	}

	return result
}

// IndexNodes 索引节点到向量存储
func (r *RecursiveRetriever) IndexNodes(ctx context.Context, nodes []*IndexNode) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 分离需要向量化的节点
	var toEmbed []*IndexNode
	for _, node := range nodes {
		// 添加到节点索引
		r.nodeIndex.Add(node)

		// 收集需要向量化的节点
		if len(node.Embedding) == 0 && node.Content != "" {
			toEmbed = append(toEmbed, node)
		}
	}

	// 批量向量化
	if len(toEmbed) > 0 {
		texts := make([]string, len(toEmbed))
		for i, node := range toEmbed {
			texts[i] = node.Content
		}

		embeddings, err := r.embedder.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("向量化节点失败: %w", err)
		}

		for i, node := range toEmbed {
			if i < len(embeddings) {
				node.Embedding = embeddings[i]
			}
		}
	}

	// 转换为 vector.Document 并存入向量存储
	vectorDocs := make([]vector.Document, len(nodes))
	for i, node := range nodes {
		metadata := node.Metadata
		if metadata == nil {
			metadata = make(map[string]any)
		}
		metadata["node_type"] = string(node.Type)
		metadata["parent_node"] = node.Parent
		metadata["child_count"] = len(node.Children)

		vectorDocs[i] = vector.Document{
			ID:        node.ID,
			Content:   node.Content,
			Metadata:  metadata,
			Embedding: node.Embedding,
		}
	}

	return r.store.Add(ctx, vectorDocs)
}

// GetNodeIndex 获取节点索引
func (r *RecursiveRetriever) GetNodeIndex() *NodeIndex {
	return r.nodeIndex
}

// Clear 清空所有数据
func (r *RecursiveRetriever) Clear(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nodeIndex.Clear()
	return r.store.Clear(ctx)
}

// cosineSimilarity32 计算余弦相似度（float32 版本）
func cosineSimilarity32(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	// 使用 float64 避免精度损失
	return float32(float64(dot) / (sqrt32(normA) * sqrt32(normB)))
}

// sqrt32 计算平方根（简单实现）
func sqrt32(x float32) float64 {
	if x <= 0 {
		return 0
	}
	// 使用牛顿迭代法
	z := float64(x)
	for i := 0; i < 10; i++ {
		z = (z + float64(x)/z) / 2
	}
	return z
}

// 确保实现了 Retriever 接口
var _ rag.Retriever = (*RecursiveRetriever)(nil)
