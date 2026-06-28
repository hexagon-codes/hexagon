// Package python 提供 AI Agent 的 Python 代码执行工具
//
// 本包实现了 Python REPL 工具，支持：
//   - Python 代码执行
//   - 虚拟环境支持
//   - 模块导入控制
//
// 安全特性：
//   - 模块白名单/黑名单
//   - 执行超时
//   - 输出大小限制
//
// 使用示例：
//
//	repl := python.New(python.DefaultConfig())
//	tool := repl.Tool()
//	result, _ := tool.Execute(ctx, map[string]any{
//	    "code": "print(1 + 1)",
//	})
package python

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/toolkit/lang/stringx"
)

// validPythonIdentifier 验证 Python 变量名：只允许字母、数字、下划线，不能以数字开头，
// 且不能是 Python 双下划线特殊属性
var validPythonIdentifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Config Python REPL 工具配置
type Config struct {
	// PythonPath Python 解释器路径
	PythonPath string

	// Timeout 执行超时时间
	Timeout time.Duration

	// WorkDir 工作目录
	WorkDir string

	// MaxOutputSize 最大输出大小（字节）
	MaxOutputSize int

	// AllowedModules 允许导入的模块（空表示允许所有）
	AllowedModules []string

	// DeniedModules 禁止导入的模块
	DeniedModules []string

	// VirtualEnv 虚拟环境路径
	VirtualEnv string
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	pythonPath := "python3"
	if runtime.GOOS == "windows" {
		pythonPath = "python"
	}

	return &Config{
		PythonPath:    pythonPath,
		Timeout:       60 * time.Second,
		MaxOutputSize: 1024 * 1024, // 1MB
		DeniedModules: []string{
			"subprocess",
			"os.system",
			"shutil.rmtree",
			"ctypes",
		},
	}
}

// Tool Python REPL 工具
type Tool struct {
	config *Config
}

// New 创建 Python REPL 工具
func New(config *Config) *Tool {
	if config == nil {
		config = DefaultConfig()
	}
	return &Tool{config: config}
}

// REPLTool 返回 Python REPL 工具
func (t *Tool) REPLTool() tool.Tool {
	return tool.NewFunc("python_repl", "执行 Python 代码", t.execute)
}

// ExecuteInput 执行代码输入
type ExecuteInput struct {
	Code    string            `json:"code" desc:"要执行的 Python 代码" required:"true"`
	Timeout int               `json:"timeout" desc:"超时时间（秒）"`
	Vars    map[string]string `json:"vars" desc:"预设变量"`
}

// ExecuteOutput 执行代码输出
type ExecuteOutput struct {
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
	ExitCode int    `json:"exit_code"`
	Duration string `json:"duration"`
}

// execute 执行 Python 代码
func (t *Tool) execute(ctx context.Context, input ExecuteInput) (*ExecuteOutput, error) {
	// 验证代码
	if err := t.validateCode(input.Code); err != nil {
		return nil, err
	}

	// 设置超时
	timeout := t.config.Timeout
	if input.Timeout > 0 {
		timeout = time.Duration(input.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 创建临时文件
	tmpFile, err := os.CreateTemp("", "hexagon_python_*.py")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	// 构建完整代码
	fullCode := t.buildCode(input.Code, input.Vars)
	if _, err := tmpFile.WriteString(fullCode); err != nil {
		return nil, fmt.Errorf("failed to write code: %w", err)
	}
	tmpFile.Close()

	// 确定 Python 路径
	pythonPath := t.config.PythonPath
	if t.config.VirtualEnv != "" {
		if runtime.GOOS == "windows" {
			pythonPath = filepath.Join(t.config.VirtualEnv, "Scripts", "python.exe")
		} else {
			pythonPath = filepath.Join(t.config.VirtualEnv, "bin", "python")
		}
	}

	// 执行代码
	cmd := exec.CommandContext(ctx, pythonPath, tmpFile.Name())
	if t.config.WorkDir != "" {
		cmd.Dir = t.config.WorkDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err = cmd.Run()
	duration := time.Since(start)

	// 获取退出码
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("code execution timed out after %v", timeout)
		}
	}

	// 截断输出
	output := stdout.String()
	errOutput := stderr.String()
	if len(output) > t.config.MaxOutputSize {
		output = stringx.TruncateBytes(output, t.config.MaxOutputSize, "\n... (truncated)")
	}
	if len(errOutput) > t.config.MaxOutputSize {
		errOutput = stringx.TruncateBytes(errOutput, t.config.MaxOutputSize, "\n... (truncated)")
	}

	return &ExecuteOutput{
		Output:   output,
		Error:    errOutput,
		ExitCode: exitCode,
		Duration: duration.String(),
	}, nil
}

// validateCode 验证代码安全性
//
// 安全检查包括：
//   - 禁止的模块导入
//   - 危险的内置函数
//   - 代码注入模式
//   - 反射和元编程功能
func (t *Tool) validateCode(code string) error {
	// 将代码转换为小写用于某些检查（但保留原始代码用于精确匹配）
	lowerCode := strings.ToLower(code)

	// 检查禁止的模块
	for _, module := range t.config.DeniedModules {
		patterns := []string{
			fmt.Sprintf("import %s", module),
			fmt.Sprintf("from %s import", module),
			fmt.Sprintf("from %s.", module), // from subprocess.xxx
			fmt.Sprintf("__import__('%s')", module),
			fmt.Sprintf(`__import__("%s")`, module),
			fmt.Sprintf("importlib.import_module('%s')", module),
			fmt.Sprintf(`importlib.import_module("%s")`, module),
		}
		for _, pattern := range patterns {
			if strings.Contains(lowerCode, strings.ToLower(pattern)) {
				return fmt.Errorf("importing '%s' is not allowed", module)
			}
		}
	}

	// 检查允许的模块（如果设置了白名单）
	if len(t.config.AllowedModules) > 0 {
		// 简单检查：查找所有 import 语句
		lines := strings.Split(code, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "from ") {
				allowed := false
				for _, module := range t.config.AllowedModules {
					if strings.Contains(line, module) {
						allowed = true
						break
					}
				}
				if !allowed {
					return fmt.Errorf("import not allowed: %s", line)
				}
			}
		}
	}

	// 检查危险代码模式
	// 这些模式可能被用于绕过安全限制
	dangerousPatterns := []struct {
		pattern string
		reason  string
	}{
		// 动态代码执行
		{"eval(", "dynamic code execution"},
		{"exec(", "dynamic code execution"},
		{"compile(", "code compilation"},

		// 内置函数访问
		{"__builtins__", "access to builtins"},
		{"__class__", "class introspection"},
		{"__subclasses__", "subclass enumeration"},
		{"__bases__", "base class access"},
		{"__mro__", "method resolution order access"},
		{"__globals__", "global namespace access"},
		{"__code__", "code object access"},
		{"__dict__", "dictionary access (potential sandbox escape)"},

		// 反射和元编程
		{"globals(", "global namespace access"},
		{"locals(", "local namespace access"},
		{"vars(", "variable access"},
		{"dir(", "object inspection"},
		{"getattr(", "attribute access"},
		{"setattr(", "attribute modification"},
		{"delattr(", "attribute deletion"},
		{"hasattr(", "attribute checking"},

		// 文件操作（如果未明确允许）
		{"open(", "file access"},
		{"file(", "file access"},

		// 代码对象操作
		{"types.CodeType", "code object creation"},
		{"types.FunctionType", "function creation"},

		// 危险的字符串操作（可能用于绕过）
		{"chr(", "character conversion (bypass)"},
		{"ord(", "ordinal conversion (bypass)"},

		// 系统退出
		{"exit(", "system exit"},
		{"quit(", "system exit"},
		{"sys.exit", "system exit"},
	}

	for _, dp := range dangerousPatterns {
		if strings.Contains(code, dp.pattern) {
			return fmt.Errorf("potentially dangerous code pattern detected: %s (%s)", dp.pattern, dp.reason)
		}
	}

	// 检查编码绕过尝试
	encodingPatterns := []string{
		"base64.b64decode",
		"codecs.decode",
		"bytes.fromhex",
		"bytearray.fromhex",
		"\\x",  // 十六进制转义
		"\\u",  // Unicode 转义
		"\\U",  // Unicode 转义
		"\\N{", // Unicode 名称
	}
	for _, pattern := range encodingPatterns {
		if strings.Contains(code, pattern) {
			return fmt.Errorf("encoding pattern detected (potential bypass attempt): %s", pattern)
		}
	}

	// 检查字符串拼接构建危险函数名的尝试
	// 例如: 'e'+'val', "ev"+"al", chr(101)+chr(118)+chr(97)+chr(108)
	concatBypassPatterns := []string{
		"chr(",           // 通过 chr() 拼接
		"getattr(",       // 通过 getattr 动态获取属性
		"__import__",     // 动态导入
		"importlib",      // 动态导入库
		"globals()",      // 访问全局变量
		"locals()",       // 访问局部变量
		"vars(",          // 访问变量字典
		"type(",          // 动态创建类型
		"__class__",      // 访问类
		"__bases__",      // 访问基类
		"__subclasses__", // 访问子类（沙箱逃逸经典手法）
		"__builtins__",   // 访问内建函数
	}
	for _, pattern := range concatBypassPatterns {
		if strings.Contains(code, pattern) {
			return fmt.Errorf("potentially dangerous code pattern detected: %s (dynamic code construction)", pattern)
		}
	}

	return nil
}

// buildCode 构建完整代码
func (t *Tool) buildCode(code string, vars map[string]string) string {
	var builder strings.Builder

	// 添加预设变量
	if len(vars) > 0 {
		for k, v := range vars {
			// 验证变量名安全性：仅允许合法 Python 标识符，且禁止双下划线属性
			if !validPythonIdentifier.MatchString(k) || strings.HasPrefix(k, "__") {
				continue
			}
			fmt.Fprintf(&builder, "%s = %q\n", k, v)
		}
		builder.WriteString("\n")
	}

	// 添加用户代码
	builder.WriteString(code)

	return builder.String()
}

// ============== 便捷工具 ==============

// DataAnalysisTool 返回数据分析工具
func DataAnalysisTool() tool.Tool {
	config := DefaultConfig()
	config.AllowedModules = []string{
		"pandas",
		"numpy",
		"matplotlib",
		"seaborn",
		"json",
		"csv",
		"math",
		"statistics",
		"datetime",
	}

	t := &Tool{config: config}
	return tool.NewFunc("python_data_analysis", "使用 Python 进行数据分析", func(ctx context.Context, input struct {
		Code string `json:"code" desc:"数据分析代码" required:"true"`
	}) (*ExecuteOutput, error) {
		// 添加常用导入
		fullCode := `
import json
import math
import statistics
from datetime import datetime
try:
    import pandas as pd
    import numpy as np
except ImportError:
    pass
` + input.Code
		return t.execute(ctx, ExecuteInput{Code: fullCode})
	})
}

// CalculatorTool 返回计算器工具
func CalculatorTool() tool.Tool {
	config := DefaultConfig()
	config.Timeout = 10 * time.Second
	config.AllowedModules = []string{"math", "decimal", "fractions"}

	t := &Tool{config: config}
	return tool.NewFunc("python_calculator", "使用 Python 进行数学计算", func(ctx context.Context, input struct {
		Expression string `json:"expression" desc:"数学表达式" required:"true"`
	}) (*ExecuteOutput, error) {
		// 将表达式直接作为代码执行，表达式本身会经过 validateCode 检查
		code := fmt.Sprintf(`
import math
from decimal import Decimal
from fractions import Fraction

result = %s
print(result)
`, input.Expression)
		return t.execute(ctx, ExecuteInput{Code: code})
	})
}

// JSONProcessorTool 返回 JSON 处理工具
func JSONProcessorTool() tool.Tool {
	config := DefaultConfig()
	config.Timeout = 30 * time.Second
	config.AllowedModules = []string{"json", "collections"}

	t := &Tool{config: config}
	return tool.NewFunc("python_json", "使用 Python 处理 JSON 数据", func(ctx context.Context, input struct {
		Code string `json:"code" desc:"JSON 处理代码" required:"true"`
		Data string `json:"data" desc:"输入的 JSON 数据"`
	}) (*ExecuteOutput, error) {
		// 通过预设变量安全传递数据，避免三引号逃逸
		fullCode := `
import json
from collections import defaultdict, Counter

# 输入数据（通过预设变量安全传入）
if _input_data.strip():
    data = json.loads(_input_data)
else:
    data = None

# 用户代码
` + input.Code
		return t.execute(ctx, ExecuteInput{
			Code: fullCode,
			Vars: map[string]string{"_input_data": input.Data},
		})
	})
}

// TextProcessorTool 返回文本处理工具
func TextProcessorTool() tool.Tool {
	config := DefaultConfig()
	config.Timeout = 30 * time.Second
	config.AllowedModules = []string{"re", "string", "textwrap", "unicodedata"}

	t := &Tool{config: config}
	return tool.NewFunc("python_text", "使用 Python 处理文本", func(ctx context.Context, input struct {
		Code string `json:"code" desc:"文本处理代码" required:"true"`
		Text string `json:"text" desc:"输入文本"`
	}) (*ExecuteOutput, error) {
		// 通过预设变量安全传递文本，避免三引号逃逸
		fullCode := `
import re
import string
import textwrap
import unicodedata

# 输入文本（通过预设变量安全传入）
text = _input_text

# 用户代码
` + input.Code
		return t.execute(ctx, ExecuteInput{
			Code: fullCode,
			Vars: map[string]string{"_input_text": input.Text},
		})
	})
}
