package middleware

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	hr "github.com/hexagon-codes/hexagon/runtime"
)

func TestCompactionUsesFullRequestInsteadOfAccumulatedUsage(t *testing.T) {
	called := 0
	c := Compaction{MaxTokens: 1000, Compact: func(_ context.Context, messages []llm.Message) ([]llm.Message, error) {
		called++
		return messages[:1], nil
	}}
	state := &hr.State{Messages: []llm.Message{llm.SystemMessage("system"), llm.UserMessage("short history")}, Usage: llm.Usage{TotalTokens: 1000000}}
	if err := c.BeforeLLM(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Fatal("accumulated usage was used as current context size")
	}
	state.Request.Tools = []llm.ToolDefinition{llm.NewToolDefinition("lookup", strings.Repeat("schema description ", 2000), &llm.Schema{Type: "object", Properties: map[string]*llm.Schema{"query": {Type: "string"}}})}
	if err := c.BeforeLLM(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatal("large tool schema did not trigger compaction")
	}
	if state.Usage.TotalTokens != 1000000 {
		t.Fatal("context estimate changed actual accumulated usage")
	}
}

func TestCompactionRequestPreservesCallbackMetadata(t *testing.T) {
	observer := func(llm.ReasoningReceipt) {}
	request := llm.CompletionRequest{
		Messages: []llm.Message{llm.UserMessage("hello")},
		Metadata: map[string]any{llm.ReasoningReceiptObserverMetadataKey: observer},
	}
	c := Compaction{MaxTokens: 1, Compact: func(_ context.Context, messages []llm.Message) ([]llm.Message, error) {
		return messages, nil
	}}
	work, err := c.Prepare(request, 1)
	if err != nil || work == nil {
		t.Fatalf("prepare=%v err=%v", work, err)
	}
	if _, ok := work.request.Metadata[llm.ReasoningReceiptObserverMetadataKey].(func(llm.ReasoningReceipt)); !ok {
		t.Fatalf("callback metadata was not preserved: %#v", work.request.Metadata)
	}
	request.Metadata["later"] = "mutation"
	if _, ok := work.request.Metadata["later"]; ok {
		t.Fatal("compaction request metadata was not isolated from the caller")
	}
}

func TestCompactionCandidatePreservesAppendedTailAndAliases(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	original := []llm.Message{llm.SystemMessage("system"), llm.UserMessage("old request"), llm.AssistantMessage("old answer")}
	returned := []llm.Message{llm.SystemMessage("system"), llm.AssistantMessage("summary")}
	c := Compaction{MaxTokens: 1, Compact: func(callCtx context.Context, _ []llm.Message) ([]llm.Message, error) {
		close(entered)
		select {
		case <-release:
		case <-callCtx.Done():
			return nil, callCtx.Err()
		}
		return returned, nil
	}}
	work, err := c.Prepare(llm.CompletionRequest{Messages: original}, 7)
	if err != nil || work == nil {
		t.Fatalf("prepare=%v err=%v", work, err)
	}
	type outcome struct {
		candidate *CompactionCandidate
		err       error
	}
	done := make(chan outcome, 1)
	go func() { candidate, err := work.Run(ctx); done <- outcome{candidate, err} }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("summary did not begin")
	}
	tail := []llm.Message{llm.UserMessage("new instruction"), llm.AssistantToolCallMessage("", []llm.ToolCallRef{{ID: "new-call", Name: "lookup", Arguments: `{"query":"new"}`}}), llm.ToolResultMessage("new-call", "new tool result")}
	current := append(hr.CloneOutputMessages(original), hr.CloneOutputMessages(tail)...)
	close(release)
	var result outcome
	select {
	case result = <-done:
	case <-ctx.Done():
		t.Fatal("summary did not finish")
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	returned[1].Content = "mutated source alias"
	adopted, err := AdoptCompaction(result.candidate, 9, current)
	if err != nil {
		t.Fatal(err)
	}
	if adopted[1].Content != "summary" || !reflect.DeepEqual(adopted[2:], tail) {
		t.Fatalf("candidate alias or appended messages were lost: %+v", adopted)
	}
	adopted[0].Content = "mutated result alias"
	if result.candidate.Replacement[0].Content != "system" || current[0].Content != "system" || original[0].Content != "system" {
		t.Fatal("adopted result aliases a candidate or current history")
	}
	// 空水位也表示合法的旧前缀，不因 nil 与空切片的表示差异丢弃新尾部。
	emptyWork, err := (Compaction{ShouldCompact: func([]llm.Message) bool { return true }}).Prepare(llm.CompletionRequest{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	emptyCandidate, err := emptyWork.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	adopted, err = AdoptCompaction(emptyCandidate, 2, tail)
	if err != nil || !reflect.DeepEqual(adopted, tail) {
		t.Fatalf("empty prefix rejected appended messages: %v %v", adopted, err)
	}
}

func TestCompactionCandidateRejectsChangedPrefix(t *testing.T) {
	original := []llm.Message{llm.UserMessage("original request"), llm.AssistantMessage("original answer")}
	c := Compaction{MaxTokens: 1, Compact: func(context.Context, []llm.Message) ([]llm.Message, error) {
		return []llm.Message{llm.AssistantMessage("summary")}, nil
	}}
	work, err := c.Prepare(llm.CompletionRequest{Messages: original}, 5)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := work.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	edited := hr.CloneOutputMessages(original)
	edited[0].Content = "edited instruction"
	for _, test := range []struct {
		name     string
		revision uint64
		messages []llm.Message
	}{{"edited_prefix", 6, edited}, {"recompressed_prefix", 6, []llm.Message{llm.AssistantMessage("newer summary")}}, {"older_revision", 4, original}} {
		t.Run(test.name, func(t *testing.T) {
			before := hr.CloneOutputMessages(test.messages)
			if _, err := AdoptCompaction(candidate, test.revision, test.messages); !errors.Is(err, ErrCompactionConflict) {
				t.Fatalf("stale candidate was accepted: %v", err)
			}
			if !reflect.DeepEqual(test.messages, before) {
				t.Fatal("rejected adoption changed current messages")
			}
		})
	}
}

type summaryProvider struct {
	calls    int
	response *llm.CompletionResponse
	failure  error
}

func (*summaryProvider) Name() string { return "summary-provider" }
func (p *summaryProvider) Complete(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.calls++
	return p.response, p.failure
}
func (*summaryProvider) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, errors.New("stream not used")
}

func TestModelSummaryCompactorUsesUnifiedInvokerAndKeepsFailedUsage(t *testing.T) {
	failure := errors.New("summary failed after provider response")
	usage := llm.Usage{PromptTokens: 12, CompletionTokens: 3, TotalTokens: 15}
	provider := &summaryProvider{response: &llm.CompletionResponse{Content: "partial summary", Usage: usage, UsageReceipt: &usage}, failure: failure}
	temperature := 0.2
	request := llm.CompletionRequest{Model: "summary-model", Messages: []llm.Message{llm.SystemMessage("custom summary prefix")}, Tools: []llm.ToolDefinition{llm.NewToolDefinition("lookup", "original tool definition", &llm.Schema{Type: "object"})}, ToolChoice: "none", ResponseFormat: &llm.ResponseFormat{Type: "text"}, MaxTokens: 42, Temperature: &temperature, Metadata: map[string]any{"custom": "original"}}
	compactor := ModelSummaryCompactor(provider, request, 1)
	request.Tools[0].Function.Description = "mutated caller tool"
	request.Metadata["custom"] = "mutated caller metadata"
	var receipts []llm.Usage
	ctx := llm.WithCompletionInvoker(t.Context(), llm.CompletionInvokerFunc(func(callCtx context.Context, actual llm.CallProvider, prepared llm.CompletionRequest, purpose string) (*llm.CompletionResponse, error) {
		if actual != provider || purpose != CompactionPurpose {
			t.Fatalf("wrong provider or purpose: %v %q", actual, purpose)
		}
		if prepared.Model != "summary-model" || prepared.MaxTokens != 42 || prepared.Temperature == nil || *prepared.Temperature != 0.2 || prepared.ToolChoice != "none" || prepared.ResponseFormat == nil || prepared.Tools[0].Function.Description != "original tool definition" || prepared.Metadata["custom"] != "original" || prepared.Messages[0].Content != "custom summary prefix" {
			t.Fatalf("summary request lost caller configuration: %+v", prepared)
		}
		response, err := actual.Complete(callCtx, prepared)
		if response != nil && response.UsageReceipt != nil {
			receipts = append(receipts, *response.UsageReceipt)
		}
		return response, err
	}))
	original := msgs(5)
	state := &hr.State{Messages: hr.CloneOutputMessages(original), Usage: llm.Usage{TotalTokens: 100}}
	c := Compaction{MaxTokens: 1, Compact: compactor}
	if err := c.BeforeLLM(ctx, state); !errors.Is(err, failure) {
		t.Fatalf("summary failure was hidden: %v", err)
	}
	if !reflect.DeepEqual(state.Messages, original) || state.Usage.TotalTokens != 100 {
		t.Fatal("failed summary changed original history or overwrote actual usage")
	}
	if provider.calls != 1 || len(receipts) != 1 || receipts[0] != usage {
		t.Fatalf("actual failed attempt usage was lost or duplicated: calls=%d receipts=%+v", provider.calls, receipts)
	}
}
