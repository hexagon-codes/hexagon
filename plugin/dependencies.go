package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

type dependencyContextKey struct{}

type dependencyView struct {
	mu      sync.Mutex
	plugins map[string]*generation
	used    map[string]bool
}

// DependencyFromContext 返回生命周期回调所固定的声明依赖。
// 候选集和历史恢复必须使用此入口，不能通过 Registry.Get 偷换为当前路由。
func DependencyFromContext(ctx context.Context, name string) (Plugin, bool) {
	view, ok := ctx.Value(dependencyContextKey{}).(*dependencyView)
	if !ok {
		return nil, false
	}
	g, ok := view.plugins[name]
	if !ok {
		return nil, false
	}
	view.mu.Lock()
	view.used[name] = true
	view.mu.Unlock()
	return g.plugin, true
}

func (v *dependencyView) usedNames() map[string]bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	used := make(map[string]bool, len(v.used))
	for name, value := range v.used {
		used[name] = value
	}
	return used
}

func contextWithDependencies(ctx context.Context, bindings []*generation) (context.Context, *dependencyView) {
	view := &dependencyView{plugins: make(map[string]*generation), used: make(map[string]bool)}
	for _, g := range bindings {
		view.plugins[g.reference.Name] = g
	}
	return context.WithValue(ctx, dependencyContextKey{}, view), view
}

func releaseGeneration(g *generation) {
	g.mu.Lock()
	g.refs--
	if g.retired && g.refs == 0 {
		close(g.drained)
	}
	g.mu.Unlock()
}

func releaseDependencyBindings(g *generation) {
	g.mu.Lock()
	bindings := g.bindings
	g.bindings, g.legacyBindings = nil, nil
	g.mu.Unlock()
	for _, dependency := range bindings {
		releaseGeneration(dependency)
	}
}

// 仅在注册表锁内取得当前可见依赖；初始化阶段允许依赖尚未 Start。
func (r *Registry) captureDependencies(names []string) ([]*generation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bindings := make([]*generation, 0, len(names))
	seen := make(map[string]bool)
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		instance, ok := r.plugins[name]
		if !ok {
			for _, g := range bindings {
				releaseGeneration(g)
			}
			return nil, fmt.Errorf("dependency %s is not available", name)
		}
		g := instance.generation
		g.mu.Lock()
		if g.retired || g.forced {
			g.mu.Unlock()
			for _, pinned := range bindings {
				releaseGeneration(pinned)
			}
			return nil, fmt.Errorf("dependency %s is retired", name)
		}
		g.refs++
		g.mu.Unlock()
		bindings = append(bindings, g)
	}
	return bindings, nil
}

func (r *Registry) legacyBindingsCurrent(bindings []*generation, used map[string]bool) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, g := range bindings {
		if used[g.reference.Name] {
			continue
		}
		instance := r.plugins[g.reference.Name]
		if instance == nil || instance.generation != g {
			return false
		}
	}
	return true
}

func cloneReference(ref Reference) Reference {
	ref.Definition = append(json.RawMessage(nil), ref.Definition...)
	ref.Dependencies = append([]Reference(nil), ref.Dependencies...)
	for i := range ref.Dependencies {
		ref.Dependencies[i] = cloneReference(ref.Dependencies[i])
	}
	return ref
}

func generationReference(g *generation, recoverable bool) (Reference, error) {
	g.mu.Lock()
	ref := cloneReference(g.reference)
	bindings := append([]*generation(nil), g.bindings...)
	g.mu.Unlock()
	ref.Dependencies = nil
	if recoverable {
		provider, ok := g.plugin.(DefinitionReferencer)
		if !ok {
			return Reference{}, fmt.Errorf("plugin %s has no recoverable definition", ref.Name)
		}
		var objectKey string
		var definition json.RawMessage
		err := invokePluginCallback(ref.Name, "definition.reference", func() error {
			var err error
			objectKey, definition, err = provider.DefinitionReference()
			return err
		})
		if err != nil {
			return Reference{}, err
		}
		if objectKey == "" && len(definition) == 0 || len(definition) > 0 && !json.Valid(definition) {
			return Reference{}, fmt.Errorf("plugin %s has no valid recoverable definition", ref.Name)
		}
		ref.ObjectKey, ref.Definition = objectKey, append(json.RawMessage(nil), definition...)
	}
	for _, dependency := range bindings {
		child, err := generationReference(dependency, recoverable)
		if err != nil {
			return Reference{}, err
		}
		ref.Dependencies = append(ref.Dependencies, child)
	}
	sort.Slice(ref.Dependencies, func(i, j int) bool { return ref.Dependencies[i].Name < ref.Dependencies[j].Name })
	return ref, nil
}

func sameReferenceIdentity(a, b Reference) bool {
	a.ObjectKey, a.Definition, b.ObjectKey, b.Definition = "", nil, "", nil
	for i := range a.Dependencies {
		if i >= len(b.Dependencies) || !sameReferenceIdentity(a.Dependencies[i], b.Dependencies[i]) {
			return false
		}
	}
	return a.Name == b.Name && a.Version == b.Version && a.ConfigHash == b.ConfigHash && len(a.Dependencies) == len(b.Dependencies)
}
