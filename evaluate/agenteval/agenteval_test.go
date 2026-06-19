package agenteval

import (
	"context"
	"strings"
	"testing"

	"github.com/hexagon-codes/hexagon/agent"
	"github.com/hexagon-codes/hexagon/evaluate"
)

// mockAgent 是只实现 Run 的最小 Agent 桩：回声预设回复（按 query 取）。
type mockAgent struct {
	replies map[string]string
}

func (m *mockAgent) Run(_ context.Context, input agent.Input) (agent.Output, error) {
	return agent.Output{Content: m.replies[input.Query]}, nil
}

// containsEvaluator 是一个简单评估器：响应包含参考答案得 1 分，否则 0 分。
func containsEvaluator() evaluate.Evaluator {
	return evaluate.NewCustomEvaluator("contains", "响应是否包含参考答案",
		func(_ context.Context, in evaluate.EvalInput) (*evaluate.EvalResult, error) {
			score := 0.0
			if in.Reference == "" || strings.Contains(in.Response, in.Reference) {
				score = 1.0
			}
			return &evaluate.EvalResult{Name: "contains", Score: score}, nil
		})
}

func sampleDataset() *evaluate.Dataset {
	return evaluate.NewDatasetBuilder("baseline").
		AddSample(evaluate.Sample{ID: "1", Query: "首都", Reference: "北京"}).
		AddSample(evaluate.Sample{ID: "2", Query: "语言", Reference: "Go"}).
		Build()
}

// TestQualityBaseline_Pass 验证 Agent 回复都包含参考答案时基线通过。
func TestQualityBaseline_Pass(t *testing.T) {
	a := &mockAgent{replies: map[string]string{
		"首都": "中国的首都是北京",
		"语言": "这是 Go 语言",
	}}
	res, err := QualityBaseline(context.Background(), a, sampleDataset(), 0.9, containsEvaluator())
	if err != nil {
		t.Fatalf("QualityBaseline: %v", err)
	}
	if !res.Passed {
		t.Errorf("应通过基线, failures=%v", res.Failures)
	}
}

// TestQualityBaseline_Fail 验证有回复不含参考答案时基线不通过并列出未达标维度。
func TestQualityBaseline_Fail(t *testing.T) {
	a := &mockAgent{replies: map[string]string{
		"首都": "我不知道",
		"语言": "这是 Go 语言",
	}}
	res, err := QualityBaseline(context.Background(), a, sampleDataset(), 0.9, containsEvaluator())
	if err != nil {
		t.Fatalf("QualityBaseline: %v", err)
	}
	if res.Passed {
		t.Error("一半样本不含参考答案，不应通过基线")
	}
	if len(res.Failures) == 0 {
		t.Error("应列出未达标维度")
	}
}

// TestBugRev0618_3_QualityBaselineEmptyDataset 回归锁定 [BUG-REV0618-3]：
// 空数据集报错而非真空判通过（此前空 Summary → 循环不跑 → Passed 真空为 true）。
func TestBugRev0618_3_QualityBaselineEmptyDataset(t *testing.T) {
	a := &mockAgent{}
	empty := evaluate.NewDatasetBuilder("empty").Build()
	if _, err := QualityBaseline(context.Background(), a, empty, 0.5, containsEvaluator()); err == nil {
		t.Error("空数据集应报错，而非真空判通过")
	}
	if _, err := QualityBaseline(context.Background(), a, nil, 0.5, containsEvaluator()); err == nil {
		t.Error("nil 数据集应报错")
	}
}

// TestQualityBaseline_NoEvaluators 验证未提供评估器时报错。
func TestQualityBaseline_NoEvaluators(t *testing.T) {
	a := &mockAgent{}
	if _, err := QualityBaseline(context.Background(), a, sampleDataset(), 0.5); err == nil {
		t.Error("无评估器应报错")
	}
}

// TestAgentSystem_Adapts 验证适配器把 Agent 输出转为系统响应。
func TestAgentSystem_Adapts(t *testing.T) {
	a := &mockAgent{replies: map[string]string{"q": "答案"}}
	resp, err := NewAgentSystem(a).Run(context.Background(), "q")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Response != "答案" {
		t.Errorf("Response = %q, want 答案", resp.Response)
	}
}
