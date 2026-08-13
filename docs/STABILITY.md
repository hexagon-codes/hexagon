
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

当前 Hexagon 根模块要求 **Go 1.25.12 或更高版本**。仓库内的 `examples/` 是独立 Go module，不属于 Hexagon 根模块的发布表面，也不随根模块依赖自动同步。

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
- `Runnable[I, O]`、`Component[I, O]` 接口
- `StreamReader[T]` 类型别名（底层来自 `github.com/hexagon-codes/ai-core/streamx`）
- `Schema` 类型别名（底层来自 `github.com/hexagon-codes/ai-core/llm`）

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

**安全守卫** (`github.com/hexagon-codes/hexagon/security/guard`)
- `Guard` 接口
- `NewPromptInjectionGuard()`, `NewPIIGuard()`
- `GuardChain` 及其模式

**成本控制** (`github.com/hexagon-codes/hexagon/security/cost`)
- `Controller`、`NewController()` 及其选项函数
- `CheckRequest()`、`BudgetCostFunc()`、`RecordUsageFunc()`

**可观测性**
- `github.com/hexagon-codes/hexagon/observe/tracer`：`Tracer`、`Span`、`NewMemoryTracer()`
- `github.com/hexagon-codes/hexagon/observe/metrics`：`Metrics`、`NewMemoryMetrics()`

### Alpha (实验)

以下 API 处于实验阶段，可能有较大改动：

**工作流** (`github.com/hexagon-codes/hexagon/orchestration/workflow`)
- `Workflow`, `Step` 类型
- 持久化接口

**统一运行时** (`github.com/hexagon-codes/hexagon/runtime`)
- `Runner`、`DurableExecution`（per-tool exactly-once + Resume）
- `github.com/hexagon-codes/hexagon/runtime/middleware`：`BudgetControlConfig`、`BudgetControl`（统一单次 run 与跨 run 预算）、`Compaction`、`PermissionMode`
- `github.com/hexagon-codes/hexagon/runtime/strategy`：ReAct/PlanExecute/Reflection 执行策略

**持久化检查点** (`github.com/hexagon-codes/hexagon/checkpoint`)
- 统一 `Checkpointer` 接口（Memory/File 实现）

**检查点** (`github.com/hexagon-codes/hexagon/orchestration/graph`)
- `CheckpointSaver` 接口
- Redis 检查点实现
- 中断和恢复功能
- 分布式执行、Barrier 同步、节点缓存

**向量存储**
- RAG 层的 `VectorStore` 接口位于 `github.com/hexagon-codes/hexagon/rag`。
- 通用向量存储契约与内存实现位于 `github.com/hexagon-codes/ai-core/store/vector`；Qdrant 实现位于 `github.com/hexagon-codes/ai-core/store/vector/qdrant`。
- Hexagon 自有适配器分别位于 `github.com/hexagon-codes/hexagon/store/vector/faiss`、`github.com/hexagon-codes/hexagon/store/vector/pgvector`、`github.com/hexagon-codes/hexagon/store/vector/redis`、`github.com/hexagon-codes/hexagon/store/vector/milvus`、`github.com/hexagon-codes/hexagon/store/vector/chroma`、`github.com/hexagon-codes/hexagon/store/vector/pinecone`、`github.com/hexagon-codes/hexagon/store/vector/weaviate`；不存在可直接导入的 `github.com/hexagon-codes/hexagon/store/vector` 根包。

ai-core v0.2.7 起，Qdrant 新集合默认使用 SHA-256 派生 UUIDv8 point ID。迁移旧集合时，应直接导入 `github.com/hexagon-codes/ai-core/store/vector/qdrant`，显式选择 `PointIDLegacyHash31` 读取并导出旧数据，再重建为默认 UUIDv8 的新集合；不得在同一集合中混用两种 ID 策略。

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

**记忆共享** (`github.com/hexagon-codes/hexagon/agent`)
- `SharedMemory`、`SharedMemoryProxy`、`WithSharedMemory()` 实现多 Agent 记忆自动共享

**Agent 原语** (`github.com/hexagon-codes/hexagon/agent`)
- `ParallelAgent`、`SequentialAgent`、`LoopAgent` 原语

**MCP 协议** (`github.com/hexagon-codes/hexagon/mcp`)
- MCP 协议支持

### Deprecated (已弃用)

**顶层重导出** (`github.com/hexagon-codes/hexagon` — `deprecated.go`)

自 v0.3.2-beta 起，`hexagon.go` 的导出符号从 98 个精简至核心入口符号。原先通过顶层包暴露的便捷别名已全部移至 `deprecated.go`，将在下一个大版本中移除。

> v0.5.0 进一步将 `a2a`/`artifact`/`semantic`/`skill` 归组到 `agent/` 下，`adw` 归组到 `rag/` 下，并裁剪了 `compose`/`process`/`flow` 包。迁移期 `deprecated.go` 仍重导出这些符号的旧入口。

弃用只通过 Go 文档中的 `Deprecated:` 注释和迁移文档表达；兼容性重导出不会仅因调用旧入口而输出运行时 warning。

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
- 改进错误消息
- 修复 Bug

### 不兼容的更改

以下更改被视为不兼容（需要增加 MAJOR 版本）：

- 删除或重命名导出的函数、类型、常量
- 更改函数签名（参数或返回值）
- 更改导出接口定义，包括添加、删除或修改方法（Go 接口没有默认方法；添加方法会破坏外部实现者）
- 更改已有行为的语义

## 弃用流程

1. **标记弃用**: 在文档和代码注释中标记 `Deprecated:`
2. **迁移指南**: 提供迁移到新 API 的说明
3. **无运行时副作用**: 不为弃用本身输出 warning，避免库代码污染调用方日志
4. **保留期**: 至少保留 1 个 MINOR 版本周期
5. **移除**: 在下一个 MAJOR 版本中移除

## 导入路径稳定性

以下导入路径是稳定的：

```go
import "github.com/hexagon-codes/hexagon"                     // 顶层 API
import "github.com/hexagon-codes/hexagon/agent"               // Agent 与共享记忆
import "github.com/hexagon-codes/hexagon/core"                // 核心接口
import "github.com/hexagon-codes/hexagon/orchestration/graph" // 图编排
import "github.com/hexagon-codes/hexagon/rag"                 // RAG 与 RAG VectorStore
import "github.com/hexagon-codes/hexagon/runtime/middleware"  // 运行时中间件
import "github.com/hexagon-codes/hexagon/security/cost"       // 成本控制
import "github.com/hexagon-codes/hexagon/security/guard"      // 安全守卫
import "github.com/hexagon-codes/hexagon/observe/tracer"      // 追踪
import "github.com/hexagon-codes/hexagon/observe/metrics"     // 指标
```

导入路径稳定不代表其中所有 API 都已达到 Stable 等级；具体承诺仍以上方模块稳定性分级为准。

`internal/` 包不对外公开，可能随时更改。

## 依赖稳定性

Hexagon 根模块的当前依赖拓扑如下：

- L0：`toolkit v0.3.4`。
- L1：`ai-core v0.2.10`，其自身也要求 `toolkit v0.3.4`。
- L2：Hexagon 根模块直接要求上述两个版本，并以 Go module 最小版本选择规则解析为单一 toolkit 版本。

| 依赖 | 版本 | 说明 |
|-----|------|------|
| `github.com/hexagon-codes/ai-core` | v0.2.10 | AI 基础能力库 |
| `github.com/hexagon-codes/toolkit` | v0.3.4 | Go 通用工具库 |

根模块要求 Go 1.25.12 或更高版本。`examples/` 的 `go.mod` 独立维护并固定已发布版本，不属于上表，也不应据此推断与根模块 lockstep。依赖公开 API 的变化须先在 Hexagon 完成适配和回归，再随 Hexagon 自身版本发布。

## 反馈

如果您对 API 稳定性有任何问题或建议：

- 提交 [GitHub Issue](https://github.com/hexagon-codes/hexagon/issues)
