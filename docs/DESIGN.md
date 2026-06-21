<div align="right">语言: 中文 | <a href="DESIGN.en.md">English</a></div>

# Hexagon 架构设计文档

<div align="center">

**Go 生态全能型 AI Agent 框架**

</div>

## 目录

- [项目简介](#项目简介)
- [设计理念](#设计理念)
- [核心目标](#核心目标)
- [生态系统](#生态系统)
- [分层架构](#分层架构)
- [核心接口](#核心接口)
- [Agent 系统](#agent-系统)
- [RAG 系统](#rag-系统)
- [图编排引擎](#图编排引擎)
- [安全防护](#安全防护)
- [可观测性](#可观测性)

---

## 项目简介

**Hexagon** 取名自网络热词「**六边形战士**」，寓意均衡强大、无懈可击。

我们聚焦 **易用性、性能、扩展性、任务编排、可观测性、安全性** 六大核心维度，深耕技术打磨，致力于实现各能力模块的均衡卓越，为 Go 开发者打造企业级落地首选的 AI Agent 开发基座。

### 核心特性

* ⚡ **高性能** │ 原生 Go 驱动，极致并发，支持 100k+ 活跃 Agent
* 🧩 **易用性** │ 声明式 API 设计，3 行代码极速构建基础原型
* 🛡️ **安全性** │ 企业级沙箱隔离，内置完备的权限管控与防护
* 🔧 **扩展性** │ 插件化架构，支持高度自定义的组件无缝集成
* 🛠️ **编排力** │ 强大的图编排引擎，轻松驾驭复杂的多级任务链路
* 🔍 **可观测** │ 深度集成 OpenTelemetry，实现全链路透明追踪

---

## 设计理念

### 核心哲学

```
"简单的事情简单做，复杂的事情可能做"
```

Hexagon 遵循五大设计原则：

1. **渐进式复杂度**: 入门 3 行代码，进阶声明式配置，专家图编排
2. **约定优于配置**: 合理默认值，零配置可运行，需要时可完全定制
3. **组合优于继承**: 小而专注的组件，灵活组合，接口驱动
4. **显式优于隐式**: 类型安全，编译时检查，清晰的数据流
5. **生产优先**: 内置可观测性，优雅降级，运维友好

### Go 语言优势

选择 Go 作为实现语言的原因：

| 优势 | 说明 |
|-----|------|
| 原生并发 | goroutine + channel 实现高效并行 Agent 执行 |
| 单二进制部署 | 无运行时依赖，容器友好，运维简单 |
| 编译时类型检查 | 泛型支持，减少运行时错误 |
| 高性能 | 零分配流处理，对象池优化 |
| 可嵌入 | 轻松嵌入其他 Go 应用 |

---

## 核心目标

| 目标 | 量化指标 |
|-----|---------|
| 极简入门 | 学习曲线 < 1 小时 |
| 类型安全 | 0 运行时类型错误 |
| 高性能 | 100k+ 并发 Agent |
| 可观测 | 100% 覆盖率 |
| 生产就绪 | 99.99% 可用性 |

---

## 生态系统

Hexagon 是一个完整的 AI Agent 开发生态，由多个仓库组成：

| 仓库 | 说明 |
|-----|------|
| **hexagon** | AI Agent 框架核心 (编排、RAG、Graph、Hooks) |
| **ai-core** | AI 基础能力库 (LLM/Tool/Memory/Schema) |
| **toolkit** | Go 通用工具库 (lang/crypto/net/cache/util) |
| **hexagon-ui** | Dev UI 前端 (Vue 3 + TypeScript) |

### 生态系统依赖关系

```
                              ┌─────────────┐
                              │   hexagon   │
                              │ (AI Agent)  │
                              └──────┬──────┘
                                     │
                 ┌───────────────────┼───────────────────┐
                 │                   │                   │
                 ▼                   ▼                   ▼
          ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
          │   ai-core   │    │   toolkit   │    │ hexagon-ui  │
          │ (LLM/Tool/  │    │ (通用工具)   │    │  (Dev UI)   │
          │   Memory)   │    │             │    │             │
          └──────┬──────┘    └─────────────┘    └─────────────┘
                 │
                 ▼
          ┌─────────────┐
          │   toolkit   │
          └─────────────┘
```

---

## 分层架构

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            Application Layer                                 │
│  ┌─────────┐ ┌───────────┐ ┌──────────┐ ┌─────────────┐ ┌──────────────┐   │
│  │Chat Bot │ │ RAG Agent │ │ Workflow │ │ Multi-Agent │ │ Custom Agent │   │
│  └─────────┘ └───────────┘ └──────────┘ └─────────────┘ └──────────────┘   │
├─────────────────────────────────────────────────────────────────────────────┤
│                           Orchestration Layer                                │
│  ┌────────┐ ┌─────────┐ ┌───────────┐ ┌──────────┐ ┌──────────┐ ┌───────┐  │
│  │ Router │ │ Planner │ │ Scheduler │ │ Executor │ │  Graph   │ │ State │  │
│  └────────┘ └─────────┘ └───────────┘ └──────────┘ └──────────┘ └───────┘  │
├─────────────────────────────────────────────────────────────────────────────┤
│                            Agent Core Layer                                  │
│  ┌───────┐ ┌──────┐ ┌──────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌──────┐  │
│  │ Agent │ │ Role │ │ Team │ │ Network │ │ Handoff │ │ Context │ │ Msg  │  │
│  └───────┘ └──────┘ └──────┘ └─────────┘ └─────────┘ └─────────┘ └──────┘  │
├─────────────────────────────────────────────────────────────────────────────┤
│                            Capability Layer                                  │
│  ┌──────────────┐ ┌────────────┐ ┌─────────────┐ ┌────────────┐ ┌───────┐  │
│  │ LLM Provider │ │ RAG Engine │ │ Tool System │ │   Memory   │ │  KB   │  │
│  │ (ai-core)    │ │            │ │  (ai-core)  │ │  (ai-core) │ │       │  │
│  └──────────────┘ └────────────┘ └─────────────┘ └────────────┘ └───────┘  │
├─────────────────────────────────────────────────────────────────────────────┤
│                          Infrastructure Layer                                │
│  ┌────────┐ ┌────────┐ ┌─────────┐ ┌────────┐ ┌──────────┐ ┌────────────┐  │
│  │ Tracer │ │ Logger │ │ Metrics │ │ Config │ │ Security │ │   Plugin   │  │
│  └────────┘ └────────┘ └─────────┘ └────────┘ └──────────┘ └────────────┘  │
├─────────────────────────────────────────────────────────────────────────────┤
│                            Foundation Layer                                  │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │                         toolkit (通用工具库)                          │   │
│  │   lang │ crypto │ net │ cache │ util │ collection │ infra           │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 各层职责

**Application Layer (应用层)**
- 面向最终用户的完整应用
- 组合底层能力构建具体业务场景
- 示例：聊天机器人、RAG 问答系统、自动化工作流

**Orchestration Layer (编排层)**
- 组件编排和流程控制
- 图编排、工作流引擎、状态管理
- 支持条件分支、并行执行、检查点恢复

**Agent Core Layer (Agent 核心层)**
- Agent 生命周期管理
- 角色系统、团队协作、消息传递
- 状态管理（Turn/Session/Agent/Global 四层）

**Capability Layer (能力层)**
- LLM Provider 抽象和实现
- RAG 检索增强生成
- 工具系统和记忆系统

**Infrastructure Layer (基础设施层)**
- 可观测性（追踪、日志、指标）
- 安全防护（注入检测、PII、RBAC）
- 配置管理、缓存、插件系统

---

## 核心接口

### Component 接口

所有组件（Agent、Tool、Chain、Graph）的统一接口：

```go
// Component 是所有组件的统一接口
type Component[I, O any] interface {
    // Name 返回组件名称
    Name() string

    // Description 返回组件描述
    Description() string

    // Run 执行组件（非流式）
    Run(ctx context.Context, input I) (O, error)

    // Stream 执行组件（流式）
    Stream(ctx context.Context, input I) (Stream[O], error)

    // Batch 批量执行组件
    Batch(ctx context.Context, inputs []I) ([]O, error)

    // InputSchema 返回输入参数的 Schema
    InputSchema() *Schema

    // OutputSchema 返回输出参数的 Schema
    OutputSchema() *Schema
}
```

**设计要点：**
- 泛型支持，编译时类型检查
- 统一的执行模型（同步/流式/批量）
- Schema 自省能力，支持动态组合
- 所有组件可任意嵌套组合

### Stream 接口

流式数据处理能力：

```go
// Stream 是泛型流接口
type Stream[T any] interface {
    // Next 读取下一个元素
    Next(ctx context.Context) (T, bool)

    // Err 返回流处理中发生的错误
    Err() error

    // Close 关闭流，释放资源
    Close() error

    // Collect 收集所有元素到切片
    Collect(ctx context.Context) ([]T, error)

    // ForEach 对每个元素执行操作
    ForEach(ctx context.Context, fn func(T) error) error
}
```

---

## Agent 系统

### Agent 接口

```go
// Agent 是 AI Agent 的核心接口
type Agent interface {
    Component[Input, Output]

    // ID 返回 Agent 唯一标识
    ID() string

    // Role 返回 Agent 的角色定义
    Role() Role

    // Tools 返回 Agent 可用的工具列表
    Tools() []tool.Tool

    // Memory 返回 Agent 的记忆系统
    Memory() memory.Memory

    // LLM 返回 Agent 使用的 LLM Provider
    LLM() llm.Provider
}
```

### 输入输出

```go
// Input 是 Agent 的输入
type Input struct {
    Query   string         `json:"query"`           // 用户查询
    Context map[string]any `json:"context,omitempty"` // 额外上下文
}

// Output 是 Agent 的输出
type Output struct {
    Content   string           `json:"content"`            // 最终回复
    ToolCalls []ToolCallRecord `json:"tool_calls,omitempty"` // 工具调用记录
    Usage     llm.Usage        `json:"usage,omitempty"`    // Token 使用统计
    Metadata  map[string]any   `json:"metadata,omitempty"` // 额外元数据
}
```

### 角色系统

```go
// Role 角色定义
type Role struct {
    Name        string   // 角色名称
    Goal        string   // 角色目标
    Backstory   string   // 背景故事
    Constraints []string // 约束条件
    Capabilities []string // 能力列表
}
```

### 团队模式

```go
// TeamMode 团队工作模式
const (
    TeamModeSequential    // 顺序执行：Agent 依次执行
    TeamModeHierarchical  // 层级模式：Manager 协调分配
    TeamModeCollaborative // 协作模式：并行工作，消息传递
    TeamModeRoundRobin    // 轮询模式：轮流执行直到完成
)
```

### Agent 交接

```go
// TransferTo 创建转交工具
func TransferTo(target Agent) tool.Tool

// SwarmRunner 自动处理 Agent 之间的交接
type SwarmRunner struct {
    InitialAgent Agent
    MaxHandoffs  int
    GlobalState  GlobalState
}
```

### 四层状态管理

```go
// StateManager 状态管理器
type StateManager interface {
    // Turn 轮次状态（单次对话）
    Turn() State

    // Session 会话状态（多轮对话）
    Session() State

    // Agent Agent 持久状态
    Agent() State

    // Global 全局共享状态
    Global() State
}
```

---

## RAG 系统

### 核心组件

```go
// Document 文档
type Document struct {
    ID        string         // 唯一标识
    Content   string         // 文档内容
    Metadata  map[string]any // 元数据
    Embedding []float32      // 向量
    Score     float32        // 检索分数
}

// Loader 文档加载器
type Loader interface {
    Load(ctx context.Context) ([]Document, error)
}

// Splitter 文档分割器
type Splitter interface {
    Split(ctx context.Context, docs []Document) ([]Document, error)
}

// Indexer 索引器
type Indexer interface {
    Index(ctx context.Context, docs []Document) error
    Delete(ctx context.Context, ids []string) error
}

// Retriever 检索器
type Retriever interface {
    Retrieve(ctx context.Context, query string, opts ...RetrieveOption) ([]Document, error)
}

// Embedder 向量生成器
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dimension() int
}

// VectorStore 向量存储
type VectorStore interface {
    Add(ctx context.Context, docs []Document) error
    Search(ctx context.Context, embedding []float32, topK int, filter map[string]any) ([]Document, error)
}
```

### RAG Pipeline

```go
// Pipeline 是 RAG 处理管道
pipeline := rag.NewPipeline(loader, splitter, indexer, retriever)

// 摄取文档
pipeline.Ingest(ctx)

// 检索
docs, _ := pipeline.Query(ctx, "query", rag.WithTopK(5))
```

### 支持的组件

| 组件类型 | 实现 |
|---------|------|
| Loader | TextLoader, MarkdownLoader, DirectoryLoader, URLLoader, CSVLoader, ExcelLoader, PPTXLoader, DOCXLoader, PDFLoader, OCRLoader |
| Splitter | CharacterSplitter, RecursiveSplitter, MarkdownSplitter, SentenceSplitter, TokenSplitter, CodeSplitter, SemanticSplitter |
| Retriever | VectorRetriever, KeywordRetriever, HybridRetriever, MultiRetriever, HyDERetriever, AdaptiveRetriever, ParentDocRetriever |
| Indexer | VectorIndexer, ConcurrentIndexer, IncrementalIndexer |
| Embedder | OpenAIEmbedder, CachedEmbedder, MockEmbedder |
| VectorStore | MemoryStore, QdrantStore, FAISSStore, PgVectorStore, RedisStore, MilvusStore, ChromaStore, PineconeStore, WeaviateStore |

---

## 图编排引擎

### Graph 构建

```go
// 创建图
graph, _ := graph.NewGraph[MyState]("my-graph").
    AddNode("step1", step1Handler).
    AddNode("step2", step2Handler).
    AddNode("step3", step3Handler).
    AddEdge(graph.START, "step1").
    AddEdge("step1", "step2").
    AddConditionalEdge("step2", routerFunc, map[string]string{
        "yes": "step3",
        "no":  graph.END,
    }).
    AddEdge("step3", graph.END).
    Build()

// 执行
result, _ := graph.Run(ctx, initialState)
```

### State 接口

```go
// State 图状态接口
type State interface {
    // Clone 克隆状态
    Clone() State
}

// MapState 通用 map 状态
type MapState map[string]any

func (s MapState) Clone() State {
    clone := make(MapState, len(s))
    for k, v := range s {
        clone[k] = v
    }
    return clone
}
```

### Node Handler

```go
// NodeHandler 节点处理函数
type NodeHandler[S State] func(ctx context.Context, state S) (S, error)

// RouterFunc 路由函数（返回标签）
type RouterFunc[S State] func(state S) string
```

### 流式执行

```go
// 流式执行，返回每个节点的事件
events, _ := graph.Stream(ctx, initialState)
for event := range events {
    switch event.Type {
    case graph.EventTypeNodeStart:
        fmt.Printf("Starting node: %s\n", event.NodeName)
    case graph.EventTypeNodeEnd:
        fmt.Printf("Finished node: %s\n", event.NodeName)
    case graph.EventTypeEnd:
        fmt.Println("Graph completed")
    }
}
```

### 检查点

```go
// 启用检查点保存
graph.WithCheckpointer(checkpointer).Build()

// 从检查点恢复
checkpoint, _ := checkpointer.Get(ctx, threadID)
graph.Run(ctx, checkpoint.State, graph.WithThread(checkpoint.Config))
```

---

## 安全防护

### Guard 接口

```go
// Guard 安全守卫接口
type Guard interface {
    Name() string
    Check(ctx context.Context, input string) (*CheckResult, error)
    Enabled() bool
}

// CheckResult 检查结果
type CheckResult struct {
    Passed   bool      // 是否通过
    Score    float64   // 风险分数 (0-1)
    Category string    // 风险类别
    Reason   string    // 原因
    Findings []Finding // 发现的问题
}
```

### 内置守卫

```go
// Prompt 注入检测
guard := hexagon.NewPromptInjectionGuard()

// PII 检测
guard := hexagon.NewPIIGuard()

// 守卫链
chain := hexagon.NewGuardChain(hexagon.ChainModeAll,
    injectionGuard,
    piiGuard,
)
```

### 守卫链模式

```go
const (
    ChainModeAll   // 所有守卫都必须通过
    ChainModeAny   // 任一守卫通过即可
    ChainModeFirst // 第一个失败就停止
)
```

### 成本控制

```go
controller := hexagon.NewCostController(
    hexagon.WithBudget(10.0),            // $10 预算
    hexagon.WithMaxTokensTotal(100000),  // 总 token 限制
    hexagon.WithRequestsPerMinute(60),   // RPM 限制
)
```

---

## 可观测性

### Tracer

```go
// 创建追踪器
tracer := hexagon.NewTracer()
ctx := hexagon.ContextWithTracer(ctx, tracer)

// 开始 Span
span := hexagon.StartSpan(ctx, "operation_name")
defer span.End()

span.SetAttribute("key", "value")
span.RecordError(err)
```

### Metrics

```go
// 创建指标收集器
m := hexagon.NewMetrics()

// 计数器
m.Counter("agent_calls", "agent", "react").Inc()

// 直方图
m.Histogram("latency_ms", "operation", "chat").Observe(123.5)

// 仪表盘
m.Gauge("active_agents").Set(5)
```

---


## 目录结构

```
hexagon/
├── agent/                        # Agent 核心
│   ├── agent.go                  # Agent 接口定义
│   ├── react.go                  # ReAct Agent 实现
│   ├── primitives.go             # Agent 原语 (Parallel/Sequential/Route)
│   ├── agent_tool.go             # AgentTool (Agent 即工具)
│   ├── supervisor.go             # Supervisor 调度
│   ├── role.go                   # 角色系统
│   ├── team.go                   # 团队协作 (4 种工作模式)
│   ├── handoff.go                # Agent 交接
│   ├── state.go                  # 四层状态管理
│   ├── network.go                # Agent 网络通信
│   ├── consensus.go              # 共识机制
│   ├── a2a/                      # A2A 协议 (Client/Server/Handler/Discovery)
│   ├── artifact/                 # 工件系统
│   ├── semantic/                 # 语义能力
│   └── skill/                    # 技能注册与签名
│
├── core/                         # 核心接口
│   ├── runnable.go               # Component/Runnable 统一接口 + Stream[T] + Schema 别名
│   ├── compose.go                # 声明式组合
│   └── fallback.go               # 弹性回退
│
├── runtime/                      # 统一执行运行时
│   ├── runner.go                 # 统一 Runner
│   ├── durable.go                # DurableExecution (per-tool exactly-once + Resume)
│   ├── middleware/               # Budget/CostControl/Compaction/PermissionMode
│   └── strategy/                 # 执行策略 (ReAct/PlanExecute/Reflection)
│
├── orchestration/                # 编排层
│   ├── graph/                    # 图编排引擎
│   │   ├── graph.go              # 图定义和执行
│   │   ├── node.go               # 节点类型
│   │   ├── edge.go               # 边定义
│   │   ├── state.go              # 状态管理
│   │   ├── checkpoint.go         # 检查点保存
│   │   ├── interrupt.go          # 中断恢复
│   │   ├── barrier.go            # 同步屏障
│   │   ├── cache.go              # 节点缓存
│   │   ├── command.go            # 命令模式
│   │   ├── distributed.go        # 分布式执行
│   │   ├── functional.go         # 函数式 API
│   │   ├── stream_mode.go        # 流模式
│   │   └── visualize.go          # 图可视化
│   ├── chain/                    # 链式编排 (Compile 期 I/O 类型校验)
│   ├── workflow/                 # 工作流引擎
│   └── planner/                  # 规划器
│
├── checkpoint/                   # 统一 Checkpointer 持久化
├── interrupt/                    # 中断恢复
│
├── rag/                          # RAG 系统
│   ├── rag.go                    # RAG 核心接口
│   ├── loader/                   # 文档加载器 + Parser 层 (Text/MD/CSV/XLSX/PPTX/DOCX/PDF/OCR)
│   ├── splitter/                 # 文档分割器 (Character/Recursive/MD/Sentence/Token/Code)
│   ├── embedder/                 # 向量生成器
│   ├── indexer/                  # 索引器
│   ├── retriever/                # 检索器 (Vector/Keyword/Hybrid/HyDE/Adaptive/ParentDoc)
│   ├── reranker/                 # 重排序器
│   ├── synthesizer/              # 响应合成器 (Refine/Compact/Tree)
│   ├── adw/                      # 智能文档工作流 (extractor/validator)
│   ├── agentic/                  # Agentic RAG
│   ├── corrective/               # 纠错式 RAG
│   ├── selfrag/                  # Self-RAG
│   └── multimodal/               # 多模态
│
├── llm/                          # LLM 编排层
│   ├── structured/               # 原生 json_schema 结构化输出
│   ├── batch/                    # 批量调用
│   ├── conversation/             # 会话管理
│   ├── parser/                   # 输出解析
│   └── template/                 # Prompt 模板
│
├── memory/store/                 # 多 Agent 记忆存储 (InMemory/File/Redis/Persistent)
├── mcp/                          # MCP 协议 (动态发现/自动重连/多传输)
│
├── hooks/                        # 钩子系统
│
├── observe/                      # 可观测性
│   ├── tracer/                   # 追踪
│   ├── metrics/                  # 指标
│   ├── logger/                   # 日志
│   ├── devui/                    # Dev UI 后端
│   ├── otel/                     # OpenTelemetry + Langfuse 导出
│   ├── prometheus/               # Prometheus 集成
│   └── replay/                   # 录制回放
│
├── security/                     # 安全
│   ├── guard/                    # 安全守卫 (注入检测)
│   ├── guardrails/               # 防护栏
│   ├── pii/                      # PII 检测
│   ├── rbac/                     # 角色权限控制
│   ├── cost/                     # 成本控制
│   ├── audit/                    # 审计日志
│   ├── filter/                   # 内容过滤
│   ├── tenant/                   # 多租户隔离
│   └── credential/               # 凭证管理
│
├── tool/                         # 工具系统
│   ├── file/                     # 文件操作
│   ├── python/                   # Python 执行
│   ├── shell/                    # Shell 执行
│   ├── sandbox/                  # 沙箱执行
│   ├── http/                     # HTTP 请求
│   ├── search/                   # 搜索
│   ├── database/                 # 数据库
│   └── browser/                  # 浏览器
│
├── client/                       # 客户端
│
├── store/                        # 存储
│   └── vector/                   # 向量存储
│       ├── faiss/                # FAISS
│       ├── pgvector/             # PgVector
│       ├── redis/                # Redis
│       ├── milvus/               # Milvus
│       ├── chroma/               # Chroma
│       ├── pinecone/             # Pinecone
│       └── weaviate/             # Weaviate
│
│   # 注：Qdrant 实现位于 ai-core (ai-core/store/vector/qdrant)
│
├── plugin/                       # 插件系统
├── config/                       # 配置管理
├── evaluate/                     # 评估系统 (agenteval/rag/metrics)
│
├── testing/                      # 测试
│   ├── mock/                     # Mock 工具
│   ├── record/                   # 录制回放
│   ├── e2e/                      # 端到端测试
│   └── integration/              # 集成测试
│
├── bench/                        # 基准测试
├── examples/                     # 示例代码 (独立 module)
├── deploy/                       # 部署配置 (Docker Compose/Helm/CI)
├── docs/                         # 公开文档
├── internal/                     # 内部实现
│
├── hexagon.go                    # 主入口包 (版本: v0.5.0)
├── deprecated.go                 # 过渡性重导出 (下一大版本移除)
├── go.mod
├── Makefile
└── README.md
```

---

## 依赖关系

```
hexagon (主框架)
├── ai-core       ← AI 基础能力 (LLM/Tool/Memory/Schema)
└── toolkit       ← 通用工具 (lang/crypto/net/cache/util)
```

### ai-core — AI 基础能力库

`github.com/hexagon-codes/ai-core`

提供 LLM、Tool、Memory、Schema 等核心抽象：

- `llm/` - LLM Provider 接口 + 实现 (OpenAI, DeepSeek, Anthropic, Gemini, 通义, 豆包, Ollama)
- `tool/` - 工具系统，支持函数式定义
- `memory/` - 记忆系统，支持向量存储
- `schema/` - JSON Schema 自动生成
- `streamx/` - 流式响应处理
- `template/` - Prompt 模板引擎

### toolkit — Go 通用工具库

`github.com/hexagon-codes/toolkit`

生产级 Go 通用工具包，提供语言增强、加密、网络、缓存、协程池等基础能力：

- `lang/` - 语言增强 (conv, stringx, slicex, mapx, timex, contextx, errorx, syncx)
- `crypto/` - 加密 (aes, rsa, sign)
- `net/` - 网络 (httpx, sse, ip)
- `cache/` - 缓存 (local, redis, multi)
- `util/` - 工具 (retry, rate, idgen, logger, validator, poolx 协程池)
- `collection/` - 数据结构 (set, list, queue, stack)

---

## LLM Provider 支持

| Provider | 状态 |
|----------|:----:|
| OpenAI (GPT-4, GPT-4o, o1, o3) | ✅ 已完成 |
| DeepSeek | ✅ 已完成 |
| Anthropic (Claude) | ✅ 已完成 |
| Google Gemini | ✅ 已完成 |
| 通义千问 (Qwen) | ✅ 已完成 |
| 豆包 (Ark) | ✅ 已完成 |
| Ollama (本地模型) | ✅ 已完成 |

