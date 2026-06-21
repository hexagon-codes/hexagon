
<div align="right">语言: 中文 | <a href="STABILITY.en.md">English</a></div>

# Hexagon API 稳定性说明

本文档说明 Hexagon 框架各模块的 API 稳定性等级和兼容性承诺。

## 版本规范

Hexagon 遵循 [语义化版本](https://semver.org/lang/zh-CN/) 规范：

```
MAJOR.MINOR.PATCH[-PRERELEASE]
```

- **MAJOR**: 不兼容的 API 更改
- **MINOR**: 向后兼容的功能新增
- **PATCH**: 向后兼容的 Bug 修复
- **PRERELEASE**: 预发布标识 (alpha, beta, GA)

## 稳定性等级

| 等级 | 说明 | 兼容性承诺 |
|:---:|------|-----------|
| **Stable** | 稳定 API，可用于生产 | MAJOR 版本内保持兼容 |
| **Beta** | 功能完整，API 可能微调 | MINOR 版本内尽量保持兼容 |
| **Alpha** | 实验性功能，API 可能大改 | 无兼容性承诺 |
| **Deprecated** | 已弃用，将在未来版本移除 | 至少保留 1 个 MINOR 版本 |

## 模块稳定性

### Stable (稳定)

以下 API 在 v1.x 版本内保持向后兼容：

**顶层 API** (`github.com/hexagon-codes/hexagon`)
- `Chat()`, `ChatWithTools()`, `Run()`
- `QuickStart()` 及其选项函数 (`WithProvider`, `WithTools`, `WithSystemPrompt`, `WithMemory`)
- `NewTool()`, `SetDefaultProvider()`
- 类型导出 (`Input`, `Output`, `Tool`, `Memory`, `Message`, `Agent`, `Provider`)

**核心接口** (`github.com/hexagon-codes/hexagon/core`)
- `Component[I, O]` 接口
- `Stream[T]` 接口
- `Schema` 类型

**Agent** (`github.com/hexagon-codes/hexagon/agent`)
- `Agent` 接口
- `Input`, `Output` 类型
- `NewReAct()` 及其选项函数
- `Role` 类型

**图编排** (`github.com/hexagon-codes/hexagon/orchestration/graph`)
- `State` 接口
- `MapState` 类型
- `NewGraph[S]()` 构建器
- `Graph[S].Run()`, `Graph[S].Stream()`
- `START`, `END` 常量

### Beta (测试)

以下 API 功能完整，但可能在 MINOR 版本中微调：

**多 Agent** (`github.com/hexagon-codes/hexagon/agent`)
- `Team` 及其选项函数
- `TeamMode` 常量
- `TransferTo()`, `SwarmRunner`
- `StateManager` 接口

**RAG** (`github.com/hexagon-codes/hexagon/rag`)
- `Engine` 及其选项函数
- `Document`, `Loader`, `Splitter`, `Retriever`, `Indexer`, `Embedder` 接口
- 内置加载器、分割器、检索器实现

**安全** (`github.com/hexagon-codes/hexagon/security`)
- `Guard` 接口
- `NewPromptInjectionGuard()`, `NewPIIGuard()`
- `GuardChain` 及其模式
- `CostController` 及其选项函数

**可观测性** (`github.com/hexagon-codes/hexagon/observe`)
- `Tracer`, `Span` 接口
- `Metrics` 接口
- `NewTracer()`, `NewMetrics()`

### Alpha (实验)

以下 API 处于实验阶段，可能有较大改动：

**工作流** (`github.com/hexagon-codes/hexagon/orchestration/workflow`)
- `Workflow`, `Step` 类型
- 持久化接口

**统一运行时** (`github.com/hexagon-codes/hexagon/runtime`)
- `Runner`、`DurableExecution`（per-tool exactly-once + Resume）
- `runtime/middleware`：Budget/CostControl/Compaction/PermissionMode
- `runtime/strategy`：ReAct/PlanExecute/Reflection 执行策略

**持久化检查点** (`github.com/hexagon-codes/hexagon/checkpoint`)
- 统一 `Checkpointer` 接口（Memory/File 实现）

**检查点** (`github.com/hexagon-codes/hexagon/orchestration/graph`)
- `CheckpointSaver` 接口
- Redis 检查点实现
- 中断和恢复功能
- 分布式执行、Barrier 同步、节点缓存

**向量存储** (`github.com/hexagon-codes/hexagon/store/vector`)
- `VectorStore` 接口
- Qdrant、FAISS、PgVector、Redis、Milvus、Chroma、Pinecone、Weaviate 实现

**高级检索器** (`github.com/hexagon-codes/hexagon/rag/retriever`)
- `HyDERetriever` - 假设文档检索
- `AdaptiveRetriever` - 自适应检索
- `ParentDocRetriever` - 父文档检索

**高级加载器** (`github.com/hexagon-codes/hexagon/rag/loader`)
- `ExcelLoader`, `PPTXLoader`, `CSVLoader` - Office 文件加载
- `OCRLoader` - VisionLLM OCR 文字提取

**高级分割器** (`github.com/hexagon-codes/hexagon/rag/splitter`)
- `TokenSplitter` - Token 计数分割
- `CodeSplitter` - 代码语法分割

**记忆共享** (`github.com/hexagon-codes/hexagon/memory`)
- 多 Agent 记忆自动共享

**Agent 原语** (`github.com/hexagon-codes/hexagon/agent`)
- `Parallel`, `Sequential`, `Route` 原语

**MCP 协议** (`github.com/hexagon-codes/hexagon/mcp`)
- MCP 协议支持

### Deprecated (已弃用)

**顶层重导出** (`github.com/hexagon-codes/hexagon` — `deprecated.go`)

自 v0.3.2-beta 起，`hexagon.go` 的导出符号从 98 个精简至核心入口符号。原先通过顶层包暴露的便捷别名已全部移至 `deprecated.go`，将在下一个大版本中移除。

> v0.5.0 进一步将 `a2a`/`artifact`/`semantic`/`skill` 归组到 `agent/` 下，`adw` 归组到 `rag/` 下，并裁剪了 `compose`/`process`/`flow` 包。迁移期 `deprecated.go` 仍重导出这些符号的旧入口。

涉及的符号包括但不限于：

- 编排引擎：`NewGraph()`, `NewChain()`, `START`, `END`
- 多 Agent：`NewTeam()`, `TransferTo()`, `WithAgents()`, `WithMode()`, `TeamMode*` 常量
- 可观测性：`NewTracer()`, `NewMetrics()`, `StartSpan()`, `ContextWithTracer()`
- 安全防护：`NewPromptInjectionGuard()`, `NewPIIGuard()`, `NewCostController()`
- RAG 系统：`NewRAGEngine()`, `NewRAGPipeline()`, 加载器/分割器/检索器/索引器/嵌入器工厂函数
- 向量存储：`NewMemoryVectorStore()`, `NewQdrantStore()`, `Qdrant*` 选项和常量
- LLM 相关：`NewOpenAI()`, `OpenAIWith*` 选项, `NewLLMRouter()`, 角色常量
- 状态管理：`NewStateManager()`, `NewGlobalState()`
- MCP 协议：`ConnectMCPServer()`, `ConnectMCPStdio()`, `ConnectMCPSSE()`, `NewMCPServer()`
- 记忆存储：`NewInMemoryStore()`, `NewFileStore()`, `NewRedisStore()`, `NewPersistentMemory()`
- 事件流：`NewEventStream()`, `Event*` 常量
- Skill 系统：`NewSkillRegistry()`, `NewHMACSigner()`
- 所有已弃用的类型别名（`Graph`, `Chain`, `State`, `MapState`, `Tracer`, `Span`, `Metrics`, `Guard` 等）

**迁移方式：** 直接 import 对应子包。例如：

```go
// 旧方式（已弃用）
team := hexagon.NewTeam("my-team", hexagon.WithAgents(a1, a2))

// 新方式（推荐）
import "github.com/hexagon-codes/hexagon/agent"
team := agent.NewTeam("my-team", agent.WithAgents(a1, a2))
```

## 兼容性策略

### 向后兼容的更改

以下更改被视为向后兼容：

- 添加新的导出函数、类型、常量
- 为现有函数添加可选参数（通过选项函数模式）
- 为接口添加具有默认实现的方法
- 改进错误消息
- 修复 Bug

### 不兼容的更改

以下更改被视为不兼容（需要增加 MAJOR 版本）：

- 删除或重命名导出的函数、类型、常量
- 更改函数签名（参数或返回值）
- 更改接口定义
- 更改已有行为的语义

## 弃用流程

1. **标记弃用**: 在文档和代码注释中标记 `Deprecated`
2. **迁移指南**: 提供迁移到新 API 的说明
3. **警告日志**: 运行时输出弃用警告
4. **保留期**: 至少保留 1 个 MINOR 版本周期
5. **移除**: 在下一个 MAJOR 版本中移除

## 导入路径稳定性

以下导入路径是稳定的：

```go
import "github.com/hexagon-codes/hexagon"                  // 顶层 API
import "github.com/hexagon-codes/hexagon/agent"            // Agent
import "github.com/hexagon-codes/hexagon/core"             // 核心接口
import "github.com/hexagon-codes/hexagon/orchestration/graph" // 图编排
import "github.com/hexagon-codes/hexagon/rag"              // RAG
import "github.com/hexagon-codes/hexagon/security/guard"   // 安全守卫
import "github.com/hexagon-codes/hexagon/observe/tracer"   // 追踪
import "github.com/hexagon-codes/hexagon/observe/metrics"  // 指标
```

`internal/` 包不对外公开，可能随时更改。

## 依赖稳定性

Hexagon 依赖以下外部库：

| 依赖 | 版本 | 说明 |
|-----|------|------|
| `github.com/hexagon-codes/ai-core` | v0.1.4 | AI 基础能力库 |
| `github.com/hexagon-codes/toolkit` | v0.1.0 | Go 通用工具库 |

这些依赖的公开 API 变更会同步反映在 Hexagon 的版本号中。

## 反馈

如果您对 API 稳定性有任何问题或建议：

- 提交 [GitHub Issue](https://github.com/hexagon-codes/hexagon/issues)
- 参与 [GitHub Discussions](https://github.com/hexagon-codes/hexagon/discussions)
