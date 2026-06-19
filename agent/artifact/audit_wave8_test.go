package artifact

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// audit_wave8_test.go
//
// 本测试文件针对 agent/artifact 包进行全场景挑刺式审计：
//   - 正常路径：Save/Get/GetByVersion/GetLatest/List/ListVersions/Delete/DeleteAll
//   - 边界情况：空/nil/超长/0/负值/Unicode/并发
//   - 错误路径：缺失字段、不存在的 ID
//   - 功能闭环：版本自增、Size 自动计算、CreatedAt 默认值
//   - 状态一致性：返回值是否泄漏内部指针（封装性）
//
// 约束：只测本包公开函数，不修改源码。

// ============== Service.Save 正常路径 ==============

// TestServiceSave_FirstVersion 验证首次保存版本号为 1，ID 自动生成，Size 自动计算。
func TestServiceSave_FirstVersion(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()

	data := []byte("hello world")
	id, err := svc.Save(ctx, Artifact{
		SessionID: "s1",
		Name:      "a.txt",
		MimeType:  "text/plain",
		Data:      data,
	})
	if err != nil {
		t.Fatalf("Save 返回错误: %v", err)
	}
	wantID := "s1/a.txt/v1"
	if id != wantID {
		t.Fatalf("ID = %q, want %q", id, wantID)
	}

	got, err := svc.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get 返回错误: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
	if got.Size != int64(len(data)) {
		t.Errorf("Size = %d, want %d", got.Size, len(data))
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt 未被自动设置")
	}
}

// TestServiceSave_VersionIncrement 验证同 session 同 name 多次保存版本自增。
func TestServiceSave_VersionIncrement(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		_, err := svc.Save(ctx, Artifact{
			SessionID: "s1",
			Name:      "a.txt",
			Data:      []byte(fmt.Sprintf("v%d", i)),
		})
		if err != nil {
			t.Fatalf("第 %d 次 Save 错误: %v", i, err)
		}
	}

	latest, err := svc.GetLatest(ctx, "s1", "a.txt")
	if err != nil {
		t.Fatalf("GetLatest 错误: %v", err)
	}
	if latest == nil {
		t.Fatal("GetLatest 返回 nil")
	}
	if latest.Version != 5 {
		t.Errorf("最新版本 = %d, want 5", latest.Version)
	}

	versions, err := svc.ListVersions(ctx, "s1", "a.txt")
	if err != nil {
		t.Fatalf("ListVersions 错误: %v", err)
	}
	if len(versions) != 5 {
		t.Fatalf("版本数 = %d, want 5", len(versions))
	}
	for i, v := range versions {
		if v.Version != i+1 {
			t.Errorf("versions[%d].Version = %d, want %d", i, v.Version, i+1)
		}
	}
}

// TestServiceSave_PreservesUserID 验证用户提供的 ID 不被覆盖。
func TestServiceSave_PreservesUserID(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()

	id, err := svc.Save(ctx, Artifact{
		ID:        "custom-id",
		SessionID: "s1",
		Name:      "a.txt",
		Data:      []byte("x"),
	})
	if err != nil {
		t.Fatalf("Save 错误: %v", err)
	}
	if id != "custom-id" {
		t.Errorf("ID = %q, want custom-id", id)
	}
}

// TestServiceSave_PreservesCreatedAt 验证用户提供的 CreatedAt 不被覆盖。
func TestServiceSave_PreservesCreatedAt(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()

	fixed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	id, err := svc.Save(ctx, Artifact{
		SessionID: "s1",
		Name:      "a.txt",
		CreatedAt: fixed,
	})
	if err != nil {
		t.Fatalf("Save 错误: %v", err)
	}
	got, _ := svc.Get(ctx, id)
	if !got.CreatedAt.Equal(fixed) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, fixed)
	}
}

// ============== Service.Save 错误路径 ==============

// TestServiceSave_EmptySessionID 验证空 SessionID 报错。
func TestServiceSave_EmptySessionID(t *testing.T) {
	svc := NewService(NewMemoryStore())
	_, err := svc.Save(context.Background(), Artifact{Name: "a.txt"})
	if err == nil {
		t.Fatal("空 SessionID 应该报错，但 err 为 nil")
	}
}

// TestServiceSave_EmptyName 验证空 Name 报错。
func TestServiceSave_EmptyName(t *testing.T) {
	svc := NewService(NewMemoryStore())
	_, err := svc.Save(context.Background(), Artifact{SessionID: "s1"})
	if err == nil {
		t.Fatal("空 Name 应该报错，但 err 为 nil")
	}
}

// ============== 边界：nil/空 Data ==============

// TestServiceSave_NilData 验证 nil Data 保存成功，Size 为 0。
func TestServiceSave_NilData(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()
	id, err := svc.Save(ctx, Artifact{SessionID: "s1", Name: "a.txt", Data: nil})
	if err != nil {
		t.Fatalf("nil Data 保存失败: %v", err)
	}
	got, _ := svc.Get(ctx, id)
	if got.Size != 0 {
		t.Errorf("nil Data 的 Size = %d, want 0", got.Size)
	}
}

// TestServiceSave_EmptyData 验证空切片 Data 保存成功。
func TestServiceSave_EmptyData(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()
	id, err := svc.Save(ctx, Artifact{SessionID: "s1", Name: "a.txt", Data: []byte{}})
	if err != nil {
		t.Fatalf("空 Data 保存失败: %v", err)
	}
	got, _ := svc.Get(ctx, id)
	if got.Size != 0 {
		t.Errorf("空 Data 的 Size = %d, want 0", got.Size)
	}
}

// TestServiceSave_LargeData 验证超长 Data（1MB）保存成功，Size 准确。
func TestServiceSave_LargeData(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()
	large := make([]byte, 1<<20) // 1MB
	id, err := svc.Save(ctx, Artifact{SessionID: "s1", Name: "big.bin", Data: large})
	if err != nil {
		t.Fatalf("超长 Data 保存失败: %v", err)
	}
	got, _ := svc.Get(ctx, id)
	if got.Size != int64(1<<20) {
		t.Errorf("Size = %d, want %d", got.Size, 1<<20)
	}
}

// TestServiceSave_UnicodeName 验证 Unicode 名称与会话 ID 正常工作。
func TestServiceSave_UnicodeName(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()
	id, err := svc.Save(ctx, Artifact{
		SessionID: "会话-😀",
		Name:      "报告_文档.pdf",
		Data:      []byte("内容"),
	})
	if err != nil {
		t.Fatalf("Unicode 保存失败: %v", err)
	}
	got, err := svc.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get Unicode 失败: %v", err)
	}
	if got.Name != "报告_文档.pdf" {
		t.Errorf("Name = %q, want 报告_文档.pdf", got.Name)
	}
	// 验证 GetLatest 同样可用 Unicode 检索
	latest, err := svc.GetLatest(ctx, "会话-😀", "报告_文档.pdf")
	if err != nil || latest == nil {
		t.Fatalf("GetLatest Unicode 失败: err=%v latest=%v", err, latest)
	}
}

// ============== Get / GetByVersion 错误路径 ==============

// TestServiceGet_NotFound 验证获取不存在 ID 报错。
func TestServiceGet_NotFound(t *testing.T) {
	svc := NewService(NewMemoryStore())
	_, err := svc.Get(context.Background(), "nope")
	if err == nil {
		t.Fatal("获取不存在 ID 应报错")
	}
}

// TestStoreGetByVersion_NotFound 验证按版本获取不存在时报错。
func TestStoreGetByVersion_NotFound(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.GetByVersion(context.Background(), "s1", "a.txt", 99)
	if err == nil {
		t.Fatal("不存在版本应报错")
	}
}

// TestStoreGetByVersion_Found 验证按版本获取正确版本。
func TestStoreGetByVersion_Found(t *testing.T) {
	svc := NewService(NewMemoryStore())
	store := NewMemoryStore()
	svc = NewService(store)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		_, _ = svc.Save(ctx, Artifact{SessionID: "s1", Name: "a.txt", Data: []byte(fmt.Sprintf("%d", i))})
	}
	got, err := store.GetByVersion(ctx, "s1", "a.txt", 2)
	if err != nil {
		t.Fatalf("GetByVersion 错误: %v", err)
	}
	if got.Version != 2 {
		t.Errorf("Version = %d, want 2", got.Version)
	}
}

// ============== GetLatest 未闭环 / 边界 ==============

// TestServiceGetLatest_NotExist 验证不存在时返回 (nil, nil)。
// 注意：此处约定 GetLatest 在无数据时返回 nil error + nil artifact，
// 调用方必须显式判空，否则极易 nil 解引用 panic。
func TestServiceGetLatest_NotExist(t *testing.T) {
	svc := NewService(NewMemoryStore())
	got, err := svc.GetLatest(context.Background(), "s1", "missing")
	if err != nil {
		t.Fatalf("GetLatest 不应返回 error, got %v", err)
	}
	if got != nil {
		t.Errorf("GetLatest 应返回 nil, got %v", got)
	}
}

// ============== List / ListVersions ==============

// TestServiceList_LatestOnly 验证 List 返回每个 name 的最新版本且按 name 排序。
func TestServiceList_LatestOnly(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()

	// b.txt 两个版本，a.txt 一个版本
	_, _ = svc.Save(ctx, Artifact{SessionID: "s1", Name: "b.txt", Data: []byte("b1")})
	_, _ = svc.Save(ctx, Artifact{SessionID: "s1", Name: "b.txt", Data: []byte("b2")})
	_, _ = svc.Save(ctx, Artifact{SessionID: "s1", Name: "a.txt", Data: []byte("a1")})

	list, err := svc.List(ctx, "s1")
	if err != nil {
		t.Fatalf("List 错误: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List 长度 = %d, want 2", len(list))
	}
	// 排序：a.txt 在前
	if list[0].Name != "a.txt" || list[1].Name != "b.txt" {
		t.Errorf("排序错误: %q, %q", list[0].Name, list[1].Name)
	}
	// b.txt 应该是最新版本 v2
	if list[1].Version != 2 {
		t.Errorf("b.txt 最新版本 = %d, want 2", list[1].Version)
	}
}

// TestServiceList_Empty 验证空会话 List 返回空（非 nil panic）。
func TestServiceList_Empty(t *testing.T) {
	svc := NewService(NewMemoryStore())
	list, err := svc.List(context.Background(), "empty-session")
	if err != nil {
		t.Fatalf("List 错误: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("空会话 List 长度 = %d, want 0", len(list))
	}
}

// TestServiceListVersions_Empty 验证不存在 name 的 ListVersions 返回空。
func TestServiceListVersions_Empty(t *testing.T) {
	svc := NewService(NewMemoryStore())
	versions, err := svc.ListVersions(context.Background(), "s1", "none")
	if err != nil {
		t.Fatalf("ListVersions 错误: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("长度 = %d, want 0", len(versions))
	}
}

// TestServiceList_SessionIsolation 验证不同会话隔离。
func TestServiceList_SessionIsolation(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()
	_, _ = svc.Save(ctx, Artifact{SessionID: "s1", Name: "a.txt", Data: []byte("x")})
	_, _ = svc.Save(ctx, Artifact{SessionID: "s2", Name: "a.txt", Data: []byte("y")})

	l1, _ := svc.List(ctx, "s1")
	l2, _ := svc.List(ctx, "s2")
	if len(l1) != 1 || len(l2) != 1 {
		t.Fatalf("会话隔离失败: l1=%d l2=%d", len(l1), len(l2))
	}
}

// ============== Delete / DeleteAll ==============

// TestServiceDelete 验证删除后 Get 报错。
func TestServiceDelete(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()
	id, _ := svc.Save(ctx, Artifact{SessionID: "s1", Name: "a.txt", Data: []byte("x")})
	if err := svc.Delete(ctx, id); err != nil {
		t.Fatalf("Delete 错误: %v", err)
	}
	if _, err := svc.Get(ctx, id); err == nil {
		t.Error("删除后 Get 应报错")
	}
}

// TestServiceDelete_NonExistent 验证删除不存在 ID 不报错（幂等）。
func TestServiceDelete_NonExistent(t *testing.T) {
	svc := NewService(NewMemoryStore())
	if err := svc.Delete(context.Background(), "nope"); err != nil {
		t.Errorf("删除不存在 ID 应幂等无错, got %v", err)
	}
}

// TestServiceDeleteAll 验证删除会话下所有 artifact，不影响其他会话。
func TestServiceDeleteAll(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()
	_, _ = svc.Save(ctx, Artifact{SessionID: "s1", Name: "a.txt", Data: []byte("x")})
	_, _ = svc.Save(ctx, Artifact{SessionID: "s1", Name: "b.txt", Data: []byte("y")})
	_, _ = svc.Save(ctx, Artifact{SessionID: "s2", Name: "c.txt", Data: []byte("z")})

	if err := svc.DeleteAll(ctx, "s1"); err != nil {
		t.Fatalf("DeleteAll 错误: %v", err)
	}
	l1, _ := svc.List(ctx, "s1")
	if len(l1) != 0 {
		t.Errorf("DeleteAll 后 s1 仍有 %d 个", len(l1))
	}
	l2, _ := svc.List(ctx, "s2")
	if len(l2) != 1 {
		t.Errorf("DeleteAll 误删 s2, 剩 %d 个", len(l2))
	}
}

// TestServiceDeleteAll_Empty 验证删除空会话不报错。
func TestServiceDeleteAll_Empty(t *testing.T) {
	svc := NewService(NewMemoryStore())
	if err := svc.DeleteAll(context.Background(), "none"); err != nil {
		t.Errorf("空会话 DeleteAll 应无错, got %v", err)
	}
}

// ============== 封装性 / 状态一致性挑刺 ==============

// TestMemoryStore_GetReturnsSharedPointer 暴露封装性缺陷：
// Get/GetLatest 返回的是内部存储的同一指针，
// 调用方修改返回的 Artifact 会污染 store 内部状态。
//
// 这是真实 bug：违反"显式优于隐式"与并发安全约定。
// 期望行为：返回值应为副本，修改不影响 store。
//
// 回归: MemoryStore.Get 曾直接返回内部共享指针，调用方修改污染 store；现 copy-on-read 返回深拷贝。
func TestMemoryStore_GetReturnsSharedPointer(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()
	id, _ := svc.Save(ctx, Artifact{SessionID: "s1", Name: "a.txt", Data: []byte("orig")})

	got1, _ := store.Get(ctx, id)
	// 调用方篡改返回值
	got1.Name = "TAMPERED"

	got2, _ := store.Get(ctx, id)
	if got2.Name == "TAMPERED" {
		t.Errorf("封装性缺陷确认：调用方修改返回值污染了 store 内部状态 (got2.Name=%q)。"+
			"Get 应返回副本而非内部指针。", got2.Name)
	}
}

// TestMemoryStore_GetLatestReturnsSharedPointer 同上，针对 GetLatest。
//
// 回归: MemoryStore.GetLatest 曾返回内部共享指针（被篡改后污染版本计算）；现 copy-on-read 返回深拷贝。
func TestMemoryStore_GetLatestReturnsSharedPointer(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()
	_, _ = svc.Save(ctx, Artifact{SessionID: "s1", Name: "a.txt", Data: []byte("orig")})

	got1, _ := store.GetLatest(ctx, "s1", "a.txt")
	got1.Version = 999

	got2, _ := store.GetLatest(ctx, "s1", "a.txt")
	if got2.Version == 999 {
		t.Errorf("封装性缺陷确认：GetLatest 返回内部指针，被篡改后 store 受污染 (Version=%d)。", got2.Version)
	}
}

// ============== 并发场景 ==============

// TestServiceSave_ConcurrentVersioning 暴露并发版本竞态：
// 多个 goroutine 并发对同一 (session, name) Save，
// Service.Save 中 "GetLatest 读 -> +1 -> Save 写" 非原子，
// 会产生版本号冲突（多个 artifact 拿到相同 Version 并因 ID 相同互相覆盖）。
//
// 期望：N 次保存应产生 N 个不同版本（v1..vN）。
//
// 回归: Service.Save 的 GetLatest->+1->Save 非原子导致并发版本冲突丢失；现走 Store.SaveNextVersion 锁内原子分配版本号。
func TestServiceSave_ConcurrentVersioning(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, _ = svc.Save(ctx, Artifact{
				SessionID: "s1",
				Name:      "a.txt",
				Data:      []byte(fmt.Sprintf("%d", i)),
			})
		}(i)
	}
	wg.Wait()

	versions, err := svc.ListVersions(ctx, "s1", "a.txt")
	if err != nil {
		t.Fatalf("ListVersions 错误: %v", err)
	}
	// 检查是否有版本号重复或丢失
	seen := make(map[int]int)
	maxV := 0
	for _, v := range versions {
		seen[v.Version]++
		if v.Version > maxV {
			maxV = v.Version
		}
	}
	dup := false
	for ver, cnt := range seen {
		if cnt > 1 {
			dup = true
			t.Logf("版本 v%d 出现 %d 次（重复）", ver, cnt)
		}
	}
	if len(versions) != n || dup || maxV != n {
		t.Errorf("并发版本竞态确认：n=%d 次并发 Save 后，得到 %d 个版本，最大版本 v%d。"+
			"由于 GetLatest->+1->Save 非原子，版本号冲突导致同 ID 互相覆盖，丢失版本。", n, len(versions), maxV)
	}
}

// TestMemoryStore_ConcurrentReadWrite 验证读写并发不触发 data race（需配合 -race）。
func TestMemoryStore_ConcurrentReadWrite(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	var wg sync.WaitGroup

	// 写
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = store.Save(ctx, Artifact{
				ID:        fmt.Sprintf("id-%d", i),
				SessionID: "s1",
				Name:      fmt.Sprintf("n-%d", i),
				Version:   1,
			})
		}(i)
	}
	// 读
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.List(ctx, "s1")
			_, _ = store.GetLatest(ctx, "s1", "n-0")
		}()
	}
	wg.Wait()
}

// ============== 边界：负版本 / 0 版本通过 Store 直接写入 ==============

// TestMemoryStore_DirectSaveNegativeVersion 验证 Store 层不校验版本号，
// 负版本/0 版本可被直接写入（Service 层才保证 >=1）。
// 这记录了 Store 接口缺乏版本校验的现状。
func TestMemoryStore_DirectSaveNegativeVersion(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	id, err := store.Save(ctx, Artifact{ID: "x", SessionID: "s1", Name: "a", Version: -5})
	if err != nil {
		t.Fatalf("Store.Save 不应报错: %v", err)
	}
	got, _ := store.Get(ctx, id)
	if got.Version != -5 {
		t.Errorf("Store 层未保留写入的负版本: %d", got.Version)
	}
}

// ============== 闭环：GetByVersion 返回内容正确性 ==============

// TestStoreGetByVersion_CorrectData 验证不同版本数据互不串扰。
func TestStoreGetByVersion_CorrectData(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()
	_, _ = svc.Save(ctx, Artifact{SessionID: "s1", Name: "a.txt", Data: []byte("AAA")})
	_, _ = svc.Save(ctx, Artifact{SessionID: "s1", Name: "a.txt", Data: []byte("BBB")})

	v1, err := store.GetByVersion(ctx, "s1", "a.txt", 1)
	if err != nil {
		t.Fatalf("GetByVersion v1 错误: %v", err)
	}
	if string(v1.Data) != "AAA" {
		t.Errorf("v1 Data = %q, want AAA", v1.Data)
	}
	v2, _ := store.GetByVersion(ctx, "s1", "a.txt", 2)
	if string(v2.Data) != "BBB" {
		t.Errorf("v2 Data = %q, want BBB", v2.Data)
	}
}

// ============== 文档一致性挑刺 ==============

// TestDocExampleFunctionExists 验证包注释示例引用的构造函数与实际导出一致。
//
// 历史现状：包注释示例曾使用不存在的 NewMemoryArtifactStore()，而实际导出的是
// NewMemoryStore()。修复后包注释已改为 NewMemoryStore()。
//
// 回归: 包注释示例曾引用不存在的 NewMemoryArtifactStore（文档与代码不一致）；现改为真实的 NewMemoryStore。
// 本测试读取本包源码文件，断言包注释不再出现错误函数名、且出现正确函数名，
// 从而把文档一致性钉死为可回归断言（而非仅 t.Log 记录）。
func TestDocExampleFunctionExists(t *testing.T) {
	// 真实可用的构造函数
	store := NewMemoryStore()
	if store == nil {
		t.Fatal("NewMemoryStore 返回 nil")
	}

	src, err := os.ReadFile("artifact.go")
	if err != nil {
		t.Fatalf("读取 artifact.go 失败: %v", err)
	}
	doc := string(src)
	if strings.Contains(doc, "NewMemoryArtifactStore") {
		t.Errorf("文档一致性缺陷确认：包源码仍引用不存在的构造函数 NewMemoryArtifactStore")
	}
	if !strings.Contains(doc, "NewMemoryStore()") {
		t.Errorf("包注释示例应引用真实构造函数 NewMemoryStore()，但未找到")
	}
}

// ============== Save 冲突检测（create vs overwrite）==============

// TestServiceSave_OverwriteSilentlyByDefault 记录默认 upsert 现状：
// 默认情况下，自带相同 ID 的两次 Save 会静默覆盖（不报错）。
// 这是有意保留的默认语义，新增的 WithFailOnConflict 选项才改为报错。
func TestServiceSave_OverwriteSilentlyByDefault(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()

	id1, err := svc.Save(ctx, Artifact{ID: "dup", SessionID: "s1", Name: "a.txt", Data: []byte("first")})
	if err != nil {
		t.Fatalf("首次 Save 错误: %v", err)
	}
	// 默认语义：相同 ID 再次 Save 不报错（upsert 覆盖）。
	_, err = svc.Save(ctx, Artifact{ID: "dup", SessionID: "s1", Name: "a.txt", Data: []byte("second")})
	if err != nil {
		t.Fatalf("默认应允许覆盖，但报错: %v", err)
	}
	got, _ := svc.Get(ctx, id1)
	if string(got.Data) != "second" {
		t.Errorf("覆盖后 Data = %q, want second", got.Data)
	}
}

// TestServiceSave_FailOnConflict 暴露并验证 bug #4 的修复：
// Save 默认允许用户自带 ID 静默覆盖任意已存在 artifact，无冲突检测。
// 修复方案：新增 additive 选项 WithFailOnConflict()，开启后对已存在 ID 报错。
//
// 回归: Service.Save 曾无条件 upsert，用户自带 ID 可静默覆盖任意已存在 artifact；现 WithFailOnConflict 选项可拦截覆盖并显式报错。
func TestServiceSave_FailOnConflict(t *testing.T) {
	svc := NewService(NewMemoryStore(), WithFailOnConflict())
	ctx := context.Background()

	_, err := svc.Save(ctx, Artifact{ID: "dup", SessionID: "s1", Name: "a.txt", Data: []byte("first")})
	if err != nil {
		t.Fatalf("首次 Save 不应报错: %v", err)
	}

	// 相同 ID 再次 Save：开启冲突检测后应报错，且不覆盖原数据。
	_, err = svc.Save(ctx, Artifact{ID: "dup", SessionID: "s1", Name: "a.txt", Data: []byte("second")})
	if err == nil {
		t.Fatal("冲突检测缺陷确认：开启 WithFailOnConflict 后，覆盖已存在 ID 仍未报错")
	}

	// 原数据未被污染。
	got, getErr := svc.Get(ctx, "dup")
	if getErr != nil {
		t.Fatalf("Get 错误: %v", getErr)
	}
	if string(got.Data) != "first" {
		t.Errorf("冲突被拒绝后原数据应保持不变，Data = %q, want first", got.Data)
	}
}

// TestServiceSave_FailOnConflict_AutoIDNoFalsePositive 验证开启冲突检测后，
// 自动生成 ID 的版本化保存不会被误判为冲突（版本号自增产生不同 ID）。
//
// 回归: WithFailOnConflict 不应误伤自动 ID 的正常版本自增。
func TestServiceSave_FailOnConflict_AutoIDNoFalsePositive(t *testing.T) {
	svc := NewService(NewMemoryStore(), WithFailOnConflict())
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		_, err := svc.Save(ctx, Artifact{SessionID: "s1", Name: "a.txt", Data: []byte(fmt.Sprintf("%d", i))})
		if err != nil {
			t.Fatalf("第 %d 次自动 ID Save 不应报冲突: %v", i, err)
		}
	}
	versions, _ := svc.ListVersions(ctx, "s1", "a.txt")
	if len(versions) != 3 {
		t.Errorf("自动 ID 版本自增应得到 3 个版本, got %d", len(versions))
	}
}

// TestMemoryStore_DataMetadataDeepCopy 验证 copy-on-read 对引用类型字段
// （Data []byte / Metadata map）同样做深拷贝，避免共享底层数组/映射导致污染。
//
// 回归: 读取路径曾仅浅拷贝结构体，Data/Metadata 仍共享底层存储；现 cloneArtifact 深拷贝引用字段。
func TestMemoryStore_DataMetadataDeepCopy(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()
	id, _ := svc.Save(ctx, Artifact{
		SessionID: "s1", Name: "a.txt",
		Data:     []byte("orig"),
		Metadata: map[string]any{"k": "v"},
	})

	got1, _ := store.Get(ctx, id)
	got1.Data[0] = 'X'          // 篡改返回值的底层数组
	got1.Metadata["k"] = "HACK" // 篡改返回值的映射

	got2, _ := store.Get(ctx, id)
	if string(got2.Data) != "orig" {
		t.Errorf("Data 深拷贝缺陷：底层数组被共享篡改, got %q", got2.Data)
	}
	if got2.Metadata["k"] != "v" {
		t.Errorf("Metadata 深拷贝缺陷：映射被共享篡改, got %v", got2.Metadata["k"])
	}
}
