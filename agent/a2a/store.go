package a2a

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/hexagon-codes/hexagon/internal/util"
)

// ============== TaskStore 接口 ==============

// TaskStore 任务存储接口
// 提供任务的持久化存储能力。
type TaskStore interface {
	// Create 创建任务
	Create(ctx context.Context, task *Task) error

	// Get 获取任务
	Get(ctx context.Context, id string) (*Task, error)

	// Update 更新任务
	Update(ctx context.Context, task *Task) error

	// Delete 删除任务
	Delete(ctx context.Context, id string) error

	// List 列出任务
	List(ctx context.Context, opts *ListTasksRequest) (*ListTasksResponse, error)

	// GenerateID 生成任务 ID
	GenerateID() string
}

// ============== MemoryTaskStore ==============

// MemoryTaskStore 内存任务存储
// 用于开发和测试，生产环境应使用持久化存储。
//
// 重要：使用完毕后必须调用 Close() 释放清理协程，否则会导致 goroutine 泄漏。
type MemoryTaskStore struct {
	// tasks 任务映射 (id -> task)
	tasks map[string]*Task

	// sessionTasks 会话任务映射 (sessionId -> []taskId)
	sessionTasks map[string][]string

	// pushConfigs 推送配置 (taskId -> config)
	pushConfigs map[string]*PushNotificationConfig

	// maxTasks 最大任务数（0 表示无限制）
	maxTasks int

	// taskTTL 任务过期时间（0 表示永不过期）
	taskTTL time.Duration

	// done 用于通知清理协程停止
	done chan struct{}

	mu sync.RWMutex
}

// MemoryStoreOption 内存存储选项
type MemoryStoreOption func(*MemoryTaskStore)

// NewMemoryTaskStore 创建内存任务存储
func NewMemoryTaskStore(opts ...MemoryStoreOption) *MemoryTaskStore {
	s := &MemoryTaskStore{
		tasks:        make(map[string]*Task),
		sessionTasks: make(map[string][]string),
		pushConfigs:  make(map[string]*PushNotificationConfig),
		maxTasks:     10000,
		taskTTL:      24 * time.Hour,
		done:         make(chan struct{}),
	}

	for _, opt := range opts {
		opt(s)
	}

	// 启动清理协程
	if s.taskTTL > 0 {
		go s.cleanupLoop()
	}

	return s
}

// WithMaxTasks 设置最大任务数
func WithMaxTasks(max int) MemoryStoreOption {
	return func(s *MemoryTaskStore) {
		s.maxTasks = max
	}
}

// WithTaskTTL 设置任务过期时间
func WithTaskTTL(ttl time.Duration) MemoryStoreOption {
	return func(s *MemoryTaskStore) {
		s.taskTTL = ttl
	}
}

// GenerateID 生成任务 ID
func (s *MemoryTaskStore) GenerateID() string {
	return util.GenerateID("task")
}

// Create 创建任务
func (s *MemoryTaskStore) Create(_ context.Context, task *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查是否超过最大任务数
	if s.maxTasks > 0 && len(s.tasks) >= s.maxTasks {
		// 清理最旧的已完成任务
		s.cleanupOldTasks()
	}

	// 检查任务是否已存在
	if _, exists := s.tasks[task.ID]; exists {
		return NewInvalidParamsError("task already exists: " + task.ID)
	}

	// 设置时间戳
	now := time.Now()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	task.UpdatedAt = now

	// 保存任务
	s.tasks[task.ID] = task

	// 更新会话索引
	if task.SessionID != "" {
		s.sessionTasks[task.SessionID] = append(s.sessionTasks[task.SessionID], task.ID)
	}

	return nil
}

// Get 获取任务
func (s *MemoryTaskStore) Get(_ context.Context, id string) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, exists := s.tasks[id]
	if !exists {
		return nil, ErrTaskNotFound
	}

	// 回归: B13 — copy-on-read 返回深拷贝, 避免调用方就地修改污染存储内部状态。
	return cloneTask(task), nil
}

// Update 更新任务
func (s *MemoryTaskStore) Update(_ context.Context, task *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.tasks[task.ID]; !exists {
		return ErrTaskNotFound
	}

	// 回归: B13 — 存入前深拷贝隔离, 防止调用方后续修改传入指针影响存储内部状态。
	stored := cloneTask(task)
	stored.UpdatedAt = time.Now()
	s.tasks[task.ID] = stored

	return nil
}

// Delete 删除任务
func (s *MemoryTaskStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, exists := s.tasks[id]
	if !exists {
		return ErrTaskNotFound
	}

	// 从会话索引中移除
	if task.SessionID != "" {
		s.removeFromSession(task.SessionID, id)
	}

	// 删除推送配置
	delete(s.pushConfigs, id)

	// 删除任务
	delete(s.tasks, id)

	return nil
}

// List 列出任务
func (s *MemoryTaskStore) List(_ context.Context, opts *ListTasksRequest) (*ListTasksResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if opts == nil {
		opts = &ListTasksRequest{}
	}

	// 收集符合条件的任务
	var tasks []*Task

	// 如果指定了会话 ID，只查询该会话的任务
	if opts.SessionID != "" {
		taskIDs := s.sessionTasks[opts.SessionID]
		for _, id := range taskIDs {
			if task, exists := s.tasks[id]; exists {
				if s.matchTaskFilter(task, opts) {
					tasks = append(tasks, task)
				}
			}
		}
	} else {
		for _, task := range s.tasks {
			if s.matchTaskFilter(task, opts) {
				tasks = append(tasks, task)
			}
		}
	}

	// 按创建时间倒序排序
	sortTasksByCreatedAt(tasks)

	total := len(tasks)

	// 应用分页
	if opts.Offset > 0 {
		if opts.Offset >= len(tasks) {
			tasks = nil
		} else {
			tasks = tasks[opts.Offset:]
		}
	}
	if opts.Limit > 0 && len(tasks) > opts.Limit {
		tasks = tasks[:opts.Limit]
	}

	return &ListTasksResponse{
		Tasks: tasks,
		Total: total,
	}, nil
}

// matchTaskFilter 检查任务是否匹配过滤条件
func (s *MemoryTaskStore) matchTaskFilter(task *Task, opts *ListTasksRequest) bool {
	// 状态过滤
	if len(opts.Status) > 0 {
		if !slices.Contains(opts.Status, task.Status.State) {
			return false
		}
	}

	return true
}

// removeFromSession 从会话索引中移除任务
func (s *MemoryTaskStore) removeFromSession(sessionID, taskID string) {
	taskIDs := s.sessionTasks[sessionID]
	for i, id := range taskIDs {
		if id == taskID {
			s.sessionTasks[sessionID] = append(taskIDs[:i], taskIDs[i+1:]...)
			break
		}
	}
	// 如果会话没有任务了，删除会话索引
	if len(s.sessionTasks[sessionID]) == 0 {
		delete(s.sessionTasks, sessionID)
	}
}

// cleanupOldTasks 清理最旧的已完成任务
func (s *MemoryTaskStore) cleanupOldTasks() {
	// 收集已完成的任务
	var completedTasks []*Task
	for _, task := range s.tasks {
		if task.Status.State.IsTerminal() {
			completedTasks = append(completedTasks, task)
		}
	}

	// 按创建时间排序
	sortTasksByCreatedAt(completedTasks)

	// 删除最旧的一半已完成任务
	deleteCount := len(completedTasks) / 2
	if deleteCount == 0 {
		deleteCount = 1
	}

	for i := len(completedTasks) - 1; i >= len(completedTasks)-deleteCount && i >= 0; i-- {
		task := completedTasks[i]
		if task.SessionID != "" {
			s.removeFromSession(task.SessionID, task.ID)
		}
		delete(s.pushConfigs, task.ID)
		delete(s.tasks, task.ID)
	}
}

// Close 关闭内存任务存储，停止清理协程
// 必须在不再使用 MemoryTaskStore 时调用，否则清理协程会泄漏。
func (s *MemoryTaskStore) Close() {
	close(s.done)
}

// cleanupLoop 定期清理过期任务
// 通过 done channel 接收停止信号，避免 goroutine 泄漏。
func (s *MemoryTaskStore) cleanupLoop() {
	ticker := time.NewTicker(s.taskTTL / 10)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.cleanupExpiredTasks()
		}
	}
}

// cleanupExpiredTasks 清理过期任务
func (s *MemoryTaskStore) cleanupExpiredTasks() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, task := range s.tasks {
		// 只清理已完成的任务
		if task.Status.State.IsTerminal() {
			if now.Sub(task.UpdatedAt) > s.taskTTL {
				if task.SessionID != "" {
					s.removeFromSession(task.SessionID, id)
				}
				delete(s.pushConfigs, id)
				delete(s.tasks, id)
			}
		}
	}
}

// ============== 推送配置存储 ==============

// SetPushConfig 设置推送配置
func (s *MemoryTaskStore) SetPushConfig(_ context.Context, taskID string, config *PushNotificationConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.tasks[taskID]; !exists {
		return ErrTaskNotFound
	}

	s.pushConfigs[taskID] = config
	return nil
}

// GetPushConfig 获取推送配置
func (s *MemoryTaskStore) GetPushConfig(_ context.Context, taskID string) (*PushNotificationConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, exists := s.tasks[taskID]; !exists {
		return nil, ErrTaskNotFound
	}

	return s.pushConfigs[taskID], nil
}

// ============== 辅助函数 ==============

// sortTasksByCreatedAt 按创建时间倒序排序
//
// 回归: B15 — 改用 slices.SortFunc 取代原 O(n^2) 手写选择排序, 保持创建时间降序。
func sortTasksByCreatedAt(tasks []*Task) {
	slices.SortFunc(tasks, func(a, b *Task) int {
		// 创建时间晚的排前面 (降序)
		return b.CreatedAt.Compare(a.CreatedAt)
	})
}

// cloneTask 对任务执行深拷贝, 隔离引用类型字段, 避免存储内部状态被调用方污染。
//
// 回归: B13/B14 — Get/Update 借此实现 copy-on-read / 写入隔离。
// 拷贝范围: History 切片、Artifacts 切片、Metadata map 均独立分配;
// 标量与不可变值 (ID/Status/时间戳) 直接复制。
func cloneTask(task *Task) *Task {
	if task == nil {
		return nil
	}

	cloned := *task

	if task.History != nil {
		cloned.History = slices.Clone(task.History)
	}
	if task.Artifacts != nil {
		cloned.Artifacts = slices.Clone(task.Artifacts)
	}
	if task.Metadata != nil {
		cloned.Metadata = maps.Clone(task.Metadata)
	}

	return &cloned
}

// ============== PushConfigStore 接口 ==============

// PushConfigStore 推送配置存储接口
type PushConfigStore interface {
	// SetPushConfig 设置推送配置
	SetPushConfig(ctx context.Context, taskID string, config *PushNotificationConfig) error

	// GetPushConfig 获取推送配置
	GetPushConfig(ctx context.Context, taskID string) (*PushNotificationConfig, error)
}
