package agent

import (
	"context"
	"encoding/json"
	"testing"

	coremem "github.com/hexagon-codes/ai-core/memory"
	runmemory "github.com/hexagon-codes/hexagon/memory"
)

func TestSharedMemoryCaptureKeepsFixedViewAndInputMetadata(t *testing.T) {
	shared := NewSharedMemory()
	ctx := context.Background()
	entry := coremem.Entry{ID: "original", Role: "user", Content: "Original input", Metadata: map[string]any{"nested": map[string]any{"value": "original"}}}
	if err := shared.Save(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry.Metadata["nested"].(map[string]any)["value"] = "caller mutation"
	scope := runmemory.Scope{Namespace: []string{"team", "synthetic"}}
	snapshot, err := shared.Capture(ctx, scope, "shared/team-synthetic", coremem.SearchQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Entries()[0].Metadata["nested"].(map[string]any)["value"] != "original" {
		t.Fatal("shared memory retained caller metadata")
	}
	if !snapshot.Records()[0].Unversioned || snapshot.Records()[0].Revision != 0 {
		t.Fatal("legacy memory fabricated persistent revision guarantees")
	}
	if err := shared.Save(ctx, coremem.Entry{ID: "later", Role: "assistant", Content: "Later shared result"}); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries()) != 1 || snapshot.Entries()[0].Content != "Original input" {
		t.Fatal("shared update changed prepared run input")
	}
	saved, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := runmemory.DecodeSnapshot(saved, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Entries()) != 1 || restored.Revision() != snapshot.Revision() {
		t.Fatal("restored view searched shared memory again")
	}
}

func TestSharedMemoryProxyDoesNotMutateCallerProvenance(t *testing.T) {
	shared := NewSharedMemory()
	proxy := NewSharedMemoryProxy(coremem.NewBuffer(10), shared, "agent-a", "Agent A")
	entry := coremem.Entry{ID: "message", Metadata: map[string]any{"source": "host"}}
	if err := proxy.Save(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if _, exists := entry.Metadata["_agent_id"]; exists {
		t.Fatal("proxy added provenance to caller-owned metadata")
	}
	got, err := shared.Get(context.Background(), "message")
	if err != nil {
		t.Fatal(err)
	}
	got.Metadata["_agent_id"] = "mutated"
	again, err := shared.Get(context.Background(), "message")
	if err != nil {
		t.Fatal(err)
	}
	if again.Metadata["_agent_id"] != "agent-a" {
		t.Fatal("shared getter leaked mutable provenance")
	}
}
