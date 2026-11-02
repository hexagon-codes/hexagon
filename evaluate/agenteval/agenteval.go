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
	"sort"

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
	// Undetermined 保留关键维度缺少显式通过判断的样本，不折算为分数或通过。
	Undetermined []string
}

// BaselineCriteria 由宿主冻结普通均分阈值与必须逐案例通过的评估器名称。
// 不根据名称、分数或评估器类型推断严重程度。
type BaselineCriteria struct {
	MinScore           float64
	CriticalEvaluators []string
}

// QualityBaseline 用一组评估器在参考数据集上评测 Agent，并按 minScore 判定每个指标是否达标。
//
// 任一指标平均分低于 minScore 或任一执行/评估错误均使基线不通过。
// 非关键维度仍按一般分数阈值判断；逐案例关键规则由带 Criteria 的入口显式指定。
func QualityBaseline(
	ctx context.Context,
	a Runnable,
	dataset *evaluate.Dataset,
	minScore float64,
	evaluators ...evaluate.Evaluator,
) (*BaselineResult, error) {
	return QualityBaselineWithCriteria(ctx, a, dataset, BaselineCriteria{MinScore: minScore}, evaluators...)
}

// QualityBaselineWithCriteria 在相同评估报告上检查均分、执行错误及显式关键维度。
// 关键结果缺失/错误/Passed=false 为失败；Passed=nil 记录未判定且整体不能通过。
// 规则判断不额外调用被测系统或模型，返回报告保留逐案例依据。
func QualityBaselineWithCriteria(ctx context.Context, a Runnable, dataset *evaluate.Dataset, criteria BaselineCriteria, evaluators ...evaluate.Evaluator) (*BaselineResult, error) {
	if len(evaluators) == 0 {
		return nil, fmt.Errorf("agenteval: 至少需要一个评估器")
	}
	if dataset == nil || len(dataset.Samples) == 0 {
		return nil, fmt.Errorf("agenteval: 数据集为空，无法建立质量基线")
	}
	critical := make(map[string]struct{}, len(criteria.CriticalEvaluators))
	for _, name := range criteria.CriticalEvaluators {
		critical[name] = struct{}{}
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
	if err := ctx.Err(); err != nil {
		res.Failures = append(res.Failures, "evaluation canceled: "+err.Error())
	}
	if len(report.Results) != len(dataset.Samples) {
		res.Failures = append(res.Failures, "evaluation results are incomplete")
	}
	for _, sample := range report.Results {
		if sample.Error != "" {
			res.Failures = append(res.Failures, fmt.Sprintf("sample %q: %s", sample.SampleID, sample.Error))
			continue
		}
		for name, result := range sample.Results {
			if result != nil && result.Error != "" {
				res.Failures = append(res.Failures, fmt.Sprintf("sample %q evaluator %q: %s", sample.SampleID, name, result.Error))
			}
		}
		for name := range critical {
			result, exists := sample.Results[name]
			if !exists || result == nil {
				res.Failures = append(res.Failures, fmt.Sprintf("sample %q critical evaluator %q: result is missing", sample.SampleID, name))
				continue
			}
			if result.Error != "" {
				continue
			}
			if result.Passed == nil {
				res.Undetermined = append(res.Undetermined, fmt.Sprintf("sample %q critical evaluator %q: pass verdict is undetermined", sample.SampleID, name))
				continue
			}
			if !*result.Passed {
				message := fmt.Sprintf("sample %q critical evaluator %q: explicit pass verdict is false", sample.SampleID, name)
				if result.Reason != "" {
					message += ": " + result.Reason
				}
				res.Failures = append(res.Failures, message)
			}
		}
	}
	for name, summary := range report.Summary {
		if summary.Mean < criteria.MinScore {
			res.Failures = append(res.Failures,
				fmt.Sprintf("%s: mean %.3f < baseline %.3f", name, summary.Mean, criteria.MinScore))
		}
	}
	sort.Strings(res.Failures)
	sort.Strings(res.Undetermined)
	res.Passed = len(res.Failures) == 0 && len(res.Undetermined) == 0
	return res, nil
}
