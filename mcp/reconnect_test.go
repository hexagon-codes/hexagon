package mcp

import (
	"context"
	"errors"
	"testing"
	"time"
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
// 零值 ReconnectConfig（MaxAttempts=0）经规范化兜底后仍至少尝试一次 Initialize，
// 而非一次不跑直接失败（底层 retry 循环 attempt=1..MaxAttempts，0 时 fn 永不执行）。
func TestBugRev0618_1_ReconnectZeroConfigStillAttemptsOnce(t *testing.T) {
	ft := &flakyTransport{failInits: 0, tools: []map[string]any{{"name": "a"}}} // 首次即成功
	ts := NewMCPToolSet(NewTransportClient(ft), nil)
	ts.SetReconnectPolicy(&ReconnectConfig{}) // 全零值

	if err := ts.Reconnect(context.Background()); err != nil {
		t.Fatalf("零值配置经兜底应成功重连: %v", err)
	}
	if ft.initCalls != 1 {
		t.Errorf("零值配置应至少尝试 1 次 Initialize, got %d", ft.initCalls)
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
	listCalls int
	tools     []map[string]any
}

func (f *listFailOnceTransport) Send(ctx context.Context, req *MCPRequest) (*MCPResponse, error) {
	switch req.Method {
	case MethodInitialize:
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
