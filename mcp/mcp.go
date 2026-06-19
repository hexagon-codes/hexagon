// Package mcp 实现 Model Context Protocol (MCP) 支持
//
// MCP 是 Anthropic 提出的标准协议，用于 LLM 与外部工具/数据源通信。
//
// 主要功能：
//   - MCP 客户端：连接 MCP 服务器，调用远程工具
//   - MCP 服务器：暴露本地工具为 MCP 服务
//   - 工具适配器：将 MCP 工具转换为 Hexagon 工具
//
// 使用示例：
//
//	client := mcp.NewClient("localhost:8080")
//	tools, _ := client.ListTools(ctx)
//	result, _ := client.CallTool(ctx, "calculator", map[string]any{"a": 1, "b": 2})
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ============== MCP 协议定义 ==============

// MCPVersion MCP 协议版本
const MCPVersion = "2024-11-05"

// MessageType 消息类型
type MessageType string

const (
	MessageTypeRequest      MessageType = "request"
	MessageTypeResponse     MessageType = "response"
	MessageTypeNotification MessageType = "notification"
)

// Method MCP 方法
type Method string

const (
	// 初始化
	MethodInitialize Method = "initialize"

	// 工具相关
	MethodToolsList Method = "tools/list"
	MethodToolsCall Method = "tools/call"

	// 资源相关
	MethodResourcesList      Method = "resources/list"
	MethodResourcesRead      Method = "resources/read"
	MethodResourcesSubscribe Method = "resources/subscribe"

	// 提示相关
	MethodPromptsList Method = "prompts/list"
	MethodPromptsGet  Method = "prompts/get"

	// 采样
	MethodSamplingCreateMessage Method = "sampling/createMessage"

	// 日志
	MethodLoggingSetLevel Method = "logging/setLevel"
)

// MCPRequest MCP 请求
type MCPRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  Method `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// MCPResponse MCP 响应
type MCPResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *MCPError `json:"error,omitempty"`
}

// MCPError MCP 错误
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// 标准错误码
const (
	ErrorCodeParseError     = -32700
	ErrorCodeInvalidRequest = -32600
	ErrorCodeMethodNotFound = -32601
	ErrorCodeInvalidParams  = -32602
	ErrorCodeInternalError  = -32603
)

// ============== MCP 工具定义 ==============

// Tool MCP 工具定义
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema *JSONSchema `json:"inputSchema"`
}

// JSONSchema JSON Schema 定义
type JSONSchema struct {
	Type        string                 `json:"type"`
	Properties  map[string]*JSONSchema `json:"properties,omitempty"`
	Required    []string               `json:"required,omitempty"`
	Description string                 `json:"description,omitempty"`
	Enum        []any                  `json:"enum,omitempty"`
	Items       *JSONSchema            `json:"items,omitempty"`
}

// ToolCallRequest 工具调用请求
type ToolCallRequest struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ToolCallResponse 工具调用响应
type ToolCallResponse struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock 内容块
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// ============== MCP 资源定义 ==============

// Resource MCP 资源
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ResourceContent 资源内容
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// ============== MCP 提示定义 ==============

// Prompt MCP 提示模板
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument 提示参数
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptMessage 提示消息
type PromptMessage struct {
	Role    string       `json:"role"`
	Content ContentBlock `json:"content"`
}

// ============== MCP 客户端 ==============

// Client MCP 客户端
type Client struct {
	endpoint   string
	httpClient *http.Client

	// 服务器能力
	serverCapabilities *ServerCapabilities

	// 请求 ID 生成
	nextID int64
	mu     sync.Mutex
}

// ServerCapabilities 服务器能力
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
	Logging   *LoggingCapability   `json:"logging,omitempty"`
}

// ToolsCapability 工具能力
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability 资源能力
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptsCapability 提示能力
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// LoggingCapability 日志能力
type LoggingCapability struct{}

// ClientOption 客户端选项
type ClientOption func(*Client)

// WithHTTPClientOption 设置 HTTP 客户端
func WithHTTPClientOption(client *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = client
	}
}

// NewClient 创建 MCP 客户端
func NewClient(endpoint string, opts ...ClientOption) *Client {
	c := &Client{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Initialize 初始化客户端
func (c *Client) Initialize(ctx context.Context) error {
	resp, err := c.call(ctx, MethodInitialize, map[string]any{
		"protocolVersion": MCPVersion,
		"capabilities": map[string]any{
			"roots": map[string]any{
				"listChanged": true,
			},
		},
		"clientInfo": map[string]any{
			"name":    "hexagon-mcp-client",
			"version": "1.0.0",
		},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	// 解析服务器能力
	if result, ok := resp.Result.(map[string]any); ok {
		if caps, ok := result["capabilities"]; ok {
			capsBytes, _ := json.Marshal(caps)
			var serverCaps ServerCapabilities
			// 守卫 Unmarshal 错误：畸形 capabilities（如字符串而非对象）解析失败时
			// 不应把半解析/零值的 caps 写入 serverCapabilities，对齐 transport.go 行为。
			if err := json.Unmarshal(capsBytes, &serverCaps); err == nil {
				c.serverCapabilities = &serverCaps
			}
		}
	}

	return nil
}

// ListTools 列出可用工具
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	resp, err := c.call(ctx, MethodToolsList, nil)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	toolsRaw, ok := result["tools"]
	if !ok {
		return nil, nil
	}

	toolsBytes, _ := json.Marshal(toolsRaw)
	var tools []Tool
	if err := json.Unmarshal(toolsBytes, &tools); err != nil {
		return nil, fmt.Errorf("parse tools: %w", err)
	}

	return tools, nil
}

// CallTool 调用工具
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResponse, error) {
	resp, err := c.call(ctx, MethodToolsCall, ToolCallRequest{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("call tool: %w", err)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var toolResp ToolCallResponse
	if err := json.Unmarshal(resultBytes, &toolResp); err != nil {
		return nil, fmt.Errorf("parse tool response: %w", err)
	}

	return &toolResp, nil
}

// ListResources 列出可用资源
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	resp, err := c.call(ctx, MethodResourcesList, nil)
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	resourcesRaw, ok := result["resources"]
	if !ok {
		return nil, nil
	}

	resourcesBytes, _ := json.Marshal(resourcesRaw)
	var resources []Resource
	if err := json.Unmarshal(resourcesBytes, &resources); err != nil {
		return nil, fmt.Errorf("parse resources: %w", err)
	}

	return resources, nil
}

// ReadResource 读取资源
func (c *Client) ReadResource(ctx context.Context, uri string) (*ResourceContent, error) {
	resp, err := c.call(ctx, MethodResourcesRead, map[string]any{
		"uri": uri,
	})
	if err != nil {
		return nil, fmt.Errorf("read resource: %w", err)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	contentsRaw, ok := result["contents"]
	if !ok {
		return nil, fmt.Errorf("no contents in response")
	}

	contentsList, ok := contentsRaw.([]any)
	if !ok || len(contentsList) == 0 {
		return nil, fmt.Errorf("empty contents")
	}

	contentBytes, _ := json.Marshal(contentsList[0])
	var content ResourceContent
	if err := json.Unmarshal(contentBytes, &content); err != nil {
		return nil, fmt.Errorf("parse content: %w", err)
	}

	return &content, nil
}

// ListPrompts 列出可用提示
func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error) {
	resp, err := c.call(ctx, MethodPromptsList, nil)
	if err != nil {
		return nil, fmt.Errorf("list prompts: %w", err)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	promptsRaw, ok := result["prompts"]
	if !ok {
		return nil, nil
	}

	promptsBytes, _ := json.Marshal(promptsRaw)
	var prompts []Prompt
	if err := json.Unmarshal(promptsBytes, &prompts); err != nil {
		return nil, fmt.Errorf("parse prompts: %w", err)
	}

	return prompts, nil
}

// GetPrompt 获取提示
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) ([]PromptMessage, error) {
	resp, err := c.call(ctx, MethodPromptsGet, map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return nil, fmt.Errorf("get prompt: %w", err)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	messagesRaw, ok := result["messages"]
	if !ok {
		return nil, nil
	}

	messagesBytes, _ := json.Marshal(messagesRaw)
	var messages []PromptMessage
	if err := json.Unmarshal(messagesBytes, &messages); err != nil {
		return nil, fmt.Errorf("parse messages: %w", err)
	}

	return messages, nil
}

// call 发送 RPC 调用
func (c *Client) call(ctx context.Context, method Method, params any) (*MCPResponse, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp MCPResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	return &resp, nil
}

// GetServerCapabilities 获取服务器能力
func (c *Client) GetServerCapabilities() *ServerCapabilities {
	return c.serverCapabilities
}
