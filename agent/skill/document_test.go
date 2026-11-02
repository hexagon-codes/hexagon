package skill

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/hexagon-codes/toolkit/util/hash"
)

type observedSource struct {
	DirectorySource
	reads atomic.Int32
}

func (s *observedSource) Read(ctx context.Context, ref DocumentReference, name string) ([]byte, error) {
	s.reads.Add(1)
	return s.DirectorySource.Read(ctx, ref, name)
}

func documentFixture(t *testing.T, base, root, scope, body string, resources map[string]string) *Skill {
	t.Helper()
	files := map[string]string{"SKILL.md": body}
	for name, content := range resources {
		files[name] = content
	}
	digests := make(map[string]string, len(files))
	for name, content := range files {
		file := filepath.Join(base, root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		digests[name] = hash.SHA256(content)
	}
	ref, err := NewDocumentReference("local", root, "SKILL.md", digests)
	if err != nil {
		t.Fatal(err)
	}
	return &Skill{Name: "guide", Description: "A document guide", Version: root, Kind: KindDocument, Scope: scope, Document: &ref}
}

func TestDocumentCatalogLoadsOnlySelectedRevisionAndResources(t *testing.T) {
	base := t.TempDir()
	r := NewRegistry()
	source := &observedSource{DirectorySource: DirectorySource{Directory: base}}
	if err := r.RegisterSource("local", source); err != nil {
		t.Fatal(err)
	}
	definition := documentFixture(t, base, "v1", "", "Original instructions", map[string]string{"references/example.txt": "Original resource", "scripts/setup.sh": "exit 71\n"})
	if err := r.Register(definition); err != nil {
		t.Fatal(err)
	}
	if catalog := r.Catalog(""); len(catalog) != 1 || catalog[0].Kind != KindDocument {
		t.Fatalf("catalog: %#v", catalog)
	}
	if source.reads.Load() != 0 {
		t.Fatal("catalog eagerly read content")
	}
	selected, err := r.Select(context.Background(), "", "guide")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Content() != "Original instructions" || source.reads.Load() != 1 {
		t.Fatal("selection did not load only its body")
	}
	resource, err := selected.ReadResource(context.Background(), "references/example.txt")
	if err != nil || string(resource) != "Original resource" {
		t.Fatalf("resource: %s, %v", resource, err)
	}
	if _, err := selected.ReadResource(context.Background(), "scripts/setup.sh"); err != nil {
		t.Fatal(err)
	}
	if _, err := selected.Execute(context.Background(), nil); !errors.Is(err, ErrDocumentNotExecutable) {
		t.Fatalf("document became executable: %v", err)
	}
	if _, err := r.Execute(context.Background(), "guide", nil); !errors.Is(err, ErrDocumentNotExecutable) {
		t.Fatal(err)
	}
	if source.reads.Load() != 3 {
		t.Fatalf("unexpected resource reads: %d", source.reads.Load())
	}
}

func TestDocumentRestoreUsesOriginalVersionAfterDirectoryUpdate(t *testing.T) {
	base := t.TempDir()
	r := NewRegistry()
	if err := r.RegisterSource("local", DirectorySource{Directory: base}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(documentFixture(t, base, "v1", "", "Original instructions", map[string]string{"sample": "old"})); err != nil {
		t.Fatal(err)
	}
	selected, err := r.Select(context.Background(), "", "guide")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := json.Marshal(selected.Reference())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Replace(documentFixture(t, base, "v2", "", "Updated instructions", map[string]string{"sample": "new"})); err != nil {
		t.Fatal(err)
	}
	current, err := r.Select(context.Background(), "", "guide")
	if err != nil {
		t.Fatal(err)
	}
	if current.Content() != "Updated instructions" || selected.Content() != "Original instructions" {
		t.Fatal("definition update changed an in-flight selection")
	}
	selection, err := DecodeSelection(saved)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewRegistry()
	if err := restarted.RegisterSource("local", DirectorySource{Directory: base}); err != nil {
		t.Fatal(err)
	}
	restored, err := restarted.Restore(context.Background(), "", selection)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := restored.ReadResource(context.Background(), "sample")
	if err != nil || restored.Content() != "Original instructions" || string(resource) != "old" {
		t.Fatalf("restore drifted: %s, %s, %v", restored.Content(), resource, err)
	}
	if err := os.WriteFile(filepath.Join(base, "v1", "SKILL.md"), []byte("overwritten"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Restore(context.Background(), "", selection); !errors.Is(err, ErrRevisionUnavailable) {
		t.Fatalf("overwritten revision silently accepted: %v", err)
	}
}

func TestDocumentScopeSelectionAndDecodeFailure(t *testing.T) {
	base := t.TempDir()
	r := NewRegistry()
	if err := r.RegisterSource("local", DirectorySource{Directory: base}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"", "team-a", "team-b"} {
		root := scope
		if root == "" {
			root = "global"
		}
		if err := r.Register(documentFixture(t, base, root, scope, root, nil)); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []string{"", "team-a", "team-b", "unregistered"} {
		want := scope
		if want == "" || want == "unregistered" {
			want = "global"
		}
		catalog := r.Catalog(scope)
		if len(catalog) != 1 {
			t.Fatalf("scope %s leaked catalog: %#v", scope, catalog)
		}
		selected, err := r.Select(context.Background(), scope, "guide")
		if err != nil || selected.Content() != want {
			t.Fatalf("scope %s: %v", scope, err)
		}
	}
	selected, err := r.Select(context.Background(), "team-a", "guide")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Restore(context.Background(), "team-b", selected.Reference()); !errors.Is(err, ErrScopeMismatch) {
		t.Fatal(err)
	}
	for _, bad := range []string{"{", `{}`, `{"metadata":{"name":"guide","kind":"unknown","revision":"x"}}`} {
		if _, err := DecodeSelection([]byte(bad)); err == nil {
			t.Fatalf("invalid selection decoded: %s", bad)
		}
	}
	invalid := documentFixture(t, base, "bad-text", "", string([]byte{0xff, 0xfe}), nil)
	invalid.Name = "invalid"
	if err := r.Register(invalid); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Select(context.Background(), "", "invalid"); err == nil {
		t.Fatal("invalid document encoding accepted")
	}
	global := documentFixture(t, base, "global-key", "", "Shared definition", nil)
	global.Name = "team\x00guide"
	local := documentFixture(t, base, "local-key", "team", "Local definition", nil)
	if err := r.Register(global); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(local); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(global.Name); err != nil {
		t.Fatalf("scope/name key collision: %v", err)
	}
}

func TestHandlerSelectionKeepsVersionAndDefinitionSnapshot(t *testing.T) {
	r := NewRegistry()
	definition := &Skill{Name: "handler", Version: "v1", Schema: map[string]any{"nested": map[string]any{"value": "original"}}, Handler: func(context.Context, map[string]any) (map[string]any, error) {
		return map[string]any{"version": "v1"}, nil
	}}
	if err := r.Register(definition); err != nil {
		t.Fatal(err)
	}
	definition.Schema["nested"].(map[string]any)["value"] = "mutated"
	got, err := r.Get("handler")
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema["nested"].(map[string]any)["value"] != "original" {
		t.Fatal("registry retained mutable caller schema")
	}
	selected, err := r.Select(context.Background(), "", "handler")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Replace(&Skill{Name: "handler", Version: "v2", Handler: func(context.Context, map[string]any) (map[string]any, error) {
		return map[string]any{"version": "v2"}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	restored, err := r.Restore(context.Background(), "", selected.Reference())
	if err != nil {
		t.Fatal(err)
	}
	result, err := restored.Execute(context.Background(), nil)
	if err != nil || result["version"] != "v1" {
		t.Fatalf("handler revision drifted: %#v, %v", result, err)
	}
}
