package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/memory"
	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/hexagon/core"
	"github.com/hexagon-codes/hexagon/hooks"
	"github.com/hexagon-codes/hexagon/internal/util"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
)

const defaultReActPrompt = `You are a helpful AI assistant with access to tools.

When you need to use a tool, respond with a tool call. After receiving the tool result, analyze it and continue reasoning.

Think step by step:
1. Understand the user's question
2. Decide if you need to use any tools
3. If yes, call the appropriate tool
4. Analyze the tool result
5. Continue until you have enough information to answer
6. Provide a final answer

Always be helpful, accurate, and concise.`

// ReActAgent 实现 ReAct (Reasoning + Acting) 模式的 Agent
// ReAct 模式让 Agent 交替进行推理和行动，直到完成任务
type ReActAgent struct {
	*BaseAgent
}

// NewReAct 创建 ReAct Agent
func NewReAct(opts ...Option) *ReActAgent {
	base := NewBaseAgent(opts...)

	// 设置默认系统提示词
	if base.config.SystemPrompt == "" {
		base.config.SystemPrompt = defaultReActPrompt
	}

	// 设置默认名称
	if base.config.Name == "Agent" {
		base.config.Name = "ReActAgent"
	}

	return &ReActAgent{BaseAgent: base}
}

// Run 执行 ReAct Agent。
//
// 若经 WithCheckpointer 开启了持久化，本次执行会在每步边界持久化快照；返回的
// Output.Metadata["run_id"] 即本次 run 的标识符，可用于后续 Resume。
func (a *ReActAgent) Run(ctx context.Context, input Input) (Output, error) {
	if a.config.LLM == nil {
		return Output{}, fmt.Errorf("LLM provider not configured")
	}

	runID := util.GenerateID("run")
	startTime := time.Now()
	hookManager := hooks.ManagerFromContext(ctx)

	runner := a.newRunner(runID, hookManager)
	result, err := runner.RunWithSink(ctx, a.newRequest(ctx, input, runID), a.runtimeHookSink(runID, input, startTime, hookManager))
	return a.finishRun(ctx, input, runID, result, err, hookManager)
}

// Resume 从某次执行最近的持久化快照续跑（需经 WithCheckpointer 开启持久化）。
//
// runID 取自先前 Run 返回的 Output.Metadata["run_id"]。若该 run 已完成，Resume 直接
// 返回其最终结果（不重跑）；若中断在中间步，则从该步之后继续。
func (a *ReActAgent) Resume(ctx context.Context, runID string, input Input) (Output, error) {
	if a.config.LLM == nil {
		return Output{}, fmt.Errorf("LLM provider not configured")
	}
	if a.config.Durable == nil {
		return Output{}, fmt.Errorf("agent: 未配置 WithCheckpointer，无法 Resume")
	}

	startTime := time.Now()
	hookManager := hooks.ManagerFromContext(ctx)

	runner := a.newRunner(runID, hookManager)
	result, err := runner.Resume(ctx, runID, a.newRequest(ctx, input, runID), a.runtimeHookSink(runID, input, startTime, hookManager))
	return a.finishRun(ctx, input, runID, result, err, hookManager)
}

// newRunner 按 Agent 配置构造底层 runtime runner（Run/Resume 共用）。
func (a *ReActAgent) newRunner(runID string, hookManager *hooks.Manager) *agentruntime.DefaultRunner {
	return agentruntime.NewRunner(agentruntime.Config{
		ProviderSelector: agentruntime.StaticProviderSelector{
			Provider: a.config.LLM,
			Name:     a.config.LLM.Name(),
		},
		ToolExecutor:    &agentToolExecutor{tools: a.config.Tools, runID: runID, hookManager: hookManager},
		DefaultMaxTurns: a.config.MaxIterations,
		Middleware:      a.config.Middleware,
		Durable:         a.config.Durable,
	})
}

// newRequest 按 Agent 配置构造一次 runtime 请求（Run/Resume 共用）。
func (a *ReActAgent) newRequest(ctx context.Context, input Input, runID string) agentruntime.Request {
	return agentruntime.Request{
		ID:       runID,
		Messages: a.buildInitialMessages(ctx, input),
		Tools:    a.buildToolDefinitions(),
		Limits: agentruntime.Limits{
			MaxTurns: a.config.MaxIterations,
		},
		Strategy: a.config.Strategy,
	}
}

// finishRun 统一收尾：转换输出、写入 run_id、错误钩子、记忆保存（Run/Resume 共用）。
func (a *ReActAgent) finishRun(ctx context.Context, input Input, runID string, result *agentruntime.Result, err error, hookManager *hooks.Manager) (Output, error) {
	if err != nil {
		if hookManager != nil {
			hookManager.TriggerError(ctx, &hooks.ErrorEvent{
				RunID:   runID,
				AgentID: a.ID(),
				Error:   err,
				Phase:   "run",
			})
		}
		// 运行时在部分错误（如 MaxTurns 耗尽）下会回传携带已累积用量/推理/工具记录的
		// 部分结果。此时把它转成 Output 一并返回，便于调用方恢复部分工作与已计费用量；
		// 调用方仍应优先判断 err。无部分结果（result==nil）时保持返回空 Output 的旧契约。
		if result == nil {
			return Output{}, err
		}
		output := outputFromRuntime(result)
		if output.Metadata == nil {
			output.Metadata = map[string]any{}
		}
		output.Metadata["run_id"] = runID
		return output, err
	}

	output := outputFromRuntime(result)
	if output.Metadata == nil {
		output.Metadata = map[string]any{}
	}
	output.Metadata["run_id"] = runID

	// 保存到记忆（保存失败不影响主流程，但通过钩子报告错误）
	if a.config.Memory != nil {
		if err := a.saveToMemory(ctx, input, output); err != nil {
			// 记忆保存失败不应阻止返回成功的输出
			// 通过错误钩子报告，便于监控和调试
			if hookManager != nil {
				hookManager.TriggerError(ctx, &hooks.ErrorEvent{
					RunID:   runID,
					AgentID: a.ID(),
					Error:   err,
					Phase:   "memory_save",
				})
			}
		}
	}

	return output, nil
}

func (a *ReActAgent) runtimeHookSink(runID string, input Input, start time.Time, hookManager *hooks.Manager) agentruntime.EventSink {
	if hookManager == nil {
		return nil
	}
	var llmStart time.Time
	return agentruntime.EventSinkFunc(func(ctx context.Context, event agentruntime.Event) error {
		switch event.Type {
		case agentruntime.EventRunStarted:
			return hookManager.TriggerRunStart(ctx, &hooks.RunStartEvent{
				RunID:   runID,
				AgentID: a.ID(),
				Input:   input,
			})
		case agentruntime.EventLLMStarted:
			llmStart = time.Now()
			return hookManager.TriggerLLMStart(ctx, &hooks.LLMStartEvent{
				RunID:    runID,
				Provider: a.config.LLM.Name(),
				Messages: convertMessagesToAny(event.State.Messages),
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
		case agentruntime.EventRunFinished:
			return hookManager.TriggerRunEnd(ctx, &hooks.RunEndEvent{
				RunID:    runID,
				AgentID:  a.ID(),
				Output:   outputFromRuntime(agentruntimeResultFromState(event.State)),
				Duration: time.Since(start).Milliseconds(),
			})
		case agentruntime.EventRunFailed:
			return hookManager.TriggerError(ctx, &hooks.ErrorEvent{
				RunID:   runID,
				AgentID: a.ID(),
				Error:   event.Error,
				Phase:   "run",
			})
		}
		return nil
	})
}

type agentToolExecutor struct {
	tools       []tool.Tool
	runID       string
	hookManager *hooks.Manager
}

func (e *agentToolExecutor) Execute(ctx context.Context, call llm.ToolCall) (agentruntime.ToolResult, error) {
	var targetTool tool.Tool
	for _, t := range e.tools {
		if t.Name() == call.Name {
			targetTool = t
			break
		}
	}
	if targetTool == nil {
		msg := fmt.Sprintf("Error: tool '%s' not found", call.Name)
		return agentruntime.ToolResult{Content: msg, Error: msg}, nil
	}
	args, err := tool.ParseArgs(call.Arguments)
	if err != nil {
		msg := fmt.Sprintf("Error: failed to parse arguments: %v", err)
		return agentruntime.ToolResult{Content: msg, Error: err.Error()}, nil
	}

	toolID := call.ID
	if toolID == "" {
		toolID = util.GenerateID("tool")
	}
	if e.hookManager != nil {
		e.hookManager.TriggerToolStart(ctx, &hooks.ToolStartEvent{
			RunID:    e.runID,
			ToolName: call.Name,
			ToolID:   toolID,
			Input:    args,
		})
	}
	start := time.Now()
	toolResult, execErr := targetTool.Execute(ctx, args)
	if e.hookManager != nil {
		e.hookManager.TriggerToolEnd(ctx, &hooks.ToolEndEvent{
			RunID:    e.runID,
			ToolName: call.Name,
			ToolID:   toolID,
			Output:   toolResult,
			Duration: time.Since(start).Milliseconds(),
			Error:    execErr,
		})
	}
	if execErr != nil {
		msg := fmt.Sprintf("Error: tool execution failed: %v", execErr)
		return agentruntime.ToolResult{Content: msg, Raw: toolResult, Error: execErr.Error()}, nil
	}
	return agentruntime.ToolResult{Content: formatToolResult(toolResult), Raw: toolResult}, nil
}

func outputFromRuntime(result *agentruntime.Result) Output {
	if result == nil {
		return Output{}
	}
	out := Output{
		Content:    result.Content,
		Usage:      result.Usage,
		Metadata:   result.Metadata,
		StopReason: result.StopReason,
	}
	for _, call := range result.ToolCalls {
		rec := ToolCallRecord{Name: call.Name}
		args, _ := tool.ParseArgs(call.Arguments)
		rec.Arguments = args
		if tr, ok := call.Result.Raw.(tool.Result); ok {
			rec.Result = tr
		} else if call.Result.Error != "" {
			rec.Result = tool.Result{Success: false, Error: call.Result.Error}
		} else {
			rec.Result = tool.NewResult(call.Result.Content)
		}
		out.ToolCalls = append(out.ToolCalls, rec)
	}
	return out
}

func agentruntimeResultFromState(state *agentruntime.State) *agentruntime.Result {
	if state == nil {
		return nil
	}
	stop := agentruntime.StopReasonEndTurn
	if !state.Final {
		stop = agentruntime.StopReasonMaxTurns
	}
	return &agentruntime.Result{
		Content:    state.FinalText,
		Reasoning:  state.Reasoning,
		ToolCalls:  state.ToolCalls,
		Usage:      state.Usage,
		Metadata:   state.Attributes,
		StopReason: stop,
	}
}

// executeToolCallsWithHooks 执行工具调用（带钩子）
func (a *ReActAgent) executeToolCallsWithHooks(ctx context.Context, runID string, calls []llm.ToolCall, hookManager *hooks.Manager) ([]string, []ToolCallRecord, error) {
	results := make([]string, 0, len(calls))
	records := make([]ToolCallRecord, 0, len(calls))

	for _, call := range calls {
		// 查找工具
		var targetTool tool.Tool
		for _, t := range a.config.Tools {
			if t.Name() == call.Name {
				targetTool = t
				break
			}
		}

		if targetTool == nil {
			result := fmt.Sprintf("Error: tool '%s' not found", call.Name)
			results = append(results, result)
			continue
		}

		// 解析参数
		args, err := tool.ParseArgs(call.Arguments)
		if err != nil {
			result := fmt.Sprintf("Error: failed to parse arguments: %v", err)
			results = append(results, result)
			continue
		}

		toolID := util.GenerateID("tool")

		// 触发工具开始钩子
		if hookManager != nil {
			hookManager.TriggerToolStart(ctx, &hooks.ToolStartEvent{
				RunID:    runID,
				ToolName: call.Name,
				ToolID:   toolID,
				Input:    args,
			})
		}

		// 执行工具
		toolStartTime := time.Now()
		toolResult, err := targetTool.Execute(ctx, args)
		toolDuration := time.Since(toolStartTime).Milliseconds()

		// 触发工具结束钩子
		if hookManager != nil {
			hookManager.TriggerToolEnd(ctx, &hooks.ToolEndEvent{
				RunID:    runID,
				ToolName: call.Name,
				ToolID:   toolID,
				Output:   toolResult,
				Duration: toolDuration,
				Error:    err,
			})
		}

		if err != nil {
			result := fmt.Sprintf("Error: tool execution failed: %v", err)
			results = append(results, result)
			continue
		}

		// 记录工具调用
		records = append(records, ToolCallRecord{
			Name:      call.Name,
			Arguments: args,
			Result:    toolResult,
		})

		// 格式化结果
		resultStr := formatToolResult(toolResult)
		results = append(results, resultStr)
	}

	return results, records, nil
}

// convertMessagesToAny 将消息列表转换为 any 切片
func convertMessagesToAny(messages []llm.Message) []any {
	result := make([]any, len(messages))
	for i, m := range messages {
		result[i] = m
	}
	return result
}

// buildInitialMessages 构建初始消息
//
// 参数：
//   - ctx: 上下文，用于记忆查询的超时和取消控制
//   - input: 用户输入
//
// 返回构建好的消息列表
func (a *ReActAgent) buildInitialMessages(ctx context.Context, input Input) []llm.Message {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: a.config.SystemPrompt},
	}

	// 从记忆中获取历史上下文
	if a.config.Memory != nil {
		entries, _ := a.config.Memory.Search(ctx, memory.SearchQuery{
			Limit:     10,
			OrderDesc: true,
		})
		// 按时间正序添加
		for i := len(entries) - 1; i >= 0; i-- {
			entry := entries[i]
			messages = append(messages, llm.Message{
				Role:    llm.Role(entry.Role),
				Content: entry.Content,
			})
		}
	}

	// 添加用户输入
	messages = append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: input.Query,
	})

	return messages
}

// buildToolDefinitions 构建工具定义
func (a *ReActAgent) buildToolDefinitions() []llm.ToolDefinition {
	if len(a.config.Tools) == 0 {
		return nil
	}

	defs := make([]llm.ToolDefinition, len(a.config.Tools))
	for i, t := range a.config.Tools {
		defs[i] = llm.ToolDefinition{
			Type: "function",
			Function: llm.ToolFunctionDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Schema(),
			},
		}
	}
	return defs
}

// formatToolResult 格式化工具结果
// maxToolResultChars 工具结果最大字符数，超过则截断
// 防止单个工具返回（如网页抓取）撑爆 LLM 上下文窗口
const maxToolResultChars = 8000

func formatToolResult(result tool.Result) string {
	if !result.Success {
		return fmt.Sprintf("Error: %s", result.Error)
	}

	var s string
	switch v := result.Output.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			s = fmt.Sprintf("%v", v)
		} else {
			s = string(b)
		}
	}

	return truncateToolResult(s, maxToolResultChars)
}

// truncateToolResult 截断过长的工具结果，保留开头和结尾
func truncateToolResult(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// 保留前 70% + 后 20%，中间用省略标记
	headLen := maxLen * 7 / 10
	tailLen := maxLen * 2 / 10
	return s[:headLen] + fmt.Sprintf("\n\n...[结果已截断，原始 %d 字符，保留前 %d + 后 %d 字符]...\n\n", len(s), headLen, tailLen) + s[len(s)-tailLen:]
}

// saveToMemory 保存到记忆
//
// 将对话记录保存到记忆系统，包括用户输入、工具调用和 Agent 回复。
// 如果保存失败，错误会被返回但不会中断主流程（记忆保存是可选的增强功能）。
//
// 参数：
//   - ctx: 上下文
//   - input: 用户输入
//   - output: Agent 输出
//
// 返回：
//   - 保存过程中遇到的第一个错误，如果全部成功则返回 nil
func (a *ReActAgent) saveToMemory(ctx context.Context, input Input, output Output) error {
	// 保存用户输入
	if err := a.config.Memory.Save(ctx, memory.Entry{
		Role:    "user",
		Content: input.Query,
	}); err != nil {
		return fmt.Errorf("failed to save user input to memory: %w", err)
	}

	// 保存工具调用记录
	if len(output.ToolCalls) > 0 {
		var toolSummary strings.Builder
		for _, tc := range output.ToolCalls {
			fmt.Fprintf(&toolSummary, "Called %s: %s\n", tc.Name, tc.Result.String())
		}
		if err := a.config.Memory.Save(ctx, memory.Entry{
			Role:    "tool",
			Content: toolSummary.String(),
		}); err != nil {
			return fmt.Errorf("failed to save tool calls to memory: %w", err)
		}
	}

	// 保存 Agent 回复
	if err := a.config.Memory.Save(ctx, memory.Entry{
		Role:    "assistant",
		Content: output.Content,
	}); err != nil {
		return fmt.Errorf("failed to save assistant response to memory: %w", err)
	}

	return nil
}

// Invoke 执行 ReAct Agent（实现 Runnable 接口）
func (a *ReActAgent) Invoke(ctx context.Context, input Input, opts ...core.Option) (Output, error) {
	return a.Run(ctx, input)
}

// Stream 流式执行 ReAct Agent
func (a *ReActAgent) Stream(ctx context.Context, input Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	output, err := a.Run(ctx, input)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(output), nil
}

// Batch 批量执行 ReAct Agent
func (a *ReActAgent) Batch(ctx context.Context, inputs []Input, opts ...core.Option) ([]Output, error) {
	results := make([]Output, len(inputs))
	for i, input := range inputs {
		output, err := a.Run(ctx, input)
		if err != nil {
			return nil, err
		}
		results[i] = output
	}
	return results, nil
}

// Collect 收集流式输入并执行
func (a *ReActAgent) Collect(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (Output, error) {
	var zero Output
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		return zero, err
	}
	return a.Run(ctx, collected)
}

// Transform 转换流
func (a *ReActAgent) Transform(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (*stream.StreamReader[Output], error) {
	reader, writer := stream.Pipe[Output](10)
	go func() {
		defer writer.Close()
		for {
			in, err := input.Recv()
			if err != nil {
				return
			}
			result, err := a.Run(ctx, in)
			if err != nil {
				writer.CloseWithError(err)
				return
			}
			writer.Send(result)
		}
	}()
	return reader, nil
}

// BatchStream 批量流式执行
func (a *ReActAgent) BatchStream(ctx context.Context, inputs []Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	results, err := a.Batch(ctx, inputs, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}

// 确保实现了 Agent 接口
var _ Agent = (*ReActAgent)(nil)
