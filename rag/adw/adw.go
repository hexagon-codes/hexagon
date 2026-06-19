// Package adw 提供智能文档工作流（Agentic Document Workflows）功能
//
// ADW 系统超越传统 RAG，提供端到端的文档自动化能力：
// - 智能文档解析和理解
// - 结构化信息提取
// - 表格和表单识别
// - 实体识别和关系抽取
// - Schema 驱动的数据验证
// - 与 Agent 系统深度集成
//
// 对标框架：
// - LlamaIndex: Document Workflows
// - Semantic Kernel: Document Processing
//
// 使用示例：
//
//	pipeline := adw.NewPipeline("invoice-processing").
//	    AddStep(adw.NewOCRStep()).
//	    AddStep(adw.NewTableExtractor()).
//	    AddStep(adw.NewEntityExtractor(llmProvider)).
//	    AddStep(adw.NewSchemaValidator(invoiceSchema)).
//	    Build()
//
//	result, err := pipeline.Process(ctx, documents)
package adw

import (
	"context"
	"errors"
	"time"

	"github.com/hexagon-codes/hexagon/rag"
)

// ============== 错误定义 ==============

var (
	// ErrInvalidDocument 无效的文档
	ErrInvalidDocument = errors.New("adw: invalid document")

	// ErrExtractionFailed 提取失败
	ErrExtractionFailed = errors.New("adw: extraction failed")

	// ErrValidationFailed 验证失败
	ErrValidationFailed = errors.New("adw: validation failed")

	// ErrNoExtractor 没有可用的提取器
	ErrNoExtractor = errors.New("adw: no extractor available")

	// ErrSchemaRequired Schema 必需
	ErrSchemaRequired = errors.New("adw: schema required for extraction")

	// ErrProcessingFailed 处理失败
	ErrProcessingFailed = errors.New("adw: processing failed")

	// ErrUnsupportedFormat 不支持的格式
	ErrUnsupportedFormat = errors.New("adw: unsupported document format")
)

// ============== 文档类型 ==============

// DocumentType 文档类型
type DocumentType string

const (
	// DocTypeUnknown 未知类型
	DocTypeUnknown DocumentType = "unknown"

	// DocTypePDF PDF 文档
	DocTypePDF DocumentType = "pdf"

	// DocTypeWord Word 文档
	DocTypeWord DocumentType = "word"

	// DocTypeExcel Excel 文档
	DocTypeExcel DocumentType = "excel"

	// DocTypeImage 图片文档
	DocTypeImage DocumentType = "image"

	// DocTypeHTML HTML 文档
	DocTypeHTML DocumentType = "html"

	// DocTypeJSON JSON 文档
	DocTypeJSON DocumentType = "json"

	// DocTypeXML XML 文档
	DocTypeXML DocumentType = "xml"

	// DocTypeText 纯文本文档
	DocTypeText DocumentType = "text"

	// DocTypeEmail 电子邮件
	DocTypeEmail DocumentType = "email"

	// DocTypeInvoice 发票
	DocTypeInvoice DocumentType = "invoice"

	// DocTypeContract 合同
	DocTypeContract DocumentType = "contract"

	// DocTypeReceipt 收据
	DocTypeReceipt DocumentType = "receipt"

	// DocTypeForm 表单
	DocTypeForm DocumentType = "form"
)

// ============== Document 定义 ==============

// Document ADW 增强文档
// 扩展 rag.Document，增加结构化信息
type Document struct {
	// 继承基础文档
	rag.Document

	// Type 文档类型
	Type DocumentType

	// StructuredData 结构化数据（提取结果）
	StructuredData map[string]any

	// Tables 表格列表
	Tables []Table

	// Forms 表单字段列表
	Forms []FormField

	// Entities 实体列表
	Entities []Entity

	// Relations 实体关系列表
	Relations []Relation

	// Sections 文档分节
	Sections []Section

	// Images 图片列表
	Images []Image

	// ValidationErrors 验证错误列表
	ValidationErrors []ValidationError

	// ProcessingHistory 处理历史
	ProcessingHistory []ProcessingStep

	// Confidence 整体置信度 (0-1)
	Confidence float64

	// ProcessedAt 处理时间
	ProcessedAt time.Time

	// ProcessingDuration 处理耗时
	ProcessingDuration time.Duration

	// ExtraMetadata 额外元数据
	ExtraMetadata map[string]any
}

// NewDocument 从 rag.Document 创建 ADW Document
func NewDocument(doc rag.Document) *Document {
	return &Document{
		Document:          doc,
		Type:              DocTypeUnknown,
		StructuredData:    make(map[string]any),
		Tables:            make([]Table, 0),
		Forms:             make([]FormField, 0),
		Entities:          make([]Entity, 0),
		Relations:         make([]Relation, 0),
		Sections:          make([]Section, 0),
		Images:            make([]Image, 0),
		ValidationErrors:  make([]ValidationError, 0),
		ProcessingHistory: make([]ProcessingStep, 0),
		ExtraMetadata:     make(map[string]any),
	}
}

// AddTable 添加表格
func (d *Document) AddTable(table Table) {
	d.Tables = append(d.Tables, table)
}

// AddEntity 添加实体
func (d *Document) AddEntity(entity Entity) {
	d.Entities = append(d.Entities, entity)
}

// AddRelation 添加关系
func (d *Document) AddRelation(relation Relation) {
	d.Relations = append(d.Relations, relation)
}

// AddSection 添加分节
func (d *Document) AddSection(section Section) {
	d.Sections = append(d.Sections, section)
}

// AddValidationError 添加验证错误
func (d *Document) AddValidationError(err ValidationError) {
	d.ValidationErrors = append(d.ValidationErrors, err)
}

// AddProcessingStep 添加处理步骤
func (d *Document) AddProcessingStep(step ProcessingStep) {
	d.ProcessingHistory = append(d.ProcessingHistory, step)
}

// IsValid 检查文档是否有效（无验证错误）
func (d *Document) IsValid() bool {
	return len(d.ValidationErrors) == 0
}

// GetStructuredValue 获取结构化数据值
func (d *Document) GetStructuredValue(key string) (any, bool) {
	v, ok := d.StructuredData[key]
	return v, ok
}

// SetStructuredValue 设置结构化数据值
func (d *Document) SetStructuredValue(key string, value any) {
	d.StructuredData[key] = value
}

// ============== 表格定义 ==============

// Table 表格
type Table struct {
	// ID 表格 ID
	ID string

	// Name 表格名称
	Name string

	// Headers 表头
	Headers []string

	// Rows 数据行
	Rows [][]string

	// PageNumber 所在页码
	PageNumber int

	// BoundingBox 边界框
	BoundingBox *BoundingBox

	// Confidence 置信度
	Confidence float64

	// Metadata 元数据
	Metadata map[string]any
}

// RowCount 返回行数
func (t *Table) RowCount() int {
	return len(t.Rows)
}

// ColCount 返回列数
func (t *Table) ColCount() int {
	if len(t.Headers) > 0 {
		return len(t.Headers)
	}
	if len(t.Rows) > 0 {
		return len(t.Rows[0])
	}
	return 0
}

// GetCell 获取单元格
func (t *Table) GetCell(row, col int) string {
	if row < 0 || row >= len(t.Rows) {
		return ""
	}
	if col < 0 || col >= len(t.Rows[row]) {
		return ""
	}
	return t.Rows[row][col]
}

// ToMap 转换为 map 列表
func (t *Table) ToMap() []map[string]string {
	if len(t.Headers) == 0 || len(t.Rows) == 0 {
		return nil
	}

	result := make([]map[string]string, len(t.Rows))
	for i, row := range t.Rows {
		m := make(map[string]string)
		for j, cell := range row {
			if j < len(t.Headers) {
				m[t.Headers[j]] = cell
			}
		}
		result[i] = m
	}
	return result
}

// ============== 表单字段定义 ==============

// FormField 表单字段
type FormField struct {
	// ID 字段 ID
	ID string

	// Name 字段名称
	Name string

	// Label 字段标签
	Label string

	// Value 字段值
	Value string

	// Type 字段类型
	Type string

	// Required 是否必填
	Required bool

	// Confidence 置信度
	Confidence float64

	// BoundingBox 边界框
	BoundingBox *BoundingBox

	// Metadata 元数据
	Metadata map[string]any
}

// ============== 实体定义 ==============

// Entity 实体
type Entity struct {
	// ID 实体 ID
	ID string

	// Text 实体文本
	Text string

	// Type 实体类型
	Type EntityType

	// NormalizedValue 标准化值
	NormalizedValue string

	// Start 起始位置
	Start int

	// End 结束位置
	End int

	// Confidence 置信度
	Confidence float64

	// Properties 属性
	Properties map[string]any

	// Metadata 元数据
	Metadata map[string]any
}

// EntityType 实体类型
type EntityType string

const (
	// EntityPerson 人名
	EntityPerson EntityType = "person"

	// EntityOrganization 组织名
	EntityOrganization EntityType = "organization"

	// EntityLocation 地名
	EntityLocation EntityType = "location"

	// EntityDate 日期
	EntityDate EntityType = "date"

	// EntityTime 时间
	EntityTime EntityType = "time"

	// EntityMoney 金额
	EntityMoney EntityType = "money"

	// EntityPercent 百分比
	EntityPercent EntityType = "percent"

	// EntityEmail 电子邮件
	EntityEmail EntityType = "email"

	// EntityPhone 电话
	EntityPhone EntityType = "phone"

	// EntityURL URL
	EntityURL EntityType = "url"

	// EntityProduct 产品
	EntityProduct EntityType = "product"

	// EntityEvent 事件
	EntityEvent EntityType = "event"

	// EntityCustom 自定义
	EntityCustom EntityType = "custom"
)

// ============== 关系定义 ==============

// Relation 实体关系
type Relation struct {
	// ID 关系 ID
	ID string

	// Type 关系类型
	Type string

	// SourceEntityID 源实体 ID
	SourceEntityID string

	// TargetEntityID 目标实体 ID
	TargetEntityID string

	// Confidence 置信度
	Confidence float64

	// Properties 属性
	Properties map[string]any
}

// ============== 文档分节 ==============

// Section 文档分节
type Section struct {
	// ID 分节 ID
	ID string

	// Title 标题
	Title string

	// Content 内容
	Content string

	// Level 层级 (1-6)
	Level int

	// StartPage 起始页
	StartPage int

	// EndPage 结束页
	EndPage int

	// SubSections 子分节
	SubSections []Section

	// Metadata 元数据
	Metadata map[string]any
}

// ============== 图片定义 ==============

// Image 图片
type Image struct {
	// ID 图片 ID
	ID string

	// Name 图片名称
	Name string

	// Data 图片数据（base64）
	Data string

	// MimeType MIME 类型
	MimeType string

	// Width 宽度
	Width int

	// Height 高度
	Height int

	// PageNumber 所在页码
	PageNumber int

	// BoundingBox 边界框
	BoundingBox *BoundingBox

	// Caption 图片说明（如果有）
	Caption string

	// OCRText OCR 识别文本
	OCRText string

	// Metadata 元数据
	Metadata map[string]any
}

// ============== 边界框 ==============

// BoundingBox 边界框
type BoundingBox struct {
	// X 左上角 X 坐标
	X float64

	// Y 左上角 Y 坐标
	Y float64

	// Width 宽度
	Width float64

	// Height 高度
	Height float64
}

// ============== 验证错误 ==============

// ValidationError 验证错误
type ValidationError struct {
	// Field 字段名
	Field string

	// Rule 违反的规则
	Rule string

	// Message 错误消息
	Message string

	// Severity 严重程度
	Severity ErrorSeverity

	// ExpectedValue 期望值
	ExpectedValue any

	// ActualValue 实际值
	ActualValue any
}

// ErrorSeverity 错误严重程度
type ErrorSeverity string

const (
	// SeverityError 错误
	SeverityError ErrorSeverity = "error"

	// SeverityWarning 警告
	SeverityWarning ErrorSeverity = "warning"

	// SeverityInfo 信息
	SeverityInfo ErrorSeverity = "info"
)

// ============== 处理步骤记录 ==============

// ProcessingStep 处理步骤记录
type ProcessingStep struct {
	// StepName 步骤名称
	StepName string

	// StartTime 开始时间
	StartTime time.Time

	// EndTime 结束时间
	EndTime time.Time

	// Duration 耗时
	Duration time.Duration

	// Success 是否成功
	Success bool

	// Error 错误信息
	Error error

	// Output 输出数据
	Output map[string]any

	// Metadata 元数据
	Metadata map[string]any
}

// ============== 提取 Schema ==============

// ExtractionSchema 提取 Schema
// 定义需要从文档中提取的数据结构
type ExtractionSchema struct {
	// Name Schema 名称
	Name string

	// Description Schema 描述
	Description string

	// Fields 字段定义
	Fields []SchemaField

	// Required 必需字段
	Required []string

	// Metadata 元数据
	Metadata map[string]any
}

// SchemaField Schema 字段定义
type SchemaField struct {
	// Name 字段名
	Name string

	// Description 字段描述
	Description string

	// Type 字段类型
	Type FieldType

	// Format 格式（如 date: "YYYY-MM-DD"）
	Format string

	// Required 是否必需
	Required bool

	// Default 默认值
	Default any

	// Enum 枚举值（如果有）
	Enum []any

	// Pattern 正则表达式（如果有）
	Pattern string

	// Min 最小值（数值或长度）
	Min *float64

	// Max 最大值（数值或长度）
	Max *float64

	// Items 数组元素定义（如果 Type 是 array）
	Items *SchemaField

	// Properties 对象属性定义（如果 Type 是 object）
	Properties []SchemaField

	// Examples 示例值
	Examples []any

	// Metadata 元数据
	Metadata map[string]any
}

// FieldType 字段类型
type FieldType string

const (
	FieldTypeString  FieldType = "string"
	FieldTypeNumber  FieldType = "number"
	FieldTypeInteger FieldType = "integer"
	FieldTypeBoolean FieldType = "boolean"
	FieldTypeArray   FieldType = "array"
	FieldTypeObject  FieldType = "object"
	FieldTypeDate    FieldType = "date"
	FieldTypeTime    FieldType = "time"
	FieldTypeMoney   FieldType = "money"
	FieldTypeEmail   FieldType = "email"
	FieldTypePhone   FieldType = "phone"
	FieldTypeURL     FieldType = "url"
)

// NewExtractionSchema 创建提取 Schema
func NewExtractionSchema(name string) *ExtractionSchema {
	return &ExtractionSchema{
		Name:     name,
		Fields:   make([]SchemaField, 0),
		Required: make([]string, 0),
		Metadata: make(map[string]any),
	}
}

// AddField 添加字段
func (s *ExtractionSchema) AddField(field SchemaField) *ExtractionSchema {
	s.Fields = append(s.Fields, field)
	if field.Required {
		s.Required = append(s.Required, field.Name)
	}
	return s
}

// AddStringField 添加字符串字段
func (s *ExtractionSchema) AddStringField(name, description string, required bool) *ExtractionSchema {
	return s.AddField(SchemaField{
		Name:        name,
		Description: description,
		Type:        FieldTypeString,
		Required:    required,
	})
}

// AddNumberField 添加数值字段
func (s *ExtractionSchema) AddNumberField(name, description string, required bool) *ExtractionSchema {
	return s.AddField(SchemaField{
		Name:        name,
		Description: description,
		Type:        FieldTypeNumber,
		Required:    required,
	})
}

// AddDateField 添加日期字段
func (s *ExtractionSchema) AddDateField(name, description, format string, required bool) *ExtractionSchema {
	return s.AddField(SchemaField{
		Name:        name,
		Description: description,
		Type:        FieldTypeDate,
		Format:      format,
		Required:    required,
	})
}

// AddMoneyField 添加金额字段
func (s *ExtractionSchema) AddMoneyField(name, description string, required bool) *ExtractionSchema {
	return s.AddField(SchemaField{
		Name:        name,
		Description: description,
		Type:        FieldTypeMoney,
		Required:    required,
	})
}

// GetField 获取字段定义
func (s *ExtractionSchema) GetField(name string) *SchemaField {
	for i := range s.Fields {
		if s.Fields[i].Name == name {
			return &s.Fields[i]
		}
	}
	return nil
}

// ============== 处理选项 ==============

// ProcessOptions 处理选项
type ProcessOptions struct {
	// EnableOCR 启用 OCR
	EnableOCR bool

	// EnableTableExtraction 启用表格提取
	EnableTableExtraction bool

	// EnableFormRecognition 启用表单识别
	EnableFormRecognition bool

	// EnableEntityRecognition 启用实体识别
	EnableEntityRecognition bool

	// EnableRelationExtraction 启用关系抽取
	EnableRelationExtraction bool

	// EnableValidation 启用验证
	EnableValidation bool

	// MaxConcurrency 最大并发数
	MaxConcurrency int

	// Timeout 处理超时（秒）
	Timeout int

	// Language 语言
	Language string

	// Schema 提取 Schema
	Schema *ExtractionSchema

	// CustomExtractors 自定义提取器
	CustomExtractors []string

	// Metadata 额外元数据
	Metadata map[string]any
}

// DefaultProcessOptions 默认处理选项
func DefaultProcessOptions() ProcessOptions {
	return ProcessOptions{
		EnableOCR:               false,
		EnableTableExtraction:   true,
		EnableFormRecognition:   true,
		EnableEntityRecognition: true,
		EnableValidation:        true,
		MaxConcurrency:          4,
		Timeout:                 300,
		Language:                "auto",
		Metadata:                make(map[string]any),
	}
}

// ============== Pipeline 接口 ==============

// Pipeline ADW 管道接口
type Pipeline interface {
	// Name 管道名称
	Name() string

	// Process 处理文档
	Process(ctx context.Context, input PipelineInput) (*PipelineOutput, error)

	// AddStep 添加处理步骤
	AddStep(step Step) Pipeline

	// GetSteps 获取所有步骤
	GetSteps() []Step
}

// PipelineInput 管道输入
type PipelineInput struct {
	// Documents 待处理文档
	Documents []rag.Document

	// Schema 提取 Schema
	Schema *ExtractionSchema

	// Options 处理选项
	Options ProcessOptions

	// Metadata 元数据
	Metadata map[string]any
}

// PipelineOutput 管道输出
type PipelineOutput struct {
	// Documents 处理后的文档
	Documents []*Document

	// ExtractedData 提取的数据（汇总）
	ExtractedData map[string]any

	// TotalDocuments 总文档数
	TotalDocuments int

	// SuccessCount 成功数
	SuccessCount int

	// FailureCount 失败数
	FailureCount int

	// ValidationErrors 所有验证错误
	ValidationErrors []ValidationError

	// ProcessingTime 处理时间
	ProcessingTime time.Duration

	// Metadata 元数据
	Metadata map[string]any
}

// ============== Step 接口 ==============

// Step 处理步骤接口
type Step interface {
	// Name 步骤名称
	Name() string

	// Description 步骤描述
	Description() string

	// Process 处理文档
	Process(ctx context.Context, doc *Document, opts ProcessOptions) error

	// CanHandle 检查是否能处理该文档
	CanHandle(doc *Document) bool
}

// ============== Extractor 接口 ==============

// Extractor 提取器接口
type Extractor interface {
	// Name 提取器名称
	Name() string

	// Extract 提取结构化数据
	Extract(ctx context.Context, doc *Document, schema *ExtractionSchema) (map[string]any, error)

	// ExtractEntities 提取实体
	ExtractEntities(ctx context.Context, doc *Document) ([]Entity, error)

	// ExtractRelations 提取关系
	ExtractRelations(ctx context.Context, doc *Document, entities []Entity) ([]Relation, error)
}

// ============== Validator 接口 ==============

// Validator 验证器接口
type Validator interface {
	// Name 验证器名称
	Name() string

	// Validate 验证文档
	Validate(ctx context.Context, doc *Document, schema *ExtractionSchema) ([]ValidationError, error)
}
