// Package validator 提供文档验证功能
//
// 本包实现 Schema 驱动的文档验证：
// - SchemaValidator: 基于 Schema 的数据验证
// - TypeValidator: 类型验证
// - FormatValidator: 格式验证
// - CustomValidator: 自定义验证规则
package validator

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/hexagon-codes/hexagon/rag/adw"
)

// SchemaValidator Schema 验证器
// 根据 Schema 定义验证提取的数据
type SchemaValidator struct {
	// customRules 自定义验证规则
	customRules map[string]ValidationRule

	// strictMode 严格模式（未定义字段会报错）
	strictMode bool

	// continueOnError 出错是否继续验证
	continueOnError bool
}

// ValidationRule 验证规则
type ValidationRule func(ctx context.Context, value any, field *adw.SchemaField) *adw.ValidationError

// SchemaValidatorOption SchemaValidator 配置选项
type SchemaValidatorOption func(*SchemaValidator)

// WithStrictMode 启用严格模式
func WithStrictMode() SchemaValidatorOption {
	return func(v *SchemaValidator) {
		v.strictMode = true
	}
}

// WithContinueOnError 出错继续验证
func WithContinueOnError() SchemaValidatorOption {
	return func(v *SchemaValidator) {
		v.continueOnError = true
	}
}

// WithCustomRule 添加自定义规则
func WithCustomRule(name string, rule ValidationRule) SchemaValidatorOption {
	return func(v *SchemaValidator) {
		v.customRules[name] = rule
	}
}

// NewSchemaValidator 创建 Schema 验证器
func NewSchemaValidator(opts ...SchemaValidatorOption) *SchemaValidator {
	v := &SchemaValidator{
		customRules:     make(map[string]ValidationRule),
		strictMode:      false,
		continueOnError: true,
	}

	for _, opt := range opts {
		opt(v)
	}

	return v
}

// Name 返回验证器名称
func (v *SchemaValidator) Name() string {
	return "schema_validator"
}

// Validate 验证文档
func (v *SchemaValidator) Validate(ctx context.Context, doc *adw.Document, schema *adw.ExtractionSchema) ([]adw.ValidationError, error) {
	if schema == nil {
		return nil, nil
	}

	errors := make([]adw.ValidationError, 0)

	// 验证每个字段
	for _, field := range schema.Fields {
		value, exists := doc.GetStructuredValue(field.Name)

		// 检查必需字段
		if field.Required && (!exists || value == nil) {
			errors = append(errors, adw.ValidationError{
				Field:    field.Name,
				Rule:     "required",
				Message:  fmt.Sprintf("字段 '%s' 是必需的", field.Name),
				Severity: adw.SeverityError,
			})

			if !v.continueOnError {
				return errors, adw.ErrValidationFailed
			}
			continue
		}

		// 如果字段不存在且非必需，跳过
		if !exists || value == nil {
			continue
		}

		// 验证类型
		if err := v.validateType(value, &field); err != nil {
			errors = append(errors, *err)
			if !v.continueOnError {
				return errors, adw.ErrValidationFailed
			}
		}

		// 验证格式
		if err := v.validateFormat(value, &field); err != nil {
			errors = append(errors, *err)
			if !v.continueOnError {
				return errors, adw.ErrValidationFailed
			}
		}

		// 验证范围
		if err := v.validateRange(value, &field); err != nil {
			errors = append(errors, *err)
			if !v.continueOnError {
				return errors, adw.ErrValidationFailed
			}
		}

		// 验证枚举
		if err := v.validateEnum(value, &field); err != nil {
			errors = append(errors, *err)
			if !v.continueOnError {
				return errors, adw.ErrValidationFailed
			}
		}

		// 验证正则表达式
		if err := v.validatePattern(value, &field); err != nil {
			errors = append(errors, *err)
			if !v.continueOnError {
				return errors, adw.ErrValidationFailed
			}
		}

		// 执行自定义规则
		if rule, ok := v.customRules[field.Name]; ok {
			if err := rule(ctx, value, &field); err != nil {
				errors = append(errors, *err)
				if !v.continueOnError {
					return errors, adw.ErrValidationFailed
				}
			}
		}
	}

	// 严格模式下检查未定义字段
	if v.strictMode {
		for key := range doc.StructuredData {
			if schema.GetField(key) == nil {
				errors = append(errors, adw.ValidationError{
					Field:    key,
					Rule:     "undefined_field",
					Message:  fmt.Sprintf("字段 '%s' 未在 Schema 中定义", key),
					Severity: adw.SeverityWarning,
				})
			}
		}
	}

	if len(errors) > 0 {
		return errors, adw.ErrValidationFailed
	}

	return errors, nil
}

// validateType 验证类型
func (v *SchemaValidator) validateType(value any, field *adw.SchemaField) *adw.ValidationError {
	valid := false

	switch field.Type {
	case adw.FieldTypeString:
		_, valid = value.(string)
	case adw.FieldTypeNumber:
		switch value.(type) {
		case float64, float32, int, int64, int32:
			valid = true
		}
	case adw.FieldTypeInteger:
		switch value.(type) {
		case int, int64, int32:
			valid = true
		case float64:
			// JSON 解析的整数也是 float64
			f := value.(float64)
			valid = f == float64(int64(f))
		}
	case adw.FieldTypeBoolean:
		_, valid = value.(bool)
	case adw.FieldTypeArray:
		_, valid = value.([]any)
	case adw.FieldTypeObject:
		_, valid = value.(map[string]any)
	case adw.FieldTypeDate, adw.FieldTypeTime:
		// 日期/时间可以是字符串
		_, valid = value.(string)
	case adw.FieldTypeMoney:
		switch value.(type) {
		case float64, float32, int, int64, string:
			valid = true
		}
	case adw.FieldTypeEmail, adw.FieldTypePhone, adw.FieldTypeURL:
		_, valid = value.(string)
	default:
		valid = true // 未知类型不验证
	}

	if !valid {
		return &adw.ValidationError{
			Field:         field.Name,
			Rule:          "type",
			Message:       fmt.Sprintf("字段 '%s' 的类型应为 %s", field.Name, field.Type),
			Severity:      adw.SeverityError,
			ExpectedValue: string(field.Type),
			ActualValue:   fmt.Sprintf("%T", value),
		}
	}

	return nil
}

// validateFormat 验证格式
func (v *SchemaValidator) validateFormat(value any, field *adw.SchemaField) *adw.ValidationError {
	str, ok := value.(string)
	if !ok {
		return nil
	}

	switch field.Type {
	case adw.FieldTypeEmail:
		if !isValidEmail(str) {
			return &adw.ValidationError{
				Field:       field.Name,
				Rule:        "format_email",
				Message:     fmt.Sprintf("字段 '%s' 不是有效的邮箱地址", field.Name),
				Severity:    adw.SeverityError,
				ActualValue: str,
			}
		}

	case adw.FieldTypePhone:
		if !isValidPhone(str) {
			return &adw.ValidationError{
				Field:       field.Name,
				Rule:        "format_phone",
				Message:     fmt.Sprintf("字段 '%s' 不是有效的电话号码", field.Name),
				Severity:    adw.SeverityWarning,
				ActualValue: str,
			}
		}

	case adw.FieldTypeURL:
		if !isValidURL(str) {
			return &adw.ValidationError{
				Field:       field.Name,
				Rule:        "format_url",
				Message:     fmt.Sprintf("字段 '%s' 不是有效的 URL", field.Name),
				Severity:    adw.SeverityError,
				ActualValue: str,
			}
		}

	case adw.FieldTypeDate:
		if !isValidDate(str, field.Format) {
			return &adw.ValidationError{
				Field:         field.Name,
				Rule:          "format_date",
				Message:       fmt.Sprintf("字段 '%s' 不是有效的日期格式", field.Name),
				Severity:      adw.SeverityError,
				ExpectedValue: field.Format,
				ActualValue:   str,
			}
		}
	}

	return nil
}

// validateRange 验证范围
func (v *SchemaValidator) validateRange(value any, field *adw.SchemaField) *adw.ValidationError {
	// 数值范围
	if field.Min != nil || field.Max != nil {
		num, ok := toFloat64(value)
		if !ok {
			return nil
		}

		if field.Min != nil && num < *field.Min {
			return &adw.ValidationError{
				Field:         field.Name,
				Rule:          "min",
				Message:       fmt.Sprintf("字段 '%s' 的值不能小于 %v", field.Name, *field.Min),
				Severity:      adw.SeverityError,
				ExpectedValue: *field.Min,
				ActualValue:   num,
			}
		}

		if field.Max != nil && num > *field.Max {
			return &adw.ValidationError{
				Field:         field.Name,
				Rule:          "max",
				Message:       fmt.Sprintf("字段 '%s' 的值不能大于 %v", field.Name, *field.Max),
				Severity:      adw.SeverityError,
				ExpectedValue: *field.Max,
				ActualValue:   num,
			}
		}
	}

	// 字符串长度范围
	if str, ok := value.(string); ok {
		length := float64(len(str))

		if field.Min != nil && length < *field.Min {
			return &adw.ValidationError{
				Field:         field.Name,
				Rule:          "min_length",
				Message:       fmt.Sprintf("字段 '%s' 的长度不能小于 %v", field.Name, int(*field.Min)),
				Severity:      adw.SeverityError,
				ExpectedValue: *field.Min,
				ActualValue:   length,
			}
		}

		if field.Max != nil && length > *field.Max {
			return &adw.ValidationError{
				Field:         field.Name,
				Rule:          "max_length",
				Message:       fmt.Sprintf("字段 '%s' 的长度不能大于 %v", field.Name, int(*field.Max)),
				Severity:      adw.SeverityError,
				ExpectedValue: *field.Max,
				ActualValue:   length,
			}
		}
	}

	return nil
}

// validateEnum 验证枚举
func (v *SchemaValidator) validateEnum(value any, field *adw.SchemaField) *adw.ValidationError {
	if len(field.Enum) == 0 {
		return nil
	}

	for _, enumValue := range field.Enum {
		if value == enumValue {
			return nil
		}
	}

	return &adw.ValidationError{
		Field:         field.Name,
		Rule:          "enum",
		Message:       fmt.Sprintf("字段 '%s' 的值必须是 %v 之一", field.Name, field.Enum),
		Severity:      adw.SeverityError,
		ExpectedValue: field.Enum,
		ActualValue:   value,
	}
}

// validatePattern 验证正则表达式
func (v *SchemaValidator) validatePattern(value any, field *adw.SchemaField) *adw.ValidationError {
	if field.Pattern == "" {
		return nil
	}

	str, ok := value.(string)
	if !ok {
		return nil
	}

	pattern, err := regexp.Compile(field.Pattern)
	if err != nil {
		return &adw.ValidationError{
			Field:    field.Name,
			Rule:     "pattern_invalid",
			Message:  fmt.Sprintf("字段 '%s' 的正则表达式无效: %v", field.Name, err),
			Severity: adw.SeverityWarning,
		}
	}

	if !pattern.MatchString(str) {
		return &adw.ValidationError{
			Field:         field.Name,
			Rule:          "pattern",
			Message:       fmt.Sprintf("字段 '%s' 的值不符合指定格式", field.Name),
			Severity:      adw.SeverityError,
			ExpectedValue: field.Pattern,
			ActualValue:   str,
		}
	}

	return nil
}

// ============== 辅助函数 ==============

// isValidEmail 验证邮箱格式
func isValidEmail(email string) bool {
	pattern := regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	return pattern.MatchString(email)
}

// isValidPhone 验证电话格式
func isValidPhone(phone string) bool {
	// 支持多种格式
	pattern := regexp.MustCompile(`^[\d\s\-\+\(\)]+$`)
	return pattern.MatchString(phone) && len(phone) >= 7
}

// isValidURL 验证 URL 格式
func isValidURL(url string) bool {
	pattern := regexp.MustCompile(`^(https?|ftp)://[^\s/$.?#].[^\s]*$`)
	return pattern.MatchString(url)
}

// isValidDate 验证日期格式
func isValidDate(dateStr, format string) bool {
	if format == "" {
		format = "2006-01-02"
	}

	// 转换常见格式
	goFormat := convertDateFormat(format)
	_, err := time.Parse(goFormat, dateStr)
	return err == nil
}

// convertDateFormat 转换日期格式
func convertDateFormat(format string) string {
	// 简单的格式转换
	replacer := map[string]string{
		"YYYY": "2006",
		"yyyy": "2006",
		"MM":   "01",
		"DD":   "02",
		"dd":   "02",
		"HH":   "15",
		"hh":   "03",
		"mm":   "04",
		"ss":   "05",
	}

	result := format
	for from, to := range replacer {
		result = regexp.MustCompile(from).ReplaceAllString(result, to)
	}

	if result == format {
		return "2006-01-02" // 默认格式
	}

	return result
}

// toFloat64 转换为 float64
func toFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	default:
		return 0, false
	}
}

// ============== 验证步骤 ==============

// SchemaValidationStep Schema 验证步骤
type SchemaValidationStep struct {
	validator *SchemaValidator
	schema    *adw.ExtractionSchema
}

// NewSchemaValidationStep 创建验证步骤
func NewSchemaValidationStep(schema *adw.ExtractionSchema, opts ...SchemaValidatorOption) *SchemaValidationStep {
	return &SchemaValidationStep{
		validator: NewSchemaValidator(opts...),
		schema:    schema,
	}
}

// Name 返回步骤名称
func (s *SchemaValidationStep) Name() string {
	return "schema_validation"
}

// Description 返回步骤描述
func (s *SchemaValidationStep) Description() string {
	return "验证提取的数据是否符合 Schema"
}

// Process 处理文档
func (s *SchemaValidationStep) Process(ctx context.Context, doc *adw.Document, opts adw.ProcessOptions) error {
	if !opts.EnableValidation {
		return nil
	}

	// 使用传入的 schema 或步骤自带的 schema
	schema := opts.Schema
	if schema == nil {
		schema = s.schema
	}

	if schema == nil {
		return nil
	}

	// 验证
	validationErrors, err := s.validator.Validate(ctx, doc, schema)

	// 添加验证错误到文档
	for _, ve := range validationErrors {
		doc.AddValidationError(ve)
	}

	return err
}

// CanHandle 检查是否能处理
func (s *SchemaValidationStep) CanHandle(doc *adw.Document) bool {
	return len(doc.StructuredData) > 0
}
