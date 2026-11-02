// Package evaluate 提供 AI Agent 系统的评估框架
package evaluate

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Runner 评估运行器
type Runner struct {
	config     *EvalConfig
	evaluators []Evaluator
}

// NewRunner 创建评估运行器
func NewRunner(config *EvalConfig) *Runner {
	if config == nil {
		config = DefaultEvalConfig()
	}
	return &Runner{
		config:     config,
		evaluators: config.Evaluators,
	}
}

// AddEvaluator 添加评估器
func (r *Runner) AddEvaluator(evaluator Evaluator) *Runner {
	r.evaluators = append(r.evaluators, evaluator)
	return r
}

// AddEvaluators 批量添加评估器
func (r *Runner) AddEvaluators(evaluators ...Evaluator) *Runner {
	r.evaluators = append(r.evaluators, evaluators...)
	return r
}

// SystemUnderTest 被测系统接口
type SystemUnderTest interface {
	// Run 运行系统并返回响应
	Run(ctx context.Context, query string) (*SystemResponse, error)
}

// SystemResponse 系统响应
type SystemResponse struct {
	Response string
	Context  []string
	Timing   *TimingInfo
	Cost     *CostInfo
	Metadata map[string]any
}

// SystemFunc 函数式被测系统
type SystemFunc func(ctx context.Context, query string) (*SystemResponse, error)

// Run 实现 SystemUnderTest 接口
func (f SystemFunc) Run(ctx context.Context, query string) (*SystemResponse, error) {
	return f(ctx, query)
}

// EvaluateDataset 评估整个数据集
func (r *Runner) EvaluateDataset(ctx context.Context, dataset *Dataset, system SystemUnderTest) (*EvalReport, error) {
	if len(r.evaluators) == 0 {
		return nil, fmt.Errorf("no evaluators configured")
	}

	report := &EvalReport{
		Name:         fmt.Sprintf("Evaluation of %s", dataset.Name),
		Dataset:      dataset.Name,
		StartTime:    time.Now(),
		TotalSamples: len(dataset.Samples),
		Summary:      make(map[string]*MetricSummary),
		Results:      make([]SampleResult, 0, len(dataset.Samples)),
	}

	// 初始化指标汇总
	for _, eval := range r.evaluators {
		report.Summary[eval.Name()] = &MetricSummary{
			Name:         eval.Name(),
			Min:          1.0,
			Max:          0.0,
			Distribution: make(map[string]int),
		}
	}

	// 创建工作队列
	type workItem struct {
		index  int
		sample Sample
	}

	workCh := make(chan workItem, len(dataset.Samples))
	resultCh := make(chan SampleResult, len(dataset.Samples))

	// 填充工作队列
	for i, sample := range dataset.Samples {
		workCh <- workItem{index: i, sample: sample}
	}
	close(workCh)

	// 启动工作协程
	var wg sync.WaitGroup
	for i := 0; i < r.config.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for work := range workCh {
				result := r.evaluateSample(ctx, work.sample, system)
				resultCh <- result
			}
		}()
	}

	// 等待完成并关闭结果通道
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// 收集结果
	for result := range resultCh {
		report.Results = append(report.Results, result)

		failed := result.Error != ""
		for _, evaluation := range result.Results {
			if evaluation == nil || evaluation.Error != "" {
				failed = true
				break
			}
		}
		if failed {
			report.FailedSamples++
		} else {
			report.SuccessSamples++
		}

		// 更新汇总统计
		for name, evalResult := range result.Results {
			if evalResult == nil {
				continue
			}
			if summary, ok := report.Summary[name]; ok {
				summary.Count++
				summary.Mean += evalResult.Score

				if evalResult.Score < summary.Min {
					summary.Min = evalResult.Score
				}
				if evalResult.Score > summary.Max {
					summary.Max = evalResult.Score
				}

				// 更新分布
				level := string(GetScoreLevel(evalResult.Score))
				summary.Distribution[level]++

				// 更新通过率
				if evalResult.Error == "" && evalResult.Passed != nil && *evalResult.Passed {
					if summary.PassRate == nil {
						passRate := 0.0
						summary.PassRate = &passRate
					}
					*summary.PassRate++
				}
			}
		}
	}

	// 计算最终统计
	for _, summary := range report.Summary {
		if summary.Count > 0 {
			summary.Mean /= float64(summary.Count)
			if summary.PassRate != nil {
				*summary.PassRate /= float64(summary.Count)
			}
		}

		// 计算中位数（简化：使用均值近似）
		summary.Median = summary.Mean

		// 计算标准差（需要二次遍历，这里简化处理）
		// 实际应该在收集结果时记录所有分数
	}

	report.EndTime = time.Now()
	report.Duration = report.EndTime.Sub(report.StartTime)

	return report, nil
}

// evaluateSample 评估单个样本
func (r *Runner) evaluateSample(ctx context.Context, sample Sample, system SystemUnderTest) SampleResult {
	start := time.Now()
	result := SampleResult{
		SampleID: sample.ID,
		Query:    sample.Query,
		Results:  make(map[string]*EvalResult),
	}

	// 创建超时上下文
	evalCtx := ctx
	if r.config.Timeout > 0 {
		var cancel context.CancelFunc
		evalCtx, cancel = context.WithTimeout(ctx, r.config.Timeout)
		defer cancel()
	}

	// 运行被测系统
	sysResp, err := system.Run(evalCtx, sample.Query)
	if err != nil {
		result.Error = fmt.Sprintf("system error: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	if sysResp == nil {
		result.Error = "system returned no response"
		result.Duration = time.Since(start)
		return result
	}

	result.Response = sysResp.Response

	// 构建评估输入
	input := EvalInput{
		Query:     sample.Query,
		Response:  sysResp.Response,
		Context:   sysResp.Context,
		Reference: sample.Reference,
		Metadata:  sample.Metadata,
		Timing:    sysResp.Timing,
		Cost:      sysResp.Cost,
	}

	// 如果系统没有提供上下文但样本有，使用样本的
	if len(input.Context) == 0 && len(sample.Context) > 0 {
		input.Context = sample.Context
	}

	// 运行所有评估器
	for _, eval := range r.evaluators {
		evalResult, err := eval.Evaluate(evalCtx, input)
		result.Results[eval.Name()] = normalizeEvalResult(eval.Name(), evalResult, err)
	}

	result.Duration = time.Since(start)
	return result
}

// EvaluateSingle 评估单个输入
func (r *Runner) EvaluateSingle(ctx context.Context, input EvalInput) (map[string]*EvalResult, error) {
	results := make(map[string]*EvalResult)

	for _, eval := range r.evaluators {
		result, err := eval.Evaluate(ctx, input)
		results[eval.Name()] = normalizeEvalResult(eval.Name(), result, err)
	}

	return results, nil
}

// normalizeEvalResult 保留部分评分事实，并将空返回或执行错误显式记入同一结果。
func normalizeEvalResult(name string, result *EvalResult, err error) *EvalResult {
	if err == nil && result != nil {
		return result
	}
	failed := EvalResult{Name: name}
	if result != nil {
		failed = *result
		if failed.Name == "" {
			failed.Name = name
		}
	}
	if err == nil {
		failed.Error = "evaluator returned no result"
	} else if failed.Error == "" {
		failed.Error = err.Error()
	} else {
		failed.Error += "; " + err.Error()
	}
	return &failed
}

// EvaluateBatch 批量评估
func (r *Runner) EvaluateBatch(ctx context.Context, inputs []EvalInput) ([]map[string]*EvalResult, error) {
	results := make([]map[string]*EvalResult, len(inputs))
	resultCh := make(chan struct {
		index  int
		result map[string]*EvalResult
	}, len(inputs))

	// 创建工作队列
	workCh := make(chan struct {
		index int
		input EvalInput
	}, len(inputs))

	for i, input := range inputs {
		workCh <- struct {
			index int
			input EvalInput
		}{index: i, input: input}
	}
	close(workCh)

	// 启动工作协程
	var wg sync.WaitGroup
	for i := 0; i < r.config.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for work := range workCh {
				result, _ := r.EvaluateSingle(ctx, work.input)
				resultCh <- struct {
					index  int
					result map[string]*EvalResult
				}{index: work.index, result: result}
			}
		}()
	}

	// 等待完成
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// 收集结果
	for res := range resultCh {
		results[res.index] = res.result
	}

	return results, nil
}

// QuickEval 快速评估（使用所有评估器评估单个输入）
func QuickEval(ctx context.Context, evaluators []Evaluator, input EvalInput) (map[string]*EvalResult, error) {
	runner := NewRunner(&EvalConfig{
		Evaluators:  evaluators,
		Concurrency: len(evaluators),
	})
	return runner.EvaluateSingle(ctx, input)
}
