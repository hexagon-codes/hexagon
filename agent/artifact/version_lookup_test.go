package artifact

import (
	"context"
	"errors"
	"testing"
)

type lookupFailureStore struct {
	Store
	lookupError error
	writes      int
}

func (s *lookupFailureStore) GetLatest(ctx context.Context, sessionID, name string) (*Artifact, error) {
	if s.lookupError != nil {
		return nil, s.lookupError
	}
	return s.Store.GetLatest(ctx, sessionID, name)
}

func (s *lookupFailureStore) Save(ctx context.Context, art Artifact) (string, error) {
	s.writes++
	return s.Store.Save(ctx, art)
}

func TestServiceSaveLookupFailureKeepsOriginalVersion(t *testing.T) {
	store := &lookupFailureStore{Store: NewMemoryStore()}
	service := NewService(store)
	id, err := service.Save(t.Context(), Artifact{SessionID: "session", Name: "result", Data: []byte("original")})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("lookup unavailable")
	store.lookupError = failure
	if _, err := service.Save(t.Context(), Artifact{SessionID: "session", Name: "result", Data: []byte("replacement")}); !errors.Is(err, failure) {
		t.Fatalf("lookup failure lost: %v", err)
	}
	got, err := store.Get(t.Context(), id)
	if err != nil || string(got.Data) != "original" || got.Version != 1 || store.writes != 1 {
		t.Fatalf("failed lookup wrote a replacement: artifact=%+v writes=%d error=%v", got, store.writes, err)
	}
	store.lookupError = nil
	nextID, err := service.Save(t.Context(), Artifact{SessionID: "session", Name: "result", Data: []byte("next")})
	if err != nil || nextID == id {
		t.Fatalf("recovered lookup failed to allocate next version: id=%q error=%v", nextID, err)
	}
}
