// Package skill 提供 Skill（技能）注册、发现和执行系统
//
// Skill 是可复用的 Agent 能力单元，支持：
//   - 动态注册和注销
//   - 按名称/标签/描述搜索
//   - 启用/禁用控制
//   - 签名验证确保安全
//   - 变更钩子通知
//
// 使用示例：
//
//	registry := skill.NewRegistry()
//	registry.Register(&skill.Skill{
//	    Name:        "translate",
//	    Description: "多语言翻译",
//	    Handler: func(ctx context.Context, input map[string]any) (map[string]any, error) {
//	        return map[string]any{"result": "translated"}, nil
//	    },
//	})
//	result, err := registry.Execute(ctx, "translate", input)
package skill

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/reflectx"
)

// ============== 错误定义 ==============

var (
	// ErrSkillNotFound 技能未找到
	ErrSkillNotFound = errors.New("skill: 技能未找到")

	// ErrSkillExists 技能已存在
	ErrSkillExists = errors.New("skill: 技能已存在")

	// ErrSkillDisabled 技能已禁用
	ErrSkillDisabled = errors.New("skill: 技能已禁用")

	// ErrInvalidSkill 无效的技能定义
	ErrInvalidSkill = errors.New("skill: 无效的技能定义")
)

// ============== Skill 定义 ==============

// Skill 技能定义
type Skill struct {
	// Kind 区分可调用 Handler 和仅提供说明与资源的文档技能，零值沿用 Handler。
	Kind Kind `json:"kind,omitempty"`

	// Scope 是宿主声明的目录作用域；空值表示共享目录，不表示业务授权。
	Scope string `json:"scope,omitempty"`

	// Document 固定文档来源、不可变修订和资源根；仅文档技能使用。
	Document *DocumentReference `json:"document,omitempty"`

	// Name 技能名称（唯一标识）
	Name string `json:"name"`

	// Description 技能描述
	Description string `json:"description"`

	// Version 版本号
	Version string `json:"version"`

	// Author 作者
	Author string `json:"author,omitempty"`

	// Tags 标签列表（用于分类和搜索）
	Tags []string `json:"tags,omitempty"`

	// Schema 输入参数 Schema（JSON Schema 格式）
	Schema map[string]any `json:"schema,omitempty"`

	// Handler 执行处理函数
	Handler func(ctx context.Context, input map[string]any) (map[string]any, error) `json:"-"`

	// CreatedAt 注册时间
	CreatedAt time.Time `json:"created_at"`

	// Enabled 是否启用
	Enabled bool `json:"enabled"`
}

// ============== Hook ==============

// HookEvent 钩子事件类型
type HookEvent string

const (
	// EventRegistered 技能已注册
	EventRegistered HookEvent = "registered"

	// EventUnregistered 技能已注销
	EventUnregistered HookEvent = "unregistered"

	// EventEnabled 技能已启用
	EventEnabled HookEvent = "enabled"

	// EventDisabled 技能已禁用
	EventDisabled HookEvent = "disabled"
)

// Hook 变更钩子函数
type Hook func(event HookEvent, skill *Skill)

// ============== Registry ==============

// Registry 技能注册中心
//
// 管理所有已注册的 Skill，提供增删改查和执行能力。
// 线程安全。
type Registry struct {
	skills    map[string]*Skill
	hooks     []Hook
	mu        sync.RWMutex
	sources   map[string]DocumentSource
	revisions map[string]*Skill
}

// NewRegistry 创建技能注册中心
func NewRegistry() *Registry {
	return &Registry{
		skills:    make(map[string]*Skill),
		sources:   make(map[string]DocumentSource),
		revisions: make(map[string]*Skill),
	}
}

// Register 注册技能
//
// 要求 Name 非空且唯一。若 Skill 为首次注册则自动设置 CreatedAt 和 Enabled。
func (r *Registry) Register(s *Skill) error {
	if s == nil || s.Name == "" {
		return ErrInvalidSkill
	}
	if err := validateSkill(s); err != nil {
		return ErrInvalidSkill
	}
	frozen, err := freezeSkill(s)
	if err != nil {
		return err
	}

	r.mu.Lock()

	key := skillKey(s.Scope, s.Name)
	if _, exists := r.skills[key]; exists {
		r.mu.Unlock()
		return ErrSkillExists
	}
	if _, exists := r.revisions[revisionKey(frozen)]; exists {
		r.mu.Unlock()
		return ErrSkillExists
	}

	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	s.Enabled = true

	frozen.CreatedAt = s.CreatedAt
	frozen.Enabled = true
	r.skills[key] = frozen
	r.revisions[revisionKey(frozen)] = copySkill(frozen)
	hooks := make([]Hook, len(r.hooks))
	copy(hooks, r.hooks)
	skillCopy := copySkill(frozen)
	r.mu.Unlock()

	// 在锁外调用钩子，防止用户回调中调用 Registry 方法导致死锁
	notifyHooksUnlocked(hooks, EventRegistered, skillCopy)
	return nil
}

// Unregister 注销技能
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()

	s, exists := r.skills[skillKey("", name)]
	if !exists {
		r.mu.Unlock()
		return ErrSkillNotFound
	}

	delete(r.skills, skillKey("", name))
	hooks := make([]Hook, len(r.hooks))
	copy(hooks, r.hooks)
	skillCopy := copySkill(s)
	r.mu.Unlock()

	notifyHooksUnlocked(hooks, EventUnregistered, skillCopy)
	return nil
}

// Get 获取技能（返回副本，不会泄露内部指针）
func (r *Registry) Get(name string) (*Skill, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	s, exists := r.skills[skillKey("", name)]
	if !exists {
		return nil, ErrSkillNotFound
	}
	return copySkill(s), nil
}

// copySkill 复制定义的可变元数据，Handler 按不可变函数引用借用。
func copySkill(s *Skill) *Skill {
	c := *s
	if s.Tags != nil {
		c.Tags = make([]string, len(s.Tags))
		copy(c.Tags, s.Tags)
	}
	c.Schema = reflectx.DeepCopy(s.Schema)
	if s.Document != nil {
		ref := s.Document.Clone()
		c.Document = &ref
	}
	return &c
}

// List 列出共享作用域内已启用的技能（返回副本）；指定作用域使用 Catalog。
func (r *Registry) List() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*Skill
	for _, s := range r.skills {
		if s.Enabled && s.Scope == "" {
			result = append(result, copySkill(s))
		}
	}
	return result
}

// ListAll 列出共享作用域内全部技能（包括已禁用的，返回副本）。
func (r *Registry) ListAll() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		if s.Scope != "" {
			continue
		}
		result = append(result, copySkill(s))
	}
	return result
}

// Search 搜索技能（返回副本）
//
// 在名称、描述和标签中进行大小写不敏感的子串匹配。
func (r *Registry) Search(query string) []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	query = strings.ToLower(query)
	var result []*Skill
	for _, s := range r.skills {
		if !s.Enabled || s.Scope != "" {
			continue
		}
		if matchesQuery(s, query) {
			result = append(result, copySkill(s))
		}
	}
	return result
}

// Enable 启用技能
func (r *Registry) Enable(name string) error {
	r.mu.Lock()

	s, exists := r.skills[skillKey("", name)]
	if !exists {
		r.mu.Unlock()
		return ErrSkillNotFound
	}
	s.Enabled = true
	hooks := make([]Hook, len(r.hooks))
	copy(hooks, r.hooks)
	skillCopy := copySkill(s)
	r.mu.Unlock()

	notifyHooksUnlocked(hooks, EventEnabled, skillCopy)
	return nil
}

// Disable 禁用技能
func (r *Registry) Disable(name string) error {
	r.mu.Lock()

	s, exists := r.skills[skillKey("", name)]
	if !exists {
		r.mu.Unlock()
		return ErrSkillNotFound
	}
	s.Enabled = false
	hooks := make([]Hook, len(r.hooks))
	copy(hooks, r.hooks)
	skillCopy := copySkill(s)
	r.mu.Unlock()

	notifyHooksUnlocked(hooks, EventDisabled, skillCopy)
	return nil
}

// OnChange 注册变更钩子
func (r *Registry) OnChange(hook Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, hook)
}

// Execute 查找并执行技能
func (r *Registry) Execute(ctx context.Context, name string, input map[string]any) (map[string]any, error) {
	r.mu.RLock()
	s, exists := r.skills[skillKey("", name)]
	if exists {
		s = copySkill(s)
	}
	r.mu.RUnlock()

	if !exists {
		return nil, ErrSkillNotFound
	}
	if !s.Enabled {
		return nil, ErrSkillDisabled
	}
	if s.Kind == KindDocument {
		return nil, ErrDocumentNotExecutable
	}
	return s.Handler(ctx, input)
}

// Count 返回已注册技能数量
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.skills)
}

// notifyHooksUnlocked 在锁外通知所有钩子
//
// 接收钩子列表的快照而非直接访问 r.hooks，避免在持锁状态下调用用户回调。
// 用户回调中可以安全地调用 Registry 的其他方法。
func notifyHooksUnlocked(hooks []Hook, event HookEvent, s *Skill) {
	for _, hook := range hooks {
		func() {
			defer func() { recover() }() // 防止钩子 panic 影响主流程
			hook(event, s)
		}()
	}
}

// matchesQuery 检查技能是否匹配搜索词
func matchesQuery(s *Skill, query string) bool {
	if strings.Contains(strings.ToLower(s.Name), query) {
		return true
	}
	if strings.Contains(strings.ToLower(s.Description), query) {
		return true
	}
	for _, tag := range s.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}
