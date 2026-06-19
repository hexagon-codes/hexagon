package mcp

// audit_wave2_test.go 是 mcp 包第二轮挑刺式审计的回归测试。
//
// 与 audit_wave1 的探针式（仅记录现状、不钉死缺陷）不同，本文件中的断言
// 针对真实缺陷收敛：测试在修复前必须 FAIL，在修复后 PASS，并作为永久回归
// 锁定下列三项不变量：
//
//   - B6: handleResourcesRead 对 nil content 解引用导致服务端 goroutine panic
//   - B7: V2 代理工具 Execute 错误契约与 V1 相反，且错误文本在 Result.String() 中丢失
//   - B8: 旧版 Client.Initialize 吞掉 json.Unmarshal 错误，畸形 capabilities 仍被写入
//
// 注意：所有上游依赖均用 httptest / in-memory transport 模拟，不真打网络。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/tool"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ============== B6: handleResourcesRead nil content 优雅降级 ==============

// TestServer_HandleResourcesRead_NilContentStructuredError 回归: B6
//
// 当 ResourceHandler 返回 (nil, nil) 时，handleResourcesRead 不应对 nil content
// 解引用（*content）导致服务端 goroutine panic。修复后应返回结构化
// ErrorCodeInternalError，与 err!=nil 分支一样优雅降级。
//
// 直接调用 handleResourcesRead，避免 net/http recover 把 panic 转成连接错误而
// 掩盖根因——本测试钉死"不 panic 且返回结构化内部错误"。
func TestServer_HandleResourcesRead_NilContentStructuredError(t *testing.T) {
	s := NewServer(nil)
	// 注册一个返回 (nil, nil) 的资源 handler——模拟实现疏忽
	s.RegisterResource(Resource{URI: "file:///bad"}, func(ctx context.Context) (*ResourceContent, error) {
		return nil, nil
	})

	req := &MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  MethodResourcesRead,
		Params:  map[string]interface{}{"uri": "file:///bad"},
	}

	// 修复前：此处会 panic（nil 指针解引用 *content），导致测试崩溃。
	resp := s.handleResourcesRead(context.Background(), req)

	if resp == nil {
		t.Fatal("响应不应为 nil")
	}
	// 修复后：nil content 应映射为结构化内部错误，而非 panic 或返回非法内容。
	if resp.Error == nil {
		t.Fatalf("nil content 应返回结构化错误, 实际 Result=%+v", resp.Result)
	}
	if resp.Error.Code != ErrorCodeInternalError {
		t.Errorf("nil content 应返回 ErrorCodeInternalError(%d), 实际 %d", ErrorCodeInternalError, resp.Error.Code)
	}
}

// ============== B7: V2 错误契约对齐 V1 且错误文本不丢失 ==============

// TestMCPProxyToolV2_ErrorPreservesText 回归: B7
//
// V2 代理工具在远程工具返回 IsError 时，必须用 tool.NewErrorResult 构造结果
// （Success=false 且 Error 字段写入错误文本），确保错误文本不被 Result.String()
// 吞掉；与 V1 (proxy_tool.go) 的契约对齐。
//
// 修复前：official.go 返回 tool.Result{Output: output}，Success 为零值 false，
// 错误文本落在 Output；而 ai-core 的 Result.String() 在 Success=false 时只输出
// "Error: " + Error 字段，导致 Output 中的错误文本彻底丢失。
func TestMCPProxyToolV2_ErrorPreservesText(t *testing.T) {
	ctx := context.Background()

	hexServer := NewMCPServerV2("test", "0.1.0")
	hexServer.RegisterTool(&mockTool{
		name:   "error-tool",
		schema: nil,
		executeFn: func(ctx context.Context, args map[string]any) (tool.Result, error) {
			return tool.Result{}, fmt.Errorf("上游炸了-unique-marker")
		},
	})

	t1, t2 := sdkmcp.NewInMemoryTransports()
	if _, err := hexServer.Server().Connect(ctx, t1, nil); err != nil {
		t.Fatalf("Server 连接失败: %v", err)
	}

	tools, closer, err := ConnectMCPServerV2(ctx, t2)
	if err != nil {
		t.Fatalf("ConnectMCPServerV2 失败: %v", err)
	}
	defer closer.Close()

	res, _ := tools[0].Execute(ctx, map[string]any{})

	// 契约对齐 V1：IsError 应映射为 Success=false。
	if res.Success {
		t.Error("IsError 响应应映射为 Result.Success=false")
	}
	// 错误文本必须保留在 Error 字段（而非 Output），否则 String() 会吞掉它。
	if !strings.Contains(res.Error, "上游炸了-unique-marker") {
		t.Errorf("错误文本应保留在 Result.Error, 实际 Error=%q Output=%v", res.Error, res.Output)
	}
	// 关键不变量：错误文本必须出现在 String() 输出中，不能被吞掉。
	if !strings.Contains(res.String(), "上游炸了-unique-marker") {
		t.Errorf("错误文本不应被 Result.String() 吞掉, 实际 %q", res.String())
	}
}

// ============== B8: Client.Initialize 不得吞掉 Unmarshal 错误 ==============

// TestClient_InitializeMalformedCaps 回归: B8
//
// 旧版 Client.Initialize 解析服务器 capabilities 时若 json.Unmarshal 失败，
// 必须像 transport.go 那样以 err==nil 守卫，避免把"半解析/零值"的 caps 写入
// serverCapabilities。修复前 mcp.go 忽略 Unmarshal 错误，畸形 capabilities
// （字符串而非对象）会让 serverCapabilities 被赋一个非 nil 的零值指针。
func TestClient_InitializeMalformedCaps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		// capabilities 是字符串而非对象——json.Unmarshal 进 struct 会失败
		resp := MCPResponse{
			JSONRPC: "2.0", ID: req.ID,
			Result: map[string]any{"capabilities": "not-an-object"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("畸形 capabilities 不应导致 Initialize 报错: %v", err)
	}
	// 修复后：Unmarshal 失败时 serverCapabilities 应保持 nil。
	if c.GetServerCapabilities() != nil {
		t.Errorf("畸形 capabilities 解析失败后应为 nil, 实际 %+v", c.GetServerCapabilities())
	}
}
