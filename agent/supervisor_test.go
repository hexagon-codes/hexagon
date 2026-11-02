package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// ============== AgentAsTool 测试 ==============

func TestAgentAsTool_Name(t *testing.T) {
	ag := newMockAgent("researcher", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "research"}, nil
	})
	at := AgentAsTool(ag)

	if at.Name() != "agent_researcher" {
		t.Fatalf("expected 'agent_researcher', got %q", at.Name())
	}
}

func TestAgentAsTool_Description(t *testing.T) {
	ag := newMockAgent("writer", func(ctx context.Context, input Input) (Output, error) {
		return Output{}, nil
	})
	at := AgentAsTool(ag)

	desc := at.Description()
	if desc == "" {
		t.Fatal("expected non-empty description")
	}
}

func TestAgentAsTool_Execute(t *testing.T) {
	ag := newMockAgent("echo", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "echo: " + input.Query}, nil
	})
	at := AgentAsTool(ag)

	result, err := at.Execute(context.Background(), map[string]any{
		"message": "hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatal("expected success")
	}
	child, ok := result.Output.(ChildRunResult)
	if !ok || child.Child.Output.Content != "echo: hello world" {
		t.Fatalf("expected 'echo: hello world', got %v", result.Output)
	}
}

func TestAgentAsTool_ExecuteError(t *testing.T) {
	ag := newMockAgent("failing", func(ctx context.Context, input Input) (Output, error) {
		return Output{}, fmt.Errorf("agent failed")
	})
	at := AgentAsTool(ag)

	result, err := at.Execute(context.Background(), map[string]any{
		"message": "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure result")
	}
}

func TestAgentAsTool_Validate(t *testing.T) {
	ag := newMockAgent("test", nil)
	at := AgentAsTool(ag)

	err := at.Validate(map[string]any{"message": "ok"})
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	err = at.Validate(map[string]any{})
	if err == nil {
		t.Fatal("expected validation error for missing message")
	}
}

func TestAgentAsTool_Schema(t *testing.T) {
	ag := newMockAgent("test", nil)
	at := AgentAsTool(ag)

	s := at.Schema()
	if s == nil {
		t.Fatal("expected non-nil schema")
	}
}

// ============== SupervisorAgent 测试 ==============

// svMockLLM 用于 Supervisor 测试的 Mock LLM
type svMockLLM struct {
	completeFn func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error)
}

func (m *svMockLLM) Name() string            { return "sv-mock" }
func (m *svMockLLM) Models() []llm.ModelInfo { return nil }
func (m *svMockLLM) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	if m.completeFn != nil {
		return m.completeFn(ctx, req)
	}
	return &llm.CompletionResponse{Content: "mock"}, nil
}
func (m *svMockLLM) Stream(ctx context.Context, req llm.CompletionRequest) (*llm.Stream, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *svMockLLM) CountTokens(messages []llm.Message) (int, error) { return 0, nil }
func (m *svMockLLM) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, nil
}
func (m *svMockLLM) EmbedWithModel(ctx context.Context, model string, texts []string) ([][]float32, error) {
	return nil, nil
}

// svMockAgent 用于 Supervisor 测试的 Mock Agent（支持 LLM）
type svMockAgent struct {
	mockAgent
	llmProvider llm.Provider
}

func newSvMockAgent(name string, llmProvider llm.Provider, runFn func(context.Context, Input) (Output, error)) *svMockAgent {
	return &svMockAgent{
		mockAgent: mockAgent{
			id:          "sv-" + name,
			name:        name,
			description: "SV mock: " + name,
			runFunc:     runFn,
		},
		llmProvider: llmProvider,
	}
}

func (m *svMockAgent) LLM() llm.Provider { return m.llmProvider }

func TestSupervisor_Creation(t *testing.T) {
	manager := newSvMockAgent("manager", &svMockLLM{}, nil)
	worker1 := newMockAgent("worker1", nil)
	worker2 := newMockAgent("worker2", nil)

	sv := NewSupervisor("test-sv", manager, []Agent{worker1, worker2})

	if sv.Name() != "test-sv" {
		t.Fatalf("expected name 'test-sv', got %q", sv.Name())
	}
	if sv.ID() == "" {
		t.Fatal("expected non-empty ID")
	}
	if sv.maxRounds != 10 {
		t.Fatalf("expected default maxRounds=10, got %d", sv.maxRounds)
	}
}

func TestSupervisor_WithSupervisorRounds(t *testing.T) {
	manager := newSvMockAgent("manager", &svMockLLM{}, nil)
	sv := NewSupervisor("test", manager, nil, WithSupervisorRounds(5))

	if sv.maxRounds != 5 {
		t.Fatalf("expected maxRounds=5, got %d", sv.maxRounds)
	}
}

func TestSupervisor_NoLLM(t *testing.T) {
	manager := newSvMockAgent("manager", nil, nil)

	sv := NewSupervisor("test", manager, nil)

	_, err := sv.Run(context.Background(), Input{Query: "test"})
	if err == nil {
		t.Fatal("expected error for nil LLM")
	}
}

func TestSupervisor_DirectAnswer(t *testing.T) {
	// 模拟 manager LLM 直接给出回答（无 tool call）
	mockLLM := &svMockLLM{
		completeFn: func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
			return &llm.CompletionResponse{
				Content: "The answer is 42",
			}, nil
		},
	}

	manager := newSvMockAgent("manager", mockLLM, nil)
	sv := NewSupervisor("test", manager, nil)

	output, err := sv.Run(context.Background(), Input{Query: "what is the answer?"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Content != "The answer is 42" {
		t.Fatalf("expected 'The answer is 42', got %q", output.Content)
	}
	if output.Metadata["agent_type"] != "supervisor" {
		t.Fatal("expected agent_type=supervisor in metadata")
	}
}

func TestSupervisor_DelegateToWorker(t *testing.T) {
	callCount := 0

	// Manager LLM: 第一次调用工具，第二次直接回答
	mockLLM := &svMockLLM{
		completeFn: func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
			callCount++
			if callCount == 1 {
				return &llm.CompletionResponse{
					Content: "Let me delegate this",
					ToolCalls: []llm.ToolCall{
						{
							ID:        "call-1",
							Name:      "agent_worker1",
							Arguments: `{"message":"research AI"}`,
						},
					},
				}, nil
			}
			return &llm.CompletionResponse{
				Content: "Based on the research: AI is great",
			}, nil
		},
	}

	manager := newSvMockAgent("manager", mockLLM, nil)
	worker1 := newMockAgent("worker1", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "AI research results"}, nil
	})

	sv := NewSupervisor("test-sv", manager, []Agent{worker1})

	output, err := sv.Run(context.Background(), Input{Query: "tell me about AI"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Content != "Based on the research: AI is great" {
		t.Fatalf("unexpected content: %q", output.Content)
	}
	if len(output.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(output.ToolCalls))
	}
	if output.Metadata["rounds"] != 2 {
		t.Fatalf("expected 2 rounds, got %v", output.Metadata["rounds"])
	}
}

func TestSupervisor_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	manager := newSvMockAgent("manager", &svMockLLM{}, nil)
	sv := NewSupervisor("test", manager, nil)

	_, err := sv.Run(ctx, Input{Query: "test"})
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestSupervisor_Invoke(t *testing.T) {
	mockLLM := &svMockLLM{
		completeFn: func(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
			return &llm.CompletionResponse{Content: "ok"}, nil
		},
	}
	manager := newSvMockAgent("manager", mockLLM, nil)
	sv := NewSupervisor("test", manager, nil)

	output, err := sv.Invoke(context.Background(), Input{Query: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Content != "ok" {
		t.Fatalf("expected 'ok', got %q", output.Content)
	}
}

func TestSupervisor_AgentInterface(t *testing.T) {
	var _ Agent = (*SupervisorAgent)(nil)
}
