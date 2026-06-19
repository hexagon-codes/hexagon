// Package artifact 提供 Agent 生成文件的版本化管理
//
// Artifact 管理系统参考 Google ADK Go 设计，用于：
//   - 保存 Agent 执行过程中生成的文件和二进制数据
//   - 版本化管理，支持按版本回溯
//   - 与 Session 关联，支持按会话查询
//
// 使用示例：
//
//	store := NewMemoryStore()
//	svc := NewService(store)
//
//	// 保存 artifact
//	id, err := svc.Save(ctx, Artifact{
//	    SessionID: "session-123",
//	    Name:      "report.pdf",
//	    MimeType:  "application/pdf",
//	    Data:      pdfBytes,
//	})
//
//	// 获取最新版本
//	art, err := svc.GetLatest(ctx, "session-123", "report.pdf")
//
//	// 列出所有版本
//	versions, err := svc.ListVersions(ctx, "session-123", "report.pdf")
package artifact

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"
)

// Artifact 代表 Agent 生成的一个文件或数据对象
type Artifact struct {
	// ID 唯一标识
	ID string `json:"id"`

	// SessionID 关联的会话 ID
	SessionID string `json:"session_id"`

	// Name 文件名称（同名同 session 的多个 artifact 视为不同版本）
	Name string `json:"name"`

	// MimeType MIME 类型
	MimeType string `json:"mime_type"`

	// Data 文件内容
	Data []byte `json:"data,omitempty"`

	// Version 版本号（从 1 开始自增）
	Version int `json:"version"`

	// Size 文件大小（字节）
	Size int64 `json:"size"`

	// Metadata 额外元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`

	// CreatedBy 创建者（Agent ID）
	CreatedBy string `json:"created_by,omitempty"`
}

// Store artifact 存储接口
type Store interface {
	// Save 保存 artifact，返回 ID
	Save(ctx context.Context, art Artifact) (string, error)

	// Get 按 ID 获取 artifact
	Get(ctx context.Context, id string) (*Artifact, error)

	// GetByVersion 按名称和版本获取
	GetByVersion(ctx context.Context, sessionID, name string, version int) (*Artifact, error)

	// GetLatest 获取最新版本
	GetLatest(ctx context.Context, sessionID, name string) (*Artifact, error)

	// List 列出会话下所有 artifact（最新版本）
	List(ctx context.Context, sessionID string) ([]Artifact, error)

	// ListVersions 列出指定名称的所有版本
	ListVersions(ctx context.Context, sessionID, name string) ([]Artifact, error)

	// Delete 删除指定 artifact
	Delete(ctx context.Context, id string) error

	// DeleteAll 删除会话下所有 artifact
	DeleteAll(ctx context.Context, sessionID string) error
}

// versionedSaver 是 Store 的可选扩展接口，用于原子地分配版本号并写入。
//
// 背景：Service.Save 默认走 "GetLatest 读 -> +1 -> Save 写" 三步，在并发场景下
// 这三步之间存在竞态——多个 goroutine 可能读到相同的最大版本号，从而计算出相同
// 的新版本号与 ID，最终互相覆盖导致版本丢失。
//
// 若底层 Store 实现了本接口，则 Service.Save 会把"计算下一个版本号 + 写入"交给
// Store 在单次加锁内原子完成，彻底消除上述竞态。未实现本接口的 Store 仍可正常工作
// （退化为非原子路径），因此本接口是对 Store 的向后兼容增强，而非破坏性变更。
type versionedSaver interface {
	// SaveNextVersion 原子地为 (SessionID, Name) 分配下一个版本号并写入。
	//
	// 入参 art 中调用方已填好的 Version 会被忽略，由 Store 在锁内根据当前
	// 最大版本号 +1 计算；若调用方未指定 ID，则按计算出的版本号生成默认 ID。
	// 返回最终写入的 Artifact 的 ID。
	SaveNextVersion(ctx context.Context, art Artifact) (string, error)
}

// Service artifact 管理服务
type Service struct {
	store Store

	// failOnConflict 为 true 时，Save 在目标 ID 已存在时返回冲突错误，
	// 而非静默覆盖（upsert）。默认 false，保持原有 upsert 语义不变。
	failOnConflict bool
}

// Option 是 Service 的可选配置项。
//
// 采用 functional option 模式，保证 NewService 既有签名 NewService(store) 不被破坏，
// 后续新增配置只需追加 Option 而无需修改调用方。
type Option func(*Service)

// WithFailOnConflict 让 Save 在目标 ID 已存在时返回显式冲突错误，而非静默覆盖。
//
// 适用于需要严格区分 create / overwrite 语义的场景：当调用方自带 ID 且该 ID
// 已存在时，默认的 upsert 行为会无声覆盖已有 artifact；开启本选项后将改为报错，
// 避免误覆盖任意已存在数据。
func WithFailOnConflict() Option {
	return func(s *Service) {
		s.failOnConflict = true
	}
}

// NewService 创建 artifact 管理服务
func NewService(store Store, opts ...Option) *Service {
	s := &Service{store: store}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Save 保存 artifact，自动生成 ID 和版本号
//
// 并发安全：若底层 Store 实现了 versionedSaver（如内置的 MemoryStore），
// 则"分配版本号 + 写入"会在 Store 内部单次加锁原子完成，避免并发版本竞态；
// 否则退化为非原子路径（读取最大版本号后 +1 再写入），仅适用于无并发写入的场景。
func (s *Service) Save(ctx context.Context, art Artifact) (string, error) {
	if art.SessionID == "" {
		return "", fmt.Errorf("SessionID 不能为空")
	}
	if art.Name == "" {
		return "", fmt.Errorf("Name 不能为空")
	}

	art.Size = int64(len(art.Data))
	if art.CreatedAt.IsZero() {
		art.CreatedAt = time.Now()
	}

	// 冲突检测：仅当调用方自带 ID 且开启 failOnConflict 时生效。
	// 自动生成的 ID 形如 session/name/vN，由原子版本分配保证唯一，不会冲突，
	// 故此处只需拦截"用户自带 ID 覆盖已存在 artifact"的情形。
	if s.failOnConflict && art.ID != "" {
		if _, err := s.store.Get(ctx, art.ID); err == nil {
			return "", fmt.Errorf("artifact ID 已存在，拒绝覆盖: %s", art.ID)
		}
	}

	// 优先走原子路径：由 Store 在锁内完成 "下一个版本号 + 写入"，消除并发竞态。
	if vs, ok := s.store.(versionedSaver); ok {
		return vs.SaveNextVersion(ctx, art)
	}

	// 退化路径：Store 未实现原子版本分配，沿用非原子的读-改-写。
	latest, _ := s.store.GetLatest(ctx, art.SessionID, art.Name)
	if latest != nil {
		art.Version = latest.Version + 1
	} else {
		art.Version = 1
	}
	if art.ID == "" {
		art.ID = fmt.Sprintf("%s/%s/v%d", art.SessionID, art.Name, art.Version)
	}
	return s.store.Save(ctx, art)
}

// Get 获取 artifact
func (s *Service) Get(ctx context.Context, id string) (*Artifact, error) {
	return s.store.Get(ctx, id)
}

// GetLatest 获取最新版本
func (s *Service) GetLatest(ctx context.Context, sessionID, name string) (*Artifact, error) {
	return s.store.GetLatest(ctx, sessionID, name)
}

// ListVersions 列出所有版本
func (s *Service) ListVersions(ctx context.Context, sessionID, name string) ([]Artifact, error) {
	return s.store.ListVersions(ctx, sessionID, name)
}

// List 列出会话下所有 artifact
func (s *Service) List(ctx context.Context, sessionID string) ([]Artifact, error) {
	return s.store.List(ctx, sessionID)
}

// Delete 删除 artifact
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// DeleteAll 删除会话下所有 artifact
func (s *Service) DeleteAll(ctx context.Context, sessionID string) error {
	return s.store.DeleteAll(ctx, sessionID)
}

// ============== MemoryStore 内存存储实现 ==============

// MemoryStore 内存存储
// 适用于开发和测试环境
type MemoryStore struct {
	mu        sync.RWMutex
	artifacts map[string]*Artifact // ID -> Artifact
}

// NewMemoryStore 创建内存存储
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		artifacts: make(map[string]*Artifact),
	}
}

// cloneArtifact 返回 Artifact 的深拷贝。
//
// Artifact 中的 Data（[]byte）与 Metadata（map）是引用类型，仅做结构体浅拷贝
// 仍会让调用方与 store 内部共享底层数组/映射。读取路径统一返回深拷贝，确保调用方
// 对返回值的任何修改都不会污染 store 内部状态（封装性 + 并发安全约定）。
func cloneArtifact(art *Artifact) *Artifact {
	if art == nil {
		return nil
	}
	cloned := *art
	if art.Data != nil {
		cloned.Data = slices.Clone(art.Data)
	}
	if art.Metadata != nil {
		cloned.Metadata = maps.Clone(art.Metadata)
	}
	return &cloned
}

func (s *MemoryStore) Save(_ context.Context, art Artifact) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 写入时同样深拷贝，避免调用方持有的原始切片/映射被后续修改而影响 store。
	s.artifacts[art.ID] = cloneArtifact(&art)
	return art.ID, nil
}

// SaveNextVersion 原子地为 (SessionID, Name) 分配下一个版本号并写入。
//
// 在单次写锁内完成 "扫描当前最大版本号 -> +1 -> 生成 ID -> 写入"，
// 从而消除 Service.Save 默认路径中 "读-改-写" 之间的并发竞态。
func (s *MemoryStore) SaveNextVersion(_ context.Context, art Artifact) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 锁内扫描当前最大版本号，保证版本分配的原子性。
	maxVersion := 0
	for _, existing := range s.artifacts {
		if existing.SessionID == art.SessionID && existing.Name == art.Name {
			if existing.Version > maxVersion {
				maxVersion = existing.Version
			}
		}
	}
	art.Version = maxVersion + 1

	if art.ID == "" {
		art.ID = fmt.Sprintf("%s/%s/v%d", art.SessionID, art.Name, art.Version)
	}

	s.artifacts[art.ID] = cloneArtifact(&art)
	return art.ID, nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (*Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	art, ok := s.artifacts[id]
	if !ok {
		return nil, fmt.Errorf("artifact 不存在: %s", id)
	}
	// 返回深拷贝，避免调用方修改污染 store 内部状态。
	return cloneArtifact(art), nil
}

func (s *MemoryStore) GetByVersion(_ context.Context, sessionID, name string, version int) (*Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, art := range s.artifacts {
		if art.SessionID == sessionID && art.Name == name && art.Version == version {
			// 返回深拷贝，避免调用方修改污染 store 内部状态。
			return cloneArtifact(art), nil
		}
	}
	return nil, fmt.Errorf("artifact 不存在: %s/%s v%d", sessionID, name, version)
}

func (s *MemoryStore) GetLatest(_ context.Context, sessionID, name string) (*Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var latest *Artifact
	for _, art := range s.artifacts {
		if art.SessionID == sessionID && art.Name == name {
			if latest == nil || art.Version > latest.Version {
				latest = art
			}
		}
	}

	if latest == nil {
		return nil, nil
	}
	// 返回深拷贝，避免调用方修改污染 store 内部状态。
	return cloneArtifact(latest), nil
}

func (s *MemoryStore) List(_ context.Context, sessionID string) ([]Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 按名称聚合，取最新版本
	latestByName := make(map[string]*Artifact)
	for _, art := range s.artifacts {
		if art.SessionID != sessionID {
			continue
		}
		if existing, ok := latestByName[art.Name]; !ok || art.Version > existing.Version {
			latestByName[art.Name] = art
		}
	}

	var result []Artifact
	for _, art := range latestByName {
		// 返回深拷贝，避免调用方修改污染 store 内部状态。
		result = append(result, *cloneArtifact(art))
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, nil
}

func (s *MemoryStore) ListVersions(_ context.Context, sessionID, name string) ([]Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var versions []Artifact
	for _, art := range s.artifacts {
		if art.SessionID == sessionID && art.Name == name {
			// 返回深拷贝，避免调用方修改污染 store 内部状态。
			versions = append(versions, *cloneArtifact(art))
		}
	}

	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Version < versions[j].Version
	})

	return versions, nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.artifacts, id)
	return nil
}

func (s *MemoryStore) DeleteAll(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, art := range s.artifacts {
		if art.SessionID == sessionID {
			delete(s.artifacts, id)
		}
	}
	return nil
}

var _ Store = (*MemoryStore)(nil)
