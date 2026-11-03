package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
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
	pending map[string]bool
}

// request只登记tools/list的真实请求身份，不记录业务请求载荷。
func (w *wireSchemas) request(data []byte) string {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if json.Unmarshal(data, &request) != nil || request.Method != "tools/list" || len(request.ID) == 0 {
		return ""
	}
	id := string(bytes.TrimSpace(request.ID))
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending == nil {
		w.pending = make(map[string]bool)
	}
	w.pending[id] = true
	return id
}
func (w *wireSchemas) forget(id string) { w.mu.Lock(); delete(w.pending, id); w.mu.Unlock() }

func (w *wireSchemas) capture(data []byte) {
	var response struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
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
	id := string(bytes.TrimSpace(response.ID))
	if response.Method != "" || !w.pending[id] {
		return
	}
	delete(w.pending, id)
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

func (c *schemaReadConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	id := ""
	if raw, err := jsonrpc.EncodeMessage(msg); err == nil {
		id = c.schemas.request(raw)
	}
	err := c.Connection.Write(ctx, msg)
	if err != nil && id != "" {
		c.schemas.forget(id)
	}
	return err
}

// Streamable连接必须保留SDK内部的会话更新接口，因此在HTTP正文处观察而不包装连接。
type schemaHTTPTransport struct {
	base    http.RoundTripper
	schemas *wireSchemas
}

func (t *schemaHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	id := ""
	if req.GetBody != nil {
		if body, err := req.GetBody(); err == nil {
			data, readErr := io.ReadAll(body)
			_ = body.Close()
			if readErr == nil {
				id = t.schemas.request(data)
			}
		}
	}
	response, err := t.base.RoundTrip(req)
	if err != nil && id != "" {
		t.schemas.forget(id)
	}
	if err == nil && response.Body != nil {
		mediaType, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if parseErr == nil && (mediaType == "application/json" || mediaType == "text/event-stream") {
			response.Body = &schemaResponseBody{ReadCloser: response.Body, schemas: t.schemas, sse: mediaType == "text/event-stream"}
		}
	}
	return response, err
}

type schemaResponseBody struct {
	io.ReadCloser
	schemas   *wireSchemas
	sse       bool
	pending   []byte
	event     []byte
	eventName string
	captured  bool
}

func (b *schemaResponseBody) dispatch() {
	if len(b.event) > 0 && (b.eventName == "" || b.eventName == "message") {
		b.schemas.capture(b.event)
	}
	b.event = nil
	b.eventName = ""
}
func (b *schemaResponseBody) line(line []byte) {
	line = bytes.TrimRight(line, "\r\n")
	if len(line) == 0 {
		b.dispatch()
		return
	}
	field, value, found := bytes.Cut(line, []byte{':'})
	if !found {
		return
	}
	switch string(field) {
	case "event":
		b.eventName = strings.TrimSpace(string(value))
	case "data":
		if len(b.event) > 0 {
			b.event = append(b.event, '\n')
		}
		b.event = append(b.event, bytes.TrimSpace(value)...)
	}
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
				b.line(b.pending[:end])
				b.pending = b.pending[end+1:]
			}
			// SDK在EOF也提交最后事件，捕获器必须在SDK解码之前作出同样的决议。
			if err == io.EOF {
				if len(b.pending) > 0 {
					b.line(b.pending)
				}
				b.pending = nil
				b.dispatch()
				b.captured = true
			}
		} else if json.Valid(b.pending) || err == io.EOF {
			b.schemas.capture(b.pending)
			b.pending = nil
			b.captured = true
		}
	}
	return n, err
}
