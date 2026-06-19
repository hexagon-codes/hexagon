package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hexagon-codes/ai-core/llm"
)

// concurrentMockProvider 是并发安全的 mock provider（原子计数），用于验证共享 runner 的可重入性。
type concurrentMockProvider struct {
	calls atomic.Int64
}

func (p *concurrentMockProvider) Name() string             { return "concurrent-mock" }
func (p *concurrentMockProvider) Models() []llm.ModelInfo  { return []llm.ModelInfo{{Name: "m"}} }
func (p *concurrentMockProvider) CountTokens([]llm.Message) (int, error) { return 1, nil }
func (p *concurrentMockProvider) Stream(context.Context, llm.CompletionRequest) (*llm.Stream, error) {
	return nil, nil
}
func (p *concurrentMockProvider) Complete(_ context.Context, _ llm.CompletionRequest) (*llm.CompletionResponse, error) {
	p.calls.Add(1)
	return &llm.CompletionResponse{Content: "ok", Usage: llm.Usage{TotalTokens: 3}}, nil
}

// TestCompleteWithRuntime_SharedRunnerConcurrent 验证 P1-3 复用的 memoized runner 在并发调用同一
// agent 时可重入、结果正确、且 -race 干净（runner 只构造一次、被并发 RunWithSink 安全共享）。
func TestCompleteWithRuntime_SharedRunnerConcurrent(t *testing.T) {
	provider := &concurrentMockProvider{}
	base := &BaseAgent{config: Config{LLM: provider}}

	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			_, errs[i] = base.completeWithRuntime(
				context.Background(),
				"", // 空 runID → 各自生成，互不串扰
				[]llm.Message{{Role: llm.RoleUser, Content: "hi"}},
				nil,
			)
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("并发调用 %d 失败: %v", i, err)
		}
	}
	if got := provider.calls.Load(); got != n {
		t.Errorf("应有 %d 次 Complete 调用, got %d", n, got)
	}
	if base.runner == nil {
		t.Error("runner 应已 memo 构造")
	}
}

// TestCompleteWithRuntime_RunnerMemoizedOnce 验证 runner 仅构造一次（多次调用复用同一实例）。
func TestCompleteWithRuntime_RunnerMemoizedOnce(t *testing.T) {
	base := &BaseAgent{config: Config{LLM: &concurrentMockProvider{}}}
	ctx := context.Background()
	msg := []llm.Message{{Role: llm.RoleUser, Content: "x"}}

	if _, err := base.completeWithRuntime(ctx, "", msg, nil); err != nil {
		t.Fatal(err)
	}
	first := base.runner
	if _, err := base.completeWithRuntime(ctx, "", msg, nil); err != nil {
		t.Fatal(err)
	}
	if base.runner != first {
		t.Error("runner 应复用同一实例，而非每次重建")
	}
}

// TestCompleteWithRuntime_NoProvider 验证未配置 provider 时返回错误（边界）。
func TestCompleteWithRuntime_NoProvider(t *testing.T) {
	base := &BaseAgent{config: Config{}}
	if _, err := base.completeWithRuntime(context.Background(), "", nil, nil); err == nil {
		t.Error("未配置 provider 应返回错误")
	}
}
