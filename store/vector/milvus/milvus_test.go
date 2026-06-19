package milvus

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/hexagon-codes/ai-core/store/vector"
)

// ============================================================
// Mock Milvus REST API 服务器
// ============================================================

// mockMilvus 模拟 Milvus REST API 服务器的内存后端
type mockMilvus struct {
	mu          sync.RWMutex
	collections map[string]*mockCollection
}

// mockCollection 模拟集合
type mockCollection struct {
	docs   map[string]map[string]any // id -> fields
	loaded bool
}

// newMockMilvusServer 创建 mock Milvus HTTP 服务器
//
// 返回 httptest.Server 和 cleanup 函数。
// 模拟 Milvus REST API v2 的核心端点：
//   - 集合管理: create, describe, drop, load, release
//   - 实体操作: insert, upsert, search, get, query, delete
//   - 索引操作: create, drop
func newMockMilvusServer() *httptest.Server {
	mock := &mockMilvus{
		collections: make(map[string]*mockCollection),
	}

	mux := http.NewServeMux()

	// 集合操作
	mux.HandleFunc("/v2/vectordb/collections/create", mock.handleCreateCollection)
	mux.HandleFunc("/v2/vectordb/collections/describe", mock.handleDescribeCollection)
	mux.HandleFunc("/v2/vectordb/collections/drop", mock.handleDropCollection)
	mux.HandleFunc("/v2/vectordb/collections/load", mock.handleLoadCollection)
	mux.HandleFunc("/v2/vectordb/collections/release", mock.handleReleaseCollection)

	// 实体操作
	mux.HandleFunc("/v2/vectordb/entities/insert", mock.handleInsert)
	mux.HandleFunc("/v2/vectordb/entities/upsert", mock.handleUpsert)
	mux.HandleFunc("/v2/vectordb/entities/search", mock.handleSearch)
	mux.HandleFunc("/v2/vectordb/entities/get", mock.handleGet)
	mux.HandleFunc("/v2/vectordb/entities/query", mock.handleQuery)
	mux.HandleFunc("/v2/vectordb/entities/delete", mock.handleDelete)

	// 索引操作
	mux.HandleFunc("/v2/vectordb/indexes/create", mock.handleOK)
	mux.HandleFunc("/v2/vectordb/indexes/drop", mock.handleOK)

	return httptest.NewServer(mux)
}

func (m *mockMilvus) writeJSON(w http.ResponseWriter, code int, data any) {
	resp := map[string]any{"code": code}
	if data != nil {
		b, _ := json.Marshal(data)
		resp["data"] = json.RawMessage(b)
	} else {
		resp["data"] = json.RawMessage("{}")
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (m *mockMilvus) writeError(w http.ResponseWriter, code int, msg string) {
	resp := map[string]any{"code": code, "message": msg}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (m *mockMilvus) handleOK(w http.ResponseWriter, r *http.Request) {
	m.writeJSON(w, 0, nil)
}

func (m *mockMilvus) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CollectionName string `json:"collectionName"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.collections[req.CollectionName] = &mockCollection{
		docs:   make(map[string]map[string]any),
		loaded: true,
	}
	m.writeJSON(w, 0, nil)
}

func (m *mockMilvus) handleDescribeCollection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CollectionName string `json:"collectionName"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.collections[req.CollectionName]; !ok {
		m.writeError(w, 100, "collection not found")
		return
	}

	m.writeJSON(w, 0, map[string]any{
		"collectionName": req.CollectionName,
		"load":           "LoadStateLoaded",
	})
}

func (m *mockMilvus) handleDropCollection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CollectionName string `json:"collectionName"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.collections, req.CollectionName)
	m.writeJSON(w, 0, nil)
}

func (m *mockMilvus) handleLoadCollection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CollectionName string `json:"collectionName"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	m.mu.Lock()
	defer m.mu.Unlock()

	if col, ok := m.collections[req.CollectionName]; ok {
		col.loaded = true
	}
	m.writeJSON(w, 0, nil)
}

func (m *mockMilvus) handleReleaseCollection(w http.ResponseWriter, r *http.Request) {
	m.writeJSON(w, 0, nil)
}

func (m *mockMilvus) handleInsert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CollectionName string           `json:"collectionName"`
		Data           []map[string]any `json:"data"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	m.mu.Lock()
	defer m.mu.Unlock()

	col, ok := m.collections[req.CollectionName]
	if !ok {
		m.writeError(w, 100, "collection not found")
		return
	}

	ids := make([]string, len(req.Data))
	for i, item := range req.Data {
		id, _ := item["id"].(string)
		col.docs[id] = item
		ids[i] = id
	}

	m.writeJSON(w, 0, map[string]any{
		"insertCount": len(req.Data),
		"insertIds":   ids,
	})
}

func (m *mockMilvus) handleUpsert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CollectionName string           `json:"collectionName"`
		Data           []map[string]any `json:"data"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	m.mu.Lock()
	defer m.mu.Unlock()

	col, ok := m.collections[req.CollectionName]
	if !ok {
		m.writeError(w, 100, "collection not found")
		return
	}

	for _, item := range req.Data {
		id, _ := item["id"].(string)
		col.docs[id] = item
	}

	m.writeJSON(w, 0, map[string]any{
		"upsertCount": len(req.Data),
	})
}

func (m *mockMilvus) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CollectionName string      `json:"collectionName"`
		Data           [][]float64 `json:"data"` // JSON 默认解析为 float64
		Limit          int         `json:"limit"`
		OutputFields   []string    `json:"outputFields"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	m.mu.RLock()
	defer m.mu.RUnlock()

	col, ok := m.collections[req.CollectionName]
	if !ok {
		m.writeJSON(w, 0, []any{})
		return
	}

	if len(req.Data) == 0 {
		m.writeJSON(w, 0, []any{})
		return
	}

	queryVec := req.Data[0]

	// 简单的余弦相似度搜索
	type scored struct {
		doc   map[string]any
		score float64
	}

	var results []scored
	for _, doc := range col.docs {
		vec := toFloat64Slice(doc["vector"])
		if len(vec) == 0 || len(vec) != len(queryVec) {
			continue
		}
		score := cosine(queryVec, vec)
		results = append(results, scored{doc: doc, score: score})
	}

	// 排序（降序）
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if req.Limit > 0 && len(results) > req.Limit {
		results = results[:req.Limit]
	}

	// 构建返回数据
	outputFields := make(map[string]bool)
	for _, f := range req.OutputFields {
		outputFields[f] = true
	}

	var data []map[string]any
	for _, r := range results {
		item := map[string]any{
			"distance": r.score,
		}
		if outputFields["id"] {
			item["id"] = r.doc["id"]
		}
		if outputFields["content"] {
			item["content"] = r.doc["content"]
		}
		if outputFields["metadata_json"] {
			item["metadata_json"] = r.doc["metadata_json"]
		}
		if outputFields["vector"] {
			item["vector"] = r.doc["vector"]
		}
		data = append(data, item)
	}

	if data == nil {
		data = []map[string]any{}
	}
	m.writeJSON(w, 0, data)
}

func (m *mockMilvus) handleGet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CollectionName string   `json:"collectionName"`
		ID             []string `json:"id"`
		OutputFields   []string `json:"outputFields"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	m.mu.RLock()
	defer m.mu.RUnlock()

	col, ok := m.collections[req.CollectionName]
	if !ok {
		m.writeJSON(w, 0, []any{})
		return
	}

	var data []map[string]any
	for _, id := range req.ID {
		if doc, ok := col.docs[id]; ok {
			item := make(map[string]any)
			for _, f := range req.OutputFields {
				if v, ok := doc[f]; ok {
					item[f] = v
				}
			}
			data = append(data, item)
		}
	}

	if data == nil {
		data = []map[string]any{}
	}
	m.writeJSON(w, 0, data)
}

func (m *mockMilvus) handleQuery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CollectionName string   `json:"collectionName"`
		OutputFields   []string `json:"outputFields"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	m.mu.RLock()
	defer m.mu.RUnlock()

	col, ok := m.collections[req.CollectionName]
	if !ok {
		m.writeJSON(w, 0, []any{})
		return
	}

	var data []map[string]any
	for _, doc := range col.docs {
		item := make(map[string]any)
		for _, f := range req.OutputFields {
			if v, ok := doc[f]; ok {
				item[f] = v
			}
		}
		data = append(data, item)
	}

	if data == nil {
		data = []map[string]any{}
	}
	m.writeJSON(w, 0, data)
}

func (m *mockMilvus) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CollectionName string `json:"collectionName"`
		Filter         string `json:"filter"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	m.mu.Lock()
	defer m.mu.Unlock()

	col, ok := m.collections[req.CollectionName]
	if !ok {
		m.writeJSON(w, 0, nil)
		return
	}

	// 解析 filter 中的 id 列表
	// 格式: id in ["id1","id2"]
	ids := parseFilterIDs(req.Filter)
	for _, id := range ids {
		delete(col.docs, id)
	}

	m.writeJSON(w, 0, nil)
}

// ============================================================
// 辅助函数
// ============================================================

// toFloat64Slice 将 any 类型转换为 float64 切片
func toFloat64Slice(v any) []float64 {
	switch vec := v.(type) {
	case []float64:
		return vec
	case []any:
		result := make([]float64, len(vec))
		for i, item := range vec {
			if f, ok := item.(float64); ok {
				result[i] = f
			}
		}
		return result
	}
	return nil
}

// cosine 计算余弦相似度
func cosine(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// parseFilterIDs 从 filter 表达式中解析 ID 列表
//
// 支持格式: id in ["id1","id2"]
func parseFilterIDs(filter string) []string {
	start := strings.Index(filter, "[")
	end := strings.LastIndex(filter, "]")
	if start < 0 || end < 0 || end <= start {
		return nil
	}

	inner := filter[start+1 : end]
	parts := strings.Split(inner, ",")
	var ids []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "\"")
		if p != "" {
			ids = append(ids, p)
		}
	}
	return ids
}

// newTestStore 创建连接到 mock 服务器的测试用 Store
func newTestStore(t *testing.T, server *httptest.Server, dim int) *Store {
	t.Helper()
	ctx := context.Background()
	store, err := NewStore(ctx,
		WithAddress(server.URL),
		WithDimension(dim),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("NewStore 错误: %v", err)
	}
	return store
}

// ============================================================
// 测试用例
// ============================================================

// TestNewStore 测试创建存储
func TestNewStore(t *testing.T) {
	server := newMockMilvusServer()
	defer server.Close()

	t.Run("默认配置", func(t *testing.T) {
		ctx := context.Background()
		store, err := NewStore(ctx,
			WithAddress(server.URL),
			WithHTTPClient(server.Client()),
		)
		if err != nil {
			t.Fatalf("NewStore 错误: %v", err)
		}
		defer store.Close()

		if store.dimension != 1536 {
			t.Errorf("dimension = %d, want 1536", store.dimension)
		}
		if store.indexType != "HNSW" {
			t.Errorf("indexType = %s, want HNSW", store.indexType)
		}
		if store.metricType != "COSINE" {
			t.Errorf("metricType = %s, want COSINE", store.metricType)
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		ctx := context.Background()
		store, err := NewStore(ctx,
			WithAddress(server.URL),
			WithHTTPClient(server.Client()),
			WithCollection("my_collection"),
			WithDimension(768),
			WithAuth("user", "pass"),
			WithIndexType("IVF_FLAT"),
			WithMetricType("L2"),
		)
		if err != nil {
			t.Fatalf("NewStore 错误: %v", err)
		}
		defer store.Close()

		if store.collection != "my_collection" {
			t.Errorf("collection = %s, want my_collection", store.collection)
		}
		if store.dimension != 768 {
			t.Errorf("dimension = %d, want 768", store.dimension)
		}
		if store.token != "user:pass" {
			t.Errorf("token = %s, want user:pass", store.token)
		}
		if store.indexType != "IVF_FLAT" {
			t.Errorf("indexType = %s, want IVF_FLAT", store.indexType)
		}
		if store.metricType != "L2" {
			t.Errorf("metricType = %s, want L2", store.metricType)
		}
	})
}

// TestStoreAdd 测试添加文档
func TestStoreAdd(t *testing.T) {
	server := newMockMilvusServer()
	defer server.Close()
	store := newTestStore(t, server, 3)
	defer store.Close()

	t.Run("添加单个文档", func(t *testing.T) {
		docs := []vector.Document{
			{
				ID:        "doc1",
				Content:   "test content",
				Embedding: []float32{0.1, 0.2, 0.3},
				Metadata:  map[string]any{"source": "test"},
			},
		}
		if err := store.Add(context.Background(), docs); err != nil {
			t.Errorf("Add 错误: %v", err)
		}
	})

	t.Run("添加多个文档", func(t *testing.T) {
		docs := []vector.Document{
			{ID: "doc2", Content: "content 2", Embedding: []float32{0.4, 0.5, 0.6}},
			{ID: "doc3", Content: "content 3", Embedding: []float32{0.7, 0.8, 0.9}},
		}
		if err := store.Add(context.Background(), docs); err != nil {
			t.Errorf("Add 错误: %v", err)
		}
	})

	t.Run("空文档列表", func(t *testing.T) {
		if err := store.Add(context.Background(), []vector.Document{}); err != nil {
			t.Errorf("空列表不应报错: %v", err)
		}
	})

	t.Run("无 ID 文档", func(t *testing.T) {
		docs := []vector.Document{
			{Content: "auto id content", Embedding: []float32{0.1, 0.2, 0.3}},
		}
		if err := store.Add(context.Background(), docs); err != nil {
			t.Errorf("Add 错误: %v", err)
		}
	})

	t.Run("维度不匹配", func(t *testing.T) {
		docs := []vector.Document{
			{ID: "bad", Content: "wrong dim", Embedding: []float32{0.1, 0.2}},
		}
		if err := store.Add(context.Background(), docs); err == nil {
			t.Error("维度不匹配应该报错")
		}
	})
}

// TestStoreSearch 测试搜索
func TestStoreSearch(t *testing.T) {
	server := newMockMilvusServer()
	defer server.Close()
	store := newTestStore(t, server, 3)
	defer store.Close()

	ctx := context.Background()

	// 添加测试数据
	docs := []vector.Document{
		{ID: "doc1", Content: "content 1", Embedding: []float32{1.0, 0.0, 0.0}},
		{ID: "doc2", Content: "content 2", Embedding: []float32{0.0, 1.0, 0.0}},
		{ID: "doc3", Content: "content 3", Embedding: []float32{0.0, 0.0, 1.0}},
		{ID: "doc4", Content: "content 4", Embedding: []float32{0.5, 0.5, 0.0}},
	}
	if err := store.Add(ctx, docs); err != nil {
		t.Fatalf("添加测试数据错误: %v", err)
	}

	t.Run("基本搜索", func(t *testing.T) {
		results, err := store.Search(ctx, []float32{1.0, 0.0, 0.0}, 2)
		if err != nil {
			t.Fatalf("Search 错误: %v", err)
		}

		if len(results) != 2 {
			t.Fatalf("结果数量 = %d, want 2", len(results))
		}

		// 第一个结果应该是 doc1（完全匹配）
		if results[0].ID != "doc1" {
			t.Errorf("最相似文档 = %s, want doc1", results[0].ID)
		}

		// 验证分数降序
		for i := 1; i < len(results); i++ {
			if results[i].Score > results[i-1].Score {
				t.Errorf("结果未按分数降序排列")
			}
		}
	})

	t.Run("带最小分数过滤", func(t *testing.T) {
		results, err := store.Search(ctx, []float32{1.0, 0.0, 0.0}, 10,
			vector.WithMinScore(0.9),
		)
		if err != nil {
			t.Fatalf("Search 错误: %v", err)
		}

		// 只有 doc1 应该满足 0.9+ 分数
		if len(results) != 1 {
			t.Errorf("结果数量 = %d, want 1", len(results))
		}
	})

	t.Run("不包含嵌入向量", func(t *testing.T) {
		results, err := store.Search(ctx, []float32{1.0, 0.0, 0.0}, 1)
		if err != nil {
			t.Fatalf("Search 错误: %v", err)
		}

		if len(results) > 0 && results[0].Embedding != nil {
			t.Error("默认不应包含嵌入向量")
		}
	})

	t.Run("包含嵌入向量", func(t *testing.T) {
		results, err := store.Search(ctx, []float32{1.0, 0.0, 0.0}, 1,
			vector.WithEmbedding(true),
		)
		if err != nil {
			t.Fatalf("Search 错误: %v", err)
		}

		if len(results) > 0 && results[0].Embedding == nil {
			t.Error("应该包含嵌入向量")
		}
	})

	t.Run("维度不匹配", func(t *testing.T) {
		_, err := store.Search(ctx, []float32{1.0, 0.0}, 2)
		if err == nil {
			t.Error("维度不匹配应该报错")
		}
	})
}

// TestStoreGet 测试获取文档
func TestStoreGet(t *testing.T) {
	server := newMockMilvusServer()
	defer server.Close()
	store := newTestStore(t, server, 3)
	defer store.Close()

	ctx := context.Background()

	// 添加测试数据
	store.Add(ctx, []vector.Document{
		{ID: "doc1", Content: "test content", Embedding: []float32{0.1, 0.2, 0.3}},
	})

	t.Run("获取存在的文档", func(t *testing.T) {
		doc, err := store.Get(ctx, "doc1")
		if err != nil {
			t.Fatalf("Get 错误: %v", err)
		}

		if doc == nil {
			t.Fatal("doc 不应为 nil")
		}
		if doc.ID != "doc1" {
			t.Errorf("ID = %s, want doc1", doc.ID)
		}
		if doc.Content != "test content" {
			t.Errorf("Content = %s, want test content", doc.Content)
		}
	})

	t.Run("获取不存在的文档", func(t *testing.T) {
		_, err := store.Get(ctx, "nonexistent")
		if err == nil {
			t.Error("获取不存在的文档应该报错")
		}
	})
}

// TestStoreDelete 测试删除文档
func TestStoreDelete(t *testing.T) {
	server := newMockMilvusServer()
	defer server.Close()
	store := newTestStore(t, server, 3)
	defer store.Close()

	ctx := context.Background()

	// 添加测试数据
	store.Add(ctx, []vector.Document{
		{ID: "doc1", Content: "content 1", Embedding: []float32{0.1, 0.2, 0.3}},
		{ID: "doc2", Content: "content 2", Embedding: []float32{0.4, 0.5, 0.6}},
	})

	t.Run("删除单个文档", func(t *testing.T) {
		if err := store.Delete(ctx, []string{"doc1"}); err != nil {
			t.Errorf("Delete 错误: %v", err)
		}

		// 验证已删除
		_, err := store.Get(ctx, "doc1")
		if err == nil {
			t.Error("已删除的文档不应该能获取")
		}
	})

	t.Run("删除多个文档", func(t *testing.T) {
		store.Add(ctx, []vector.Document{
			{ID: "doc3", Content: "content 3", Embedding: []float32{0.1, 0.2, 0.3}},
			{ID: "doc4", Content: "content 4", Embedding: []float32{0.4, 0.5, 0.6}},
		})

		if err := store.Delete(ctx, []string{"doc3", "doc4"}); err != nil {
			t.Errorf("Delete 错误: %v", err)
		}
	})
}

// TestStoreUpdate 测试更新文档
func TestStoreUpdate(t *testing.T) {
	server := newMockMilvusServer()
	defer server.Close()
	store := newTestStore(t, server, 3)
	defer store.Close()

	ctx := context.Background()

	// 添加测试数据
	store.Add(ctx, []vector.Document{
		{ID: "doc1", Content: "original content", Embedding: []float32{0.1, 0.2, 0.3}},
	})

	t.Run("更新文档", func(t *testing.T) {
		updates := []vector.Document{
			{ID: "doc1", Content: "updated content", Embedding: []float32{0.9, 0.8, 0.7}},
		}

		if err := store.Update(ctx, updates); err != nil {
			t.Errorf("Update 错误: %v", err)
		}

		// 验证更新
		doc, err := store.Get(ctx, "doc1")
		if err != nil {
			t.Fatalf("Get 错误: %v", err)
		}
		if doc.Content != "updated content" {
			t.Errorf("Content = %s, want updated content", doc.Content)
		}
	})
}

// TestStoreCount 测试统计数量
func TestStoreCount(t *testing.T) {
	server := newMockMilvusServer()
	defer server.Close()
	store := newTestStore(t, server, 3)
	defer store.Close()

	ctx := context.Background()

	t.Run("空存储", func(t *testing.T) {
		count, err := store.Count(ctx)
		if err != nil {
			t.Errorf("Count 错误: %v", err)
		}
		if count != 0 {
			t.Errorf("count = %d, want 0", count)
		}
	})

	t.Run("有数据", func(t *testing.T) {
		store.Add(ctx, []vector.Document{
			{ID: "doc1", Content: "content 1", Embedding: []float32{0.1, 0.2, 0.3}},
			{ID: "doc2", Content: "content 2", Embedding: []float32{0.4, 0.5, 0.6}},
		})

		count, err := store.Count(ctx)
		if err != nil {
			t.Errorf("Count 错误: %v", err)
		}
		if count != 2 {
			t.Errorf("count = %d, want 2", count)
		}
	})
}

// TestStoreClear 测试清空
func TestStoreClear(t *testing.T) {
	server := newMockMilvusServer()
	defer server.Close()
	store := newTestStore(t, server, 3)
	defer store.Close()

	ctx := context.Background()

	// 添加测试数据
	store.Add(ctx, []vector.Document{
		{ID: "doc1", Content: "content 1", Embedding: []float32{0.1, 0.2, 0.3}},
		{ID: "doc2", Content: "content 2", Embedding: []float32{0.4, 0.5, 0.6}},
	})

	t.Run("清空存储", func(t *testing.T) {
		if err := store.Clear(ctx); err != nil {
			t.Errorf("Clear 错误: %v", err)
		}

		count, _ := store.Count(ctx)
		if count != 0 {
			t.Errorf("清空后 count = %d, want 0", count)
		}
	})
}

// TestStoreStats 测试统计信息
func TestStoreStats(t *testing.T) {
	server := newMockMilvusServer()
	defer server.Close()

	ctx := context.Background()
	store, err := NewStore(ctx,
		WithAddress(server.URL),
		WithHTTPClient(server.Client()),
		WithCollection("stats_test"),
		WithDimension(512),
		WithIndexType("HNSW"),
		WithMetricType("COSINE"),
	)
	if err != nil {
		t.Fatalf("NewStore 错误: %v", err)
	}
	defer store.Close()

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats 错误: %v", err)
	}

	if stats["collection"] != "stats_test" {
		t.Errorf("collection = %v, want stats_test", stats["collection"])
	}
	if stats["dimension"] != 512 {
		t.Errorf("dimension = %v, want 512", stats["dimension"])
	}
	if stats["index_type"] != "HNSW" {
		t.Errorf("index_type = %v, want HNSW", stats["index_type"])
	}
	if stats["connected"] != true {
		t.Error("connected 应为 true")
	}
}

// TestStoreClose 测试关闭连接
func TestStoreClose(t *testing.T) {
	server := newMockMilvusServer()
	defer server.Close()

	ctx := context.Background()
	store, err := NewStore(ctx,
		WithAddress(server.URL),
		WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("NewStore 错误: %v", err)
	}

	t.Run("关闭连接", func(t *testing.T) {
		if err := store.Close(); err != nil {
			t.Errorf("Close 错误: %v", err)
		}

		// 再次关闭不应报错
		if err := store.Close(); err != nil {
			t.Errorf("重复 Close 错误: %v", err)
		}
	})

	t.Run("关闭后操作应失败", func(t *testing.T) {
		_, err := store.Get(ctx, "doc1")
		if err == nil {
			t.Error("关闭后 Get 应该返回错误")
		}

		_, err = store.Search(ctx, []float32{0.1, 0.2, 0.3}, 1)
		if err == nil {
			t.Error("关闭后 Search 应该返回错误")
		}
	})
}

// TestStoreIndexOperations 测试索引操作
func TestStoreIndexOperations(t *testing.T) {
	server := newMockMilvusServer()
	defer server.Close()
	store := newTestStore(t, server, 3)
	defer store.Close()

	ctx := context.Background()

	t.Run("创建索引", func(t *testing.T) {
		if err := store.CreateIndex(ctx); err != nil {
			t.Errorf("CreateIndex 错误: %v", err)
		}
	})

	t.Run("删除索引", func(t *testing.T) {
		if err := store.DropIndex(ctx); err != nil {
			t.Errorf("DropIndex 错误: %v", err)
		}
	})
}

// TestStoreCollectionOperations 测试集合操作
func TestStoreCollectionOperations(t *testing.T) {
	server := newMockMilvusServer()
	defer server.Close()
	store := newTestStore(t, server, 3)
	defer store.Close()

	ctx := context.Background()

	t.Run("加载集合", func(t *testing.T) {
		if err := store.LoadCollection(ctx); err != nil {
			t.Errorf("LoadCollection 错误: %v", err)
		}
	})

	t.Run("释放集合", func(t *testing.T) {
		if err := store.ReleaseCollection(ctx); err != nil {
			t.Errorf("ReleaseCollection 错误: %v", err)
		}
	})

	t.Run("刷新数据", func(t *testing.T) {
		if err := store.Flush(ctx); err != nil {
			t.Errorf("Flush 错误: %v", err)
		}
	})
}

// TestStoreMetadata 测试元数据的序列化和反序列化
func TestStoreMetadata(t *testing.T) {
	server := newMockMilvusServer()
	defer server.Close()
	store := newTestStore(t, server, 3)
	defer store.Close()

	ctx := context.Background()

	// 添加带元数据的文档
	docs := []vector.Document{
		{
			ID:        "meta1",
			Content:   "metadata test",
			Embedding: []float32{0.1, 0.2, 0.3},
			Metadata: map[string]any{
				"source": "test",
				"count":  42.0,
				"tags":   []any{"a", "b"},
			},
		},
	}
	if err := store.Add(ctx, docs); err != nil {
		t.Fatalf("Add 错误: %v", err)
	}

	// 获取并验证元数据
	doc, err := store.Get(ctx, "meta1")
	if err != nil {
		t.Fatalf("Get 错误: %v", err)
	}

	if doc.Metadata == nil {
		t.Fatal("元数据不应为 nil")
	}
	if doc.Metadata["source"] != "test" {
		t.Errorf("metadata[source] = %v, want test", doc.Metadata["source"])
	}
}

// TestGenerateID 测试 ID 生成
func TestGenerateID(t *testing.T) {
	t.Run("生成唯一 ID", func(t *testing.T) {
		ids := make(map[string]bool)
		for i := 0; i < 100; i++ {
			id := generateID()
			if ids[id] {
				t.Errorf("生成了重复 ID: %s", id)
			}
			ids[id] = true
		}
	})

	t.Run("ID 长度", func(t *testing.T) {
		id := generateID()
		if len(id) < 10 {
			t.Errorf("ID 太短: %s", id)
		}
	})
}
