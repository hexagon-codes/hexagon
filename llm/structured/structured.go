// Package structured 提供 LLM 结构化输出能力
//
// 本包实现了类型安全的 LLM 结构化输出，支持：
//   - 自动 Schema 生成：从 Go 类型自动生成 JSON Schema
//   - 格式指令注入：自动在 Prompt 中添加格式说明
//   - 自动解析验证：JSON 解析 + 自定义验证
//   - 失败重试修复：解析失败时自动重试并附带错误信息
//
// 本包用 Go 泛型提供了
// 编译时类型安全的结构化输出体验。核心思路是：
//  1. 从 Go 类型自动推导 JSON Schema
//  2. 将 Schema 作为格式指令注入到系统提示词中
//  3. 调用 LLM 获取 JSON 响应
//  4. 解析并验证响应，失败时自动重试
//
// 使用示例：
//
//	type User struct {
//	    Name  string `json:"name" desc:"用户名"`
//	    Age   int    `json:"age" desc:"年龄"`
//	    Email string `json:"email" desc:"邮箱"`
//	}
//
//	user, err := structured.Generate[User](ctx, provider, "从以下文本中提取用户信息：张三，25岁，zhangsan@example.com")
//
// 带自定义选项：
//
//	user, err := structured.Generate[User](ctx, provider, prompt,
//	    structured.WithModel("gpt-4o"),
//	    structured.WithMaxRetries(5),
//	    structured.WithTemperature(0.1),
//	)
//
// 使用自定义消息：
//
//	messages := []llm.Message{
//	    llm.SystemMessage("你是一个数据提取专家"),
//	    llm.UserMessage("提取用户信息：张三，25岁"),
//	}
//	user, err := structured.GenerateWithMessages[User](ctx, provider, messages)
package structured

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/llm/parser"
)

// ============== 错误类型 ==============

// AttemptError 单次尝试的错误信息
//
// 包含 LLM 返回的原始输出和解析/验证过程中产生的错误，
// 用于在最终失败时提供完整的调试信息
type AttemptError struct {
	// Output LLM 返回的原始输出内容
	Output string

	// Err 该次尝试产生的错误
	Err error
}

// Error 结构化输出错误
//
// 当所有重试均失败时返回此错误。包含每次尝试的详细信息，
// 便于调试和排查问题。
//
// 可通过 errors.As 进行类型断言：
//
//	var structErr *structured.Error
//	if errors.As(err, &structErr) {
//	    for i, attempt := range structErr.Attempts {
//	        fmt.Printf("第 %d 次尝试: 输出=%s, 错误=%v\n", i+1, attempt.Output, attempt.Err)
//	    }
//	}
type Error struct {
	// Attempts 所有尝试的错误记录
	Attempts []AttemptError
}

// Error 实现 error 接口
func (e *Error) Error() string {
	if len(e.Attempts) == 0 {
		return "structured output: 未知错误"
	}

	lastAttempt := e.Attempts[len(e.Attempts)-1]
	return fmt.Sprintf("structured output: 经过 %d 次尝试仍然失败, 最后一次错误: %v",
		len(e.Attempts), lastAttempt.Err)
}

// Unwrap 返回最后一次尝试的错误，支持 errors.Is/As 链式判断
func (e *Error) Unwrap() error {
	if len(e.Attempts) == 0 {
		return nil
	}
	return e.Attempts[len(e.Attempts)-1].Err
}

// ============== 配置 ==============

// config 内部配置结构
//
// 使用 Option 函数式选项模式配置，默认值：
//   - MaxRetries: 3 (最多重试 3 次，共 4 次调用)
//   - MaxTokens: 4096
//   - StrictMode: false (宽松解析模式)
type config struct {
	// Model 指定使用的模型名称
	// 为空时使用 Provider 的默认模型
	Model string

	// MaxTokens 最大生成 token 数
	MaxTokens int

	// Temperature 温度参数，控制输出随机性
	// nil 表示使用模型默认值
	Temperature *float64

	// MaxRetries 最大重试次数
	// 解析失败时自动重试，每次重试会附带上一次的错误信息
	MaxRetries int

	// SystemPrompt 额外的系统提示词
	// 会与格式指令合并，放在格式指令之前
	SystemPrompt string

	// StrictMode 严格模式
	// 开启后 JSON 解析不允许多余字段
	StrictMode bool

	// NativeJSONSchema 启用 provider 原生 JSON Schema 强制解码
	// 开启后经 CompletionRequest.ResponseFormat 把 Schema 下发给 provider
	// （json_schema + strict），由 provider 端约束解码，而非仅靠 prompt 注入。
	// 对支持的 provider（如 OpenAI GPT-4o+）能从根本上保证输出合法；不支持的
	// provider 会忽略该字段并退化为 prompt 注入 + 重试（行为不变）。
	NativeJSONSchema bool

	// SchemaName 原生 json_schema 模式下的 schema 名称（默认 "structured_output"）
	SchemaName string
}

// defaultConfig 返回默认配置
func defaultConfig() *config {
	return &config{
		MaxRetries: 3,
		MaxTokens:  4096,
		StrictMode: false,
	}
}

// Option 配置选项函数类型
//
// 使用函数式选项模式，支持链式配置：
//
//	structured.Generate[T](ctx, provider, prompt,
//	    structured.WithModel("gpt-4o"),
//	    structured.WithMaxRetries(5),
//	)
type Option func(*config)

// WithModel 设置使用的模型名称
//
// 为空时使用 Provider 的默认模型
func WithModel(model string) Option {
	return func(c *config) {
		c.Model = model
	}
}

// WithMaxTokens 设置最大生成 token 数
//
// 默认值为 4096。对于复杂结构体，可能需要增大此值
func WithMaxTokens(n int) Option {
	return func(c *config) {
		c.MaxTokens = n
	}
}

// WithTemperature 设置温度参数
//
// 温度越低，输出越确定性；温度越高，输出越随机。
// 对于结构化输出，推荐使用较低温度 (0.0-0.3)
func WithTemperature(t float64) Option {
	return func(c *config) {
		c.Temperature = &t
	}
}

// WithMaxRetries 设置最大重试次数
//
// 当 JSON 解析或验证失败时，会自动重试并在消息中附带错误信息，
// 让 LLM 有机会纠正输出。默认值为 3
func WithMaxRetries(n int) Option {
	return func(c *config) {
		c.MaxRetries = n
	}
}

// WithSystemPrompt 设置额外的系统提示词
//
// 该提示词会与自动生成的格式指令合并。格式指令始终生效，
// 自定义系统提示词放在格式指令之前
func WithSystemPrompt(prompt string) Option {
	return func(c *config) {
		c.SystemPrompt = prompt
	}
}

// WithStrictMode 设置严格解析模式
//
// 开启后 JSON 解析不允许多余字段（DisallowUnknownFields）。
// 默认关闭，适用于 LLM 可能返回额外说明字段的场景
func WithStrictMode(strict bool) Option {
	return func(c *config) {
		c.StrictMode = strict
	}
}

// WithNativeJSONSchema 启用 provider 原生 JSON Schema 强制解码
//
// 开启后把从类型 T 生成的（严格化）Schema 经 ResponseFormat 下发给 provider，
// 由 provider 端约束解码，而非仅靠 prompt 注入 + 解析重试。对支持的 provider
// 能从根本上保证输出合法；不支持的 provider 忽略该字段、自动退化为 prompt 注入。
// name 为可选的 schema 名称（空则用 "structured_output"）。
func WithNativeJSONSchema(name ...string) Option {
	return func(c *config) {
		c.NativeJSONSchema = true
		if len(name) > 0 && name[0] != "" {
			c.SchemaName = name[0]
		}
	}
}

// ============== 核心函数 ==============

// Generate 从 LLM 生成结构化输出
//
// 这是本包的核心函数，完整流程：
//  1. 从类型参数 T 自动生成 JSON Schema
//  2. 构建包含格式指令的系统消息和用户消息
//  3. 调用 LLM Provider 获取响应
//  4. 使用 JSONParser 解析响应为类型 T
//  5. 解析失败时自动重试，附带错误信息帮助 LLM 修正
//
// 类型参数 T 必须是可 JSON 序列化的结构体类型。
// 支持使用 `json` tag 指定字段名，`desc` tag 添加字段描述
//
// 参数：
//   - ctx: 上下文，支持超时和取消
//   - provider: LLM 提供者实例
//   - prompt: 用户提示词，描述要提取/生成的内容
//   - opts: 可选配置项
//
// 返回：
//   - T: 解析后的结构化结果
//   - error: 失败时返回 *Error 类型，包含所有尝试的详细信息
//
// 示例：
//
//	type Movie struct {
//	    Title    string   `json:"title" desc:"电影名称"`
//	    Year     int      `json:"year" desc:"上映年份"`
//	    Genres   []string `json:"genres" desc:"类型列表"`
//	    Rating   float64  `json:"rating" desc:"评分 (0-10)"`
//	}
//
//	movie, err := structured.Generate[Movie](ctx, provider,
//	    "告诉我关于电影《肖申克的救赎》的信息",
//	    structured.WithModel("gpt-4o"),
//	    structured.WithTemperature(0.1),
//	)
func Generate[T any](ctx context.Context, provider llm.Provider, prompt string, opts ...Option) (T, error) {
	// 构建消息列表：系统消息 + 用户消息
	messages := []llm.Message{
		llm.UserMessage(prompt),
	}

	return generateInternal[T](ctx, provider, messages, opts...)
}

// GenerateWithMessages 使用自定义消息列表生成结构化输出
//
// 与 Generate 类似，但允许传入完整的消息列表，适用于：
//   - 需要多轮对话上下文
//   - 需要自定义系统提示词
//   - 需要包含示例对话 (few-shot)
//
// 格式指令会作为系统消息插入到消息列表的最前面，
// 确保 LLM 始终知道输出格式要求
//
// 参数：
//   - ctx: 上下文，支持超时和取消
//   - provider: LLM 提供者实例
//   - messages: 自定义消息列表
//   - opts: 可选配置项
//
// 返回：
//   - T: 解析后的结构化结果
//   - error: 失败时返回 *Error 类型
//
// 示例：
//
//	messages := []llm.Message{
//	    llm.SystemMessage("你是一个数据提取专家，擅长从文本中提取结构化信息"),
//	    llm.UserMessage("请提取以下简历中的个人信息：\n张三，男，30岁，高级工程师..."),
//	}
//	profile, err := structured.GenerateWithMessages[Profile](ctx, provider, messages)
func GenerateWithMessages[T any](ctx context.Context, provider llm.Provider, messages []llm.Message, opts ...Option) (T, error) {
	// 复制消息列表，避免修改调用方的切片
	msgCopy := make([]llm.Message, len(messages))
	copy(msgCopy, messages)

	return generateInternal[T](ctx, provider, msgCopy, opts...)
}

// generateInternal 内部生成逻辑，Generate 和 GenerateWithMessages 共用
//
// 执行流程：
//  1. 应用配置选项
//  2. 生成格式指令并注入系统消息
//  3. 循环调用 LLM 直到成功或达到最大重试次数
//  4. 每次失败后将错误信息追加到消息中，引导 LLM 修正
func generateInternal[T any](ctx context.Context, provider llm.Provider, messages []llm.Message, opts ...Option) (T, error) {
	var zero T

	// 应用配置选项
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	// 构建格式指令，生成包含 Schema 的系统提示
	formatInstructions := buildFormatInstructions[T]()

	// 组装系统提示：自定义系统提示 + 格式指令
	systemPrompt := formatInstructions
	if cfg.SystemPrompt != "" {
		systemPrompt = cfg.SystemPrompt + "\n\n" + formatInstructions
	}

	// 将系统消息插入到消息列表最前面
	allMessages := make([]llm.Message, 0, len(messages)+1)
	allMessages = append(allMessages, llm.SystemMessage(systemPrompt))
	allMessages = append(allMessages, messages...)

	// 启用原生强制解码时，构造 provider 端的 json_schema 约束（一次构造、各次重试复用）。
	// 用 Strict() 把 Schema 收紧（additionalProperties:false + 全字段 required），
	// 配合 provider 的 strict 模式从根本上约束输出。
	var responseFormat *llm.ResponseFormat
	if cfg.NativeJSONSchema {
		schemaName := cfg.SchemaName
		if schemaName == "" {
			schemaName = "structured_output"
		}
		responseFormat = &llm.ResponseFormat{
			Type: "json_schema",
			JSONSchema: &llm.ResponseFormatJSONSchema{
				Name:   schemaName,
				Schema: llm.SchemaOf[T]().Strict(),
				Strict: true,
			},
		}
	}

	// 创建 JSON 解析器
	jsonParser := parser.NewJSONParser[T]().
		WithStrictMode(cfg.StrictMode).
		WithExtractJSON(true)

	// 收集每次尝试的错误
	var attempts []AttemptError

	// 重试循环：首次 + MaxRetries 次重试
	maxAttempts := cfg.MaxRetries + 1
	for attempt := 0; attempt < maxAttempts; attempt++ {
		// 检查上下文是否已取消
		if err := ctx.Err(); err != nil {
			return zero, err
		}

		// 构建 LLM 请求
		req := llm.CompletionRequest{
			Model:          cfg.Model,
			Messages:       allMessages,
			MaxTokens:      cfg.MaxTokens,
			Temperature:    cfg.Temperature,
			ResponseFormat: responseFormat,
		}

		// 调用 LLM
		resp, err := provider.Complete(ctx, req)
		if err != nil {
			return zero, fmt.Errorf("structured output: LLM 调用失败: %w", err)
		}

		// 尝试解析响应
		result, parseErr := jsonParser.Parse(ctx, resp.Content)
		if parseErr == nil {
			// 解析成功，返回结果
			return result, nil
		}

		// 记录本次尝试的错误
		attempts = append(attempts, AttemptError{
			Output: resp.Content,
			Err:    parseErr,
		})

		// 如果还有重试机会，将错误信息追加到消息中
		// 让 LLM 知道上一次输出的问题，以便修正
		if attempt < maxAttempts-1 {
			// 追加 LLM 上一次的响应
			allMessages = append(allMessages, llm.Message{
				Role:    llm.RoleAssistant,
				Content: resp.Content,
			})

			// 追加修正提示
			fixPrompt := fmt.Sprintf(
				"你上一次的输出解析失败，错误信息: %s\n\n请严格按照要求的 JSON Schema 格式重新输出，确保：\n"+
					"1. 输出合法的 JSON\n"+
					"2. 包含所有必需字段\n"+
					"3. 字段类型正确\n"+
					"4. 不要包含任何非 JSON 内容",
				parseErr.Error(),
			)
			allMessages = append(allMessages, llm.UserMessage(fixPrompt))
		}
	}

	// 所有尝试均失败
	return zero, &Error{Attempts: attempts}
}

// ============== 内部辅助函数 ==============

// buildFormatInstructions 构建格式指令
//
// 从类型参数 T 生成 JSON Schema，并构建清晰的格式说明。
// 格式指令告诉 LLM：
//   - 期望的 JSON Schema
//   - 输出格式要求
//   - 注意事项
//
// 使用 ai-core/schema 包通过反射自动生成 Schema，
// 支持 json tag 和 desc tag
func buildFormatInstructions[T any]() string {
	// 从 Go 类型生成 JSON Schema
	s := llm.SchemaOf[T]()

	// 将 Schema 序列化为 JSON 字符串
	schemaJSON, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		// Schema 生成失败时使用降级方案
		schemaJSON = []byte("{}")
	}

	var sb strings.Builder
	sb.WriteString("你必须以 JSON 格式输出，严格遵循以下 JSON Schema：\n\n")
	sb.WriteString("```json\n")
	sb.Write(schemaJSON)
	sb.WriteString("\n```\n\n")
	sb.WriteString("输出要求：\n")
	sb.WriteString("1. 仅输出合法的 JSON 对象，不要包含任何其他文字说明\n")
	sb.WriteString("2. 所有必需字段 (required) 必须包含\n")
	sb.WriteString("3. 字段值的类型必须与 Schema 定义一致\n")
	sb.WriteString("4. 不要使用 markdown 代码块包裹 JSON\n")

	return sb.String()
}
