package runtime

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type storedEventPages struct{ pages int }

func (s *storedEventPages) ReadEvents(_ context.Context, scope, runID string, after int64, limit int) ([]StoredEvent, error) {
	s.pages++
	if s.pages == 1 && after == 0 {
		return []StoredEvent{{ID: "run/1", Scope: scope, RunID: runID, Sequence: 1, Type: EventRunStarted, Version: EventVersionV1}}, nil
	}
	if s.pages == 2 && after == 1 {
		return []StoredEvent{{ID: "run/2", Scope: scope, RunID: runID, Sequence: 2, Type: EventRunFinished, Version: EventVersionV1}}, nil
	}
	return nil, errors.New("unexpected event cursor")
}

func TestStoredSSEFollowRetainsIDsFromHistoryIntoLaterFacts(t *testing.T) {
	recorder := httptest.NewRecorder()
	sink, err := NewSSEEventSink(recorder)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := &storedEventPages{}
	err = FollowEvents(ctx, store, "scope", "run", 0, time.Millisecond, func(event StoredEvent) error {
		if err := sink.EmitStored(ctx, event); err != nil {
			return err
		}
		if event.Sequence == 2 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected subscription termination: %v", err)
	}
	body := recorder.Body.String()
	if strings.Count(body, "id: run/1") != 1 || strings.Count(body, "id: run/2") != 1 || strings.Index(body, "id: run/1") > strings.Index(body, "id: run/2") || store.pages != 2 {
		t.Fatalf("durable IDs lost or replayed: pages=%d body=%q", store.pages, body)
	}
}

func TestStoredSSERejectsUncommittedIdentity(t *testing.T) {
	recorder := httptest.NewRecorder()
	sink, err := NewSSEEventSink(recorder)
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if err := sink.EmitStored(t.Context(), StoredEvent{Type: EventLLMChunk}); err == nil || recorder.Body.Len() != 0 {
		t.Fatalf("temporary output acquired durable identity: body=%q error=%v", recorder.Body.String(), err)
	}
}
