// Package conversation 提供多轮对话管理功能
//
// 本包实现了对话历史管理、上下文窗口控制和 Token 预算管理：
//   - 自动维护对话历史
//   - 基于 Token 预算的上下文窗口
//   - 超出预算时自动截断旧消息
//   - 支持对话摘要压缩
//
// 使用示例：
//
//	mgr := conversation.New(
//	    conversation.WithMaxTokens(4096),
//	    conversation.WithSystemPrompt("你是一个助手"),
//	)
//	mgr.AddUserMessage("你好")
//	messages := mgr.GetMessages()
package conversation

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/tokenizer"
)

// ============== 对话管理器 ==============

// Manager 多轮对话管理器
//
// 维护有序的消息列表，支持 Token 预算限制和上下文窗口控制。
// 线程安全，可在多 goroutine 中使用。
type Manager struct {
	messages     []TimedMessage
	systemPrompt string
	maxTokens    int // Token 预算上限（0 表示不限制）
	maxTurns     int // 最大轮次（0 表示不限制）
	maxMessages  int // 最大存储消息数（0 表示不限制，防止内存无限增长）
	mu           sync.RWMutex
}

// TimedMessage 带时间戳的消息
type TimedMessage struct {
	// Role 消息角色（user/assistant/system/tool）
	Role string `json:"role"`

	// Content 消息内容
	Content string `json:"content"`

	// Timestamp 消息时间
	Timestamp time.Time `json:"timestamp"`
}

// Option 配置选项
type Option func(*Manager)

// WithMaxTokens 设置 Token 预算上限
//
// 超出预算时 GetMessages 会自动截断旧消息。
// 设为 0 表示不限制。
func WithMaxTokens(n int) Option {
	return func(m *Manager) {
		m.maxTokens = n
	}
}

// WithMaxTurns 设置最大对话轮次
//
// 一个 "轮次" 指一对 user + assistant 消息。
// 设为 0 表示不限制。
func WithMaxTurns(n int) Option {
	return func(m *Manager) {
		m.maxTurns = n
	}
}

// WithMaxMessages 设置最大存储消息数
//
// 超过此限制时，最旧的消息会被自动丢弃。
// 设为 0 表示不限制。默认 1000。
func WithMaxMessages(n int) Option {
	return func(m *Manager) {
		m.maxMessages = n
	}
}

// WithSystemPrompt 设置系统提示词
func WithSystemPrompt(prompt string) Option {
	return func(m *Manager) {
		m.systemPrompt = prompt
	}
}

// New 创建对话管理器
func New(opts ...Option) *Manager {
	m := &Manager{
		maxTokens:   4096,
		maxMessages: 1000,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// AddUserMessage 添加用户消息
func (m *Manager) AddUserMessage(content string) {
	m.addMessage("user", content)
}

// AddAssistantMessage 添加助手回复
func (m *Manager) AddAssistantMessage(content string) {
	m.addMessage("assistant", content)
}

// AddSystemMessage 添加系统消息
func (m *Manager) AddSystemMessage(content string) {
	m.addMessage("system", content)
}

// AddToolResult 添加工具调用结果
func (m *Manager) AddToolResult(toolName string, result string) {
	m.addMessage("tool", fmt.Sprintf("[%s] %s", toolName, result))
}

// addMessage 内部添加消息方法
func (m *Manager) addMessage(role, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, TimedMessage{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	})
	// 防止消息列表无限增长
	if m.maxMessages > 0 && len(m.messages) > m.maxMessages {
		excess := len(m.messages) - m.maxMessages
		copy(m.messages, m.messages[excess:])
		// 清除尾部引用帮助 GC
		for i := m.maxMessages; i < len(m.messages); i++ {
			m.messages[i] = TimedMessage{}
		}
		m.messages = m.messages[:m.maxMessages]
	}
}

// GetMessages 获取消息列表（应用 Token 预算和轮次限制）
//
// 始终保留系统提示词，然后从最近的消息开始向前填充，
// 直到达到 Token 预算或轮次限制。
func (m *Manager) GetMessages() []llm.Message {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.buildMessages(m.maxTokens)
}

// GetContext 获取指定 Token 预算内的消息
func (m *Manager) GetContext(maxTokens int) []llm.Message {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.buildMessages(maxTokens)
}

// buildMessages 构建消息列表（内部方法，需持锁调用）
func (m *Manager) buildMessages(tokenBudget int) []llm.Message {
	var result []llm.Message

	// 始终包含系统提示词
	usedTokens := 0
	if m.systemPrompt != "" {
		result = append(result, llm.Message{
			Role:    llm.RoleSystem,
			Content: m.systemPrompt,
		})
		usedTokens += estimateTokens(m.systemPrompt)
	}

	// 应用轮次限制：找到最近 maxTurns 个 user 消息的起始位置
	msgs := m.messages
	if m.maxTurns > 0 {
		userCount := 0
		startIdx := len(msgs)
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == "user" {
				userCount++
				if userCount > m.maxTurns {
					break
				}
				startIdx = i
			}
		}
		if startIdx < len(msgs) {
			msgs = msgs[startIdx:]
		}
	}

	// 从最近的消息向前填充，遵守 Token 预算
	if tokenBudget > 0 {
		var selected []TimedMessage
		for i := len(msgs) - 1; i >= 0; i-- {
			tokens := estimateTokens(msgs[i].Content)
			if usedTokens+tokens > tokenBudget {
				break
			}
			usedTokens += tokens
			selected = append(selected, msgs[i])
		}
		// 反转为时间顺序
		for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
			selected[i], selected[j] = selected[j], selected[i]
		}
		msgs = selected
	}

	// 转换为 llm.Message
	for _, msg := range msgs {
		result = append(result, llm.Message{
			Role:    llm.Role(msg.Role),
			Content: msg.Content,
		})
	}

	return result
}

// TurnCount 返回用户轮次数
func (m *Manager) TurnCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, msg := range m.messages {
		if msg.Role == "user" {
			count++
		}
	}
	return count
}

// MessageCount 返回总消息数
func (m *Manager) MessageCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.messages)
}

// Clear 清空对话历史
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = nil
}

// History 返回对话历史的副本
func (m *Manager) History() []TimedMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]TimedMessage, len(m.messages))
	copy(result, m.messages)
	return result
}

// Summarize 使用 LLM 将旧消息压缩为摘要
//
// 保留最近 keepRecent 条消息，将更早的消息发给 LLM 生成摘要，
// 然后用摘要替换旧消息。
// 注意：LLM 调用在锁外进行，不会阻塞其他 goroutine。
func (m *Manager) Summarize(ctx context.Context, provider llm.Provider, keepRecent int) error {
	// 防御负值：keepRecent<0 在语义上等价于"不保留任何最近消息"，
	// 同时避免后续 m.messages[:len-keepRecent]（len-keepRecent>len）切片越界 panic。
	if keepRecent < 0 {
		keepRecent = 0
	}

	// 第一阶段：在锁内读取需要摘要的消息
	m.mu.RLock()
	if len(m.messages) <= keepRecent {
		m.mu.RUnlock()
		return nil
	}
	oldMessages := make([]TimedMessage, len(m.messages)-keepRecent)
	copy(oldMessages, m.messages[:len(m.messages)-keepRecent])
	snapshotLen := len(m.messages)
	m.mu.RUnlock()

	// 第二阶段：在锁外调用 LLM（可能耗时数秒）
	var sb strings.Builder
	for _, msg := range oldMessages {
		fmt.Fprintf(&sb, "%s: %s\n", msg.Role, msg.Content)
	}

	req := llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "请将以下对话历史压缩为简洁的摘要，保留关键信息和上下文。用中文回复。"},
			{Role: llm.RoleUser, Content: sb.String()},
		},
		MaxTokens: 500,
	}

	resp, err := provider.Complete(ctx, req)
	if err != nil {
		return fmt.Errorf("对话摘要生成失败: %w", err)
	}

	// 第三阶段：在锁内替换旧消息
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查消息列表是否在 LLM 调用期间被修改
	if len(m.messages) < snapshotLen {
		return nil // 被 Clear 了，不再替换
	}

	recentMessages := make([]TimedMessage, len(m.messages)-(snapshotLen-keepRecent))
	copy(recentMessages, m.messages[snapshotLen-keepRecent:])

	m.messages = append([]TimedMessage{
		{
			Role:      "system",
			Content:   fmt.Sprintf("[对话摘要] %s", resp.Content),
			Timestamp: time.Now(),
		},
	}, recentMessages...)

	return nil
}

// estimateTokens 估算文本的 Token 数
//
// 委托 ai-core tokenizer.CountGPT4（GPT-4 参数，中文感知），与全生态统一口径，
// 避免手写 runeCount/2 对英文高估、对中文漂移（RU-8）。
func estimateTokens(text string) int {
	n := tokenizer.CountGPT4(text)
	if n == 0 && text != "" {
		n = 1
	}
	return n
}
