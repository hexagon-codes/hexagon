package synthesizer

import (
	"context"
	"strings"
	"testing"

	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/hexagon/testing/mock"
)

// BUG-20260618-F1: CompactAndRefineSynthesizer.Synthesize 构建了 contents 切片但从不读取
// （SA4010：append 结果未使用），仅用 totalLength 决策 compact/refine。删除死代码不得改变
// 这一"按总长度选策略"的行为。本测试钉死该不变量（修复前后均应 GREEN，守护死代码移除）。
func TestBUG_20260618_F1_CompactAndRefine_StrategyByTotalLength(t *testing.T) {
	s := NewCompactAndRefineSynthesizer(
		WithCompactAndRefineLLM(mock.FixedProvider("answer")),
		WithCompactAndRefineMaxContext(50),
	)
	ctx := context.Background()

	// 总长度 <= 50 → compact
	resp, err := s.Synthesize(ctx, "q", []rag.Document{{Content: "short"}})
	if err != nil {
		t.Fatalf("compact 路径出错: %v", err)
	}
	if got := resp.Metadata["actual_strategy"]; got != "compact" {
		t.Fatalf("小文档应走 compact，实际 %v", got)
	}

	// 总长度 > 50 → refine
	resp, err = s.Synthesize(ctx, "q", []rag.Document{
		{Content: strings.Repeat("x", 60)},
		{Content: strings.Repeat("y", 60)},
	})
	if err != nil {
		t.Fatalf("refine 路径出错: %v", err)
	}
	if got := resp.Metadata["actual_strategy"]; got != "refine" {
		t.Fatalf("大文档应走 refine，实际 %v", got)
	}
}
