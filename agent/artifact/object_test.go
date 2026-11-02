package artifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type testDiskObjects struct {
	dir  string
	puts int
}

func (s *testDiskObjects) Put(_ context.Context, key string, data []byte, _ string) error {
	s.puts++
	return os.WriteFile(filepath.Join(s.dir, key), data, 0600)
}
func (s *testDiskObjects) Get(_ context.Context, key string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrObjectNotFound
	}
	return data, err
}

type failRegistrationStore struct {
	*MemoryStore
	fail bool
}

func (s *failRegistrationStore) CreateNextVersion(ctx context.Context, art Artifact) (string, error) {
	if s.fail {
		return "", errors.New("registration unavailable")
	}
	return s.MemoryStore.CreateNextVersion(ctx, art)
}

// 对象字节已经落盘后重建服务，只补登记原对象，不再次 Put 或创建新版本。
func TestRegisterOriginalObjectAfterRegistrationFailure(t *testing.T) {
	objects := &testDiskObjects{dir: t.TempDir()}
	store := &failRegistrationStore{MemoryStore: NewMemoryStore(), fail: true}
	service := NewService(store)
	art := Artifact{ID: "artifact-1", SessionID: "session", Name: "clip", ObjectKey: "stable-object", OperationID: "operation-1", Data: []byte("original media bytes")}
	_, err := service.SaveObject(t.Context(), objects, art)
	var pending *PendingRegistrationError
	if !errors.As(err, &pending) || objects.puts != 1 {
		t.Fatalf("expected stored bytes with pending registration: %v puts=%d", err, objects.puts)
	}
	store.fail = false
	restarted := NewService(store)
	if _, err := restarted.RegisterObject(t.Context(), objects, pending.Artifact); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.RegisterObject(t.Context(), objects, pending.Artifact); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(t.Context(), art.ID)
	if err != nil || got.Version != 1 || got.Digest != contentDigest(art.Data) || got.ObjectKey != "stable-object" || objects.puts != 1 || len(got.Data) != 0 {
		t.Fatalf("registered=%+v err=%v puts=%d", got, err, objects.puts)
	}
	tampered := pending.Artifact
	tampered.Digest = "different"
	if _, err := restarted.RegisterObject(t.Context(), objects, tampered); !errors.Is(err, ErrObjectDigest) {
		t.Fatalf("wrong bytes accepted: %v", err)
	}
}
