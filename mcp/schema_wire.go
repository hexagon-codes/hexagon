package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// wireSchemas在SDK把interface数值转为float64之前，保留本会话的工具Schema原文。
// 只保存Schema，不记录认证、工具参数或业务响应。
type wireSchemas struct {
	mu      sync.Mutex
	schemas map[string]json.RawMessage
}

func (w *wireSchemas) capture(data []byte) {
	var response struct {
		Result struct {
			Tools []struct {
				Name  string          `json:"name"`
				Input json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &response) != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, t := range response.Result.Tools {
		if t.Name != "" && len(t.Input) > 0 {
			if w.schemas == nil {
				w.schemas = make(map[string]json.RawMessage)
			}
			w.schemas[t.Name] = append(json.RawMessage(nil), t.Input...)
		}
	}
}

func (w *wireSchemas) schema(name string, fallback any) *llm.Schema {
	w.mu.Lock()
	raw := append(json.RawMessage(nil), w.schemas[name]...)
	w.mu.Unlock()
	if len(raw) > 0 {
		return sdkSchemaToAICore(raw)
	}
	return sdkSchemaToAICore(fallback)
}

type schemaReadConnection struct {
	sdkmcp.Connection
	schemas *wireSchemas
}

func (c *schemaReadConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	msg, err := c.Connection.Read(ctx)
	if err == nil {
		if raw, e := jsonrpc.EncodeMessage(msg); e == nil {
			c.schemas.capture(raw)
		}
	}
	return msg, err
}

// Streamable连接必须保留SDK内部的会话更新接口，因此在HTTP正文处观察而不包装连接。
type schemaHTTPTransport struct {
	base    http.RoundTripper
	schemas *wireSchemas
}

func (t *schemaHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(req)
	if err == nil && response.Body != nil {
		sse := strings.Contains(response.Header.Get("Content-Type"), "text/event-stream")
		response.Body = &schemaResponseBody{ReadCloser: response.Body, schemas: t.schemas, sse: sse}
	}
	return response, err
}

type schemaResponseBody struct {
	io.ReadCloser
	schemas  *wireSchemas
	sse      bool
	pending  []byte
	event    []byte
	captured bool
}

func (b *schemaResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if !b.captured {
		b.pending = append(b.pending, p[:n]...)
		if b.sse {
			for {
				end := bytes.IndexByte(b.pending, '\n')
				if end < 0 {
					break
				}
				line := bytes.TrimSuffix(b.pending[:end], []byte{'\r'})
				if len(line) == 0 {
					b.schemas.capture(b.event)
					b.event = nil
				} else if bytes.HasPrefix(line, []byte("data:")) {
					if len(b.event) > 0 {
						b.event = append(b.event, '\n')
					}
					b.event = append(b.event, bytes.TrimPrefix(line[5:], []byte{' '})...)
				}
				b.pending = b.pending[end+1:]
			}
		} else if json.Valid(b.pending) || err == io.EOF {
			b.schemas.capture(b.pending)
			b.pending = nil
			b.captured = true
		}
	}
	return n, err
}
