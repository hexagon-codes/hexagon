package reranker

import (
	"context"
	"testing"

	"github.com/hexagon-codes/hexagon/rag"
)

// TestRRF_FuseRankings 验证多列表融合：在多个列表都靠前的文档 RRF 分数最高，
// 且按文档 ID 去重。
func TestRRF_FuseRankings(t *testing.T) {
	r := NewRRFReranker(WithRRFK(60), WithRRFTopK(10))

	r1 := []rag.Document{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	r2 := []rag.Document{{ID: "a"}, {ID: "c"}, {ID: "d"}}

	out := r.FuseRankings(r1, r2)

	// a 在两个列表都排第一 → RRF 最高
	if len(out) == 0 || out[0].ID != "a" {
		t.Fatalf("融合后首位应为 a, got %v", ids(out))
	}
	// 去重：a/b/c/d 共 4 个唯一文档
	if len(out) != 4 {
		t.Errorf("应去重为 4 个文档, got %d (%v)", len(out), ids(out))
	}
	// 分数应已写回（降序）
	for i := 1; i < len(out); i++ {
		if out[i-1].Score < out[i].Score {
			t.Errorf("结果应按 RRF 分数降序, 位置 %d 违反", i)
		}
	}
}

// TestRRF_SkipNoID 验证无 ID 文档被跳过，避免错误合并。
func TestRRF_SkipNoID(t *testing.T) {
	r := NewRRFReranker()
	out := r.FuseRankings([]rag.Document{{ID: ""}, {ID: "x"}, {ID: ""}})
	if len(out) != 1 || out[0].ID != "x" {
		t.Errorf("应只保留有 ID 的文档 x, got %v", ids(out))
	}
}

// TestRRF_TopK 验证 TopK 截断。
func TestRRF_TopK(t *testing.T) {
	r := NewRRFReranker(WithRRFTopK(2))
	out := r.FuseRankings([]rag.Document{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}})
	if len(out) != 2 {
		t.Errorf("TopK=2 应返回 2 个, got %d", len(out))
	}
}

// TestRRF_Rerank 验证单列表 Rerank：按原分数排序后写入 RRF 分数并截断。
func TestRRF_Rerank(t *testing.T) {
	r := NewRRFReranker(WithRRFK(60), WithRRFTopK(2))
	docs := []rag.Document{
		{ID: "low", Score: 0.1},
		{ID: "high", Score: 0.9},
		{ID: "mid", Score: 0.5},
	}
	out, err := r.Rerank(context.Background(), "q", docs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("TopK=2 应返回 2 个, got %d", len(out))
	}
	// 原分数最高的 high 排第一
	if out[0].ID != "high" {
		t.Errorf("Rerank 首位应为原分数最高的 high, got %s", out[0].ID)
	}
	// RRF 分数 = 1/(k+rank)，rank1 → 1/61
	if want := float32(1.0 / 61.0); out[0].Score != want {
		t.Errorf("首位 RRF 分数 = %v, want %v", out[0].Score, want)
	}
}

// TestRRF_Empty 验证空输入的边界处理。
func TestRRF_Empty(t *testing.T) {
	r := NewRRFReranker()
	if out, _ := r.Rerank(context.Background(), "q", nil); len(out) != 0 {
		t.Error("空文档 Rerank 应返回空")
	}
	if out := r.FuseRankings(); out != nil {
		t.Error("无排名列表 FuseRankings 应返回 nil")
	}
}

func ids(docs []rag.Document) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.ID
	}
	return out
}
