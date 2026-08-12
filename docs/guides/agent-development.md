<div align="right">语言: 中文 | <a href="agent-development.en.md">English</a></div>

# Agent 开发指南

本指南将帮助您快速开始开发 Hexagon AI Agent。

## 快速开始

### 创建第一个 Agent

```go
package main

import (
    "context"
    "fmt"

    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/ai-core/llm/openai"
)

func main() {
    // 创建 LLM Provider
    provider := openai.New("your-api-key")

    // 创建 Agent
    myAgent := agent.NewBaseAgent(
        agent.WithName("my-agent"),
        agent.WithSystemPrompt("你是一个有用的助手"),
        agent.WithLLM(provider),
    )

    // 运行 Agent
    ctx := context.Background()
    result, err := myAgent.Invoke(ctx, agent.Input{
        Query: "你好！",
    })

    if err != nil {
        panic(err)
    }

    fmt.Println(result.Content)
}
```

## Agent 类型

### BaseAgent

最基础的 Agent 实现，适合简单的对话场景。

**特点**:
- 轻量级
- 支持工具调用
- 支持记忆系统

**使用场景**:
- 简单问答
- 客服机器人
- 知识查询

### ReActAgent

实现 ReAct (Reasoning + Acting) 推理模式的 Agent。

**特点**:
- 推理-行动循环
- 自动工具选择
- 思维链可见

**使用场景**:
- 复杂任务分解
- 需要多步推理
- 工具密集型任务

```go
reactAgent := agent.NewReAct(
    agent.WithName("react-agent"),
    agent.WithSystemPrompt("你是一个能够推理和行动的 AI 助手"),
    agent.WithLLM(provider),
    agent.WithTools(
        searchTool,
        calculatorTool,
    ),
    agent.WithMaxIterations(5), // 最多5轮推理
)
```

## 添加工具

工具让 Agent 能够执行具体操作。

### 使用内置工具

```go
import (
    "github.com/hexagon-codes/hexagon/tool/file"
    "github.com/hexagon-codes/hexagon/tool/shell"
)

// 文件操作工具
fileTools := file.New(file.DefaultConfig()).All()

// Shell 执行工具
shellTool := shell.New(shell.DefaultConfig()).ExecuteTool()

myAgent := agent.NewBaseAgent(
    agent.WithTools(append(fileTools, shellTool)...),
)
```

### 创建自定义工具

```go
import "github.com/hexagon-codes/ai-core/tool"

// 使用函数式工具
weatherTool := tool.NewFunc(
    "get_weather",
    "获取指定城市的天气信息",
    func(ctx context.Context, input struct {
        City string `json:"city" description:"城市名称"`
    }) (struct {
        Temperature int    `json:"temperature"`
        Condition   string `json:"condition"`
    }, error) {
        // 实现天气查询逻辑
        return struct {
            Temperature int    `json:"temperature"`
            Condition   string `json:"condition"`
        }{
            Temperature: 25,
            Condition:   "晴天",
        }, nil
    },
)
```

## 记忆系统

### 配置记忆

```go
import "github.com/hexagon-codes/ai-core/memory"

// 创建记忆实例（capacity 为保留的最近消息条数）
mem := memory.NewBuffer(10)

myAgent := agent.NewBaseAgent(
    agent.WithMemory(mem),
)
```

### 记忆类型

- **BufferMemory** (`memory.NewBuffer`): 简单缓冲记忆，保留最近 N 条消息
- **SummaryMemory** (`memory.NewSummaryMemory`): 摘要记忆，定期总结历史对话
- **VectorMemory** (`memory.NewVectorMemory`): 向量记忆，基于语义相似度检索
- **MultiLayerMemory** (`memory.NewMultiLayerMemory`): 多层记忆，组合缓冲与长期存储

## 配置 Agent

### 使用 YAML 配置

```yaml
# agent.yaml
name: my-agent
type: react
role:
  name: 助手
  goal: 帮助用户解决问题
  backstory: 你是一个经验丰富的AI助手
llm:
  provider: openai
  model: gpt-4o
  api_key: ${OPENAI_API_KEY}
  temperature: 0.7
max_iterations: 5
verbose: false
memory:
  type: buffer
  max_size: 10
```

```go
import (
    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/hexagon/config"
)

func buildAgentFromConfig(path string) (agent.Agent, error) {
    cfg, err := config.LoadAgentConfig(path)
    if err != nil {
        return nil, err
    }

    return config.NewBuilder().BuildAgent(cfg)
}
```

`LoadAgentConfig` 只负责解析和展开环境变量；`AgentConfig` 本身没有 `Build` 方法。需要由 `config.Builder.BuildAgent` 显式构造 Agent。默认工具工厂不会凭 YAML 自动创建文件或 Shell 工具；需要这类工具时，请按“使用内置工具”一节显式构造并注入，或提供自定义 `ToolFactory`。

## 最佳实践

### 1. 系统提示词设计

**好的提示词**:
```
你是一个专业的客服助手。你的职责是：
1. 礼貌地回答客户问题
2. 使用工具查询订单信息
3. 如果无法回答，引导客户联系人工客服

注意事项：
- 保持专业和友善的语气
- 回答要简洁明了
- 确认关键信息后再操作
```

**不好的提示词**:
```
你是一个助手，回答问题。
```

### 2. 工具命名规范

- 使用小写下划线命名: `get_user_info`
- 描述要清晰准确
- 参数要有详细的 description

### 3. 错误处理

```go
result, err := myAgent.Invoke(ctx, input)
if err != nil {
    switch {
    case errors.Is(err, context.DeadlineExceeded):
        // 处理超时
        fmt.Println("执行超时")
    default:
        // 错误使用 fmt.Errorf("...: %w", err) 逐层包装，
        // 可用 errors.Is / errors.As 解包判断具体原因
        fmt.Printf("执行失败: %v\n", err)
    }

    return err
}
```

### 4. 性能优化

- 使用流式输出提升响应速度
- 合理设置记忆窗口大小
- 限制工具执行次数防止死循环
- 使用缓存减少重复计算

```go
import (
    "errors"
    "io"
)

// 流式输出：Stream 返回 *stream.StreamReader[agent.Output]
reader, err := myAgent.Stream(ctx, input)
if err != nil {
    panic(err)
}
defer reader.Close()

for {
    chunk, err := reader.Recv()
    if errors.Is(err, io.EOF) {
        break
    }
    if err != nil {
        panic(err)
    }
    fmt.Print(chunk.Content)
}
```

## 调试技巧

### 启用详细日志

```go
import "github.com/hexagon-codes/hexagon/observe/logger"

// 设置全局日志级别（取值为字符串 "debug" / "info" / "warn" / "error"）
logger.SetLevel("debug")

// 或在构造 Agent 时开启详细日志
myAgent := agent.NewReAct(
    agent.WithLLM(provider),
    agent.WithVerbose(true),
)
```

### 使用 Dev UI

```go
import (
    "context"
    "log"
    "time"

    "github.com/hexagon-codes/hexagon/hooks"
    "github.com/hexagon-codes/hexagon/observe/devui"
)

ui := devui.New(devui.WithAddr("127.0.0.1:8080"))
ctx = hooks.ContextWithManager(ctx, ui.HookManager())

go func() {
    if err := ui.Start(); err != nil {
        log.Printf("Dev UI stopped with error: %v", err)
    }
}()

defer func() {
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    if err := ui.Stop(shutdownCtx); err != nil {
        log.Printf("Failed to stop Dev UI: %v", err)
    }
}()
```

Dev UI 默认和示例都只监听本机回环地址。若改为非回环地址，必须通过 `devui.WithAuthToken` 配置至少 32 个无空白字节的令牌，否则服务会拒绝启动。

## 下一步

- 学习 [多 Agent 协作](./multi-agent.md)
- 了解 [RAG 系统集成](./rag-integration.md)
- 掌握 [图编排](./graph-orchestration.md)
