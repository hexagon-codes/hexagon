package pii

import "testing"

// TestDedup_RefillsValueAfterPartialOverlapMerge 证明：当两个**部分重叠且互不包含**的
// 区间被 dedup 合并后，结果实体的 Value 会被按合并端点从原文重新截取，与 [Start,End)
// 对齐——而不是沿用 winner 的旧 Value（旧实现下 Value 比合并区间短，暴露错位子串）。
//
// 修复前：合并区间 [2,9] 但 Value 仍为 winner 的 "EFGHI"（5 字节 ≠ 7 字节）→ FAIL。
// 修复后：Value 回填为 text[2:9]="CDEFGHI" → PASS。
func TestDedup_RefillsValueAfterPartialOverlapMerge(t *testing.T) {
	text := "ABCDEFGHIJ"
	d := &RegexDetector{}

	// a=[2,6]"CDEF" 与 b=[4,9]"EFGHI" 部分重叠，互不包含；b 更宽，胜出。
	entities := []*PIIEntity{
		{Type: PIITypeEmail, Value: text[2:6], Start: 2, End: 6, Score: 0.7},
		{Type: PIITypePhone, Value: text[4:9], Start: 4, End: 9, Score: 0.9},
	}

	out := d.dedup(text, entities)
	if len(out) != 1 {
		t.Fatalf("partial-overlap entities must merge to 1, got %d", len(out))
	}
	got := out[0]
	if got.Start != 2 || got.End != 9 {
		t.Fatalf("merged span = [%d,%d], want [2,9]", got.Start, got.End)
	}
	want := text[got.Start:got.End]
	if got.Value != want {
		t.Fatalf("Value=%q not aligned with [Start,End)=%q (stale-value leak)", got.Value, want)
	}
}

// TestDedup_ValueConsistencyInvariant 对每个 dedup 返回实体强制不变量
// Value == text[Start:End]，覆盖嵌套包含 + 部分重叠 + 不相交三种关系混合的情形。
func TestDedup_ValueConsistencyInvariant(t *testing.T) {
	text := "0123456789ABCDEFGHIJ"
	d := &RegexDetector{}
	entities := []*PIIEntity{
		{Type: PIITypeCreditCard, Value: text[0:6], Start: 0, End: 6, Score: 0.8}, // 含
		{Type: PIITypePhone, Value: text[2:4], Start: 2, End: 4, Score: 0.9},      // 被前者包含
		{Type: PIITypeEmail, Value: text[8:14], Start: 8, End: 14, Score: 0.6},    // 部分重叠
		{Type: PIITypeIDCard, Value: text[12:18], Start: 12, End: 18, Score: 0.7}, // 部分重叠
	}
	for _, e := range d.dedup(text, entities) {
		if e.Start < 0 || e.End > len(text) || e.Start >= e.End {
			t.Fatalf("invalid span [%d,%d]", e.Start, e.End)
		}
		if e.Value != text[e.Start:e.End] {
			t.Fatalf("invariant broken: Value=%q != text[%d:%d]=%q", e.Value, e.Start, e.End, text[e.Start:e.End])
		}
	}
}
