package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	hr "github.com/hexagon-codes/hexagon/runtime"
	"github.com/hexagon-codes/hexagon/runtime/middleware"
)

func TestWaitingCommitPreservesAcceptedTargetWake(t *testing.T) {
	now := time.Now()
	authority := hr.ExecutionAuthority{Owner: "worker", AttemptID: "one", Fence: 1}
	for _, consumed := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "consumed"}[consumed], func(t *testing.T) {
			current := hr.RunRecord{Scope: "scope", RunID: "run", Status: hr.RunQueued, Authority: authority, LeaseUntil: now.Add(time.Minute), Inputs: map[string]hr.InboundInput{"approval": {ID: "approval", Target: "tool-b", Payload: json.RawMessage(`true`)}}}
			targets := []string{"tool-b"}
			current.WaitingTargets = &targets
			request := hr.CommitRequest{Scope: "scope", RunID: "run", CommitID: "finish", Authority: authority, Status: hr.RunWaiting}
			if consumed {
				request.ConsumeInputIDs = []string{"approval"}
			}
			sealed, err := hr.SealCommit(request)
			if err != nil {
				t.Fatal(err)
			}
			next, _, err := hr.ApplyCommit(current, sealed, now)
			if err != nil {
				t.Fatal(err)
			}
			want := hr.RunQueued
			if consumed {
				want = hr.RunWaiting
			}
			if next.Status != want {
				t.Fatalf("status=%s want=%s", next.Status, want)
			}
			if !consumed && next.Inputs["approval"].ConsumedRevision != 0 {
				t.Fatal("pending input was consumed")
			}
		})
	}
}

func TestWaitingCommitDoesNotWakeCompletedTarget(t *testing.T) {
	now := time.Now()
	authority := hr.ExecutionAuthority{Owner: "worker", AttemptID: "one", Fence: 1}
	targets := []string{"still-waiting-b"}
	current := hr.RunRecord{Scope: "s", RunID: "r", Status: hr.RunRunning, Authority: authority, LeaseUntil: now.Add(time.Minute), WaitingTargets: &targets, Inputs: map[string]hr.InboundInput{"late-a": {ID: "late-a", Target: "completed-a", Payload: json.RawMessage(`true`)}}}
	request, err := hr.SealCommit(hr.CommitRequest{Scope: "s", RunID: "r", CommitID: "wait-b", Authority: authority, Status: hr.RunWaiting})
	if err != nil {
		t.Fatal(err)
	}
	next, _, err := hr.ApplyCommit(current, request, now)
	if err != nil || next.Status != hr.RunWaiting || next.Inputs["late-a"].ConsumedRevision != 0 {
		t.Fatalf("stale input woke or was silently consumed: next=%+v err=%v", next, err)
	}
}

func TestBudgetStartSurvivesSnapshotJSON(t *testing.T) {
	start := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	now := start
	budget := middleware.Budget{Limits: middleware.BudgetLimits{MaxDuration: time.Hour}, Now: func() time.Time { return now }}
	state := (hr.Snapshot{RunID: "run", Phase: hr.PhaseBeforeModel}).RestoreState()
	if err := budget.BeforeLLM(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	encoded, err := hr.EncodeSnapshot(hr.SnapshotState(state, "run"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := hr.DecodeSnapshot(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.BudgetStartedAt == nil || !snapshot.BudgetStartedAt.Equal(start) {
		t.Fatal("budget origin was not persisted")
	}
	now = start.Add(2 * time.Hour)
	if err := budget.BeforeLLM(t.Context(), snapshot.RestoreState()); !errors.Is(err, hr.ErrBudgetExceeded) {
		t.Fatalf("duration budget restarted: %v", err)
	}
}

type ingressStore struct {
	hr.RunStore
	record   hr.RunRecord
	accepted int
}

func (s *ingressStore) GetRun(context.Context, string, string) (hr.RunRecord, bool, error) {
	return s.record, s.accepted > 0, nil
}
func (s *ingressStore) Submit(_ context.Context, request hr.SubmitRequest) (hr.RunRecord, error) {
	s.accepted++
	digest, err := hr.SubmissionDigest(request)
	if err != nil {
		return hr.RunRecord{}, err
	}
	s.record = hr.RunRecord{Scope: request.Scope, RunID: request.RunID, Spec: request.Spec, Inputs: map[string]hr.InboundInput{request.InputID: {ID: request.InputID, Payload: request.Input, Digest: digest}}}
	return s.record, nil
}

type unusedIngressProvider struct{}

func (unusedIngressProvider) Name() string { return "original" }
func (unusedIngressProvider) Complete(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
	panic("submit must not dispatch")
}
func (unusedIngressProvider) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	panic("submit must not dispatch")
}

func TestDurableSubmitReusesOriginalBeforePreparingLatest(t *testing.T) {
	store := &ingressStore{}
	prepared := 0
	runner := hr.NewRunner(hr.Config{ProviderSelector: hr.StaticProviderSelector{Provider: unusedIngressProvider{}, Model: "v1"}, PrepareRun: func(context.Context, hr.Request) (hr.PreparedRun, error) { prepared++; return hr.PreparedRun{}, nil }})
	durable := hr.DurableRunner{Runner: runner, Store: store}
	request := hr.Request{ID: "original-run", Messages: []llm.Message{llm.UserMessage("original input")}}
	first, err := durable.Submit(t.Context(), "scope", "incoming", request)
	if err != nil {
		t.Fatal(err)
	}
	// 当前装配已经不可用，原入站重试仍应取得原接收结果。
	durable.Runner = hr.NewRunner(hr.Config{PrepareRun: func(context.Context, hr.Request) (hr.PreparedRun, error) {
		t.Fatal("duplicate prepared latest resources")
		return hr.PreparedRun{}, nil
	}})
	second, err := durable.Submit(t.Context(), "scope", "incoming", request)
	if err != nil || second.Spec.SelectedProvider.Model != "v1" || first.Spec.IngressDigest != second.Spec.IngressDigest || store.accepted != 1 || prepared != 1 {
		t.Fatalf("original acceptance was not reused: result=%+v err=%v", second, err)
	}
	request.Messages[0].Content = "changed input"
	if _, err := durable.Submit(t.Context(), "scope", "incoming", request); !errors.Is(err, hr.ErrCommitConflict) {
		t.Fatalf("changed input accepted: %v", err)
	}
}

func TestDurableSubmitDoesNotTreatSignalAsAcceptance(t *testing.T) {
	store := &ingressStore{}
	durable := hr.DurableRunner{Runner: hr.NewRunner(hr.Config{ProviderSelector: hr.StaticProviderSelector{Provider: unusedIngressProvider{}, Model: "v1"}}), Store: store}
	request := hr.Request{ID: "run"}
	if _, err := durable.Submit(t.Context(), "s", "initial", request); err != nil {
		t.Fatal(err)
	}
	store.record.Inputs["signal"] = hr.InboundInput{ID: "signal", Target: "waiting", Payload: json.RawMessage(`true`)}
	if _, err := durable.Submit(t.Context(), "s", "signal", request); !errors.Is(err, hr.ErrCommitConflict) {
		t.Fatalf("signal reused as submission: %v", err)
	}
}
