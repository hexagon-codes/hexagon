package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/llm/structured"
)

func runtimeResultSchema() *llm.Schema {
	minimum, maximum := 1.0, 3.0
	return &llm.Schema{Type: "object", Required: []string{"count"}, AdditionalProperties: false,
		Properties: map[string]*llm.Schema{"count": {Type: "integer", Minimum: &minimum, Maximum: &maximum}}}
}

func runtimeResultCall(id, arguments string, tokens int) *llm.CompletionResponse {
	return &llm.CompletionResponse{
		ToolCalls:    []llm.ToolCall{{ID: id, Type: "function", Name: "final_result", Arguments: arguments}},
		FinishReason: "tool_calls", Usage: llm.Usage{TotalTokens: tokens},
		ProviderContinuation: &llm.ProviderContinuation{Protocol: "test", Output: []json.RawMessage{json.RawMessage(`{"type":"function_call"}`)}},
	}
}

func TestStructuredSpecFreezesSchemaAndRejectsInvalidConfiguration(t *testing.T) {
	original := &StructuredOutputSpec{Schema: runtimeResultSchema()}
	frozen, err := cloneStructuredOutputSpec(original)
	if err != nil {
		t.Fatal(err)
	}
	original.Schema.Properties["count"].Type = "string"
	if frozen.Mode != StructuredModePrompt || frozen.Schema.Properties["count"].Type != "integer" || frozen.Schema == original.Schema {
		t.Fatal("structured schema did not remain an independent frozen contract")
	}
	for name, spec := range map[string]*StructuredOutputSpec{
		"missing schema":   {},
		"negative repairs": {Schema: runtimeResultSchema(), MaxRepairs: -1},
		"unknown mode":     {Schema: runtimeResultSchema(), Mode: "guess"},
		"unresolved ref":   {Schema: &llm.Schema{Ref: "#/$defs/missing"}},
	} {
		t.Run(name, func(t *testing.T) {
			provider := &fakeProvider{name: "invalid-contract"}
			_, err := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}}).Run(t.Context(), Request{StructuredOutput: spec})
			if err == nil || provider.calls != 0 {
				t.Fatalf("invalid configuration reached provider: calls=%d error=%v", provider.calls, err)
			}
		})
	}
}

func TestStructuredPreparationPreservesCallerParametersAndTools(t *testing.T) {
	zero, topP := 0.0, 0.7
	original := llm.CompletionRequest{
		Model: "pinned-model", Messages: []llm.Message{llm.UserMessage("work")},
		Tools:      []llm.ToolDefinition{llm.NewToolDefinition("lookup", "lookup", &llm.Schema{Type: "object"})},
		ToolChoice: "auto", MaxTokens: 800, Temperature: &zero, TopP: &topP,
		Stop: []string{"END"}, User: "request-user", Metadata: map[string]any{"route": "pinned"},
	}
	for _, mode := range []StructuredOutputMode{StructuredModePrompt, StructuredModeNative, StructuredModeTool} {
		t.Run(string(mode), func(t *testing.T) {
			spec, err := cloneStructuredOutputSpec(&StructuredOutputSpec{Schema: runtimeResultSchema(), Mode: mode, Name: "final_schema", ToolName: "final_result"})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := prepareStructuredModelRequest(original, spec)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Model != "pinned-model" || prepared.MaxTokens != 800 || prepared.Temperature == nil || *prepared.Temperature != 0 || prepared.TopP == nil || *prepared.TopP != 0.7 || prepared.User != "request-user" || prepared.ToolChoice != "auto" || !reflect.DeepEqual(prepared.Stop, []string{"END"}) || prepared.Metadata["route"] != "pinned" {
				t.Fatalf("generation parameters were replaced: %+v", prepared)
			}
			if prepared.Tools[0].Function.Name != "lookup" || len(prepared.Messages) != 2 || prepared.Messages[1].Content != "work" {
				t.Fatalf("caller bindings or history were replaced: %+v", prepared)
			}
			switch mode {
			case StructuredModeNative:
				if prepared.ResponseFormat == nil || prepared.ResponseFormat.Type != "json_schema" || !prepared.ResponseFormat.JSONSchema.Strict || prepared.ResponseFormat.JSONSchema.Name != "final_schema" || prepared.ResponseFormat.JSONSchema.Schema != spec.Schema {
					t.Fatal("native mode did not carry the frozen result schema")
				}
			case StructuredModeTool:
				if prepared.ResponseFormat != nil || len(prepared.Tools) != 2 || prepared.Tools[1].Function.Name != "final_result" || prepared.Tools[1].Function.Parameters != spec.Schema {
					t.Fatal("tool mode did not carry the frozen result schema")
				}
			default:
				if prepared.ResponseFormat != nil || len(prepared.Tools) != 1 || !strings.Contains(prepared.Messages[0].Content, `"maximum":3`) {
					t.Fatal("prompt mode did not carry the result schema")
				}
			}
			prepared.Messages[1].Content = "changed"
			prepared.Tools[0].Function.Name = "changed"
			prepared.Stop[0] = "changed"
			if original.Messages[0].Content != "work" || original.Tools[0].Function.Name != "lookup" || original.Stop[0] != "END" {
				t.Fatal("preparation mutated caller-owned request")
			}
		})
	}
}

func TestStructuredRuntimeBusinessToolsThenRepairAndFinalResult(t *testing.T) {
	responses := []*llm.CompletionResponse{
		{ToolCalls: []llm.ToolCall{{ID: "lookup-1", Name: "lookup", Arguments: `{}`}}, FinishReason: "tool_calls", Usage: llm.Usage{TotalTokens: 2}},
		runtimeResultCall("invalid-result", `{"count":9}`, 3),
		runtimeResultCall("valid-result", `{"count":2}`, 5),
	}
	var requests []llm.CompletionRequest
	provider := &continuationProvider{complete: func(req llm.CompletionRequest) (*llm.CompletionResponse, error) {
		requests = append(requests, req)
		if len(requests) > len(responses) {
			t.Fatal("unexpected extra model invocation")
		}
		return responses[len(requests)-1], nil
	}}
	executor := &fakeToolExecutor{}
	var attempts []ModelAttempt
	var observedState *State
	runner := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider, Model: "actual-model"}, ToolExecutor: executor,
		Middleware: []Middleware{MiddlewareFuncSet{AfterModelAttemptFunc: func(_ context.Context, _ *State, attempt ModelAttempt) error {
			attempts = append(attempts, attempt)
			return nil
		}}}})
	result, err := runner.RunWithSink(t.Context(), Request{
		ID: "structured-tools", Messages: []llm.Message{llm.UserMessage("lookup and count")},
		Tools:            []llm.ToolDefinition{llm.NewToolDefinition("lookup", "lookup", &llm.Schema{Type: "object"})},
		Generation:       llm.GenerationOptions{MaxTokens: 800},
		StructuredOutput: &StructuredOutputSpec{Schema: runtimeResultSchema(), Mode: StructuredModeTool, ToolName: "final_result", MaxRepairs: 1},
	}, EventSinkFunc(func(_ context.Context, event Event) error {
		if event.Type == EventRunFinished {
			observedState = event.State
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != `{"count":2}` || string(result.Structured) != `{"count":2}` || result.ModelFinishReason != "tool_calls" || result.StopReason != StopReasonEndTurn {
		t.Fatalf("validated result or original ending lost: %+v", result)
	}
	if executor.calls != 1 || len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "lookup" || len(requests) != 3 || result.Usage.TotalTokens != 10 {
		t.Fatalf("result tool executed or attempts lost: tools=%d requests=%d result=%+v", executor.calls, len(requests), result)
	}
	if len(attempts) != 3 || attempts[0].Purpose != "structured.generate" || attempts[1].Purpose != "structured.generate" || attempts[2].Purpose != "structured.repair" || attempts[0].ID == attempts[1].ID || attempts[1].ID == attempts[2].ID {
		t.Fatalf("actual attempt facts missing: %+v", attempts)
	}
	if len(result.OutputMessages) != 3 || result.OutputMessages[2].ProviderContinuation != nil || len(result.OutputMessages[2].ToolCalls) != 0 {
		t.Fatalf("final result retained synthetic tool protocol: %+v", result.OutputMessages)
	}
	for _, message := range append(CloneOutputMessages(result.OutputMessages), observedState.Messages...) {
		if strings.Contains(message.Content, "validation failed") || strings.Contains(message.Content, `"count":9`) {
			t.Fatalf("repair-only history polluted accepted run messages: %+v", message)
		}
	}
	for _, req := range requests {
		if req.Model != "actual-model" || req.MaxTokens != 800 || len(req.Tools) != 2 {
			t.Fatalf("repair replaced selected model, parameters or tools: %+v", req)
		}
	}
	last := requests[2].Messages
	if len(last) < 3 || last[len(last)-2].Role != llm.RoleTool || last[len(last)-2].ToolCallID != "invalid-result" {
		t.Fatal("repair tool result was not paired with the failed result call")
	}
	if len(responses[2].ToolCalls) != 1 || responses[2].ProviderContinuation == nil || responses[2].FinishReason != "tool_calls" {
		t.Fatal("normalization mutated original provider evidence")
	}
}

func TestStructuredRuntimeFailuresKeepUsageAndDoNotFallback(t *testing.T) {
	for name, tc := range map[string]struct {
		response *llm.CompletionResponse
		callErr  error
		want     error
		calls    int
	}{
		"refusal":               {&llm.CompletionResponse{Content: `{"count":2}`, FinishReason: "refusal", Usage: llm.Usage{TotalTokens: 3}}, nil, structured.ErrRefused, 1},
		"truncation":            {&llm.CompletionResponse{Content: `{"count":2}`, FinishReason: "length", Usage: llm.Usage{TotalTokens: 3}}, nil, structured.ErrTruncated, 1},
		"truncation with error": {&llm.CompletionResponse{Content: `{"count":2}`, FinishReason: "length", Usage: llm.Usage{TotalTokens: 3}}, errors.New("incomplete response"), structured.ErrTruncated, 1},
		"repairs exhausted":     {&llm.CompletionResponse{Content: `{"count":9}`, FinishReason: "stop", Usage: llm.Usage{TotalTokens: 3}}, nil, nil, 2},
	} {
		t.Run(name, func(t *testing.T) {
			primaryCalls, backupCalls := 0, 0
			primary := &continuationProvider{complete: func(llm.CompletionRequest) (*llm.CompletionResponse, error) {
				primaryCalls++
				return tc.response, tc.callErr
			}}
			backup := &continuationProvider{complete: func(llm.CompletionRequest) (*llm.CompletionResponse, error) {
				backupCalls++
				return &llm.CompletionResponse{Content: `{"count":2}`}, nil
			}}
			runner := NewRunner(Config{ProviderSelector: fallbackSelector{primary: primary, backup: backup}})
			result, err := runner.Run(t.Context(), Request{ID: "structured-failure", StructuredOutput: &StructuredOutputSpec{Schema: runtimeResultSchema(), MaxRepairs: 1}})
			if err == nil || tc.want != nil && !errors.Is(err, tc.want) || primaryCalls != tc.calls || backupCalls != 0 || result == nil || result.Usage.TotalTokens != 3*tc.calls || len(result.Structured) != 0 {
				t.Fatalf("failure became success or bypassed bound: result=%+v primary=%d fallback=%d error=%v", result, primaryCalls, backupCalls, err)
			}
			if tc.callErr != nil && !errors.Is(err, tc.callErr) {
				t.Fatal("provider error disappeared from the terminal structured failure")
			}
		})
	}
}

func TestStructuredRuntimePromptAndNativeValidateFinalOutput(t *testing.T) {
	for _, mode := range []StructuredOutputMode{StructuredModePrompt, StructuredModeNative} {
		t.Run(string(mode), func(t *testing.T) {
			provider := &fakeProvider{name: "structured", responses: []*llm.CompletionResponse{{Content: `{"count":2}`, FinishReason: "stop"}}}
			result, err := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}}).Run(t.Context(), Request{StructuredOutput: &StructuredOutputSpec{Schema: runtimeResultSchema(), Mode: mode}})
			if err != nil || string(result.Structured) != `{"count":2}` || provider.calls != 1 {
				t.Fatalf("final JSON not accepted: result=%+v calls=%d error=%v", result, provider.calls, err)
			}
			if mode == StructuredModeNative && provider.lastRequest.ResponseFormat == nil || mode == StructuredModePrompt && provider.lastRequest.ResponseFormat != nil {
				t.Fatal("actual provider received the wrong output strategy")
			}
		})
	}
}

func TestStructuredRuntimeRevalidatesAfterLLMResult(t *testing.T) {
	for name, tc := range map[string]struct {
		content string
		valid   bool
	}{
		"missing required field": {`{}`, false},
		"non JSON":               {`not JSON`, false},
		"valid replacement":      {`{"count":3}`, true},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			provider := &continuationProvider{complete: func(llm.CompletionRequest) (*llm.CompletionResponse, error) {
				calls++
				return &llm.CompletionResponse{
					Content: `{"count":2}`, FinishReason: "stop", Usage: llm.Usage{TotalTokens: 5},
					ProviderContinuation: &llm.ProviderContinuation{Protocol: "test", Output: []json.RawMessage{json.RawMessage(`{"text":"old result"}`)}},
				}, nil
			}}
			runner := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: provider}, Middleware: []Middleware{MiddlewareFuncSet{
				AfterLLMFunc: func(_ context.Context, _ *State, response *llm.CompletionResponse) error {
					response.Content = tc.content
					return nil
				},
			}}})
			result, err := runner.Run(t.Context(), Request{StructuredOutput: &StructuredOutputSpec{Schema: runtimeResultSchema(), MaxRepairs: 2}})
			if result == nil || result.Usage.TotalTokens != 5 || calls != 1 {
				t.Fatalf("after hook revalidation lost usage or called model again: result=%+v calls=%d error=%v", result, calls, err)
			}
			if !tc.valid {
				var failure *structuredOutputError
				if !errors.As(err, &failure) || len(result.Structured) != 0 || result.StopReason != "" {
					t.Fatalf("invalid after hook result became successful structured output: result=%+v error=%v", result, err)
				}
				return
			}
			if err != nil || string(result.Structured) != tc.content || result.Content != tc.content || len(result.OutputMessages) != 1 || result.OutputMessages[0].ProviderContinuation != nil {
				t.Fatalf("valid replacement drifted from structured result or protocol history: result=%+v error=%v", result, err)
			}
		})
	}
}
