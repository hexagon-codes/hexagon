package retriever

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/hexagon/testing/mock"
)

// ============== TestHyDERetriever_Basic ==============

// TestHyDERetriever_Basic 测试 HyDE 检索的基本流程：
// LLM 生成假设文档 → 向量化 → 向量检索 → 返回结果
func TestHyDERetriever_Basic(t *testing.T) {
	// 准备 LLM：返回一个假设文档
	llmProvider := mock.FixedProvider("Go 语言使用 goroutine 和 channel 实现并发。")

	// 准备 Embedder：3 维固定向量
	embedder := mock.NewMockEmbedder(3)

	// 准备向量存储：预设搜索结果
	searchResults := []vector.Document{
		{ID: "doc1", Content: "Go 并发编程指南", Score: 0.95, Metadata: map[string]any{"source": "guide.md"}},
		{ID: "doc2", Content: "goroutine 使用详解", Score: 0.88},
	}
	store := mock.NewMockVectorStore(mock.WithSearchResults(searchResults))

	// 创建 HyDE 检索器
	hyde := NewHyDERetriever(llmProvider, embedder, store)

	ctx := context.Background()
	docs, err := hyde.Retrieve(ctx, "Go 的并发模型")
	if err != nil {
		t.Fatalf("Retrieve 失败: %v", err)
	}

	// 验证返回了正确数量的文档
	if len(docs) != 2 {
		t.Fatalf("期望返回 2 个文档，实际返回 %d 个", len(docs))
	}

	// 验证文档内容正确转换
	if docs[0].ID != "doc1" {
		t.Errorf("第一个文档 ID 期望 doc1，实际 %s", docs[0].ID)
	}
	if docs[0].Content != "Go 并发编程指南" {
		t.Errorf("第一个文档内容不匹配: %s", docs[0].Content)
	}
	if docs[0].Score != 0.95 {
		t.Errorf("第一个文档分数期望 0.95，实际 %f", docs[0].Score)
	}

	// 验证元数据被正确传递
	if docs[0].Metadata == nil || docs[0].Metadata["source"] != "guide.md" {
		t.Errorf("第一个文档元数据未正确传递")
	}

	// 验证 LLM 被调用了一次（默认 numHypothetical=1）
	if llmProvider.CallCount() != 1 {
		t.Errorf("LLM 调用次数期望 1，实际 %d", llmProvider.CallCount())
	}

	// 验证 Embedder 被调用了一次
	if embedder.EmbedCallCount() != 1 {
		t.Errorf("Embedder 调用次数期望 1，实际 %d", embedder.EmbedCallCount())
	}

	// 验证向量存储被搜索了一次
	if store.SearchCallCount() != 1 {
		t.Errorf("向量存储搜索次数期望 1，实际 %d", store.SearchCallCount())
	}
}

// ============== TestHyDERetriever_MergeAverage ==============

// TestHyDERetriever_MergeAverage 测试平均向量合并策略
// 多个假设文档的向量取平均值后进行单次检索
func TestHyDERetriever_MergeAverage(t *testing.T) {
	// 准备 LLM：返回多个假设文档
	llmProvider := mock.NewLLMProvider("test")
	llmProvider.AddResponse("假设文档一：Go 并发特性")
	llmProvider.AddResponse("假设文档二：Go channel 通信")

	// 自定义 Embedder，通过调用计数返回不同向量
	embedCallIdx := 0
	embedder := mock.NewMockEmbedder(3, mock.WithEmbedFn(
		func(ctx context.Context, texts []string) ([][]float32, error) {
			results := make([][]float32, len(texts))
			for i := range texts {
				// 根据调用顺序返回不同向量（EmbedOne 每次传入单个文本）
				if embedCallIdx == 0 {
					results[i] = []float32{1.0, 0.0, 0.0}
				} else {
					results[i] = []float32{0.0, 1.0, 0.0}
				}
				embedCallIdx++
			}
			return results, nil
		},
	))

	// 记录搜索时使用的查询向量
	var capturedQuery []float32
	store := mock.NewMockVectorStore(mock.WithSearchFn(
		func(ctx context.Context, query []float32, k int, opts ...vector.SearchOption) ([]vector.Document, error) {
			capturedQuery = make([]float32, len(query))
			copy(capturedQuery, query)
			return []vector.Document{
				{ID: "result1", Content: "合并结果", Score: 0.9},
			}, nil
		},
	))

	// 创建 HyDE 检索器，2 个假设文档，使用平均合并策略
	hyde := NewHyDERetriever(llmProvider, embedder, store,
		WithHyDENumHypothetical(2),
		WithHyDEMergeStrategy(MergeAverage),
	)

	ctx := context.Background()
	docs, err := hyde.Retrieve(ctx, "Go 并发")
	if err != nil {
		t.Fatalf("Retrieve 失败: %v", err)
	}

	if len(docs) != 1 {
		t.Fatalf("期望返回 1 个文档，实际返回 %d 个", len(docs))
	}

	// 验证搜索使用了平均向量: (1+0)/2=0.5, (0+1)/2=0.5, (0+0)/2=0
	if capturedQuery == nil {
		t.Fatal("搜索未被调用")
	}
	if len(capturedQuery) != 3 {
		t.Fatalf("查询向量维度期望 3，实际 %d", len(capturedQuery))
	}

	const epsilon = 0.001
	if math.Abs(float64(capturedQuery[0]-0.5)) > epsilon {
		t.Errorf("平均向量[0] 期望 0.5，实际 %f", capturedQuery[0])
	}
	if math.Abs(float64(capturedQuery[1]-0.5)) > epsilon {
		t.Errorf("平均向量[1] 期望 0.5，实际 %f", capturedQuery[1])
	}
	if math.Abs(float64(capturedQuery[2]-0.0)) > epsilon {
		t.Errorf("平均向量[2] 期望 0.0，实际 %f", capturedQuery[2])
	}

	// 验证向量存储只被搜索了一次（平均策略只搜索一次）
	if store.SearchCallCount() != 1 {
		t.Errorf("平均策略应只搜索一次，实际搜索 %d 次", store.SearchCallCount())
	}
}

// ============== TestHyDERetriever_MergeSearchAll ==============

// TestHyDERetriever_MergeSearchAll 测试逐一检索合并策略
// 每个假设文档分别检索，结果去重后按分数排序
func TestHyDERetriever_MergeSearchAll(t *testing.T) {
	// 准备 LLM：返回多个假设文档
	llmProvider := mock.NewLLMProvider("test")
	llmProvider.AddResponse("假设文档一")
	llmProvider.AddResponse("假设文档二")

	embedder := mock.NewMockEmbedder(3)

	// 自定义搜索：不同查询向量返回不同文档，包含重叠
	callCount := 0
	store := mock.NewMockVectorStore(mock.WithSearchFn(
		func(ctx context.Context, query []float32, k int, opts ...vector.SearchOption) ([]vector.Document, error) {
			callCount++
			if callCount == 1 {
				return []vector.Document{
					{ID: "doc1", Content: "文档1", Score: 0.95},
					{ID: "doc2", Content: "文档2", Score: 0.80},
				}, nil
			}
			// 第二次搜索包含重复的 doc1
			return []vector.Document{
				{ID: "doc1", Content: "文档1", Score: 0.90},
				{ID: "doc3", Content: "文档3", Score: 0.85},
			}, nil
		},
	))

	hyde := NewHyDERetriever(llmProvider, embedder, store,
		WithHyDENumHypothetical(2),
		WithHyDEMergeStrategy(MergeSearchAll),
		WithHyDETopK(5),
	)

	ctx := context.Background()
	docs, err := hyde.Retrieve(ctx, "测试查询")
	if err != nil {
		t.Fatalf("Retrieve 失败: %v", err)
	}

	// 验证去重：doc1 只出现一次（保留第一次出现的）
	if len(docs) != 3 {
		t.Fatalf("期望返回 3 个去重文档，实际返回 %d 个", len(docs))
	}

	// 验证文档 ID 不重复
	seen := make(map[string]bool)
	for _, doc := range docs {
		if seen[doc.ID] {
			t.Errorf("文档 %s 重复出现", doc.ID)
		}
		seen[doc.ID] = true
	}

	// 验证向量存储被搜索了两次（每个假设文档一次）
	if store.SearchCallCount() != 2 {
		t.Errorf("SearchAll 策略应搜索两次，实际搜索 %d 次", store.SearchCallCount())
	}
}

// TestHyDERetriever_MergeSearchAll_TopK 测试 SearchAll 结果超过 TopK 时的截断
func TestHyDERetriever_MergeSearchAll_TopK(t *testing.T) {
	llmProvider := mock.NewLLMProvider("test")
	llmProvider.AddResponse("假设一")
	llmProvider.AddResponse("假设二")

	embedder := mock.NewMockEmbedder(3)

	callCount := 0
	store := mock.NewMockVectorStore(mock.WithSearchFn(
		func(ctx context.Context, query []float32, k int, opts ...vector.SearchOption) ([]vector.Document, error) {
			callCount++
			if callCount == 1 {
				return []vector.Document{
					{ID: "d1", Content: "1", Score: 0.9},
					{ID: "d2", Content: "2", Score: 0.8},
				}, nil
			}
			return []vector.Document{
				{ID: "d3", Content: "3", Score: 0.95},
				{ID: "d4", Content: "4", Score: 0.7},
			}, nil
		},
	))

	// TopK=3，但总共有 4 个不重复文档
	hyde := NewHyDERetriever(llmProvider, embedder, store,
		WithHyDENumHypothetical(2),
		WithHyDEMergeStrategy(MergeSearchAll),
		WithHyDETopK(3),
	)

	ctx := context.Background()
	docs, err := hyde.Retrieve(ctx, "查询")
	if err != nil {
		t.Fatalf("Retrieve 失败: %v", err)
	}

	// 应只返回 TopK=3 个文档
	if len(docs) != 3 {
		t.Fatalf("期望返回 3 个文档（TopK），实际返回 %d 个", len(docs))
	}

	// 验证按分数降序排列（取前 3 个高分）
	if docs[0].Score < docs[1].Score || docs[1].Score < docs[2].Score {
		t.Errorf("文档未按分数降序排列: %.2f, %.2f, %.2f", docs[0].Score, docs[1].Score, docs[2].Score)
	}
}

// ============== TestHyDERetriever_LLMFallback ==============

// TestHyDERetriever_LLMFallback 测试 LLM 失败时的降级检索
// 当 LLM 调用失败时，应直接用原始查询向量进行检索
func TestHyDERetriever_LLMFallback(t *testing.T) {
	// LLM 总是返回错误
	llmProvider := mock.ErrorProvider(errors.New("LLM 服务不可用"))

	embedder := mock.NewMockEmbedder(3)

	searchResults := []vector.Document{
		{ID: "fallback1", Content: "降级检索结果", Score: 0.75},
	}
	store := mock.NewMockVectorStore(mock.WithSearchResults(searchResults))

	hyde := NewHyDERetriever(llmProvider, embedder, store)

	ctx := context.Background()
	docs, err := hyde.Retrieve(ctx, "测试降级")
	if err != nil {
		t.Fatalf("降级检索不应失败: %v", err)
	}

	// 验证降级成功，返回了结果
	if len(docs) != 1 {
		t.Fatalf("降级检索期望返回 1 个文档，实际返回 %d 个", len(docs))
	}
	if docs[0].ID != "fallback1" {
		t.Errorf("降级检索文档 ID 期望 fallback1，实际 %s", docs[0].ID)
	}

	// 验证 Embedder 被调用了一次（对原始查询向量化）
	if embedder.EmbedCallCount() != 1 {
		t.Errorf("降级时 Embedder 调用次数期望 1，实际 %d", embedder.EmbedCallCount())
	}
}

// TestHyDERetriever_LLMFallback_EmbedFail 测试 LLM 和 Embedder 同时失败的情况
func TestHyDERetriever_LLMFallback_EmbedFail(t *testing.T) {
	llmProvider := mock.ErrorProvider(errors.New("LLM 服务不可用"))
	embedder := mock.NewMockEmbedder(3, mock.WithEmbedError(errors.New("Embedder 失败")))
	store := mock.NewMockVectorStore()

	hyde := NewHyDERetriever(llmProvider, embedder, store)

	ctx := context.Background()
	_, err := hyde.Retrieve(ctx, "双重失败")
	if err == nil {
		t.Fatal("LLM 和 Embedder 同时失败时应返回错误")
	}
}

// ============== TestHyDERetriever_MultipleHypothetical ==============

// TestHyDERetriever_MultipleHypothetical 测试生成多个假设文档
func TestHyDERetriever_MultipleHypothetical(t *testing.T) {
	// 准备 LLM：返回 3 个假设文档
	llmProvider := mock.NewLLMProvider("test")
	llmProvider.AddResponse("假设文档一：关于 Go 并发")
	llmProvider.AddResponse("假设文档二：关于 goroutine")
	llmProvider.AddResponse("假设文档三：关于 channel")

	embedder := mock.NewMockEmbedder(3)

	searchResults := []vector.Document{
		{ID: "r1", Content: "结果", Score: 0.9},
	}
	store := mock.NewMockVectorStore(mock.WithSearchResults(searchResults))

	hyde := NewHyDERetriever(llmProvider, embedder, store,
		WithHyDENumHypothetical(3),
		WithHyDEMergeStrategy(MergeAverage),
	)

	ctx := context.Background()
	docs, err := hyde.Retrieve(ctx, "Go 并发")
	if err != nil {
		t.Fatalf("Retrieve 失败: %v", err)
	}

	// 验证 LLM 被调用了 3 次
	if llmProvider.CallCount() != 3 {
		t.Errorf("LLM 调用次数期望 3，实际 %d", llmProvider.CallCount())
	}

	// 验证 Embedder 被调用了 3 次（每个假设文档向量化一次）
	if embedder.EmbedCallCount() != 3 {
		t.Errorf("Embedder 调用次数期望 3，实际 %d", embedder.EmbedCallCount())
	}

	// 验证返回了结果
	if len(docs) != 1 {
		t.Fatalf("期望返回 1 个文档，实际返回 %d 个", len(docs))
	}
}

// ============== TestHyDERetriever_Options ==============

// TestHyDERetriever_Options 测试各种选项函数是否正确设置
func TestHyDERetriever_Options(t *testing.T) {
	llmProvider := mock.FixedProvider("test")
	embedder := mock.NewMockEmbedder(3)
	store := mock.NewMockVectorStore()

	t.Run("默认值", func(t *testing.T) {
		hyde := NewHyDERetriever(llmProvider, embedder, store)
		if hyde.numHypothetical != 1 {
			t.Errorf("默认 numHypothetical 期望 1，实际 %d", hyde.numHypothetical)
		}
		if hyde.topK != 5 {
			t.Errorf("默认 topK 期望 5，实际 %d", hyde.topK)
		}
		if hyde.mergeStrategy != MergeAverage {
			t.Errorf("默认 mergeStrategy 期望 MergeAverage，实际 %d", hyde.mergeStrategy)
		}
		if hyde.temperature != 0.7 {
			t.Errorf("默认 temperature 期望 0.7，实际 %f", hyde.temperature)
		}
		if hyde.promptTemplate != defaultHyDEPrompt {
			t.Errorf("默认 promptTemplate 不正确")
		}
	})

	t.Run("WithHyDEModel", func(t *testing.T) {
		hyde := NewHyDERetriever(llmProvider, embedder, store, WithHyDEModel("gpt-4"))
		if hyde.model != "gpt-4" {
			t.Errorf("model 期望 gpt-4，实际 %s", hyde.model)
		}
	})

	t.Run("WithHyDEPrompt", func(t *testing.T) {
		customPrompt := "请根据问题 %s 生成文档"
		hyde := NewHyDERetriever(llmProvider, embedder, store, WithHyDEPrompt(customPrompt))
		if hyde.promptTemplate != customPrompt {
			t.Errorf("promptTemplate 未正确设置")
		}
	})

	t.Run("WithHyDENumHypothetical", func(t *testing.T) {
		hyde := NewHyDERetriever(llmProvider, embedder, store, WithHyDENumHypothetical(5))
		if hyde.numHypothetical != 5 {
			t.Errorf("numHypothetical 期望 5，实际 %d", hyde.numHypothetical)
		}
	})

	t.Run("WithHyDENumHypothetical_忽略非法值", func(t *testing.T) {
		hyde := NewHyDERetriever(llmProvider, embedder, store, WithHyDENumHypothetical(0))
		if hyde.numHypothetical != 1 {
			t.Errorf("numHypothetical 应忽略 0，保持默认值 1，实际 %d", hyde.numHypothetical)
		}
		hyde = NewHyDERetriever(llmProvider, embedder, store, WithHyDENumHypothetical(-1))
		if hyde.numHypothetical != 1 {
			t.Errorf("numHypothetical 应忽略负数，保持默认值 1，实际 %d", hyde.numHypothetical)
		}
	})

	t.Run("WithHyDETopK", func(t *testing.T) {
		hyde := NewHyDERetriever(llmProvider, embedder, store, WithHyDETopK(20))
		if hyde.topK != 20 {
			t.Errorf("topK 期望 20，实际 %d", hyde.topK)
		}
	})

	t.Run("WithHyDETopK_忽略非法值", func(t *testing.T) {
		hyde := NewHyDERetriever(llmProvider, embedder, store, WithHyDETopK(0))
		if hyde.topK != 5 {
			t.Errorf("topK 应忽略 0，保持默认值 5，实际 %d", hyde.topK)
		}
	})

	t.Run("WithHyDEMergeStrategy", func(t *testing.T) {
		hyde := NewHyDERetriever(llmProvider, embedder, store, WithHyDEMergeStrategy(MergeSearchAll))
		if hyde.mergeStrategy != MergeSearchAll {
			t.Errorf("mergeStrategy 期望 MergeSearchAll，实际 %d", hyde.mergeStrategy)
		}
	})

	t.Run("WithHyDETemperature", func(t *testing.T) {
		hyde := NewHyDERetriever(llmProvider, embedder, store, WithHyDETemperature(0.9))
		if hyde.temperature != 0.9 {
			t.Errorf("temperature 期望 0.9，实际 %f", hyde.temperature)
		}
	})
}

// ============== TestAverageVectors ==============

// TestAverageVectors 测试向量平均值计算
func TestAverageVectors(t *testing.T) {
	const epsilon = 0.0001

	t.Run("空向量列表", func(t *testing.T) {
		result := averageVectors(nil)
		if result != nil {
			t.Errorf("空输入应返回 nil，实际返回 %v", result)
		}
		result = averageVectors([][]float32{})
		if result != nil {
			t.Errorf("空切片应返回 nil，实际返回 %v", result)
		}
	})

	t.Run("单个向量", func(t *testing.T) {
		input := [][]float32{{1.0, 2.0, 3.0}}
		result := averageVectors(input)
		if len(result) != 3 {
			t.Fatalf("结果维度期望 3，实际 %d", len(result))
		}
		expected := []float32{1.0, 2.0, 3.0}
		for i, v := range expected {
			if math.Abs(float64(result[i]-v)) > epsilon {
				t.Errorf("result[%d] 期望 %f，实际 %f", i, v, result[i])
			}
		}
	})

	t.Run("多个向量取平均", func(t *testing.T) {
		input := [][]float32{
			{1.0, 0.0, 3.0},
			{3.0, 2.0, 1.0},
		}
		result := averageVectors(input)
		if len(result) != 3 {
			t.Fatalf("结果维度期望 3，实际 %d", len(result))
		}
		// 平均值: (1+3)/2=2.0, (0+2)/2=1.0, (3+1)/2=2.0
		expected := []float32{2.0, 1.0, 2.0}
		for i, v := range expected {
			if math.Abs(float64(result[i]-v)) > epsilon {
				t.Errorf("result[%d] 期望 %f，实际 %f", i, v, result[i])
			}
		}
	})

	t.Run("三个向量取平均", func(t *testing.T) {
		input := [][]float32{
			{3.0, 6.0},
			{0.0, 3.0},
			{6.0, 0.0},
		}
		result := averageVectors(input)
		if len(result) != 2 {
			t.Fatalf("结果维度期望 2，实际 %d", len(result))
		}
		// 平均值: (3+0+6)/3=3.0, (6+3+0)/3=3.0
		expected := []float32{3.0, 3.0}
		for i, v := range expected {
			if math.Abs(float64(result[i]-v)) > epsilon {
				t.Errorf("result[%d] 期望 %f，实际 %f", i, v, result[i])
			}
		}
	})

	t.Run("不等长向量", func(t *testing.T) {
		// averageVectors 以第一个向量的维度为基准
		input := [][]float32{
			{1.0, 2.0, 3.0},
			{4.0, 5.0},
		}
		result := averageVectors(input)
		if len(result) != 3 {
			t.Fatalf("结果维度应以第一个向量为准，期望 3，实际 %d", len(result))
		}
		// 第二个向量只有 2 维，第 3 维只计算第一个向量的值
		// (1+4)/2=2.5, (2+5)/2=3.5, (3+0)/2=1.5
		expected := []float32{2.5, 3.5, 1.5}
		for i, v := range expected {
			if math.Abs(float64(result[i]-v)) > epsilon {
				t.Errorf("result[%d] 期望 %f，实际 %f", i, v, result[i])
			}
		}
	})
}

// ============== TestConvertVectorDocs ==============

// TestConvertVectorDocs 测试 vector.Document 到 rag.Document 的转换
func TestConvertVectorDocs(t *testing.T) {
	t.Run("空文档列表", func(t *testing.T) {
		result := convertVectorDocs(nil)
		if len(result) != 0 {
			t.Errorf("空输入应返回空切片，实际返回 %d 个文档", len(result))
		}
	})

	t.Run("单个文档转换", func(t *testing.T) {
		now := time.Now()
		vectorDocs := []vector.Document{
			{
				ID:        "vec-doc-1",
				Content:   "测试文档内容",
				Embedding: []float32{0.1, 0.2, 0.3},
				Metadata:  map[string]any{"key": "value", "num": 42},
				Score:     0.88,
				CreatedAt: now,
			},
		}

		result := convertVectorDocs(vectorDocs)
		if len(result) != 1 {
			t.Fatalf("期望返回 1 个文档，实际返回 %d 个", len(result))
		}

		doc := result[0]
		if doc.ID != "vec-doc-1" {
			t.Errorf("ID 期望 vec-doc-1，实际 %s", doc.ID)
		}
		if doc.Content != "测试文档内容" {
			t.Errorf("Content 不匹配: %s", doc.Content)
		}
		if doc.Score != 0.88 {
			t.Errorf("Score 期望 0.88，实际 %f", doc.Score)
		}
		if len(doc.Embedding) != 3 {
			t.Errorf("Embedding 维度期望 3，实际 %d", len(doc.Embedding))
		}
		if doc.Metadata["key"] != "value" {
			t.Errorf("Metadata[key] 期望 value，实际 %v", doc.Metadata["key"])
		}
		if doc.Metadata["num"] != 42 {
			t.Errorf("Metadata[num] 期望 42，实际 %v", doc.Metadata["num"])
		}
		if !doc.CreatedAt.Equal(now) {
			t.Errorf("CreatedAt 不匹配")
		}
	})

	t.Run("多个文档转换", func(t *testing.T) {
		vectorDocs := []vector.Document{
			{ID: "a", Content: "文档A", Score: 0.9},
			{ID: "b", Content: "文档B", Score: 0.8},
			{ID: "c", Content: "文档C", Score: 0.7},
		}

		result := convertVectorDocs(vectorDocs)
		if len(result) != 3 {
			t.Fatalf("期望返回 3 个文档，实际返回 %d 个", len(result))
		}

		// 验证顺序和内容保持一致
		for i, doc := range result {
			expected := vectorDocs[i]
			if doc.ID != expected.ID {
				t.Errorf("文档[%d] ID 期望 %s，实际 %s", i, expected.ID, doc.ID)
			}
			if doc.Content != expected.Content {
				t.Errorf("文档[%d] Content 期望 %s，实际 %s", i, expected.Content, doc.Content)
			}
			if doc.Score != expected.Score {
				t.Errorf("文档[%d] Score 期望 %f，实际 %f", i, expected.Score, doc.Score)
			}
		}
	})
}

// ============== 补充场景测试 ==============

// TestHyDERetriever_ImplementsRetriever 验证 HyDERetriever 实现了 rag.Retriever 接口
func TestHyDERetriever_ImplementsRetriever(t *testing.T) {
	llmProvider := mock.FixedProvider("test")
	embedder := mock.NewMockEmbedder(3)
	store := mock.NewMockVectorStore()

	var _ rag.Retriever = NewHyDERetriever(llmProvider, embedder, store)
}
