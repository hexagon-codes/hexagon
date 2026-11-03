package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

type failingToolExecutor struct{ calls int }

func (e *failingToolExecutor) Execute(context.Context, llm.ToolCall) (ToolResult, error) {
	e.calls++
	return ToolResult{}, errors.New("tool failed")
}

func TestRunnerToolFailureSinkErrorKeepsRecord(t *testing.T) {
	provider := &fakeProvider{name: "fake", responses: []*llm.CompletionResponse{
		{ToolCalls: []llm.ToolCall{{ID: "tool-1", Name: "fail", Arguments: `{}`}}, FinishReason: "tool_calls"},
		{Content: "done", FinishReason: "stop"},
	}}
	executor := &failingToolExecutor{}
	sinkErr := errors.New("sink write failed")
	runner := NewRunner(Config{
		ProviderSelector: StaticProviderSelector{Provider: provider, Name: "fake", Model: "fake-model"},
		ToolExecutor:     executor,
	})
	result, err := runner.RunWithSink(context.Background(), Request{
		ID:       "tool-failure-sink",
		Messages: []llm.Message{llm.UserMessage("go")},
		Tools:    []llm.ToolDefinition{llm.NewToolDefinition("fail", "fail", &llm.Schema{Type: "object"})},
	}, EventSinkFunc(func(_ context.Context, event Event) error {
		if event.Type == EventToolCallFailed {
			return sinkErr
		}
		return nil
	}))
	if !errors.Is(err, sinkErr) {
		t.Fatalf("error = %v, want sink error", err)
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls)
	}
	if result == nil || len(result.ToolCalls) != 1 {
		t.Fatalf("completed tool record lost: result=%+v", result)
	}
	if result.ToolCalls[0].ID != "tool-1" || result.ToolCalls[0].Result.Status != ToolStatusError {
		t.Fatalf("unexpected record: %+v", result.ToolCalls[0])
	}
}
