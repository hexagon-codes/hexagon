// Package client 提供 Hexagon 框架的 Fluent API
//
// 本包实现类似 Spring AI ChatClient 的流畅 API 风格。
//
// 设计借鉴：
//   - Spring AI: ChatClient Fluent API
//   - LangChain: 链式调用
//
// 使用示例：
//
//	result, err := hexagon.Chat().
//	    Model("gpt-4").
//	    System("你是一个助手").
//	    User("你好").
//	    Tools(weatherTool).
//	    Temperature(0.7).
//	    MaxTokens(1000).
//	    Call(ctx)
//
//	// 流式调用
//	stream, err := hexagon.Chat().
//	    Model("gpt-4").
//	    User("写一首诗").
//	    Stream().
//	    Call(ctx)
package client

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/hexagon-codes/ai-core/llm"
	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/toolkit/lang/conv"
)

// ErrNoProvider 表示 ChatClient 没有可用的 LLM Provider。
//
// 当通过 Chat() 便捷入口创建客户端但未事先调用 SetDefaultProvider 设置默认
// Provider 时，Call/CallStream 会返回包装了本错误的结果，而非直接 nil 解引用 panic。
var ErrNoProvider = fmt.Errorf("client: 未配置 LLM Provider")

// ============== ChatClient ==============

// ChatClient Fluent 风格的聊天客户端
//
// 线程安全：ChatClient 内部使用 mu 保护共享可变的 *chatConfig，因此在同一实例上
// 并发调用 Fluent 方法（如 User/Assistant/Metadata）不会触发 "concurrent map writes"
// 之类的 fatal error。但 Fluent builder 语义本身是"在单实例上累积状态"，并发写入的
// 最终状态顺序仍不确定，建议典型用法是每个 goroutine 使用独立的 ChatClient 实例。
type ChatClient struct {
	provider llm.Provider

	// mu 保护 config 的并发读写，避免同一实例上并发 Fluent 调用触发数据竞争。
	mu     sync.Mutex
	config *chatConfig
}

type chatConfig struct {
	// 模型配置
	model       string
	temperature *float64
	maxTokens   int
	topP        *float64
	stop        []string

	// 消息
	systemPrompt string
	messages     []llm.Message

	// 工具
	tools []tool.Tool

	// 流式
	streaming bool

	// 元数据
	metadata map[string]any
}

// NewChatClient 创建聊天客户端
func NewChatClient(provider llm.Provider) *ChatClient {
	return &ChatClient{
		provider: provider,
		config: &chatConfig{
			messages: make([]llm.Message, 0),
			metadata: make(map[string]any),
		},
	}
}

// ============== Fluent 方法 ==============

// Model 设置模型
func (c *ChatClient) Model(model string) *ChatClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.model = model
	return c
}

// System 设置系统提示
func (c *ChatClient) System(prompt string) *ChatClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.systemPrompt = prompt
	return c
}

// User 添加用户消息
func (c *ChatClient) User(content string) *ChatClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.messages = append(c.config.messages, llm.Message{
		Role:    llm.RoleUser,
		Content: content,
	})
	return c
}

// Assistant 添加助手消息
func (c *ChatClient) Assistant(content string) *ChatClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.messages = append(c.config.messages, llm.Message{
		Role:    llm.RoleAssistant,
		Content: content,
	})
	return c
}

// Messages 设置消息列表
//
// 注意：此处对传入切片做防御性拷贝（defensive copy），避免 ChatClient 与调用方共享
// 同一底层数组，从而杜绝后续 User()/Assistant() 的 append 写穿调用方原始切片的别名副作用。
func (c *ChatClient) Messages(messages []llm.Message) *ChatClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 复制一份独立的底层数组，切断与调用方切片的别名关系。
	c.config.messages = append([]llm.Message(nil), messages...)
	return c
}

// AddMessage 添加消息
func (c *ChatClient) AddMessage(role llm.Role, content string) *ChatClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.messages = append(c.config.messages, llm.Message{
		Role:    role,
		Content: content,
	})
	return c
}

// Tools 设置工具
func (c *ChatClient) Tools(tools ...tool.Tool) *ChatClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.tools = tools
	return c
}

// Temperature 设置温度
func (c *ChatClient) Temperature(temp float64) *ChatClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.temperature = &temp
	return c
}

// MaxTokens 设置最大 token 数
func (c *ChatClient) MaxTokens(max int) *ChatClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.maxTokens = max
	return c
}

// TopP 设置 TopP
func (c *ChatClient) TopP(p float64) *ChatClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.topP = &p
	return c
}

// Stop 设置停止序列
func (c *ChatClient) Stop(sequences ...string) *ChatClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.stop = sequences
	return c
}

// Metadata 设置元数据
func (c *ChatClient) Metadata(key string, value any) *ChatClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.metadata[key] = value
	return c
}

// Stream 启用流式输出
func (c *ChatClient) Stream() *ChatClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.streaming = true
	return c
}

// ============== 执行方法 ==============

// ChatResponse 聊天响应
type ChatResponse struct {
	Content      string
	ToolCalls    []llm.ToolCall
	Usage        llm.Usage
	FinishReason string
	Metadata     map[string]any
}

// StreamResponse 流式响应
type StreamResponse struct {
	Stream   *stream.StreamReader[*ChatChunk]
	Metadata map[string]any
}

// ChatChunk 流式块
type ChatChunk struct {
	Content   string
	Delta     string
	ToolCalls []llm.ToolCall
	Done      bool
}

// Call 执行调用
func (c *ChatClient) Call(ctx context.Context) (*ChatResponse, error) {
	// 入口校验 Provider，避免默认 Provider 未设置时直接 nil 解引用 panic。
	if c.provider == nil {
		return nil, ErrNoProvider
	}

	c.mu.Lock()
	// 构建请求（持锁读取 config，避免与并发 Fluent 写竞争）。
	req := c.buildRequestLocked()
	streaming := c.config.streaming
	c.mu.Unlock()

	// 如果是流式
	if streaming {
		return c.callStreaming(ctx, req)
	}

	// 非流式调用
	return c.callNonStreaming(ctx, req)
}

// CallStream 流式调用
func (c *ChatClient) CallStream(ctx context.Context) (*StreamResponse, error) {
	// 入口校验 Provider，避免默认 Provider 未设置时直接 nil 解引用 panic。
	if c.provider == nil {
		return nil, ErrNoProvider
	}

	c.mu.Lock()
	c.config.streaming = true
	req := c.buildRequestLocked()
	// 拷贝 metadata，避免返回值与内部 config.metadata 共享同一 map 引用。
	meta := copyMetadata(c.config.metadata)
	c.mu.Unlock()

	streamResp, err := c.provider.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	// 包装为 StreamReader
	reader, writer := stream.Pipe[*ChatChunk](10)
	go func() {
		defer writer.Close()
		chunks := streamResp.Chunks()
		for chunk := range chunks {
			writer.Send(&ChatChunk{
				Content:   chunk.Content,
				Delta:     chunk.Content,
				ToolCalls: chunk.ToolCalls,
				Done:      chunk.FinishReason != "",
			})
		}
	}()

	return &StreamResponse{
		Stream:   reader,
		Metadata: meta,
	}, nil
}

// buildRequestLocked 构建请求。
//
// 调用方必须已持有 c.mu，本方法只读 c.config，不再额外加锁。
func (c *ChatClient) buildRequestLocked() llm.CompletionRequest {
	messages := make([]llm.Message, 0, len(c.config.messages)+1)

	// 添加系统消息
	if c.config.systemPrompt != "" {
		messages = append(messages, llm.Message{
			Role:    llm.RoleSystem,
			Content: c.config.systemPrompt,
		})
	}

	// 添加其他消息
	messages = append(messages, c.config.messages...)

	req := llm.CompletionRequest{
		Model:       c.config.model,
		Messages:    messages,
		Temperature: c.config.temperature,
		MaxTokens:   c.config.maxTokens,
		TopP:        c.config.topP,
		Stop:        c.config.stop,
	}

	// 添加工具
	if len(c.config.tools) > 0 {
		toolDefs := make([]llm.ToolDefinition, len(c.config.tools))
		for i, t := range c.config.tools {
			toolDefs[i] = llm.NewToolDefinition(
				t.Name(),
				t.Description(),
				t.Schema(),
			)
		}
		req.Tools = toolDefs
	}

	return req
}

// callNonStreaming 非流式调用
func (c *ChatClient) callNonStreaming(ctx context.Context, req llm.CompletionRequest) (*ChatResponse, error) {
	resp, err := c.provider.Complete(ctx, req)
	if err != nil {
		return nil, err
	}

	return &ChatResponse{
		Content:      resp.Content,
		ToolCalls:    resp.ToolCalls,
		Usage:        resp.Usage,
		FinishReason: resp.FinishReason,
		// 拷贝 metadata，避免响应与内部 config.metadata 共享同一 map 引用导致跨响应别名。
		Metadata: c.snapshotMetadata(),
	}, nil
}

// callStreaming 流式调用并合并结果
func (c *ChatClient) callStreaming(ctx context.Context, req llm.CompletionRequest) (*ChatResponse, error) {
	streamResp, err := c.provider.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	// 收集结果
	result, err := streamResp.Collect()
	if err != nil {
		return nil, err
	}

	return &ChatResponse{
		Content:      result.Content,
		ToolCalls:    result.ToolCalls,
		FinishReason: result.FinishReason,
		Usage:        result.Usage,
		// 拷贝 metadata，避免响应与内部 config.metadata 共享同一 map 引用导致跨响应别名。
		Metadata: c.snapshotMetadata(),
	}, nil
}

// snapshotMetadata 返回 config.metadata 的浅拷贝快照。
//
// 持锁读取后拷贝，确保返回给调用方的 map 与内部状态解耦，
// 避免复用 client 或并发读取响应时产生意外耦合。
func (c *ChatClient) snapshotMetadata() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return copyMetadata(c.config.metadata)
}

// copyMetadata 对 metadata map 做浅拷贝。nil 输入返回 nil。
func copyMetadata(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// ============== 便捷函数 ==============

// defaultProvider 默认 Provider
var defaultProvider llm.Provider

// SetDefaultProvider 设置默认 Provider
func SetDefaultProvider(provider llm.Provider) {
	defaultProvider = provider
}

// Chat 创建聊天客户端（使用默认 Provider）
func Chat() *ChatClient {
	return NewChatClient(defaultProvider)
}

// ChatWith 创建聊天客户端（指定 Provider）
func ChatWith(provider llm.Provider) *ChatClient {
	return NewChatClient(provider)
}

// ============== PromptClient ==============

// PromptClient Prompt 模板客户端
type PromptClient struct {
	template string
	vars     map[string]any
}

// NewPromptClient 创建 Prompt 客户端
func NewPromptClient(template string) *PromptClient {
	return &PromptClient{
		template: template,
		vars:     make(map[string]any),
	}
}

// Var 设置变量
func (p *PromptClient) Var(key string, value any) *PromptClient {
	p.vars[key] = value
	return p
}

// Vars 批量设置变量
func (p *PromptClient) Vars(vars map[string]any) *PromptClient {
	for k, v := range vars {
		p.vars[k] = v
	}
	return p
}

// Render 渲染模板
//
// 使用 {key} 形式的占位符：将模板中所有 {key} 替换为对应变量的字符串值。
// 变量值通过 toolkit conv.String 统一转换为字符串，支持任意类型。
// 未在 vars 中出现的占位符保持原样不替换。
func (p *PromptClient) Render() (string, error) {
	result := p.template
	for k, v := range p.vars {
		// 将 {key} 占位符替换为变量值的字符串形式。
		placeholder := "{" + k + "}"
		result = strings.ReplaceAll(result, placeholder, conv.String(v))
	}
	return result, nil
}

// ToChat 转换为聊天客户端
func (p *PromptClient) ToChat(provider llm.Provider) *ChatClient {
	content, _ := p.Render()
	return NewChatClient(provider).User(content)
}

// Prompt 创建 Prompt 客户端
func Prompt(template string) *PromptClient {
	return NewPromptClient(template)
}
