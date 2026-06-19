// Package agenteval 提供 Agent 的质量评估基线（非性能基准）。
//
// 它把任意 Agent 适配为评估框架的被测系统，在参考数据集上用一组评估器（含
// LLM-as-judge）打分，并按最低分阈值判定每个质量维度是否达标——为 Agent 建立可
// 重复运行、可回归的质量闭环（区别于 bench/ 的性能基准）。
package agenteval

import (
	"context"
	"fmt"
	"maps"

	"github.com/hexagon-codes/hexagon/agent"
	"github.com/hexagon-codes/hexagon/evaluate"
)

// Runnable 是质量评估只需的最小 Agent 能力：执行一次查询。
//
// agent.Agent 天然满足该接口；按需窄化依赖（只要 Run），便于评测任意可执行体。
type Runnable interface {
	Run(ctx context.Context, input agent.Input) (agent.Output, error)
}

// AgentSystem 把 Runnable 适配为 evaluate.SystemUnderTest。
type AgentSystem struct {
	agent Runnable
}

// NewAgentSystem 创建 Agent 被测系统适配器。
func NewAgentSystem(a Runnable) *AgentSystem {
	return &AgentSystem{agent: a}
}

// Run 执行查询并把 Agent 输出转为评估框架的系统响应。
func (s *AgentSystem) Run(ctx context.Context, query string) (*evaluate.SystemResponse, error) {
	out, err := s.agent.Run(ctx, agent.Input{Query: query})
	if err != nil {
		return nil, err
	}
	meta := map[string]any{
		"prompt_tokens":     out.Usage.PromptTokens,
		"completion_tokens": out.Usage.CompletionTokens,
		"total_tokens":      out.Usage.TotalTokens,
		"tool_calls":        len(out.ToolCalls),
	}
	maps.Copy(meta, out.Metadata)
	return &evaluate.SystemResponse{
		Response: out.Content,
		Metadata: meta,
	}, nil
}

// 编译期断言：AgentSystem 满足 SystemUnderTest。
var _ evaluate.SystemUnderTest = (*AgentSystem)(nil)

// BaselineResult 是 Agent 质量基线评测结果。
type BaselineResult struct {
	// Report 完整评估报告
	Report *evaluate.EvalReport
	// Passed 是否所有质量维度均达到基线
	Passed bool
	// Failures 未达标维度的说明（指标名 + 实际均分 + 基线阈值）
	Failures []string
}

// QualityBaseline 用一组评估器在参考数据集上评测 Agent，并按 minScore 判定每个指标是否达标。
//
// 任一指标平均分低于 minScore 即基线不通过（Failures 列出未达标项）。这是 Agent
// 的质量基线：把它纳入 CI/回归即可在 Agent 行为退化时被挡下，而非只看性能。
func QualityBaseline(
	ctx context.Context,
	a Runnable,
	dataset *evaluate.Dataset,
	minScore float64,
	evaluators ...evaluate.Evaluator,
) (*BaselineResult, error) {
	if len(evaluators) == 0 {
		return nil, fmt.Errorf("agenteval: 至少需要一个评估器")
	}
	if dataset == nil || len(dataset.Samples) == 0 {
		return nil, fmt.Errorf("agenteval: 数据集为空，无法建立质量基线")
	}

	runner := evaluate.NewRunner(evaluate.DefaultEvalConfig()).AddEvaluators(evaluators...)
	report, err := runner.EvaluateDataset(ctx, dataset, NewAgentSystem(a))
	if err != nil {
		return nil, err
	}

	// 空 summary（无任何指标）不应真空判通过——视为基线未成立。
	if len(report.Summary) == 0 {
		return &BaselineResult{
			Report:   report,
			Passed:   false,
			Failures: []string{"无任何评估指标产出，基线未成立"},
		}, nil
	}

	res := &BaselineResult{Report: report, Passed: true}
	for name, summary := range report.Summary {
		if summary.Mean < minScore {
			res.Passed = false
			res.Failures = append(res.Failures,
				fmt.Sprintf("%s: 平均分 %.3f < 基线 %.3f", name, summary.Mean, minScore))
		}
	}
	return res, nil
}
