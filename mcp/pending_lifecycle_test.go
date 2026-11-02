package mcp

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPendingSchemaRequestEndsWithResponse(t *testing.T) {
	for _, mode := range []string{"valid", "error", "non-rpc", "unsupported", "accepted"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "valid":
					io.WriteString(w, `{"jsonrpc":"2.0","id":7,"result":{"tools":[]}}`)
				case "error":
					w.WriteHeader(503)
					io.WriteString(w, `{}`)
				case "accepted":
					w.WriteHeader(202)
				case "unsupported":
					w.Header().Set("Content-Type", "text/plain")
					io.WriteString(w, "not JSON-RPC")
				default:
					io.WriteString(w, `{}`)
				}
			}))
			defer server.Close()
			schemas := &wireSchemas{}
			transport := &schemaHTTPTransport{base: server.Client().Transport, schemas: schemas}
			req, e := http.NewRequest(http.MethodPost, server.URL, bytes.NewBufferString(`{"jsonrpc":"2.0","id":7,"method":"tools/list"}`))
			if e != nil {
				t.Fatal(e)
			}
			resp, e := transport.RoundTrip(req)
			if e != nil {
				t.Fatal(e)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			schemas.mu.Lock()
			remaining := len(schemas.pending)
			schemas.mu.Unlock()
			want := 0
			if mode == "accepted" {
				want = 1
				schemas.capture([]byte(`{"jsonrpc":"2.0","id":7,"result":{"tools":[]}}`))
				if len(schemas.pending) != 0 {
					t.Fatal("later response not correlated")
				}
			}
			if remaining != want {
				t.Fatalf("remaining=%d want=%d", remaining, want)
			}
		})
	}
}
func TestWireRequestIDKeepsTypesAndExactIntegerValue(t *testing.T) {
	if wireIDKey([]byte(`2`)) != wireIDKey([]byte(`2e0`)) {
		t.Fatal("equivalent integers differ")
	}
	if wireIDKey([]byte(`"2"`)) == wireIDKey([]byte(`2`)) {
		t.Fatal("ID types collapsed")
	}
	if wireIDKey([]byte(`2.1`)) != "" {
		t.Fatal("fractional ID truncated")
	}
}

type deliveredSchemaBody struct{ delivered chan struct{} }

func (b *deliveredSchemaBody) Read(p []byte) (int, error) {
	n := copy(p, "id: resume\n\n")
	close(b.delivered)
	return n, io.EOF
}
func (*deliveredSchemaBody) Close() error { return nil }
func TestSchemaResponseCloseCanOverlapRead(t *testing.T) {
	underlying := &deliveredSchemaBody{delivered: make(chan struct{})}
	body := &schemaResponseBody{ReadCloser: underlying, schemas: &wireSchemas{}, sse: true}
	done := make(chan struct{})
	go func() { defer close(done); body.Read(make([]byte, 64)) }()
	<-underlying.delivered
	if e := body.Close(); e != nil {
		t.Fatal(e)
	}
	<-done
}
