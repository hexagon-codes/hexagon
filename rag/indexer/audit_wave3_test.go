package indexer

// audit_wave3_test.go
//
// 本文件为 rag/indexer 包的"挑刺式"审计测试，覆盖：
//   - 正常路径
//   - 边界情况 (空/nil/超长/0/负数/Unicode/并发)
//   - 错误路径
//   - 未闭环逻辑 / 状态一致性
//
// 设计原则：针对真实公开 API，不 mock 掉被测逻辑本身。
// 仅使用已存在的 mock (mockVectorStore / mockEmbedder, 定义在 indexer_test.go)。

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/hexagon-codes/hexagon/rag"
)

// ============================================================
// 1. VectorIndexer: embedder 返回数量与输入不匹配 (越界panic风险)
// ============================================================

// shortEmbedder 故意返回比输入文本数量更少的向量。
// 真实 Provider 在异常时可能返回与输入不等长的结果。
type shortEmbedder struct {
	dim int
}

func (e *shortEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	// 故意只返回 1 个向量，无论输入多少
	if len(texts) == 0 {
		return nil, nil
	}
	return [][]float32{make([]float32, e.dim)}, nil
}
func (e *shortEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	return make([]float32, e.dim), nil
}
func (e *shortEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	return make([]float32, e.dim), nil
}
func (e *shortEmbedder) Dimension() int { return e.dim }

// TestVectorIndexer_EmbeddingCountMismatch 验证 embedder 返回数量 < 文本数量时的行为。
// 回归: W3-29
// indexer.go:93 `Embedding: embeddings[j]` 在 j 超出 embeddings 长度时会 panic。
// 期望：要么返回明确错误，要么不 panic；不应让一个 Provider 返回异常导致整个进程崩溃。
func TestVectorIndexer_EmbeddingCountMismatch(t *testing.T) {
	store := newMockVectorStore()
	idx := NewVectorIndexer(store, &shortEmbedder{dim: 8})

	ctx := context.Background()
	docs := []rag.Document{
		{ID: "1", Content: "a"},
		{ID: "2", Content: "b"},
		{ID: "3", Content: "c"},
	}

	// 用 recover 捕获 panic，把 panic 转成测试失败信息，避免崩溃整个测试进程
	var panicked any
	err := func() (e error) {
		defer func() {
			if r := recover(); r != nil {
				panicked = r
			}
		}()
		return idx.Index(ctx, docs)
	}()

	if panicked != nil {
		t.Fatalf("Index 在 embedder 返回数量不足时发生 panic: %v (应返回错误而非 panic)", panicked)
	}
	// 若没 panic 也没报错，则把不匹配的向量静默存入，属于数据损坏，也应视为问题
	if err == nil {
		t.Errorf("embedder 返回向量数量(%d) < 文本数量(%d)，期望返回错误，实际 err=nil", 1, len(docs))
	}
}

// TestConcurrentIndexer_EmbeddingCountMismatch 并发索引器同样存在该越界风险 (indexer.go:222)。
// 回归: W3-29
//
// 注意: ConcurrentIndexer.Index 在内部自行 spawn worker goroutine, 越界 panic 原本发生在
// 库的内部 goroutine 中, 调用方 *无法* 通过 recover 捕获 → 会直接 crash 整个进程。
// 修复后 worker 内部校验 len(embeddings)==len(texts), 不等时通过 error channel 返回错误,
// 既不崩溃也不静默写入损坏数据。本测试断言: 不 panic 且返回明确错误。
func TestConcurrentIndexer_EmbeddingCountMismatch(t *testing.T) {
	store := newMockVectorStore()
	idx := NewConcurrentIndexer(store, &shortEmbedder{dim: 8})

	ctx := context.Background()
	docs := []rag.Document{
		{ID: "1", Content: "a"},
		{ID: "2", Content: "b"},
		{ID: "3", Content: "c"},
	}

	var panicked any
	err := func() (e error) {
		defer func() {
			if r := recover(); r != nil {
				panicked = r
			}
		}()
		return idx.Index(ctx, docs)
	}()

	if panicked != nil {
		t.Fatalf("ConcurrentIndexer.Index 在 embedder 返回数量不足时发生 panic: %v (应通过 error channel 返回错误)", panicked)
	}
	if err == nil {
		t.Errorf("embedder 返回向量数量(%d) < 文本数量(%d)，期望返回错误，实际 err=nil", 1, len(docs))
	}
}

// ============================================================
// 2. VectorIndexer: 边界 batchSize
// ============================================================

// TestVectorIndexer_ZeroBatchSize 验证 batchSize=0 被构造函数校验回退默认值。
// 回归: W3-37
// indexer.go 循环 `start += i.batchSize`，若 batchSize<=0 则 start 永不前进 → 死循环。
// 期望：构造函数对非法 batchSize 做防御 (回退默认值)，非空文档能正常索引完成。
func TestVectorIndexer_ZeroBatchSize(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(8)
	idx := NewVectorIndexer(store, embedder, WithBatchSize(0))

	if idx.batchSize <= 0 {
		t.Fatalf("batchSize=0 应被构造函数 clamp 到正数默认值, 实际 %d", idx.batchSize)
	}

	docs := []rag.Document{{ID: "1", Content: "x"}}
	if err := idx.Index(context.Background(), docs); err != nil {
		t.Fatalf("batchSize=0 经校验后 Index 应正常完成, 实际 err=%v", err)
	}
	if n, _ := store.Count(context.Background()); n != 1 {
		t.Errorf("应索引 1 篇文档, store 实际 %d", n)
	}
}

// TestVectorIndexer_NegativeBatchSize 负数 batchSize 应被构造函数校验回退默认值。
// 回归: W3-37
// 负数 batchSize 在 Index 非空文档时, end=start+(负数) 永远 < start, 切片 docs[start:end] 会 panic。
func TestVectorIndexer_NegativeBatchSize(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(8)
	idx := NewVectorIndexer(store, embedder, WithBatchSize(-5))

	if idx.batchSize <= 0 {
		t.Fatalf("负数 batchSize 应被 clamp 到正数默认值, 实际 %d", idx.batchSize)
	}

	docs := []rag.Document{{ID: "1", Content: "x"}}
	var panicked any
	err := func() (e error) {
		defer func() {
			if r := recover(); r != nil {
				panicked = r
			}
		}()
		return idx.Index(context.Background(), docs)
	}()
	if panicked != nil {
		t.Fatalf("负数 batchSize 经校验后 Index 不应 panic: %v", panicked)
	}
	if err != nil {
		t.Errorf("负数 batchSize 经校验后 Index 应正常完成, 实际 err=%v", err)
	}
}

// TestConcurrentIndexer_ZeroBatchSize 并发索引器 batchSize=0 / concurrency=0 应被校验。
// 回归: W3-37
func TestConcurrentIndexer_ZeroBatchSize(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(8)
	idx := NewConcurrentIndexer(store, embedder,
		WithConcurrentBatchSize(0), WithConcurrency(0))

	if idx.batchSize <= 0 {
		t.Fatalf("并发索引器 batchSize=0 应被 clamp 到正数, 实际 %d", idx.batchSize)
	}
	if idx.concurrency <= 0 {
		t.Fatalf("并发索引器 concurrency=0 应被 clamp 到正数, 实际 %d", idx.concurrency)
	}

	docs := []rag.Document{{ID: "1", Content: "x"}, {ID: "2", Content: "y"}}
	if err := idx.Index(context.Background(), docs); err != nil {
		t.Fatalf("校验后并发 Index 应正常完成, 实际 err=%v", err)
	}
	if n, _ := store.Count(context.Background()); n != 2 {
		t.Errorf("应索引 2 篇文档, store 实际 %d", n)
	}
}

// ============================================================
// 3. VectorIndexer: store.Add 出错路径
// ============================================================

func TestVectorIndexer_StoreAddError(t *testing.T) {
	store := newMockVectorStore()
	store.addErr = errors.New("store down")
	embedder := newMockEmbedder(8)
	idx := NewVectorIndexer(store, embedder)

	err := idx.Index(context.Background(), []rag.Document{{ID: "1", Content: "x"}})
	if err == nil {
		t.Fatal("store.Add 出错时 Index 应返回错误")
	}
	if !strings.Contains(err.Error(), "store down") {
		t.Errorf("错误应包装底层错误, got: %v", err)
	}
}

func TestVectorIndexer_EmbedError(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(8)
	embedder.embedErr = errors.New("embed fail")
	idx := NewVectorIndexer(store, embedder)

	err := idx.Index(context.Background(), []rag.Document{{ID: "1", Content: "x"}})
	if err == nil {
		t.Fatal("embedder 出错时 Index 应返回错误")
	}
}

// ============================================================
// 4. IncrementalIndexer: 空 ID 文档的去重状态一致性
// ============================================================

// TestIncrementalIndexer_EmptyIDDocsCollapse 验证多个空 ID 但内容不同的文档。
// 回归: W3-30
// indexer.go:309 原用 doc.ID 作为 checksums 的 key。所有空 ID 文档共享 key ""，
// 导致：第一篇空 ID 文档写入 checksums[""]=csA 后，第二篇空 ID 内容不同的文档
// 也能正确判定 changed 并索引——但它们在 checksums 中互相覆盖，状态语义混乱。
// 更严重：同一批次内多篇空 ID 文档若内容相同，会被全部判为需索引(因为 map 里还没有"")，
// 但底层 VectorIndexer 会给每篇生成不同的随机 ID 存入 store，造成重复存储。
// 修复后: 空 ID 文档基于内容派生稳定 ID, 相同内容落到同一槽位去重。
func TestIncrementalIndexer_EmptyIDDocsDuplicateStore(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(8)
	idx := NewIncrementalIndexer(store, embedder)
	ctx := context.Background()

	// 两篇内容完全相同、ID 均为空的文档
	docs := []rag.Document{
		{ID: "", Content: "same content"},
		{ID: "", Content: "same content"},
	}
	if err := idx.Index(ctx, docs); err != nil {
		t.Fatalf("Index failed: %v", err)
	}

	// 再次索引同样两篇
	if err := idx.Index(ctx, docs); err != nil {
		t.Fatalf("re-Index failed: %v", err)
	}

	count, _ := store.Count(ctx)
	// 期望: 增量索引器为空 ID 文档派生稳定 (基于内容) ID, 相同内容落到同一槽位,
	//       两篇相同内容的空 ID 文档去重为 1, 两轮索引也不会重复增长。
	t.Logf("空 ID 相同内容文档索引两轮后 store 文档数 = %d (期望去重为 1)", count)
	if count != 1 {
		t.Errorf("空 ID 相同内容文档应去重为 1, count=%d", count)
	}
}

// TestIncrementalIndexer_EmptyIDChecksumKey
// 回归: W3-30
// 删除时按传入 ID 删 checksums, 但空 ID 文档原本的 checksum key 是 "",
// 业务无法用真实 ID 删除它们 → 增量状态与 store 永久不一致。
// 修复后: 空 ID 文档不再使用空字符串作为 checksum key。
func TestIncrementalIndexer_EmptyIDChecksumKey(t *testing.T) {
	store := newMockVectorStore()
	embedder := newMockEmbedder(8)
	idx := NewIncrementalIndexer(store, embedder)
	ctx := context.Background()

	_ = idx.Index(ctx, []rag.Document{{ID: "", Content: "hello"}})

	idx.mu.RLock()
	_, hasEmpty := idx.checksums[""]
	n := len(idx.checksums)
	idx.mu.RUnlock()

	if hasEmpty {
		t.Logf("checksums 使用空字符串作为 key (n=%d), 说明空 ID 文档共用一个 checksum 槽位, 无法独立追踪", n)
	}
	// 这是设计缺陷断言: 空 ID 文档不应共享 checksum key
	if hasEmpty {
		t.Errorf("增量索引器用空字符串作为 checksum key, 多个空 ID 文档将互相覆盖状态")
	}
}

// ============================================================
// 5. MemoryGraphStore: UpdateEntity 改名后旧名仍可查 (状态不一致)
// ============================================================

// TestMemoryGraphStore_UpdateEntityRenameStaleName 验证改名后旧名索引未清理。
// 回归: W3-31
// graph.go:412 UpdateEntity 原本写入 entityNames[新名]=id, 但从不删除 entityNames[旧名]。
// 结果: GetEntityByName(旧名) 仍能查到该实体 → 名称索引出现脏数据。
func TestMemoryGraphStore_UpdateEntityRenameStaleName(t *testing.T) {
	store := NewMemoryGraphStore()
	ctx := context.Background()

	e := &Entity{ID: "e1", Name: "OldName", Type: EntityTypePerson}
	if err := store.AddEntity(ctx, e); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	// 改名
	e.Name = "NewName"
	if err := store.UpdateEntity(ctx, e); err != nil {
		t.Fatalf("UpdateEntity: %v", err)
	}

	// 旧名不应再能查到
	got, err := store.GetEntityByName(ctx, "OldName")
	if err == nil {
		t.Errorf("改名后用旧名 OldName 仍查到实体 %+v, entityNames 索引未清理脏数据", got)
	}

	// 新名应能查到
	if _, err := store.GetEntityByName(ctx, "NewName"); err != nil {
		t.Errorf("新名 NewName 应能查到, got err: %v", err)
	}
}

// ============================================================
// 6. MemoryGraphStore: DeleteEntity 留下悬空边引用 (未闭环)
// ============================================================

// TestMemoryGraphStore_DeleteEntityDanglingEdges 验证删除实体后, 邻居实体的
// 边列表仍残留已删除关系的 RelationID。
// 回归: W3-32
// graph.go:428 DeleteEntity 原本删除了 s.relations 中的关系, 并删除了被删实体自己的
// outEdges/inEdges, 但 *没有* 从另一端实体的 inEdges/outEdges 中移除这些 relID。
// 后果: GetRelations(邻居) 时按 relID 查 s.relations 已为 nil 会被 ok 过滤掉 (尚可忍受),
//       但 GetNeighbors / GetSubgraph 遍历邻居边列表时残留 relID 会持续累积, 内存泄漏式残留。
func TestMemoryGraphStore_DeleteEntityDanglingEdges(t *testing.T) {
	store := NewMemoryGraphStore()
	ctx := context.Background()

	a := &Entity{ID: "A", Name: "A", Type: EntityTypeConcept}
	b := &Entity{ID: "B", Name: "B", Type: EntityTypeConcept}
	_ = store.AddEntity(ctx, a)
	_ = store.AddEntity(ctx, b)

	// A --rel--> B
	rel := &Relation{ID: "r1", SourceID: "A", TargetID: "B", Type: RelationTypeRelatedTo}
	if err := store.AddRelation(ctx, rel); err != nil {
		t.Fatalf("AddRelation: %v", err)
	}

	// 删除 A (它是 r1 的源)。期望 B 的 inEdges 中不再残留 r1。
	if err := store.DeleteEntity(ctx, "A"); err != nil {
		t.Fatalf("DeleteEntity: %v", err)
	}

	// 直接检查内部 inEdges 残留
	store.mu.RLock()
	bInEdges := append([]string(nil), store.inEdges["B"]...)
	store.mu.RUnlock()

	for _, rid := range bInEdges {
		if rid == "r1" {
			t.Errorf("删除实体 A 后, 邻居 B 的 inEdges 仍残留已删关系 r1: %v (悬空边未清理)", bInEdges)
		}
	}
}

// ============================================================
// 7. MemoryGraphStore: SearchEntities 分页 Offset 越界返回全量 (逻辑错误)
// ============================================================

// TestMemoryGraphStore_SearchOffsetOutOfRange 验证 Offset >= 结果数时的行为。
// 回归: W3-33
// graph.go:401 原 `if query.Offset > 0 && query.Offset < len(results)` —— 当 Offset >= len 时
// 条件不成立, 切片被跳过, 返回的是 *全量* 结果而非空集。这违反分页语义 (越界页应为空)。
func TestMemoryGraphStore_SearchOffsetOutOfRange(t *testing.T) {
	store := NewMemoryGraphStore()
	ctx := context.Background()

	for _, name := range []string{"x1", "x2", "x3"} {
		_ = store.AddEntity(ctx, &Entity{Name: name, Type: EntityTypeConcept})
	}

	// 共 3 个实体, 请求 Offset=10 (远超总数)
	results, err := store.SearchEntities(ctx, EntityQuery{Offset: 10})
	if err != nil {
		t.Fatalf("SearchEntities: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Offset(10) >= 实体数(3) 时应返回空集, 实际返回 %d 条 (分页越界返回了全量数据)", len(results))
	}
}

// TestMemoryGraphStore_SearchOffsetEqualsLen Offset 恰好等于总数, 也应为空
// 回归: W3-33
func TestMemoryGraphStore_SearchOffsetEqualsLen(t *testing.T) {
	store := NewMemoryGraphStore()
	ctx := context.Background()
	for _, name := range []string{"a", "b"} {
		_ = store.AddEntity(ctx, &Entity{Name: name, Type: EntityTypeConcept})
	}

	results, _ := store.SearchEntities(ctx, EntityQuery{Offset: 2})
	if len(results) != 0 {
		t.Errorf("Offset(2) == 实体数(2) 应返回空集, 实际 %d 条", len(results))
	}
}

// ============================================================
// 8. removeFromSlice: 切片别名/底层数组改写 (隐藏 bug)
// ============================================================

// TestRemoveFromSlice_AliasingCorruption 验证 removeFromSlice 的 append 原地改写。
// graph.go:1201 `append(slice[:i], slice[i+1:]...)` 会原地修改底层数组。
// 当 outEdges/inEdges 与其他持有同一底层数组的切片共享时, 会破坏对方数据。
// 这里直接验证函数语义本身是否正确 (移除元素)。
func TestRemoveFromSlice_Basic(t *testing.T) {
	tests := []struct {
		name  string
		slice []string
		elem  string
		want  []string
	}{
		{"移除中间", []string{"a", "b", "c"}, "b", []string{"a", "c"}},
		{"移除首个", []string{"a", "b", "c"}, "a", []string{"b", "c"}},
		{"移除末尾", []string{"a", "b", "c"}, "c", []string{"a", "b"}},
		{"不存在", []string{"a", "b"}, "z", []string{"a", "b"}},
		{"空切片", nil, "x", nil},
		{"单元素命中", []string{"a"}, "a", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := removeFromSlice(tt.slice, tt.elem)
			if len(got) != len(tt.want) {
				t.Fatalf("removeFromSlice(%v,%q) 长度=%d, want %d", tt.slice, tt.elem, len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("removeFromSlice(%v,%q)[%d]=%q want %q", tt.slice, tt.elem, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestRemoveFromSlice_OnlyRemovesFirst removeFromSlice 仅移除首个匹配。
// 当同一 relID 在边列表中出现多次 (重复 AddRelation 同 ID) 时, 只删一个 → 残留。
func TestRemoveFromSlice_OnlyFirstMatch(t *testing.T) {
	got := removeFromSlice([]string{"r", "r", "x"}, "r")
	// 仅移除第一个 "r", 残留一个
	remain := 0
	for _, v := range got {
		if v == "r" {
			remain++
		}
	}
	if remain != 1 {
		t.Errorf("removeFromSlice 应仅移除首个匹配, 残留 r 数量=%d (want 1)", remain)
	}
}

// ============================================================
// 9. MemoryGraphStore: 重复添加同 ID 关系导致边列表重复 (状态膨胀)
// ============================================================

// TestMemoryGraphStore_DuplicateRelationID 用相同 ID 两次 AddRelation。
// 回归: W3-34
// graph.go:476 s.relations[id] 被覆盖 (1 条), 但 outEdges/inEdges 各 append 两次 (2 条),
// 造成边列表与关系表不一致; DeleteRelation 用 removeFromSlice 只删首个 → 残留悬空 relID。
// 修复后: AddRelation 对已存在的关系 ID 仅更新关系本体, 不再追加边引用。
func TestMemoryGraphStore_DuplicateRelationIDInconsistency(t *testing.T) {
	store := NewMemoryGraphStore()
	ctx := context.Background()
	_ = store.AddEntity(ctx, &Entity{ID: "A", Name: "A", Type: EntityTypeConcept})
	_ = store.AddEntity(ctx, &Entity{ID: "B", Name: "B", Type: EntityTypeConcept})

	rel := &Relation{ID: "dup", SourceID: "A", TargetID: "B", Type: RelationTypeRelatedTo}
	_ = store.AddRelation(ctx, rel)
	_ = store.AddRelation(ctx, rel) // 同 ID 再加一次

	store.mu.RLock()
	relCount := len(store.relations)
	outCount := len(store.outEdges["A"])
	store.mu.RUnlock()

	// relations 表去重为 1, 但 outEdges 膨胀为 2 → 不一致
	if relCount == 1 && outCount > 1 {
		t.Errorf("重复添加同 ID 关系: relations 表=%d 但 A.outEdges=%d, 边列表与关系表不一致 (未做去重)", relCount, outCount)
	}

	// 删除后 GetRelations 应为空; 因边列表残留, 验证一致性
	_ = store.DeleteRelation(ctx, "dup")
	rels, _ := store.GetRelations(ctx, "A", RelationDirectionOutgoing)
	if len(rels) != 0 {
		t.Errorf("删除关系后 A 的出边关系应为空, 实际 %d (边列表残留悬空 relID)", len(rels))
	}
}

// ============================================================
// 10. MemoryGraphStore: AddRelation 引用不存在实体应报错 (正常错误路径)
// ============================================================

func TestMemoryGraphStore_AddRelationMissingEntities(t *testing.T) {
	store := NewMemoryGraphStore()
	ctx := context.Background()

	// 源/目标实体都不存在
	err := store.AddRelation(ctx, &Relation{SourceID: "nope", TargetID: "nope2", Type: RelationTypeIsA})
	if err == nil {
		t.Fatal("源实体不存在时 AddRelation 应报错")
	}

	_ = store.AddEntity(ctx, &Entity{ID: "A", Name: "A", Type: EntityTypeConcept})
	err = store.AddRelation(ctx, &Relation{SourceID: "A", TargetID: "missing", Type: RelationTypeIsA})
	if err == nil {
		t.Fatal("目标实体不存在时 AddRelation 应报错")
	}
}

// ============================================================
// 11. MemoryGraphStore: GetNeighbors / GetSubgraph 边界 (depth=0, 负数)
// ============================================================

func TestMemoryGraphStore_GetNeighborsDepthBoundary(t *testing.T) {
	store := NewMemoryGraphStore()
	ctx := context.Background()
	_ = store.AddEntity(ctx, &Entity{ID: "A", Name: "A", Type: EntityTypeConcept})
	_ = store.AddEntity(ctx, &Entity{ID: "B", Name: "B", Type: EntityTypeConcept})
	_ = store.AddRelation(ctx, &Relation{ID: "r", SourceID: "A", TargetID: "B", Type: RelationTypeRelatedTo})

	// depth=0: 不应遍历任何边
	got, _ := store.GetNeighbors(ctx, "A", 0)
	if len(got) != 0 {
		t.Errorf("depth=0 应无邻居, 实际 %d", len(got))
	}

	// depth 负数: 循环条件 d<depth 直接不进入, 应为空
	gotNeg, _ := store.GetNeighbors(ctx, "A", -1)
	if len(gotNeg) != 0 {
		t.Errorf("depth=-1 应无邻居, 实际 %d", len(gotNeg))
	}

	// depth=1: 应找到 B
	got1, _ := store.GetNeighbors(ctx, "A", 1)
	if len(got1) != 1 || got1[0].ID != "B" {
		t.Errorf("depth=1 应找到 B, 实际 %+v", got1)
	}
}

// ============================================================
// 12. MemoryGraphStore: 并发读写竞态 (-race 下验证)
// ============================================================

func TestMemoryGraphStore_ConcurrentReadWrite(t *testing.T) {
	store := NewMemoryGraphStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := "e" + string(rune('A'+n%26)) + string(rune('0'+n))
			_ = store.AddEntity(ctx, &Entity{ID: id, Name: id, Type: EntityTypeConcept})
			_, _ = store.GetEntity(ctx, id)
			_, _ = store.SearchEntities(ctx, EntityQuery{})
			_, _ = store.Stats(ctx)
		}(i)
	}
	wg.Wait()
}

// ============================================================
// 13. RuleBasedExtractor: 默认日期抽取 + 去重 + Unicode
// ============================================================

func TestRuleBasedExtractor_DateExtraction(t *testing.T) {
	e := NewRuleBasedExtractor()
	ctx := context.Background()

	text := "事件发生于 2024-01-15 和 2024年03月20日，又一次 2024-01-15 重复出现。"
	entities, relations, err := e.Extract(ctx, text)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// RuleBasedExtractor 不产生关系
	if relations != nil {
		t.Errorf("RuleBasedExtractor 不应产生关系, 实际 %d", len(relations))
	}

	// 应抽到 2 个不同日期 (重复的 2024-01-15 被 seen 去重)
	names := make(map[string]int)
	for _, ent := range entities {
		if ent.Type != EntityTypeDate {
			t.Errorf("规则抽取的实体类型应为 DATE, 实际 %s", ent.Type)
		}
		names[ent.Name]++
	}
	if names["2024-01-15"] != 1 {
		t.Errorf("重复日期 2024-01-15 应去重为 1, 实际 %d", names["2024-01-15"])
	}
	if len(names) != 2 {
		t.Errorf("应抽到 2 个不同日期, 实际 %d: %v", len(names), names)
	}
}

func TestRuleBasedExtractor_AddInvalidPattern(t *testing.T) {
	e := NewRuleBasedExtractor()
	// 非法正则
	if err := e.AddPattern(EntityTypeOther, "[invalid("); err == nil {
		t.Error("非法正则模式应返回错误")
	}
}

// ============================================================
// 14. LLMEntityExtractor: 解析响应的健壮性 (空/畸形/孤立关系)
// ============================================================

// 由于 llm.Provider 接口较大, 直接对未导出方法 parseExtractionResponse 做白盒测试更稳妥。
// LLMEntityExtractor 同包, 可直接构造并调用未导出方法。

func TestLLMExtractor_ParseResponse_WellFormed(t *testing.T) {
	e := NewLLMEntityExtractor(nil)
	entities, relations, err := e.parseExtractionResponse(
		"ENTITY: Alice | PERSON | 一个人\n" +
			"ENTITY: Acme | ORGANIZATION | 公司\n" +
			"RELATION: Alice | WORKS_FOR | Acme\n",
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entities) != 2 {
		t.Fatalf("应解析 2 个实体, 实际 %d", len(entities))
	}
	if len(relations) != 1 {
		t.Fatalf("应解析 1 个关系, 实际 %d", len(relations))
	}
	if relations[0].SourceID != entities[0].ID || relations[0].TargetID != entities[1].ID {
		t.Errorf("关系应正确连接 Alice->Acme")
	}
}

func TestLLMExtractor_ParseResponse_OrphanRelation(t *testing.T) {
	e := NewLLMEntityExtractor(nil)
	// 关系引用了未声明的实体, 应被丢弃 (不报错)
	entities, relations, err := e.parseExtractionResponse(
		"RELATION: Ghost | IS_A | Phantom\n",
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entities) != 0 {
		t.Errorf("无 ENTITY 行时不应有实体, 实际 %d", len(entities))
	}
	if len(relations) != 0 {
		t.Errorf("孤立关系 (两端实体未声明) 应被丢弃, 实际 %d", len(relations))
	}
}

func TestLLMExtractor_ParseResponse_EmptyAndMalformed(t *testing.T) {
	e := NewLLMEntityExtractor(nil)
	cases := []string{
		"",
		"   \n  \n",
		"ENTITY: OnlyName\n",          // 缺少类型字段 (parts<2 → 跳过)
		"RELATION: A | IS_A\n",        // 关系字段不足 (parts<3 → 跳过)
		"garbage line without prefix", // 无前缀
	}
	for _, c := range cases {
		ents, rels, err := e.parseExtractionResponse(c)
		if err != nil {
			t.Errorf("parse(%q) 不应报错: %v", c, err)
		}
		if len(ents) != 0 || len(rels) != 0 {
			t.Errorf("parse(%q) 应解析出空, 实际 ents=%d rels=%d", c, len(ents), len(rels))
		}
	}
}

// TestLLMExtractor_ParseResponse_EmptyEntityName 验证空实体名也会被接受 (潜在脏数据)
func TestLLMExtractor_ParseResponse_EmptyEntityName(t *testing.T) {
	e := NewLLMEntityExtractor(nil)
	// "ENTITY:  | PERSON" -> name 为空字符串, 但 parts>=2, 仍会创建空名实体
	ents, _, _ := e.parseExtractionResponse("ENTITY:  | PERSON | desc\n")
	if len(ents) == 1 && ents[0].Name == "" {
		t.Logf("解析器接受空实体名 (Name=\"\"), 产生脏实体, 缺乏名称非空校验")
	}
}

// ============================================================
// 15. GraphIndexer: Index / Delete 链路 + 抽取器出错
// ============================================================

// errExtractor 总是返回错误
type errExtractor struct{}

func (errExtractor) Extract(ctx context.Context, text string) ([]*Entity, []*Relation, error) {
	return nil, nil, errors.New("extract boom")
}

func TestGraphIndexer_ExtractError(t *testing.T) {
	store := NewMemoryGraphStore()
	idx := NewGraphIndexer(store, WithExtractor(errExtractor{}))

	err := idx.Index(context.Background(), []rag.Document{{ID: "d1", Content: "x"}})
	if err == nil {
		t.Fatal("抽取器出错时 GraphIndexer.Index 应返回错误")
	}
}

// fixedExtractor 返回固定实体/关系, 用于验证 SourceDocIDs 注入与存储链路
type fixedExtractor struct{}

func (fixedExtractor) Extract(ctx context.Context, text string) ([]*Entity, []*Relation, error) {
	a := &Entity{ID: "A", Name: "A", Type: EntityTypeConcept}
	b := &Entity{ID: "B", Name: "B", Type: EntityTypeConcept}
	r := &Relation{ID: "r", SourceID: "A", TargetID: "B", Type: RelationTypeRelatedTo}
	return []*Entity{a, b}, []*Relation{r}, nil
}

func TestGraphIndexer_IndexAndDelete(t *testing.T) {
	store := NewMemoryGraphStore()
	idx := NewGraphIndexer(store, WithExtractor(fixedExtractor{}))
	ctx := context.Background()

	if err := idx.Index(ctx, []rag.Document{{ID: "doc1", Content: "anything"}}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	cnt, _ := idx.Count(ctx)
	if cnt != 2 {
		t.Errorf("索引后实体数应为 2, 实际 %d", cnt)
	}

	// 验证 SourceDocIDs 被注入
	ents, _ := store.SearchEntities(ctx, EntityQuery{})
	for _, e := range ents {
		found := false
		for _, sid := range e.SourceDocIDs {
			if sid == "doc1" {
				found = true
			}
		}
		if !found {
			t.Errorf("实体 %s 缺少来源文档 doc1: %v", e.Name, e.SourceDocIDs)
		}
	}

	// Delete 应删除来自 doc1 的实体
	if err := idx.Delete(ctx, []string{"doc1"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	cnt2, _ := idx.Count(ctx)
	if cnt2 != 0 {
		t.Errorf("删除 doc1 后实体数应为 0, 实际 %d", cnt2)
	}
}

// TestGraphIndexer_DeleteIgnoresEntityQueryLimit 暴露 Delete 内 SearchEntities 未带 docID 过滤,
// 而是拉取所有实体 (Limit=DefaultEntityQueryLimit) 再逐个比对 SourceDocIDs。
// 当实体数超过 DefaultEntityQueryLimit(10000) 时, 超出部分的实体永远删不掉。
// 此处用小规模验证逻辑正确性 (功能层面), 并记录可扩展性隐患。
func TestGraphIndexer_DeleteMultiDocReindexCost(t *testing.T) {
	store := NewMemoryGraphStore()
	idx := NewGraphIndexer(store, WithExtractor(fixedExtractor{}))
	ctx := context.Background()

	// fixedExtractor 每篇文档都产出相同 ID 的 A/B 实体 → 后写覆盖,
	// 但 SourceDocIDs 会被 append (因为 AddEntity 直接覆盖, 实际只保留最后一篇的来源)
	_ = idx.Index(ctx, []rag.Document{{ID: "d1", Content: "a"}})
	_ = idx.Index(ctx, []rag.Document{{ID: "d2", Content: "b"}})

	// 因为实体 ID 固定为 A/B, 第二次 Index 覆盖了第一次的实体对象 (含其 SourceDocIDs)。
	// 删除 d1 时, 实体的 SourceDocIDs 里可能已不含 d1 → 删不掉, 暴露覆盖语义问题。
	ents, _ := store.SearchEntities(ctx, EntityQuery{})
	hasD1 := false
	for _, e := range ents {
		for _, sid := range e.SourceDocIDs {
			if sid == "d1" {
				hasD1 = true
			}
		}
	}
	if !hasD1 {
		t.Logf("固定 ID 实体被后续文档覆盖, 第一篇文档 d1 的来源信息丢失, Delete(d1) 将无法清理")
	}
}

// ============================================================
// 16. GraphRetriever: 空查询 / 无匹配 / 特殊正则字符
// ============================================================

func TestGraphRetriever_NoMatch(t *testing.T) {
	store := NewMemoryGraphStore()
	r := NewGraphRetriever(store)
	gc, err := r.Retrieve(context.Background(), "doesnotexist")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if gc == nil {
		t.Fatal("Retrieve 应返回非 nil GraphContext")
	}
	if len(gc.Entities) != 0 {
		t.Errorf("无匹配应返回空实体, 实际 %d", len(gc.Entities))
	}
}

// TestGraphRetriever_RegexSpecialChars 验证查询包含正则元字符时的健壮性。
// 回归: W3-35
// GraphRetriever.Retrieve 用 "*"+query+"*" 拼成 NamePattern, SearchEntities 将其
// 当作通配符转正则。若 query 含 "(" 等元字符且未转义, regexp.Compile 会失败 → 返回错误。
// 修复后: Retrieve 对用户查询做 regexp.QuoteMeta 转义后再拼通配符。
func TestGraphRetriever_RegexSpecialChars(t *testing.T) {
	store := NewMemoryGraphStore()
	ctx := context.Background()
	_ = store.AddEntity(ctx, &Entity{Name: "C++ Lang", Type: EntityTypeTechnology})

	r := NewGraphRetriever(store)
	// 查询包含未配对括号, 会被原样塞进正则
	_, err := r.Retrieve(ctx, "foo(bar")
	if err != nil {
		t.Errorf("查询含正则元字符 '(' 导致 Retrieve 报错 (用户输入未转义直接进正则): %v", err)
	}
}

// TestSearchEntities_InvalidPattern 直接验证 SearchEntities 对非法 NamePattern 报错
func TestSearchEntities_InvalidPattern(t *testing.T) {
	store := NewMemoryGraphStore()
	ctx := context.Background()
	_ = store.AddEntity(ctx, &Entity{Name: "X", Type: EntityTypeConcept})

	_, err := store.SearchEntities(ctx, EntityQuery{NamePattern: "[unclosed"})
	if err == nil {
		t.Error("非法 NamePattern 应返回错误")
	}
}

// ============================================================
// 17. GraphContext.ToText: 空 / 仅实体 / 完整
// ============================================================

func TestGraphContext_ToText(t *testing.T) {
	empty := &GraphContext{}
	if empty.ToText() != "" {
		t.Errorf("空上下文 ToText 应为空串, 实际 %q", empty.ToText())
	}

	gc := &GraphContext{
		Entities: []*Entity{{Name: "Go", Type: EntityTypeTechnology, Description: "语言"}},
		Triples:  []Triple{{Subject: "Go", Predicate: RelationTypeUsedFor, Object: "后端"}},
	}
	txt := gc.ToText()
	if !strings.Contains(txt, "Go") || !strings.Contains(txt, "后端") {
		t.Errorf("ToText 应包含实体与三元组内容, 实际: %q", txt)
	}
}

// ============================================================
// 18. SQL: QueryBuilder 纯逻辑 (table-driven)
// ============================================================

func TestQueryBuilder_Build(t *testing.T) {
	tests := []struct {
		name  string
		build func() *QueryBuilder
		want  string
	}{
		{
			name:  "默认全选",
			build: func() *QueryBuilder { return NewQueryBuilder("users") },
			want:  "SELECT * FROM users",
		},
		{
			name: "选列+条件+排序+分页",
			build: func() *QueryBuilder {
				return NewQueryBuilder("users").
					Select("id", "name").
					Where("age > 18").
					Where("status = 'active'").
					OrderBy("id DESC").
					Limit(10).
					Offset(5)
			},
			want: "SELECT id, name FROM users WHERE age > 18 AND status = 'active' ORDER BY id DESC LIMIT 10 OFFSET 5",
		},
		{
			name: "JOIN+GROUP+HAVING",
			build: func() *QueryBuilder {
				return NewQueryBuilder("orders").
					Join("JOIN users ON users.id = orders.uid").
					GroupBy("orders.uid").
					Having("COUNT(*) > 3")
			},
			want: "SELECT * FROM orders JOIN users ON users.id = orders.uid GROUP BY orders.uid HAVING COUNT(*) > 3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.build().Build()
			if got != tt.want {
				t.Errorf("Build()\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// TestQueryBuilder_NegativeLimitOffset 验证负数 limit/offset 被忽略 (边界)
func TestQueryBuilder_NegativeLimitOffset(t *testing.T) {
	got := NewQueryBuilder("t").Limit(-1).Offset(-1).Build()
	if strings.Contains(got, "LIMIT") || strings.Contains(got, "OFFSET") {
		t.Errorf("负数 limit/offset 应被忽略, 实际: %q", got)
	}
}

// TestQueryBuilder_SelectEmpty 验证 Select() 不传参数时的行为 (空 SELECT 子句)。
// 回归: W3-36
// Select() 会把 columns 设为空切片 → Build 产生 "SELECT  FROM t" (语法错误)。
// 修复后: Select() 无参数时退回默认 "*"。
func TestQueryBuilder_SelectEmpty(t *testing.T) {
	got := NewQueryBuilder("t").Select().Build()
	// 期望: 退回 "*" 或保持默认; 实际会产生 "SELECT  FROM t"
	if got == "SELECT  FROM t" {
		t.Errorf("Select() 无参数产生非法 SQL: %q (应退回 * 或保留默认列)", got)
	}
}

// ============================================================
// 19. SQL: inferType / inferColumns 纯逻辑
// ============================================================

func TestInferType(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want string
	}{
		{"nil", nil, "TEXT"},
		{"int", 42, "INTEGER"},
		{"int64", int64(42), "INTEGER"},
		{"float64", 3.14, "REAL"},
		{"float32", float32(3.14), "REAL"},
		{"bool", true, "BOOLEAN"},
		{"string", "hi", "TEXT"},
		{"uint默认TEXT", uint(5), "TEXT"}, // 注意: uint 未被覆盖, 落到 default TEXT
		{"slice默认TEXT", []int{1}, "TEXT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferType(tt.val); got != tt.want {
				t.Errorf("inferType(%v)=%q want %q", tt.val, got, tt.want)
			}
		})
	}
}

// TestInferType_UintNotHandled 单独标记: 无符号整数被推断为 TEXT 而非 INTEGER (类型推断不完整)
func TestInferType_UintTreatedAsText(t *testing.T) {
	if inferType(uint64(100)) != "TEXT" {
		t.Skip("uint64 已被处理")
	}
	t.Logf("inferType 未处理无符号整数 (uint/uint64), 被错误推断为 TEXT 而非 INTEGER")
}

func TestInferColumns(t *testing.T) {
	row := map[string]any{
		"id":     1,
		"name":   "alice",
		"score":  9.5,
		"active": true,
		"note":   nil,
	}
	cols := inferColumns(row)
	want := map[string]string{
		"id":     "INTEGER",
		"name":   "TEXT",
		"score":  "REAL",
		"active": "BOOLEAN",
		"note":   "TEXT",
	}
	for k, v := range want {
		if cols[k] != v {
			t.Errorf("inferColumns[%q]=%q want %q", k, cols[k], v)
		}
	}
}

// ============================================================
// 20. computeChecksum: Unicode / 超长 / 一致性
// ============================================================

func TestComputeChecksum_UnicodeAndLong(t *testing.T) {
	// 空内容
	if computeChecksum("") != "empty" {
		t.Errorf("空内容应返回 'empty'")
	}
	// Unicode 内容稳定性
	u1 := computeChecksum("你好世界🌍")
	u2 := computeChecksum("你好世界🌍")
	if u1 != u2 {
		t.Errorf("相同 Unicode 内容校验和应一致")
	}
	if u1 == computeChecksum("你好世界") {
		t.Errorf("不同 Unicode 内容校验和应不同")
	}
	// 超长内容不应 panic
	long := strings.Repeat("x", 1<<20) // 1MB
	if computeChecksum(long) == "" {
		t.Errorf("超长内容校验和不应为空")
	}
}

// TestComputeChecksum_EmptySentinelCollision 验证 "empty" 哨兵值的潜在碰撞风险。
// 空字符串返回字面量 "empty"。理论上不会与任何 SHA256(content) 输出相同 (SHA256 为 64 位十六进制),
// 但若未来有内容真的产生哈希 "empty" 则无法区分。此处仅验证非空内容不会得到 "empty"。
func TestComputeChecksum_NonEmptyNeverSentinel(t *testing.T) {
	if computeChecksum("a") == "empty" {
		t.Error("非空内容不应返回哨兵值 'empty'")
	}
}

