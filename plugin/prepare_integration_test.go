package plugin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	corememory "github.com/hexagon-codes/ai-core/memory"
	"github.com/hexagon-codes/hexagon/agent/skill"
	runmemory "github.com/hexagon-codes/hexagon/memory"
	memorystore "github.com/hexagon-codes/hexagon/memory/store"
	"github.com/hexagon-codes/hexagon/plugin"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
	"github.com/hexagon-codes/toolkit/util/hash"
)

// 同一个插件同时提供模型与工具，便于观察实际调用是否借用了同一份 prepare pin。
type preparedBundle struct {
	*plugin.BasePlugin
	version      string
	modelCalls   atomic.Int32
	toolCalls    atomic.Int32
	releases     atomic.Int32
	stops        atomic.Int32
	firstCall    chan struct{}
	continueCall chan struct{}
	stopped      chan struct{}
}

func newPreparedBundle(version string) *preparedBundle {
	return &preparedBundle{BasePlugin: plugin.NewBasePlugin(plugin.PluginInfo{Name: "bundle", Version: version}), version: version, stopped: make(chan struct{})}
}

func (p *preparedBundle) Name() string { return "fixture-" + p.version }

func (p *preparedBundle) Complete(ctx context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	if p.stops.Load() != 0 {
		return nil, fmt.Errorf("fixture: model resource is stopped")
	}
	call := p.modelCalls.Add(1)
	if call == 1 && p.firstCall != nil {
		close(p.firstCall)
		select {
		case <-p.continueCall:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.Config()["mode"] == "tool" && call == 1 {
		return &llm.CompletionResponse{ToolCalls: []llm.ToolCall{{ID: "version-call", Type: "function", Name: "version", Arguments: "{}"}}}, nil
	}
	return &llm.CompletionResponse{Content: "version:" + p.version}, nil
}

func (p *preparedBundle) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, fmt.Errorf("fixture: streaming is not used")
}

func (p *preparedBundle) Execute(context.Context, llm.ToolCall) (agentruntime.ToolResult, error) {
	if p.stops.Load() != 0 {
		return agentruntime.ToolResult{}, fmt.Errorf("fixture: tool resource is stopped")
	}
	p.toolCalls.Add(1)
	return agentruntime.ToolResult{Content: "tool:" + p.version, Status: agentruntime.ToolStatusSuccess}, nil
}

func (*preparedBundle) SideEffectOf(llm.ToolCall) agentruntime.ToolSideEffect {
	return agentruntime.SideEffectReadOnly
}

func (p *preparedBundle) Stop(context.Context) error {
	if p.stops.Add(1) == 1 {
		close(p.stopped)
	}
	return nil
}

type preparedDefinition struct {
	Version string         `json:"version"`
	Config  map[string]any `json:"config"`
}

func (p *preparedBundle) DefinitionReference() (string, json.RawMessage, error) {
	data, err := json.Marshal(preparedDefinition{Version: p.version, Config: p.Config()})
	return "fixtures/bundle/" + p.version, data, err
}

func prepareReferences(lease *plugin.Lease) ([]agentruntime.ContentReference, error) {
	identities := lease.References()
	references, err := lease.RecoverableReferences()
	if err != nil {
		return nil, err
	}
	if len(identities) != len(references) {
		return nil, fmt.Errorf("fixture: reference set differs from pinned identities")
	}
	result := make([]agentruntime.ContentReference, len(references))
	for i, reference := range references {
		if reference.Name != identities[i].Name || reference.Version != identities[i].Version || reference.ConfigHash != identities[i].ConfigHash {
			return nil, fmt.Errorf("fixture: recoverable reference differs from pinned identity")
		}
		data, err := json.Marshal(reference)
		if err != nil {
			return nil, err
		}
		digest, err := agentruntime.ContentDigest(data)
		if err != nil {
			return nil, err
		}
		result[i] = agentruntime.ContentReference{Kind: "plugin", ID: reference.Name, Revision: reference.Version + ":" + reference.ConfigHash, Digest: digest, Content: data}
	}
	return result, nil
}

func preparedFromLease(lease *plugin.Lease) (agentruntime.PreparedRun, error) {
	value, ok := lease.Plugin("bundle")
	if !ok {
		lease.Release()
		return agentruntime.PreparedRun{}, fmt.Errorf("fixture: pinned bundle is absent")
	}
	bundle, ok := value.(*preparedBundle)
	if !ok {
		lease.Release()
		return agentruntime.PreparedRun{}, fmt.Errorf("fixture: unexpected bundle type")
	}
	references, err := prepareReferences(lease)
	if err != nil {
		lease.Release()
		return agentruntime.PreparedRun{}, err
	}
	return agentruntime.PreparedRun{
		ProviderSelector: agentruntime.StaticProviderSelector{Provider: bundle, Name: bundle.Name(), Model: "fixture-model"},
		ToolExecutor:     bundle,
		References:       references,
		Release:          func() { bundle.releases.Add(1); lease.Release() },
	}, nil
}

func loadedPreparedBundle(t *testing.T, mode string) (*plugin.PluginManager, *preparedBundle) {
	t.Helper()
	manager := plugin.NewPluginManager()
	bundle := newPreparedBundle("v1")
	if err := manager.Load(bundle); err != nil {
		t.Fatal(err)
	}
	if err := manager.Enable(context.Background(), "bundle", map[string]any{"setting": "v1", "mode": mode}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Disable(context.Background(), "bundle"); err != nil {
			t.Errorf("close current bundle: %v", err)
		}
	})
	return manager, bundle
}

func TestPrepareIntegrationPinsProviderAndToolUntilRunRelease(t *testing.T) {
	manager, old := loadedPreparedBundle(t, "tool")
	old.firstCall, old.continueCall = make(chan struct{}), make(chan struct{})
	var allow sync.Once
	unblock := func() { allow.Do(func() { close(old.continueCall) }) }
	defer unblock()
	runner := agentruntime.NewRunner(agentruntime.Config{PrepareRun: func(_ context.Context, _ agentruntime.Request) (agentruntime.PreparedRun, error) {
		lease, err := manager.Registry.Acquire("bundle")
		if err != nil {
			return agentruntime.PreparedRun{}, err
		}
		return preparedFromLease(lease)
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type outcome struct {
		result *agentruntime.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runner.Run(ctx, agentruntime.Request{ID: "old-run", Messages: []llm.Message{{Role: llm.RoleUser, Content: "Inspect the version"}}})
		done <- outcome{result, err}
	}()
	select {
	case <-old.firstCall:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	current := newPreparedBundle("v2")
	retirement, err := manager.ReplaceCandidate(ctx, "bundle", current, map[string]any{"setting": "v2", "mode": "tool"})
	if err != nil {
		t.Fatal(err)
	}
	if old.stops.Load() != 0 {
		t.Fatal("publication stopped the prepared run resource")
	}
	newResult, err := runner.Run(ctx, agentruntime.Request{ID: "new-run", Messages: []llm.Message{{Role: llm.RoleUser, Content: "Inspect the version"}}})
	if err != nil {
		t.Fatal(err)
	}
	if newResult.Content != "version:v2" || len(newResult.ToolCalls) != 1 || newResult.ToolCalls[0].Result.Content != "tool:v2" {
		t.Fatalf("new run did not use one new resource set: %+v", newResult)
	}
	if old.stops.Load() != 0 || old.toolCalls.Load() != 0 {
		t.Fatal("new run changed or released the blocked old run")
	}
	unblock()
	var oldResult outcome
	select {
	case oldResult = <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if oldResult.err != nil {
		t.Fatal(oldResult.err)
	}
	if oldResult.result.Content != "version:v1" || len(oldResult.result.ToolCalls) != 1 || oldResult.result.ToolCalls[0].Result.Content != "tool:v1" {
		t.Fatalf("old run mixed resource generations: %+v", oldResult.result)
	}
	if err := retirement.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if old.modelCalls.Load() != 2 || old.toolCalls.Load() != 1 || old.releases.Load() != 1 || old.stops.Load() != 1 || current.releases.Load() != 1 {
		t.Fatal("run did not borrow between attempts and release exactly once")
	}
}

// 该端口只在测试内保存合成状态，复用真实 ApplyCommit；不作为 SQL 事务或耐久证据。
type preparedRunStore struct {
	agentruntime.RunStore
	mu       sync.Mutex
	record   agentruntime.RunRecord
	receipts map[string]agentruntime.CommitReceipt
}

func (s *preparedRunStore) GetRun(_ context.Context, scope, runID string) (agentruntime.RunRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record.Scope != scope || s.record.RunID != runID {
		return agentruntime.RunRecord{}, false, nil
	}
	data, err := json.Marshal(s.record)
	if err != nil {
		return agentruntime.RunRecord{}, false, err
	}
	var record agentruntime.RunRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return agentruntime.RunRecord{}, false, err
	}
	return record, true, nil
}

func (s *preparedRunStore) Commit(_ context.Context, request agentruntime.CommitRequest) (agentruntime.CommitReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if receipt, ok := s.receipts[request.CommitID]; ok {
		return receipt, nil
	}
	next, receipt, err := agentruntime.ApplyCommit(s.record, request, time.Now())
	if err != nil {
		return agentruntime.CommitReceipt{}, err
	}
	s.record = next
	s.receipts[request.CommitID] = receipt
	return receipt, nil
}

func (s *preparedRunStore) LookupCommit(_ context.Context, _ string, id string) (agentruntime.CommitReceipt, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.receipts[id]
	return receipt, ok, nil
}

func TestPrepareIntegrationRestoresFrozenVersionAndRejectsMissingOriginal(t *testing.T) {
	manager, old := loadedPreparedBundle(t, "final")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lease, err := manager.Registry.Acquire("bundle")
	if err != nil {
		t.Fatal(err)
	}
	references, err := prepareReferences(lease)
	lease.Release()
	if err != nil {
		t.Fatal(err)
	}
	request := agentruntime.Request{ID: "persisted-run", Messages: []llm.Message{{Role: llm.RoleUser, Content: "Continue the original version"}}, StreamMode: agentruntime.StreamModeOff}
	spec, err := agentruntime.FreezeRunSpec(request, references)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var restoredSpec agentruntime.RunSpec
	if err := json.Unmarshal(data, &restoredSpec); err != nil {
		t.Fatal(err)
	}
	current := newPreparedBundle("v2")
	retired, err := manager.ReplaceCandidate(ctx, "bundle", current, map[string]any{"setting": "v2", "mode": "final"})
	if err != nil {
		t.Fatal(err)
	}
	if err := retired.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if old.stops.Load() != 1 {
		t.Fatal("persisted waiting state retained a process resource pin")
	}
	missing := errors.New("fixture: original plugin revision is unavailable")
	allowOriginal := true
	var restored *preparedBundle
	var frozenReads, latestReads atomic.Int32
	preparer := func(prepareCtx context.Context, _ agentruntime.Request) (agentruntime.PreparedRun, error) {
		frozen, ok := agentruntime.FrozenRunSpecFromContext(prepareCtx)
		if !ok {
			latestReads.Add(1)
			return agentruntime.PreparedRun{}, fmt.Errorf("fixture: frozen run spec is missing")
		}
		frozenReads.Add(1)
		var pinned []plugin.Reference
		for _, resource := range frozen.Resources {
			if resource.Kind != "plugin" {
				continue
			}
			var reference plugin.Reference
			if err := json.Unmarshal(resource.Content, &reference); err != nil {
				return agentruntime.PreparedRun{}, err
			}
			pinned = append(pinned, reference)
		}
		if len(pinned) != 1 || pinned[0].Version != "v1" {
			return agentruntime.PreparedRun{}, fmt.Errorf("fixture: original frozen plugin reference is missing")
		}
		restoredLease, err := manager.Registry.RestoreReferences(prepareCtx, pinned, func(_ context.Context, reference plugin.Reference) (plugin.Plugin, map[string]any, error) {
			if !allowOriginal {
				return nil, nil, errors.Join(agentruntime.ErrUnrecoverableSpec, missing)
			}
			var definition preparedDefinition
			if err := json.Unmarshal(reference.Definition, &definition); err != nil {
				return nil, nil, err
			}
			if definition.Version != "v1" || reference.ObjectKey != "fixtures/bundle/v1" {
				return nil, nil, agentruntime.ErrUnrecoverableSpec
			}
			restored = newPreparedBundle(definition.Version)
			return restored, definition.Config, nil
		})
		if err != nil {
			return agentruntime.PreparedRun{}, err
		}
		return preparedFromLease(restoredLease)
	}
	authority := agentruntime.ExecutionAuthority{Owner: "fixture-worker", AttemptID: "fixture-attempt", Fence: 1}
	newStore := func() *preparedRunStore {
		return &preparedRunStore{record: agentruntime.RunRecord{Scope: "fixture-scope", RunID: request.ID, Spec: restoredSpec, Revision: 1, Status: agentruntime.RunRunning, Authority: authority, LeaseUntil: time.Now().Add(time.Minute)}, receipts: make(map[string]agentruntime.CommitReceipt)}
	}
	executionCtx := agentruntime.ContextWithExecutionAuthority(ctx, "fixture-scope", authority)
	durable := &agentruntime.DurableRunner{Runner: agentruntime.NewRunner(agentruntime.Config{PrepareRun: preparer}), Store: newStore()}
	result, err := durable.Execute(executionCtx, durable.Store.(*preparedRunStore).record, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "version:v1" || restored == nil || restored.modelCalls.Load() != 1 || restored.releases.Load() != 1 {
		t.Fatalf("recovery selected latest plugin: result=%+v", result)
	}
	select {
	case <-restored.stopped:
	case <-ctx.Done():
		t.Fatal("restored process resource was not released")
	}
	allowOriginal = false
	durable.Store = newStore()
	if _, err := durable.Execute(executionCtx, durable.Store.(*preparedRunStore).record, nil); !errors.Is(err, agentruntime.ErrUnrecoverableSpec) || !errors.Is(err, missing) {
		t.Fatalf("missing original revision did not fail explicitly: %v", err)
	}
	if frozenReads.Load() != 2 || latestReads.Load() != 0 || current.modelCalls.Load() != 0 || current.toolCalls.Load() != 0 {
		t.Fatal("recovery fell back to current registry routes")
	}
}

// 只统计对真实 Store 的当前视图读取，所有操作原样委托，不替换存储行为。
type trackedSnapshotStore struct {
	memorystore.ConditionalStore
	liveReads atomic.Int32
}

func (s *trackedSnapshotStore) List(ctx context.Context, namespace []string, opts ...memorystore.ListOption) ([]*memorystore.Item, error) {
	s.liveReads.Add(1)
	return s.ConditionalStore.List(ctx, namespace, opts...)
}

func (s *trackedSnapshotStore) Get(ctx context.Context, namespace []string, key string) (*memorystore.Item, error) {
	s.liveReads.Add(1)
	return s.ConditionalStore.Get(ctx, namespace, key)
}

func (s *trackedSnapshotStore) Search(ctx context.Context, namespace []string, query *memorystore.SearchQuery) ([]*memorystore.SearchResult, error) {
	s.liveReads.Add(1)
	return s.ConditionalStore.Search(ctx, namespace, query)
}

type skillMemoryInputProvider struct {
	calls    int
	messages []llm.Message
}

func (*skillMemoryInputProvider) Name() string { return "fixture-context" }
func (p *skillMemoryInputProvider) Complete(_ context.Context, request llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.calls++
	p.messages = agentruntime.CloneOutputMessages(request.Messages)
	return &llm.CompletionResponse{Content: "restored original context"}, nil
}
func (*skillMemoryInputProvider) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, fmt.Errorf("fixture: streaming is not used")
}

func TestPrepareIntegrationRestoresSkillAndMemory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const scopeID = "fixture-context-scope"
	const oldDocument = "Original skill instructions"
	const newDocument = "Updated skill instructions"
	const oldMemory = "Original shared memory"
	const newMemory = "Updated shared memory"
	root := t.TempDir()
	skills := skill.NewRegistry()
	if err := skills.RegisterSource("fixture-skills", skill.DirectorySource{Directory: root}); err != nil {
		t.Fatal(err)
	}
	document := func(version, body string) *skill.Skill {
		t.Helper()
		directory := filepath.Join(root, version)
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		reference, err := skill.NewDocumentReference("fixture-skills", version, "SKILL.md", map[string]string{"SKILL.md": hash.SHA256(body)})
		if err != nil {
			t.Fatal(err)
		}
		return &skill.Skill{Name: "guide", Scope: scopeID, Version: version, Kind: skill.KindDocument, Description: "A context fixture", Document: &reference}
	}
	if err := skills.Register(document("v1", oldDocument)); err != nil {
		t.Fatal(err)
	}
	selected, err := skills.Select(ctx, scopeID, "guide")
	if err != nil {
		t.Fatal(err)
	}
	storage := memorystore.NewInMemoryStore()
	t.Cleanup(func() { _ = storage.Close() })
	tracked := &trackedSnapshotStore{ConditionalStore: storage}
	memoryScope := runmemory.Scope{Namespace: []string{"team", scopeID}}
	shared, err := runmemory.NewScopedMemory(tracked, memoryScope)
	if err != nil {
		t.Fatal(err)
	}
	record, err := shared.Save(ctx, runmemory.Record{Scope: memoryScope, Source: "fixture-session", Entry: corememory.Entry{ID: "memory-input", Role: "user", Content: oldMemory}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := shared.Capture(ctx, corememory.SearchQuery{})
	if err != nil {
		t.Fatal(err)
	}
	inline := func(kind, id, revision string, value any) agentruntime.ContentReference {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := agentruntime.ContentDigest(data)
		if err != nil {
			t.Fatal(err)
		}
		return agentruntime.ContentReference{Kind: kind, ID: id, Revision: revision, Digest: digest, Content: data}
	}
	selection := selected.Reference()
	references := []agentruntime.ContentReference{inline("skill", "guide", selection.Metadata.Revision, selection), inline("memory", "shared", snapshot.Revision(), snapshot)}
	query := llm.Message{Role: llm.RoleUser, Content: "Continue with the frozen context"}
	request := agentruntime.Request{ID: "frozen-context-run", StreamMode: agentruntime.StreamModeOff, Messages: []llm.Message{{Role: llm.RoleSystem, Content: selected.Content()}, {Role: llm.RoleUser, Content: snapshot.Entries()[0].Content}, query}}
	spec, err := agentruntime.FreezeRunSpec(request, references)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var savedSpec agentruntime.RunSpec
	if err := json.Unmarshal(data, &savedSpec); err != nil {
		t.Fatal(err)
	}
	if err := skills.Replace(document("v2", newDocument)); err != nil {
		t.Fatal(err)
	}
	record.Entry.Content = newMemory
	if _, err := shared.Save(ctx, record, record.Revision); err != nil {
		t.Fatal(err)
	}
	currentSkill, err := skills.Select(ctx, scopeID, "guide")
	if err != nil {
		t.Fatal(err)
	}
	currentMemory, err := shared.Capture(ctx, corememory.SearchQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if currentSkill.Content() != newDocument || currentMemory.Entries()[0].Content != newMemory {
		t.Fatal("current skill and memory fixtures were not updated")
	}
	readsBeforeRestore := tracked.liveReads.Load()
	provider := &skillMemoryInputProvider{}
	var frozenReads int
	preparer := func(prepareCtx context.Context, preparedRequest agentruntime.Request) (agentruntime.PreparedRun, error) {
		frozen, ok := agentruntime.FrozenRunSpecFromContext(prepareCtx)
		if !ok {
			return agentruntime.PreparedRun{}, agentruntime.ErrUnrecoverableSpec
		}
		frozenReads++
		var originalSkill *skill.Selected
		var originalMemory *runmemory.Snapshot
		for _, reference := range frozen.Resources {
			switch reference.Kind {
			case "skill":
				selection, err := skill.DecodeSelection(reference.Content)
				if err != nil {
					return agentruntime.PreparedRun{}, err
				}
				originalSkill, err = skills.Restore(prepareCtx, scopeID, selection)
				if err != nil {
					return agentruntime.PreparedRun{}, errors.Join(agentruntime.ErrUnrecoverableSpec, err)
				}
			case "memory":
				var err error
				originalMemory, err = runmemory.DecodeSnapshot(reference.Content, memoryScope)
				if err != nil {
					return agentruntime.PreparedRun{}, err
				}
			}
		}
		if originalSkill == nil || originalMemory == nil || len(originalMemory.Entries()) != 1 {
			return agentruntime.PreparedRun{}, agentruntime.ErrUnrecoverableSpec
		}
		// 每次从固定引用重建输入，避免把已存在的旧消息当作引用恢复的替代证明。
		messages := []llm.Message{{Role: llm.RoleSystem, Content: originalSkill.Content()}}
		for _, entry := range originalMemory.Entries() {
			messages = append(messages, llm.Message{Role: llm.Role(entry.Role), Content: entry.Content})
		}
		messages = append(messages, preparedRequest.Messages[len(preparedRequest.Messages)-1])
		preparedRequest.Messages = messages
		return agentruntime.PreparedRun{Request: &preparedRequest, ProviderSelector: agentruntime.StaticProviderSelector{Provider: provider, Name: provider.Name(), Model: "fixture-model"}, References: frozen.Resources}, nil
	}
	authority := agentruntime.ExecutionAuthority{Owner: "context-worker", AttemptID: "context-attempt", Fence: 1}
	newStore := func() *preparedRunStore {
		return &preparedRunStore{record: agentruntime.RunRecord{Scope: scopeID, RunID: request.ID, Spec: savedSpec, Revision: 1, Status: agentruntime.RunRunning, Authority: authority, LeaseUntil: time.Now().Add(time.Minute)}, receipts: make(map[string]agentruntime.CommitReceipt)}
	}
	executionCtx := agentruntime.ContextWithExecutionAuthority(ctx, scopeID, authority)
	durable := &agentruntime.DurableRunner{Runner: agentruntime.NewRunner(agentruntime.Config{PrepareRun: preparer}), Store: newStore()}
	if _, err := durable.Execute(executionCtx, durable.Store.(*preparedRunStore).record, nil); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || len(provider.messages) != 3 || provider.messages[0].Content != oldDocument || provider.messages[1].Content != oldMemory || provider.messages[2].Content != query.Content {
		t.Fatalf("provider did not receive original skill and memory: %+v", provider.messages)
	}
	if tracked.liveReads.Load() != readsBeforeRestore {
		t.Fatal("recovery searched current memory instead of decoding the saved snapshot")
	}
	if err := os.Remove(filepath.Join(root, "v1", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	provider = &skillMemoryInputProvider{}
	durable.Store = newStore()
	if _, err := durable.Execute(executionCtx, durable.Store.(*preparedRunStore).record, nil); !errors.Is(err, agentruntime.ErrUnrecoverableSpec) || !errors.Is(err, skill.ErrRevisionUnavailable) {
		t.Fatalf("missing original skill did not fail explicitly: %v", err)
	}
	if provider.calls != 0 || frozenReads != 2 || tracked.liveReads.Load() != readsBeforeRestore {
		t.Fatal("missing original skill fell back to latest context or dispatched the provider")
	}
}
