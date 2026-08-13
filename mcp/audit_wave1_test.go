package mcp

// audit_wave1_test.go 是针对 mcp 包的全场景挑刺式审计测试。
//
// 覆盖范围：
//   - 正常路径：客户端/服务端 RPC 往返、Schema 转换、工具代理执行
//   - 边界情况：空/nil 输入、Unicode、超长、并发竞态、零值配置
//   - 错误路径：HTTP 错误、JSON 解析失败、工具未找到、handler 返回 nil
//   - 状态一致性：MCPToolSet 动态重建、请求 ID 单调递增
//   - 未闭环逻辑：image-only 内容块输出、Validate 把 nil 值当作"已提供"、
//     handleResourcesRead 对 nil content 的解引用
//
// 注意：所有上游依赖（HTTP MCP 服务器、stdio 进程）均用 httptest / 接口桩
// 模拟，不真打网络，仅验证本包的请求构造 / 响应解析 / 错误处理逻辑。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/tool"
)

// ============== 测试桩 ==============

// scriptedTransport 是一个可编程的 Transport 桩：根据 method 返回预设响应或错误。
// 用于隔离测试 TransportClient / MCPProxyTool 的本地解析逻辑。
type scriptedTransport struct {
	mu       sync.Mutex
	handler  func(req *MCPRequest) (*MCPResponse, error)
	sends    int
	closed   bool
	closeErr error
}

func (s *scriptedTransport) Send(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	s.mu.Lock()
	s.sends++
	s.mu.Unlock()
	return s.handler(req)
}

func (s *scriptedTransport) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return s.closeErr
}

// newToolServer 返回一个最小可用的 httptest MCP 服务器，可注入工具调用响应。
func newToolServer(t *testing.T, tools []Tool, callResp *ToolCallResponse) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp := MCPResponse{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case MethodInitialize:
			resp.Result = map[string]any{"capabilities": map[string]any{}}
		case MethodToolsList:
			resp.Result = map[string]any{"tools": tools}
		case MethodToolsCall:
			resp.Result = callResp
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// ============== Schema 转换：边界与往返 ==============

// TestSchemaToMCP_NestedAndEmptySlices 验证递归嵌套及空切片的处理。
func TestSchemaToMCP_NestedAndEmptySlices(t *testing.T) {
	tests := []struct {
		name  string
		input *llm.Schema
		check func(t *testing.T, js *JSONSchema)
	}{
		{
			name:  "nil 输入返回 nil",
			input: nil,
			check: func(t *testing.T, js *JSONSchema) {
				if js != nil {
					t.Errorf("期望 nil, 实际 %+v", js)
				}
			},
		},
		{
			name:  "空但非 nil 的 Enum 被丢弃",
			input: &llm.Schema{Type: "string", Enum: []any{}},
			check: func(t *testing.T, js *JSONSchema) {
				if js.Enum != nil {
					t.Errorf("空 Enum 应被丢弃, 实际 %v", js.Enum)
				}
			},
		},
		{
			name: "深层嵌套 object->array->object 递归转换",
			input: &llm.Schema{
				Type: "object",
				Properties: map[string]*llm.Schema{
					"list": {
						Type: "array",
						Items: &llm.Schema{
							Type: "object",
							Properties: map[string]*llm.Schema{
								"leaf": {Type: "string", Description: "叶子节点"},
							},
						},
					},
				},
			},
			check: func(t *testing.T, js *JSONSchema) {
				leaf := js.Properties["list"].Items.Properties["leaf"]
				if leaf == nil || leaf.Type != "string" || leaf.Description != "叶子节点" {
					t.Errorf("深层嵌套丢失: %+v", leaf)
				}
			},
		},
		{
			name:  "Unicode 描述保留",
			input: &llm.Schema{Type: "string", Description: "中文描述🚀émoji"},
			check: func(t *testing.T, js *JSONSchema) {
				if js.Description != "中文描述🚀émoji" {
					t.Errorf("Unicode 描述丢失: %q", js.Description)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, SchemaToMCP(tt.input))
		})
	}
}

// TestSchemaRoundTrip_Enum 验证 Enum 经 ai-core->MCP->ai-core 往返保留。
func TestSchemaRoundTrip_Enum(t *testing.T) {
	original := &llm.Schema{
		Type: "string",
		Enum: []any{"a", "b", "c"},
	}
	back := SchemaFromMCP(SchemaToMCP(original))
	if len(back.Enum) != 3 {
		t.Fatalf("Enum 往返丢失: %v", back.Enum)
	}
}

// ============== MCPProxyTool（V1）执行与边界 ==============

// TestMCPProxyTool_ImageOnlyContent 验证仅含 image 内容块时的输出。
// 这是一个未闭环逻辑探针：proxy_tool.go 仅在 Data!="" 时写入占位文本，
// 且占位文本不含图片数据，纯 image 响应会丢失实际内容。
func TestMCPProxyTool_ImageOnlyContent(t *testing.T) {
	st := &scriptedTransport{
		handler: func(req *MCPRequest) (*MCPResponse, error) {
			return &MCPResponse{Result: &ToolCallResponse{
				Content: []ContentBlock{
					{Type: "image", Data: "base64data", MimeType: "image/png"},
				},
			}}, nil
		},
	}
	client := NewTransportClient(st)
	proxy := NewMCPProxyTool(Tool{Name: "img"}, client)

	res, err := proxy.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Execute 不应返回 error: %v", err)
	}
	// 期望输出包含 mime 占位提示（验证 image 分支被走到）
	if !strings.Contains(fmt.Sprint(res.Output), "image/png") {
		t.Errorf("image 内容块占位丢失: %v", res.Output)
	}
}

// TestMCPProxyTool_ImageWithoutData 验证 image 块但 Data 为空时被静默丢弃。
func TestMCPProxyTool_ImageWithoutData(t *testing.T) {
	st := &scriptedTransport{
		handler: func(req *MCPRequest) (*MCPResponse, error) {
			return &MCPResponse{Result: &ToolCallResponse{
				Content: []ContentBlock{
					{Type: "image", Data: "", MimeType: "image/png"},
				},
			}}, nil
		},
	}
	proxy := NewMCPProxyTool(Tool{Name: "img"}, NewTransportClient(st))
	res, err := proxy.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Execute 不应返回 error: %v", err)
	}
	// Data 为空时输出应为空串（验证当前行为）
	if fmt.Sprint(res.Output) != "" {
		t.Errorf("Data 为空的 image 块应产生空输出, 实际 %q", res.Output)
	}
}

// TestMCPProxyTool_ErrorResultNoError 验证工具执行报错时通过 Result 表达而非返回 error。
func TestMCPProxyTool_ErrorResultNoError(t *testing.T) {
	st := &scriptedTransport{
		handler: func(req *MCPRequest) (*MCPResponse, error) {
			return &MCPResponse{Result: &ToolCallResponse{
				Content: []ContentBlock{{Type: "text", Text: "boom"}},
				IsError: true,
			}}, nil
		},
	}
	proxy := NewMCPProxyTool(Tool{Name: "x"}, NewTransportClient(st))
	res, err := proxy.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("工具执行失败应通过 Result 表达, 不应返回 error: %v", err)
	}
	if res.Success {
		t.Error("IsError 响应应映射为 Result.Success=false")
	}
	if !strings.Contains(res.Error, "boom") {
		t.Errorf("错误文本应保留: %q", res.Error)
	}
}

// TestMCPProxyTool_TransportError 验证传输层错误被包装为错误 Result（非 error 返回）。
func TestMCPProxyTool_TransportError(t *testing.T) {
	st := &scriptedTransport{
		handler: func(req *MCPRequest) (*MCPResponse, error) {
			return nil, fmt.Errorf("network down")
		},
	}
	proxy := NewMCPProxyTool(Tool{Name: "x"}, NewTransportClient(st))
	res, err := proxy.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("传输错误应通过 Result 表达: %v", err)
	}
	if res.Success {
		t.Error("传输错误时 Result.Success 应为 false")
	}
}

// TestMCPProxyTool_ValidateNilValue 探针：Validate 仅检查 key 是否存在，
// 不检查值是否为 nil。args[field]=nil 会被当作"已提供"而通过校验。
func TestMCPProxyTool_ValidateNilValue(t *testing.T) {
	proxy := &MCPProxyTool{
		cachedSchema: &llm.Schema{Type: "object", Required: []string{"x"}},
	}
	// 提供 nil 值——语义上等于"未提供"，但当前实现会放行
	err := proxy.Validate(map[string]any{"x": nil})
	if err != nil {
		t.Logf("当前行为：nil 值通过校验（key 存在即可），err=%v", err)
	}
	// 完全缺失 key 必须报错
	if err := proxy.Validate(map[string]any{}); err == nil {
		t.Error("缺失必填字段必须报错")
	}
}

// TestMCPProxyTool_NilSchemaValidate 验证 nil schema 时 Validate 放行任意参数。
func TestMCPProxyTool_NilSchemaValidate(t *testing.T) {
	proxy := &MCPProxyTool{cachedSchema: nil}
	if err := proxy.Validate(map[string]any{"any": 1}); err != nil {
		t.Errorf("nil schema 应放行所有参数: %v", err)
	}
}

// TestMCPProxyTool_MultiTextConcat 验证多个 text 块被顺序拼接。
func TestMCPProxyTool_MultiTextConcat(t *testing.T) {
	st := &scriptedTransport{
		handler: func(req *MCPRequest) (*MCPResponse, error) {
			return &MCPResponse{Result: &ToolCallResponse{
				Content: []ContentBlock{
					{Type: "text", Text: "Hello"},
					{Type: "text", Text: ", "},
					{Type: "text", Text: "世界"},
				},
			}}, nil
		},
	}
	proxy := NewMCPProxyTool(Tool{Name: "x"}, NewTransportClient(st))
	res, _ := proxy.Execute(context.Background(), map[string]any{})
	if fmt.Sprint(res.Output) != "Hello, 世界" {
		t.Errorf("多 text 块拼接错误: %q", res.Output)
	}
}

// ============== MCPToolSet 状态一致性与并发 ==============

// TestMCPToolSet_ConcurrentRebuildAndRead 并发竞态：Tools/Get 与 rebuild 并发执行
// 不应触发 data race 或 panic（需配合 -race）。
func TestMCPToolSet_ConcurrentRebuildAndRead(t *testing.T) {
	ts := NewMCPToolSet(nil, []Tool{{Name: "a"}})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 读协程
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = ts.Tools()
					_, _ = ts.Get("a")
				}
			}
		}()
	}
	// 写协程
	for i := range 4 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 50 {
				ts.rebuild([]Tool{{Name: fmt.Sprintf("t%d-%d", id, j)}})
			}
		}(i)
	}

	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()
	// 最终状态应恰好 1 个工具（每次 rebuild 替换为单元素切片）
	if len(ts.Tools()) != 1 {
		t.Errorf("最终工具数应为 1, 实际 %d", len(ts.Tools()))
	}
}

// TestMCPToolSet_EmptyAndNilTools 验证空/nil 工具列表的构造。
func TestMCPToolSet_EmptyAndNilTools(t *testing.T) {
	tests := []struct {
		name  string
		tools []Tool
		want  int
	}{
		{"nil 列表", nil, 0},
		{"空列表", []Tool{}, 0},
		{"单工具", []Tool{{Name: "a"}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := NewMCPToolSet(nil, tt.tools)
			if len(ts.Tools()) != tt.want {
				t.Errorf("工具数: 期望 %d, 实际 %d", tt.want, len(ts.Tools()))
			}
			if _, ok := ts.Get("nonexistent"); ok {
				t.Error("不应找到不存在的工具")
			}
		})
	}
}

// TestMCPToolSet_RefreshTriggersCallback 验证 Refresh 成功后触发 OnToolsChanged。
func TestMCPToolSet_RefreshTriggersCallback(t *testing.T) {
	st := &scriptedTransport{
		handler: func(req *MCPRequest) (*MCPResponse, error) {
			if req.Method == MethodToolsList {
				return &MCPResponse{Result: map[string]any{
					"tools": []map[string]any{{"name": "fresh"}},
				}}, nil
			}
			return &MCPResponse{Result: map[string]any{}}, nil
		},
	}
	ts := NewMCPToolSet(NewTransportClient(st), nil)

	var called int32
	var mu sync.Mutex
	var got []tool.Tool
	ts.SetOnToolsChanged(func(tools []tool.Tool) {
		mu.Lock()
		called++
		got = tools
		mu.Unlock()
	})

	if err := ts.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh 失败: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if called != 1 {
		t.Errorf("回调应被调用 1 次, 实际 %d", called)
	}
	if len(got) != 1 || got[0].Name() != "fresh" {
		t.Errorf("回调收到的工具不正确: %+v", got)
	}
}

// TestMCPToolSet_Close 验证 Close 传播到底层传输层。
func TestMCPToolSet_Close(t *testing.T) {
	st := &scriptedTransport{handler: func(req *MCPRequest) (*MCPResponse, error) {
		return &MCPResponse{Result: map[string]any{}}, nil
	}}
	ts := NewMCPToolSet(NewTransportClient(st), nil)
	if err := ts.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.closed {
		t.Error("Close 应关闭底层传输层")
	}
}

// ============== TransportClient 解析路径 ==============

// TestTransportClient_ListToolsMissingKey 验证响应缺少 tools 键时返回 (nil, nil)。
func TestTransportClient_ListToolsMissingKey(t *testing.T) {
	st := &scriptedTransport{handler: func(req *MCPRequest) (*MCPResponse, error) {
		return &MCPResponse{Result: map[string]any{}}, nil // 无 tools 键
	}}
	tools, err := NewTransportClient(st).ListTools(context.Background())
	if err != nil {
		t.Fatalf("缺少 tools 键不应报错: %v", err)
	}
	if tools != nil {
		t.Errorf("缺少 tools 键应返回 nil, 实际 %v", tools)
	}
}

// TestTransportClient_ListToolsInvalidResult 验证 Result 非 map 时报错。
func TestTransportClient_ListToolsInvalidResult(t *testing.T) {
	st := &scriptedTransport{handler: func(req *MCPRequest) (*MCPResponse, error) {
		return &MCPResponse{Result: "i am a string not a map"}, nil
	}}
	_, err := NewTransportClient(st).ListTools(context.Background())
	if err == nil {
		t.Error("Result 非 map 应报错")
	}
}

// TestTransportClient_ReadResourceEmptyContents 验证 contents 为空数组时报错。
func TestTransportClient_ReadResourceEmptyContents(t *testing.T) {
	st := &scriptedTransport{handler: func(req *MCPRequest) (*MCPResponse, error) {
		return &MCPResponse{Result: map[string]any{"contents": []any{}}}, nil
	}}
	_, err := NewTransportClient(st).ReadResource(context.Background(), "file:///x")
	if err == nil {
		t.Error("空 contents 应报错")
	}
}

// TestTransportClient_InitializeMalformedCaps 验证 capabilities 字段类型异常时
// 不 panic、不报错（serverCapabilities 保持 nil 或可解析）。
func TestTransportClient_InitializeMalformedCaps(t *testing.T) {
	st := &scriptedTransport{handler: func(req *MCPRequest) (*MCPResponse, error) {
		// capabilities 是字符串而非对象——json.Unmarshal 进 struct 会失败但被吞掉
		return &MCPResponse{Result: map[string]any{"capabilities": "not-an-object"}}, nil
	}}
	c := NewTransportClient(st)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("畸形 capabilities 不应导致 Initialize 报错: %v", err)
	}
	// 解析失败时 serverCapabilities 应保持 nil
	if c.GetServerCapabilities() != nil {
		t.Errorf("畸形 capabilities 解析失败后应为 nil, 实际 %+v", c.GetServerCapabilities())
	}
}

// ============== Server（V1）请求处理与边界 ==============

// TestServer_HandleResourcesRead_NilContent 探针：当 ResourceHandler 返回 (nil, nil)
// 时，handleResourcesRead 会执行 *content 解引用。验证是否 panic / 如何回应。
func TestServer_HandleResourcesRead_NilContent(t *testing.T) {
	s := NewServer(nil)
	// 注册一个返回 (nil, nil) 的资源 handler——模拟实现疏忽
	s.RegisterResource(Resource{URI: "file:///bad"}, func(ctx context.Context) (*ResourceContent, error) {
		return nil, nil
	})

	srv := httptest.NewServer(http.HandlerFunc(s.handleRequest))
	defer srv.Close()

	transport := NewHTTPTransport(srv.URL)
	c := NewTransportClient(transport)
	// 该调用若服务端 panic，net/http 会中断连接，客户端收到读响应错误。
	_, err := c.ReadResource(context.Background(), "file:///bad")
	// 期望：要么有结构化错误返回，要么连接错误；无论如何客户端不应静默拿到非法内容。
	if err == nil {
		t.Error("handler 返回 nil content 时客户端应感知错误（结构化错误或连接中断）")
	} else {
		t.Logf("nil content handler 导致客户端错误: %v", err)
	}
}

// TestServer_HandleToolsCall_HandlerError 验证 handler 返回 error 时
// 被映射为 Result.IsError=true（而非 JSON-RPC Error）。
func TestServer_HandleToolsCall_HandlerError(t *testing.T) {
	s := NewServer(nil)
	s.RegisterTool(Tool{Name: "fail"}, func(ctx context.Context, args map[string]any) (*ToolCallResponse, error) {
		return nil, fmt.Errorf("handler 内部错误")
	})
	srv := httptest.NewServer(http.HandlerFunc(s.handleRequest))
	defer srv.Close()

	c := NewTransportClient(NewHTTPTransport(srv.URL))
	resp, err := c.CallTool(context.Background(), "fail", map[string]any{})
	if err != nil {
		t.Fatalf("handler error 应映射为 IsError, 不应是传输错误: %v", err)
	}
	if !resp.IsError {
		t.Error("handler 返回 error 应映射为 IsError=true")
	}
	if len(resp.Content) == 0 || !strings.Contains(resp.Content[0].Text, "handler 内部错误") {
		t.Errorf("错误文本应透传: %+v", resp.Content)
	}
}

// TestServer_ToolNotFound 验证调用未注册工具返回 MethodNotFound 错误。
func TestServer_ToolNotFound(t *testing.T) {
	s := NewServer(nil)
	srv := httptest.NewServer(http.HandlerFunc(s.handleRequest))
	defer srv.Close()

	c := NewTransportClient(NewHTTPTransport(srv.URL))
	_, err := c.CallTool(context.Background(), "ghost", map[string]any{})
	if err == nil {
		t.Error("调用不存在的工具应报错")
	}
}

// TestServer_GET_MethodNotAllowed 验证非 POST 请求被拒绝。
func TestServer_GET_MethodNotAllowed(t *testing.T) {
	s := NewServer(nil)
	srv := httptest.NewServer(http.HandlerFunc(s.handleRequest))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET 请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("非 POST 应返回 405, 实际 %d", resp.StatusCode)
	}
}

// TestServer_InvalidJSON 验证非法 JSON body 返回 ParseError。
func TestServer_InvalidJSON(t *testing.T) {
	s := NewServer(nil)
	srv := httptest.NewServer(http.HandlerFunc(s.handleRequest))
	defer srv.Close()

	resp, err := http.Post(srv.URL, "application/json", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("POST 失败: %v", err)
	}
	defer resp.Body.Close()
	var mcpResp MCPResponse
	_ = json.NewDecoder(resp.Body).Decode(&mcpResp)
	if mcpResp.Error == nil || mcpResp.Error.Code != ErrorCodeParseError {
		t.Errorf("非法 JSON 应返回 ParseError(%d), 实际 %+v", ErrorCodeParseError, mcpResp.Error)
	}
}

// TestServer_RegisterTool_Overwrite 验证同名工具注册会覆盖（map 行为）。
func TestServer_RegisterTool_Overwrite(t *testing.T) {
	s := NewServer(nil)
	s.RegisterTool(Tool{Name: "dup", Description: "v1"}, func(ctx context.Context, a map[string]any) (*ToolCallResponse, error) {
		return &ToolCallResponse{Content: []ContentBlock{{Type: "text", Text: "v1"}}}, nil
	})
	s.RegisterTool(Tool{Name: "dup", Description: "v2"}, func(ctx context.Context, a map[string]any) (*ToolCallResponse, error) {
		return &ToolCallResponse{Content: []ContentBlock{{Type: "text", Text: "v2"}}}, nil
	})

	srv := httptest.NewServer(http.HandlerFunc(s.handleRequest))
	defer srv.Close()
	c := NewTransportClient(NewHTTPTransport(srv.URL))

	tools, _ := c.ListTools(context.Background())
	if len(tools) != 1 {
		t.Fatalf("同名工具应去重为 1, 实际 %d", len(tools))
	}
	resp, _ := c.CallTool(context.Background(), "dup", map[string]any{})
	if resp.Content[0].Text != "v2" {
		t.Errorf("后注册应覆盖, 期望 v2, 实际 %s", resp.Content[0].Text)
	}
}

// TestServer_ConcurrentRegisterAndList 并发注册与列举工具不应触发 race（配合 -race）。
func TestServer_ConcurrentRegisterAndList(t *testing.T) {
	s := NewServer(nil)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			s.RegisterTool(Tool{Name: fmt.Sprintf("t%d", id)}, func(ctx context.Context, a map[string]any) (*ToolCallResponse, error) {
				return &ToolCallResponse{}, nil
			})
		}(i)
	}
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.handleToolsList(&MCPRequest{ID: 1})
		}()
	}
	wg.Wait()
	resp := s.handleToolsList(&MCPRequest{ID: 1})
	result := resp.Result.(map[string]any)
	tools := result["tools"].([]Tool)
	if len(tools) != 20 {
		t.Errorf("应注册 20 个工具, 实际 %d", len(tools))
	}
}

// ============== HTTPTransport 请求构造与 ID ==============

// TestHTTPTransport_ConcurrentSendIDsUnique 并发发送时自动分配的 ID 应唯一（atomic 保证）。
func TestHTTPTransport_ConcurrentSendIDsUnique(t *testing.T) {
	var mu sync.Mutex
	seen := map[int64]bool{}
	dup := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if id, ok := req.ID.(float64); ok {
			mu.Lock()
			if seen[int64(id)] {
				dup = true
			}
			seen[int64(id)] = true
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	}))
	defer srv.Close()

	transport := NewHTTPTransport(srv.URL)
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = transport.Send(context.Background(), &MCPRequest{JSONRPC: "2.0", Method: "test"})
		}()
	}
	wg.Wait()
	if dup {
		t.Error("并发自动分配的请求 ID 出现重复")
	}
	if len(seen) != 50 {
		t.Errorf("应有 50 个唯一 ID, 实际 %d", len(seen))
	}
}

// TestHTTPTransport_PreservesExplicitID 验证已显式设置的 ID 不被覆盖。
func TestHTTPTransport_PreservesExplicitID(t *testing.T) {
	var gotID any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotID = req.ID
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	}))
	defer srv.Close()

	transport := NewHTTPTransport(srv.URL)
	_, _ = transport.Send(context.Background(), &MCPRequest{JSONRPC: "2.0", ID: "custom-id", Method: "test"})
	if gotID != "custom-id" {
		t.Errorf("显式 ID 应被保留, 实际 %v", gotID)
	}
}

// TestHTTPTransport_BadJSONResponse 验证服务器返回非法 JSON 时报错。
func TestHTTPTransport_BadJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<<<not json>>>"))
	}))
	defer srv.Close()
	_, err := NewHTTPTransport(srv.URL).Send(context.Background(), &MCPRequest{Method: "x"})
	if err == nil {
		t.Error("非法 JSON 响应应报错")
	}
}

// ============== 兼容性适配器（adapter.go）==============

// TestConvertParamsToSchema 表驱动验证参数 map 到 JSONSchema 的转换。
func TestConvertParamsToSchema(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		check  func(t *testing.T, js *JSONSchema)
	}{
		{
			name:   "nil 参数返回空 object",
			params: nil,
			check: func(t *testing.T, js *JSONSchema) {
				if js.Type != "object" || js.Properties != nil {
					t.Errorf("nil 应返回 {object, nil props}, 实际 %+v", js)
				}
			},
		},
		{
			name:   "空 map 返回 object + 空 props",
			params: map[string]any{},
			check: func(t *testing.T, js *JSONSchema) {
				if js.Type != "object" || js.Properties == nil || len(js.Properties) != 0 {
					t.Errorf("空 map 应返回空 props, 实际 %+v", js)
				}
			},
		},
		{
			name: "嵌套 map 描述符",
			params: map[string]any{
				"q": map[string]any{"type": "string", "description": "查询"},
			},
			check: func(t *testing.T, js *JSONSchema) {
				if js.Properties["q"].Type != "string" || js.Properties["q"].Description != "查询" {
					t.Errorf("嵌套 map 解析错误: %+v", js.Properties["q"])
				}
			},
		},
		{
			name:   "字符串简写",
			params: map[string]any{"n": "integer"},
			check: func(t *testing.T, js *JSONSchema) {
				if js.Properties["n"].Type != "integer" {
					t.Errorf("字符串简写解析错误: %+v", js.Properties["n"])
				}
			},
		},
		{
			name:   "未知类型回退 string",
			params: map[string]any{"x": 12345},
			check: func(t *testing.T, js *JSONSchema) {
				if js.Properties["x"].Type != "string" {
					t.Errorf("未知类型应回退 string, 实际 %+v", js.Properties["x"])
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, convertParamsToSchema(tt.params))
		})
	}
}

// TestMCPToolAdapter_CallToolMergesText 验证旧适配器合并文本内容。
func TestMCPToolAdapter_CallToolMergesText(t *testing.T) {
	srv := newToolServer(t, []Tool{{Name: "t"}}, &ToolCallResponse{
		Content: []ContentBlock{
			{Type: "text", Text: "AB"},
			{Type: "image", Data: "x"}, // 非 text，应被忽略
			{Type: "text", Text: "CD"},
		},
	})
	defer srv.Close()

	adapter, err := LoadMCPTools(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("LoadMCPTools 失败: %v", err)
	}
	out, err := adapter.CallTool(context.Background(), "t", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool 失败: %v", err)
	}
	if out != "ABCD" {
		t.Errorf("仅合并 text 块, 期望 ABCD, 实际 %q", out)
	}
}

// TestMCPToolAdapter_CallToolError 验证 IsError 响应被转为 Go error。
func TestMCPToolAdapter_CallToolError(t *testing.T) {
	srv := newToolServer(t, []Tool{{Name: "t"}}, &ToolCallResponse{
		Content: []ContentBlock{{Type: "text", Text: "失败原因"}},
		IsError: true,
	})
	defer srv.Close()

	adapter, _ := LoadMCPTools(context.Background(), srv.URL)
	_, err := adapter.CallTool(context.Background(), "t", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "失败原因") {
		t.Errorf("IsError 响应应转为 error 并保留文本, 实际 %v", err)
	}
}

// TestHexagonToolAdapter_RegisterAndCall 验证 ToolDefinition 注册并经服务端调用。
func TestHexagonToolAdapter_RegisterAndCall(t *testing.T) {
	s := NewServer(nil)
	adapter := NewHexagonToolAdapter(s)
	adapter.RegisterToolDefinition(ToolDefinition{
		Name:        "echo",
		Description: "回声",
		Parameters:  map[string]any{"msg": "string"},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return fmt.Sprint(args["msg"]), nil
		},
	})

	srv := httptest.NewServer(http.HandlerFunc(s.handleRequest))
	defer srv.Close()
	c := NewTransportClient(NewHTTPTransport(srv.URL))

	resp, err := c.CallTool(context.Background(), "echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("CallTool 失败: %v", err)
	}
	if resp.Content[0].Text != "hi" {
		t.Errorf("回声错误, 期望 hi, 实际 %s", resp.Content[0].Text)
	}
}

// TestHexagonToolAdapter_HandlerError 验证 ToolDefinition handler 报错映射为 IsError。
func TestHexagonToolAdapter_HandlerError(t *testing.T) {
	s := NewServer(nil)
	NewHexagonToolAdapter(s).RegisterToolDefinition(ToolDefinition{
		Name: "boom",
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "", fmt.Errorf("炸了")
		},
	})
	srv := httptest.NewServer(http.HandlerFunc(s.handleRequest))
	defer srv.Close()
	c := NewTransportClient(NewHTTPTransport(srv.URL))

	resp, err := c.CallTool(context.Background(), "boom", map[string]any{})
	if err != nil {
		t.Fatalf("不应是传输错误: %v", err)
	}
	if !resp.IsError || !strings.Contains(resp.Content[0].Text, "炸了") {
		t.Errorf("handler error 应映射为 IsError, 实际 %+v", resp)
	}
}

// ============== 旧版 Client（mcp.go）==============

// TestClient_CallTool 验证旧版 Client.CallTool 的请求构造与响应解析。
func TestClient_CallTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp := MCPResponse{JSONRPC: "2.0", ID: req.ID}
		if req.Method == MethodToolsCall {
			resp.Result = &ToolCallResponse{Content: []ContentBlock{{Type: "text", Text: "ok"}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	resp, err := c.CallTool(context.Background(), "x", map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("CallTool 失败: %v", err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "ok" {
		t.Errorf("响应解析错误: %+v", resp)
	}
}

// TestClient_ServerError 验证 JSON-RPC Error 被转为 Go error。
func TestClient_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0", ID: req.ID,
			Error: &MCPError{Code: ErrorCodeInvalidParams, Message: "bad params"},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	_, err := c.ListTools(context.Background())
	if err == nil || !strings.Contains(err.Error(), "bad params") {
		t.Errorf("JSON-RPC Error 应转为 error 并保留消息, 实际 %v", err)
	}
}

// TestClient_MonotonicIDs 验证旧版 Client 串行调用 ID 单调递增。
func TestClient_MonotonicIDs(t *testing.T) {
	var ids []int64
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if id, ok := req.ID.(float64); ok {
			mu.Lock()
			ids = append(ids, int64(id))
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	for range 3 {
		_, _ = c.ListTools(context.Background())
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Errorf("ID 应单调递增: %v", ids)
		}
	}
}

// ============== official.go V2 双返回值一致性探针 ==============

// TestMCPProxyToolV2_ErrorReturnsBothResultAndError 探针：V2 的 Execute 在
// IsError 时同时返回非空 Result 与 error，而 Result.Success 为零值 false。
// 验证调用方若忽略 error 仅看 Result 会得到一个 Success=false 的结果。
func TestMCPProxyToolV2_ErrorReturnsBothResultAndError(t *testing.T) {
	// 通过 in-memory server 触发 IsError 工具，走完整 V2 链路在 official_test.go 已覆盖；
	// 这里直接断言 ai-core 的 Result 零值语义，确认契约。
	r := tool.Result{Output: "err-output"} // V2 IsError 分支构造的形态
	if r.Success {
		t.Error("零值 Result 的 Success 应为 false")
	}
	// String() 在 Success=false 时返回 "Error: " 前缀且不含 Output
	if !strings.HasPrefix(r.String(), "Error:") {
		t.Errorf("Success=false 的 Result.String 应以 Error: 开头, 实际 %q", r.String())
	}
	if strings.Contains(r.String(), "err-output") {
		t.Errorf("Success=false 时 Output 在 String() 中丢失（已知 ai-core 契约）: %q", r.String())
	}
}
