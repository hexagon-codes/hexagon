package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FileStore 基于文件系统的持久化 MemoryStore 实现
//
// 将每条记忆存储为一个独立的 JSON 文件，目录结构按命名空间组织。
// 适合单机部署和快速开始，无需外部依赖。
//
// 目录结构示例：
//
//	baseDir/
//	├── users/
//	│   └── u123/
//	│       ├── preferences.json
//	│       └── history.json
//	└── agents/
//	    └── a1/
//	        └── config.json
//
// 特性：
//   - 命名空间映射为子目录
//   - 每条记忆一个 JSON 文件
//   - 原子写入（tmp + rename），防止写入中断导致损坏
//   - 支持 TTL 过期（启动时和定期扫描清理过期文件）
//   - 后台 goroutine 定期清理过期条目
//
// 线程安全：所有方法都是并发安全的。
//
// 使用示例：
//
//	store, err := NewFileStore("/data/memory")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer store.Close()
//
//	store.Put(ctx, []string{"users", "u1"}, "prefs", map[string]any{
//	    "theme": "dark",
//	})
type FileStore struct {
	// baseDir 数据存储根目录
	baseDir string

	mu sync.RWMutex

	// done 用于停止后台清理协程
	done chan struct{}

	// cleanupInterval TTL 清理间隔
	cleanupInterval time.Duration
}

// FileStoreOption 是 FileStore 的配置选项
type FileStoreOption func(*FileStore)

// WithFileCleanupInterval 设置 TTL 过期清理间隔
//
// 默认每 5 分钟清理一次过期条目
func WithFileCleanupInterval(d time.Duration) FileStoreOption {
	return func(s *FileStore) {
		s.cleanupInterval = d
	}
}

// NewFileStore 创建文件存储实例
//
// baseDir: 数据存储根目录，不存在时自动创建。
// 会启动一个后台协程定期清理过期条目，使用完毕后应调用 Close() 释放资源。
func NewFileStore(baseDir string, opts ...FileStoreOption) (*FileStore, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建存储目录失败: %w", err)
	}

	s := &FileStore{
		baseDir:         baseDir,
		done:            make(chan struct{}),
		cleanupInterval: 5 * time.Minute,
	}

	for _, opt := range opts {
		opt(s)
	}

	go s.cleanupLoop()

	return s, nil
}

// Put 存储一条记忆
func (s *FileStore) Put(ctx context.Context, namespace []string, key string, value map[string]any, opts ...PutOption) error {
	_, err := s.put(ctx, namespace, key, value, nil, opts...)
	return err
}

// PutIfRevision 保证同一 FileStore 实例内的条件更新，不宣称跨进程事务能力。
func (s *FileStore) PutIfRevision(ctx context.Context, namespace []string, key string, value map[string]any, expected uint64, opts ...PutOption) (*Item, error) {
	return s.put(ctx, namespace, key, value, &expected, opts...)
}

func (s *FileStore) put(ctx context.Context, namespace []string, key string, value map[string]any, expected *uint64, opts ...PutOption) (*Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, fmt.Errorf("key 不能为空")
	}
	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}

	options := applyPutOptions(opts)
	now := time.Now()

	item := &fileItem{
		Namespace: namespace,
		Key:       key,
		Value:     value,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if options.ttl > 0 {
		expiresAt := now.Add(options.ttl)
		item.ExpiresAt = &expiresAt
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.namespaceDir(namespace)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建命名空间目录失败: %w", err)
	}

	filePath := s.itemPath(namespace, key)

	existing, err := s.readItemUnlocked(filePath)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.isExpired() {
		existing = nil
	}
	var current uint64
	if existing != nil {
		current = existing.Revision
	}
	if expected != nil && (*expected != current || *expected == 0 && existing != nil) {
		return nil, ErrRevisionConflict
	}
	if current == ^uint64(0) {
		return nil, fmt.Errorf("memory store: revision overflow")
	}
	item.Revision = current + 1
	if existing != nil {
		item.CreatedAt = existing.CreatedAt
	}

	if err := s.writeItemUnlocked(filePath, item); err != nil {
		return nil, err
	}
	return item.toItem(), nil
}

// Get 获取一条记忆
func (s *FileStore) Get(ctx context.Context, namespace []string, key string) (*Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	filePath := s.itemPath(namespace, key)
	fi, err := s.readItemUnlocked(filePath)
	if err != nil {
		return nil, err
	}
	if fi == nil {
		return nil, nil
	}

	// 检查过期
	if fi.isExpired() {
		// 惰性清理需要写锁，这里先返回 nil，后台清理负责删除文件
		return nil, nil
	}

	return fi.toItem(), nil
}

// Search 搜索记忆
func (s *FileStore) Search(ctx context.Context, namespace []string, query *SearchQuery) ([]*SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query == nil {
		return nil, nil
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 10
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	items, err := s.listItemsUnlocked(namespace)
	if err != nil {
		return nil, err
	}

	var results []*SearchResult
	for _, fi := range items {
		if fi.isExpired() {
			continue
		}

		// 元数据过滤
		if !matchFilter(fi.Value, query.Filter) {
			continue
		}

		// 关键词搜索
		score := float64(1.0)
		if query.Query != "" {
			matched, s := keywordMatch(fi.Value, query.Query)
			if !matched {
				continue
			}
			score = s
		}

		results = append(results, &SearchResult{
			Item:  fi.toItem(),
			Score: score,
		})
	}

	// 按分数降序排序
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// 分页
	start := max(query.Offset, 0)
	if start >= len(results) {
		return nil, nil
	}
	end := len(results)
	if start+limit < end {
		end = start + limit
	}

	return results[start:end], nil
}

// Delete 删除一条记忆
func (s *FileStore) Delete(ctx context.Context, namespace []string, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.itemPath(namespace, key)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除记忆文件失败: %w", err)
	}
	return nil
}

// List 列出命名空间下的所有记忆
func (s *FileStore) List(ctx context.Context, namespace []string, opts ...ListOption) ([]*Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	options := applyListOptions(opts)

	s.mu.RLock()
	defer s.mu.RUnlock()

	fileItems, err := s.listItemsUnlocked(namespace)
	if err != nil {
		return nil, err
	}

	var items []*Item
	for _, fi := range fileItems {
		if fi.isExpired() {
			continue
		}

		// 键前缀过滤
		if options.prefix != "" && !strings.HasPrefix(fi.Key, options.prefix) {
			continue
		}

		items = append(items, fi.toItem())
	}

	// 按更新时间排序
	sort.Slice(items, func(i, j int) bool {
		if options.orderDesc {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].UpdatedAt.Before(items[j].UpdatedAt)
	})

	// 分页
	start := max(options.offset, 0)
	if start >= len(items) {
		return nil, nil
	}
	end := len(items)
	if options.limit > 0 && start+options.limit < end {
		end = start + options.limit
	}

	return items[start:end], nil
}

// DeleteNamespace 删除整个命名空间及其下所有记忆
func (s *FileStore) DeleteNamespace(ctx context.Context, namespace []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := s.namespaceDir(namespace)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除命名空间目录失败: %w", err)
	}
	return nil
}

// Close 关闭存储，停止后台清理协程
func (s *FileStore) Close() error {
	close(s.done)
	return nil
}

// ============== 内部类型 ==============

// fileItem 文件存储的记忆条目（JSON 序列化格式）
type fileItem struct {
	Revision  uint64         `json:"revision"`
	Namespace []string       `json:"namespace"`
	Key       string         `json:"key"`
	Value     map[string]any `json:"value"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	ExpiresAt *time.Time     `json:"expires_at,omitempty"`
}

func (fi *fileItem) isExpired() bool {
	if fi.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*fi.ExpiresAt)
}

func (fi *fileItem) toItem() *Item {
	item := &Item{
		Revision:  fi.Revision,
		Key:       fi.Key,
		CreatedAt: fi.CreatedAt,
		UpdatedAt: fi.UpdatedAt,
		ExpiresAt: fi.ExpiresAt,
	}

	if fi.Namespace != nil {
		item.Namespace = make([]string, len(fi.Namespace))
		copy(item.Namespace, fi.Namespace)
	}

	if fi.Value != nil {
		item.Value = make(map[string]any, len(fi.Value))
		for k, v := range fi.Value {
			item.Value[k] = v
		}
	}

	return item
}

// ============== 内部方法 ==============

// namespaceDir 返回命名空间对应的目录路径
func (s *FileStore) namespaceDir(namespace []string) string {
	parts := make([]string, 0, len(namespace)+1)
	parts = append(parts, s.baseDir)
	parts = append(parts, namespace...)
	return filepath.Join(parts...)
}

// itemPath 返回记忆条目对应的文件路径
func (s *FileStore) itemPath(namespace []string, key string) string {
	return filepath.Join(s.namespaceDir(namespace), key+".json")
}

// readItemUnlocked 从文件读取记忆条目（调用方负责持锁）
func (s *FileStore) readItemUnlocked(filePath string) (*fileItem, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取记忆文件失败: %w", err)
	}

	var fi fileItem
	if err := decodeJSONNumbers(data, &fi); err != nil {
		return nil, fmt.Errorf("解析记忆文件失败: %w", err)
	}
	return &fi, nil
}

// writeItemUnlocked 原子写入记忆条目到文件（调用方负责持锁）
func (s *FileStore) writeItemUnlocked(filePath string, fi *fileItem) error {
	data, err := json.MarshalIndent(fi, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化记忆失败: %w", err)
	}

	// 原子写入：先写临时文件，再 rename
	tmp := filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, filePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("重命名文件失败: %w", err)
	}
	return nil
}

// listItemsUnlocked 列出命名空间目录下所有记忆条目（调用方负责持锁）
func (s *FileStore) listItemsUnlocked(namespace []string) ([]*fileItem, error) {
	dir := s.namespaceDir(namespace)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取命名空间目录失败: %w", err)
	}

	var items []*fileItem
	for _, entry := range entries {
		// 只处理 .json 文件，跳过目录和临时文件
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		fi, err := s.readItemUnlocked(filePath)
		if err != nil {
			// 跳过不可读的文件，保持鲁棒
			continue
		}
		if fi != nil {
			items = append(items, fi)
		}
	}

	return items, nil
}

// cleanupLoop 后台定期清理过期条目
func (s *FileStore) cleanupLoop() {
	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.cleanup()
		}
	}
}

// cleanup 扫描并删除所有过期的记忆文件
func (s *FileStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	_ = filepath.Walk(s.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过不可访问的路径
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".json") {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".tmp") {
			return nil
		}

		fi, err := s.readItemUnlocked(path)
		if err != nil || fi == nil {
			return nil
		}

		if fi.isExpired() {
			_ = os.Remove(path)
		}
		return nil
	})
}

// validateNamespace 校验命名空间路径，防止路径穿越
func validateNamespace(namespace []string) error {
	for _, part := range namespace {
		if part == "" {
			return fmt.Errorf("命名空间段不能为空")
		}
		if part == "." || part == ".." {
			return fmt.Errorf("命名空间段不能为 '.' 或 '..'")
		}
		if strings.ContainsAny(part, `/\`) {
			return fmt.Errorf("命名空间段不能包含路径分隔符")
		}
	}
	return nil
}

// 确保实现了 MemoryStore 接口
var _ MemoryStore = (*FileStore)(nil)
