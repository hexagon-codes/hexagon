package runtime

import (
	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/template"
)

func cloneContentParts(parts []llm.ContentPart) []llm.ContentPart {
	if parts == nil {
		return nil
	}
	return CloneOutputMessages([]llm.Message{{MultiContent: parts}})[0].MultiContent
}

func cloneStructuredPayload(payload *StructuredPayload) *StructuredPayload {
	if payload == nil {
		return nil
	}
	copy := *payload
	copy.Data = append([]byte(nil), payload.Data...)
	return &copy
}

func cloneToolResult(result ToolResult) ToolResult {
	result.Raw = cloneRuntimeValue(result.Raw)
	result.ContentParts = cloneContentParts(result.ContentParts)
	result.ToolSnapshot = cloneRuntimeToolSnapshot(result.ToolSnapshot)
	result.Structured = cloneStructuredPayload(result.Structured)
	return result
}

// snapshotEventPayload 隔离观察者可见的数据；协议续传字节仍按私有字段保留。
func snapshotEventPayload(event Event) Event {
	if event.Response != nil {
		response := *event.Response
		response.ToolCalls = append([]llm.ToolCall(nil), response.ToolCalls...)
		response.ProviderContinuation = response.ProviderContinuation.Clone()
		if response.UsageReceipt != nil {
			usage := *response.UsageReceipt
			response.UsageReceipt = &usage
		}
		if response.ReasoningReceipt != nil {
			receipt := *response.ReasoningReceipt
			response.ReasoningReceipt = &receipt
		}
		event.Response = &response
	}
	if event.Chunk != nil {
		chunk := *event.Chunk
		chunk.ToolCalls = append([]llm.ToolCall(nil), chunk.ToolCalls...)
		chunk.Raw = append([]byte(nil), chunk.Raw...)
		chunk.ProviderContinuation = chunk.ProviderContinuation.Clone()
		chunk.ReasoningEvidence = cloneRuntimeMetadata(chunk.ReasoningEvidence)
		if chunk.Usage != nil {
			usage := *chunk.Usage
			chunk.Usage = &usage
		}
		if chunk.ReasoningDisclosure != nil {
			disclosure := *chunk.ReasoningDisclosure
			chunk.ReasoningDisclosure = &disclosure
		}
		event.Chunk = &chunk
	}
	if event.ToolCall != nil {
		call := *event.ToolCall
		event.ToolCall = &call
	}
	if event.ToolResult != nil {
		result := cloneToolResult(*event.ToolResult)
		event.ToolResult = &result
	}
	event.Metadata = cloneRuntimeMetadata(event.Metadata)
	event.Redaction.Fields = append([]string(nil), event.Redaction.Fields...)
	if result, ok := event.Payload.(*Result); ok && result != nil {
		copy := *result
		copy.Metadata = cloneRuntimeMetadata(result.Metadata)
		copy.ToolCalls = append([]ToolCallRecord(nil), result.ToolCalls...)
		for i := range copy.ToolCalls {
			copy.ToolCalls[i].Result = cloneToolResult(copy.ToolCalls[i].Result)
		}
		copy.OutputMessages = CloneOutputMessages(result.OutputMessages)
		copy.Structured = append([]byte(nil), result.Structured...)
		copy.Blocks = cloneRuntimeValue(result.Blocks).(template.Blocks)
		event.Payload = &copy
	} else {
		event.Payload = cloneRuntimeValue(event.Payload)
	}
	return event
}
