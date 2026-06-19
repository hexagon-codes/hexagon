// Package mcp 实现 Model Context Protocol (MCP) 支持
//
// 本文件实现 MCP 服务器，用于将 Hexagon 工具暴露为 MCP 服务
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Server MCP 服务器
type Server struct {
	// 工具注册
	tools   map[string]*RegisteredTool
	toolsMu sync.RWMutex

	// 资源注册
	resources   map[string]*RegisteredResource
	resourcesMu sync.RWMutex

	// 提示注册
	prompts   map[string]*RegisteredPrompt
	promptsMu sync.RWMutex

	// 配置
	config *ServerConfig

	// HTTP 服务器
	httpServer *http.Server
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Addr    string `json:"addr"`
}

// DefaultServerConfig 默认服务器配置
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		Name:    "hexagon-mcp-server",
		Version: "1.0.0",
		Addr:    ":8080",
	}
}

// RegisteredTool 注册的工具
type RegisteredTool struct {
	Tool    Tool
	Handler ToolHandler
}

// ToolHandler 工具处理函数
type ToolHandler func(ctx context.Context, args map[string]any) (*ToolCallResponse, error)

// RegisteredResource 注册的资源
type RegisteredResource struct {
	Resource Resource
	Handler  ResourceHandler
}

// ResourceHandler 资源处理函数
type ResourceHandler func(ctx context.Context) (*ResourceContent, error)

// RegisteredPrompt 注册的提示
type RegisteredPrompt struct {
	Prompt  Prompt
	Handler PromptHandler
}

// PromptHandler 提示处理函数
type PromptHandler func(ctx context.Context, args map[string]string) ([]PromptMessage, error)

// NewServer 创建 MCP 服务器
func NewServer(config *ServerConfig) *Server {
	if config == nil {
		config = DefaultServerConfig()
	}
	return &Server{
		tools:     make(map[string]*RegisteredTool),
		resources: make(map[string]*RegisteredResource),
		prompts:   make(map[string]*RegisteredPrompt),
		config:    config,
	}
}

// RegisterTool 注册工具
func (s *Server) RegisterTool(tool Tool, handler ToolHandler) {
	s.toolsMu.Lock()
	defer s.toolsMu.Unlock()
	s.tools[tool.Name] = &RegisteredTool{
		Tool:    tool,
		Handler: handler,
	}
}

// RegisterResource 注册资源
func (s *Server) RegisterResource(resource Resource, handler ResourceHandler) {
	s.resourcesMu.Lock()
	defer s.resourcesMu.Unlock()
	s.resources[resource.URI] = &RegisteredResource{
		Resource: resource,
		Handler:  handler,
	}
}

// RegisterPrompt 注册提示
func (s *Server) RegisterPrompt(prompt Prompt, handler PromptHandler) {
	s.promptsMu.Lock()
	defer s.promptsMu.Unlock()
	s.prompts[prompt.Name] = &RegisteredPrompt{
		Prompt:  prompt,
		Handler: handler,
	}
}

// Start 启动服务器
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRequest)

	s.httpServer = &http.Server{
		Addr:    s.config.Addr,
		Handler: mux,
		// ReadHeaderTimeout 防 Slowloris（慢速请求头）DoS。
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s.httpServer.ListenAndServe()
}

// Stop 停止服务器
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

// handleRequest 处理请求
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeError(w, nil, ErrorCodeParseError, "Failed to read request body")
		return
	}

	var req MCPRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeError(w, nil, ErrorCodeParseError, "Invalid JSON")
		return
	}

	resp := s.processRequest(r.Context(), &req)
	s.writeResponse(w, resp)
}

// processRequest 处理 MCP 请求
func (s *Server) processRequest(ctx context.Context, req *MCPRequest) *MCPResponse {
	switch req.Method {
	case MethodInitialize:
		return s.handleInitialize(req)
	case MethodToolsList:
		return s.handleToolsList(req)
	case MethodToolsCall:
		return s.handleToolsCall(ctx, req)
	case MethodResourcesList:
		return s.handleResourcesList(req)
	case MethodResourcesRead:
		return s.handleResourcesRead(ctx, req)
	case MethodPromptsList:
		return s.handlePromptsList(req)
	case MethodPromptsGet:
		return s.handlePromptsGet(ctx, req)
	default:
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    ErrorCodeMethodNotFound,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			},
		}
	}
}

// handleInitialize 处理初始化请求
func (s *Server) handleInitialize(req *MCPRequest) *MCPResponse {
	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"protocolVersion": MCPVersion,
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{
					"listChanged": true,
				},
				"resources": map[string]interface{}{
					"subscribe":   false,
					"listChanged": true,
				},
				"prompts": map[string]interface{}{
					"listChanged": true,
				},
			},
			"serverInfo": map[string]interface{}{
				"name":    s.config.Name,
				"version": s.config.Version,
			},
		},
	}
}

// handleToolsList 处理工具列表请求
func (s *Server) handleToolsList(req *MCPRequest) *MCPResponse {
	s.toolsMu.RLock()
	defer s.toolsMu.RUnlock()

	tools := make([]Tool, 0, len(s.tools))
	for _, rt := range s.tools {
		tools = append(tools, rt.Tool)
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"tools": tools,
		},
	}
}

// handleToolsCall 处理工具调用请求
func (s *Server) handleToolsCall(ctx context.Context, req *MCPRequest) *MCPResponse {
	// 解析参数
	paramsBytes, _ := json.Marshal(req.Params)
	var callReq ToolCallRequest
	if err := json.Unmarshal(paramsBytes, &callReq); err != nil {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    ErrorCodeInvalidParams,
				Message: "Invalid params",
			},
		}
	}

	// 查找工具
	s.toolsMu.RLock()
	rt, ok := s.tools[callReq.Name]
	s.toolsMu.RUnlock()

	if !ok {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    ErrorCodeMethodNotFound,
				Message: fmt.Sprintf("Tool not found: %s", callReq.Name),
			},
		}
	}

	// 调用工具
	result, err := rt.Handler(ctx, callReq.Arguments)
	if err != nil {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: &ToolCallResponse{
				Content: []ContentBlock{
					{Type: "text", Text: err.Error()},
				},
				IsError: true,
			},
		}
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}
}

// handleResourcesList 处理资源列表请求
func (s *Server) handleResourcesList(req *MCPRequest) *MCPResponse {
	s.resourcesMu.RLock()
	defer s.resourcesMu.RUnlock()

	resources := make([]Resource, 0, len(s.resources))
	for _, rr := range s.resources {
		resources = append(resources, rr.Resource)
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"resources": resources,
		},
	}
}

// handleResourcesRead 处理资源读取请求
func (s *Server) handleResourcesRead(ctx context.Context, req *MCPRequest) *MCPResponse {
	paramsMap, ok := req.Params.(map[string]interface{})
	if !ok {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    ErrorCodeInvalidParams,
				Message: "Invalid params",
			},
		}
	}

	uri, ok := paramsMap["uri"].(string)
	if !ok {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    ErrorCodeInvalidParams,
				Message: "Missing uri parameter",
			},
		}
	}

	s.resourcesMu.RLock()
	rr, ok := s.resources[uri]
	s.resourcesMu.RUnlock()

	if !ok {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    ErrorCodeMethodNotFound,
				Message: fmt.Sprintf("Resource not found: %s", uri),
			},
		}
	}

	content, err := rr.Handler(ctx)
	if err != nil {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    ErrorCodeInternalError,
				Message: err.Error(),
			},
		}
	}

	// 守卫 handler 返回 (nil, nil) 的情况：直接解引用 *content 会触发
	// nil 指针 panic（拖垮服务端 goroutine）。对齐 err!=nil 分支优雅降级，
	// 返回结构化内部错误。
	if content == nil {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    ErrorCodeInternalError,
				Message: fmt.Sprintf("Resource handler returned nil content: %s", uri),
			},
		}
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"contents": []ResourceContent{*content},
		},
	}
}

// handlePromptsList 处理提示列表请求
func (s *Server) handlePromptsList(req *MCPRequest) *MCPResponse {
	s.promptsMu.RLock()
	defer s.promptsMu.RUnlock()

	prompts := make([]Prompt, 0, len(s.prompts))
	for _, rp := range s.prompts {
		prompts = append(prompts, rp.Prompt)
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"prompts": prompts,
		},
	}
}

// handlePromptsGet 处理获取提示请求
func (s *Server) handlePromptsGet(ctx context.Context, req *MCPRequest) *MCPResponse {
	paramsMap, ok := req.Params.(map[string]interface{})
	if !ok {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    ErrorCodeInvalidParams,
				Message: "Invalid params",
			},
		}
	}

	name, ok := paramsMap["name"].(string)
	if !ok {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    ErrorCodeInvalidParams,
				Message: "Missing name parameter",
			},
		}
	}

	s.promptsMu.RLock()
	rp, ok := s.prompts[name]
	s.promptsMu.RUnlock()

	if !ok {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    ErrorCodeMethodNotFound,
				Message: fmt.Sprintf("Prompt not found: %s", name),
			},
		}
	}

	// 解析参数
	args := make(map[string]string)
	if argsRaw, ok := paramsMap["arguments"].(map[string]interface{}); ok {
		for k, v := range argsRaw {
			if str, ok := v.(string); ok {
				args[k] = str
			}
		}
	}

	messages, err := rp.Handler(ctx, args)
	if err != nil {
		return &MCPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &MCPError{
				Code:    ErrorCodeInternalError,
				Message: err.Error(),
			},
		}
	}

	return &MCPResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"messages": messages,
		},
	}
}

// writeResponse 写入响应
func (s *Server) writeResponse(w http.ResponseWriter, resp *MCPResponse) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// writeError 写入错误响应
func (s *Server) writeError(w http.ResponseWriter, id interface{}, code int, message string) {
	s.writeResponse(w, &MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &MCPError{
			Code:    code,
			Message: message,
		},
	})
}
