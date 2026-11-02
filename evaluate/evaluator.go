// Package evaluate 提供 AI Agent 系统的评估框架
//
// Evaluate 用于评估 Agent、RAG、LLM 等系统的质量：
//   - Evaluator: 评估器接口
//   - Dataset: 评估数据集
//   - Runner: 评估运行器
//   - Reporter: 评估报告生成器
//
// 内置评估指标：
//   - Relevance: 检索相关性
//   - Faithfulness: 回答忠实度
//   - Correctness: 回答正确性
//   - Latency: 延迟指标
//   - Cost: 成本指标
package evaluate

import (
	"context"
	"encoding/json"
	"time"
)

// Evaluator 是评估器接口
type Evaluator interface {
	// Name 返回一次执行中唯一的评估器身份，由 Runner 在执行前固定。
	Name() string

	// Description 返回评估器描述
	Description() string

	// Evaluate 执行评估
	Evaluate(ctx context.Context, input EvalInput) (*EvalResult, error)

	// RequiresLLM 返回是否需要 LLM 进行评估
	RequiresLLM() bool
}

// EvalInput 评估输入
type EvalInput struct {
	// Query 用户查询
	Query string `json:"query"`

	// Response 系统响应
	Response string `json:"response"`

	// Context 检索上下文（RAG 场景）
	Context []string `json:"context,omitempty"`

	// Reference 参考答案（用于正确性评估）
	Reference string `json:"reference,omitempty"`

	// Metadata 额外元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// Timing 时间信息
	Timing *TimingInfo `json:"timing,omitempty"`

	// Cost 成本信息
	Cost *CostInfo `json:"cost,omitempty"`
}

// TimingInfo 时间信息
type TimingInfo struct {
	StartTime    time.Time     `json:"start_time"`
	EndTime      time.Time     `json:"end_time"`
	Duration     time.Duration `json:"duration"`
	TTFBDuration time.Duration `json:"ttfb_duration,omitempty"` // Time To First Byte
}

// CostInfo 成本信息
type CostInfo struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	Cost         float64 `json:"cost"` // 美元
}

// EvalResult 评估结果
type EvalResult struct {
	// Name 评估器名称
	Name string `json:"name"`

	// Score 评分（0-1）
	Score float64 `json:"score"`

	// Passed 是否通过（可选）
	Passed *bool `json:"passed,omitempty"`

	// Reason 评分理由
	Reason string `json:"reason,omitempty"`

	// Details 详细信息
	Details map[string]any `json:"details,omitempty"`

	// SubScores 子分数（用于复合指标）
	SubScores map[string]float64 `json:"sub_scores,omitempty"`

	// Error 错误信息
	Error string `json:"error,omitempty"`

	// Duration 评估耗时
	Duration time.Duration `json:"duration"`
}

// Dataset 评估数据集
type Dataset struct {
	// Name 数据集名称
	Name string `json:"name"`

	// Description 数据集描述
	Description string `json:"description,omitempty"`

	// Samples 样本列表
	Samples []Sample `json:"samples"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"created_at"`
}

// Sample 评估样本
type Sample struct {
	// ID 样本 ID
	ID string `json:"id"`

	// Query 查询
	Query string `json:"query"`

	// Reference 参考答案（可选）
	Reference string `json:"reference,omitempty"`

	// Context 参考上下文（可选）
	Context []string `json:"context,omitempty"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// Tags 标签
	Tags []string `json:"tags,omitempty"`
}

// DatasetBuilder 数据集构建器
type DatasetBuilder struct {
	dataset Dataset
}

// NewDatasetBuilder 创建数据集构建器
func NewDatasetBuilder(name string) *DatasetBuilder {
	return &DatasetBuilder{
		dataset: Dataset{
			Name:      name,
			CreatedAt: time.Now(),
		},
	}
}

// WithDescription 设置描述
func (b *DatasetBuilder) WithDescription(desc string) *DatasetBuilder {
	b.dataset.Description = desc
	return b
}

// WithMetadata 设置元数据
func (b *DatasetBuilder) WithMetadata(metadata map[string]any) *DatasetBuilder {
	b.dataset.Metadata = metadata
	return b
}

// AddSample 添加样本
func (b *DatasetBuilder) AddSample(sample Sample) *DatasetBuilder {
	b.dataset.Samples = append(b.dataset.Samples, sample)
	return b
}

// AddSamples 批量添加样本
func (b *DatasetBuilder) AddSamples(samples []Sample) *DatasetBuilder {
	b.dataset.Samples = append(b.dataset.Samples, samples...)
	return b
}

// Build 构建数据集
func (b *DatasetBuilder) Build() *Dataset {
	return &b.dataset
}

// EvalConfig 评估配置
type EvalConfig struct {
	// Evaluators 要使用的评估器列表
	Evaluators []Evaluator

	// Concurrency 并发数
	Concurrency int

	// Timeout 单次评估超时
	Timeout time.Duration

	// StopOnError 遇到错误是否停止
	StopOnError bool

	// Verbose 是否输出详细日志
	Verbose bool
}

// DefaultEvalConfig 默认评估配置
func DefaultEvalConfig() *EvalConfig {
	return &EvalConfig{
		Concurrency: 5,
		Timeout:     30 * time.Second,
		StopOnError: false,
		Verbose:     false,
	}
}

// EvalReport 评估报告
type EvalReport struct {
	// Name 报告名称
	Name string `json:"name"`

	// Dataset 数据集名称
	Dataset string `json:"dataset"`

	// StartTime 开始时间
	StartTime time.Time `json:"start_time"`

	// EndTime 结束时间
	EndTime time.Time `json:"end_time"`

	// Duration 总耗时
	Duration time.Duration `json:"duration"`

	// TotalSamples 样本总数
	TotalSamples int `json:"total_samples"`

	// SuccessSamples 系统及评估器均无执行错误的样本数，不表示达到质量阈值。
	SuccessSamples int `json:"success_samples"`

	// FailedSamples 系统或评估器存在执行错误的样本数。
	FailedSamples int `json:"failed_samples"`

	// Summary 汇总统计
	Summary map[string]*MetricSummary `json:"summary"`

	// Results 详细结果
	Results []SampleResult `json:"results,omitempty"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// MetricSummary 指标汇总
type MetricSummary struct {
	// Name 指标名称
	Name string `json:"name"`

	// Mean 平均分
	Mean float64 `json:"mean"`

	// Min 最小分
	Min float64 `json:"min"`

	// Max 最大分
	Max float64 `json:"max"`

	// Median 中位数
	Median float64 `json:"median"`

	// StdDev 标准差
	StdDev float64 `json:"std_dev"`

	// PassRate 通过率（如适用）
	PassRate *float64 `json:"pass_rate,omitempty"`

	// Distribution 分数分布
	Distribution map[string]int `json:"distribution,omitempty"`

	// Count 样本数
	Count int `json:"count"`
}

// SampleResult 样本评估结果
type SampleResult struct {
	// SampleID 样本 ID
	SampleID string `json:"sample_id"`

	// Query 查询
	Query string `json:"query"`

	// Response 响应
	Response string `json:"response,omitempty"`

	// Results 各评估器的结果
	Results map[string]*EvalResult `json:"results"`

	// Error 错误信息
	Error string `json:"error,omitempty"`

	// Duration 耗时
	Duration time.Duration `json:"duration"`
}

// ToJSON 转换为 JSON
func (r *EvalReport) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// LLMJudge LLM 评判接口
// 用于需要 LLM 进行评估的场景
type LLMJudge interface {
	// Judge 使用 LLM 进行评判
	Judge(ctx context.Context, prompt string) (string, error)
}

// LLMJudgeFunc 函数式 LLM 评判
type LLMJudgeFunc func(ctx context.Context, prompt string) (string, error)

// Judge 实现 LLMJudge 接口
func (f LLMJudgeFunc) Judge(ctx context.Context, prompt string) (string, error) {
	return f(ctx, prompt)
}

// ScoreLevel 分数等级
type ScoreLevel string

const (
	ScoreLevelExcellent ScoreLevel = "excellent" // 0.8-1.0
	ScoreLevelGood      ScoreLevel = "good"      // 0.6-0.8
	ScoreLevelFair      ScoreLevel = "fair"      // 0.4-0.6
	ScoreLevelPoor      ScoreLevel = "poor"      // 0.2-0.4
	ScoreLevelBad       ScoreLevel = "bad"       // 0.0-0.2
)

// GetScoreLevel 获取分数等级
func GetScoreLevel(score float64) ScoreLevel {
	switch {
	case score >= 0.8:
		return ScoreLevelExcellent
	case score >= 0.6:
		return ScoreLevelGood
	case score >= 0.4:
		return ScoreLevelFair
	case score >= 0.2:
		return ScoreLevelPoor
	default:
		return ScoreLevelBad
	}
}

// Thresholds 评估阈值
type Thresholds struct {
	Relevance    float64 `json:"relevance"`
	Faithfulness float64 `json:"faithfulness"`
	Correctness  float64 `json:"correctness"`
	Latency      float64 `json:"latency"` // 毫秒
	Cost         float64 `json:"cost"`    // 美元
}

// DefaultThresholds 默认阈值
func DefaultThresholds() *Thresholds {
	return &Thresholds{
		Relevance:    0.7,
		Faithfulness: 0.7,
		Correctness:  0.7,
		Latency:      5000, // 5秒
		Cost:         0.01, // 1分钱
	}
}
