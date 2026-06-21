package pii

import (
	"strings"
	"testing"
)

// FuzzAnonymize_NeverPanicsAndNonOverlapping fuzzes Anonymize with arbitrary text
// and arbitrary (possibly out-of-bounds / overlapping / out-of-order / negative)
// spans. Invariants that must hold for ANY input:
//
//	I1. Anonymize never panics (the normalizeSpans bounds filter must clamp/drop
//	    every illegal span before any text[Start:End] slice).
//	I2. normalizeSpans output is strictly ascending and non-overlapping.
//	I3. Idempotence-ish: re-running Anonymize over already-normalized spans is safe.
//
// This is the property-based (method 1) closed loop for the overlap-merge fix.
func FuzzAnonymize_NeverPanicsAndNonOverlapping(f *testing.F) {
	f.Add("card 4012888888881881 end", 5, 21, 5, 15)
	f.Add("", -3, 99, 0, 0)
	f.Add("abc", 2, 2, 1, 3)
	f.Add("phone 13800138000 here", 6, 17, 6, 11)

	f.Fuzz(func(t *testing.T, text string, s1, e1, s2, e2 int) {
		entities := []*PIIEntity{
			{Type: PIITypePhone, Start: s1, End: e1, Score: 0.7},
			{Type: PIITypeCreditCard, Start: s2, End: e2, Score: 0.9},
		}

		// I1: must not panic for any span values.
		out := NewAnonymizer().Anonymize(text, entities)

		// I2: normalized spans must be ascending + non-overlapping + in-bounds.
		spans := normalizeSpans(text, entities)
		prevEnd := -1
		for _, sp := range spans {
			if sp.Start < 0 || sp.End > len(text) || sp.Start >= sp.End {
				t.Fatalf("normalizeSpans yielded out-of-bounds span [%d,%d] for len=%d", sp.Start, sp.End, len(text))
			}
			if sp.Start < prevEnd {
				t.Fatalf("overlapping spans after normalize: start %d < prevEnd %d", sp.Start, prevEnd)
			}
			if sp.Value != text[sp.Start:sp.End] {
				t.Fatalf("span Value %q misaligned with [%d,%d]", sp.Value, sp.Start, sp.End)
			}
			prevEnd = sp.End
		}

		// I3: re-anonymizing the output must also not panic.
		_ = NewAnonymizer().Anonymize(out, entities)
		// sanity: output never shorter than the un-spanned remainder isn't asserted
		// (masks may be longer/shorter); we only require structural safety above.
		_ = strings.Count(out, "")
	})
}
