package memory

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	coremem "github.com/hexagon-codes/ai-core/memory"
	"github.com/hexagon-codes/hexagon/memory/store"
)

func localStores(t *testing.T) map[string]func(*testing.T) store.MemoryStore {
	t.Helper()
	return map[string]func(*testing.T) store.MemoryStore{
		"in-memory": func(t *testing.T) store.MemoryStore {
			s := store.NewInMemoryStore()
			t.Cleanup(func() { _ = s.Close() })
			return s
		},
		"file": func(t *testing.T) store.MemoryStore {
			s, err := store.NewFileStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			return s
		},
	}
}

func scopedFixture(t *testing.T, s store.MemoryStore, namespace ...string) *ScopedMemory {
	t.Helper()
	m, err := NewScopedMemory(s, Scope{Namespace: namespace})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func recordFixture(scope Scope, id, content string) Record {
	return Record{Scope: scope, Source: "session:synthetic", Entry: coremem.Entry{ID: id, Role: "user", Content: content, Metadata: map[string]any{"count": 1, "nested": map[string]any{"original": true}}}}
}

func TestSnapshotFixedVersionsAndConditionalUpdates(t *testing.T) {
	for name, create := range localStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			storage := create(t)
			m := scopedFixture(t, storage, "users", "synthetic-a")
			record, err := m.Save(ctx, recordFixture(m.Scope(), "preference", "Original fact"), 0)
			if err != nil {
				t.Fatal(err)
			}
			if record.Revision != 1 {
				t.Fatal("first stored revision is not one")
			}
			metadataView, filterErr := m.Capture(ctx, coremem.SearchQuery{Metadata: map[string]any{"count": 1}})
			if filterErr != nil || len(metadataView.Entries()) != 1 {
				t.Fatalf("JSON metadata filter: %v", filterErr)
			}
			old, err := m.Capture(ctx, coremem.SearchQuery{})
			if err != nil {
				t.Fatal(err)
			}
			saved, err := json.Marshal(old)
			if err != nil {
				t.Fatal(err)
			}
			record.Entry.Content = "Updated fact"
			var decoded Snapshot
			if err := json.Unmarshal(saved, &decoded); err != nil || decoded.Revision() != old.Revision() {
				t.Fatalf("snapshot JSON round trip: %v", err)
			}
			updated, err := m.Save(ctx, record, 1)
			if err != nil || updated.Revision != 2 {
				t.Fatalf("conditional update: %#v, %v", updated, err)
			}
			if _, err := m.Save(ctx, record, 1); !errors.Is(err, store.ErrRevisionConflict) {
				t.Fatalf("stale write overwrote latest: %v", err)
			}
			if _, err := m.Save(ctx, record, 0); !errors.Is(err, store.ErrRevisionConflict) {
				t.Fatalf("create overwrote existing: %v", err)
			}
			current, err := m.Capture(ctx, coremem.SearchQuery{})
			if err != nil {
				t.Fatal(err)
			}
			if old.Entries()[0].Content != "Original fact" || current.Entries()[0].Content != "Updated fact" {
				t.Fatal("shared update changed fixed run view")
			}
			entries := old.Entries()
			entries[0].Metadata["nested"].(map[string]any)["original"] = false
			if old.Entries()[0].Metadata["nested"].(map[string]any)["original"] != true {
				t.Fatal("snapshot getter exposed mutable metadata")
			}
			restored, err := DecodeSnapshot(saved, m.Scope())
			if err != nil {
				t.Fatal(err)
			}
			if restored.Revision() != old.Revision() || restored.Entries()[0].Content != "Original fact" {
				t.Fatal("snapshot restore read current store instead of original revision")
			}
			latest, err := m.Get(ctx, "preference")
			if err != nil || latest.Entry.Content != "Updated fact" {
				t.Fatalf("conflict changed persisted result: %#v, %v", latest, err)
			}
		})
	}
}

func TestSnapshotScopeAndDecodeFailures(t *testing.T) {
	storage := store.NewInMemoryStore()
	defer storage.Close()
	a := scopedFixture(t, storage, "a:b", "c")
	b := scopedFixture(t, storage, "a", "b:c")
	ctx := context.Background()
	for _, m := range []*ScopedMemory{a, b} {
		record := recordFixture(m.Scope(), "same-id", m.Scope().Namespace[0])
		if _, err := m.Save(ctx, record, 0); err != nil {
			t.Fatal(err)
		}
		metadataView, err := m.Capture(ctx, coremem.SearchQuery{Metadata: map[string]any{"nested": map[string]any{"original": true}}})
		if err != nil || len(metadataView.Entries()) != 1 {
			t.Fatalf("JSON metadata filter: %v", err)
		}
	}
	snapshot, err := a.Capture(ctx, coremem.SearchQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries()) != 1 || snapshot.Entries()[0].Content != "a:b" {
		t.Fatal("scope namespace collision or search leak")
	}
	saved, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSnapshot(saved, b.Scope()); !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("snapshot crossed scope: %v", err)
	}
	if _, err := a.Save(ctx, recordFixture(b.Scope(), "wrong", "wrong"), 0); !errors.Is(err, ErrScopeMismatch) {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte("{"), []byte(`{"format":8}`)} {
		if _, err := DecodeSnapshot(data, a.Scope()); err == nil {
			t.Fatal("invalid snapshot decoded")
		}
	}
	var document map[string]any
	if err := json.Unmarshal(saved, &document); err != nil {
		t.Fatal(err)
	}
	document["records"].([]any)[0].(map[string]any)["entry"].(map[string]any)["content"] = "tampered"
	bad, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSnapshot(bad, a.Scope()); err == nil {
		t.Fatal("changed content retained original snapshot revision")
	}
	if err := storage.Put(ctx, a.namespace("records"), recordKey("malformed"), map[string]any{"record": "{"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Capture(ctx, coremem.SearchQuery{}); err == nil {
		t.Fatal("stored decoding failure was silently skipped")
	}
}

func TestSnapshotDoesNotAdoptStaleOrExpiredFacts(t *testing.T) {
	storage := store.NewInMemoryStore()
	defer storage.Close()
	m := scopedFixture(t, storage, "users", "synthetic")
	ctx := context.Background()
	active := recordFixture(m.Scope(), "active", "Active fact")
	stale := recordFixture(m.Scope(), "stale", "Stale fact")
	stale.Entry.Stale = true
	expired := recordFixture(m.Scope(), "expired", "Expired fact")
	until := time.Now().Add(-time.Second)
	expired.ValidUntil = &until
	for _, record := range []Record{active, stale, expired} {
		if _, err := m.Save(ctx, record, 0); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := m.Capture(ctx, coremem.SearchQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries()) != 1 || snapshot.Entries()[0].ID != "active" {
		t.Fatal("stale or expired facts were adopted into fresh context")
	}
}

func TestHistoryDiscoveryReadsOriginalAnchoredWindow(t *testing.T) {
	for name, create := range localStores(t) {
		t.Run(name, func(t *testing.T) {
			storage := create(t)
			m := scopedFixture(t, storage, "users", "synthetic")
			ctx := context.Background()
			messages := []coremem.Entry{{ID: "m1", Role: "user", Content: "Original question"}, {ID: "m2", Role: "assistant", Content: "Original answer"}, {ID: "m3", Role: "user", Content: "Original follow-up"}}
			anchor, err := m.SaveHistory(ctx, "task-1", "session-1", "m2", messages)
			if err != nil {
				t.Fatal(err)
			}
			summary := recordFixture(m.Scope(), "summary", "A historical summary")
			summary.Anchor = &anchor
			if _, err := m.Save(ctx, summary, 0); err != nil {
				t.Fatal(err)
			}
			hits, err := m.DiscoverHistory(ctx, "historical", 5)
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != 1 || hits[0].Anchor.Revision != anchor.Revision || hits[0].Summary != "A historical summary" {
				t.Fatalf("history discovery: %#v", hits)
			}
			messages[1].Content = "Updated answer"
			newAnchor, err := m.SaveHistory(ctx, "task-1", "session-1", "m2", messages)
			if err != nil {
				t.Fatal(err)
			}
			if newAnchor.Revision == anchor.Revision {
				t.Fatal("history updates reused immutable revision")
			}
			window, err := m.ReadWindow(ctx, hits[0].Anchor, 1, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(window) != 3 || window[1].Content != "Original answer" {
				t.Fatal("original anchor drifted to latest history")
			}
			newWindow, err := m.ReadWindow(ctx, newAnchor, 0, 0)
			if err != nil || len(newWindow) != 1 || newWindow[0].Content != "Updated answer" {
				t.Fatalf("new history window: %#v, %v", newWindow, err)
			}
			other := scopedFixture(t, storage, "users", "other")
			if _, err := other.ReadWindow(ctx, anchor, 1, 1); !errors.Is(err, ErrScopeMismatch) {
				t.Fatal(err)
			}
			anchor.Revision = "missing"
			if _, err := m.ReadWindow(ctx, anchor, 1, 1); !errors.Is(err, ErrRevisionUnavailable) {
				t.Fatalf("missing original revision selected latest: %v", err)
			}
		})
	}
}
