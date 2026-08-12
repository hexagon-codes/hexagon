<div align="right">Language: <a href="DESIGN.md">中文</a> | English</div>

# Hexagon Architecture Design Document

<div align="center">

**The All-Around AI Agent Framework for the Go Ecosystem**

</div>

## Table of Contents

- [Project Overview](#project-overview)
- [Design Philosophy](#design-philosophy)
- [Core Goals](#core-goals)
- [Ecosystem](#ecosystem)
- [Layered Architecture](#layered-architecture)
- [Core Interfaces](#core-interfaces)
- [Agent System](#agent-system)
- [RAG System](#rag-system)
- [Graph Orchestration Engine](#graph-orchestration-engine)
- [Security Guards](#security-guards)
- [Observability](#observability)

---

## Project Overview

**Hexagon** takes its name from the Chinese internet phrase "hexagonal warrior" (六边形战士), reflecting a focus on balanced development across framework capabilities.

We focus on six dimensions — **ease of use, performance, extensibility, task orchestration, observability, and security** — and provide Go developers with a composable AI Agent framework. The capabilities below describe the current code; performance, capacity, and availability limits must be established by benchmarks and load tests in the target deployment environment.

### Core Features

* ⚡ **Concurrent Execution** | Organizes concurrent work with goroutines, streaming, batch execution, and goroutine pools
* 🧩 **Ease of Use** | Provides a top-level QuickStart API while allowing direct composition through sub-packages
* 🛡️ **Security** | Provides Guard, PII, RBAC, credential, sandbox, and SSRF protection components
* 🔧 **Extensibility** | Extends components through interfaces, option functions, Hooks, and plugins
* 🛠️ **Orchestration** | Provides Graph, Chain, Workflow, Planner, and multi-Agent orchestration
* 🔍 **Observability** | Provides tracing, metrics, logging, and OpenTelemetry and Prometheus adapters

---

## Design Philosophy

### Core Principles

```
"Simple things should be simple; complex things should be possible."
```

Hexagon follows five design principles:

1. **Progressive Complexity**: Top-level convenience APIs, declarative configuration, and graph orchestration expose capabilities in layers
2. **Convention over Configuration**: Sensible defaults for common scenarios, with customization where needed
3. **Composition over Inheritance**: Small, focused components that compose flexibly via interfaces
4. **Explicit over Implicit**: Type-safe, compile-time checked, with clear data flow
5. **Production-First**: Built-in observability, graceful degradation, operations-friendly

### Go Language Advantages

Why Go was chosen as the implementation language:

| Advantage | Description |
|-----------|-------------|
| Native concurrency | goroutine + channel for efficient parallel Agent execution |
| Single-binary deployment | No runtime dependencies, container-friendly, simple operations |
| Compile-time type checking | Generic support reduces runtime errors |
| High performance | Native concurrency, streaming, and object pools |
| Embeddable | Easily embedded into other Go applications |

---

## Core Goals

| Goal | Design Direction |
|------|------------------|
| Progressive onboarding | Top-level convenience APIs coexist with composable sub-packages |
| Type safety | Generic `Runnable`, explicit interfaces, and compile-time checks |
| Concurrency and streaming | Native Go concurrency, batching, backpressure, and streaming execution |
| Observability | Tracing, metrics, logging, and standard-protocol export |
| Runtime reliability | Context cancellation, retries, fallback, security checks, and checkpoint capabilities |

---

## Ecosystem

Hexagon is a complete AI Agent development ecosystem consisting of multiple repositories:

| Repository | Description |
|------------|-------------|
| **hexagon** | AI Agent framework core (orchestration, RAG, Graph, Hooks) |
| **ai-core** | AI capabilities library (LLM/Tool/Memory/Schema/Stream/Vector Store) |
| **toolkit** | Go general-purpose utility library (lang/crypto/net/cache/util/infra) |
| **hexagon-ui** | Independent, optional Dev UI frontend; not a Go module dependency of Hexagon |

### Go Module Dependency Graph

```
hexagon
├── ai-core v0.2.7
│   └── toolkit v0.3.4
└── toolkit v0.3.4
```

`hexagon-ui` is an independent optional application in the ecosystem and is not part of the Go module dependency graph above.

---

## Layered Architecture

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
│  │                         toolkit (Utility Library)                    │   │
│  │   lang │ crypto │ net │ cache │ util │ collection │ infra           │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Layer Responsibilities

**Application Layer**
- Complete applications facing end users
- Combines lower-layer capabilities to build specific business scenarios
- Examples: chatbots, RAG Q&A systems, automated workflows

**Orchestration Layer**
- Component orchestration and flow control
- Graph orchestration, workflow engine, state management
- Supports conditional branching, parallel execution, and checkpoint recovery

**Agent Core Layer**
- Agent lifecycle management
- Role system, team collaboration, message passing
- State management (four layers: Turn/Session/Agent/Global)

**Capability Layer**
- ai-core provides LLM Provider, Tool, Memory, Schema, Stream, and the shared vector-store contract
- Hexagon provides RAG flows, retrieval/indexing orchestration, and Agent capability composition
- `*` Memory and Qdrant vector stores come from ai-core; other backend adapters live in Hexagon

**Infrastructure Layer**
- Hexagon provides Agent/LLM/Tool/Retriever-aware tracing, metrics, logging adapters, and Hooks
- toolkit provides general logging, OpenTelemetry, Prometheus, and lower-level observe implementations
- Security protection (injection detection, PII, RBAC)
- Configuration management, caching, plugin system

---

## Core Interfaces

### Runnable and Component

`core.Runnable` is the six-mode interface for executable components; `core.Component` embeds `Runnable` to retain the old name. Domain objects such as Tool and Graph still use their own sub-package interfaces and are not forced behind one artificial interface.

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

**Design highlights:**
- Generic input/output and Schema introspection provide compile-time constraints
- Covers six modes across ordinary input/output, batching, and stream input/output
- `BaseRunnable` can derive default behavior from the core `Invoke` implementation
- Execution options are passed explicitly through `core.Option`

### StreamReader

The stream type belongs to ai-core's `streamx` package. Hexagon's `core.StreamReader` is an alias to it, while the old `core.Stream` alias is deprecated. `io.EOF` marks the end of reading.

```go
type StreamReader[T any] = streamx.StreamReader[T]

item, err := reader.Recv()
items, err := reader.Collect(ctx)
err = reader.ForEach(ctx, func(item T) error { return nil })
err = reader.Close()
```

`core.Schema` is likewise an alias to ai-core's `llm.Schema`: ai-core owns the underlying data contracts, while Hexagon owns execution orchestration.

---

## Agent System

### Agent Interface

```go
type Agent interface {
    core.Runnable[Input, Output]

    ID() string
    Role() Role
    Tools() []tool.Tool
    Memory() memory.Memory
    LLM() llm.Provider

    // 为兼容旧调用方保留 Run；新代码使用 Invoke。
    Run(ctx context.Context, input Input) (Output, error)
}
```

### Input and Output

```go
// Input is the Agent's input
type Input struct {
    Query   string         `json:"query"`           // User query
    Context map[string]any `json:"context,omitempty"` // Additional context
}

// Output is the Agent's output
type Output struct {
    Content   string           `json:"content"`              // Final response
    ToolCalls []ToolCallRecord `json:"tool_calls,omitempty"` // Tool call records
    Blocks    template.Blocks  `json:"blocks,omitempty"`     // Ordered content blocks
    Usage     llm.Usage        `json:"usage,omitempty"`      // Token usage statistics
    StopReason runtime.StopReason `json:"stop_reason,omitempty"`
    Metadata  map[string]any   `json:"metadata,omitempty"`   // Additional metadata
}
```

### Role System

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

### Team Modes

```go
// TeamMode represents the team work mode
const (
    TeamModeSequential    // Sequential: Agents execute one after another
    TeamModeHierarchical  // Hierarchical: Manager coordinates and delegates
    TeamModeCollaborative // Collaborative: Parallel work with message passing
    TeamModeRoundRobin    // Round-robin: Agents take turns until completion
)
```

### Agent Handoff

```go
// TransferTo creates a handoff tool
func TransferTo(target Agent) tool.Tool

// SwarmRunner automatically handles handoffs between Agents
type SwarmRunner struct {
    InitialAgent Agent
    MaxHandoffs  int
    GlobalState  GlobalState
    Verbose      bool
}
```

### Four-Layer State Management

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

## RAG System

### Core Components

```go
// Document、Loader、Splitter、Indexer 与 Retriever 归 Hexagon 的 rag 包所有。
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

// Engine 使用的轻量 Embedder 合同。
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dimension() int
}

// 共享向量存储合同属于 ai-core/store/vector。
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

`rag.Engine`, `rag/indexer`, and `rag/retriever` convert `rag.Document` to ai-core's `vector.Document` at flow boundaries. ai-core owns the shared `vector.Store`, `vector.Embedder`, in-memory store, and Qdrant adapter; Hexagon owns RAG orchestration and the other vector-backend adapters.

### RAG Pipeline

```go
// Pipeline is the RAG processing pipeline
pipeline := rag.NewPipeline(loader, splitter, indexer, retriever)

// Ingest documents
pipeline.Ingest(ctx)

// Query
docs, _ := pipeline.Query(ctx, "query", rag.WithTopK(5))
```

### Supported Components

| Component Type | Implementations |
|----------------|-----------------|
| Loader | TextLoader, MarkdownLoader, DirectoryLoader, URLLoader, CSVLoader, ExcelLoader, PPTXLoader, DOCXLoader, PDFLoader, OCRLoader |
| Splitter | CharacterSplitter, RecursiveSplitter, MarkdownSplitter, SentenceSplitter, TokenSplitter, CodeSplitter, SemanticSplitter |
| Retriever | VectorRetriever, KeywordRetriever, HybridRetriever, MultiRetriever, HyDERetriever, AdaptiveRetriever, ParentDocRetriever |
| Indexer | VectorIndexer, ConcurrentIndexer, IncrementalIndexer |
| Embedder | OpenAIEmbedder, CachedEmbedder, MockEmbedder |
| Vector Store (ai-core) | MemoryStore, Qdrant Store |
| Vector Store (Hexagon adapters) | FAISS, PgVector, Redis, Milvus, Chroma, Pinecone, Weaviate |

Starting with ai-core v0.2.7, new Qdrant collections use SHA-256-derived UUIDv8 point IDs by default. To migrate an old collection, import `github.com/hexagon-codes/ai-core/store/vector/qdrant` directly, explicitly select `PointIDLegacyHash31` to read the old data, and rebuild it into a new UUIDv8 collection. Do not mix the two ID strategies in one collection.

---

## Graph Orchestration Engine

### Building a Graph

```go
// Create a graph
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

// Execute
result, _ := graph.Run(ctx, initialState)
```

### State Interface

```go
// State is the graph state interface
type State interface {
    // Clone clones the state
    Clone() State
}

// MapState is a general-purpose map state
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
// NodeHandler is the node processing function
type NodeHandler[S State] func(ctx context.Context, state S) (S, error)

// RouterFunc is the routing function (returns a label)
type RouterFunc[S State] func(state S) string
```

### Streaming Execution

```go
// Stream execution, returns an event for each node
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

### Checkpoints

The repository currently has two checkpoint contracts for different purposes; they are not interchangeable:

- `checkpoint.Checkpointer` is the framework-wide byte-payload persistence port. It provides Memory/File backends and is consumed by Agent durable execution, Interrupt Handler, and Graph StateMachine, among others.
- `orchestration/graph.CheckpointSaver` stores graph-specific snapshots. Use `EnhancedCheckpointSaver` with `CheckpointRunner` when graph recovery, history, and branching are required.

```go
// 框架级类型化值持久化
cp := checkpoint.NewMemory()
err := checkpoint.PutValue(ctx, cp, checkpoint.Checkpoint{
    Namespace: "run-1",
    ID:        "step-1",
}, state)
restored, _, ok, err := checkpoint.GetValue[MyState](ctx, cp, "run-1", "step-1")

// Graph 专用执行与恢复
saver := graph.NewMemoryEnhancedCheckpointSaver()
runner := graph.NewCheckpointRunner(compiledGraph, saver, nil)
result, err := runner.Run(ctx, "thread-1", initialState)
result, err = runner.ResumeFromLatest(ctx, "thread-1")
```

Calling `Graph.Run` directly does not automatically perform this recovery flow merely because its builder holds a saver. Do not use the old pseudo-APIs `checkpointer.Get(ctx, threadID)` or `checkpoint.Config`.

---

## Security Guards

### Guard Interface

```go
// Guard is the security guard interface
type Guard interface {
    Name() string
    Check(ctx context.Context, input string) (*CheckResult, error)
    Enabled() bool
}

// CheckResult is the guard check result
type CheckResult struct {
    Passed   bool      // Whether the check passed
    Score    float64   // Risk score (0-1)
    Category string    // Risk category
    Reason   string    // Reason
    Findings []Finding // Issues found
}
```

### Built-in Guards

```go
// Prompt injection detection
injectionGuard := guard.NewPromptInjectionGuard()

// PII detection
piiGuard := guard.NewPIIGuard()

// Guard chain
chain := guard.NewGuardChain(guard.ChainModeAll,
    injectionGuard,
    piiGuard,
)
```

### Guard Chain Modes

```go
const (
    ChainModeAll   // All guards must pass
    ChainModeAny   // Any single guard passing is sufficient
    ChainModeFirst // Stop at the first failure
)
```

### Cost Control

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

`middleware.Budget` fails closed before an LLM call within one run; `middleware.CostControl` writes post-call usage into the shared Controller, completing the cross-run ledger and budget enforcement. Direct LLM calls that require RPM preflight must also call `controller.CheckRequest` before the request.

---

## Observability

### Tracer

```go
// 使用 Hexagon 的内存追踪实现
t := tracer.NewMemoryTracer()
ctx = tracer.ContextWithTracer(ctx, t)

// StartSpan 返回派生 context 与 Span
ctx, span := tracer.StartSpan(ctx, "operation_name")
defer span.End()

span.SetAttribute("key", "value")
span.RecordError(err)
```

### Metrics

```go
// Create a metrics collector
m := metrics.NewMemoryMetrics()

// Counter
m.Counter("agent_calls", "agent", "react").Inc()

// Histogram
m.Histogram("latency_ms", "operation", "chat").Observe(123.5)

// Gauge
m.Gauge("active_agents").Set(5)
```

Observability is layered by responsibility:

- `observe/tracer`, `observe/metrics`, and `observe/logger` provide Hexagon-side interfaces and in-memory implementations.
- `observe/otel` reuses toolkit's general OpenTelemetry/OTLP implementation and adds Agent-, LLM-, Tool-, and Retriever-aware tracing and Hooks.
- `observe/prometheus` reuses toolkit's Exporter, Registry, and metrics adapter and adds Hexagon runtime Hooks.
- `observe/logger` wraps toolkit logger to attach Agent, Session, Trace, and Span context.

Top-level helpers such as `hexagon.NewTracer()` and `hexagon.NewMetrics()` are transitional re-exports. New code should import the corresponding `observe/*` sub-packages directly.

---

## Directory Structure

```
hexagon/
├── agent/                        # Agent core
│   ├── agent.go                  # Agent interface definition
│   ├── react.go                  # ReAct Agent implementation
│   ├── primitives.go             # Agent primitives (Parallel/Sequential/Route)
│   ├── agent_tool.go             # AgentTool (Agent as a tool)
│   ├── supervisor.go             # Supervisor scheduling
│   ├── role.go                   # Role system
│   ├── team.go                   # Team collaboration (4 work modes)
│   ├── handoff.go                # Agent handoff
│   ├── state.go                  # Four-layer state management
│   ├── network.go                # Agent network communication
│   ├── consensus.go              # Consensus mechanism
│   ├── a2a/                      # A2A protocol (Client/Server/Handler/Discovery)
│   ├── artifact/                 # Artifact system
│   ├── semantic/                 # Semantic capabilities
│   └── skill/                    # Skill registry & signing
│
├── core/                         # Core interfaces
│   ├── runnable.go               # Six-mode Runnable + ai-core StreamReader/Schema aliases
│   ├── compose.go                # Declarative composition
│   └── fallback.go               # Resilient fallback
│
├── runtime/                      # Unified execution runtime
│   ├── runner.go                 # Unified Runner
│   ├── durable.go                # DurableExecution (per-tool exactly-once + Resume)
│   ├── middleware/               # Budget/CostControl/Compaction/PermissionMode
│   └── strategy/                 # Execution strategies (ReAct/PlanExecute/Reflection)
│
├── orchestration/                # Orchestration layer
│   ├── graph/                    # Graph orchestration engine
│   │   ├── graph.go              # Graph definition and execution
│   │   ├── node.go               # Node types
│   │   ├── edge.go               # Edge definitions
│   │   ├── state.go              # State management
│   │   ├── checkpoint.go         # Checkpoint saving
│   │   ├── interrupt.go          # Interrupt/resume
│   │   ├── barrier.go            # Synchronization barrier
│   │   ├── cache.go              # Node caching
│   │   ├── command.go            # Command pattern
│   │   ├── distributed.go        # Distributed execution
│   │   ├── functional.go         # Functional API
│   │   ├── stream_mode.go        # Stream mode
│   │   └── visualize.go          # Graph visualization
│   ├── chain/                    # Chain orchestration (compile-time I/O type checks)
│   ├── workflow/                 # Workflow engine
│   └── planner/                  # Planner
│
├── checkpoint/                   # Framework-wide Checkpointer (Memory/File)
├── interrupt/                    # Interrupt & resume
│
├── rag/                          # RAG system
│   ├── rag.go                    # RAG core interfaces
│   ├── loader/                   # Document loaders + Parser layer (Text/MD/CSV/XLSX/PPTX/DOCX/PDF/OCR)
│   ├── splitter/                 # Document splitters (Character/Recursive/MD/Sentence/Token/Code)
│   ├── embedder/                 # Embedders
│   ├── indexer/                  # Indexers
│   ├── retriever/                # Retrievers (Vector/Keyword/Hybrid/HyDE/Adaptive/ParentDoc)
│   ├── reranker/                 # Rerankers
│   ├── synthesizer/              # Response synthesizers (Refine/Compact/Tree)
│   ├── adw/                      # Agentic Document Workflows (extractor/validator)
│   ├── agentic/                  # Agentic RAG
│   ├── corrective/               # Corrective RAG
│   ├── selfrag/                  # Self-RAG
│   └── multimodal/               # Multimodal
│
├── llm/                          # LLM orchestration layer
│   ├── structured/               # Native json_schema structured output
│   ├── batch/                    # Batch calls
│   ├── conversation/             # Conversation management
│   ├── parser/                   # Output parsing
│   └── template/                 # Prompt templates
│
├── memory/store/                 # Multi-Agent memory stores (InMemory/File/Redis/Persistent)
├── mcp/                          # MCP protocol (discovery/auto-reconnect/multi-transport)
│
├── hooks/                        # Hook system
│
├── observe/                      # Observability
│   ├── tracer/                   # Tracing
│   ├── metrics/                  # Metrics
│   ├── logger/                   # Logging
│   ├── devui/                    # Dev UI backend
│   ├── events/                   # Structured events
│   ├── eventstream/              # Event streams
│   ├── trace/                    # slog tracing handler
│   ├── langfuse/                 # Langfuse client
│   ├── otel/                     # toolkit OTel reuse + Hexagon semantic adapter
│   ├── prometheus/               # toolkit Prometheus reuse + Hexagon Hooks
│   └── replay/                   # Record and replay
│
├── security/                     # Security
│   ├── guard/                    # Security guards (injection detection)
│   ├── guardrails/               # Guardrails
│   ├── pii/                      # PII detection
│   ├── rbac/                     # Role-based access control
│   ├── cost/                     # Cost control
│   ├── audit/                    # Audit logging
│   ├── filter/                   # Content filtering
│   ├── tenant/                   # Multi-tenant isolation
│   └── credential/               # Credential management
│
├── tool/                         # Tool system
│   ├── file/                     # File operations
│   ├── python/                   # Python execution
│   ├── shell/                    # Shell execution
│   ├── sandbox/                  # Sandbox execution
│   ├── http/                     # HTTP requests
│   ├── search/                   # Search
│   ├── database/                 # Database
│   └── browser/                  # Browser
│
├── client/                       # Client
│
├── store/                        # Storage adapters
│   └── vector/                   # Backend adapters for ai-core vector.Store
│       ├── faiss/                # FAISS
│       ├── pgvector/             # PgVector
│       ├── redis/                # Redis
│       ├── milvus/               # Milvus
│       ├── chroma/               # Chroma
│       ├── pinecone/             # Pinecone
│       └── weaviate/             # Weaviate
│
│   # Note: Memory/Qdrant live in ai-core/store/vector
│
├── plugin/                       # Plugin system
├── config/                       # Configuration management
├── evaluate/                     # Evaluation system (agenteval/rag/metrics)
│
├── testing/                      # Testing
│   ├── mock/                     # Mock utilities
│   ├── record/                   # Record and replay
│   ├── e2e/                      # End-to-end tests
│   └── integration/              # Integration tests
│
├── bench/                        # Benchmarks
├── examples/                     # Example code (standalone module)
├── deploy/                       # Deployment configs (Docker Compose/Helm)
├── .github/workflows/            # CI and Release workflows
├── docs/                         # Public documentation
├── internal/                     # Internal implementation
│
├── hexagon.go                    # Main entry (version resolved from injection/build info)
├── deprecated.go                 # Transitional re-exports (removed in next major)
├── go.mod
├── Makefile
└── README.md
```

---

## Dependencies

```
hexagon (Go >= 1.25.12)
├── ai-core v0.2.7
│   └── toolkit v0.3.4
└── toolkit v0.3.4
```

### ai-core — AI Capabilities Library

`github.com/hexagon-codes/ai-core` `v0.2.7` (Go >= 1.25.12)

Provides core abstractions for LLM, Tool, Memory, Schema, Stream, and vector storage:

- `llm/` - LLM Provider interfaces + implementations (OpenAI, DeepSeek, Anthropic, Gemini, Qwen, Ark, Ollama)
- `tool/` - Tool system with function-based definition support
- `memory/` - Memory system
- `schema/` - Automatic JSON Schema generation
- `streamx/` - Streaming response processing
- `store/vector/` - `Store`/`Embedder` contracts, MemoryStore, and the Qdrant adapter
- `template/` - Prompt template engine

### toolkit — Go General-Purpose Utility Library

`github.com/hexagon-codes/toolkit` `v0.3.4` (Go >= 1.25.12)

A production-grade Go utility library providing language enhancements, cryptography, networking, caching, goroutine pools, and other foundational capabilities:

- `lang/` - Language enhancements (conv, stringx, slicex, mapx, timex, contextx, errorx, syncx)
- `crypto/` - Cryptography (aes, rsa, sign)
- `net/` - Networking (httpx, sse, ip, ssrf)
- `cache/` - Caching (local, redis, multi)
- `util/` - Utilities (retry, circuit, rate, idgen, hash, logger, validator, poolx goroutine pool)
- `collection/` - Data structures (set, list, queue, stack)
- `infra/observe`, `infra/otel`, `infra/prometheus` - General observability foundation

Hexagon depends on toolkit both directly and transitively through ai-core at the same version. Hexagon implements Agent/RAG/Graph semantics and adapters on top of these shared foundations rather than duplicating ai-core or toolkit ownership.

---

## LLM Provider Support

| Provider | Implementation Status |
|----------|:---------------------:|
| OpenAI | Adapter provided |
| DeepSeek | Adapter provided |
| Anthropic (Claude) | Adapter provided |
| Google Gemini | Adapter provided |
| Qwen (通义千问) | Adapter provided |
| Ark (豆包) | Adapter provided |
| Ollama (local models) | Adapter provided |
