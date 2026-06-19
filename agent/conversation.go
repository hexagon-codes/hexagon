// conversation.go 提供多轮对话 Agent 封装
//
// ConversationAgent 在普通 Agent 之上增加对话历史管理，
// 自动将历史上下文注入到每次请求中。
//
// 使用示例：
//
//	conv := agent.NewConversation(myAgent,
//	    agent.WithConvMaxTurns(20),
//	    agent.WithConvMaxTokens(4096),
//	)
//	output, err := conv.Chat(ctx, "你好")
//	output, err = conv.Chat(ctx, "继续上面的话题")
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/tokenizer"
)

// ============== 对话消息 ==============

// ConvMessage 对话消息记录
//
// 注意：与 llm/conversation.TimedMessage 结构相似，但属于不同包。
// agent 包不直接依赖 llm/conversation 包，以避免循环依赖。
type ConvMessage struct {
	// Role 角色（user/assistant）
	Role string `json:"role"`

	// Content 内容
	Content string `json:"content"`

	// Timestamp 时间戳
	Timestamp time.Time `json:"timestamp"`
}

// ============== 对话 Agent ==============

// ConversationAgent 多轮对话 Agent
//
// 封装一个 Agent，自动维护对话历史并将上下文注入到每次调用。
// 线程安全。
type ConversationAgent struct {
	agent     Agent
	messages  []ConvMessage
	maxTurns  int // 最大轮次（0 不限）
	maxTokens int // Token 预算上限（0 不限）
	mu        sync.RWMutex
}

// ConversationOption 对话 Agent 配置选项
type ConversationOption func(*ConversationAgent)

// WithConvMaxTurns 设置最大对话轮次
func WithConvMaxTurns(n int) ConversationOption {
	return func(c *ConversationAgent) {
		c.maxTurns = n
	}
}

// WithConvMaxTokens 设置 Token 预算上限
func WithConvMaxTokens(n int) ConversationOption {
	return func(c *ConversationAgent) {
		c.maxTokens = n
	}
}

// NewConversation 创建多轮对话 Agent
func NewConversation(agent Agent, opts ...ConversationOption) *ConversationAgent {
	c := &ConversationAgent{
		agent:     agent,
		maxTurns:  50,
		maxTokens: 8192,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Chat 发送用户消息并获取回复
//
// 流程：
//  1. 添加用户消息到历史
//  2. 构建上下文字符串（在 Token 预算内）
//  3. 调用内部 Agent
//  4. 添加助手回复到历史
//  5. 返回结果
func (c *ConversationAgent) Chat(ctx context.Context, userMessage string) (Output, error) {
	c.mu.Lock()

	// 添加用户消息
	c.messages = append(c.messages, ConvMessage{
		Role:      "user",
		Content:   userMessage,
		Timestamp: time.Now(),
	})

	// 构建上下文
	contextStr := c.buildContext()
	c.mu.Unlock()

	// 调用内部 Agent
	input := Input{
		Query: userMessage,
		Context: map[string]any{
			"conversation_history": contextStr,
		},
	}

	output, err := c.agent.Run(ctx, input)
	if err != nil {
		// 回滚用户消息，避免失败消息残留在历史中
		c.mu.Lock()
		if len(c.messages) > 0 {
			c.messages = c.messages[:len(c.messages)-1]
		}
		c.mu.Unlock()
		return output, err
	}

	// 添加助手回复
	c.mu.Lock()
	c.messages = append(c.messages, ConvMessage{
		Role:      "assistant",
		Content:   output.Content,
		Timestamp: time.Now(),
	})
	c.mu.Unlock()

	return output, nil
}

// buildContext 构建对话上下文字符串（需持锁调用）
//
// 从最近的消息向前填充，确保不超过 Token 预算。
// Token 估算复用 ai-core/tokenizer，按字符类型（中文/英文/特殊）分别估算，
// 比按字节长度粗算更贴近真实 Token 数。
func (c *ConversationAgent) buildContext() string {
	msgs := c.messages

	// 应用轮次限制
	if c.maxTurns > 0 {
		userCount := 0
		startIdx := len(msgs)
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == "user" {
				userCount++
				if userCount > c.maxTurns {
					break
				}
				startIdx = i
			}
		}
		if startIdx < len(msgs) {
			msgs = msgs[startIdx:]
		}
	}

	// 应用 Token 预算
	if c.maxTokens > 0 {
		usedTokens := 0
		startIdx := len(msgs)
		for i := len(msgs) - 1; i >= 0; i-- {
			// 复用 ai-core/tokenizer 估算 Token 数（GPT-4 参数，含缓存计数器，无额外分配）
			tokens := tokenizer.CountGPT4(msgs[i].Content)
			if tokens == 0 {
				tokens = 1
			}
			if usedTokens+tokens > c.maxTokens {
				break
			}
			usedTokens += tokens
			startIdx = i
		}
		if startIdx < len(msgs) {
			msgs = msgs[startIdx:]
		}
	}

	// 构建上下文字符串
	var sb strings.Builder
	for _, msg := range msgs[:len(msgs)-1] { // 排除最后一条（当前用户消息）
		sb.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
	}
	return sb.String()
}

// History 返回对话历史副本
func (c *ConversationAgent) History() []ConvMessage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]ConvMessage, len(c.messages))
	copy(result, c.messages)
	return result
}

// ClearHistory 清空对话历史
func (c *ConversationAgent) ClearHistory() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = nil
}

// TurnCount 返回用户轮次数
func (c *ConversationAgent) TurnCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	count := 0
	for _, msg := range c.messages {
		if msg.Role == "user" {
			count++
		}
	}
	return count
}

// Agent 返回内部 Agent
func (c *ConversationAgent) Agent() Agent {
	return c.agent
}
