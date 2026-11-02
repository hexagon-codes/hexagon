package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	coretool "github.com/hexagon-codes/ai-core/tool"
)

type dynamicCatalogTool struct {
	version  string
	calls    atomic.Int32
	releases atomic.Int32
	started  chan struct{}
	proceed  chan struct{}
}

func (*dynamicCatalogTool) Name() string          { return "lookup" }
func (t *dynamicCatalogTool) Description() string { return "lookup " + t.version }
func (t *dynamicCatalogTool) Schema() *llm.Schema {
	return &llm.Schema{Type: "object", Properties: map[string]*llm.Schema{t.version: {Type: "string"}}}
}
func (*dynamicCatalogTool) Validate(map[string]any) error { return nil }
func (t *dynamicCatalogTool) Execute(context.Context, map[string]any) (coretool.Result, error) {
	t.calls.Add(1)
	if t.started != nil {
		close(t.started)
		<-t.proceed
	}
	return coretool.NewResult(t.version), nil
}

// 复用实际 ai-core Tool 对象；选择时只读一次对象，再从它派生定义和执行路由。
type dynamicBoundExecutor struct{ tool coretool.Tool }

func (e dynamicBoundExecutor) Execute(ctx context.Context, call llm.ToolCall) (ToolResult, error) {
	args, err := coretool.ParseArgs(call.Arguments)
	if err != nil {
		return ToolResult{}, err
	}
	result, err := e.tool.Execute(ctx, args)
	return ToolResult{Content: fmt.Sprint(result.Output), Raw: result, Error: result.Error, Status: ToolStatusSuccess}, err
}
func (dynamicBoundExecutor) SideEffectOf(llm.ToolCall) ToolSideEffect { return SideEffectReadOnly }

func dynamicBinding(t *testing.T, item *dynamicCatalogTool) ToolBinding {
	t.Helper()
	definition := llm.NewToolDefinition(item.Name(), item.Description(), item.Schema())
	content, _ := json.Marshal(map[string]string{"tool": item.Name(), "revision": item.version})
	digest, err := ContentDigest(content)
	if err != nil {
		t.Fatal(err)
	}
	return ToolBinding{Definition: definition, Executor: dynamicBoundExecutor{tool: item}, References: []ContentReference{{Kind: "tool", ID: item.Name(), Revision: item.version, Digest: digest, Content: content}}}
}

type dynamicModel struct {
	complete func(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error)
}

func (*dynamicModel) Name() string { return "dynamic-fixture" }
func (p *dynamicModel) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return p.complete(ctx, req)
}
func (*dynamicModel) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, errors.New("fixture does not stream")
}

func assertDynamicDefinitions(t *testing.T, defs []llm.ToolDefinition, names ...string) {
	t.Helper()
	if len(defs) != len(names) {
		t.Fatalf("visible definitions=%v want=%v", defs, names)
	}
	for index, name := range names {
		if defs[index].Function.Name != name {
			t.Fatalf("definition[%d]=%s want=%s", index, defs[index].Function.Name, name)
		}
	}
}

// 实际 Runner 的四轮请求证明搜索后模型可见集变化，而且执行仍使用最初固定的资源。
func TestDynamicToolsRunnerDiscoversAndExecutesPinnedBinding(t *testing.T) {
	v1, v2 := &dynamicCatalogTool{version: "v1"}, &dynamicCatalogTool{version: "v2"}
	catalog := coretool.NewRegistry()
	if err := catalog.Register(v1); err != nil {
		t.Fatal(err)
	}
	view, err := NewDynamicTools(DynamicToolConfig{Discover: func(_ context.Context, query string) (ToolBindingBatch, error) {
		if query != "lookup" {
			return ToolBindingBatch{}, fmt.Errorf("unexpected query %q", query)
		}
		selected, _ := catalog.Get("lookup")
		item := selected.(*dynamicCatalogTool)
		return ToolBindingBatch{Bindings: []ToolBinding{dynamicBinding(t, item)}, Release: func() { item.releases.Add(1) }}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.Release)
	turn, policyCalls := 0, 0
	provider := &dynamicModel{complete: func(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
		turn++
		if turn == 1 {
			assertDynamicDefinitions(t, req.Tools, "search_tools")
		} else {
			assertDynamicDefinitions(t, req.Tools, "search_tools", "lookup")
			if req.Tools[1].Function.Parameters.Properties["v1"] == nil || req.Tools[1].Function.Description != "lookup v1" {
				t.Fatalf("published binding changed generation: %+v", req.Tools[1])
			}
		}
		switch turn {
		case 1:
			return &llm.CompletionResponse{ToolCalls: []llm.ToolCall{{ID: "search-1", Name: "search_tools", Arguments: `{"query":"lookup"}`}}}, nil
		case 2:
			if err := catalog.Register(v2); err != nil {
				t.Fatal(err)
			}
			return &llm.CompletionResponse{ToolCalls: []llm.ToolCall{{ID: "search-2", Name: "search_tools", Arguments: `{"query":"lookup"}`}}}, nil
		case 3:
			return &llm.CompletionResponse{ToolCalls: []llm.ToolCall{{ID: "lookup", Name: "lookup", Arguments: `{"v1":"value"}`}}}, nil
		default:
			return &llm.CompletionResponse{Content: "complete"}, nil
		}
	}}
	runner := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}, ToolExecutor: view, Middleware: []Middleware{MiddlewareFuncSet{BeforeToolFunc: func(_ context.Context, _ *State, call llm.ToolCall) error {
		if call.Name == "lookup" {
			policyCalls++
		}
		return nil
	}}}})
	result, err := runner.Run(t.Context(), Request{ID: "dynamic-run", Tools: []llm.ToolDefinition{llm.NewToolDefinition("unselected", "must not be sent", &llm.Schema{Type: "object"})}})
	if err != nil || result.Content != "complete" || v1.calls.Load() != 1 || v2.calls.Load() != 0 || policyCalls != 1 || v2.releases.Load() != 1 || v1.releases.Load() != 0 {
		t.Fatalf("dynamic binding chain failed: result=%+v err=%v calls=%d/%d policy=%d release=%d/%d", result, err, v1.calls.Load(), v2.calls.Load(), policyCalls, v1.releases.Load(), v2.releases.Load())
	}
	view.Release()
	view.Release()
	if v1.releases.Load() != 1 {
		t.Fatalf("selected lease released %d times", v1.releases.Load())
	}
}

func TestDynamicToolsRestoreUsesSavedBindingsWithoutSearchingLatest(t *testing.T) {
	v1 := &dynamicCatalogTool{version: "v1"}
	first, err := NewDynamicTools(DynamicToolConfig{Discover: func(context.Context, string) (ToolBindingBatch, error) {
		return ToolBindingBatch{Bindings: []ToolBinding{dynamicBinding(t, v1)}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.BindRun("restore-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Execute(t.Context(), llm.ToolCall{Name: "search_tools", Arguments: `{"query":"lookup"}`}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := first.SnapshotTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	first.Release()
	var saved ToolSnapshot
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	var latestSearches atomic.Int32
	restoredV1 := &dynamicCatalogTool{version: "v1"}
	config := DynamicToolConfig{Discover: func(context.Context, string) (ToolBindingBatch, error) {
		latestSearches.Add(1)
		return ToolBindingBatch{}, errors.New("latest must not be queried")
	}, Restore: func(_ context.Context, s ToolSnapshot) (ToolBindingBatch, error) {
		if len(s.Bindings) != 1 || s.Bindings[0].References[0].Revision != "v1" {
			return ToolBindingBatch{}, errors.New("wrong saved revision")
		}
		return ToolBindingBatch{Bindings: []ToolBinding{dynamicBinding(t, restoredV1)}, Release: func() { restoredV1.releases.Add(1) }}, nil
	}}
	restored, err := NewDynamicTools(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Release()
	if err := restored.RestoreTools(t.Context(), saved); err != nil {
		t.Fatal(err)
	}
	if err := restored.BindRun("other-run"); err == nil {
		t.Fatal("a mutable tool selection crossed run ownership")
	}
	result, err := restored.Execute(t.Context(), llm.ToolCall{Name: "lookup", Arguments: `{}`})
	if err != nil || result.Content != "v1" || restoredV1.calls.Load() != 1 || latestSearches.Load() != 0 {
		t.Fatalf("restore queried latest or lost execution: %+v %v", result, err)
	}
	v2 := &dynamicCatalogTool{version: "v2"}
	config.Restore = func(context.Context, ToolSnapshot) (ToolBindingBatch, error) {
		return ToolBindingBatch{Bindings: []ToolBinding{dynamicBinding(t, v2)}, Release: func() { v2.releases.Add(1) }}, nil
	}
	wrong, err := NewDynamicTools(config)
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.Release()
	if err := wrong.RestoreTools(t.Context(), saved); !errors.Is(err, ErrUnrecoverableSpec) {
		t.Fatalf("wrong generation accepted: %v", err)
	}
	if v2.calls.Load() != 0 || v2.releases.Load() != 1 || latestSearches.Load() != 0 {
		t.Fatalf("failed restore executed latest or leaked its lease")
	}
}

func TestDynamicToolsReleaseWaitsForBorrowedExecution(t *testing.T) {
	item := &dynamicCatalogTool{version: "v1", started: make(chan struct{}), proceed: make(chan struct{})}
	view, err := NewDynamicTools(DynamicToolConfig{Discover: func(context.Context, string) (ToolBindingBatch, error) {
		return ToolBindingBatch{Bindings: []ToolBinding{dynamicBinding(t, item)}, Release: func() { item.releases.Add(1) }}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := view.BindRun("release-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := view.Execute(t.Context(), llm.ToolCall{Name: "search_tools", Arguments: `{"query":"lookup"}`}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := view.Execute(t.Context(), llm.ToolCall{Name: "lookup", Arguments: `{}`})
		done <- err
	}()
	<-item.started
	view.Release()
	if item.releases.Load() != 0 {
		t.Fatal("active tool lost its pinned resource")
	}
	close(item.proceed)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if item.releases.Load() != 1 {
		t.Fatalf("resource not drained exactly once: %d", item.releases.Load())
	}
}

func TestDynamicToolsInitialSelectionAndSnapshotAreIndependent(t *testing.T) {
	item := &dynamicCatalogTool{version: "v1"}
	initial := dynamicBinding(t, item)
	view, err := NewDynamicTools(DynamicToolConfig{Initial: []ToolBinding{initial}, Discover: func(context.Context, string) (ToolBindingBatch, error) { return ToolBindingBatch{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer view.Release()
	if err := view.BindRun("initial-run"); err != nil {
		t.Fatal(err)
	}
	initial.Definition.Function.Parameters.Properties["v1"].Description = "changed after pin"
	snapshot, err := view.SnapshotTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := snapshot.ToolDefinitions()
	if err != nil {
		t.Fatal(err)
	}
	assertDynamicDefinitions(t, definitions, "search_tools", "lookup")
	if strings.Contains(definitions[1].Function.Parameters.Properties["v1"].Description, "changed") {
		t.Fatal("caller mutation changed the fixed schema")
	}
	snapshot.Bindings[0].Definition.Function.Parameters.Properties["v1"].Description = "changed snapshot"
	again, err := view.SnapshotTools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if again.Bindings[0].Definition.Function.Parameters.Properties["v1"].Description != "" {
		t.Fatal("snapshot mutation changed the executor binding")
	}
}

// 该替身只提供本场景使用的读/提交端口；状态转换调用真实 ApplyCommit。
// 它证明 pending tool result 接线，不替代 SQL 原子性或进程耐久证据。
type dynamicPendingStore struct {
	RunStore
	mu       sync.Mutex
	record   RunRecord
	receipts map[string]CommitReceipt
}

func (s *dynamicPendingStore) GetRun(_ context.Context, scope, runID string) (RunRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope != s.record.Scope || runID != s.record.RunID {
		return RunRecord{}, false, nil
	}
	data, err := json.Marshal(s.record)
	if err != nil {
		return RunRecord{}, false, err
	}
	var record RunRecord
	err = json.Unmarshal(data, &record)
	return record, err == nil, err
}
func (s *dynamicPendingStore) Commit(_ context.Context, request CommitRequest) (CommitReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.receipts[request.CommitID]; ok {
		if old.Digest != request.Digest {
			return CommitReceipt{}, ErrCommitConflict
		}
		return old, nil
	}
	next, receipt, err := ApplyCommit(s.record, request, time.Now())
	if err != nil {
		return CommitReceipt{}, err
	}
	s.record = next
	s.receipts[request.CommitID] = receipt
	return receipt, nil
}
func (s *dynamicPendingStore) LookupCommit(_ context.Context, scope, id string) (CommitReceipt, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.receipts[id]
	return receipt, ok && scope == s.record.Scope, nil
}

func TestDynamicToolsCachedSearchRestoresBeforeSnapshotAdoption(t *testing.T) {
	const runID = "search-before-adoption"
	authority := ExecutionAuthority{Owner: "fixture-worker", AttemptID: "authority-1", Fence: 1}
	spec, err := FreezeRunSpec(Request{ID: runID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := &dynamicPendingStore{record: RunRecord{Scope: "scope", RunID: runID, Spec: spec, Authority: authority, LeaseUntil: time.Now().Add(time.Hour), Status: RunRunning}, receipts: map[string]CommitReceipt{}}
	firstSession := &runSession{store: store, record: store.record, authority: authority, pendingAdopt: map[string]bool{}, modelOrdinals: map[string]int{}}
	old := &dynamicCatalogTool{version: "v1"}
	var discoveries atomic.Int32
	firstView, err := NewDynamicTools(DynamicToolConfig{Discover: func(context.Context, string) (ToolBindingBatch, error) {
		discoveries.Add(1)
		return ToolBindingBatch{Bindings: []ToolBinding{dynamicBinding(t, old)}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := firstView.BindRun(runID); err != nil {
		t.Fatal(err)
	}
	searchCall := llm.ToolCall{ID: "search", Name: "search_tools", Arguments: `{"query":"lookup"}`}
	ctx := ContextWithToolInvocation(t.Context(), ToolInvocation{Scope: "scope", RunID: runID, TaskID: runID + "/turn/0/tool/0", Activation: "1", AttemptID: "physical-search", ToolCallID: searchCall.ID})
	first, err := firstSession.callTool(ctx, firstView, searchCall)
	if err != nil || first.ToolSnapshot == nil {
		t.Fatalf("search binding was not stored with its outcome: %+v %v", first, err)
	}
	firstView.Release()
	reloaded, found, err := store.GetRun(t.Context(), "scope", runID)
	if err != nil || !found || len(reloaded.Snapshot) != 0 || len(reloaded.Tasks) != 1 {
		t.Fatalf("fixture did not stop between result persistence and snapshot adoption: %+v %v", reloaded, err)
	}
	for _, task := range reloaded.Tasks {
		if task.AdoptedRevision != 0 {
			t.Fatal("search result was already adopted")
		}
	}
	restartedSession := &runSession{store: store, record: reloaded, authority: authority, pendingAdopt: map[string]bool{}, modelOrdinals: map[string]int{}}
	restored := &dynamicCatalogTool{version: "v1"}
	var restores atomic.Int32
	view, err := NewDynamicTools(DynamicToolConfig{Discover: func(context.Context, string) (ToolBindingBatch, error) {
		discoveries.Add(1)
		return ToolBindingBatch{}, errors.New("cached search must not use current discovery")
	}, Restore: func(_ context.Context, snapshot ToolSnapshot) (ToolBindingBatch, error) {
		restores.Add(1)
		if len(snapshot.Bindings) != 1 || snapshot.Bindings[0].References[0].Revision != "v1" {
			return ToolBindingBatch{}, errors.New("wrong persisted binding")
		}
		return ToolBindingBatch{Bindings: []ToolBinding{dynamicBinding(t, restored)}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer view.Release()
	turn := 0
	provider := &dynamicModel{complete: func(_ context.Context, request llm.CompletionRequest) (*llm.CompletionResponse, error) {
		turn++
		switch turn {
		case 1:
			assertDynamicDefinitions(t, request.Tools, "search_tools")
			return &llm.CompletionResponse{ToolCalls: []llm.ToolCall{searchCall}}, nil
		case 2:
			assertDynamicDefinitions(t, request.Tools, "search_tools", "lookup")
			return &llm.CompletionResponse{ToolCalls: []llm.ToolCall{{ID: "lookup", Name: "lookup", Arguments: `{}`}}}, nil
		default:
			return &llm.CompletionResponse{Content: "restored result"}, nil
		}
	}}
	runner := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}, ToolExecutor: view})
	result, err := runner.Run(ContextWithTaskExecutor(t.Context(), restartedSession), Request{ID: runID})
	if err != nil || result.Content != "restored result" || discoveries.Load() != 1 || restores.Load() != 1 || restored.calls.Load() != 1 || old.calls.Load() != 0 {
		t.Fatalf("cached discovery did not restore executable binding: %+v err=%v discoveries=%d restores=%d calls=%d", result, err, discoveries.Load(), restores.Load(), restored.calls.Load())
	}
}
