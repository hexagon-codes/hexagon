package mcp

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/schema"
	"github.com/hexagon-codes/ai-core/tool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func interopMCPResult() *sdk.CallToolResult {
	return &sdk.CallToolResult{IsError: true, Meta: sdk.Meta{"trace": "local-fixture"}, StructuredContent: map[string]any{"status": "partial"}, Content: []sdk.Content{
		&sdk.TextContent{Text: "partial output"},
		&sdk.ImageContent{MIMEType: "image/png", Data: []byte{1, 2, 3}},
		&sdk.AudioContent{MIMEType: "audio/wav", Data: []byte{4, 5, 6}},
		&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "fixture://report", MIMEType: "application/octet-stream", Blob: []byte{7, 8, 9}}},
		&sdk.ResourceLink{URI: "fixture://report", Name: "report", MIMEType: "application/octet-stream"},
	}}
}

func interopMCPResources(server *sdk.Server) {
	server.AddResource(&sdk.Resource{URI: "fixture://report", Name: "report", MIMEType: "application/octet-stream"}, func(ctx context.Context, req *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{URI: req.Params.URI, MIMEType: "application/octet-stream", Blob: []byte{7, 8, 9}}}}, nil
	})
	server.AddPrompt(&sdk.Prompt{Name: "inspect", Description: "Inspect the fixture"}, func(ctx context.Context, req *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		return &sdk.GetPromptResult{Messages: []*sdk.PromptMessage{{Role: "user", Content: &sdk.ImageContent{MIMEType: "image/png", Data: []byte{1, 2, 3}}}}}, nil
	})
}

func interopOfficialMCP() *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "independent-official", Version: "v1.5.0"}, nil)
	server.AddTool(&sdk.Tool{Name: "mixed", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return interopMCPResult(), nil
	})
	interopMCPResources(server)
	return server
}

func interopHexagonMCP() *ServerV2 {
	server := NewMCPServerV2("hexagon-interop", "1")
	server.RegisterTool(&mockTool{name: "mixed", schema: &schema.Schema{Type: "object"}, executeFn: func(ctx context.Context, args map[string]any) (tool.Result, error) {
		result := tool.NewErrorResult(errors.New("partial output"))
		result.Output = &PreservedToolResult{CallToolResult: interopMCPResult()}
		return result, errors.New("partial output")
	}})
	interopMCPResources(server.Server())
	return server
}

func assertMCPMixed(t *testing.T, result *sdk.CallToolResult) {
	t.Helper()
	if result == nil || !result.IsError || len(result.Content) != 5 || result.Meta["trace"] != "local-fixture" || result.StructuredContent == nil {
		t.Fatalf("MCP result metadata or failure content lost: %+v", result)
	}
	image, ok := result.Content[1].(*sdk.ImageContent)
	if !ok || !bytes.Equal(image.Data, []byte{1, 2, 3}) {
		t.Fatalf("image content lost: %+v", result.Content[1])
	}
	audio, ok := result.Content[2].(*sdk.AudioContent)
	if !ok || !bytes.Equal(audio.Data, []byte{4, 5, 6}) {
		t.Fatalf("audio content lost: %+v", result.Content[2])
	}
	embedded, ok := result.Content[3].(*sdk.EmbeddedResource)
	if !ok || !bytes.Equal(embedded.Resource.Blob, []byte{7, 8, 9}) {
		t.Fatalf("embedded resource lost: %+v", result.Content[3])
	}
	link, ok := result.Content[4].(*sdk.ResourceLink)
	if !ok || link.URI != "fixture://report" {
		t.Fatalf("resource link lost: %+v", result.Content[4])
	}
}

func assertOfficialMCPClient(t *testing.T, ctx context.Context, session *sdk.ClientSession) {
	t.Helper()
	if session.InitializeResult().ProtocolVersion != "2025-11-25" {
		t.Fatalf("negotiated protocol: %s", session.InitializeResult().ProtocolVersion)
	}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "mixed", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	assertMCPMixed(t, result)
	resources, err := session.ListResources(ctx, &sdk.ListResourcesParams{})
	if err != nil || len(resources.Resources) != 1 {
		t.Fatalf("resources: %+v %v", resources, err)
	}
	resource, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: "fixture://report"})
	if err != nil || len(resource.Contents) != 1 || !bytes.Equal(resource.Contents[0].Blob, []byte{7, 8, 9}) {
		t.Fatalf("resource bytes: %+v %v", resource, err)
	}
	prompts, err := session.ListPrompts(ctx, &sdk.ListPromptsParams{})
	if err != nil || len(prompts.Prompts) != 1 {
		t.Fatalf("prompts: %+v %v", prompts, err)
	}
	prompt, err := session.GetPrompt(ctx, &sdk.GetPromptParams{Name: "inspect"})
	if err != nil || len(prompt.Messages) != 1 || prompt.Messages[0].Role != "user" {
		t.Fatalf("prompt: %+v %v", prompt, err)
	}
	if image, ok := prompt.Messages[0].Content.(*sdk.ImageContent); !ok || !bytes.Equal(image.Data, []byte{1, 2, 3}) {
		t.Fatal("prompt media lost")
	}
}

func assertHexagonMCPClient(t *testing.T, ctx context.Context, session *Session) {
	t.Helper()
	if session.session.InitializeResult().ProtocolVersion != "2025-11-25" {
		t.Fatal("unexpected negotiated protocol")
	}
	tools := session.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools: %d", len(tools))
	}
	result, err := tools[0].Execute(ctx, map[string]any{})
	if err == nil || result.Success {
		t.Fatal("valid MCP tool failure must remain a failure")
	}
	preserved, ok := result.Output.(*PreservedToolResult)
	if !ok {
		t.Fatalf("mixed output: %T", result.Output)
	}
	assertMCPMixed(t, preserved.CallToolResult)
	resources, err := session.Resources(ctx)
	if err != nil || len(resources) != 1 {
		t.Fatalf("resources: %+v %v", resources, err)
	}
	resource, err := session.ReadResource(ctx, "fixture://report")
	if err != nil || len(resource.Contents) != 1 || !bytes.Equal(resource.Contents[0].Blob, []byte{7, 8, 9}) {
		t.Fatalf("resource: %+v %v", resource, err)
	}
	prompts, err := session.Prompts(ctx)
	if err != nil || len(prompts) != 1 {
		t.Fatalf("prompts: %+v %v", prompts, err)
	}
	prompt, err := session.GetPrompt(ctx, "inspect", nil)
	if err != nil || len(prompt.Messages) != 1 {
		t.Fatalf("prompt: %+v %v", prompt, err)
	}
	if image, ok := prompt.Messages[0].Content.(*sdk.ImageContent); !ok || !bytes.Equal(image.Data, []byte{1, 2, 3}) {
		t.Fatal("prompt image lost")
	}
}

// 两个方向均通过 HTTP 建立会话，服务或客户端的一端直接使用官方 SDK。
func TestMCPIndependentStreamableHTTPInterop(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	t.Run("official-client-to-hexagon", func(t *testing.T) {
		server := httptest.NewServer(interopHexagonMCP().HTTPHandler())
		defer server.Close()
		client := sdk.NewClient(&sdk.Implementation{Name: "independent-client", Version: "v1.5.0"}, nil)
		session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL, HTTPClient: server.Client()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		assertOfficialMCPClient(t, ctx, session)
	})
	t.Run("hexagon-client-to-official", func(t *testing.T) {
		official := interopOfficialMCP()
		server := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return official }, nil))
		defer server.Close()
		session, err := ConnectStreamableSession(ctx, server.URL, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		assertHexagonMCPClient(t, ctx, session)
	})
}

// 子进程只运行合成协议服务；标准输出完全交由 MCP stdio 传输。
func TestMCPProtocolStdioHelper(t *testing.T) {
	mode := os.Getenv("HEXAGON_MCP_INTEROP_HELPER")
	if mode == "" {
		return
	}
	var err error
	if mode == "official" {
		err = interopOfficialMCP().Run(context.Background(), &sdk.StdioTransport{})
	} else {
		err = interopHexagonMCP().ServeStdio(context.Background())
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestMCPIndependentStdioInterop(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	t.Run("hexagon-client-to-official", func(t *testing.T) {
		session, err := ConnectStdioSession(ctx, os.Args[0], map[string]string{"HEXAGON_MCP_INTEROP_HELPER": "official"}, "-test.run=^TestMCPProtocolStdioHelper$")
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		assertHexagonMCPClient(t, ctx, session)
	})
	t.Run("official-client-to-hexagon", func(t *testing.T) {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMCPProtocolStdioHelper$")
		command.Env = append(os.Environ(), "HEXAGON_MCP_INTEROP_HELPER=hexagon")
		client := sdk.NewClient(&sdk.Implementation{Name: "official-stdio-client", Version: "v1.5.0"}, nil)
		session, err := client.Connect(ctx, &sdk.CommandTransport{Command: command}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		assertOfficialMCPClient(t, ctx, session)
	})
}

func TestMCPOfficialCancellationInterop(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	started, stopped := make(chan struct{}), make(chan struct{})
	server := sdk.NewServer(&sdk.Implementation{Name: "official-cancel", Version: "v1.5.0"}, nil)
	server.AddTool(&sdk.Tool{Name: "wait", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	})
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
	defer httpServer.Close()
	session, err := ConnectStreamableSession(ctx, httpServer.URL, httpServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	callCtx, stop := context.WithCancel(ctx)
	defer stop()
	finished := make(chan error, 1)
	go func() { _, err := session.Tools()[0].Execute(callCtx, map[string]any{}); finished <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("tool did not start")
	}
	stop()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("canceled call returned success")
		}
	case <-ctx.Done():
		t.Fatal("client did not cancel")
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("official server did not observe cancellation")
	}
}
