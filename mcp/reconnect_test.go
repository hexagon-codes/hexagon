package mcp

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/hexagon-codes/toolkit/util/retry"
)

// flakyTransport 是一个可控的传输层桩：前 failInits 次 Initialize 失败、之后成功；
// ListTools 始终返回 tools 列表。用于验证指数退避自动重连。
type flakyTransport struct {
	failInits int // 还需失败的 Initialize 次数
	initCalls int // Initialize 调用计数
	listCalls int // ListTools 调用计数
	tools     []map[string]any
}

func (f *flakyTransport) Send(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	switch req.Method {
	case MethodInitialize:
		f.initCalls++
		if f.initCalls <= f.failInits {
			return nil, errors.New("connection refused")
		}
		return &MCPResponse{Result: map[string]any{"capabilities": map[string]any{}}}, nil
	case MethodToolsList:
		f.listCalls++
		return &MCPResponse{Result: map[string]any{"tools": f.tools}}, nil
	default:
		return &MCPResponse{Result: map[string]any{}}, nil
	}
}

func (f *flakyTransport) Close() error { return nil }

// fastReconnectConfig 退避极短，避免测试因等待变慢。
func fastReconnectConfig(maxAttempts int) *ReconnectConfig {
	return &ReconnectConfig{
		MaxAttempts:  maxAttempts,
		InitialDelay: time.Microsecond,
		MaxDelay:     time.Millisecond,
		Multiplier:   2.0,
	}
}

// TestReconnect_ExponentialBackoffSucceeds 验证前两次 Initialize 失败、退避重试后第三次成功并刷新工具集。
func TestReconnect_ExponentialBackoffSucceeds(t *testing.T) {
	ft := &flakyTransport{failInits: 2, tools: []map[string]any{{"name": "a"}}}
	client := NewTransportClient(ft)
	ts := NewMCPToolSet(client, nil)
	ts.SetReconnectPolicy(fastReconnectConfig(5))

	if err := ts.Reconnect(context.Background()); err != nil {
		t.Fatalf("退避重连应成功: %v", err)
	}
	if ft.initCalls != 3 { // 失败 2 次 + 成功 1 次
		t.Errorf("Initialize 应调用 3 次, got %d", ft.initCalls)
	}
	if _, ok := ts.Get("a"); !ok {
		t.Error("重连后应刷新出工具 a")
	}
}

// TestReconnect_ExhaustReturnsError 验证重试次数耗尽仍失败时返回错误。
func TestReconnect_ExhaustReturnsError(t *testing.T) {
	ft := &flakyTransport{failInits: 100} // 永远失败
	ts := NewMCPToolSet(NewTransportClient(ft), nil)
	ts.SetReconnectPolicy(fastReconnectConfig(3))

	if err := ts.Reconnect(context.Background()); err == nil {
		t.Error("重试耗尽应返回错误")
	}
	if ft.initCalls != 3 {
		t.Errorf("应恰好尝试 3 次 Initialize, got %d", ft.initCalls)
	}
}

// TestBugRev0618_1_ReconnectZeroConfigStillAttemptsOnce 回归锁定 [BUG-REV0618-1]：
// 零值 ReconnectConfig 表示执行一次、零次重试；即使初始化失败，也必须调用一次
// Initialize，而不是在适配 Toolkit 前因缺少退避字段直接拒绝。
func TestBugRev0618_1_ReconnectZeroConfigStillAttemptsOnce(t *testing.T) {
	ft := &flakyTransport{failInits: 100}
	ts := NewMCPToolSet(NewTransportClient(ft), nil)
	ts.SetReconnectPolicy(&ReconnectConfig{})

	err := ts.Reconnect(context.Background())
	if !errors.Is(err, retry.ErrMaxAttemptsReached) {
		t.Fatalf("zero-value configuration error = %v, want retry exhaustion chain", err)
	}
	if ft.initCalls != 1 {
		t.Errorf("zero-value configuration Initialize calls = %d, want 1", ft.initCalls)
	}
	if ft.listCalls != 0 {
		t.Errorf("tool-list calls after initialization failure = %d, want 0", ft.listCalls)
	}
}

// TestReconnect_PartialConfigUsesDefaults 验证部分配置只补齐未设置的零值字段。
func TestReconnect_PartialConfigUsesDefaults(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ReconnectConfig
	}{
		{name: "only attempts", cfg: &ReconnectConfig{MaxAttempts: 1}},
		{name: "only initial delay", cfg: &ReconnectConfig{InitialDelay: time.Microsecond}},
		{name: "only max delay", cfg: &ReconnectConfig{MaxDelay: time.Millisecond}},
		{name: "only multiplier", cfg: &ReconnectConfig{Multiplier: 1.5}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ft := &flakyTransport{tools: []map[string]any{{"name": "a"}}}
			ts := NewMCPToolSet(NewTransportClient(ft), nil)
			ts.SetReconnectPolicy(test.cfg)

			if err := ts.Reconnect(context.Background()); err != nil {
				t.Fatalf("normalized partial configuration error = %v, want nil", err)
			}
			if ft.initCalls != 1 {
				t.Errorf("partial configuration Initialize calls = %d, want 1", ft.initCalls)
			}
		})
	}
}

// TestReconnect_ExplicitInvalidConfigFailsClosed 验证显式非法值不被默认值掩盖。
func TestReconnect_ExplicitInvalidConfigFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ReconnectConfig
	}{
		{
			name: "negative attempts",
			cfg:  &ReconnectConfig{MaxAttempts: -1, InitialDelay: time.Microsecond, MaxDelay: time.Millisecond, Multiplier: 2},
		},
		{
			name: "negative initial delay",
			cfg:  &ReconnectConfig{MaxAttempts: 1, InitialDelay: -time.Microsecond, MaxDelay: time.Millisecond, Multiplier: 2},
		},
		{
			name: "negative max delay",
			cfg:  &ReconnectConfig{MaxAttempts: 1, InitialDelay: time.Microsecond, MaxDelay: -time.Millisecond, Multiplier: 2},
		},
		{
			name: "negative multiplier",
			cfg:  &ReconnectConfig{MaxAttempts: 1, InitialDelay: time.Microsecond, MaxDelay: time.Millisecond, Multiplier: -1},
		},
		{
			name: "nan multiplier",
			cfg:  &ReconnectConfig{MaxAttempts: 1, InitialDelay: time.Microsecond, MaxDelay: time.Millisecond, Multiplier: math.NaN()},
		},
		{
			name: "infinite multiplier",
			cfg:  &ReconnectConfig{MaxAttempts: 1, InitialDelay: time.Microsecond, MaxDelay: time.Millisecond, Multiplier: math.Inf(1)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ft := &flakyTransport{}
			ts := NewMCPToolSet(NewTransportClient(ft), nil)
			ts.SetReconnectPolicy(test.cfg)

			err := ts.Reconnect(context.Background())
			if !errors.Is(err, retry.ErrInvalidConfig) {
				t.Fatalf("explicit invalid configuration error = %v, want ErrInvalidConfig chain", err)
			}
			if ft.initCalls != 0 {
				t.Errorf("explicit invalid configuration Initialize calls = %d, want 0", ft.initCalls)
			}
		})
	}
}

// TestRefresh_AutoReconnectOnFailure 验证 Refresh 拉取失败时按策略自动重连后重列成功。
func TestRefresh_AutoReconnectOnFailure(t *testing.T) {
	// listFailFirst 让首次 ListTools 失败一次（模拟连接断），重连后再列成功。
	lf := &listFailOnceTransport{tools: []map[string]any{{"name": "x"}}}
	ts := NewMCPToolSet(NewTransportClient(lf), nil)
	ts.SetReconnectPolicy(fastReconnectConfig(3))

	if err := ts.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh 应在自动重连后成功: %v", err)
	}
	if _, ok := ts.Get("x"); !ok {
		t.Error("重连重列后应有工具 x")
	}
}

// TestRefresh_NoReconnectPolicyFailsFast 验证未配置重连策略时，拉取失败直接返回错误（行为不变）。
func TestRefresh_NoReconnectPolicyFailsFast(t *testing.T) {
	lf := &listFailOnceTransport{tools: []map[string]any{{"name": "x"}}}
	ts := NewMCPToolSet(NewTransportClient(lf), nil)
	// 不设置重连策略

	if err := ts.Refresh(context.Background()); err == nil {
		t.Error("无重连策略时首次拉取失败应直接返回错误")
	}
}

// listFailOnceTransport 让首次 ListTools 失败、之后成功；Initialize 始终成功。
type listFailOnceTransport struct {
	initCalls int
	listCalls int
	tools     []map[string]any
}

func (f *listFailOnceTransport) Send(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	switch req.Method {
	case MethodInitialize:
		f.initCalls++
		return &MCPResponse{Result: map[string]any{"capabilities": map[string]any{}}}, nil
	case MethodToolsList:
		f.listCalls++
		if f.listCalls == 1 {
			return nil, errors.New("connection reset")
		}
		return &MCPResponse{Result: map[string]any{"tools": f.tools}}, nil
	default:
		return &MCPResponse{Result: map[string]any{}}, nil
	}
}

func (f *listFailOnceTransport) Close() error { return nil }

// TestRefresh_PartialReconnectConfigUsesDefaults 验证 Refresh 复用同一规范化边界。
func TestRefresh_PartialReconnectConfigUsesDefaults(t *testing.T) {
	lf := &listFailOnceTransport{tools: []map[string]any{{"name": "x"}}}
	ts := NewMCPToolSet(NewTransportClient(lf), nil)
	ts.SetReconnectPolicy(&ReconnectConfig{MaxAttempts: 1})

	if err := ts.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() with normalized partial reconnect configuration error = %v", err)
	}
	if lf.initCalls != 1 {
		t.Errorf("Refresh() Initialize calls = %d, want 1", lf.initCalls)
	}
	if lf.listCalls != 2 {
		t.Errorf("Refresh() tool-list calls after reconnect = %d, want 1", lf.listCalls)
	}
}
