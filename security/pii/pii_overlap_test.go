package pii

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// rawDigitRun matches any run of 4+ digits — a fragment long enough to be a
// meaningful piece of leaked card/account PII.
var rawDigitRun = regexp.MustCompile(`\d{4,}`)

// TestAnonymize_OverlappingSpansLeakRawFragment proves that two PARTIALLY
// overlapping detections (same start, different end) combined with the
// back-to-front replacement using STALE byte offsets splice a raw digit run
// from the original value back into the redacted output.
//
// Spans: phone 5..15 over "4012888888" and credit_card 5..21 over the full
// "4012888888881881". dedup only merges IDENTICAL {start,end}, so both survive;
// Anonymize then rewrites with offsets that no longer line up after the first
// replacement, leaking raw middle digits ("8888").
func TestAnonymize_OverlappingSpansLeakRawFragment(t *testing.T) {
	text := "card 4012888888881881 end"
	cardStart := strings.Index(text, "4012888888881881")
	if cardStart < 0 {
		t.Fatal("setup: card not found")
	}

	// Order matters for the stale-offset bug: process the wider span first so the
	// narrower span's End offset points into already-rewritten bytes.
	entities := []*PIIEntity{
		{Type: PIITypeCreditCard, Value: text[cardStart : cardStart+16], Start: cardStart, End: cardStart + 16, Score: 0.8},
		{Type: PIITypePhone, Value: text[cardStart : cardStart+10], Start: cardStart, End: cardStart + 10, Score: 0.85},
	}

	a := NewAnonymizer()
	out := a.Anonymize(text, entities)

	// "8888" is a middle fragment of the card that BOTH overlapping spans cover;
	// it must be fully masked. Its survival is the stale-offset splice leak (the
	// preserved first-4/last-4 are an intentional MaskStrategy choice, not a leak).
	if strings.Contains(out, "8888") {
		t.Fatalf("RAW PII LEAK: output %q still contains raw card middle fragment %q", out, "8888")
	}
	// Defense in depth: the masked region must be contiguous, never re-splicing
	// raw digits between mask chars. After a correct merge the only digit runs
	// left are the deliberately preserved edges.
	for _, run := range rawDigitRun.FindAllString(out, -1) {
		if run != "4012" && run != "1881" {
			t.Fatalf("RAW PII LEAK: unexpected raw digit run %q in output %q", run, out)
		}
	}
}

// TestProcess_OverlappingDetectorsNoRawDigitLeak runs the FULL pipeline (real
// detector + default anonymizer). The detector emits a us_phone span over the
// first 10 digits AND a credit_card span over all 16; both pass the default
// MinScore. The leak is nondeterministic (map/sort ordering), so iterate to
// surface it reliably — any single leaked fragment fails the test.
func TestProcess_OverlappingDetectorsNoRawDigitLeak(t *testing.T) {
	// 4012888888881881 is a Luhn-valid test card (passes validateCreditCard).
	text := "card 4012888888881881 end"
	p := NewProcessor()

	for i := 0; i < 256; i++ {
		res, err := p.Process(context.Background(), text)
		if err != nil {
			t.Fatalf("Process error: %v", err)
		}
		if !res.HasPII {
			t.Fatalf("expected PII detected, entities=%+v", res.Entities)
		}
		// No middle fragment of the card may survive. The card's distinctive
		// middle run "8888" must never appear raw in the output.
		if strings.Contains(res.AnonymizedText, "8888") {
			t.Fatalf("RAW PII LEAK on iter %d: anonymized %q contains raw card fragment %q",
				i, res.AnonymizedText, "8888")
		}
	}
}
