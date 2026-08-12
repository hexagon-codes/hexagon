// Package mcp 提供 MCP（Model Context Protocol）客户端与服务端能力
//
// 本文件实现 MCP 工具集的自动重连：当与远端 MCP 服务器的连接出现瞬时故障
// （网络抖动、服务器重启等）时，按指数退避重试初始化握手，恢复后重新拉取工具列表。
// 重连退避复用 toolkit/util/retry，不重复实现退避逻辑。
package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/hexagon-codes/toolkit/util/retry"
)

// ReconnectConfig 自动重连策略。
//
// 退避序列：第 n 次重试等待 min(InitialDelay × Multiplier^(n-1), MaxDelay)。
type ReconnectConfig struct {
	// MaxAttempts 最大尝试次数（含首次）；零值表示执行一次、零次重试
	MaxAttempts int
	// InitialDelay 首次重试前的等待；零值使用默认值
	InitialDelay time.Duration
	// MaxDelay 退避上限；零值使用默认值
	MaxDelay time.Duration
	// Multiplier 退避倍数（指数退避）；零值使用默认值
	Multiplier float64
}

// DefaultReconnectConfig 默认重连策略：最多 5 次、初始 500ms、上限 30s、倍数 2.0。
func DefaultReconnectConfig() *ReconnectConfig {
	return &ReconnectConfig{
		MaxAttempts:  5,
		InitialDelay: 500 * time.Millisecond,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
	}
}

// SetReconnectPolicy 设置工具集的自动重连策略。
//
// 设置后，Refresh 在拉取失败时会按该策略指数退避重连；传 nil 关闭自动重连。
func (s *MCPToolSet) SetReconnectPolicy(cfg *ReconnectConfig) {
	s.mu.Lock()
	s.reconnect = cfg
	s.mu.Unlock()
}

// Reconnect 主动按指数退避重连远端 MCP 服务器并刷新工具集。
//
// 未设置策略时使用 DefaultReconnectConfig。重连恢复连接后会重新拉取工具列表
// 并触发 OnToolsChanged 回调。重试耗尽仍失败则返回错误。
//
// 注意：本方法重试的是初始化握手，可恢复传输层能自愈的瞬时故障（HTTP/SSE
// 抖动、服务器重启后可重新连上）。对子进程已退出的 stdio 传输，进程重启属传输
// 层职责，本层重试会耗尽并返回错误。
func (s *MCPToolSet) Reconnect(ctx context.Context) error {
	s.mu.RLock()
	cfg := s.reconnect
	s.mu.RUnlock()
	if cfg == nil {
		cfg = DefaultReconnectConfig()
	}
	if err := s.reconnectWithBackoff(ctx, cfg); err != nil {
		return fmt.Errorf("mcp: 重连失败（已重试 %d 次）: %w", cfg.MaxAttempts, err)
	}
	return s.listAndRebuild(ctx)
}

// reconnectWithBackoff 按指数退避重试初始化握手，恢复连接（不刷新工具列表）。
//
// 仅补齐未设置的零值字段；显式负数及非有限值原样交由 Toolkit 严格校验。
func (s *MCPToolSet) reconnectWithBackoff(ctx context.Context, cfg *ReconnectConfig) error {
	normalized := normalizeReconnectConfig(cfg)
	return retry.DoWithContext(ctx, func() error {
		return s.client.Initialize(ctx)
	},
		retry.Attempts(normalized.MaxAttempts),
		retry.Delay(normalized.InitialDelay),
		retry.MaxDelay(normalized.MaxDelay),
		retry.Multiplier(normalized.Multiplier),
		retry.DelayType(retry.ExponentialBackoff),
	)
}

// normalizeReconnectConfig 将 Hexagon 的可选字段转换为 Toolkit 所需的完整配置。
func normalizeReconnectConfig(cfg *ReconnectConfig) ReconnectConfig {
	normalized := *cfg
	defaults := DefaultReconnectConfig()
	if normalized.MaxAttempts == 0 {
		normalized.MaxAttempts = 1
	}
	if normalized.InitialDelay == 0 {
		normalized.InitialDelay = defaults.InitialDelay
	}
	if normalized.MaxDelay == 0 {
		normalized.MaxDelay = defaults.MaxDelay
	}
	if normalized.Multiplier == 0 {
		normalized.Multiplier = defaults.Multiplier
	}
	return normalized
}
