package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

type toolImageResult struct{}

func (toolImageResult) ToolMessageContent() []llm.ContentPart {
	return []llm.ContentPart{llm.NewImageURLPart("data:image/png;base64,aW1hZ2U=", "auto")}
}

type imageToolExecutor struct{ calls int }

func (e *imageToolExecutor) Execute(context.Context, llm.ToolCall) (ToolResult, error) {
	e.calls++
	return ToolResult{Content: "[MCP image content]", Raw: toolImageResult{}}, nil
}

func TestRunnerToolImagePreservesPairingAndModelInput(t *testing.T) {
	p := &fakeProvider{name: "vision", responses: []*llm.CompletionResponse{{ToolCalls: []llm.ToolCall{{ID: "image-call", Name: "read-image", Arguments: `{}`}}}, {Content: "image read"}}}
	e := &imageToolExecutor{}
	r := NewRunner(Config{ProviderSelector: StaticProviderSelector{Provider: p, Name: "vision", Model: "vision-model"}, ToolExecutor: e})
	result, err := r.Run(context.Background(), Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "Read the tool image"}}, Tools: []llm.ToolDefinition{llm.NewToolDefinition("read-image", "Read an image", nil)}})
	if err != nil || result.Content != "image read" || e.calls != 1 {
		t.Fatalf("tool execution/result=%+v calls=%d error=%v", result, e.calls, err)
	}
	var paired, media bool
	for i, message := range p.lastRequest.Messages {
		if message.Role == llm.RoleTool && message.ToolCallID == "image-call" {
			paired = true
		}
		if len(message.MultiContent) > 0 {
			if i == 0 || p.lastRequest.Messages[i-1].Role != llm.RoleTool || !strings.Contains(message.MultiContent[0].Text, "image-call") {
				t.Fatal("tool media lost provenance or tool pairing")
			}
			for _, part := range message.MultiContent {
				if part.ImageURL != nil && part.ImageURL.URL == "data:image/png;base64,aW1hZ2U=" {
					media = true
				}
			}
		}
		if strings.Contains(message.Content, "aW1hZ2U=") {
			t.Fatal("image was flattened into ordinary text")
		}
	}
	if !paired || !media {
		t.Fatal("required image or matching tool result is absent from the model request")
	}
}
