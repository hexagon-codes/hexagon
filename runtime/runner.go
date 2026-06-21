package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
)

// Runner executes agent requests using a unified state machine.
type Runner interface {
	Run(ctx context.Context, req Request) (*Result, error)
	Stream(ctx context.Context, req Request, sink EventSink) (*Result, error)
}

// Config configures DefaultRunner.
type Config struct {
	ProviderSelector ProviderSelector
	ToolExecutor     ToolExecutor
	Middleware       []Middleware
	DefaultMaxTurns  int

	// Durable 可选：开启可持久化/可恢复执行。
	//
	// 非 nil 时，Runner 在每个步边界把执行快照 Save 到该端口（详见 durable.go 的
	// DurableExecution 契约），并支持 Resume 从最近快照续跑。nil 时（默认）完全不
	// 触碰持久化，行为与无此字段时一致。
	//
	// 持久化以 Request.ID 作为 RunID（命名空间）；Request.ID 为空时 Save 静默跳过
	// （无可供恢复的标识符）。Resume 必须由调用方提供同一 RunID。
	Durable DurableExecution
}

// DefaultRunner is the default runtime kernel implementation.
type DefaultRunner struct {
	selector        ProviderSelector
	toolExecutor    ToolExecutor
	middleware      []Middleware
	defaultMaxTurns int
	durable         DurableExecution
}

// NewRunner creates a runtime runner.
func NewRunner(cfg Config) *DefaultRunner {
	maxTurns := cfg.DefaultMaxTurns
	if maxTurns <= 0 {
		maxTurns = 5
	}
	return &DefaultRunner{
		selector:        cfg.ProviderSelector,
		toolExecutor:    cfg.ToolExecutor,
		middleware:      append([]Middleware(nil), cfg.Middleware...),
		defaultMaxTurns: maxTurns,
		durable:         cfg.Durable,
	}
}

// Run executes a request and aggregates the final result.
func (r *DefaultRunner) Run(ctx context.Context, req Request) (*Result, error) {
	req.StreamMode = StreamModeOff
	return r.RunWithSink(ctx, req, nil)
}

// RunWithSink executes a request and also emits events to the supplied sink.
func (r *DefaultRunner) RunWithSink(ctx context.Context, req Request, sink EventSink) (*Result, error) {
	if req.StreamMode == "" {
		req.StreamMode = StreamModeEvents
	}
	return r.runStateMachine(ctx, req, sink)
}

// Stream executes the same state machine while emitting events to sink.
func (r *DefaultRunner) Stream(ctx context.Context, req Request, sink EventSink) (*Result, error) {
	if req.StreamMode == "" || req.StreamMode == StreamModeOff {
		req.StreamMode = StreamModeTokens
	}
	return r.runStateMachine(ctx, req, sink)
}

func (r *DefaultRunner) runStateMachine(ctx context.Context, req Request, sink EventSink) (*Result, error) {
	return r.execute(ctx, req, sink, nil, 0, nil)
}

// Resume 从某次执行最近的持久化快照续跑（需在 Config.Durable 配置持久化端口）。
//
// 行为：
//   - 未配置 Durable → ErrNoDurable。
//   - runID 无任何快照 → ErrNoSnapshot。
//   - 命中的快照已是终态（Final）→ 该次执行早已完成，直接返回其最终结果，不重跑。
//   - 否则用快照重建 State，从"快照步号 + 1"继续状态机；req 提供 provider 选择 /
//     工具 / 策略 / 限额等运行配置（消息历史以快照为准）。
func (r *DefaultRunner) Resume(ctx context.Context, runID string, req Request, sink EventSink) (*Result, error) {
	if r.durable == nil {
		return nil, ErrNoDurable
	}
	snap, ok, err := r.durable.Load(ctx, runID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoSnapshot
	}
	if snap.Final {
		return snapshotResult(snap), nil
	}
	if req.StreamMode == "" {
		req.StreamMode = StreamModeEvents
	}
	// 步内进度/意图快照（Pending 非空）= 该步工具已发起但未确认全部完成（崩溃窗口）。
	if len(snap.Pending) > 0 {
		// 已完成工具（结果已在快照 ToolCalls）按 ID 跳过；仅「在途未完成的 Unsafe 工具」
		// 才 fail-closed —— 把 fail-closed 窗口从「整步含任一 Unsafe」收窄到「真正在途的」。
		done := make(map[string]bool, len(snap.ToolCalls))
		for _, rec := range snap.ToolCalls {
			if rec.ID != "" {
				done[rec.ID] = true
			}
		}
		if UnsafeNotDone(snap.Pending, done) {
			return nil, ErrUnsafeReplay
		}
		// 精确续跑：补跑未完成的工具（execute 在进入循环前完成该步），再从下一步继续。
		return r.execute(ctx, req, sink, snap.RestoreState(), snap.Step+1, snap.Pending)
	}
	return r.execute(ctx, req, sink, snap.RestoreState(), snap.Step+1, nil)
}

// execute 是状态机核心：seed 为 nil 时从 req 全新开始（startTurn=0）；seed 非 nil 时
// 从恢复出的 State 续跑（startTurn=快照步号+1）。fresh 路径行为与重构前完全一致。
func (r *DefaultRunner) execute(ctx context.Context, req Request, sink EventSink, seed *State, startTurn int, resumePending []PendingTool) (*Result, error) {
	if r.selector == nil {
		return nil, ErrNoProvider
	}
	emitter := newRunEmitter(req, sink)
	strategy := strategyOrDefault(req.Strategy)

	var state *State
	if seed != nil {
		// resume：消息历史已含 system prefix（首跑时已 prepend 并随快照持久化），不重复注入。
		state = seed
		state.Request = req
		state.emit = emitter.emit
	} else {
		state = &State{
			Request:    req,
			Messages:   append([]llm.Message(nil), req.Messages...),
			Attributes: map[string]any{},
			emit:       emitter.emit,
		}
		if prefix := strings.TrimSpace(strategy.BuildSystemPrefix(ctx, req)); prefix != "" {
			state.Messages = prependSystemPrefix(state.Messages, prefix)
		}
	}

	selection, err := r.selector.Select(ctx, req)
	if err != nil {
		return nil, err
	}
	if selection.Provider == nil {
		return nil, ErrNoProvider
	}
	state.Attributes["provider"] = selection.Name
	state.Attributes["model"] = selection.Model

	if err := emitter.emit(ctx, Event{Type: EventRunStarted, State: state}); err != nil {
		return nil, err
	}
	if err := emitter.emit(ctx, Event{
		Type:  EventProviderSelected,
		State: state,
		Metadata: map[string]any{
			"provider": selection.Name,
			"model":    selection.Model,
		},
	}); err != nil {
		return nil, err
	}

	maxTurns := req.Limits.MaxTurns
	if maxTurns <= 0 {
		maxTurns = r.defaultMaxTurns
	}

	// runID 作为持久化命名空间（仅 Durable 已配置且 Request.ID 非空时生效）。
	runID := requestID(req)

	// 续跑步内中断：在进入主循环前，先精确补跑被中断步的未完成工具（按 ID 跳过已完成），
	// 完成该步后由下方循环从 startTurn（=中断步+1）继续。主循环本身不受影响。
	if len(resumePending) > 0 {
		var remaining []llm.ToolCall
		for _, p := range resumePending {
			remaining = append(remaining, p.Call)
		}
		// 续跑补跑：executeToolCalls 内部按 ID 跳过已完成的，故可传全部 pending 调用。
		if err := r.executeToolCalls(ctx, state, remaining, emitter, runID, resumePending, true); err != nil {
			_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: err, RuntimeError: runtimeError("resume_tool_execution", err)})
			return nil, err
		}
		if err := strategy.AfterLLM(ctx, state); err != nil {
			_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: err, RuntimeError: runtimeError("strategy_after_llm", err)})
			return nil, err
		}
		// 被中断步在此完成 —— 落完成快照（同步号覆盖进度/意图快照，Pending 清空）。
		if err := r.saveSnapshot(ctx, state, runID, emitter); err != nil {
			return nil, err
		}
	}

	for state.Turn = startTurn; state.Turn < maxTurns; state.Turn++ {
		if err := ctx.Err(); err != nil {
			_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: err, RuntimeError: runtimeError("context_canceled", err)})
			return nil, err
		}
		if err := strategy.BeforeTurn(ctx, state); err != nil {
			_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: err, RuntimeError: runtimeError("strategy_before_turn", err)})
			return nil, err
		}
		if err := runBeforeLLM(ctx, r.middleware, state); err != nil {
			_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: err, RuntimeError: runtimeError("middleware_before_llm", err)})
			return nil, err
		}

		// 每轮调 LLM 前 root-level sanitize：剥离孤立 tool message（找不到对应
		// assistant.ToolCalls 的 tool result）。这是 host-agnostic 防御 —— 即使
		// 上游历史回放 / session 反序列化 / context 压缩 / fallback 切 provider
		// 导致 ToolCalls 字段丢失，本层也能产出契约级合规序列发给所有 provider
		// （Anthropic 直连尤其依赖此层，它自己 buildRequestBody 不做 sanitize）。
		// 参考 ai-core/llm/openai 内 BUG-20260523_* 测试套件还原的具体故障模式。
		// cron_context 守卫：cron 相关上下文（解析/创建/编辑 cron）一律禁 tools，
		// 从协议层根除 LLM tool_call 失败导致的 tool_use_id 链路 400 bug。
		// 上游 host（hexclaw cron handlers）在 Metadata 里打 cron_context: true 即生效。
		effectiveTools := req.Tools
		if isCronContext(req.Metadata) {
			effectiveTools = nil
		}
		callReq := llm.CompletionRequest{
			Model:    selection.Model,
			Messages: sanitizeToolCallSequence(state.Messages),
			Tools:    effectiveTools,
			Metadata: req.Metadata,
		}
		if err := emitter.emit(ctx, Event{
			Type:  EventLLMStarted,
			State: state,
			Metadata: map[string]any{
				"provider": selection.Name,
				"model":    selection.Model,
				"stream":   req.StreamMode == StreamModeTokens,
			},
		}); err != nil {
			return nil, err
		}
		resp, err := r.callProvider(ctx, req, selection, callReq, state, emitter)
		if err != nil {
			next, fbErr := r.selector.Fallback(ctx, selection, err)
			if fbErr == nil && next.Provider != nil {
				_ = emitter.emit(ctx, Event{
					Type:         EventProviderFallback,
					State:        state,
					Error:        err,
					RuntimeError: runtimeError("provider_call_failed", err),
					Metadata: map[string]any{
						"from": selection.Name,
						"to":   next.Name,
					},
				})
				selection = next
				state.Attributes["provider"] = selection.Name
				state.Attributes["model"] = selection.Model
				callReq.Model = selection.Model
				resp, err = r.callProvider(ctx, req, selection, callReq, state, emitter)
			}
		}
		if err != nil {
			runErr := fmt.Errorf("llm complete: %w", err)
			_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: runErr, RuntimeError: runtimeError("llm_complete", runErr)})
			return nil, runErr
		}

		state.AddUsage(resp.Usage)
		if err := runAfterLLM(ctx, r.middleware, state, resp); err != nil {
			_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: err, RuntimeError: runtimeError("middleware_after_llm", err)})
			return nil, err
		}
		if err := emitter.emit(ctx, Event{Type: EventLLMCompleted, State: state, Response: resp}); err != nil {
			return nil, err
		}

		if len(resp.ToolCalls) == 0 {
			state.Final = true
			state.FinalText = resp.Content
			if err := strategy.AfterLLM(ctx, state); err != nil {
				_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: err, RuntimeError: runtimeError("strategy_after_llm", err)})
				return nil, err
			}
			break
		}

		state.Messages = append(state.Messages, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: toolCallRefs(resp.ToolCalls),
		})

		// Durable exactly-once：工具执行**前**先落「意图快照」（含完整 pending 工具调用
		// 与重放安全性，消息已含本轮 assistant 工具调用消息）。崩溃后 Resume 据此精确
		// 续跑：按 ID 跳过已完成工具、补跑未完成工具，仅对在途 Unsafe 工具 fail-closed。
		// 步正常完成时下方完成快照以同步号覆盖它。
		pending := classifyPending(r.toolExecutor, resp.ToolCalls)
		if err := r.savePendingProgress(ctx, state, runID, pending, emitter); err != nil {
			return nil, err
		}

		if err := r.executeToolCalls(ctx, state, resp.ToolCalls, emitter, runID, pending, false); err != nil {
			_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: err, RuntimeError: runtimeError("tool_execution", err)})
			return nil, err
		}
		if err := strategy.AfterLLM(ctx, state); err != nil {
			_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: err, RuntimeError: runtimeError("strategy_after_llm", err)})
			return nil, err
		}
		// 步边界持久化（本步工具已执行、未终态）—— 供进程中断后从此步之后续跑。
		if err := r.saveSnapshot(ctx, state, runID, emitter); err != nil {
			return nil, err
		}
		if !strategy.ShouldContinue(ctx, state) {
			break
		}
	}

	if !state.Final {
		err := ErrMaxTurns
		_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: err, RuntimeError: runtimeError("max_turns", err)})
		// 返回携带已累积用量/推理/工具记录的部分结果：MaxTurns 耗尽时这些工作（含已
		// 计费的 token）确实发生了，调用方应能据此恢复部分结果，而不是被丢弃为 nil。
		return stateResult(state), err
	}
	if err := strategy.Finalize(ctx, state); err != nil {
		_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: err, RuntimeError: runtimeError("strategy_finalize", err)})
		return nil, err
	}
	if err := runFinalize(ctx, r.middleware, state); err != nil {
		_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: err, RuntimeError: runtimeError("middleware_finalize", err)})
		return nil, err
	}
	// 终态持久化（Final=true，含 Finalize/middleware 后处理后的最终结果）—— Resume
	// 命中该快照时直接返回结果、不重跑。
	if err := r.saveSnapshot(ctx, state, runID, emitter); err != nil {
		return nil, err
	}
	result := stateResult(state)
	if err := emitter.emit(ctx, Event{Type: EventRunFinished, State: state, Payload: result}); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *DefaultRunner) callProvider(ctx context.Context, req Request, selection ProviderSelection, callReq llm.CompletionRequest, state *State, emitter *runEmitter) (*llm.CompletionResponse, error) {
	if req.StreamMode != StreamModeTokens {
		return selection.Provider.Complete(ctx, callReq)
	}
	stream, err := selection.Provider.Stream(ctx, callReq)
	if err != nil {
		return nil, err
	}
	if stream == nil {
		return nil, ErrNilStream
	}
	defer stream.Close()
	for chunk := range stream.Chunks() {
		if chunk == nil {
			continue
		}
		if chunk.Reasoning != "" {
			state.Reasoning += chunk.Reasoning
		}
		if err := emitter.emit(ctx, Event{
			Type:  EventLLMChunk,
			State: state,
			Chunk: chunk,
			Metadata: map[string]any{
				"provider": selection.Name,
				"model":    selection.Model,
			},
		}); err != nil {
			return nil, err
		}
	}
	result := stream.Result()
	if err := streamError(stream); err != nil {
		return nil, fmt.Errorf("stream read: %w", err)
	}
	if result == nil {
		return &llm.CompletionResponse{Model: selection.Model}, nil
	}
	return &llm.CompletionResponse{
		ID:           result.ID,
		Model:        result.Model,
		Content:      result.Content,
		ToolCalls:    result.ToolCalls,
		Usage:        result.Usage,
		FinishReason: result.FinishReason,
	}, nil
}

func streamError(stream *llm.Stream) error {
	if stream == nil {
		return nil
	}
	select {
	case err := <-stream.Errors():
		return err
	default:
		return nil
	}
}

func (r *DefaultRunner) executeToolCalls(ctx context.Context, state *State, calls []llm.ToolCall, emitter *runEmitter, runID string, pending []PendingTool, resuming bool) error {
	// per-tool 幂等去重只在「续跑补跑某个被中断步」时生效：该步崩溃前已完成的工具其
	// 结果已记录在快照的 state.ToolCalls 里，按 ID 跳过、不重复执行（含副作用）。
	//
	// 正常热路径（resuming=false）一律不跨 turn 去重：provider 可能在后续 turn 复用
	// 同一个 tool-call ID（廉价/有 bug 的模型确实会），那是新 turn 的全新 tool_call，
	// 必须执行并补上配对的 tool result —— 否则该 turn 的 assistant tool_call 没有
	// 配对结果，破坏下一次请求的 tool_call/result 配对契约。
	done := make(map[string]bool)
	if resuming {
		for _, rec := range state.ToolCalls {
			if rec.ID != "" {
				done[rec.ID] = true
			}
		}
	}
	for _, call := range calls {
		if call.ID != "" && done[call.ID] {
			continue // 续跑：该工具已在中断步完成，跳过（per-tool exactly-once）
		}
		if err := runBeforeTool(ctx, r.middleware, state, call); err != nil {
			return err
		}
		if err := emitter.emit(ctx, Event{Type: EventToolCallStarted, State: state, ToolCall: &call}); err != nil {
			return err
		}
		var result ToolResult
		var err error
		if r.toolExecutor == nil {
			result = ToolResult{Content: fmt.Sprintf("Error: tool %q not available", call.Name), Error: "tool executor not configured"}
		} else {
			result, err = r.toolExecutor.Execute(ctx, call)
			if err != nil {
				result = ToolResult{Content: fmt.Sprintf("Error: %v", err), Error: err.Error()}
				if emitErr := emitter.emit(ctx, Event{Type: EventToolCallFailed, State: state, ToolCall: &call, ToolResult: &result, Error: err, RuntimeError: runtimeError("tool_call_failed", err)}); emitErr != nil {
					return emitErr
				}
			}
		}
		record := ToolCallRecord{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
			Result:    result,
		}
		state.ToolCalls = append(state.ToolCalls, record)
		state.Messages = append(state.Messages, llm.Message{
			Role:       llm.RoleTool,
			Content:    result.Content,
			ToolCallID: call.ID,
		})
		// 记录本次已产出结果的 ID，使同一批内重复出现的同 ID 调用不再重复执行
		// （同一 tool_call ID 只需一个配对结果）。
		if call.ID != "" {
			done[call.ID] = true
		}
		if err := runAfterTool(ctx, r.middleware, state, call, result); err != nil {
			return err
		}
		if err := emitter.emit(ctx, Event{Type: EventToolCallCompleted, State: state, ToolCall: &call, ToolResult: &result}); err != nil {
			return err
		}
		// 逐工具落进度快照（Durable）：记录本工具已完成，使崩溃后 Resume 能跳过它。
		if err := r.savePendingProgress(ctx, state, runID, pending, emitter); err != nil {
			return err
		}
	}
	return nil
}

type runEmitter struct {
	req  Request
	sink EventSink
	seq  int64
}

func newRunEmitter(req Request, sink EventSink) *runEmitter {
	return &runEmitter{req: req, sink: sink}
}

func (e *runEmitter) emit(ctx context.Context, event Event) error {
	e.seq++
	event.Version = EventVersionV1
	event.Sequence = e.seq
	event.Timestamp = time.Now()
	event.RunID = e.req.ID
	event.RequestID = requestID(e.req)
	if event.State != nil {
		event.Turn = event.State.Turn
		event.StateSummary = StateSummary{
			Turn:      event.State.Turn,
			Final:     event.State.Final,
			Messages:  len(event.State.Messages),
			ToolCalls: len(event.State.ToolCalls),
		}
	}
	return emit(ctx, e.sink, event)
}

func requestID(req Request) string {
	if req.ID != "" {
		return req.ID
	}
	if req.Metadata != nil {
		if v, ok := req.Metadata["request_id"].(string); ok {
			return v
		}
	}
	return ""
}

func stateResult(state *State) *Result {
	if state == nil {
		return nil
	}
	return &Result{
		Content:   state.FinalText,
		Reasoning: state.Reasoning,
		ToolCalls: append([]ToolCallRecord(nil), state.ToolCalls...),
		Usage:     state.Usage,
		Metadata:  state.Attributes,
	}
}

// saveSnapshot 在步边界持久化执行快照。仅当 Config.Durable 已配置且 runID 非空时生效。
//
// 持久化失败按 fatal 处理并上报 —— 调用方已显式 opt-in 持久化，Save 失败意味着该步
// 不可恢复，与其它步边界操作（middleware/strategy 失败）一致地中断执行，避免静默
// 产出"看似成功却无法 Resume"的运行。
func (r *DefaultRunner) saveSnapshot(ctx context.Context, state *State, runID string, emitter *runEmitter) error {
	if r.durable == nil || runID == "" {
		return nil
	}
	if err := r.durable.Save(ctx, SnapshotState(state, runID)); err != nil {
		runErr := fmt.Errorf("durable save: %w", err)
		_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: runErr, RuntimeError: runtimeError("durable_save", runErr)})
		return runErr
	}
	return nil
}

// classifyPending 把一组待执行工具调用按重放安全性分类。
//
// 执行器实现 SideEffectClassifier 时按其声明，否则一律按 SideEffectUnsafe（保守默认）。
func classifyPending(exec ToolExecutor, calls []llm.ToolCall) []PendingTool {
	classifier, _ := exec.(SideEffectClassifier)
	out := make([]PendingTool, len(calls))
	for i, c := range calls {
		se := SideEffectUnsafe
		if classifier != nil {
			se = classifier.SideEffectOf(c)
		}
		out[i] = PendingTool{Call: c, SideEffect: se}
	}
	return out
}

// savePendingProgress 持久化「步内进度快照」：当前消息/工具结果 + 仍登记的 pending
// 列表。逐工具完成后调用，使崩溃后 Resume 能按 ToolCalls 里已记录的 ID 跳过已完成
// 工具，只补跑未完成的（per-tool exactly-once）。仅 Durable + runID 非空时生效。
func (r *DefaultRunner) savePendingProgress(ctx context.Context, state *State, runID string, pending []PendingTool, emitter *runEmitter) error {
	if r.durable == nil || runID == "" {
		return nil
	}
	snap := SnapshotState(state, runID)
	snap.Pending = pending
	if err := r.durable.Save(ctx, snap); err != nil {
		runErr := fmt.Errorf("durable save progress: %w", err)
		_ = emitter.emit(ctx, Event{Type: EventRunFailed, State: state, Error: runErr, RuntimeError: runtimeError("durable_save_progress", runErr)})
		return runErr
	}
	return nil
}


// snapshotResult 由终态快照重建最终结果（Resume 命中 Final 快照时返回，不重跑）。
func snapshotResult(snap Snapshot) *Result {
	return &Result{
		Content:   snap.FinalText,
		ToolCalls: append([]ToolCallRecord(nil), snap.ToolCalls...),
		Usage:     snap.Usage,
	}
}

func prependSystemPrefix(messages []llm.Message, prefix string) []llm.Message {
	if len(messages) > 0 && messages[0].Role == llm.RoleSystem {
		messages[0].Content = prefix + "\n" + messages[0].Content
		return messages
	}
	return append([]llm.Message{{Role: llm.RoleSystem, Content: prefix}}, messages...)
}

// isCronContext 判断当前 LLM 调用是否处于 cron 上下文（解析/创建/编辑 cron）。
//
// 约定：host 在 Request.Metadata 里塞 cron_context: true（或 "true" 字符串）即视为
// cron 上下文。本层据此强制 tools=nil，从协议层根除 tool_use_id 链路 400 bug —
// cron 解析永远不需要 tool_call，统一走 response_format=json 或纯文字。
func isCronContext(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	v, ok := metadata["cron_context"]
	if !ok {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1"
	}
	return false
}

func toolCallRefs(calls []llm.ToolCall) []llm.ToolCallRef {
	refs := make([]llm.ToolCallRef, 0, len(calls))
	for _, c := range calls {
		refs = append(refs, llm.ToolCallRef{
			ID:        c.ID,
			Name:      c.Name,
			Arguments: c.Arguments,
		})
	}
	return refs
}

// sanitizeToolCallSequence 剥离孤立 tool message —— 即 ToolCallID 找不到对应
// 前序 assistant.ToolCalls[].ID 的 tool result。这是 runtime 层的契约级防御，
// 对所有下游 provider 生效（OpenAI 兼容 / Anthropic 直连 / Gemini / 自研网关）。
//
// 触发场景（无论 host 是谁都可能踩）：
//   - session 历史从 DB 反序列化时 ToolCalls 字段丢失，下次加载只剩 tool message
//   - context 压缩误剥离 assistant tool_call message，留下孤立 tool
//   - 多 provider failover 切换后老 turn 的 tool message 还在 state.Messages 里
//   - 网关在 OpenAI ↔ Anthropic 翻译时 race 丢失 assistant tool_use
//
// 自愈契约：
//   - 维护"当前可见 tool_call_id 集合"，每见到 assistant.ToolCalls 累加 ID
//   - 每条 tool message 的 ToolCallID 必须在集合内，否则**丢弃**
//   - assistant message 自身（含 ToolCalls）无条件保留——它是 producer
//   - user/system message 不重置集合（同会话内 tool_use ID 跨多轮仍可被引用）
//
// 纯函数无副作用，不改原 slice。
func sanitizeToolCallSequence(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return messages
	}
	known := make(map[string]struct{})
	out := make([]llm.Message, 0, len(messages))
	for _, m := range messages {
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					known[tc.ID] = struct{}{}
				}
			}
			out = append(out, m)
			continue
		}
		if m.Role == llm.RoleTool {
			if m.ToolCallID == "" {
				continue
			}
			if _, ok := known[m.ToolCallID]; !ok {
				continue
			}
			out = append(out, m)
			continue
		}
		out = append(out, m)
	}
	return out
}
