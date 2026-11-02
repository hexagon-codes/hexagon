package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/schema"
	"github.com/hexagon-codes/hexagon/llm/structured"
)

// structuredOutputError 保留结构化错误链，不让普通 Provider fallback 绕过修正上限。
type structuredOutputError struct{ Err error }

func (e *structuredOutputError) Error() string {
	return fmt.Sprintf("runtime structured output: %v", e.Err)
}
func (e *structuredOutputError) Unwrap() error      { return e.Err }
func (*structuredOutputError) UnsafeToReplay() bool { return true }

func cloneStructuredOutputSpec(spec *StructuredOutputSpec) (*StructuredOutputSpec, error) {
	if spec == nil {
		return nil, nil
	}
	copy := *spec
	if copy.Mode == "" {
		copy.Mode = StructuredModePrompt
	}
	if copy.Name == "" {
		copy.Name = "structured_output"
	}
	if copy.ToolName == "" {
		copy.ToolName = "structured_output"
	}
	if err := validateStructuredSpec(&copy); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(copy.Schema)
	if err != nil {
		return nil, fmt.Errorf("runtime structured output: encode schema: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var frozen llm.Schema
	if err := decoder.Decode(&frozen); err != nil {
		return nil, fmt.Errorf("runtime structured output: clone schema: %w", err)
	}
	copy.Schema = &frozen
	if copy.Mode == StructuredModeNative {
		copy.Schema.Strict()
	}
	if _, err := schema.NewValidator(copy.Schema); err != nil {
		return nil, fmt.Errorf("runtime structured output: invalid schema: %w", err)
	}
	return &copy, nil
}

func validateStructuredSpec(spec *StructuredOutputSpec) error {
	if spec.Schema == nil {
		return fmt.Errorf("runtime structured output: schema is required")
	}
	if spec.MaxRepairs < 0 {
		return fmt.Errorf("runtime structured output: max repairs must not be negative")
	}
	switch spec.Mode {
	case "", StructuredModePrompt, StructuredModeNative, StructuredModeTool:
		return nil
	default:
		return fmt.Errorf("runtime structured output: unknown mode %q", spec.Mode)
	}
}

// prepareStructuredModelRequest 使用已冻结的结果合同派生请求，保留业务工具与生成参数。
func prepareStructuredModelRequest(req llm.CompletionRequest, spec *StructuredOutputSpec) (llm.CompletionRequest, error) {
	if spec == nil {
		return req, nil
	}
	if err := validateStructuredSpec(spec); err != nil {
		return llm.CompletionRequest{}, err
	}
	prepared, err := CloneModelRequest(req)
	if err != nil {
		return llm.CompletionRequest{}, err
	}
	encoded, err := json.Marshal(spec.Schema)
	if err != nil {
		return llm.CompletionRequest{}, fmt.Errorf("runtime structured output: encode schema: %w", err)
	}
	instruction := "Complete any necessary business tools first. Return the final result as JSON matching this JSON Schema, without other text:\n" + string(encoded)
	switch spec.Mode {
	case StructuredModeNative:
		name := spec.Name
		if name == "" {
			name = "structured_output"
		}
		format := &llm.ResponseFormat{Type: "json_schema", JSONSchema: &llm.ResponseFormatJSONSchema{Name: name, Schema: spec.Schema, Strict: true}}
		if prepared.ResponseFormat != nil {
			current, err := json.Marshal(prepared.ResponseFormat)
			if err != nil {
				return llm.CompletionRequest{}, err
			}
			expected, _ := json.Marshal(format)
			if !bytes.Equal(current, expected) {
				return llm.CompletionRequest{}, fmt.Errorf("runtime structured output: generation response format conflicts with native result schema")
			}
		}
		prepared.ResponseFormat = format
	case StructuredModeTool:
		if prepared.ResponseFormat != nil {
			return llm.CompletionRequest{}, fmt.Errorf("runtime structured output: response format and tool output are mutually exclusive")
		}
		name := structuredToolName(spec)
		for _, tool := range prepared.Tools {
			if tool.Function.Name == name {
				return llm.CompletionRequest{}, fmt.Errorf("runtime structured output: result tool %q conflicts with a business tool", name)
			}
		}
		businessTools := len(prepared.Tools)
		prepared.Tools = append(prepared.Tools, llm.NewToolDefinition(name, "Return the final result matching the provided JSON Schema after completing necessary business tools.", spec.Schema))
		if prepared.ToolChoice == nil && businessTools == 0 {
			prepared.ToolChoice = "required"
		}
		instruction = fmt.Sprintf("Complete any necessary business tools first. Return the final result by calling %q exactly once with arguments matching its JSON Schema. Do not combine the result tool with business tool calls in the same response.", name)
	default:
		if prepared.ResponseFormat != nil {
			return llm.CompletionRequest{}, fmt.Errorf("runtime structured output: prompt mode conflicts with generation response format")
		}
	}
	prepared.Messages = append([]llm.Message{llm.SystemMessage(instruction)}, prepared.Messages...)
	return prepared, nil
}

func structuredToolName(spec *StructuredOutputSpec) string {
	if spec.ToolName != "" {
		return spec.ToolName
	}
	return "structured_output"
}

func (r *DefaultRunner) invokeStructured(ctx context.Context, req Request, callReq llm.CompletionRequest, state *State, emitter *runEmitter, next ModelCall) (*llm.CompletionResponse, error) {
	if req.StructuredOutput == nil {
		return next(ctx, callReq)
	}
	state.structured = nil
	state.structuredFinishReason = ""
	spec := req.StructuredOutput
	if err := validateStructuredSpec(spec); err != nil {
		return nil, &structuredOutputError{Err: err}
	}
	validator, err := schema.NewValidator(spec.Schema)
	if err != nil {
		return nil, &structuredOutputError{Err: err}
	}
	mode, name := structured.OutputMode(spec.Mode), structuredToolName(spec)
	prepared, err := CloneModelRequest(callReq)
	if err != nil {
		return nil, &structuredOutputError{Err: err}
	}
	var failures []structured.AttemptError
	for repair := 0; ; repair++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		callCtx := WithModelPurpose(ctx, "structured.generate")
		if repair > 0 {
			callCtx = WithModelPurpose(ctx, "structured.repair")
		}
		response, callErr := next(callCtx, prepared)
		raw, validationErr := structured.ExtractOutput(response, mode, name)
		if errors.Is(validationErr, structured.ErrRefused) || errors.Is(validationErr, structured.ErrTruncated) {
			return response, &structuredOutputError{Err: errors.Join(validationErr, callErr)}
		}
		if callErr != nil {
			return response, callErr
		}
		if response != nil && len(response.ToolCalls) > 0 {
			containsResult := false
			if spec.Mode == StructuredModeTool {
				for _, call := range response.ToolCalls {
					containsResult = containsResult || call.Name == name
				}
			}
			if !containsResult {
				return response, nil
			}
		}
		if validationErr == nil {
			validationErr = validator.ValidateJSON(raw)
		}
		if validationErr == nil {
			state.structured = append(json.RawMessage(nil), raw...)
			state.structuredFinishReason = response.FinishReason
			accepted := *response
			accepted.Content = string(raw)
			accepted.ToolCalls = nil
			if len(response.ToolCalls) > 0 {
				// 合成结果工具的正常工具结束映射为最终输出，暂停等原终态仍由执行核决议。
				switch strings.ToLower(strings.TrimSpace(response.FinishReason)) {
				case "tool_calls", "tool_use", "function_call":
					accepted.FinishReason = "stop"
				}
			}
			if len(response.ToolCalls) > 0 || response.Content != accepted.Content {
				accepted.ProviderContinuation = nil
			} else {
				accepted.ProviderContinuation = response.ProviderContinuation.Clone()
			}
			return &accepted, nil
		}
		output := ""
		if response != nil {
			output = response.Content
			if spec.Mode == StructuredModeTool && len(response.ToolCalls) > 0 {
				calls, _ := json.Marshal(response.ToolCalls)
				output = string(calls)
			}
		}
		failures = append(failures, structured.AttemptError{Output: output, Err: validationErr})
		if repair >= spec.MaxRepairs {
			return response, &structuredOutputError{Err: &structured.Error{Attempts: failures}}
		}
		feedback, err := structured.RepairMessages(response, mode, name, validationErr)
		if err != nil {
			return response, &structuredOutputError{Err: errors.Join(&structured.Error{Attempts: failures}, err)}
		}
		prepared, err = CloneModelRequest(prepared)
		if err != nil {
			return response, &structuredOutputError{Err: err}
		}
		prepared.Messages = append(prepared.Messages, feedback...)
	}
}

// revalidateStructuredFinal 在受控 AfterLLM 修改后校验实际将采用的最终正文。
// 此处不修正模型输出；中间件造成的合同错误不能额外触发模型调用。
func revalidateStructuredFinal(state *State, response *llm.CompletionResponse) error {
	spec := state.Request.StructuredOutput
	if spec == nil {
		return nil
	}
	previous := state.structured
	state.structured = nil
	if response != nil && len(response.ToolCalls) > 0 {
		state.structuredFinishReason = ""
		if len(previous) > 0 {
			response.ProviderContinuation = nil
		}
		if spec.Mode == StructuredModeTool {
			for _, call := range response.ToolCalls {
				if call.Name == structuredToolName(spec) {
					return &structuredOutputError{Err: fmt.Errorf("after LLM middleware introduced a result tool call after validation")}
				}
			}
		}
		return nil
	}
	raw, err := structured.ExtractOutput(response, structured.OutputModeNative, "")
	if err == nil {
		var validator *schema.Validator
		validator, err = schema.NewValidator(spec.Schema)
		if err == nil {
			err = validator.ValidateJSON(raw)
		}
	}
	if err != nil {
		state.structuredFinishReason = ""
		return &structuredOutputError{Err: fmt.Errorf("after LLM final result validation failed: %w", err)}
	}
	if !bytes.Equal(previous, raw) {
		response.ProviderContinuation = nil
	}
	state.structured = append(json.RawMessage(nil), raw...)
	return nil
}
