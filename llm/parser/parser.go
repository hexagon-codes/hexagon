// Package parser 提供 LLM 输出解析和验证功能
//
// 本包实现了多种输出解析器，支持：
//
//   - JSON 解析器：解析 JSON 格式输出
//
//   - Regex 解析器：使用正则表达式提取内容
//
//   - Enum 解析器：验证枚举值
//
//   - List 解析器：解析列表输出
//
//   - 结构化解析器：解析到指定结构体
//
//   - 组合解析器：链式组合多个解析器
//
//   - Semantic Kernel: OutputFilter
//
// 使用示例：
//
//	parser := parser.NewJSONParser[User]()
//	user, err := parser.Parse(ctx, llmOutput)
package parser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// ============== 错误定义 ==============

var (
	// ErrParseFailure 解析失败
	ErrParseFailure = errors.New("parse failure")

	// ErrValidationFailure 验证失败
	ErrValidationFailure = errors.New("validation failure")

	// ErrEmptyOutput 空输出
	ErrEmptyOutput = errors.New("empty output")

	// ErrInvalidFormat 无效格式
	ErrInvalidFormat = errors.New("invalid format")

	// ErrTypeMismatch 类型不匹配
	ErrTypeMismatch = errors.New("type mismatch")
)

// ============== 解析器接口 ==============

// Parser 输出解析器接口
type Parser[T any] interface {
	// Parse 解析输出
	Parse(ctx context.Context, output string) (T, error)

	// GetFormatInstructions 获取格式说明（用于提示词）
	GetFormatInstructions() string

	// GetTypeName 获取类型名称
	GetTypeName() string
}

// Validator 验证器接口
type Validator[T any] interface {
	// Validate 验证解析结果
	Validate(ctx context.Context, result T) error
}

// ParserWithValidation 带验证的解析器
type ParserWithValidation[T any] interface {
	Parser[T]
	Validator[T]
}

// ============== JSON 解析器 ==============

// JSONParser JSON 解析器
type JSONParser[T any] struct {
	// StrictMode 严格模式（不允许多余字段）
	StrictMode bool

	// ExtractJSON 是否从文本中提取 JSON
	ExtractJSON bool

	// Validators 验证器列表
	Validators []func(T) error
}

// NewJSONParser 创建 JSON 解析器
func NewJSONParser[T any]() *JSONParser[T] {
	return &JSONParser[T]{
		ExtractJSON: true,
	}
}

// WithStrictMode 设置严格模式
func (p *JSONParser[T]) WithStrictMode(strict bool) *JSONParser[T] {
	p.StrictMode = strict
	return p
}

// WithExtractJSON 设置是否提取 JSON
func (p *JSONParser[T]) WithExtractJSON(extract bool) *JSONParser[T] {
	p.ExtractJSON = extract
	return p
}

// WithValidator 添加验证器
func (p *JSONParser[T]) WithValidator(validator func(T) error) *JSONParser[T] {
	p.Validators = append(p.Validators, validator)
	return p
}

// Parse 解析 JSON 输出
func (p *JSONParser[T]) Parse(ctx context.Context, output string) (T, error) {
	var result T

	if output == "" {
		return result, ErrEmptyOutput
	}

	// 提取 JSON
	jsonStr := output
	if p.ExtractJSON {
		jsonStr = extractJSON(output)
		if jsonStr == "" {
			return result, fmt.Errorf("%w: no JSON found in output", ErrParseFailure)
		}
	}

	// 解析
	if p.StrictMode {
		decoder := json.NewDecoder(strings.NewReader(jsonStr))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&result); err != nil {
			return result, fmt.Errorf("%w: %v", ErrParseFailure, err)
		}
	} else {
		if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
			return result, fmt.Errorf("%w: %v", ErrParseFailure, err)
		}
	}

	// 验证
	for _, validator := range p.Validators {
		if err := validator(result); err != nil {
			return result, fmt.Errorf("%w: %v", ErrValidationFailure, err)
		}
	}

	return result, nil
}

// GetFormatInstructions 获取格式说明
func (p *JSONParser[T]) GetFormatInstructions() string {
	var zero T
	schema := generateJSONSchema(zero)
	return fmt.Sprintf(`Please respond with a JSON object that matches the following schema:

%s

Important:
- Your response should be valid JSON only
- Do not include any text before or after the JSON
- Do not include markdown code blocks`, schema)
}

// GetTypeName 获取类型名称
func (p *JSONParser[T]) GetTypeName() string {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Name()
}

// Validate 验证结果
func (p *JSONParser[T]) Validate(ctx context.Context, result T) error {
	for _, validator := range p.Validators {
		if err := validator(result); err != nil {
			return err
		}
	}
	return nil
}

// ============== 正则解析器 ==============

// RegexParser 正则表达式解析器
type RegexParser struct {
	// Pattern 正则表达式
	Pattern *regexp.Regexp

	// GroupNames 分组名称
	GroupNames []string

	// Required 必需的分组
	Required []string
}

// NewRegexParser 创建正则解析器
func NewRegexParser(pattern string) (*RegexParser, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return &RegexParser{
		Pattern:    re,
		GroupNames: re.SubexpNames(),
	}, nil
}

// WithRequired 设置必需的分组
func (p *RegexParser) WithRequired(names ...string) *RegexParser {
	p.Required = names
	return p
}

// Parse 解析输出
func (p *RegexParser) Parse(ctx context.Context, output string) (map[string]string, error) {
	if output == "" {
		return nil, ErrEmptyOutput
	}

	matches := p.Pattern.FindStringSubmatch(output)
	if matches == nil {
		return nil, fmt.Errorf("%w: pattern not matched", ErrParseFailure)
	}

	result := make(map[string]string)
	for i, name := range p.GroupNames {
		if name != "" && i < len(matches) {
			result[name] = matches[i]
		}
	}

	// 检查必需字段
	for _, required := range p.Required {
		if val, ok := result[required]; !ok || val == "" {
			return nil, fmt.Errorf("%w: required field %s is missing", ErrValidationFailure, required)
		}
	}

	return result, nil
}

// GetFormatInstructions 获取格式说明
func (p *RegexParser) GetFormatInstructions() string {
	return fmt.Sprintf("Please respond in a format that matches the pattern: %s", p.Pattern.String())
}

// GetTypeName 获取类型名称
func (p *RegexParser) GetTypeName() string {
	return "RegexMatch"
}

// ============== 枚举解析器 ==============

// EnumParser 枚举解析器
type EnumParser struct {
	// Values 有效值列表
	Values []string

	// CaseSensitive 是否大小写敏感
	CaseSensitive bool

	// AllowPartialMatch 是否允许部分匹配
	AllowPartialMatch bool
}

// NewEnumParser 创建枚举解析器
func NewEnumParser(values ...string) *EnumParser {
	return &EnumParser{
		Values:        values,
		CaseSensitive: false,
	}
}

// WithCaseSensitive 设置大小写敏感
func (p *EnumParser) WithCaseSensitive(sensitive bool) *EnumParser {
	p.CaseSensitive = sensitive
	return p
}

// WithPartialMatch 设置部分匹配
func (p *EnumParser) WithPartialMatch(allow bool) *EnumParser {
	p.AllowPartialMatch = allow
	return p
}

// Parse 解析枚举值
func (p *EnumParser) Parse(ctx context.Context, output string) (string, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return "", ErrEmptyOutput
	}

	for _, value := range p.Values {
		if p.matches(output, value) {
			return value, nil
		}
	}

	return "", fmt.Errorf("%w: '%s' is not a valid value. Expected one of: %s",
		ErrValidationFailure, output, strings.Join(p.Values, ", "))
}

func (p *EnumParser) matches(output, value string) bool {
	if p.CaseSensitive {
		if p.AllowPartialMatch {
			return strings.Contains(output, value)
		}
		return output == value
	}

	outputLower := strings.ToLower(output)
	valueLower := strings.ToLower(value)
	if p.AllowPartialMatch {
		return strings.Contains(outputLower, valueLower)
	}
	return outputLower == valueLower
}

// GetFormatInstructions 获取格式说明
func (p *EnumParser) GetFormatInstructions() string {
	return fmt.Sprintf("Please respond with exactly one of the following values: %s",
		strings.Join(p.Values, ", "))
}

// GetTypeName 获取类型名称
func (p *EnumParser) GetTypeName() string {
	return "Enum"
}

// ============== 列表解析器 ==============

// ListParser 列表解析器
type ListParser struct {
	// Separator 分隔符
	Separator string

	// TrimItems 是否去除空白
	TrimItems bool

	// FilterEmpty 是否过滤空项
	FilterEmpty bool

	// MinItems 最少项数
	MinItems int

	// MaxItems 最大项数
	MaxItems int
}

// NewListParser 创建列表解析器
func NewListParser() *ListParser {
	return &ListParser{
		Separator:   "\n",
		TrimItems:   true,
		FilterEmpty: true,
	}
}

// WithSeparator 设置分隔符
func (p *ListParser) WithSeparator(sep string) *ListParser {
	p.Separator = sep
	return p
}

// WithMinItems 设置最少项数
func (p *ListParser) WithMinItems(min int) *ListParser {
	p.MinItems = min
	return p
}

// WithMaxItems 设置最大项数
func (p *ListParser) WithMaxItems(max int) *ListParser {
	p.MaxItems = max
	return p
}

// Parse 解析列表
func (p *ListParser) Parse(ctx context.Context, output string) ([]string, error) {
	if output == "" {
		return nil, ErrEmptyOutput
	}

	items := strings.Split(output, p.Separator)
	result := make([]string, 0, len(items))

	for _, item := range items {
		if p.TrimItems {
			item = strings.TrimSpace(item)
		}
		if p.FilterEmpty && item == "" {
			continue
		}
		// 移除常见的列表前缀
		item = strings.TrimPrefix(item, "- ")
		item = strings.TrimPrefix(item, "* ")
		item = regexp.MustCompile(`^\d+\.\s*`).ReplaceAllString(item, "")
		result = append(result, item)
	}

	// 验证项数
	if p.MinItems > 0 && len(result) < p.MinItems {
		return nil, fmt.Errorf("%w: expected at least %d items, got %d",
			ErrValidationFailure, p.MinItems, len(result))
	}
	if p.MaxItems > 0 && len(result) > p.MaxItems {
		return nil, fmt.Errorf("%w: expected at most %d items, got %d",
			ErrValidationFailure, p.MaxItems, len(result))
	}

	return result, nil
}

// GetFormatInstructions 获取格式说明
func (p *ListParser) GetFormatInstructions() string {
	instructions := fmt.Sprintf("Please respond with a list of items, one per line")
	if p.MinItems > 0 {
		instructions += fmt.Sprintf(" (minimum %d items)", p.MinItems)
	}
	if p.MaxItems > 0 {
		instructions += fmt.Sprintf(" (maximum %d items)", p.MaxItems)
	}
	return instructions
}

// GetTypeName 获取类型名称
func (p *ListParser) GetTypeName() string {
	return "List"
}

// ============== 布尔解析器 ==============

// BoolParser 布尔解析器
type BoolParser struct {
	// TrueValues 真值列表
	TrueValues []string

	// FalseValues 假值列表
	FalseValues []string
}

// NewBoolParser 创建布尔解析器
func NewBoolParser() *BoolParser {
	return &BoolParser{
		TrueValues:  []string{"yes", "true", "1", "correct", "right", "确定", "是"},
		FalseValues: []string{"no", "false", "0", "incorrect", "wrong", "否", "不是"},
	}
}

// Parse 解析布尔值
func (p *BoolParser) Parse(ctx context.Context, output string) (bool, error) {
	output = strings.TrimSpace(strings.ToLower(output))
	if output == "" {
		return false, ErrEmptyOutput
	}

	for _, v := range p.TrueValues {
		if strings.Contains(output, strings.ToLower(v)) {
			return true, nil
		}
	}

	for _, v := range p.FalseValues {
		if strings.Contains(output, strings.ToLower(v)) {
			return false, nil
		}
	}

	return false, fmt.Errorf("%w: cannot determine boolean value from '%s'", ErrParseFailure, output)
}

// GetFormatInstructions 获取格式说明
func (p *BoolParser) GetFormatInstructions() string {
	return "Please respond with 'yes' or 'no'"
}

// GetTypeName 获取类型名称
func (p *BoolParser) GetTypeName() string {
	return "Boolean"
}

// ============== 数字解析器 ==============

// NumberParser 数字解析器
type NumberParser struct {
	// Min 最小值
	Min *float64

	// Max 最大值
	Max *float64

	// Integer 是否只接受整数
	Integer bool
}

// NewNumberParser 创建数字解析器
func NewNumberParser() *NumberParser {
	return &NumberParser{}
}

// WithRange 设置范围
func (p *NumberParser) WithRange(min, max float64) *NumberParser {
	p.Min = &min
	p.Max = &max
	return p
}

// WithInteger 设置只接受整数
func (p *NumberParser) WithInteger(integer bool) *NumberParser {
	p.Integer = integer
	return p
}

// Parse 解析数字
func (p *NumberParser) Parse(ctx context.Context, output string) (float64, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return 0, ErrEmptyOutput
	}

	// 提取数字
	re := regexp.MustCompile(`-?\d+\.?\d*`)
	match := re.FindString(output)
	if match == "" {
		return 0, fmt.Errorf("%w: no number found in output", ErrParseFailure)
	}

	var result float64
	_, err := fmt.Sscanf(match, "%f", &result)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrParseFailure, err)
	}

	// 验证整数
	if p.Integer && result != float64(int64(result)) {
		return 0, fmt.Errorf("%w: expected integer, got %v", ErrValidationFailure, result)
	}

	// 验证范围
	if p.Min != nil && result < *p.Min {
		return 0, fmt.Errorf("%w: %v is less than minimum %v", ErrValidationFailure, result, *p.Min)
	}
	if p.Max != nil && result > *p.Max {
		return 0, fmt.Errorf("%w: %v is greater than maximum %v", ErrValidationFailure, result, *p.Max)
	}

	return result, nil
}

// GetFormatInstructions 获取格式说明
func (p *NumberParser) GetFormatInstructions() string {
	instructions := "Please respond with a number"
	if p.Integer {
		instructions = "Please respond with an integer"
	}
	if p.Min != nil && p.Max != nil {
		instructions += fmt.Sprintf(" between %v and %v", *p.Min, *p.Max)
	} else if p.Min != nil {
		instructions += fmt.Sprintf(" greater than or equal to %v", *p.Min)
	} else if p.Max != nil {
		instructions += fmt.Sprintf(" less than or equal to %v", *p.Max)
	}
	return instructions
}

// GetTypeName 获取类型名称
func (p *NumberParser) GetTypeName() string {
	if p.Integer {
		return "Integer"
	}
	return "Number"
}

// ============== 组合解析器 ==============

// PipelineParser 管道解析器
// 按顺序执行多个解析步骤
type PipelineParser[T any] struct {
	steps []func(context.Context, string) (string, error)
	final Parser[T]
}

// NewPipelineParser 创建管道解析器
func NewPipelineParser[T any](final Parser[T]) *PipelineParser[T] {
	return &PipelineParser[T]{
		final: final,
	}
}

// AddStep 添加处理步骤
func (p *PipelineParser[T]) AddStep(step func(context.Context, string) (string, error)) *PipelineParser[T] {
	p.steps = append(p.steps, step)
	return p
}

// Parse 执行管道解析
func (p *PipelineParser[T]) Parse(ctx context.Context, output string) (T, error) {
	current := output
	var err error

	for _, step := range p.steps {
		current, err = step(ctx, current)
		if err != nil {
			var zero T
			return zero, err
		}
	}

	return p.final.Parse(ctx, current)
}

// GetFormatInstructions 获取格式说明
func (p *PipelineParser[T]) GetFormatInstructions() string {
	return p.final.GetFormatInstructions()
}

// GetTypeName 获取类型名称
func (p *PipelineParser[T]) GetTypeName() string {
	return p.final.GetTypeName()
}

// ============== 重试解析器 ==============

// RetryParser 重试解析器
// 解析失败时自动重试
type RetryParser[T any] struct {
	parser     Parser[T]
	maxRetries int
	fixFunc    func(string, error) string
}

// NewRetryParser 创建重试解析器
func NewRetryParser[T any](parser Parser[T], maxRetries int) *RetryParser[T] {
	return &RetryParser[T]{
		parser:     parser,
		maxRetries: maxRetries,
	}
}

// WithFixFunc 设置修复函数
func (p *RetryParser[T]) WithFixFunc(fixFunc func(string, error) string) *RetryParser[T] {
	p.fixFunc = fixFunc
	return p
}

// Parse 解析（带重试）
func (p *RetryParser[T]) Parse(ctx context.Context, output string) (T, error) {
	var lastErr error
	current := output

	for i := 0; i <= p.maxRetries; i++ {
		result, err := p.parser.Parse(ctx, current)
		if err == nil {
			return result, nil
		}
		lastErr = err

		// 尝试修复
		if p.fixFunc != nil && i < p.maxRetries {
			current = p.fixFunc(current, err)
		}
	}

	var zero T
	return zero, fmt.Errorf("parse failed after %d retries: %w", p.maxRetries, lastErr)
}

// GetFormatInstructions 获取格式说明
func (p *RetryParser[T]) GetFormatInstructions() string {
	return p.parser.GetFormatInstructions()
}

// GetTypeName 获取类型名称
func (p *RetryParser[T]) GetTypeName() string {
	return p.parser.GetTypeName()
}

// ============== 工具函数 ==============

// extractJSON 从文本中提取 JSON
func extractJSON(text string) string {
	text = strings.TrimSpace(text)

	// 尝试匹配 markdown 代码块
	codeBlockRe := regexp.MustCompile("(?s)```(?:json)?\\s*([\\s\\S]*?)\\s*```")
	if matches := codeBlockRe.FindStringSubmatch(text); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}

	// 查找 JSON 对象或数组
	start := -1
	for i, ch := range text {
		if ch == '{' || ch == '[' {
			start = i
			break
		}
	}
	if start == -1 {
		return ""
	}

	// 找到匹配的结束符
	opening := text[start]
	var closing byte
	if opening == '{' {
		closing = '}'
	} else {
		closing = ']'
	}

	depth := 0
	inString := false
	escape := false

	for i := start; i < len(text); i++ {
		ch := text[i]

		if escape {
			escape = false
			continue
		}

		if ch == '\\' && inString {
			escape = true
			continue
		}

		if ch == '"' {
			inString = !inString
			continue
		}

		if inString {
			continue
		}

		if ch == opening {
			depth++
		} else if ch == closing {
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}

	return ""
}

// generateJSONSchema 生成 JSON Schema
func generateJSONSchema(v any) string {
	t := reflect.TypeOf(v)
	if t == nil {
		return "{}"
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return "{}"
	}

	var builder strings.Builder
	builder.WriteString("{\n")

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" { // 跳过私有字段
			continue
		}

		name := field.Name
		if tag := field.Tag.Get("json"); tag != "" {
			parts := strings.Split(tag, ",")
			if parts[0] != "-" {
				name = parts[0]
			}
		}

		typeName := getJSONTypeName(field.Type)
		desc := field.Tag.Get("desc")
		if desc == "" {
			desc = field.Tag.Get("description")
		}

		if i > 0 {
			builder.WriteString(",\n")
		}
		builder.WriteString(fmt.Sprintf("  \"%s\": %s", name, typeName))
		if desc != "" {
			builder.WriteString(fmt.Sprintf(" // %s", desc))
		}
	}

	builder.WriteString("\n}")
	return builder.String()
}

// getJSONTypeName 获取 JSON 类型名称
func getJSONTypeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "\"string\""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Bool:
		return "boolean"
	case reflect.Slice, reflect.Array:
		elemType := getJSONTypeName(t.Elem())
		return fmt.Sprintf("[%s]", elemType)
	case reflect.Map:
		return "object"
	case reflect.Struct:
		return "object"
	case reflect.Ptr:
		return getJSONTypeName(t.Elem())
	default:
		return "any"
	}
}

// ============== 常用解析器预设 ==============

// ParseYesNo 解析 Yes/No 回答
func ParseYesNo(output string) (bool, error) {
	parser := NewBoolParser()
	return parser.Parse(context.Background(), output)
}

// ParseNumber 解析数字
func ParseNumber(output string) (float64, error) {
	parser := NewNumberParser()
	return parser.Parse(context.Background(), output)
}

// ParseList 解析列表
func ParseList(output string) ([]string, error) {
	parser := NewListParser()
	return parser.Parse(context.Background(), output)
}

// ParseJSON 解析 JSON 到指定类型
func ParseJSON[T any](output string) (T, error) {
	parser := NewJSONParser[T]()
	return parser.Parse(context.Background(), output)
}
