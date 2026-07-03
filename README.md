<div align="right">语言: 中文 | <a href="README.en.md">English</a></div>

<div align="center">

<img src=".github/assets/logo.jpg" alt="Hexagon Logo" width="160">

**Go 生态全能型 AI Agent 框架**

[![Go Reference](https://img.shields.io/badge/Go-1.25.7+-00ADD8?logo=go&logoColor=white)](https://pkg.go.dev/github.com/hexagon-codes/hexagon)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![CI](https://img.shields.io/badge/CI-passing-brightgreen)](https://github.com/hexagon-codes/hexagon/actions)

</div>

---

### 📖 项目简介

**Hexagon** 取名自网络热词「**六边形战士**」，寓意均衡强大、无懈可击。

我们聚焦 **易用性、性能、扩展性、任务编排、可观测性、安全性** 六大核心维度，深耕技术打磨，致力于实现各能力模块的均衡卓越，为 Go 开发者打造企业级落地首选的 AI Agent 开发基座。

</p>

### 🚀 核心特性

* ⚡ **高性能** │ 原生 Go 驱动，极致并发，支持 100k+ 活跃 Agent
* 🧩 **易用性** │ 声明式 API 设计，3 行代码极速构建基础原型
* 🛡️ **安全性** │ 企业级沙箱隔离，内置完备的权限管控与防护
* 🔧 **扩展性** │ 插件化架构，支持高度自定义的组件无缝集成
* 🛠️ **编排力** │ 强大的图编排引擎，轻松驾驭复杂的多级任务链路
* 🔍 **可观测** │ 深度集成 OpenTelemetry，实现全链路透明追踪

---

## 🌐 生态系统

Hexagon 是一个完整的 AI Agent 开发生态，由多个仓库组成：

| 仓库 | 说明 | 链接 |
|-----|------|------|
| **hexagon** | AI Agent 框架核心 (编排、RAG、Graph、Hooks) | [github.com/hexagon-codes/hexagon](https://github.com/hexagon-codes/hexagon) |
| **ai-core** | AI 基础能力库 (LLM/Tool/Memory/Schema) | [github.com/hexagon-codes/ai-core](https://github.com/hexagon-codes/ai-core) |
| **toolkit** | Go 通用工具库 (lang/crypto/net/cache/util) | [github.com/hexagon-codes/toolkit](https://github.com/hexagon-codes/toolkit) |
| **hexagon-ui** | Dev UI 前端 (Vue 3 + TypeScript) | [github.com/hexagon-codes/hexagon-ui](https://github.com/hexagon-codes/hexagon-ui) |

### 🧠 ai-core — AI 基础能力库

提供 LLM、Tool、Memory、Schema 等核心抽象，支持多种 LLM Provider：

```go
import "github.com/hexagon-codes/ai-core/llm"
import "github.com/hexagon-codes/ai-core/llm/openai"
import "github.com/hexagon-codes/ai-core/tool"
import "github.com/hexagon-codes/ai-core/memory"
```

**主要模块：**
- `llm/` - LLM Provider 接口 + 实现 (OpenAI, DeepSeek, Anthropic, Gemini, 通义, 豆包, Ollama)
- `llm/router/` - 智能模型路由 (任务感知 + 模型能力档案)
- `tool/` - 工具系统，支持函数式定义
- `memory/` - 记忆系统，支持向量存储
- `schema/` - JSON Schema 自动生成
- `streamx/` - 流式响应处理
- `template/` - Prompt 模板引擎

### 🛠️ toolkit — Go 通用工具库

生产级 Go 通用工具包，提供语言增强、加密、网络、缓存、协程池等基础能力：

```go
import "github.com/hexagon-codes/toolkit/lang/conv"      // 类型转换
import "github.com/hexagon-codes/toolkit/lang/stringx"   // 字符串工具
import "github.com/hexagon-codes/toolkit/lang/syncx"     // 并发工具
import "github.com/hexagon-codes/toolkit/net/httpx"      // HTTP 客户端
import "github.com/hexagon-codes/toolkit/net/sse"        // SSE 客户端
import "github.com/hexagon-codes/toolkit/util/retry"     // 重试机制
import "github.com/hexagon-codes/toolkit/util/idgen"     // ID 生成
import "github.com/hexagon-codes/toolkit/util/poolx"     // 协程池
import "github.com/hexagon-codes/toolkit/cache/local"    // 本地缓存
```

**主要模块：**
- `lang/` - 语言增强 (conv, stringx, slicex, mapx, timex, contextx, errorx, syncx)
- `crypto/` - 加密 (aes, rsa, sign)
- `net/` - 网络 (httpx, sse, ip)
- `cache/` - 缓存 (local, redis, multi)
- `util/` - 工具 (retry, rate, idgen, logger, validator, poolx 协程池)
- `collection/` - 数据结构 (set, list, queue, stack)

### 🎨 hexagon-ui — Dev UI 前端

基于 Vue 3 + TypeScript 的开发调试界面：

```bash
cd hexagon-ui
npm install
npm run dev
# 访问 http://localhost:5173
```

**功能特性：**
- 实时事件流 (SSE 推送)
- 指标仪表板
- 事件详情查看
- LLM 流式输出展示

## ⚡ 快速开始

### 📦 安装

```bash
go get github.com/hexagon-codes/hexagon
```

### ⚙️ 环境配置

```bash
# OpenAI
export OPENAI_API_KEY=your-api-key

# 或 DeepSeek
export DEEPSEEK_API_KEY=your-api-key
```

### 🎯 3 行代码入门

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

### 🔧 带工具的 Agent

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

func main() {
    // 定义计算器工具
    type CalcInput struct {
        A  float64 `json:"a" desc:"第一个数字" required:"true"`
        B  float64 `json:"b" desc:"第二个数字" required:"true"`
        Op string  `json:"op" desc:"运算符" required:"true" enum:"add,sub,mul,div"`
    }

    calculator := hexagon.NewTool("calculator", "执行数学计算",
        func(ctx context.Context, input CalcInput) (float64, error) {
            switch input.Op {
            case "add": return input.A + input.B, nil
            case "sub": return input.A - input.B, nil
            case "mul": return input.A * input.B, nil
            case "div": return input.A / input.B, nil
            }
            return 0, fmt.Errorf("unknown operator")
        },
    )

    // 创建带工具的 Agent
    agent := hexagon.QuickStart(
        hexagon.WithTools(calculator),
        hexagon.WithSystemPrompt("你是一个数学助手"),
    )

    output, _ := agent.Run(context.Background(), hexagon.Input{
        Query: "计算 123 * 456",
    })
    fmt.Println(output.Content)
}
```

### 🔍 RAG 检索增强

```go
// 创建 RAG 引擎
engine := hexagon.NewRAGEngine(
    hexagon.WithRAGStore(hexagon.NewMemoryVectorStore()),
    hexagon.WithRAGEmbedder(hexagon.NewOpenAIEmbedder()),
)

// 索引文档
engine.Index(ctx, []hexagon.Document{
    {ID: "1", Content: "Go 支持并发编程"},
    {ID: "2", Content: "Go 有丰富的标准库"},
})

// 检索
docs, _ := engine.Retrieve(ctx, "Go 的特性", hexagon.WithTopK(2))
```

### 📊 图编排

```go
import "github.com/hexagon-codes/hexagon/orchestration/graph"

// 构建工作流图
g, _ := graph.NewGraph[MyState]("workflow").
    AddNode("analyze", analyzeHandler).
    AddNode("process", processHandler).
    AddEdge(graph.START, "analyze").
    AddEdge("analyze", "process").
    AddEdge("process", graph.END).
    Build()

// 执行
result, _ := g.Run(ctx, initialState)
```

### 👥 多 Agent 团队

```go
// 创建团队
team := hexagon.NewTeam("research-team",
    hexagon.WithAgents(researcher, writer, reviewer),
    hexagon.WithMode(hexagon.TeamModeSequential),
)

// 执行
output, _ := team.Run(ctx, hexagon.Input{Query: "写一篇技术文章"})
```

## 🚀 高级能力

### 🔀 智能模型路由 (Smart Router)

根据任务类型、复杂度自动选择最优模型：

```go
import "github.com/hexagon-codes/ai-core/llm/router"

// 创建智能路由器
smartRouter := router.NewSmartRouter(baseRouter,
    router.WithAutoClassify(true),
)

// 带路由上下文的请求
routingCtx := router.NewRoutingContext(router.TaskTypeCoding, router.ComplexityMedium).
    WithPriority(router.PriorityQuality).
    RequireFunctions()

resp, decision, _ := smartRouter.CompleteWithRouting(ctx, req, routingCtx)
// decision 包含: 选择的模型、得分、原因、备选方案
```

**特性：**
- 任务感知路由 (coding/reasoning/creative/analysis 等)
- 质量/成本/延迟优先级策略
- 20+ 预定义模型能力档案
- 路由历史和统计分析

### ♻️ 持久化运行时 (Durable Runtime)

统一的 Agent 执行运行时，支持 per-tool exactly-once 快照与中断恢复（Resume）：

```go
import (
    "github.com/hexagon-codes/hexagon/runtime"
    "github.com/hexagon-codes/hexagon/checkpoint"
)

// 基于 Checkpointer 的持久化执行：崩溃/中断后 Resume 不重复执行已完成的工具
durable := runtime.NewDurableExecution(checkpoint.NewMemory())
```

**特性：**
- per-tool exactly-once：已完成工具调用入快照，Resume 不重放
- 统一 Runner + 执行策略（ReAct / PlanExecute / Reflection 共用同一运行时）
- 三维 Budget（token/调用/时长）+ CostControl 统一抽象
- 五级 PermissionMode 与 Context Compaction 中间件 (`runtime/middleware`)
- 长任务进度事件 + SSE 事件 sink

### 🧬 Agent 即工具 (AgentTool)

将一个 Agent 直接包装为工具，作为另一 Agent 的子能力组合：

```go
import "github.com/hexagon-codes/hexagon/agent"

// 把 researcher Agent 暴露为可被 supervisor 调用的工具
researcherTool := agent.NewAgentTool(researcher,
    agent.WithAgentToolName("research"),
    agent.WithAgentToolDescription("调研指定主题并返回要点"),
)

supervisor := hexagon.QuickStart(
    hexagon.WithTools(researcherTool),
    hexagon.WithSystemPrompt("你是一个调度者，按需调用子 Agent"),
)
```

### 🧾 原生结构化输出 (Structured Output)

借助 Provider 原生 `json_schema` 强制解码为强类型 Go 结构体：

```go
import "github.com/hexagon-codes/hexagon/llm/structured"

type Invoice struct {
    Number string  `json:"number"`
    Amount float64 `json:"amount"`
}

inv, _ := structured.Generate[Invoice](ctx, provider, "解析这张发票：...",
    structured.WithNativeJSONSchema("invoice"),
    structured.WithStrictMode(true),
)
```

### 📄 智能文档工作流 (ADW)

超越传统 RAG 的端到端文档自动化：

```go
import "github.com/hexagon-codes/hexagon/rag/adw"
import "github.com/hexagon-codes/hexagon/rag/adw/extractor"
import "github.com/hexagon-codes/hexagon/rag/adw/validator"

// 定义提取 Schema
schema := adw.NewExtractionSchema("invoice").
    AddStringField("invoice_number", "发票号码", true).
    AddDateField("date", "日期", "YYYY-MM-DD", true).
    AddMoneyField("amount", "金额", true).
    AddStringField("vendor", "供应商", false)

// 创建处理管道
pipeline := adw.NewPipeline("invoice-processing").
    AddStep(adw.NewDocumentTypeDetectorStep()).
    AddStep(extractor.NewLLMExtractionStep(llmProvider, schema)).
    AddStep(extractor.NewEntityExtractionStep(llmProvider)).
    AddStep(validator.NewSchemaValidationStep(schema)).
    AddStep(adw.NewConfidenceCalculatorStep()).
    Build()

// 处理文档
output, _ := pipeline.Process(ctx, adw.PipelineInput{
    Documents: documents,
    Schema:    schema,
})

// 访问结果
for _, doc := range output.Documents {
    fmt.Println("发票号:", doc.StructuredData["invoice_number"])
    fmt.Println("实体:", doc.Entities)
    fmt.Println("验证:", doc.IsValid())
}
```

**特性：**
- Document 扩展：结构化数据/表格/实体/关系/验证错误
- Schema 驱动的结构化提取
- LLM 提取器：实体/关系提取
- 完整验证：类型/格式/范围/枚举/正则
- 并发处理 + 钩子系统

### 🌐 A2A 协议 (Agent-to-Agent)

实现 Google A2A 协议，支持标准化的 Agent 间通信：

```go
import "github.com/hexagon-codes/hexagon/agent/a2a"

// 将 Hexagon Agent 暴露为 A2A 服务
server := a2a.ExposeAgent(myAgent, "http://localhost:8080")
server.Start(":8080")

// 连接远程 A2A Agent
client := a2a.NewClient("http://remote-agent.example.com")
card, _ := client.GetAgentCard(ctx)

// 发送消息
task, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("你好"),
})

// 流式交互
events, _ := client.SendMessageStream(ctx, req)
for event := range events {
    switch e := event.(type) {
    case *a2a.ArtifactEvent:
        fmt.Print(e.Artifact.GetTextContent())
    }
}
```

**特性：**
- 完整 A2A 协议实现 (AgentCard/Task/Message/Artifact)
- JSON-RPC 2.0 + SSE 流式响应
- 多种认证方式 (Bearer Token/API Key/Basic Auth/RBAC)
- Agent 发现服务 (Registry/Static/Remote)
- 推送通知支持
- 与 Hexagon Agent 无缝桥接

## 💡 设计理念

1. **渐进式复杂度** - 入门 3 行代码，进阶声明式配置，专家图编排
2. **约定优于配置** - 合理默认值，零配置可运行
3. **组合优于继承** - 小而专注的组件，灵活组合
4. **显式优于隐式** - 类型安全，编译时检查
5. **生产优先** - 内置可观测性，优雅降级

## 🏗️ 架构

### 📐 整体架构

<img src=".github/assets/architecture.png" alt="Hexagon 整体架构" width="800" style="height: auto;">

### 🔗 生态系统依赖

<img src=".github/assets/ecosystem.png" alt="Hexagon 生态系统依赖" width="800" style="height: auto;">

### 📈 数据流

<img src=".github/assets/workflow.png" alt="Hexagon 数据流" width="800" style="height: auto;">

## 🤖 LLM 支持

| Provider | 状态 |
|----------|------|
| OpenAI (GPT-4, GPT-4o, o1, o3) | ✅ 已支持 |
| DeepSeek | ✅ 已支持 |
| Anthropic (Claude) | ✅ 已支持 |
| Google Gemini | ✅ 已支持 |
| 通义千问 (Qwen) | ✅ 已支持 |
| 豆包 (Ark) | ✅ 已支持 |
| Ollama (本地模型) | ✅ 已支持 |

## 📁 项目结构

```
hexagon/
├── agent/              # Agent 核心 (ReAct/Role/Team/Handoff/State/Primitives/AgentTool/Supervisor)
│   ├── a2a/            # A2A 协议 (Client/Server/Handler/Discovery)
│   ├── artifact/       # 工件系统
│   ├── semantic/       # 语义能力
│   └── skill/          # 技能注册与签名
├── core/               # 统一接口 (Component/Runnable, Stream[T], Compose/Fallback)
├── runtime/            # 统一执行运行时 (Runner/DurableExecution/策略/中间件)
│   ├── middleware/     # Budget/CostControl/Compaction/PermissionMode
│   └── strategy/       # 执行策略 (ReAct/PlanExecute/Reflection)
├── orchestration/      # 编排引擎
│   ├── graph/          # 图编排 (状态图/检查点/Barrier/分布式/可视化)
│   ├── chain/          # 链式编排 (Compile 期 I/O 类型校验)
│   ├── workflow/       # 工作流引擎
│   └── planner/        # 规划器
├── checkpoint/         # 统一 Checkpointer 持久化
├── interrupt/          # 中断恢复
├── rag/                # RAG 系统
│   ├── loader/         # 文档加载 + Parser 层 (Text/MD/CSV/XLSX/PPTX/DOCX/PDF/OCR)
│   ├── splitter/       # 文档分割 (Character/Recursive/Markdown/Sentence/Token/Code)
│   ├── retriever/      # 检索器 (Vector/Keyword/Hybrid/HyDE/Adaptive/ParentDoc)
│   ├── reranker/       # 重排序
│   ├── synthesizer/    # 响应合成
│   ├── adw/            # 智能文档工作流 (extractor/validator)
│   ├── agentic/        # Agentic RAG
│   ├── corrective/     # 纠错式 RAG
│   ├── selfrag/        # Self-RAG
│   └── multimodal/     # 多模态
├── llm/                # LLM 编排层
│   ├── structured/     # 原生 json_schema 结构化输出
│   ├── batch/          # 批量调用
│   ├── conversation/   # 会话管理
│   ├── parser/         # 输出解析
│   └── template/       # Prompt 模板
├── memory/store/       # 多 Agent 记忆存储 (InMemory/File/Redis/Persistent)
├── mcp/                # MCP 协议 (动态发现/自动重连/多传输)
├── hooks/              # 钩子系统 (Run/Tool/LLM/Retriever)
├── observe/            # 可观测性 (Tracer/Metrics/OTel/Langfuse/DevUI/Replay)
├── security/           # 安全防护 (Guard/Guardrails/RBAC/Cost/Audit/Filter/PII/Tenant/Credential)
├── tool/               # 工具系统 (File/Python/Shell/Sandbox/HTTP/Search/DB/Browser...)
├── store/              # 存储
│   └── vector/         # 向量存储 (FAISS/PgVector/Redis/Milvus/Chroma/Pinecone/Weaviate)
├── client/             # 客户端
├── plugin/             # 插件系统
├── config/             # 配置管理
├── evaluate/           # 评估系统 (agenteval/rag/metrics)
├── testing/            # 测试工具 (Mock/Record/E2E/Integration)
├── deploy/             # 部署配置 (Docker Compose/Helm Chart/CI)
├── examples/           # 示例代码 (独立 module)
├── hexagon.go          # 顶层 API（核心入口符号）
└── deprecated.go       # 过渡性重导出（下一大版本移除）
```

## ⚠️ 近期重要变更

### 架构重构（v0.5.0）

v0.5.0 进行了一次结构性收敛，并升级到 ai-core v0.1.4 / toolkit v0.1.0（Go 1.25）：

- **顶层 feature 包归组**：`a2a → agent/a2a`、`artifact → agent/artifact`、`semantic → agent/semantic`、`skill → agent/skill`、`adw → rag/adw`。
- **编排正统化**：以 `orchestration/graph` 为编排正统轴，裁剪冗余的 `compose` / `process` / `flow` 包。
- **持久化收敛**：统一为单一 `Checkpointer`（`checkpoint/`）。
- **下沉去重**：stream 下沉 `ai-core/streamx`、媒体下沉 `ai-core/media`、安全/沙箱/blobstore 下沉 `toolkit`；Schema 统一走 `ai-core/schema`（`core.Schema` 为类型别名）。
- **新增能力**：统一 `runtime`（DurableExecution per-tool exactly-once + Resume、Budget/CostControl、五级 PermissionMode、Context Compaction）、`agent.AgentTool`、MCP 自动重连、`observe/otel` 的 Langfuse 导出、`orchestration/chain` 的 Compile 期类型校验、`rag/loader` Parser 层、`llm/structured` 原生 json_schema。
- **examples 独立 module**：`go get github.com/hexagon-codes/hexagon` 不再拉入示例依赖图。

> 迁移期保留 `deprecated.go` 作为顶层重导出 shim，将在下一大版本移除。

### 顶层 API 瘦身（v0.3.2-beta）

`hexagon.go` 的导出符号从 98 个精简至 **18 个核心符号**，仅保留最常用的入口：

- `Chat()`, `ChatWithTools()`, `Run()` — 便捷函数
- `QuickStart()` 及选项函数 (`WithProvider`, `WithTools`, `WithSystemPrompt`, `WithMemory`)
- `NewTool()` — 工具创建
- `SetDefaultProvider()` — 设置默认 LLM Provider
- 核心类型重导出 (`Input`, `Output`, `Tool`, `Memory`, `Message`, `Agent`, `Provider`)
- `Version` 常量

其余所有导出均已移至 `deprecated.go`，附带弃用注释，**将在下一个大版本中移除**。

**迁移方式：** 直接 import 对应子包，而非通过顶层包访问。例如：

```go
// 旧方式（已弃用）
team := hexagon.NewTeam("my-team", hexagon.WithAgents(a1, a2))
engine := hexagon.NewRAGEngine(hexagon.WithRAGStore(store))

// 新方式（推荐）
import "github.com/hexagon-codes/hexagon/agent"
import "github.com/hexagon-codes/hexagon/rag"

team := agent.NewTeam("my-team", agent.WithAgents(a1, a2))
engine := rag.NewEngine(rag.WithStore(store))
```

### Bug 修复与改进

- **`RunWithStats` 并发安全** — 使用本地节点副本，消除多 goroutine 间的数据竞争
- **`ParallelForEachLoopNode` 不再死锁** — 修复 context 取消时的死锁问题
- **`RecursiveSplitter` 防无限循环** — 当 overlap >= chunkSize 时自动保护
- **`SetDefaultProvider` 时序修复** — 即使在 `Chat()`/`QuickStart()` 之前调用也会被正确使用

## 📚 文档

### 📄 核心文档

| 文档 | 说明 |
|-----|------|
| [快速入门](docs/QUICKSTART.md) | 5 分钟上手 Hexagon |
| [架构设计](docs/DESIGN.md) | 框架设计理念和架构 |
| [API 参考](docs/API.md) | 完整 API 文档 |
| [稳定性说明](docs/STABILITY.md) | API 稳定性和版本策略 |
| [框架对比](docs/comparison.md) | 与主流框架的对比分析 |

### 📖 使用指南

| 指南 | 说明 |
|-----|------|
| [快速开始](docs/guides/getting-started.md) | 从零开始构建第一个 Agent |
| [Agent 开发](docs/guides/agent-guide.md) | Agent 开发完整指南 |
| [Agent 进阶](docs/guides/agent-development.md) | 高级 Agent 开发模式 |
| [RAG 系统](docs/guides/rag-guide.md) | 检索增强生成入门 |
| [RAG 集成](docs/guides/rag-integration.md) | RAG 系统深度集成 |
| [图编排](docs/guides/graph-orchestration.md) | 复杂工作流编排 |
| [多 Agent](docs/guides/multi-agent.md) | 多 Agent 协作系统 |
| [插件开发](docs/guides/plugin-guide.md) | 插件系统使用指南 |
| [可观测性](docs/guides/observability.md) | 追踪、指标、日志集成 |
| [安全防护](docs/guides/security.md) | 安全最佳实践 |
| [性能优化](docs/guides/performance-optimization.md) | 性能调优指南 |

### 💻 示例代码

| 示例 | 说明 |
|-----|------|
| [examples/quickstart](examples/quickstart) | 快速入门示例 |
| [examples/react](examples/react) | ReAct Agent 示例 |
| [examples/rag](examples/rag) | RAG 检索示例 |
| [examples/graph](examples/graph) | 图编排示例 |
| [examples/team](examples/team) | 多 Agent 团队示例 |
| [examples/handoff](examples/handoff) | Agent 交接示例 |
| [examples/chatbot](examples/chatbot) | 聊天机器人示例 |
| [examples/code-review](examples/code-review) | 代码审查示例 |
| [examples/data-analysis](examples/data-analysis) | 数据分析示例 |
| [examples/qdrant](examples/qdrant) | Qdrant 向量存储示例 |
| [examples/devui](examples/devui) | Dev UI 示例 |

## 🖥️ Dev UI

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

## 🚢 部署

Hexagon 提供三种部署方式，支持本地开发到生产环境的全场景覆盖：

| 方案 | 适用场景 | 命令 |
|------|---------|------|
| Docker Compose (完整模式) | 快速体验、演示、单机部署 | `make up` |
| Docker Compose (开发模式) | 团队开发（复用 docker-dev-env） | `make dev-up` |
| Helm Chart | K8s 集群、生产环境 | `make helm-install` |

### Docker 快速启动

```bash
cd deploy
cp .env.example .env
# 编辑 .env，填入 LLM API Key
make up

# 访问
# 主应用:  http://localhost:8000
# Dev UI:  http://localhost:8080
```

### Kubernetes / Helm

```bash
cd deploy
make helm-install

# 使用外部基础设施
helm install hexagon helm/hexagon/ \
  -n hexagon --create-namespace \
  --set qdrant.enabled=false \
  --set external.qdrant.url=http://my-qdrant:6333
```

详见 [部署指南](deploy/README.md)。

## 🔨 开发

```bash
make build   # 构建
make test    # 测试
make lint    # 代码检查
make fmt     # 格式化
```

## 🤝 贡献

欢迎贡献！请阅读 [CONTRIBUTING.md](CONTRIBUTING.md) 了解如何参与。

## 📜 许可证

[Apache License 2.0](LICENSE)

```
Copyright 2026 hexagon-codes

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```
