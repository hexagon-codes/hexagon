package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// 本文件为 a2a 包的全场景挑刺审计测试 (audit wave1)。
// 覆盖: 正常路径 + 边界(空/nil/超长/0/负数/Unicode/并发竞态) + 错误路径 +
//       未闭环逻辑 + 状态一致性。
//
// 设计原则: 针对真实公开 API, 不 mock 被测逻辑本身; 涉及外部 HTTP 依赖时使用 httptest。

// ============== MemoryTaskStore: 共享指针导致的状态污染 ==============

// TestMemoryStoreGetReturnsSharedPointer 验证 Get 返回的是否为存储内部的同一指针。
// 如果返回的是同一指针, 那么调用方对返回任务的修改会直接污染存储中的状态,
// 这通常被视为存储隔离性缺陷。此测试用于探测该行为。
func TestMemoryStoreGetReturnsSharedPointer(t *testing.T) {
	store := NewMemoryTaskStore()
	defer store.Close()
	ctx := context.Background()

	task := NewTask("shared-ptr")
	task.History = append(task.History, NewUserMessage("m1"), NewUserMessage("m2"), NewUserMessage("m3"))
	if err := store.Create(ctx, task); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// 第一次取出, 修改其 History (模拟调用方截断历史)
	got1, _ := store.Get(ctx, "shared-ptr")
	got1.History = got1.History[len(got1.History)-1:] // 仅保留最后一条

	// 第二次取出, 检查存储内部历史是否被污染
	got2, _ := store.Get(ctx, "shared-ptr")
	if len(got2.History) != 3 {
		t.Errorf("存储隔离性缺陷: Get 返回共享指针, 调用方截断 History 污染了存储; "+
			"二次 Get History 长度 = %d, 期望 3", len(got2.History))
	}
}

// TestHandleGetTaskHistoryTruncationCorruptsStore 通过完整的 Server/Client 链路验证:
// GetTask 携带 HistoryLength 截断历史时, 是否会污染存储中保存的完整历史。
// handleGetTask 直接对 store.Get 返回的指针执行 task.History = task.History[...],
// 由于 MemoryTaskStore 返回共享指针, 这会永久截断存储中的历史。
func TestHandleGetTaskHistoryTruncationCorruptsStore(t *testing.T) {
	card := &AgentCard{Name: "t", URL: "http://x", Version: "1", Capabilities: AgentCapabilities{Streaming: true}}
	server := NewServer(card, NewEchoHandler())
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)
	defer client.Close()
	ctx := context.Background()

	// 发送一条消息, 产生一个任务 (history: user + agent = 2 条)
	task, err := client.SendMessage(ctx, &SendMessageRequest{Message: NewUserMessage("hi")})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	fullLen := len(task.History)
	if fullLen < 2 {
		t.Fatalf("前置条件不满足: 期望 history >= 2, 实际 %d", fullLen)
	}

	// 用 HistoryLength=1 获取任务 (截断历史)
	if _, err := client.GetTask(ctx, task.ID); err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	// 直接走 server 内部的 GetTask 截断逻辑: 通过原始 RPC 带 HistoryLength
	truncated := callGetTaskRPC(t, ts.URL, task.ID, 1)
	if len(truncated.History) != 1 {
		t.Fatalf("HistoryLength=1 期望返回 1 条, 实际 %d", len(truncated.History))
	}

	// 再次以完整方式获取, 检查存储里的历史是否被永久截断
	full := callGetTaskRPC(t, ts.URL, task.ID, 0)
	if len(full.History) != fullLen {
		t.Errorf("状态污染: 带 HistoryLength 的 GetTask 永久截断了存储历史; "+
			"完整获取得到 %d 条, 期望 %d 条", len(full.History), fullLen)
	}
}

// callGetTaskRPC 直接以 JSON-RPC 调 tasks/get, 用于精确控制 HistoryLength。
func callGetTaskRPC(t *testing.T, baseURL, taskID string, historyLength int) *Task {
	t.Helper()
	rpcReq, _ := NewJSONRPCRequest(1, MethodGetTask, &GetTaskRequest{ID: taskID, HistoryLength: historyLength})
	body, _ := json.Marshal(rpcReq)
	resp, err := http.Post(baseURL+PathTasks, ContentTypeJSON, strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post error = %v", err)
	}
	defer resp.Body.Close()
	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode error = %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("rpc error = %v", rpcResp.Error)
	}
	var task Task
	if err := json.Unmarshal(rpcResp.Result, &task); err != nil {
		t.Fatalf("unmarshal task error = %v", err)
	}
	return &task
}

// ============== MemoryTaskStore: 分页/过滤边界 ==============

func TestMemoryStoreListPagination(t *testing.T) {
	store := NewMemoryTaskStore()
	defer store.Close()
	ctx := context.Background()

	// 创建 5 个任务, 时间戳递增
	for i := 0; i < 5; i++ {
		task := NewTask(store.GenerateID())
		task.CreatedAt = time.Now().Add(time.Duration(i) * time.Millisecond)
		if err := store.Create(ctx, task); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	tests := []struct {
		name      string
		req       *ListTasksRequest
		wantLen   int
		wantTotal int
	}{
		{"无分页", &ListTasksRequest{}, 5, 5},
		{"limit=2", &ListTasksRequest{Limit: 2}, 2, 5},
		{"offset=3", &ListTasksRequest{Offset: 3}, 2, 5},
		{"offset超界", &ListTasksRequest{Offset: 10}, 0, 5},
		{"offset恰好等于总数", &ListTasksRequest{Offset: 5}, 0, 5},
		{"limit+offset组合", &ListTasksRequest{Offset: 1, Limit: 2}, 2, 5},
		{"负offset", &ListTasksRequest{Offset: -1}, 5, 5},
		{"负limit", &ListTasksRequest{Limit: -1}, 5, 5},
		{"nil请求", nil, 5, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := store.List(ctx, tt.req)
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if len(resp.Tasks) != tt.wantLen {
				t.Errorf("List().Tasks len = %d, want %d", len(resp.Tasks), tt.wantLen)
			}
			if resp.Total != tt.wantTotal {
				t.Errorf("List().Total = %d, want %d", resp.Total, tt.wantTotal)
			}
		})
	}
}

// TestMemoryStoreCreateDuplicate 验证重复创建同 ID 任务报错。
func TestMemoryStoreCreateDuplicate(t *testing.T) {
	store := NewMemoryTaskStore()
	defer store.Close()
	ctx := context.Background()

	task := NewTask("dup")
	if err := store.Create(ctx, task); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	if err := store.Create(ctx, task); err == nil {
		t.Error("重复 Create 同 ID 应报错, 实际成功")
	}
}

// TestMemoryStoreUpdateNonExistent 验证更新不存在任务返回 ErrTaskNotFound。
func TestMemoryStoreUpdateNonExistent(t *testing.T) {
	store := NewMemoryTaskStore()
	defer store.Close()
	ctx := context.Background()

	if err := store.Update(ctx, NewTask("ghost")); err != ErrTaskNotFound {
		t.Errorf("Update 不存在任务 err = %v, want %v", err, ErrTaskNotFound)
	}
}

// TestMemoryStoreSessionIndexCleanup 验证删除任务后会话索引也被清理。
func TestMemoryStoreSessionIndexCleanup(t *testing.T) {
	store := NewMemoryTaskStore()
	defer store.Close()
	ctx := context.Background()

	task := NewTask("s1-t1")
	task.SessionID = "sess-x"
	if err := store.Create(ctx, task); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Delete(ctx, "s1-t1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	resp, err := store.List(ctx, &ListTasksRequest{SessionID: "sess-x"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(resp.Tasks) != 0 {
		t.Errorf("删除后会话仍残留 %d 个任务, want 0", len(resp.Tasks))
	}
}

// ============== MemoryTaskStore: 推送配置存储 ==============

func TestMemoryStorePushConfig(t *testing.T) {
	store := NewMemoryTaskStore()
	defer store.Close()
	ctx := context.Background()

	// 任务不存在时设置推送配置应失败
	if err := store.SetPushConfig(ctx, "no-task", &PushNotificationConfig{URL: "http://x"}); err != ErrTaskNotFound {
		t.Errorf("SetPushConfig 不存在任务 err = %v, want %v", err, ErrTaskNotFound)
	}

	task := NewTask("pc-task")
	store.Create(ctx, task)

	cfg := &PushNotificationConfig{URL: "http://hook"}
	if err := store.SetPushConfig(ctx, "pc-task", cfg); err != nil {
		t.Fatalf("SetPushConfig() error = %v", err)
	}
	got, err := store.GetPushConfig(ctx, "pc-task")
	if err != nil {
		t.Fatalf("GetPushConfig() error = %v", err)
	}
	if got == nil || got.URL != "http://hook" {
		t.Errorf("GetPushConfig() = %v, want URL=http://hook", got)
	}

	// 删除任务后推送配置应一并清理
	store.Delete(ctx, "pc-task")
	if _, err := store.GetPushConfig(ctx, "pc-task"); err != ErrTaskNotFound {
		t.Errorf("删除任务后 GetPushConfig err = %v, want %v", err, ErrTaskNotFound)
	}
}

// ============== Matcher 边界 ==============

// TestPrefixMatcherBoundary 验证前缀匹配器在空前缀/Unicode/超长等边界下的行为。
func TestPrefixMatcherBoundary(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		text   string
		want   bool
	}{
		{"普通前缀匹配", "/cmd", "/cmd hello", true},
		{"前缀不匹配", "/cmd", "hello", false},
		{"空前缀总匹配", "", "anything", true},
		{"空前缀空文本", "", "", true},
		{"前缀比文本长", "/command", "/cmd", false},
		{"Unicode前缀", "你好", "你好世界", true},
		{"Unicode前缀部分字节", "你", "你好", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := PrefixMatcher(tt.prefix)
			msg := NewUserMessage(tt.text)
			if got := m(&msg); got != tt.want {
				t.Errorf("PrefixMatcher(%q)(%q) = %v, want %v", tt.prefix, tt.text, got, tt.want)
			}
		})
	}
}

// TestContainsMatcherBoundary 验证包含匹配器的边界, 重点探测空子串与超长子串。
func TestContainsMatcherBoundary(t *testing.T) {
	tests := []struct {
		name   string
		substr string
		text   string
		want   bool
	}{
		{"普通包含", "world", "hello world", true},
		{"不包含", "xyz", "hello", false},
		{"空子串", "", "hello", true},
		{"空子串空文本", "", "", true},
		{"子串比文本长", "hello world", "hi", false},
		{"Unicode包含", "世界", "你好世界啊", true},
		{"文本为空子串非空", "x", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := ContainsMatcher(tt.substr)
			msg := NewUserMessage(tt.text)
			if got := m(&msg); got != tt.want {
				t.Errorf("ContainsMatcher(%q)(%q) = %v, want %v", tt.substr, tt.text, got, tt.want)
			}
		})
	}
}

// ============== SkillRouter ==============

func TestSkillRouterRouting(t *testing.T) {
	ctx := context.Background()
	task := NewTask("r1")

	cmdHandler := NewFuncHandler(func(_ context.Context, _ *Task, _ *Message) (*TaskUpdate, error) {
		return NewCompletedUpdate(&Message{Role: RoleAgent, Parts: []Part{&TextPart{Text: "cmd"}}}), nil
	})
	defaultHandler := NewFuncHandler(func(_ context.Context, _ *Task, _ *Message) (*TaskUpdate, error) {
		return NewCompletedUpdate(&Message{Role: RoleAgent, Parts: []Part{&TextPart{Text: "default"}}}), nil
	})

	router := NewSkillRouter().
		Route(PrefixMatcher("/cmd"), cmdHandler).
		Default(defaultHandler)

	// 匹配命令前缀
	msg := NewUserMessage("/cmd run")
	upd, err := router.HandleTask(ctx, task, &msg)
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}
	if upd.Message.GetTextContent() != "cmd" {
		t.Errorf("路由结果 = %q, want cmd", upd.Message.GetTextContent())
	}

	// 走默认
	msg2 := NewUserMessage("hello")
	upd2, _ := router.HandleTask(ctx, task, &msg2)
	if upd2.Message.GetTextContent() != "default" {
		t.Errorf("默认路由结果 = %q, want default", upd2.Message.GetTextContent())
	}
}

// TestSkillRouterNoDefault 验证无默认处理器且无匹配时返回 failed update。
func TestSkillRouterNoDefault(t *testing.T) {
	ctx := context.Background()
	task := NewTask("r2")
	router := NewSkillRouter().Route(PrefixMatcher("/never"), NewEchoHandler())

	msg := NewUserMessage("hello")
	upd, err := router.HandleTask(ctx, task, &msg)
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}
	if upd == nil || upd.Status == nil || upd.Status.State != TaskStateFailed {
		t.Errorf("无匹配且无默认应返回 failed, 实际 = %+v", upd)
	}
}

// ============== 中间件 ==============

// TestRecoveryMiddleware 验证 panic 被恢复为 failed update。
func TestRecoveryMiddleware(t *testing.T) {
	ctx := context.Background()
	task := NewTask("rec")

	panicHandler := NewFuncHandler(func(_ context.Context, _ *Task, _ *Message) (*TaskUpdate, error) {
		panic("boom")
	})
	wrapped := ApplyMiddleware(panicHandler, RecoveryMiddleware())

	msg := NewUserMessage("x")
	upd, err := wrapped.HandleTask(ctx, task, &msg)
	if err != nil {
		t.Fatalf("RecoveryMiddleware 应吞掉 panic 错误, 实际 err = %v", err)
	}
	if upd == nil || upd.Status == nil || upd.Status.State != TaskStateFailed {
		t.Errorf("panic 后应返回 failed update, 实际 = %+v", upd)
	}
}

// TestTimeoutMiddleware 验证超时时返回 failed update。
func TestTimeoutMiddleware(t *testing.T) {
	ctx := context.Background()
	task := NewTask("to")

	slowHandler := NewFuncHandler(func(c context.Context, _ *Task, _ *Message) (*TaskUpdate, error) {
		select {
		case <-time.After(500 * time.Millisecond):
			return NewCompletedUpdate(&Message{Role: RoleAgent, Parts: []Part{&TextPart{Text: "done"}}}), nil
		case <-c.Done():
			return nil, c.Err()
		}
	})
	wrapped := ApplyMiddleware(slowHandler, TimeoutMiddleware(50*time.Millisecond))

	msg := NewUserMessage("x")
	upd, err := wrapped.HandleTask(ctx, task, &msg)
	if err != nil {
		t.Fatalf("TimeoutMiddleware err = %v, want nil", err)
	}
	if upd == nil || upd.Status == nil || upd.Status.State != TaskStateFailed {
		t.Errorf("超时应返回 failed update, 实际 = %+v", upd)
	}
}

// TestApplyMiddlewareOrder 验证中间件按声明顺序从外到内包裹。
func TestApplyMiddlewareOrder(t *testing.T) {
	var order []string
	mw := func(tag string) Middleware {
		return func(next TaskHandler) TaskHandler {
			return NewFuncHandler(func(ctx context.Context, task *Task, msg *Message) (*TaskUpdate, error) {
				order = append(order, tag)
				return next.HandleTask(ctx, task, msg)
			})
		}
	}
	base := NewFuncHandler(func(_ context.Context, _ *Task, _ *Message) (*TaskUpdate, error) {
		order = append(order, "base")
		return NewCompletedUpdate(&Message{Role: RoleAgent, Parts: []Part{&TextPart{Text: "x"}}}), nil
	})
	wrapped := ApplyMiddleware(base, mw("a"), mw("b"))

	msg := NewUserMessage("x")
	wrapped.HandleTask(context.Background(), task0(), &msg)

	want := []string{"a", "b", "base"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("中间件执行顺序 = %v, want %v", order, want)
	}
}

func task0() *Task { return NewTask("o") }

// ============== ChainHandler ==============

// TestChainHandlerStopsAtFirstNonNil 验证链式处理器在第一个非 nil 结果处停止。
func TestChainHandlerStopsAtFirstNonNil(t *testing.T) {
	ctx := context.Background()
	task := NewTask("ch")

	var calls []string
	h1 := NewFuncHandler(func(_ context.Context, _ *Task, _ *Message) (*TaskUpdate, error) {
		calls = append(calls, "h1")
		return nil, nil // 返回 nil, 链应继续
	})
	h2 := NewFuncHandler(func(_ context.Context, _ *Task, _ *Message) (*TaskUpdate, error) {
		calls = append(calls, "h2")
		return NewCompletedUpdate(&Message{Role: RoleAgent, Parts: []Part{&TextPart{Text: "h2"}}}), nil
	})
	h3 := NewFuncHandler(func(_ context.Context, _ *Task, _ *Message) (*TaskUpdate, error) {
		calls = append(calls, "h3")
		return NewCompletedUpdate(&Message{Role: RoleAgent, Parts: []Part{&TextPart{Text: "h3"}}}), nil
	})

	chain := NewChainHandler(h1, h2, h3)
	msg := NewUserMessage("x")
	upd, err := chain.HandleTask(ctx, task, &msg)
	if err != nil {
		t.Fatalf("HandleTask() error = %v", err)
	}
	if upd.Message.GetTextContent() != "h2" {
		t.Errorf("ChainHandler 应在 h2 停止, 实际 = %q", upd.Message.GetTextContent())
	}
	if strings.Join(calls, ",") != "h1,h2" {
		t.Errorf("ChainHandler 调用序列 = %v, want [h1 h2] (h3 不应被调用)", calls)
	}
}

// ============== Server: 错误路径与状态机 ==============

// TestServerCancelTerminalTask 验证取消已终态任务返回不可取消错误。
func TestServerCancelTerminalTask(t *testing.T) {
	card := &AgentCard{Name: "t", URL: "http://x", Version: "1", Capabilities: AgentCapabilities{Streaming: true}}
	server := NewServer(card, NewEchoHandler())
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)
	defer client.Close()
	ctx := context.Background()

	// EchoHandler 会把任务置为 completed (终态)
	task, err := client.SendMessage(ctx, &SendMessageRequest{Message: NewUserMessage("hi")})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if task.Status.State != TaskStateCompleted {
		t.Fatalf("前置: 期望 completed, 实际 %v", task.Status.State)
	}

	_, err = client.CancelTask(ctx, task.ID)
	if err == nil {
		t.Fatal("取消已完成任务应报错")
	}
	a2aErr := GetA2AError(err)
	if a2aErr == nil || a2aErr.Code != CodeTaskNotCancelable {
		t.Errorf("取消终态任务 err = %v, want code %d", err, CodeTaskNotCancelable)
	}
}

// TestServerGetNonExistentTask 验证获取不存在任务返回 task not found。
func TestServerGetNonExistentTask(t *testing.T) {
	card := &AgentCard{Name: "t", URL: "http://x", Version: "1", Capabilities: AgentCapabilities{Streaming: true}}
	server := NewServer(card, NewEchoHandler())
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)
	defer client.Close()

	_, err := client.GetTask(context.Background(), "no-such-task")
	if err == nil {
		t.Fatal("获取不存在任务应报错")
	}
	if a := GetA2AError(err); a == nil || a.Code != CodeTaskNotFound {
		t.Errorf("err = %v, want code %d", err, CodeTaskNotFound)
	}
}

// TestServerSendMessageWithUnknownTaskID 验证带不存在 TaskID 发消息返回 not found。
func TestServerSendMessageWithUnknownTaskID(t *testing.T) {
	card := &AgentCard{Name: "t", URL: "http://x", Version: "1", Capabilities: AgentCapabilities{Streaming: true}}
	server := NewServer(card, NewEchoHandler())
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)
	defer client.Close()

	_, err := client.SendMessage(context.Background(), &SendMessageRequest{
		TaskID:  "ghost-task",
		Message: NewUserMessage("hi"),
	})
	if err == nil {
		t.Fatal("带不存在 TaskID 发消息应报错")
	}
}

// TestServerInvalidJSONRPCVersion 验证非 2.0 版本被拒绝。
func TestServerInvalidJSONRPCVersion(t *testing.T) {
	card := &AgentCard{Name: "t", URL: "http://x", Version: "1", Capabilities: AgentCapabilities{Streaming: true}}
	server := NewServer(card, NewEchoHandler())
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	body := `{"jsonrpc":"1.0","id":1,"method":"tasks/send","params":{}}`
	resp, err := http.Post(ts.URL+PathTasks, ContentTypeJSON, strings.NewReader(body))
	if err != nil {
		t.Fatalf("post error = %v", err)
	}
	defer resp.Body.Close()
	var rpcResp JSONRPCResponse
	json.NewDecoder(resp.Body).Decode(&rpcResp)
	if rpcResp.Error == nil || rpcResp.Error.Code != CodeInvalidRequest {
		t.Errorf("非 2.0 版本应返回 invalid request, 实际 = %v", rpcResp.Error)
	}
}

// TestServerUnknownMethod 验证未知方法返回 method not found。
func TestServerUnknownMethod(t *testing.T) {
	card := &AgentCard{Name: "t", URL: "http://x", Version: "1", Capabilities: AgentCapabilities{Streaming: true}}
	server := NewServer(card, NewEchoHandler())
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"tasks/doesNotExist","params":{}}`
	resp, err := http.Post(ts.URL+PathTasks, ContentTypeJSON, strings.NewReader(body))
	if err != nil {
		t.Fatalf("post error = %v", err)
	}
	defer resp.Body.Close()
	var rpcResp JSONRPCResponse
	json.NewDecoder(resp.Body).Decode(&rpcResp)
	if rpcResp.Error == nil || rpcResp.Error.Code != CodeMethodNotFound {
		t.Errorf("未知方法应返回 method not found, 实际 = %v", rpcResp.Error)
	}
}

// TestServerPushNotificationNotSupported 验证默认 card 不支持推送时拒绝设置。
func TestServerPushNotificationNotSupported(t *testing.T) {
	// 默认 card.Capabilities.PushNotifications = false
	card := &AgentCard{Name: "t", URL: "http://x", Version: "1", Capabilities: AgentCapabilities{Streaming: true}}
	server := NewServer(card, NewEchoHandler())
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)
	defer client.Close()

	err := client.SetPushNotification(context.Background(), "any", &PushNotificationConfig{URL: "http://hook"})
	if err == nil {
		t.Fatal("不支持推送时设置应报错")
	}
	if a := GetA2AError(err); a == nil || a.Code != CodePushNotificationNotSupported {
		t.Errorf("err = %v, want code %d", err, CodePushNotificationNotSupported)
	}
}

// TestServerStreamingDisabled 验证 card 未声明 streaming 时流式接口被拒绝。
func TestServerStreamingDisabled(t *testing.T) {
	card := &AgentCard{Name: "t", URL: "http://x", Version: "1", Capabilities: AgentCapabilities{Streaming: false}}
	server := NewServer(card, NewEchoHandler())
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)
	defer client.Close()

	_, err := client.SendMessageStream(context.Background(), &SendMessageRequest{Message: NewUserMessage("hi")})
	if err == nil {
		t.Fatal("streaming 关闭时流式发送应报错")
	}
}

// ============== Server: 流式正常路径 ==============

// TestServerSendMessageStreamNonStreamingHandler 验证非流式 handler 在流式端点下
// 仍能正确产出 status + done 事件。
func TestServerSendMessageStreamNonStreamingHandler(t *testing.T) {
	card := &AgentCard{Name: "t", URL: "http://x", Version: "1", Capabilities: AgentCapabilities{Streaming: true}}
	server := NewServer(card, NewEchoHandler())
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	client := NewClient(ts.URL)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, err := client.SendMessageStream(ctx, &SendMessageRequest{Message: NewUserMessage("hi")})
	if err != nil {
		t.Fatalf("SendMessageStream() error = %v", err)
	}

	var gotStatus, gotDone bool
	for ev := range events {
		switch ev.(type) {
		case *TaskStatusEvent:
			gotStatus = true
		case *DoneEvent:
			gotDone = true
		case *ErrorEvent:
			t.Errorf("收到非预期 error 事件: %v", ev)
		}
	}
	if !gotStatus {
		t.Error("未收到 task-status 事件")
	}
	if !gotDone {
		t.Error("未收到 done 事件")
	}
}

// ============== Auth: 链式/Basic/RBAC ==============

func TestBasicAuthValidator(t *testing.T) {
	v := NewBasicAuthValidator()
	v.AddCredentials("user", "pass", "client-1")

	tests := []struct {
		name      string
		setupAuth func(*http.Request)
		wantErr   bool
	}{
		{"有效凭证", func(r *http.Request) { r.SetBasicAuth("user", "pass") }, false},
		{"错误密码", func(r *http.Request) { r.SetBasicAuth("user", "wrong") }, true},
		{"无头", func(_ *http.Request) {}, true},
		{"非base64", func(r *http.Request) { r.Header.Set("Authorization", "Basic !!!notbase64") }, true},
		{"错误scheme", func(r *http.Request) { r.Header.Set("Authorization", "Bearer xyz") }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			tt.setupAuth(req)
			_, err := v.Validate(req)
			if tt.wantErr != (err != nil) {
				t.Errorf("Validate() err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestChainValidator(t *testing.T) {
	bearer := NewBearerTokenValidator()
	bearer.AddToken("tok", "bearer-client")
	apikey := NewAPIKeyValidator("X-Key", "header")
	apikey.AddKey("k", "key-client")

	chain := NewChainValidator(bearer, apikey)

	// bearer 命中
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.Header.Set("Authorization", "Bearer tok")
	if id, err := chain.Validate(req1); err != nil || id != "bearer-client" {
		t.Errorf("链式 bearer 命中 id = %q err = %v, want bearer-client", id, err)
	}

	// apikey 命中
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-Key", "k")
	if id, err := chain.Validate(req2); err != nil || id != "key-client" {
		t.Errorf("链式 apikey 命中 id = %q err = %v, want key-client", id, err)
	}

	// 都不命中
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := chain.Validate(req3); err == nil {
		t.Error("链式都不命中应报错")
	}
}

func TestRBACValidatorPermission(t *testing.T) {
	bearer := NewBearerTokenValidator()
	bearer.AddToken("tok", "client-1")
	rbac := NewRBACValidator(bearer)
	rbac.SetPermissions("client-1", PermissionRead)

	if !rbac.HasPermission("client-1", PermissionRead) {
		t.Error("client-1 应有 read 权限")
	}
	if rbac.HasPermission("client-1", PermissionWrite) {
		t.Error("client-1 不应有 write 权限")
	}

	// Admin 权限应覆盖一切
	rbac.SetPermissions("admin-client", PermissionAdmin)
	if !rbac.HasPermission("admin-client", PermissionWrite) {
		t.Error("admin 权限应覆盖 write")
	}
	if !rbac.HasPermission("admin-client", PermissionCancelTask) {
		t.Error("admin 权限应覆盖 cancel_task")
	}
}

// TestAuthMiddlewareReject 验证认证中间件拒绝未认证请求。
func TestAuthMiddlewareReject(t *testing.T) {
	bearer := NewBearerTokenValidator()
	bearer.AddToken("tok", "client-1")
	mw := AuthMiddleware(bearer)

	handlerCalled := false
	final := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		handlerCalled = true
	})
	srv := mw(final)

	// 无 token: 应被拒绝, handler 不应被调用
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("无 token 状态码 = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if handlerCalled {
		t.Error("认证失败时不应调用下游 handler")
	}

	// 带 token: 应放行
	handlerCalled = false
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/", nil)
	req2.Header.Set("Authorization", "Bearer tok")
	srv.ServeHTTP(rec2, req2)
	if !handlerCalled {
		t.Error("认证成功时应调用下游 handler")
	}
	authCtx := GetAuthContext(req2.Context())
	_ = authCtx // ctx 在 WithContext 后才注入, 这里不强校验
}

// ============== convert.go ==============

func TestAgentCardRoundTrip(t *testing.T) {
	card := NewAgentCard("svc", "http://svc", "2.0")
	card.Skills = []AgentSkill{
		{ID: "s1", Name: "skill1", Description: "d1", Tags: []string{"t1", "t2"}},
	}

	info := CardToAgentInfo(card)
	if info.Name != "svc" || info.Endpoint != "http://svc" || info.Version != "2.0" {
		t.Errorf("CardToAgentInfo 基本字段不匹配: %+v", info)
	}
	if len(info.Capabilities) != 1 || info.Capabilities[0].Name != "s1" {
		t.Errorf("CardToAgentInfo Capabilities = %+v, want [s1]", info.Capabilities)
	}
	if len(info.Tags) != 2 {
		t.Errorf("CardToAgentInfo Tags len = %d, want 2", len(info.Tags))
	}
}

func TestConvertNilSafety(t *testing.T) {
	if AgentInfoToCard(nil, "http://x") != nil {
		t.Error("AgentInfoToCard(nil) 应返回 nil")
	}
	if CardToAgentInfo(nil) != nil {
		t.Error("CardToAgentInfo(nil) 应返回 nil")
	}
}

// ============== JSON 序列化: Message / Part 往返 ==============

// TestMessageJSONRoundTrip 验证 Message 多模态 Part 的序列化/反序列化往返一致。
func TestMessageJSONRoundTrip(t *testing.T) {
	orig := Message{
		Role: RoleUser,
		Parts: []Part{
			&TextPart{Text: "文本内容 🚀"},
			&DataPart{Data: map[string]any{"k": "v"}},
			&FilePart{File: FileContent{Name: "a.txt", MimeType: "text/plain"}},
		},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var got Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(got.Parts) != 3 {
		t.Fatalf("往返后 Parts len = %d, want 3", len(got.Parts))
	}
	if got.Parts[0].Type() != PartTypeText || got.Parts[1].Type() != PartTypeData || got.Parts[2].Type() != PartTypeFile {
		t.Errorf("往返后 Part 类型顺序错误: %v %v %v", got.Parts[0].Type(), got.Parts[1].Type(), got.Parts[2].Type())
	}
	if got.GetTextContent() != "文本内容 🚀" {
		t.Errorf("往返后文本内容 = %q, want 文本内容 🚀", got.GetTextContent())
	}
}

// TestUnmarshalPartUnknownType 验证未知类型且无 text 字段时返回错误。
func TestUnmarshalPartUnknownType(t *testing.T) {
	_, err := UnmarshalPart([]byte(`{"type":"unknown","foo":"bar"}`))
	if err != ErrInvalidMessage {
		t.Errorf("未知类型无 text 应返回 ErrInvalidMessage, 实际 = %v", err)
	}

	// 未知类型但带 text 字段: 应回退为 TextPart
	part, err := UnmarshalPart([]byte(`{"type":"unknown","text":"fallback"}`))
	if err != nil {
		t.Fatalf("未知类型带 text 不应报错: %v", err)
	}
	if tp, ok := part.(*TextPart); !ok || tp.Text != "fallback" {
		t.Errorf("未知类型带 text 应回退为 TextPart{fallback}, 实际 = %#v", part)
	}
}

// ============== Client: 关闭后调用 ==============

func TestClientClosedRejectsCalls(t *testing.T) {
	client := NewClient("http://localhost:0")
	client.Close()
	if !client.IsClosed() {
		t.Error("Close() 后 IsClosed() 应为 true")
	}

	ctx := context.Background()
	if _, err := client.GetAgentCard(ctx); err != ErrClientClosed {
		t.Errorf("关闭后 GetAgentCard err = %v, want %v", err, ErrClientClosed)
	}
	if _, err := client.SendMessage(ctx, &SendMessageRequest{}); err != ErrClientClosed {
		t.Errorf("关闭后 SendMessage err = %v, want %v", err, ErrClientClosed)
	}
	if _, err := client.GetTask(ctx, "x"); err != ErrClientClosed {
		t.Errorf("关闭后 GetTask err = %v, want %v", err, ErrClientClosed)
	}
}

// ============== Client: SSE 解析 (parseSSEEvent) ==============

// TestClientParseSSEEvent 验证 SSE 事件解析针对各事件类型的本地解析逻辑。
func TestClientParseSSEEvent(t *testing.T) {
	c := NewClient("http://x")
	defer c.Close()

	tests := []struct {
		name      string
		eventType string
		data      string
		check     func(StreamEvent) bool
	}{
		{
			name:      "task-status",
			eventType: EventTypeTaskStatus,
			data:      `{"id":"t1","status":{"state":"working"}}`,
			check: func(e StreamEvent) bool {
				ev, ok := e.(*TaskStatusEvent)
				return ok && ev.ID == "t1" && ev.Status.State == TaskStateWorking
			},
		},
		{
			name:      "artifact",
			eventType: EventTypeArtifact,
			data:      `{"id":"t1","artifact":{"name":"r","parts":[{"type":"text","text":"hi"}]}}`,
			check: func(e StreamEvent) bool {
				ev, ok := e.(*ArtifactEvent)
				return ok && ev.Artifact.GetTextContent() == "hi"
			},
		},
		{
			name:      "error",
			eventType: EventTypeError,
			data:      `{"error":{"code":-32001,"message":"not found"}}`,
			check: func(e StreamEvent) bool {
				ev, ok := e.(*ErrorEvent)
				return ok && ev.Error.Code == CodeTaskNotFound
			},
		},
		{
			name:      "done带task",
			eventType: EventTypeDone,
			data:      `{"task":{"id":"t1","status":{"state":"completed"}}}`,
			check: func(e StreamEvent) bool {
				ev, ok := e.(*DoneEvent)
				return ok && ev.Task != nil && ev.Task.ID == "t1"
			},
		},
		{
			name:      "task-status非法JSON回退error",
			eventType: EventTypeTaskStatus,
			data:      `not-json`,
			check: func(e StreamEvent) bool {
				_, ok := e.(*ErrorEvent)
				return ok
			},
		},
		{
			name:      "未知事件类型返回nil",
			eventType: "mystery",
			data:      `{}`,
			check: func(e StreamEvent) bool {
				return e == nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := c.parseSSEEvent(tt.eventType, tt.data)
			if !tt.check(ev) {
				t.Errorf("parseSSEEvent(%q, %q) = %#v 未通过校验", tt.eventType, tt.data, ev)
			}
		})
	}
}

// ============== applyUpdate: 产物追加逻辑 ==============

// TestApplyUpdateArtifactAppend 验证 applyUpdate 的产物追加与索引设置逻辑。
func TestApplyUpdateArtifactAppend(t *testing.T) {
	card := &AgentCard{Name: "t", URL: "http://x", Version: "1", Capabilities: AgentCapabilities{Streaming: true}}
	s := NewServer(card, NewEchoHandler())

	task := NewTask("au")

	// 首个产物 (Append=false)
	s.applyUpdate(task, &TaskUpdate{Artifact: &Artifact{
		Name: "r", Parts: []Part{&TextPart{Text: "a"}},
	}})
	if len(task.Artifacts) != 1 {
		t.Fatalf("首个产物后 len = %d, want 1", len(task.Artifacts))
	}
	if task.Artifacts[0].Index != 0 {
		t.Errorf("首个产物 Index = %d, want 0", task.Artifacts[0].Index)
	}

	// 追加到最后一个产物 (Append=true)
	s.applyUpdate(task, &TaskUpdate{Artifact: &Artifact{
		Append: true, Parts: []Part{&TextPart{Text: "b"}}, LastChunk: true,
	}})
	if len(task.Artifacts) != 1 {
		t.Fatalf("追加后产物数量 = %d, want 1 (应追加而非新增)", len(task.Artifacts))
	}
	if len(task.Artifacts[0].Parts) != 2 {
		t.Errorf("追加后 Parts len = %d, want 2", len(task.Artifacts[0].Parts))
	}
	if !task.Artifacts[0].LastChunk {
		t.Error("追加后 LastChunk 应为 true")
	}

	// 第二个独立产物 (Append=false)
	s.applyUpdate(task, &TaskUpdate{Artifact: &Artifact{
		Name: "r2", Parts: []Part{&TextPart{Text: "c"}},
	}})
	if len(task.Artifacts) != 2 {
		t.Fatalf("独立产物后数量 = %d, want 2", len(task.Artifacts))
	}
	if task.Artifacts[1].Index != 1 {
		t.Errorf("第二个产物 Index = %d, want 1", task.Artifacts[1].Index)
	}
}

// TestApplyUpdateAppendOnEmpty 验证在没有任何产物时 Append=true 的处理。
// 当 len(task.Artifacts)==0 时, Append 分支条件不满足, 应作为新产物加入。
func TestApplyUpdateAppendOnEmpty(t *testing.T) {
	card := &AgentCard{Name: "t", URL: "http://x", Version: "1", Capabilities: AgentCapabilities{Streaming: true}}
	s := NewServer(card, NewEchoHandler())
	task := NewTask("ae")

	s.applyUpdate(task, &TaskUpdate{Artifact: &Artifact{
		Append: true, Parts: []Part{&TextPart{Text: "x"}},
	}})
	if len(task.Artifacts) != 1 {
		t.Errorf("空产物列表上 Append=true 应新增, 实际 len = %d", len(task.Artifacts))
	}
}

// ============== 并发竞态 ==============

// TestMemoryStoreConcurrency 并发创建/读取/列出任务, 配合 -race 检测竞态。
func TestMemoryStoreConcurrency(t *testing.T) {
	store := NewMemoryTaskStore(WithMaxTasks(0)) // 无上限避免清理干扰
	defer store.Close()
	ctx := context.Background()

	var wg sync.WaitGroup
	const n = 50
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			task := NewTask(store.GenerateID())
			task.SessionID = "sess"
			if err := store.Create(ctx, task); err != nil {
				t.Errorf("并发 Create error = %v", err)
				return
			}
			store.Get(ctx, task.ID)
			store.List(ctx, &ListTasksRequest{SessionID: "sess"})
			store.Update(ctx, task)
		}(i)
	}
	wg.Wait()

	resp, err := store.List(ctx, &ListTasksRequest{SessionID: "sess"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(resp.Tasks) != n {
		t.Errorf("并发创建 %d 个任务, 实际存储 %d 个", n, len(resp.Tasks))
	}
}

// TestServerNotifySubscribersConcurrency 并发取消任务触发 notifySubscribers,
// 配合 -race 检测订阅者通知的竞态安全性。
func TestServerNotifySubscribersConcurrency(t *testing.T) {
	card := &AgentCard{Name: "t", URL: "http://x", Version: "1", Capabilities: AgentCapabilities{Streaming: true}}
	s := NewServer(card, NewEchoHandler())

	taskID := "concurrent"
	var wg sync.WaitGroup
	// 并发订阅与通知
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := make(chan StreamEvent, 1)
			s.subscribe(taskID, ch)
			s.notifySubscribers(taskID, &TaskStatusEvent{ID: taskID})
			s.unsubscribe(taskID, ch)
		}()
	}
	wg.Wait()
}

// ============== PushManager ==============

// fakePushService 记录推送调用次数, 可控制返回错误。
type fakePushService struct {
	mu       sync.Mutex
	calls    int
	failTill int // 前 failTill 次返回错误
}

func (f *fakePushService) Push(_ context.Context, _ *PushNotificationConfig, _ *Task) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failTill {
		return errSimulatedPush
	}
	return nil
}

func (f *fakePushService) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var errSimulatedPush = &Error{Code: CodeInternalError, Message: "simulated"}

// TestPushManagerNoConfig 验证无配置任务的推送被静默跳过。
func TestPushManagerNoConfig(t *testing.T) {
	fake := &fakePushService{}
	m := NewPushManager(fake)
	if err := m.PushTask(context.Background(), NewTask("nopush")); err != nil {
		t.Errorf("无配置推送应静默成功, err = %v", err)
	}
	if fake.count() != 0 {
		t.Errorf("无配置不应调用底层服务, 实际调用 %d 次", fake.count())
	}
}

// TestPushManagerRetry 验证带重试的推送在底层先失败后成功时最终成功。
func TestPushManagerRetry(t *testing.T) {
	fake := &fakePushService{failTill: 2} // 前 2 次失败, 第 3 次成功
	m := NewPushManager(fake, WithRetryConfig(RetryConfig{
		MaxRetries:   3,
		InitialDelay: time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}))
	m.SetConfig("rt", &PushNotificationConfig{URL: "http://hook"})

	if err := m.PushTask(context.Background(), &Task{ID: "rt", Status: TaskStatus{State: TaskStateCompleted}}); err != nil {
		t.Errorf("重试后应成功, err = %v", err)
	}
	if fake.count() != 3 {
		t.Errorf("应调用 3 次 (2 失败 + 1 成功), 实际 %d 次", fake.count())
	}
}

// TestPushManagerConfigLifecycle 验证推送配置的增删查。
func TestPushManagerConfigLifecycle(t *testing.T) {
	m := NewPushManager(&fakePushService{})
	cfg := &PushNotificationConfig{URL: "http://hook"}
	m.SetConfig("task-1", cfg)

	got, ok := m.GetConfig("task-1")
	if !ok || got.URL != "http://hook" {
		t.Errorf("GetConfig = %v %v, want hook true", got, ok)
	}
	m.RemoveConfig("task-1")
	if _, ok := m.GetConfig("task-1"); ok {
		t.Error("RemoveConfig 后不应再查到配置")
	}
}

// ============== AsyncPushService ==============

// TestAsyncPushServiceQueueFull 验证队列满时返回错误 (不阻塞)。
func TestAsyncPushServiceQueueFull(t *testing.T) {
	// 阻塞型底层服务, 让 worker 卡住, 使队列快速填满
	blocker := &blockingPushService{block: make(chan struct{})}
	svc := NewAsyncPushService(blocker, 1, 1) // 队列容量 1, worker 1

	ctx := context.Background()
	cfg := &PushNotificationConfig{URL: "http://x"}

	// 让 worker 取走一个并阻塞
	_ = svc.Push(ctx, cfg, NewTask("a"))
	time.Sleep(20 * time.Millisecond) // 等 worker 拿走并阻塞
	// 填满队列 (容量 1)
	_ = svc.Push(ctx, cfg, NewTask("b"))

	// 此时队列应已满, 下一个 Push 应返回错误
	var gotFull bool
	for i := 0; i < 5; i++ {
		if err := svc.Push(ctx, cfg, NewTask("c")); err != nil {
			gotFull = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(blocker.block) // 解除阻塞
	svc.Close()

	if !gotFull {
		t.Error("队列满时 Push 应返回错误")
	}
}

type blockingPushService struct {
	block chan struct{}
}

func (b *blockingPushService) Push(_ context.Context, _ *PushNotificationConfig, _ *Task) error {
	<-b.block
	return nil
}

// ============== HTTPPushService / WebhookPushService ==============

// TestWebhookPushServiceSuccess 用 httptest 模拟接收端, 验证推送请求构造与成功路径。
func TestWebhookPushServiceSuccess(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc := NewWebhookPushService()
	cfg := &PushNotificationConfig{URL: srv.URL, Token: "secret"}
	task := &Task{ID: "wh", Status: TaskStatus{State: TaskStateCompleted}}

	if err := svc.Push(context.Background(), cfg, task); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("推送 Authorization = %q, want Bearer secret", gotAuth)
	}
	if !strings.Contains(gotBody, `"wh"`) {
		t.Errorf("推送 body 未包含任务 ID: %s", gotBody)
	}
}

// TestWebhookPushServiceErrorStatus 验证接收端返回 >=400 时报错。
func TestWebhookPushServiceErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()

	svc := NewWebhookPushService()
	err := svc.Push(context.Background(), &PushNotificationConfig{URL: srv.URL}, &Task{ID: "x"})
	if err == nil {
		t.Error("接收端 500 应返回错误")
	}
}

// TestWebhookPushServiceEmptyURL 验证空 URL 静默成功。
func TestWebhookPushServiceEmptyURL(t *testing.T) {
	svc := NewWebhookPushService()
	if err := svc.Push(context.Background(), &PushNotificationConfig{URL: ""}, &Task{ID: "x"}); err != nil {
		t.Errorf("空 URL 应静默成功, err = %v", err)
	}
}

// ============== Discovery ==============

// TestRemoteDiscoveryWithHTTPTest 用 httptest 暴露真实 agent card, 验证远程发现。
func TestRemoteDiscoveryWithHTTPTest(t *testing.T) {
	card := NewAgentCard("remote-svc", "", "1.0")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == PathAgentCard {
			w.Header().Set("Content-Type", ContentTypeJSON)
			json.NewEncoder(w).Encode(card)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	d := NewRemoteDiscovery(time.Minute)
	d.AddAgent(srv.URL)

	cards, err := d.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(cards) != 1 || cards[0].Name != "remote-svc" {
		t.Errorf("Discover() = %+v, want [remote-svc]", cards)
	}

	// 第二次应命中缓存
	got, err := d.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "remote-svc" {
		t.Errorf("Get().Name = %q, want remote-svc", got.Name)
	}
}

// TestStaticDiscoveryDeregister 验证静态发现的注销。
func TestStaticDiscoveryDeregister(t *testing.T) {
	card := &AgentCard{Name: "a", URL: "http://a"}
	d := NewStaticDiscovery(card)
	ctx := context.Background()

	if _, err := d.Get(ctx, "http://a"); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if err := d.Deregister(ctx, "http://a"); err != nil {
		t.Fatalf("Deregister() error = %v", err)
	}
	if _, err := d.Get(ctx, "http://a"); err != ErrTaskNotFound {
		t.Errorf("注销后 Get err = %v, want %v", err, ErrTaskNotFound)
	}
}

// TestStaticDiscoveryWatchClosed 验证静态发现的 Watch 返回已关闭通道。
func TestStaticDiscoveryWatchClosed(t *testing.T) {
	d := NewStaticDiscovery()
	ch, err := d.Watch(context.Background(), nil)
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("静态发现 Watch 通道应已关闭")
		}
	case <-time.After(time.Second):
		t.Error("静态发现 Watch 通道应立即可读 (已关闭)")
	}
}

// ============== Error 类型 ==============

func TestErrorFormatting(t *testing.T) {
	withData := NewError(CodeInvalidParams, "bad", "detail")
	if !strings.Contains(withData.Error(), "detail") {
		t.Errorf("带 data 的错误字符串应含 detail, 实际 = %q", withData.Error())
	}
	noData := NewError(CodeInvalidParams, "bad")
	if strings.Contains(noData.Error(), "detail") {
		t.Errorf("无 data 的错误字符串不应含 detail, 实际 = %q", noData.Error())
	}

	if ToA2AError(nil) != nil {
		t.Error("ToA2AError(nil) 应返回 nil")
	}
	if !IsA2AError(NewError(CodeInternalError, "x")) {
		t.Error("IsA2AError 应识别 *Error")
	}
	if IsA2AError(errSimulatedPlain) {
		t.Error("IsA2AError 不应把普通 error 识别为 A2A error")
	}
}

var errSimulatedPlain = &plainErr{}

type plainErr struct{}

func (e *plainErr) Error() string { return "plain" }
