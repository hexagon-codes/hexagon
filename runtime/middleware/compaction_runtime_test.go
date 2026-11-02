package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
	hr "github.com/hexagon-codes/hexagon/runtime"
)

type compactionContractProvider struct {
	name  string
	calls int
	last  llm.CompletionRequest
	usage int
}

func (p *compactionContractProvider) Name() string { return p.name }
func (p *compactionContractProvider) Complete(_ context.Context, request llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.calls++
	p.last = request
	return &llm.CompletionResponse{Content: "candidate summary", Model: p.name, Usage: llm.Usage{TotalTokens: p.usage}}, nil
}
func (*compactionContractProvider) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, errors.New("unused stream")
}

func TestCompactionRuntimeFailureKeepsOriginalInputAndInternalUsage(t *testing.T) {
	main := &compactionContractProvider{name: "main"}
	summary := &compactionContractProvider{name: "summary", usage: 7}
	originalErr := errors.New("summary could not be adopted")
	charged := 0
	compact := Compaction{ShouldCompact: func([]llm.Message) bool { return true }, Compact: func(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
		_, err := llm.InvokeComplete(ctx, summary, llm.CompletionRequest{Model: "summary-model", Messages: messages, MaxTokens: 42}, CompactionPurpose)
		if err != nil {
			return nil, err
		}
		messages[0].Content = "unaccepted summary"
		return messages, originalErr
	}}
	var purpose string
	runner := hr.NewRunner(hr.Config{ProviderSelector: hr.StaticProviderSelector{Provider: main}, Middleware: []hr.Middleware{compact, CostControl{Record: func(model string, usage llm.Usage) error {
		if model != "summary" {
			t.Fatalf("wrong model charged: %s", model)
		}
		charged += usage.TotalTokens
		return nil
	}}}})
	request := hr.Request{ID: "compaction-failure", Messages: []llm.Message{llm.UserMessage("original input")}}
	result, err := runner.RunWithSink(t.Context(), request, hr.EventSinkFunc(func(_ context.Context, event hr.Event) error {
		if event.Type == hr.EventModelAttemptFinished {
			if attempt, ok := event.Payload.(hr.ModelAttempt); ok {
				purpose = attempt.Purpose
			}
		}
		return nil
	}))
	if !errors.Is(err, originalErr) || main.calls != 0 || summary.calls != 1 || summary.last.MaxTokens != 42 || purpose != CompactionPurpose || charged != 7 || result == nil || result.Usage.TotalTokens != 7 || result.StopReason != "" || request.Messages[0].Content != "original input" {
		t.Fatalf("failed compaction lost input/usage: result=%+v main=%d summary=%d charged=%d purpose=%q error=%v", result, main.calls, summary.calls, charged, purpose, err)
	}
}
