package a2a

import (
	"encoding/json"
	"time"

	wire "github.com/a2aproject/a2a-go/v2/a2a"
)

// ============== Agent Card ==============

// AgentCard Agent 元数据卡片
// 遵循 Google A2A 规范，位于 .well-known/agent-card.json
//
// Agent Card 是 A2A 协议的核心概念，描述了 Agent 的基本信息、能力和技能。
// 客户端通过获取 Agent Card 来了解如何与 Agent 交互。
type AgentCard struct {
	SecuritySchemes      wire.NamedSecuritySchemes        `json:"securitySchemes,omitempty"`
	SecurityRequirements wire.SecurityRequirementsOptions `json:"securityRequirements,omitempty"`
	Signatures           []wire.AgentCardSignature        `json:"signatures,omitempty"`
	IconURL              string                           `json:"iconUrl,omitempty"`
	// SupportedInterfaces 仅声明实际提供的协议绑定与端点。
	SupportedInterfaces []AgentInterface `json:"supportedInterfaces,omitempty"`
	// Name Agent 名称（必需）
	Name string `json:"name"`

	// Description Agent 描述
	Description string `json:"description,omitempty"`

	// URL Agent 服务地址（必需）
	URL string `json:"url"`

	// Provider Agent 提供者信息
	Provider *AgentProvider `json:"provider,omitempty"`

	// Version 版本号
	Version string `json:"version"`

	// DocumentationURL 文档地址
	DocumentationURL string `json:"documentationUrl,omitempty"`

	// Capabilities Agent 能力
	Capabilities AgentCapabilities `json:"capabilities"`

	// Authentication 认证配置
	Authentication *AuthConfig `json:"authentication,omitempty"`

	// DefaultInputModes 默认输入模式 (text, file, data)
	DefaultInputModes []string `json:"defaultInputModes,omitempty"`

	// DefaultOutputModes 默认输出模式 (text, file, data)
	DefaultOutputModes []string `json:"defaultOutputModes,omitempty"`

	// Skills Agent 技能列表
	Skills []AgentSkill `json:"skills,omitempty"`
}

// AgentProvider Agent 提供者信息
type AgentProvider struct {
	// Organization 组织名称
	Organization string `json:"organization"`

	// URL 组织网站
	URL string `json:"url,omitempty"`
}

// AgentCapabilities Agent 能力配置
type AgentCapabilities struct {
	Extensions []wire.AgentExtension `json:"extensions,omitempty"`
	// Streaming 是否支持流式响应
	Streaming bool `json:"streaming,omitempty"`

	// PushNotifications 是否支持推送通知
	PushNotifications bool `json:"pushNotifications,omitempty"`

	// StateTransitionHistory 是否支持状态转换历史
	StateTransitionHistory bool `json:"stateTransitionHistory,omitempty"`
}

// AgentSkill Agent 技能
type AgentSkill struct {
	SecurityRequirements wire.SecurityRequirementsOptions `json:"securityRequirements,omitempty"`
	// ID 技能 ID
	ID string `json:"id"`

	// Name 技能名称
	Name string `json:"name"`

	// Description 技能描述
	Description string `json:"description,omitempty"`

	// Tags 标签列表
	Tags []string `json:"tags,omitempty"`

	// Examples 使用示例
	Examples []string `json:"examples,omitempty"`

	// InputModes 支持的输入模式
	InputModes []string `json:"inputModes,omitempty"`

	// OutputModes 支持的输出模式
	OutputModes []string `json:"outputModes,omitempty"`
}

// AuthConfig 认证配置
type AuthConfig struct {
	// Schemes 支持的认证方案列表
	Schemes []AuthScheme `json:"schemes"`
}

// AuthScheme 认证方案
type AuthScheme struct {
	// Type 认证类型 (apiKey, bearer, oauth2, basic)
	Type string `json:"type"`

	// In 参数位置 (header, query) - 用于 apiKey
	In string `json:"in,omitempty"`

	// Name 参数名称 - 用于 apiKey
	Name string `json:"name,omitempty"`

	// BearerFormat Bearer 格式 - 用于 bearer
	BearerFormat string `json:"bearerFormat,omitempty"`

	// Flows OAuth2 流程 - 用于 oauth2
	Flows *OAuth2Flows `json:"flows,omitempty"`
}

// OAuth2Flows OAuth2 流程配置
type OAuth2Flows struct {
	// AuthorizationCode 授权码流程
	AuthorizationCode *OAuth2Flow `json:"authorizationCode,omitempty"`

	// ClientCredentials 客户端凭证流程
	ClientCredentials *OAuth2Flow `json:"clientCredentials,omitempty"`
}

// OAuth2Flow OAuth2 流程详情
type OAuth2Flow struct {
	// AuthorizationURL 授权地址
	AuthorizationURL string `json:"authorizationUrl,omitempty"`

	// TokenURL Token 地址
	TokenURL string `json:"tokenUrl"`

	// Scopes 作用域
	Scopes map[string]string `json:"scopes,omitempty"`
}

// ============== Task ==============

// Task 任务定义
// 任务是 A2A 协议中的核心工作单元，表示一次 Agent 交互的完整生命周期。
type Task struct {
	// ID 任务 ID（必需）
	ID string `json:"id"`

	// SessionID 会话 ID（可选，用于关联多个任务）
	SessionID string `json:"sessionId,omitempty"`

	// Status 任务状态
	Status TaskStatus `json:"status"`

	// History 消息历史
	History []Message `json:"history,omitempty"`

	// Artifacts 任务产物
	Artifacts []Artifact `json:"artifacts,omitempty"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// CreatedAt 创建时间
	CreatedAt time.Time `json:"createdAt"`

	// UpdatedAt 更新时间
	UpdatedAt time.Time `json:"updatedAt"`
}

// TaskStatus 任务状态
type TaskStatus struct {
	// State 状态值
	State TaskState `json:"state"`

	// Message 状态消息（可选）
	Message *Message `json:"message,omitempty"`

	// Timestamp 状态更新时间
	Timestamp time.Time `json:"timestamp"`
}

// TaskState 任务状态枚举
type TaskState string

const (
	// TaskStateSubmitted 已提交 - 任务已创建，等待处理
	TaskStateSubmitted TaskState = "submitted"

	// TaskStateWorking 处理中 - Agent 正在处理任务
	TaskStateWorking TaskState = "working"

	// TaskStateInputRequired 需要输入 - 等待用户提供更多信息
	TaskStateInputRequired TaskState = "input-required"

	// TaskStateCompleted 已完成 - 任务成功完成
	TaskStateCompleted TaskState = "completed"

	// TaskStateFailed 已失败 - 任务执行失败
	TaskStateFailed TaskState = "failed"

	// TaskStateCanceled 已取消 - 任务被取消
	TaskStateCanceled     TaskState = "canceled"
	TaskStateRejected     TaskState = "rejected"
	TaskStateAuthRequired TaskState = "auth-required"
)

// IsTerminal 检查状态是否为终态
func (s TaskState) IsTerminal() bool {
	return s == TaskStateCompleted || s == TaskStateFailed || s == TaskStateCanceled || s == TaskStateRejected
}

// IsActive 检查状态是否为活动状态
func (s TaskState) IsActive() bool {
	return s == TaskStateSubmitted || s == TaskStateWorking || s == TaskStateInputRequired || s == TaskStateAuthRequired
}

// ============== Message ==============

// Role 消息角色
type Role string

const (
	// RoleUser 用户角色
	RoleUser Role = "user"

	// RoleAgent Agent 角色
	RoleAgent Role = "agent"
)

// Message 消息
// 消息是 Agent 与用户之间的通信单元，支持多模态内容。
type Message struct {
	ID               string   `json:"messageId"`
	TaskID           string   `json:"taskId,omitempty"`
	ContextID        string   `json:"contextId,omitempty"`
	Extensions       []string `json:"extensions,omitempty"`
	ReferenceTaskIDs []string `json:"referenceTaskIds,omitempty"`
	// Role 消息角色 (user, agent)
	Role Role `json:"role"`

	// Parts 消息部分（多模态支持）
	Parts []Part `json:"parts"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// GetTextContent 获取消息的纯文本内容
func (m *Message) GetTextContent() string {
	var result string
	for _, part := range m.Parts {
		if tp, ok := part.(*TextPart); ok {
			if result != "" {
				result += "\n"
			}
			result += tp.Text
		}
	}
	return result
}

// UnmarshalJSON 自定义 JSON 反序列化
func (m *Message) UnmarshalJSON(data []byte) error {
	var value wire.Message
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*m = *localMessage(&value)
	return nil
}

// ============== Part (消息部分) ==============

// PartType 消息部分类型
type PartType string

const (
	// PartTypeText 文本类型
	PartTypeText PartType = "text"

	// PartTypeFile 文件类型
	PartTypeFile PartType = "file"

	// PartTypeData 数据类型
	PartTypeData PartType = "data"
)

// Part 消息部分接口
// 支持多模态内容：文本、文件、结构化数据
type Part interface {
	// Type 返回部分类型
	Type() PartType
}

// TextPart 文本部分
type TextPart struct {
	PartMetadata
	// Text 文本内容
	Text string `json:"text"`
}

// Type 返回文本类型
func (p *TextPart) Type() PartType {
	return PartTypeText
}

// MarshalJSON 自定义 JSON 序列化
func (p *TextPart) MarshalJSON() ([]byte, error) {
	value, err := wirePart(p)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// FilePart 文件部分
type FilePart struct {
	PartMetadata
	// File 文件内容
	File FileContent `json:"file"`
}

// Type 返回文件类型
func (p *FilePart) Type() PartType {
	return PartTypeFile
}

// MarshalJSON 自定义 JSON 序列化
func (p *FilePart) MarshalJSON() ([]byte, error) {
	value, err := wirePart(p)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// FileContent 文件内容
type FileContent struct {
	// Name 文件名
	Name string `json:"name,omitempty"`

	// MimeType MIME 类型
	MimeType string `json:"mimeType,omitempty"`

	// URI 文件 URI（与 Bytes 二选一）
	URI string `json:"uri,omitempty"`

	// Bytes Base64 编码的文件内容（与 URI 二选一）
	Bytes string `json:"bytes,omitempty"`
}

// DataPart 结构化数据部分
type DataPart struct {
	PartMetadata
	// Data 结构化数据
	Data any `json:"data"`
}

// Type 返回数据类型
func (p *DataPart) Type() PartType {
	return PartTypeData
}

// MarshalJSON 自定义 JSON 序列化
func (p *DataPart) MarshalJSON() ([]byte, error) {
	value, err := wirePart(p)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// UnmarshalPart 从 JSON 反序列化 Part
func UnmarshalPart(data []byte) (Part, error) {
	var value wire.Part
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, ErrInvalidMessage
	}
	result := localPart(&value)
	if result == nil {
		return nil, ErrInvalidMessage
	}
	return result, nil
}

// UnmarshalParts 从 JSON 数组反序列化 Parts
func UnmarshalParts(data []byte) ([]Part, error) {
	var rawParts []json.RawMessage
	if err := json.Unmarshal(data, &rawParts); err != nil {
		return nil, err
	}

	parts := make([]Part, 0, len(rawParts))
	for _, rawPart := range rawParts {
		part, err := UnmarshalPart(rawPart)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, nil
}

// ============== Artifact ==============

// Artifact 任务产物
// 产物是任务执行过程中生成的输出，可以是文本、文件或结构化数据。
type Artifact struct {
	ID         string   `json:"artifactId"`
	Extensions []string `json:"extensions,omitempty"`
	// Name 产物名称
	Name string `json:"name,omitempty"`

	// Description 产物描述
	Description string `json:"description,omitempty"`

	// Parts 产物内容部分
	Parts []Part `json:"parts"`

	// Index 产物索引（用于多个产物）
	Index int `json:"index,omitempty"`

	// Append 是否追加到已有产物
	Append bool `json:"append,omitempty"`

	// LastChunk 是否为最后一块（用于流式）
	LastChunk bool `json:"lastChunk,omitempty"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// GetTextContent 获取产物的纯文本内容
func (a *Artifact) GetTextContent() string {
	var result string
	for _, part := range a.Parts {
		if tp, ok := part.(*TextPart); ok {
			if result != "" {
				result += "\n"
			}
			result += tp.Text
		}
	}
	return result
}

// UnmarshalJSON 自定义 JSON 反序列化
func (a *Artifact) UnmarshalJSON(data []byte) error {
	var value wire.Artifact
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*a = localArtifact(&value)
	return nil
}

// ============== Push Notification ==============

// PushNotificationConfig 推送通知配置
type PushNotificationConfig struct {
	// URL 推送目标 URL
	URL string `json:"url"`

	// Token 认证 Token（可选）
	Token string `json:"token,omitempty"`

	// Authentication 认证配置（可选）
	Authentication *PushAuth `json:"authentication,omitempty"`
}

// PushAuth 推送认证配置
type PushAuth struct {
	// Schemes 支持的认证方案
	Schemes []string `json:"schemes,omitempty"`

	// Credentials 凭证信息
	Credentials string `json:"credentials,omitempty"`
}

// ============== JSON-RPC ==============

// JSONRPCRequest JSON-RPC 2.0 请求
type JSONRPCRequest struct {
	// JSONRPC 版本（必须为 "2.0"）
	JSONRPC string `json:"jsonrpc"`

	// ID 请求 ID
	ID any `json:"id,omitempty"`

	// Method 方法名
	Method string `json:"method"`

	// Params 参数
	Params json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse JSON-RPC 2.0 响应
type JSONRPCResponse struct {
	// JSONRPC 版本（必须为 "2.0"）
	JSONRPC string `json:"jsonrpc"`

	// ID 请求 ID
	ID any `json:"id,omitempty"`

	// Result 结果（成功时）
	Result json.RawMessage `json:"result,omitempty"`

	// Error 错误（失败时）
	Error *Error `json:"error,omitempty"`
}

// NewJSONRPCRequest 创建 JSON-RPC 请求
func NewJSONRPCRequest(id any, method string, params any) (*JSONRPCRequest, error) {
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
	}

	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		req.Params = data
	}

	return req, nil
}

// NewJSONRPCResponse 创建成功响应
func NewJSONRPCResponse(id any, result any) (*JSONRPCResponse, error) {
	resp := &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
	}

	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		resp.Result = data
	}

	return resp, nil
}

// NewJSONRPCErrorResponse 创建错误响应
func NewJSONRPCErrorResponse(id any, err *Error) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   err,
	}
}

// ============== API 请求/响应类型 ==============

// SendMessageRequest 发送消息请求
type SendMessageRequest struct {
	// ReturnImmediately 请求异步返回；不支持的服务端必须明确返回错误。
	ReturnImmediately bool `json:"-"`
	// HistoryLimit 区分未指定历史与显式零条历史。
	HistoryLimit *int `json:"-"`
	// TaskID 任务 ID（可选，不提供则创建新任务）
	TaskID string `json:"id,omitempty"`

	// SessionID 会话 ID（可选）
	SessionID string `json:"sessionId,omitempty"`

	// Message 消息内容
	Message Message `json:"message"`

	// AcceptedOutputModes 接受的输出模式
	AcceptedOutputModes []string `json:"acceptedOutputModes,omitempty"`

	// PushNotification 推送通知配置（可选）
	PushNotification *PushNotificationConfig `json:"pushNotification,omitempty"`

	// HistoryLength 返回的历史消息数量
	HistoryLength int `json:"historyLength,omitempty"`

	// Metadata 元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// SendMessageResponse 发送消息响应
// 直接返回 Task 对象
type SendMessageResponse = Task

// GetTaskRequest 获取任务请求
type GetTaskRequest struct {
	HistoryLimit *int `json:"-"`
	// ID 任务 ID
	ID string `json:"id"`

	// HistoryLength 返回的历史消息数量
	HistoryLength int `json:"historyLength,omitempty"`
}

// GetTaskResponse 获取任务响应
type GetTaskResponse = Task

// ListTasksRequest 列出任务请求
type ListTasksRequest struct {
	PageToken            string     `json:"pageToken,omitempty"`
	HistoryLimit         *int       `json:"-"`
	IncludeArtifacts     bool       `json:"includeArtifacts,omitempty"`
	StatusTimestampAfter *time.Time `json:"statusTimestampAfter,omitempty"`
	// SessionID 会话 ID（可选，过滤条件）
	SessionID string `json:"sessionId,omitempty"`

	// Status 状态过滤（可选）
	Status []TaskState `json:"status,omitempty"`

	// Limit 返回数量限制
	Limit int `json:"limit,omitempty"`

	// Offset 偏移量
	Offset int `json:"offset,omitempty"`
}

// ListTasksResponse 列出任务响应
type ListTasksResponse struct {
	PageSize      int    `json:"pageSize"`
	NextPageToken string `json:"nextPageToken"`
	// Tasks 任务列表
	Tasks []*Task `json:"tasks"`

	// Total 总数
	Total int `json:"total,omitempty"`
}

// CancelTaskRequest 取消任务请求
type CancelTaskRequest struct {
	Metadata map[string]any `json:"metadata,omitempty"`
	// ID 任务 ID
	ID string `json:"id"`
}

// CancelTaskResponse 取消任务响应
type CancelTaskResponse = Task

// SetPushNotificationRequest 设置推送通知请求
type SetPushNotificationRequest struct {
	// TaskID 任务 ID
	TaskID string `json:"id"`

	// Config 推送配置
	Config PushNotificationConfig `json:"pushNotificationConfig"`
}

// SetPushNotificationResponse 设置推送通知响应
type SetPushNotificationResponse struct {
	// Config 推送配置
	Config PushNotificationConfig `json:"pushNotificationConfig"`
}

// GetPushNotificationRequest 获取推送通知请求
type GetPushNotificationRequest struct {
	// TaskID 任务 ID
	TaskID string `json:"id"`
}

// GetPushNotificationResponse 获取推送通知响应
type GetPushNotificationResponse struct {
	// Config 推送配置
	Config *PushNotificationConfig `json:"pushNotificationConfig,omitempty"`
}

// ResubscribeRequest 重新订阅请求
type ResubscribeRequest struct {
	// TaskID 任务 ID
	TaskID string `json:"id"`
}

// ============== SSE 事件 ==============

// StreamEvent 流式事件接口
type StreamEvent interface {
	// EventType 返回事件类型
	EventType() string
}

// TaskStatusEvent 任务状态事件
type TaskStatusEvent struct {
	ContextID string         `json:"contextId"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	// ID 任务 ID
	ID string `json:"id"`

	// Status 任务状态
	Status TaskStatus `json:"status"`

	// Final 是否为最终状态
	Final bool `json:"final,omitempty"`
}

// EventType 返回事件类型
func (e *TaskStatusEvent) EventType() string {
	return EventTypeTaskStatus
}

// ArtifactEvent 产物事件
type ArtifactEvent struct {
	ContextID string         `json:"contextId"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	// ID 任务 ID
	ID string `json:"id"`

	// Artifact 产物
	Artifact Artifact `json:"artifact"`
}

// EventType 返回事件类型
func (e *ArtifactEvent) EventType() string {
	return EventTypeArtifact
}

// ErrorEvent 错误事件
type ErrorEvent struct {
	// Error 错误信息
	Error *Error `json:"error"`
}

// EventType 返回事件类型
func (e *ErrorEvent) EventType() string {
	return EventTypeError
}

// DoneEvent 完成事件
type DoneEvent struct {
	// Task 最终任务状态
	Task *Task `json:"task,omitempty"`
}

// EventType 返回事件类型
func (e *DoneEvent) EventType() string {
	return EventTypeDone
}

// ============== 便捷构造函数 ==============

// NewTask 创建新任务
func NewTask(id string) *Task {
	now := time.Now()
	return &Task{
		ID:        id,
		SessionID: wire.NewContextID(),
		Status: TaskStatus{
			State:     TaskStateSubmitted,
			Timestamp: now,
		},
		History:   make([]Message, 0),
		Artifacts: make([]Artifact, 0),
		Metadata:  make(map[string]any),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// NewTextMessage 创建文本消息
func NewTextMessage(role Role, text string) Message {
	return Message{
		ID:   wire.NewMessageID(),
		Role: role,
		Parts: []Part{
			&TextPart{Text: text},
		},
	}
}

// NewUserMessage 创建用户消息
func NewUserMessage(text string) Message {
	return NewTextMessage(RoleUser, text)
}

// NewAgentMessage 创建 Agent 消息
func NewAgentMessage(text string) Message {
	return NewTextMessage(RoleAgent, text)
}

// NewTextArtifact 创建文本产物
func NewTextArtifact(name, text string) Artifact {
	return Artifact{
		ID:   string(wire.NewArtifactID()),
		Name: name,
		Parts: []Part{
			&TextPart{Text: text},
		},
	}
}

// NewAgentCard 创建 Agent Card
func NewAgentCard(name, url, version string) *AgentCard {
	return &AgentCard{
		Name:    name,
		URL:     url,
		Version: version,
		Capabilities: AgentCapabilities{
			Streaming: true,
		},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
	}
}
