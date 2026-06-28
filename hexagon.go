// Package hexagon 提供 Hexagon AI Agent 框架的顶层 API
//
// Hexagon 是一个新一代 Go AI Agent 框架，设计目标是：
//   - 极简入门：3 行代码即可开始
//   - 类型安全：编译时检查，零运行时类型错误
//   - 高性能：原生并发，100k+ 并发 Agent
//   - 可观测：100% 覆盖率
//   - 生产就绪：优雅降级，运维友好
//
// # 快速开始
//
// 最简单的使用方式（3 行代码）：
//
//	response, _ := hexagon.Chat(ctx, "What is Go?")
//	fmt.Println(response)
//
// 带工具的 Agent：
//
//	agent := hexagon.QuickStart(
//	    hexagon.WithTools(calculatorTool),
//	    hexagon.WithSystemPrompt("You are a math assistant."),
//	)
//	output, _ := agent.Run(ctx, hexagon.Input{Query: "What is 123 * 456?"})
package hexagon

import (
	"context"
	"errors"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/llm/openai"
	"github.com/hexagon-codes/ai-core/memory"
	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/hexagon/agent"
)

// devFallbackVersion 是开发构建（go.work / 无可用 build info 版本）下的回退版本号，
// 同时作为发布前当前框架版本基线。单一来源，测试与解析逻辑共用。
const devFallbackVersion = "0.5.7"

// hexagonModulePath 是 hexagon 框架自身的 module path，用于在 build info 依赖列表里定位。
const hexagonModulePath = "github.com/hexagon-codes/hexagon"

// Version is the current version of the Hexagon framework.
// It is automatically resolved from Go module build info when available,
// falling back to the hardcoded value for development builds.
var Version = resolveVersion()

func resolveVersion() string {
	info, ok := debug.ReadBuildInfo()
	return resolveVersionFromBuildInfo(info, ok)
}

// resolveVersionFromBuildInfo 从 build info 解析 hexagon 框架版本（纯函数，便于测试）。
func resolveVersionFromBuildInfo(info *debug.BuildInfo, ok bool) string {
	if ok {
		// hexagon 作为依赖被引入（hexclaw sidecar 永远走这条）。
		for _, dep := range info.Deps {
			if dep.Path == hexagonModulePath {
				// 过滤 "(devel)" 与空版本（go.work / 开发构建上报 "(devel)"），否则透传给前端再被过滤→版本号消失。
				if v := strings.TrimPrefix(dep.Version, "v"); v != "" && v != "(devel)" {
					return v
				}
				// 命中 hexagon 依赖但版本无效 → **直接回退 fallback**，绝不落到 info.Main.Version：
				// 主模块是上层 hexclaw，其 VCS 戳记（如 "v0.4.6+dirty"）能通过守卫，会把 Hexagon engine
				// 谎报成 hexclaw 版本（BUG-20260626 R2：装机构建实测显示 0.4.6 而非 hexagon 0.5.4）。
				return devFallbackVersion
			}
		}
		// 仅当 hexagon 自身是主模块（hexagon 的二进制/测试）才用 Main.Version。
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	return devFallbackVersion // fallback for development builds
}

// 核心类型重新导出
type (
	// Input 是 Agent 输入
	Input = agent.Input

	// Output 是 Agent 输出
	Output = agent.Output

	// Tool 是工具接口
	Tool = tool.Tool

	// Memory 是记忆接口
	Memory = memory.Memory

	// Message 是聊天消息
	Message = llm.Message

	// Agent 是 Agent 接口
	Agent = agent.Agent

	// Provider 是 LLM 提供者接口
	Provider = llm.Provider
)

// ============== QuickStart API ==============

// defaultProvider 默认 LLM Provider（延迟初始化）
var (
	defaultProvider     llm.Provider
	defaultProviderOnce sync.Once
	defaultProviderMu   sync.RWMutex
)

// ErrNoProvider 表示没有配置 LLM Provider
var ErrNoProvider = errors.New("no LLM provider configured: set OPENAI_API_KEY environment variable or use WithProvider() option")

// getDefaultProvider 获取默认 Provider（并发安全）
func getDefaultProvider() llm.Provider {
	// 使用 sync.Once 确保只初始化一次
	defaultProviderOnce.Do(func() {
		// 检查用户是否已通过 SetDefaultProvider 设置
		defaultProviderMu.RLock()
		alreadySet := defaultProvider != nil
		defaultProviderMu.RUnlock()
		if alreadySet {
			return
		}
		if key := os.Getenv("OPENAI_API_KEY"); key != "" {
			defaultProviderMu.Lock()
			defaultProvider = openai.New(key)
			defaultProviderMu.Unlock()
		}
	})

	defaultProviderMu.RLock()
	defer defaultProviderMu.RUnlock()
	return defaultProvider
}

// SetDefaultProvider 设置默认 LLM Provider（并发安全）
func SetDefaultProvider(p llm.Provider) {
	defaultProviderMu.Lock()
	defer defaultProviderMu.Unlock()
	defaultProvider = p
}

// QuickStartOption 是 QuickStart 的配置选项
type QuickStartOption func(*quickStartConfig)

type quickStartConfig struct {
	provider     llm.Provider
	tools        []tool.Tool
	systemPrompt string
	memory       memory.Memory
}

// WithProvider 设置 LLM Provider
func WithProvider(p llm.Provider) QuickStartOption {
	return func(c *quickStartConfig) {
		c.provider = p
	}
}

// WithTools 设置工具
func WithTools(tools ...tool.Tool) QuickStartOption {
	return func(c *quickStartConfig) {
		c.tools = append(c.tools, tools...)
	}
}

// WithSystemPrompt 设置系统提示词
func WithSystemPrompt(prompt string) QuickStartOption {
	return func(c *quickStartConfig) {
		c.systemPrompt = prompt
	}
}

// WithMemory 设置记忆系统
func WithMemory(m memory.Memory) QuickStartOption {
	return func(c *quickStartConfig) {
		c.memory = m
	}
}

// QuickStart 快速创建一个 ReAct Agent
//
// 注意：需要配置 LLM Provider，可以通过以下方式之一：
//   - 设置 OPENAI_API_KEY 环境变量
//   - 使用 WithProvider() 选项
//   - 调用 SetDefaultProvider()
//
// 如果没有配置 Provider，QuickStart 不会 panic，
// 但后续执行时会返回未配置 Provider 的错误。
//
// 示例：
//
//	agent := hexagon.QuickStart(
//	    hexagon.WithTools(searchTool, calculatorTool),
//	    hexagon.WithSystemPrompt("You are a helpful assistant."),
//	)
//	output, err := agent.Run(ctx, hexagon.Input{Query: "What is 2+2?"})
func QuickStart(opts ...QuickStartOption) *agent.ReActAgent {
	cfg := &quickStartConfig{
		provider: getDefaultProvider(),
		memory:   memory.NewBuffer(100),
	}

	for _, opt := range opts {
		opt(cfg)
	}

	agentOpts := []agent.Option{
		agent.WithMemory(cfg.memory),
	}
	if cfg.provider != nil {
		agentOpts = append(agentOpts, agent.WithLLM(cfg.provider))
	}

	if len(cfg.tools) > 0 {
		agentOpts = append(agentOpts, agent.WithTools(cfg.tools...))
	}
	if cfg.systemPrompt != "" {
		agentOpts = append(agentOpts, agent.WithSystemPrompt(cfg.systemPrompt))
	}

	return agent.NewReAct(agentOpts...)
}

// ============== 便捷函数 ==============

// Chat 执行简单对话（最简 API）
//
// 示例：
//
//	response, err := hexagon.Chat(ctx, "What is Go?")
//	fmt.Println(response)
func Chat(ctx context.Context, query string, opts ...QuickStartOption) (string, error) {
	a := QuickStart(opts...)
	if a.LLM() == nil {
		return "", ErrNoProvider
	}
	output, err := a.Run(ctx, Input{Query: query})
	if err != nil {
		return "", err
	}
	return output.Content, nil
}

// ChatWithTools 带工具的对话
//
// 示例：
//
//	result, err := hexagon.ChatWithTools(ctx, "What is 123 * 456?", calculatorTool)
func ChatWithTools(ctx context.Context, query string, tools ...tool.Tool) (string, error) {
	return Chat(ctx, query, WithTools(tools...))
}

// Run 执行 Agent 并返回完整输出
//
// 示例：
//
//	output, err := hexagon.Run(ctx, hexagon.Input{Query: "Hello"})
func Run(ctx context.Context, input Input, opts ...QuickStartOption) (Output, error) {
	a := QuickStart(opts...)
	if a.LLM() == nil {
		return Output{}, ErrNoProvider
	}
	return a.Run(ctx, input)
}

// ============== 工具创建便捷函数 ==============

// NewTool 从函数创建工具
//
// 示例：
//
//	type CalcInput struct {
//	    A float64 `json:"a" desc:"第一个数字" required:"true"`
//	    B float64 `json:"b" desc:"第二个数字" required:"true"`
//	}
//
//	calculator := hexagon.NewTool("calculator", "执行加法计算",
//	    func(ctx context.Context, input CalcInput) (float64, error) {
//	        return input.A + input.B, nil
//	    },
//	)
func NewTool[I, O any](name, description string, fn func(context.Context, I) (O, error)) *tool.FuncTool[I, O] {
	return tool.NewFunc(name, description, fn)
}
