package pii

import (
	"fmt"
	"testing"
)

// TestDifferential_DedupVsNormalizeSpans is the differential (method 5) closed loop
// for the overlap-merge refactor. dedup (Detect path) and normalizeSpans (Anonymize
// path) now share mergeOverlapping, so for the SAME in-bounds entity set they MUST
// produce an identical sequence of (Start, End, Value) spans. Any divergence means
// the two paths would mask differently and re-open the partial-overlap leak.
func TestDifferential_DedupVsNormalizeSpans(t *testing.T) {
	text := "0123456789ABCDEFGHIJKLMNOPQRST"
	d := &RegexDetector{}

	cases := [][]*PIIEntity{
		// nested containment
		{{Start: 0, End: 8, Score: 0.8}, {Start: 2, End: 5, Score: 0.9}},
		// partial overlap, neither contains the other
		{{Start: 2, End: 10, Score: 0.7}, {Start: 6, End: 14, Score: 0.6}},
		// disjoint
		{{Start: 0, End: 4, Score: 0.5}, {Start: 10, End: 16, Score: 0.5}},
		// triple chain overlap
		{{Start: 1, End: 6, Score: 0.5}, {Start: 5, End: 12, Score: 0.5}, {Start: 11, End: 20, Score: 0.5}},
		// identical span, different score
		{{Start: 3, End: 9, Score: 0.4}, {Start: 3, End: 9, Score: 0.95}},
	}

	for i, ents := range cases {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			for _, e := range ents {
				e.Type = PIITypePhone
				e.Value = text[e.Start:e.End]
			}
			// fresh copies for each path (both mutate/refill Value)
			dedupOut := d.dedup(text, cloneEntities(ents))
			normOut := normalizeSpans(text, cloneEntities(ents))

			if len(dedupOut) != len(normOut) {
				t.Fatalf("span count differs: dedup=%d normalize=%d", len(dedupOut), len(normOut))
			}
			for k := range dedupOut {
				a, b := dedupOut[k], normOut[k]
				if a.Start != b.Start || a.End != b.End || a.Value != b.Value {
					t.Fatalf("span %d differs: dedup{%d,%d,%q} vs normalize{%d,%d,%q}",
						k, a.Start, a.End, a.Value, b.Start, b.End, b.Value)
				}
			}
		})
	}
}

func cloneEntities(in []*PIIEntity) []*PIIEntity {
	out := make([]*PIIEntity, len(in))
	for i, e := range in {
		cp := *e
		out[i] = &cp
	}
	return out
}
