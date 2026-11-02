package structured

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/schema"
)

// completionProvider 仅替换外部模型响应，保留实际生成、Schema 校验和调用边界。
type completionProvider struct {
	mockProvider
	completions []*llm.CompletionResponse
	requests    []llm.CompletionRequest
}

func (p *completionProvider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.requests = append(p.requests, req)
	idx := len(p.requests) - 1
	if idx >= len(p.completions) {
		idx = len(p.completions) - 1
	}
	return p.completions[idx], nil
}

func outputCall(id, name, arguments string) *llm.CompletionResponse {
	return &llm.CompletionResponse{
		ToolCalls:    []llm.ToolCall{{ID: id, Type: "function", Name: name, Arguments: arguments}},
		FinishReason: "tool_calls",
		Usage:        llm.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	}
}

func TestToolOutputPreservesRequestAndRepairsUsingCallID(t *testing.T) {
	first := outputCall("call-1", "clip_result", `{"count":9,"kind":"clip"}`)
	first.ProviderContinuation = &llm.ProviderContinuation{
		Protocol: "test-continuation", Output: []json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"r1"}`)},
	}
	provider := &completionProvider{completions: []*llm.CompletionResponse{
		first, outputCall("call-2", "clip_result", `{"count":2,"kind":"clip"}`),
	}}
	definition := countSchema()
	original, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	input := []llm.Message{llm.UserMessage("produce a clip")}
	result, err := GenerateWithMessages[validatedCount](t.Context(), provider, input,
		WithToolOutput("clip_result"), WithOutputSchema(definition), WithModel("specific-model"),
		WithMaxTokens(700), WithTemperature(0), WithMaxRetries(1))
	if err != nil || result.Count != 2 || len(provider.requests) != 2 {
		t.Fatalf("result=%+v calls=%d error=%v", result, len(provider.requests), err)
	}
	for _, req := range provider.requests {
		if req.Model != "specific-model" || req.MaxTokens != 700 || req.Temperature == nil || *req.Temperature != 0 {
			t.Fatalf("generation parameters lost: %+v", req)
		}
		if req.ResponseFormat != nil || req.ToolChoice != "required" || len(req.Tools) != 1 || req.Tools[0].Function.Name != "clip_result" {
			t.Fatalf("tool mode request malformed: %+v", req)
		}
		actual, err := json.Marshal(req.Tools[0].Function.Parameters)
		if err != nil || string(actual) != string(original) {
			t.Fatalf("provider received different schema: %s error=%v", actual, err)
		}
	}
	messages := provider.requests[1].Messages
	if len(messages) != 5 || messages[2].Role != llm.RoleAssistant || len(messages[2].ToolCalls) != 1 || messages[2].ToolCalls[0].ID != "call-1" {
		t.Fatalf("assistant tool call missing from repair: %+v", messages)
	}
	if messages[3].Role != llm.RoleTool || messages[3].ToolCallID != "call-1" || !strings.Contains(messages[3].Content, "validation failed") {
		t.Fatalf("repair is not paired with original tool call: %+v", messages[3])
	}
	if messages[2].ProviderContinuation == first.ProviderContinuation || !reflect.DeepEqual(messages[2].ProviderContinuation, first.ProviderContinuation) {
		t.Fatal("repair must preserve a detached continuation snapshot")
	}
	if len(input) != 1 || input[0].Content != "produce a clip" {
		t.Fatal("caller message history changed")
	}
	after, _ := json.Marshal(definition)
	if string(after) != string(original) {
		t.Fatal("caller schema changed")
	}
}

func TestToolOutputRejectsInvalidDynamicArguments(t *testing.T) {
	cases := map[string]string{
		"required":    `{"count":2}`,
		"type":        `{"count":"2","kind":"clip"}`,
		"enum":        `{"count":2,"kind":"other"}`,
		"range":       `{"count":4,"kind":"clip"}`,
		"extra field": `{"count":2,"kind":"clip","hidden":true}`,
		"markdown":    "```json\n{\"count\":2,\"kind\":\"clip\"}\n```",
	}
	for name, arguments := range cases {
		t.Run(name, func(t *testing.T) {
			provider := &completionProvider{completions: []*llm.CompletionResponse{outputCall("call-1", "structured_output", arguments)}}
			result, err := Generate[validatedCount](t.Context(), provider, "result", WithToolOutput(), WithOutputSchema(countSchema()), WithMaxRetries(0))
			var failure *Error
			if !errors.As(err, &failure) || len(failure.Attempts) != 1 || result != (validatedCount{}) || len(provider.requests) != 1 {
				t.Fatalf("invalid arguments became a result: result=%+v error=%v", result, err)
			}
		})
	}
}

func TestToolOutputValidatesRecursiveRefs(t *testing.T) {
	definition := &schema.Schema{Ref: "#/$defs/node", Defs: map[string]*schema.Schema{
		"node": {Type: "object", Required: []string{"name"}, AdditionalProperties: false,
			Properties: map[string]*schema.Schema{"name": {Type: "string"}, "child": {Ref: "#/$defs/node"}}},
	}}
	provider := &completionProvider{completions: []*llm.CompletionResponse{
		outputCall("first", "structured_output", `{"name":"root","child":{"name":1}}`),
		outputCall("second", "structured_output", `{"name":"root","child":{"name":"leaf"}}`),
	}}
	result, err := Generate[map[string]any](t.Context(), provider, "tree", WithToolOutput(), WithOutputSchema(definition), WithMaxRetries(1))
	if err != nil || len(provider.requests) != 2 || result["child"].(map[string]any)["name"] != "leaf" {
		t.Fatalf("recursive schema not enforced: result=%+v calls=%d error=%v", result, len(provider.requests), err)
	}
}

func TestOutputModesRejectAmbiguousToolResults(t *testing.T) {
	for name, resp := range map[string]*llm.CompletionResponse{
		"missing":    {Content: `{"count":2,"kind":"clip"}`},
		"wrong name": outputCall("id", "other", `{"count":2,"kind":"clip"}`),
		"missing ID": outputCall("", "structured_output", `{"count":2,"kind":"clip"}`),
		"multiple":   {ToolCalls: []llm.ToolCall{{ID: "a", Name: "structured_output", Arguments: `{}`}, {ID: "b", Name: "structured_output", Arguments: `{}`}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ExtractOutput(resp, OutputModeTool, ""); err == nil {
				t.Fatal("ambiguous tool output became successful JSON")
			}
		})
	}
	if _, err := ExtractOutput(outputCall("id", "structured_output", `{}`), OutputModePrompt, ""); err == nil {
		t.Fatal("text mode silently accepted a tool call")
	}
	provider := newMockProvider(`{}`)
	if _, err := Generate[map[string]any](t.Context(), provider, "result", WithToolOutput(), WithNativeJSONSchema()); err == nil || provider.callCount != 0 {
		t.Fatalf("conflicting modes reached provider: calls=%d error=%v", provider.callCount, err)
	}
}

func TestGenerateDoesNotRepairRefusalOrTruncation(t *testing.T) {
	for name, tc := range map[string]struct {
		reason string
		mode   Option
		want   error
	}{
		"prompt truncated": {"length", WithMaxRetries(3), ErrTruncated},
		"native refused":   {"content_filter", WithNativeJSONSchema(), ErrRefused},
		"tool truncated":   {"max_tokens", WithToolOutput(), ErrTruncated},
		"tool refused":     {"refusal", WithToolOutput(), ErrRefused},
	} {
		t.Run(name, func(t *testing.T) {
			resp := &llm.CompletionResponse{Content: `{"count":2,"kind":"clip"}`, FinishReason: tc.reason}
			if strings.HasPrefix(name, "tool") {
				resp.ToolCalls = outputCall("id", "structured_output", resp.Content).ToolCalls
			}
			provider := &completionProvider{completions: []*llm.CompletionResponse{resp}}
			result, err := Generate[validatedCount](t.Context(), provider, "result", tc.mode)
			if !errors.Is(err, tc.want) || result != (validatedCount{}) || len(provider.requests) != 1 {
				t.Fatalf("unfinished output became success or was retried: result=%+v calls=%d error=%v", result, len(provider.requests), err)
			}
		})
	}
}

func TestToolRepairFailuresStayInsideCompletionInvoker(t *testing.T) {
	provider := &completionProvider{completions: []*llm.CompletionResponse{outputCall("id", "structured_output", `{"count":9,"kind":"clip"}`)}}
	var purposes []string
	var totalTokens int
	ctx := llm.WithCompletionInvoker(t.Context(), llm.CompletionInvokerFunc(func(ctx context.Context, actual llm.CallProvider, req llm.CompletionRequest, purpose string) (*llm.CompletionResponse, error) {
		if actual != provider {
			t.Fatal("structured generation replaced the caller's provider")
		}
		purposes = append(purposes, purpose)
		resp, err := actual.Complete(ctx, req)
		if resp != nil {
			totalTokens += resp.Usage.TotalTokens
		}
		return resp, err
	}))
	_, err := Generate[validatedCount](ctx, provider, "result", WithToolOutput(), WithOutputSchema(countSchema()), WithMaxRetries(2))
	var failure *Error
	if !errors.As(err, &failure) || len(failure.Attempts) != 3 || len(provider.requests) != 3 || totalTokens != 90 {
		t.Fatalf("repair count or usage boundary violated: calls=%d tokens=%d error=%v", len(provider.requests), totalTokens, err)
	}
	if !reflect.DeepEqual(purposes, []string{"structured.generate", "structured.repair", "structured.repair"}) {
		t.Fatalf("wrong actual call purposes: %v", purposes)
	}
}

func TestCompletionInvokerCanStopBeforeProviderDispatch(t *testing.T) {
	provider := newMockProvider(`{"count":2,"kind":"clip"}`)
	blocked := errors.New("budget exhausted")
	ctx := llm.WithCompletionInvoker(t.Context(), llm.CompletionInvokerFunc(func(context.Context, llm.CallProvider, llm.CompletionRequest, string) (*llm.CompletionResponse, error) {
		return nil, blocked
	}))
	if _, err := Generate[validatedCount](ctx, provider, "result"); !errors.Is(err, blocked) || provider.callCount != 0 {
		t.Fatalf("contextual invoker was bypassed: calls=%d error=%v", provider.callCount, err)
	}
}
