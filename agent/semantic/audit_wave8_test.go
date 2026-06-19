package semantic

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// ============== 测试用 Mock LLM Provider ==============

// mockProvider 是一个可配置的 LLM Provider，用于驱动 SemanticFunction 测试。
// 通过 completeFn 回调注入任意行为（返回固定内容、回显请求、返回错误等）。
type mockProvider struct {
	completeFn func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error)
	lastReq    llm.CompletionRequest // 记录最近一次请求，便于断言传参
}

func (p *mockProvider) Name() string { return "mock" }

func (p *mockProvider) Models() []llm.ModelInfo {
	return []llm.ModelInfo{{Name: "mock-model"}}
}

func (p *mockProvider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.lastReq = req
	if p.completeFn != nil {
		return p.completeFn(ctx, req)
	}
	return &llm.CompletionResponse{Content: "default"}, nil
}

func (p *mockProvider) Stream(ctx context.Context, req llm.CompletionRequest) (*llm.Stream, error) {
	return nil, nil
}

func (p *mockProvider) CountTokens(messages []llm.Message) (int, error) { return 0, nil }

// fixedProvider 返回固定 Content 的便捷构造。
func fixedProvider(content string) *mockProvider {
	return &mockProvider{completeFn: func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
		return &llm.CompletionResponse{Content: content}, nil
	}}
}

// ============== NewSemanticFunction 校验路径 ==============

func TestNewSemanticFunction_MissingName(t *testing.T) {
	_, err := NewSemanticFunction(SemanticFunctionConfig{
		Prompt: "hello",
		LLM:    fixedProvider("x"),
	})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("期望 name is required 错误，得到: %v", err)
	}
}

func TestNewSemanticFunction_MissingPrompt(t *testing.T) {
	_, err := NewSemanticFunction(SemanticFunctionConfig{
		Name: "f",
		LLM:  fixedProvider("x"),
	})
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("期望 prompt is required 错误，得到: %v", err)
	}
}

func TestNewSemanticFunction_MissingLLM(t *testing.T) {
	_, err := NewSemanticFunction(SemanticFunctionConfig{
		Name:   "f",
		Prompt: "hello",
	})
	if err == nil || !strings.Contains(err.Error(), "LLM provider is required") {
		t.Fatalf("期望 LLM provider is required 错误，得到: %v", err)
	}
}

func TestNewSemanticFunction_InvalidTemplate(t *testing.T) {
	// 非法模板语法应在构造时报错
	_, err := NewSemanticFunction(SemanticFunctionConfig{
		Name:   "f",
		Prompt: "hello {{ .Name ", // 缺少闭合
		LLM:    fixedProvider("x"),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid prompt template") {
		t.Fatalf("期望 invalid prompt template 错误，得到: %v", err)
	}
}

func TestNewSemanticFunction_DefaultParserIsString(t *testing.T) {
	// 未提供 OutputParser 时应默认 StringOutputParser（会 TrimSpace）
	f, err := NewSemanticFunction(SemanticFunctionConfig{
		Name:   "f",
		Prompt: "hi",
		LLM:    fixedProvider("  spaced  "),
	})
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	out, err := f.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	if out != "spaced" {
		t.Fatalf("默认 StringOutputParser 应 TrimSpace，得到: %q", out)
	}
}

// ============== SemanticFunction 元信息 getter ==============

func TestSemanticFunction_Getters(t *testing.T) {
	in := llm.SchemaOf[struct{ A string }]()
	out := llm.SchemaOf[struct{ B int }]()
	f, err := NewSemanticFunction(SemanticFunctionConfig{
		Name:         "myfn",
		Description:  "desc",
		Prompt:       "hi",
		LLM:          fixedProvider("ok"),
		InputSchema:  in,
		OutputSchema: out,
	})
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if f.Name() != "myfn" {
		t.Errorf("Name 不符: %q", f.Name())
	}
	if f.Description() != "desc" {
		t.Errorf("Description 不符: %q", f.Description())
	}
	if f.InputSchema() != in {
		t.Errorf("InputSchema 应原样返回")
	}
	if f.OutputSchema() != out {
		t.Errorf("OutputSchema 应原样返回")
	}
}

// ============== SemanticFunction.Invoke 渲染与传参 ==============

func TestSemanticFunction_Invoke_RendersPrompt(t *testing.T) {
	p := &mockProvider{completeFn: func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
		return &llm.CompletionResponse{Content: "ok"}, nil
	}}
	f, err := NewSemanticFunction(SemanticFunctionConfig{
		Name:   "greet",
		Prompt: "Hello {{ .name }}!",
		LLM:    p,
	})
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	_, err = f.Invoke(context.Background(), map[string]any{"name": "世界"})
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	// 验证渲染后的 user 消息
	last := p.lastReq.Messages[len(p.lastReq.Messages)-1]
	if last.Content != "Hello 世界!" {
		t.Fatalf("渲染结果不符，得到: %q", last.Content)
	}
}

func TestSemanticFunction_Invoke_SystemPromptIncluded(t *testing.T) {
	p := fixedProvider("ok")
	f, _ := NewSemanticFunction(SemanticFunctionConfig{
		Name:         "f",
		Prompt:       "user content",
		SystemPrompt: "you are a bot",
		LLM:          p,
	})
	_, _ = f.Invoke(context.Background(), nil)
	if len(p.lastReq.Messages) != 2 {
		t.Fatalf("含 systemPrompt 时应有 2 条消息，得到 %d", len(p.lastReq.Messages))
	}
	if string(p.lastReq.Messages[0].Role) != "system" || p.lastReq.Messages[0].Content != "you are a bot" {
		t.Fatalf("第一条应为 system 消息，得到: %+v", p.lastReq.Messages[0])
	}
}

func TestSemanticFunction_Invoke_NoSystemPrompt(t *testing.T) {
	p := fixedProvider("ok")
	f, _ := NewSemanticFunction(SemanticFunctionConfig{
		Name:   "f",
		Prompt: "u",
		LLM:    p,
	})
	_, _ = f.Invoke(context.Background(), nil)
	if len(p.lastReq.Messages) != 1 {
		t.Fatalf("无 systemPrompt 时应只有 1 条消息，得到 %d", len(p.lastReq.Messages))
	}
}

func TestSemanticFunction_Invoke_PassesModelTempMaxTokens(t *testing.T) {
	p := fixedProvider("ok")
	f, _ := NewSemanticFunction(SemanticFunctionConfig{
		Name:        "f",
		Prompt:      "u",
		LLM:         p,
		Model:       "gpt-x",
		Temperature: 0.7,
		MaxTokens:   123,
	})
	_, _ = f.Invoke(context.Background(), nil)
	if p.lastReq.Model != "gpt-x" {
		t.Errorf("Model 未传递: %q", p.lastReq.Model)
	}
	if p.lastReq.Temperature == nil || *p.lastReq.Temperature != 0.7 {
		t.Errorf("Temperature 未传递: %v", p.lastReq.Temperature)
	}
	if p.lastReq.MaxTokens != 123 {
		t.Errorf("MaxTokens 未传递: %d", p.lastReq.MaxTokens)
	}
}

// 回归: bug#3 Temperature == 0 是合法的确定性温度，必须传递给 LLM，
// 不能被 `if f.temperature > 0` 哨兵判断当作"未设置"丢弃。
func TestSemanticFunction_Invoke_ZeroTemperaturePreserved(t *testing.T) {
	p := fixedProvider("ok")
	f, _ := NewSemanticFunction(SemanticFunctionConfig{
		Name:        "f",
		Prompt:      "u",
		LLM:         p,
		Temperature: 0,
	})
	_, _ = f.Invoke(context.Background(), nil)
	if p.lastReq.Temperature == nil {
		t.Fatalf("0 温度是合法值，应被传递(非 nil)，得到 nil")
	}
	if *p.lastReq.Temperature != 0 {
		t.Fatalf("0 温度应被原样传递，得到: %v", *p.lastReq.Temperature)
	}
}

// 回归: bug#3 默认未设置温度（仍为零值 0）时也会传递 0；这与"显式 0"不可区分，
// 属于该字段为非指针类型的固有限制，作为已知行为锁定（0 始终传递）。
func TestSemanticFunction_Invoke_DefaultTemperatureIsZero(t *testing.T) {
	p := fixedProvider("ok")
	f, _ := NewSemanticFunction(SemanticFunctionConfig{
		Name:   "f",
		Prompt: "u",
		LLM:    p,
	})
	_, _ = f.Invoke(context.Background(), nil)
	if p.lastReq.Temperature == nil || *p.lastReq.Temperature != 0 {
		t.Fatalf("默认温度应作为 0 传递，得到: %v", p.lastReq.Temperature)
	}
}

func TestSemanticFunction_Invoke_LLMError(t *testing.T) {
	p := &mockProvider{completeFn: func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
		return nil, errors.New("boom")
	}}
	f, _ := NewSemanticFunction(SemanticFunctionConfig{Name: "f", Prompt: "u", LLM: p})
	_, err := f.Invoke(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "LLM call failed") {
		t.Fatalf("期望 LLM call failed 错误，得到: %v", err)
	}
}

func TestSemanticFunction_Invoke_RenderErrorOnMissingMethod(t *testing.T) {
	// 模板调用 map 上不存在的方法会在执行期报错
	p := fixedProvider("ok")
	f, _ := NewSemanticFunction(SemanticFunctionConfig{
		Name:   "f",
		Prompt: "{{ .name.DoesNotExist }}",
		LLM:    p,
	})
	_, err := f.Invoke(context.Background(), map[string]any{"name": "x"})
	if err == nil || !strings.Contains(err.Error(), "failed to render prompt") {
		t.Fatalf("期望渲染错误，得到: %v", err)
	}
}

func TestSemanticFunction_Invoke_NilInputOnNoVarTemplate(t *testing.T) {
	// nil input 配合无变量模板应正常工作
	f, _ := NewSemanticFunction(SemanticFunctionConfig{
		Name:   "f",
		Prompt: "static text",
		LLM:    fixedProvider("done"),
	})
	out, err := f.Invoke(context.Background(), nil)
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	if out != "done" {
		t.Fatalf("结果不符: %v", out)
	}
}

func TestSemanticFunction_Invoke_UnicodeAndLong(t *testing.T) {
	long := strings.Repeat("龙", 5000)
	p := fixedProvider("ok")
	f, _ := NewSemanticFunction(SemanticFunctionConfig{
		Name:   "f",
		Prompt: "{{ .v }}",
		LLM:    p,
	})
	_, err := f.Invoke(context.Background(), map[string]any{"v": long})
	if err != nil {
		t.Fatalf("超长 Unicode 输入失败: %v", err)
	}
	if !strings.Contains(p.lastReq.Messages[0].Content, "龙") {
		t.Fatalf("超长 Unicode 未正确渲染")
	}
}

// ============== NativeFunction 构造与校验 ==============

func TestNewNativeFunction_MissingName(t *testing.T) {
	_, err := NewNativeFunction(NativeFunctionConfig{Fn: func() {}})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("期望 name is required，得到: %v", err)
	}
}

func TestNewNativeFunction_MissingFn(t *testing.T) {
	_, err := NewNativeFunction(NativeFunctionConfig{Name: "f"})
	if err == nil || !strings.Contains(err.Error(), "function is required") {
		t.Fatalf("期望 function is required，得到: %v", err)
	}
}

func TestNewNativeFunction_NotAFunction(t *testing.T) {
	_, err := NewNativeFunction(NativeFunctionConfig{Name: "f", Fn: 42})
	if err == nil || !strings.Contains(err.Error(), "fn must be a function") {
		t.Fatalf("期望 fn must be a function，得到: %v", err)
	}
}

func TestNativeFunction_Getters(t *testing.T) {
	in := llm.SchemaOf[struct{ A int }]()
	out := llm.SchemaOf[struct{ B int }]()
	f, _ := NewNativeFunction(NativeFunctionConfig{
		Name:         "n",
		Description:  "d",
		Fn:           func(ctx context.Context, m map[string]any) (any, error) { return nil, nil },
		InputSchema:  in,
		OutputSchema: out,
	})
	if f.Name() != "n" || f.Description() != "d" {
		t.Errorf("getter 不符")
	}
	if f.InputSchema() != in || f.OutputSchema() != out {
		t.Errorf("schema getter 不符")
	}
}

// ============== NativeFunction.Invoke 各分支 ==============

func TestNativeFunction_Invoke_CtxAndMap(t *testing.T) {
	f, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "f",
		Fn: func(ctx context.Context, in map[string]any) (string, error) {
			return fmt.Sprintf("%v", in["k"]), nil
		},
	})
	out, err := f.Invoke(context.Background(), map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	if out != "v" {
		t.Fatalf("结果不符: %v", out)
	}
}

func TestNativeFunction_Invoke_MapWithoutCtx(t *testing.T) {
	// 函数第一个参数即为 map（无 context）
	f, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "f",
		Fn: func(in map[string]any) (int, error) {
			return len(in), nil
		},
	})
	out, err := f.Invoke(context.Background(), map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	if out != 2 {
		t.Fatalf("结果不符: %v", out)
	}
}

type addInput struct {
	X int `json:"x"`
	Y int `json:"y"`
}

func TestNativeFunction_Invoke_StructInput(t *testing.T) {
	f, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "add",
		Fn: func(ctx context.Context, in addInput) (int, error) {
			return in.X + in.Y, nil
		},
	})
	out, err := f.Invoke(context.Background(), map[string]any{"x": 3, "y": 4})
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	if out != 7 {
		t.Fatalf("结果不符: %v", out)
	}
}

func TestNativeFunction_Invoke_PtrStructInput(t *testing.T) {
	f, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "add",
		Fn: func(ctx context.Context, in *addInput) (int, error) {
			return in.X + in.Y, nil
		},
	})
	out, err := f.Invoke(context.Background(), map[string]any{"x": 10, "y": 5})
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	if out != 15 {
		t.Fatalf("结果不符: %v", out)
	}
}

func TestNativeFunction_Invoke_ReturnsError(t *testing.T) {
	wantErr := errors.New("native boom")
	f, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "f",
		Fn: func(ctx context.Context, in map[string]any) (string, error) {
			return "", wantErr
		},
	})
	_, err := f.Invoke(context.Background(), nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("期望透传原始错误，得到: %v", err)
	}
}

func TestNativeFunction_Invoke_NoReturnValues(t *testing.T) {
	// 0 返回值函数：Invoke 应返回 (nil, nil)
	called := false
	f, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "f",
		Fn: func(ctx context.Context, in map[string]any) {
			called = true
		},
	})
	out, err := f.Invoke(context.Background(), map[string]any{})
	if err != nil || out != nil {
		t.Fatalf("0 返回值应得到 (nil,nil)，得到 out=%v err=%v", out, err)
	}
	if !called {
		t.Fatalf("函数未被调用")
	}
}

func TestNativeFunction_Invoke_SingleReturnValue(t *testing.T) {
	// 单返回值（非 error）函数
	f, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "f",
		Fn: func(ctx context.Context, in map[string]any) string {
			return "single"
		},
	})
	out, err := f.Invoke(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	if out != "single" {
		t.Fatalf("结果不符: %v", out)
	}
}

func TestNativeFunction_Invoke_StructInputBadType(t *testing.T) {
	// map 中的类型与结构体字段不兼容，json.Unmarshal 应报错
	f, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "add",
		Fn: func(ctx context.Context, in addInput) (int, error) {
			return in.X, nil
		},
	})
	_, err := f.Invoke(context.Background(), map[string]any{"x": "not-an-int"})
	if err == nil || !strings.Contains(err.Error(), "failed to parse input") {
		t.Fatalf("期望 failed to parse input 错误，得到: %v", err)
	}
}

// 回归: bug#1 标量参数不再 panic，由 map["input"] 适配为标量并返回明确错误或正确执行。
// 单参数既不是 map 也不是 struct（例如 string）时，源码原先两个分支都不匹配，
// args[argOffset] 保持为零 reflect.Value，fnValue.Call(args) 会 panic。
// 修复后应优先从 input["input"] 取出对应类型的标量值并正常调用。
func TestNativeFunction_Invoke_ScalarParam(t *testing.T) {
	f, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "f",
		Fn: func(ctx context.Context, s string) (string, error) {
			return s, nil
		},
	})
	out, err := f.Invoke(context.Background(), map[string]any{"input": "hi"})
	if err != nil {
		t.Fatalf("标量参数应正常适配，得到错误: %v", err)
	}
	if out != "hi" {
		t.Fatalf("标量参数适配结果不符，得到: %v", out)
	}
}

// 回归: bug#1 标量参数缺少 input 键时返回明确错误而非 panic。
func TestNativeFunction_Invoke_ScalarParamMissingKey(t *testing.T) {
	f, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "f",
		Fn: func(ctx context.Context, s string) (string, error) {
			return s, nil
		},
	})
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("缺少 input 键不应 panic，得到 panic: %v", r)
		}
	}()
	_, err := f.Invoke(context.Background(), map[string]any{"other": "x"})
	if err == nil || !strings.Contains(err.Error(), "failed to adapt input") {
		t.Fatalf("缺少匹配标量参数时应返回 failed to adapt input 错误，得到: %v", err)
	}
}

// 回归: bug#1 slice 参数不再 panic，由 input["input"] 适配为对应 slice。
func TestNativeFunction_Invoke_SliceParam(t *testing.T) {
	f, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "f",
		Fn: func(ctx context.Context, s []int) (int, error) {
			return len(s), nil
		},
	})
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("slice 参数不应 panic，得到 panic: %v", r)
		}
	}()
	out, err := f.Invoke(context.Background(), map[string]any{"input": []int{1, 2, 3}})
	if err != nil {
		t.Fatalf("slice 参数应正常适配，得到错误: %v", err)
	}
	if out != 3 {
		t.Fatalf("slice 参数适配结果不符，得到: %v", out)
	}
}

// ============== NewFunc 便捷函数 ==============

func TestNewFunc_Basic(t *testing.T) {
	f := NewFunc("double", "翻倍", func(ctx context.Context, in addInput) (int, error) {
		return (in.X + in.Y) * 2, nil
	})
	if f == nil {
		t.Fatal("NewFunc 返回 nil")
	}
	if f.Name() != "double" || f.Description() != "翻倍" {
		t.Errorf("元信息不符")
	}
	if f.InputSchema() == nil || f.OutputSchema() == nil {
		t.Errorf("NewFunc 应自动生成输入/输出 Schema")
	}
	out, err := f.Invoke(context.Background(), map[string]any{"x": 2, "y": 3})
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	if out != 10 {
		t.Fatalf("结果不符: %v", out)
	}
}

// 回归: bug#2 NewFunc 不再吞掉 NewNativeFunction 的错误返回 nil，
// 当 name 为空（必然构造失败）时应 panic 并附带清晰信息，避免后续 nil 解引用 panic。
func TestNewFunc_EmptyNamePanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("name 为空时 NewFunc 应 panic 而非返回 nil")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "name is required") {
			t.Fatalf("panic 信息应包含底层错误 'name is required'，得到: %v", msg)
		}
	}()
	_ = NewFunc("", "desc", func(ctx context.Context, in addInput) (int, error) {
		return in.X, nil
	})
}

// ============== CompositeFunction / Chain ==============

func TestCompositeFunction_SchemaInheritance(t *testing.T) {
	in := llm.SchemaOf[struct{ A int }]()
	out := llm.SchemaOf[struct{ B int }]()
	first, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "first", Fn: func(m map[string]any) map[string]any { return m }, InputSchema: in,
	})
	last, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "last", Fn: func(m map[string]any) map[string]any { return m }, OutputSchema: out,
	})
	c := NewCompositeFunction("c", "d", first, last)
	if c.InputSchema() != in {
		t.Errorf("组合函数 InputSchema 应取第一个函数的")
	}
	if c.OutputSchema() != out {
		t.Errorf("组合函数 OutputSchema 应取最后一个函数的")
	}
	if c.Name() != "c" || c.Description() != "d" {
		t.Errorf("元信息不符")
	}
}

func TestCompositeFunction_EmptyFunctions(t *testing.T) {
	// 无子函数：Schema 应为 nil，Invoke 直接返回原始输入
	c := NewCompositeFunction("empty", "d")
	if c.InputSchema() != nil || c.OutputSchema() != nil {
		t.Errorf("空组合函数 Schema 应为 nil")
	}
	input := map[string]any{"k": "v"}
	out, err := c.Invoke(context.Background(), input)
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["k"] != "v" {
		t.Fatalf("空组合函数应原样返回输入，得到: %v", out)
	}
}

func TestCompositeFunction_Pipeline(t *testing.T) {
	// 第一个函数返回 map，第二个消费该 map
	step1, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "s1",
		Fn: func(m map[string]any) (map[string]any, error) {
			return map[string]any{"doubled": m["n"].(int) * 2}, nil
		},
	})
	step2, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "s2",
		Fn: func(m map[string]any) (int, error) {
			return m["doubled"].(int) + 1, nil
		},
	})
	c := Chain("pipe", "desc", step1, step2)
	out, err := c.Invoke(context.Background(), map[string]any{"n": 5})
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	if out != 11 { // 5*2+1
		t.Fatalf("管道结果不符: %v", out)
	}
}

func TestCompositeFunction_NonMapIntermediateWrapped(t *testing.T) {
	// 第一个函数返回非 map（标量），下一个函数应收到 {"input": <val>}
	step1, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "s1",
		Fn:   func(m map[string]any) (int, error) { return 42, nil },
	})
	var seen map[string]any
	step2, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "s2",
		Fn: func(m map[string]any) (string, error) {
			seen = m
			return "ok", nil
		},
	})
	c := Chain("pipe", "desc", step1, step2)
	_, err := c.Invoke(context.Background(), map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	if seen == nil || seen["input"] != 42 {
		t.Fatalf("非 map 中间结果应被包装为 {input: val}，得到: %v", seen)
	}
}

func TestCompositeFunction_ErrorPropagation(t *testing.T) {
	good, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "good",
		Fn:   func(m map[string]any) (map[string]any, error) { return m, nil },
	})
	bad, _ := NewNativeFunction(NativeFunctionConfig{
		Name: "bad",
		Fn:   func(m map[string]any) (any, error) { return nil, errors.New("inner") },
	})
	c := Chain("pipe", "desc", good, bad)
	_, err := c.Invoke(context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "function bad failed") {
		t.Fatalf("期望携带失败函数名的错误，得到: %v", err)
	}
	if !strings.Contains(err.Error(), "inner") {
		t.Fatalf("错误应包裹原始错误，得到: %v", err)
	}
}

// ============== Output Parsers ==============

func TestStringOutputParser(t *testing.T) {
	p := &StringOutputParser{}
	out, err := p.Parse("   hello world  \n")
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if out != "hello world" {
		t.Fatalf("应 TrimSpace，得到: %q", out)
	}
}

func TestStringOutputParser_Empty(t *testing.T) {
	p := &StringOutputParser{}
	out, _ := p.Parse("")
	if out != "" {
		t.Fatalf("空输入应得到空串，得到: %q", out)
	}
}

func TestJSONOutputParser_NoTargetType(t *testing.T) {
	p := &JSONOutputParser{}
	out, err := p.Parse(`前缀 {"a": 1, "b": "x"} 后缀`)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("应解析为 map，得到 %T", out)
	}
	if m["a"].(float64) != 1 || m["b"] != "x" {
		t.Fatalf("解析内容不符: %v", m)
	}
}

func TestJSONOutputParser_WithTargetType(t *testing.T) {
	p := &JSONOutputParser{TargetType: reflect.TypeOf(addInput{})}
	out, err := p.Parse(`{"x": 7, "y": 8}`)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	// 注意：实现返回 reflect.New(...).Interface()，即 *addInput 指针
	ptr, ok := out.(*addInput)
	if !ok {
		t.Fatalf("有 TargetType 时应返回目标类型指针，得到 %T", out)
	}
	if ptr.X != 7 || ptr.Y != 8 {
		t.Fatalf("解析内容不符: %+v", ptr)
	}
}

func TestJSONOutputParser_InvalidJSON(t *testing.T) {
	p := &JSONOutputParser{}
	_, err := p.Parse("no json here at all")
	if err == nil || !strings.Contains(err.Error(), "failed to parse JSON") {
		t.Fatalf("期望 JSON 解析错误，得到: %v", err)
	}
}

func TestJSONOutputParser_ArrayExtraction(t *testing.T) {
	// 文本中没有 {} 但有 []，应提取数组
	p := &JSONOutputParser{}
	out, err := p.Parse("结果: [1, 2, 3] 完成")
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	arr, ok := out.([]any)
	if !ok || len(arr) != 3 {
		t.Fatalf("应解析为长度 3 的数组，得到: %v", out)
	}
}

// 边界：当文本同时含 {} 和 []，extractJSON 优先匹配对象分支。
func TestExtractJSON_ObjectPreferredOverArray(t *testing.T) {
	got := extractJSON("noise [1,2] more {\"a\":1} tail")
	if got != `{"a":1}` {
		t.Fatalf("对象分支应优先，得到: %q", got)
	}
}

// 边界：纯数组文本应走数组分支。
func TestExtractJSON_ArrayOnly(t *testing.T) {
	got := extractJSON("x [1,2,3] y")
	if got != "[1,2,3]" {
		t.Fatalf("数组提取不符: %q", got)
	}
}

// 边界：无 JSON 标记时原样返回。
func TestExtractJSON_NoBrackets(t *testing.T) {
	got := extractJSON("plain text")
	if got != "plain text" {
		t.Fatalf("无括号应原样返回，得到: %q", got)
	}
}

// 边界：只有 { 没有 } 时不满足 end>start，返回原文。
func TestExtractJSON_UnbalancedBrace(t *testing.T) {
	got := extractJSON("only { open")
	if got != "only { open" {
		t.Fatalf("不平衡花括号应原样返回，得到: %q", got)
	}
}

// 回归: bug#6 多个 JSON 块时，首个 '{' 到末个 '}' 的粗暴截取会跨块错位；
// 应基于括号配平只提取第一个完整 JSON 对象。
func TestExtractJSON_MultipleObjectsFirstOnly(t *testing.T) {
	got := extractJSON(`前缀 {"a":1} 中间 {"b":2} 后缀`)
	if got != `{"a":1}` {
		t.Fatalf("多 JSON 块应只取首个完整对象，得到: %q", got)
	}
}

// 回归: bug#6 嵌套对象需正确配平到匹配的右括号，而非末个 '}'。
func TestExtractJSON_NestedThenTrailingBrace(t *testing.T) {
	got := extractJSON(`x {"a":{"b":1}} y {"c":2}`)
	if got != `{"a":{"b":1}}` {
		t.Fatalf("应配平嵌套对象并止于其右括号，得到: %q", got)
	}
}

// 回归: bug#6 提取出的首个完整对象应能被 JSONOutputParser 正确解析（端到端）。
func TestJSONOutputParser_MultipleObjectsParsesFirst(t *testing.T) {
	p := &JSONOutputParser{}
	out, err := p.Parse(`noise {"a":1} more {"b":2}`)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("应解析为 map，得到 %T", out)
	}
	if m["a"].(float64) != 1 {
		t.Fatalf("应解析首个对象 {a:1}，得到: %v", m)
	}
	if _, exists := m["b"]; exists {
		t.Fatalf("不应跨块包含第二个对象的字段: %v", m)
	}
}

func TestListOutputParser_DefaultSeparator(t *testing.T) {
	p := &ListOutputParser{}
	out, err := p.Parse("apple\nbanana\n\ncherry")
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	list := out.([]string)
	want := []string{"apple", "banana", "cherry"}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("默认换行分隔不符: %v", list)
	}
}

func TestListOutputParser_CustomSeparator(t *testing.T) {
	p := &ListOutputParser{Separator: ","}
	out, _ := p.Parse("a, b , c")
	list := out.([]string)
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("自定义分隔不符: %v", list)
	}
}

// 回归: bug#5 ListOutputParser 需正确剥离多位数字编号前缀（如 10. 11.），
// 而非仅处理单字符数字编号。
func TestListOutputParser_NumberedPrefixStripped(t *testing.T) {
	p := &ListOutputParser{}
	out, _ := p.Parse("1. first\n2) second\n10. tenth\n11) eleventh")
	list := out.([]string)
	want := []string{"first", "second", "tenth", "eleventh"}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("多位编号前缀应被剥离: %v", list)
	}
}

// 回归: bug#5 编号前可带空白，编号符号后亦可带空白，且不误伤非编号文本。
func TestListOutputParser_NumberedPrefixEdgeCases(t *testing.T) {
	p := &ListOutputParser{}
	out, _ := p.Parse("  3.   spaced\n2024 not numbered\n4)tight")
	list := out.([]string)
	want := []string{"spaced", "2024 not numbered", "tight"}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("编号前缀边界处理不符: %v", list)
	}
}

func TestListOutputParser_Empty(t *testing.T) {
	p := &ListOutputParser{}
	out, _ := p.Parse("")
	list := out.([]string)
	if len(list) != 0 {
		t.Fatalf("空输入应得到空列表，得到: %v", list)
	}
}

func TestListOutputParser_OnlyWhitespace(t *testing.T) {
	p := &ListOutputParser{}
	out, _ := p.Parse("  \n  \n  ")
	list := out.([]string)
	if len(list) != 0 {
		t.Fatalf("纯空白应得到空列表，得到: %v", list)
	}
}

func TestBoolOutputParser_TrueValues(t *testing.T) {
	p := &BoolOutputParser{}
	for _, in := range []string{"true", "YES", "是", "对", "正确", "1", "  yes  ", "true，确实"} {
		out, err := p.Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) 失败: %v", in, err)
		}
		if out != true {
			t.Fatalf("Parse(%q) 应为 true，得到 %v", in, out)
		}
	}
}

func TestBoolOutputParser_FalseValues(t *testing.T) {
	p := &BoolOutputParser{}
	for _, in := range []string{"false", "no", "否", "", "maybe", "0"} {
		out, _ := p.Parse(in)
		if out != false {
			t.Fatalf("Parse(%q) 应为 false，得到 %v", in, out)
		}
	}
}

// 回归: bug#4 BoolOutputParser 不再用宽松 HasPrefix 造成假阳性。
// "yesterday" 以 "yes" 开头但是一个独立单词，不应被判为 true。
func TestBoolOutputParser_PrefixNoFalsePositive(t *testing.T) {
	// 注：CJK 肯定词（是/对/正确）因中文无词边界仍按前缀匹配（"是的"/"正确。" 需判 true），
	// 故此处仅断言 ASCII 前缀假阳性已修复。
	p := &BoolOutputParser{}
	for _, in := range []string{"yesterday", "yesman", "notable", "trueness", "1024 results"} {
		out, _ := p.Parse(in)
		if out != false {
			t.Fatalf("'%s' 不应因前缀匹配被误判为 true，得到: %v", in, out)
		}
	}
}

// 回归: bug#4 修复后仍需容忍带标点/后续内容的合法肯定输出（按词边界整词匹配）。
func TestBoolOutputParser_PunctuationStillTrue(t *testing.T) {
	p := &BoolOutputParser{}
	for _, in := range []string{"yes.", "yes, it is correct", "true!", "是的，确实", "正确。"} {
		out, _ := p.Parse(in)
		if out != true {
			t.Fatalf("'%s' 首词为肯定词，应判 true，得到: %v", in, out)
		}
	}
}

// ============== Registry ==============

// stubFunc 是用于注册表测试的最小 Function 实现。
type stubFunc struct{ name string }

func (s *stubFunc) Name() string                { return s.name }
func (s *stubFunc) Description() string          { return "" }
func (s *stubFunc) InputSchema() *llm.Schema     { return nil }
func (s *stubFunc) OutputSchema() *llm.Schema    { return nil }
func (s *stubFunc) Invoke(ctx context.Context, input map[string]any) (any, error) {
	return s.name, nil
}

func TestRegistry_RegisterGetList(t *testing.T) {
	r := NewRegistry()
	f := &stubFunc{name: "a"}
	if err := r.Register(f); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	got, ok := r.Get("a")
	if !ok || got != f {
		t.Fatalf("Get 未返回已注册函数")
	}
	if len(r.List()) != 1 {
		t.Fatalf("List 长度应为 1")
	}
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubFunc{name: "dup"})
	err := r.Register(&stubFunc{name: "dup"})
	if err == nil || !strings.Contains(err.Error(), "function already exists") {
		t.Fatalf("重复注册应报错，得到: %v", err)
	}
}

func TestRegistry_GetMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nope")
	if ok {
		t.Fatalf("未注册函数 Get 应返回 false")
	}
}

func TestRegistry_InvokeMissing(t *testing.T) {
	r := NewRegistry()
	_, err := r.Invoke(context.Background(), "nope", nil)
	if err == nil || !strings.Contains(err.Error(), "function not found") {
		t.Fatalf("调用未注册函数应报错，得到: %v", err)
	}
}

func TestRegistry_Invoke(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubFunc{name: "echo"})
	out, err := r.Invoke(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	if out != "echo" {
		t.Fatalf("结果不符: %v", out)
	}
}

func TestRegistry_EmptyList(t *testing.T) {
	r := NewRegistry()
	if len(r.List()) != 0 {
		t.Fatalf("新注册表 List 应为空")
	}
}

// 并发：并发注册不同名函数 + 并发读取，验证无数据竞争（配合 -race）。
func TestRegistry_ConcurrentRegisterAndRead(t *testing.T) {
	r := NewRegistry()
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = r.Register(&stubFunc{name: fmt.Sprintf("f%d", i)})
		}(i)
	}
	// 同时并发读取
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.List()
			_, _ = r.Get("f1")
		}()
	}
	wg.Wait()
	if len(r.List()) != n {
		t.Fatalf("并发注册后应有 %d 个函数，得到 %d", n, len(r.List()))
	}
}

// 并发：多 goroutine 注册同名函数，恰好一个成功。
func TestRegistry_ConcurrentDuplicateOnlyOneWins(t *testing.T) {
	r := NewRegistry()
	const n = 30
	var wg sync.WaitGroup
	var successes int32
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Register(&stubFunc{name: "same"}); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("并发注册同名函数应恰好成功一次，实际成功 %d 次", successes)
	}
}

// ============== 全局注册表 ==============

func TestGlobalRegistry_Singleton(t *testing.T) {
	a := GlobalRegistry()
	b := GlobalRegistry()
	if a != b {
		t.Fatalf("GlobalRegistry 应返回同一单例")
	}
}

func TestGlobalRegister_And_Invoke(t *testing.T) {
	// 使用唯一名避免与其它测试/全局状态冲突
	name := "global_test_fn_wave8_unique"
	if err := Register(&stubFunc{name: name}); err != nil {
		t.Fatalf("全局注册失败: %v", err)
	}
	out, err := Invoke(context.Background(), name, nil)
	if err != nil {
		t.Fatalf("全局调用失败: %v", err)
	}
	if out != name {
		t.Fatalf("结果不符: %v", out)
	}
	// 二次注册同名应报错（验证全局状态一致性）
	if err := Register(&stubFunc{name: name}); err == nil {
		t.Fatalf("全局重复注册应报错")
	}
}

// ============== 接口符合性 ==============

func TestFunctionInterfaceCompliance(t *testing.T) {
	// 编译期断言三种实现满足 Function 接口
	var _ Function = (*SemanticFunction)(nil)
	var _ Function = (*NativeFunction)(nil)
	var _ Function = (*CompositeFunction)(nil)
	var _ Function = (*stubFunc)(nil)
}
