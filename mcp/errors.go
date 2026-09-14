package mcp

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

const (
	maxStdioDiagnosticBytes = 8 << 10
	maxDiagnosticTextBytes  = 4 << 10
)

// ProtocolError 标记 MCP 会话在哪个协议阶段失败。
// Stage 只使用稳定的协议阶段名称，便于上层分类和诊断。
type ProtocolError struct {
	Stage string
	Cause error
}

func (e *ProtocolError) Error() string {
	if e == nil {
		return "MCP protocol error"
	}
	if e.Cause == nil {
		return fmt.Sprintf("MCP protocol error during %s", e.Stage)
	}
	switch e.Stage {
	case "initialize":
		return fmt.Sprintf("连接 MCP Server 失败: %v", e.Cause)
	case "tools/list":
		return fmt.Sprintf("获取 MCP 工具列表失败: %v", e.Cause)
	default:
		return fmt.Sprintf("MCP protocol failed during %s: %v", e.Stage, e.Cause)
	}
}

func (e *ProtocolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// StdioConnectError 保留 stdio 子进程启动及 MCP 握手失败的可操作事实。
// Stderr 只包含截断且脱敏后的子进程诊断，不包含环境变量或凭据原文。
type StdioConnectError struct {
	Stage       string
	Cause       error
	ExitCode    int
	HasExitCode bool
	Signal      string
	Stderr      string
}

func (e *StdioConnectError) Error() string {
	if e == nil {
		return "stdio MCP server connection failed"
	}
	parts := make([]string, 0, 4)
	if e.Stage != "" {
		parts = append(parts, "stage="+e.Stage)
	}
	if e.HasExitCode {
		parts = append(parts, fmt.Sprintf("exit_code=%d", e.ExitCode))
	} else if e.Signal != "" {
		parts = append(parts, "signal="+e.Signal)
	}
	if e.Stderr != "" {
		parts = append(parts, "stderr="+e.Stderr)
	}
	if e.Cause != nil {
		parts = append(parts, "cause="+sanitizeDiagnostic(e.Cause.Error()))
	}
	if len(parts) == 0 {
		return "stdio MCP server connection failed"
	}
	return "stdio MCP server connection failed (" + strings.Join(parts, ", ") + ")"
}

func (e *StdioConnectError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// diagnosticBuffer 收集有限长度 stderr，避免异常子进程无限刷屏或占用内存。
type diagnosticBuffer struct {
	mu    sync.Mutex
	data  bytes.Buffer
	limit int
}

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	consumed := len(p)
	remaining := b.limit - b.data.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.data.Write(p)
	}
	// io.Writer 合约要求返回完整消费长度；超出部分被有意丢弃。
	return consumed, nil
}

func (b *diagnosticBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return sanitizeDiagnostic(b.data.String())
}

var (
	credentialAssignmentPattern = regexp.MustCompile(`(?i)((?:password|passwd|pass|token|secret|api[_-]?key|authorization|credential)["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|(?:bearer|basic)\s+[^\s,;]+|[^\s,;]+)`)
	urlCredentialPattern        = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)([^/@\s]+)@`)
	authSchemePattern           = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)[^\s,;]+`)
)

func sanitizeDiagnostic(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = urlCredentialPattern.ReplaceAllString(s, `$1[REDACTED]@`)
	s = credentialAssignmentPattern.ReplaceAllString(s, `$1[REDACTED]`)
	s = authSchemePattern.ReplaceAllString(s, `$1$2[REDACTED]`)
	if len(s) > maxDiagnosticTextBytes {
		s = strings.ToValidUTF8(s[:maxDiagnosticTextBytes], "") + "…"
	}
	return s
}

func protocolStage(err error) string {
	var protocolErr *ProtocolError
	if errors.As(err, &protocolErr) && protocolErr != nil && protocolErr.Stage != "" {
		return protocolErr.Stage
	}
	return "connect"
}
