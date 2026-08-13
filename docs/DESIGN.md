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

**Hexagon** 取名自网络热词「**六边形战士**」，表达对框架各项能力均衡发展的关注。

我们聚焦 **易用性、性能、扩展性、任务编排、可观测性、安全性** 六个维度，为 Go 开发者提供可组合的 AI Agent 开发框架。下列内容描述当前代码能力；性能、容量与可用性上限应由具体部署环境中的基准和压测结果确定。

### 核心特性

* ⚡ **并发执行** │ 基于 goroutine、流式处理、批执行与协程池组织并发任务
* 🧩 **易用性** │ 提供顶层 QuickStart API，并允许按子包直接组合底层能力
* 🛡️ **安全性** │ 提供 Guard、PII、RBAC、凭证、沙箱与 SSRF 防护组件
* 🔧 **扩展性** │ 通过接口、选项函数、Hooks 和插件扩展组件
* 🛠️ **编排力** │ 提供 Graph、Chain、Workflow、Planner 与多 Agent 编排
* 🔍 **可观测** │ 提供追踪、指标、日志，以及 OpenTelemetry、Prometheus 适配

---

## 设计理念

### 核心哲学

```
"简单的事情简单做，复杂的事情可能做"
```

Hexagon 遵循五大设计原则：

1. **渐进式复杂度**: 顶层便捷 API、声明式配置与图编排逐层开放
2. **约定优于配置**: 为常见场景提供合理默认值，并允许按需定制
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
| 高性能 | 原生并发、流式处理和对象池 |
| 可嵌入 | 轻松嵌入其他 Go 应用 |

---

## 核心目标

| 目标 | 设计方向 |
|-----|---------|
| 渐进式上手 | 顶层便捷 API 与可组合子包并存 |
| 类型安全 | 泛型 `Runnable`、显式接口与编译期检查 |
| 并发与流式 | 原生 Go 并发、批处理、背压与流式执行 |
| 可观测 | 追踪、指标、日志以及标准协议导出 |
| 运行可靠性 | Context 取消、重试、降级、安全检查与检查点能力 |

---

## 生态系统

Hexagon 是一个完整的 AI Agent 开发生态，由多个仓库组成：

| 仓库 | 说明 |
|-----|------|
| **hexagon** | AI Agent 框架核心 (编排、RAG、Graph、Hooks) |
| **ai-core** | AI 基础能力库（LLM/Tool/Memory/Schema/Stream/Vector Store） |
| **toolkit** | Go 通用工具库（lang/crypto/net/cache/util/infra） |
| **hexagon-ui** | 独立、可选的 Dev UI 前端；不属于 Hexagon 的 Go module 依赖 |

### Go module 依赖关系

```
hexagon
├── ai-core v0.2.10
│   └── toolkit v0.3.4
└── toolkit v0.3.4
```

`hexagon-ui` 是生态中的独立可选应用，不在上述 Go module 依赖图中。

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
│  │ LLM Provider │ │ RAG Engine │ │ Tool System │ │   Memory   │ │ Vector│  │
│  │ (ai-core)    │ │ (hexagon)  │ │  (ai-core)  │ │ (ai-core) │ │Store* │  │
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
- ai-core 提供 LLM Provider、Tool、Memory、Schema、Stream 和共享向量存储契约
- Hexagon 提供 RAG 流程、检索/索引编排和 Agent 能力组合
- `*` Memory 与 Qdrant 向量存储由 ai-core 提供，其余后端适配器位于 Hexagon

**Infrastructure Layer (基础设施层)**
- Hexagon 提供 Agent/LLM/Tool/Retriever 语义的追踪、指标、日志适配和 Hooks
- toolkit 提供通用日志、OpenTelemetry、Prometheus 与底层 observe 实现
- 安全防护（注入检测、PII、RBAC）
- 配置管理、缓存、插件系统

---

## 核心接口

### Runnable 与 Component

`core.Runnable` 是可执行组件的六范式接口；`core.Component` 为兼容旧名称而嵌入 `Runnable`。Tool、Graph 等领域对象仍各自使用对应子包的接口，不强制伪装成同一个接口。

```go
type Runnable[I, O any] interface {
    Invoke(ctx context.Context, input I, opts ...Option) (O, error)
    Stream(ctx context.Context, input I, opts ...Option) (*StreamReader[O], error)
    Batch(ctx context.Context, inputs []I, opts ...Option) ([]O, error)
    Collect(ctx context.Context, input *StreamReader[I], opts ...Option) (O, error)
    Transform(ctx context.Context, input *StreamReader[I], opts ...Option) (*StreamReader[O], error)
    BatchStream(ctx context.Context, inputs []I, opts ...Option) (*StreamReader[O], error)

    Name() string
    Description() string
    InputSchema() *Schema
    OutputSchema() *Schema
}

type Component[I, O any] interface {
    Runnable[I, O]
}
```

**设计要点：**
- 泛型输入输出与 Schema 自省提供编译期约束
- 覆盖普通输入/输出、批处理以及流输入/输出六种执行范式
- `BaseRunnable` 可从核心 `Invoke` 实现推导其余默认行为
- 执行选项通过 `core.Option` 显式传入

### StreamReader

流类型归属 ai-core 的 `streamx` 包；Hexagon 的 `core.StreamReader` 是其别名，旧 `core.Stream` 别名已经弃用。读取结束使用 `io.EOF` 表示。

```go
type StreamReader[T any] = streamx.StreamReader[T]

item, err := reader.Recv()
items, err := reader.Collect(ctx)
err = reader.ForEach(ctx, func(item T) error { return nil })
err = reader.Close()
```

`core.Schema` 同样是 ai-core `llm.Schema` 的别名；底层数据契约由 ai-core 持有，Hexagon 负责执行编排。

---

## Agent 系统

### Agent 接口

```go
type Agent interface {
    core.Runnable[Input, Output]

    ID() string
    Role() Role
    Tools() []tool.Tool
    Memory() memory.Memory
    LLM() llm.Provider

    // Run 是兼容旧调用方的方法；新代码使用 Invoke。
    Run(ctx context.Context, input Input) (Output, error)
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
    Content   string           `json:"content"`              // 最终回复
    ToolCalls []ToolCallRecord `json:"tool_calls,omitempty"` // 工具调用记录
    Blocks    template.Blocks  `json:"blocks,omitempty"`     // 有序内容块
    Usage     llm.Usage        `json:"usage,omitempty"`      // Token 使用统计
    StopReason runtime.StopReason `json:"stop_reason,omitempty"`
    Metadata  map[string]any   `json:"metadata,omitempty"`   // 额外元数据
}
```

### 角色系统

```go
type Role struct {
    Name            string
    Title           string
    Goal            string
    Backstory       string
    Expertise       []string
    Tools           []string
    Personality     string
    Constraints     []string
    AllowDelegation bool
    DelegateTo      []string
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
    Verbose      bool
}
```

### 四层状态管理

```go
type StateManager interface {
    Turn() TurnState
    Session() SessionState
    Agent() AgentState
    Global() GlobalState
    NewTurn() TurnState
    Snapshot() StateSnapshot
    Restore(snapshot StateSnapshot) error
}
```

---

## RAG 系统

### 核心组件

```go
// Document、Loader、Splitter、Indexer、Retriever 归属 Hexagon 的 rag 包。
type Document struct {
    ID        string
    Content   string
    Metadata  map[string]any
    Embedding []float32
    Score     float32
    Source    string
    CreatedAt time.Time
}

type Loader interface {
    Load(ctx context.Context) ([]Document, error)
    Name() string
}

type Splitter interface {
    Split(ctx context.Context, docs []Document) ([]Document, error)
    Name() string
}

type Indexer interface {
    Index(ctx context.Context, docs []Document) error
    Delete(ctx context.Context, ids []string) error
    Clear(ctx context.Context) error
    Count(ctx context.Context) (int, error)
}

type Retriever interface {
    Retrieve(ctx context.Context, query string, opts ...RetrieveOption) ([]Document, error)
}

// Engine 使用的轻量 Embedder 契约。
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dimension() int
}

// 共享向量存储契约归属 ai-core/store/vector。
type Store interface {
    Add(ctx context.Context, docs []vector.Document) error
    Search(ctx context.Context, query []float32, k int, opts ...vector.SearchOption) ([]vector.Document, error)
    Get(ctx context.Context, id string) (*vector.Document, error)
    Delete(ctx context.Context, ids []string) error
    Clear(ctx context.Context) error
    Count(ctx context.Context) (int, error)
    Close() error
}
```

`rag.Engine`、`rag/indexer` 和 `rag/retriever` 在流程边界把 `rag.Document` 转换为 ai-core 的 `vector.Document`。通用 `vector.Store`、`vector.Embedder`、内存存储与 Qdrant 适配器由 ai-core 持有；Hexagon 持有 RAG 编排以及其他向量后端适配器。

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
| Vector Store（ai-core） | MemoryStore、Qdrant Store |
| Vector Store（Hexagon 适配器） | FAISS、PgVector、Redis、Milvus、Chroma、Pinecone、Weaviate |

ai-core v0.2.7 起，Qdrant 新集合默认使用 SHA-256 派生的 UUIDv8 point ID。旧集合迁移时须直接使用 `github.com/hexagon-codes/ai-core/store/vector/qdrant`，显式选择 `PointIDLegacyHash31` 读取旧数据，再重建为 UUIDv8 新集合；同一集合不得混用两种 ID 策略。

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

仓库当前存在两个用途不同的检查点契约，不能混用：

- `checkpoint.Checkpointer` 是框架级、字节负载的持久化端口，提供 Memory/File 后端，并由 Agent durable execution、Interrupt Handler 和 Graph StateMachine 等消费者使用。
- `orchestration/graph.CheckpointSaver` 保存图专用快照；需要图的恢复、历史和分支能力时，使用 `EnhancedCheckpointSaver` 与 `CheckpointRunner`。

```go
// 框架级类型化值持久化
cp := checkpoint.NewMemory()
err := checkpoint.PutValue(ctx, cp, checkpoint.Checkpoint{
    Namespace: "run-1",
    ID:        "step-1",
}, state)
restored, _, ok, err := checkpoint.GetValue[MyState](ctx, cp, "run-1", "step-1")

// 图专用的运行与恢复
saver := graph.NewMemoryEnhancedCheckpointSaver()
runner := graph.NewCheckpointRunner(compiledGraph, saver, nil)
result, err := runner.Run(ctx, "thread-1", initialState)
result, err = runner.ResumeFromLatest(ctx, "thread-1")
```

直接调用 `Graph.Run` 不会因为构建器持有 saver 就自动完成上述恢复流程；不要使用旧的 `checkpointer.Get(ctx, threadID)` / `checkpoint.Config` 伪 API。

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
injectionGuard := guard.NewPromptInjectionGuard()

// PII 检测
piiGuard := guard.NewPIIGuard()

// 守卫链
chain := guard.NewGuardChain(guard.ChainModeAll,
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
controller, err := cost.NewController(
    cost.WithBudget(10.0),
    cost.WithMaxTokensTotal(100000),
    cost.WithRequestsPerMinute(60),
)
if err != nil {
    return err
}

agent := agent.NewReAct(
    agent.WithLLM(provider),
    agent.WithMiddleware(
        middleware.Budget{
            Limits: middleware.BudgetLimits{
                MaxTokens:  100000,
                MaxCostUSD: 10.0,
            },
            Cost: controller.BudgetCostFunc(),
        },
        middleware.CostControl{Record: controller.RecordUsageFunc()},
    ),
)
```

`middleware.Budget` 在单次 run 内于 LLM 调用前 fail-closed；`middleware.CostControl` 在调用后把用量写入共享 Controller，补齐跨 run 的累计账和预算强制。直接调用 LLM 且需要 RPM 预检时，还应在请求前调用 `controller.CheckRequest`。

---

## 可观测性

### Tracer

```go
// 使用 Hexagon 的内存追踪实现
t := tracer.NewMemoryTracer()
ctx = tracer.ContextWithTracer(ctx, t)

// StartSpan 返回派生 context 和 Span
ctx, span := tracer.StartSpan(ctx, "operation_name")
defer span.End()

span.SetAttribute("key", "value")
span.RecordError(err)
```

### Metrics

```go
// 创建指标收集器
m := metrics.NewMemoryMetrics()

// 计数器
m.Counter("agent_calls", "agent", "react").Inc()

// 直方图
m.Histogram("latency_ms", "operation", "chat").Observe(123.5)

// 仪表盘
m.Gauge("active_agents").Set(5)
```

可观测性按职责分层：

- `observe/tracer`、`observe/metrics` 与 `observe/logger` 提供 Hexagon 侧接口和内存实现。
- `observe/otel` 复用 toolkit 的通用 OpenTelemetry/OTLP 实现，并增加 Agent、LLM、Tool、Retriever 语义的 tracer 与 Hooks。
- `observe/prometheus` 复用 toolkit 的 Exporter、Registry 和指标适配器，并增加 Hexagon 运行时 Hooks。
- `observe/logger` 包装 toolkit logger，以便加入 Agent、Session、Trace 和 Span 上下文。

顶层 `hexagon.NewTracer()`、`hexagon.NewMetrics()` 等仅为迁移期重导出；新代码应直接 import 对应 `observe/*` 子包。

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
│   ├── runnable.go               # 六范式 Runnable + ai-core StreamReader/Schema 别名
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
├── checkpoint/                   # 框架级 Checkpointer（Memory/File）
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
│   ├── events/                   # 结构化事件
│   ├── eventstream/              # 事件流
│   ├── trace/                    # slog 追踪处理
│   ├── langfuse/                 # Langfuse 客户端
│   ├── otel/                     # toolkit OTel 复用 + Hexagon 语义适配
│   ├── prometheus/               # toolkit Prometheus 复用 + Hexagon Hooks
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
├── store/                        # 存储适配器
│   └── vector/                   # ai-core vector.Store 的后端适配器
│       ├── faiss/                # FAISS
│       ├── pgvector/             # PgVector
│       ├── redis/                # Redis
│       ├── milvus/               # Milvus
│       ├── chroma/               # Chroma
│       ├── pinecone/             # Pinecone
│       └── weaviate/             # Weaviate
│
│   # 注：Memory/Qdrant 位于 ai-core/store/vector
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
├── deploy/                       # 部署配置 (Docker Compose/Helm)
├── .github/workflows/            # 根模块 CI 工作流
├── docs/                         # 公开文档
├── internal/                     # 内部实现
│
├── hexagon.go                    # 主入口包（版本由构建注入/build info 解析）
├── deprecated.go                 # 过渡性重导出 (下一大版本移除)
├── go.mod
├── Makefile
└── README.md
```

---

## 依赖关系

```
hexagon（Go >= 1.25.12）
├── ai-core v0.2.10
│   └── toolkit v0.3.4
└── toolkit v0.3.4
```

### ai-core — AI 基础能力库

`github.com/hexagon-codes/ai-core` `v0.2.10`（Go >= 1.25.12）

提供 LLM、Tool、Memory、Schema、Stream 与向量存储等核心抽象：

- `llm/` - LLM Provider 接口 + 实现 (OpenAI, DeepSeek, Anthropic, Gemini, 通义, 豆包, Ollama)
- `tool/` - 工具系统，支持函数式定义
- `memory/` - 记忆系统
- `schema/` - JSON Schema 自动生成
- `streamx/` - 流式响应处理
- `store/vector/` - `Store`/`Embedder` 契约、MemoryStore 与 Qdrant 适配器
- `template/` - Prompt 模板引擎

### toolkit — Go 通用工具库

`github.com/hexagon-codes/toolkit` `v0.3.4`（Go >= 1.25.12）

生产级 Go 通用工具包，提供语言增强、加密、网络、缓存、协程池等基础能力：

- `lang/` - 语言增强 (conv, stringx, slicex, mapx, timex, contextx, errorx, syncx)
- `crypto/` - 加密 (aes, rsa, sign)
- `net/` - 网络 (httpx, sse, ip, ssrf)
- `cache/` - 缓存 (local, redis, multi)
- `util/` - 工具 (retry, circuit, rate, idgen, hash, logger, validator, poolx 协程池)
- `collection/` - 数据结构 (set, list, queue, stack)
- `infra/observe`、`infra/otel`、`infra/prometheus` - 通用可观测性底座

Hexagon 同时直接依赖 toolkit，并通过 ai-core 间接使用同一版本。Hexagon 在通用底座之上实现 Agent/RAG/Graph 语义和适配层，不复制 ai-core 或 toolkit 的所有权。

---

## LLM Provider 支持

| Provider | 实现状态 |
|----------|:--------:|
| OpenAI | 已提供适配器 |
| DeepSeek | 已提供适配器 |
| Anthropic (Claude) | 已提供适配器 |
| Google Gemini | 已提供适配器 |
| 通义千问 (Qwen) | 已提供适配器 |
| 豆包 (Ark) | 已提供适配器 |
| Ollama (本地模型) | 已提供适配器 |
