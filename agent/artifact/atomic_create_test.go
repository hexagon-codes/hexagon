package artifact

import (
	"errors"
	"sync"
	"testing"
)

// 两个服务共享同一存储时，显式 ID 的创建仍只有一个写入者成功。
func TestAtomicCreateAcrossServices(t *testing.T) {
	store := NewMemoryStore()
	a, b := NewService(store, WithFailOnConflict()), NewService(store, WithFailOnConflict())
	start := make(chan struct{})
	errorsOut := make(chan error, 2)
	var done sync.WaitGroup
	for _, service := range []*Service{a, b} {
		done.Add(1)
		go func() {
			defer done.Done()
			<-start
			_, err := service.Save(t.Context(), Artifact{ID: "fixed", SessionID: "s", Name: "clip", Data: []byte("media")})
			errorsOut <- err
		}()
	}
	close(start)
	done.Wait()
	close(errorsOut)
	success, conflicts := 0, 0
	for err := range errorsOut {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrArtifactConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
	got, err := store.Get(t.Context(), "fixed")
	if err != nil || got.Version != 1 || string(got.Data) != "media" {
		t.Fatalf("stored=%v error=%v", got, err)
	}
}
