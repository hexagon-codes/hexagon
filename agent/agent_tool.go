package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/schema"
	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/hexagon/agent/artifact"
	"github.com/hexagon-codes/hexagon/internal/util"
	"github.com/hexagon-codes/hexagon/interrupt"
	agentruntime "github.com/hexagon-codes/hexagon/runtime"
	"github.com/hexagon-codes/toolkit/util/reflectx"
)

// AgentTool 把一个 Agent 适配为 tool.Tool，使任意 agent loop（ReAct / PlanExecute /
// Reflection 等）都能把另一个 Agent 当作工具来调用，从而构成「递归子链」——
// agent 调 agent、loop 套 loop。
//
// 设计意图（A10 统一 agent loop 的组合落点）：与其把三种 agent 强行合并进同一个
// 回合循环（会丢失 PlanExecute 的规划/重计划、Reflection 的自检等各自特性），不如
// 让它们各自保留循环、通过统一的「agent 即工具」基元相互嵌套。这样：
//   - DeepAgent 的递归分解、Swarm 的 handoff 等专用组合可统一表达为「子 agent 作工具」；
//   - 任意 agent 都能作为子链节点被另一 agent 调度，无需为每种组合各写一套适配。
//
// 入参约定为单字段对象 {"query": string}，即交给子 Agent 的子任务文本。
type AgentTool struct {
	agent       Agent
	name        string
	description string
	queryKey    string
}

// AgentToolOption 配置 AgentTool。
type AgentToolOption func(*AgentTool)

// WithAgentToolName 覆盖工具名（默认取 agent.Name()）。
func WithAgentToolName(name string) AgentToolOption {
	return func(t *AgentTool) {
		if name != "" {
			t.name = name
		}
	}
}

// WithAgentToolDescription 覆盖工具描述（默认取 agent.Description() 或通用说明）。
func WithAgentToolDescription(desc string) AgentToolOption {
	return func(t *AgentTool) {
		if desc != "" {
			t.description = desc
		}
	}
}

// WithAgentToolQueryKey 覆盖入参字段名（默认 "query"）。
func WithAgentToolQueryKey(key string) AgentToolOption {
	return func(t *AgentTool) {
		if key != "" {
			t.queryKey = key
		}
	}
}

// NewAgentTool 把 agent 包装为可被其它 agent 调用的工具。
//
// 默认工具名取 agent.Name()，描述取 agent.Description()（为空则用通用说明），
// 入参字段为 "query"。
func NewAgentTool(a Agent, opts ...AgentToolOption) *AgentTool {
	t := &AgentTool{
		agent:    a,
		name:     a.Name(),
		queryKey: "query",
	}
	if d := a.Description(); d != "" {
		t.description = d
	} else {
		t.description = fmt.Sprintf("将子任务委派给子 Agent %q 处理并返回其结果", t.name)
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Name 返回工具名。
func (t *AgentTool) Name() string { return t.name }

// Description 返回工具描述。
func (t *AgentTool) Description() string { return t.description }

// Schema 返回单字段入参 Schema：{"query": string}。
func (t *AgentTool) Schema() *schema.Schema {
	return &schema.Schema{
		Type: "object",
		Properties: map[string]*schema.Schema{
			t.queryKey: {
				Type:        "string",
				Description: "交给子 Agent 处理的子任务或查询",
			},
		},
		Required: []string{t.queryKey},
	}
}

// Validate 校验入参含非空字符串 query。
func (t *AgentTool) Validate(args map[string]any) error {
	q, ok := args[t.queryKey].(string)
	if !ok || q == "" {
		return fmt.Errorf("agent tool %q: 缺少字符串参数 %q", t.name, t.queryKey)
	}
	return nil
}

// Execute 保留子运行的完整结果。普通失败作为失败 Result 返回；中断和取消
// 同时保留结构化结果与 Go error，供父运行在自己的作用域内处理。
func (t *AgentTool) Execute(ctx context.Context, args map[string]any) (tool.Result, error) {
	q, _ := args[t.queryKey].(string)
	child, err := runChildTask(ctx, t.agent, Input{Query: q}, "agent", t.agent.Run)
	return childToolResult(child, err)
}

// ChildRunResult 将执行身份与完整子结果绑定。RunID 属于子运行，ParentRunID
// 来自执行上下文；模型参数里的同名字段不参与身份或恢复权威。
type ChildRunResult struct {
	Version     int                        `json:"child_result_version"`
	Scope       string                     `json:"scope,omitempty"`
	RunID       string                     `json:"run_id"`
	ParentRunID string                     `json:"parent_run_id,omitempty"`
	TaskID      string                     `json:"task_id"`
	Activation  string                     `json:"activation"`
	AttemptID   string                     `json:"attempt_id"`
	ToolCallID  string                     `json:"tool_call_id,omitempty"`
	Child       ChildResult                `json:"child"`
	Parts       []llm.ContentPart          `json:"parts,omitempty"`
	Interrupt   *interrupt.PersistedSignal `json:"interrupt,omitempty"`
	// ControlError 固定原执行是否返回控制错误；nil 为旧记录，恢复时沿用保守决议。
	// 普通失败的已存结果可继续组合中其他位置，但不能重新派发原任务。
	ControlError *bool `json:"control_error,omitempty"`
}

// ModelToolContent 只返回模型使用的视图；完整 Blocks、用量及子结果仍由信封保留。
func (r ChildRunResult) ModelToolContent() string              { return r.Child.Output.Content }
func (r ChildRunResult) ToolMessageContent() []llm.ContentPart { return r.Parts }

type childRunKey struct{}
type childCodecsKey struct{}

// ContextWithChildStateCodecs 注入宿主持有的中断状态编解码器，不转移其所有权。
func ContextWithChildStateCodecs(ctx context.Context, codecs *interrupt.StateCodecs) context.Context {
	return context.WithValue(ctx, childCodecsKey{}, codecs)
}

func childRunID(ctx context.Context) string {
	if run, ok := ctx.Value(childRunKey{}).(ChildRunResult); ok {
		return run.RunID
	}
	return util.GenerateID("run")
}

func stableChildID(parent, task string) string {
	sum := sha256.Sum256([]byte(parent + "\x00" + task))
	return "child_" + hex.EncodeToString(sum[:16])
}

func childToolResult(child ChildRunResult, err error) (tool.Result, error) {
	result := tool.Result{Success: err == nil, Output: child}
	if err == nil {
		return result, nil
	}
	result.Error = err.Error()
	if childControlError(err) {
		return result, err
	}
	return result, nil
}

func childControlError(err error) bool {
	if _, ok := interrupt.IsInterruptSignal(err); ok {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return llm.IsUnsafeToReplay(err)
}

// AgentPanicError 保留子运行身份和原 panic；动作结果未知时禁止自动重放。
type AgentPanicError struct {
	AgentID string
	Value   any
	Stack   []byte
}

func (e *AgentPanicError) Error() string {
	return fmt.Sprintf("agent %q panicked: %v", e.AgentID, e.Value)
}
func (e *AgentPanicError) Unwrap() error {
	err, _ := e.Value.(error)
	return err
}
func (*AgentPanicError) UnsafeToReplay() bool { return true }

// ChildOutputCopyError 表示子任务已经返回，但其可变结果无法隔离。
// 原结果及已知用量仍随 ChildRunResult 返回；不能通过重跑子任务修复复制失败。
type ChildOutputCopyError struct {
	AgentID string
	Cause   error
}

func (e *ChildOutputCopyError) Error() string {
	return fmt.Sprintf("agent %q output cannot be isolated: %v", e.AgentID, e.Cause)
}
func (e *ChildOutputCopyError) Unwrap() error      { return e.Cause }
func (*ChildOutputCopyError) UnsafeToReplay() bool { return true }

// runChildTask 复用父运行的任务提交器；没有持久宿主时直接执行，不强制 JSON 限制。
// 只有显式选中的中断获得新激活，已完成兄弟任务继续使用原激活。
// 子输入与完整返回结果按值隔离，父输入、兄弟任务和下游交接不共享可变载荷。
func runChildTask(ctx context.Context, a Agent, input Input, position string, execute func(context.Context, Input) (Output, error)) (ChildRunResult, error) {
	invocation, hasInvocation := agentruntime.ToolInvocationFromContext(ctx)
	parentID, taskID, activation := invocation.RunID, invocation.TaskID+"/"+position, "initial"
	continuation := invocation.Activation
	if current, ok := ctx.Value(childRunKey{}).(ChildRunResult); ok && !hasInvocation {
		parentID, taskID = current.RunID, current.TaskID+"/"+position
		invocation.Scope = current.Scope
		continuation = current.Activation
	}
	if parentID == "" {
		parentID, taskID = childRunID(ctx), position
	}
	child := ChildRunResult{Version: 1, Scope: invocation.Scope, RunID: stableChildID(parentID, taskID), ParentRunID: parentID, TaskID: taskID, Activation: activation, AttemptID: util.GenerateID("attempt"), ToolCallID: invocation.ToolCallID, Child: ChildResult{AgentID: a.ID(), AgentName: a.Name()}}
	childCtx := interrupt.AppendAddressSegment(ctx, interrupt.SegmentAgent, position, "")
	target, _, _ := interrupt.GetResumeContext[any](childCtx)
	resumeTargetID, hasResumeTarget := interrupt.ResumeTargetID(childCtx)
	if target && len(continuation) > len("resume/") && continuation[:len("resume/")] == "resume/" {
		activation = continuation
	}
	childCtx = context.WithValue(childCtx, childRunKey{}, child)
	// 子运行不能把父工具的调用位置再当作自己的父位置。
	childCtx = agentruntime.ContextWithToolInvocation(childCtx, agentruntime.ToolInvocation{})
	codecs, _ := ctx.Value(childCodecsKey{}).(*interrupt.StateCodecs)
	run := func(runCtx context.Context) (out ChildRunResult, runErr error) {
		out = child
		out.Activation, out.AttemptID = activation, util.GenerateID("attempt")
		runCtx = context.WithValue(runCtx, childRunKey{}, out)
		defer func() {
			if value := recover(); value != nil {
				runErr = &AgentPanicError{AgentID: child.Child.AgentID, Value: value, Stack: debug.Stack()}
			}
			frozen, copyErr := reflectx.DeepCopyChecked(out.Child.Output)
			if copyErr != nil {
				runErr = errors.Join(runErr, &ChildOutputCopyError{AgentID: child.Child.AgentID, Cause: copyErr})
			} else {
				out.Child.Output = frozen
			}
			control := childControlError(runErr)
			out.ControlError = &control
			out.Child.Status = "returned"
			if runErr != nil {
				out.Child.Status, out.Child.Error = "failed", runErr.Error()
				if _, ok := interrupt.IsInterruptSignal(runErr); ok {
					out.Child.Status = "waiting"
				}
				if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
					out.Child.Status = "cancelled"
				}
			}
			if out.Child.Output.Metadata == nil {
				out.Child.Output.Metadata = map[string]any{}
			} else if copyErr != nil {
				// 失败结果仍需补充身份，不能为此修改子执行者持有的原元数据容器。
				metadata := make(map[string]any, len(out.Child.Output.Metadata))
				for key, value := range out.Child.Output.Metadata {
					metadata[key] = value
				}
				out.Child.Output.Metadata = metadata
			}
			out.Child.Output.Metadata["run_id"] = out.RunID
			out.Child.Output.Metadata["parent_run_id"] = out.ParentRunID
			out.Child.Output.Metadata["task_id"] = out.TaskID
			for _, call := range out.Child.Output.ToolCalls {
				out.Parts = append(out.Parts, toolResultParts(call.Result)...)
			}
		}()
		candidate, copyErr := reflectx.DeepCopyChecked(input)
		if copyErr != nil {
			return out, fmt.Errorf("agent %q input cannot be isolated: %w", child.Child.AgentID, copyErr)
		}
		out.Child.Output, runErr = execute(runCtx, candidate)
		return out, runErr
	}
	if _, durable := agentruntime.TaskExecutorFromContext(ctx); !durable {
		return run(childCtx)
	}
	data, err := json.Marshal(input)
	if err != nil {
		return child, fmt.Errorf("agent: encode child input: %w", err)
	}
	for {
		var live *ChildRunResult
		var liveErr error
		var resumeTargets []string
		if hasResumeTarget && activation == "resume/"+resumeTargetID {
			resumeTargets = []string{resumeTargetID}
		}
		outcome, execErr := agentruntime.ExecuteTask(childCtx, agentruntime.TaskCall{ID: taskID, Activation: activation, Input: data, ResumeTargets: resumeTargets}, func(runCtx context.Context) (agentruntime.TaskOutcome, error) {
			result, runErr := run(runCtx)
			live, liveErr = &result, runErr
			status := agentruntime.TaskSucceeded
			if runErr != nil {
				status = agentruntime.TaskFailed
			}
			if signal, ok := interrupt.IsInterruptSignal(runErr); ok {
				status = agentruntime.TaskWaiting
				result.Interrupt, err = interrupt.EncodeSignal(signal, codecs)
				if err != nil {
					return agentruntime.TaskOutcome{}, fmt.Errorf("agent: encode child interrupt: %w", err)
				}
			}
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				status = agentruntime.TaskCancelled
			}
			wire, encodeErr := encodeTypedToolOutput(result)
			if encodeErr != nil {
				return agentruntime.TaskOutcome{}, encodeErr
			}
			encoded, encodeErr := json.Marshal(wire)
			errorText := result.Child.Error
			if status == agentruntime.TaskWaiting {
				errorText = ""
			}
			return agentruntime.TaskOutcome{Status: status, Data: encoded, Error: errorText}, encodeErr
		})
		if execErr != nil {
			if live != nil {
				return *live, errors.Join(liveErr, execErr)
			}
			var taskErr *agentruntime.TaskExecutionError
			if !errors.As(execErr, &taskErr) || len(outcome.Data) == 0 {
				return child, execErr
			}
		}
		var wire typedToolOutput
		if err := decodeToolJSON(outcome.Data, &wire); err != nil {
			return child, fmt.Errorf("agent: decode child result: %w", err)
		}
		decoded, decodeErr := decodeTypedToolOutput(wire)
		if decodeErr != nil {
			return child, fmt.Errorf("agent: decode child result: %w", decodeErr)
		}
		saved, ok := decoded.(ChildRunResult)
		if !ok {
			return child, fmt.Errorf("agent: stored task is not a child result")
		}
		if live != nil {
			return *live, liveErr
		}
		if saved.Interrupt != nil {
			signal, err := interrupt.DecodeSignal(saved.Interrupt, codecs)
			if err != nil {
				return saved, err
			}
			if target := requestedChildInterrupt(ctx, signal); target != "" {
				if next := "resume/" + target; activation != next {
					activation = next
					continue
				}
			}
			return saved, signal
		}
		if outcome.Status == agentruntime.TaskCancelled {
			return saved, context.Canceled
		}
		if outcome.Error != "" {
			return saved, &agentruntime.TaskExecutionError{TaskID: taskID, Message: outcome.Error}
		}
		return saved, nil
	}
}

func requestedChildInterrupt(ctx context.Context, signal *interrupt.InterruptSignal) string {
	if interrupt.IsResumeRequested(ctx, signal.ID) {
		return signal.ID
	}
	for _, sub := range signal.Subs {
		if id := requestedChildInterrupt(ctx, sub); id != "" {
			return id
		}
	}
	return ""
}

// ToolResultOffloader 将完整工具结果和必要媒体写入现有 Artifact 服务。
// 宿主负责存储生命周期；保存或登记失败时返回原结果与错误，不生成成功引用。
type ToolResultOffloader struct {
	Artifacts      *artifact.Service
	Objects        artifact.ObjectStore
	MaxInlineBytes int
}

type toolResultOffloaderKey struct{}

// ContextWithToolResultOffloader 为当前运行选择大结果存储，不改变其他运行的策略。
func ContextWithToolResultOffloader(ctx context.Context, offloader *ToolResultOffloader) context.Context {
	return context.WithValue(ctx, toolResultOffloaderKey{}, offloader)
}

// StoredToolResult 同时保留公开完整结果、稳定引用和模型视图，引用可由 Read 回读。
type StoredToolResult struct {
	Result       tool.Result       `json:"result"`
	Artifact     artifact.Artifact `json:"artifact"`
	Parts        []llm.ContentPart `json:"parts,omitempty"`
	StorageError string            `json:"storage_error,omitempty"`
}

func (r StoredToolResult) ModelToolContent() string {
	if r.StorageError != "" {
		return formatToolResult(r.Result) + "\nTool result storage incomplete: " + r.StorageError
	}
	return fmt.Sprintf("Full tool result saved as artifact %s (object %s, sha256 %s, %d bytes).", r.Artifact.ID, r.Artifact.ObjectKey, r.Artifact.Digest, r.Artifact.Size)
}
func (r StoredToolResult) ToolMessageContent() []llm.ContentPart {
	if r.StorageError != "" {
		return r.Parts
	}
	// 原文位于可回读对象中；模型保留必要媒体，避免把已卸载正文再次塞回上下文。
	var media []llm.ContentPart
	for _, part := range r.Parts {
		if part.Type != "text" {
			media = append(media, part)
		}
	}
	return media
}

// ToolResultStorageError 区分工具已返回与产物存储未完成，不能通过重跑工具修复。
type ToolResultStorageError struct{ Cause error }

func (e *ToolResultStorageError) Error() string {
	return fmt.Sprintf("agent: tool result storage incomplete: %v", e.Cause)
}
func (e *ToolResultStorageError) Unwrap() error           { return e.Cause }
func (*ToolResultStorageError) UnsafeToReplay() bool      { return true }
func (*ToolResultStorageError) NeedsReconciliation() bool { return true }

func (o *ToolResultOffloader) Offload(ctx context.Context, result tool.Result) (tool.Result, error) {
	if o == nil {
		return result, nil
	}
	payload := StoredToolResult{Result: result, Parts: toolResultParts(result)}
	wire, err := encodeTypedToolOutput(payload)
	if err != nil {
		return result, fmt.Errorf("agent: encode tool result: %w", err)
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return result, fmt.Errorf("agent: encode tool result: %w", err)
	}
	limit := o.MaxInlineBytes
	if limit <= 0 {
		limit = maxToolResultChars
	}
	if len(data) <= limit {
		return result, nil
	}
	if o.Artifacts == nil || o.Objects == nil {
		return result, fmt.Errorf("agent: tool result artifact storage is not configured")
	}
	invocation, ok := agentruntime.ToolInvocationFromContext(ctx)
	if !ok {
		invocation.RunID, invocation.TaskID, invocation.Activation = childRunID(ctx), util.GenerateID("tool"), "initial"
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	id := stableChildID(invocation.Scope+"/"+invocation.RunID, invocation.TaskID+"/"+invocation.Activation+"/"+digest)
	art := artifact.Artifact{ID: id, SessionID: invocation.Scope + "/" + invocation.RunID, Name: "tool-result.json", MimeType: "application/json", ObjectKey: "tool-results/" + id, Digest: digest, Data: data, Metadata: map[string]any{"scope": invocation.Scope, "run_id": invocation.RunID, "task_id": invocation.TaskID, "activation": invocation.Activation, "tool_call_id": invocation.ToolCallID}}
	storageFailure := func(err error) (tool.Result, error) {
		art.Data = nil
		art.RegistrationStatus = "storage_unconfirmed"
		payload.Artifact, payload.StorageError = art, err.Error()
		return tool.Result{Success: result.Success, Output: payload, Error: result.Error}, err
	}
	if _, err := o.Artifacts.SaveObject(ctx, o.Objects, art); err != nil {
		return storageFailure(err)
	}
	registered, err := o.Artifacts.Get(ctx, id)
	if err != nil {
		return storageFailure(err)
	}
	if _, err := o.Read(ctx, id); err != nil {
		return storageFailure(err)
	}
	payload.Artifact = *registered
	return tool.Result{Success: result.Success, Output: payload, Error: result.Error}, nil
}

// Repair 只核对并登记失败描述中的原对象，不重新执行工具或重新上传内容。
func (o *ToolResultOffloader) Repair(ctx context.Context, pending StoredToolResult) (tool.Result, error) {
	if o == nil || o.Artifacts == nil || o.Objects == nil {
		return pending.Result, fmt.Errorf("agent: tool result artifact storage is not configured")
	}
	if _, err := o.Artifacts.RegisterObject(ctx, o.Objects, pending.Artifact); err != nil {
		return tool.Result{Success: pending.Result.Success, Output: pending, Error: pending.Result.Error}, err
	}
	stored, err := o.Read(ctx, pending.Artifact.ID)
	if err != nil {
		return tool.Result{Success: pending.Result.Success, Output: pending, Error: pending.Result.Error}, err
	}
	return tool.Result{Success: stored.Result.Success, Output: stored, Error: stored.Result.Error}, nil
}

// Read 校验已登记对象摘要后恢复完整结果；不会使用摘要或预览代替原文和媒体。
func (o *ToolResultOffloader) Read(ctx context.Context, id string) (StoredToolResult, error) {
	if o == nil || o.Artifacts == nil || o.Objects == nil {
		return StoredToolResult{}, fmt.Errorf("agent: tool result artifact storage is not configured")
	}
	art, err := o.Artifacts.Get(ctx, id)
	if err != nil {
		return StoredToolResult{}, err
	}
	data, err := o.Objects.Get(ctx, art.ObjectKey)
	if err != nil {
		return StoredToolResult{}, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != art.Digest {
		return StoredToolResult{}, artifact.ErrObjectDigest
	}
	var wire typedToolOutput
	if err := decodeToolJSON(data, &wire); err != nil {
		return StoredToolResult{}, err
	}
	decoded, err := decodeTypedToolOutput(wire)
	if err != nil {
		return StoredToolResult{}, err
	}
	saved, ok := decoded.(StoredToolResult)
	if !ok {
		return StoredToolResult{}, fmt.Errorf("agent: artifact is not a stored tool result")
	}
	saved.Artifact = *art
	return saved, nil
}

func toolResultParts(result tool.Result) []llm.ContentPart {
	if media, ok := result.Output.(interface{ ToolMessageContent() []llm.ContentPart }); ok {
		return media.ToolMessageContent()
	}
	return nil
}

const agentToolResultKind = "hexagon.agent.tool-result"

// typedToolOutput 的种类只由实际 Go 类型产生；JSON map 不能自行声明为子运行或产物回执。
type typedToolOutput struct {
	Kind    string          `json:"kind"`
	Version int             `json:"version"`
	Data    json.RawMessage `json:"data"`
}
type toolResultWire struct {
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Output  typedToolOutput `json:"output"`
}
type toolCallWire struct {
	ToolCallRecord
	Result toolResultWire `json:"result"`
}
type childResultWire struct {
	ChildResult
	Output agentOutputWire `json:"output"`
}
type agentOutputWire struct {
	Output
	ToolCalls []toolCallWire    `json:"tool_calls,omitempty"`
	Children  []childResultWire `json:"children,omitempty"`
}
type childRunWire struct {
	ChildRunResult
	Child childResultWire `json:"child"`
}
type storedToolWire struct {
	StoredToolResult
	Result toolResultWire `json:"result"`
}

func encodeAgentToolPayload(result tool.Result) (typedToolOutput, error) {
	wire, err := encodeToolResultWire(result)
	if err != nil {
		return typedToolOutput{}, err
	}
	data, err := json.Marshal(wire)
	return typedToolOutput{Kind: agentToolResultKind, Version: 1, Data: data}, err
}
func decodeAgentToolPayload(payload typedToolOutput) (tool.Result, error) {
	if payload.Kind != agentToolResultKind || payload.Version != 1 {
		return tool.Result{}, fmt.Errorf("agent: unsupported structured tool result %q version %d", payload.Kind, payload.Version)
	}
	var wire toolResultWire
	if err := decodeToolJSON(payload.Data, &wire); err != nil {
		return tool.Result{}, err
	}
	return decodeToolResultWire(wire)
}

func encodeToolResultWire(result tool.Result) (toolResultWire, error) {
	output, err := encodeTypedToolOutput(result.Output)
	return toolResultWire{Success: result.Success, Error: result.Error, Output: output}, err
}
func decodeToolResultWire(wire toolResultWire) (tool.Result, error) {
	output, err := decodeTypedToolOutput(wire.Output)
	return tool.Result{Success: wire.Success, Error: wire.Error, Output: output}, err
}
func encodeTypedToolOutput(value any) (typedToolOutput, error) {
	encoded := typedToolOutput{Kind: "json", Version: 1}
	var data any = value
	switch value := value.(type) {
	case *ChildRunResult:
		if value != nil {
			return encodeTypedToolOutput(*value)
		}
	case *StoredToolResult:
		if value != nil {
			return encodeTypedToolOutput(*value)
		}
	case ChildRunResult:
		output, err := encodeAgentOutput(value.Child.Output)
		if err != nil {
			return encoded, err
		}
		encoded.Kind = "child-run"
		data = childRunWire{ChildRunResult: value, Child: childResultWire{ChildResult: value.Child, Output: output}}
	case StoredToolResult:
		result, err := encodeToolResultWire(value.Result)
		if err != nil {
			return encoded, err
		}
		encoded.Kind = "stored-tool-result"
		data = storedToolWire{StoredToolResult: value, Result: result}
	}
	var err error
	encoded.Data, err = json.Marshal(data)
	return encoded, err
}
func decodeTypedToolOutput(encoded typedToolOutput) (any, error) {
	if encoded.Version != 1 {
		return nil, fmt.Errorf("agent: unsupported tool output version %d", encoded.Version)
	}
	switch encoded.Kind {
	case "json":
		var value any
		err := decodeToolJSON(encoded.Data, &value)
		return value, err
	case "child-run":
		var wire childRunWire
		if err := decodeToolJSON(encoded.Data, &wire); err != nil {
			return nil, err
		}
		if wire.Version != 1 {
			return nil, fmt.Errorf("agent: unsupported child result version %d", wire.Version)
		}
		output, err := decodeAgentOutput(wire.Child.Output)
		wire.ChildRunResult.Child = wire.Child.ChildResult
		wire.ChildRunResult.Child.Output = output
		return wire.ChildRunResult, err
	case "stored-tool-result":
		var wire storedToolWire
		if err := decodeToolJSON(encoded.Data, &wire); err != nil {
			return nil, err
		}
		result, err := decodeToolResultWire(wire.Result)
		wire.StoredToolResult.Result = result
		return wire.StoredToolResult, err
	default:
		return nil, fmt.Errorf("agent: unsupported tool output kind %q", encoded.Kind)
	}
}
func encodeAgentOutput(output Output) (agentOutputWire, error) {
	wire := agentOutputWire{Output: output}
	for _, call := range output.ToolCalls {
		result, err := encodeToolResultWire(call.Result)
		if err != nil {
			return wire, err
		}
		wire.ToolCalls = append(wire.ToolCalls, toolCallWire{ToolCallRecord: call, Result: result})
	}
	for _, child := range output.Children {
		result, err := encodeAgentOutput(child.Output)
		if err != nil {
			return wire, err
		}
		wire.Children = append(wire.Children, childResultWire{ChildResult: child, Output: result})
	}
	return wire, nil
}
func decodeAgentOutput(wire agentOutputWire) (Output, error) {
	output := wire.Output
	output.ToolCalls, output.Children = nil, nil
	for _, call := range wire.ToolCalls {
		result, err := decodeToolResultWire(call.Result)
		if err != nil {
			return output, err
		}
		record := call.ToolCallRecord
		record.Result = result
		output.ToolCalls = append(output.ToolCalls, record)
	}
	for _, child := range wire.Children {
		result, err := decodeAgentOutput(child.Output)
		if err != nil {
			return output, err
		}
		record := child.ChildResult
		record.Output = result
		output.Children = append(output.Children, record)
	}
	return output, nil
}
func decodeToolJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

// 编译期断言：*AgentTool 满足 tool.Tool。
var _ tool.Tool = (*AgentTool)(nil)
