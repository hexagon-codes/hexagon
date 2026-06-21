<div align="right">语言: 中文 | <a href="QUICKSTART.en.md">English</a></div>

# Hexagon 快速入门指南

本指南帮助你在 30 分钟内上手 Hexagon AI Agent 框架。

## 项目简介

**Hexagon** 取名自网络热词「六边形战士」，寓意均衡强大、无懈可击。框架聚焦 **易用性、性能、扩展性、任务编排、可观测性、安全性** 六大核心维度，为 Go 开发者打造企业级落地首选的 AI Agent 开发基座。

### 生态系统

Hexagon 是一个完整的 AI Agent 开发生态：

| 仓库 | 说明 |
|-----|------|
| **hexagon** | AI Agent 框架核心 (编排、RAG、Graph、Hooks) |
| **ai-core** | AI 基础能力库 (LLM/Tool/Memory/Schema) |
| **toolkit** | Go 通用工具库 (lang/crypto/net/cache/util) |
| **hexagon-ui** | Dev UI 前端 (Vue 3 + TypeScript) |

## 目录

- [环境准备](#环境准备)
- [安装](#安装)
- [3 行代码入门](#3-行代码入门)
- [带工具的 Agent](#带工具的-agent)
- [RAG 检索增强](#rag-检索增强)
- [图编排](#图编排)
- [多 Agent 协作](#多-agent-协作)
- [Dev UI](#dev-ui)
- [下一步](#下一步)

---

## 环境准备

### 系统要求

- Go 1.25 或更高版本
- 网络连接（访问 LLM API）

### 环境变量

Hexagon 支持多种 LLM Provider，需要配置相应的 API Key：

```bash
# OpenAI (默认)
export OPENAI_API_KEY=your-api-key

# DeepSeek
export DEEPSEEK_API_KEY=your-api-key
```

---

## 安装

```bash
go get github.com/hexagon-codes/hexagon
```

验证安装：

```bash
go list -m github.com/hexagon-codes/hexagon
```

---

## 3 行代码入门

这是最简单的使用方式：

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

func main() {
    response, _ := hexagon.Chat(context.Background(), "What is Go?")
    fmt.Println(response)
}
```

运行：

```bash
export OPENAI_API_KEY=your-api-key
go run main.go
```

---

## 带工具的 Agent

Agent 可以使用工具来完成任务：

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

// 定义计算器工具的输入
type CalculatorInput struct {
    A  float64 `json:"a" desc:"第一个数字" required:"true"`
    B  float64 `json:"b" desc:"第二个数字" required:"true"`
    Op string  `json:"op" desc:"运算符" required:"true" enum:"add,sub,mul,div"`
}

func main() {
    ctx := context.Background()

    // 创建计算器工具
    calculator := hexagon.NewTool("calculator", "执行数学计算",
        func(ctx context.Context, input CalculatorInput) (float64, error) {
            switch input.Op {
            case "add":
                return input.A + input.B, nil
            case "sub":
                return input.A - input.B, nil
            case "mul":
                return input.A * input.B, nil
            case "div":
                if input.B == 0 {
                    return 0, fmt.Errorf("division by zero")
                }
                return input.A / input.B, nil
            default:
                return 0, fmt.Errorf("unknown operator: %s", input.Op)
            }
        },
    )

    // 创建带工具的 Agent
    agent := hexagon.QuickStart(
        hexagon.WithTools(calculator),
        hexagon.WithSystemPrompt("你是一个数学助手"),
    )

    // 执行查询
    output, _ := agent.Run(ctx, hexagon.Input{
        Query: "请计算 123 乘以 456",
    })

    fmt.Println(output.Content)
}
```

### 工具定义说明

- `name`: 工具名称，LLM 用来识别和调用
- `desc`: 工具描述，帮助 LLM 理解何时使用
- 输入结构体标签：
  - `json`: 字段名
  - `desc`: 字段描述
  - `required`: 是否必填
  - `enum`: 可选值列表

---

## RAG 检索增强

RAG (Retrieval-Augmented Generation) 让 Agent 能够基于外部知识库回答问题：

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

func main() {
    ctx := context.Background()

    // 创建内存向量存储
    store := hexagon.NewMemoryVectorStore()

    // 创建 Embedder
    embedder := hexagon.NewOpenAIEmbedder()

    // 创建 RAG 引擎
    engine := hexagon.NewRAGEngine(
        hexagon.WithRAGStore(store),
        hexagon.WithRAGEmbedder(embedder),
    )

    // 索引文档
    docs := []hexagon.Document{
        {ID: "1", Content: "Go 是一种静态类型、编译型语言，由 Google 开发。"},
        {ID: "2", Content: "Go 支持并发编程，通过 goroutine 和 channel 实现。"},
        {ID: "3", Content: "Go 的标准库非常丰富，包括 HTTP、JSON、加密等。"},
    }
    engine.Index(ctx, docs)

    // 检索相关文档
    results, _ := engine.Retrieve(ctx, "Go 的并发特性",
        hexagon.WithTopK(2),
        hexagon.WithMinScore(0.5),
    )

    for _, doc := range results {
        fmt.Printf("[%.2f] %s\n", doc.Score, doc.Content)
    }
}
```

### 使用 Qdrant 向量数据库

对于生产环境，推荐使用 Qdrant：

```go
// 创建 Qdrant 存储
store, _ := hexagon.NewQdrantStore(hexagon.QdrantConfig{
    Host:             "localhost",
    Port:             6333,
    Collection:       "my-docs",
    Dimension:        1536,
    CreateCollection: true,
})
```

---

## 图编排

图编排允许构建复杂的多步骤工作流：

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
    "github.com/hexagon-codes/hexagon/orchestration/graph"
)

// 定义状态
type MyState struct {
    Input   string
    Step1   string
    Step2   string
    Final   string
}

func (s MyState) Clone() graph.State {
    return s
}

func main() {
    ctx := context.Background()

    // 构建图
    g, _ := hexagon.NewGraph[MyState]("example-graph").
        AddNode("analyze", func(ctx context.Context, s MyState) (MyState, error) {
            s.Step1 = "Analyzed: " + s.Input
            return s, nil
        }).
        AddNode("process", func(ctx context.Context, s MyState) (MyState, error) {
            s.Step2 = "Processed: " + s.Step1
            return s, nil
        }).
        AddNode("summarize", func(ctx context.Context, s MyState) (MyState, error) {
            s.Final = "Summary: " + s.Step2
            return s, nil
        }).
        AddEdge(hexagon.START, "analyze").
        AddEdge("analyze", "process").
        AddEdge("process", "summarize").
        AddEdge("summarize", hexagon.END).
        Build()

    // 执行
    result, _ := g.Run(ctx, MyState{Input: "Hello World"})
    fmt.Println(result.Final)
}
```

### 条件分支

```go
g, _ := hexagon.NewGraph[MyState]("conditional-graph").
    AddNode("check", checkHandler).
    AddNode("path_a", pathAHandler).
    AddNode("path_b", pathBHandler).
    AddEdge(hexagon.START, "check").
    AddConditionalEdge("check", func(s MyState) string {
        if s.ShouldUsePathA {
            return "a"
        }
        return "b"
    }, map[string]string{
        "a": "path_a",
        "b": "path_b",
    }).
    AddEdge("path_a", hexagon.END).
    AddEdge("path_b", hexagon.END).
    Build()
```

---

## 多 Agent 协作

### 团队模式

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

func main() {
    ctx := context.Background()

    // 创建 Agent
    researcher := hexagon.QuickStart(
        hexagon.WithSystemPrompt("你是一个研究员，负责收集信息"),
    )
    writer := hexagon.QuickStart(
        hexagon.WithSystemPrompt("你是一个作家，负责撰写内容"),
    )

    // 创建团队（顺序执行）
    team := hexagon.NewTeam("content-team",
        hexagon.WithAgents(researcher, writer),
        hexagon.WithMode(hexagon.TeamModeSequential),
    )

    // 执行
    output, _ := team.Run(ctx, hexagon.Input{
        Query: "写一篇关于 Go 语言的介绍",
    })

    fmt.Println(output.Content)
}
```

### Agent 交接 (Swarm 模式)

```go
// 创建 Agent
salesAgent := hexagon.QuickStart(
    hexagon.WithSystemPrompt("你是销售客服"),
    hexagon.WithTools(
        hexagon.TransferTo(supportAgent), // 交接工具
    ),
)

supportAgent := hexagon.QuickStart(
    hexagon.WithSystemPrompt("你是技术支持"),
    hexagon.WithTools(
        hexagon.TransferTo(salesAgent),
    ),
)

// 创建 Swarm 运行器
runner := agent.NewSwarmRunner(salesAgent)
runner.MaxHandoffs = 5

// 运行
output, _ := runner.Run(ctx, hexagon.Input{
    Query: "我想了解产品价格，还有一些技术问题",
})
```

---

## 安全防护

### Prompt 注入检测

```go
guard := hexagon.NewPromptInjectionGuard()
result, _ := guard.Check(ctx, userInput)

if !result.Passed {
    fmt.Printf("检测到潜在的注入攻击: %s\n", result.Reason)
}
```

### 成本控制

```go
controller := hexagon.NewCostController(
    hexagon.WithBudget(10.0),           // $10 预算
    hexagon.WithMaxTokensTotal(100000), // 总 token 限制
)
```

---

## 可观测性

### 追踪

```go
tracer := hexagon.NewTracer()
ctx := hexagon.ContextWithTracer(ctx, tracer)

span := hexagon.StartSpan(ctx, "my_operation")
defer span.End()

span.SetAttribute("user_id", "123")
```

### 指标

```go
metrics := hexagon.NewMetrics()
metrics.Counter("agent_calls", "agent", "react").Inc()
metrics.Histogram("latency_ms").Observe(123.5)
```

---

## Dev UI

内置开发调试界面，实时查看 Agent 执行过程。

```go
import "github.com/hexagon-codes/hexagon/observe/devui"

// 创建 DevUI
ui := devui.New(
    devui.WithAddr(":8080"),
    devui.WithMaxEvents(1000),
)

// 启动服务
go ui.Start()

// 访问 http://localhost:8080
```

**运行示例：**

```bash
# 启动后端
go run examples/devui/main.go

# 启动前端 (hexagon-ui)
cd ../hexagon-ui
npm install
npm run dev
# 访问 http://localhost:5173
```

**功能特性：**
- 实时事件流 (SSE 推送)
- 指标仪表板
- 事件详情查看
- LLM 流式输出展示

---

## 部署

Hexagon 提供三种部署方式：

### Docker 快速启动

```bash
cd deploy
cp .env.example .env
# 编辑 .env，填入 LLM API Key
make up
# 主应用: http://localhost:8000  Dev UI: http://localhost:8080
```

### Kubernetes / Helm

```bash
cd deploy
make helm-install
```

详见 [部署指南](../deploy/README.md)。

---

## 下一步

- 阅读 [API 参考文档](API.md) 了解完整 API
- 阅读 [架构设计文档](DESIGN.md) 深入理解框架设计
- 阅读 [框架对比](comparison.md) 了解与其他框架的差异
- 阅读 [部署指南](../deploy/README.md) 了解部署配置
- 查看 [示例代码](../examples/) 获取更多用例
- 访问 [GitHub](https://github.com/hexagon-codes/hexagon) 参与贡献

## 常见问题

### Q: 如何切换 LLM Provider？

```go
import "github.com/hexagon-codes/ai-core/llm/deepseek"

provider := deepseek.New(os.Getenv("DEEPSEEK_API_KEY"))
agent := hexagon.QuickStart(
    hexagon.WithProvider(provider),
)
```

### Q: 如何自定义 Memory？

```go
// 使用更大的 buffer
memory := hexagon.NewBufferMemory(1000)
agent := hexagon.QuickStart(
    hexagon.WithMemory(memory),
)
```

### Q: 如何调试 Agent？

`WithVerbose` 是 `agent` 包的 Agent 选项，需要直接构造 Agent 时使用：

```go
import "github.com/hexagon-codes/hexagon/agent"

myAgent := agent.NewReAct(
    agent.WithLLM(provider),
    agent.WithVerbose(true), // 开启详细日志
)
```
