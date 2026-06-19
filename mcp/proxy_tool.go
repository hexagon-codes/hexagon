// Package mcp 实现 Model Context Protocol (MCP) 支持
//
// 本文件实现将远程 MCP 工具包装为 ai-core tool.Tool 的代理
package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/tool"
)

// MCPProxyTool 将远程 MCP 工具包装为 ai-core tool.Tool
//
// 这是 MCP 适配层的核心组件，它允许将任意 MCP 服务器上的工具
// 直接用于 Hexagon Agent，无需任何额外适配代码。
//
// 示例：
//
//	// 连接 MCP 服务器并获取工具
//	tools, _ := mcp.ConnectMCPServer(ctx, "http://localhost:8080")
//
//	// 直接用于 Agent
//	agent := agent.New(
//	    agent.WithTools(tools...),
//	)
type MCPProxyTool struct {
	// MCP 工具定义
	mcpTool Tool

	// 传输客户端（用于调用远程工具）
	client *TransportClient

	// 缓存的 ai-core Schema
	cachedSchema *llm.Schema
}

// NewMCPProxyTool 创建 MCP 代理工具
//
// mcpTool 是从 MCP 服务器获取的工具定义
// client 是用于调用工具的传输客户端
func NewMCPProxyTool(mcpTool Tool, client *TransportClient) *MCPProxyTool {
	return &MCPProxyTool{
		mcpTool:      mcpTool,
		client:       client,
		cachedSchema: SchemaFromMCP(mcpTool.InputSchema),
	}
}

// Name 返回工具名称
func (t *MCPProxyTool) Name() string {
	return t.mcpTool.Name
}

// Description 返回工具描述
func (t *MCPProxyTool) Description() string {
	return t.mcpTool.Description
}

// Schema 返回工具参数的 JSON Schema
func (t *MCPProxyTool) Schema() *llm.Schema {
	return t.cachedSchema
}

// Execute 执行工具
//
// 将调用请求发送到远程 MCP 服务器，并将响应转换为 tool.Result
func (t *MCPProxyTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	// 调用远程工具
	resp, err := t.client.CallTool(ctx, t.mcpTool.Name, args)
	if err != nil {
		return tool.NewErrorResult(err), nil
	}

	// 检查工具执行是否报错
	if resp.IsError {
		errMsg := "工具执行失败"
		if len(resp.Content) > 0 && resp.Content[0].Text != "" {
			errMsg = resp.Content[0].Text
		}
		return tool.NewErrorResult(fmt.Errorf("%s", errMsg)), nil
	}

	// 合并所有文本内容
	var result strings.Builder
	for _, content := range resp.Content {
		switch content.Type {
		case "text":
			result.WriteString(content.Text)
		case "image":
			// 图片类型，返回 data URI 或提示
			if content.Data != "" {
				fmt.Fprintf(&result, "[图片: %s]", content.MimeType)
			}
		}
	}

	return tool.NewResult(result.String()), nil
}

// Validate 验证参数
func (t *MCPProxyTool) Validate(args map[string]any) error {
	// 基于 Schema 验证必填字段
	if t.cachedSchema != nil && len(t.cachedSchema.Required) > 0 {
		for _, field := range t.cachedSchema.Required {
			if _, ok := args[field]; !ok {
				return fmt.Errorf("缺少必填字段: %s", field)
			}
		}
	}
	return nil
}

// ============== MCP 工具集合 ==============

// MCPToolSet 管理一组来自同一 MCP 服务器的工具
//
// 使用 MCPToolSet 可以方便地管理多个 MCP 工具，
// 并提供统一的生命周期管理（如关闭连接）
type MCPToolSet struct {
	client *TransportClient

	mu        sync.RWMutex
	tools     []tool.Tool
	onChange  func([]tool.Tool) // 可选：工具集变化回调（Refresh 后触发）
	reconnect *ReconnectConfig  // 可选：自动重连策略（nil=禁用，Refresh 失败不重连）
}

// NewMCPToolSet 从 MCP 服务器创建工具集合
func NewMCPToolSet(client *TransportClient, mcpTools []Tool) *MCPToolSet {
	tools := make([]tool.Tool, len(mcpTools))
	for i, t := range mcpTools {
		tools[i] = NewMCPProxyTool(t, client)
	}
	return &MCPToolSet{
		client: client,
		tools:  tools,
	}
}

// Tools 返回所有工具（并发安全：与 Refresh 互斥）
func (s *MCPToolSet) Tools() []tool.Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tools
}

// Get 按名称获取工具
func (s *MCPToolSet) Get(name string) (tool.Tool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tools {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}

// SetOnToolsChanged 注册工具集变化回调。
//
// Refresh 重建工具集后会触发该回调，便于上层（如 Agent 工具注册表）同步
// 动态发现的工具集。传 nil 可清除回调。
func (s *MCPToolSet) SetOnToolsChanged(fn func([]tool.Tool)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// Refresh 重新从 MCP 服务器拉取工具列表并重建工具集（动态发现）。
//
// 适用场景：收到 notifications/tools/list_changed 通知时、或需要主动刷新远端
// 工具时调用。客户端在初始化握手中已声明 tools.listChanged 能力，服务器据此
// 在工具集变化时推送通知；上层把通知路由到本方法即可实现动态工具发现。
// 成功刷新后若注册了 OnToolsChanged 回调会被触发。
// 若配置了重连策略（SetReconnectPolicy），首次拉取失败时会按指数退避自动重连
// 后再列一次；重连仍失败才返回错误。未配置策略时行为不变（失败即返回）。
func (s *MCPToolSet) Refresh(ctx context.Context) error {
	err := s.listAndRebuild(ctx)
	if err == nil {
		return nil
	}

	s.mu.RLock()
	cfg := s.reconnect
	s.mu.RUnlock()
	if cfg != nil {
		if reconnErr := s.reconnectWithBackoff(ctx, cfg); reconnErr == nil {
			if listErr := s.listAndRebuild(ctx); listErr == nil {
				return nil
			}
		}
	}
	return fmt.Errorf("mcp: 刷新工具列表失败: %w", err)
}

// listAndRebuild 拉取工具列表并重建工具集（不含重连）。
func (s *MCPToolSet) listAndRebuild(ctx context.Context) error {
	mcpTools, err := s.client.ListTools(ctx)
	if err != nil {
		return err
	}
	s.rebuild(mcpTools)
	return nil
}

// HandleListChanged 是 MCP notifications/tools/list_changed 通知的处理入口，
// 语义等价于 Refresh——收到该通知时调用即可动态更新工具集。
func (s *MCPToolSet) HandleListChanged(ctx context.Context) error {
	return s.Refresh(ctx)
}

// rebuild 用新的 MCP 工具列表重建代理工具集并触发变化回调。
func (s *MCPToolSet) rebuild(mcpTools []Tool) {
	tools := make([]tool.Tool, len(mcpTools))
	for i, t := range mcpTools {
		tools[i] = NewMCPProxyTool(t, s.client)
	}
	s.mu.Lock()
	s.tools = tools
	cb := s.onChange
	s.mu.Unlock()

	if cb != nil {
		cb(tools)
	}
}

// Close 关闭工具集合（关闭底层传输连接）
func (s *MCPToolSet) Close() error {
	return s.client.Close()
}

// ============== 便捷函数 ==============

// WrapMCPTools 将 MCP 工具列表包装为 ai-core tool.Tool 列表
//
// 这是一个便捷函数，用于快速将 MCP 工具转换为 Hexagon 可用的工具
//
// 示例：
//
//	mcpTools, _ := client.ListTools(ctx)
//	tools := mcp.WrapMCPTools(client, mcpTools)
func WrapMCPTools(client *TransportClient, mcpTools []Tool) []tool.Tool {
	tools := make([]tool.Tool, len(mcpTools))
	for i, t := range mcpTools {
		tools[i] = NewMCPProxyTool(t, client)
	}
	return tools
}

// ConnectMCPServer 连接 MCP 服务器并返回工具列表
//
// Deprecated: 请使用 ConnectMCPServerV2，基于官方 Go SDK 实现。
//
// 这是最常用的便捷函数，一行代码即可获取远程 MCP 工具
//
// 示例：
//
//	tools, err := mcp.ConnectMCPServer(ctx, "http://localhost:8080")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	agent := agent.New(
//	    agent.WithTools(tools...),
//	)
func ConnectMCPServer(ctx context.Context, endpoint string) ([]tool.Tool, error) {
	// 创建 HTTP 传输
	transport := NewHTTPTransport(endpoint)

	// 创建客户端
	client := NewTransportClient(transport)

	// 初始化连接
	if err := client.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("初始化 MCP 客户端失败: %w", err)
	}

	// 获取工具列表
	mcpTools, err := client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取工具列表失败: %w", err)
	}

	// 包装为 ai-core 工具
	return WrapMCPTools(client, mcpTools), nil
}

// ConnectMCPServerWithToolSet 连接 MCP 服务器并返回工具集合
//
// Deprecated: 请使用 ConnectMCPServerV2，基于官方 Go SDK 实现。
//
// 与 ConnectMCPServer 类似，但返回 MCPToolSet 以便管理生命周期
//
// 示例：
//
//	toolSet, err := mcp.ConnectMCPServerWithToolSet(ctx, "http://localhost:8080")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer toolSet.Close()
//
//	agent := agent.New(
//	    agent.WithTools(toolSet.Tools()...),
//	)
func ConnectMCPServerWithToolSet(ctx context.Context, endpoint string) (*MCPToolSet, error) {
	// 创建 HTTP 传输
	transport := NewHTTPTransport(endpoint)

	// 创建客户端
	client := NewTransportClient(transport)

	// 初始化连接
	if err := client.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("初始化 MCP 客户端失败: %w", err)
	}

	// 获取工具列表
	mcpTools, err := client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取工具列表失败: %w", err)
	}

	return NewMCPToolSet(client, mcpTools), nil
}
