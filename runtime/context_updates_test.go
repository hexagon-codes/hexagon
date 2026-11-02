package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	rt "github.com/hexagon-codes/hexagon/runtime"
	mw "github.com/hexagon-codes/hexagon/runtime/middleware"
)

// 该 fixture 保存真实快照编码，验证重建时沿用水位与已采用身份，不声称 SQL 原子性。
type contextUpdateSnapshots struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (s *contextUpdateSnapshots) Save(_ context.Context, snapshot rt.Snapshot) error {
	data, err := rt.EncodeSnapshot(snapshot)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = map[string][]byte{}
	}
	s.data[snapshot.RunID] = append([]byte(nil), data...)
	return nil
}
func (s *contextUpdateSnapshots) Load(_ context.Context, runID string) (rt.Snapshot, bool, error) {
	s.mu.Lock()
	data, ok := s.data[runID]
	data = append([]byte(nil), data...)
	s.mu.Unlock()
	if !ok {
		return rt.Snapshot{}, false, nil
	}
	snapshot, err := rt.DecodeSnapshot(data)
	return snapshot, err == nil, err
}

type contextUpdateTool struct {
	execute func(context.Context, llm.ToolCall) (rt.ToolResult, error)
	calls   atomic.Int32
}

func (t *contextUpdateTool) Execute(ctx context.Context, call llm.ToolCall) (rt.ToolResult, error) {
	t.calls.Add(1)
	return t.execute(ctx, call)
}
func (*contextUpdateTool) SideEffectOf(llm.ToolCall) rt.ToolSideEffect { return rt.SideEffectReadOnly }

type countedContextTransform struct {
	candidate *mw.CompactionCandidate
	calls     atomic.Int32
}

func (c *countedContextTransform) Apply(revision uint64, messages []llm.Message) ([]llm.Message, error) {
	c.calls.Add(1)
	return c.candidate.Apply(revision, messages)
}

func TestContextUpdateAsyncSessionPreservesTailAndDeduplicatesAfterRestore(t *testing.T) {
	runContextUpdateScenario(t, false)
}
func TestContextUpdateRejectsEditedPrefixWithoutChangingHistory(t *testing.T) {
	runContextUpdateScenario(t, true)
}

func runContextUpdateScenario(t *testing.T, editPrefix bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	updates := make(chan rt.ContextUpdate, 2)
	ctx = rt.ContextWithUpdates(ctx, updates)
	summaryEntered, releaseSummary := make(chan struct{}), make(chan struct{})
	workerDone := make(chan struct{})
	var unblock sync.Once
	allowSummary := func() { unblock.Do(func() { close(releaseSummary) }) }
	type summaryOutcome struct {
		candidate *mw.CompactionCandidate
		report    rt.ModelSessionReport
		err       error
	}
	summaryDone := make(chan summaryOutcome, 1)
	var started, stopped, adopted atomic.Bool
	var compacted, rejected atomic.Int32
	var transform *countedContextTransform
	var workerReport rt.ModelSessionReport
	store := &contextUpdateSnapshots{}
	parentID := "context-update-append"
	if editPrefix {
		parentID = "context-update-edit"
	}
	childID := parentID + "/summary"
	summaryUsage := llm.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}
	summary := &modelSessionTestProvider{name: "async-summary", complete: func(callCtx context.Context, request llm.CompletionRequest) (*llm.CompletionResponse, error) {
		close(summaryEntered)
		select {
		case <-releaseSummary:
		case <-callCtx.Done():
			return nil, callCtx.Err()
		}
		return &llm.CompletionResponse{Content: "frozen history summary", Usage: summaryUsage, UsageReceipt: &summaryUsage}, nil
	}}
	var parentCalls atomic.Int32
	parent := &modelSessionTestProvider{name: "parent-model", complete: func(_ context.Context, request llm.CompletionRequest) (*llm.CompletionResponse, error) {
		call := parentCalls.Add(1)
		if call == 1 {
			usage := llm.Usage{TotalTokens: 1}
			return &llm.CompletionResponse{ToolCalls: []llm.ToolCall{{ID: "new-tool", Type: "function", Name: "lookup", Arguments: `{"query":"new"}`}}, Usage: usage, UsageReceipt: &usage}, nil
		}
		if call != 2 {
			return nil, fmt.Errorf("unexpected model replay: call %d", call)
		}
		hasSummary, hasEdited, toolResults, toolRequests := false, false, 0, 0
		for _, message := range request.Messages {
			hasSummary = hasSummary || strings.Contains(message.Content, "frozen history summary")
			hasEdited = hasEdited || message.Content == "edited original instruction"
			if message.Role == llm.RoleTool && message.ToolCallID == "new-tool" && message.Content == "new tool result" {
				toolResults++
			}
			for _, tool := range message.ToolCalls {
				if tool.ID == "new-tool" {
					toolRequests++
				}
			}
		}
		if editPrefix {
			if hasSummary || !hasEdited {
				return nil, fmt.Errorf("stale summary replaced edited prefix")
			}
		} else if !hasSummary {
			return nil, fmt.Errorf("summary candidate was not applied")
		}
		if toolResults != 1 || toolRequests != 1 {
			return nil, fmt.Errorf("new tool group was lost or replayed: requests=%d results=%d", toolRequests, toolResults)
		}
		usage := llm.Usage{TotalTokens: 2}
		return &llm.CompletionResponse{Content: "done", Usage: usage, UsageReceipt: &usage}, nil
	}}
	tool := &contextUpdateTool{execute: func(callCtx context.Context, _ llm.ToolCall) (rt.ToolResult, error) {
		select {
		case <-summaryEntered:
		case <-callCtx.Done():
			return rt.ToolResult{}, callCtx.Err()
		}
		allowSummary()
		var outcome summaryOutcome
		select {
		case outcome = <-summaryDone:
		case <-callCtx.Done():
			return rt.ToolResult{}, callCtx.Err()
		}
		if outcome.err != nil {
			return rt.ToolResult{}, outcome.err
		}
		workerReport = outcome.report
		transform = &countedContextTransform{candidate: outcome.candidate}
		update := rt.ContextUpdate{ID: "summary-candidate", RunID: parentID, Transform: transform}
		updates <- update
		if !editPrefix {
			updates <- update
		}
		return rt.ToolResult{Content: "new tool result", Status: rt.ToolStatusSuccess}, nil
	}}
	stopAfterAdoption := errors.New("stop after candidate snapshot")
	var runner *rt.DefaultRunner
	middleware := rt.MiddlewareFuncSet{BeforeLLMFunc: func(callCtx context.Context, state *rt.State) error {
		if started.CompareAndSwap(false, true) {
			session, err := runner.NewModelSession(callCtx, rt.Request{ID: childID, Purpose: mw.CompactionPurpose, Metadata: map[string]any{"parent_run_id": parentID}}, nil)
			if err != nil {
				close(workerDone)
				return err
			}
			selection, err := session.SelectProvider(callCtx)
			if err != nil {
				_ = session.Close()
				close(workerDone)
				return err
			}
			compaction := mw.Compaction{MaxTokens: 1, Compact: mw.ModelSummaryCompactor(selection.Provider, llm.CompletionRequest{Model: selection.Model}, 1)}
			work, err := compaction.Prepare(llm.CompletionRequest{Model: "parent-model", Messages: state.Messages, Tools: state.Request.Tools}, state.MessageRevision)
			if err != nil || work == nil {
				_ = session.Close()
				close(workerDone)
				return fmt.Errorf("prepare summary: %w", err)
			}
			go func() {
				defer close(workerDone)
				candidate, err := work.Run(session.Context(callCtx))
				closeErr := session.Close()
				summaryDone <- summaryOutcome{candidate: candidate, report: session.Report(), err: errors.Join(err, closeErr)}
			}()
			if editPrefix {
				for i := range state.Messages {
					if state.Messages[i].Content == "old request" {
						state.Messages[i].Content = "edited original instruction"
					}
				}
			}
		}
		if !editPrefix && adopted.Load() && stopped.CompareAndSwap(false, true) {
			return stopAfterAdoption
		}
		return nil
	}}
	runner = rt.NewRunner(rt.Config{Durable: store, ToolExecutor: tool, Middleware: []rt.Middleware{middleware}, PrepareRun: func(_ context.Context, request rt.Request) (rt.PreparedRun, error) {
		provider := rt.Provider(parent)
		model := "parent-model"
		if request.ID == childID {
			provider = summary
			model = "summary-model"
		}
		return rt.PreparedRun{ProviderSelector: rt.StaticProviderSelector{Provider: provider, Model: model}}, nil
	}})
	t.Cleanup(func() {
		cancel()
		allowSummary()
		if started.Load() {
			<-workerDone
		}
	})
	sink := rt.EventSinkFunc(func(_ context.Context, event rt.Event) error {
		if event.Type == rt.EventContextCompacted && event.Metadata["update_id"] == "summary-candidate" {
			compacted.Add(1)
			adopted.Store(true)
		}
		if event.Type == "context_update_rejected" {
			rejected.Add(1)
		}
		return nil
	})
	request := rt.Request{ID: parentID, Messages: []llm.Message{llm.SystemMessage("system"), llm.UserMessage("old request"), llm.AssistantMessage("old answer"), llm.UserMessage("latest question")}}
	result, err := runner.RunWithSink(ctx, request, sink)
	if !editPrefix {
		if !errors.Is(err, stopAfterAdoption) {
			t.Fatalf("did not stop after adoption snapshot: result=%+v err=%v", result, err)
		}
		snapshot, found, loadErr := store.Load(ctx, parentID)
		if loadErr != nil || !found || !snapshot.AppliedContextUpdates["summary-candidate"] || snapshot.Phase != rt.PhaseBeforeModel {
			t.Fatalf("adoption snapshot lost identity or execution phase: %+v %v", snapshot, loadErr)
		}
		result, err = runner.Resume(ctx, parentID, request, sink)
	}
	if err != nil || result == nil || result.Content != "done" {
		t.Fatalf("runtime did not finish: result=%+v err=%v", result, err)
	}
	if result.Usage.TotalTokens != 3 || workerReport.Usage != summaryUsage || workerReport.ReportedCalls != 1 || summary.calls.Load() != 1 {
		t.Fatalf("parent/session accounting was mixed: parent=%+v child=%+v calls=%d", result.Usage, workerReport, summary.calls.Load())
	}
	if tool.calls.Load() != 1 || parentCalls.Load() != 2 || transform == nil || transform.calls.Load() != 1 {
		t.Fatalf("restore repeated an update or dispatched work: tool=%d model=%d transform=%v", tool.calls.Load(), parentCalls.Load(), transform)
	}
	if editPrefix {
		if rejected.Load() != 1 || compacted.Load() != 0 {
			t.Fatalf("edited prefix event status: rejected=%d compacted=%d", rejected.Load(), compacted.Load())
		}
	} else if compacted.Load() != 1 || rejected.Load() != 0 {
		t.Fatalf("duplicate ID was adopted: compacted=%d rejected=%d", compacted.Load(), rejected.Load())
	}
	if _, found, _ := store.Load(ctx, childID); found {
		t.Fatal("model-only session wrote a child snapshot through the parent's durable port")
	}
}
