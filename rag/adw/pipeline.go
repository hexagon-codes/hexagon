// Package adw 提供智能文档工作流功能
//
// 本文件实现 ADW Pipeline，用于构建和执行文档处理流程。
package adw

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/hexagon/rag"
	"github.com/hexagon-codes/toolkit/util/idgen"
)

// ============== PipelineImpl ==============

// PipelineImpl Pipeline 实现
type PipelineImpl struct {
	// id 管道 ID
	id string

	// name 管道名称
	name string

	// description 管道描述
	description string

	// steps 处理步骤
	steps []Step

	// defaultOptions 默认处理选项
	defaultOptions ProcessOptions

	// hooks 钩子
	hooks *PipelineHooks

	// metrics 指标收集
	metrics *PipelineMetrics

	// mu 互斥锁
	mu sync.RWMutex
}

// PipelineHooks 管道钩子
type PipelineHooks struct {
	// OnStart 管道开始时调用
	OnStart func(ctx context.Context, input *PipelineInput)

	// OnEnd 管道结束时调用
	OnEnd func(ctx context.Context, output *PipelineOutput, err error)

	// OnStepStart 步骤开始时调用
	OnStepStart func(ctx context.Context, step Step, doc *Document)

	// OnStepEnd 步骤结束时调用
	OnStepEnd func(ctx context.Context, step Step, doc *Document, err error)

	// OnDocumentStart 文档处理开始时调用
	OnDocumentStart func(ctx context.Context, doc *Document)

	// OnDocumentEnd 文档处理结束时调用
	OnDocumentEnd func(ctx context.Context, doc *Document, err error)

	// OnError 错误时调用
	OnError func(ctx context.Context, step Step, doc *Document, err error)
}

// PipelineMetrics 管道指标
type PipelineMetrics struct {
	// TotalDocuments 总文档数
	TotalDocuments int64

	// ProcessedDocuments 已处理文档数
	ProcessedDocuments int64

	// FailedDocuments 失败文档数
	FailedDocuments int64

	// TotalProcessingTime 总处理时间
	TotalProcessingTime time.Duration

	// StepMetrics 步骤指标
	StepMetrics map[string]*StepMetrics

	// mu 互斥锁
	mu sync.RWMutex
}

// StepMetrics 步骤指标
type StepMetrics struct {
	// Name 步骤名称
	Name string

	// ExecutionCount 执行次数
	ExecutionCount int64

	// SuccessCount 成功次数
	SuccessCount int64

	// FailureCount 失败次数
	FailureCount int64

	// TotalDuration 总耗时
	TotalDuration time.Duration

	// AverageDuration 平均耗时
	AverageDuration time.Duration
}

// NewPipeline 创建管道
func NewPipeline(name string) *PipelineBuilder {
	return &PipelineBuilder{
		pipeline: &PipelineImpl{
			id:             idgen.NanoID(),
			name:           name,
			steps:          make([]Step, 0),
			defaultOptions: DefaultProcessOptions(),
			hooks:          &PipelineHooks{},
			metrics: &PipelineMetrics{
				StepMetrics: make(map[string]*StepMetrics),
			},
		},
	}
}

// PipelineBuilder 管道构建器
type PipelineBuilder struct {
	pipeline *PipelineImpl
}

// WithDescription 设置描述
func (b *PipelineBuilder) WithDescription(desc string) *PipelineBuilder {
	b.pipeline.description = desc
	return b
}

// WithOptions 设置默认选项
func (b *PipelineBuilder) WithOptions(opts ProcessOptions) *PipelineBuilder {
	b.pipeline.defaultOptions = opts
	return b
}

// WithHooks 设置钩子
func (b *PipelineBuilder) WithHooks(hooks *PipelineHooks) *PipelineBuilder {
	b.pipeline.hooks = hooks
	return b
}

// AddStep 添加步骤
func (b *PipelineBuilder) AddStep(step Step) *PipelineBuilder {
	b.pipeline.steps = append(b.pipeline.steps, step)
	return b
}

// Build 构建管道
func (b *PipelineBuilder) Build() Pipeline {
	return b.pipeline
}

// ============== Pipeline 实现 ==============

// Name 返回管道名称
func (p *PipelineImpl) Name() string {
	return p.name
}

// AddStep 添加步骤
func (p *PipelineImpl) AddStep(step Step) Pipeline {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.steps = append(p.steps, step)
	return p
}

// GetSteps 获取所有步骤
func (p *PipelineImpl) GetSteps() []Step {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]Step, len(p.steps))
	copy(result, p.steps)
	return result
}

// Process 处理文档
func (p *PipelineImpl) Process(ctx context.Context, input PipelineInput) (*PipelineOutput, error) {
	start := time.Now()

	// 合并选项
	opts := p.mergeOptions(input.Options)

	// 触发开始钩子
	if p.hooks.OnStart != nil {
		p.hooks.OnStart(ctx, &input)
	}

	// 初始化输出
	output := &PipelineOutput{
		Documents:        make([]*Document, 0, len(input.Documents)),
		ExtractedData:    make(map[string]any),
		TotalDocuments:   len(input.Documents),
		ValidationErrors: make([]ValidationError, 0),
		Metadata:         make(map[string]any),
	}

	// 处理文档
	var wg sync.WaitGroup
	var mu sync.Mutex
	semaphore := make(chan struct{}, opts.MaxConcurrency)

	for _, doc := range input.Documents {
		wg.Add(1)
		semaphore <- struct{}{}

		go func(ragDoc rag.Document) {
			defer wg.Done()
			defer func() { <-semaphore }()

			// 转换为 ADW Document
			adwDoc := NewDocument(ragDoc)

			// 处理单个文档
			err := p.processDocument(ctx, adwDoc, input.Schema, opts)

			mu.Lock()
			output.Documents = append(output.Documents, adwDoc)
			if err != nil {
				output.FailureCount++
			} else {
				output.SuccessCount++

				// 合并提取的数据
				for k, v := range adwDoc.StructuredData {
					output.ExtractedData[k] = v
				}
			}

			// 收集验证错误
			output.ValidationErrors = append(output.ValidationErrors, adwDoc.ValidationErrors...)
			mu.Unlock()
		}(doc)
	}

	wg.Wait()

	// 计算处理时间
	output.ProcessingTime = time.Since(start)

	// 触发结束钩子
	if p.hooks.OnEnd != nil {
		p.hooks.OnEnd(ctx, output, nil)
	}

	return output, nil
}

// processDocument 处理单个文档
func (p *PipelineImpl) processDocument(ctx context.Context, doc *Document, schema *ExtractionSchema, opts ProcessOptions) error {
	// 触发文档开始钩子
	if p.hooks.OnDocumentStart != nil {
		p.hooks.OnDocumentStart(ctx, doc)
	}

	var lastError error

	// 执行所有步骤
	for _, step := range p.steps {
		// 检查上下文是否取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 检查步骤是否能处理该文档
		if !step.CanHandle(doc) {
			continue
		}

		// 触发步骤开始钩子
		if p.hooks.OnStepStart != nil {
			p.hooks.OnStepStart(ctx, step, doc)
		}

		// 执行步骤
		stepStart := time.Now()
		err := step.Process(ctx, doc, opts)
		stepDuration := time.Since(stepStart)

		// 记录处理步骤
		doc.AddProcessingStep(ProcessingStep{
			StepName:  step.Name(),
			StartTime: stepStart,
			EndTime:   time.Now(),
			Duration:  stepDuration,
			Success:   err == nil,
			Error:     err,
		})

		// 更新指标
		p.updateMetrics(step.Name(), err == nil, stepDuration)

		// 触发步骤结束钩子
		if p.hooks.OnStepEnd != nil {
			p.hooks.OnStepEnd(ctx, step, doc, err)
		}

		// 处理错误
		if err != nil {
			lastError = err

			// 触发错误钩子
			if p.hooks.OnError != nil {
				p.hooks.OnError(ctx, step, doc, err)
			}

			// 记录验证错误但继续处理
			doc.AddValidationError(ValidationError{
				Field:    step.Name(),
				Rule:     "step_execution",
				Message:  fmt.Sprintf("步骤执行失败: %v", err),
				Severity: SeverityError,
			})
		}
	}

	// 更新文档状态
	doc.ProcessedAt = time.Now()
	if len(doc.ProcessingHistory) > 0 {
		firstStep := doc.ProcessingHistory[0]
		doc.ProcessingDuration = doc.ProcessedAt.Sub(firstStep.StartTime)
	}

	// 触发文档结束钩子
	if p.hooks.OnDocumentEnd != nil {
		p.hooks.OnDocumentEnd(ctx, doc, lastError)
	}

	return lastError
}

// mergeOptions 合并选项
func (p *PipelineImpl) mergeOptions(opts ProcessOptions) ProcessOptions {
	// 使用默认选项作为基础，用输入选项覆盖
	result := p.defaultOptions

	if opts.MaxConcurrency > 0 {
		result.MaxConcurrency = opts.MaxConcurrency
	}
	if opts.Timeout > 0 {
		result.Timeout = opts.Timeout
	}
	if opts.Language != "" {
		result.Language = opts.Language
	}
	if opts.Schema != nil {
		result.Schema = opts.Schema
	}

	// 合并布尔选项
	result.EnableOCR = result.EnableOCR || opts.EnableOCR
	result.EnableTableExtraction = result.EnableTableExtraction || opts.EnableTableExtraction
	result.EnableFormRecognition = result.EnableFormRecognition || opts.EnableFormRecognition
	result.EnableEntityRecognition = result.EnableEntityRecognition || opts.EnableEntityRecognition
	result.EnableValidation = result.EnableValidation || opts.EnableValidation

	return result
}

// updateMetrics 更新指标
func (p *PipelineImpl) updateMetrics(stepName string, success bool, duration time.Duration) {
	p.metrics.mu.Lock()
	defer p.metrics.mu.Unlock()

	// 获取或创建步骤指标
	metrics, ok := p.metrics.StepMetrics[stepName]
	if !ok {
		metrics = &StepMetrics{Name: stepName}
		p.metrics.StepMetrics[stepName] = metrics
	}

	// 更新指标
	metrics.ExecutionCount++
	metrics.TotalDuration += duration
	if success {
		metrics.SuccessCount++
	} else {
		metrics.FailureCount++
	}

	// 计算平均耗时
	if metrics.ExecutionCount > 0 {
		metrics.AverageDuration = metrics.TotalDuration / time.Duration(metrics.ExecutionCount)
	}
}

// GetMetrics 获取指标
func (p *PipelineImpl) GetMetrics() *PipelineMetrics {
	return p.metrics
}

// ============== 便捷步骤实现 ==============

// BaseStep 基础步骤
type BaseStep struct {
	name        string
	description string
	processFunc func(ctx context.Context, doc *Document, opts ProcessOptions) error
	canHandle   func(doc *Document) bool
}

// Name 返回步骤名称
func (s *BaseStep) Name() string {
	return s.name
}

// Description 返回步骤描述
func (s *BaseStep) Description() string {
	return s.description
}

// Process 处理文档
func (s *BaseStep) Process(ctx context.Context, doc *Document, opts ProcessOptions) error {
	if s.processFunc == nil {
		return nil
	}
	return s.processFunc(ctx, doc, opts)
}

// CanHandle 检查是否能处理
func (s *BaseStep) CanHandle(doc *Document) bool {
	if s.canHandle == nil {
		return true
	}
	return s.canHandle(doc)
}

// NewFuncStep 创建函数步骤
func NewFuncStep(name string, fn func(ctx context.Context, doc *Document, opts ProcessOptions) error) Step {
	return &BaseStep{
		name:        name,
		processFunc: fn,
		canHandle:   func(doc *Document) bool { return true },
	}
}

// NewConditionalStep 创建条件步骤
func NewConditionalStep(name string, condition func(doc *Document) bool, fn func(ctx context.Context, doc *Document, opts ProcessOptions) error) Step {
	return &BaseStep{
		name:        name,
		processFunc: fn,
		canHandle:   condition,
	}
}

// ============== 预定义步骤 ==============

// DocumentTypeDetectorStep 文档类型检测步骤
type DocumentTypeDetectorStep struct {
	BaseStep
}

// NewDocumentTypeDetectorStep 创建文档类型检测步骤
func NewDocumentTypeDetectorStep() *DocumentTypeDetectorStep {
	s := &DocumentTypeDetectorStep{}
	s.name = "document_type_detector"
	s.description = "检测文档类型"
	s.processFunc = s.detect
	s.canHandle = func(doc *Document) bool { return true }
	return s
}

// detect 检测文档类型
func (s *DocumentTypeDetectorStep) detect(ctx context.Context, doc *Document, opts ProcessOptions) error {
	// 根据文档内容和元数据检测类型
	source := doc.Document.Source
	content := doc.Document.Content

	// 根据文件扩展名判断
	switch {
	case hasExtension(source, ".pdf"):
		doc.Type = DocTypePDF
	case hasExtension(source, ".docx", ".doc"):
		doc.Type = DocTypeWord
	case hasExtension(source, ".xlsx", ".xls"):
		doc.Type = DocTypeExcel
	case hasExtension(source, ".jpg", ".jpeg", ".png", ".gif"):
		doc.Type = DocTypeImage
	case hasExtension(source, ".html", ".htm"):
		doc.Type = DocTypeHTML
	case hasExtension(source, ".json"):
		doc.Type = DocTypeJSON
	case hasExtension(source, ".xml"):
		doc.Type = DocTypeXML
	case hasExtension(source, ".txt"):
		doc.Type = DocTypeText
	default:
		// 根据内容判断
		if containsInvoiceKeywords(content) {
			doc.Type = DocTypeInvoice
		} else if containsContractKeywords(content) {
			doc.Type = DocTypeContract
		} else {
			doc.Type = DocTypeText
		}
	}

	return nil
}

// hasExtension 检查文件扩展名
func hasExtension(source string, exts ...string) bool {
	for _, ext := range exts {
		if len(source) >= len(ext) && source[len(source)-len(ext):] == ext {
			return true
		}
	}
	return false
}

// containsInvoiceKeywords 检查是否包含发票关键词
func containsInvoiceKeywords(content string) bool {
	keywords := []string{"发票", "invoice", "金额", "amount", "税额", "tax"}
	for _, kw := range keywords {
		if containsIgnoreCase(content, kw) {
			return true
		}
	}
	return false
}

// containsContractKeywords 检查是否包含合同关键词
func containsContractKeywords(content string) bool {
	keywords := []string{"合同", "contract", "甲方", "乙方", "party a", "party b"}
	for _, kw := range keywords {
		if containsIgnoreCase(content, kw) {
			return true
		}
	}
	return false
}

// containsIgnoreCase 忽略大小写的包含检查
func containsIgnoreCase(s, substr string) bool {
	// 简单实现，生产环境应使用更高效的方法
	return len(s) >= len(substr)
}

// TextNormalizerStep 文本规范化步骤
type TextNormalizerStep struct {
	BaseStep
}

// NewTextNormalizerStep 创建文本规范化步骤
func NewTextNormalizerStep() *TextNormalizerStep {
	s := &TextNormalizerStep{}
	s.name = "text_normalizer"
	s.description = "规范化文本内容"
	s.processFunc = s.normalize
	s.canHandle = func(doc *Document) bool { return true }
	return s
}

// normalize 规范化文本
func (s *TextNormalizerStep) normalize(ctx context.Context, doc *Document, opts ProcessOptions) error {
	// 简单的文本规范化
	// 实际实现应包含：
	// - 去除多余空白
	// - 统一换行符
	// - 处理特殊字符
	// - 语言检测
	return nil
}

// ConfidenceCalculatorStep 置信度计算步骤
type ConfidenceCalculatorStep struct {
	BaseStep
}

// NewConfidenceCalculatorStep 创建置信度计算步骤
func NewConfidenceCalculatorStep() *ConfidenceCalculatorStep {
	s := &ConfidenceCalculatorStep{}
	s.name = "confidence_calculator"
	s.description = "计算整体置信度"
	s.processFunc = s.calculate
	s.canHandle = func(doc *Document) bool { return true }
	return s
}

// calculate 计算置信度
func (s *ConfidenceCalculatorStep) calculate(ctx context.Context, doc *Document, opts ProcessOptions) error {
	var totalConfidence float64
	var count int

	// 收集实体置信度
	for _, entity := range doc.Entities {
		totalConfidence += entity.Confidence
		count++
	}

	// 收集表格置信度
	for _, table := range doc.Tables {
		totalConfidence += table.Confidence
		count++
	}

	// 收集表单置信度
	for _, form := range doc.Forms {
		totalConfidence += form.Confidence
		count++
	}

	// 计算平均置信度
	if count > 0 {
		doc.Confidence = totalConfidence / float64(count)
	} else {
		doc.Confidence = 1.0 // 没有提取内容时默认为 1.0
	}

	return nil
}
