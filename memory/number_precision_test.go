package memory

import (
	"encoding/json"
	"testing"

	coremem "github.com/hexagon-codes/ai-core/memory"
)

func assertMetadataInteger(t *testing.T, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil || string(encoded) != "9007199254740993" {
		t.Fatalf("metadata integer lost precision: %s, %v", encoded, err)
	}
}

func TestMemoryNumbersPreserveRecordSnapshotAndHistory(t *testing.T) {
	for name, create := range localStores(t) {
		t.Run(name, func(t *testing.T) {
			m := scopedFixture(t, create(t), "number-precision")
			entry := coremem.Entry{ID: "large", Content: "original material", Metadata: map[string]any{"id": int64(9007199254740993)}}
			saved, err := m.Save(t.Context(), Record{Scope: m.Scope(), Source: "material", Entry: entry}, 0)
			if err != nil {
				t.Fatal(err)
			}
			assertMetadataInteger(t, saved.Entry.Metadata["id"])
			loaded, err := m.Get(t.Context(), entry.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertMetadataInteger(t, loaded.Entry.Metadata["id"])
			snapshot, err := m.Capture(t.Context(), coremem.SearchQuery{Metadata: map[string]any{"id": int64(9007199254740993)}})
			if err != nil || len(snapshot.Records()) != 1 {
				t.Fatalf("capture: %v", err)
			}
			bytes, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var restored Snapshot
			if err := json.Unmarshal(bytes, &restored); err != nil {
				t.Fatal(err)
			}
			assertMetadataInteger(t, restored.Records()[0].Entry.Metadata["id"])
			decoded, err := DecodeSnapshot(bytes, m.Scope())
			if err != nil || decoded.Revision() != snapshot.Revision() {
				t.Fatalf("restore: %v", err)
			}
			entries := decoded.Entries()
			assertMetadataInteger(t, entries[0].Metadata["id"])
			entries[0].Metadata["id"] = "modified"
			assertMetadataInteger(t, decoded.Entries()[0].Metadata["id"])
			frozen, err := FreezeEntries(m.Scope(), "material", []coremem.Entry{entry})
			if err != nil {
				t.Fatal(err)
			}
			assertMetadataInteger(t, frozen.Entries()[0].Metadata["id"])
			anchor, err := m.SaveHistory(t.Context(), "history", "material", entry.ID, []coremem.Entry{entry})
			if err != nil {
				t.Fatal(err)
			}
			window, err := m.ReadWindow(t.Context(), anchor, 0, 0)
			if err != nil || len(window) != 1 {
				t.Fatalf("history: %v", err)
			}
			assertMetadataInteger(t, window[0].Metadata["id"])
		})
	}
}
