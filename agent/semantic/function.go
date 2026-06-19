// Package semantic 提供语义函数功能
//
// 本包实现 Semantic Functions：
//   - Prompt 函数：基于 Prompt 模板定义的函数
//   - Native 函数：Go 原生函数
//   - 函数组合：函数链式调用
//   - 函数注册：统一函数管理
//
// 设计借鉴：
//   - Semantic Kernel: Semantic Functions
//   - LangChain: Prompt Templates + Chains
//   - OpenAI: Function Calling
package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"text/template"

	"github.com/hexagon-codes/ai-core/llm"
)

// ============== 核心类型 ==============

// Function 语义函数接口
type Function interface {
	// Name 函数名称
	Name() string

	// Description 函数描述
	Description() string

	// InputSchema 输入 Schema
	InputSchema() *llm.Schema

	// OutputSchema 输出 Schema
	OutputSchema() *llm.Schema

	// Invoke 调用函数
	Invoke(ctx context.Context, input map[string]any) (any, error)
}

// FunctionType 函数类型
type FunctionType string

const (
	// FunctionTypeSemantic 语义函数（基于 Prompt）
	FunctionTypeSemantic FunctionType = "semantic"

	// FunctionTypeNative Native 函数
	FunctionTypeNative FunctionType = "native"

	// FunctionTypeComposite 组合函数
	FunctionTypeComposite FunctionType = "composite"
)

// ============== Semantic Function ==============

// SemanticFunction 语义函数
// 使用 Prompt 模板和 LLM 实现的函数
type SemanticFunction struct {
	// name 函数名称
	name string

	// description 函数描述
	description string

	// prompt Prompt 模板
	prompt *template.Template

	// promptText 原始 Prompt 文本
	promptText string

	// inputSchema 输入 Schema
	inputSchema *llm.Schema

	// outputSchema 输出 Schema
	outputSchema *llm.Schema

	// llm LLM 提供者
	llm llm.Provider

	// model 使用的模型
	model string

	// temperature 温度参数
	temperature float64

	// maxTokens 最大 token 数
	maxTokens int

	// outputParser 输出解析器
	outputParser OutputParser

	// systemPrompt 系统提示
	systemPrompt string
}

// SemanticFunctionConfig 语义函数配置
type SemanticFunctionConfig struct {
	// Name 函数名称
	Name string

	// Description 函数描述
	Description string

	// Prompt Prompt 模板
	Prompt string

	// SystemPrompt 系统提示
	SystemPrompt string

	// LLM LLM 提供者
	LLM llm.Provider

	// Model 模型名称
	Model string

	// Temperature 温度
	Temperature float64

	// MaxTokens 最大 token 数
	MaxTokens int

	// InputSchema 输入 Schema
	InputSchema *llm.Schema

	// OutputSchema 输出 Schema
	OutputSchema *llm.Schema

	// OutputParser 输出解析器
	OutputParser OutputParser
}

// NewSemanticFunction 创建语义函数
func NewSemanticFunction(config SemanticFunctionConfig) (*SemanticFunction, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if config.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	if config.LLM == nil {
		return nil, fmt.Errorf("LLM provider is required")
	}

	// 解析 Prompt 模板
	tmpl, err := template.New(config.Name).Parse(config.Prompt)
	if err != nil {
		return nil, fmt.Errorf("invalid prompt template: %w", err)
	}

	// 默认输出解析器
	if config.OutputParser == nil {
		config.OutputParser = &StringOutputParser{}
	}

	return &SemanticFunction{
		name:         config.Name,
		description:  config.Description,
		prompt:       tmpl,
		promptText:   config.Prompt,
		inputSchema:  config.InputSchema,
		outputSchema: config.OutputSchema,
		llm:          config.LLM,
		model:        config.Model,
		temperature:  config.Temperature,
		maxTokens:    config.MaxTokens,
		outputParser: config.OutputParser,
		systemPrompt: config.SystemPrompt,
	}, nil
}

// Name 返回函数名称
func (f *SemanticFunction) Name() string {
	return f.name
}

// Description 返回函数描述
func (f *SemanticFunction) Description() string {
	return f.description
}

// InputSchema 返回输入 Schema
func (f *SemanticFunction) InputSchema() *llm.Schema {
	return f.inputSchema
}

// OutputSchema 返回输出 Schema
func (f *SemanticFunction) OutputSchema() *llm.Schema {
	return f.outputSchema
}

// Invoke 调用函数
func (f *SemanticFunction) Invoke(ctx context.Context, input map[string]any) (any, error) {
	// 渲染 Prompt
	var buf strings.Builder
	if err := f.prompt.Execute(&buf, input); err != nil {
		return nil, fmt.Errorf("failed to render prompt: %w", err)
	}
	prompt := buf.String()

	// 构建消息
	messages := []llm.Message{}
	if f.systemPrompt != "" {
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: f.systemPrompt,
		})
	}
	messages = append(messages, llm.Message{
		Role:    "user",
		Content: prompt,
	})

	// 调用 LLM
	req := llm.CompletionRequest{
		Messages: messages,
	}
	if f.model != "" {
		req.Model = f.model
	}
	// 温度始终传递：0 是合法的确定性温度，不能用 `> 0` 哨兵当作"未设置"丢弃。
	// CompletionRequest.Temperature 为 *float64 正是为了区分"未设置(nil)"与"显式 0"；
	// 由于本结构体的 temperature 为非指针字段，无法区分默认零值与显式 0，
	// 这里统一将其作为有效值传递（取局部变量地址避免对结构体字段取址）。
	temp := f.temperature
	req.Temperature = &temp
	if f.maxTokens > 0 {
		req.MaxTokens = f.maxTokens
	}

	resp, err := f.llm.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	// 解析输出
	return f.outputParser.Parse(resp.Content)
}

// ============== Native Function ==============

// NativeFunction Native 函数
// 直接封装 Go 函数
type NativeFunction struct {
	// name 函数名称
	name string

	// description 函数描述
	description string

	// fn 函数实现
	fn any

	// inputSchema 输入 Schema
	inputSchema *llm.Schema

	// outputSchema 输出 Schema
	outputSchema *llm.Schema
}

// NativeFunctionConfig Native 函数配置
type NativeFunctionConfig struct {
	Name         string
	Description  string
	Fn           any
	InputSchema  *llm.Schema
	OutputSchema *llm.Schema
}

// NewNativeFunction 创建 Native 函数
func NewNativeFunction(config NativeFunctionConfig) (*NativeFunction, error) {
	if config.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if config.Fn == nil {
		return nil, fmt.Errorf("function is required")
	}

	// 验证函数签名
	fnType := reflect.TypeOf(config.Fn)
	if fnType.Kind() != reflect.Func {
		return nil, fmt.Errorf("fn must be a function")
	}

	return &NativeFunction{
		name:         config.Name,
		description:  config.Description,
		fn:           config.Fn,
		inputSchema:  config.InputSchema,
		outputSchema: config.OutputSchema,
	}, nil
}

// Name 返回函数名称
func (f *NativeFunction) Name() string {
	return f.name
}

// Description 返回函数描述
func (f *NativeFunction) Description() string {
	return f.description
}

// InputSchema 返回输入 Schema
func (f *NativeFunction) InputSchema() *llm.Schema {
	return f.inputSchema
}

// OutputSchema 返回输出 Schema
func (f *NativeFunction) OutputSchema() *llm.Schema {
	return f.outputSchema
}

// Invoke 调用函数
func (f *NativeFunction) Invoke(ctx context.Context, input map[string]any) (any, error) {
	fnValue := reflect.ValueOf(f.fn)
	fnType := fnValue.Type()

	// 构建参数
	args := make([]reflect.Value, fnType.NumIn())

	// 第一个参数可能是 context
	argOffset := 0
	if fnType.NumIn() > 0 && fnType.In(0) == reflect.TypeOf((*context.Context)(nil)).Elem() {
		args[0] = reflect.ValueOf(ctx)
		argOffset = 1
	}

	// 处理剩余参数
	if fnType.NumIn() > argOffset {
		inputType := fnType.In(argOffset)

		// 如果输入是 map
		if inputType.Kind() == reflect.Map {
			args[argOffset] = reflect.ValueOf(input)
		} else if inputType.Kind() == reflect.Struct || (inputType.Kind() == reflect.Ptr && inputType.Elem().Kind() == reflect.Struct) {
			// 如果输入是结构体，尝试转换
			inputData, _ := json.Marshal(input)
			inputVal := reflect.New(inputType)
			if inputType.Kind() == reflect.Ptr {
				inputVal = reflect.New(inputType.Elem())
			}
			if err := json.Unmarshal(inputData, inputVal.Interface()); err != nil {
				return nil, fmt.Errorf("failed to parse input: %w", err)
			}
			if inputType.Kind() == reflect.Ptr {
				args[argOffset] = inputVal
			} else {
				args[argOffset] = inputVal.Elem()
			}
		} else {
			// 兜底分支：参数既不是 map 也不是 struct/*struct（如标量、slice、array 等）。
			// 此前缺少该分支会使 args[argOffset] 保持为零 reflect.Value，
			// 随后 fnValue.Call 触发 "reflect: Call using zero Value argument" panic。
			// 约定从 input["input"] 取单值（与 CompositeFunction 对非 map 中间结果的
			// {"input": val} 包装约定一致），通过 JSON 适配到目标类型；无法适配则返回明确错误。
			adapted, err := adaptScalarArg(input, inputType)
			if err != nil {
				return nil, err
			}
			args[argOffset] = adapted
		}
	}

	// 调用函数
	results := fnValue.Call(args)

	// 处理返回值
	if len(results) == 0 {
		return nil, nil
	}

	// 检查错误
	if len(results) == 2 {
		if !results[1].IsNil() {
			return nil, results[1].Interface().(error)
		}
		return results[0].Interface(), nil
	}

	return results[0].Interface(), nil
}

// adaptScalarArg 将 input 适配为非 map/非 struct 的目标参数类型（标量、slice、array 等）。
//
// 取值约定：优先使用 input["input"] 作为单值来源，这与 CompositeFunction 对非 map
// 中间结果包装为 {"input": val} 的约定保持一致。取出的值经 JSON 序列化后再反序列化
// 到目标类型，以兼容 float64<->int 等常见数值转换。
//
// 当 input 中不存在 "input" 键，或值无法转换到目标类型时，返回明确错误而非 panic。
func adaptScalarArg(input map[string]any, inputType reflect.Type) (reflect.Value, error) {
	raw, ok := input["input"]
	if !ok {
		return reflect.Value{}, fmt.Errorf("failed to adapt input: function expects a %s argument but no matching value found (provide it under the \"input\" key)", inputType)
	}

	// 通过 JSON 中转适配到目标类型，兼容数值/切片等结构。
	data, err := json.Marshal(raw)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("failed to adapt input: %w", err)
	}
	target := reflect.New(inputType)
	if err := json.Unmarshal(data, target.Interface()); err != nil {
		return reflect.Value{}, fmt.Errorf("failed to adapt input to %s: %w", inputType, err)
	}
	return target.Elem(), nil
}

// ============== Composite Function ==============

// CompositeFunction 组合函数
// 将多个函数组合成一个
type CompositeFunction struct {
	// name 函数名称
	name string

	// description 函数描述
	description string

	// functions 函数列表
	functions []Function

	// inputSchema 输入 Schema
	inputSchema *llm.Schema

	// outputSchema 输出 Schema
	outputSchema *llm.Schema
}

// NewCompositeFunction 创建组合函数
func NewCompositeFunction(name, description string, functions ...Function) *CompositeFunction {
	var inputSchema, outputSchema *llm.Schema
	if len(functions) > 0 {
		inputSchema = functions[0].InputSchema()
		outputSchema = functions[len(functions)-1].OutputSchema()
	}

	return &CompositeFunction{
		name:         name,
		description:  description,
		functions:    functions,
		inputSchema:  inputSchema,
		outputSchema: outputSchema,
	}
}

// Name 返回函数名称
func (f *CompositeFunction) Name() string {
	return f.name
}

// Description 返回函数描述
func (f *CompositeFunction) Description() string {
	return f.description
}

// InputSchema 返回输入 Schema
func (f *CompositeFunction) InputSchema() *llm.Schema {
	return f.inputSchema
}

// OutputSchema 返回输出 Schema
func (f *CompositeFunction) OutputSchema() *llm.Schema {
	return f.outputSchema
}

// Invoke 调用函数
func (f *CompositeFunction) Invoke(ctx context.Context, input map[string]any) (any, error) {
	var result any = input

	for _, fn := range f.functions {
		// 转换输入
		inputMap, ok := result.(map[string]any)
		if !ok {
			inputMap = map[string]any{"input": result}
		}

		// 调用函数
		var err error
		result, err = fn.Invoke(ctx, inputMap)
		if err != nil {
			return nil, fmt.Errorf("function %s failed: %w", fn.Name(), err)
		}
	}

	return result, nil
}

// ============== Output Parser ==============

// OutputParser 输出解析器接口
type OutputParser interface {
	Parse(output string) (any, error)
}

// StringOutputParser 字符串输出解析器
type StringOutputParser struct{}

// Parse 解析输出
func (p *StringOutputParser) Parse(output string) (any, error) {
	return strings.TrimSpace(output), nil
}

// JSONOutputParser JSON 输出解析器
type JSONOutputParser struct {
	// TargetType 目标类型
	TargetType reflect.Type
}

// Parse 解析输出
func (p *JSONOutputParser) Parse(output string) (any, error) {
	// 提取 JSON
	output = extractJSON(output)

	if p.TargetType == nil {
		var result any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			return nil, fmt.Errorf("failed to parse JSON: %w", err)
		}
		return result, nil
	}

	result := reflect.New(p.TargetType).Interface()
	if err := json.Unmarshal([]byte(output), result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}
	return result, nil
}

// extractJSON 从文本中提取第一个完整的 JSON 块。
//
// 此前实现用首个 '{' 到末个 '}' 粗暴截取，对多 JSON 块或含花括号的文本会错位
// （例如 `{"a":1} 文本 {"b":2}` 会被错误地整段截取）。
// 现改为基于括号配平的扫描：优先提取第一个配平完整的对象 {...}，
// 若不存在则提取第一个配平完整的数组 [...]，二者皆无则原样返回文本。
// 配平时正确处理字符串字面量内的括号与转义，避免被字符串内容干扰。
func extractJSON(text string) string {
	if obj := extractBalanced(text, '{', '}'); obj != "" {
		return obj
	}
	if arr := extractBalanced(text, '[', ']'); arr != "" {
		return arr
	}
	return text
}

// extractBalanced 在 text 中查找以 open 开头、括号配平到对应 close 结束的第一个完整片段。
// 找不到（无 open 或始终不配平）时返回空字符串。
// 扫描过程中跳过 JSON 字符串字面量内的括号，并正确处理反斜杠转义。
func extractBalanced(text string, open, close byte) string {
	start := strings.IndexByte(text, open)
	if start == -1 {
		return ""
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			// 字符串内部：处理转义，遇到未转义的引号结束字符串。
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				// 配平完成，返回从首个 open 到当前 close 的完整片段。
				return text[start : i+1]
			}
		}
	}

	// 始终未配平到 0，视为不完整。
	return ""
}

// ListOutputParser 列表输出解析器
type ListOutputParser struct {
	// Separator 分隔符
	Separator string
}

// Parse 解析输出
func (p *ListOutputParser) Parse(output string) (any, error) {
	sep := p.Separator
	if sep == "" {
		sep = "\n"
	}

	lines := strings.Split(output, sep)
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			// 移除可能的编号前缀，支持多位编号（如 "10." / "11)"）。
			// 此前用固定下标 line[0]/line[1] 只能识别单位数字编号，"10. tenth" 会残留。
			// 现循环消费连续前导数字，后接 '.' 或 ')' 视为编号符号再剥离。
			line = stripNumberPrefix(line)
			result = append(result, line)
		}
	}

	return result, nil
}

// stripNumberPrefix 剥离形如 "10. " / "11) " 的列表编号前缀。
//
// 匹配模式等价于 ^\d+[.)]\s*：先消费一个或多个连续数字，紧接 '.' 或 ')'，
// 再吃掉其后的空白。若前导数字后不是 '.'/')'（如 "2024 not numbered"），
// 视为正文而非编号，原样返回不剥离。
func stripNumberPrefix(line string) string {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	// 必须至少有一位数字，且其后紧跟 '.' 或 ')'，才认定为编号前缀。
	if i > 0 && i < len(line) && (line[i] == '.' || line[i] == ')') {
		return strings.TrimSpace(line[i+1:])
	}
	return line
}

// BoolOutputParser 布尔输出解析器
type BoolOutputParser struct{}

// Parse 解析输出
//
// 判定逻辑区分两类肯定词，避免此前 HasPrefix 造成的假阳性（如 "yesterday" 被判 true）：
//   - ASCII 肯定词（true/yes/1）：按非字母数字边界整词匹配首词，
//     即首词恰为该值，或该值后紧跟非字母数字字符（标点/空白），例如 "yes." / "true, ..."。
//   - CJK 肯定词（是/对/正确）：中文无词边界，沿用前缀匹配以容忍 "是的" / "正确。" 等。
func (p *BoolOutputParser) Parse(output string) (any, error) {
	output = strings.ToLower(strings.TrimSpace(output))

	// ASCII 肯定词：整词匹配。
	asciiTrue := []string{"true", "yes", "1"}
	for _, v := range asciiTrue {
		if matchLeadingWord(output, v) {
			return true, nil
		}
	}

	// CJK 肯定词：前缀匹配（中文无空格词边界）。
	cjkTrue := []string{"是", "对", "正确"}
	for _, v := range cjkTrue {
		if output == v || strings.HasPrefix(output, v) {
			return true, nil
		}
	}

	return false, nil
}

// matchLeadingWord 判断 s 的首词是否恰为 word（按非字母数字边界切词）。
// 即 s == word，或 s 以 word 开头且紧随其后的字符不是字母或数字
// （如标点、空白、CJK 字符等），从而 "yes." 命中而 "yesterday" 不命中。
func matchLeadingWord(s, word string) bool {
	if !strings.HasPrefix(s, word) {
		return false
	}
	if len(s) == len(word) {
		return true
	}
	next := s[len(word)] // word 为 ASCII，按字节判断边界即可
	isAlnum := (next >= 'a' && next <= 'z') || (next >= 'A' && next <= 'Z') || (next >= '0' && next <= '9')
	return !isAlnum
}

// ============== Function Registry ==============

// Registry 函数注册表
type Registry struct {
	functions map[string]Function
	mu        sync.RWMutex
}

// NewRegistry 创建函数注册表
func NewRegistry() *Registry {
	return &Registry{
		functions: make(map[string]Function),
	}
}

// Register 注册函数
func (r *Registry) Register(fn Function) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.functions[fn.Name()]; exists {
		return fmt.Errorf("function already exists: %s", fn.Name())
	}

	r.functions[fn.Name()] = fn
	return nil
}

// Get 获取函数
func (r *Registry) Get(name string) (Function, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	fn, exists := r.functions[name]
	return fn, exists
}

// List 列出所有函数
func (r *Registry) List() []Function {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Function, 0, len(r.functions))
	for _, fn := range r.functions {
		result = append(result, fn)
	}
	return result
}

// Invoke 调用函数
func (r *Registry) Invoke(ctx context.Context, name string, input map[string]any) (any, error) {
	fn, exists := r.Get(name)
	if !exists {
		return nil, fmt.Errorf("function not found: %s", name)
	}
	return fn.Invoke(ctx, input)
}

// ============== 便捷函数 ==============

// NewFunc 从普通函数创建 Native 函数。
//
// 该便捷构造为保持简洁签名不返回 error，但绝不能吞掉底层 NewNativeFunction 的错误后
// 返回 nil（否则调用方对 nil 解引用会在后续 Invoke 处发生隐蔽 panic）。
// 因此当底层构造失败（如 name 为空）时，本函数 panic 并附带清晰的错误信息。
// 由泛型签名保证 fn 非 nil 且为函数类型，故唯一可能的失败为 name 为空。
func NewFunc[I, O any](name, description string, fn func(ctx context.Context, input I) (O, error)) *NativeFunction {
	nf, err := NewNativeFunction(NativeFunctionConfig{
		Name:         name,
		Description:  description,
		Fn:           fn,
		InputSchema:  llm.SchemaOf[I](),
		OutputSchema: llm.SchemaOf[O](),
	})
	if err != nil {
		panic(fmt.Errorf("NewFunc(%q): %w", name, err))
	}
	return nf
}

// Chain 创建函数链
func Chain(name, description string, functions ...Function) *CompositeFunction {
	return NewCompositeFunction(name, description, functions...)
}

// ============== 全局注册表 ==============

var (
	globalRegistry     *Registry
	globalRegistryOnce sync.Once
)

// GlobalRegistry 获取全局注册表
func GlobalRegistry() *Registry {
	globalRegistryOnce.Do(func() {
		globalRegistry = NewRegistry()
	})
	return globalRegistry
}

// Register 注册到全局注册表
func Register(fn Function) error {
	return GlobalRegistry().Register(fn)
}

// Invoke 从全局注册表调用函数
func Invoke(ctx context.Context, name string, input map[string]any) (any, error) {
	return GlobalRegistry().Invoke(ctx, name, input)
}
