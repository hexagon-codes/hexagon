package structured

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// ============== 测试用类型 ==============

// testUser 测试用的用户结构体
type testUser struct {
	Name  string `json:"name" desc:"用户名"`
	Age   int    `json:"age" desc:"年龄"`
	Email string `json:"email" desc:"邮箱"`
}

// testProduct 测试用的产品结构体
type testProduct struct {
	Name    string   `json:"name" desc:"产品名称"`
	Price   float64  `json:"price" desc:"价格"`
	Tags    []string `json:"tags" desc:"标签列表"`
	InStock bool     `json:"in_stock" desc:"是否有库存"`
}

// ============== Mock Provider ==============

// mockProvider 模拟 LLM Provider
//
// 支持配置多次调用的不同响应，用于测试重试逻辑。
// 当 responses 不为空时，按顺序返回对应响应；
// 超出范围时返回最后一个响应
type mockProvider struct {
	// responses 按调用顺序返回的响应列表
	responses []string

	// err 模拟调用错误
	err error

	// callCount 记录 Complete 被调用的次数
	callCount int

	// lastRequest 记录最后一次请求，用于验证请求内容
	lastRequest *llm.CompletionRequest
}

// newMockProvider 创建单一响应的 Mock Provider
func newMockProvider(response string) *mockProvider {
	return &mockProvider{
		responses: []string{response},
	}
}

// newMockProviderWithResponses 创建多次响应的 Mock Provider
//
// 每次调用 Complete 按顺序返回响应，用于测试重试场景
func newMockProviderWithResponses(responses ...string) *mockProvider {
	return &mockProvider{
		responses: responses,
	}
}

// newMockProviderWithError 创建返回错误的 Mock Provider
func newMockProviderWithError(err error) *mockProvider {
	return &mockProvider{
		err: err,
	}
}

// Name 返回 Provider 名称
func (p *mockProvider) Name() string {
	return "mock"
}

// Complete 模拟 LLM 补全请求
//
// 按 responses 列表顺序返回响应。如果调用次数超过
// responses 长度，返回最后一个响应
func (p *mockProvider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	// 检查上下文取消
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 如果配置了错误，直接返回
	if p.err != nil {
		return nil, p.err
	}

	p.callCount++
	p.lastRequest = &req

	// 确定返回哪个响应
	idx := p.callCount - 1
	if idx >= len(p.responses) {
		idx = len(p.responses) - 1
	}

	return &llm.CompletionResponse{
		Content: p.responses[idx],
		Usage: llm.Usage{
			PromptTokens:     50,
			CompletionTokens: 100,
			TotalTokens:      150,
		},
	}, nil
}

// Stream 模拟流式调用（本包未使用，返回 nil）
func (p *mockProvider) Stream(ctx context.Context, req llm.CompletionRequest) (*llm.Stream, error) {
	return nil, nil
}

// Models 返回支持的模型列表
func (p *mockProvider) Models() []llm.ModelInfo {
	return []llm.ModelInfo{{Name: "mock-model"}}
}

// CountTokens 模拟 token 计数
func (p *mockProvider) CountTokens(messages []llm.Message) (int, error) {
	return 100, nil
}

// ============== 测试用例 ==============

// TestGenerate_ValidJSON 测试正常 JSON 响应的解析
func TestGenerate_ValidJSON(t *testing.T) {
	user := testUser{
		Name:  "张三",
		Age:   25,
		Email: "zhangsan@example.com",
	}
	jsonBytes, _ := json.Marshal(user)

	provider := newMockProvider(string(jsonBytes))

	result, err := Generate[testUser](context.Background(), provider, "提取用户信息")
	if err != nil {
		t.Fatalf("期望成功但返回错误: %v", err)
	}

	if result.Name != "张三" {
		t.Errorf("Name 期望 '张三', 实际 '%s'", result.Name)
	}
	if result.Age != 25 {
		t.Errorf("Age 期望 25, 实际 %d", result.Age)
	}
	if result.Email != "zhangsan@example.com" {
		t.Errorf("Email 期望 'zhangsan@example.com', 实际 '%s'", result.Email)
	}

	// 验证只调用了一次
	if provider.callCount != 1 {
		t.Errorf("期望调用 1 次, 实际调用 %d 次", provider.callCount)
	}
}

// TestGenerate_JSONInMarkdownCodeBlock 测试从 markdown 代码块中提取 JSON
//
// LLM 经常会用 ```json ... ``` 包裹 JSON 输出，
// 解析器需要能自动提取
func TestGenerate_JSONInMarkdownCodeBlock(t *testing.T) {
	// 模拟 LLM 用 markdown 代码块包裹的响应
	response := "以下是提取的用户信息：\n\n```json\n{\"name\": \"李四\", \"age\": 30, \"email\": \"lisi@test.com\"}\n```\n\n以上就是提取结果。"

	provider := newMockProvider(response)

	result, err := Generate[testUser](context.Background(), provider, "提取用户信息")
	if err != nil {
		t.Fatalf("期望成功但返回错误: %v", err)
	}

	if result.Name != "李四" {
		t.Errorf("Name 期望 '李四', 实际 '%s'", result.Name)
	}
	if result.Age != 30 {
		t.Errorf("Age 期望 30, 实际 %d", result.Age)
	}
	if result.Email != "lisi@test.com" {
		t.Errorf("Email 期望 'lisi@test.com', 实际 '%s'", result.Email)
	}
}

// TestGenerate_RetryOnInvalidResponse 测试首次响应无效时自动重试
//
// 模拟场景：第一次返回无效内容，第二次返回正确 JSON
func TestGenerate_RetryOnInvalidResponse(t *testing.T) {
	validJSON := `{"name": "王五", "age": 28, "email": "wangwu@example.com"}`

	// 第一次返回无效内容，第二次返回合法 JSON
	provider := newMockProviderWithResponses(
		"这不是一个有效的 JSON 响应",
		validJSON,
	)

	result, err := Generate[testUser](context.Background(), provider, "提取用户信息")
	if err != nil {
		t.Fatalf("期望成功但返回错误: %v", err)
	}

	if result.Name != "王五" {
		t.Errorf("Name 期望 '王五', 实际 '%s'", result.Name)
	}
	if result.Age != 28 {
		t.Errorf("Age 期望 28, 实际 %d", result.Age)
	}

	// 验证调用了 2 次（首次 + 1 次重试）
	if provider.callCount != 2 {
		t.Errorf("期望调用 2 次, 实际调用 %d 次", provider.callCount)
	}

	// 验证重试时消息中包含错误信息
	if provider.lastRequest != nil {
		lastMsgs := provider.lastRequest.Messages
		found := false
		for _, msg := range lastMsgs {
			if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "validation failed") {
				found = true
				break
			}
		}
		if !found {
			t.Error("重试请求中应包含上一次的错误信息")
		}
	}
}

// TestGenerate_WithAllOptions 测试所有配置选项
func TestGenerate_WithAllOptions(t *testing.T) {
	validJSON := `{"name": "测试", "age": 20, "email": "test@test.com"}`
	provider := newMockProvider(validJSON)

	temp := 0.2
	result, err := Generate[testUser](
		context.Background(),
		provider,
		"提取用户信息",
		WithModel("gpt-4o"),
		WithMaxTokens(2048),
		WithTemperature(temp),
		WithMaxRetries(5),
		WithSystemPrompt("你是一个数据提取专家"),
		WithStrictMode(true),
	)
	if err != nil {
		t.Fatalf("期望成功但返回错误: %v", err)
	}

	if result.Name != "测试" {
		t.Errorf("Name 期望 '测试', 实际 '%s'", result.Name)
	}

	// 验证请求参数
	req := provider.lastRequest
	if req == nil {
		t.Fatal("lastRequest 不应为 nil")
	}

	if req.Model != "gpt-4o" {
		t.Errorf("Model 期望 'gpt-4o', 实际 '%s'", req.Model)
	}
	if req.MaxTokens != 2048 {
		t.Errorf("MaxTokens 期望 2048, 实际 %d", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0.2 {
		t.Errorf("Temperature 期望 0.2, 实际 %v", req.Temperature)
	}

	// 验证系统提示包含自定义内容和格式指令
	if len(req.Messages) > 0 {
		systemMsg := req.Messages[0]
		if systemMsg.Role != llm.RoleSystem {
			t.Errorf("第一条消息应为系统消息, 实际角色为 '%s'", systemMsg.Role)
		}
		if !strings.Contains(systemMsg.Content, "数据提取专家") {
			t.Error("系统消息应包含自定义提示词")
		}
		if !strings.Contains(systemMsg.Content, "JSON Schema") {
			t.Error("系统消息应包含格式指令")
		}
	}
}

// TestGenerateWithMessages 测试使用自定义消息列表
func TestGenerateWithMessages(t *testing.T) {
	validJSON := `{"name": "赵六", "age": 35, "email": "zhaoliu@example.com"}`
	provider := newMockProvider(validJSON)

	// 构建自定义消息列表
	messages := []llm.Message{
		llm.SystemMessage("你是一个数据提取助手"),
		llm.UserMessage("请从下面的信息中提取：赵六，35岁，zhaoliu@example.com"),
	}

	result, err := GenerateWithMessages[testUser](context.Background(), provider, messages)
	if err != nil {
		t.Fatalf("期望成功但返回错误: %v", err)
	}

	if result.Name != "赵六" {
		t.Errorf("Name 期望 '赵六', 实际 '%s'", result.Name)
	}
	if result.Age != 35 {
		t.Errorf("Age 期望 35, 实际 %d", result.Age)
	}

	// 验证消息结构：格式指令系统消息应在最前面
	req := provider.lastRequest
	if req == nil {
		t.Fatal("lastRequest 不应为 nil")
	}

	// 第一条应为包含格式指令的系统消息
	if len(req.Messages) < 3 {
		t.Fatalf("消息数量期望至少 3 条, 实际 %d 条", len(req.Messages))
	}

	// 第一条：自动注入的格式指令系统消息
	if req.Messages[0].Role != llm.RoleSystem {
		t.Errorf("第一条消息应为系统消息 (格式指令)")
	}
	if !strings.Contains(req.Messages[0].Content, "JSON Schema") {
		t.Error("第一条系统消息应包含格式指令")
	}

	// 后续消息应保留用户传入的原始消息
	if req.Messages[1].Role != llm.RoleSystem {
		t.Errorf("第二条消息应为用户自定义的系统消息")
	}
	if req.Messages[2].Role != llm.RoleUser {
		t.Errorf("第三条消息应为用户消息")
	}
}

// TestGenerateWithMessages_DoesNotModifyOriginal 测试不会修改调用方的消息切片
func TestGenerateWithMessages_DoesNotModifyOriginal(t *testing.T) {
	validJSON := `{"name": "测试", "age": 1, "email": "a@b.com"}`
	provider := newMockProvider(validJSON)

	// 创建原始消息列表
	originalMessages := []llm.Message{
		llm.UserMessage("测试消息"),
	}
	originalLen := len(originalMessages)

	_, err := GenerateWithMessages[testUser](context.Background(), provider, originalMessages)
	if err != nil {
		t.Fatalf("期望成功但返回错误: %v", err)
	}

	// 确认原始消息切片未被修改
	if len(originalMessages) != originalLen {
		t.Errorf("原始消息列表不应被修改, 期望长度 %d, 实际长度 %d", originalLen, len(originalMessages))
	}
}

// TestGenerate_ErrorAfterMaxRetries 测试达到最大重试次数后返回错误
func TestGenerate_ErrorAfterMaxRetries(t *testing.T) {
	// 所有响应都是无效的
	provider := newMockProviderWithResponses(
		"无效响应 1",
		"无效响应 2",
		"无效响应 3",
		"无效响应 4",
	)

	_, err := Generate[testUser](
		context.Background(),
		provider,
		"提取用户信息",
		WithMaxRetries(3),
	)
	if err == nil {
		t.Fatal("期望返回错误但成功了")
	}

	// 验证错误类型
	var structErr *Error
	if !errors.As(err, &structErr) {
		t.Fatalf("期望 *Error 类型, 实际 %T", err)
	}

	// 验证尝试次数（1 次初始 + 3 次重试 = 4 次）
	if len(structErr.Attempts) != 4 {
		t.Errorf("期望 4 次尝试记录, 实际 %d 次", len(structErr.Attempts))
	}

	// 验证每次尝试都有输出和错误
	for i, attempt := range structErr.Attempts {
		if attempt.Output == "" {
			t.Errorf("第 %d 次尝试的 Output 不应为空", i+1)
		}
		if attempt.Err == nil {
			t.Errorf("第 %d 次尝试的 Err 不应为 nil", i+1)
		}
	}

	// 验证调用次数
	if provider.callCount != 4 {
		t.Errorf("期望调用 4 次, 实际调用 %d 次", provider.callCount)
	}

	// 验证 Error() 方法的输出
	errMsg := structErr.Error()
	if !strings.Contains(errMsg, "4 次尝试") {
		t.Errorf("错误信息应包含尝试次数, 实际: %s", errMsg)
	}
}

// TestGenerate_ContextCancellation 测试上下文取消
func TestGenerate_ContextCancellation(t *testing.T) {
	// 使用已取消的上下文
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	provider := newMockProvider(`{"name": "test", "age": 1, "email": "a@b.com"}`)

	_, err := Generate[testUser](ctx, provider, "提取用户信息")
	if err == nil {
		t.Fatal("期望返回错误但成功了")
	}

	// 验证是上下文取消错误
	if !errors.Is(err, context.Canceled) {
		t.Errorf("期望 context.Canceled 错误, 实际: %v", err)
	}
}

// TestGenerate_LLMError 测试 LLM 调用失败的情况
func TestGenerate_LLMError(t *testing.T) {
	llmErr := fmt.Errorf("API 限流: rate limit exceeded")
	provider := newMockProviderWithError(llmErr)

	_, err := Generate[testUser](context.Background(), provider, "提取用户信息")
	if err == nil {
		t.Fatal("期望返回错误但成功了")
	}

	// 验证错误信息包含原始 LLM 错误
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("错误信息应包含原始 LLM 错误, 实际: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "model call failed") {
		t.Errorf("错误信息应包含 'model call failed', 实际: %s", err.Error())
	}
}

// TestGenerate_ComplexType 测试复杂嵌套类型
func TestGenerate_ComplexType(t *testing.T) {
	product := testProduct{
		Name:    "Go 编程实战",
		Price:   59.9,
		Tags:    []string{"编程", "Go", "后端"},
		InStock: true,
	}
	jsonBytes, _ := json.Marshal(product)

	provider := newMockProvider(string(jsonBytes))

	result, err := Generate[testProduct](context.Background(), provider, "提取产品信息")
	if err != nil {
		t.Fatalf("期望成功但返回错误: %v", err)
	}

	if result.Name != "Go 编程实战" {
		t.Errorf("Name 期望 'Go 编程实战', 实际 '%s'", result.Name)
	}
	if result.Price != 59.9 {
		t.Errorf("Price 期望 59.9, 实际 %f", result.Price)
	}
	if len(result.Tags) != 3 {
		t.Errorf("Tags 期望 3 个, 实际 %d 个", len(result.Tags))
	}
	if !result.InStock {
		t.Error("InStock 期望 true, 实际 false")
	}
}

// TestGenerate_DefaultConfig 测试默认配置值
func TestGenerate_DefaultConfig(t *testing.T) {
	validJSON := `{"name": "测试", "age": 1, "email": "a@b.com"}`
	provider := newMockProvider(validJSON)

	_, err := Generate[testUser](context.Background(), provider, "提取用户信息")
	if err != nil {
		t.Fatalf("期望成功但返回错误: %v", err)
	}

	// 验证默认配置
	req := provider.lastRequest
	if req == nil {
		t.Fatal("lastRequest 不应为 nil")
	}

	// 默认 MaxTokens 应为 4096
	if req.MaxTokens != 4096 {
		t.Errorf("默认 MaxTokens 期望 4096, 实际 %d", req.MaxTokens)
	}

	// 默认 Temperature 应为 nil
	if req.Temperature != nil {
		t.Errorf("默认 Temperature 期望 nil, 实际 %v", req.Temperature)
	}

	// 默认 Model 应为空
	if req.Model != "" {
		t.Errorf("默认 Model 期望空字符串, 实际 '%s'", req.Model)
	}
}

// TestGenerate_RetryWithMultipleFailures 测试多次重试后成功的场景
//
// 验证在连续失败后，最终成功的情况下不返回错误
func TestGenerate_RetryWithMultipleFailures(t *testing.T) {
	validJSON := `{"name": "最终成功", "age": 99, "email": "final@ok.com"}`

	// 前 3 次失败，第 4 次成功
	provider := newMockProviderWithResponses(
		"第 1 次: 不是 JSON",
		"第 2 次: 还是不对",
		"第 3 次: 依然无效",
		validJSON,
	)

	result, err := Generate[testUser](
		context.Background(),
		provider,
		"提取用户信息",
		WithMaxRetries(3),
	)
	if err != nil {
		t.Fatalf("期望第 4 次成功, 但返回错误: %v", err)
	}

	if result.Name != "最终成功" {
		t.Errorf("Name 期望 '最终成功', 实际 '%s'", result.Name)
	}

	// 验证调用了 4 次
	if provider.callCount != 4 {
		t.Errorf("期望调用 4 次, 实际调用 %d 次", provider.callCount)
	}
}

// TestGenerate_ZeroRetries 测试 MaxRetries=0 时不重试
func TestGenerate_ZeroRetries(t *testing.T) {
	provider := newMockProviderWithResponses("无效响应")

	_, err := Generate[testUser](
		context.Background(),
		provider,
		"提取用户信息",
		WithMaxRetries(0),
	)
	if err == nil {
		t.Fatal("期望返回错误但成功了")
	}

	// 验证只调用了 1 次（不重试）
	if provider.callCount != 1 {
		t.Errorf("MaxRetries=0 时期望调用 1 次, 实际调用 %d 次", provider.callCount)
	}
}

// TestGenerate_JSONWithExtraFields 测试非严格模式下允许多余字段
func TestGenerate_JSONWithExtraFields(t *testing.T) {
	// JSON 包含多余字段 "phone"
	response := `{"name": "测试", "age": 25, "email": "test@test.com", "phone": "123456"}`
	provider := newMockProvider(response)

	// 非严格模式应成功
	result, err := Generate[testUser](context.Background(), provider, "提取用户信息")
	if err != nil {
		t.Fatalf("非严格模式下期望成功, 但返回错误: %v", err)
	}

	if result.Name != "测试" {
		t.Errorf("Name 期望 '测试', 实际 '%s'", result.Name)
	}
}

// TestGenerate_StrictModeRejectsExtraFields 测试严格模式下拒绝多余字段
func TestGenerate_StrictModeRejectsExtraFields(t *testing.T) {
	// 第一次返回带多余字段的 JSON，第二次返回正确的
	provider := newMockProviderWithResponses(
		`{"name": "测试", "age": 25, "email": "test@test.com", "phone": "123456"}`,
		`{"name": "测试", "age": 25, "email": "test@test.com"}`,
	)

	result, err := Generate[testUser](
		context.Background(),
		provider,
		"提取用户信息",
		WithStrictMode(true),
	)
	if err != nil {
		t.Fatalf("期望重试后成功, 但返回错误: %v", err)
	}

	if result.Name != "测试" {
		t.Errorf("Name 期望 '测试', 实际 '%s'", result.Name)
	}

	// 严格模式下第一次应失败，需要重试
	if provider.callCount != 2 {
		t.Errorf("严格模式下期望调用 2 次, 实际调用 %d 次", provider.callCount)
	}
}

// TestError_ErrorMessage 测试 Error 类型的 Error() 方法
func TestError_ErrorMessage(t *testing.T) {
	err := &Error{
		Attempts: []AttemptError{
			{Output: "输出1", Err: fmt.Errorf("错误1")},
			{Output: "输出2", Err: fmt.Errorf("错误2")},
		},
	}

	msg := err.Error()
	if !strings.Contains(msg, "2 次尝试") {
		t.Errorf("错误信息应包含尝试次数, 实际: %s", msg)
	}
	if !strings.Contains(msg, "错误2") {
		t.Errorf("错误信息应包含最后一次错误, 实际: %s", msg)
	}
}

// TestError_Unwrap 测试 Error 类型的 Unwrap() 方法
func TestError_Unwrap(t *testing.T) {
	innerErr := fmt.Errorf("解析失败")
	err := &Error{
		Attempts: []AttemptError{
			{Output: "输出", Err: innerErr},
		},
	}

	// Unwrap 应返回最后一次的错误
	unwrapped := err.Unwrap()
	if unwrapped != innerErr {
		t.Errorf("Unwrap 期望返回最后一次错误")
	}
}

// TestError_EmptyAttempts 测试空尝试列表的 Error
func TestError_EmptyAttempts(t *testing.T) {
	err := &Error{Attempts: nil}

	msg := err.Error()
	if !strings.Contains(msg, "未知错误") {
		t.Errorf("空尝试列表应返回未知错误, 实际: %s", msg)
	}

	// Unwrap 应返回 nil
	if err.Unwrap() != nil {
		t.Error("空尝试列表的 Unwrap 应返回 nil")
	}
}

// TestBuildFormatInstructions 测试格式指令生成
func TestBuildFormatInstructions(t *testing.T) {
	instructions := buildFormatInstructions[testUser]()

	// 应包含 JSON Schema 关键字
	if !strings.Contains(instructions, "JSON Schema") {
		t.Error("格式指令应包含 'JSON Schema'")
	}

	// 应包含输出要求
	if !strings.Contains(instructions, "JSON") {
		t.Error("格式指令应包含 JSON 相关说明")
	}

	// 应包含必需字段说明
	if !strings.Contains(instructions, "required") || !strings.Contains(instructions, "必须") {
		t.Error("格式指令应包含必需字段说明")
	}
}

// TestOption_WithModel 测试 WithModel 选项
func TestOption_WithModel(t *testing.T) {
	cfg := defaultConfig()
	WithModel("gpt-4o")(cfg)
	if cfg.Model != "gpt-4o" {
		t.Errorf("期望 Model 为 'gpt-4o', 实际 '%s'", cfg.Model)
	}
}

// TestOption_WithMaxTokens 测试 WithMaxTokens 选项
func TestOption_WithMaxTokens(t *testing.T) {
	cfg := defaultConfig()
	WithMaxTokens(8192)(cfg)
	if cfg.MaxTokens != 8192 {
		t.Errorf("期望 MaxTokens 为 8192, 实际 %d", cfg.MaxTokens)
	}
}

// TestOption_WithTemperature 测试 WithTemperature 选项
func TestOption_WithTemperature(t *testing.T) {
	cfg := defaultConfig()
	WithTemperature(0.7)(cfg)
	if cfg.Temperature == nil || *cfg.Temperature != 0.7 {
		t.Errorf("期望 Temperature 为 0.7, 实际 %v", cfg.Temperature)
	}
}

// TestOption_WithMaxRetries 测试 WithMaxRetries 选项
func TestOption_WithMaxRetries(t *testing.T) {
	cfg := defaultConfig()
	WithMaxRetries(10)(cfg)
	if cfg.MaxRetries != 10 {
		t.Errorf("期望 MaxRetries 为 10, 实际 %d", cfg.MaxRetries)
	}
}

// TestOption_WithSystemPrompt 测试 WithSystemPrompt 选项
func TestOption_WithSystemPrompt(t *testing.T) {
	cfg := defaultConfig()
	WithSystemPrompt("你是数据专家")(cfg)
	if cfg.SystemPrompt != "你是数据专家" {
		t.Errorf("期望 SystemPrompt 为 '你是数据专家', 实际 '%s'", cfg.SystemPrompt)
	}
}

// TestOption_WithStrictMode 测试 WithStrictMode 选项
func TestOption_WithStrictMode(t *testing.T) {
	cfg := defaultConfig()
	WithStrictMode(true)(cfg)
	if !cfg.StrictMode {
		t.Error("期望 StrictMode 为 true")
	}
}

// TestDefaultConfig 测试默认配置值
func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()

	if cfg.MaxRetries != 3 {
		t.Errorf("默认 MaxRetries 期望 3, 实际 %d", cfg.MaxRetries)
	}
	if cfg.MaxTokens != 4096 {
		t.Errorf("默认 MaxTokens 期望 4096, 实际 %d", cfg.MaxTokens)
	}
	if cfg.StrictMode {
		t.Error("默认 StrictMode 期望 false")
	}
	if cfg.Model != "" {
		t.Errorf("默认 Model 期望空字符串, 实际 '%s'", cfg.Model)
	}
	if cfg.Temperature != nil {
		t.Errorf("默认 Temperature 期望 nil, 实际 %v", cfg.Temperature)
	}
	if cfg.SystemPrompt != "" {
		t.Errorf("默认 SystemPrompt 期望空字符串, 实际 '%s'", cfg.SystemPrompt)
	}
}
