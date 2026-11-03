// Package plugin 提供插件热更新能力
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/lang/mapx"
	"github.com/hexagon-codes/toolkit/util/hash"
	"github.com/hexagon-codes/toolkit/util/reflectx"
)

// HotReloadManager 插件热更新管理器
//
// 监控插件配置变化，自动重新加载插件。
// 支持：
//   - 插件配置变更检测
//   - 插件版本管理
//   - 插件热更新（无需重启）
//   - 依赖处理
//
// 线程安全：所有方法都是并发安全的
type HotReloadManager struct {
	// manager 插件管理器
	manager *PluginManager

	// configPaths 监控的配置文件路径
	configPaths map[string]string // name -> path

	// lastModTimes 上次修改时间
	lastModTimes map[string]time.Time

	// interval 检查间隔
	interval time.Duration

	// callback 重载回调
	callback HotReloadCallback

	// running 运行状态
	running bool

	// ctx 上下文
	ctx context.Context

	// cancel 取消函数
	cancel context.CancelFunc

	// mu 互斥锁
	mu sync.RWMutex

	// versions 插件版本记录
	versions    map[string][]PluginVersion
	retirements map[string]*Retirement
	done        chan struct{}
	// lastError 保留最近一次热更新观察回调的 panic/失败，供宿主诊断。
	lastError error
}

// HotReloadCallback 热更新回调
type HotReloadCallback func(name string, oldVersion, newVersion *PluginVersion, err error)

// PluginVersion 插件版本信息
type PluginVersion struct {
	// Version 版本号
	Version string

	// LoadedAt 加载时间
	LoadedAt time.Time

	// Config 配置
	Config map[string]any

	// Hash 配置哈希
	Hash string
}

// HotReloadOption 热更新选项
type HotReloadOption func(*HotReloadManager)

// WithHotReloadInterval 设置检查间隔
func WithHotReloadInterval(interval time.Duration) HotReloadOption {
	return func(m *HotReloadManager) {
		m.interval = interval
	}
}

// WithHotReloadCallback 设置回调
func WithHotReloadCallback(callback HotReloadCallback) HotReloadOption {
	return func(m *HotReloadManager) {
		m.callback = callback
	}
}

// NewHotReloadManager 创建插件热更新管理器
func NewHotReloadManager(manager *PluginManager, opts ...HotReloadOption) *HotReloadManager {
	ctx, cancel := context.WithCancel(context.Background())

	m := &HotReloadManager{
		manager:      manager,
		configPaths:  make(map[string]string),
		lastModTimes: make(map[string]time.Time),
		interval:     5 * time.Second,
		ctx:          ctx,
		cancel:       cancel,
		versions:     make(map[string][]PluginVersion),
		retirements:  make(map[string]*Retirement),
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// Watch 监控插件配置文件
func (m *HotReloadManager) Watch(name, configPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查文件是否存在
	info, err := os.Stat(configPath)
	if err != nil {
		return fmt.Errorf("failed to stat config file: %w", err)
	}

	m.configPaths[name] = configPath
	m.lastModTimes[name] = info.ModTime()

	// 初始化版本历史
	if _, exists := m.versions[name]; !exists {
		m.versions[name] = make([]PluginVersion, 0)
	}

	return nil
}

// Unwatch 停止监控插件
func (m *HotReloadManager) Unwatch(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.configPaths, name)
	delete(m.lastModTimes, name)
}

// Start 启动热更新监控
func (m *HotReloadManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("hot reload manager already running")
	}

	m.running = true
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.done = make(chan struct{})

	// 启动监控 goroutine
	go m.watch(m.ctx, m.done)

	return nil
}

// Stop 停止热更新监控
func (m *HotReloadManager) Stop() {
	m.mu.Lock()

	if !m.running {
		m.mu.Unlock()
		return
	}

	m.cancel()
	m.running = false
	done := m.done
	m.mu.Unlock()
	<-done
}

// IsRunning 返回运行状态
func (m *HotReloadManager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// LastError 返回最近一次热更新观察回调的 panic/失败；成功回调会清除旧值。
// 回调失败不回滚已经发布的热更新，也不终止监控协程。
func (m *HotReloadManager) LastError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastError
}

// watch 监控配置变化
func (m *HotReloadManager) watch(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkAndReload()
		}
	}
}

// checkAndReload 检查并重新加载插件
func (m *HotReloadManager) checkAndReload() {
	m.mu.RLock()
	paths := make(map[string]string)
	for name, path := range m.configPaths {
		paths[name] = path
	}
	m.mu.RUnlock()

	for name, path := range paths {
		// 检查文件修改时间
		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		m.mu.RLock()
		lastMod := m.lastModTimes[name]
		m.mu.RUnlock()

		if info.ModTime().After(lastMod) {
			// 配置已修改，重新加载
			m.reloadPlugin(name, path, info.ModTime())
		}
	}
}

// reloadPlugin 重新加载插件
func (m *HotReloadManager) reloadPlugin(name, path string, modTime time.Time) {
	// 获取当前版本
	oldVersion := m.getCurrentVersion(name)

	// 重新加载插件
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := m.reloadWithRollback(ctx, name, path)

	// 更新修改时间
	m.mu.Lock()
	if err == nil {
		m.lastModTimes[name] = modTime
	}
	m.mu.Unlock()

	// 触发回调。观察回调失败/panic 不终止监控协程，也不回滚已发布热更新；
	// 错误通过 LastError 暴露，调用方仍可自行记录。
	if m.callback != nil {
		newVersion := m.getCurrentVersion(name)
		m.mu.Lock()
		m.lastError = nil
		m.mu.Unlock()
		callbackErr := invokePluginCallback(name, "hot_reload.observe", func() error {
			m.callback(name, oldVersion, newVersion, err)
			return nil
		})
		if callbackErr != nil {
			m.mu.Lock()
			m.lastError = callbackErr
			m.mu.Unlock()
		}
	}
}

// reloadWithRollback 重新加载插件（带回滚）
func (m *HotReloadManager) reloadWithRollback(ctx context.Context, name, path string) error {
	loader := NewLoader(m.manager.Registry, m.manager.Lifecycle)
	retirement, err := loader.ReplaceFromConfig(ctx, name, path)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.retirements[name] = retirement
	m.mu.Unlock()
	instance, _ := m.manager.Registry.GetInstance(name)
	m.saveVersion(name, instance.Config)
	return nil
}

// WaitForDrain 等待最近一次成功发布所替换资源排空，不持有发布锁。
func (m *HotReloadManager) WaitForDrain(ctx context.Context, name string) error {
	m.mu.RLock()
	retirement := m.retirements[name]
	m.mu.RUnlock()
	return retirement.Wait(ctx)
}

// getCurrentVersion 获取当前版本
func (m *HotReloadManager) getCurrentVersion(name string) *PluginVersion {
	m.mu.RLock()
	defer m.mu.RUnlock()

	versions, exists := m.versions[name]
	if !exists || len(versions) == 0 {
		return nil
	}

	version := copyPluginVersion(versions[len(versions)-1])
	return &version
}

// saveVersion 保存版本
func (m *HotReloadManager) saveVersion(name string, config map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()

	version := PluginVersion{
		Version:  time.Now().UTC().Format("20060102T150405.000000000Z"),
		LoadedAt: time.Now(),
		Config:   reflectx.DeepCopy(config),
		Hash:     computeConfigHash(config),
	}

	m.versions[name] = append(m.versions[name], version)

	// 保留最近 10 个版本
	if len(m.versions[name]) > 10 {
		m.versions[name] = m.versions[name][len(m.versions[name])-10:]
	}
}

// GetVersionHistory 获取版本历史
func (m *HotReloadManager) GetVersionHistory(name string) []PluginVersion {
	m.mu.RLock()
	defer m.mu.RUnlock()

	versions, exists := m.versions[name]
	if !exists {
		return nil
	}

	result := make([]PluginVersion, len(versions))
	for i, version := range versions {
		result[i] = copyPluginVersion(version)
	}
	return result
}

func copyPluginVersion(version PluginVersion) PluginVersion {
	version.Config = reflectx.DeepCopy(version.Config)
	return version
}

// RollbackToVersion 回滚到指定版本
func (m *HotReloadManager) RollbackToVersion(ctx context.Context, name, version string) error {
	m.mu.RLock()
	versions, exists := m.versions[name]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("no version history for plugin %s", name)
	}

	// 查找版本
	var targetVersion *PluginVersion
	for i := range versions {
		if versions[i].Version == version {
			targetVersion = &versions[i]
			break
		}
	}

	if targetVersion == nil {
		return fmt.Errorf("version %s not found for plugin %s", version, name)
	}

	candidate, err := m.manager.Registry.CreateFromFactory(name)
	if err != nil {
		return err
	}
	retirement, err := m.manager.ReplaceCandidate(ctx, name, candidate, targetVersion.Config)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.retirements[name] = retirement
	m.mu.Unlock()
	return nil
}

// computeConfigHash 计算配置哈希
//
// 使用 SHA-256 对配置进行哈希，确保相同配置生成相同指纹。
// 通过排序 map 的 key 来保证序列化结果的稳定性。
func computeConfigHash(config map[string]any) string {
	if config == nil {
		return ""
	}

	// 将配置序列化为 JSON（使用稳定排序）
	data, err := json.Marshal(sortMapForHash(config))
	if err != nil {
		return ""
	}

	// 复用 toolkit 的唯一 SHA-256 字节入口。
	return hash.SHA256Bytes(data)
}

// sortMapForHash 递归排序 map 的 key 以确保哈希稳定性
func sortMapForHash(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}

	// 获取排序后的 key
	keys := mapx.Keys(m)
	sort.Strings(keys)

	// 构建有序 map（Go 1.12+ 的 json.Marshal 会保持 map 的插入顺序）
	result := make(map[string]any, len(m))
	for _, k := range keys {
		v := m[k]
		// 递归处理嵌套的 map
		if nested, ok := v.(map[string]any); ok {
			result[k] = sortMapForHash(nested)
		} else {
			result[k] = v
		}
	}

	return result
}

// PluginUpdateStrategy 插件更新策略
type PluginUpdateStrategy string

const (
	// UpdateStrategyImmediate 立即更新
	UpdateStrategyImmediate PluginUpdateStrategy = "immediate"

	// UpdateStrategyGraceful 优雅更新（等待当前任务完成）
	UpdateStrategyGraceful PluginUpdateStrategy = "graceful"

	// UpdateStrategyScheduled 计划更新（在指定时间更新）
	UpdateStrategyScheduled PluginUpdateStrategy = "scheduled"
)

// UpdateOptions 更新选项
type UpdateOptions struct {
	// Strategy 更新策略
	Strategy PluginUpdateStrategy

	// GracePeriod 优雅期（用于 Graceful 策略）
	GracePeriod time.Duration

	// ScheduledTime 计划时间（用于 Scheduled 策略）
	ScheduledTime time.Time

	// BackupConfig 是否备份配置
	BackupConfig bool

	// RollbackOnError 错误时是否自动回滚
	RollbackOnError bool
}

// DefaultUpdateOptions 默认更新选项
func DefaultUpdateOptions() UpdateOptions {
	return UpdateOptions{
		Strategy:        UpdateStrategyImmediate,
		GracePeriod:     30 * time.Second,
		BackupConfig:    true,
		RollbackOnError: true,
	}
}

// UpdatePlugin 更新插件（带策略）
func (m *HotReloadManager) UpdatePlugin(ctx context.Context, name, configPath string, opts UpdateOptions) error {
	switch opts.Strategy {
	case UpdateStrategyImmediate:
		return m.updateImmediate(ctx, name, configPath, opts)

	case UpdateStrategyGraceful:
		return m.updateGraceful(ctx, name, configPath, opts)

	case UpdateStrategyScheduled:
		return m.updateScheduled(ctx, name, configPath, opts)

	default:
		return fmt.Errorf("unknown update strategy: %s", opts.Strategy)
	}
}

// updateImmediate 立即更新
func (m *HotReloadManager) updateImmediate(ctx context.Context, name, configPath string, opts UpdateOptions) error {
	// 重新加载
	err := m.reloadWithRollback(ctx, name, configPath)

	// 候选失败时旧路由从未改变，不再次操作旧实例。

	return err
}

// updateGraceful 优雅更新
//
// 先发布可用候选，再在优雅期内等待旧资源真实排空；超时不会强停旧 Run。
func (m *HotReloadManager) updateGraceful(ctx context.Context, name, configPath string, opts UpdateOptions) error {
	if err := m.updateImmediate(ctx, name, configPath, opts); err != nil {
		return err
	}
	if opts.GracePeriod > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.GracePeriod)
		defer cancel()
	}
	return m.WaitForDrain(ctx, name)
}

// updateScheduled 计划更新
func (m *HotReloadManager) updateScheduled(ctx context.Context, name, configPath string, opts UpdateOptions) error {
	// 等待到计划时间
	now := time.Now()
	if opts.ScheduledTime.After(now) {
		delay := opts.ScheduledTime.Sub(now)
		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			// 到达计划时间，执行更新
			return m.updateImmediate(ctx, name, configPath, opts)
		}
	}

	// 计划时间已过，立即执行
	return m.updateImmediate(ctx, name, configPath, opts)
}

// GetWatchedPlugins 获取所有监控的插件
func (m *HotReloadManager) GetWatchedPlugins() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return mapx.Keys(m.configPaths)
}

// Stats 获取统计信息
func (m *HotReloadManager) Stats() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	totalVersions := 0
	for _, versions := range m.versions {
		totalVersions += len(versions)
	}

	return map[string]any{
		"watched_plugins": len(m.configPaths),
		"total_versions":  totalVersions,
		"running":         m.running,
		"interval":        m.interval.String(),
	}
}
