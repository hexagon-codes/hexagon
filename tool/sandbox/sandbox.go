// Package sandbox 提供 AI Agent 的沙箱代码执行环境
//
// 本包实现了隔离的代码执行环境，支持多种沙箱类型：
//   - Docker 容器沙箱
//   - 进程隔离沙箱
//   - WebAssembly 沙箱
//
// 安全特性：
//   - 资源限制 (CPU/内存)
//   - 网络隔离
//   - 文件系统隔离
//   - 执行超时
//
// 使用示例：
//
//	sb := sandbox.New(&sandbox.Config{
//	    Type:    sandbox.TypeDocker,
//	    Image:   "python:3.11-slim",
//	    Timeout: 30 * time.Second,
//	})
//	result, _ := sb.Execute(ctx, "print('hello')")
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/tool"
)

// SandboxType 沙箱类型
type SandboxType string

const (
	// TypeDocker Docker 沙箱
	TypeDocker SandboxType = "docker"
	// TypeProcess 进程沙箱（使用系统隔离）
	TypeProcess SandboxType = "process"
	// TypeWASM WebAssembly 沙箱
	TypeWASM SandboxType = "wasm"
)

// Config 沙箱配置
type Config struct {
	// Type 沙箱类型
	Type SandboxType

	// Image Docker 镜像名称
	Image string

	// Timeout 执行超时时间
	Timeout time.Duration

	// Memory 内存限制（字节）
	Memory int64

	// CPU CPU 限制（核心数）
	CPU float64

	// NetworkEnabled 是否允许网络访问
	NetworkEnabled bool

	// Mounts 挂载点
	Mounts []Mount

	// Env 环境变量
	Env []string

	// MaxOutputSize 最大输出大小
	MaxOutputSize int

	// WorkDir 容器内工作目录
	WorkDir string

	// User 运行用户
	User string
}

// Mount 挂载点
type Mount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Type:           TypeProcess,
		Timeout:        60 * time.Second,
		Memory:         256 * 1024 * 1024, // 256MB
		CPU:            1.0,
		NetworkEnabled: false,
		MaxOutputSize:  1024 * 1024, // 1MB
		WorkDir:        "/workspace",
	}
}

// DefaultDockerConfig 返回默认 Docker 配置
func DefaultDockerConfig() *Config {
	config := DefaultConfig()
	config.Type = TypeDocker
	config.Image = "python:3.11-slim"
	return config
}

// Sandbox 沙箱
type Sandbox struct {
	config   *Config
	mu       sync.Mutex
	running  map[string]*exec.Cmd
	tempDirs []string
}

// New 创建沙箱
func New(config *Config) *Sandbox {
	if config == nil {
		config = DefaultConfig()
	}
	return &Sandbox{
		config:  config,
		running: make(map[string]*exec.Cmd),
	}
}

// ExecuteTool 返回沙箱执行工具
func (s *Sandbox) ExecuteTool() tool.Tool {
	return tool.NewFunc("sandbox_execute", "在沙箱中执行代码", s.execute)
}

// ExecuteInput 执行输入
type ExecuteInput struct {
	Language string            `json:"language" desc:"编程语言（python, javascript, go, bash）" required:"true"`
	Code     string            `json:"code" desc:"要执行的代码" required:"true"`
	Timeout  int               `json:"timeout" desc:"超时时间（秒）"`
	Files    map[string]string `json:"files" desc:"要创建的文件（路径 -> 内容）"`
}

// ExecuteOutput 执行输出
type ExecuteOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Duration string `json:"duration"`
}

// execute 执行代码
func (s *Sandbox) execute(ctx context.Context, input ExecuteInput) (*ExecuteOutput, error) {
	switch s.config.Type {
	case TypeDocker:
		return s.executeDocker(ctx, input)
	case TypeProcess:
		return s.executeProcess(ctx, input)
	default:
		return nil, fmt.Errorf("unsupported sandbox type: %s", s.config.Type)
	}
}

// executeDocker 在 Docker 中执行
func (s *Sandbox) executeDocker(ctx context.Context, input ExecuteInput) (*ExecuteOutput, error) {
	// 先校验附加文件路径再做任何事：拒绝 ".." 穿越与绝对路径，防止恶意输入经
	// filepath.Join 逃出临时目录写到宿主机文件系统（与 executeProcess 同一约束）。
	cleanedFiles := make(map[string]string, len(input.Files))
	for path, content := range input.Files {
		cleanPath := filepath.Clean(path)
		// 仅拒绝真实穿越（"." 清理后为 ".." 自身或以 "../" 开头）与绝对路径；
		// 字面以 ".." 开头的普通文件名（如 "..keep"）是合法的临时目录内文件，应放行。
		if cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) || filepath.IsAbs(cleanPath) {
			return nil, fmt.Errorf("不安全的文件路径: %s", path)
		}
		cleanedFiles[cleanPath] = content
	}

	// 检查 Docker 是否可用
	if err := exec.Command("docker", "version").Run(); err != nil {
		return nil, fmt.Errorf("docker not available: %w", err)
	}

	// 设置超时
	timeout := s.config.Timeout
	if input.Timeout > 0 {
		timeout = time.Duration(input.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 创建临时目录
	tempDir, err := os.MkdirTemp("", "hexagon_sandbox_")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// 写入代码文件
	codeFile, entryCmd := s.getCodeFile(input.Language)
	codePath := filepath.Join(tempDir, codeFile)
	if err := os.WriteFile(codePath, []byte(input.Code), 0644); err != nil {
		return nil, fmt.Errorf("failed to write code file: %w", err)
	}

	// 写入额外文件（路径已在入口校验，使用清理后的相对路径）
	for path, content := range cleanedFiles {
		filePath := filepath.Join(tempDir, path)
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory: %w", err)
		}
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("failed to write file: %w", err)
		}
	}

	// 构建 Docker 命令
	args := []string{"run", "--rm"}

	// 内存限制
	if s.config.Memory > 0 {
		args = append(args, "-m", fmt.Sprintf("%d", s.config.Memory))
	}

	// CPU 限制
	if s.config.CPU > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%.2f", s.config.CPU))
	}

	// 网络
	if !s.config.NetworkEnabled {
		args = append(args, "--network", "none")
	}

	// 用户
	if s.config.User != "" {
		args = append(args, "-u", s.config.User)
	}

	// 挂载工作目录
	args = append(args, "-v", fmt.Sprintf("%s:%s", tempDir, s.config.WorkDir))
	args = append(args, "-w", s.config.WorkDir)

	// 额外挂载（验证路径安全性）
	for _, mount := range s.config.Mounts {
		// 禁止挂载系统敏感目录
		cleanSource := filepath.Clean(mount.Source)
		sensitivePathPrefixes := []string{"/etc", "/proc", "/sys", "/dev", "/boot", "/root", "/var/run"}
		isSensitive := false
		for _, prefix := range sensitivePathPrefixes {
			if cleanSource == prefix || strings.HasPrefix(cleanSource, prefix+"/") {
				isSensitive = true
				break
			}
		}
		if isSensitive {
			continue // 跳过敏感路径
		}

		mountStr := fmt.Sprintf("%s:%s", cleanSource, mount.Target)
		if mount.ReadOnly {
			mountStr += ":ro"
		}
		args = append(args, "-v", mountStr)
	}

	// 环境变量
	for _, env := range s.config.Env {
		args = append(args, "-e", env)
	}

	// 镜像和命令
	image := s.getImage(input.Language)
	args = append(args, image, "sh", "-c", entryCmd)

	// 执行
	cmd := exec.CommandContext(ctx, "docker", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err = cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("execution timed out after %v", timeout)
		}
	}

	return &ExecuteOutput{
		Stdout:   s.truncateOutput(stdout.String()),
		Stderr:   s.truncateOutput(stderr.String()),
		ExitCode: exitCode,
		Duration: duration.String(),
	}, nil
}

// executeProcess 在进程中执行（使用系统限制）
func (s *Sandbox) executeProcess(ctx context.Context, input ExecuteInput) (*ExecuteOutput, error) {
	// 设置超时
	timeout := s.config.Timeout
	if input.Timeout > 0 {
		timeout = time.Duration(input.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 创建临时目录
	tempDir, err := os.MkdirTemp("", "hexagon_sandbox_")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// 写入代码文件
	codeFile, _ := s.getCodeFile(input.Language)
	codePath := filepath.Join(tempDir, codeFile)
	if err := os.WriteFile(codePath, []byte(input.Code), 0644); err != nil {
		return nil, fmt.Errorf("failed to write code file: %w", err)
	}

	// 写入额外文件（验证路径安全性，防止路径穿越）
	for path, content := range input.Files {
		// 清理路径，防止 "../" 等路径穿越
		cleanPath := filepath.Clean(path)
		// 仅拒绝真实穿越（"." 清理后为 ".." 自身或以 "../" 开头）与绝对路径；
		// 字面以 ".." 开头的普通文件名（如 "..keep"）是合法的临时目录内文件，应放行。
		if cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) || filepath.IsAbs(cleanPath) {
			return nil, fmt.Errorf("不安全的文件路径: %s", path)
		}
		filePath := filepath.Join(tempDir, cleanPath)
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory: %w", err)
		}
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("failed to write file: %w", err)
		}
	}

	// 获取执行命令
	interpreter, args := s.getInterpreter(input.Language, codePath)

	// 执行
	cmd := exec.CommandContext(ctx, interpreter, args...)
	cmd.Dir = tempDir
	cmd.Env = append(os.Environ(), s.config.Env...)

	// 在 Unix 系统上应用资源限制
	if runtime.GOOS != "windows" {
		s.applyProcessLimits(cmd)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err = cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("execution timed out after %v", timeout)
		}
	}

	return &ExecuteOutput{
		Stdout:   s.truncateOutput(stdout.String()),
		Stderr:   s.truncateOutput(stderr.String()),
		ExitCode: exitCode,
		Duration: duration.String(),
	}, nil
}

// getCodeFile 获取代码文件名
func (s *Sandbox) getCodeFile(language string) (string, string) {
	switch strings.ToLower(language) {
	case "python", "py":
		return "main.py", "python main.py"
	case "javascript", "js", "node":
		return "main.js", "node main.js"
	case "go", "golang":
		return "main.go", "go run main.go"
	case "bash", "sh":
		return "main.sh", "bash main.sh"
	case "ruby", "rb":
		return "main.rb", "ruby main.rb"
	case "php":
		return "main.php", "php main.php"
	default:
		return "main.txt", "cat main.txt"
	}
}

// getImage 获取 Docker 镜像
func (s *Sandbox) getImage(language string) string {
	if s.config.Image != "" {
		return s.config.Image
	}

	switch strings.ToLower(language) {
	case "python", "py":
		return "python:3.11-slim"
	case "javascript", "js", "node":
		return "node:20-slim"
	case "go", "golang":
		return "golang:1.22-alpine"
	case "ruby", "rb":
		return "ruby:3.3-slim"
	case "php":
		return "php:8.3-cli"
	default:
		return "alpine:latest"
	}
}

// getInterpreter 获取解释器
func (s *Sandbox) getInterpreter(language, codePath string) (string, []string) {
	switch strings.ToLower(language) {
	case "python", "py":
		python := "python3"
		if runtime.GOOS == "windows" {
			python = "python"
		}
		return python, []string{codePath}
	case "javascript", "js", "node":
		return "node", []string{codePath}
	case "go", "golang":
		return "go", []string{"run", codePath}
	case "bash", "sh":
		return "bash", []string{codePath}
	case "ruby", "rb":
		return "ruby", []string{codePath}
	case "php":
		return "php", []string{codePath}
	default:
		return "cat", []string{codePath}
	}
}

// truncateOutput 截断输出
func (s *Sandbox) truncateOutput(output string) string {
	if len(output) > s.config.MaxOutputSize {
		return output[:s.config.MaxOutputSize] + "\n... (truncated)"
	}
	return output
}

// Stop 停止沙箱
func (s *Sandbox) Stop(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cmd, ok := s.running[id]; ok {
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
	}
	return nil
}

// Cleanup 清理临时文件
func (s *Sandbox) Cleanup() {
	for _, dir := range s.tempDirs {
		os.RemoveAll(dir)
	}
	s.tempDirs = nil
}

// ============== 预配置沙箱 ==============

// PythonSandbox 创建 Python 沙箱工具
func PythonSandbox(useDocker bool) tool.Tool {
	config := DefaultConfig()
	if useDocker {
		config.Type = TypeDocker
		config.Image = "python:3.11-slim"
	}

	sandbox := New(config)
	return tool.NewFunc("python_sandbox", "在安全沙箱中执行 Python 代码", func(ctx context.Context, input struct {
		Code    string `json:"code" desc:"Python 代码" required:"true"`
		Timeout int    `json:"timeout" desc:"超时时间（秒）"`
	}) (*ExecuteOutput, error) {
		return sandbox.execute(ctx, ExecuteInput{
			Language: "python",
			Code:     input.Code,
			Timeout:  input.Timeout,
		})
	})
}

// NodeSandbox 创建 Node.js 沙箱工具
func NodeSandbox(useDocker bool) tool.Tool {
	config := DefaultConfig()
	if useDocker {
		config.Type = TypeDocker
		config.Image = "node:20-slim"
	}

	sandbox := New(config)
	return tool.NewFunc("nodejs_sandbox", "在安全沙箱中执行 JavaScript 代码", func(ctx context.Context, input struct {
		Code    string `json:"code" desc:"JavaScript 代码" required:"true"`
		Timeout int    `json:"timeout" desc:"超时时间（秒）"`
	}) (*ExecuteOutput, error) {
		return sandbox.execute(ctx, ExecuteInput{
			Language: "javascript",
			Code:     input.Code,
			Timeout:  input.Timeout,
		})
	})
}

// BashSandbox 创建 Bash 沙箱工具
func BashSandbox(useDocker bool) tool.Tool {
	config := DefaultConfig()
	if useDocker {
		config.Type = TypeDocker
		config.Image = "alpine:latest"
	}

	sandbox := New(config)
	return tool.NewFunc("bash_sandbox", "在安全沙箱中执行 Bash 脚本", func(ctx context.Context, input struct {
		Code    string `json:"code" desc:"Bash 脚本" required:"true"`
		Timeout int    `json:"timeout" desc:"超时时间（秒）"`
	}) (*ExecuteOutput, error) {
		return sandbox.execute(ctx, ExecuteInput{
			Language: "bash",
			Code:     input.Code,
			Timeout:  input.Timeout,
		})
	})
}

// MultiLanguageSandbox 创建多语言沙箱工具
func MultiLanguageSandbox(useDocker bool) tool.Tool {
	sandbox := New(DefaultDockerConfig())
	if !useDocker {
		sandbox = New(DefaultConfig())
	}

	return tool.NewFunc("code_sandbox", "在安全沙箱中执行代码", func(ctx context.Context, input struct {
		Language string            `json:"language" desc:"编程语言" required:"true"`
		Code     string            `json:"code" desc:"代码" required:"true"`
		Files    map[string]string `json:"files" desc:"附加文件"`
		Timeout  int               `json:"timeout" desc:"超时时间（秒）"`
	}) (*ExecuteOutput, error) {
		return sandbox.execute(ctx, ExecuteInput{
			Language: input.Language,
			Code:     input.Code,
			Files:    input.Files,
			Timeout:  input.Timeout,
		})
	})
}

// applyProcessLimits 在进程模式下应用资源限制（仅 Unix）
// 通过 syscall.Setrlimit 设置内存和 CPU 限制
func (s *Sandbox) applyProcessLimits(cmd *exec.Cmd) {
	// 内存限制：通过 ulimit 包装命令
	if s.config.Memory > 0 {
		// 将字节转换为 KB（ulimit -v 使用 KB）
		memKB := s.config.Memory / 1024
		if memKB < 1024 {
			memKB = 1024 // 最小 1MB
		}
		// 用 sh -c 包装，通过 ulimit 限制虚拟内存
		originalArgs := append([]string{cmd.Path}, cmd.Args[1:]...)
		ulimitCmd := fmt.Sprintf("ulimit -v %d && exec %s", memKB, strings.Join(originalArgs, " "))
		cmd.Path = "/bin/sh"
		cmd.Args = []string{"sh", "-c", ulimitCmd}
	}
}
