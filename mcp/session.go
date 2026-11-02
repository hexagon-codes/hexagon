package mcp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/hexagon-codes/ai-core/tool"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Session 共享一次官方 SDK 连接的工具、资源与提示模板。
// 返回的工具与后续资源/模板操作均绑定同一连接，Close 可重复调用。
type Session struct {
	session        *sdkmcp.ClientSession
	tools          []tool.Tool
	closeOnce      sync.Once
	closeErr       error
	command        *exec.Cmd
	localRPCClosed atomic.Bool
}

// Tools 返回连接时发现的完整工具列表副本，不转移连接所有权。
func (s *Session) Tools() []tool.Tool {
	return append([]tool.Tool(nil), s.tools...)
}

// Resources 读取全部分页；未声明资源能力的服务返回空列表。
func (s *Session) Resources(ctx context.Context) ([]*sdkmcp.Resource, error) {
	if s.session.InitializeResult().Capabilities.Resources == nil {
		return nil, nil
	}
	var resources []*sdkmcp.Resource
	for resource, err := range s.session.Resources(ctx, nil) {
		if err != nil {
			return resources, &ProtocolError{Stage: "resources/list", Cause: err}
		}
		if resource == nil {
			return resources, &ProtocolError{Stage: "resources/list", Cause: fmt.Errorf("MCP server returned a null resource")}
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

// ResourceTemplates 读取服务公布的 URI 模板，不在客户端猜测或展开 URI。
func (s *Session) ResourceTemplates(ctx context.Context) ([]*sdkmcp.ResourceTemplate, error) {
	if s.session.InitializeResult().Capabilities.Resources == nil {
		return nil, nil
	}
	var templates []*sdkmcp.ResourceTemplate
	for template, err := range s.session.ResourceTemplates(ctx, nil) {
		if err != nil {
			return templates, &ProtocolError{Stage: "resources/templates/list", Cause: err}
		}
		if template == nil {
			return templates, &ProtocolError{Stage: "resources/templates/list", Cause: fmt.Errorf("MCP server returned a null resource template")}
		}
		templates = append(templates, template)
	}
	return templates, nil
}

// ReadResource 保留全部文本、二进制内容及 MIME 元数据。
func (s *Session) ReadResource(ctx context.Context, uri string) (*sdkmcp.ReadResourceResult, error) {
	if s.session.InitializeResult().Capabilities.Resources == nil {
		return nil, fmt.Errorf("MCP server does not support resources")
	}
	result, err := s.session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: uri})
	if err != nil {
		return nil, &ProtocolError{Stage: "resources/read", Cause: err}
	}
	return result, nil
}

// Prompts 读取全部分页；未声明模板能力的服务返回空列表。
func (s *Session) Prompts(ctx context.Context) ([]*sdkmcp.Prompt, error) {
	if s.session.InitializeResult().Capabilities.Prompts == nil {
		return nil, nil
	}
	var prompts []*sdkmcp.Prompt
	for prompt, err := range s.session.Prompts(ctx, nil) {
		if err != nil {
			return prompts, &ProtocolError{Stage: "prompts/list", Cause: err}
		}
		if prompt == nil {
			return prompts, &ProtocolError{Stage: "prompts/list", Cause: fmt.Errorf("MCP server returned a null prompt")}
		}
		prompts = append(prompts, prompt)
	}
	return prompts, nil
}

// GetPrompt 将参数原样交给服务，保留响应的角色与多模态内容。
func (s *Session) GetPrompt(ctx context.Context, name string, args map[string]string) (*sdkmcp.GetPromptResult, error) {
	if s.session.InitializeResult().Capabilities.Prompts == nil {
		return nil, fmt.Errorf("MCP server does not support prompts")
	}
	result, err := s.session.GetPrompt(ctx, &sdkmcp.GetPromptParams{Name: name, Arguments: args})
	if err != nil {
		return nil, &ProtocolError{Stage: "prompts/get", Cause: err}
	}
	return result, nil
}

// Close 只关闭本 Session 持有的连接，所有调用共享同一关闭结果。
// SDK 主动终止且已 Wait 回收的 stdio 进程属于关闭完成，不表示工具任务成功。
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = normalizeStdioCloseError(s.session.Close(), s.command)
		// 官方 SDK 的 Close 等待本地 RPC 退出；远端清理错误仍由 closeErr 保留。
		s.localRPCClosed.Store(true)
	})
	return s.closeErr
}

// LocalRPCClosed 只报告本地 SDK 连接已经关闭，不表示远端动作或子进程已停止。
// 这是具体 Session 的可选能力，不改变旧连接接口和 Close 的错误合同。
func (s *Session) LocalRPCClosed() bool {
	return s.localRPCClosed.Load()
}

// normalizeStdioCloseError 只去掉本连接已回收进程的预期终止分支。
// 关闭管道、会话清理或其他 joined 错误仍原样返回，不依赖错误文字判断。
func normalizeStdioCloseError(err error, command *exec.Cmd) error {
	if err == nil || command == nil {
		return err
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var remaining []error
		removed := false
		for _, cause := range joined.Unwrap() {
			if cause == nil {
				continue
			}
			if filtered := normalizeStdioCloseError(cause, command); filtered != nil {
				remaining = append(remaining, filtered)
			} else {
				removed = true
			}
		}
		if !removed {
			return err
		}
		return errors.Join(remaining...)
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if cause := wrapped.Unwrap(); cause != nil && normalizeStdioCloseError(cause, command) == nil {
			return nil
		}
		return err
	}
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ProcessState == nil || exit.ProcessState != command.ProcessState {
		return err
	}
	status, ok := exit.ProcessState.Sys().(interface {
		Signaled() bool
		Signal() syscall.Signal
	})
	if ok && status.Signaled() && (status.Signal() == syscall.SIGTERM || status.Signal() == syscall.SIGKILL) {
		return nil
	}
	return err
}
