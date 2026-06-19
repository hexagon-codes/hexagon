// Package indexer 提供 RAG 系统的文档索引器
//
// Indexer 用于将文档向量化并存储到向量数据库：
//   - VectorIndexer: 基于向量存储的索引器
//   - BatchIndexer: 批量索引器
package indexer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/store/vector"
	"github.com/hexagon-codes/hexagon/internal/util"
	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/toolkit/util/hash"
)

// ============== VectorIndexer ==============

// VectorIndexer 向量索引器
type VectorIndexer struct {
	store     vector.Store
	embedder  vector.Embedder
	batchSize int
}

// VectorIndexerOption VectorIndexer 选项
type VectorIndexerOption func(*VectorIndexer)

// WithBatchSize 设置批量大小
func WithBatchSize(size int) VectorIndexerOption {
	return func(i *VectorIndexer) {
		i.batchSize = size
	}
}

// defaultBatchSize 索引器默认批量大小
const defaultBatchSize = 100

// NewVectorIndexer 创建向量索引器
func NewVectorIndexer(store vector.Store, embedder vector.Embedder, opts ...VectorIndexerOption) *VectorIndexer {
	idx := &VectorIndexer{
		store:     store,
		embedder:  embedder,
		batchSize: defaultBatchSize,
	}
	for _, opt := range opts {
		opt(idx)
	}
	// batchSize 下界校验: 0 或负数会导致 Index 循环 start 永不前进 (死循环)
	// 或 docs[start:end] 切片越界 panic, 回退到默认值。
	if idx.batchSize <= 0 {
		idx.batchSize = defaultBatchSize
	}
	return idx
}

// Index 索引文档
func (i *VectorIndexer) Index(ctx context.Context, docs []rag.Document) error {
	if len(docs) == 0 {
		return nil
	}

	// 分批处理
	for start := 0; start < len(docs); start += i.batchSize {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		end := start + i.batchSize
		if end > len(docs) {
			end = len(docs)
		}

		batch := docs[start:end]

		// 提取需要嵌入的文本
		texts := make([]string, len(batch))
		for j, doc := range batch {
			texts[j] = doc.Content
		}

		// 生成向量
		embeddings, err := i.embedder.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("failed to embed documents: %w", err)
		}

		// 断言返回向量数量与输入文本数量一致。
		// 异常的 Provider 可能返回数量不等长的结果, 若不校验, 下方
		// embeddings[j] 在 j 超出 embeddings 长度时会发生越界 panic,
		// 拖垮整个进程; 这里改为返回明确错误而非崩溃。
		if len(embeddings) != len(texts) {
			return fmt.Errorf("embedding count mismatch: got %d embeddings for %d texts", len(embeddings), len(texts))
		}

		// 转换为 vector.Document
		vectorDocs := make([]vector.Document, len(batch))
		for j, doc := range batch {
			id := doc.ID
			if id == "" {
				id = util.GenerateID("doc")
			}
			vectorDocs[j] = vector.Document{
				ID:        id,
				Content:   doc.Content,
				Embedding: embeddings[j],
				Metadata:  doc.Metadata,
				CreatedAt: time.Now(),
			}
		}

		// 存储到向量数据库
		if err := i.store.Add(ctx, vectorDocs); err != nil {
			return fmt.Errorf("failed to add documents to store: %w", err)
		}
	}

	return nil
}

// Delete 删除文档
func (i *VectorIndexer) Delete(ctx context.Context, ids []string) error {
	return i.store.Delete(ctx, ids)
}

// Clear 清空索引
func (i *VectorIndexer) Clear(ctx context.Context) error {
	return i.store.Clear(ctx)
}

// Count 返回文档数量
func (i *VectorIndexer) Count(ctx context.Context) (int, error) {
	return i.store.Count(ctx)
}

var _ rag.Indexer = (*VectorIndexer)(nil)

// ============== ConcurrentIndexer ==============

// ConcurrentIndexer 并发索引器
type ConcurrentIndexer struct {
	store       vector.Store
	embedder    vector.Embedder
	batchSize   int
	concurrency int
}

// ConcurrentIndexerOption ConcurrentIndexer 选项
type ConcurrentIndexerOption func(*ConcurrentIndexer)

// WithConcurrentBatchSize 设置批量大小
func WithConcurrentBatchSize(size int) ConcurrentIndexerOption {
	return func(i *ConcurrentIndexer) {
		i.batchSize = size
	}
}

// WithConcurrency 设置并发数
func WithConcurrency(n int) ConcurrentIndexerOption {
	return func(i *ConcurrentIndexer) {
		i.concurrency = n
	}
}

// NewConcurrentIndexer 创建并发索引器
func NewConcurrentIndexer(store vector.Store, embedder vector.Embedder, opts ...ConcurrentIndexerOption) *ConcurrentIndexer {
	idx := &ConcurrentIndexer{
		store:       store,
		embedder:    embedder,
		batchSize:   defaultBatchSize,
		concurrency: 4,
	}
	for _, opt := range opts {
		opt(idx)
	}
	// batchSize 下界校验: 0 或负数会导致分批循环异常 (死循环或切片越界),
	// 回退到默认值。
	if idx.batchSize <= 0 {
		idx.batchSize = defaultBatchSize
	}
	// concurrency 下界校验: 0 或负数会导致 sem 容量非法 → goroutine 永久阻塞,
	// 回退到至少 1。
	if idx.concurrency <= 0 {
		idx.concurrency = 1
	}
	return idx
}

// Index 并发索引文档
func (i *ConcurrentIndexer) Index(ctx context.Context, docs []rag.Document) error {
	if len(docs) == 0 {
		return nil
	}

	// 分批
	var batches [][]rag.Document
	for start := 0; start < len(docs); start += i.batchSize {
		end := start + i.batchSize
		if end > len(docs) {
			end = len(docs)
		}
		batches = append(batches, docs[start:end])
	}

	// 并发处理
	errCh := make(chan error, len(batches))
	sem := make(chan struct{}, i.concurrency)

	var wg sync.WaitGroup
	for _, batch := range batches {
		wg.Add(1)
		go func(batch []rag.Document) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			if ctx.Err() != nil {
				errCh <- ctx.Err()
				return
			}

			// 生成向量
			texts := make([]string, len(batch))
			for j, doc := range batch {
				texts[j] = doc.Content
			}

			embeddings, err := i.embedder.Embed(ctx, texts)
			if err != nil {
				errCh <- err
				return
			}

			// 断言返回向量数量与输入文本数量一致。
			// 并发路径的越界 panic 发生在库内部 worker goroutine 中,
			// 调用方无法 recover, 会直接 crash 整个进程; 这里在 worker
			// 内部校验后通过 error channel 返回错误, 避免崩溃。
			if len(embeddings) != len(texts) {
				errCh <- fmt.Errorf("embedding count mismatch: got %d embeddings for %d texts", len(embeddings), len(texts))
				return
			}

			// 转换并存储
			vectorDocs := make([]vector.Document, len(batch))
			for j, doc := range batch {
				id := doc.ID
				if id == "" {
					id = util.GenerateID("doc")
				}
				vectorDocs[j] = vector.Document{
					ID:        id,
					Content:   doc.Content,
					Embedding: embeddings[j],
					Metadata:  doc.Metadata,
					CreatedAt: time.Now(),
				}
			}

			if err := i.store.Add(ctx, vectorDocs); err != nil {
				errCh <- err
				return
			}
		}(batch)
	}

	wg.Wait()
	close(errCh)

	// 收集错误
	for err := range errCh {
		if err != nil {
			return err
		}
	}

	return nil
}

// Delete 删除文档
func (i *ConcurrentIndexer) Delete(ctx context.Context, ids []string) error {
	return i.store.Delete(ctx, ids)
}

// Clear 清空索引
func (i *ConcurrentIndexer) Clear(ctx context.Context) error {
	return i.store.Clear(ctx)
}

// Count 返回文档数量
func (i *ConcurrentIndexer) Count(ctx context.Context) (int, error) {
	return i.store.Count(ctx)
}

var _ rag.Indexer = (*ConcurrentIndexer)(nil)

// ============== IncrementalIndexer ==============

// IncrementalIndexer 增量索引器
// 只索引新增或变更的文档
type IncrementalIndexer struct {
	store     vector.Store
	embedder  vector.Embedder
	checksums map[string]string // ID -> checksum
	mu        sync.RWMutex
	batchSize int
}

// IncrementalIndexerOption IncrementalIndexer 选项
type IncrementalIndexerOption func(*IncrementalIndexer)

// WithIncrementalBatchSize 设置批量大小
func WithIncrementalBatchSize(size int) IncrementalIndexerOption {
	return func(i *IncrementalIndexer) {
		i.batchSize = size
	}
}

// NewIncrementalIndexer 创建增量索引器
func NewIncrementalIndexer(store vector.Store, embedder vector.Embedder, opts ...IncrementalIndexerOption) *IncrementalIndexer {
	idx := &IncrementalIndexer{
		store:     store,
		embedder:  embedder,
		checksums: make(map[string]string),
		batchSize: 100,
	}
	for _, opt := range opts {
		opt(idx)
	}
	return idx
}

// Index 增量索引文档
func (i *IncrementalIndexer) Index(ctx context.Context, docs []rag.Document) error {
	// 过滤出需要更新的文档
	var toIndex []rag.Document
	// batchSeen 记录本批次内已纳入 toIndex 的文档 ID, 防止同一批次内出现
	// 多篇相同 (派生) ID 的文档被重复索引 → store 端重复写入同一 ID 的文档。
	// 仅靠 i.checksums 无法去重, 因为本批次的 checksum 要等索引成功后才写入,
	// 同批多篇相同 ID 文档在过滤时 i.checksums 中尚不存在该 key, 会全部通过。
	batchSeen := make(map[string]struct{}, len(docs))

	i.mu.RLock()
	for _, doc := range docs {
		// 空 ID 文档先生成稳定 ID 再作 checksum key:
		// 否则所有空 ID 文档共享空字符串槽位 checksums[""], 互相覆盖状态,
		// 且底层 VectorIndexer 会为每篇生成不同随机 ID 造成重复存储。
		// 基于内容派生稳定 ID, 使相同内容的空 ID 文档落到同一槽位, 实现去重。
		if doc.ID == "" {
			doc.ID = stableDocID(doc.Content)
		}
		// 同批次内重复 ID 去重: 已纳入的 ID 直接跳过。
		if _, dup := batchSeen[doc.ID]; dup {
			continue
		}
		checksum := computeChecksum(doc.Content)
		if existing, ok := i.checksums[doc.ID]; !ok || existing != checksum {
			batchSeen[doc.ID] = struct{}{}
			toIndex = append(toIndex, doc)
		}
	}
	i.mu.RUnlock()

	if len(toIndex) == 0 {
		return nil
	}

	// 使用基础索引器索引。toIndex 中的文档已带有稳定 ID,
	// VectorIndexer 不会再为其生成随机 ID, store 端 ID 与 checksum key 保持一致。
	baseIndexer := NewVectorIndexer(i.store, i.embedder, WithBatchSize(i.batchSize))
	if err := baseIndexer.Index(ctx, toIndex); err != nil {
		return err
	}

	// 更新校验和
	i.mu.Lock()
	for _, doc := range toIndex {
		i.checksums[doc.ID] = computeChecksum(doc.Content)
	}
	i.mu.Unlock()

	return nil
}

// Delete 删除文档
func (i *IncrementalIndexer) Delete(ctx context.Context, ids []string) error {
	if err := i.store.Delete(ctx, ids); err != nil {
		return err
	}

	i.mu.Lock()
	for _, id := range ids {
		delete(i.checksums, id)
	}
	i.mu.Unlock()

	return nil
}

// Clear 清空索引
func (i *IncrementalIndexer) Clear(ctx context.Context) error {
	if err := i.store.Clear(ctx); err != nil {
		return err
	}

	i.mu.Lock()
	i.checksums = make(map[string]string)
	i.mu.Unlock()

	return nil
}

// Count 返回文档数量
func (i *IncrementalIndexer) Count(ctx context.Context) (int, error) {
	return i.store.Count(ctx)
}

var _ rag.Indexer = (*IncrementalIndexer)(nil)

// ============== 辅助函数 ==============

// computeChecksum 计算内容校验和
// 使用 SHA256 哈希，确保内容任意位置的变化都能被检测到
func computeChecksum(content string) string {
	if len(content) == 0 {
		return "empty"
	}
	return hash.SHA256(content)
}

// stableDocID 为空 ID 文档基于内容派生稳定的文档 ID。
//
// 增量索引器要求空 ID 文档拥有可重现的 ID, 以便:
//   - 多个空 ID 文档不再共享空字符串 checksum 槽位互相覆盖;
//   - 相同内容的文档跨批次落到同一 store ID, 实现幂等去重。
//
// 采用内容 SHA256 哈希前缀作为后缀, 保证相同内容得到相同 ID。
func stableDocID(content string) string {
	return "doc-" + computeChecksum(content)
}
