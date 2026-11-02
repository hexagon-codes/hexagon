package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

type boundResourcePlugin struct {
	*generationPlugin
	registry *Registry
	legacy   bool
	bound    Plugin
}

func boundResource(name, version string, registry *Registry, legacy bool, dependencies ...string) *boundResourcePlugin {
	return &boundResourcePlugin{generationPlugin: namedGeneration(name, version, dependencies...), registry: registry, legacy: legacy}
}

func (p *boundResourcePlugin) Start(ctx context.Context) error {
	for _, name := range p.Info().Dependencies {
		if p.legacy {
			bound, err := p.registry.Get(name)
			if err != nil {
				return err
			}
			p.bound = bound
		} else {
			bound, ok := DependencyFromContext(ctx, name)
			if !ok {
				return fmt.Errorf("dependency %s is missing", name)
			}
			p.bound = bound
		}
	}
	return p.BasePlugin.Start(ctx)
}

func (p *boundResourcePlugin) DefinitionReference() (string, json.RawMessage, error) {
	return "plugins/" + p.Info().Name + "/" + p.Info().Version, nil, nil
}

func runningBoundResources(t *testing.T, legacy bool) (*PluginManager, *boundResourcePlugin, *boundResourcePlugin) {
	t.Helper()
	m := NewPluginManager()
	provider := boundResource("provider", "v1", m.Registry, false)
	tool := boundResource("tool", "v1", m.Registry, legacy, "provider")
	for _, p := range []Plugin{provider, tool} {
		if err := m.Load(p); err != nil {
			t.Fatal(err)
		}
		if err := m.Enable(t.Context(), p.Info().Name, nil); err != nil {
			t.Fatal(err)
		}
	}
	return m, provider, tool
}

func TestDependencyGenerationSurvivesSingleProviderReplacement(t *testing.T) {
	m, old, tool := runningBoundResources(t, true)
	next := boundResource("provider", "v2", m.Registry, false)
	retirement, err := m.ReplaceCandidate(t.Context(), "provider", next, nil)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.Registry.Acquire("tool")
	if err != nil {
		t.Fatal(err)
	}
	pinned, _ := lease.Plugin("provider")
	if pinned != old || tool.bound != old || old.stops.Load() != 0 {
		t.Fatal("active tool lost its original provider")
	}
	roots, err := m.Registry.Acquire("provider", "tool")
	if err != nil {
		t.Fatal(err)
	}
	providerRoot, _ := roots.Plugin("provider")
	refs := roots.References()
	if providerRoot != next || len(refs) != 2 || refs[0].Version != "v2" || len(refs[1].Dependencies) != 1 || refs[1].Dependencies[0].Version != "v1" {
		t.Fatalf("mixed generations were rewritten: %#v", refs)
	}
	lease.Release()
	roots.Release()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := retirement.Wait(cancelled); !errors.Is(err, context.Canceled) || old.stops.Load() != 0 {
		t.Fatalf("provider did not wait for its running dependent: %v", err)
	}
	if err := m.Disable(t.Context(), "tool"); err != nil {
		t.Fatal(err)
	}
	wait, finish := context.WithTimeout(t.Context(), time.Second)
	defer finish()
	if err := retirement.Wait(wait); err != nil || old.stops.Load() != 1 {
		t.Fatalf("old provider did not retire after its owner: %v", err)
	}
	if err := m.Disable(t.Context(), "provider"); err != nil {
		t.Fatal(err)
	}
}

func TestCandidateDependencyContextBindsPublishedGeneration(t *testing.T) {
	m, oldProvider, oldTool := runningBoundResources(t, false)
	provider := boundResource("provider", "v2", m.Registry, false)
	tool := boundResource("tool", "v2", m.Registry, false, "provider")
	retirement, err := m.ReplaceCandidates(t.Context(), []Candidate{{Plugin: tool}, {Plugin: provider}})
	if err != nil {
		t.Fatal(err)
	}
	if tool.bound != provider {
		t.Fatal("candidate tool bound the old registry route")
	}
	wait, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := retirement.Wait(wait); err != nil || oldProvider.stops.Load() != 1 || oldTool.stops.Load() != 1 {
		t.Fatalf("candidate dependency retirement did not progress: %v", err)
	}
	if err := m.Disable(t.Context(), "tool"); err != nil {
		t.Fatal(err)
	}
	if err := m.Disable(t.Context(), "provider"); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceDependencyTreeRestoresTwoProviderVersions(t *testing.T) {
	m, old, _ := runningBoundResources(t, false)
	next := boundResource("provider", "v2", m.Registry, false)
	retirement, err := m.ReplaceCandidate(t.Context(), "provider", next, nil)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.Registry.Acquire("provider", "tool")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := lease.RecoverableReferences()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(refs)
	if err != nil {
		t.Fatal(err)
	}
	var persisted []Reference
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	restored, err := m.Registry.RestoreReferences(t.Context(), persisted, func(_ context.Context, ref Reference) (Plugin, map[string]any, error) {
		dependencies := make([]string, len(ref.Dependencies))
		for i, child := range ref.Dependencies {
			dependencies[i] = child.Name
		}
		return boundResource(ref.Name, ref.Version, m.Registry, false, dependencies...), nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	providerRoot, _ := restored.Plugin("provider")
	toolRoot, _ := restored.Plugin("tool")
	if providerRoot.Info().Version != "v2" || toolRoot.(*boundResourcePlugin).bound.Info().Version != "v1" {
		t.Fatal("restore substituted the latest dependency")
	}
	restored.Release()
	lease.Release()
	if old.stops.Load() != 0 {
		t.Fatal("restoring independent resources stopped the original dependent provider")
	}
	if err := m.Disable(t.Context(), "tool"); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := retirement.Wait(wait); err != nil {
		t.Fatal(err)
	}
	if err := m.Disable(t.Context(), "provider"); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyDependencyRestoreDoesNotUseCurrentRoute(t *testing.T) {
	m, _, _ := runningBoundResources(t, true)
	retirement, err := m.ReplaceCandidate(t.Context(), "provider", boundResource("provider", "v2", m.Registry, false), nil)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.Registry.Acquire("tool")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := lease.RecoverableReferences()
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Registry.RestoreReferences(t.Context(), refs, func(_ context.Context, ref Reference) (Plugin, map[string]any, error) {
		dependencies := make([]string, len(ref.Dependencies))
		for i, child := range ref.Dependencies {
			dependencies[i] = child.Name
		}
		return boundResource(ref.Name, ref.Version, m.Registry, ref.Name == "tool", dependencies...), nil, nil
	})
	if !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("legacy restore quietly used current dependency: %v", err)
	}
	lease.Release()
	if err := m.Disable(t.Context(), "tool"); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := retirement.Wait(wait); err != nil {
		t.Fatal(err)
	}
	if err := m.Disable(t.Context(), "provider"); err != nil {
		t.Fatal(err)
	}
}
