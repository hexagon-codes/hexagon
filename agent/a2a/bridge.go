package a2a

import (
	"context"
	"fmt"

	"github.com/hexagon-codes/hexagon/agent"
)

// ============== AgentWrapper ==============

// AgentWrapper 将 Hexagon Agent 包装为 A2A TaskHandler
// 使得任意 Hexagon Agent 都可以作为 A2A 服务暴露。
//
// 使用示例:
//
//	// 创建 Hexagon Agent
//	myAgent := agent.NewReActAgent(
//	    agent.WithName("assistant"),
//	    agent.WithLLM(llm),
//	)
//
//	// 包装为 A2A Handler
//	handler := a2a.WrapAgent(myAgent)
//
//	// 创建 A2A Server
//	card := a2a.AgentInfoToCard(myAgent.Info(), "http://localhost:8080")
//	server := a2a.NewServer(card, handler)
type AgentWrapper struct {
	// agent 被包装的 Hexagon Agent
	agent agent.Agent
}

// WrapAgent 将 Hexagon Agent 包装为 A2A TaskHandler
func WrapAgent(a agent.Agent) *AgentWrapper {
	return &AgentWrapper{agent: a}
}

// HandleTask 实现 TaskHandler 接口
func (w *AgentWrapper) HandleTask(ctx context.Context, task *Task, msg *Message) (*TaskUpdate, error) {
	// 转换消息为 Agent Input
	input := MessageToAgentInput(msg)

	// 添加任务上下文
	if input.Context == nil {
		input.Context = make(map[string]any)
	}
	input.Context["taskId"] = task.ID
	input.Context["sessionId"] = task.SessionID

	// 调用 Agent
	output, err := w.agent.Run(ctx, input)
	if err != nil {
		return NewFailedUpdate(err.Error()), nil
	}

	// 转换为 A2A Message
	respMsg := AgentOutputToMessage(output)

	return NewCompletedUpdate(&respMsg), nil
}

// ============== StreamingAgentWrapper ==============

// StreamingAgentWrapper 流式 Agent 包装器
// 使用 Agent 的 Stream 方法实现流式输出。
type StreamingAgentWrapper struct {
	*AgentWrapper
}

// WrapStreamingAgent 包装 Agent 为流式处理器
// Agent 必须实现 Stream 方法（所有实现 Runnable 接口的 Agent 都支持）
func WrapStreamingAgent(a agent.Agent) *StreamingAgentWrapper {
	return &StreamingAgentWrapper{
		AgentWrapper: WrapAgent(a),
	}
}

// HandleTaskStream 实现 StreamingTaskHandler 接口
func (w *StreamingAgentWrapper) HandleTaskStream(ctx context.Context, task *Task, msg *Message) (<-chan *TaskUpdate, error) {
	// 转换消息为 Agent Input
	input := MessageToAgentInput(msg)

	// 添加任务上下文
	if input.Context == nil {
		input.Context = make(map[string]any)
	}
	input.Context["taskId"] = task.ID
	input.Context["sessionId"] = task.SessionID

	// 调用 Agent 流式接口
	streamReader, err := w.agent.Stream(ctx, input)
	if err != nil {
		return nil, err
	}

	// 创建更新通道
	updates := make(chan *TaskUpdate)

	go func() {
		defer close(updates)

		var fullContent string
		artifactIndex := 0

		for {
			output, err := streamReader.Recv()
			if err != nil {
				// 流结束或错误
				if fullContent != "" {
					respMsg := &Message{
						Role: RoleAgent,
						Parts: []Part{
							&TextPart{Text: fullContent},
						},
					}
					updates <- NewCompletedUpdate(respMsg)
				} else {
					updates <- NewFailedUpdate(err.Error())
				}
				return
			}

			// 处理内容块
			if output.Content != "" {
				fullContent += output.Content

				// 发送产物更新
				updates <- &TaskUpdate{
					Artifact: &Artifact{
						Name:   "response",
						Index:  artifactIndex,
						Append: artifactIndex > 0,
						Parts: []Part{
							&TextPart{Text: output.Content},
						},
					},
				}
				artifactIndex++
			}
		}
	}()

	return updates, nil
}

// ============== NetworkBridge ==============

// NetworkBridge 将 A2A 消息桥接到 Hexagon AgentNetwork
// 使得 A2A 客户端可以与 Hexagon Agent 网络中的 Agent 通信。
type NetworkBridge struct {
	// network Agent 网络
	network *agent.AgentNetwork

	// taskStore 任务存储（用于跟踪消息）
	taskStore TaskStore
}

// NewNetworkBridge 创建网络桥接
func NewNetworkBridge(network *agent.AgentNetwork, store TaskStore) *NetworkBridge {
	return &NetworkBridge{
		network:   network,
		taskStore: store,
	}
}

// SendToAgent 将 A2A 消息发送到网络中的 Agent
func (b *NetworkBridge) SendToAgent(ctx context.Context, agentID string, msg *Message) (*Message, error) {
	// 获取目标 Agent
	targetAgent, ok := b.network.GetAgent(agentID)
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}

	// 转换消息并执行
	input := MessageToAgentInput(msg)
	output, err := targetAgent.Run(ctx, input)
	if err != nil {
		return nil, err
	}

	// 转换响应
	respMsg := AgentOutputToMessage(output)
	return &respMsg, nil
}

// BroadcastMessage 广播消息到网络中的所有 Agent
func (b *NetworkBridge) BroadcastMessage(ctx context.Context, fromAgent string, msg *Message) error {
	return b.network.Broadcast(ctx, fromAgent, msg.GetTextContent())
}

// SendToAgentNetwork 通过网络消息路由发送
func (b *NetworkBridge) SendToAgentNetwork(ctx context.Context, fromAgent, toAgent string, msg *Message) error {
	return b.network.SendTo(ctx, fromAgent, toAgent, msg.GetTextContent())
}

// ============== A2AClient 作为 Agent ==============

// RemoteAgent 将远程 A2A Agent 包装为 Hexagon Agent
// 使得 A2A Agent 可以参与 Hexagon Agent 网络。
type RemoteAgent struct {
	// id Agent ID
	id string

	// name Agent 名称
	name string

	// client A2A 客户端
	client *Client

	// card Agent Card
	card *AgentCard

	// sessionID 会话 ID（用于多轮对话）
	sessionID string

	// currentTaskID 当前任务 ID
	currentTaskID string
}

// NewRemoteAgent 创建远程 Agent
func NewRemoteAgent(url string, opts ...ClientOption) (*RemoteAgent, error) {
	client := NewClient(url, opts...)

	// 获取 Agent Card
	card, err := client.GetAgentCard(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get agent card: %w", err)
	}

	return &RemoteAgent{
		id:     card.URL,
		name:   card.Name,
		client: client,
		card:   card,
	}, nil
}

// ID 返回 Agent ID
func (a *RemoteAgent) ID() string {
	return a.id
}

// Name 返回 Agent 名称
func (a *RemoteAgent) Name() string {
	return a.name
}

// Run 执行 Agent
func (a *RemoteAgent) Run(ctx context.Context, input agent.Input) (agent.Output, error) {
	// 构建 A2A 消息
	msg := AgentInputToMessage(input)

	// 发送消息
	task, err := a.client.SendMessage(ctx, &SendMessageRequest{
		TaskID:    a.currentTaskID,
		SessionID: a.sessionID,
		Message:   msg,
	})
	if err != nil {
		return agent.Output{}, err
	}

	// 更新当前任务 ID
	a.currentTaskID = task.ID
	if a.sessionID == "" {
		a.sessionID = task.SessionID
	}

	// 等待任务完成
	for !task.Status.State.IsTerminal() {
		task, err = a.client.GetTask(ctx, task.ID)
		if err != nil {
			return agent.Output{}, err
		}
	}

	// 检查状态
	if task.Status.State == TaskStateFailed {
		errMsg := "task failed"
		if task.Status.Message != nil {
			errMsg = task.Status.Message.GetTextContent()
		}
		return agent.Output{}, fmt.Errorf("task failed: %s", errMsg)
	}

	// 获取最后一条 Agent 消息
	var lastAgentMsg *Message
	for i := len(task.History) - 1; i >= 0; i-- {
		if task.History[i].Role == RoleAgent {
			lastAgentMsg = &task.History[i]
			break
		}
	}

	if lastAgentMsg == nil {
		return agent.Output{}, fmt.Errorf("no agent response")
	}

	return MessageToAgentOutput(lastAgentMsg), nil
}

// Card 返回 Agent Card
func (a *RemoteAgent) Card() *AgentCard {
	return a.card
}

// Close 关闭远程 Agent
func (a *RemoteAgent) Close() error {
	return a.client.Close()
}

// NewSession 开始新会话
func (a *RemoteAgent) NewSession() {
	a.sessionID = ""
	a.currentTaskID = ""
}

// ============== 便捷函数 ==============

// ExposeAgent 将 Hexagon Agent 作为 A2A 服务暴露
// 返回一个可直接启动的 A2A Server。
func ExposeAgent(a agent.Agent, baseURL string, opts ...ServerOption) *Server {
	// 创建 Agent Card
	info := &agent.AgentInfo{
		ID:   a.ID(),
		Name: a.Name(),
	}

	// 如果 Agent 有更多信息，尝试获取
	if infoProvider, ok := a.(interface{ Info() *agent.AgentInfo }); ok {
		info = infoProvider.Info()
	}

	card := AgentInfoToCard(info, baseURL)
	card.Capabilities.Streaming = true

	// 创建 Handler（所有 Agent 都支持 Stream 方法）
	handler := WrapStreamingAgent(a)

	return NewServer(card, handler, opts...)
}

// ConnectToA2AAgent 连接到远程 A2A Agent
// 返回一个可用于 Hexagon Agent 网络的 Agent。
func ConnectToA2AAgent(url string, opts ...ClientOption) (*RemoteAgent, error) {
	return NewRemoteAgent(url, opts...)
}
