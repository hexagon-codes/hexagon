package template

// audit_wave8_test.go 是针对 llm/template 包的全场景审计测试。
//
// 测试策略：
//   - 正常路径：变量替换、过滤器、条件、循环、链式 API。
//   - 边界：空/nil/超长/Unicode/0/负数/并发。
//   - 错误路径：缺失变量、无效模板、找不到模板。
//   - 状态一致性：解析缓存、库注册并发。
//
// 失败的测试用例代表"断言了正确预期但被代码违反"，即疑似真实缺陷。
// 通过的测试用例固化了当前正确行为，用于防回归。

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// ============== New / 基础属性 ==============

// TestNew_NameAndTemplate 验证 New 正确保存名称并能取回。
func TestNew_NameAndTemplate(t *testing.T) {
	pt := New("greeting", "Hello, {{ name }}!")
	if pt.Name() != "greeting" {
		t.Errorf("Name() = %q, 期望 %q", pt.Name(), "greeting")
	}
}

// TestExtractVariables 验证从模板自动提取变量。
func TestExtractVariables(t *testing.T) {
	tests := []struct {
		name     string
		tmpl     string
		wantVars []string // 期望提取的变量名集合（顺序无关）
	}{
		{"单变量", "Hello {{ name }}", []string{"name"}},
		{"多变量", "{{ a }} {{ b }} {{ c }}", []string{"a", "b", "c"}},
		{"去重", "{{ x }} {{ x }} {{ x }}", []string{"x"}},
		{"无变量", "no vars here", []string{}},
		{"带过滤器", "{{ name | upper }}", []string{"name"}},
		{"空模板", "", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt := New("t", tt.tmpl)
			got := make(map[string]bool)
			for _, v := range pt.Variables() {
				got[v.Name] = true
			}
			if len(got) != len(tt.wantVars) {
				t.Errorf("提取变量数 = %d %v, 期望 %d %v", len(got), pt.Variables(), len(tt.wantVars), tt.wantVars)
			}
			for _, w := range tt.wantVars {
				if !got[w] {
					t.Errorf("缺少期望变量 %q, 实际 %v", w, pt.Variables())
				}
			}
		})
	}
}

// TestNew_WithExplicitVariables 显式提供变量时不应自动提取。
func TestNew_WithExplicitVariables(t *testing.T) {
	pt := New("t", "{{ a }} {{ b }}", Variable{Name: "a", Required: false})
	if len(pt.Variables()) != 1 {
		t.Errorf("显式变量数 = %d, 期望 1（不应触发自动提取）", len(pt.Variables()))
	}
}

// ============== Execute 正常路径 ==============

// TestExecute_BasicSubstitution 基本变量替换。
func TestExecute_BasicSubstitution(t *testing.T) {
	pt := New("g", "Hello, {{ name }}!")
	got, err := pt.Execute(map[string]any{"name": "World"})
	if err != nil {
		t.Fatalf("Execute 出错: %v", err)
	}
	if got != "Hello, World!" {
		t.Errorf("结果 = %q, 期望 %q", got, "Hello, World!")
	}
}

// TestExecute_MultipleVars 多变量替换。
func TestExecute_MultipleVars(t *testing.T) {
	pt := New("m", "{{ greeting }}, {{ name }}!")
	got, err := pt.Execute(map[string]any{"greeting": "Hi", "name": "Bob"})
	if err != nil {
		t.Fatalf("Execute 出错: %v", err)
	}
	if got != "Hi, Bob!" {
		t.Errorf("结果 = %q, 期望 %q", got, "Hi, Bob!")
	}
}

// TestExecute_SimpleFilter 单词过滤器（已知可工作）。
func TestExecute_SimpleFilter(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		vars map[string]any
		want string
	}{
		{"upper", "{{ s | upper }}", map[string]any{"s": "abc"}, "ABC"},
		{"lower", "{{ s | lower }}", map[string]any{"s": "ABC"}, "abc"},
		{"trim", "{{ s | trim }}", map[string]any{"s": "  x  "}, "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt := New("t", tt.tmpl)
			got, err := pt.Execute(tt.vars)
			if err != nil {
				t.Fatalf("Execute 出错: %v", err)
			}
			if got != tt.want {
				t.Errorf("结果 = %q, 期望 %q", got, tt.want)
			}
		})
	}
}

// TestExecute_UnicodeVars Unicode 变量值替换。
func TestExecute_UnicodeVars(t *testing.T) {
	pt := New("u", "你好，{{ name }}！")
	got, err := pt.Execute(map[string]any{"name": "世界🌍"})
	if err != nil {
		t.Fatalf("Execute 出错: %v", err)
	}
	if got != "你好，世界🌍！" {
		t.Errorf("结果 = %q", got)
	}
}

// TestExecute_EmptyTemplate 空模板返回空串。
func TestExecute_EmptyTemplate(t *testing.T) {
	pt := New("e", "")
	got, err := pt.Execute(map[string]any{})
	if err != nil {
		t.Fatalf("Execute 出错: %v", err)
	}
	if got != "" {
		t.Errorf("结果 = %q, 期望空", got)
	}
}

// TestExecute_NilVariables nil 变量映射且模板无必填变量。
func TestExecute_NilVariables(t *testing.T) {
	pt := New("n", "static text")
	got, err := pt.Execute(nil)
	if err != nil {
		t.Fatalf("Execute(nil) 出错: %v", err)
	}
	if got != "static text" {
		t.Errorf("结果 = %q", got)
	}
}

// ============== Execute 错误路径 ==============

// TestExecute_MissingRequiredVar 缺失必填变量应返回 ErrMissingVariable。
func TestExecute_MissingRequiredVar(t *testing.T) {
	pt := New("g", "Hello, {{ name }}!")
	_, err := pt.Execute(map[string]any{})
	if !errors.Is(err, ErrMissingVariable) {
		t.Errorf("err = %v, 期望 ErrMissingVariable", err)
	}
}

// TestValidate_RequiredWithDefault 必填但有默认值时不应报缺失。
func TestValidate_RequiredWithDefault(t *testing.T) {
	pt := New("t", "x", Variable{Name: "a", Required: true, Default: "fallback"})
	if err := pt.Validate(map[string]any{}); err != nil {
		t.Errorf("有默认值的必填变量校验应通过, 实际 err=%v", err)
	}
}

// TestExecute_InvalidGoTemplate 无效模板语法应返回 ErrInvalidTemplate。
func TestExecute_InvalidGoTemplate(t *testing.T) {
	// {{ 未闭合
	pt := New("bad", "{{ ")
	_, err := pt.Execute(nil)
	if err == nil {
		t.Fatalf("期望错误, 得到 nil")
	}
	if !errors.Is(err, ErrInvalidTemplate) {
		t.Errorf("err = %v, 期望包裹 ErrInvalidTemplate", err)
	}
}

// ============== 缺陷探测：带参数过滤器 ==============

// TestFilterWithArgument_Truncate 带参数过滤器 {{ s | truncate N }} 应能执行。
//
// 这是 Jinja2 风格的核心特性。convertToGoTemplate 仅处理 {{ var }} 与
// {{ var | singleword }} 两种形式，未给带参数过滤器的变量加点前缀，
// 导致 Go 模板把变量名当函数调用 -> "function s not defined"。
//
// 回归: bug #1 带参数过滤器 {{ var | filter arg }} 完全不可用。
func TestFilterWithArgument_Truncate(t *testing.T) {
	pt := New("t", "{{ s | truncate 3 }}")
	got, err := pt.Execute(map[string]any{"s": "abcdefg"})
	if err != nil {
		t.Fatalf("带参数过滤器执行失败（疑似 bug）: %v", err)
	}
	if got != "abc..." {
		t.Errorf("结果 = %q, 期望 %q", got, "abc...")
	}
}

// TestFilterWithArgument_Default {{ x | default "v" }} 应在缺失时回退默认值。
//
// 同上：default 过滤器带字符串参数，convertToGoTemplate 不识别该形式。
//
// 回归: bug #1 带参数过滤器 {{ var | default "v" }} 不可用。
func TestFilterWithArgument_Default(t *testing.T) {
	pt := New("t", `{{ language | default "English" }}`, Variable{Name: "language", Required: false})
	got, err := pt.Execute(map[string]any{})
	if err != nil {
		t.Fatalf("default 过滤器执行失败（疑似 bug）: %v", err)
	}
	if got != "English" {
		t.Errorf("结果 = %q, 期望 %q", got, "English")
	}
}

// ============== 缺陷探测：内置模板不可用 ==============

// TestBuiltinTemplate_Summarize 内置 SummarizeTemplate 应能用正常输入执行。
//
// 缺陷链：模板含 {{ language | default "English" }}，extractVariables 把
// language 标记为 Required=true 且无 Default，于是 Validate 在缺 language 时
// 直接报 ErrMissingVariable —— 内置模板设计上 language 是可选的（有 default
// 过滤器），却被强制必填，使该模板无法按设计使用。
//
// 回归: bug #2 内置 SummarizeTemplate 可选变量被强制必填导致不可用。
func TestBuiltinTemplate_Summarize(t *testing.T) {
	got, err := SummarizeTemplate.Execute(map[string]any{
		"text": "some long text",
	})
	if err != nil {
		t.Fatalf("SummarizeTemplate 无法用正常输入执行（疑似 bug）: %v", err)
	}
	if !strings.Contains(got, "some long text") {
		t.Errorf("结果未包含正文: %q", got)
	}
}

// TestBuiltinTemplate_Extract 内置 ExtractTemplate 含 {{ fields | join ", " }}。
//
// join 带参数过滤器同样不被 convertToGoTemplate 识别，fields 未加点 ->
// "function fields not defined"。
//
// 回归: bug #4 内置 ExtractTemplate join 带参过滤器 + 变量未加点导致永久报错。
func TestBuiltinTemplate_Extract(t *testing.T) {
	got, err := ExtractTemplate.Execute(map[string]any{
		"fields": []string{"name", "age"},
		"text":   "John is 30",
	})
	if err != nil {
		t.Fatalf("ExtractTemplate 执行失败（疑似 bug）: %v", err)
	}
	if !strings.Contains(got, "name") {
		t.Errorf("结果未包含字段: %q", got)
	}
}

// TestBuiltinTemplate_QA QATemplate 不含带参过滤器，应可正常工作（对照组）。
func TestBuiltinTemplate_QA(t *testing.T) {
	got, err := QATemplate.Execute(map[string]any{
		"context":  "The sky is blue.",
		"question": "What color is the sky?",
	})
	if err != nil {
		t.Fatalf("QATemplate 执行失败: %v", err)
	}
	if !strings.Contains(got, "blue") || !strings.Contains(got, "color") {
		t.Errorf("结果异常: %q", got)
	}
}

// ============== 缺陷探测：for 循环 ==============

// TestForLoop_BodyVariable for 循环体内引用循环变量应能正确取值。
//
// convertToGoTemplate 把 {% for item in items %} 转成 {{range $item := .items}}，
// 但循环体里的 {{ item }} 被转成 {{.item}}（字段访问）而非 {{$item}}（循环变量），
// 导致 "can't evaluate field item in type ..."。循环功能实质不可用。
//
// 回归: bug #3 for 循环体内引用循环变量失败，range 功能实质不可用。
func TestForLoop_BodyVariable(t *testing.T) {
	pt := New("loop", `{% for item in items %}[{{ item }}]{% endfor %}`,
		Variable{Name: "items", Required: true})
	got, err := pt.Execute(map[string]any{"items": []string{"a", "b", "c"}})
	if err != nil {
		t.Fatalf("for 循环体引用循环变量失败（疑似 bug）: %v", err)
	}
	if got != "[a][b][c]" {
		t.Errorf("结果 = %q, 期望 %q", got, "[a][b][c]")
	}
}

// TestForLoop_ExtractsLoopVarNotCollection extractVariables 行为探测：
// 对 {% for item in items %}{{ item }}{% endfor %}，应把集合 items 视为必填，
// 而不是把循环变量 item 视为必填。
//
// 回归: bug #5 extractVariables 对 for 循环提取循环变量而非集合变量。
func TestForLoop_ExtractsLoopVarNotCollection(t *testing.T) {
	pt := New("f", `{% for item in items %}{{ item }}{% endfor %}`)
	names := make(map[string]bool)
	for _, v := range pt.Variables() {
		names[v.Name] = true
	}
	if !names["items"] {
		t.Errorf("应提取集合变量 items, 实际提取: %v（疑似 bug：提取了循环变量而非集合）", pt.Variables())
	}
	if names["item"] {
		t.Errorf("不应把循环变量 item 当作外部必填变量, 实际: %v", pt.Variables())
	}
}

// ============== 缺陷探测：truncate Unicode ==============

// TestTruncate_Unicode truncate 对多字节 Unicode 不应破坏字符或产生非法 UTF-8。
//
// truncate 用 s[:length] 按字节切片，对中文等多字节字符会切到字符中间，
// 产生非法 UTF-8（甚至边界越界）。这里通过直接调用辅助函数验证。
//
// 回归: bug #6 truncate 按字节截断破坏多字节 Unicode 产生非法 UTF-8。
func TestTruncate_Unicode(t *testing.T) {
	// "你好世界" 每个字 3 字节，truncate 到 2 会切坏第一个字符
	got := truncate("你好世界", 2)
	if !utf8.ValidString(got) {
		t.Errorf("truncate(\"你好世界\", 2) = %q 含非法 UTF-8（疑似 bug：按字节而非按 rune 截断）", got)
	}
}

// TestTruncate_ASCII ASCII 场景 truncate 正常工作（对照组）。
func TestTruncate_ASCII(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("不超长应原样返回, 实际 %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate = %q, 期望 %q", got, "hello...")
	}
}

// TestTruncate_NegativeLength truncate 传负数长度不应 panic。
//
// 回归: bug #7 truncate 负长度参数触发 slice 越界 panic。
func TestTruncate_NegativeLength(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("truncate 负长度 panic（疑似 bug）: %v", r)
		}
	}()
	_ = truncate("hello", -1)
}

// ============== 辅助函数：empty / coalesce / ternary ==============

// TestEmpty 验证 empty 对各种零值的判断。
func TestEmpty(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want bool
	}{
		{"nil", nil, true},
		{"空串", "", true},
		{"非空串", "x", false},
		{"空切片", []int{}, true},
		{"非空切片", []int{1}, false},
		{"空map", map[string]int{}, true},
		{"零int", 0, true},
		{"非零int", 5, false},
		{"零float", 0.0, true},
		{"false", false, true},
		{"true", true, false},
		{"空指针", (*int)(nil), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := empty(tt.val); got != tt.want {
				t.Errorf("empty(%v) = %v, 期望 %v", tt.val, got, tt.want)
			}
		})
	}
}

// TestCoalesce 返回第一个非空值。
func TestCoalesce(t *testing.T) {
	if got := coalesce("", 0, nil, "first", "second"); got != "first" {
		t.Errorf("coalesce = %v, 期望 first", got)
	}
	if got := coalesce("", 0, nil); got != nil {
		t.Errorf("全空 coalesce = %v, 期望 nil", got)
	}
}

// TestTernary 三元逻辑。
func TestTernary(t *testing.T) {
	if got := ternary(true, "y", "n"); got != "y" {
		t.Errorf("ternary(true) = %v", got)
	}
	if got := ternary(false, "y", "n"); got != "n" {
		t.Errorf("ternary(false) = %v", got)
	}
}

// TestDefaultValue defaultValue 空值回退。
func TestDefaultValue(t *testing.T) {
	if got := defaultValue("def", ""); got != "def" {
		t.Errorf("空值应回退默认, 实际 %v", got)
	}
	if got := defaultValue("def", "real"); got != "real" {
		t.Errorf("非空值应保留, 实际 %v", got)
	}
}

// ============== 列表辅助函数 ==============

// TestListHelpers first/last/index/length/slice。
func TestListHelpers(t *testing.T) {
	list := []string{"a", "b", "c"}

	if got := first(list); got != "a" {
		t.Errorf("first = %v", got)
	}
	if got := last(list); got != "c" {
		t.Errorf("last = %v", got)
	}
	if got := first([]string{}); got != nil {
		t.Errorf("空切片 first = %v, 期望 nil", got)
	}
	if got := last([]string{}); got != nil {
		t.Errorf("空切片 last = %v, 期望 nil", got)
	}
	if got := length(list); got != 3 {
		t.Errorf("length = %d", got)
	}
	if got := index(list, 1); got != "b" {
		t.Errorf("index(1) = %v", got)
	}
	if got := index(list, 99); got != nil {
		t.Errorf("越界 index = %v, 期望 nil", got)
	}
	if got := index(list, -1); got != nil {
		t.Errorf("负 index = %v, 期望 nil", got)
	}
}

// TestSliceList slice 边界裁剪。
func TestSliceList(t *testing.T) {
	list := []int{1, 2, 3, 4, 5}
	got := sliceList(list, 1, 3)
	gv, ok := got.([]int)
	if !ok || len(gv) != 2 || gv[0] != 2 || gv[1] != 3 {
		t.Errorf("slice(1,3) = %v, 期望 [2 3]", got)
	}
	// end 超界应裁剪
	got2 := sliceList(list, 0, 99)
	if gv2, ok := got2.([]int); !ok || len(gv2) != 5 {
		t.Errorf("end 超界 slice = %v, 期望长度 5", got2)
	}
	// 负 start 归零
	got3 := sliceList(list, -5, 2)
	if gv3, ok := got3.([]int); !ok || len(gv3) != 2 {
		t.Errorf("负 start slice = %v, 期望长度 2", got3)
	}
}

// TestSliceList_StartGreaterThanEnd start>end 时 Go 的 reflect.Slice 会 panic。
// 探测该边界是否被防护。
//
// 回归: bug #8 sliceList 未防护 start>end 触发 reflect.Slice panic。
func TestSliceList_StartGreaterThanEnd(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("slice(start>end) panic（疑似 bug：未防护 start>end）: %v", r)
		}
	}()
	_ = sliceList([]int{1, 2, 3}, 3, 1)
}

// ============== JSON 辅助函数 ==============

// TestToJSON 序列化。
func TestToJSON(t *testing.T) {
	got := toJSON(map[string]int{"a": 1})
	if got != `{"a":1}` {
		t.Errorf("toJSON = %q", got)
	}
}

// TestFromJSON 反序列化。
func TestFromJSON(t *testing.T) {
	got := fromJSON(`{"a":1}`)
	m, ok := got.(map[string]any)
	if !ok || m["a"] != float64(1) {
		t.Errorf("fromJSON = %v (%T)", got, got)
	}
}

// TestFromJSON_Invalid 非法 JSON 应返回 nil 而非 panic（探测静默吞错）。
//
// 回归: bug #10 fromJSON 反序列化失败时显式返回 nil（不 panic、不残留脏值）。
func TestFromJSON_Invalid(t *testing.T) {
	got := fromJSON(`{not json`)
	if got != nil {
		t.Errorf("非法 JSON fromJSON = %v, 期望 nil", got)
	}
}

// TestTitleFilter title 过滤器应将每个单词首字母大写且不破坏多字节字符。
//
// 回归: bug #11 title 过滤器原用已废弃的 strings.Title，改为 rune 安全的 titleCase。
func TestTitleFilter(t *testing.T) {
	pt := New("t", "{{ s | title }}")
	got, err := pt.Execute(map[string]any{"s": "hello world"})
	if err != nil {
		t.Fatalf("title 过滤器执行失败: %v", err)
	}
	if got != "Hello World" {
		t.Errorf("结果 = %q, 期望 %q", got, "Hello World")
	}
	// titleCase 直接调用：多字节场景不应破坏字符。
	if out := titleCase("héllo wörld"); !utf8.ValidString(out) {
		t.Errorf("titleCase 多字节结果含非法 UTF-8: %q", out)
	}
}

// ============== MessageTemplate / ChatTemplate ==============

// TestMessageTemplate_StaticContent 无模板时使用静态内容。
func TestMessageTemplate_StaticContent(t *testing.T) {
	mt := &MessageTemplate{Role: "system", Content: "static"}
	role, content, err := mt.Execute(nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if role != "system" || content != "static" {
		t.Errorf("role=%q content=%q", role, content)
	}
}

// TestMessageTemplate_WithTemplate 有模板时执行模板。
func TestMessageTemplate_WithTemplate(t *testing.T) {
	mt := &MessageTemplate{Role: "user", Template: New("u", "Hi {{ name }}")}
	role, content, err := mt.Execute(map[string]any{"name": "Al"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if role != "user" || content != "Hi Al" {
		t.Errorf("role=%q content=%q", role, content)
	}
}

// TestChatTemplate_FullFlow 链式 API 构建并执行对话模板。
func TestChatTemplate_FullFlow(t *testing.T) {
	ct := NewChatTemplate("chat").
		AddSystemMessage("You are {{ persona }}").
		AddUserMessage("{{ question }}").
		AddAssistantMessage("Sure!")

	msgs, err := ct.Execute(map[string]any{
		"persona":  "helpful",
		"question": "Hello?",
	})
	if err != nil {
		t.Fatalf("Execute err: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("消息数 = %d, 期望 3", len(msgs))
	}
	want := []Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hello?"},
		{Role: "assistant", Content: "Sure!"},
	}
	for i, w := range want {
		if msgs[i].Role != w.Role || msgs[i].Content != w.Content {
			t.Errorf("msg[%d] = %+v, 期望 %+v", i, msgs[i], w)
		}
	}
}

// TestChatTemplate_PropagatesError 某条消息缺变量时应整体报错。
func TestChatTemplate_PropagatesError(t *testing.T) {
	ct := NewChatTemplate("chat").AddUserMessage("{{ missing }}")
	_, err := ct.Execute(map[string]any{})
	if !errors.Is(err, ErrMissingVariable) {
		t.Errorf("err = %v, 期望 ErrMissingVariable", err)
	}
}

// ============== TemplateLibrary ==============

// TestTemplateLibrary_RegisterGetExecute 注册/获取/执行。
func TestTemplateLibrary_RegisterGetExecute(t *testing.T) {
	lib := NewTemplateLibrary()
	lib.Register(New("hi", "Hello {{ name }}"))

	tmpl, err := lib.Get("hi")
	if err != nil {
		t.Fatalf("Get err: %v", err)
	}
	if tmpl.Name() != "hi" {
		t.Errorf("Name = %q", tmpl.Name())
	}

	out, err := lib.Execute("hi", map[string]any{"name": "X"})
	if err != nil {
		t.Fatalf("Execute err: %v", err)
	}
	if out != "Hello X" {
		t.Errorf("out = %q", out)
	}
}

// TestTemplateLibrary_NotFound 取不存在的模板返回 ErrTemplateNotFound。
func TestTemplateLibrary_NotFound(t *testing.T) {
	lib := NewTemplateLibrary()
	_, err := lib.Get("nope")
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("err = %v, 期望 ErrTemplateNotFound", err)
	}
	_, err = lib.Execute("nope", nil)
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("Execute err = %v, 期望 ErrTemplateNotFound", err)
	}
}

// TestTemplateLibrary_List 列出已注册模板。
func TestTemplateLibrary_List(t *testing.T) {
	lib := NewTemplateLibrary()
	lib.Register(New("a", "x"))
	lib.Register(New("b", "y"))
	list := lib.List()
	if len(list) != 2 {
		t.Errorf("List 长度 = %d, 期望 2", len(list))
	}
}

// TestRegisterBuiltinTemplates 注册内置模板后可全部取回。
func TestRegisterBuiltinTemplates(t *testing.T) {
	lib := NewTemplateLibrary()
	RegisterBuiltinTemplates(lib)
	for _, name := range []string{"summarize", "translate", "qa", "code_review", "extract"} {
		if _, err := lib.Get(name); err != nil {
			t.Errorf("内置模板 %q 未注册: %v", name, err)
		}
	}
}

// ============== FewShotTemplate ==============

// TestFewShot_StaticExamples 使用默认格式渲染示例。
func TestFewShot_StaticExamples(t *testing.T) {
	ft := NewFewShotTemplate("fs").
		WithPrefix("Examples:").
		WithExamples([]Example{
			{Input: "hi", Output: "hello"},
			{Input: "bye", Output: "goodbye"},
		}).
		WithSuffix("Now answer: {{ q }}")

	got, err := ft.Execute(map[string]any{"q": "test"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "Examples:") ||
		!strings.Contains(got, "Input: hi") ||
		!strings.Contains(got, "Now answer: test") {
		t.Errorf("结果缺少预期片段: %q", got)
	}
}

// TestFewShot_CustomExampleTemplate 自定义示例模板。
func TestFewShot_CustomExampleTemplate(t *testing.T) {
	ft := NewFewShotTemplate("fs").
		WithExampleTemplate("Q: {{ input }} A: {{ output }}").
		WithExamples([]Example{{Input: "1+1", Output: "2"}})
	got, err := ft.Execute(map[string]any{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "Q: 1+1 A: 2" {
		t.Errorf("结果 = %q", got)
	}
}

// TestFewShot_NoSuffix 无 suffix 时 Variables/Validate 返回空。
func TestFewShot_NoSuffix(t *testing.T) {
	ft := NewFewShotTemplate("fs")
	if ft.Variables() != nil {
		t.Errorf("无 suffix Variables 应为 nil, 实际 %v", ft.Variables())
	}
	if err := ft.Validate(map[string]any{}); err != nil {
		t.Errorf("无 suffix Validate 应通过, 实际 %v", err)
	}
}

// TestFewShot_Name 名称访问。
func TestFewShot_Name(t *testing.T) {
	if NewFewShotTemplate("xyz").Name() != "xyz" {
		t.Error("Name 不一致")
	}
}

// TestFewShot_SuffixTemplateCached 验证 suffix 模板被缓存复用：
// Execute/Variables/Validate 共享同一解析结果，且 WithSuffix 改写后缓存失效。
//
// 回归: bug #12 FewShotTemplate suffix 三处重复构造临时 PromptTemplate。
func TestFewShot_SuffixTemplateCached(t *testing.T) {
	ft := NewFewShotTemplate("fs").WithSuffix("Now answer: {{ q }}")

	// 触发缓存构建并验证三个方法共享同一缓存指针。
	_ = ft.Variables()
	first := ft.suffixTpl
	if first == nil {
		t.Fatal("Variables 调用后 suffixTpl 应已缓存，实际为 nil")
	}
	_ = ft.Validate(map[string]any{"q": "x"})
	if ft.suffixTpl != first {
		t.Error("Validate 不应重建后缀模板缓存")
	}
	if _, err := ft.Execute(map[string]any{"q": "x"}); err != nil {
		t.Fatalf("Execute err: %v", err)
	}
	if ft.suffixTpl != first {
		t.Error("Execute 不应重建后缀模板缓存")
	}

	// WithSuffix 改写后缀后缓存应失效，按新后缀重建。
	ft.WithSuffix("Reply: {{ r }}")
	if ft.suffixTpl != nil {
		t.Error("WithSuffix 后缓存应被清空")
	}
	names := make(map[string]bool)
	for _, v := range ft.Variables() {
		names[v.Name] = true
	}
	if !names["r"] || names["q"] {
		t.Errorf("改写后缀后变量应为 r 而非 q, 实际 %v", ft.Variables())
	}
}

// ============== 状态一致性：解析缓存 ==============

// TestParseCache_Idempotent 多次执行应复用缓存且结果稳定。
func TestParseCache_Idempotent(t *testing.T) {
	pt := New("c", "Hi {{ name }}")
	for i := 0; i < 3; i++ {
		got, err := pt.Execute(map[string]any{"name": "Z"})
		if err != nil || got != "Hi Z" {
			t.Fatalf("第 %d 次执行不一致: %q err=%v", i, got, err)
		}
	}
}

// ============== 并发竞态 ==============

// TestConcurrentExecute 并发执行同一模板（首次执行会触发解析缓存写）。
// 用 -race 运行可暴露 getParsedTemplate 的竞态。
func TestConcurrentExecute(t *testing.T) {
	pt := New("conc", "Hi {{ name }}")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := pt.Execute(map[string]any{"name": "P"})
			if err != nil || got != "Hi P" {
				t.Errorf("并发执行结果异常: %q err=%v", got, err)
			}
		}()
	}
	wg.Wait()
}

// TestConcurrentLibrary 并发读写模板库。
func TestConcurrentLibrary(t *testing.T) {
	lib := NewTemplateLibrary()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			lib.Register(New("t", "x"))
		}(i)
		go func() {
			defer wg.Done()
			_ = lib.List()
			_, _ = lib.Get("t")
		}()
	}
	wg.Wait()
}

// ============== ExecuteContext 上下文 ==============

// TestExecuteContext_Background 显式传 context 正常执行。
func TestExecuteContext_Background(t *testing.T) {
	pt := New("ctx", "{{ x }}")
	got, err := pt.ExecuteContext(context.Background(), map[string]any{"x": "v"})
	if err != nil || got != "v" {
		t.Errorf("got=%q err=%v", got, err)
	}
}

// TestExecuteContext_Canceled 已取消的 context 应被尊重：ExecuteContext
// 检测到 ctx 取消信号后直接返回错误，而非忽略取消继续渲染。
//
// 回归: bug #9 ExecuteContext 完全忽略 context 取消/超时信号。
func TestExecuteContext_Canceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pt := New("ctx", "{{ x }}")
	_, err := pt.ExecuteContext(ctx, map[string]any{"x": "v"})
	if err == nil {
		t.Fatal("已取消 ctx 应返回错误，实际为 nil（context 取消信号被忽略）")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, 期望包裹 context.Canceled", err)
	}
}
