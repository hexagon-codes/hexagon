package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
)

func sealedTestCommit(t *testing.T, request CommitRequest) CommitRequest {
	t.Helper()
	frozen, err := SealCommit(request)
	if err != nil {
		t.Fatal(err)
	}
	return frozen
}

// 独立保存 B 不采用其旧快照；A 到达后可按既定顺序同时采用 A、B。
func TestCommitPendingAndAdoptionAreSeparate(t *testing.T) {
	now := time.Now()
	authority := ExecutionAuthority{Owner: "worker", AttemptID: "coordinator-1", Fence: 7}
	current := RunRecord{Scope: "project", RunID: "run", Revision: 10, Authority: authority, LeaseUntil: now.Add(time.Minute), Status: RunRunning, Snapshot: []byte(`{"items":[]}`),
		TaskActivations: map[string]TaskActivation{
			"a": {TaskID: "a", Activation: "join-1", AttemptID: "a1", InputRevision: 10},
			"b": {TaskID: "b", Activation: "join-1", AttemptID: "b1", InputRevision: 10},
		}}
	b := TaskResult{TaskID: "b", Activation: "join-1", AttemptID: "b1", InputRevision: 10, Status: TaskSucceeded, Result: json.RawMessage(`"B"`)}
	request := sealedTestCommit(t, CommitRequest{Scope: "project", RunID: "run", CommitID: "save-b", ExpectedRevision: 10, Authority: authority, PendingResults: []TaskResult{b}, Events: []StoredEvent{{Type: "task_completed"}}})
	saved, receipt, err := ApplyCommit(current, request, now)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 11 || saved.Tasks["b"].AdoptedRevision != 0 || string(saved.Snapshot) != `{"items":[]}` || receipt.LastSequence != 1 || len(current.Tasks) != 0 {
		t.Fatalf("result was incorrectly adopted: %+v receipt=%+v", saved, receipt)
	}
	a := TaskResult{TaskID: "a", Activation: "join-1", AttemptID: "a1", InputRevision: 10, Status: TaskSucceeded, Result: json.RawMessage(`"A"`)}
	adopt := sealedTestCommit(t, CommitRequest{Scope: "project", RunID: "run", CommitID: "adopt", ExpectedRevision: 11, Authority: authority, PendingResults: []TaskResult{a}, AdoptTaskIDs: []string{"a", "b"}, Snapshot: []byte(`{"items":["A","B"]}`), Events: []StoredEvent{{Type: "state_adopted"}}})
	next, _, err := ApplyCommit(saved, adopt, now)
	if err != nil || next.Tasks["a"].AdoptedRevision != 12 || next.Tasks["b"].AdoptedRevision != 12 || next.NextSequence != 2 {
		t.Fatalf("adoption=%+v err=%v", next, err)
	}
	adopt.ExpectedRevision, adopt.CommitID = 12, "duplicate-adoption"
	adopt.PendingResults = nil
	if _, _, err := ApplyCommit(next, sealedTestCommit(t, adopt), now); err == nil {
		t.Fatal("adopted result was applied twice")
	}
	stale := request
	stale.CommitID, stale.ExpectedRevision = "stale", 12
	stale.PendingResults[0].AttemptID = "old-attempt"
	if _, _, err := ApplyCommit(next, sealedTestCommit(t, stale), now); err == nil {
		t.Fatal("stale task attempt was accepted")
	}
}

func TestCommitSealFreezesFactsAndPreservesEmptySnapshot(t *testing.T) {
	request := CommitRequest{Scope: "s", RunID: "r", CommitID: "c", Snapshot: []byte{}, Events: []StoredEvent{{Type: "fact", Payload: json.RawMessage(`{"x":1}`)}}}
	frozen := sealedTestCommit(t, request)
	request.Events[0].Payload[5] = '2'
	if frozen.Snapshot == nil || string(frozen.Events[0].Payload) != `{"x":1}` || ValidateCommit(frozen) != nil {
		t.Fatalf("commit is not isolated: %+v", frozen)
	}
	frozen.Events[0].Type = "changed"
	if !errors.Is(ValidateCommit(frozen), ErrCommitConflict) {
		t.Fatal("modified frozen request retained its digest")
	}
}

type specVersionedStrategy struct{ NoopStrategy }

func (specVersionedStrategy) Name() string { return "custom" }

func TestRunSpecPreservesConfigurationAndRejectsMissingVersion(t *testing.T) {
	request := Request{ID: "run", Purpose: "summary", Metadata: map[string]any{"revision": "old"}, Messages: []llm.Message{{Role: llm.RoleUser, Content: "original"}}}
	spec, err := FreezeRunSpec(request, nil)
	if err != nil {
		t.Fatal(err)
	}
	content := json.RawMessage("{\n  \"label\": \"<original>\",\n  \"revision\": 1000000000000000001\n}")
	sum := sha256.Sum256(content)
	withContent, err := FreezeRunSpec(request, []ContentReference{{Kind: "material", ID: "original", Revision: "1", Digest: hex.EncodeToString(sum[:]), Content: content}})
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := json.Marshal(withContent)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded RunSpec
	if err := json.Unmarshal(persisted, &reloaded); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Validate(); err != nil {
		t.Fatalf("inline content changed digest across persistence: %v", err)
	}
	request.Messages[0].Content = "changed"
	request.Metadata["revision"] = "new"
	restored, err := spec.RestoreRequest(nil)
	if err != nil || restored.Purpose != "summary" || restored.Messages[0].Content != "original" || restored.Metadata["revision"] != "old" {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	request.Strategy = specVersionedStrategy{}
	if _, err := FreezeRunSpec(request, nil); !errors.Is(err, ErrUnrecoverableSpec) {
		t.Fatalf("unversioned strategy accepted: %v", err)
	}
	broken := spec
	broken.Codec.Version = "future"
	if _, err := broken.RestoreRequest(nil); !errors.Is(err, ErrUnrecoverableSpec) {
		t.Fatalf("unknown codec accepted: %v", err)
	}
	if _, err := DecodeSnapshot([]byte(`{"run_id":"run","codec":{"type":"runtime.Snapshot","version":"future"}}`)); !errors.Is(err, ErrUnrecoverableSpec) {
		t.Fatalf("unknown snapshot decoded as new run: %v", err)
	}
	original := []byte(`{"run_id":"run"}`)
	codecs := NewSnapshotCodecs()
	codec := StateCodec{Type: "old", Version: "1"}
	if err := codecs.Register(codec, func(data []byte) ([]byte, error) { data[0] = '!'; return nil, errors.New("migration failed") }); err != nil {
		t.Fatal(err)
	}
	if _, err := codecs.Decode(original, codec, "run"); !errors.Is(err, ErrUnrecoverableSpec) || string(original) != `{"run_id":"run"}` {
		t.Fatalf("migration changed original data: %s err=%v", original, err)
	}
}

type operationLookupFunc func(context.Context, Operation) (Operation, bool, error)

func (f operationLookupFunc) LookupOperation(ctx context.Context, operation Operation) (Operation, bool, error) {
	return f(ctx, operation)
}

func TestUnknownOperationOnlyReconcilesOriginalIdentity(t *testing.T) {
	operation := Operation{ID: "logical-1", InputDigest: "input", Provider: "supplier", IdempotencyKey: "stable-1", Status: OperationUnknown}
	queries := 0
	lookup := operationLookupFunc(func(_ context.Context, original Operation) (Operation, bool, error) {
		queries++
		if original.ID != "logical-1" || original.IdempotencyKey != "stable-1" {
			t.Fatal("original identity changed")
		}
		return Operation{}, false, nil
	})
	if _, err := ResolveOperation(t.Context(), operation, lookup); !errors.Is(err, ErrOperationUnresolved) || queries != 1 {
		t.Fatalf("unknown result=%v lookups=%d", err, queries)
	}
	operation.Status = OperationSucceeded
	if _, err := ResolveOperation(t.Context(), operation, lookup); err != nil || queries != 1 {
		t.Fatalf("confirmed result was reexecuted: %v", err)
	}
}
