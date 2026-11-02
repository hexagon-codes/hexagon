package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestFileStore(t *testing.T, opts ...FileStoreOption) *FileStore {
	t.Helper()
	s, err := NewFileStore(t.TempDir(), opts...)
	if err != nil {
		t.Fatalf("创建 FileStore 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFileStore_PutAndGet(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()

	err := s.Put(ctx, []string{"users", "u1"}, "prefs", map[string]any{
		"theme": "dark",
		"lang":  "zh-CN",
	})
	if err != nil {
		t.Fatalf("Put 失败: %v", err)
	}

	item, err := s.Get(ctx, []string{"users", "u1"}, "prefs")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if item == nil {
		t.Fatal("Get 返回 nil")
	}
	if item.Key != "prefs" {
		t.Errorf("Key = %q, 期望 %q", item.Key, "prefs")
	}
	if item.Value["theme"] != "dark" {
		t.Errorf("Value[theme] = %v, 期望 %q", item.Value["theme"], "dark")
	}
	if item.Value["lang"] != "zh-CN" {
		t.Errorf("Value[lang] = %v, 期望 %q", item.Value["lang"], "zh-CN")
	}
}

func TestFileStore_GetNonExistent(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()

	item, err := s.Get(ctx, []string{"none"}, "none")
	if err != nil {
		t.Fatalf("Get 不应返回错误: %v", err)
	}
	if item != nil {
		t.Fatal("不存在的 key 应返回 nil")
	}
}

func TestFileStore_PutUpdate(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()
	ns := []string{"test"}

	if err := s.Put(ctx, ns, "k1", map[string]any{"v": 1}); err != nil {
		t.Fatalf("首次 Put 失败: %v", err)
	}

	item1, _ := s.Get(ctx, ns, "k1")
	createdAt := item1.CreatedAt

	time.Sleep(10 * time.Millisecond)

	if err := s.Put(ctx, ns, "k1", map[string]any{"v": 2}); err != nil {
		t.Fatalf("更新 Put 失败: %v", err)
	}

	item2, _ := s.Get(ctx, ns, "k1")
	if item2.Value["v"] != json.Number("2") {
		t.Errorf("更新后 Value[v] = %v, 期望 2", item2.Value["v"])
	}
	if !item2.CreatedAt.Equal(createdAt) {
		t.Errorf("更新后 CreatedAt 应保持不变")
	}
	if !item2.UpdatedAt.After(createdAt) {
		t.Error("更新后 UpdatedAt 应大于 CreatedAt")
	}
}

func TestFileStore_PutEmptyKey(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, []string{"test"}, "", map[string]any{"v": 1}); err == nil {
		t.Fatal("空 key 应返回错误")
	}
}

func TestFileStore_Delete(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()
	ns := []string{"del"}

	if err := s.Put(ctx, ns, "k1", map[string]any{"v": 1}); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}

	if err := s.Delete(ctx, ns, "k1"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}

	item, err := s.Get(ctx, ns, "k1")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if item != nil {
		t.Fatal("删除后应返回 nil")
	}
}

func TestFileStore_DeleteNonExistent(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()

	// 不应报错
	if err := s.Delete(ctx, []string{"none"}, "none"); err != nil {
		t.Fatalf("删除不存在的 key 不应报错: %v", err)
	}
}

func TestFileStore_List(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()
	ns := []string{"list"}

	for i := range 5 {
		if err := s.Put(ctx, ns, fmt.Sprintf("k%d", i), map[string]any{"i": i}); err != nil {
			t.Fatalf("Put 失败: %v", err)
		}
	}

	items, err := s.List(ctx, ns)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("List 返回 %d 条, 期望 5", len(items))
	}
}

func TestFileStore_ListWithPagination(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()
	ns := []string{"page"}

	for i := range 10 {
		if err := s.Put(ctx, ns, fmt.Sprintf("k%02d", i), map[string]any{
			"i": i,
		}); err != nil {
			t.Fatalf("Put 失败: %v", err)
		}
		time.Sleep(time.Millisecond) // 确保时间戳不同
	}

	items, err := s.List(ctx, ns, WithListLimit(3))
	if err != nil {
		t.Fatalf("List 页1 失败: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("List 页1 返回 %d 条, 期望 3", len(items))
	}
}

func TestFileStore_ListWithKeyPrefix(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()
	ns := []string{"prefix"}

	if err := s.Put(ctx, ns, "user_a", map[string]any{"v": 1}); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if err := s.Put(ctx, ns, "user_b", map[string]any{"v": 2}); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if err := s.Put(ctx, ns, "config_x", map[string]any{"v": 3}); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}

	items, err := s.List(ctx, ns, WithKeyPrefix("user_"))
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("按前缀过滤 List 返回 %d 条, 期望 2", len(items))
	}
}

func TestFileStore_DeleteNamespace(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()

	ns1 := []string{"ns1"}
	ns2 := []string{"ns2"}

	if err := s.Put(ctx, ns1, "k1", map[string]any{"v": 1}); err != nil {
		t.Fatalf("Put ns1 失败: %v", err)
	}
	if err := s.Put(ctx, ns2, "k2", map[string]any{"v": 2}); err != nil {
		t.Fatalf("Put ns2 失败: %v", err)
	}

	if err := s.DeleteNamespace(ctx, ns1); err != nil {
		t.Fatalf("DeleteNamespace 失败: %v", err)
	}

	// ns1 应为空
	items1, _ := s.List(ctx, ns1)
	if len(items1) != 0 {
		t.Fatalf("ns1 删除后应为空, 实际 %d 条", len(items1))
	}

	// ns2 不受影响
	items2, _ := s.List(ctx, ns2)
	if len(items2) != 1 {
		t.Fatalf("ns2 应不受影响, 实际 %d 条", len(items2))
	}
}

func TestFileStore_NamespaceIsolation(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()

	nsAlice := []string{"users", "alice"}
	nsBob := []string{"users", "bob"}

	if err := s.Put(ctx, nsAlice, "pref", map[string]any{"theme": "dark"}); err != nil {
		t.Fatalf("Put alice 失败: %v", err)
	}
	if err := s.Put(ctx, nsBob, "pref", map[string]any{"theme": "light"}); err != nil {
		t.Fatalf("Put bob 失败: %v", err)
	}

	// Alice 的数据
	item, _ := s.Get(ctx, nsAlice, "pref")
	if item == nil || item.Value["theme"] != "dark" {
		t.Error("Alice 的数据不正确")
	}

	// Bob 的数据
	item, _ = s.Get(ctx, nsBob, "pref")
	if item == nil || item.Value["theme"] != "light" {
		t.Error("Bob 的数据不正确")
	}

	// Alice 看不到 Bob 的 key（不同命名空间）
	item, _ = s.Get(ctx, nsAlice, "bob_key")
	if item != nil {
		t.Error("Alice 不应看到 Bob 的数据")
	}
}

func TestFileStore_TTL(t *testing.T) {
	s := newTestFileStore(t, WithFileCleanupInterval(50*time.Millisecond))
	ctx := context.Background()
	ns := []string{"ttl"}

	// 存一条短 TTL 的记忆
	if err := s.Put(ctx, ns, "short", map[string]any{"v": 1}, WithTTL(100*time.Millisecond)); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	// 存一条不过期的
	if err := s.Put(ctx, ns, "permanent", map[string]any{"v": 2}); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}

	// 立即应能获取
	item, _ := s.Get(ctx, ns, "short")
	if item == nil {
		t.Fatal("TTL 未过期应能获取")
	}

	// 等待过期
	time.Sleep(200 * time.Millisecond)

	// 过期后应返回 nil
	item, _ = s.Get(ctx, ns, "short")
	if item != nil {
		t.Error("TTL 过期后应返回 nil")
	}

	// 永久记忆不受影响
	item, _ = s.Get(ctx, ns, "permanent")
	if item == nil {
		t.Error("永久记忆不应受 TTL 影响")
	}
}

func TestFileStore_Search(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()
	ns := []string{"search"}

	if err := s.Put(ctx, ns, "k1", map[string]any{"title": "Go 编程语言", "lang": "go"}); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if err := s.Put(ctx, ns, "k2", map[string]any{"title": "Python 数据分析", "lang": "python"}); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	if err := s.Put(ctx, ns, "k3", map[string]any{"title": "Go 并发模型", "lang": "go"}); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}

	// 关键词搜索
	results, err := s.Search(ctx, ns, &SearchQuery{Query: "Go", Limit: 10})
	if err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("搜索 Go 结果数 = %d, 期望 2", len(results))
	}

	// 过滤搜索
	results, err = s.Search(ctx, ns, &SearchQuery{
		Filter: map[string]any{"lang": "python"},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("Filter Search 失败: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("按 lang=python 过滤结果数 = %d, 期望 1", len(results))
	}
}

func TestFileStore_SearchNilQuery(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()

	results, err := s.Search(ctx, []string{"test"}, nil)
	if err != nil {
		t.Fatalf("nil query 不应报错: %v", err)
	}
	if results != nil {
		t.Fatal("nil query 应返回 nil")
	}
}

func TestFileStore_ConcurrentAccess(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()
	ns := []string{"concurrent"}

	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	// 并发写入
	for i := range 10 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.Put(ctx, ns, fmt.Sprintf("k%d", i), map[string]any{"i": i})
			if err != nil {
				errCh <- fmt.Errorf("Put k%d: %w", i, err)
			}
		}(i)
	}

	// 并发读取
	for i := range 10 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Get(ctx, ns, fmt.Sprintf("k%d", i))
			if err != nil {
				errCh <- fmt.Errorf("Get k%d: %w", i, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("并发错误: %v", err)
	}
}

func TestFileStore_AtomicWrite(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()
	ns := []string{"atomic"}

	if err := s.Put(ctx, ns, "k1", map[string]any{"v": "original"}); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}

	// 验证没有残留 .tmp 文件
	dir := s.namespaceDir(ns)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir 失败: %v", err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tmp" {
			t.Errorf("发现残留 .tmp 文件: %s", entry.Name())
		}
	}
}

func TestFileStore_NamespaceValidation(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()

	tests := []struct {
		name      string
		namespace []string
		wantErr   bool
	}{
		{"正常命名空间", []string{"users", "u1"}, false},
		{"空段", []string{"users", ""}, true},
		{"点号", []string{"users", ".."}, true},
		{"路径分隔符", []string{"users", "a/b"}, true},
		{"反斜杠", []string{"users", `a\b`}, true},
		{"空命名空间", []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Put(ctx, tt.namespace, "test", map[string]any{"v": 1})
			if tt.wantErr && err == nil {
				t.Error("期望返回错误")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("不期望返回错误: %v", err)
			}
		})
	}
}

func TestFileStore_ContextCanceled(t *testing.T) {
	s := newTestFileStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Put(ctx, []string{"test"}, "k1", map[string]any{"v": 1}); err == nil {
		t.Fatal("已取消 context 应返回错误")
	}
	if _, err := s.Get(ctx, []string{"test"}, "k1"); err == nil {
		t.Fatal("已取消 context 应返回错误")
	}
	if _, err := s.List(ctx, []string{"test"}); err == nil {
		t.Fatal("已取消 context 应返回错误")
	}
	if _, err := s.Search(ctx, []string{"test"}, &SearchQuery{Limit: 10}); err == nil {
		t.Fatal("已取消 context 应返回错误")
	}
	if err := s.Delete(ctx, []string{"test"}, "k1"); err == nil {
		t.Fatal("已取消 context 应返回错误")
	}
	if err := s.DeleteNamespace(ctx, []string{"test"}); err == nil {
		t.Fatal("已取消 context 应返回错误")
	}
}

func TestFileStore_PersistenceAcrossInstances(t *testing.T) {
	baseDir := t.TempDir()
	ctx := context.Background()

	// 第一个实例写入
	s1, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("创建 FileStore 失败: %v", err)
	}
	if err := s1.Put(ctx, []string{"test"}, "k1", map[string]any{"v": "persisted"}); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	s1.Close()

	// 第二个实例读取
	s2, err := NewFileStore(baseDir)
	if err != nil {
		t.Fatalf("创建第二个 FileStore 失败: %v", err)
	}
	defer s2.Close()

	item, err := s2.Get(ctx, []string{"test"}, "k1")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if item == nil {
		t.Fatal("跨实例持久化数据丢失")
	}
	if item.Value["v"] != "persisted" {
		t.Errorf("Value[v] = %v, 期望 %q", item.Value["v"], "persisted")
	}
}

func TestFileStore_ListOrderDesc(t *testing.T) {
	s := newTestFileStore(t)
	ctx := context.Background()
	ns := []string{"order"}

	for i := range 5 {
		if err := s.Put(ctx, ns, fmt.Sprintf("k%d", i), map[string]any{"i": i}); err != nil {
			t.Fatalf("Put 失败: %v", err)
		}
		time.Sleep(5 * time.Millisecond) // 确保时间戳不同
	}

	items, err := s.List(ctx, ns, WithOrderDesc())
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("List 返回 %d 条, 期望 5", len(items))
	}

	// 验证降序（最新在前）
	for i := 1; i < len(items); i++ {
		if items[i].UpdatedAt.After(items[i-1].UpdatedAt) {
			t.Error("降序列表中发现时间不递减")
		}
	}
}
