package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	wire "github.com/a2aproject/a2a-go/v2/a2a"
)

// rpcStreamWriter 把同一个 JSON-RPC 请求 ID 贯穿整个 SSE 响应。
type rpcStreamWriter struct {
	http.ResponseWriter
	id any
}

func (w *rpcStreamWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) handleProtocolRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, NewParseError(err.Error()), nil)
		return
	}
	if req.JSONRPC != "2.0" {
		s.writeError(w, NewInvalidRequestError("invalid JSON-RPC version"), req.ID)
		return
	}
	switch req.ID.(type) {
	case string, float64:
	default:
		s.writeError(w, NewInvalidRequestError("request ID must be a string or number"), nil)
		return
	}
	if version := r.Header.Get("A2A-Version"); version != "" && version != ProtocolVersion {
		s.writeError(w, &Error{Code: -32009, Message: "A2A version not supported"}, req.ID)
		return
	}
	ctx := r.Context()
	if req.Method == MethodSendMessageStream || req.Method == MethodResubscribe {
		if !s.card.Capabilities.Streaming {
			s.writeError(w, NewUnsupportedOperationError("streaming"), req.ID)
			return
		}
		if _, ok := w.(http.Flusher); !ok {
			s.writeError(w, NewInternalError("streaming not supported"), req.ID)
			return
		}
		stream := &rpcStreamWriter{ResponseWriter: w, id: req.ID}
		if req.Method == MethodSendMessageStream {
			s.handleSendMessageStream(ctx, stream, req.Params)
		} else {
			s.handleResubscribe(ctx, stream, req.Params)
		}
		return
	}
	var result any
	var rpcErr *Error
	switch req.Method {
	case MethodSendMessage:
		var task *Task
		task, rpcErr = s.handleSendMessage(ctx, req.Params)
		if rpcErr == nil {
			result = MessageResult{Task: task}
		}
	case MethodGetTask:
		result, rpcErr = s.handleGetTask(ctx, req.Params)
	case MethodListTasks:
		result, rpcErr = s.handleListTasks(ctx, req.Params)
	case MethodCancelTask:
		result, rpcErr = s.handleCancelTask(ctx, req.Params)
	case MethodSetPushNotification, MethodGetPushNotification, "ListTaskPushNotificationConfigs", "DeleteTaskPushNotificationConfig":
		rpcErr = NewPushNotSupportedError()
	case "GetExtendedAgentCard":
		rpcErr = &Error{Code: -32007, Message: "extended agent card is not configured"}
	default:
		rpcErr = NewMethodNotFoundError(req.Method)
	}
	if rpcErr != nil {
		s.writeError(w, rpcErr, req.ID)
		return
	}
	s.writeResult(w, result, req.ID)
}

func (s *Server) protocolEvent(event any) (wire.Event, error) {
	switch e := event.(type) {
	case *TaskEvent:
		return wireTask(e.Task)
	case *DoneEvent:
		// A2A 1.0 通过终态与流关闭结束，不发送私有 done 消息。
		return nil, nil
	case *TaskStatusEvent:
		status, err := wireStatus(e.Status)
		if err != nil {
			return nil, err
		}
		contextID := e.ContextID
		if contextID == "" {
			if task, err := s.store.Get(context.Background(), e.ID); err == nil {
				contextID = task.SessionID
			}
		}
		return &wire.TaskStatusUpdateEvent{TaskID: wire.TaskID(e.ID), ContextID: contextID, Status: status, Metadata: e.Metadata}, nil
	case *ArtifactEvent:
		artifact, err := wireArtifact(&e.Artifact)
		if err != nil {
			return nil, err
		}
		contextID := e.ContextID
		if contextID == "" {
			if task, err := s.store.Get(context.Background(), e.ID); err == nil {
				contextID = task.SessionID
			}
		}
		return &wire.TaskArtifactUpdateEvent{TaskID: wire.TaskID(e.ID), ContextID: contextID, Artifact: artifact, Append: e.Artifact.Append, LastChunk: e.Artifact.LastChunk, Metadata: e.Metadata}, nil
	case *MessageEvent:
		return wireMessage(e.Message)
	default:
		return nil, fmt.Errorf("unsupported stream event %T", event)
	}
}

func (s *Server) writeProtocolEvent(w http.ResponseWriter, event any) {
	var id any
	if stream, ok := w.(*rpcStreamWriter); ok {
		id = stream.id
	}
	var response *JSONRPCResponse
	if failure, ok := event.(*ErrorEvent); ok {
		response = NewJSONRPCErrorResponse(id, failure.Error)
	} else {
		value, err := s.protocolEvent(event)
		if err != nil {
			response = NewJSONRPCErrorResponse(id, NewInternalError(err.Error()))
		} else {
			if value == nil {
				return
			}
			response, err = NewJSONRPCResponse(id, wire.StreamResponse{Event: value})
			if err != nil {
				response = NewJSONRPCErrorResponse(id, NewInternalError(err.Error()))
			}
		}
	}
	data, err := json.Marshal(response)
	if err != nil {
		return
	}
	w.Header().Set("Content-Type", ContentTypeSSE)
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func decodeProtocolEvent(data string) StreamEvent {
	var response JSONRPCResponse
	if err := json.Unmarshal([]byte(data), &response); err != nil {
		return &ErrorEvent{Error: NewParseError(err.Error())}
	}
	if response.Error != nil {
		return &ErrorEvent{Error: response.Error}
	}
	if response.JSONRPC != "2.0" {
		return &ErrorEvent{Error: NewInvalidRequestError("invalid JSON-RPC response version")}
	}
	var value wire.StreamResponse
	if err := json.Unmarshal(response.Result, &value); err != nil {
		return &ErrorEvent{Error: NewParseError(err.Error())}
	}
	switch e := value.Event.(type) {
	case *wire.Task:
		return &TaskEvent{Task: localTask(e)}
	case *wire.Message:
		return &MessageEvent{Message: localMessage(e)}
	case *wire.TaskStatusUpdateEvent:
		status := localStatus(e.Status)
		return &TaskStatusEvent{ID: string(e.TaskID), ContextID: e.ContextID, Status: status, Final: status.State.IsTerminal() || status.State == TaskStateInputRequired || status.State == TaskStateAuthRequired, Metadata: e.Metadata}
	case *wire.TaskArtifactUpdateEvent:
		artifact := localArtifact(e.Artifact)
		artifact.Append = e.Append
		artifact.LastChunk = e.LastChunk
		return &ArtifactEvent{ID: string(e.TaskID), ContextID: e.ContextID, Artifact: artifact, Metadata: e.Metadata}
	default:
		return &ErrorEvent{Error: NewParseError("unknown stream event")}
	}
}

func projectHistory(task *Task, limit *int) *Task {
	result := cloneTask(task)
	if limit != nil && *limit >= 0 && len(result.History) > *limit {
		result.History = result.History[len(result.History)-*limit:]
	}
	return result
}

func (s *Server) protocolList(ctx context.Context, req *ListTasksRequest) (*ListTasksResponse, *Error) {
	// 先按状态与上下文筛选，再执行稳定排序后的分页；时间与内容投影不改变持久状态。
	query := *req
	query.Limit = 0
	query.Offset = 0
	all, err := s.store.List(ctx, &query)
	if err != nil {
		return nil, ToA2AError(err)
	}
	tasks := make([]*Task, 0, len(all.Tasks))
	for _, task := range all.Tasks {
		if req.StatusTimestampAfter != nil && task.Status.Timestamp.Before(*req.StatusTimestampAfter) {
			continue
		}
		tasks = append(tasks, task)
	}
	slices.SortFunc(tasks, func(a, b *Task) int {
		if order := b.UpdatedAt.Compare(a.UpdatedAt); order != 0 {
			return order
		}
		return strings.Compare(b.ID, a.ID)
	})
	limit := req.Limit
	if limit == 0 {
		limit = 50
	}
	result := &ListTasksResponse{Tasks: make([]*Task, 0), Total: len(tasks), PageSize: limit}
	start := req.Offset
	if start > len(tasks) {
		start = len(tasks)
	}
	end := start + limit
	if end > len(tasks) {
		end = len(tasks)
	}
	for _, task := range tasks[start:end] {
		value := projectHistory(task, req.HistoryLimit)
		if !req.IncludeArtifacts {
			value.Artifacts = nil
		}
		result.Tasks = append(result.Tasks, value)
	}
	if end < len(tasks) {
		result.NextPageToken = strconv.Itoa(end)
	}
	return result, nil
}

func (s *Server) protocolCard() *AgentCard {
	card := *s.card
	card.Capabilities.PushNotifications = false
	card.SupportedInterfaces = []AgentInterface{*wire.NewAgentInterface(strings.TrimRight(card.URL, "/")+PathTasks, wire.TransportProtocolJSONRPC)}
	return &card
}

func normalizeMessage(message *Message, task *Task) {
	if message.ID == "" {
		message.ID = wire.NewMessageID()
	}
	message.TaskID, message.ContextID = task.ID, task.SessionID
}

func (s *Server) beginExecution(ctx context.Context, id string) (context.Context, *Task, func(), *Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.executions[id]; ok {
		return nil, nil, nil, NewUnsupportedOperationError("task already executing")
	}
	// 任务读取与执行准入共享临界区；锁外读取的旧快照不能重开已完成任务。
	task, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, nil, nil, ToA2AError(err)
	}
	if task.Status.State.IsTerminal() {
		return nil, nil, nil, NewUnsupportedOperationError("terminal tasks are immutable")
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.executions[id] = cancel
	return runCtx, task, func() { cancel(); s.mu.Lock(); delete(s.executions, id); s.mu.Unlock() }, nil
}

func (s *Server) commitTask(ctx context.Context, task *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.store.Get(context.WithoutCancel(ctx), task.ID)
	if err != nil {
		return err
	}
	if current.Status.State.IsTerminal() {
		*task = *current
		return nil
	}
	return s.store.Update(ctx, task)
}
