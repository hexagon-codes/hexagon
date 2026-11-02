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

// Reference 保存插件版本身份与可选的重建描述，不包含进程内 generation 或资源句柄。
// 只有 ObjectKey/Definition 存在时才能跨进程恢复；名称与摘要本身不能重建资源。
type Reference struct {
	Name       string          `json:"name"`
	Version    string          `json:"version"`
	ConfigHash string          `json:"config_hash"`
	ObjectKey  string          `json:"object_key,omitempty"`
	Definition json.RawMessage `json:"definition,omitempty"`
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
	plugin       Plugin
	reference    Reference
	dependencies []string
	mu           sync.Mutex
	refs         int
	retired      bool
	forced       bool
	drained      chan struct{}
	done         chan struct{}
	stopOnce     sync.Once
	stopCancel   context.CancelFunc
	err          error
}

func newGeneration(p Plugin, ref Reference) *generation {
	return &generation{plugin: p, reference: ref, drained: make(chan struct{}), done: make(chan struct{})}
}

// Lease 固定一次 prepare 解析出的全部插件路由。Run 持有它，各 attempt 只借用。
// Release 幂等；调用后不得再使用借用的 Plugin。Lease 本身不能序列化恢复。
type Lease struct {
	plugins     map[string]Plugin
	refs        []Reference
	generations []*generation
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
	// 依赖与目标路由在同一快照中固定，避免只 pin 工具却提前回收其 Provider。
	selected := make(map[string]bool)
	resolved := make([]string, 0, len(names))
	var include func(string) error
	include = func(name string) error {
		if selected[name] {
			return nil
		}
		instance, ok := r.plugins[name]
		if !ok || instance.State != PluginStateRunning {
			return fmt.Errorf("plugin %s is not running", name)
		}
		selected[name] = true
		for _, dep := range instance.generation.dependencies {
			if err := include(dep); err != nil {
				return err
			}
		}
		resolved = append(resolved, name)
		return nil
	}
	for _, name := range names {
		if err := include(name); err != nil {
			return nil, err
		}
	}
	lease := &Lease{plugins: make(map[string]Plugin)}
	for _, name := range resolved {
		if _, exists := lease.plugins[name]; exists {
			continue
		}
		instance, ok := r.plugins[name]
		if !ok || instance.State != PluginStateRunning {
			lease.Release()
			return nil, fmt.Errorf("plugin %s is not running", name)
		}
		g := instance.generation
		g.mu.Lock()
		if g.retired {
			g.mu.Unlock()
			lease.Release()
			return nil, fmt.Errorf("plugin %s is retired", name)
		}
		g.refs++
		g.mu.Unlock()
		lease.generations = append(lease.generations, g)
		lease.plugins[name] = instance.Plugin
		lease.refs = append(lease.refs, g.reference)
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
	actual := make(map[string]Reference, len(lease.refs))
	for _, ref := range lease.refs {
		actual[ref.Name] = ref
	}
	for _, ref := range refs {
		got := actual[ref.Name]
		if got.Name != ref.Name || got.Version != ref.Version || got.ConfigHash != ref.ConfigHash {
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
	refs := append([]Reference(nil), l.refs...)
	for i := range refs {
		refs[i].Definition = append(json.RawMessage(nil), refs[i].Definition...)
	}
	return refs
}

// RecoverableReferences 为持久交接获取真正可重建的引用，不把名称与摘要冒充重建配置。
// 此方法在 prepare pin 之后、发布锁之外调用；失败不改变普通 Lease 的使用权。
func (l *Lease) RecoverableReferences() ([]Reference, error) {
	refs := l.References()
	for i := range refs {
		provider, ok := l.plugins[refs[i].Name].(DefinitionReferencer)
		if !ok {
			return nil, fmt.Errorf("plugin %s has no recoverable definition", refs[i].Name)
		}
		objectKey, definition, err := provider.DefinitionReference()
		if err != nil {
			return nil, fmt.Errorf("plugin %s definition reference: %w", refs[i].Name, err)
		}
		if objectKey == "" && len(definition) == 0 {
			return nil, fmt.Errorf("plugin %s definition reference is empty", refs[i].Name)
		}
		if len(definition) > 0 && !json.Valid(definition) {
			return nil, fmt.Errorf("plugin %s definition is not valid JSON", refs[i].Name)
		}
		refs[i].ObjectKey = objectKey
		refs[i].Definition = append(json.RawMessage(nil), definition...)
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
		return nil, fmt.Errorf("plugin: restore resolver is required")
	}
	cleanup := func(err error) (*Lease, error) {
		lease.Release()
		errs := []error{err}
		for i := len(lease.generations) - 1; i >= 0; i-- {
			g := lease.generations[i]
			retireGeneration(g)
			g.stop(context.WithoutCancel(ctx), false)
			<-g.done
			g.mu.Lock()
			errs = append(errs, g.err)
			g.mu.Unlock()
		}
		return nil, errors.Join(errs...)
	}
	for _, ref := range refs {
		if _, exists := lease.plugins[ref.Name]; exists {
			return cleanup(fmt.Errorf("plugin %s has duplicate restore references", ref.Name))
		}
		if ref.ObjectKey == "" && len(ref.Definition) == 0 {
			return cleanup(fmt.Errorf("plugin %s has no recoverable definition", ref.Name))
		}
		if len(ref.Definition) > 0 && !json.Valid(ref.Definition) {
			return cleanup(fmt.Errorf("plugin %s definition is not valid JSON", ref.Name))
		}
		ref.Definition = append(json.RawMessage(nil), ref.Definition...)
		candidate, config, err := resolve(ctx, ref)
		if err != nil {
			return cleanup(err)
		}
		if candidate == nil {
			return cleanup(fmt.Errorf("plugin %s resolver returned no instance", ref.Name))
		}
		r.mu.RLock()
		current := r.plugins[ref.Name]
		shared := current != nil && reflect.TypeOf(candidate).Comparable() && candidate == current.Plugin
		r.mu.RUnlock()
		if shared {
			return cleanup(fmt.Errorf("plugin %s restore requires an independent instance", ref.Name))
		}
		failCandidate := func(err error) (*Lease, error) {
			stopErr := invokePluginCallback(ref.Name, "restore.cleanup.stop", func() error { return candidate.Stop(context.WithoutCancel(ctx)) })
			_, failure := cleanup(errors.Join(err, stopErr))
			return nil, failure
		}
		if candidate.Info().Name != ref.Name || candidate.Info().Version != ref.Version || computeConfigHash(config) != ref.ConfigHash {
			return failCandidate(fmt.Errorf("plugin %s restored revision does not match", ref.Name))
		}
		frozen, err := reflectx.DeepCopyChecked(config)
		if err != nil {
			return failCandidate(err)
		}
		if err := invokePluginCallback(ref.Name, "restore.init", func() error { return candidate.Init(ctx, frozen) }); err != nil {
			return failCandidate(err)
		}
		if err := invokePluginCallback(ref.Name, "restore.start", func() error { return candidate.Start(ctx) }); err != nil {
			return failCandidate(err)
		}
		if err := ctx.Err(); err != nil {
			return failCandidate(err)
		}
		g := newGeneration(candidate, ref)
		g.refs = 1
		lease.plugins[ref.Name] = candidate
		lease.refs = append(lease.refs, ref)
		lease.generations = append(lease.generations, g)
	}
	retiring := make([]*generation, 0, len(lease.generations))
	for i := len(lease.generations) - 1; i >= 0; i-- {
		g := lease.generations[i]
		retireGeneration(g)
		retiring = append(retiring, g)
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
			g.mu.Lock()
			g.refs--
			if g.retired && g.refs == 0 {
				close(g.drained)
			}
			g.mu.Unlock()
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
			defer func() {
				value := recover()
				if !returned {
					err = pluginPanic(g.reference.Name, "stop", value)
				}
				stopCancel()
				g.mu.Lock()
				g.err = err
				if g.forced {
					g.err = errors.Join(ErrForcedStop, err)
				}
				g.mu.Unlock()
				close(g.done)
			}()
			err = g.plugin.Stop(stopCtx)
			returned = true
		}()
	})
}

func startRetirement(gs []*generation) *Retirement {
	r := &Retirement{generations: gs}
	if len(gs) > 0 {
		go func() {
			// 集合整体排空后按调用者给出的依赖逆序关闭。
			for _, g := range gs {
				select {
				case <-g.drained:
				case <-g.done:
				}
			}
			for _, g := range gs {
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
		candidate Candidate
		info      PluginInfo
		old       *PluginInstance
		config    map[string]any
		started   bool
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
		if reflect.TypeOf(item.Plugin).Comparable() && item.Plugin == old.Plugin {
			return nil, fmt.Errorf("plugin %s candidate must be a separate instance", info.Name)
		}
		frozen, err := reflectx.DeepCopyChecked(item.Config)
		if err != nil {
			return nil, fmt.Errorf("plugin config cannot be snapshotted: %w", err)
		}
		prepared[info.Name] = &preparedCandidate{candidate: item, info: info, old: old, config: frozen}
		infos = append(infos, info)
	}
	owned := make([]*preparedCandidate, 0, len(prepared))
	cleanup := func(cause error) (*Retirement, error) {
		errs := []error{cause}
		for i := len(owned) - 1; i >= 0; i-- {
			item := owned[i]
			errs = append(errs, invokePluginCallback(item.info.Name, "candidate.cleanup.stop", func() error { return item.candidate.Plugin.Stop(context.WithoutCancel(ctx)) }))
		}
		return nil, errors.Join(errs...)
	}
	ordered := m.Lifecycle.sortByDependency(infos)
	for _, info := range ordered {
		item := prepared[info.Name]
		owned = append(owned, item)
		initConfig, err := reflectx.DeepCopyChecked(item.config)
		if err != nil {
			return cleanup(err)
		}
		if err := invokePluginCallback(info.Name, "candidate.init", func() error { return item.candidate.Plugin.Init(ctx, initConfig) }); err != nil {
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
		if err := invokePluginCallback(info.Name, "candidate.start", func() error { return item.candidate.Plugin.Start(ctx) }); err != nil {
			return cleanup(fmt.Errorf("failed to start plugin candidate %s: %w", info.Name, err))
		}
		item.started = true
	}
	if err := ctx.Err(); err != nil {
		return cleanup(err)
	}
	newInstances := make(map[string]*PluginInstance, len(prepared))
	for name, item := range prepared {
		now := time.Now()
		ref := Reference{Name: name, Version: item.info.Version, ConfigHash: computeConfigHash(item.config)}
		newInstances[name] = &PluginInstance{Plugin: item.candidate.Plugin, State: PluginStateRunning, Config: item.config, LoadedAt: now, StartedAt: &now, generation: newGeneration(item.candidate.Plugin, ref)}
		newInstances[name].generation.dependencies = append([]string(nil), item.info.Dependencies...)
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
