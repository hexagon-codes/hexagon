package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hexagon-codes/ai-core/schema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMCPWireSchemaMatchesAcceptedResponse(t *testing.T) {
	// 独立合同使用固定原始JSON，不以适配后的Schema计算期望。
	raw := json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer","enum":[9007199254740993]}},"required":["n"]}`)
	for _, mode := range []string{"json", "json-parameter", "sse", "sse-eof-newline", "sse-eof-no-newline", "ignored-event-before-response"} {
		t.Run(mode, func(t *testing.T) {
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					w.WriteHeader(405)
					return
				}
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if len(request.ID) == 0 {
					w.WriteHeader(202)
					return
				}
				result := any(map[string]any{"tools": []any{map[string]any{"name": "exact", "inputSchema": raw}}})
				if request.Method == "initialize" {
					result = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "test", "version": "1"}}
				}
				body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
				if request.Method == "initialize" || mode == "json" || mode == "json-parameter" {
					contentType := "application/json"
					if mode == "json-parameter" && request.Method != "initialize" {
						contentType = `application/json; profile="text/event-stream"`
					}
					w.Header().Set("Content-Type", contentType)
					_, _ = w.Write(body)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if mode == "ignored-event-before-response" {
					wrong, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"tools": []any{map[string]any{"name": "exact", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"enum": []int{7}}}}}}}})
					_, _ = fmt.Fprintf(w, "event: sideband\ndata: %s\n\n", wrong)
				}
				ending := "\n\n"
				if mode == "sse-eof-newline" {
					ending = "\n"
				}
				if mode == "sse-eof-no-newline" {
					ending = ""
				}
				_, _ = fmt.Fprintf(w, "data: %s%s", body, ending)
			}))
			defer host.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			tools, closer, err := ConnectMCPServerV2(ctx, &sdk.StreamableClientTransport{Endpoint: host.URL, DisableStandaloneSSE: true, MaxRetries: -1})
			if err != nil {
				t.Fatal(err)
			}
			defer closer.Close()
			if len(tools) != 1 {
				t.Fatalf("tools=%d", len(tools))
			}
			v, err := schema.NewValidator(tools[0].Schema())
			if err != nil {
				t.Fatal(err)
			}
			if err := v.ValidateJSON([]byte(`{"n":9007199254740993}`)); err != nil {
				t.Fatal(err)
			}
			for _, bad := range []string{`{"n":9007199254740992}`, `{"n":7}`} {
				if v.ValidateJSON([]byte(bad)) == nil {
					t.Fatalf("wrong value accepted: %s", bad)
				}
			}
		})
	}
}

func TestWireSchemasIgnoreUnrequestedAndRepeatedResponses(t *testing.T) {
	w := &wireSchemas{}
	w.request([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	wrong := []byte(`{"jsonrpc":"2.0","id":9,"result":{"tools":[{"name":"exact","inputSchema":{"enum":[7]}}]}}`)
	w.capture(wrong)
	if w.schema("exact", nil) != nil {
		t.Fatal("unmatched response adopted")
	}
	good := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"exact","inputSchema":{"enum":[3]}}]}}`)
	w.capture(good)
	w.capture([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"exact","inputSchema":{"enum":[7]}}]}}`))
	v, err := schema.NewValidator(w.schema("exact", nil))
	if err != nil {
		t.Fatal(err)
	}
	if v.ValidateJSON([]byte(`3`)) != nil || v.ValidateJSON([]byte(`7`)) == nil {
		t.Fatal("completed response identity was reused")
	}
}

func TestMCPProjectionPreservesEmptyConstraints(t *testing.T) {
	for _, raw := range []string{`{"enum":[]}`, `{"anyOf":[]}`, `{"oneOf":[]}`, `{"allOf":[]}`} {
		var input map[string]any
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatal(err)
		}
		converted := sdkSchemaToAICore(input)
		encoded, err := json.Marshal(aicoreSchemaToSDKMap(converted, false))
		if err != nil {
			t.Fatal(err)
		}
		var actual map[string]any
		if err := json.Unmarshal(encoded, &actual); err != nil {
			t.Fatal(err)
		}
		for name := range input {
			if _, ok := actual[name]; !ok {
				t.Fatalf("%s was removed", name)
			}
		}
	}
}
