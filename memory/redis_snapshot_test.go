package memory

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/hexagon-codes/hexagon/memory/store"
	"github.com/redis/go-redis/v9"
)

func TestSnapshotRedisConditionalWrites(t *testing.T) {
	socket := os.Getenv("HEXAGON_MEMORY_REDIS_SOCKET")
	if socket == "" {
		t.Skip("dedicated local Redis socket is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clients := []*redis.Client{redis.NewClient(&redis.Options{Network: "unix", Addr: socket}), redis.NewClient(&redis.Options{Network: "unix", Addr: socket})}
	for _, client := range clients {
		t.Cleanup(func() { _ = client.Close() })
		if err := client.Ping(ctx).Err(); err != nil {
			t.Fatal(err)
		}
	}
	a := scopedFixture(t, store.NewRedisStore(clients[0], store.WithRedisPrefix("memory-cas-test:")), "synthetic")
	b := scopedFixture(t, store.NewRedisStore(clients[1], store.WithRedisPrefix("memory-cas-test:")), "synthetic")
	record, err := a.Save(ctx, recordFixture(a.Scope(), "entry", "Original"), 0)
	if err != nil {
		t.Fatal(err)
	}
	old, err := b.Get(ctx, "entry")
	if err != nil {
		t.Fatal(err)
	}
	record.Entry.Content = "Updated"
	if _, err := a.Save(ctx, record, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Save(ctx, old, 1); !errors.Is(err, store.ErrRevisionConflict) {
		t.Fatalf("cross-client stale revision overwrote update: %v", err)
	}
	gate := make(chan struct{})
	results := make(chan error, 2)
	for _, client := range []*ScopedMemory{a, b} {
		go func(m *ScopedMemory) { <-gate; _, err := m.Save(ctx, record, 2); results <- err }(client)
	}
	close(gate)
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, store.ErrRevisionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("conditional write outcomes: successes=%d conflicts=%d", successes, conflicts)
	}
	latest, err := a.Get(ctx, "entry")
	if err != nil || latest.Revision != 3 {
		t.Fatalf("persisted revision: %#v, %v", latest, err)
	}
}
