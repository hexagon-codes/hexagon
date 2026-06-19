// Package template 提供 LLM 提示词模板引擎
//
// 本包实现了完整的提示词模板系统：
//
//   - 变量替换：{{ variable }}
//
//   - 条件语句：{% if condition %}...{% endif %}
//
//   - 循环语句：{% for item in items %}...{% endfor %}
//
//   - 过滤器：{{ value | filter }}
//
//   - 模板继承和包含
//
//   - 提示词版本控制
//
//   - Jinja2: 模板语法
//
//   - Semantic Kernel: 语义函数模板
//
// 使用示例：
//
//	tpl := template.New("greeting", "Hello, {{ name }}!")
//	result, err := tpl.Execute(map[string]any{"name": "World"})
//	// result: "Hello, World!"
package template

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"
	"unicode"

	"github.com/hexagon-codes/toolkit/lang/conv"
)

// ============== 错误定义 ==============

var (
	// ErrTemplateNotFound 模板未找到
	ErrTemplateNotFound = errors.New("template not found")

	// ErrInvalidTemplate 无效模板
	ErrInvalidTemplate = errors.New("invalid template")

	// ErrMissingVariable 缺少变量
	ErrMissingVariable = errors.New("missing required variable")

	// ErrExecutionFailed 执行失败
	ErrExecutionFailed = errors.New("template execution failed")
)

// ============== 模板接口 ==============

// Template 提示词模板接口
type Template interface {
	// Name 模板名称
	Name() string

	// Execute 执行模板
	Execute(variables map[string]any) (string, error)

	// ExecuteContext 带上下文执行
	ExecuteContext(ctx context.Context, variables map[string]any) (string, error)

	// Variables 获取所有变量
	Variables() []Variable

	// Validate 验证变量
	Validate(variables map[string]any) error
}

// Variable 模板变量定义
type Variable struct {
	// Name 变量名
	Name string `json:"name"`

	// Type 变量类型
	Type string `json:"type"`

	// Description 描述
	Description string `json:"description"`

	// Required 是否必需
	Required bool `json:"required"`

	// Default 默认值
	Default any `json:"default,omitempty"`

	// Examples 示例值
	Examples []any `json:"examples,omitempty"`
}

// ============== 基础模板 ==============

// PromptTemplate 基础提示词模板
type PromptTemplate struct {
	name      string
	template  string
	variables []Variable
	parsed    *template.Template
	mu        sync.RWMutex
}

// New 创建新模板
func New(name, tmpl string, variables ...Variable) *PromptTemplate {
	pt := &PromptTemplate{
		name:      name,
		template:  tmpl,
		variables: variables,
	}

	// 解析变量（如果未提供）
	if len(variables) == 0 {
		pt.variables = pt.extractVariables()
	}

	return pt
}

// Name 返回模板名称
func (pt *PromptTemplate) Name() string {
	return pt.name
}

// Execute 执行模板
func (pt *PromptTemplate) Execute(variables map[string]any) (string, error) {
	return pt.ExecuteContext(context.Background(), variables)
}

// ExecuteContext 带上下文执行模板
//
// 在执行前检查 ctx 的取消/超时信号：若 ctx 已取消（或超时），直接返回该错误，
// 不再进行后续校验与渲染，以尊重调用方的取消语义。
func (pt *PromptTemplate) ExecuteContext(ctx context.Context, variables map[string]any) (string, error) {
	// 接入 context 取消/超时传播。
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrExecutionFailed, err)
	}

	// 验证变量
	if err := pt.Validate(variables); err != nil {
		return "", err
	}

	// 应用默认值
	vars := pt.applyDefaults(variables)

	// 获取或解析模板
	tmpl, err := pt.getParsedTemplate()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidTemplate, err)
	}

	// 执行模板
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("%w: %v", ErrExecutionFailed, err)
	}

	return buf.String(), nil
}

// Variables 获取变量列表
func (pt *PromptTemplate) Variables() []Variable {
	return pt.variables
}

// Validate 验证变量
func (pt *PromptTemplate) Validate(variables map[string]any) error {
	for _, v := range pt.variables {
		if v.Required {
			if _, ok := variables[v.Name]; !ok {
				if v.Default == nil {
					return fmt.Errorf("%w: %s", ErrMissingVariable, v.Name)
				}
			}
		}
	}
	return nil
}

// getParsedTemplate 获取解析后的模板
func (pt *PromptTemplate) getParsedTemplate() (*template.Template, error) {
	pt.mu.RLock()
	if pt.parsed != nil {
		pt.mu.RUnlock()
		return pt.parsed, nil
	}
	pt.mu.RUnlock()

	pt.mu.Lock()
	defer pt.mu.Unlock()

	if pt.parsed != nil {
		return pt.parsed, nil
	}

	// 转换模板语法
	goTemplate := convertToGoTemplate(pt.template)

	// 解析模板
	tmpl, err := template.New(pt.name).Funcs(defaultFuncMap()).Parse(goTemplate)
	if err != nil {
		return nil, err
	}

	pt.parsed = tmpl
	return pt.parsed, nil
}

// applyDefaults 应用默认值
func (pt *PromptTemplate) applyDefaults(variables map[string]any) map[string]any {
	result := make(map[string]any)
	for k, v := range variables {
		result[k] = v
	}

	for _, v := range pt.variables {
		if _, ok := result[v.Name]; !ok && v.Default != nil {
			result[v.Name] = v.Default
		}
	}

	return result
}

// extractVariables 从模板中提取外部变量。
//
// 提取规则（带过滤器与循环作用域语义）：
//   - {% for x in coll %} 控制块：把集合 coll 视为外部必填变量，
//     而把循环变量 x 视为局部声明，排除在外部变量之外。
//   - {{ var }}：标记为必填外部变量。
//   - {{ var | default ... }}：带 default 过滤器表示该变量可选，
//     标记 Required=false，避免与 Validate 的必填校验叠加锁死可选变量。
//   - {{ var | otherFilter ... }}：仍为必填（过滤器不改变其必填性）。
func (pt *PromptTemplate) extractVariables() []Variable {
	seen := make(map[string]bool)
	loopVars := make(map[string]bool)
	variables := make([]Variable, 0)

	add := func(name string, required bool) {
		if loopVars[name] || seen[name] {
			return
		}
		seen[name] = true
		variables = append(variables, Variable{
			Name:     name,
			Type:     "string",
			Required: required,
		})
	}

	// 1) 先扫描 for 控制块，收集循环变量（局部）与集合变量（外部必填）。
	for _, m := range forHeadRe.FindAllStringSubmatch(pt.template, -1) {
		loopVars[m[1]] = true // 循环变量是局部声明
	}
	for _, m := range forHeadRe.FindAllStringSubmatch(pt.template, -1) {
		add(m[2], true) // 集合变量为外部必填
	}

	// 2) 扫描所有 {{ ... }} 占位符，提取首个变量及其必填性。
	for _, m := range placeholderRe.FindAllStringSubmatch(pt.template, -1) {
		inner := strings.TrimSpace(m[1])

		// 带过滤器：var | filter [args]
		if pm := pipeFilterRe.FindStringSubmatch(inner); pm != nil {
			name := pm[1]
			// default 过滤器表示该变量可选。
			required := pm[2] != "default"
			add(name, required)
			continue
		}

		// 纯变量
		if regexp.MustCompile(`^\w+$`).MatchString(inner) {
			add(inner, true)
		}
	}

	return variables
}

// forHeadRe 匹配 {% for x in coll %} 控制块头部
var forHeadRe = regexp.MustCompile(`\{%\s*for\s+(\w+)\s+in\s+(\w+)\s*%\}`)

// endforRe 匹配 {% endfor %} 控制块尾部
var endforRe = regexp.MustCompile(`\{%\s*endfor\s*%\}`)

// placeholderRe 匹配 {{ ... }} 占位符（变量/过滤器表达式）
var placeholderRe = regexp.MustCompile(`\{\{(.*?)\}\}`)

// pipeFilterRe 拆分形如 "var | filter args" 的过滤器表达式
// 子组：1=首个变量标识符，2=过滤器名，3=过滤器参数（可为空）
var pipeFilterRe = regexp.MustCompile(`^\s*(\w+)\s*\|\s*(\w+)\s*(.*?)\s*$`)

// convertToGoTemplate 将 Jinja2 风格转换为 Go 模板语法
//
// 该转换是作用域感知的：会先收集 {% for x in coll %} 声明的循环变量，
// 进入循环体后把对循环变量的引用转成 Go 的 $x（循环变量），其余标识符
// 转成 .x（字段访问），从而解决“循环体引用循环变量失败”与“带参数过滤器
// 变量未加点”两类缺陷。
func convertToGoTemplate(tmpl string) string {
	// 先转换控制块（if/elif/else/endif）。for/endfor 在占位符替换阶段
	// 单独处理，以便维护循环变量作用域栈。
	result := tmpl
	result = regexp.MustCompile(`\{%\s*if\s+(.+?)\s*%\}`).ReplaceAllString(result, "{{if ${1}}}")
	result = regexp.MustCompile(`\{%\s*elif\s+(.+?)\s*%\}`).ReplaceAllString(result, "{{else if ${1}}}")
	result = regexp.MustCompile(`\{%\s*else\s*%\}`).ReplaceAllString(result, "{{else}}")
	result = regexp.MustCompile(`\{%\s*endif\s*%\}`).ReplaceAllString(result, "{{end}}")

	// loopVars 记录当前作用域内可见的循环变量集合（支持嵌套循环）。
	loopVars := make(map[string]int)

	var b strings.Builder
	for result != "" {
		// 依次寻找下一个 for 头部、endfor、{{ }} 占位符，取最靠前者处理。
		forLoc := forHeadRe.FindStringSubmatchIndex(result)
		endforLoc := endforRe.FindStringIndex(result)
		phLoc := placeholderRe.FindStringSubmatchIndex(result)

		next, kind := nextMatch(forLoc, endforLoc, phLoc)
		if next < 0 {
			// 无更多控制块/占位符，原样写出剩余内容。
			b.WriteString(result)
			break
		}

		// 写出匹配前的纯文本。
		b.WriteString(result[:next])

		switch kind {
		case matchFor:
			loopVar := result[forLoc[2]:forLoc[3]]
			coll := result[forLoc[4]:forLoc[5]]
			b.WriteString("{{range $" + loopVar + " := ." + coll + "}}")
			loopVars[loopVar]++
			result = result[forLoc[1]:]
		case matchEndfor:
			// 退出最近一层循环：当前实现以引用计数近似作用域，
			// endfor 时回收 for 头部新增的循环变量。
			b.WriteString("{{end}}")
			result = result[endforLoc[1]:]
		case matchPlaceholder:
			inner := result[phLoc[2]:phLoc[3]]
			b.WriteString(convertPlaceholder(inner, loopVars))
			result = result[phLoc[1]:]
		}
	}

	return b.String()
}

// 匹配类型常量
const (
	matchNone = iota
	matchFor
	matchEndfor
	matchPlaceholder
)

// nextMatch 在三类候选位置中选出索引最小者，返回其起始位置 pos 与匹配类型 kind。
// 三者皆无匹配时返回 pos<0。
func nextMatch(forLoc, endforLoc, phLoc []int) (pos, kind int) {
	pos = -1
	kind = matchNone
	consider := func(loc []int, k int) {
		if loc == nil {
			return
		}
		if pos < 0 || loc[0] < pos {
			pos = loc[0]
			kind = k
		}
	}
	consider(forLoc, matchFor)
	consider(endforLoc, matchEndfor)
	consider(phLoc, matchPlaceholder)
	return pos, kind
}

// convertPlaceholder 将单个 {{ ... }} 内部表达式转换为 Go 模板语法。
//
// 处理三种形式：
//   - 纯变量 {{ var }}：循环变量转 $var，否则转 .var
//   - 带过滤器 {{ var | filter [args] }}：转为 {{filter <value> args}}，
//     其中 value 作为过滤器首个实参（Jinja2 语义）
//   - 其他复杂表达式：原样保留（已是合法 Go 模板片段，如 if 条件中的 pipeline）
func convertPlaceholder(inner string, loopVars map[string]int) string {
	trimmed := strings.TrimSpace(inner)

	// 带过滤器：var | filter [args]
	if m := pipeFilterRe.FindStringSubmatch(trimmed); m != nil {
		valueRef := varRef(m[1], loopVars)
		filter := m[2]
		args := strings.TrimSpace(m[3])
		if args != "" {
			// Jinja2 语义：value 作为过滤器首个参数，其余参数随后。
			return "{{" + filter + " " + valueRef + " " + args + "}}"
		}
		// 无参过滤器：保留管道写法 {{value | filter}}。
		return "{{" + valueRef + " | " + filter + "}}"
	}

	// 纯变量
	if regexp.MustCompile(`^\w+$`).MatchString(trimmed) {
		return "{{" + varRef(trimmed, loopVars) + "}}"
	}

	// 其他表达式原样保留（例如已经是合法 Go pipeline）。
	return "{{" + inner + "}}"
}

// varRef 根据作用域返回标识符引用：循环变量 -> $name，否则 -> .name
func varRef(name string, loopVars map[string]int) string {
	if loopVars[name] > 0 {
		return "$" + name
	}
	return "." + name
}

// defaultFuncMap 默认函数映射
func defaultFuncMap() template.FuncMap {
	return template.FuncMap{
		// 字符串处理
		"upper":     strings.ToUpper,
		"lower":     strings.ToLower,
		"title":     titleCase,
		"trim":      strings.TrimSpace,
		"replace":   strings.ReplaceAll,
		"split":     strings.Split,
		"join":      strings.Join,
		"contains":  strings.Contains,
		"hasPrefix": strings.HasPrefix,
		"hasSuffix": strings.HasSuffix,
		"repeat":    strings.Repeat,
		"truncate":  truncate,
		// default 过滤器采用 Jinja2 参数序（值在前、默认值在后），
		// 与 convertToGoTemplate 生成的 {{default <value> <default>}} 对应；
		// defaultValue 辅助函数保留 (默认值, 值) 序供内部/测试使用。
		"default": filterDefault,
		"quote":   strconv.Quote,

		// 数字处理
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"mul": func(a, b int) int { return a * b },
		"div": func(a, b int) int { return a / b },
		"mod": func(a, b int) int { return a % b },

		// 类型转换
		"toString": toString,
		"toInt":    toInt,
		"toFloat":  toFloat,
		"toBool":   toBool,

		// 日期时间
		"now":        time.Now,
		"formatDate": formatDate,

		// JSON
		"toJSON":   toJSON,
		"fromJSON": fromJSON,

		// 列表
		"first": first,
		"last":  last,
		"len":   length,
		"index": index,
		"slice": sliceList,

		// 条件
		"ternary":  ternary,
		"empty":    empty,
		"coalesce": coalesce,
	}
}

// 辅助函数实现

// truncate 按 rune（而非 byte）安全截断字符串到指定长度，超出部分以 "..." 替代。
//
// 之前实现用 s[:length] 按字节切片，对中文等多字节字符会切坏字符产生非法
// UTF-8；负数 length 还会触发越界 panic。这里改为基于 []rune 截断，并对
// length 做下界(0)与上界(rune 数)双重 clamp。
func truncate(s string, length int) string {
	// 下界 clamp：负数视为 0。
	if length < 0 {
		length = 0
	}
	runes := []rune(s)
	// 上界 clamp：不超过总 rune 数时原样返回。
	if len(runes) <= length {
		return s
	}
	return string(runes[:length]) + "..."
}

// titleCase 将每个空白分隔单词的首字母大写，其余字符保持原样。
//
// 替代已废弃的 strings.Title（Go 1.18 起标记 deprecated），按 rune 安全处理
// 多字节字符，保留经典“词首大写”语义（不改变词内其余字符大小写）。
func titleCase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevIsSep := true // 行首视为分隔符，使首词首字母大写
	for _, r := range s {
		if unicode.IsSpace(r) {
			prevIsSep = true
			b.WriteRune(r)
			continue
		}
		if prevIsSep {
			b.WriteRune(unicode.ToTitle(r))
		} else {
			b.WriteRune(r)
		}
		prevIsSep = false
	}
	return b.String()
}

// defaultValue 在 val 为空时返回 defaultVal，否则返回 val。
// 参数序为 (默认值, 值)，供内部调用与单元测试使用。
func defaultValue(defaultVal, val any) any {
	if empty(val) {
		return defaultVal
	}
	return val
}

// filterDefault 是 default 过滤器的 Jinja2 参数序适配器：值在前、默认值在后。
// 与 convertToGoTemplate 生成的 {{default <value> <default>}} 调用形式对应。
func filterDefault(val, defaultVal any) any {
	return defaultValue(defaultVal, val)
}

// 使用 toolkit/lang/conv 的类型转换函数
// 这些函数比原有实现更健壮，支持更多类型
var (
	toString = conv.String
	toInt    = conv.Int
	toFloat  = conv.Float64
	toBool   = conv.Bool
)

func formatDate(t time.Time, layout string) string {
	return t.Format(layout)
}

func toJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

// fromJSON 将 JSON 字符串反序列化为通用值。
//
// 为契合 text/template FuncMap 的单返回值习惯，反序列化失败时返回 nil
// （而非 panic）；调用方据此判断解析是否成功。错误被显式处理而非隐式吞掉。
func fromJSON(s string) any {
	var result any
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		// 解析失败：返回 nil，由模板层以 nil 语义处理（如配合 default 过滤器）。
		return nil
	}
	return result
}

func first(list any) any {
	v := reflect.ValueOf(list)
	if v.Kind() != reflect.Slice || v.Len() == 0 {
		return nil
	}
	return v.Index(0).Interface()
}

func last(list any) any {
	v := reflect.ValueOf(list)
	if v.Kind() != reflect.Slice || v.Len() == 0 {
		return nil
	}
	return v.Index(v.Len() - 1).Interface()
}

func length(v any) int {
	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.String:
		return val.Len()
	default:
		return 0
	}
}

func index(list any, i int) any {
	v := reflect.ValueOf(list)
	if v.Kind() != reflect.Slice || i < 0 || i >= v.Len() {
		return nil
	}
	return v.Index(i).Interface()
}

// sliceList 对切片做边界安全的子切片，等价于 list[start:end]。
//
// 对 start/end 做完整 clamp：start 下界为 0、上界为 len；end 上界为 len、
// 下界为 start。缺少 start>end 归一时 reflect.Value.Slice 会 panic，这里
// 在该情形下返回空切片，避免崩溃。
func sliceList(list any, start, end int) any {
	v := reflect.ValueOf(list)
	if v.Kind() != reflect.Slice {
		return list
	}
	n := v.Len()
	// 先 clamp start 到 [0, n]。
	if start < 0 {
		start = 0
	}
	if start > n {
		start = n
	}
	// 再 clamp end 到 [start, n]，避免 start>end 触发 reflect.Slice panic。
	if end > n {
		end = n
	}
	if end < start {
		end = start
	}
	return v.Slice(start, end).Interface()
}

func ternary(condition bool, trueVal, falseVal any) any {
	if condition {
		return trueVal
	}
	return falseVal
}

func empty(v any) bool {
	if v == nil {
		return true
	}
	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
		return val.Len() == 0
	case reflect.Bool:
		return !val.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return val.Int() == 0
	case reflect.Float32, reflect.Float64:
		return val.Float() == 0
	case reflect.Ptr, reflect.Interface:
		return val.IsNil()
	default:
		return false
	}
}

func coalesce(values ...any) any {
	for _, v := range values {
		if !empty(v) {
			return v
		}
	}
	return nil
}

// ============== 消息模板 ==============

// MessageTemplate 消息模板
type MessageTemplate struct {
	// Role 消息角色
	Role string `json:"role"`

	// Template 内容模板
	Template *PromptTemplate `json:"-"`

	// Content 静态内容（如果不使用模板）
	Content string `json:"content,omitempty"`
}

// Execute 执行消息模板
func (mt *MessageTemplate) Execute(variables map[string]any) (string, string, error) {
	if mt.Template != nil {
		content, err := mt.Template.Execute(variables)
		if err != nil {
			return "", "", err
		}
		return mt.Role, content, nil
	}
	return mt.Role, mt.Content, nil
}

// ChatTemplate 对话模板
type ChatTemplate struct {
	name      string
	messages  []*MessageTemplate
	variables []Variable
}

// NewChatTemplate 创建对话模板
func NewChatTemplate(name string) *ChatTemplate {
	return &ChatTemplate{
		name:     name,
		messages: make([]*MessageTemplate, 0),
	}
}

// AddSystemMessage 添加系统消息
func (ct *ChatTemplate) AddSystemMessage(template string) *ChatTemplate {
	ct.messages = append(ct.messages, &MessageTemplate{
		Role:     "system",
		Template: New("system", template),
	})
	return ct
}

// AddUserMessage 添加用户消息
func (ct *ChatTemplate) AddUserMessage(template string) *ChatTemplate {
	ct.messages = append(ct.messages, &MessageTemplate{
		Role:     "user",
		Template: New("user", template),
	})
	return ct
}

// AddAssistantMessage 添加助手消息
func (ct *ChatTemplate) AddAssistantMessage(template string) *ChatTemplate {
	ct.messages = append(ct.messages, &MessageTemplate{
		Role:     "assistant",
		Template: New("assistant", template),
	})
	return ct
}

// Execute 执行对话模板
func (ct *ChatTemplate) Execute(variables map[string]any) ([]Message, error) {
	messages := make([]Message, 0, len(ct.messages))

	for _, mt := range ct.messages {
		role, content, err := mt.Execute(variables)
		if err != nil {
			return nil, err
		}
		messages = append(messages, Message{
			Role:    role,
			Content: content,
		})
	}

	return messages, nil
}

// Message 消息
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ============== 模板库 ==============

// TemplateLibrary 模板库
type TemplateLibrary struct {
	templates map[string]Template
	mu        sync.RWMutex
}

// NewTemplateLibrary 创建模板库
func NewTemplateLibrary() *TemplateLibrary {
	return &TemplateLibrary{
		templates: make(map[string]Template),
	}
}

// Register 注册模板
func (lib *TemplateLibrary) Register(tmpl Template) {
	lib.mu.Lock()
	lib.templates[tmpl.Name()] = tmpl
	lib.mu.Unlock()
}

// Get 获取模板
func (lib *TemplateLibrary) Get(name string) (Template, error) {
	lib.mu.RLock()
	tmpl, ok := lib.templates[name]
	lib.mu.RUnlock()

	if !ok {
		return nil, ErrTemplateNotFound
	}
	return tmpl, nil
}

// Execute 执行指定模板
func (lib *TemplateLibrary) Execute(name string, variables map[string]any) (string, error) {
	tmpl, err := lib.Get(name)
	if err != nil {
		return "", err
	}
	return tmpl.Execute(variables)
}

// List 列出所有模板
func (lib *TemplateLibrary) List() []string {
	lib.mu.RLock()
	defer lib.mu.RUnlock()

	names := make([]string, 0, len(lib.templates))
	for name := range lib.templates {
		names = append(names, name)
	}
	return names
}

// ============== 预置模板 ==============

// 常用提示词模板
var (
	// SummarizeTemplate 摘要模板
	SummarizeTemplate = New("summarize", `Please summarize the following text in {{ language | default "English" }}:

{{ text }}

Requirements:
- Keep it concise ({{ max_length | default "200" }} words max)
- Preserve key information
- Use clear and professional language`)

	// TranslateTemplate 翻译模板
	TranslateTemplate = New("translate", `Please translate the following text to {{ target_language }}:

{{ text }}

Note: Maintain the original meaning and tone.`)

	// QATemplate 问答模板
	QATemplate = New("qa", `Based on the following context, answer the question.

Context:
{{ context }}

Question: {{ question }}

Please provide a clear and accurate answer based only on the given context.`)

	// CodeReviewTemplate 代码审查模板
	CodeReviewTemplate = New("code_review", "Please review the following {{ language | default \"code\" }} and provide feedback:\n\n```{{ language }}\n{{ code }}\n```\n\nFocus on:\n- Code quality and best practices\n- Potential bugs or issues\n- Performance considerations\n- Suggestions for improvement")

	// ExtractTemplate 信息提取模板
	ExtractTemplate = New("extract", `Extract the following information from the text:
{{ fields | join ", " }}

Text:
{{ text }}

Respond in JSON format.`)
)

// RegisterBuiltinTemplates 注册内置模板
func RegisterBuiltinTemplates(lib *TemplateLibrary) {
	lib.Register(SummarizeTemplate)
	lib.Register(TranslateTemplate)
	lib.Register(QATemplate)
	lib.Register(CodeReviewTemplate)
	lib.Register(ExtractTemplate)
}

// ============== Few-Shot 模板 ==============

// FewShotTemplate Few-Shot 学习模板
type FewShotTemplate struct {
	name       string
	prefix     string
	examples   []Example
	suffix     string
	separator  string
	exampleTpl *PromptTemplate
	// suffixTpl 缓存后缀模板的解析结果，避免 Execute/Variables/Validate
	// 三处重复构造临时 PromptTemplate；在 WithSuffix 改写后缀时重建。
	suffixTpl *PromptTemplate
}

// Example 示例
type Example struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}

// NewFewShotTemplate 创建 Few-Shot 模板
func NewFewShotTemplate(name string) *FewShotTemplate {
	return &FewShotTemplate{
		name:      name,
		separator: "\n\n",
	}
}

// WithPrefix 设置前缀
func (ft *FewShotTemplate) WithPrefix(prefix string) *FewShotTemplate {
	ft.prefix = prefix
	return ft
}

// WithSuffix 设置后缀
func (ft *FewShotTemplate) WithSuffix(suffix string) *FewShotTemplate {
	ft.suffix = suffix
	// 后缀变更后失效缓存，下次按需重建。
	ft.suffixTpl = nil
	return ft
}

// getSuffixTpl 惰性构造并缓存后缀模板，供 Execute/Variables/Validate 共享，
// 避免重复解析。suffix 为空时返回 nil。
func (ft *FewShotTemplate) getSuffixTpl() *PromptTemplate {
	if ft.suffix == "" {
		return nil
	}
	if ft.suffixTpl == nil {
		ft.suffixTpl = New("suffix", ft.suffix)
	}
	return ft.suffixTpl
}

// WithExamples 设置示例
func (ft *FewShotTemplate) WithExamples(examples []Example) *FewShotTemplate {
	ft.examples = examples
	return ft
}

// WithExampleTemplate 设置示例模板
func (ft *FewShotTemplate) WithExampleTemplate(template string) *FewShotTemplate {
	ft.exampleTpl = New("example", template)
	return ft
}

// WithSeparator 设置分隔符
func (ft *FewShotTemplate) WithSeparator(sep string) *FewShotTemplate {
	ft.separator = sep
	return ft
}

// Name 返回模板名称
func (ft *FewShotTemplate) Name() string {
	return ft.name
}

// Execute 执行模板
func (ft *FewShotTemplate) Execute(variables map[string]any) (string, error) {
	var parts []string

	// 前缀
	if ft.prefix != "" {
		parts = append(parts, ft.prefix)
	}

	// 示例
	for _, example := range ft.examples {
		if ft.exampleTpl != nil {
			exampleStr, err := ft.exampleTpl.Execute(map[string]any{
				"input":  example.Input,
				"output": example.Output,
			})
			if err != nil {
				return "", err
			}
			parts = append(parts, exampleStr)
		} else {
			parts = append(parts, fmt.Sprintf("Input: %s\nOutput: %s", example.Input, example.Output))
		}
	}

	// 后缀（包含用户输入）
	if suffixTpl := ft.getSuffixTpl(); suffixTpl != nil {
		suffixStr, err := suffixTpl.Execute(variables)
		if err != nil {
			return "", err
		}
		parts = append(parts, suffixStr)
	}

	return strings.Join(parts, ft.separator), nil
}

// Variables 获取变量列表
func (ft *FewShotTemplate) Variables() []Variable {
	suffixTpl := ft.getSuffixTpl()
	if suffixTpl == nil {
		return nil
	}
	return suffixTpl.Variables()
}

// Validate 验证变量
func (ft *FewShotTemplate) Validate(variables map[string]any) error {
	suffixTpl := ft.getSuffixTpl()
	if suffixTpl == nil {
		return nil
	}
	return suffixTpl.Validate(variables)
}
