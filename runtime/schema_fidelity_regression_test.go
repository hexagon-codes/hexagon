package runtime

import (
	"context"
	"encoding/json"
	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/llm/openai"
	"github.com/hexagon-codes/ai-core/schema"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeStructuredSchemaKeepsOriginalNumericContract(t *testing.T) {
	type minimum struct {
		N int64 `json:"n" min:"9007199254740993" required:"true"`
	}
	type maximum struct {
		N int64 `json:"n" max:"9007199254740995" required:"true"`
	}
	type unsigned struct {
		N uint64 `json:"n" enum:"18446744073709551615" required:"true"`
	}
	var dynamic, empty llm.Schema
	if err := json.Unmarshal([]byte(`{"type":"object","properties":{"n":{"type":"integer","enum":[9007199254740993]}},"required":["n"]}`), &dynamic); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"type":"object","properties":{"n":{"enum":[]}},"required":["n"]}`), &empty); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		s        *llm.Schema
		response string
		reject   bool
	}{
		{"minimum invalid", schema.Of[minimum](), `{"n":9007199254740992}`, true},
		{"minimum valid", schema.Of[minimum](), `{"n":9007199254740993}`, false},
		{"maximum invalid", schema.Of[maximum](), `{"n":9007199254740996}`, true},
		{"unsigned enum valid", schema.Of[unsigned](), `{"n":18446744073709551615}`, false},
		{"dynamic enum invalid", &dynamic, `{"n":9007199254740992}`, true},
		{"empty enum", &empty, `{"n":0}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var calls atomic.Int32
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "model": "gpt-6-luna", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": c.response}}}, "usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
			}))
			defer host.Close()
			provider := openai.New("local-fixture", openai.WithBaseURL(host.URL), openai.WithModel("gpt-6-luna"), openai.WithHTTPClient(host.Client()))
			runner := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			result, err := runner.Run(ctx, Request{ID: c.name, Messages: []llm.Message{llm.UserMessage("Return the requested number")}, StructuredOutput: &StructuredOutputSpec{Mode: StructuredModePrompt, Schema: c.s, MaxRepairs: 0}})
			if (err != nil) != c.reject {
				t.Fatalf("reject=%v error=%v", c.reject, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("model calls=%d, want 1", calls.Load())
			}
			if result == nil || result.Usage.TotalTokens != 2 {
				t.Fatalf("usage lost: %+v", result)
			}
			if c.reject {
				if len(result.Structured) > 0 {
					t.Fatal("invalid output adopted")
				}
			} else {
				v, e := schema.NewValidator(c.s)
				if e != nil {
					t.Fatal(e)
				}
				if e := v.ValidateJSON(result.Structured); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
