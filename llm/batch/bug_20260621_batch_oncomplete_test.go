package batch

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// BUG-20260621-batch-oncomplete
//
// Bug: processBatch delivered each request's response (`pending.response <- resp`
// in processRequest) — which unblocks the BatchSubmit caller — BEFORE calling
// OnBatchComplete. So BatchSubmit could return with OnBatchComplete not yet invoked.
// The audit test TestCallbacks_Invoked asserts OnBatchComplete >= 1 right after
// BatchSubmit returns and flaked ~20% of runs ("OnBatchComplete 未被调用").
//
// Contract being locked: BatchSubmit must not return until the full batch lifecycle
// — including OnBatchComplete — has fired for every batch it produced. This makes
// OnBatchComplete consistent with OnRequestComplete/OnError, which are already
// invoked before the response is delivered.
//
// The bug is a happens-before race; a single run reproduces it only ~20% of the
// time. Looping makes the RED deterministic: under the buggy ordering at least one
// iteration observes OnBatchComplete missing at BatchSubmit return; the fixed
// ordering passes every iteration.
func TestBatchSubmit_OnBatchCompleteFiresBeforeReturn_BUG20260621(t *testing.T) {
	const iterations = 300

	for i := range iterations {
		var batchStart, batchComplete int64

		cfg := fastConfig()
		cfg.OnBatchStart = func(string, int) { atomic.AddInt64(&batchStart, 1) }
		cfg.OnBatchComplete = func(string, int, time.Duration) { atomic.AddInt64(&batchComplete, 1) }

		p := &stubProvider{fn: func(_ context.Context, req *Request) (*Response, error) {
			return &Response{ID: req.ID, TokensUsed: 1}, nil
		}}

		b := NewBatcher(p, cfg)
		b.Start()
		b.BatchSubmit(context.Background(), []*Request{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}})

		// Snapshot at the moment BatchSubmit returns — BEFORE Stop(), which would
		// otherwise mask the race by waiting for all workers to drain.
		gotStart := atomic.LoadInt64(&batchStart)
		gotComplete := atomic.LoadInt64(&batchComplete)
		b.Stop()

		if gotComplete < 1 {
			t.Fatalf("BUG-20260621 iter %d: OnBatchComplete NOT invoked by the time BatchSubmit returned (OnBatchStart=%d, OnBatchComplete=%d)",
				i, gotStart, gotComplete)
		}
		// Every started batch must also have completed before BatchSubmit returned.
		if gotStart != gotComplete {
			t.Fatalf("BUG-20260621 iter %d: at BatchSubmit return OnBatchStart=%d != OnBatchComplete=%d (a batch started but completion callback hadn't fired)",
				i, gotStart, gotComplete)
		}
	}
}
