package mcp

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/hexagon-codes/ai-core/schema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestServerV2_HTTPTransport_E2E 端到端验证内置 MCP 服务器的 Streamable HTTP 传输：
// 把 HTTPHandler 挂到 httptest 服务，用官方 SDK 的 streamable client 连接、握手、
// 列出工具，确认注册的工具可经 HTTP 传输被发现。
func TestServerV2_HTTPTransport_E2E(t *testing.T) {
	srv := NewMCPServerV2("test", "0.1.0")
	srv.RegisterTool(&mockTool{
		name:        "ping",
		description: "ping tool",
		schema:      &schema.Schema{Type: "object"},
	})

	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()

	ctx := context.Background()
	transport := &sdkmcp.StreamableClientTransport{Endpoint: ts.URL}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "c", Version: "1"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("经 HTTP 传输连接失败: %v", err)
	}
	defer session.Close()

	res, err := session.ListTools(ctx, &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools 失败: %v", err)
	}
	found := false
	for _, tl := range res.Tools {
		if tl.Name == "ping" {
			found = true
		}
	}
	if !found {
		t.Error("HTTP 传输应能发现注册的工具 ping")
	}
}

// TestServerV2_TransportHandlers 验证 HTTP / SSE 两种传输的 handler 均可构造。
func TestServerV2_TransportHandlers(t *testing.T) {
	srv := NewMCPServerV2("test", "0.1.0")
	if srv.HTTPHandler() == nil {
		t.Error("HTTPHandler 不应为 nil")
	}
	if srv.SSEHandler() == nil {
		t.Error("SSEHandler 不应为 nil")
	}
}
