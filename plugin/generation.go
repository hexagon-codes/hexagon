package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/hexagon-codes/toolkit/util/reflectx"
)

// ErrForcedStop 表示宿主在使用者尚未排空时显式停止资源。
var ErrForcedStop = errors.New("plugin: generation forcibly stopped")

// ErrDependencyUnavailable 表示原资源或实际依赖绑定无法安全恢复，不得改用当前路由。
var ErrDependencyUnavailable = errors.New("plugin: original dependency binding cannot be restored")

// Reference 保存插件版本身份与可选的重建描述，不包含进程内 generation 或资源句柄。
// 只有 ObjectKey/Definition 存在时才能跨进程恢复；名称与摘要本身不能重建资源。
type Reference struct {
	Name       string          `json:"name"`
	Version    string          `json:"version"`
	ConfigHash string          `json:"config_hash"`
	ObjectKey  string          `json:"object_key,omitempty"`
	Definition json.RawMessage `json:"definition,omitempty"`
	// Dependencies 固定该实例实际使用的依赖版本，与显式选择的同名新路由分开。
	Dependencies []Reference `json:"dependencies,omitempty"`
}

// DefinitionReferencer 由需要持久恢复的插件或宿主适配器提供固定版本的重建描述。
// 描述只包含声明配置或稳定定位，凭据使用宿主 secret locator，不包含 Go 资源句柄。
// 普通轻量插件无需实现；只有实际请求持久引用时才校验该能力。
type DefinitionReferencer interface {
	DefinitionReference() (objectKey string, definition json.RawMessage, err error)
}

// RestoreResolver 按给定原修订返回尚未 Init/Start 的独立实例与配置。
// 不得忽略固定定位改读最新文件；不存在原修订时必须返回错误。
type RestoreResolver func(context.Context, Reference) (Plugin, map[string]any, error)

type generation struct {
	plugin         Plugin
	reference      Reference
	dependencies   []string
	bindings       []*generation
	legacyBindings map[string]bool
	started        bool
	mu             sync.Mutex
	refs           int
	retired        bool
	forced         bool
	drained        chan struct{}
	done           chan struct{}
	stopOnce       sync.Once
	stopCancel     context.CancelFunc
	stopPhase      string
	err            error
}

func newGeneration(p Plugin, ref Reference) *generation {
	return &generation{plugin: p, reference: ref, drained: make(chan struct{}), done: make(chan struct{})}
}

// Lease 固定一次 prepare 解析出的全部插件路由。Run 持有它，各 attempt 只借用。
// Release 幂等；调用后不得再使用借用的 Plugin。Lease 本身不能序列化恢复。
type Lease struct {
	plugins     map[string]Plugin
	generations []*generation
	roots       []*generation
	once        sync.Once
}

// Acquire 在同一个注册锁临界区内解析路由并取得使用权，消除读取旧路由后才 pin 的窗口。
// names 为空时固定全部正在运行的插件；指定名称必须全部可用，否则不留下部分 pin。
func (r *Registry) Acquire(names ...string) (*Lease, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(names) == 0 {
		for name, instance := range r.plugins {
			if instance.State == PluginStateRunning {
				names = append(names, name)
			}
		}
		sort.Strings(names)
	}
	lease := &Lease{plugins: make(map[string]Plugin)}
	roots := make(map[string]*generation)
	for _, name := range names {
		instance := r.plugins[name]
		if instance == nil || instance.State != PluginStateRunning {
			return nil, fmt.Errorf("plugin %s is not running", name)
		}
		if _, exists := roots[name]; !exists {
			roots[name] = instance.generation
			lease.roots = append(lease.roots, instance.generation)
		}
	}
	seen := make(map[*generation]bool)
	ambiguous := make(map[string]bool)
	byName := make(map[string]*generation)
	var include func(*generation, bool) error
	include = func(g *generation, root bool) error {
		if seen[g] {
			return nil
		}
		g.mu.Lock()
		if g.forced || root && g.retired || !root && !g.started {
			g.mu.Unlock()
			return fmt.Errorf("plugin %s generation is unavailable", g.reference.Name)
		}
		select {
		case <-g.done:
			g.mu.Unlock()
			return fmt.Errorf("plugin %s generation is stopped", g.reference.Name)
		default:
		}
		g.refs++
		bindings := append([]*generation(nil), g.bindings...)
		g.mu.Unlock()
		seen[g] = true
		lease.generations = append(lease.generations, g)
		name := g.reference.Name
		if selected, explicit := roots[name]; explicit {
			lease.plugins[name] = selected.plugin
		} else if previous, exists := byName[name]; !exists && !ambiguous[name] {
			byName[name] = g
			lease.plugins[name] = g.plugin
		} else if previous != g {
			delete(lease.plugins, name)
			ambiguous[name] = true
		}
		for _, dependency := range bindings {
			if err := include(dependency, false); err != nil {
				return err
			}
		}
		return nil
	}
	for _, g := range lease.roots {
		if err := include(g, true); err != nil {
			lease.Release()
			return nil, err
		}
	}
	return lease, nil
}

// AcquireReferences 仅在当前路由的可重建身份完全一致时恢复使用权。
// 旧版本已不可获取时明确失败；宿主可先从其版本源重新装配原版本。
func (r *Registry) AcquireReferences(refs []Reference) (*Lease, error) {
	if len(refs) == 0 {
		return &Lease{plugins: make(map[string]Plugin)}, nil
	}
	names := make([]string, len(refs))
	for i, ref := range refs {
		names[i] = ref.Name
	}
	lease, err := r.Acquire(names...)
	if err != nil {
		return nil, err
	}
	actual := make(map[string]Reference, len(refs))
	for _, ref := range lease.References() {
		actual[ref.Name] = ref
	}
	for _, ref := range refs {
		got := actual[ref.Name]
		if !sameReferenceIdentity(got, ref) {
			lease.Release()
			return nil, fmt.Errorf("plugin %s revision is unavailable", ref.Name)
		}
	}
	return lease, nil
}

// Plugin 借用已固定路由，不重新查询注册表或取得额外使用权。
func (l *Lease) Plugin(name string) (Plugin, bool) { p, ok := l.plugins[name]; return p, ok }

// References 返回版本身份的独立副本；持久交接使用 RecoverableReferences 获取重建描述。
func (l *Lease) References() []Reference {
	refs := make([]Reference, 0, len(l.roots))
	for _, g := range l.roots {
		ref, _ := generationReference(g, false)
		refs = append(refs, ref)
	}
	return refs
}

// RecoverableReferences 返回根选择及其实际依赖树，各代保持独立固定引用。
func (l *Lease) RecoverableReferences() ([]Reference, error) {
	refs := make([]Reference, 0, len(l.roots))
	for _, g := range l.roots {
		ref, err := generationReference(g, true)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// RestoreReferences 重建原版本的独立资源使用视图，不改变当前注册路由。
// 返回 Lease 归本次恢复 Run 所有，Release 后排空并 Stop；没有永久占用的等待连接。
func (r *Registry) RestoreReferences(ctx context.Context, refs []Reference, resolve RestoreResolver) (*Lease, error) {
	lease := &Lease{plugins: make(map[string]Plugin)}
	if len(refs) == 0 {
		return lease, nil
	}
	if resolve == nil {
		return nil, fmt.Errorf("%w: restore resolver is required", ErrDependencyUnavailable)
	}
	cache := make(map[string]*generation)
	visiting := make(map[string]bool)
	created := make([]*generation, 0)
	cleanup := func(cause error) (*Lease, error) {
		lease.Release()
		retiring := make([]*generation, 0, len(created))
		for i := len(created) - 1; i >= 0; i-- {
			created[i].stopPhase = "restore.cleanup.stop"
			retireGeneration(created[i])
			retiring = append(retiring, created[i])
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		stopErr := startRetirement(retiring).Wait(cleanupCtx)
		return nil, fmt.Errorf("%w: %w", ErrDependencyUnavailable, errors.Join(cause, stopErr))
	}
	var build func(Reference) (*generation, error)
	build = func(ref Reference) (*generation, error) {
		ref = cloneReference(ref)
		if ref.Name == "" || ref.Version == "" || ref.ObjectKey == "" && len(ref.Definition) == 0 || len(ref.Definition) > 0 && !json.Valid(ref.Definition) {
			return nil, fmt.Errorf("plugin %s has no valid recoverable reference", ref.Name)
		}
		identityData, err := json.Marshal(ref)
		if err != nil {
			return nil, err
		}
		identity := string(identityData)
		if g := cache[identity]; g != nil {
			return g, nil
		}
		if visiting[identity] {
			return nil, fmt.Errorf("plugin %s has cyclic restore dependencies", ref.Name)
		}
		visiting[identity] = true
		defer delete(visiting, identity)
		dependencies := make([]*generation, 0, len(ref.Dependencies))
		names := make(map[string]bool)
		for _, child := range ref.Dependencies {
			if names[child.Name] {
				return nil, fmt.Errorf("plugin %s has duplicate dependency %s", ref.Name, child.Name)
			}
			names[child.Name] = true
			dependency, err := build(child)
			if err != nil {
				return nil, err
			}
			dependencies = append(dependencies, dependency)
		}
		dependencyCtx, view := contextWithDependencies(ctx, dependencies)
		var candidate Plugin
		var config map[string]any
		err = invokePluginCallback(ref.Name, "restore.resolve", func() error {
			var err error
			candidate, config, err = resolve(dependencyCtx, ref)
			return err
		})
		if err != nil {
			return nil, err
		}
		if candidate == nil {
			return nil, fmt.Errorf("plugin %s resolver returned no instance", ref.Name)
		}
		r.mu.RLock()
		current := r.plugins[ref.Name]
		shared := current != nil && reflect.ValueOf(candidate).Comparable() && candidate == current.Plugin
		r.mu.RUnlock()
		if shared {
			return nil, fmt.Errorf("plugin %s restore requires an independent instance", ref.Name)
		}
		info := candidate.Info()
		g := newGeneration(candidate, ref)
		created = append(created, g)
		if info.Name != ref.Name || info.Version != ref.Version || computeConfigHash(config) != ref.ConfigHash {
			return nil, fmt.Errorf("plugin %s restored revision does not match", ref.Name)
		}
		if len(info.Dependencies) != len(dependencies) {
			return nil, fmt.Errorf("plugin %s dependency references do not match its declaration", ref.Name)
		}
		for _, name := range info.Dependencies {
			if !names[name] {
				return nil, fmt.Errorf("plugin %s original dependency %s is missing", ref.Name, name)
			}
		}
		for _, dependency := range dependencies {
			dependency.mu.Lock()
			dependency.refs++
			dependency.mu.Unlock()
			g.bindings = append(g.bindings, dependency)
		}
		frozen, err := reflectx.DeepCopyChecked(config)
		if err != nil {
			return nil, err
		}
		if err := invokePluginCallback(ref.Name, "restore.init", func() error { return candidate.Init(dependencyCtx, frozen) }); err != nil {
			return nil, err
		}
		if err := invokePluginCallback(ref.Name, "restore.start", func() error { return candidate.Start(dependencyCtx) }); err != nil {
			return nil, err
		}
		used := view.usedNames()
		for _, dependency := range dependencies {
			if !used[dependency.reference.Name] {
				return nil, fmt.Errorf("plugin %s did not resolve original dependency %s from lifecycle context", ref.Name, dependency.reference.Name)
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g.started = true
		cache[identity] = g
		return g, nil
	}
	roots := make(map[string]*generation)
	for _, ref := range refs {
		if _, exists := roots[ref.Name]; exists {
			return cleanup(fmt.Errorf("plugin %s has duplicate root references", ref.Name))
		}
		g, err := build(ref)
		if err != nil {
			return cleanup(err)
		}
		roots[ref.Name] = g
		lease.roots = append(lease.roots, g)
	}
	seen := make(map[*generation]bool)
	implicit := make(map[string]*generation)
	ambiguous := make(map[string]bool)
	var pin func(*generation)
	pin = func(g *generation) {
		if seen[g] {
			return
		}
		seen[g] = true
		g.refs++
		lease.generations = append(lease.generations, g)
		name := g.reference.Name
		if root, ok := roots[name]; ok {
			lease.plugins[name] = root.plugin
		} else if previous, ok := implicit[name]; !ok && !ambiguous[name] {
			implicit[name] = g
			lease.plugins[name] = g.plugin
		} else if previous != g {
			delete(lease.plugins, name)
			ambiguous[name] = true
		}
		for _, dependency := range g.bindings {
			pin(dependency)
		}
	}
	for _, g := range lease.roots {
		pin(g)
	}
	retiring := make([]*generation, 0, len(created))
	for i := len(created) - 1; i >= 0; i-- {
		retireGeneration(created[i])
		retiring = append(retiring, created[i])
	}
	startRetirement(retiring)
	return lease, nil
}

// Err 返回显式强停状态；普通切换与正常排空不会使旧 Lease 失效。
func (l *Lease) Err() error {
	for _, g := range l.generations {
		g.mu.Lock()
		forced := g.forced
		g.mu.Unlock()
		if forced {
			return ErrForcedStop
		}
	}
	return nil
}

// Release 释放 Run 的引用，不关闭共享资源；所有者负责 retired 资源的最终 Stop。
func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		for _, g := range l.generations {
			releaseGeneration(g)
		}
	})
}

// Retirement 表示已发布替代路由后的旧资源排空过程。
// Wait 的取消只停止等待，不回滚已经发布的新路由，也不会强停在途请求。
type Retirement struct{ generations []*generation }

func retireGeneration(g *generation) {
	g.mu.Lock()
	if !g.retired {
		g.retired = true
		if g.refs == 0 {
			close(g.drained)
		}
	}
	g.mu.Unlock()
}

func (g *generation) stop(ctx context.Context, force bool) {
	var cancel context.CancelFunc
	if force {
		g.mu.Lock()
		g.forced = true
		cancel = g.stopCancel
		g.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	g.stopOnce.Do(func() {
		stopCtx, stopCancel := context.WithCancel(ctx)
		g.mu.Lock()
		g.stopCancel = stopCancel
		cancelledWhileStarting := g.forced && !force
		g.mu.Unlock()
		if cancelledWhileStarting {
			stopCancel()
		}
		go func() {
			var err error
			returned := false
			phase := g.stopPhase
			if phase == "" {
				phase = "stop"
			}
			defer func() {
				value := recover()
				if !returned {
					err = pluginPanic(g.reference.Name, phase, value)
				}
				stopCancel()
				g.mu.Lock()
				g.err = err
				if g.forced {
					g.err = errors.Join(ErrForcedStop, err)
				}
				g.mu.Unlock()
				releaseDependencyBindings(g)
				close(g.done)
			}()
			g.mu.Lock()
			bindings := append([]*generation(nil), g.bindings...)
			g.mu.Unlock()
			dependencyCtx, _ := contextWithDependencies(stopCtx, bindings)
			err = g.plugin.Stop(dependencyCtx)
			returned = true
		}()
	})
}

func startRetirement(gs []*generation) *Retirement {
	r := &Retirement{generations: gs}
	if len(gs) > 0 {
		go func() {
			// 按依赖逆序逐个排空；依赖引用在拥有者 Stop 完成后才释放。
			for _, g := range gs {
				select {
				case <-g.drained:
				case <-g.done:
				}
				g.stop(context.Background(), false)
				<-g.done
			}
		}()
	}
	return r
}

// Wait 等待旧资源实际完成 Stop，并返回其真实错误。
func (r *Retirement) Wait(ctx context.Context) error {
	if r == nil {
		return nil
	}
	var errs []error
	for _, g := range r.generations {
		select {
		case <-g.done:
			g.mu.Lock()
			errs = append(errs, g.err)
			g.mu.Unlock()
		case <-ctx.Done():
			return errors.Join(append(errs, ctx.Err())...)
		}
	}
	return errors.Join(errs...)
}

// ForceStop 显式中断未排空资源，并取消已经开始的 Stop 上下文。
// 结果包含 ErrForcedStop；插件未完成停止时仍受 ctx 等待限制，不会伪称正常排空。
func (r *Retirement) ForceStop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	for _, g := range r.generations {
		g.stop(ctx, true)
	}
	return errors.Join(ErrForcedStop, r.Wait(ctx))
}

// Candidate 是尚未发布的独立插件及其配置，成功发布后资源归 PluginManager 所有。
type Candidate struct {
	Plugin Plugin
	Config map[string]any
}

// ReplaceCandidate 独立初始化并启动单个候选，委托同一候选集发布路径。
func (m *PluginManager) ReplaceCandidate(ctx context.Context, name string, candidate Plugin, config map[string]any) (*Retirement, error) {
	if candidate == nil || candidate.Info().Name != name {
		return nil, fmt.Errorf("plugin candidate name does not match %s", name)
	}
	return m.ReplaceCandidates(ctx, []Candidate{{Plugin: candidate, Config: config}})
}

// ReplaceCandidates 完整装配候选集后，在同一注册锁临界区发布全部路由并 retire 全部旧代。
// 输入必须是新实例；初始化和启动失败只清理已经接手的候选，不停止旧资源。
// 候选按依赖顺序启动，清理与排空等待都在发布锁外完成。
func (m *PluginManager) ReplaceCandidates(ctx context.Context, candidates []Candidate) (*Retirement, error) {
	type preparedCandidate struct {
		candidate   Candidate
		info        PluginInfo
		old         *PluginInstance
		config      map[string]any
		started     bool
		generation  *generation
		held        []*generation
		callbackCtx context.Context
		view        *dependencyView
	}
	r := m.Registry
	prepared := make(map[string]*preparedCandidate, len(candidates))
	infos := make([]PluginInfo, 0, len(candidates))
	for _, item := range candidates {
		if item.Plugin == nil {
			return nil, fmt.Errorf("plugin: nil candidate")
		}
		info := item.Plugin.Info()
		if _, exists := prepared[info.Name]; exists {
			return nil, fmt.Errorf("plugin %s has duplicate candidates", info.Name)
		}
		r.mu.RLock()
		old, ok := r.plugins[info.Name]
		r.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("plugin %s not found", info.Name)
		}
		if reflect.ValueOf(item.Plugin).Comparable() && item.Plugin == old.Plugin {
			return nil, fmt.Errorf("plugin %s candidate must be a separate instance", info.Name)
		}
		frozen, err := reflectx.DeepCopyChecked(item.Config)
		if err != nil {
			return nil, fmt.Errorf("plugin config cannot be snapshotted: %w", err)
		}
		ref := Reference{Name: info.Name, Version: info.Version, ConfigHash: computeConfigHash(frozen)}
		g := newGeneration(item.Plugin, ref)
		g.dependencies = append([]string(nil), info.Dependencies...)
		prepared[info.Name] = &preparedCandidate{candidate: item, info: info, old: old, config: frozen, generation: g}
		infos = append(infos, info)
	}
	owned := make([]*preparedCandidate, 0, len(prepared))
	cleanup := func(cause error) (*Retirement, error) {
		errs := []error{cause}
		for i := len(owned) - 1; i >= 0; i-- {
			item := owned[i]
			stopCtx := context.WithoutCancel(ctx)
			if item.callbackCtx != nil {
				stopCtx = context.WithoutCancel(item.callbackCtx)
			}
			errs = append(errs, invokePluginCallback(item.info.Name, "candidate.cleanup.stop", func() error { return item.candidate.Plugin.Stop(stopCtx) }))
			if item.held != nil {
				for _, g := range item.held {
					releaseGeneration(g)
				}
				item.held = nil
			} else {
				releaseDependencyBindings(item.generation)
			}
		}
		return nil, errors.Join(errs...)
	}
	ordered := m.Lifecycle.sortByDependency(infos)
	for _, info := range ordered {
		item := prepared[info.Name]
		owned = append(owned, item)
		visible, err := r.captureDependencies(info.Dependencies)
		if err != nil {
			return cleanup(err)
		}
		item.held = append(item.held, visible...)
		preferred := make([]*generation, 0, len(visible))
		for _, g := range visible {
			selected := g
			if replacement, ok := prepared[g.reference.Name]; ok {
				if !replacement.started {
					return cleanup(fmt.Errorf("candidate dependency %s is not running for plugin %s", g.reference.Name, info.Name))
				}
				selected = replacement.generation
				selected.mu.Lock()
				selected.refs++
				selected.mu.Unlock()
				item.held = append(item.held, selected)
			}
			preferred = append(preferred, selected)
		}
		item.callbackCtx, item.view = contextWithDependencies(ctx, preferred)
		initConfig, err := reflectx.DeepCopyChecked(item.config)
		if err != nil {
			return cleanup(err)
		}
		if err := invokePluginCallback(info.Name, "candidate.init", func() error { return item.candidate.Plugin.Init(item.callbackCtx, initConfig) }); err != nil {
			return cleanup(fmt.Errorf("failed to init plugin candidate %s: %w", info.Name, err))
		}
		for _, dep := range info.Dependencies {
			if dependency, ok := prepared[dep]; ok {
				if !dependency.started {
					return cleanup(fmt.Errorf("candidate dependency %s is not running for plugin %s", dep, info.Name))
				}
			} else {
				instance, err := r.GetInstance(dep)
				if err != nil || instance.State != PluginStateRunning {
					return cleanup(fmt.Errorf("dependency %s is not running for plugin %s", dep, info.Name))
				}
			}
		}
		if err := invokePluginCallback(info.Name, "candidate.start", func() error { return item.candidate.Plugin.Start(item.callbackCtx) }); err != nil {
			return cleanup(fmt.Errorf("failed to start plugin candidate %s: %w", info.Name, err))
		}
		used := item.view.usedNames()
		if !r.legacyBindingsCurrent(visible, used) {
			return cleanup(fmt.Errorf("plugin %s dependencies changed while preparing candidate", info.Name))
		}
		chosen := make(map[*generation]bool)
		for _, g := range visible {
			selected := g
			if used[g.reference.Name] {
				selected = item.view.plugins[g.reference.Name]
			}
			item.generation.bindings = append(item.generation.bindings, selected)
			chosen[selected] = true
		}
		for _, g := range item.held {
			if !chosen[g] {
				releaseGeneration(g)
			}
		}
		item.held = nil
		item.generation.started = true
		item.started = true
	}
	if err := ctx.Err(); err != nil {
		return cleanup(err)
	}
	newInstances := make(map[string]*PluginInstance, len(prepared))
	for name, item := range prepared {
		now := time.Now()
		newInstances[name] = &PluginInstance{Plugin: item.candidate.Plugin, State: PluginStateRunning, Config: item.config, LoadedAt: now, StartedAt: &now, generation: item.generation}
	}
	r.mu.Lock()
	for name, item := range prepared {
		item.old.generation.mu.Lock()
		retired := item.old.generation.retired
		item.old.generation.mu.Unlock()
		if r.plugins[name] != item.old || retired {
			r.mu.Unlock()
			return cleanup(fmt.Errorf("plugin %s route changed while preparing candidates", name))
		}
	}
	oldGenerations := make([]*generation, 0, len(ordered))
	for i := len(ordered) - 1; i >= 0; i-- {
		name := ordered[i].Name
		item := prepared[name]
		r.plugins[name] = newInstances[name]
		retireGeneration(item.old.generation)
		oldGenerations = append(oldGenerations, item.old.generation)
	}
	r.mu.Unlock()
	retirement := startRetirement(oldGenerations)
	for _, info := range ordered {
		r.emitEvent(PluginEvent{Type: PluginEventStarted, Plugin: info.Name})
	}
	return retirement, nil
}
