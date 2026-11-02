package a2a

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	wire "github.com/a2aproject/a2a-go/v2/a2a"
)

// AgentInterface 描述 A2A 1.0 的实际绑定；当前服务端只提供 JSONRPC。
type AgentInterface = wire.AgentInterface

// PartMetadata 为所有内容类型保留文件名、媒体类型与扩展元数据。
type PartMetadata struct {
	Filename  string         `json:"filename,omitempty"`
	MediaType string         `json:"mediaType,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func wireState(s TaskState) wire.TaskState {
	return wire.TaskState("TASK_STATE_" + strings.ToUpper(strings.ReplaceAll(string(s), "-", "_")))
}

func localState(s wire.TaskState) TaskState {
	return TaskState(strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(string(s), "TASK_STATE_"), "_", "-")))
}

func wirePart(p Part) (*wire.Part, error) {
	var result wire.Part
	var meta PartMetadata
	switch v := p.(type) {
	case *TextPart:
		result.Content, meta = wire.Text(v.Text), v.PartMetadata
	case *DataPart:
		result.Content, meta = wire.Data{Value: v.Data}, v.PartMetadata
	case *FilePart:
		meta = v.PartMetadata
		if meta.Filename == "" {
			meta.Filename = v.File.Name
		}
		if meta.MediaType == "" {
			meta.MediaType = v.File.MimeType
		}
		if v.File.URI != "" && v.File.Bytes != "" {
			return nil, fmt.Errorf("file part must contain exactly one of URL or raw bytes")
		}
		if v.File.URI != "" {
			result.Content = wire.URL(v.File.URI)
		} else {
			raw, err := base64.StdEncoding.DecodeString(v.File.Bytes)
			if err != nil {
				return nil, fmt.Errorf("invalid file base64: %w", err)
			}
			result.Content = wire.Raw(raw)
		}
	default:
		return nil, fmt.Errorf("unsupported part type %T", p)
	}
	result.Filename, result.MediaType, result.Metadata = meta.Filename, meta.MediaType, meta.Metadata
	return &result, nil
}

func localPart(p *wire.Part) Part {
	meta := PartMetadata{Filename: p.Filename, MediaType: p.MediaType, Metadata: p.Metadata}
	switch content := p.Content.(type) {
	case wire.Text:
		return &TextPart{Text: string(content), PartMetadata: meta}
	case wire.Data:
		return &DataPart{Data: content.Value, PartMetadata: meta}
	case wire.Raw:
		return &FilePart{File: FileContent{Name: p.Filename, MimeType: p.MediaType, Bytes: base64.StdEncoding.EncodeToString(content)}, PartMetadata: meta}
	case wire.URL:
		return &FilePart{File: FileContent{Name: p.Filename, MimeType: p.MediaType, URI: string(content)}, PartMetadata: meta}
	default:
		return nil
	}
}

func wireParts(parts []Part) (wire.ContentParts, error) {
	result := make(wire.ContentParts, 0, len(parts))
	for _, p := range parts {
		part, err := wirePart(p)
		if err != nil {
			return nil, err
		}
		result = append(result, part)
	}
	return result, nil
}

func localParts(parts wire.ContentParts) []Part {
	result := make([]Part, 0, len(parts))
	for _, p := range parts {
		if p != nil {
			result = append(result, localPart(p))
		}
	}
	return result
}

func wireMessage(m *Message) (*wire.Message, error) {
	if m == nil {
		return nil, nil
	}
	parts, err := wireParts(m.Parts)
	if err != nil {
		return nil, err
	}
	if m.Role != RoleUser && m.Role != RoleAgent {
		return nil, fmt.Errorf("invalid message role")
	}
	role := wire.MessageRoleUser
	if m.Role == RoleAgent {
		role = wire.MessageRoleAgent
	}
	refs := make([]wire.TaskID, len(m.ReferenceTaskIDs))
	for i, id := range m.ReferenceTaskIDs {
		refs[i] = wire.TaskID(id)
	}
	return &wire.Message{ID: m.ID, TaskID: wire.TaskID(m.TaskID), ContextID: m.ContextID, Role: role, Parts: parts, Metadata: m.Metadata, Extensions: m.Extensions, ReferenceTasks: refs}, nil
}

func localMessage(m *wire.Message) *Message {
	if m == nil {
		return nil
	}
	role := RoleUser
	if m.Role == wire.MessageRoleAgent {
		role = RoleAgent
	}
	refs := make([]string, len(m.ReferenceTasks))
	for i, id := range m.ReferenceTasks {
		refs[i] = string(id)
	}
	return &Message{ID: m.ID, TaskID: string(m.TaskID), ContextID: m.ContextID, Role: role, Parts: localParts(m.Parts), Metadata: m.Metadata, Extensions: m.Extensions, ReferenceTaskIDs: refs}
}

func wireArtifact(a *Artifact) (*wire.Artifact, error) {
	parts, err := wireParts(a.Parts)
	if err != nil {
		return nil, err
	}
	return &wire.Artifact{ID: wire.ArtifactID(a.ID), Name: a.Name, Description: a.Description, Parts: parts, Metadata: a.Metadata, Extensions: a.Extensions}, nil
}

func localArtifact(a *wire.Artifact) Artifact {
	return Artifact{ID: string(a.ID), Name: a.Name, Description: a.Description, Parts: localParts(a.Parts), Metadata: a.Metadata, Extensions: a.Extensions}
}

func wireStatus(s TaskStatus) (wire.TaskStatus, error) {
	message, err := wireMessage(s.Message)
	result := wire.TaskStatus{State: wireState(s.State), Message: message}
	if !s.Timestamp.IsZero() {
		result.Timestamp = &s.Timestamp
	}
	return result, err
}

func localStatus(s wire.TaskStatus) TaskStatus {
	result := TaskStatus{State: localState(s.State), Message: localMessage(s.Message)}
	if s.Timestamp != nil {
		result.Timestamp = *s.Timestamp
	}
	return result
}

func wireTask(t *Task) (*wire.Task, error) {
	if t == nil {
		return nil, nil
	}
	status, err := wireStatus(t.Status)
	if err != nil {
		return nil, err
	}
	result := &wire.Task{ID: wire.TaskID(t.ID), ContextID: t.SessionID, Status: status, Metadata: t.Metadata}
	for i := range t.History {
		m, err := wireMessage(&t.History[i])
		if err != nil {
			return nil, err
		}
		result.History = append(result.History, m)
	}
	for i := range t.Artifacts {
		a, err := wireArtifact(&t.Artifacts[i])
		if err != nil {
			return nil, err
		}
		result.Artifacts = append(result.Artifacts, a)
	}
	return result, nil
}

func localTask(t *wire.Task) *Task {
	if t == nil {
		return nil
	}
	result := &Task{ID: string(t.ID), SessionID: t.ContextID, Status: localStatus(t.Status), Metadata: t.Metadata}
	for _, m := range t.History {
		if m != nil {
			result.History = append(result.History, *localMessage(m))
		}
	}
	for _, a := range t.Artifacts {
		if a != nil {
			result.Artifacts = append(result.Artifacts, localArtifact(a))
		}
	}
	return result
}

func (m Message) MarshalJSON() ([]byte, error) {
	value, err := wireMessage(&m)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func (t Task) MarshalJSON() ([]byte, error) {
	value, err := wireTask(&t)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func (t *Task) UnmarshalJSON(data []byte) error {
	var value wire.Task
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*t = *localTask(&value)
	return nil
}
func (a Artifact) MarshalJSON() ([]byte, error) {
	value, err := wireArtifact(&a)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func historyLimit(explicit *int, legacy int) *int {
	if explicit != nil {
		return explicit
	}
	if legacy > 0 {
		return &legacy
	}
	return nil
}

func (r SendMessageRequest) MarshalJSON() ([]byte, error) {
	m, err := wireMessage(&r.Message)
	if err != nil {
		return nil, err
	}
	if m.ID == "" {
		m.ID = wire.NewMessageID()
	}
	if r.TaskID != "" {
		m.TaskID = wire.TaskID(r.TaskID)
	}
	if r.SessionID != "" {
		m.ContextID = r.SessionID
	}
	if r.PushNotification != nil {
		return nil, fmt.Errorf("A2A 1.0 push configuration is not implemented")
	}
	return json.Marshal(&wire.SendMessageRequest{Message: m, Metadata: r.Metadata, Config: &wire.SendMessageConfig{AcceptedOutputModes: r.AcceptedOutputModes, HistoryLength: historyLimit(r.HistoryLimit, r.HistoryLength), ReturnImmediately: r.ReturnImmediately}})
}

func (r *SendMessageRequest) UnmarshalJSON(data []byte) error {
	var value wire.SendMessageRequest
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Message == nil || value.Message.ID == "" || len(value.Message.Parts) == 0 {
		return fmt.Errorf("messageId and message parts are required")
	}
	if value.Message.Role != wire.MessageRoleUser && value.Message.Role != wire.MessageRoleAgent {
		return fmt.Errorf("invalid message role")
	}
	*r = SendMessageRequest{Message: *localMessage(value.Message), TaskID: string(value.Message.TaskID), SessionID: value.Message.ContextID, Metadata: value.Metadata}
	if value.Tenant != "" {
		return fmt.Errorf("tenant routing is not implemented")
	}
	if value.Config != nil {
		if value.Config.PushConfig != nil {
			return NewPushNotSupportedError()
		}
		r.AcceptedOutputModes = value.Config.AcceptedOutputModes
		r.HistoryLimit = value.Config.HistoryLength
		r.ReturnImmediately = value.Config.ReturnImmediately
		if r.HistoryLimit != nil && *r.HistoryLimit < 0 {
			return fmt.Errorf("historyLength must be non-negative")
		}
	}
	return nil
}

func (r GetTaskRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(wire.GetTaskRequest{ID: wire.TaskID(r.ID), HistoryLength: historyLimit(r.HistoryLimit, r.HistoryLength)})
}
func (r *GetTaskRequest) UnmarshalJSON(data []byte) error {
	var v wire.GetTaskRequest
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.Tenant != "" {
		return fmt.Errorf("tenant routing is not implemented")
	}
	*r = GetTaskRequest{ID: string(v.ID), HistoryLimit: v.HistoryLength}
	return nil
}

// MessageResult 保留 SendMessage 的 Task/Message 联合结果，不把无任务回复伪造成任务。
type MessageResult struct {
	Task    *Task
	Message *Message
}

func (r MessageResult) MarshalJSON() ([]byte, error) {
	if (r.Task == nil) == (r.Message == nil) {
		return nil, fmt.Errorf("exactly one task or message is required")
	}
	if r.Task != nil {
		t, err := wireTask(r.Task)
		if err != nil {
			return nil, err
		}
		return json.Marshal(wire.StreamResponse{Event: t})
	}
	m, err := wireMessage(r.Message)
	if err != nil {
		return nil, err
	}
	return json.Marshal(wire.StreamResponse{Event: m})
}
func (r *MessageResult) UnmarshalJSON(data []byte) error {
	var value wire.StreamResponse
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*r = MessageResult{}
	switch v := value.Event.(type) {
	case *wire.Task:
		r.Task = localTask(v)
	case *wire.Message:
		r.Message = localMessage(v)
	default:
		return fmt.Errorf("SendMessage returned an invalid event")
	}
	return nil
}

// MessageEvent 保留流式无任务消息。
type MessageEvent struct{ Message *Message }

func (*MessageEvent) EventType() string { return "message" }

// TaskEvent 表示流式任务快照，终态与执行中快照由任务状态区分。
type TaskEvent struct{ Task *Task }

func (*TaskEvent) EventType() string { return "task" }

func (c AgentCard) MarshalJSON() ([]byte, error) {
	interfaces := make([]*wire.AgentInterface, 0, len(c.SupportedInterfaces))
	for i := range c.SupportedInterfaces {
		v := c.SupportedInterfaces[i]
		interfaces = append(interfaces, &v)
	}
	if len(interfaces) == 0 {
		interfaces = append(interfaces, wire.NewAgentInterface(strings.TrimRight(c.URL, "/")+PathTasks, wire.TransportProtocolJSONRPC))
	}
	v := wire.AgentCard{SecuritySchemes: c.SecuritySchemes, SecurityRequirements: c.SecurityRequirements, Signatures: c.Signatures, IconURL: c.IconURL, Name: c.Name, Description: c.Description, Version: c.Version, DocumentationURL: c.DocumentationURL, SupportedInterfaces: interfaces, Capabilities: wire.AgentCapabilities{Streaming: c.Capabilities.Streaming, Extensions: c.Capabilities.Extensions}, DefaultInputModes: c.DefaultInputModes, DefaultOutputModes: c.DefaultOutputModes, Skills: make([]wire.AgentSkill, 0, len(c.Skills))}
	if c.Provider != nil {
		v.Provider = &wire.AgentProvider{Org: c.Provider.Organization, URL: c.Provider.URL}
	}
	for _, skill := range c.Skills {
		v.Skills = append(v.Skills, wire.AgentSkill{SecurityRequirements: skill.SecurityRequirements, ID: skill.ID, Name: skill.Name, Description: skill.Description, Tags: skill.Tags, Examples: skill.Examples, InputModes: skill.InputModes, OutputModes: skill.OutputModes})
	}
	return json.Marshal(v)
}
func (c *AgentCard) UnmarshalJSON(data []byte) error {
	var v wire.AgentCard
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*c = AgentCard{SecuritySchemes: v.SecuritySchemes, SecurityRequirements: v.SecurityRequirements, Signatures: v.Signatures, IconURL: v.IconURL, Name: v.Name, Description: v.Description, Version: v.Version, DocumentationURL: v.DocumentationURL, Capabilities: AgentCapabilities{Streaming: v.Capabilities.Streaming, PushNotifications: v.Capabilities.PushNotifications, Extensions: v.Capabilities.Extensions}, DefaultInputModes: v.DefaultInputModes, DefaultOutputModes: v.DefaultOutputModes}
	for _, iface := range v.SupportedInterfaces {
		if iface != nil {
			c.SupportedInterfaces = append(c.SupportedInterfaces, *iface)
			if c.URL == "" && iface.ProtocolBinding == wire.TransportProtocolJSONRPC {
				c.URL = iface.URL
			}
		}
	}
	if v.Provider != nil {
		c.Provider = &AgentProvider{Organization: v.Provider.Org, URL: v.Provider.URL}
	}
	for _, skill := range v.Skills {
		c.Skills = append(c.Skills, AgentSkill{SecurityRequirements: skill.SecurityRequirements, ID: skill.ID, Name: skill.Name, Description: skill.Description, Tags: skill.Tags, Examples: skill.Examples, InputModes: skill.InputModes, OutputModes: skill.OutputModes})
	}
	return nil
}

func (r ListTasksRequest) MarshalJSON() ([]byte, error) {
	if len(r.Status) > 1 {
		return nil, fmt.Errorf("A2A 1.0 accepts one task status filter")
	}
	v := wire.ListTasksRequest{ContextID: r.SessionID, PageSize: r.Limit, PageToken: r.PageToken, HistoryLength: r.HistoryLimit, IncludeArtifacts: r.IncludeArtifacts, StatusTimestampAfter: r.StatusTimestampAfter}
	if len(r.Status) > 0 {
		v.Status = wireState(r.Status[0])
	}
	if v.PageToken == "" && r.Offset > 0 {
		v.PageToken = strconv.Itoa(r.Offset)
	}
	return json.Marshal(v)
}
func (r *ListTasksRequest) UnmarshalJSON(data []byte) error {
	var v wire.ListTasksRequest
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.Tenant != "" {
		return fmt.Errorf("tenant routing is not implemented")
	}
	*r = ListTasksRequest{SessionID: v.ContextID, Limit: v.PageSize, PageToken: v.PageToken, HistoryLimit: v.HistoryLength, IncludeArtifacts: v.IncludeArtifacts, StatusTimestampAfter: v.StatusTimestampAfter}
	if v.Status != "" {
		r.Status = []TaskState{localState(v.Status)}
	}
	if v.PageToken != "" {
		offset, err := strconv.Atoi(v.PageToken)
		if err != nil || offset < 0 {
			return fmt.Errorf("invalid page token")
		}
		r.Offset = offset
	}
	if r.Limit < 0 || r.Limit > 100 || (r.HistoryLimit != nil && *r.HistoryLimit < 0) {
		return fmt.Errorf("invalid list limits")
	}
	return nil
}
func (r ListTasksResponse) MarshalJSON() ([]byte, error) {
	v := wire.ListTasksResponse{Tasks: make([]*wire.Task, 0, len(r.Tasks)), TotalSize: r.Total, PageSize: r.PageSize, NextPageToken: r.NextPageToken}
	for _, task := range r.Tasks {
		t, err := wireTask(task)
		if err != nil {
			return nil, err
		}
		v.Tasks = append(v.Tasks, t)
	}
	return json.Marshal(v)
}
func (r *ListTasksResponse) UnmarshalJSON(data []byte) error {
	var v wire.ListTasksResponse
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*r = ListTasksResponse{Total: v.TotalSize, PageSize: v.PageSize, NextPageToken: v.NextPageToken, Tasks: make([]*Task, 0, len(v.Tasks))}
	for _, task := range v.Tasks {
		r.Tasks = append(r.Tasks, localTask(task))
	}
	return nil
}
