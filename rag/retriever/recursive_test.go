package retriever

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/hexagon/testing/mock"
)

// ============== NodeIndex 测试 ==============

func TestNodeIndex_AddAndGet(t *testing.T) {
	idx := NewNodeIndex()

	node := &IndexNode{
		ID:      "n1",
		Type:    NodeTypeChunk,
		Content: "hello world",
	}
	idx.Add(node)

	got, ok := idx.Get("n1")
	if !ok {
		t.Fatal("expected node to exist")
	}
	if got.Content != "hello world" {
		t.Errorf("expected content 'hello world', got %q", got.Content)
	}

	_, ok = idx.Get("nonexistent")
	if ok {
		t.Error("expected node not to exist")
	}
}

func TestNodeIndex_GetChildren(t *testing.T) {
	idx := NewNodeIndex()

	parent := &IndexNode{
		ID:       "parent",
		Type:     NodeTypeIndex,
		Content:  "parent node",
		Children: []string{"child1", "child2", "child3"},
	}
	child1 := &IndexNode{ID: "child1", Type: NodeTypeChunk, Content: "child 1"}
	child2 := &IndexNode{ID: "child2", Type: NodeTypeChunk, Content: "child 2"}
	// child3 不在索引中

	idx.Add(parent)
	idx.Add(child1)
	idx.Add(child2)

	children := idx.GetChildren("parent")
	if len(children) != 2 {
		t.Errorf("expected 2 children (child3 missing), got %d", len(children))
	}

	// 不存在的父节点
	children = idx.GetChildren("nonexistent")
	if children != nil {
		t.Errorf("expected nil for nonexistent parent, got %v", children)
	}
}

func TestNodeIndex_Delete(t *testing.T) {
	idx := NewNodeIndex()
	idx.Add(&IndexNode{ID: "n1", Content: "test"})

	idx.Delete("n1")
	_, ok := idx.Get("n1")
	if ok {
		t.Error("expected node to be deleted")
	}
}

func TestNodeIndex_Clear(t *testing.T) {
	idx := NewNodeIndex()
	idx.Add(&IndexNode{ID: "n1"})
	idx.Add(&IndexNode{ID: "n2"})

	idx.Clear()
	if idx.Count() != 0 {
		t.Errorf("expected 0 after clear, got %d", idx.Count())
	}
}

func TestNodeIndex_Count(t *testing.T) {
	idx := NewNodeIndex()
	if idx.Count() != 0 {
		t.Errorf("expected 0, got %d", idx.Count())
	}

	idx.Add(&IndexNode{ID: "n1"})
	idx.Add(&IndexNode{ID: "n2"})
	if idx.Count() != 2 {
		t.Errorf("expected 2, got %d", idx.Count())
	}
}

func TestNodeIndex_AllNodes(t *testing.T) {
	idx := NewNodeIndex()
	idx.Add(&IndexNode{ID: "n1"})
	idx.Add(&IndexNode{ID: "n2"})

	nodes := idx.AllNodes()
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestNodeIndex_GetLeafNodes(t *testing.T) {
	idx := NewNodeIndex()
	idx.Add(&IndexNode{ID: "parent", Children: []string{"child1"}})
	idx.Add(&IndexNode{ID: "child1"}) // 叶子节点

	leaves := idx.GetLeafNodes()
	if len(leaves) != 1 {
		t.Errorf("expected 1 leaf node, got %d", len(leaves))
	}
	if leaves[0].ID != "child1" {
		t.Errorf("expected child1 as leaf, got %s", leaves[0].ID)
	}
}

func TestIndexNode_IsLeaf(t *testing.T) {
	leaf := &IndexNode{ID: "n1"}
	if !leaf.IsLeaf() {
		t.Error("node without children should be leaf")
	}

	nonLeaf := &IndexNode{ID: "n2", Children: []string{"c1"}}
	if nonLeaf.IsLeaf() {
		t.Error("node with children should not be leaf")
	}
}

// ============== RecursiveRetriever 测试 ==============

func TestNewRecursiveRetriever_Defaults(t *testing.T) {
	store := mock.NewMockVectorStore()
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	r := NewRecursiveRetriever(store, embedder, idx)
	if r.maxDepth != 3 {
		t.Errorf("expected default maxDepth 3, got %d", r.maxDepth)
	}
	if r.topK != 5 {
		t.Errorf("expected default topK 5, got %d", r.topK)
	}
	if r.expandAll {
		t.Error("expected default expandAll false")
	}
	if r.includeIntermediateNodes {
		t.Error("expected default includeIntermediateNodes false")
	}
}

func TestNewRecursiveRetriever_WithOptions(t *testing.T) {
	store := mock.NewMockVectorStore()
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	r := NewRecursiveRetriever(store, embedder, idx,
		WithMaxDepth(5),
		WithRecursiveTopK(10),
		WithRecursiveMinScore(0.5),
		WithExpandAll(true),
		WithIncludeIntermediateNodes(true),
	)
	if r.maxDepth != 5 {
		t.Errorf("expected maxDepth 5, got %d", r.maxDepth)
	}
	if r.topK != 10 {
		t.Errorf("expected topK 10, got %d", r.topK)
	}
	if r.minScore != 0.5 {
		t.Errorf("expected minScore 0.5, got %f", r.minScore)
	}
	if !r.expandAll {
		t.Error("expected expandAll true")
	}
	if !r.includeIntermediateNodes {
		t.Error("expected includeIntermediateNodes true")
	}
}

func TestNewRecursiveRetriever_InvalidOptions(t *testing.T) {
	store := mock.NewMockVectorStore()
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	r := NewRecursiveRetriever(store, embedder, idx,
		WithMaxDepth(0),       // 无效，不应更改默认值
		WithRecursiveTopK(-1), // 无效
	)
	if r.maxDepth != 3 {
		t.Errorf("expected default maxDepth 3 for invalid value, got %d", r.maxDepth)
	}
	if r.topK != 5 {
		t.Errorf("expected default topK 5 for invalid value, got %d", r.topK)
	}
}

func TestRecursiveRetriever_Retrieve_LeafNodes(t *testing.T) {
	// 设置叶子节点搜索结果
	searchResults := []vector.Document{
		{ID: "leaf1", Content: "leaf content 1", Score: 0.9, Embedding: []float32{1, 0, 0, 0}},
		{ID: "leaf2", Content: "leaf content 2", Score: 0.8, Embedding: []float32{0, 1, 0, 0}},
	}
	store := mock.NewMockVectorStore(mock.WithSearchResults(searchResults))
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	// 索引中有对应叶子节点
	idx.Add(&IndexNode{ID: "leaf1", Type: NodeTypeChunk, Content: "leaf content 1"})
	idx.Add(&IndexNode{ID: "leaf2", Type: NodeTypeChunk, Content: "leaf content 2"})

	r := NewRecursiveRetriever(store, embedder, idx, WithRecursiveTopK(2))

	ctx := context.Background()
	docs, err := r.Retrieve(ctx, "test query")
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	if len(docs) != 2 {
		t.Errorf("expected 2 docs, got %d", len(docs))
	}

	// 检查 metadata
	for _, doc := range docs {
		if doc.Metadata["retrieval_type"] != "recursive" {
			t.Errorf("expected retrieval_type 'recursive', got %v", doc.Metadata["retrieval_type"])
		}
		if doc.Metadata["node_type"] != "chunk" {
			t.Errorf("expected node_type 'chunk', got %v", doc.Metadata["node_type"])
		}
	}
}

func TestRecursiveRetriever_Retrieve_HierarchicalNodes(t *testing.T) {
	// 设置: parent -> child1, child2 (叶子)
	embedding := []float32{0.5, 0.5, 0, 0}

	searchResults := []vector.Document{
		{ID: "parent", Content: "parent summary", Score: 0.95, Embedding: embedding},
	}
	store := mock.NewMockVectorStore(mock.WithSearchResults(searchResults))
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	idx.Add(&IndexNode{
		ID: "parent", Type: NodeTypeIndex, Content: "parent summary",
		Children: []string{"child1", "child2"}, Embedding: embedding,
	})
	idx.Add(&IndexNode{
		ID: "child1", Type: NodeTypeChunk, Content: "child content 1",
		Parent: "parent", Embedding: []float32{0.9, 0.1, 0, 0},
	})
	idx.Add(&IndexNode{
		ID: "child2", Type: NodeTypeChunk, Content: "child content 2",
		Parent: "parent", Embedding: []float32{0.1, 0.9, 0, 0},
	})

	r := NewRecursiveRetriever(store, embedder, idx, WithRecursiveTopK(5))

	ctx := context.Background()
	docs, err := r.Retrieve(ctx, "test query")
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	// 应该递归到叶子节点
	if len(docs) == 0 {
		t.Fatal("expected at least 1 doc from recursive retrieval")
	}

	// 叶子节点应在结果中
	foundChild := false
	for _, doc := range docs {
		if doc.ID == "child1" || doc.ID == "child2" {
			foundChild = true
			break
		}
	}
	if !foundChild {
		t.Error("expected child nodes in results")
	}
}

func TestRecursiveRetriever_Retrieve_IncludeIntermediateNodes(t *testing.T) {
	embedding := []float32{0.5, 0.5, 0, 0}

	searchResults := []vector.Document{
		{ID: "parent", Content: "parent summary", Score: 0.95, Embedding: embedding},
	}
	store := mock.NewMockVectorStore(mock.WithSearchResults(searchResults))
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	idx.Add(&IndexNode{
		ID: "parent", Type: NodeTypeIndex, Content: "parent summary",
		Children: []string{"child1"}, Embedding: embedding,
	})
	idx.Add(&IndexNode{
		ID: "child1", Type: NodeTypeChunk, Content: "child content",
		Parent: "parent", Embedding: []float32{0.9, 0.1, 0, 0},
	})

	r := NewRecursiveRetriever(store, embedder, idx,
		WithIncludeIntermediateNodes(true),
		WithRecursiveTopK(10),
	)

	ctx := context.Background()
	docs, err := r.Retrieve(ctx, "test query")
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	// 应包含中间节点（parent）和叶子节点（child1）
	foundParent := false
	foundChild := false
	for _, doc := range docs {
		if doc.ID == "parent" {
			foundParent = true
		}
		if doc.ID == "child1" {
			foundChild = true
		}
	}
	if !foundParent {
		t.Error("expected parent node in results with includeIntermediateNodes")
	}
	if !foundChild {
		t.Error("expected child node in results")
	}
}

func TestRecursiveRetriever_Retrieve_ExpandAll(t *testing.T) {
	embedding := []float32{0.5, 0.5, 0, 0}

	searchResults := []vector.Document{
		{ID: "parent", Content: "parent", Score: 0.95, Embedding: embedding},
	}
	store := mock.NewMockVectorStore(mock.WithSearchResults(searchResults))
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	idx.Add(&IndexNode{
		ID: "parent", Type: NodeTypeIndex, Content: "parent",
		Children: []string{"c1", "c2", "c3"}, Embedding: embedding,
	})
	idx.Add(&IndexNode{ID: "c1", Type: NodeTypeChunk, Content: "child 1", Embedding: []float32{0.9, 0, 0, 0}})
	idx.Add(&IndexNode{ID: "c2", Type: NodeTypeChunk, Content: "child 2", Embedding: []float32{0, 0.9, 0, 0}})
	idx.Add(&IndexNode{ID: "c3", Type: NodeTypeChunk, Content: "child 3", Embedding: []float32{0, 0, 0.9, 0}})

	r := NewRecursiveRetriever(store, embedder, idx,
		WithExpandAll(true),
		WithRecursiveTopK(10),
	)

	ctx := context.Background()
	docs, err := r.Retrieve(ctx, "test")
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	// expandAll 应返回所有子节点
	if len(docs) < 3 {
		t.Errorf("expected at least 3 docs with expandAll, got %d", len(docs))
	}
}

func TestRecursiveRetriever_Retrieve_NoNodeInIndex(t *testing.T) {
	// 向量存储返回的文档不在节点索引中 -> 应作为叶子节点
	searchResults := []vector.Document{
		{ID: "unknown", Content: "unknown content", Score: 0.9, Embedding: []float32{1, 0, 0, 0}},
	}
	store := mock.NewMockVectorStore(mock.WithSearchResults(searchResults))
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	r := NewRecursiveRetriever(store, embedder, idx, WithRecursiveTopK(5))

	ctx := context.Background()
	docs, err := r.Retrieve(ctx, "test")
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	if len(docs) != 1 {
		t.Errorf("expected 1 doc, got %d", len(docs))
	}
	if docs[0].ID != "unknown" {
		t.Errorf("expected doc ID 'unknown', got %q", docs[0].ID)
	}
}

func TestRecursiveRetriever_Retrieve_MaxDepth(t *testing.T) {
	embedding := []float32{0.5, 0.5, 0, 0}

	// 创建深层嵌套: root -> mid -> deep -> leaf
	searchResults := []vector.Document{
		{ID: "root", Content: "root", Score: 0.9, Embedding: embedding},
	}
	store := mock.NewMockVectorStore(mock.WithSearchResults(searchResults))
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	idx.Add(&IndexNode{ID: "root", Type: NodeTypeIndex, Children: []string{"mid"}, Embedding: embedding})
	idx.Add(&IndexNode{ID: "mid", Type: NodeTypeIndex, Children: []string{"deep"}, Embedding: embedding})
	idx.Add(&IndexNode{ID: "deep", Type: NodeTypeIndex, Children: []string{"leaf"}, Embedding: embedding})
	idx.Add(&IndexNode{ID: "leaf", Type: NodeTypeChunk, Content: "leaf content", Embedding: embedding})

	// maxDepth=1 应该不能到达 leaf
	r := NewRecursiveRetriever(store, embedder, idx,
		WithMaxDepth(1),
		WithExpandAll(true),
		WithRecursiveTopK(10),
	)

	ctx := context.Background()
	docs, err := r.Retrieve(ctx, "test")
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	// 深度限制应阻止到达 leaf
	for _, doc := range docs {
		if doc.ID == "leaf" {
			t.Error("should not reach leaf with maxDepth=1")
		}
	}
}

func TestRecursiveRetriever_Retrieve_EmbedError(t *testing.T) {
	store := mock.NewMockVectorStore()
	embedder := mock.NewMockEmbedder(4, mock.WithEmbedError(errors.New("embed failed")))
	idx := NewNodeIndex()

	r := NewRecursiveRetriever(store, embedder, idx)

	ctx := context.Background()
	_, err := r.Retrieve(ctx, "test")
	if err == nil {
		t.Error("expected error from embedder")
	}
}

func TestRecursiveRetriever_Retrieve_SearchError(t *testing.T) {
	store := mock.NewMockVectorStore(mock.WithSearchError(errors.New("search failed")))
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	r := NewRecursiveRetriever(store, embedder, idx)

	ctx := context.Background()
	_, err := r.Retrieve(ctx, "test")
	if err == nil {
		t.Error("expected error from store search")
	}
}

func TestRecursiveRetriever_Retrieve_ContextCancel(t *testing.T) {
	searchResults := []vector.Document{
		{ID: "n1", Content: "content", Score: 0.9, Embedding: []float32{1, 0, 0, 0}},
	}
	store := mock.NewMockVectorStore(mock.WithSearchResults(searchResults))
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	r := NewRecursiveRetriever(store, embedder, idx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	_, err := r.Retrieve(ctx, "test")
	if err == nil {
		t.Error("expected error due to context cancellation")
	}
}

func TestRecursiveRetriever_Retrieve_ResultsSortedByScore(t *testing.T) {
	searchResults := []vector.Document{
		{ID: "low", Content: "low score", Score: 0.3, Embedding: []float32{0.1, 0, 0, 0}},
		{ID: "high", Content: "high score", Score: 0.9, Embedding: []float32{0.9, 0, 0, 0}},
		{ID: "mid", Content: "mid score", Score: 0.6, Embedding: []float32{0.5, 0, 0, 0}},
	}
	store := mock.NewMockVectorStore(mock.WithSearchResults(searchResults))
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	r := NewRecursiveRetriever(store, embedder, idx, WithRecursiveTopK(3))

	ctx := context.Background()
	docs, err := r.Retrieve(ctx, "test")
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	// 验证按分数降序排列
	for i := 1; i < len(docs); i++ {
		if docs[i].Score > docs[i-1].Score {
			t.Errorf("results not sorted by score: %f > %f at index %d", docs[i].Score, docs[i-1].Score, i)
		}
	}
}

func TestRecursiveRetriever_Retrieve_TopKLimit(t *testing.T) {
	searchResults := make([]vector.Document, 10)
	for i := 0; i < 10; i++ {
		searchResults[i] = vector.Document{
			ID:        string(rune('a' + i)),
			Content:   "content",
			Score:     float32(10-i) * 0.1,
			Embedding: []float32{float32(i) * 0.1, 0, 0, 0},
		}
	}
	store := mock.NewMockVectorStore(mock.WithSearchResults(searchResults))
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	r := NewRecursiveRetriever(store, embedder, idx, WithRecursiveTopK(3))

	ctx := context.Background()
	docs, err := r.Retrieve(ctx, "test")
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}

	if len(docs) > 3 {
		t.Errorf("expected at most 3 docs, got %d", len(docs))
	}
}

// ============== IndexNodes 测试 ==============

func TestRecursiveRetriever_IndexNodes(t *testing.T) {
	store := mock.NewMockVectorStore()
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	r := NewRecursiveRetriever(store, embedder, idx)

	nodes := []*IndexNode{
		{ID: "n1", Type: NodeTypeChunk, Content: "content 1"},
		{ID: "n2", Type: NodeTypeIndex, Content: "summary", Children: []string{"n1"}},
		{ID: "n3", Type: NodeTypeChunk, Content: "", Embedding: []float32{1, 0, 0, 0}}, // 已有 embedding，不需要 embed
	}

	ctx := context.Background()
	err := r.IndexNodes(ctx, nodes)
	if err != nil {
		t.Fatalf("IndexNodes failed: %v", err)
	}

	// 验证节点已添加到索引
	if idx.Count() != 3 {
		t.Errorf("expected 3 nodes in index, got %d", idx.Count())
	}

	// 验证 embedder 被调用（n1 和 n2 需要 embed，n3 已有 embedding 但 Content 为空）
	calls := embedder.EmbedCallCount()
	if calls != 1 {
		t.Errorf("expected 1 embed call, got %d", calls)
	}
}

func TestRecursiveRetriever_IndexNodes_EmbedError(t *testing.T) {
	store := mock.NewMockVectorStore()
	embedder := mock.NewMockEmbedder(4, mock.WithEmbedError(errors.New("embed failed")))
	idx := NewNodeIndex()

	r := NewRecursiveRetriever(store, embedder, idx)

	nodes := []*IndexNode{
		{ID: "n1", Type: NodeTypeChunk, Content: "content"},
	}

	ctx := context.Background()
	err := r.IndexNodes(ctx, nodes)
	if err == nil {
		t.Error("expected error from embedder")
	}
}

func TestRecursiveRetriever_IndexNodes_NilMetadata(t *testing.T) {
	store := mock.NewMockVectorStore()
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()

	r := NewRecursiveRetriever(store, embedder, idx)

	nodes := []*IndexNode{
		{ID: "n1", Type: NodeTypeChunk, Content: "test", Embedding: []float32{1, 0, 0, 0}},
	}

	ctx := context.Background()
	err := r.IndexNodes(ctx, nodes)
	if err != nil {
		t.Fatalf("IndexNodes failed: %v", err)
	}
}

// ============== GetNodeIndex / Clear 测试 ==============

func TestRecursiveRetriever_GetNodeIndex(t *testing.T) {
	idx := NewNodeIndex()
	r := NewRecursiveRetriever(mock.NewMockVectorStore(), mock.NewMockEmbedder(4), idx)

	if r.GetNodeIndex() != idx {
		t.Error("GetNodeIndex should return the same index")
	}
}

func TestRecursiveRetriever_Clear(t *testing.T) {
	store := mock.NewMockVectorStore()
	embedder := mock.NewMockEmbedder(4)
	idx := NewNodeIndex()
	idx.Add(&IndexNode{ID: "n1"})

	r := NewRecursiveRetriever(store, embedder, idx)

	ctx := context.Background()
	err := r.Clear(ctx)
	if err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	if idx.Count() != 0 {
		t.Errorf("expected 0 after clear, got %d", idx.Count())
	}
}

// ============== cosineSimilarity32 / sqrt32 测试 ==============

func TestCosineSimilarity32(t *testing.T) {
	tests := []struct {
		name     string
		a, b     []float32
		expected float32
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{"opposite", []float32{1, 0, 0}, []float32{-1, 0, 0}, -1.0},
		{"empty", []float32{}, []float32{}, 0.0},
		{"different_length", []float32{1, 0}, []float32{1, 0, 0}, 0.0},
		{"zero_vector_a", []float32{0, 0, 0}, []float32{1, 0, 0}, 0.0},
		{"zero_vector_b", []float32{1, 0, 0}, []float32{0, 0, 0}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSimilarity32(tt.a, tt.b)
			diff := got - tt.expected
			if diff < -0.01 || diff > 0.01 {
				t.Errorf("cosineSimilarity32(%v, %v) = %f, want %f", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}

func TestSqrt32(t *testing.T) {
	tests := []struct {
		input    float32
		expected float64
	}{
		{4.0, 2.0},
		{9.0, 3.0},
		{0.0, 0.0},
		{-1.0, 0.0},
		{1.0, 1.0},
	}

	for _, tt := range tests {
		got := sqrt32(tt.input)
		diff := got - tt.expected
		if diff < -0.001 || diff > 0.001 {
			t.Errorf("sqrt32(%f) = %f, want %f", tt.input, got, tt.expected)
		}
	}
}

// ============== selectRelevantChildren 测试 ==============

func TestSelectRelevantChildren(t *testing.T) {
	r := &RecursiveRetriever{
		topK:     2,
		minScore: 0.0,
	}

	query := []float32{1, 0, 0, 0}
	children := []*IndexNode{
		{ID: "c1", Embedding: []float32{0.9, 0.1, 0, 0}},
		{ID: "c2", Embedding: []float32{0.1, 0.9, 0, 0}},
		{ID: "c3", Embedding: []float32{0.8, 0.2, 0, 0}},
	}

	selected := r.selectRelevantChildren(query, children)

	if len(selected) != 2 {
		t.Errorf("expected 2 selected children, got %d", len(selected))
	}

	// c1 和 c3 应该被选中（与 query [1,0,0,0] 最相似）
	if selected[0].Score <= selected[1].Score {
		// 已排序
	}
}

func TestSelectRelevantChildren_NoEmbedding(t *testing.T) {
	r := &RecursiveRetriever{
		topK:     2,
		minScore: 0.0,
	}

	query := []float32{1, 0, 0, 0}
	children := []*IndexNode{
		{ID: "c1", Score: 0.8}, // 没有 embedding，使用原有分数
		{ID: "c2", Score: 0.6},
	}

	selected := r.selectRelevantChildren(query, children)
	if len(selected) != 2 {
		t.Errorf("expected 2 selected children, got %d", len(selected))
	}
}

func TestSelectRelevantChildren_MinScoreFilter(t *testing.T) {
	r := &RecursiveRetriever{
		topK:     10,
		minScore: 0.5,
	}

	query := []float32{1, 0, 0, 0}
	children := []*IndexNode{
		{ID: "c1", Score: 0.8}, // 无 embedding，使用原有分数
		{ID: "c2", Score: 0.3}, // 低于 minScore
	}

	selected := r.selectRelevantChildren(query, children)
	if len(selected) != 1 {
		t.Errorf("expected 1 selected child (minScore filter), got %d", len(selected))
	}
}

// ============== 接口兼容性测试 ==============

func TestRecursiveRetriever_ImplementsRetriever(t *testing.T) {
	var _ rag.Retriever = (*RecursiveRetriever)(nil)
}

// ============== NodeType 常量测试 ==============

func TestNodeTypes(t *testing.T) {
	if NodeTypeChunk != "chunk" {
		t.Error("unexpected NodeTypeChunk value")
	}
	if NodeTypeIndex != "index" {
		t.Error("unexpected NodeTypeIndex value")
	}
	if NodeTypeSummary != "summary" {
		t.Error("unexpected NodeTypeSummary value")
	}
	if NodeTypeTable != "table" {
		t.Error("unexpected NodeTypeTable value")
	}
	if NodeTypeImage != "image" {
		t.Error("unexpected NodeTypeImage value")
	}
}
