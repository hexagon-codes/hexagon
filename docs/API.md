<div align="right">语言: 中文 | <a href="API.en.md">English</a></div>

# Hexagon API 参考文档

本文档提供 Hexagon 框架的完整 API 参考。

## 目录

- [顶层 API](#顶层-api)
- [Agent](#agent)
- [Tool](#tool)
- [RAG](#rag)
- [Graph 编排](#graph-编排)
- [Team 多 Agent](#team-多-agent)
- [安全防护](#安全防护)
- [可观测性](#可观测性)
- [类型定义](#类型定义)

---

## 顶层 API

`github.com/hexagon-codes/hexagon` 包提供最简洁的入口 API。

### 便捷函数

#### Chat

执行简单对话（最简 API）。

```go
func Chat(ctx context.Context, query string, opts ...QuickStartOption) (string, error)
```

**示例：**
```go
response, err := hexagon.Chat(ctx, "What is Go?")
```

#### ChatWithTools

带工具的对话。

```go
func ChatWithTools(ctx context.Context, query string, tools ...tool.Tool) (string, error)
```

**示例：**
```go
result, err := hexagon.ChatWithTools(ctx, "What is 123 * 456?", calculatorTool)
```

#### Run

执行 Agent 并返回完整输出。

```go
func Run(ctx context.Context, input Input, opts ...QuickStartOption) (Output, error)
```

### QuickStart

快速创建一个 ReAct Agent。

```go
func QuickStart(opts ...QuickStartOption) *agent.ReActAgent
```

**选项：**

| 选项 | 说明 |
|-----|------|
| `WithProvider(p llm.Provider)` | 设置 LLM Provider |
| `WithTools(tools ...tool.Tool)` | 设置工具 |
| `WithSystemPrompt(prompt string)` | 设置系统提示词 |
| `WithMemory(m memory.Memory)` | 设置记忆系统 |

**示例：**
```go
agent := hexagon.QuickStart(
    hexagon.WithTools(searchTool, calculatorTool),
    hexagon.WithSystemPrompt("You are a helpful assistant."),
)
output, err := agent.Run(ctx, hexagon.Input{Query: "What is 2+2?"})
```

### SetDefaultProvider

设置默认 LLM Provider。

```go
func SetDefaultProvider(p llm.Provider)
```

---

## Agent

### Agent 接口

```go
type Agent interface {
    core.Component[Input, Output]
    ID() string
    Role() Role
    Tools() []tool.Tool
    Memory() memory.Memory
    LLM() llm.Provider
}
```

### Input

```go
type Input struct {
    Query   string         `json:"query"`             // 用户查询
    Context map[string]any `json:"context,omitempty"` // 额外上下文
}
```

### Output

```go
type Output struct {
    Content   string           `json:"content"`              // 最终回复
    ToolCalls []ToolCallRecord `json:"tool_calls,omitempty"` // 工具调用记录
    Usage     llm.Usage        `json:"usage,omitempty"`      // Token 使用统计
    Metadata  map[string]any   `json:"metadata,omitempty"`   // 额外元数据
}
```

### NewReActAgent

创建 ReAct Agent。

```go
var NewReActAgent = agent.NewReAct
```

**选项：**

| 选项 | 说明 |
|-----|------|
| `WithID(id string)` | 设置 Agent ID |
| `WithName(name string)` | 设置 Agent 名称 |
| `WithDescription(desc string)` | 设置 Agent 描述 |
| `WithSystemPrompt(prompt string)` | 设置系统提示词 |
| `WithLLM(provider llm.Provider)` | 设置 LLM Provider |
| `WithTools(tools ...tool.Tool)` | 设置工具列表 |
| `WithMemory(mem memory.Memory)` | 设置记忆系统 |
| `WithMaxIterations(n int)` | 设置最大迭代次数 |
| `WithVerbose(v bool)` | 设置详细输出模式 |
| `WithRole(role Role)` | 设置 Agent 角色 |

### Role

```go
type Role struct {
    Name         string   // 角色名称
    Goal         string   // 角色目标
    Backstory    string   // 背景故事
    Constraints  []string // 约束条件
    Capabilities []string // 能力列表
}
```

### StateManager

```go
type StateManager interface {
    Turn() State    // 轮次状态
    Session() State // 会话状态
    Agent() State   // Agent 状态
    Global() State  // 全局状态
}

var NewStateManager = agent.NewStateManager
var NewGlobalState = agent.NewGlobalState
```

---

## Tool

### NewTool

从函数创建工具。

```go
func NewTool[I, O any](name, description string, fn func(context.Context, I) (O, error)) *tool.FuncTool[I, O]
```

**输入结构体标签：**

| 标签 | 说明 |
|-----|------|
| `json:"name"` | JSON 字段名 |
| `desc:"description"` | 字段描述 |
| `required:"true"` | 是否必填 |
| `enum:"a,b,c"` | 可选值列表 |

**示例：**
```go
type CalcInput struct {
    A float64 `json:"a" desc:"第一个数字" required:"true"`
    B float64 `json:"b" desc:"第二个数字" required:"true"`
}

calculator := hexagon.NewTool("calculator", "执行加法计算",
    func(ctx context.Context, input CalcInput) (float64, error) {
        return input.A + input.B, nil
    },
)
```

---

## RAG

### NewRAGEngine

创建 RAG 引擎。

```go
var NewRAGEngine = rag.NewEngine
```

**选项：**

| 选项 | 说明 |
|-----|------|
| `WithRAGStore(store VectorStore)` | 设置向量存储 |
| `WithRAGEmbedder(embedder Embedder)` | 设置向量生成器 |
| `WithRAGLoader(loader Loader)` | 设置文档加载器 |
| `WithRAGSplitter(splitter Splitter)` | 设置文档分割器 |
| `WithRAGTopK(k int)` | 设置默认返回数量 |
| `WithRAGMinScore(score float32)` | 设置默认最小分数 |

**方法：**
```go
// 索引文档
func (e *Engine) Index(ctx context.Context, docs []Document) error

// 检索文档
func (e *Engine) Retrieve(ctx context.Context, query string, opts ...RetrieveOption) ([]Document, error)
```

### 检索选项

| 选项 | 说明 |
|-----|------|
| `WithTopK(k int)` | 返回文档数量 |
| `WithMinScore(score float32)` | 最小相关性分数 |
| `WithFilter(filter map[string]any)` | 元数据过滤条件 |

### Document

```go
type Document struct {
    ID        string         `json:"id"`
    Content   string         `json:"content"`
    Metadata  map[string]any `json:"metadata,omitempty"`
    Embedding []float32      `json:"embedding,omitempty"`
    Score     float32        `json:"score,omitempty"`
    Source    string         `json:"source,omitempty"`
    CreatedAt time.Time      `json:"created_at,omitempty"`
}
```

### 文档加载器

| 函数 | 说明 |
|-----|------|
| `NewTextLoader(path string)` | 文本文件加载器 |
| `NewMarkdownLoader(path string)` | Markdown 文件加载器 |
| `NewDirectoryLoader(dir string, patterns ...string)` | 目录批量加载器 |
| `NewURLLoader(url string)` | URL 加载器 |
| `NewStringLoader(content string)` | 字符串加载器 |
| `NewCSVLoader(path string)` | CSV 文件加载器 |
| `NewExcelLoader(path string)` | Excel (.xlsx) 文件加载器 |
| `NewPPTXLoader(path string)` | PowerPoint (.pptx) 文件加载器 |
| `NewDOCXLoader(path string)` | Word (.docx) 文件加载器 |
| `NewPDFLoader(path string)` | PDF 文件加载器 |
| `NewOCRLoader(path string, opts ...OCROption)` | OCR 图片文字提取加载器 (支持 VisionLLM) |

### 文档分割器

| 函数 | 说明 |
|-----|------|
| `NewCharacterSplitter(chunkSize, overlap int)` | 字符分割器 |
| `NewRecursiveSplitter(chunkSize, overlap int)` | 递归分割器 |
| `NewMarkdownSplitter()` | Markdown 分割器 |
| `NewSentenceSplitter()` | 句子分割器 |
| `NewTokenSplitter(chunkSize int, opts ...TokenOption)` | Token 分割器 (按 Token 计数分割) |
| `NewCodeSplitter(language string)` | 代码分割器 (按语言语法分割) |
| `NewSemanticSplitter(embedder Embedder)` | 语义分割器 (按语义相似度分割) |

### 检索器

| 函数 | 说明 |
|-----|------|
| `NewVectorRetriever(store, embedder)` | 向量检索器 |
| `NewKeywordRetriever(docs []Document)` | 关键词检索器 |
| `NewHybridRetriever(vector, keyword, weight)` | 混合检索器 |
| `NewMultiRetriever(retrievers ...Retriever)` | 多源检索器 |
| `NewHyDERetriever(llm, embedder, store, opts ...)` | HyDE 假设文档检索器 (LLM 生成假设文档后检索) |
| `NewAdaptiveRetriever(retrievers ...Retriever)` | 自适应检索器 (根据查询特征自动选择策略) |
| `NewParentDocRetriever(store, splitter)` | 父文档检索器 (检索子块后返回完整父文档) |

### 向量生成器

| 函数 | 说明 |
|-----|------|
| `NewOpenAIEmbedder()` | OpenAI Embedder |
| `NewCachedEmbedder(base Embedder)` | 带缓存的 Embedder |
| `NewMockEmbedder(dim int)` | 模拟 Embedder（测试用） |

### 向量存储

#### NewMemoryVectorStore

创建内存向量存储。

```go
var NewMemoryVectorStore = vector.NewMemoryStore
```

#### NewQdrantStore

创建 Qdrant 向量存储。

```go
var NewQdrantStore = qdrant.New
```

**配置：**
```go
type QdrantConfig struct {
    Host             string // 主机地址
    Port             int    // 端口
    Collection       string // 集合名称
    Dimension        int    // 向量维度
    APIKey           string // API Key（可选）
    HTTPS            bool   // 是否使用 HTTPS
    Timeout          time.Duration
    Distance         DistanceType // 距离度量
    OnDisk           bool         // 是否存储在磁盘
    CreateCollection bool         // 是否自动创建集合
}
```

**选项式创建：**
```go
store, err := hexagon.NewQdrantStoreWithOptions(
    hexagon.QdrantWithHost("localhost"),
    hexagon.QdrantWithPort(6333),
    hexagon.QdrantWithCollection("docs"),
    hexagon.QdrantWithDimension(1536),
    hexagon.QdrantWithCreateCollection(true),
)
```

#### 更多向量存储

| 函数 | 说明 |
|-----|------|
| `faiss.NewStore(config)` | FAISS 向量存储 (高性能本地检索) |
| `pgvector.NewStore(config)` | PgVector 向量存储 (PostgreSQL 扩展) |
| `redis.NewStore(config)` | Redis 向量存储 (Redis Stack) |
| `milvus.NewStore(config)` | Milvus 向量存储 |
| `chroma.NewStore(config)` | Chroma 向量存储 |
| `pinecone.NewStore(config)` | Pinecone 向量存储 |
| `weaviate.NewStore(config)` | Weaviate 向量存储 |

---

## Graph 编排

### NewGraph

创建图编排构建器。

```go
func NewGraph[S graph.State](name string) *graph.GraphBuilder[S]
```

**构建器方法：**

| 方法 | 说明 |
|-----|------|
| `AddNode(name string, handler NodeHandler[S])` | 添加节点 |
| `AddEdge(from, to string)` | 添加边 |
| `AddConditionalEdge(from string, router RouterFunc[S], edges map[string]string)` | 添加条件边 |
| `SetEntryPoint(node string)` | 设置入口点 |
| `SetFinishPoint(nodes ...string)` | 设置结束点 |
| `WithCheckpointer(saver CheckpointSaver)` | 设置检查点保存器 |
| `Build() (*Graph[S], error)` | 构建图 |
| `MustBuild() *Graph[S]` | 构建图（失败则 panic） |

**常量：**
```go
const START = graph.START // 起始节点
const END = graph.END     // 结束节点
```

### State 接口

```go
type State interface {
    Clone() State
}

// 通用 map 状态
type MapState map[string]any
```

### NodeHandler

```go
type NodeHandler[S State] func(ctx context.Context, state S) (S, error)
```

### RouterFunc

```go
type RouterFunc[S State] func(state S) string
```

### 运行选项

| 选项 | 说明 |
|-----|------|
| `WithThread(config *ThreadConfig)` | 设置线程配置 |
| `WithInterrupt(nodes ...string)` | 设置中断节点 |
| `WithDebug(debug bool)` | 设置调试模式 |

### StreamEvent

```go
type StreamEvent[S State] struct {
    Type     EventType
    NodeName string
    State    S
    Error    error
    Metadata map[string]any
}

const (
    EventTypeNodeStart // 节点开始
    EventTypeNodeEnd   // 节点结束
    EventTypeError     // 错误
    EventTypeEnd       // 图执行结束
)
```

**示例：**
```go
g, _ := hexagon.NewGraph[MyState]("my-graph").
    AddNode("step1", handler1).
    AddNode("step2", handler2).
    AddEdge(hexagon.START, "step1").
    AddEdge("step1", "step2").
    AddEdge("step2", hexagon.END).
    Build()

result, _ := g.Run(ctx, initialState)
```

### NewChain

创建链式编排构建器。

```go
func NewChain[I, O any](name string) *chain.ChainBuilder[I, O]
```

**方法：**
```go
builder.Pipe(component).Build()
```

---

## Team 多 Agent

### NewTeam

创建 Agent 团队。

```go
var NewTeam = agent.NewTeam
```

**选项：**

| 选项 | 说明 |
|-----|------|
| `WithAgents(agents ...Agent)` | 设置团队成员 |
| `WithMode(mode TeamMode)` | 设置工作模式 |
| `WithManager(manager Agent)` | 设置管理者（Hierarchical 模式） |
| `WithMaxRounds(rounds int)` | 设置最大轮次 |
| `WithTeamDescription(desc string)` | 设置团队描述 |

### TeamMode

```go
const (
    TeamModeSequential    // 顺序执行
    TeamModeHierarchical  // 层级模式
    TeamModeCollaborative // 协作模式
    TeamModeRoundRobin    // 轮询模式
)
```

### TransferTo

创建 Agent 交接工具。

```go
var TransferTo = agent.TransferTo
```

**示例：**
```go
tools := []hexagon.Tool{
    hexagon.TransferTo(salesAgent),
    hexagon.TransferTo(supportAgent),
}
```

---

## 安全防护

### Guard

#### NewPromptInjectionGuard

创建 Prompt 注入检测守卫。

```go
var NewPromptInjectionGuard = guard.NewPromptInjectionGuard
```

#### NewPIIGuard

创建 PII 检测守卫。

```go
var NewPIIGuard = guard.NewPIIGuard
```

#### NewGuardChain

创建守卫链。

```go
var NewGuardChain = guard.NewGuardChain
```

**链模式：**
```go
const (
    ChainModeAll   // 所有守卫都必须通过
    ChainModeAny   // 任一守卫通过即可
    ChainModeFirst // 第一个失败就停止
)
```

### CheckResult

```go
type CheckResult struct {
    Passed   bool      // 是否通过
    Score    float64   // 风险分数 (0-1)
    Category string    // 风险类别
    Reason   string    // 原因
    Findings []Finding // 发现的问题
}
```

### CostController

#### NewCostController

创建成本控制器。

```go
var NewCostController = cost.NewController
```

**选项：**

| 选项 | 说明 |
|-----|------|
| `WithBudget(amount float64)` | 设置预算 |
| `WithMaxTokensPerRequest(n int)` | 单次请求 token 限制 |
| `WithMaxTokensPerSession(n int)` | 会话 token 限制 |
| `WithMaxTokensTotal(n int)` | 总 token 限制 |
| `WithRequestsPerMinute(n int)` | RPM 限制 |

---

## 可观测性

### Tracer

#### NewTracer

创建内存追踪器。

```go
var NewTracer = tracer.NewMemoryTracer
```

#### NewNoopTracer

创建空追踪器（禁用追踪）。

```go
var NewNoopTracer = tracer.NewNoopTracer
```

#### ContextWithTracer

将追踪器添加到 context。

```go
var ContextWithTracer = tracer.ContextWithTracer
```

#### StartSpan

开始新的追踪 Span。

```go
var StartSpan = tracer.StartSpan
```

**Span 方法：**
```go
span.SetAttribute(key string, value any)
span.RecordError(err error)
span.End()
```

### Metrics

#### NewMetrics

创建内存指标收集器。

```go
var NewMetrics = metrics.NewMemoryMetrics
```

**方法：**
```go
// 计数器
m.Counter(name string, labels ...string).Inc()
m.Counter(name string, labels ...string).Add(delta float64)

// 直方图
m.Histogram(name string, labels ...string).Observe(value float64)

// 仪表盘
m.Gauge(name string, labels ...string).Set(value float64)
m.Gauge(name string, labels ...string).Inc()
m.Gauge(name string, labels ...string).Dec()
```

---

## 类型定义

### 重新导出类型

```go
// 核心类型
type Input = agent.Input
type Output = agent.Output
type Tool = tool.Tool
type Memory = memory.Memory
type Message = llm.Message
type Schema = core.Schema
type Component[I, O any] = core.Component[I, O]
type Stream[T any] = core.Stream[T]

// Agent 类型
type Agent = agent.Agent
type Role = agent.Role
type Team = agent.Team
type StateManager = agent.StateManager

// 图编排类型
type Graph[S graph.State] = graph.Graph[S]
type Chain[I, O any] = chain.Chain[I, O]
type State = graph.State
type MapState = graph.MapState

// 可观测性类型
type Tracer = tracer.Tracer
type Span = tracer.Span
type Metrics = metrics.Metrics

// 安全类型
type Guard = guard.Guard
type CostController = cost.Controller

// RAG 类型
type Document = rag.Document
type Loader = rag.Loader
type Splitter = rag.Splitter
type Indexer = rag.Indexer
type Retriever = rag.Retriever
type Embedder = rag.Embedder
type VectorStore = rag.VectorStore
type RAGEngine = rag.Engine
```

---

## 完整示例

### 带工具的 ReAct Agent

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

type SearchInput struct {
    Query string `json:"query" desc:"搜索关键词" required:"true"`
}

func main() {
    ctx := context.Background()

    searchTool := hexagon.NewTool("search", "搜索信息",
        func(ctx context.Context, input SearchInput) (string, error) {
            return fmt.Sprintf("搜索结果: %s 相关信息...", input.Query), nil
        },
    )

    agent := hexagon.QuickStart(
        hexagon.WithTools(searchTool),
        hexagon.WithSystemPrompt("你是一个助手，可以搜索信息回答问题"),
    )

    output, _ := agent.Run(ctx, hexagon.Input{
        Query: "Go 语言的最新版本是什么?",
    })

    fmt.Println(output.Content)
}
```

### RAG 问答系统

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

func main() {
    ctx := context.Background()

    // 设置 RAG
    store := hexagon.NewMemoryVectorStore()
    embedder := hexagon.NewOpenAIEmbedder()
    engine := hexagon.NewRAGEngine(
        hexagon.WithRAGStore(store),
        hexagon.WithRAGEmbedder(embedder),
    )

    // 索引文档
    engine.Index(ctx, []hexagon.Document{
        {ID: "1", Content: "Hexagon 是一个 Go AI Agent 框架"},
        {ID: "2", Content: "Hexagon 支持 RAG、图编排、多 Agent"},
    })

    // 检索
    docs, _ := engine.Retrieve(ctx, "Hexagon 有什么功能",
        hexagon.WithTopK(2),
    )

    for _, doc := range docs {
        fmt.Printf("[%.2f] %s\n", doc.Score, doc.Content)
    }
}
```
