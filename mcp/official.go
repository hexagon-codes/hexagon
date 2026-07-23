package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/tool"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ============== MCP Client V2 (消费外部 MCP Server) ==============

// MCPProxyToolV2 将官方 SDK 获取的远程 MCP 工具包装为 ai-core tool.Tool
//
// 使用官方 Go SDK 通信，支持最新协议特性。
//
// 示例：
//
//	tools, closer, _ := mcp.ConnectMCPServerV2(ctx, transport)
//	defer closer.Close()
//	agent := agent.New(agent.WithTools(tools...))
type MCPProxyToolV2 struct {
	// mcpTool 远程 MCP 工具定义
	mcpTool *sdkmcp.Tool

	// session 官方 SDK 客户端会话
	session *sdkmcp.ClientSession

	// cachedSchema 缓存的 ai-core Schema（从 MCP InputSchema 转换）
	cachedSchema *llm.Schema
}

func (t *MCPProxyToolV2) Name() string {
	return t.mcpTool.Name
}

func (t *MCPProxyToolV2) Description() string {
	return t.mcpTool.Description
}

func (t *MCPProxyToolV2) Schema() *llm.Schema {
	return t.cachedSchema
}

// Execute 调用远程 MCP 工具
func (t *MCPProxyToolV2) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	result, err := t.session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      t.mcpTool.Name,
		Arguments: args,
	})
	if err != nil {
		return tool.Result{}, fmt.Errorf("调用 MCP 工具 %s 失败: %w", t.mcpTool.Name, err)
	}

	// 提取文本内容
	var texts []string
	for _, content := range result.Content {
		if tc, ok := content.(*sdkmcp.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}

	output := strings.Join(texts, "\n")
	if result.IsError {
		// 对齐 V1 (proxy_tool.go) 契约：用 tool.NewErrorResult 构造结果，使
		// Success=false 且错误文本写入 Error 字段。直接放进 Output 会被
		// Result.String() 在 Success=false 时丢弃（只输出 "Error: " 前缀）。
		err := fmt.Errorf("MCP 工具 %s 返回错误: %s", t.mcpTool.Name, output)
		return tool.NewErrorResult(err), err
	}

	return tool.NewResult(output), nil
}

// Validate 校验工具参数
func (t *MCPProxyToolV2) Validate(args map[string]any) error {
	// 基础校验：检查必需参数
	if t.cachedSchema != nil {
		for _, required := range t.cachedSchema.Required {
			if _, ok := args[required]; !ok {
				return fmt.Errorf("缺少必需参数: %s", required)
			}
		}
	}
	return nil
}

var _ tool.Tool = (*MCPProxyToolV2)(nil)

// sessionCloser 组合 ClientSession 和 Transport 的关闭逻辑
type sessionCloser struct {
	session *sdkmcp.ClientSession
}

func (sc *sessionCloser) Close() error {
	return sc.session.Close()
}

// ConnectMCPServerV2 使用官方 SDK 连接 MCP Server 并获取工具列表
//
// 返回的 []tool.Tool 可直接用于 Hexagon Agent。
// 调用方需要在使用完毕后调用 closer.Close() 释放连接。
//
// transport: 官方 SDK 的 Transport（如 &mcp.CommandTransport{}, &mcp.SSEClientTransport{}）
//
// 示例：
//
//	// 连接 SSE 服务器
//	transport := &mcp.SSEClientTransport{Endpoint: "http://localhost:8080/sse"}
//	tools, closer, err := mcp.ConnectMCPServerV2(ctx, transport)
//	defer closer.Close()
func ConnectMCPServerV2(ctx context.Context, transport sdkmcp.Transport) ([]tool.Tool, io.Closer, error) {
	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "hexagon",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("连接 MCP Server 失败: %w", err)
	}

	// 获取工具列表
	toolsResult, err := session.ListTools(ctx, &sdkmcp.ListToolsParams{})
	if err != nil {
		session.Close()
		return nil, nil, fmt.Errorf("获取 MCP 工具列表失败: %w", err)
	}

	// 包装为 ai-core tool.Tool
	tools := make([]tool.Tool, 0, len(toolsResult.Tools))
	for _, mcpTool := range toolsResult.Tools {
		proxyTool := &MCPProxyToolV2{
			mcpTool:      mcpTool,
			session:      session,
			cachedSchema: sdkSchemaToAICore(mcpTool.InputSchema),
		}
		tools = append(tools, proxyTool)
	}

	return tools, &sessionCloser{session: session}, nil
}

// ConnectStdioServerV2 使用官方 SDK 连接 Stdio MCP Server
//
// 启动子进程并通过 stdin/stdout 通信。
// 返回的 cleanup 函数会终止子进程并释放资源。
//
// 示例：
//
//	tools, cleanup, err := mcp.ConnectStdioServerV2(ctx, "npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp")
//	defer cleanup()
func ConnectStdioServerV2(ctx context.Context, command string, args ...string) ([]tool.Tool, func(), error) {
	return ConnectStdioServerV2WithEnv(ctx, command, nil, args...)
}

// ConnectStdioServerV2WithEnv 同 ConnectStdioServerV2，但向子进程注入额外环境变量。
//
// 数据连接器走 MCP 的地基：MySQL/Redis 等 stdio MCP server 通过 env 配置连接信息
// （如 MYSQL_HOST / MYSQL_PASSWORD）。env 合并进 os.Environ()（保留 PATH 等，否则 npx/uvx 不可用）。
//
// 示例：
//
//	tools, cleanup, err := mcp.ConnectStdioServerV2WithEnv(ctx, "npx",
//	    map[string]string{"MYSQL_HOST": "localhost"}, "-y", "@benborla29/mcp-server-mysql")
func ConnectStdioServerV2WithEnv(ctx context.Context, command string, env map[string]string, args ...string) ([]tool.Tool, func(), error) {
	cmd := buildStdioCmd(ctx, command, env, args...)
	transport := &sdkmcp.CommandTransport{Command: cmd}

	tools, closer, err := ConnectMCPServerV2(ctx, transport)
	if err != nil {
		return nil, nil, err
	}

	cleanup := func() {
		closer.Close()
	}
	return tools, cleanup, nil
}

// buildStdioCmd 构造 stdio MCP server 的子进程命令。env 非空时合并进 os.Environ()
// （确定性排序，便于测试与复现）；env 为空时保持 cmd.Env=nil（继承父进程，不改既有行为）。
func buildStdioCmd(ctx context.Context, command string, env map[string]string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, command, args...)
	if len(env) > 0 {
		merged := os.Environ()
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			merged = append(merged, k+"="+env[k])
		}
		cmd.Env = merged
	}
	return cmd
}

// ConnectSSEServerV2 使用官方 SDK 连接 SSE MCP Server
//
// 通过 Server-Sent Events 协议通信，适合远程 MCP 服务。
//
// 示例：
//
//	tools, closer, err := mcp.ConnectSSEServerV2(ctx, "http://localhost:8080/sse")
//	defer closer.Close()
func ConnectSSEServerV2(ctx context.Context, endpoint string) ([]tool.Tool, io.Closer, error) {
	transport := &sdkmcp.SSEClientTransport{Endpoint: endpoint}
	return ConnectMCPServerV2(ctx, transport)
}

// ConnectStreamableServerV2 uses Streamable HTTP transport (MCP 2025-03-26 standard).
//
// Single-endpoint bidirectional communication via HTTP POST + optional SSE response.
//
// Example:
//
//	tools, closer, err := mcp.ConnectStreamableServerV2(ctx, "http://localhost:8080/mcp")
//	defer closer.Close()
func ConnectStreamableServerV2(ctx context.Context, endpoint string) ([]tool.Tool, io.Closer, error) {
	transport := &sdkmcp.StreamableClientTransport{Endpoint: endpoint}
	return ConnectMCPServerV2(ctx, transport)
}

// ============== MCP Server V2 (暴露 Hexagon Tool) ==============

// ServerV2 基于官方 SDK 的 MCP 服务器
//
// 将 Hexagon/ai-core 工具暴露为标准 MCP 服务，
// 支持 Stdio 和 HTTP 两种运行模式。
//
// 示例：
//
//	server := mcp.NewMCPServerV2("my-tools", "1.0.0")
//	server.RegisterTool(myCalculator)
//	server.RegisterTool(myFileReader)
//
//	// Stdio 模式（CLI 工具）
//	server.ServeStdio(ctx)
//
//	// 或 HTTP 模式
//	server.ServeHTTP(ctx, ":8080")
type ServerV2 struct {
	server *sdkmcp.Server
}

// NewMCPServerV2 创建基于官方 SDK 的 MCP 服务器
func NewMCPServerV2(name, version string) *ServerV2 {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    name,
		Version: version,
	}, nil)

	return &ServerV2{server: server}
}

// RegisterTool 注册单个 ai-core 工具到 MCP 服务器
//
// 自动将 ai-core Schema 转换为 MCP InputSchema
func (s *ServerV2) RegisterTool(t tool.Tool) {
	inputSchema := aicoreSchemaToSDK(t.Schema())

	mcpTool := &sdkmcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
		InputSchema: inputSchema,
	}

	handler := func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		// 解析参数
		var args map[string]any
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{
					&sdkmcp.TextContent{Text: fmt.Sprintf("参数解析失败: %v", err)},
				},
				IsError: true,
			}, nil
		}

		// 校验参数
		if err := t.Validate(args); err != nil {
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{
					&sdkmcp.TextContent{Text: fmt.Sprintf("参数校验失败: %v", err)},
				},
				IsError: true,
			}, nil
		}

		// 执行工具
		result, err := t.Execute(ctx, args)
		if err != nil {
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{
					&sdkmcp.TextContent{Text: err.Error()},
				},
				IsError: true,
			}, nil
		}

		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: fmt.Sprint(result.Output)},
			},
		}, nil
	}

	s.server.AddTool(mcpTool, handler)
}

// RegisterTools 批量注册 ai-core 工具
func (s *ServerV2) RegisterTools(tools ...tool.Tool) {
	for _, t := range tools {
		s.RegisterTool(t)
	}
}

// ServeStdio 以 Stdio 模式运行 MCP 服务器
//
// 通过 stdin/stdout 与客户端通信，适合作为 CLI 工具或 IDE 插件。
// 阻塞直到 context 取消或连接断开。
func (s *ServerV2) ServeStdio(ctx context.Context) error {
	return s.server.Run(ctx, &sdkmcp.StdioTransport{})
}

// HTTPHandler 返回基于 Streamable HTTP 传输的 http.Handler。
//
// 适合挂载到调用方自己的 mux/路由（如鉴权中间件之后），所有请求复用本服务器实例。
// Streamable HTTP 是 MCP 的现代远程传输（单端点 POST + 可选 SSE 响应）。
func (s *ServerV2) HTTPHandler() http.Handler {
	return sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return s.server },
		nil,
	)
}

// SSEHandler 返回基于 SSE 传输的 http.Handler（兼容较旧的 SSE 客户端）。
func (s *ServerV2) SSEHandler() http.Handler {
	return sdkmcp.NewSSEHandler(
		func(*http.Request) *sdkmcp.Server { return s.server },
		nil,
	)
}

// ServeHTTP 以 Streamable HTTP 模式在 addr 上运行 MCP 服务器。
//
// 阻塞直到 ctx 取消（取消时优雅关闭，最长等待 5s）。需要把 handler 挂到既有
// HTTP 服务时改用 HTTPHandler()。
//
// 注意：本方法签名为 (ctx, addr)，与 http.Handler.ServeHTTP(w, r) 不同，
// ServerV2 不是 http.Handler。
func (s *ServerV2) ServeHTTP(ctx context.Context, addr string) error {
	return serveHTTP(ctx, addr, s.HTTPHandler())
}

// ServeSSE 以 SSE 模式在 addr 上运行 MCP 服务器（兼容旧客户端）。
func (s *ServerV2) ServeSSE(ctx context.Context, addr string) error {
	return serveHTTP(ctx, addr, s.SSEHandler())
}

// serveHTTP 在 addr 上启动一个 HTTP 服务承载给定 handler，ctx 取消时优雅关闭。
func serveHTTP(ctx context.Context, addr string, h http.Handler) error {
	// ReadHeaderTimeout 防 Slowloris（慢速发送请求头耗尽连接）DoS。
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Server 返回底层官方 SDK Server，用于高级配置
func (s *ServerV2) Server() *sdkmcp.Server {
	return s.server
}

// ============== Schema 转换 ==============

// sdkSchemaToAICore 将官方 SDK 的 InputSchema (any) 转换为 ai-core Schema
//
// 官方 SDK 的 Tool.InputSchema 是 any 类型，
// 从服务端返回时通常是 map[string]any（JSON 解码的 schema）
func sdkSchemaToAICore(inputSchema any) *llm.Schema {
	if inputSchema == nil {
		return nil
	}

	// 将 any 类型转为 map[string]any
	var schemaMap map[string]any
	switch v := inputSchema.(type) {
	case map[string]any:
		schemaMap = v
	default:
		// 尝试 JSON 往返转换
		data, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		if err := json.Unmarshal(data, &schemaMap); err != nil {
			return nil
		}
	}

	return parseSchemaMap(schemaMap)
}

// parseSchemaMap 递归解析 JSON Schema map 为 ai-core Schema
func parseSchemaMap(m map[string]any) *llm.Schema {
	if m == nil {
		return nil
	}

	s := &llm.Schema{}

	if v, ok := m["type"].(string); ok {
		s.Type = v
	}
	if v, ok := m["title"].(string); ok {
		s.Title = v
	}
	if v, ok := m["description"].(string); ok {
		s.Description = v
	}
	if v, ok := m["default"]; ok {
		s.Default = v
	}
	if v, ok := m["minimum"].(float64); ok {
		s.Minimum = &v
	}
	if v, ok := m["maximum"].(float64); ok {
		s.Maximum = &v
	}
	if v, ok := schemaInt(m["minLength"]); ok {
		s.MinLength = &v
	}
	if v, ok := schemaInt(m["maxLength"]); ok {
		s.MaxLength = &v
	}
	if v, ok := m["pattern"].(string); ok {
		s.Pattern = v
	}
	if v, ok := m["format"].(string); ok {
		s.Format = v
	}

	// 解析 properties
	if props, ok := m["properties"].(map[string]any); ok {
		s.Properties = make(map[string]*llm.Schema, len(props))
		for name, propValue := range props {
			if propMap, ok := propValue.(map[string]any); ok {
				s.Properties[name] = parseSchemaMap(propMap)
			}
		}
	}

	// 解析 required
	s.Required = schemaStringSlice(m["required"])

	// 解析 items
	if items, ok := m["items"].(map[string]any); ok {
		s.Items = parseSchemaMap(items)
	}

	// 解析 enum
	if enum, ok := m["enum"].([]any); ok {
		s.Enum = make([]any, len(enum))
		copy(s.Enum, enum)
	}

	s.AnyOf = parseSchemaList(m["anyOf"])
	s.OneOf = parseSchemaList(m["oneOf"])
	s.AllOf = parseSchemaList(m["allOf"])
	if notMap, ok := m["not"].(map[string]any); ok {
		s.Not = parseSchemaMap(notMap)
	}

	switch additional := m["additionalProperties"].(type) {
	case bool:
		s.AdditionalProperties = additional
	case map[string]any:
		s.AdditionalProperties = parseSchemaMap(additional)
	}

	return s
}

func schemaStringSlice(raw any) []string {
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func schemaInt(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float64:
		return int(value), value == float64(int(value))
	default:
		return 0, false
	}
}

func parseSchemaList(raw any) []*llm.Schema {
	values, ok := raw.([]any)
	if !ok {
		return nil
	}
	result := make([]*llm.Schema, 0, len(values))
	for _, value := range values {
		if schemaMap, ok := value.(map[string]any); ok {
			result = append(result, parseSchemaMap(schemaMap))
		}
	}
	return result
}

// aicoreSchemaToSDK 将 ai-core Schema 转换为官方 SDK 接受的 InputSchema (map[string]any)
//
// 官方 SDK 的 server.AddTool 要求 InputSchema 的 type 必须为 "object"
func aicoreSchemaToSDK(s *llm.Schema) map[string]any {
	return aicoreSchemaToSDKMap(s, true)
}

func aicoreSchemaToSDKMap(s *llm.Schema, root bool) map[string]any {
	if s == nil {
		if !root {
			return map[string]any{}
		}
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	result := map[string]any{}

	if s.Type != "" {
		result["type"] = s.Type
	} else if root {
		result["type"] = "object"
	}

	if s.Title != "" {
		result["title"] = s.Title
	}
	if s.Description != "" {
		result["description"] = s.Description
	}
	if s.Default != nil {
		result["default"] = s.Default
	}
	if s.Minimum != nil {
		result["minimum"] = *s.Minimum
	}
	if s.Maximum != nil {
		result["maximum"] = *s.Maximum
	}
	if s.MinLength != nil {
		result["minLength"] = *s.MinLength
	}
	if s.MaxLength != nil {
		result["maxLength"] = *s.MaxLength
	}
	if s.Pattern != "" {
		result["pattern"] = s.Pattern
	}
	if s.Format != "" {
		result["format"] = s.Format
	}

	if len(s.Properties) > 0 {
		props := make(map[string]any, len(s.Properties))
		for name, prop := range s.Properties {
			props[name] = aicoreSchemaToSDKMap(prop, false)
		}
		result["properties"] = props
	} else if result["type"] == "object" {
		result["properties"] = map[string]any{}
	}

	if len(s.Required) > 0 {
		result["required"] = s.Required
	}

	if s.Items != nil {
		result["items"] = aicoreSchemaToSDKMap(s.Items, false)
	}

	if len(s.Enum) > 0 {
		result["enum"] = s.Enum
	}

	if len(s.AnyOf) > 0 {
		result["anyOf"] = schemaListToSDK(s.AnyOf)
	}
	if len(s.OneOf) > 0 {
		result["oneOf"] = schemaListToSDK(s.OneOf)
	}
	if len(s.AllOf) > 0 {
		result["allOf"] = schemaListToSDK(s.AllOf)
	}
	if s.Not != nil {
		result["not"] = aicoreSchemaToSDKMap(s.Not, false)
	}

	switch additional := s.AdditionalProperties.(type) {
	case bool:
		result["additionalProperties"] = additional
	case *llm.Schema:
		result["additionalProperties"] = aicoreSchemaToSDKMap(additional, false)
	case map[string]any:
		result["additionalProperties"] = additional
	}

	return result
}

func schemaListToSDK(schemas []*llm.Schema) []any {
	result := make([]any, 0, len(schemas))
	for _, item := range schemas {
		if item != nil {
			result = append(result, aicoreSchemaToSDKMap(item, false))
		}
	}
	return result
}
