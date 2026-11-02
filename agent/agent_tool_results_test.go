package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/template"
	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/hexagon/agent/artifact"
	"github.com/hexagon-codes/hexagon/interrupt"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

func TestDeepAgentCompletedTaskRebuildReusesModelAndChildResults(t *testing.T) {
	var calls atomic.Int32
	provider := &svMockLLM{completeFn: func(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
		calls.Add(1)
		for _, msg := range req.Messages {
			if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "[Subtask") {
				return &llm.CompletionResponse{Content: "original child", Usage: llm.Usage{TotalTokens: 3}}, nil
			}
			if msg.Role == llm.RoleTool {
				return &llm.CompletionResponse{Content: "final", Usage: llm.Usage{TotalTokens: 5}}, nil
			}
		}
		return &llm.CompletionResponse{ToolCalls: []llm.ToolCall{{ID: "call", Name: "create_subtask", Arguments: `{"task":"research"}`}}, Usage: llm.Usage{TotalTokens: 2}}, nil
	}}
	base := newSvMockAgent("base", provider, nil)
	cache := &childTaskCache{saved: map[string][]byte{}}
	ctx := agentruntime.ContextWithTaskExecutor(t.Context(), cache)
	ctx = agentruntime.ContextWithToolInvocation(ctx, agentruntime.ToolInvocation{RunID: "parent", TaskID: "deep", AttemptID: "first"})
	first, err := NewAgentTool(NewDeepAgent("deep", base)).Execute(ctx, map[string]any{"query": "work"})
	if err != nil || !first.Success {
		t.Fatalf("first run failed: %+v %v", first, err)
	}
	second, err := NewAgentTool(NewDeepAgent("deep", base)).Execute(ctx, map[string]any{"query": "work"})
	child, ok := second.Output.(ChildRunResult)
	if err != nil || !second.Success || !ok || calls.Load() != 3 || child.Child.Output.Usage.TotalTokens != 10 || len(child.Child.Output.Children) != 1 || child.Child.Output.Children[0].Output.Content != "original child" {
		t.Fatalf("rebuild repeated completed work or lost facts: %+v err=%v model_calls=%d", second, err, calls.Load())
	}
}

type toolMediaFixture struct {
	Text  string `json:"text"`
	Image string `json:"image"`
}

func (r toolMediaFixture) ToolMessageContent() []llm.ContentPart {
	return []llm.ContentPart{template.NewTextPart(r.Text), template.NewImageURLPart(r.Image, "auto")}
}

func TestAgentToolsPreservePartialResultAndUsage(t *testing.T) {
	output := Output{Content: "partial result", Blocks: template.Blocks{template.TextBlock("retained block")}, Usage: llm.Usage{TotalTokens: 9, CacheCreationTokens: 3}, StopReason: agentruntime.StopReasonMaxTurns}
	child := &childResultFixture{BaseAgent: NewBaseAgent(WithName("partial")), output: output, failure: errors.New("child failed")}
	ctx := agentruntime.ContextWithToolInvocation(t.Context(), agentruntime.ToolInvocation{RunID: "parent", TaskID: "task", AttemptID: "attempt", ToolCallID: "call"})
	for _, adapter := range []tool.Tool{NewAgentTool(child), AgentAsTool(child)} {
		t.Run(adapter.Name(), func(t *testing.T) {
			result, err := adapter.Execute(ctx, map[string]any{"query": "task", "message": "task"})
			envelope, ok := result.Output.(ChildRunResult)
			if err != nil || result.Success || result.Error == "" || !ok || envelope.ParentRunID != "parent" || envelope.RunID == "parent" || envelope.Child.Output.Usage.TotalTokens != 9 || envelope.Child.Output.Usage.CacheCreationTokens != 3 || envelope.Child.Output.StopReason != agentruntime.StopReasonMaxTurns || len(envelope.Child.Output.Blocks) != 1 || envelope.Child.Output.Content != "partial result" {
				t.Fatalf("lost partial facts: %+v %v", result, err)
			}
		})
	}
}

func TestAgentToolExecutorPreservesTypedInterruptAndMedia(t *testing.T) {
	image := "data:image/png;base64,cGl4ZWw="
	child := &childResultFixture{BaseAgent: NewBaseAgent(WithName("waiting")), run: func(ctx context.Context, _ Input) (Output, error) {
		return Output{Content: "draft", Usage: llm.Usage{TotalTokens: 4}, ToolCalls: []ToolCallRecord{{Name: "render", Result: tool.NewResult(toolMediaFixture{Text: "preview", Image: image})}}}, interrupt.InterruptSignalFunc(ctx, "review")
	}}
	executor := &agentToolExecutor{tools: []tool.Tool{NewAgentTool(child)}}
	result, err := executor.Execute(t.Context(), llm.ToolCall{ID: "call", Name: "waiting", Arguments: `{"query":"create"}`})
	if _, ok := interrupt.IsInterruptSignal(err); !ok {
		t.Fatalf("interrupt became an ordinary failure: %+v %v", result, err)
	}
	raw, ok := result.Raw.(tool.Result)
	if !ok {
		t.Fatalf("missing structured raw result: %+v", result)
	}
	envelope, ok := raw.Output.(ChildRunResult)
	if !ok || envelope.Child.Output.Content != "draft" || envelope.Child.Output.Usage.TotalTokens != 4 || len(result.ContentParts) != 2 || result.ContentParts[1].ImageURL.URL != image {
		t.Fatalf("lost waiting output or media: %+v", result)
	}
}

// 真实临时文件保存原字节，登记端仅注入一次失败以验证补登记不会重新生成对象。
type toolResultDiskObjects struct {
	root string
	puts int
}

func (o *toolResultDiskObjects) Put(_ context.Context, key string, data []byte, _ string) error {
	o.puts++
	path := filepath.Join(o.root, key)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
func (o *toolResultDiskObjects) Get(_ context.Context, key string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(o.root, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, artifact.ErrObjectNotFound
	}
	return data, err
}

type toolResultRegistrationStore struct {
	*artifact.MemoryStore
	fail bool
}

func (s *toolResultRegistrationStore) CreateNextVersion(ctx context.Context, art artifact.Artifact) (string, error) {
	if s.fail {
		return "", errors.New("registration unavailable")
	}
	return s.MemoryStore.CreateNextVersion(ctx, art)
}

func TestToolResultOffloadRepairsRegistrationAndReadsOriginalMedia(t *testing.T) {
	objects := &toolResultDiskObjects{root: t.TempDir()}
	store := &toolResultRegistrationStore{MemoryStore: artifact.NewMemoryStore(), fail: true}
	offloader := &ToolResultOffloader{Artifacts: artifact.NewService(store), Objects: objects, MaxInlineBytes: 32}
	ctx := agentruntime.ContextWithToolInvocation(t.Context(), agentruntime.ToolInvocation{Scope: "scope", RunID: "run-one", TaskID: "call-position", Activation: "initial", AttemptID: "attempt", ToolCallID: "call"})
	original := toolMediaFixture{Text: strings.Repeat("complete source ", 100), Image: "data:image/png;base64,b3JpZ2luYWw="}
	failed, err := offloader.Offload(ctx, tool.NewResult(original))
	var pending *artifact.PendingRegistrationError
	pendingResult, hasPending := failed.Output.(StoredToolResult)
	if !errors.As(err, &pending) || !hasPending || pendingResult.Result.Output != original || objects.puts != 1 || strings.Contains(formatToolResult(failed), "Full tool result saved") {
		t.Fatalf("failed registration claimed success or lost data: %+v %v", failed, err)
	}
	store.fail = false
	restarted := &ToolResultOffloader{Artifacts: artifact.NewService(store), Objects: objects, MaxInlineBytes: 32}
	if _, err := restarted.Repair(ctx, pendingResult); err != nil {
		t.Fatal(err)
	}
	result, err := restarted.Offload(ctx, tool.NewResult(original))
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := result.Output.(StoredToolResult)
	if !ok || stored.Artifact.Version != 1 || objects.puts != 1 {
		t.Fatalf("repair regenerated content: %+v puts=%d", result, objects.puts)
	}
	if parts := stored.ToolMessageContent(); len(parts) != 1 || parts[0].ImageURL.URL != original.Image || strings.Contains(formatToolResult(result), original.Text) {
		t.Fatalf("offloaded view repeated full text or lost media: %+v", parts)
	}
	read, err := restarted.Read(ctx, stored.Artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := read.Result.Output.(map[string]any)
	if !ok || payload["text"] != original.Text || payload["image"] != original.Image || len(read.Parts) != 2 || read.Parts[1].ImageURL.URL != original.Image {
		t.Fatalf("original content was truncated: %+v", read)
	}
	otherCtx := agentruntime.ContextWithToolInvocation(t.Context(), agentruntime.ToolInvocation{Scope: "scope", RunID: "run-two", TaskID: "call-position", Activation: "initial", AttemptID: "attempt", ToolCallID: "call"})
	other, err := restarted.Offload(otherCtx, tool.NewResult(original))
	if err != nil || other.Output.(StoredToolResult).Artifact.ID == stored.Artifact.ID {
		t.Fatalf("separate runs overwrote the same object: %+v %v", other, err)
	}
}

func roundTripRuntimeToolResult(t *testing.T, result agentruntime.ToolResult) agentruntime.ToolResult {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var restored agentruntime.ToolResult
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.Raw.(map[string]any); !ok {
		t.Fatalf("fixture did not cross the Raw JSON boundary: %T", restored.Raw)
	}
	return restored
}

func TestAgentRuntimeJSONRoundTripPreservesNestedChildFacts(t *testing.T) {
	image := template.NewImageURLPart("data:image/png;base64,b3JpZ2luYWw=", "auto")
	grandchild := ChildRunResult{Version: 1, RunID: "grandchild", ParentRunID: "child", TaskID: "nested", Activation: "initial", AttemptID: "nested-attempt", Child: ChildResult{AgentID: "nested", AgentName: "nested", Status: "returned", Output: Output{Content: "nested original", Blocks: template.Blocks{template.TextBlock("nested block")}, Usage: llm.Usage{TotalTokens: 2}, StopReason: agentruntime.StopReasonMaxTurns}}, Parts: []llm.ContentPart{image}}
	child := &childResultFixture{BaseAgent: NewBaseAgent(WithName("child")), output: Output{Content: "partial original", Blocks: template.Blocks{template.TextBlock("original block")}, Usage: llm.Usage{PromptTokens: 4, CompletionTokens: 5, TotalTokens: 9, CacheReadTokens: 3, CacheCreationTokens: 1}, StopReason: agentruntime.StopReasonMaxTurns, Metadata: map[string]any{"sequence": json.Number("9007199254740993")}, ToolCalls: []ToolCallRecord{{Name: "nested", Result: tool.NewResult(grandchild)}}, Children: []ChildResult{grandchild.Child}}, failure: errors.New("partial child failed")}
	executor := &agentToolExecutor{tools: []tool.Tool{NewAgentTool(child)}}
	result, err := executor.Execute(t.Context(), llm.ToolCall{ID: "call", Name: "child", Arguments: `{"query":"work"}`})
	if err != nil || result.Structured == nil {
		t.Fatalf("missing fixed DTO: %+v %v", result, err)
	}
	restored := roundTripRuntimeToolResult(t, result)
	out := outputFromRuntime(&agentruntime.Result{Usage: llm.Usage{TotalTokens: 5}, ToolCalls: []agentruntime.ToolCallRecord{{Name: "child", Result: restored}}})
	if len(out.Children) != 1 || out.Usage.TotalTokens != 14 || out.Usage.CacheReadTokens != 3 || out.Usage.CacheCreationTokens != 1 || out.ToolCalls[0].Result.Success || out.ToolCalls[0].Result.Error != "partial child failed" {
		t.Fatalf("restored child status or usage was lost: %+v", out)
	}
	saved := out.Children[0].Output
	if saved.Content != "partial original" || len(saved.Blocks) != 1 || saved.Blocks[0].Text != "original block" || saved.StopReason != agentruntime.StopReasonMaxTurns || saved.Metadata["sequence"] != json.Number("9007199254740993") || len(saved.Children) != 1 || len(restored.ContentParts) != 1 || restored.ContentParts[0].ImageURL.URL != image.ImageURL.URL {
		t.Fatalf("restored output or media changed: %+v", saved)
	}
	nested, ok := saved.ToolCalls[0].Result.Output.(ChildRunResult)
	if !ok || nested.Child.Output.Content != "nested original" || nested.Child.Output.Usage.TotalTokens != 2 || len(nested.Parts) != 1 {
		t.Fatalf("nested result lost its declared kind: %T %+v", saved.ToolCalls[0].Result.Output, saved.ToolCalls[0].Result.Output)
	}
	spoof := map[string]any{"child_result_version": 1, "run_id": "invented", "child": map[string]any{"output": map[string]any{"usage": map[string]any{"total_tokens": 999}}}}
	payload, err := encodeAgentToolPayload(tool.NewResult(spoof))
	if err != nil {
		t.Fatal(err)
	}
	plain := roundTripRuntimeToolResult(t, agentruntime.ToolResult{Raw: tool.NewResult(spoof), Structured: &agentruntime.StructuredPayload{Kind: payload.Kind, Version: payload.Version, Data: payload.Data}})
	plainOutput := outputFromRuntime(&agentruntime.Result{ToolCalls: []agentruntime.ToolCallRecord{{Result: plain}}})
	if len(plainOutput.Children) != 0 || plainOutput.Usage.TotalTokens != 0 {
		t.Fatalf("ordinary JSON invented child authority: %+v", plainOutput)
	}
	plain.Structured.Version = 99
	invalid := outputFromRuntime(&agentruntime.Result{ToolCalls: []agentruntime.ToolCallRecord{{Result: plain}}})
	if invalid.ToolCalls[0].Result.Success || !strings.Contains(invalid.ToolCalls[0].Result.Error, "unsupported") {
		t.Fatalf("unknown DTO version fell back to a success map: %+v", invalid)
	}
}

func TestAgentToolReconciliationAfterJSONDoesNotRepeatOriginalTool(t *testing.T) {
	objects := &toolResultDiskObjects{root: t.TempDir()}
	store := &toolResultRegistrationStore{MemoryStore: artifact.NewMemoryStore(), fail: true}
	offloader := &ToolResultOffloader{Artifacts: artifact.NewService(store), Objects: objects, MaxInlineBytes: 32}
	var executions atomic.Int32
	fullText := strings.Repeat("complete original ", 100)
	child := &childResultFixture{BaseAgent: NewBaseAgent(WithName("original")), run: func(context.Context, Input) (Output, error) {
		executions.Add(1)
		return Output{Content: fullText, Usage: llm.Usage{TotalTokens: 7}, ToolCalls: []ToolCallRecord{{Name: "image", Result: tool.NewResult(toolMediaFixture{Text: "preview", Image: "data:image/png;base64,aW1hZ2U="})}}}, nil
	}}
	ctx := agentruntime.ContextWithToolInvocation(t.Context(), agentruntime.ToolInvocation{Scope: "scope", RunID: "parent", TaskID: "original-task", Activation: "1", AttemptID: "original-attempt", ToolCallID: "call"})
	ctx = ContextWithToolResultOffloader(ctx, offloader)
	executor := &agentToolExecutor{tools: []tool.Tool{NewAgentTool(child)}}
	call := llm.ToolCall{ID: "call", Name: "original", Arguments: `{"query":"generate"}`}
	result, err := executor.Execute(ctx, call)
	var storageErr *ToolResultStorageError
	if !errors.As(err, &storageErr) || !storageErr.NeedsReconciliation() || !result.NeedsReconciliation || result.Structured == nil {
		t.Fatalf("missing explicit storage repair state: %+v %v", result, err)
	}
	restored := roundTripRuntimeToolResult(t, result)
	projected := outputFromRuntime(&agentruntime.Result{ToolCalls: []agentruntime.ToolCallRecord{{Name: call.Name, Result: restored}}})
	pending, ok := projected.ToolCalls[0].Result.Output.(StoredToolResult)
	if !ok || pending.StorageError == "" || pending.Artifact.ObjectKey == "" || len(projected.Children) != 1 || projected.Usage.TotalTokens != 7 || projected.Children[0].Output.Content != fullText {
		t.Fatalf("repair descriptor or child facts lost after JSON: %+v", projected)
	}
	store.fail = false
	ctx = ContextWithToolResultOffloader(ctx, &ToolResultOffloader{Artifacts: artifact.NewService(store), Objects: objects, MaxInlineBytes: 32})
	// 新的执行器没有原工具；补登记只依赖回执及宿主恢复的原存储。
	repairer := &agentToolExecutor{}
	repaired, handled, err := repairer.ReconcileToolResult(ctx, call, restored)
	if err != nil || !handled || repaired.NeedsReconciliation || repaired.Status != agentruntime.ToolStatusSuccess || repaired.Error != "" || executions.Load() != 1 || objects.puts != 1 {
		t.Fatalf("repair repeated business execution: %+v handled=%v err=%v executions=%d puts=%d", repaired, handled, err, executions.Load(), objects.puts)
	}
	repaired = roundTripRuntimeToolResult(t, repaired)
	final := outputFromRuntime(&agentruntime.Result{ToolCalls: []agentruntime.ToolCallRecord{{Name: call.Name, Result: repaired}}})
	if len(final.Children) != 1 || final.Children[0].Output.Content != fullText || final.Usage.TotalTokens != 7 || len(repaired.ContentParts) != 1 || repaired.ContentParts[0].ImageURL.URL != "data:image/png;base64,aW1hZ2U=" {
		t.Fatalf("reconciled public result lost original content: %+v", final)
	}
	if _, handled, err := repairer.ReconcileToolResult(ctx, call, restored); err != nil || !handled || objects.puts != 1 || executions.Load() != 1 {
		t.Fatalf("repeated metadata repair changed original work: handled=%v err=%v", handled, err)
	}
}

// 合成存储仅提供此集成实际使用的读/提交端口；转换复用真实 ApplyCommit。
// 协调者接管在测试中显式模拟，不替代 SQL 事务、领取和进程耐久验证。
type agentReconciliationStore struct {
	agentruntime.RunStore
	mu       sync.Mutex
	record   agentruntime.RunRecord
	receipts map[string]agentruntime.CommitReceipt
}

func (s *agentReconciliationStore) GetRun(_ context.Context, scope, runID string) (agentruntime.RunRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope != s.record.Scope || runID != s.record.RunID {
		return agentruntime.RunRecord{}, false, nil
	}
	data, err := json.Marshal(s.record)
	if err != nil {
		return agentruntime.RunRecord{}, false, err
	}
	var record agentruntime.RunRecord
	err = json.Unmarshal(data, &record)
	return record, err == nil, err
}
func (s *agentReconciliationStore) Commit(_ context.Context, request agentruntime.CommitRequest) (agentruntime.CommitReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.receipts[request.CommitID]; ok {
		if old.Digest != request.Digest {
			return agentruntime.CommitReceipt{}, agentruntime.ErrCommitConflict
		}
		return old, nil
	}
	next, receipt, err := agentruntime.ApplyCommit(s.record, request, time.Now())
	if err != nil {
		return agentruntime.CommitReceipt{}, err
	}
	s.record = next
	s.receipts[request.CommitID] = receipt
	return receipt, nil
}
func (s *agentReconciliationStore) LookupCommit(_ context.Context, scope, id string) (agentruntime.CommitReceipt, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.receipts[id]
	return receipt, ok && scope == s.record.Scope, nil
}

func TestAgentToolDurableReconciliationKeepsOriginalTaskAndUsage(t *testing.T) {
	const runID = "agent-artifact-reconciliation"
	objects := &toolResultDiskObjects{root: t.TempDir()}
	artifacts := &toolResultRegistrationStore{MemoryStore: artifact.NewMemoryStore(), fail: true}
	var executions, modelCalls atomic.Int32
	fullText := strings.Repeat("original child output ", 100)
	child := &childResultFixture{BaseAgent: NewBaseAgent(WithName("original")), run: func(context.Context, Input) (Output, error) {
		executions.Add(1)
		return Output{Content: fullText, Usage: llm.Usage{TotalTokens: 7}, ToolCalls: []ToolCallRecord{{Name: "render", Result: tool.NewResult(toolMediaFixture{Text: "image", Image: "data:image/png;base64,cHJldmlldw=="})}}}, nil
	}}
	wrapped := NewAgentTool(child)
	call := llm.ToolCall{ID: "original-call", Name: wrapped.Name(), Arguments: `{"query":"generate"}`}
	provider := &svMockLLM{completeFn: func(_ context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
		modelCalls.Add(1)
		for _, message := range req.Messages {
			if message.Role != llm.RoleTool {
				continue
			}
			if !strings.Contains(message.Content, "Full tool result saved as artifact") || message.ToolResultError {
				t.Fatalf("model saw an unreconciled result: %+v", message)
			}
			return &llm.CompletionResponse{Content: "parent complete", Usage: llm.Usage{TotalTokens: 3}, UsageReceipt: &llm.Usage{TotalTokens: 3}}, nil
		}
		return &llm.CompletionResponse{ToolCalls: []llm.ToolCall{call}, Usage: llm.Usage{TotalTokens: 2}, UsageReceipt: &llm.Usage{TotalTokens: 2}}, nil
	}}
	request := agentruntime.Request{ID: runID, Tools: []llm.ToolDefinition{llm.NewToolDefinition(wrapped.Name(), wrapped.Description(), wrapped.Schema())}, Messages: []llm.Message{{Role: llm.RoleUser, Content: "create artifact"}}}
	spec, err := agentruntime.FreezeRunSpec(request, nil)
	if err != nil {
		t.Fatal(err)
	}
	authority := agentruntime.ExecutionAuthority{Owner: "worker", AttemptID: "first-coordinator", Fence: 1}
	store := &agentReconciliationStore{record: agentruntime.RunRecord{Scope: "scope", RunID: runID, Spec: spec, Authority: authority, LeaseUntil: time.Now().Add(time.Hour), Status: agentruntime.RunRunning}, receipts: map[string]agentruntime.CommitReceipt{}}
	newRunner := func() *agentruntime.DurableRunner {
		return &agentruntime.DurableRunner{Store: store, Runner: agentruntime.NewRunner(agentruntime.Config{ProviderSelector: agentruntime.StaticProviderSelector{Provider: provider}, ToolExecutor: &agentToolExecutor{tools: []tool.Tool{wrapped}}})}
	}
	newContext := func(authority agentruntime.ExecutionAuthority) context.Context {
		ctx := agentruntime.ContextWithRunAuthority(t.Context(), "scope", runID, authority)
		return ContextWithToolResultOffloader(ctx, &ToolResultOffloader{Artifacts: artifact.NewService(artifacts), Objects: objects, MaxInlineBytes: 32})
	}
	first, err := newRunner().Execute(newContext(authority), store.record, nil)
	if err == nil || first == nil || store.record.Status != agentruntime.RunWaiting || executions.Load() != 1 || objects.puts != 1 || modelCalls.Load() != 1 {
		t.Fatalf("storage failure did not preserve waiting run: result=%+v err=%v status=%s executions=%d puts=%d models=%d", first, err, store.record.Status, executions.Load(), objects.puts, modelCalls.Load())
	}
	var failedID string
	var original agentruntime.TaskResult
	for id, task := range store.record.Tasks {
		if task.Status == agentruntime.TaskFailed {
			failedID, original = id, task
			break
		}
	}
	if failedID == "" {
		t.Fatal("original failed task fact was not retained")
	}
	originalJSON, _ := json.Marshal(original)
	artifacts.fail = false
	authority.AttemptID, authority.Fence = "second-coordinator", 2
	store.mu.Lock()
	store.record.Authority, store.record.Status = authority, agentruntime.RunRunning
	store.record.Revision++
	store.mu.Unlock()
	reloaded, _, err := store.GetRun(t.Context(), "scope", runID)
	if err != nil {
		t.Fatal(err)
	}
	final, err := newRunner().Execute(newContext(authority), reloaded, nil)
	if err != nil || final == nil || final.Content != "parent complete" || store.record.Status != agentruntime.RunSucceeded || executions.Load() != 1 || objects.puts != 1 || modelCalls.Load() != 2 {
		t.Fatalf("durable repair repeated original work or failed: result=%+v err=%v status=%s executions=%d puts=%d models=%d", final, err, store.record.Status, executions.Load(), objects.puts, modelCalls.Load())
	}
	out := outputFromRuntime(final)
	if out.Usage.TotalTokens != 12 || len(out.Children) != 1 || out.Children[0].Output.Content != fullText || len(final.ToolCalls) != 1 || final.ToolCalls[0].Result.NeedsReconciliation || len(final.ToolCalls[0].Result.ContentParts) != 1 {
		t.Fatalf("reconciliation lost facts or counted usage twice: %+v", out)
	}
	afterJSON, _ := json.Marshal(store.record.Tasks[failedID])
	if string(afterJSON) != string(originalJSON) {
		t.Fatal("repair overwrote the original failed task")
	}
	reconciled := false
	for id, task := range store.record.Tasks {
		if id == failedID || task.Status != agentruntime.TaskSucceeded {
			continue
		}
		var outcome agentruntime.TaskOutcome
		if json.Unmarshal(task.Result, &outcome) != nil {
			continue
		}
		var receipt struct {
			Handled bool                    `json:"handled"`
			Result  agentruntime.ToolResult `json:"result"`
		}
		if json.Unmarshal(outcome.Data, &receipt) == nil && receipt.Handled && receipt.Result.Structured != nil && !receipt.Result.NeedsReconciliation && task.AdoptedRevision > 0 {
			reconciled = true
		}
	}
	if !reconciled {
		t.Fatal("successful reconciliation was not independently retained and adopted")
	}
}
