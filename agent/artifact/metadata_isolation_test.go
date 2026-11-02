package artifact

import (
	"context"
	"testing"
	"time"
)

type metadataLabels map[string][]byte
type metadataDetails struct {
	Labels    []string
	CreatedAt time.Time
}

// 所有公开存取入口都隔离嵌套值，同时保留原动态类型和循环结构。
func TestMemoryStoreNestedMetadataIsolation(t *testing.T) {
	for _, writeName := range []string{"Save", "SaveNextVersion", "CreateNextVersion"} {
		t.Run(writeName, func(t *testing.T) {
			store := NewMemoryStore()
			writes := map[string]func(context.Context, Artifact) (string, error){"Save": store.Save, "SaveNextVersion": store.SaveNextVersion, "CreateNextVersion": store.CreateNextVersion}
			createdAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.FixedZone("metadata", 3600))
			details := &metadataDetails{Labels: []string{"original"}, CreatedAt: createdAt}
			labels := metadataLabels{"origin": []byte("original")}
			cycle := map[string]any{"value": "original"}
			cycle["self"] = cycle
			art := Artifact{ID: "fixed", SessionID: "session", Name: "result", Version: 1, Data: []byte("bytes"), Metadata: map[string]any{"details": details, "labels": labels, "cycle": cycle}}
			if _, err := writes[writeName](t.Context(), art); err != nil {
				t.Fatal(err)
			}
			details.Labels[0] = "changed"
			labels["origin"][0] = 'X'
			cycle["value"] = "changed"
			art.Data[0] = 'X'
			getters := map[string]func() (*Artifact, error){
				"Get":          func() (*Artifact, error) { return store.Get(t.Context(), "fixed") },
				"GetLatest":    func() (*Artifact, error) { return store.GetLatest(t.Context(), "session", "result") },
				"GetByVersion": func() (*Artifact, error) { return store.GetByVersion(t.Context(), "session", "result", 1) },
				"List": func() (*Artifact, error) {
					items, err := store.List(t.Context(), "session")
					if err != nil {
						return nil, err
					}
					return &items[0], nil
				},
				"ListVersions": func() (*Artifact, error) {
					items, err := store.ListVersions(t.Context(), "session", "result")
					if err != nil {
						return nil, err
					}
					return &items[0], nil
				},
			}
			for name, get := range getters {
				t.Run(name, func(t *testing.T) {
					got, err := get()
					if err != nil {
						t.Fatal(err)
					}
					if got.Metadata["details"].(*metadataDetails).Labels[0] != "original" || string(got.Metadata["labels"].(metadataLabels)["origin"]) != "original" || got.Metadata["cycle"].(map[string]any)["value"] != "original" || string(got.Data) != "bytes" {
						t.Fatal("stored artifact retained caller references")
					}
					storedTime := got.Metadata["details"].(*metadataDetails).CreatedAt
					zone, offset := storedTime.Zone()
					if !storedTime.Equal(createdAt) || zone != "metadata" || offset != 3600 {
						t.Fatalf("metadata time changed: %v", storedTime)
					}
					got.Metadata["details"].(*metadataDetails).Labels[0] = "read-change"
					got.Metadata["labels"].(metadataLabels)["origin"][0] = 'Y'
					gotCycle := got.Metadata["cycle"].(map[string]any)
					gotCycle["self"].(map[string]any)["value"] = "read-change"
					if gotCycle["value"] != "read-change" {
						t.Fatal("copied cycle was not preserved")
					}
					again, err := store.Get(t.Context(), "fixed")
					if err != nil {
						t.Fatal(err)
					}
					if again.Metadata["details"].(*metadataDetails).Labels[0] != "original" || string(again.Metadata["labels"].(metadataLabels)["origin"]) != "original" || again.Metadata["cycle"].(map[string]any)["value"] != "original" {
						t.Fatal("read result mutated stored artifact")
					}
				})
			}
		})
	}
}

// 不支持隔离的值不写入记录，也不占用后续版本号。
func TestMemoryStoreUncopyableMetadataDoesNotWrite(t *testing.T) {
	for _, writeName := range []string{"Save", "SaveNextVersion", "CreateNextVersion"} {
		t.Run(writeName, func(t *testing.T) {
			store := NewMemoryStore()
			writes := map[string]func(context.Context, Artifact) (string, error){"Save": store.Save, "SaveNextVersion": store.SaveNextVersion, "CreateNextVersion": store.CreateNextVersion}
			art := Artifact{ID: "rejected", SessionID: "session", Name: "result", Version: 1, Metadata: map[string]any{"callback": func() {}}}
			if _, err := writes[writeName](t.Context(), art); err == nil {
				t.Fatal("unisolated metadata was accepted")
			}
			if _, err := store.Get(t.Context(), art.ID); err == nil {
				t.Fatal("rejected artifact was stored")
			}
			if _, err := store.CreateNextVersion(t.Context(), Artifact{ID: "valid", SessionID: "session", Name: "result"}); err != nil {
				t.Fatal(err)
			}
			got, err := store.Get(t.Context(), "valid")
			if err != nil || got.Version != 1 {
				t.Fatalf("failed write consumed version: %+v %v", got, err)
			}
		})
	}
}
