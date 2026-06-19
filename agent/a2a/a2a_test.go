package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hexagon-codes/toolkit/util/rate"
)

// ============== 类型测试 ==============

func TestTaskState(t *testing.T) {
	tests := []struct {
		state      TaskState
		isTerminal bool
		isActive   bool
	}{
		{TaskStateSubmitted, false, true},
		{TaskStateWorking, false, true},
		{TaskStateInputRequired, false, true},
		{TaskStateCompleted, true, false},
		{TaskStateFailed, true, false},
		{TaskStateCanceled, true, false},
	}

	for _, tt := range tests {
		if got := tt.state.IsTerminal(); got != tt.isTerminal {
			t.Errorf("TaskState(%s).IsTerminal() = %v, want %v", tt.state, got, tt.isTerminal)
		}
		if got := tt.state.IsActive(); got != tt.isActive {
			t.Errorf("TaskState(%s).IsActive() = %v, want %v", tt.state, got, tt.isActive)
		}
	}
}

func TestNewTask(t *testing.T) {
	task := NewTask("test-123")

	if task.ID != "test-123" {
		t.Errorf("NewTask().ID = %v, want test-123", task.ID)
	}
	if task.Status.State != TaskStateSubmitted {
		t.Errorf("NewTask().Status.State = %v, want %v", task.Status.State, TaskStateSubmitted)
	}
	if task.History == nil {
		t.Error("NewTask().History should not be nil")
	}
	if task.Artifacts == nil {
		t.Error("NewTask().Artifacts should not be nil")
	}
	if task.Metadata == nil {
		t.Error("NewTask().Metadata should not be nil")
	}
}

func TestNewTextMessage(t *testing.T) {
	msg := NewTextMessage(RoleUser, "Hello")

	if msg.Role != RoleUser {
		t.Errorf("NewTextMessage().Role = %v, want %v", msg.Role, RoleUser)
	}
	if len(msg.Parts) != 1 {
		t.Errorf("len(NewTextMessage().Parts) = %v, want 1", len(msg.Parts))
	}

	textPart, ok := msg.Parts[0].(*TextPart)
	if !ok {
		t.Error("NewTextMessage().Parts[0] should be *TextPart")
	}
	if textPart.Text != "Hello" {
		t.Errorf("NewTextMessage().Parts[0].Text = %v, want Hello", textPart.Text)
	}
}

func TestMessageGetTextContent(t *testing.T) {
	msg := Message{
		Role: RoleAgent,
		Parts: []Part{
			&TextPart{Text: "Hello"},
			&TextPart{Text: "World"},
		},
	}

	content := msg.GetTextContent()
	expected := "Hello\nWorld"

	if content != expected {
		t.Errorf("Message.GetTextContent() = %v, want %v", content, expected)
	}
}

func TestPartMarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		part     Part
		wantType string
	}{
		{
			name:     "TextPart",
			part:     &TextPart{Text: "Hello"},
			wantType: "text",
		},
		{
			name:     "DataPart",
			part:     &DataPart{Data: map[string]any{"key": "value"}},
			wantType: "data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.part)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			// 解析结果验证类型字段
			var parsed map[string]any
			if err := json.Unmarshal(got, &parsed); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if parsed["type"] != tt.wantType {
				t.Errorf("json.Marshal().type = %v, want %v", parsed["type"], tt.wantType)
			}
		})
	}
}

func TestUnmarshalPart(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantTyp PartType
	}{
		{
			name:    "TextPart",
			data:    `{"type":"text","text":"Hello"}`,
			wantTyp: PartTypeText,
		},
		{
			name:    "DataPart",
			data:    `{"type":"data","data":{"key":"value"}}`,
			wantTyp: PartTypeData,
		},
		{
			name:    "FilePart",
			data:    `{"type":"file","file":{"name":"test.txt"}}`,
			wantTyp: PartTypeFile,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			part, err := UnmarshalPart([]byte(tt.data))
			if err != nil {
				t.Fatalf("UnmarshalPart() error = %v", err)
			}
			if part.Type() != tt.wantTyp {
				t.Errorf("UnmarshalPart().Type() = %v, want %v", part.Type(), tt.wantTyp)
			}
		})
	}
}

// ============== 错误测试 ==============

func TestA2AError(t *testing.T) {
	err := NewError(CodeTaskNotFound, "task not found", "task-123")

	if err.Code != CodeTaskNotFound {
		t.Errorf("Error.Code = %v, want %v", err.Code, CodeTaskNotFound)
	}
	if err.Message != "task not found" {
		t.Errorf("Error.Message = %v, want task not found", err.Message)
	}
	if err.Data != "task-123" {
		t.Errorf("Error.Data = %v, want task-123", err.Data)
	}

	errStr := err.Error()
	if errStr == "" {
		t.Error("Error.Error() should not be empty")
	}
}

func TestToA2AError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{
			name:     "TaskNotFound",
			err:      ErrTaskNotFound,
			wantCode: CodeTaskNotFound,
		},
		{
			name:     "AuthRequired",
			err:      ErrAuthRequired,
			wantCode: CodeAuthenticationRequired,
		},
		{
			name:     "A2AError",
			err:      NewError(CodeInvalidParams, "invalid"),
			wantCode: CodeInvalidParams,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a2aErr := ToA2AError(tt.err)
			if a2aErr.Code != tt.wantCode {
				t.Errorf("ToA2AError().Code = %v, want %v", a2aErr.Code, tt.wantCode)
			}
		})
	}
}

// ============== 存储测试 ==============

func TestMemoryTaskStore(t *testing.T) {
	store := NewMemoryTaskStore()
	ctx := context.Background()

	// 测试创建任务
	task := NewTask(store.GenerateID())
	task.SessionID = "session-1"

	err := store.Create(ctx, task)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// 测试获取任务
	got, err := store.Get(ctx, task.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != task.ID {
		t.Errorf("Get().ID = %v, want %v", got.ID, task.ID)
	}

	// 测试更新任务
	task.Status.State = TaskStateWorking
	err = store.Update(ctx, task)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, _ = store.Get(ctx, task.ID)
	if got.Status.State != TaskStateWorking {
		t.Errorf("Get().Status.State = %v, want %v", got.Status.State, TaskStateWorking)
	}

	// 测试列出任务
	resp, err := store.List(ctx, &ListTasksRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(resp.Tasks) != 1 {
		t.Errorf("List().Tasks length = %v, want 1", len(resp.Tasks))
	}

	// 测试删除任务
	err = store.Delete(ctx, task.ID)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	_, err = store.Get(ctx, task.ID)
	if err != ErrTaskNotFound {
		t.Errorf("Get() after delete error = %v, want %v", err, ErrTaskNotFound)
	}
}

// ============== Handler 测试 ==============

func TestEchoHandler(t *testing.T) {
	handler := NewEchoHandler()
	ctx := context.Background()

	task := NewTask("test-1")
	msg := NewUserMessage("Hello")

	update, err := handler.HandleTask(ctx, task, &msg)
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}

	if update == nil {
		t.Fatal("HandleTask() returned nil update")
	}
	if update.Status == nil {
		t.Fatal("HandleTask() returned nil status")
	}
	if update.Status.State != TaskStateCompleted {
		t.Errorf("HandleTask().Status.State = %v, want %v", update.Status.State, TaskStateCompleted)
	}
	if update.Message == nil {
		t.Fatal("HandleTask() returned nil message")
	}

	content := update.Message.GetTextContent()
	if content != "Echo: Hello" {
		t.Errorf("HandleTask().Message content = %v, want Echo: Hello", content)
	}
}

func TestFuncHandler(t *testing.T) {
	handler := NewFuncHandler(func(ctx context.Context, task *Task, msg *Message) (*TaskUpdate, error) {
		return NewCompletedUpdate(&Message{
			Role: RoleAgent,
			Parts: []Part{
				&TextPart{Text: "Custom response"},
			},
		}), nil
	})

	ctx := context.Background()
	task := NewTask("test-1")
	msg := NewUserMessage("Hello")

	update, err := handler.HandleTask(ctx, task, &msg)
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}

	if update.Message.GetTextContent() != "Custom response" {
		t.Errorf("HandleTask().Message content = %v, want Custom response", update.Message.GetTextContent())
	}
}

// ============== Server/Client 集成测试 ==============

func TestServerClient(t *testing.T) {
	// 创建服务器
	card := &AgentCard{
		Name:    "test-agent",
		URL:     "http://localhost:8080",
		Version: "1.0.0",
		Capabilities: AgentCapabilities{
			Streaming: true,
		},
	}

	handler := NewEchoHandler()
	server := NewServer(card, handler)

	// 创建测试服务器
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// 创建客户端
	client := NewClient(ts.URL)
	defer client.Close()

	ctx := context.Background()

	// 测试获取 Agent Card
	gotCard, err := client.GetAgentCard(ctx)
	if err != nil {
		t.Fatalf("GetAgentCard() error = %v", err)
	}
	if gotCard.Name != card.Name {
		t.Errorf("GetAgentCard().Name = %v, want %v", gotCard.Name, card.Name)
	}

	// 测试发送消息
	task, err := client.SendMessage(ctx, &SendMessageRequest{
		Message: NewUserMessage("Hello"),
	})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if task.ID == "" {
		t.Error("SendMessage().ID should not be empty")
	}
	if task.Status.State != TaskStateCompleted {
		t.Errorf("SendMessage().Status.State = %v, want %v", task.Status.State, TaskStateCompleted)
	}

	// 测试获取任务
	gotTask, err := client.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if gotTask.ID != task.ID {
		t.Errorf("GetTask().ID = %v, want %v", gotTask.ID, task.ID)
	}

	// 测试列出任务
	resp, err := client.ListTasks(ctx, nil)
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(resp.Tasks) < 1 {
		t.Error("ListTasks() should return at least one task")
	}
}

// ============== 认证测试 ==============

func TestBearerTokenValidator(t *testing.T) {
	validator := NewBearerTokenValidator()
	validator.AddToken("valid-token", "client-1")

	tests := []struct {
		name      string
		authValue string
		wantErr   bool
		wantID    string
	}{
		{
			name:      "valid token",
			authValue: "Bearer valid-token",
			wantErr:   false,
			wantID:    "client-1",
		},
		{
			name:      "invalid token",
			authValue: "Bearer invalid-token",
			wantErr:   true,
		},
		{
			name:      "no auth header",
			authValue: "",
			wantErr:   true,
		},
		{
			name:      "wrong scheme",
			authValue: "Basic dXNlcjpwYXNz",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.authValue != "" {
				req.Header.Set("Authorization", tt.authValue)
			}

			clientID, err := validator.Validate(req)
			if tt.wantErr {
				if err == nil {
					t.Error("Validate() should return error")
				}
			} else {
				if err != nil {
					t.Errorf("Validate() error = %v", err)
				}
				if clientID != tt.wantID {
					t.Errorf("Validate() clientID = %v, want %v", clientID, tt.wantID)
				}
			}
		})
	}
}

func TestAPIKeyValidator(t *testing.T) {
	validator := NewAPIKeyValidator("X-API-Key", "header")
	validator.AddKey("valid-key", "client-1")

	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{
			name:    "valid key",
			key:     "valid-key",
			wantErr: false,
		},
		{
			name:    "invalid key",
			key:     "invalid-key",
			wantErr: true,
		},
		{
			name:    "no key",
			key:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.key != "" {
				req.Header.Set("X-API-Key", tt.key)
			}

			_, err := validator.Validate(req)
			if tt.wantErr && err == nil {
				t.Error("Validate() should return error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Validate() error = %v", err)
			}
		})
	}
}

// ============== Push 测试 ==============

func TestRateLimiter(t *testing.T) {
	// 使用 toolkit 的 TokenBucket: 容量为 2，速率为 2/0.1s = 20/s
	limiter := rate.NewTokenBucket(2, 20)

	// 前两次应该允许
	if !limiter.Allow() {
		t.Error("RateLimiter.Allow() should return true")
	}
	if !limiter.Allow() {
		t.Error("RateLimiter.Allow() should return true")
	}

	// 第三次应该被限制
	if limiter.Allow() {
		t.Error("RateLimiter.Allow() should return false")
	}

	// 等待窗口重置
	time.Sleep(150 * time.Millisecond)

	// 现在应该允许
	if !limiter.Allow() {
		t.Error("RateLimiter.Allow() should return true after window reset")
	}
}

// ============== Discovery 测试 ==============

func TestStaticDiscovery(t *testing.T) {
	card1 := &AgentCard{
		Name: "agent-1",
		URL:  "http://agent1.example.com",
		Skills: []AgentSkill{
			{ID: "search"},
		},
	}
	card2 := &AgentCard{
		Name: "agent-2",
		URL:  "http://agent2.example.com",
		Skills: []AgentSkill{
			{ID: "code"},
		},
	}

	discovery := NewStaticDiscovery(card1, card2)
	ctx := context.Background()

	// 测试发现所有
	cards, err := discovery.Discover(ctx, nil)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(cards) != 2 {
		t.Errorf("Discover() returned %d cards, want 2", len(cards))
	}

	// 测试按技能过滤
	cards, err = discovery.Discover(ctx, &AgentFilter{Skills: []string{"search"}})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(cards) != 1 {
		t.Errorf("Discover() with filter returned %d cards, want 1", len(cards))
	}
	if cards[0].Name != "agent-1" {
		t.Errorf("Discover() with filter returned %v, want agent-1", cards[0].Name)
	}

	// 测试获取单个
	card, err := discovery.Get(ctx, "http://agent1.example.com")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if card.Name != "agent-1" {
		t.Errorf("Get().Name = %v, want agent-1", card.Name)
	}
}
