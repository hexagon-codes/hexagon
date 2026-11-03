package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/tool"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type precisionInput struct {
	ID int64 `json:"id" required:"true"`
}

func TestMCPArgumentsRejectTrailingJSON(t *testing.T) {
	var args map[string]any
	if err := decodeMCPArguments([]byte(`{"id":1} {"id":2}`), &args); err == nil {
		t.Fatal("trailing JSON value was accepted")
	}
	var precise map[string]any
	if err := decodeMCPArguments([]byte(`{"id":9007199254740993}`), &precise); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(precise["id"]) != "9007199254740993" {
		t.Fatalf("id=%v", precise["id"])
	}
}

func TestMCPServerPreservesLargeIntegerArguments(t *testing.T) {
	var received atomic.Int64
	echo := tool.NewFunc("echo_id", "return the supplied integer", func(_ context.Context, input precisionInput) (map[string]any, error) {
		received.Store(input.ID)
		return map[string]any{"ok": true}, nil
	})
	server := NewMCPServerV2("review", "1.0.0")
	server.RegisterTool(echo)
	host := httptest.NewServer(server.HTTPHandler())
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "independent", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{Endpoint: host.URL, HTTPClient: host.Client()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	_, err = session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "echo_id", Arguments: map[string]any{"id": json.Number("9007199254740993")}})
	if err != nil {
		t.Fatal(err)
	}
	if received.Load() != 9007199254740993 {
		t.Fatalf("server received %d", received.Load())
	}
}

func TestMCPServeHTTPBindFailureDoesNotLeakWatcher(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = NewMCPServerV2("review", "1.0.0").ServeHTTP(ctx, occupied.Addr().String())
	if err == nil {
		t.Fatal("expected bind failure")
	}
	var output strings.Builder
	if err := pprof.Lookup("goroutine").WriteTo(&output, 2); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "mcp.serveHTTP.func1") {
		t.Fatal("serveHTTP shutdown watcher still running after bind failure")
	}
}
