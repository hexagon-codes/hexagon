package rag

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/hexagon-codes/ai-core/store/vector"
)

// ============================================================================
// 测试桩 (stub) 实现
// ============================================================================

// stubEmbedder 是可控的 Embedder 实现，用于驱动 Engine 的真实逻辑。
//
// 它把每段文本映射为一个固定维度的向量，向量第 0 维使用文本长度，
// 其余维度填充常量，以便 MemoryStore 能基于余弦相似度做检索。
type stubEmbedder struct {
	dim int
	// embedFn 若非 nil 则覆盖默认行为，便于注入错误或返回数量不一致的向量。
	embedFn func(ctx context.Context, texts []string) ([][]float32, error)
}

func (e *stubEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if e.embedFn != nil {
		return e.embedFn(ctx, texts)
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, e.dim)
		// 用文本长度作为第 0 维，保证不同文本可区分，且非零向量
		v[0] = float32(len(t) + 1)
		for j := 1; j < e.dim; j++ {
			v[j] = 1.0
		}
		out[i] = v
	}
	return out, nil
}

func (e *stubEmbedder) Dimension() int { return e.dim }

// stubLoader 是可控的 Loader 实现。
type stubLoader struct {
	docs []Document
	err  error
	name string
}

func (l *stubLoader) Load(ctx context.Context) ([]Document, error) {
	return l.docs, l.err
}

func (l *stubLoader) Name() string { return l.name }

// stubSplitter 是可控的 Splitter 实现，会在每个文档内容上追加标记。
type stubSplitter struct {
	err  error
	name string
	// splitFn 若非 nil 则覆盖默认行为
	splitFn func(ctx context.Context, docs []Document) ([]Document, error)
}

func (s *stubSplitter) Split(ctx context.Context, docs []Document) ([]Document, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.splitFn != nil {
		return s.splitFn(ctx, docs)
	}
	out := make([]Document, len(docs))
	for i, d := range docs {
		d.Content = d.Content + "#split"
		out[i] = d
	}
	return out, nil
}

func (s *stubSplitter) Name() string { return s.name }

// newTestEngine 构造一个使用真实 MemoryStore + stubEmbedder 的 Engine。
func newTestEngine(t *testing.T, dim int, opts ...EngineOption) (*Engine, *vector.MemoryStore) {
	t.Helper()
	store := vector.NewMemoryStore(dim)
	base := []EngineOption{
		WithStore(store),
		WithEngineEmbedder(&stubEmbedder{dim: dim}),
	}
	base = append(base, opts...)
	return NewEngine(base...), store
}

// ============================================================================
// IndexDocuments / 摄取路径
// ============================================================================

// TestIndexDocuments_Normal 验证正常索引后 Count 与文档数一致。
func TestIndexDocuments_Normal(t *testing.T) {
	engine, store := newTestEngine(t, 8)
	docs := []Document{
		{ID: "a", Content: "hello"},
		{ID: "b", Content: "world"},
	}
	if err := engine.IndexDocuments(context.Background(), docs); err != nil {
		t.Fatalf("IndexDocuments 不应报错: %v", err)
	}
	n, err := store.Count(context.Background())
	if err != nil {
		t.Fatalf("Count 报错: %v", err)
	}
	if n != 2 {
		t.Errorf("期望 store 中有 2 个文档, 实际 %d", n)
	}
}

// TestIndexDocuments_Empty 验证空文档切片索引不报错且 store 为空。
func TestIndexDocuments_Empty(t *testing.T) {
	engine, store := newTestEngine(t, 8)
	if err := engine.IndexDocuments(context.Background(), []Document{}); err != nil {
		t.Fatalf("空文档索引不应报错: %v", err)
	}
	n, _ := store.Count(context.Background())
	if n != 0 {
		t.Errorf("空索引后 Count 应为 0, 实际 %d", n)
	}
}

// TestIndexDocuments_Nil 验证 nil 文档切片不会 panic。
func TestIndexDocuments_Nil(t *testing.T) {
	engine, _ := newTestEngine(t, 8)
	if err := engine.IndexDocuments(context.Background(), nil); err != nil {
		t.Fatalf("nil 文档索引不应报错: %v", err)
	}
}

// TestIndexDocuments_EmbedderError 验证 embedder 报错时被包装返回。
func TestIndexDocuments_EmbedderError(t *testing.T) {
	store := vector.NewMemoryStore(8)
	emb := &stubEmbedder{dim: 8, embedFn: func(ctx context.Context, texts []string) ([][]float32, error) {
		return nil, fmt.Errorf("embed boom")
	}}
	engine := NewEngine(WithStore(store), WithEngineEmbedder(emb))
	err := engine.IndexDocuments(context.Background(), []Document{{ID: "x", Content: "y"}})
	if err == nil {
		t.Fatal("embedder 报错时应返回错误")
	}
	if !strings.Contains(err.Error(), "failed to embed documents") {
		t.Errorf("错误应被包装为 'failed to embed documents', 实际: %v", err)
	}
}

// TestIndexDocuments_NoStore 验证缺少 store 时报错。
func TestIndexDocuments_NoStore(t *testing.T) {
	engine := NewEngine(WithEngineEmbedder(&stubEmbedder{dim: 8}))
	if err := engine.IndexDocuments(context.Background(), nil); err == nil {
		t.Error("缺少 store 应报错")
	}
}

// TestIndexDocuments_NoEmbedder 验证缺少 embedder 时报错。
func TestIndexDocuments_NoEmbedder(t *testing.T) {
	engine := NewEngine(WithStore(vector.NewMemoryStore(8)))
	if err := engine.IndexDocuments(context.Background(), nil); err == nil {
		t.Error("缺少 embedder 应报错")
	}
}

// TestIndexDocuments_EmbeddingCountMismatch 关键边界测试。
//
// IndexDocuments 用 docs 的下标 i 去索引 embeddings[i] (engine.go:139)。
// 若 embedder 返回的向量数量少于文档数量, 应优雅处理而非 panic。
// 这是一个常见的契约未校验缺陷：embedder 是外部接口, 返回数量不可信。
func TestIndexDocuments_EmbeddingCountMismatch(t *testing.T) {
	// 回归: B2 (embedder 返回向量数与文档数不匹配, 校验拦截不 panic) / B4 (维度校验)
	store := vector.NewMemoryStore(8)
	// embedder 故意只返回 1 个向量, 但传入 2 个文档
	emb := &stubEmbedder{dim: 8, embedFn: func(ctx context.Context, texts []string) ([][]float32, error) {
		return [][]float32{{1, 0, 0, 0, 0, 0, 0, 0}}, nil
	}}
	engine := NewEngine(WithStore(store), WithEngineEmbedder(emb))
	docs := []Document{
		{ID: "a", Content: "first"},
		{ID: "b", Content: "second"},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("embedding 数量不匹配导致 panic (越界), 应被校验: %v", r)
		}
	}()

	err := engine.IndexDocuments(context.Background(), docs)
	// 期望: 要么返回明确错误说明数量不匹配, 要么安全处理。
	if err == nil {
		t.Error("embedder 返回向量数量少于文档数时应返回错误, 实际无错误 (静默/数据错位风险)")
	} else if !strings.Contains(strings.ToLower(err.Error()), "embed") &&
		!strings.Contains(strings.ToLower(err.Error()), "mismatch") &&
		!strings.Contains(strings.ToLower(err.Error()), "count") &&
		!strings.Contains(strings.ToLower(err.Error()), "length") {
		t.Errorf("数量不匹配的错误应清晰说明原因, 实际: %v", err)
	}
}

// ============================================================================
// Ingest 摄取流程
// ============================================================================

// TestIngest_FullPipeline 验证 loader -> splitter -> index 完整链路。
func TestIngest_FullPipeline(t *testing.T) {
	store := vector.NewMemoryStore(8)
	loader := &stubLoader{docs: []Document{
		{ID: "1", Content: "alpha"},
		{ID: "2", Content: "beta"},
	}, name: "stub"}
	engine := NewEngine(
		WithStore(store),
		WithEngineEmbedder(&stubEmbedder{dim: 8}),
		WithLoader(loader),
		WithEngineSplitter(&stubSplitter{name: "stub"}),
	)
	if err := engine.Ingest(context.Background()); err != nil {
		t.Fatalf("Ingest 不应报错: %v", err)
	}
	n, _ := store.Count(context.Background())
	if n != 2 {
		t.Errorf("摄取后期望 2 个文档, 实际 %d", n)
	}
	// 验证 splitter 确实生效 (内容带 #split 标记)
	got, _ := store.Get(context.Background(), "1")
	if got == nil || !strings.HasSuffix(got.Content, "#split") {
		t.Errorf("splitter 未生效, 文档内容: %+v", got)
	}
}

// TestIngest_NoSplitter 验证无 splitter 时跳过分割直接索引。
func TestIngest_NoSplitter(t *testing.T) {
	store := vector.NewMemoryStore(8)
	loader := &stubLoader{docs: []Document{{ID: "1", Content: "raw"}}}
	engine := NewEngine(
		WithStore(store),
		WithEngineEmbedder(&stubEmbedder{dim: 8}),
		WithLoader(loader),
	)
	if err := engine.Ingest(context.Background()); err != nil {
		t.Fatalf("无 splitter 的 Ingest 不应报错: %v", err)
	}
	got, _ := store.Get(context.Background(), "1")
	if got == nil || got.Content != "raw" {
		t.Errorf("无 splitter 时内容应保持原样, 实际: %+v", got)
	}
}

// TestIngest_MissingDeps 表驱动验证缺依赖时报错。
func TestIngest_MissingDeps(t *testing.T) {
	loader := &stubLoader{docs: []Document{{ID: "1", Content: "x"}}}
	emb := &stubEmbedder{dim: 8}
	store := vector.NewMemoryStore(8)

	tests := []struct {
		name   string
		engine *Engine
	}{
		{"无 loader", NewEngine(WithStore(store), WithEngineEmbedder(emb))},
		{"无 store", NewEngine(WithLoader(loader), WithEngineEmbedder(emb))},
		{"无 embedder", NewEngine(WithLoader(loader), WithStore(store))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.engine.Ingest(context.Background()); err == nil {
				t.Errorf("%s 时 Ingest 应报错", tt.name)
			}
		})
	}
}

// TestIngest_LoaderError 验证 loader 错误被包装。
func TestIngest_LoaderError(t *testing.T) {
	store := vector.NewMemoryStore(8)
	loader := &stubLoader{err: fmt.Errorf("load boom")}
	engine := NewEngine(
		WithStore(store),
		WithEngineEmbedder(&stubEmbedder{dim: 8}),
		WithLoader(loader),
	)
	err := engine.Ingest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to load documents") {
		t.Errorf("loader 错误应被包装为 'failed to load documents', 实际: %v", err)
	}
}

// TestIngest_SplitterError 验证 splitter 错误被包装。
func TestIngest_SplitterError(t *testing.T) {
	store := vector.NewMemoryStore(8)
	loader := &stubLoader{docs: []Document{{ID: "1", Content: "x"}}}
	engine := NewEngine(
		WithStore(store),
		WithEngineEmbedder(&stubEmbedder{dim: 8}),
		WithLoader(loader),
		WithEngineSplitter(&stubSplitter{err: fmt.Errorf("split boom")}),
	)
	err := engine.Ingest(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to split documents") {
		t.Errorf("splitter 错误应被包装为 'failed to split documents', 实际: %v", err)
	}
}

// ============================================================================
// Retrieve 检索路径
// ============================================================================

// TestRetrieve_Normal 验证检索能拿到已索引的文档。
func TestRetrieve_Normal(t *testing.T) {
	engine, _ := newTestEngine(t, 8)
	docs := []Document{
		{ID: "a", Content: "golang"},
		{ID: "b", Content: "python"},
	}
	if err := engine.IndexDocuments(context.Background(), docs); err != nil {
		t.Fatalf("索引失败: %v", err)
	}
	got, err := engine.Retrieve(context.Background(), "golang")
	if err != nil {
		t.Fatalf("Retrieve 报错: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("应至少检索到一个文档")
	}
}

// TestRetrieve_TopK 验证 WithTopK 限制返回数量。
func TestRetrieve_TopK(t *testing.T) {
	engine, _ := newTestEngine(t, 8)
	var docs []Document
	for i := 0; i < 10; i++ {
		docs = append(docs, Document{ID: fmt.Sprintf("d%d", i), Content: fmt.Sprintf("doc %d", i)})
	}
	if err := engine.IndexDocuments(context.Background(), docs); err != nil {
		t.Fatalf("索引失败: %v", err)
	}
	got, err := engine.Retrieve(context.Background(), "doc", WithTopK(3))
	if err != nil {
		t.Fatalf("Retrieve 报错: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("WithTopK(3) 期望返回 3 个, 实际 %d", len(got))
	}
}

// TestRetrieve_TopKZero 边界：TopK=0。
//
// 检索 TopK 为 0 时的行为：MemoryStore 会返回 0 个文档 (k 取 min)。
// 这里验证不会 panic 且返回空集合, 暴露"0 表示无结果"而非"全部"的语义。
func TestRetrieve_TopKZero(t *testing.T) {
	engine, _ := newTestEngine(t, 8)
	if err := engine.IndexDocuments(context.Background(), []Document{
		{ID: "a", Content: "x"},
	}); err != nil {
		t.Fatalf("索引失败: %v", err)
	}
	got, err := engine.Retrieve(context.Background(), "x", WithTopK(0))
	if err != nil {
		t.Fatalf("TopK=0 不应报错: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("TopK=0 期望返回空, 实际 %d 个", len(got))
	}
}

// TestRetrieve_TopKNegative 边界：TopK 为负数。
//
// 负数 TopK 是非法输入。MemoryStore.Search 在 k 为负时执行
// make([]Document, k) 会 panic。Engine 直接把 cfg.TopK 透传给 store,
// 未做下限校验。此测试用于暴露该缺陷。
func TestRetrieve_TopKNegative(t *testing.T) {
	// 回归: B3 (负数 TopK 在 Engine 层 clamp, 不透传给 store 致 panic)
	engine, _ := newTestEngine(t, 8)
	if err := engine.IndexDocuments(context.Background(), []Document{
		{ID: "a", Content: "x"},
	}); err != nil {
		t.Fatalf("索引失败: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("负数 TopK 导致 panic, Engine 应做下限校验: %v", r)
		}
	}()

	got, err := engine.Retrieve(context.Background(), "x", WithTopK(-1))
	if err == nil && len(got) > 0 {
		t.Errorf("负数 TopK 应返回错误或空结果, 实际返回 %d 个文档", len(got))
	}
}

// TestRetrieve_NoStore / NoEmbedder 验证缺依赖报错。
func TestRetrieve_NoStore(t *testing.T) {
	engine := NewEngine(WithEngineEmbedder(&stubEmbedder{dim: 8}))
	if _, err := engine.Retrieve(context.Background(), "q"); err == nil {
		t.Error("缺 store 应报错")
	}
}

func TestRetrieve_NoEmbedder(t *testing.T) {
	engine := NewEngine(WithStore(vector.NewMemoryStore(8)))
	if _, err := engine.Retrieve(context.Background(), "q"); err == nil {
		t.Error("缺 embedder 应报错")
	}
}

// TestRetrieve_EmbedderError 验证查询向量生成失败被包装。
func TestRetrieve_EmbedderError(t *testing.T) {
	store := vector.NewMemoryStore(8)
	emb := &stubEmbedder{dim: 8, embedFn: func(ctx context.Context, texts []string) ([][]float32, error) {
		return nil, fmt.Errorf("embed boom")
	}}
	engine := NewEngine(WithStore(store), WithEngineEmbedder(emb))
	_, err := engine.Retrieve(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "failed to embed query") {
		t.Errorf("查询 embed 错误应被包装, 实际: %v", err)
	}
}

// TestRetrieve_EmptyEmbedding 验证 embedder 返回空向量集时报错。
//
// engine.go:170 专门检查 len(embedding)==0 的情况。
func TestRetrieve_EmptyEmbedding(t *testing.T) {
	store := vector.NewMemoryStore(8)
	emb := &stubEmbedder{dim: 8, embedFn: func(ctx context.Context, texts []string) ([][]float32, error) {
		return [][]float32{}, nil // 返回空集合, 无错误
	}}
	engine := NewEngine(WithStore(store), WithEngineEmbedder(emb))
	_, err := engine.Retrieve(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "no embedding returned") {
		t.Errorf("空 embedding 应报 'no embedding returned', 实际: %v", err)
	}
}

// TestRetrieve_EmptyQuery 边界：空查询字符串。
func TestRetrieve_EmptyQuery(t *testing.T) {
	engine, _ := newTestEngine(t, 8)
	if err := engine.IndexDocuments(context.Background(), []Document{
		{ID: "a", Content: "x"},
	}); err != nil {
		t.Fatalf("索引失败: %v", err)
	}
	// 空查询应正常走完流程 (stubEmbedder 对空串也生成向量)
	if _, err := engine.Retrieve(context.Background(), ""); err != nil {
		t.Errorf("空查询不应报错: %v", err)
	}
}

// TestRetrieve_Filter 验证元数据过滤透传。
func TestRetrieve_Filter(t *testing.T) {
	engine, _ := newTestEngine(t, 8)
	docs := []Document{
		{ID: "a", Content: "x", Metadata: map[string]any{"cat": "go"}},
		{ID: "b", Content: "x", Metadata: map[string]any{"cat": "py"}},
	}
	if err := engine.IndexDocuments(context.Background(), docs); err != nil {
		t.Fatalf("索引失败: %v", err)
	}
	got, err := engine.Retrieve(context.Background(), "x", WithFilter(map[string]any{"cat": "go"}))
	if err != nil {
		t.Fatalf("Retrieve 报错: %v", err)
	}
	for _, d := range got {
		if d.Metadata["cat"] != "go" {
			t.Errorf("过滤未生效, 返回了 cat=%v 的文档", d.Metadata["cat"])
		}
	}
}

// TestRetrieve_ContextCanceled 验证已取消的 context 被 store 感知并返回错误。
func TestRetrieve_ContextCanceled(t *testing.T) {
	engine, _ := newTestEngine(t, 8)
	if err := engine.IndexDocuments(context.Background(), []Document{
		{ID: "a", Content: "x"},
	}); err != nil {
		t.Fatalf("索引失败: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := engine.Retrieve(ctx, "x")
	if err == nil {
		t.Error("已取消 context 的检索应返回错误")
	}
}

// ============================================================================
// Query 格式化输出
// ============================================================================

// TestQuery_Format 验证 Query 返回的上下文字符串格式。
func TestQuery_Format(t *testing.T) {
	engine, _ := newTestEngine(t, 8)
	if err := engine.IndexDocuments(context.Background(), []Document{
		{ID: "a", Content: "content-A"},
	}); err != nil {
		t.Fatalf("索引失败: %v", err)
	}
	out, err := engine.Query(context.Background(), "content-A")
	if err != nil {
		t.Fatalf("Query 报错: %v", err)
	}
	if !strings.Contains(out, "[Document 1") || !strings.Contains(out, "content-A") {
		t.Errorf("格式化输出不符合预期, 实际: %q", out)
	}
}

// TestQuery_EmptyResult 验证无检索结果时返回空字符串。
func TestQuery_EmptyResult(t *testing.T) {
	engine, _ := newTestEngine(t, 8)
	out, err := engine.Query(context.Background(), "anything")
	if err != nil {
		t.Fatalf("Query 报错: %v", err)
	}
	if out != "" {
		t.Errorf("无文档时应返回空串, 实际: %q", out)
	}
}

// TestQuery_PropagatesError 验证底层 Retrieve 错误被透传。
func TestQuery_PropagatesError(t *testing.T) {
	engine := NewEngine(WithEngineEmbedder(&stubEmbedder{dim: 8})) // 无 store
	if _, err := engine.Query(context.Background(), "q"); err == nil {
		t.Error("Query 应透传 Retrieve 的错误")
	}
}

// ============================================================================
// Delete / Clear / Count
// ============================================================================

// TestDelete_Normal 验证删除后文档不可检索。
func TestDelete_Normal(t *testing.T) {
	engine, store := newTestEngine(t, 8)
	if err := engine.IndexDocuments(context.Background(), []Document{
		{ID: "a", Content: "x"},
		{ID: "b", Content: "y"},
	}); err != nil {
		t.Fatalf("索引失败: %v", err)
	}
	if err := engine.Delete(context.Background(), []string{"a"}); err != nil {
		t.Fatalf("Delete 报错: %v", err)
	}
	n, _ := store.Count(context.Background())
	if n != 1 {
		t.Errorf("删除一个后期望剩 1, 实际 %d", n)
	}
}

// TestClear_Normal 验证清空后 Count 为 0。
func TestClear_Normal(t *testing.T) {
	engine, store := newTestEngine(t, 8)
	if err := engine.IndexDocuments(context.Background(), []Document{
		{ID: "a", Content: "x"},
	}); err != nil {
		t.Fatalf("索引失败: %v", err)
	}
	if err := engine.Clear(context.Background()); err != nil {
		t.Fatalf("Clear 报错: %v", err)
	}
	n, _ := store.Count(context.Background())
	if n != 0 {
		t.Errorf("清空后期望 0, 实际 %d", n)
	}
}

// TestCount_NoStore / Delete_NoStore / Clear_NoStore 缺依赖报错。
func TestCount_NoStoreErr(t *testing.T) {
	engine := NewEngine()
	if _, err := engine.Count(context.Background()); err == nil {
		t.Error("缺 store 时 Count 应报错")
	}
}

func TestDelete_NoStoreErr(t *testing.T) {
	engine := NewEngine()
	if err := engine.Delete(context.Background(), []string{"a"}); err == nil {
		t.Error("缺 store 时 Delete 应报错")
	}
}

func TestClear_NoStoreErr(t *testing.T) {
	engine := NewEngine()
	if err := engine.Clear(context.Background()); err == nil {
		t.Error("缺 store 时 Clear 应报错")
	}
}

// TestIndex_DelegatesToIndexDocuments 验证 Index 接口方法等价于 IndexDocuments。
func TestIndex_DelegatesToIndexDocuments(t *testing.T) {
	engine, store := newTestEngine(t, 8)
	if err := engine.Index(context.Background(), []Document{{ID: "a", Content: "x"}}); err != nil {
		t.Fatalf("Index 报错: %v", err)
	}
	n, _ := store.Count(context.Background())
	if n != 1 {
		t.Errorf("Index 后期望 1, 实际 %d", n)
	}
}

// ============================================================================
// 并发竞态
// ============================================================================

// TestConcurrentIndexRetrieve 验证并发索引/检索无数据竞争 (配合 -race)。
func TestConcurrentIndexRetrieve(t *testing.T) {
	engine, _ := newTestEngine(t, 8)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			_ = engine.IndexDocuments(context.Background(), []Document{
				{ID: fmt.Sprintf("d%d", n), Content: fmt.Sprintf("c%d", n)},
			})
		}(i)
		go func() {
			defer wg.Done()
			_, _ = engine.Retrieve(context.Background(), "c", WithTopK(5))
		}()
	}
	wg.Wait()
}

// ============================================================================
// Pipeline
// ============================================================================

// TestPipeline_Ingest 验证 Pipeline 完整摄取链路。
func TestPipeline_Ingest(t *testing.T) {
	store := vector.NewMemoryStore(8)
	engine := NewEngine(WithStore(store), WithEngineEmbedder(&stubEmbedder{dim: 8}))
	loader := &stubLoader{docs: []Document{{ID: "1", Content: "a"}, {ID: "2", Content: "b"}}}
	// engine 同时实现 Indexer 与 Retriever
	pipeline := NewPipeline(loader, &stubSplitter{}, engine, engine)
	if err := pipeline.Ingest(context.Background()); err != nil {
		t.Fatalf("Pipeline.Ingest 报错: %v", err)
	}
	n, _ := store.Count(context.Background())
	if n != 2 {
		t.Errorf("Pipeline 摄取后期望 2, 实际 %d", n)
	}
}

// TestPipeline_IngestNoSplitter 验证 Pipeline 在 splitter 为 nil 时跳过分割。
func TestPipeline_IngestNoSplitter(t *testing.T) {
	store := vector.NewMemoryStore(8)
	engine := NewEngine(WithStore(store), WithEngineEmbedder(&stubEmbedder{dim: 8}))
	loader := &stubLoader{docs: []Document{{ID: "1", Content: "a"}}}
	pipeline := NewPipeline(loader, nil, engine, engine)
	if err := pipeline.Ingest(context.Background()); err != nil {
		t.Fatalf("Pipeline.Ingest (无 splitter) 报错: %v", err)
	}
}

// TestPipeline_LoaderError 验证 Pipeline loader 错误透传。
func TestPipeline_LoaderError(t *testing.T) {
	store := vector.NewMemoryStore(8)
	engine := NewEngine(WithStore(store), WithEngineEmbedder(&stubEmbedder{dim: 8}))
	loader := &stubLoader{err: fmt.Errorf("boom")}
	pipeline := NewPipeline(loader, nil, engine, engine)
	if err := pipeline.Ingest(context.Background()); err == nil {
		t.Error("Pipeline loader 错误应透传")
	}
}

// TestPipeline_Query 验证 Pipeline.Query 委托给 retriever。
func TestPipeline_Query(t *testing.T) {
	store := vector.NewMemoryStore(8)
	engine := NewEngine(WithStore(store), WithEngineEmbedder(&stubEmbedder{dim: 8}))
	if err := engine.IndexDocuments(context.Background(), []Document{{ID: "1", Content: "hit"}}); err != nil {
		t.Fatalf("索引失败: %v", err)
	}
	loader := &stubLoader{}
	pipeline := NewPipeline(loader, nil, engine, engine)
	got, err := pipeline.Query(context.Background(), "hit")
	if err != nil {
		t.Fatalf("Pipeline.Query 报错: %v", err)
	}
	if len(got) == 0 {
		t.Error("Pipeline.Query 应返回检索结果")
	}
}
