// Package shell 提供 AI Agent 的 Shell 命令执行工具
//
// 本包实现了安全的命令行执行工具，支持：
//   - 命令执行 (Execute)
//   - 超时控制
//   - 输出大小限制
//
// 安全特性：
//   - 命令白名单/黑名单
//   - 执行超时
//   - 输出截断
//
// 使用示例：
//
//	executor := shell.New(shell.DefaultConfig())
//	tool := executor.Tool()
//	result, _ := tool.Execute(ctx, map[string]any{"command": "ls -la"})
package shell

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/toolkit/lang/stringx"
)

// Config Shell 工具配置
type Config struct {
	// Shell Shell 程序路径
	Shell string

	// Timeout 默认超时时间
	Timeout time.Duration

	// WorkDir 工作目录
	WorkDir string

	// Env 环境变量
	Env []string

	// AllowedCommands 允许的命令（空表示允许所有）
	AllowedCommands []string

	// DeniedCommands 禁止的命令
	DeniedCommands []string

	// MaxOutputSize 最大输出大小（字节）
	MaxOutputSize int
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	shell := "/bin/sh"
	if runtime.GOOS == "windows" {
		shell = "cmd"
	}

	return &Config{
		Shell:         shell,
		Timeout:       30 * time.Second,
		MaxOutputSize: 1024 * 1024, // 1MB
		DeniedCommands: []string{
			"rm -rf /",
			"rm -rf /*",
			"mkfs",
			"dd if=/dev/zero",
			":(){ :|:& };:",
			"chmod -R 777 /",
		},
	}
}

// Tool Shell 工具
type Tool struct {
	config *Config
}

// New 创建 Shell 工具
func New(config *Config) *Tool {
	if config == nil {
		config = DefaultConfig()
	}
	return &Tool{config: config}
}

// ExecuteTool 返回执行命令工具
func (t *Tool) ExecuteTool() tool.Tool {
	return tool.NewFunc("shell_execute", "执行 Shell 命令", t.execute)
}

// ExecuteInput 执行命令输入
type ExecuteInput struct {
	Command string   `json:"command" desc:"要执行的命令" required:"true"`
	Args    []string `json:"args" desc:"命令参数"`
	WorkDir string   `json:"work_dir" desc:"工作目录"`
	Timeout int      `json:"timeout" desc:"超时时间（秒）"`
}

// ExecuteOutput 执行命令输出
type ExecuteOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Duration string `json:"duration"`
}

// execute 执行命令
func (t *Tool) execute(ctx context.Context, input ExecuteInput) (*ExecuteOutput, error) {
	// 验证命令
	if err := t.validateCommand(input.Command); err != nil {
		return nil, err
	}

	// 验证参数（检查参数中是否有危险字符）
	for _, arg := range input.Args {
		if err := t.validateArg(arg); err != nil {
			return nil, err
		}
	}

	// 设置超时
	timeout := t.config.Timeout
	if input.Timeout > 0 {
		timeout = time.Duration(input.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 构建命令
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Windows: 使用 cmd /C，参数需要转义
		args := append([]string{"/C", input.Command}, escapeArgsWindows(input.Args)...)
		cmd = exec.CommandContext(ctx, t.config.Shell, args...)
	} else {
		// Unix: 优先使用直接执行方式（更安全），避免 shell 注入
		if len(input.Args) == 0 && !containsShellMetachars(input.Command) {
			// 简单命令，可以直接执行
			parts := strings.Fields(input.Command)
			if len(parts) > 0 {
				cmd = exec.CommandContext(ctx, parts[0], parts[1:]...)
			} else {
				return nil, fmt.Errorf("empty command")
			}
		} else {
			// 复杂命令或有参数，使用 shell -c
			// 参数使用 shell 转义
			fullCmd := input.Command
			if len(input.Args) > 0 {
				escapedArgs := make([]string, len(input.Args))
				for i, arg := range input.Args {
					escapedArgs[i] = shellEscape(arg)
				}
				fullCmd += " " + strings.Join(escapedArgs, " ")
			}
			cmd = exec.CommandContext(ctx, t.config.Shell, "-c", fullCmd)
		}
	}

	// 设置工作目录
	if input.WorkDir != "" {
		cmd.Dir = input.WorkDir
	} else if t.config.WorkDir != "" {
		cmd.Dir = t.config.WorkDir
	}

	// 设置环境变量
	if len(t.config.Env) > 0 {
		cmd.Env = append(cmd.Env, t.config.Env...)
	}

	// 执行命令
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	// 获取退出码
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("command timed out after %v", timeout)
		} else {
			return nil, fmt.Errorf("failed to execute command: %w", err)
		}
	}

	// 截断输出
	stdoutStr := stdout.String()
	stderrStr := stderr.String()
	if len(stdoutStr) > t.config.MaxOutputSize {
		stdoutStr = stringx.TruncateBytes(stdoutStr, t.config.MaxOutputSize, "\n... (truncated)")
	}
	if len(stderrStr) > t.config.MaxOutputSize {
		stderrStr = stringx.TruncateBytes(stderrStr, t.config.MaxOutputSize, "\n... (truncated)")
	}

	return &ExecuteOutput{
		Stdout:   stdoutStr,
		Stderr:   stderrStr,
		ExitCode: exitCode,
		Duration: duration.String(),
	}, nil
}

// validateCommand 验证命令
//
// 安全说明：
//   - 检查命令链接符（&&, ||, ;, |）防止命令注入
//   - 检查命令替换符（$(), “）防止注入
//   - 白名单模式下只允许指定的命令
func (t *Tool) validateCommand(cmd string) error {
	// 检查危险的命令链接符和命令替换符
	dangerousPatterns := []string{
		"&&", "||", ";", // 命令链接
		"|",       // 管道（可能被滥用）
		"$(", "`", // 命令替换
		"\n", "\r", // 换行符注入
		"\x00", // 空字节注入
	}
	cmdLower := strings.ToLower(cmd)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(cmdLower, pattern) {
			return fmt.Errorf("command contains dangerous pattern: %s", pattern)
		}
	}

	// 检查禁止的命令
	for _, denied := range t.config.DeniedCommands {
		// 使用更精确的匹配：检查命令是否包含禁止模式
		deniedLower := strings.ToLower(denied)
		if strings.Contains(cmdLower, deniedLower) {
			return fmt.Errorf("command not allowed: contains forbidden pattern '%s'", denied)
		}
	}

	// 检查允许的命令（如果设置了白名单）
	if len(t.config.AllowedCommands) > 0 {
		allowed := false
		cmdParts := strings.Fields(cmd)
		if len(cmdParts) > 0 {
			cmdName := cmdParts[0]
			// 获取命令的基础名称（去掉路径）
			baseName := filepath.Base(cmdName)
			for _, allowedCmd := range t.config.AllowedCommands {
				// 精确匹配命令名或基础名
				if cmdName == allowedCmd || baseName == allowedCmd {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			return fmt.Errorf("command not in allowed list: %s", cmd)
		}
	}

	return nil
}

// validateArg 验证参数安全性
func (t *Tool) validateArg(arg string) error {
	// 检查参数中是否有命令替换模式
	dangerousPatterns := []string{
		"$(", "`", // 命令替换
		"&&", "||", ";", // 命令链接
		"|",    // 管道
		">",    // 重定向
		"<",    // 输入重定向
		"\n",   // 换行符
		"\x00", // 空字节
	}
	for _, pattern := range dangerousPatterns {
		if strings.Contains(arg, pattern) {
			return fmt.Errorf("argument contains potentially dangerous pattern: %s", pattern)
		}
	}
	return nil
}

// shellEscape 对参数进行 shell 转义
// 使用单引号包裹，并转义单引号
func shellEscape(s string) string {
	// 空字符串返回空引号
	if s == "" {
		return "''"
	}
	// 使用单引号包裹，单引号本身需要特殊处理
	// 'arg' -> 'ar'\''g' (结束单引号，转义单引号，开始单引号)
	escaped := strings.ReplaceAll(s, "'", "'\"'\"'")
	return "'" + escaped + "'"
}

// escapeArgsWindows 对 Windows 参数进行转义
func escapeArgsWindows(args []string) []string {
	result := make([]string, len(args))
	for i, arg := range args {
		// Windows 使用双引号，需要转义双引号和反斜杠
		escaped := strings.ReplaceAll(arg, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		if strings.ContainsAny(escaped, " \t") {
			result[i] = `"` + escaped + `"`
		} else {
			result[i] = escaped
		}
	}
	return result
}

// containsShellMetachars 检查字符串是否包含 shell 元字符
func containsShellMetachars(s string) bool {
	metachars := []string{
		"|", "&", ";", "(", ")", "<", ">",
		"$", "`", "\\", "\"", "'", " ", "\t", "\n",
		"*", "?", "[", "]", "#", "~", "=", "%",
	}
	for _, c := range metachars {
		if strings.Contains(s, c) {
			return true
		}
	}
	return false
}

// Exec 便捷执行函数
func Exec(ctx context.Context, command string) (*ExecuteOutput, error) {
	t := New(nil)
	return t.execute(ctx, ExecuteInput{Command: command})
}

// ExecWithTimeout 带超时的便捷执行函数
func ExecWithTimeout(ctx context.Context, command string, timeout time.Duration) (*ExecuteOutput, error) {
	t := New(&Config{
		Shell:   DefaultConfig().Shell,
		Timeout: timeout,
	})
	return t.execute(ctx, ExecuteInput{Command: command})
}

// ============== 常用命令工具 ==============

// GitTool 返回 Git 命令工具
func GitTool(workDir string) tool.Tool {
	config := DefaultConfig()
	config.WorkDir = workDir
	config.AllowedCommands = []string{"git"}

	t := &Tool{config: config}
	return tool.NewFunc("git", "执行 Git 命令", func(ctx context.Context, input struct {
		Args string `json:"args" desc:"Git 命令参数" required:"true"`
	}) (*ExecuteOutput, error) {
		// 使用 Args 切片传入参数，避免 shell 注入
		args := strings.Fields(input.Args)
		return t.execute(ctx, ExecuteInput{
			Command: "git",
			Args:    args,
		})
	})
}

// CurlTool 返回 Curl 命令工具
func CurlTool() tool.Tool {
	config := DefaultConfig()
	config.AllowedCommands = []string{"curl"}
	config.Timeout = 60 * time.Second

	t := &Tool{config: config}
	return tool.NewFunc("curl", "执行 HTTP 请求", func(ctx context.Context, input struct {
		URL     string            `json:"url" desc:"请求 URL" required:"true"`
		Method  string            `json:"method" desc:"HTTP 方法（GET, POST 等）"`
		Headers map[string]string `json:"headers" desc:"请求头"`
		Data    string            `json:"data" desc:"请求数据"`
	}) (*ExecuteOutput, error) {
		args := []string{"-s", "-S"}

		if input.Method != "" {
			args = append(args, "-X", input.Method)
		}

		for k, v := range input.Headers {
			args = append(args, "-H", fmt.Sprintf("%s: %s", k, v))
		}

		if input.Data != "" {
			args = append(args, "-d", input.Data)
		}

		args = append(args, input.URL)

		// 使用 Args 切片传入参数，避免 shell 注入
		return t.execute(ctx, ExecuteInput{
			Command: "curl",
			Args:    args,
		})
	})
}

// LsTool 返回列出文件工具
func LsTool() tool.Tool {
	config := DefaultConfig()
	config.AllowedCommands = []string{"ls"}

	t := &Tool{config: config}
	return tool.NewFunc("ls", "列出文件和目录", func(ctx context.Context, input struct {
		Path string `json:"path" desc:"目录路径"`
		All  bool   `json:"all" desc:"显示隐藏文件"`
		Long bool   `json:"long" desc:"详细格式"`
	}) (*ExecuteOutput, error) {
		args := []string{}
		if input.All {
			args = append(args, "-a")
		}
		if input.Long {
			args = append(args, "-l")
		}
		if input.Path != "" {
			args = append(args, input.Path)
		}

		cmd := "ls"
		if len(args) > 0 {
			cmd += " " + strings.Join(args, " ")
		}

		return t.execute(ctx, ExecuteInput{Command: cmd})
	})
}

// GrepTool 返回搜索工具
func GrepTool() tool.Tool {
	config := DefaultConfig()
	config.AllowedCommands = []string{"grep"}

	t := &Tool{config: config}
	return tool.NewFunc("grep", "搜索文本内容", func(ctx context.Context, input struct {
		Pattern    string `json:"pattern" desc:"搜索模式" required:"true"`
		Path       string `json:"path" desc:"搜索路径"`
		Recursive  bool   `json:"recursive" desc:"递归搜索"`
		IgnoreCase bool   `json:"ignore_case" desc:"忽略大小写"`
	}) (*ExecuteOutput, error) {
		args := []string{}
		if input.Recursive {
			args = append(args, "-r")
		}
		if input.IgnoreCase {
			args = append(args, "-i")
		}
		args = append(args, input.Pattern)
		if input.Path != "" {
			args = append(args, input.Path)
		}

		return t.execute(ctx, ExecuteInput{
			Command: "grep " + strings.Join(args, " "),
		})
	})
}
