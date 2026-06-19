package retriever

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/hexagon/testing/mock"
)

// TestVectorRetriever_Basic 测试基本的向量检索功能
// 验证检索器能正确调用 embedder 和 store，并将 vector.Document 转换为 rag.Document
func TestVectorRetriever_Basic(t *testing.T) {
	now := time.Now()

	// 准备搜索结果
	searchDocs := []vector.Document{
		{
			ID:        "doc-1",
			Content:   "Go 是一门编程语言",
			Embedding: []float32{0.1, 0.2, 0.3},
			Metadata:  map[string]any{"source": "wiki"},
			Score:     0.95,
			CreatedAt: now,
		},
		{
			ID:        "doc-2",
			Content:   "Rust 也是一门编程语言",
			Embedding: []float32{0.4, 0.5, 0.6},
			Metadata:  map[string]any{"source": "blog"},
			Score:     0.85,
			CreatedAt: now,
		},
	}

	store := mock.NewMockVectorStore(mock.WithSearchResults(searchDocs))
	embedder := mock.NewMockEmbedder(3)

	r := NewVectorRetriever(store, embedder)
	ctx := context.Background()

	results, err := r.Retrieve(ctx, "什么是编程语言")
	if err != nil {
		t.Fatalf("Retrieve 失败: %v", err)
	}

	// 验证返回文档数量
	if len(results) != 2 {
		t.Fatalf("期望返回 2 个文档，实际返回 %d 个", len(results))
	}

	// 验证文档字段正确转换
	if results[0].ID != "doc-1" {
		t.Errorf("第一个文档 ID 期望 doc-1，实际 %s", results[0].ID)
	}
	if results[0].Content != "Go 是一门编程语言" {
		t.Errorf("第一个文档 Content 不匹配: %s", results[0].Content)
	}
	if results[0].Score != 0.95 {
		t.Errorf("第一个文档 Score 期望 0.95，实际 %f", results[0].Score)
	}
	if results[0].Metadata["source"] != "wiki" {
		t.Errorf("第一个文档 Metadata 不匹配: %v", results[0].Metadata)
	}
	if !results[0].CreatedAt.Equal(now) {
		t.Errorf("第一个文档 CreatedAt 不匹配")
	}

	// 验证 Embedding 字段被正确传递
	if len(results[0].Embedding) != 3 {
		t.Errorf("第一个文档 Embedding 维度期望 3，实际 %d", len(results[0].Embedding))
	}

	// 验证 embedder 被调用了一次（用于生成查询向量）
	if embedder.EmbedCallCount() != 1 {
		t.Errorf("Embedder 期望被调用 1 次，实际 %d 次", embedder.EmbedCallCount())
	}

	// 验证 store 的 Search 被调用了一次
	if store.SearchCallCount() != 1 {
		t.Errorf("Store.Search 期望被调用 1 次，实际 %d 次", store.SearchCallCount())
	}
}

// TestVectorRetriever_TopK 测试 TopK 限制
// 验证通过构造选项和检索选项两种方式设置 TopK
func TestVectorRetriever_TopK(t *testing.T) {
	// 准备 10 个搜索结果
	searchDocs := make([]vector.Document, 10)
	for i := 0; i < 10; i++ {
		searchDocs[i] = vector.Document{
			ID:      fmt.Sprintf("doc-%d", i),
			Content: fmt.Sprintf("文档内容 %d", i),
			Score:   float32(10-i) * 0.1,
		}
	}

	store := mock.NewMockVectorStore(mock.WithSearchResults(searchDocs))
	embedder := mock.NewMockEmbedder(3)

	// 测试通过构造选项设置 TopK
	t.Run("通过构造选项设置TopK", func(t *testing.T) {
		r := NewVectorRetriever(store, embedder, WithTopK(3))
		ctx := context.Background()

		results, err := r.Retrieve(ctx, "测试查询")
		if err != nil {
			t.Fatalf("Retrieve 失败: %v", err)
		}

		// store mock 会根据 k 裁剪结果，验证 store 被传入正确的 k
		calls := store.SearchCalls()
		lastCall := calls[len(calls)-1]
		if lastCall.K != 3 {
			t.Errorf("传给 Store.Search 的 TopK 期望 3，实际 %d", lastCall.K)
		}

		// 结果不应超过 3 个
		if len(results) > 3 {
			t.Errorf("结果数量期望不超过 3，实际 %d", len(results))
		}
	})

	// 测试通过检索选项覆盖 TopK
	t.Run("通过检索选项覆盖TopK", func(t *testing.T) {
		r := NewVectorRetriever(store, embedder, WithTopK(10))
		ctx := context.Background()

		results, err := r.Retrieve(ctx, "测试查询", rag.WithTopK(2))
		if err != nil {
			t.Fatalf("Retrieve 失败: %v", err)
		}

		// 检索选项应覆盖构造选项
		calls := store.SearchCalls()
		lastCall := calls[len(calls)-1]
		if lastCall.K != 2 {
			t.Errorf("检索选项覆盖后，TopK 期望 2，实际 %d", lastCall.K)
		}

		if len(results) > 2 {
			t.Errorf("结果数量期望不超过 2，实际 %d", len(results))
		}
	})

	// 测试默认 TopK
	t.Run("默认TopK为5", func(t *testing.T) {
		r := NewVectorRetriever(store, embedder)
		ctx := context.Background()

		_, err := r.Retrieve(ctx, "测试查询")
		if err != nil {
			t.Fatalf("Retrieve 失败: %v", err)
		}

		calls := store.SearchCalls()
		lastCall := calls[len(calls)-1]
		if lastCall.K != 5 {
			t.Errorf("默认 TopK 期望 5，实际 %d", lastCall.K)
		}
	})
}

// TestVectorRetriever_MinScore 测试最小分数过滤
// 验证 MinScore 选项能正确传递给向量存储
func TestVectorRetriever_MinScore(t *testing.T) {
	// 注意：MinScore 过滤由 vector.Store 的 SearchOption 控制
	// 此处验证 MinScore 被正确传递给 store

	// 使用自定义搜索函数来验证 MinScore 是否被传递
	var capturedOpts []vector.SearchOption
	store := mock.NewMockVectorStore(mock.WithSearchFn(
		func(ctx context.Context, query []float32, k int, opts ...vector.SearchOption) ([]vector.Document, error) {
			capturedOpts = opts
			return []vector.Document{
				{ID: "doc-1", Content: "高分文档", Score: 0.9},
			}, nil
		},
	))
	embedder := mock.NewMockEmbedder(3)

	// 测试通过构造选项设置 MinScore
	t.Run("通过构造选项设置MinScore", func(t *testing.T) {
		r := NewVectorRetriever(store, embedder, WithMinScore(0.5))
		ctx := context.Background()

		results, err := r.Retrieve(ctx, "查询")
		if err != nil {
			t.Fatalf("Retrieve 失败: %v", err)
		}

		// 验证搜索选项中包含了 MinScore 选项
		if len(capturedOpts) == 0 {
			t.Error("期望传递搜索选项给 Store.Search")
		}

		if len(results) != 1 {
			t.Errorf("期望返回 1 个文档，实际 %d", len(results))
		}
	})

	// 测试通过检索选项覆盖 MinScore
	t.Run("通过检索选项覆盖MinScore", func(t *testing.T) {
		r := NewVectorRetriever(store, embedder, WithMinScore(0.3))
		ctx := context.Background()

		_, err := r.Retrieve(ctx, "查询", rag.WithMinScore(0.7))
		if err != nil {
			t.Fatalf("Retrieve 失败: %v", err)
		}

		// 搜索选项已被传递
		if len(capturedOpts) == 0 {
			t.Error("期望传递搜索选项给 Store.Search")
		}
	})
}

// TestVectorRetriever_WithFilter 测试元数据过滤
// 验证 Filter 条件能正确传递给向量存储进行过滤
func TestVectorRetriever_WithFilter(t *testing.T) {
	// 使用自定义搜索函数验证 filter 被正确传递
	var capturedOpts []vector.SearchOption
	store := mock.NewMockVectorStore(mock.WithSearchFn(
		func(ctx context.Context, query []float32, k int, opts ...vector.SearchOption) ([]vector.Document, error) {
			capturedOpts = opts
			return []vector.Document{
				{
					ID:       "doc-1",
					Content:  "过滤后的文档",
					Metadata: map[string]any{"category": "tech"},
					Score:    0.9,
				},
			}, nil
		},
	))
	embedder := mock.NewMockEmbedder(3)

	r := NewVectorRetriever(store, embedder)
	ctx := context.Background()

	filter := map[string]any{"category": "tech"}
	results, err := r.Retrieve(ctx, "技术文章", rag.WithFilter(filter))
	if err != nil {
		t.Fatalf("Retrieve 失败: %v", err)
	}

	// 验证返回了过滤后的结果
	if len(results) != 1 {
		t.Fatalf("期望返回 1 个文档，实际 %d", len(results))
	}
	if results[0].ID != "doc-1" {
		t.Errorf("文档 ID 期望 doc-1，实际 %s", results[0].ID)
	}
	if results[0].Metadata["category"] != "tech" {
		t.Errorf("文档 Metadata 中 category 期望 tech，实际 %v", results[0].Metadata["category"])
	}

	// 验证 filter 搜索选项被传递（至少包含 MinScore、Metadata 和 Filter 选项）
	// 有 filter 时，选项数应为 3：WithMinScore + WithMetadata + WithFilter
	if len(capturedOpts) != 3 {
		t.Errorf("期望传递 3 个搜索选项（含 Filter），实际 %d", len(capturedOpts))
	}
}

// TestVectorRetriever_WithoutFilter 测试不带 Filter 时的选项数量
func TestVectorRetriever_WithoutFilter(t *testing.T) {
	var capturedOpts []vector.SearchOption
	store := mock.NewMockVectorStore(mock.WithSearchFn(
		func(ctx context.Context, query []float32, k int, opts ...vector.SearchOption) ([]vector.Document, error) {
			capturedOpts = opts
			return nil, nil
		},
	))
	embedder := mock.NewMockEmbedder(3)

	r := NewVectorRetriever(store, embedder)
	ctx := context.Background()

	// 不传 filter
	_, err := r.Retrieve(ctx, "查询")
	if err != nil {
		t.Fatalf("Retrieve 失败: %v", err)
	}

	// 没有 filter 时，选项数应为 2：WithMinScore + WithMetadata
	if len(capturedOpts) != 2 {
		t.Errorf("期望传递 2 个搜索选项（无 Filter），实际 %d", len(capturedOpts))
	}
}

// TestVectorRetriever_EmbedError 测试向量生成失败的情况
// 验证 embedder 返回错误时，检索器能正确传播错误
func TestVectorRetriever_EmbedError(t *testing.T) {
	embedErr := errors.New("embed 服务不可用")
	store := mock.NewMockVectorStore()
	embedder := mock.NewMockEmbedder(3, mock.WithEmbedError(embedErr))

	r := NewVectorRetriever(store, embedder)
	ctx := context.Background()

	results, err := r.Retrieve(ctx, "查询")

	// 应该返回错误
	if err == nil {
		t.Fatal("期望返回错误，但未返回")
	}
	if !errors.Is(err, embedErr) {
		t.Errorf("期望错误为 embed 错误，实际: %v", err)
	}

	// 结果应为 nil
	if results != nil {
		t.Errorf("出错时期望返回 nil 结果，实际: %v", results)
	}

	// embedder 被调用了，但 store 不应被调用
	if embedder.EmbedCallCount() != 1 {
		t.Errorf("Embedder 期望被调用 1 次，实际 %d 次", embedder.EmbedCallCount())
	}
	if store.SearchCallCount() != 0 {
		t.Errorf("Store.Search 不应被调用，实际被调用 %d 次", store.SearchCallCount())
	}
}

// TestVectorRetriever_SearchError 测试向量搜索失败的情况
// 验证 store.Search 返回错误时，检索器能正确传播错误
func TestVectorRetriever_SearchError(t *testing.T) {
	searchErr := errors.New("向量存储连接失败")
	store := mock.NewMockVectorStore(mock.WithSearchError(searchErr))
	embedder := mock.NewMockEmbedder(3)

	r := NewVectorRetriever(store, embedder)
	ctx := context.Background()

	results, err := r.Retrieve(ctx, "查询")

	// 应该返回错误
	if err == nil {
		t.Fatal("期望返回错误，但未返回")
	}
	if !errors.Is(err, searchErr) {
		t.Errorf("期望错误为搜索错误，实际: %v", err)
	}

	// 结果应为 nil
	if results != nil {
		t.Errorf("出错时期望返回 nil 结果，实际: %v", results)
	}

	// embedder 和 store 都应该被调用
	if embedder.EmbedCallCount() != 1 {
		t.Errorf("Embedder 期望被调用 1 次，实际 %d 次", embedder.EmbedCallCount())
	}
	if store.SearchCallCount() != 1 {
		t.Errorf("Store.Search 期望被调用 1 次，实际 %d 次", store.SearchCallCount())
	}
}

// TestVectorRetriever_Options 测试各选项的正确性
// 验证 WithTopK 和 WithMinScore 选项能正确设置 VectorRetriever 的内部状态
func TestVectorRetriever_Options(t *testing.T) {
	store := mock.NewMockVectorStore(mock.WithSearchResults([]vector.Document{}))
	embedder := mock.NewMockEmbedder(3)

	// 测试默认值
	t.Run("默认值验证", func(t *testing.T) {
		r := NewVectorRetriever(store, embedder)

		// 默认 TopK 应为 5
		if r.topK != 5 {
			t.Errorf("默认 topK 期望 5，实际 %d", r.topK)
		}

		// 默认 MinScore 应为 0.0
		if r.minScore != 0.0 {
			t.Errorf("默认 minScore 期望 0.0，实际 %f", r.minScore)
		}
	})

	// 测试自定义选项
	t.Run("自定义选项", func(t *testing.T) {
		r := NewVectorRetriever(store, embedder,
			WithTopK(10),
			WithMinScore(0.8),
		)

		if r.topK != 10 {
			t.Errorf("topK 期望 10，实际 %d", r.topK)
		}
		if r.minScore != 0.8 {
			t.Errorf("minScore 期望 0.8，实际 %f", r.minScore)
		}
	})

	// 测试多次设置选项，后设置的覆盖前面的
	t.Run("选项覆盖", func(t *testing.T) {
		r := NewVectorRetriever(store, embedder,
			WithTopK(3),
			WithTopK(7),
		)

		if r.topK != 7 {
			t.Errorf("后设置的 topK 应覆盖前面的，期望 7，实际 %d", r.topK)
		}
	})

	// 测试接口兼容性：VectorRetriever 实现了 rag.Retriever
	t.Run("实现Retriever接口", func(t *testing.T) {
		r := NewVectorRetriever(store, embedder)
		var _ rag.Retriever = r // 编译时检查
	})

	// 测试空结果
	t.Run("空搜索结果", func(t *testing.T) {
		emptyStore := mock.NewMockVectorStore(mock.WithSearchResults([]vector.Document{}))
		r := NewVectorRetriever(emptyStore, embedder)
		ctx := context.Background()

		results, err := r.Retrieve(ctx, "不存在的内容")
		if err != nil {
			t.Fatalf("Retrieve 失败: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("期望空结果，实际返回 %d 个文档", len(results))
		}
	})
}
