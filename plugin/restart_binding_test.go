package plugin

import (
	"context"
	"errors"
	"testing"
	"time"
)

type initBoundPlugin struct {
	*BasePlugin
	bound          Plugin
	inits          int
	stops          int
	reinitPanic    error
	cleanupFailure error
}

func (p *initBoundPlugin) Init(ctx context.Context, config map[string]any) error {
	p.inits++
	if p.inits > 1 && p.reinitPanic != nil {
		panic(p.reinitPanic)
	}
	for _, name := range p.Info().Dependencies {
		var ok bool
		p.bound, ok = DependencyFromContext(ctx, name)
		if !ok {
			return errors.New("original dependency is unavailable")
		}
	}
	return p.BasePlugin.Init(ctx, config)
}

func (p *initBoundPlugin) Stop(ctx context.Context) error {
	p.stops++
	return errors.Join(p.BasePlugin.Stop(ctx), p.cleanupFailure)
}

func TestLifecycleRestartRebindsChangedInitDependency(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "same generation"
		if changed {
			name = "new generation"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			m := NewPluginManager()
			old := NewBasePlugin(PluginInfo{Name: "provider", Version: "v1"})
			tool := &initBoundPlugin{BasePlugin: NewBasePlugin(PluginInfo{Name: "tool", Version: "v1", Dependencies: []string{"provider"}})}
			for _, p := range []Plugin{old, tool} {
				if err := m.Load(p); err != nil {
					t.Fatal(err)
				}
				if err := m.Enable(ctx, p.Info().Name, map[string]any{"mode": "original"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Disable(ctx, "tool"); err != nil {
				t.Fatal(err)
			}
			var expected Plugin = old
			if changed {
				expected = NewBasePlugin(PluginInfo{Name: "provider", Version: "v2"})
				retired, err := m.ReplaceCandidate(ctx, "provider", expected, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := retired.Wait(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Lifecycle.Start(ctx, "tool"); err != nil {
				t.Fatal(err)
			}
			lease, err := m.Registry.Acquire("tool")
			if err != nil {
				t.Fatal(err)
			}
			pinned, _ := lease.Plugin("provider")
			wantInits := 1
			if changed {
				wantInits = 2
			}
			if tool.bound != expected || pinned != expected || tool.inits != wantInits || tool.Config()["mode"] != "original" {
				t.Fatalf("restart changed lifecycle or bound the wrong resource: inits=%d refs=%+v", tool.inits, lease.References())
			}
			lease.Release()
			if err := m.Disable(ctx, "tool"); err != nil {
				t.Fatal(err)
			}
			if err := m.Disable(ctx, "provider"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLifecycleRestartRebindPanicReleasesDependencies(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	m := NewPluginManager()
	provider := NewBasePlugin(PluginInfo{Name: "provider", Version: "v1"})
	failure := errors.New("reinitialization failed")
	tool := &initBoundPlugin{BasePlugin: NewBasePlugin(PluginInfo{Name: "tool", Version: "v1", Dependencies: []string{"provider"}}), reinitPanic: failure}
	for _, p := range []Plugin{provider, tool} {
		if err := m.Load(p); err != nil {
			t.Fatal(err)
		}
		if err := m.Enable(ctx, p.Info().Name, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Disable(ctx, "tool"); err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("reinitialization cleanup failed")
	tool.cleanupFailure = cleanupFailure
	retired, err := m.ReplaceCandidate(ctx, "provider", NewBasePlugin(PluginInfo{Name: "provider", Version: "v2"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := retired.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Lifecycle.Start(ctx, "tool"); !errors.Is(err, failure) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("reinit panic lost: %v", err)
	}
	instance, err := m.Registry.GetInstance("tool")
	if err != nil || instance.State != PluginStateError || !errors.Is(instance.Error, failure) || !errors.Is(instance.Error, cleanupFailure) || tool.stops != 2 {
		t.Fatalf("cleanup facts lost: state=%+v stops=%d err=%v", instance, tool.stops, err)
	}
	if err := m.Disable(ctx, "provider"); err != nil {
		t.Fatalf("failed reinitialization retained dependency pins: %v", err)
	}
}
