// Package plugin 提供 Hexagon AI Agent 框架的插件系统
package plugin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/reflectx"
)

// Lifecycle 插件生命周期管理器
type Lifecycle struct {
	registry        *Registry
	mu              sync.RWMutex
	startOrder      []string
	healthCheckTick time.Duration
	stopChan        chan struct{}
	stopOnce        sync.Once // 确保 stopChan 只关闭一次
	operations      sync.Map
}

// LifecycleOption Lifecycle 选项
type LifecycleOption func(*Lifecycle)

// WithHealthCheckInterval 设置健康检查间隔
func WithHealthCheckInterval(d time.Duration) LifecycleOption {
	return func(l *Lifecycle) {
		l.healthCheckTick = d
	}
}

// NewLifecycle 创建生命周期管理器
func NewLifecycle(registry *Registry, opts ...LifecycleOption) *Lifecycle {
	l := &Lifecycle{
		registry:        registry,
		startOrder:      make([]string, 0),
		healthCheckTick: 30 * time.Second,
		stopChan:        make(chan struct{}),
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Init 初始化插件
func (l *Lifecycle) Init(ctx context.Context, name string, config map[string]any) error {
	unlock, waitErr := l.lockOperation(ctx, name)
	if waitErr != nil {
		return waitErr
	}
	defer unlock()
	instance, err := l.registry.getInstance(name)
	if err != nil {
		return err
	}

	state := l.registry.instanceState(instance)
	if state != PluginStateLoaded {
		return fmt.Errorf("plugin %s is not in loaded state (current: %s)", name, state)
	}

	frozen, err := reflectx.DeepCopyChecked(config)
	if err != nil {
		return fmt.Errorf("plugin config cannot be snapshotted: %w", err)
	}
	initConfig, err := reflectx.DeepCopyChecked(frozen)
	if err != nil {
		return err
	}
	for _, dependency := range instance.Plugin.Info().Dependencies {
		if dependency == name {
			return fmt.Errorf("plugin %s cannot depend on itself", name)
		}
	}
	bindings, err := l.registry.captureDependencies(instance.Plugin.Info().Dependencies)
	if err != nil {
		return err
	}
	dependencyCtx, view := contextWithDependencies(ctx, bindings)
	if err := invokePluginCallback(name, "init", func() error { return instance.Plugin.Init(dependencyCtx, initConfig) }); err != nil {
		for _, dependency := range bindings {
			releaseGeneration(dependency)
		}
		l.registry.mu.Lock()
		instance.State = PluginStateError
		instance.Error = err
		l.registry.mu.Unlock()
		return fmt.Errorf("failed to init plugin %s: %w", name, err)
	}

	used := view.usedNames()
	if !l.registry.legacyBindingsCurrent(bindings, used) {
		cleanupErr := invokePluginCallback(name, "init.cleanup.stop", func() error { return instance.Plugin.Stop(dependencyCtx) })
		for _, dependency := range bindings {
			releaseGeneration(dependency)
		}
		return errors.Join(fmt.Errorf("plugin %s dependencies changed during initialization", name), cleanupErr)
	}
	configHash := computeConfigHash(frozen)
	l.registry.mu.Lock()
	if l.registry.plugins[name] != instance {
		l.registry.mu.Unlock()
		cleanupErr := invokePluginCallback(name, "init.cleanup.stop", func() error { return instance.Plugin.Stop(dependencyCtx) })
		for _, dependency := range bindings {
			releaseGeneration(dependency)
		}
		return errors.Join(fmt.Errorf("plugin %s route changed during initialization", name), cleanupErr)
	}
	instance.State = PluginStateInitialized
	instance.Config = frozen
	instance.generation.mu.Lock()
	instance.generation.reference.ConfigHash = configHash
	instance.generation.bindings = bindings
	instance.generation.legacyBindings = make(map[string]bool)
	for _, dependency := range bindings {
		instance.generation.legacyBindings[dependency.reference.Name] = !used[dependency.reference.Name]
	}
	instance.generation.mu.Unlock()
	l.registry.mu.Unlock()

	l.registry.emitEvent(PluginEvent{
		Type:   PluginEventInitialized,
		Plugin: name,
	})

	return nil
}

// Start 启动插件。停止后重启时，若实际依赖代次已变化，先用原冻结配置重新初始化绑定；
// 依赖未变化时仍只调用 Start，不重复初始化。
func (l *Lifecycle) Start(ctx context.Context, name string) error {
	unlock, waitErr := l.lockOperation(ctx, name)
	if waitErr != nil {
		return waitErr
	}
	defer unlock()
	instance, err := l.registry.getInstance(name)
	if err != nil {
		return err
	}

	state := l.registry.instanceState(instance)
	if state != PluginStateInitialized && state != PluginStateStopped {
		return fmt.Errorf("plugin %s is not ready to start (current: %s)", name, state)
	}

	g := instance.generation
	rebind := false
	if state == PluginStateStopped {
		previous := g
		g = newGeneration(instance.Plugin, previous.reference)
		g.dependencies = append([]string(nil), previous.dependencies...)
		bindings, err := l.registry.captureDependencies(g.dependencies)
		if err != nil {
			return err
		}
		g.bindings = bindings
		previous.mu.Lock()
		oldBindings := append([]*generation(nil), previous.stoppedBindings...)
		previous.mu.Unlock()
		for _, dependency := range bindings {
			matched := false
			for _, old := range oldBindings {
				if dependency == old {
					matched = true
					break
				}
			}
			if !matched {
				rebind = true
			}
		}
		g.legacyBindings = make(map[string]bool)
		for _, dependency := range bindings {
			g.legacyBindings[dependency.reference.Name] = true
		}
	}
	g.mu.Lock()
	bindings := append([]*generation(nil), g.bindings...)
	legacy := make(map[string]bool, len(g.legacyBindings))
	for dependency, raw := range g.legacyBindings {
		legacy[dependency] = raw
	}
	g.mu.Unlock()
	for _, dependency := range bindings {
		dependency.mu.Lock()
		started, forced := dependency.started, dependency.forced
		dependency.mu.Unlock()
		if !started || forced {
			if state == PluginStateStopped {
				releaseDependencyBindings(g)
			}
			return fmt.Errorf("dependency %s is not running for plugin %s", dependency.reference.Name, name)
		}
	}
	dependencyCtx, view := contextWithDependencies(ctx, bindings)
	if rebind {
		config, copyErr := reflectx.DeepCopyChecked(instance.Config)
		if copyErr != nil {
			releaseDependencyBindings(g)
			return fmt.Errorf("plugin restart config cannot be snapshotted: %w", copyErr)
		}
		if err := invokePluginCallback(name, "restart.init", func() error { return instance.Plugin.Init(dependencyCtx, config) }); err != nil {
			cleanupErr := invokePluginCallback(name, "restart.init.cleanup.stop", func() error { return instance.Plugin.Stop(dependencyCtx) })
			releaseDependencyBindings(g)
			l.registry.mu.Lock()
			instance.State, instance.Error = PluginStateError, errors.Join(err, cleanupErr)
			l.registry.mu.Unlock()
			return fmt.Errorf("failed to reinitialize plugin %s: %w", name, errors.Join(err, cleanupErr))
		}
	}
	if err := invokePluginCallback(name, "start", func() error { return instance.Plugin.Start(dependencyCtx) }); err != nil {
		cleanupErr := invokePluginCallback(name, "start.cleanup.stop", func() error { return instance.Plugin.Stop(dependencyCtx) })
		releaseDependencyBindings(g)
		l.registry.mu.Lock()
		instance.State = PluginStateError
		instance.Error = errors.Join(err, cleanupErr)
		l.registry.mu.Unlock()
		return fmt.Errorf("failed to start plugin %s: %w", name, errors.Join(err, cleanupErr))
	}
	used := view.usedNames()
	for dependency, raw := range legacy {
		if !raw {
			used[dependency] = true
		}
	}
	if !l.registry.legacyBindingsCurrent(bindings, used) {
		cleanupErr := invokePluginCallback(name, "start.cleanup.stop", func() error { return instance.Plugin.Stop(dependencyCtx) })
		releaseDependencyBindings(g)
		l.registry.mu.Lock()
		instance.State = PluginStateError
		l.registry.mu.Unlock()
		return errors.Join(fmt.Errorf("plugin %s dependencies changed during startup", name), cleanupErr)
	}
	now := time.Now()
	l.registry.mu.Lock()
	if l.registry.plugins[name] != instance {
		l.registry.mu.Unlock()
		cleanupErr := invokePluginCallback(name, "start.cleanup.stop", func() error { return instance.Plugin.Stop(dependencyCtx) })
		releaseDependencyBindings(g)
		return errors.Join(fmt.Errorf("plugin %s route changed during startup", name), cleanupErr)
	}
	instance.generation = g
	g.mu.Lock()
	g.started = true
	g.mu.Unlock()
	instance.State, instance.StartedAt, instance.Error = PluginStateRunning, &now, nil
	l.registry.mu.Unlock()

	l.mu.Lock()
	l.startOrder = append(l.startOrder, name)
	l.mu.Unlock()

	l.registry.emitEvent(PluginEvent{
		Type:   PluginEventStarted,
		Plugin: name,
	})

	return nil
}

// Stop 停止插件
func (l *Lifecycle) Stop(ctx context.Context, name string) error {
	unlock, waitErr := l.lockOperation(ctx, name)
	if waitErr != nil {
		return waitErr
	}
	defer unlock()
	instance, err := l.registry.getInstance(name)
	if err != nil {
		return err
	}

	state := l.registry.instanceState(instance)
	if state != PluginStateRunning {
		return fmt.Errorf("plugin %s is not running (current: %s)", name, state)
	}

	// 只阻止仍使用此实际代次的运行依赖者，不把同名新版本误当旧绑定。
	l.registry.mu.RLock()
	for dependentName, dependent := range l.registry.plugins {
		if dependent.State != PluginStateRunning || dependent == instance {
			continue
		}
		dependent.generation.mu.Lock()
		for _, binding := range dependent.generation.bindings {
			if binding == instance.generation {
				dependent.generation.mu.Unlock()
				l.registry.mu.RUnlock()
				return fmt.Errorf("cannot stop plugin %s: plugin %s depends on it", name, dependentName)
			}
		}
		dependent.generation.mu.Unlock()
	}
	l.registry.mu.RUnlock()

	l.registry.mu.Lock()
	retireGeneration(instance.generation)
	l.registry.mu.Unlock()
	retirement := startRetirement([]*generation{instance.generation})
	if err := retirement.Wait(ctx); err != nil {
		// 等待取消不等于底层停止失败；退休资源仍会在引用排空后清理。
		if ctx.Err() != nil {
			return ctx.Err()
		}
		l.registry.mu.Lock()
		instance.State = PluginStateError
		instance.Error = err
		l.registry.mu.Unlock()
		return fmt.Errorf("failed to stop plugin %s: %w", name, err)
	}

	l.registry.mu.Lock()
	instance.State = PluginStateStopped
	instance.StartedAt = nil
	l.registry.mu.Unlock()

	l.mu.Lock()
	// 从启动顺序中移除
	for i, n := range l.startOrder {
		if n == name {
			l.startOrder = append(l.startOrder[:i], l.startOrder[i+1:]...)
			break
		}
	}
	l.mu.Unlock()

	l.registry.emitEvent(PluginEvent{
		Type:   PluginEventStopped,
		Plugin: name,
	})

	return nil
}

type lifecycleOperation struct {
	mu   sync.Mutex
	done chan struct{}
}

// 同名生命周期操作仍串行，但用户回调期间不持有操作锁或注册表锁。
func (l *Lifecycle) lockOperation(ctx context.Context, name string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, _ := l.operations.LoadOrStore(name, &lifecycleOperation{})
	operation := value.(*lifecycleOperation)
	for {
		operation.mu.Lock()
		if err := ctx.Err(); err != nil {
			operation.mu.Unlock()
			return nil, err
		}
		if operation.done == nil {
			done := make(chan struct{})
			operation.done = done
			operation.mu.Unlock()
			return func() {
				operation.mu.Lock()
				if operation.done == done {
					operation.done = nil
					close(done)
				}
				operation.mu.Unlock()
			}, nil
		}
		done := operation.done
		operation.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Restart 重启插件
func (l *Lifecycle) Restart(ctx context.Context, name string) error {
	instance, err := l.registry.GetInstance(name)
	if err != nil {
		return err
	}

	if instance.State == PluginStateRunning {
		if err := l.Stop(ctx, name); err != nil {
			return err
		}
	}

	return l.Start(ctx, name)
}

// InitAll 初始化所有插件
func (l *Lifecycle) InitAll(ctx context.Context, configs map[string]map[string]any) error {
	for _, info := range l.registry.List() {
		config := configs[info.Name]
		if config == nil {
			config = make(map[string]any)
		}

		if err := l.Init(ctx, info.Name, config); err != nil {
			return err
		}
	}

	return nil
}

// StartAll 启动所有插件（按依赖顺序）
func (l *Lifecycle) StartAll(ctx context.Context) error {
	// 获取所有插件并按依赖排序
	plugins := l.registry.List()
	sorted := l.sortByDependency(plugins)

	for _, info := range sorted {
		instance, _ := l.registry.GetInstance(info.Name)
		if instance == nil || instance.State == PluginStateRunning {
			continue
		}

		if instance.State != PluginStateInitialized && instance.State != PluginStateStopped {
			continue
		}

		if err := l.Start(ctx, info.Name); err != nil {
			return err
		}
	}

	return nil
}

// StopAll 停止所有插件（按启动的逆序）
// 即使某个插件停止失败，也会继续停止其他插件，最终返回所有错误的聚合。
func (l *Lifecycle) StopAll(ctx context.Context) error {
	l.mu.RLock()
	order := make([]string, len(l.startOrder))
	copy(order, l.startOrder)
	l.mu.RUnlock()

	// 逆序停止，收集所有错误
	var errs []error
	for i := len(order) - 1; i >= 0; i-- {
		name := order[i]
		if err := l.Stop(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("stop plugin %s: %w", name, err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// sortByDependency 按依赖关系排序（使用 Kahn 算法进行拓扑排序）
//
// 依赖关系说明：
//   - 如果插件 A 依赖插件 B，则 B 必须在 A 之前启动
//   - graph[A] = [B, C] 表示 A 依赖 B 和 C
//   - 返回的列表中，被依赖的插件排在前面
//
// 算法流程：
//  1. 构建反向依赖图：dependents[B] = [A] 表示 A 依赖 B
//  2. 计算每个插件的入度（依赖项数量）
//  3. 从入度为 0 的插件开始（无依赖的插件）
//  4. 处理一个插件后，更新依赖它的插件的入度
//  5. 重复直到所有插件都被处理
func (l *Lifecycle) sortByDependency(plugins []PluginInfo) []PluginInfo {
	if len(plugins) == 0 {
		return plugins
	}

	// 构建插件集合和映射
	pluginSet := make(map[string]bool)
	pluginMap := make(map[string]PluginInfo)
	for _, p := range plugins {
		pluginSet[p.Name] = true
		pluginMap[p.Name] = p
	}

	// 构建反向依赖图：dependents[B] = [A] 表示 A 依赖 B
	// 即 B 被 A 依赖，B 必须在 A 之前启动
	dependents := make(map[string][]string)

	// 计算入度：插件有多少个依赖项
	inDegree := make(map[string]int)
	for _, p := range plugins {
		inDegree[p.Name] = 0
	}

	for _, p := range plugins {
		for _, dep := range p.Dependencies {
			// 只考虑在当前插件集合中的依赖
			if pluginSet[dep] {
				inDegree[p.Name]++
				dependents[dep] = append(dependents[dep], p.Name)
			}
		}
	}

	// Kahn's algorithm：从入度为 0 的节点开始
	var queue []string
	for name, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, name)
		}
	}

	// 拓扑排序结果
	var sorted []PluginInfo

	for len(queue) > 0 {
		// 取出队列头部
		name := queue[0]
		queue = queue[1:]

		// 添加到结果
		if info, ok := pluginMap[name]; ok {
			sorted = append(sorted, info)
		}

		// 更新依赖当前插件的所有插件的入度
		for _, dependent := range dependents[name] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	// 检测循环依赖：如果排序结果少于输入插件数，说明存在循环依赖
	if len(sorted) < len(plugins) {
		// 存在循环依赖，返回原始列表（让后续启动检查报错）
		return plugins
	}

	return sorted
}

// HealthCheck 执行健康检查
func (l *Lifecycle) HealthCheck(ctx context.Context) map[string]HealthStatus {
	results := make(map[string]HealthStatus)

	for _, info := range l.registry.List() {
		lease, err := l.registry.Acquire(info.Name)
		if err != nil {
			results[info.Name] = HealthStatus{
				Status:    HealthStateUnknown,
				Message:   err.Error(),
				LastCheck: time.Now(),
			}
			continue
		}
		status := func() HealthStatus {
			defer lease.Release()
			plugin, _ := lease.Plugin(info.Name)
			return plugin.Health()
		}()
		results[info.Name] = status

		l.registry.emitEvent(PluginEvent{
			Type:   PluginEventHealthCheck,
			Plugin: info.Name,
			Data: map[string]any{
				"status": string(status.Status),
			},
		})
	}

	return results
}

// StartHealthChecker 启动健康检查器
func (l *Lifecycle) StartHealthChecker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(l.healthCheckTick)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-l.stopChan:
				return
			case <-ticker.C:
				l.HealthCheck(ctx)
			}
		}
	}()
}

// StopHealthChecker 停止健康检查器
//
// 线程安全：此方法可以被多次调用，只有第一次会真正关闭 channel
func (l *Lifecycle) StopHealthChecker() {
	l.stopOnce.Do(func() {
		close(l.stopChan)
	})
}

// GetStartOrder 获取启动顺序
func (l *Lifecycle) GetStartOrder() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	order := make([]string, len(l.startOrder))
	copy(order, l.startOrder)
	return order
}

// ============== PluginManager ==============

// PluginManager 插件管理器（组合 Registry 和 Lifecycle）
type PluginManager struct {
	Registry  *Registry
	Lifecycle *Lifecycle
}

// NewPluginManager 创建插件管理器
func NewPluginManager(opts ...LifecycleOption) *PluginManager {
	registry := NewRegistry()
	lifecycle := NewLifecycle(registry, opts...)

	return &PluginManager{
		Registry:  registry,
		Lifecycle: lifecycle,
	}
}

// Load 加载并注册插件
func (m *PluginManager) Load(plugin Plugin) error {
	return m.Registry.Register(plugin)
}

// LoadFromFactory 从工厂加载插件
func (m *PluginManager) LoadFromFactory(name string) error {
	plugin, err := m.Registry.CreateFromFactory(name)
	if err != nil {
		return err
	}
	return m.Registry.Register(plugin)
}

// Unload 卸载插件
func (m *PluginManager) Unload(ctx context.Context, name string) error {
	instance, err := m.Registry.GetInstance(name)
	if err != nil {
		return err
	}

	if instance.State == PluginStateRunning {
		if err := m.Lifecycle.Stop(ctx, name); err != nil {
			return err
		}
	}

	return m.Registry.Unregister(name)
}

// Enable 启用插件（初始化 + 启动）
func (m *PluginManager) Enable(ctx context.Context, name string, config map[string]any) error {
	if err := m.Lifecycle.Init(ctx, name, config); err != nil {
		return err
	}
	return m.Lifecycle.Start(ctx, name)
}

// Disable 禁用插件
func (m *PluginManager) Disable(ctx context.Context, name string) error {
	return m.Lifecycle.Stop(ctx, name)
}

// Status 获取插件状态
func (m *PluginManager) Status() map[string]PluginState {
	status := make(map[string]PluginState)

	for _, info := range m.Registry.List() {
		instance, err := m.Registry.GetInstance(info.Name)
		if err != nil {
			continue
		}
		status[info.Name] = instance.State
	}

	return status
}

// Stats 获取统计信息
func (m *PluginManager) Stats() map[string]any {
	plugins := m.Registry.List()
	status := m.Status()

	var running, stopped, errored int
	for _, state := range status {
		switch state {
		case PluginStateRunning:
			running++
		case PluginStateStopped:
			stopped++
		case PluginStateError:
			errored++
		}
	}

	typeCount := make(map[string]int)
	for _, info := range plugins {
		typeCount[string(info.Type)]++
	}

	return map[string]any{
		"total":     len(plugins),
		"running":   running,
		"stopped":   stopped,
		"errored":   errored,
		"by_type":   typeCount,
		"factories": len(m.Registry.ListFactories()),
	}
}

// ============== 便捷函数 ==============

// SortPluginsByPriority 按优先级排序插件配置
func SortPluginsByPriority(configs []PluginConfig) []PluginConfig {
	sorted := make([]PluginConfig, len(configs))
	copy(sorted, configs)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})

	return sorted
}
