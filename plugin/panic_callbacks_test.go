package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type callbackPanicPlugin struct {
	*BasePlugin
	panics map[string]any
	before func(string) error
	stops  atomic.Int32
}

func newCallbackPanicPlugin(name, version string, dependencies ...string) *callbackPanicPlugin {
	return &callbackPanicPlugin{BasePlugin: NewBasePlugin(PluginInfo{Name: name, Version: version, Dependencies: dependencies}), panics: make(map[string]any)}
}

func (p *callbackPanicPlugin) invoke(phase string) error {
	if p.before != nil {
		if err := p.before(phase); err != nil {
			return err
		}
	}
	if value, ok := p.panics[phase]; ok {
		panic(value)
	}
	return nil
}

func (p *callbackPanicPlugin) Init(ctx context.Context, config map[string]any) error {
	if err := p.invoke("init"); err != nil {
		return err
	}
	return p.BasePlugin.Init(ctx, config)
}

func (p *callbackPanicPlugin) Start(ctx context.Context) error {
	for _, name := range p.Info().Dependencies {
		if _, ok := DependencyFromContext(ctx, name); !ok {
			return fmt.Errorf("dependency %s is missing", name)
		}
	}
	if err := p.invoke("start"); err != nil {
		return err
	}
	return p.BasePlugin.Start(ctx)
}

func (p *callbackPanicPlugin) Stop(ctx context.Context) error {
	p.stops.Add(1)
	if err := p.invoke("stop"); err != nil {
		return err
	}
	return p.BasePlugin.Stop(ctx)
}

func assertCallbackPanic(t *testing.T, err error, name, phase string, cause error) {
	t.Helper()
	var failure *CallbackPanicError
	if !errors.As(err, &failure) || failure.Plugin != name || failure.Phase != phase {
		t.Fatalf("callback failure identity: %v, %+v", err, failure)
	}
	if !failure.UnsafeToReplay() || !bytes.Contains(failure.Stack, []byte("panic_callbacks_test.go")) {
		t.Fatalf("callback panic lost stack or replay classification: %+v", failure)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("callback panic lost original cause: %v", err)
	}
}

func loadCallbackPlugins(t *testing.T, manager *PluginManager, plugins ...*callbackPanicPlugin) {
	t.Helper()
	for _, p := range plugins {
		if err := manager.Load(p); err != nil {
			t.Fatal(err)
		}
		if err := manager.Enable(context.Background(), p.Info().Name, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func closeCallbackManager(t *testing.T, manager *PluginManager) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Lifecycle.StopAll(ctx); err != nil {
			t.Errorf("close current plugins: %v", err)
		}
	})
}

func TestPluginCallbackPanicLifecycleArchivesState(t *testing.T) {
	for _, phase := range []string{"init", "start"} {
		t.Run(phase, func(t *testing.T) {
			manager := NewPluginManager()
			p := newCallbackPanicPlugin("lifecycle", "v1")
			cause := errors.New("lifecycle callback panic")
			p.panics[phase] = cause
			p.before = func(string) error {
				if !manager.Registry.mu.TryLock() {
					return fmt.Errorf("fixture: registry lock held during callback")
				}
				manager.Registry.mu.Unlock()
				value, ok := manager.Lifecycle.operations.Load("lifecycle")
				if !ok {
					return fmt.Errorf("fixture: lifecycle operation is not reserved")
				}
				operation := value.(*lifecycleOperation)
				if !operation.mu.TryLock() {
					return fmt.Errorf("fixture: lifecycle mutex held during callback")
				}
				operation.mu.Unlock()
				return nil
			}
			if err := manager.Load(p); err != nil {
				t.Fatal(err)
			}
			err := manager.Enable(context.Background(), "lifecycle", nil)
			assertCallbackPanic(t, err, "lifecycle", phase, cause)
			instance, err := manager.Registry.GetInstance("lifecycle")
			if err != nil {
				t.Fatal(err)
			}
			if instance.State != PluginStateError {
				t.Fatalf("panic state: %s", instance.State)
			}
			assertCallbackPanic(t, instance.Error, "lifecycle", phase, cause)
		})
	}
}

func TestPluginCallbackPanicCandidatePreservesOldRoute(t *testing.T) {
	for _, phase := range []string{"init", "start"} {
		t.Run(phase, func(t *testing.T) {
			manager := NewPluginManager()
			old := newCallbackPanicPlugin("route", "v1")
			loadCallbackPlugins(t, manager, old)
			closeCallbackManager(t, manager)
			lease, err := manager.Registry.Acquire("route")
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			candidate := newCallbackPanicPlugin("route", "v2")
			cause := errors.New("candidate callback panic")
			candidate.panics[phase] = cause
			candidate.before = func(string) error {
				if !manager.Registry.mu.TryLock() {
					return fmt.Errorf("fixture: publication lock held during callback")
				}
				manager.Registry.mu.Unlock()
				return nil
			}
			_, err = manager.ReplaceCandidate(context.Background(), "route", candidate, nil)
			assertCallbackPanic(t, err, "route", "candidate."+phase, cause)
			current, err := manager.Registry.Acquire("route")
			if err != nil {
				t.Fatal(err)
			}
			defer current.Release()
			if p, _ := current.Plugin("route"); p != old || old.stops.Load() != 0 || candidate.stops.Load() != 1 {
				t.Fatal("candidate panic changed old route or skipped candidate cleanup")
			}
		})
	}
}

func TestPluginCallbackPanicCandidateCleanupContinues(t *testing.T) {
	manager := NewPluginManager()
	oldA, oldB := newCallbackPanicPlugin("a", "v1"), newCallbackPanicPlugin("b", "v1", "a")
	loadCallbackPlugins(t, manager, oldA, oldB)
	closeCallbackManager(t, manager)
	a, b := newCallbackPanicPlugin("a", "v2"), newCallbackPanicPlugin("b", "v2", "a")
	initFailure, stopA, stopB := errors.New("candidate init panic"), errors.New("candidate a stop panic"), errors.New("candidate b stop panic")
	b.panics["init"] = initFailure
	a.panics["stop"], b.panics["stop"] = stopA, stopB
	_, err := manager.ReplaceCandidates(context.Background(), []Candidate{{Plugin: a}, {Plugin: b}})
	assertCallbackPanic(t, err, "b", "candidate.init", initFailure)
	if !errors.Is(err, stopA) || !errors.Is(err, stopB) || !strings.Contains(err.Error(), "candidate.cleanup.stop") {
		t.Fatalf("candidate cleanup errors were not joined: %v", err)
	}
	if a.stops.Load() != 1 || b.stops.Load() != 1 || oldA.stops.Load() != 0 || oldB.stops.Load() != 0 {
		t.Fatal("one cleanup panic skipped remaining candidates or touched old resources")
	}
}

func TestPluginCallbackPanicStopCompletesRetirement(t *testing.T) {
	manager := NewPluginManager()
	oldA, oldB := newCallbackPanicPlugin("a", "v1"), newCallbackPanicPlugin("b", "v1", "a")
	loadCallbackPlugins(t, manager, oldA, oldB)
	oldFailure := errors.New("retired plugin stop panic")
	oldB.panics["stop"] = oldFailure
	closeCallbackManager(t, manager)
	a, b := newCallbackPanicPlugin("a", "v2"), newCallbackPanicPlugin("b", "v2", "a")
	retirement, err := manager.ReplaceCandidates(context.Background(), []Candidate{{Plugin: a}, {Plugin: b}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = retirement.Wait(ctx)
	assertCallbackPanic(t, err, "b", "stop", oldFailure)
	if oldA.stops.Load() != 1 || oldB.stops.Load() != 1 || a.stops.Load() != 0 || b.stops.Load() != 0 {
		t.Fatal("Stop panic blocked dependency cleanup or stopped new resources")
	}
	if err := retirement.Wait(ctx); !errors.Is(err, oldFailure) || oldB.stops.Load() != 1 {
		t.Fatalf("retirement did not retain its failure result: %v", err)
	}
}

func TestPluginCallbackPanicStopArchivesState(t *testing.T) {
	manager := NewPluginManager()
	p := newCallbackPanicPlugin("stopping", "v1")
	loadCallbackPlugins(t, manager, p)
	cause := errors.New("lifecycle stop panic")
	p.panics["stop"] = cause
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := manager.Disable(ctx, "stopping")
	assertCallbackPanic(t, err, "stopping", "stop", cause)
	instance, err := manager.Registry.GetInstance("stopping")
	if err != nil {
		t.Fatal(err)
	}
	if instance.State != PluginStateError || p.stops.Load() != 1 {
		t.Fatal("stop panic was not archived as failure")
	}
	assertCallbackPanic(t, instance.Error, "stopping", "stop", cause)
}

func TestPluginCallbackPanicRestoreCleansAllResources(t *testing.T) {
	for _, phase := range []string{"init", "start"} {
		t.Run(phase, func(t *testing.T) {
			manager := NewPluginManager()
			oldA, oldB := newCallbackPanicPlugin("a", "v2"), newCallbackPanicPlugin("b", "v2", "a")
			loadCallbackPlugins(t, manager, oldA, oldB)
			closeCallbackManager(t, manager)
			a, b := newCallbackPanicPlugin("a", "v1"), newCallbackPanicPlugin("b", "v1", "a")
			failure, stopA, stopB := errors.New("restore callback panic"), errors.New("restore a stop panic"), errors.New("restore b stop panic")
			b.panics[phase] = failure
			a.panics["stop"], b.panics["stop"] = stopA, stopB
			aReference := Reference{Name: "a", Version: "v1", ConfigHash: computeConfigHash(nil), ObjectKey: "fixture/a/v1"}
			references := []Reference{aReference, {Name: "b", Version: "v1", ConfigHash: computeConfigHash(nil), ObjectKey: "fixture/b/v1", Dependencies: []Reference{aReference}}}
			_, err := manager.Registry.RestoreReferences(context.Background(), references, func(_ context.Context, ref Reference) (Plugin, map[string]any, error) {
				if ref.Name == "a" {
					return a, nil, nil
				}
				return b, nil, nil
			})
			assertCallbackPanic(t, err, "b", "restore."+phase, failure)
			if !errors.Is(err, stopA) || !errors.Is(err, stopB) || !strings.Contains(err.Error(), "restore.cleanup.stop") {
				t.Fatalf("restore cleanup failures were not retained: %v", err)
			}
			if a.stops.Load() != 1 || b.stops.Load() != 1 || oldA.stops.Load() != 0 || oldB.stops.Load() != 0 {
				t.Fatal("restore panic skipped cleanup or stopped current resources")
			}
		})
	}
}
