package weaviate

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/store/vector"
)

// ============== 模拟 Weaviate 服务器 ==============

// mockWeaviate 模拟 Weaviate REST/GraphQL API
type mockWeaviate struct {
	mu      sync.RWMutex
	objects map[string]map[string]*weaviateObject // className -> id -> object
	schemas map[string]bool                       // className -> exists
}

// newMockWeaviateServer 创建模拟 Weaviate 服务器
func newMockWeaviateServer() (*httptest.Server, *mockWeaviate) {
	mock := &mockWeaviate{
		objects: make(map[string]map[string]*weaviateObject),
		schemas: make(map[string]bool),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/.well-known/ready", mock.handleReady)
	mux.HandleFunc("/v1/schema/", mock.handleSchema)
	mux.HandleFunc("/v1/schema", mock.handleSchemaCreate)
	mux.HandleFunc("/v1/objects", mock.handleObjects)
	mux.HandleFunc("/v1/objects/", mock.handleObjectByID)
	mux.HandleFunc("/v1/graphql", mock.handleGraphQL)

	server := httptest.NewServer(mux)
	return server, mock
}

// handleReady 处理健康检查
func (m *mockWeaviate) handleReady(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// handleSchema 处理 schema 相关请求 (GET /v1/schema/{class}, DELETE /v1/schema/{class})
func (m *mockWeaviate) handleSchema(w http.ResponseWriter, r *http.Request) {
	// 提取 class 名称
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/schema/"), "/")
	className := parts[0]

	m.mu.Lock()
	defer m.mu.Unlock()

	switch r.Method {
	case "GET":
		if m.schemas[className] {
			json.NewEncoder(w).Encode(map[string]any{
				"class": className,
			})
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	case "DELETE":
		delete(m.schemas, className)
		delete(m.objects, className)
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleSchemaCreate 处理创建 schema 请求 (POST /v1/schema)
func (m *mockWeaviate) handleSchemaCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	className, _ := body["class"].(string)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.schemas[className] = true
	if m.objects[className] == nil {
		m.objects[className] = make(map[string]*weaviateObject)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{"class": className})
}

// handleObjects 处理创建对象请求 (POST /v1/objects)
func (m *mockWeaviate) handleObjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var obj weaviateObject
	if err := json.NewDecoder(r.Body).Decode(&obj); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.objects[obj.Class] == nil {
		m.objects[obj.Class] = make(map[string]*weaviateObject)
	}
	m.objects[obj.Class][obj.ID] = &obj

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(obj)
}

// handleObjectByID 处理单个对象操作 (DELETE /v1/objects/{class}/{id})
func (m *mockWeaviate) handleObjectByID(w http.ResponseWriter, r *http.Request) {
	// 解析 /v1/objects/{class}/{id}
	path := strings.TrimPrefix(r.URL.Path, "/v1/objects/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	className := parts[0]
	id := parts[1]

	m.mu.Lock()
	defer m.mu.Unlock()

	switch r.Method {
	case "DELETE":
		if objs, ok := m.objects[className]; ok {
			delete(objs, id)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleGraphQL 处理 GraphQL 查询
func (m *mockWeaviate) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req graphqlRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// 解析查询中的 class 名称
	className := m.extractClassName(req.Query)
	objs := m.objects[className]

	// 判断查询类型并构建响应
	var items []map[string]any

	if strings.Contains(req.Query, "nearVector") {
		// 向量搜索 - 从查询中提取向量并计算相似度
		queryVec := m.extractVector(req.Query)
		limit := m.extractLimit(req.Query)

		type scored struct {
			obj   *weaviateObject
			score float64
		}
		var results []scored
		for _, obj := range objs {
			if len(queryVec) > 0 && len(obj.Vector) > 0 {
				score := cosineF64(queryVec, obj.Vector)
				results = append(results, scored{obj: obj, score: score})
			}
		}
		// 排序
		for i := 0; i < len(results); i++ {
			for j := i + 1; j < len(results); j++ {
				if results[j].score > results[i].score {
					results[i], results[j] = results[j], results[i]
				}
			}
		}
		if limit > 0 && len(results) > limit {
			results = results[:limit]
		}
		for _, r := range results {
			items = append(items, m.objToGraphQL(r.obj, 1-r.score, r.score))
		}
	} else if strings.Contains(req.Query, "hybrid") {
		// 混合搜索 - 简单返回所有文档
		limit := m.extractLimit(req.Query)
		count := 0
		for _, obj := range objs {
			if limit > 0 && count >= limit {
				break
			}
			items = append(items, m.objToGraphQL(obj, 0, 0.8))
			count++
		}
	} else if strings.Contains(req.Query, "bm25") {
		// BM25 搜索 - 按关键词匹配
		queryStr := m.extractBM25Query(req.Query)
		limit := m.extractLimit(req.Query)
		count := 0
		for _, obj := range objs {
			if limit > 0 && count >= limit {
				break
			}
			content, _ := obj.Properties["content"].(string)
			if queryStr == "" || strings.Contains(strings.ToLower(content), strings.ToLower(queryStr)) {
				items = append(items, m.objToGraphQL(obj, 0, 0.7))
				count++
			}
		}
	}

	resp := graphqlResponse{
		Data: map[string]any{
			"Get": map[string]any{
				className: toAnySlice(items),
			},
		},
	}

	json.NewEncoder(w).Encode(resp)
}

// extractClassName 从 GraphQL 查询中提取 class 名称
func (m *mockWeaviate) extractClassName(query string) string {
	// 简单查找 Get { ClassName(
	idx := strings.Index(query, "Get")
	if idx < 0 {
		return ""
	}
	rest := query[idx+3:]
	// 跳过空白和 {
	rest = strings.TrimLeft(rest, " \t\n\r{")
	// 读到 ( 或空白
	end := strings.IndexAny(rest, "( \t\n\r")
	if end < 0 {
		return rest
	}
	return strings.TrimSpace(rest[:end])
}

// extractVector 从查询中提取向量
func (m *mockWeaviate) extractVector(query string) []float32 {
	// 查找 vector: [...] 模式
	idx := strings.Index(query, "vector:")
	if idx < 0 {
		return nil
	}
	rest := query[idx+7:]
	rest = strings.TrimLeft(rest, " \t\n\r")
	if len(rest) == 0 || rest[0] != '[' {
		return nil
	}
	end := strings.Index(rest, "]")
	if end < 0 {
		return nil
	}
	vecStr := rest[1:end]
	parts := strings.Split(vecStr, " ")
	var vec []float32
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var f float32
		fmt.Sscanf(p, "%f", &f)
		vec = append(vec, f)
	}
	return vec
}

// extractLimit 从查询中提取 limit
func (m *mockWeaviate) extractLimit(query string) int {
	idx := strings.Index(query, "limit:")
	if idx < 0 {
		return 0
	}
	rest := query[idx+6:]
	rest = strings.TrimLeft(rest, " \t\n\r")
	var limit int
	fmt.Sscanf(rest, "%d", &limit)
	return limit
}

// extractBM25Query 从 BM25 查询中提取搜索文本
func (m *mockWeaviate) extractBM25Query(query string) string {
	idx := strings.Index(query, `query:`)
	if idx < 0 {
		return ""
	}
	rest := query[idx+6:]
	rest = strings.TrimLeft(rest, " \t\n\r")
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	end := strings.Index(rest[1:], `"`)
	if end < 0 {
		return ""
	}
	return rest[1 : end+1]
}

// objToGraphQL 将对象转换为 GraphQL 响应格式
func (m *mockWeaviate) objToGraphQL(obj *weaviateObject, distance, score float64) map[string]any {
	item := map[string]any{
		"_additional": map[string]any{
			"id":       obj.ID,
			"distance": distance,
			"score":    score,
		},
	}
	if content, ok := obj.Properties["content"]; ok {
		item["content"] = content
	}
	if docId, ok := obj.Properties["docId"]; ok {
		item["docId"] = docId
	}
	return item
}

// toAnySlice 将 map 切片转为 any 切片
func toAnySlice(items []map[string]any) []any {
	result := make([]any, len(items))
	for i, item := range items {
		result[i] = item
	}
	return result
}

// cosineF64 计算余弦相似度 (float32 向量)
func cosineF64(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// ============== 辅助函数 ==============

// newTestStore 创建连接到模拟服务器的测试 Store
func newTestStore(t *testing.T, server *httptest.Server, opts ...Option) *Store {
	t.Helper()

	// 从 httptest.Server URL 中提取 host (去掉 http:// 前缀)
	host := strings.TrimPrefix(server.URL, "http://")

	allOpts := []Option{
		WithHost(host),
		WithScheme("http"),
		WithClass("TestDoc"),
	}
	allOpts = append(allOpts, opts...)

	store, err := NewStore(context.Background(), allOpts...)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	return store
}

// makeEmbedding 创建指定维度的测试向量
func makeEmbedding(dim int, base float32) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = base + float32(i)*0.01
	}
	return v
}

// ============== 测试用例 ==============

func TestNewStore(t *testing.T) {
	server, _ := newMockWeaviateServer()
	defer server.Close()

	host := strings.TrimPrefix(server.URL, "http://")

	t.Run("默认配置", func(t *testing.T) {
		store, err := NewStore(context.Background(),
			WithHost(host),
			WithScheme("http"),
		)
		if err != nil {
			t.Fatalf("创建 Store 失败: %v", err)
		}
		defer store.Close()

		if store.host != host {
			t.Errorf("host = %q, want %q", store.host, host)
		}
		if store.scheme != "http" {
			t.Errorf("scheme = %q, want %q", store.scheme, "http")
		}
		if store.className != "Document" {
			t.Errorf("className = %q, want %q", store.className, "Document")
		}
		if store.vectorizer != "none" {
			t.Errorf("vectorizer = %q, want %q", store.vectorizer, "none")
		}
		if !store.connected {
			t.Error("expected connected = true")
		}
	})

	t.Run("自定义配置", func(t *testing.T) {
		customClient := &http.Client{Timeout: 10 * time.Second}
		store, err := NewStore(context.Background(),
			WithHost(host),
			WithScheme("http"),
			WithClass("CustomClass"),
			WithAPIKey("test-key"),
			WithTenant("tenant-1"),
			WithVectorizer("text2vec-openai"),
			WithHTTPClient(customClient),
		)
		if err != nil {
			t.Fatalf("创建 Store 失败: %v", err)
		}
		defer store.Close()

		if store.className != "CustomClass" {
			t.Errorf("className = %q, want %q", store.className, "CustomClass")
		}
		if store.apiKey != "test-key" {
			t.Errorf("apiKey = %q, want %q", store.apiKey, "test-key")
		}
		if store.tenant != "tenant-1" {
			t.Errorf("tenant = %q, want %q", store.tenant, "tenant-1")
		}
		if store.vectorizer != "text2vec-openai" {
			t.Errorf("vectorizer = %q, want %q", store.vectorizer, "text2vec-openai")
		}
		if store.client != customClient {
			t.Error("client 不是自定义的 HTTP 客户端")
		}
	})

	t.Run("连接失败时使用模拟模式", func(t *testing.T) {
		// 使用不存在的地址，connect 不会报错（模拟模式）
		store, err := NewStore(context.Background(),
			WithHost("invalid-host:99999"),
			WithScheme("http"),
		)
		if err != nil {
			t.Fatalf("模拟模式不应返回错误: %v", err)
		}
		defer store.Close()

		if !store.connected {
			t.Error("模拟模式下应标记为 connected")
		}
	})
}

func TestStoreAdd(t *testing.T) {
	server, _ := newMockWeaviateServer()
	defer server.Close()

	t.Run("添加单个文档", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		err := store.Add(context.Background(), []vector.Document{
			{
				ID:        "doc-1",
				Content:   "Hello World",
				Embedding: makeEmbedding(4, 0.1),
			},
		})
		if err != nil {
			t.Fatalf("Add 失败: %v", err)
		}

		// 验证本地存储
		if len(store.documents) != 1 {
			t.Errorf("documents 数量 = %d, want 1", len(store.documents))
		}
		if doc, ok := store.documents["doc-1"]; !ok {
			t.Error("文档 doc-1 未找到")
		} else if doc.Content != "Hello World" {
			t.Errorf("Content = %q, want %q", doc.Content, "Hello World")
		}
	})

	t.Run("添加多个文档", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		docs := []vector.Document{
			{ID: "a", Content: "文档A", Embedding: makeEmbedding(4, 0.1)},
			{ID: "b", Content: "文档B", Embedding: makeEmbedding(4, 0.5)},
			{ID: "c", Content: "文档C", Embedding: makeEmbedding(4, 0.9)},
		}
		if err := store.Add(context.Background(), docs); err != nil {
			t.Fatalf("Add 失败: %v", err)
		}

		if len(store.documents) != 3 {
			t.Errorf("documents 数量 = %d, want 3", len(store.documents))
		}
	})

	t.Run("添加带元数据的文档", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		err := store.Add(context.Background(), []vector.Document{
			{
				ID:        "meta-1",
				Content:   "带元数据的文档",
				Embedding: makeEmbedding(4, 0.3),
				Metadata: map[string]any{
					"source": "test",
					"page":   1,
				},
			},
		})
		if err != nil {
			t.Fatalf("Add 失败: %v", err)
		}

		doc := store.documents["meta-1"]
		if doc == nil {
			t.Fatal("文档 meta-1 未找到")
		}
		if doc.Metadata["source"] != "test" {
			t.Errorf("Metadata[source] = %v, want %q", doc.Metadata["source"], "test")
		}
	})

	t.Run("未连接时返回错误", func(t *testing.T) {
		store := newTestStore(t, server)
		store.Close() // 断开连接

		err := store.Add(context.Background(), []vector.Document{
			{ID: "x", Content: "test"},
		})
		if err == nil {
			t.Error("未连接时 Add 应返回错误")
		}
	})
}

func TestStoreSearch(t *testing.T) {
	server, mock := newMockWeaviateServer()
	defer server.Close()

	t.Run("向量搜索", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		// 准备数据 - 添加到 mock 服务器和本地存储
		docs := []vector.Document{
			{ID: "s1", Content: "Go 编程", Embedding: []float32{1, 0, 0, 0}},
			{ID: "s2", Content: "Python 编程", Embedding: []float32{0.9, 0.1, 0, 0}},
			{ID: "s3", Content: "美食推荐", Embedding: []float32{0, 0, 1, 0}},
		}
		if err := store.Add(context.Background(), docs); err != nil {
			t.Fatalf("Add 失败: %v", err)
		}

		// 搜索与 "Go 编程" 相似的文档
		results, err := store.Search(context.Background(), []float32{1, 0, 0, 0}, 2)
		if err != nil {
			t.Fatalf("Search 失败: %v", err)
		}

		if len(results) == 0 {
			t.Fatal("搜索结果为空")
		}

		// 验证返回了结果（通过 GraphQL 或本地回退）
		if len(results) > 2 {
			t.Errorf("results 数量 = %d, want <= 2", len(results))
		}
	})

	t.Run("本地回退搜索", func(t *testing.T) {
		// 使用无效地址，强制本地搜索
		store := &Store{
			host:      "invalid:0",
			scheme:    "http",
			className: "Test",
			client:    &http.Client{Timeout: 100 * time.Millisecond},
			documents: make(map[string]*vector.Document),
			connected: true,
		}
		defer store.Close()

		// 直接写入本地存储
		store.documents["l1"] = &vector.Document{ID: "l1", Content: "Go", Embedding: []float32{1, 0, 0}}
		store.documents["l2"] = &vector.Document{ID: "l2", Content: "Python", Embedding: []float32{0.8, 0.2, 0}}
		store.documents["l3"] = &vector.Document{ID: "l3", Content: "Cooking", Embedding: []float32{0, 0, 1}}

		results, err := store.Search(context.Background(), []float32{1, 0, 0}, 2)
		if err != nil {
			t.Fatalf("本地搜索失败: %v", err)
		}

		if len(results) != 2 {
			t.Fatalf("results 数量 = %d, want 2", len(results))
		}

		// 第一个结果应该是 "Go"（完全匹配）
		if results[0].Content != "Go" {
			t.Errorf("第一个结果 = %q, want %q", results[0].Content, "Go")
		}
		if results[0].Score < 0.9 {
			t.Errorf("第一个结果相似度 = %f, 应该 > 0.9", results[0].Score)
		}

		// 第二个结果应该是 "Python"
		if results[1].Content != "Python" {
			t.Errorf("第二个结果 = %q, want %q", results[1].Content, "Python")
		}
	})

	t.Run("MinScore 过滤", func(t *testing.T) {
		store := &Store{
			host:      "invalid:0",
			scheme:    "http",
			className: "Test",
			client:    &http.Client{Timeout: 100 * time.Millisecond},
			documents: make(map[string]*vector.Document),
			connected: true,
		}
		defer store.Close()

		store.documents["h1"] = &vector.Document{ID: "h1", Content: "Very similar", Embedding: []float32{1, 0, 0}}
		store.documents["h2"] = &vector.Document{ID: "h2", Content: "Somewhat similar", Embedding: []float32{0.5, 0.5, 0}}
		store.documents["h3"] = &vector.Document{ID: "h3", Content: "Not similar", Embedding: []float32{0, 0, 1}}

		results, err := store.Search(context.Background(), []float32{1, 0, 0}, 10,
			vector.WithMinScore(0.8))
		if err != nil {
			t.Fatalf("搜索失败: %v", err)
		}

		// 只有 "Very similar" 应该通过阈值
		if len(results) != 1 {
			t.Fatalf("results 数量 = %d, want 1", len(results))
		}
		if results[0].Content != "Very similar" {
			t.Errorf("结果 = %q, want %q", results[0].Content, "Very similar")
		}
	})

	t.Run("未连接时返回错误", func(t *testing.T) {
		store := newTestStore(t, server)
		store.Close()

		_, err := store.Search(context.Background(), []float32{1, 0, 0}, 5)
		if err == nil {
			t.Error("未连接时 Search 应返回错误")
		}
	})

	_ = mock // 保持引用
}

func TestStoreHybridSearch(t *testing.T) {
	server, _ := newMockWeaviateServer()
	defer server.Close()

	t.Run("混合搜索", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		// 添加数据
		docs := []vector.Document{
			{ID: "h1", Content: "Go programming language", Embedding: []float32{1, 0, 0, 0}},
			{ID: "h2", Content: "Python programming language", Embedding: []float32{0.8, 0.2, 0, 0}},
		}
		if err := store.Add(context.Background(), docs); err != nil {
			t.Fatalf("Add 失败: %v", err)
		}

		results, err := store.HybridSearch(context.Background(),
			"programming", []float32{1, 0, 0, 0}, 2, 0.5)
		if err != nil {
			t.Fatalf("HybridSearch 失败: %v", err)
		}

		if len(results) == 0 {
			t.Error("混合搜索结果为空")
		}
	})

	t.Run("未连接时返回错误", func(t *testing.T) {
		store := newTestStore(t, server)
		store.Close()

		_, err := store.HybridSearch(context.Background(),
			"test", []float32{1, 0, 0}, 5, 0.5)
		if err == nil {
			t.Error("未连接时 HybridSearch 应返回错误")
		}
	})
}

func TestStoreBM25Search(t *testing.T) {
	server, _ := newMockWeaviateServer()
	defer server.Close()

	t.Run("BM25 搜索", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		docs := []vector.Document{
			{ID: "b1", Content: "Go is fast", Embedding: []float32{1, 0, 0}},
			{ID: "b2", Content: "Python is versatile", Embedding: []float32{0, 1, 0}},
			{ID: "b3", Content: "Go concurrency", Embedding: []float32{0.9, 0, 0.1}},
		}
		if err := store.Add(context.Background(), docs); err != nil {
			t.Fatalf("Add 失败: %v", err)
		}

		results, err := store.BM25Search(context.Background(), "Go", 5)
		if err != nil {
			t.Fatalf("BM25Search 失败: %v", err)
		}

		// 应该匹配包含 "Go" 的文档
		if len(results) == 0 {
			t.Error("BM25 搜索结果为空")
		}

		// 验证结果包含 Go 相关内容
		for _, r := range results {
			if !strings.Contains(r.Content, "Go") {
				t.Errorf("结果 %q 不包含 'Go'", r.Content)
			}
		}
	})

	t.Run("未连接时返回错误", func(t *testing.T) {
		store := newTestStore(t, server)
		store.Close()

		_, err := store.BM25Search(context.Background(), "test", 5)
		if err == nil {
			t.Error("未连接时 BM25Search 应返回错误")
		}
	})
}

func TestStoreGet(t *testing.T) {
	server, _ := newMockWeaviateServer()
	defer server.Close()

	t.Run("获取存在的文档", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		err := store.Add(context.Background(), []vector.Document{
			{ID: "get-1", Content: "测试文档", Embedding: makeEmbedding(4, 0.5)},
		})
		if err != nil {
			t.Fatalf("Add 失败: %v", err)
		}

		doc, err := store.Get(context.Background(), "get-1")
		if err != nil {
			t.Fatalf("Get 失败: %v", err)
		}
		if doc == nil {
			t.Fatal("doc 为 nil")
		}
		if doc.Content != "测试文档" {
			t.Errorf("Content = %q, want %q", doc.Content, "测试文档")
		}
	})

	t.Run("获取不存在的文档", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		_, err := store.Get(context.Background(), "non-existent")
		if err == nil {
			t.Error("获取不存在的文档应返回错误")
		}
	})

	t.Run("未连接时返回错误", func(t *testing.T) {
		store := newTestStore(t, server)
		store.Close()

		_, err := store.Get(context.Background(), "any")
		if err == nil {
			t.Error("未连接时 Get 应返回错误")
		}
	})
}

func TestStoreDelete(t *testing.T) {
	server, _ := newMockWeaviateServer()
	defer server.Close()

	t.Run("删除单个文档", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		// 先添加
		err := store.Add(context.Background(), []vector.Document{
			{ID: "del-1", Content: "待删除", Embedding: makeEmbedding(4, 0.1)},
			{ID: "del-2", Content: "保留", Embedding: makeEmbedding(4, 0.2)},
		})
		if err != nil {
			t.Fatalf("Add 失败: %v", err)
		}

		// 删除一个
		err = store.Delete(context.Background(), []string{"del-1"})
		if err != nil {
			t.Fatalf("Delete 失败: %v", err)
		}

		// 验证
		if len(store.documents) != 1 {
			t.Errorf("documents 数量 = %d, want 1", len(store.documents))
		}
		if _, ok := store.documents["del-1"]; ok {
			t.Error("del-1 应该已被删除")
		}
		if _, ok := store.documents["del-2"]; !ok {
			t.Error("del-2 应该保留")
		}
	})

	t.Run("删除多个文档", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		err := store.Add(context.Background(), []vector.Document{
			{ID: "m1", Content: "a"},
			{ID: "m2", Content: "b"},
			{ID: "m3", Content: "c"},
		})
		if err != nil {
			t.Fatalf("Add 失败: %v", err)
		}

		err = store.Delete(context.Background(), []string{"m1", "m3"})
		if err != nil {
			t.Fatalf("Delete 失败: %v", err)
		}

		if len(store.documents) != 1 {
			t.Errorf("documents 数量 = %d, want 1", len(store.documents))
		}
	})

	t.Run("删除不存在的文档", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		// 不应报错
		err := store.Delete(context.Background(), []string{"non-existent"})
		if err != nil {
			t.Errorf("删除不存在的文档不应报错: %v", err)
		}
	})

	t.Run("未连接时返回错误", func(t *testing.T) {
		store := newTestStore(t, server)
		store.Close()

		err := store.Delete(context.Background(), []string{"any"})
		if err == nil {
			t.Error("未连接时 Delete 应返回错误")
		}
	})
}

func TestStoreClear(t *testing.T) {
	server, _ := newMockWeaviateServer()
	defer server.Close()

	t.Run("清空存储", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		// 添加数据
		err := store.Add(context.Background(), []vector.Document{
			{ID: "c1", Content: "doc1"},
			{ID: "c2", Content: "doc2"},
			{ID: "c3", Content: "doc3"},
		})
		if err != nil {
			t.Fatalf("Add 失败: %v", err)
		}

		if len(store.documents) != 3 {
			t.Fatalf("清空前数量 = %d, want 3", len(store.documents))
		}

		// 清空
		err = store.Clear(context.Background())
		if err != nil {
			t.Fatalf("Clear 失败: %v", err)
		}

		if len(store.documents) != 0 {
			t.Errorf("清空后数量 = %d, want 0", len(store.documents))
		}
	})

	t.Run("清空空存储", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		err := store.Clear(context.Background())
		if err != nil {
			t.Errorf("清空空存储不应报错: %v", err)
		}
	})

	t.Run("未连接时返回错误", func(t *testing.T) {
		store := newTestStore(t, server)
		store.Close()

		err := store.Clear(context.Background())
		if err == nil {
			t.Error("未连接时 Clear 应返回错误")
		}
	})
}

func TestStoreCount(t *testing.T) {
	server, _ := newMockWeaviateServer()
	defer server.Close()

	t.Run("空存储", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		count, err := store.Count(context.Background())
		if err != nil {
			t.Fatalf("Count 失败: %v", err)
		}
		if count != 0 {
			t.Errorf("count = %d, want 0", count)
		}
	})

	t.Run("有文档", func(t *testing.T) {
		store := newTestStore(t, server)
		defer store.Close()

		store.Add(context.Background(), []vector.Document{
			{ID: "n1", Content: "a"},
			{ID: "n2", Content: "b"},
		})

		count, err := store.Count(context.Background())
		if err != nil {
			t.Fatalf("Count 失败: %v", err)
		}
		if count != 2 {
			t.Errorf("count = %d, want 2", count)
		}
	})

	t.Run("未连接时返回错误", func(t *testing.T) {
		store := newTestStore(t, server)
		store.Close()

		_, err := store.Count(context.Background())
		if err == nil {
			t.Error("未连接时 Count 应返回错误")
		}
	})
}

func TestStoreClose(t *testing.T) {
	server, _ := newMockWeaviateServer()
	defer server.Close()

	t.Run("关闭连接", func(t *testing.T) {
		store := newTestStore(t, server)

		err := store.Close()
		if err != nil {
			t.Fatalf("Close 失败: %v", err)
		}

		if store.connected {
			t.Error("关闭后应标记为未连接")
		}
	})

	t.Run("关闭后所有操作失败", func(t *testing.T) {
		store := newTestStore(t, server)
		store.Close()

		ctx := context.Background()

		if err := store.Add(ctx, nil); err == nil {
			t.Error("Add 应返回错误")
		}
		if _, err := store.Search(ctx, nil, 5); err == nil {
			t.Error("Search 应返回错误")
		}
		if _, err := store.Get(ctx, "x"); err == nil {
			t.Error("Get 应返回错误")
		}
		if err := store.Delete(ctx, nil); err == nil {
			t.Error("Delete 应返回错误")
		}
		if err := store.Clear(ctx); err == nil {
			t.Error("Clear 应返回错误")
		}
		if _, err := store.Count(ctx); err == nil {
			t.Error("Count 应返回错误")
		}
		if _, err := store.HybridSearch(ctx, "", nil, 5, 0.5); err == nil {
			t.Error("HybridSearch 应返回错误")
		}
		if _, err := store.BM25Search(ctx, "", 5); err == nil {
			t.Error("BM25Search 应返回错误")
		}
	})
}

func TestStoreInterface(t *testing.T) {
	// 编译时验证 Store 实现了 vector.Store 接口
	var _ vector.Store = (*Store)(nil)
}

// ============== 辅助函数测试 ==============

func TestEscapeString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"普通字符串", "hello", "hello"},
		{"双引号", `say "hi"`, `say \"hi\"`},
		{"反斜杠", `path\to\file`, `path\\to\\file`},
		{"混合", `a\"b`, `a\\\"b`},
		{"空字符串", "", ""},
		{"中文", "你好世界", "你好世界"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeString(tt.input)
			if got != tt.want {
				t.Errorf("escapeString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestWeaviateCosineSimilarity(t *testing.T) {
	t.Run("相同向量", func(t *testing.T) {
		a := []float32{1, 2, 3}
		got := weaviateCosineSimilarity(a, a)
		if math.Abs(got-1.0) > 1e-6 {
			t.Errorf("相同向量的相似度 = %f, want 1.0", got)
		}
	})

	t.Run("正交向量", func(t *testing.T) {
		a := []float32{1, 0, 0}
		b := []float32{0, 1, 0}
		got := weaviateCosineSimilarity(a, b)
		if math.Abs(got) > 1e-6 {
			t.Errorf("正交向量的相似度 = %f, want 0.0", got)
		}
	})

	t.Run("反向向量", func(t *testing.T) {
		a := []float32{1, 0, 0}
		b := []float32{-1, 0, 0}
		got := weaviateCosineSimilarity(a, b)
		if math.Abs(got+1.0) > 1e-6 {
			t.Errorf("反向向量的相似度 = %f, want -1.0", got)
		}
	})

	t.Run("不同长度向量", func(t *testing.T) {
		a := []float32{1, 2}
		b := []float32{1, 2, 3}
		got := weaviateCosineSimilarity(a, b)
		if got != 0 {
			t.Errorf("不同长度向量的相似度 = %f, want 0", got)
		}
	})

	t.Run("空向量", func(t *testing.T) {
		got := weaviateCosineSimilarity(nil, nil)
		if got != 0 {
			t.Errorf("空向量的相似度 = %f, want 0", got)
		}
	})

	t.Run("零向量", func(t *testing.T) {
		a := []float32{0, 0, 0}
		b := []float32{1, 2, 3}
		got := weaviateCosineSimilarity(a, b)
		if got != 0 {
			t.Errorf("零向量的相似度 = %f, want 0", got)
		}
	})
}

func TestWeaviateSqrt(t *testing.T) {
	tests := []struct {
		name string
		x    float64
		want float64
	}{
		{"sqrt(4)", 4.0, 2.0},
		{"sqrt(9)", 9.0, 3.0},
		{"sqrt(2)", 2.0, math.Sqrt(2)},
		{"sqrt(0)", 0.0, 0.0},
		{"sqrt(-1)", -1.0, 0.0},
		{"sqrt(100)", 100.0, 10.0},
		{"sqrt(0.25)", 0.25, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := weaviateSqrt(tt.x)
			if math.Abs(got-tt.want) > 1e-6 {
				t.Errorf("weaviateSqrt(%f) = %f, want %f", tt.x, got, tt.want)
			}
		})
	}
}

func TestBuildWhereClause(t *testing.T) {
	store := &Store{className: "Test"}

	t.Run("空过滤", func(t *testing.T) {
		got := store.buildWhereClause(nil)
		if got != "" {
			t.Errorf("空过滤应返回空字符串, got %q", got)
		}
	})

	t.Run("单条件", func(t *testing.T) {
		got := store.buildWhereClause(map[string]any{
			"source": "test",
		})
		if !strings.Contains(got, "where:") {
			t.Error("应包含 'where:'")
		}
		if !strings.Contains(got, `"source"`) {
			t.Error("应包含 source 字段")
		}
		if !strings.Contains(got, "Equal") {
			t.Error("应包含 Equal 操作符")
		}
		// 单条件不应包含 And
		if strings.Contains(got, "And") {
			t.Error("单条件不应包含 And")
		}
	})

	t.Run("多条件", func(t *testing.T) {
		got := store.buildWhereClause(map[string]any{
			"source": "test",
			"page":   "1",
		})
		if !strings.Contains(got, "And") {
			t.Error("多条件应包含 And")
		}
		if !strings.Contains(got, "operands") {
			t.Error("多条件应包含 operands")
		}
	})
}

func TestBuildGraphQLQuery(t *testing.T) {
	store := &Store{className: "TestDoc"}

	t.Run("基本查询", func(t *testing.T) {
		query := store.buildGraphQLQuery(
			[]float32{1, 0, 0}, 5, &vector.SearchConfig{})

		if !strings.Contains(query, "TestDoc") {
			t.Error("查询应包含类名")
		}
		if !strings.Contains(query, "nearVector") {
			t.Error("查询应包含 nearVector")
		}
		if !strings.Contains(query, "limit: 5") {
			t.Error("查询应包含 limit")
		}
		if !strings.Contains(query, "content") {
			t.Error("查询应返回 content 字段")
		}
		if !strings.Contains(query, "_additional") {
			t.Error("查询应返回 _additional 字段")
		}
	})

	t.Run("带过滤查询", func(t *testing.T) {
		query := store.buildGraphQLQuery(
			[]float32{1, 0}, 3,
			&vector.SearchConfig{
				Filter: map[string]any{"source": "test"},
			})

		if !strings.Contains(query, "where:") {
			t.Error("应包含过滤条件")
		}
	})
}

func TestParseGraphQLResponse(t *testing.T) {
	store := &Store{className: "TestDoc"}

	t.Run("正常响应", func(t *testing.T) {
		data := map[string]any{
			"Get": map[string]any{
				"TestDoc": []any{
					map[string]any{
						"content": "Hello",
						"docId":   "doc-1",
						"_additional": map[string]any{
							"id":       "doc-1",
							"distance": 0.1,
						},
					},
					map[string]any{
						"content": "World",
						"docId":   "doc-2",
						"_additional": map[string]any{
							"id":       "doc-2",
							"distance": 0.3,
							"score":    0.85,
						},
					},
				},
			},
		}

		docs, err := store.parseGraphQLResponse(data)
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if len(docs) != 2 {
			t.Fatalf("docs 数量 = %d, want 2", len(docs))
		}

		if docs[0].ID != "doc-1" {
			t.Errorf("docs[0].ID = %q, want %q", docs[0].ID, "doc-1")
		}
		if docs[0].Content != "Hello" {
			t.Errorf("docs[0].Content = %q, want %q", docs[0].Content, "Hello")
		}
		// distance=0.1, score=1-0.1=0.9
		if docs[0].Score < 0.89 || docs[0].Score > 0.91 {
			t.Errorf("docs[0].Score = %f, want ~0.9", docs[0].Score)
		}

		// 第二个文档有 score 字段，应使用 score 而非 distance
		if docs[1].Score < 0.84 || docs[1].Score > 0.86 {
			t.Errorf("docs[1].Score = %f, want ~0.85", docs[1].Score)
		}
	})

	t.Run("空响应", func(t *testing.T) {
		data := map[string]any{}
		docs, err := store.parseGraphQLResponse(data)
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if docs != nil {
			t.Errorf("空响应应返回 nil, got %v", docs)
		}
	})

	t.Run("无匹配类", func(t *testing.T) {
		data := map[string]any{
			"Get": map[string]any{
				"OtherClass": []any{},
			},
		}
		docs, err := store.parseGraphQLResponse(data)
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if docs != nil {
			t.Errorf("无匹配类应返回 nil, got %v", docs)
		}
	})

	t.Run("docId 为空时使用 _additional.id", func(t *testing.T) {
		data := map[string]any{
			"Get": map[string]any{
				"TestDoc": []any{
					map[string]any{
						"content": "Fallback ID",
						"_additional": map[string]any{
							"id": "fallback-id",
						},
					},
				},
			},
		}

		docs, err := store.parseGraphQLResponse(data)
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if len(docs) != 1 {
			t.Fatalf("docs 数量 = %d, want 1", len(docs))
		}
		if docs[0].ID != "fallback-id" {
			t.Errorf("ID = %q, want %q", docs[0].ID, "fallback-id")
		}
	})
}

func TestDoRequest(t *testing.T) {
	t.Run("成功请求", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}))
		defer server.Close()

		host := strings.TrimPrefix(server.URL, "http://")
		store := &Store{
			host:   host,
			scheme: "http",
			client: &http.Client{Timeout: 5 * time.Second},
		}

		var result map[string]string
		url := fmt.Sprintf("http://%s/test", host)
		err := store.doRequest(context.Background(), "GET", url, nil, &result)
		if err != nil {
			t.Fatalf("doRequest 失败: %v", err)
		}
		if result["status"] != "ok" {
			t.Errorf("status = %q, want %q", result["status"], "ok")
		}
	})

	t.Run("API 错误", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("bad request"))
		}))
		defer server.Close()

		host := strings.TrimPrefix(server.URL, "http://")
		store := &Store{
			host:   host,
			scheme: "http",
			client: &http.Client{Timeout: 5 * time.Second},
		}

		url := fmt.Sprintf("http://%s/test", host)
		err := store.doRequest(context.Background(), "GET", url, nil, nil)
		if err == nil {
			t.Error("应返回错误")
		}
		if !strings.Contains(err.Error(), "400") {
			t.Errorf("错误信息应包含状态码 400, got: %v", err)
		}
	})

	t.Run("带 API Key", func(t *testing.T) {
		var gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		host := strings.TrimPrefix(server.URL, "http://")
		store := &Store{
			host:   host,
			scheme: "http",
			apiKey: "my-secret-key",
			client: &http.Client{Timeout: 5 * time.Second},
		}

		url := fmt.Sprintf("http://%s/test", host)
		store.doRequest(context.Background(), "GET", url, nil, nil)
		if gotAuth != "Bearer my-secret-key" {
			t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer my-secret-key")
		}
	})

	t.Run("带 Tenant", func(t *testing.T) {
		var gotTenant string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotTenant = r.Header.Get("X-Weaviate-Tenant")
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		host := strings.TrimPrefix(server.URL, "http://")
		store := &Store{
			host:   host,
			scheme: "http",
			tenant: "test-tenant",
			client: &http.Client{Timeout: 5 * time.Second},
		}

		url := fmt.Sprintf("http://%s/test", host)
		store.doRequest(context.Background(), "GET", url, nil, nil)
		if gotTenant != "test-tenant" {
			t.Errorf("X-Weaviate-Tenant = %q, want %q", gotTenant, "test-tenant")
		}
	})

	t.Run("带请求体", func(t *testing.T) {
		var gotBody map[string]string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&gotBody)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		host := strings.TrimPrefix(server.URL, "http://")
		store := &Store{
			host:   host,
			scheme: "http",
			client: &http.Client{Timeout: 5 * time.Second},
		}

		body := map[string]string{"key": "value"}
		url := fmt.Sprintf("http://%s/test", host)
		store.doRequest(context.Background(), "POST", url, body, nil)
		if gotBody["key"] != "value" {
			t.Errorf("body.key = %q, want %q", gotBody["key"], "value")
		}
	})
}

func TestLocalSearch(t *testing.T) {
	store := &Store{
		className: "Test",
		documents: map[string]*vector.Document{
			"d1": {ID: "d1", Content: "Go", Embedding: []float32{1, 0, 0}},
			"d2": {ID: "d2", Content: "Python", Embedding: []float32{0.7, 0.7, 0}},
			"d3": {ID: "d3", Content: "Cooking", Embedding: []float32{0, 0, 1}},
			"d4": {ID: "d4", Content: "No embedding"}, // 无向量
		},
	}

	t.Run("基本搜索", func(t *testing.T) {
		results, err := store.localSearch(
			[]float32{1, 0, 0}, 2, &vector.SearchConfig{})
		if err != nil {
			t.Fatalf("本地搜索失败: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("results 数量 = %d, want 2", len(results))
		}
		// "Go" 应排第一
		if results[0].Content != "Go" {
			t.Errorf("第一个结果 = %q, want %q", results[0].Content, "Go")
		}
	})

	t.Run("MinScore 过滤", func(t *testing.T) {
		results, err := store.localSearch(
			[]float32{1, 0, 0}, 10, &vector.SearchConfig{MinScore: 0.9})
		if err != nil {
			t.Fatalf("搜索失败: %v", err)
		}
		// 只有 "Go" 的相似度 = 1.0 > 0.9
		if len(results) != 1 {
			t.Fatalf("results 数量 = %d, want 1", len(results))
		}
		if results[0].Content != "Go" {
			t.Errorf("结果 = %q, want %q", results[0].Content, "Go")
		}
	})

	t.Run("topK 限制", func(t *testing.T) {
		results, err := store.localSearch(
			[]float32{1, 0, 0}, 1, &vector.SearchConfig{})
		if err != nil {
			t.Fatalf("搜索失败: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("results 数量 = %d, want 1", len(results))
		}
	})
}

func TestTenantSupport(t *testing.T) {
	server, _ := newMockWeaviateServer()
	defer server.Close()

	t.Run("租户配置", func(t *testing.T) {
		store := newTestStore(t, server, WithTenant("tenant-1"))
		defer store.Close()

		if store.tenant != "tenant-1" {
			t.Errorf("tenant = %q, want %q", store.tenant, "tenant-1")
		}
	})

	t.Run("删除时附带租户参数", func(t *testing.T) {
		var gotQuery string
		tenantServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/v1/objects/") && r.Method == "DELETE" {
				gotQuery = r.URL.RawQuery
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{})
		}))
		defer tenantServer.Close()

		host := strings.TrimPrefix(tenantServer.URL, "http://")
		store := &Store{
			host:      host,
			scheme:    "http",
			className: "Test",
			tenant:    "my-tenant",
			client:    &http.Client{Timeout: 5 * time.Second},
			documents: map[string]*vector.Document{"x": {ID: "x"}},
			connected: true,
		}

		store.Delete(context.Background(), []string{"x"})

		if !strings.Contains(gotQuery, "tenant=my-tenant") {
			t.Errorf("query = %q, should contain tenant=my-tenant", gotQuery)
		}
	})
}

func TestConcurrentAccess(t *testing.T) {
	server, _ := newMockWeaviateServer()
	defer server.Close()

	store := newTestStore(t, server)
	defer store.Close()

	// 并发添加文档
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			store.Add(context.Background(), []vector.Document{
				{
					ID:        fmt.Sprintf("concurrent-%d", idx),
					Content:   fmt.Sprintf("Doc %d", idx),
					Embedding: makeEmbedding(4, float32(idx)*0.1),
				},
			})
		}(i)
	}
	wg.Wait()

	count, err := store.Count(context.Background())
	if err != nil {
		t.Fatalf("Count 失败: %v", err)
	}
	if count != 10 {
		t.Errorf("并发添加后 count = %d, want 10", count)
	}

	// 并发搜索
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.Search(context.Background(), makeEmbedding(4, 0.5), 3)
		}()
	}
	wg.Wait()
}
