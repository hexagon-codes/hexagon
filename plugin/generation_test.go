package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type generationPlugin struct {
	*BasePlugin
	stops         atomic.Int32
	initErr       error
	entered       chan struct{}
	proceed       chan struct{}
	stopEntered   chan struct{}
	stopProceed   chan struct{}
	stopDone      chan struct{}
	healthEntered chan struct{}
	healthProceed chan struct{}
}

func generationFixture(version string) *generationPlugin {
	return &generationPlugin{BasePlugin: NewBasePlugin(PluginInfo{Name: "routing", Version: version})}
}

func (p *generationPlugin) Init(ctx context.Context, config map[string]any) error {
	if p.entered != nil {
		close(p.entered)
		select {
		case <-p.proceed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if p.initErr != nil {
		return p.initErr
	}
	return p.BasePlugin.Init(ctx, config)
}

func (p *generationPlugin) Start(ctx context.Context) error {
	for _, name := range p.Info().Dependencies {
		if _, ok := DependencyFromContext(ctx, name); !ok {
			return fmt.Errorf("dependency %s is missing", name)
		}
	}
	return p.BasePlugin.Start(ctx)
}

func (p *generationPlugin) Stop(context.Context) error {
	if p.stops.Add(1) == 1 {
		if p.stopEntered != nil {
			close(p.stopEntered)
		}
		if p.stopProceed != nil {
			<-p.stopProceed
		}
		if p.stopDone != nil {
			close(p.stopDone)
		}
	}
	return nil
}

func (p *generationPlugin) Health() HealthStatus {
	if p.healthEntered != nil {
		close(p.healthEntered)
		<-p.healthProceed
	}
	return p.BasePlugin.Health()
}

type recoverablePlugin struct{ *generationPlugin }

func (p *recoverablePlugin) DefinitionReference() (string, json.RawMessage, error) {
	return "plugins/routing/" + p.Info().Version, nil, nil
}

type cancellableStopPlugin struct{ *generationPlugin }

func (p *cancellableStopPlugin) Stop(ctx context.Context) error {
	p.stops.Add(1)
	close(p.stopEntered)
	<-ctx.Done()
	return ctx.Err()
}

func runningGeneration(t *testing.T) (*PluginManager, *generationPlugin) {
	t.Helper()
	m := NewPluginManager()
	p := generationFixture("v1")
	if err := m.Load(p); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(context.Background(), "routing", map[string]any{"value": "old"}); err != nil {
		t.Fatal(err)
	}
	return m, p
}

func TestGenerationPreparedRunSurvivesPublicationAndAttempts(t *testing.T) {
	m, old := runningGeneration(t)
	prepared, err := m.Registry.Acquire("routing")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Release()
	next := generationFixture("v2")
	retired, err := m.ReplaceCandidate(context.Background(), "routing", next, map[string]any{"value": "new"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := m.Registry.Acquire("routing")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Release()
	for i := 0; i < 2; i++ {
		p, _ := prepared.Plugin("routing")
		if p != old || old.stops.Load() != 0 {
			t.Fatal("prepared run lost old resource between attempts")
		}
	}
	p, _ := current.Plugin("routing")
	if p != next {
		t.Fatal("new request did not use published route")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := retired.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("old resource did not remain pinned: %v", err)
	}
	prepared.Release()
	prepared.Release()
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := retired.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if old.stops.Load() != 1 {
		t.Fatalf("Stop calls: %d", old.stops.Load())
	}
}

func TestGenerationCandidateFailureKeepsOldRoute(t *testing.T) {
	m, old := runningGeneration(t)
	bad := generationFixture("v2")
	bad.initErr = errors.New("candidate init failed")
	if _, err := m.ReplaceCandidate(context.Background(), "routing", bad, nil); !errors.Is(err, bad.initErr) {
		t.Fatal(err)
	}
	lease, err := m.Registry.Acquire("routing")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	p, _ := lease.Plugin("routing")
	if p != old || old.stops.Load() != 0 || bad.stops.Load() != 1 {
		t.Fatal("candidate failure changed old resource ownership")
	}
}

func TestGenerationAcquireDuringCandidatePreparationIsAtomic(t *testing.T) {
	m, old := runningGeneration(t)
	candidate := generationFixture("v2")
	candidate.entered = make(chan struct{})
	candidate.proceed = make(chan struct{})
	type updateResult struct {
		retired *Retirement
		err     error
	}
	done := make(chan updateResult, 1)
	go func() {
		r, e := m.ReplaceCandidate(context.Background(), "routing", candidate, nil)
		done <- updateResult{r, e}
	}()
	<-candidate.entered
	lease, err := m.Registry.Acquire("routing")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	close(candidate.proceed)
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	if old.stops.Load() != 0 {
		t.Fatal("published update closed a concurrently acquired route")
	}
	p, _ := lease.Plugin("routing")
	if p != old {
		t.Fatal("acquire did not freeze its route")
	}
	lease.Release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := result.retired.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationForceStopAndRevisionRestore(t *testing.T) {
	m, old := runningGeneration(t)
	lease, err := m.Registry.Acquire("routing")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	refs := lease.References()
	restored, err := m.Registry.AcquireReferences(refs)
	if err != nil {
		t.Fatal(err)
	}
	restored.Release()
	retired, err := m.ReplaceCandidate(context.Background(), "routing", generationFixture("v2"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Registry.AcquireReferences(refs); err == nil {
		t.Fatal("restore silently selected latest revision")
	}
	if err := retired.ForceStop(context.Background()); !errors.Is(err, ErrForcedStop) {
		t.Fatal(err)
	}
	if !errors.Is(lease.Err(), ErrForcedStop) || old.stops.Load() != 1 {
		t.Fatal("force stop was not explicit on old lease")
	}
	lease.Release()
	lease.Release()
}

func TestGenerationHotReloadUsesIndependentCandidate(t *testing.T) {
	m, old := runningGeneration(t)
	if err := m.Registry.RegisterFactory("routing", func() Plugin { return generationFixture("v2") }); err != nil {
		t.Fatal(err)
	}
	lease, err := m.Registry.Acquire("routing")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	path := filepath.Join(t.TempDir(), "plugins.yaml")
	if err := os.WriteFile(path, []byte("plugins:\n  - name: routing\n    enabled: true\n    config:\n      value: new\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hot := NewHotReloadManager(m)
	if err := hot.UpdatePlugin(context.Background(), "routing", path, DefaultUpdateOptions()); err != nil {
		t.Fatal(err)
	}
	if old.stops.Load() != 0 {
		t.Fatal("hot reload stopped an in-flight resource")
	}
	lease.Release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := hot.WaitForDrain(ctx, "routing"); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationClosesOutsidePublicationLock(t *testing.T) {
	m, old := runningGeneration(t)
	old.stopEntered = make(chan struct{})
	old.stopProceed = make(chan struct{})
	defer close(old.stopProceed)
	_, err := m.ReplaceCandidate(context.Background(), "routing", generationFixture("v2"), nil)
	if err != nil {
		t.Fatal(err)
	}
	<-old.stopEntered
	lease, err := m.Registry.Acquire("routing")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

func TestGenerationPersistentReferenceRebuildsOriginalWithoutPublishing(t *testing.T) {
	m := NewPluginManager()
	old := &recoverablePlugin{generationFixture("v1")}
	if err := m.Load(old); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"value": "old"}
	if err := m.Enable(context.Background(), "routing", config); err != nil {
		t.Fatal(err)
	}
	config["value"] = "mutated"
	if old.Config()["value"] != "old" {
		t.Fatal("published plugin definition retained caller config")
	}
	lease, err := m.Registry.Acquire("routing")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := lease.RecoverableReferences()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := json.Marshal(refs)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	retired, err := m.ReplaceCandidate(context.Background(), "routing", generationFixture("v2"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := retired.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(saved, &refs); err != nil {
		t.Fatal(err)
	}
	var restoredInstance *generationPlugin
	restored, err := m.Registry.RestoreReferences(context.Background(), refs, func(_ context.Context, ref Reference) (Plugin, map[string]any, error) {
		if ref.ObjectKey != "plugins/routing/v1" {
			t.Fatalf("unstable reference: %#v", ref)
		}
		restoredInstance = generationFixture("v1")
		restoredInstance.stopDone = make(chan struct{})
		return restoredInstance, map[string]any{"value": "old"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := m.Registry.Acquire("routing")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Release()
	if p, _ := current.Plugin("routing"); p.Info().Version != "v2" {
		t.Fatal("restore republished an old route")
	}
	if p, _ := restored.Plugin("routing"); p.Info().Version != "v1" {
		t.Fatal("restore selected latest version")
	}
	restored.Release()
	restored.Release()
	select {
	case <-restoredInstance.stopDone:
	case <-time.After(time.Second):
		t.Fatal("restored resource was not released at handoff")
	}
	if restoredInstance.stops.Load() != 1 {
		t.Fatal("restored resource closed repeatedly")
	}
}

func TestGenerationOrdinaryPluginNeedsNoRecoveryDescription(t *testing.T) {
	m, old := runningGeneration(t)
	lease, err := m.Registry.Acquire("routing")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if _, err := lease.RecoverableReferences(); err == nil {
		t.Fatal("hash-only identity masqueraded as a recoverable definition")
	}
	if p, _ := lease.Plugin("routing"); p != old || lease.Err() != nil {
		t.Fatal("optional persistence blocked ordinary resource use")
	}
}

func namedGeneration(name, version string, dependencies ...string) *generationPlugin {
	return &generationPlugin{BasePlugin: NewBasePlugin(PluginInfo{Name: name, Version: version, Dependencies: dependencies})}
}

func TestGenerationCandidateSetPublishesTogetherAndPinsDependencies(t *testing.T) {
	m := NewPluginManager()
	oldProvider := namedGeneration("provider", "v1")
	oldTool := namedGeneration("tool", "v1", "provider")
	for _, p := range []*generationPlugin{oldProvider, oldTool} {
		if err := m.Load(p); err != nil {
			t.Fatal(err)
		}
		if err := m.Enable(context.Background(), p.Info().Name, nil); err != nil {
			t.Fatal(err)
		}
	}
	provider := namedGeneration("provider", "v2")
	tool := namedGeneration("tool", "v2", "provider")
	tool.entered = make(chan struct{})
	tool.proceed = make(chan struct{})
	type outcome struct {
		retirement *Retirement
		err        error
	}
	done := make(chan outcome, 1)
	go func() {
		r, e := m.ReplaceCandidates(context.Background(), []Candidate{{Plugin: tool}, {Plugin: provider}})
		done <- outcome{r, e}
	}()
	<-tool.entered
	oldRun, err := m.Registry.Acquire("tool")
	if err != nil {
		t.Fatal(err)
	}
	defer oldRun.Release()
	if p, ok := oldRun.Plugin("provider"); !ok || p != oldProvider {
		t.Fatal("tool dependency was not pinned atomically")
	}
	close(tool.proceed)
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	newRun, err := m.Registry.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer newRun.Release()
	for _, name := range []string{"provider", "tool"} {
		p, ok := newRun.Plugin(name)
		if !ok || p.Info().Version != "v2" {
			t.Fatal("published set contains mixed versions")
		}
	}
	if oldProvider.stops.Load() != 0 || oldTool.stops.Load() != 0 {
		t.Fatal("old candidate set did not wait for prepared run")
	}
	oldRun.Release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := result.retirement.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if oldProvider.stops.Load() != 1 || oldTool.stops.Load() != 1 {
		t.Fatal("old set did not close exactly once")
	}
}

func TestGenerationCandidateSetFailureCleansOnlyCandidates(t *testing.T) {
	m := NewPluginManager()
	oldA, oldB := namedGeneration("a", "v1"), namedGeneration("b", "v1", "a")
	for _, p := range []*generationPlugin{oldA, oldB} {
		if err := m.Load(p); err != nil {
			t.Fatal(err)
		}
		if err := m.Enable(context.Background(), p.Info().Name, nil); err != nil {
			t.Fatal(err)
		}
	}
	a, b := namedGeneration("a", "v2"), namedGeneration("b", "v2", "a")
	b.initErr = errors.New("second candidate failed")
	if _, err := m.ReplaceCandidates(context.Background(), []Candidate{{Plugin: a}, {Plugin: b}}); !errors.Is(err, b.initErr) {
		t.Fatal(err)
	}
	lease, err := m.Registry.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	for _, name := range []string{"a", "b"} {
		p, _ := lease.Plugin(name)
		if p.Info().Version != "v1" {
			t.Fatal("candidate failure partially published routes")
		}
	}
	if oldA.stops.Load() != 0 || oldB.stops.Load() != 0 || a.stops.Load() != 1 || b.stops.Load() != 1 {
		t.Fatal("failed candidate cleanup touched old resources or omitted candidates")
	}
}

func TestGenerationHealthConsumerPinsBeforePublication(t *testing.T) {
	m, old := runningGeneration(t)
	old.healthEntered = make(chan struct{})
	old.healthProceed = make(chan struct{})
	health := make(chan map[string]HealthStatus, 1)
	go func() { health <- m.Lifecycle.HealthCheck(context.Background()) }()
	<-old.healthEntered
	retired, err := m.ReplaceCandidate(context.Background(), "routing", generationFixture("v2"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if old.stops.Load() != 0 {
		t.Fatal("health consumer borrowed old route without a pin")
	}
	close(old.healthProceed)
	if result := <-health; result["routing"].Status != HealthStateHealthy {
		t.Fatal("health consumer lost its original resource")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := retired.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationForceStopCancelsAnAlreadyRunningStop(t *testing.T) {
	m := NewPluginManager()
	old := &cancellableStopPlugin{generationFixture("v1")}
	old.stopEntered = make(chan struct{})
	if err := m.Load(old); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(context.Background(), "routing", nil); err != nil {
		t.Fatal(err)
	}
	retired, err := m.ReplaceCandidate(context.Background(), "routing", generationFixture("v2"), nil)
	if err != nil {
		t.Fatal(err)
	}
	<-old.stopEntered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := retired.ForceStop(ctx); !errors.Is(err, ErrForcedStop) || !errors.Is(err, context.Canceled) {
		t.Fatalf("forced stop result: %v", err)
	}
	if old.stops.Load() != 1 {
		t.Fatal("force stop duplicated resource Stop")
	}
}
