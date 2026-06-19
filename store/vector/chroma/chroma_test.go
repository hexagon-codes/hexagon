package chroma

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/store/vector"
)

// TestNewStore 测试创建存储
func TestNewStore(t *testing.T) {
	// 创建模拟 ChromaDB 服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/heartbeat":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"nanosecond heartbeat": 1234567890}`))
		case "/api/v1/collections/test_collection":
			if r.Method == "GET" {
				// 集合已存在
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{
					"name":  "test_collection",
					"count": 0,
				})
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	// 解析服务器地址
	host, port := parseServerAddress(server.URL)

	t.Run("成功创建", func(t *testing.T) {
		ctx := context.Background()
		store, err := NewStore(ctx,
			WithHost(host),
			WithPort(port),
			WithCollection("test_collection"),
		)

		if err != nil {
			t.Fatalf("NewStore 错误: %v", err)
		}
		defer store.Close()

		if store.collection != "test_collection" {
			t.Errorf("collection = %s, want test_collection", store.collection)
		}
	})

	t.Run("配置选项", func(t *testing.T) {
		ctx := context.Background()
		store, err := NewStore(ctx,
			WithHost(host),
			WithPort(port),
			WithCollection("custom_collection"),
			WithAPIKey("test-key"),
			WithDistanceMetric("l2"),
		)

		if err != nil {
			t.Fatalf("NewStore 错误: %v", err)
		}
		defer store.Close()

		if store.apiKey != "test-key" {
			t.Errorf("apiKey = %s, want test-key", store.apiKey)
		}
		if store.distanceMetric != "l2" {
			t.Errorf("distanceMetric = %s, want l2", store.distanceMetric)
		}
	})
}

// TestStoreAdd 测试添加文档
func TestStoreAdd(t *testing.T) {
	server := createMockServer()
	defer server.Close()

	host, port := parseServerAddress(server.URL)

	ctx := context.Background()
	store, err := NewStore(ctx,
		WithHost(host),
		WithPort(port),
		WithCollection("test"),
	)
	if err != nil {
		t.Fatalf("NewStore 错误: %v", err)
	}
	defer store.Close()

	t.Run("添加单个文档", func(t *testing.T) {
		docs := []vector.Document{
			{
				ID:        "doc1",
				Content:   "test content",
				Embedding: []float32{0.1, 0.2, 0.3},
				Metadata: map[string]any{
					"source": "test",
				},
			},
		}

		err := store.Add(ctx, docs)
		if err != nil {
			t.Errorf("Add 错误: %v", err)
		}
	})

	t.Run("添加多个文档", func(t *testing.T) {
		docs := []vector.Document{
			{ID: "doc2", Content: "content 2", Embedding: []float32{0.1, 0.2}},
			{ID: "doc3", Content: "content 3", Embedding: []float32{0.3, 0.4}},
		}

		err := store.Add(ctx, docs)
		if err != nil {
			t.Errorf("Add 错误: %v", err)
		}
	})
}

// TestStoreSearch 测试搜索
func TestStoreSearch(t *testing.T) {
	server := createMockSearchServer()
	defer server.Close()

	host, port := parseServerAddress(server.URL)

	ctx := context.Background()
	store, err := NewStore(ctx,
		WithHost(host),
		WithPort(port),
		WithCollection("test"),
	)
	if err != nil {
		t.Fatalf("NewStore 错误: %v", err)
	}
	defer store.Close()

	t.Run("基本搜索", func(t *testing.T) {
		embedding := []float32{0.1, 0.2, 0.3}
		docs, err := store.Search(ctx, embedding, 5)

		if err != nil {
			t.Errorf("Search 错误: %v", err)
		}

		// 模拟服务器返回 2 个结果
		if len(docs) != 2 {
			t.Errorf("结果数量 = %d, want 2", len(docs))
		}
	})

	t.Run("带选项搜索", func(t *testing.T) {
		embedding := []float32{0.1, 0.2, 0.3}
		docs, err := store.Search(ctx, embedding, 10,
			vector.WithMinScore(0.5),
			vector.WithFilter(map[string]any{"category": "test"}),
		)

		if err != nil {
			t.Errorf("Search 错误: %v", err)
		}

		// 验证文档分数过滤
		for _, doc := range docs {
			if doc.Score < 0.5 {
				t.Errorf("文档分数 %f < 0.5", doc.Score)
			}
		}
	})
}

// TestStoreGet 测试获取文档
func TestStoreGet(t *testing.T) {
	server := createMockGetServer()
	defer server.Close()

	host, port := parseServerAddress(server.URL)

	ctx := context.Background()
	store, err := NewStore(ctx,
		WithHost(host),
		WithPort(port),
		WithCollection("test"),
	)
	if err != nil {
		t.Fatalf("NewStore 错误: %v", err)
	}
	defer store.Close()

	t.Run("获取存在的文档", func(t *testing.T) {
		doc, err := store.Get(ctx, "doc1")
		if err != nil {
			t.Errorf("Get 错误: %v", err)
		}

		if doc == nil {
			t.Fatal("doc 不应为 nil")
		}

		if doc.ID != "doc1" {
			t.Errorf("ID = %s, want doc1", doc.ID)
		}
	})
}

// TestStoreDelete 测试删除文档
func TestStoreDelete(t *testing.T) {
	server := createMockServer()
	defer server.Close()

	host, port := parseServerAddress(server.URL)

	ctx := context.Background()
	store, err := NewStore(ctx,
		WithHost(host),
		WithPort(port),
		WithCollection("test"),
	)
	if err != nil {
		t.Fatalf("NewStore 错误: %v", err)
	}
	defer store.Close()

	t.Run("删除单个文档", func(t *testing.T) {
		err := store.Delete(ctx, []string{"doc1"})
		if err != nil {
			t.Errorf("Delete 错误: %v", err)
		}
	})

	t.Run("删除多个文档", func(t *testing.T) {
		err := store.Delete(ctx, []string{"doc2", "doc3"})
		if err != nil {
			t.Errorf("Delete 错误: %v", err)
		}
	})
}

// TestStoreUpdate 测试更新文档
func TestStoreUpdate(t *testing.T) {
	server := createMockServer()
	defer server.Close()

	host, port := parseServerAddress(server.URL)

	ctx := context.Background()
	store, err := NewStore(ctx,
		WithHost(host),
		WithPort(port),
		WithCollection("test"),
	)
	if err != nil {
		t.Fatalf("NewStore 错误: %v", err)
	}
	defer store.Close()

	t.Run("更新文档", func(t *testing.T) {
		docs := []vector.Document{
			{
				ID:        "doc1",
				Content:   "updated content",
				Embedding: []float32{0.5, 0.6, 0.7},
			},
		}

		err := store.Update(ctx, docs)
		if err != nil {
			t.Errorf("Update 错误: %v", err)
		}
	})
}

// TestStoreCount 测试统计数量
func TestStoreCount(t *testing.T) {
	server := createMockCountServer()
	defer server.Close()

	host, port := parseServerAddress(server.URL)

	ctx := context.Background()
	store, err := NewStore(ctx,
		WithHost(host),
		WithPort(port),
		WithCollection("test"),
	)
	if err != nil {
		t.Fatalf("NewStore 错误: %v", err)
	}
	defer store.Close()

	t.Run("获取数量", func(t *testing.T) {
		count, err := store.Count(ctx)
		if err != nil {
			t.Errorf("Count 错误: %v", err)
		}

		if count != 42 {
			t.Errorf("count = %d, want 42", count)
		}
	})
}

// TestStoreClear 测试清空
func TestStoreClear(t *testing.T) {
	server := createMockClearServer()
	defer server.Close()

	host, port := parseServerAddress(server.URL)

	ctx := context.Background()
	store, err := NewStore(ctx,
		WithHost(host),
		WithPort(port),
		WithCollection("test"),
	)
	if err != nil {
		t.Fatalf("NewStore 错误: %v", err)
	}
	defer store.Close()

	t.Run("清空集合", func(t *testing.T) {
		err := store.Clear(ctx)
		if err != nil {
			t.Errorf("Clear 错误: %v", err)
		}
	})
}

// TestStoreStats 测试统计信息
func TestStoreStats(t *testing.T) {
	server := createMockCountServer()
	defer server.Close()

	host, port := parseServerAddress(server.URL)

	ctx := context.Background()
	store, err := NewStore(ctx,
		WithHost(host),
		WithPort(port),
		WithCollection("test"),
	)
	if err != nil {
		t.Fatalf("NewStore 错误: %v", err)
	}
	defer store.Close()

	t.Run("获取统计信息", func(t *testing.T) {
		stats, err := store.Stats(ctx)
		if err != nil {
			t.Errorf("Stats 错误: %v", err)
		}

		if stats["collection"] != "test" {
			t.Errorf("collection = %v, want test", stats["collection"])
		}

		if stats["connected"] != true {
			t.Error("connected 应为 true")
		}
	})
}

// TestStoreClose 测试关闭连接
func TestStoreClose(t *testing.T) {
	server := createMockServer()
	defer server.Close()

	host, port := parseServerAddress(server.URL)

	ctx := context.Background()
	store, err := NewStore(ctx,
		WithHost(host),
		WithPort(port),
		WithCollection("test"),
	)
	if err != nil {
		t.Fatalf("NewStore 错误: %v", err)
	}

	t.Run("关闭连接", func(t *testing.T) {
		err := store.Close()
		if err != nil {
			t.Errorf("Close 错误: %v", err)
		}

		// 再次关闭不应报错
		err = store.Close()
		if err != nil {
			t.Errorf("重复 Close 错误: %v", err)
		}
	})

	t.Run("关闭后操作应失败", func(t *testing.T) {
		_, err := store.Get(ctx, "doc1")
		if err == nil {
			t.Error("关闭后 Get 应该返回错误")
		}
	})
}

// TestStoreConnectionFailure 测试连接失败
func TestStoreConnectionFailure(t *testing.T) {
	t.Run("连接不存在的服务器", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, err := NewStore(ctx,
			WithHost("nonexistent.host"),
			WithPort(12345),
		)

		if err == nil {
			t.Error("应该返回连接错误")
		}
	})
}

// ===== 辅助函数 =====

// parseServerAddress 解析测试服务器地址
func parseServerAddress(url string) (string, int) {
	// url 格式: http://127.0.0.1:port
	var host string
	var port int

	// 跳过 http://
	addr := url[7:]
	for i, c := range addr {
		if c == ':' {
			host = addr[:i]
			for _, d := range addr[i+1:] {
				port = port*10 + int(d-'0')
			}
			break
		}
	}

	return host, port
}

// createMockServer 创建基本模拟服务器
func createMockServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/heartbeat":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/collections/test":
			if r.Method == "GET" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{"name": "test"})
			} else if r.Method == "DELETE" {
				w.WriteHeader(http.StatusOK)
			}
		case "/api/v1/collections":
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
}

// createMockSearchServer 创建搜索模拟服务器
func createMockSearchServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/heartbeat":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/collections/test":
			if r.Method == "GET" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{"name": "test"})
			}
		case "/api/v1/collections/test/query":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"ids":        [][]string{{"doc1", "doc2"}},
				"documents":  [][]string{{"content1", "content2"}},
				"metadatas":  [][]map[string]any{{{"key": "value"}, {"key": "value2"}}},
				"embeddings": [][][]float32{{{0.1, 0.2}, {0.3, 0.4}}},
				"distances":  [][]float32{{0.1, 0.2}},
			})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
}

// createMockGetServer 创建 Get 模拟服务器
func createMockGetServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/heartbeat":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/collections/test":
			if r.Method == "GET" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{"name": "test"})
			}
		case "/api/v1/collections/test/get":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"ids":        []string{"doc1"},
				"documents":  []string{"test content"},
				"metadatas":  []map[string]any{{"source": "test"}},
				"embeddings": [][]float32{{0.1, 0.2, 0.3}},
			})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
}

// createMockCountServer 创建 Count 模拟服务器
func createMockCountServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/heartbeat":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/collections/test":
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"name":  "test",
				"count": 42,
			})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
}

// createMockClearServer 创建 Clear 模拟服务器
func createMockClearServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/heartbeat":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/collections/test":
			if r.Method == "GET" {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{"name": "test"})
			} else if r.Method == "DELETE" {
				w.WriteHeader(http.StatusOK)
			}
		case "/api/v1/collections":
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
}
