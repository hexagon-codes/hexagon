package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/hexagon-codes/ai-core/llm"
)

var ErrModelSessionClosed = errors.New("runtime: model session is closed")

// ModelSessionReport 只读关联一次独立模型会话的调用事实，不自动计入父 State。
// Usage 是已知原始用量；未知部分通过计数与 UsageSource 声明，不能再按 AfterLLM 累加一次。
type ModelSessionReport struct {
	RunID         string
	ParentRunID   string
	Usage         llm.Usage
	UsageSource   string
	ReportedCalls int
	UnknownCalls  int
	PartialCalls  int
	Attempts      []ModelAttempt
	References    []ContentReference
	Closed        bool
}

// ModelSession 持有独立 State、emitter 与 prepare pin，不持有父运行的可变 State。
// 调用串行；宿主拥有工作 goroutine、取消、Join 与 Close 的顺序。
type ModelSession struct {
	callMu       sync.Mutex
	runner       *DefaultRunner
	request      Request
	state        *State
	emitter      *runEmitter
	lifetime     context.Context
	cancel       context.CancelFunc
	release      func()
	closed       atomic.Bool
	closeDone    chan struct{}
	closeErr     error
	reportMu     sync.RWMutex
	report       ModelSessionReport
	seenAttempts map[string]bool
}

// NewModelSession 固定一个稳定子运行身份并独立取得 prepare 使用权，不启动后台工作。
// parent_run_id 仅供关联；持久执行权只能来自真实的父 TaskExecutor。
func (r *DefaultRunner) NewModelSession(ctx context.Context, request Request, sink EventSink) (*ModelSession, error) {
	if r == nil {
		return nil, fmt.Errorf("runtime: model session runner is nil")
	}
	if request.ID == "" {
		return nil, fmt.Errorf("runtime: model session requires a stable request ID")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request.StructuredOutput = nil
	lifetime, cancel := context.WithCancel(ctx)
	bound, prepared, references, release, err := r.prepareExecution(lifetime, request)
	if err != nil {
		cancel()
		return nil, err
	}
	cleanup := func(cause error) error {
		cancel()
		if release != nil {
			cause = errors.Join(cause, invokeExtension("model_session.release", func() error { release(); return nil }))
		}
		return cause
	}
	prepared.StructuredOutput = nil
	prepared, err = CloneRequest(prepared)
	if err != nil {
		return nil, cleanup(err)
	}
	if err := lifetime.Err(); err != nil {
		return nil, cleanup(err)
	}
	prepared.StreamMode = StreamModeOff
	prepared.StructuredOutput = nil
	active := *bound
	active.durable = nil
	if active.session == nil {
		if executor, ok := TaskExecutorFromContext(ctx); ok {
			if parent, ok := executor.(*runSession); ok {
				active.session = parent
			}
		}
	}
	references = append(cloneReferences(active.references), references...)
	state := &State{Request: prepared, Messages: CloneOutputMessages(prepared.Messages), Attributes: map[string]any{}}
	parentID, _ := prepared.Metadata["parent_run_id"].(string)
	session := &ModelSession{runner: &active, request: prepared, state: state, lifetime: lifetime, cancel: cancel, release: release, closeDone: make(chan struct{}), seenAttempts: map[string]bool{}, report: ModelSessionReport{RunID: prepared.ID, ParentRunID: parentID, UsageSource: "unknown", References: cloneReferences(references)}}
	session.emitter = newRunEmitter(prepared, EventSinkFunc(func(callCtx context.Context, event Event) error {
		if event.Type == EventModelAttemptFinished {
			if attempt, ok := event.Payload.(ModelAttempt); ok {
				session.refreshReport(&attempt)
			}
		}
		return emit(callCtx, sink, event)
	}))
	state.emit = session.emitter.emit
	return session, nil
}

// Context 只绑定本 Session 的统一调用器；调用方仍负责其工作 context。
func (s *ModelSession) Context(ctx context.Context) context.Context {
	return llm.WithCompletionInvoker(ctx, s)
}

func (s *ModelSession) callContext(ctx context.Context) (context.Context, func()) {
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.lifetime, cancel)
	return call, func() { stop(); cancel() }
}

// SelectProvider 从已 prepare/pin 的选择器借用 Provider/Model，句柄有效期截止 Close。
// 不重新获取 latest，也不替任意额外 Provider 申请使用权。
func (s *ModelSession) SelectProvider(ctx context.Context) (ProviderSelection, error) {
	if s == nil {
		return ProviderSelection{}, ErrModelSessionClosed
	}
	s.callMu.Lock()
	defer s.callMu.Unlock()
	if s.closed.Load() {
		return ProviderSelection{}, ErrModelSessionClosed
	}
	if err := s.lifetime.Err(); err != nil {
		return ProviderSelection{}, err
	}
	if s.runner.selector == nil {
		return ProviderSelection{}, fmt.Errorf("runtime: model session provider selector is not configured")
	}
	request, err := CloneRequest(s.request)
	if err != nil {
		return ProviderSelection{}, err
	}
	callCtx, cancel := s.callContext(ctx)
	defer cancel()
	var selection ProviderSelection
	err = invokeExtension("model_session.select", func() error { var err error; selection, err = s.runner.selector.Select(callCtx, request); return err })
	if err != nil {
		return ProviderSelection{}, err
	}
	if err := callCtx.Err(); err != nil {
		return ProviderSelection{}, err
	}
	return selection, nil
}

// InvokeComplete 使用调用者传入的 Provider 与完整请求，经共同 attempt 边界归档。
// 显式额外 Provider 由宿主独立持有；本方法不按名称替换它或伪造 pin。
func (s *ModelSession) InvokeComplete(ctx context.Context, provider llm.CallProvider, request llm.CompletionRequest, purpose string) (response *llm.CompletionResponse, err error) {
	if s == nil {
		return nil, ErrModelSessionClosed
	}
	s.callMu.Lock()
	defer s.callMu.Unlock()
	defer s.refreshReport(nil)
	err = invokeExtension("model_session.invoke", func() error {
		var callErr error
		response, callErr = s.invokeCompleteLocked(ctx, provider, request, purpose)
		return callErr
	})
	return response, err
}

func (s *ModelSession) invokeCompleteLocked(ctx context.Context, provider llm.CallProvider, request llm.CompletionRequest, purpose string) (*llm.CompletionResponse, error) {
	if s.closed.Load() {
		return nil, ErrModelSessionClosed
	}
	if err := s.lifetime.Err(); err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, fmt.Errorf("runtime: model session provider is nil")
	}
	callCtx, cancel := s.callContext(ctx)
	defer cancel()
	if err := callCtx.Err(); err != nil {
		return nil, err
	}
	prepared, err := CloneModelRequest(request)
	if err != nil {
		return nil, err
	}
	invocation, err := CloneRequest(s.request)
	if err != nil {
		return nil, err
	}
	if purpose == "" {
		purpose = invocation.Purpose
	}
	if purpose == "" {
		purpose = "model_session"
	}
	invocation.StreamMode, invocation.Purpose = StreamModeOff, purpose
	invocation.StructuredOutput = nil
	providerName := provider.Name()
	invocation.ProviderName, invocation.ModelName = providerName, prepared.Model
	invocation.Messages = CloneOutputMessages(prepared.Messages)
	invocation.Tools = prepared.Tools
	metadata := cloneRuntimeMetadata(invocation.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	for key, value := range cloneRuntimeMetadata(prepared.Metadata) {
		metadata[key] = value
	}
	if parentID, ok := s.request.Metadata["parent_run_id"]; ok {
		metadata["parent_run_id"] = parentID
	}
	invocation.Metadata = metadata
	invocation.Generation = llm.GenerationOptions{ToolChoice: prepared.ToolChoice, MaxTokens: prepared.MaxTokens, Temperature: prepared.Temperature, TopP: prepared.TopP, Stop: prepared.Stop, User: prepared.User, ReasoningPolicyScope: prepared.ReasoningPolicyScope, ResponseFormat: prepared.ResponseFormat}
	s.state.Request = invocation
	s.state.Messages = CloneOutputMessages(prepared.Messages)
	s.state.Turn++
	selection := ProviderSelection{Provider: provider, Name: providerName, Model: prepared.Model}
	return s.runner.invokeProvider(WithModelPurpose(callCtx, purpose), invocation, selection, prepared, s.state, s.emitter)
}

// refreshReport 仅由串行模型调用线程读取 State；观察者只读取独立缓存。
func (s *ModelSession) refreshReport(attempt *ModelAttempt) {
	s.reportMu.Lock()
	defer s.reportMu.Unlock()
	if attempt != nil && !s.seenAttempts[attempt.ID] {
		copy := *attempt
		copy.UsageFields = append([]string(nil), attempt.UsageFields...)
		s.report.Attempts = append(s.report.Attempts, copy)
		s.seenAttempts[attempt.ID] = true
	}
	s.report.Usage = s.state.Usage
	s.report.ReportedCalls = usageCount(s.state.Attributes, UsageReportedCallsMetadataKey)
	s.report.UnknownCalls = usageCount(s.state.Attributes, UsageMissingCallsMetadataKey)
	s.report.PartialCalls = usageCount(s.state.Attributes, UsagePartialCallsMetadataKey)
	if source, ok := s.state.Attributes[UsageSourceMetadataKey].(string); ok {
		s.report.UsageSource = source
	}
}

// Report 返回独立事实投影；可在事件观察回调中读取，不与调用锁相互等待。
func (s *ModelSession) Report() ModelSessionReport {
	if s == nil {
		return ModelSessionReport{Closed: true, UsageSource: "unknown"}
	}
	s.reportMu.RLock()
	defer s.reportMu.RUnlock()
	copy := s.report
	copy.Closed = s.closed.Load()
	copy.References = cloneReferences(copy.References)
	copy.Attempts = append([]ModelAttempt(nil), copy.Attempts...)
	for i := range copy.Attempts {
		copy.Attempts[i].UsageFields = append([]string(nil), copy.Attempts[i].UsageFields...)
	}
	return copy
}

// UsageSnapshot 只返回已知原始用量；未知回执计数见 Report，不代表新增一次计费。
func (s *ModelSession) UsageSnapshot() llm.Usage { return s.Report().Usage }

// Close 取消合作式调用、等待当前委托结束后释放 prepare pin 一次。
// 由宿主在事件回调之外调用；不会强杀忽略 context 的 Provider。
func (s *ModelSession) Close() error {
	if s == nil {
		return nil
	}
	if !s.closed.CompareAndSwap(false, true) {
		<-s.closeDone
		return s.closeErr
	}
	s.cancel()
	s.callMu.Lock()
	if s.release != nil {
		s.closeErr = invokeExtension("model_session.release", func() error { s.release(); return nil })
	}
	s.callMu.Unlock()
	close(s.closeDone)
	return s.closeErr
}

var _ llm.CompletionInvoker = (*ModelSession)(nil)
