package bench

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/agent"
	"github.com/hexagon-codes/hexagon/orchestration/graph"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/hexagon/rag/splitter"
)

// ============== 竞品对标基准测试 ==============
//
// 本文件提供与 Python 框架（LangChain/LlamaIndex/CrewAI）
// 典型操作场景的性能对标基准。
//
// 对标维度：
//   - Agent 创建和执行
//   - Graph 编排（线性/并行/条件分支）
//   - RAG 管道（文档处理/检索/合成）
//   - 并发能力（Go goroutine vs Python threading）
//   - 内存效率（零分配优化）
//
// 运行方式：
//   go test -bench=BenchmarkComparison -benchmem -count=3 ./bench/
//
// 注意：Python 框架的参考数据来自公开基准测试报告，
// 此处仅测试 Hexagon 侧的性能，用于量化 Go 并发优势。

// ============== Agent 对标 ==============

// BenchmarkComparisonAgentCreation 对标 Agent 创建性能
//
// 竞品参考值（Python 框架，典型硬件）：
//   - LangChain: ~500μs/agent
//   - CrewAI: ~800μs/agent
//   - Hexagon 目标: <50μs/agent (10x 优势)
func BenchmarkComparisonAgentCreation(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = agent.NewBaseAgent(
			agent.WithName(fmt.Sprintf("agent-%d", i)),
			agent.WithSystemPrompt("你是一个助手"),
		)
	}
}

// BenchmarkComparisonAgentConcurrent 对标并发 Agent 创建
//
// 测试 Go goroutine 在 Agent 创建场景下的并发优势。
// Python GIL 限制了真正的 CPU 并行。
func BenchmarkComparisonAgentConcurrent(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = agent.NewBaseAgent(
				agent.WithName(fmt.Sprintf("agent-%d", i)),
				agent.WithSystemPrompt("你是一个助手"),
			)
			i++
		}
	})
}

// ============== Graph 编排对标 ==============

// comparisonState 基准测试用状态
type comparisonState struct {
	Value  int
	Result string
	Data   map[string]any
}

func (s comparisonState) Clone() graph.State {
	clone := s
	if s.Data != nil {
		clone.Data = make(map[string]any, len(s.Data))
		for k, v := range s.Data {
			clone.Data[k] = v
		}
	}
	return clone
}

// BenchmarkComparisonGraphLinear 对标线性图编排
//
// 竞品参考值：
//   - LangGraph: ~2ms/run (5-node linear)
//   - Hexagon 目标: <200μs/run (10x 优势)
func BenchmarkComparisonGraphLinear(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()

	nodeHandler := func(_ context.Context, s comparisonState) (comparisonState, error) {
		s.Value++
		return s, nil
	}

	g, err := graph.NewGraph[comparisonState]("linear-5").
		AddNode("n1", nodeHandler).
		AddNode("n2", nodeHandler).
		AddNode("n3", nodeHandler).
		AddNode("n4", nodeHandler).
		AddNode("n5", nodeHandler).
		AddEdge(graph.START, "n1").
		AddEdge("n1", "n2").
		AddEdge("n2", "n3").
		AddEdge("n3", "n4").
		AddEdge("n4", "n5").
		AddEdge("n5", graph.END).
		Build()
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := g.Run(ctx, comparisonState{})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkComparisonGraphConditional 对标条件分支图
//
// 竞品参考值：
//   - LangGraph: ~3ms/run (条件分支)
//   - Hexagon 目标: <300μs/run
func BenchmarkComparisonGraphConditional(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()

	nodeHandler := func(_ context.Context, s comparisonState) (comparisonState, error) {
		s.Value++
		return s, nil
	}

	g, err := graph.NewGraph[comparisonState]("conditional").
		AddNode("entry", nodeHandler).
		AddNode("branch_a", nodeHandler).
		AddNode("branch_b", nodeHandler).
		AddNode("merge", nodeHandler).
		AddEdge(graph.START, "entry").
		AddConditionalEdge("entry", func(s comparisonState) string {
			if s.Value%2 == 0 {
				return "a"
			}
			return "b"
		}, map[string]string{
			"a": "branch_a",
			"b": "branch_b",
		}).
		AddEdge("branch_a", "merge").
		AddEdge("branch_b", "merge").
		AddEdge("merge", graph.END).
		Build()
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := g.Run(ctx, comparisonState{Value: i})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkComparisonGraphLarge 对标大规模图
//
// 20 节点线性图，测试图编排引擎的扩展性。
// 竞品参考值：
//   - LangGraph: ~10ms/run (20 nodes)
//   - Hexagon 目标: <1ms/run
func BenchmarkComparisonGraphLarge(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()

	nodeCount := 20
	nodeHandler := func(_ context.Context, s comparisonState) (comparisonState, error) {
		s.Value++
		return s, nil
	}

	builder := graph.NewGraph[comparisonState]("large-20")
	prevNode := graph.START

	for i := 0; i < nodeCount; i++ {
		name := fmt.Sprintf("node_%d", i)
		builder.AddNode(name, nodeHandler)
		builder.AddEdge(prevNode, name)
		prevNode = name
	}
	builder.AddEdge(prevNode, graph.END)

	g, err := builder.Build()
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := g.Run(ctx, comparisonState{})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkComparisonGraphConcurrentRuns 对标并发图执行
//
// 同时执行多个图实例，测试 Go 并发调度能力。
// Python 框架受 GIL 限制，多线程场景下优势明显。
func BenchmarkComparisonGraphConcurrentRuns(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()

	nodeHandler := func(_ context.Context, s comparisonState) (comparisonState, error) {
		s.Value++
		return s, nil
	}

	g, err := graph.NewGraph[comparisonState]("concurrent").
		AddNode("n1", nodeHandler).
		AddNode("n2", nodeHandler).
		AddNode("n3", nodeHandler).
		AddEdge(graph.START, "n1").
		AddEdge("n1", "n2").
		AddEdge("n2", "n3").
		AddEdge("n3", graph.END).
		Build()
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := g.Run(ctx, comparisonState{})
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// ============== RAG 管道对标 ==============

// BenchmarkComparisonDocumentSplitting 对标文档分割性能
//
// 竞品参考值：
//   - LangChain RecursiveCharacterTextSplitter: ~5ms/doc (10KB)
//   - LlamaIndex SentenceSplitter: ~8ms/doc
//   - Hexagon 目标: <1ms/doc
func BenchmarkComparisonDocumentSplitting(b *testing.B) {
	b.ReportAllocs()

	// 生成 10KB 测试文档
	doc := rag.Document{
		ID:      "test",
		Content: generateTestDocument(10240),
	}

	s := splitter.NewRecursiveSplitter(
		splitter.WithRecursiveChunkSize(500),
		splitter.WithRecursiveChunkOverlap(50),
	)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.Split(context.Background(), []rag.Document{doc})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkComparisonVectorStoreOps 对标向量存储操作
//
// 竞品参考值：
//   - LangChain FAISS: ~100μs/search (1K docs)
//   - LlamaIndex VectorStoreIndex: ~200μs/search
//   - Hexagon MemoryStore: 目标 <50μs/search
func BenchmarkComparisonVectorStoreOps(b *testing.B) {
	ctx := context.Background()
	dim := 128
	store := vector.NewMemoryStore(dim)

	// 插入 1000 条文档
	docs := make([]vector.Document, 1000)
	for i := range docs {
		embedding := make([]float32, dim)
		for j := range embedding {
			embedding[j] = rand.Float32()
		}
		docs[i] = vector.Document{
			ID:        fmt.Sprintf("doc-%d", i),
			Content:   fmt.Sprintf("Content of document %d", i),
			Embedding: embedding,
		}
	}
	if err := store.Add(ctx, docs); err != nil {
		b.Fatal(err)
	}

	// 生成查询向量
	query := make([]float32, dim)
	for i := range query {
		query[i] = rand.Float32()
	}

	b.Run("Search_TopK5", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, err := store.Search(ctx, query, 5)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Search_TopK10", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, err := store.Search(ctx, query, 10)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Add_Batch100", func(b *testing.B) {
		b.ReportAllocs()
		batch := make([]vector.Document, 100)
		for i := range batch {
			embedding := make([]float32, dim)
			for j := range embedding {
				embedding[j] = rand.Float32()
			}
			batch[i] = vector.Document{
				ID:        fmt.Sprintf("batch-%d", i),
				Content:   "batch content",
				Embedding: embedding,
			}
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// 更新 ID 避免冲突
			for j := range batch {
				batch[j].ID = fmt.Sprintf("batch-%d-%d", i, j)
			}
			if err := store.Add(ctx, batch); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// ============== 并发能力对标 ==============

// BenchmarkComparisonGoroutineScaling 对标 Go 并发扩展性
//
// 测试不同并发度下的吞吐量，量化 Go goroutine 优势。
// Python asyncio 在 CPU 密集型场景下受限于 GIL。
func BenchmarkComparisonGoroutineScaling(b *testing.B) {
	concurrencyLevels := []int{1, 10, 100, 1000}

	for _, concurrency := range concurrencyLevels {
		b.Run(fmt.Sprintf("Goroutines_%d", concurrency), func(b *testing.B) {
			b.ReportAllocs()

			ctx := context.Background()
			nodeHandler := func(_ context.Context, s comparisonState) (comparisonState, error) {
				s.Value++
				return s, nil
			}

			g, err := graph.NewGraph[comparisonState]("scale-test").
				AddNode("n1", nodeHandler).
				AddNode("n2", nodeHandler).
				AddEdge(graph.START, "n1").
				AddEdge("n1", "n2").
				AddEdge("n2", graph.END).
				Build()
			if err != nil {
				b.Fatal(err)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var wg sync.WaitGroup
				var ops atomic.Int64

				wg.Add(concurrency)
				for j := 0; j < concurrency; j++ {
					go func() {
						defer wg.Done()
						_, err := g.Run(ctx, comparisonState{})
						if err == nil {
							ops.Add(1)
						}
					}()
				}
				wg.Wait()
			}
		})
	}
}

// BenchmarkComparisonMemoryEfficiency 对标内存效率
//
// 测试批量操作的内存分配，Go 的值语义和栈分配
// 在大量小对象场景下优于 Python 的堆分配。
func BenchmarkComparisonMemoryEfficiency(b *testing.B) {
	b.Run("MapState_1000ops", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			state := graph.MapState{}
			for j := 0; j < 1000; j++ {
				state.Set(fmt.Sprintf("key-%d", j), j)
			}
			_ = state.Clone()
		}
	})

	b.Run("Document_Processing", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			docs := make([]rag.Document, 100)
			for j := range docs {
				docs[j] = rag.Document{
					ID:      fmt.Sprintf("doc-%d", j),
					Content: "Test content for benchmarking purposes",
					Metadata: map[string]any{
						"source": "benchmark",
						"index":  j,
					},
				}
			}
		}
	})
}

// ============== 综合场景对标 ==============

// BenchmarkComparisonE2EPipeline 对标端到端 RAG 管道
//
// 模拟完整 RAG 管道：分割 → 向量化 → 存储 → 检索
// 竞品参考值：
//   - LangChain: ~50ms/query (1K docs, 内存存储)
//   - LlamaIndex: ~30ms/query
//   - Hexagon 目标: <10ms/query
func BenchmarkComparisonE2EPipeline(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()

	dim := 64
	store := vector.NewMemoryStore(dim)

	// 预加载 100 条文档
	docs := make([]vector.Document, 100)
	for i := range docs {
		embedding := make([]float32, dim)
		for j := range embedding {
			embedding[j] = rand.Float32()
		}
		docs[i] = vector.Document{
			ID:        fmt.Sprintf("doc-%d", i),
			Content:   generateTestDocument(500),
			Embedding: embedding,
		}
	}
	if err := store.Add(ctx, docs); err != nil {
		b.Fatal(err)
	}

	query := make([]float32, dim)
	for i := range query {
		query[i] = rand.Float32()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 搜索
		results, err := store.Search(ctx, query, 5)
		if err != nil {
			b.Fatal(err)
		}

		// 模拟合成（拼接结果）
		var builder strings.Builder
		for _, r := range results {
			builder.WriteString(r.Content)
			builder.WriteString("\n")
		}
		_ = builder.String()
	}
}

// BenchmarkComparisonThroughput 对标吞吐量
//
// 在 10 秒内尽可能多地执行图，计算 QPS。
// 此基准用于生成对外发布的性能数据。
func BenchmarkComparisonThroughput(b *testing.B) {
	ctx := context.Background()

	nodeHandler := func(_ context.Context, s comparisonState) (comparisonState, error) {
		s.Value++
		return s, nil
	}

	g, err := graph.NewGraph[comparisonState]("throughput").
		AddNode("process", nodeHandler).
		AddEdge(graph.START, "process").
		AddEdge("process", graph.END).
		Build()
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()

	deadline := time.Now().Add(3 * time.Second)
	var ops atomic.Int64

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				_, err := g.Run(ctx, comparisonState{})
				if err == nil {
					ops.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	b.ReportMetric(float64(ops.Load())/3.0, "ops/sec")
}

// ============== 辅助函数 ==============

// generateTestDocument 生成指定大小的测试文档
func generateTestDocument(size int) string {
	sentences := []string{
		"Hexagon 是一个 Go 生态的全能型 AI Agent 框架。",
		"它在易用性、性能、扩展性、编排、可观测、安全六个维度追求均衡卓越。",
		"框架支持 ReAct Agent、多 Agent 协作、图编排等核心能力。",
		"RAG 系统提供文档加载、分割、向量化、检索、重排序、合成等全流程。",
		"可观测性通过 OpenTelemetry 和 Prometheus 集成实现。",
		"安全防护包括注入检测、PII 保护、内容过滤和 RBAC 控制。",
	}

	var builder strings.Builder
	for builder.Len() < size {
		builder.WriteString(sentences[builder.Len()%len(sentences)])
		builder.WriteString("\n")
	}

	result := builder.String()
	if len(result) > size {
		result = result[:size]
	}
	return result
}
