package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/hexagon/hooks"
	"github.com/hexagon-codes/hexagon/internal/util"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

// runCompletionWithRuntime 用统一 runtime 执行一次单轮 LLM 补全。
//
// mws 是注入到该次 run 的 runtime 中间件（来自 agent 的 WithMiddleware）。
//
// 预算语义取决于注入的中间件。本函数每次调用都新建一个 run（全新
// State），因此底层 middleware.Budget 只按单 run 检查 token/墙钟/成本；
// middleware.CostControl 则通过共享 Controller 的 RecordUsageFunc 在 AfterLLM 写入
// 跨 run 累计账。首选 middleware.NewBudgetControl 同时组合这两层，使
// PlanExecute/Reflection 的 planner/step/replan/critique 等独立 run 无需额外
// 跨 run 累加器。调用方/provider 还应在每次真正发起 LLM 请求前，对同一
// Controller 调用 CheckRequest 完成 token/频率预检；该预检不由中间件代替。
func runCompletionWithRuntime(ctx context.Context, provider llm.Provider, runID string, messages []llm.Message, sink agentruntime.EventSink, mws ...agentruntime.Middleware) (*llm.CompletionResponse, error) {
	if provider == nil {
		return nil, fmt.Errorf("LLM provider not configured")
	}
	if runID == "" {
		runID = util.GenerateID("run")
	}

	runner := newCompletionRunner(provider, mws)
	return runCompletionOnRunner(ctx, runner, runID, messages, sink)
}

// newCompletionRunner 构造一个单轮补全用的 runtime runner。
func newCompletionRunner(provider llm.Provider, mws []agentruntime.Middleware) *agentruntime.DefaultRunner {
	return agentruntime.NewRunner(agentruntime.Config{
		ProviderSelector: agentruntime.StaticProviderSelector{
			Provider: provider,
			Name:     provider.Name(),
		},
		DefaultMaxTurns: 1,
		Middleware:      mws,
	})
}

// runCompletionOnRunner 在给定 runner 上执行一次单轮补全。
//
// runner 不持有 per-run 可变状态（每次 RunWithSink 自建 State），故可在 agent 生命周期内
// 复用（见 BaseAgent.completeWithRuntime），无需每次调用重新构造。
func runCompletionOnRunner(ctx context.Context, runner *agentruntime.DefaultRunner, runID string, messages []llm.Message, sink agentruntime.EventSink) (*llm.CompletionResponse, error) {
	result, err := runner.RunWithSink(ctx, agentruntime.Request{
		ID:         runID,
		Messages:   append([]llm.Message(nil), messages...),
		Limits:     agentruntime.Limits{MaxTurns: 1},
		StreamMode: agentruntime.StreamModeEvents,
	}, sink)
	if err != nil {
		return nil, err
	}
	resp := &llm.CompletionResponse{}
	if result != nil {
		resp.Content = result.Content
		resp.Usage = result.Usage
	}
	return resp, nil
}

// completeWithRuntime 用 agent 生命周期内复用的 runner 执行一次单轮补全。
//
// 与逐次调用 runCompletionWithRuntime 等价，但 runner（含 provider 与 middleware 链）
// 只在首次调用时构造、之后复用，消除多 run agent（PlanExecute/Reflection）每次内部
// LLM 调用都重新构造 runner 的开销。
func (a *BaseAgent) completeWithRuntime(ctx context.Context, runID string, messages []llm.Message, sink agentruntime.EventSink) (*llm.CompletionResponse, error) {
	if a.config.LLM == nil {
		return nil, fmt.Errorf("LLM provider not configured")
	}
	if runID == "" {
		runID = util.GenerateID("run")
	}
	a.runnerOnce.Do(func() {
		a.runner = newCompletionRunner(a.config.LLM, a.config.Middleware)
	})
	return runCompletionOnRunner(ctx, a.runner, runID, messages, sink)
}

func runtimeLLMHookSink(runID, providerName string, hookManager *hooks.Manager) agentruntime.EventSink {
	if hookManager == nil {
		return nil
	}
	var llmStart time.Time
	return agentruntime.EventSinkFunc(func(ctx context.Context, event agentruntime.Event) error {
		switch event.Type {
		case agentruntime.EventLLMStarted:
			llmStart = time.Now()
			if v, ok := event.Metadata["provider"].(string); ok && v != "" {
				providerName = v
			}
			var messages []any
			if event.State != nil {
				messages = convertMessagesToAny(event.State.Messages)
			}
			return hookManager.TriggerLLMStart(ctx, &hooks.LLMStartEvent{
				RunID:    runID,
				Provider: providerName,
				Messages: messages,
			})
		case agentruntime.EventLLMCompleted:
			if event.Response == nil {
				return nil
			}
			return hookManager.TriggerLLMEnd(ctx, &hooks.LLMEndEvent{
				RunID:            runID,
				Response:         event.Response.Content,
				PromptTokens:     event.Response.Usage.PromptTokens,
				CompletionTokens: event.Response.Usage.CompletionTokens,
				Duration:         time.Since(llmStart).Milliseconds(),
			})
		}
		return nil
	})
}
