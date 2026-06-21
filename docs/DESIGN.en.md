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

**Hexagon** takes its name from the Chinese internet phrase "hexagonal warrior" (六边形战士), symbolizing balanced strength with no weak points.

We focus on six core dimensions — **ease of use, performance, extensibility, task orchestration, observability, and security** — striving for balanced excellence across all capability modules, building the preferred enterprise-grade AI Agent development foundation for Go developers.

### Core Features

* ⚡ **High Performance** | Native Go concurrency, supporting 100k+ active Agents
* 🧩 **Ease of Use** | Declarative API design, build basic prototypes in 3 lines of code
* 🛡️ **Security** | Enterprise-grade sandbox isolation with comprehensive permission control
* 🔧 **Extensibility** | Plugin-based architecture supporting seamless integration of custom components
* 🛠️ **Orchestration** | Powerful graph orchestration engine for complex multi-level task pipelines
* 🔍 **Observability** | Deep OpenTelemetry integration for full end-to-end transparent tracing

---

## Design Philosophy

### Core Principles

```
"Simple things should be simple; complex things should be possible."
```

Hexagon follows five design principles:

1. **Progressive Complexity**: 3 lines to get started, declarative config for intermediate use, graph orchestration for experts
2. **Convention over Configuration**: Sensible defaults, zero-config operation, fully customizable when needed
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
| High performance | Zero-allocation stream processing, object pool optimization |
| Embeddable | Easily embedded into other Go applications |

---

## Core Goals

| Goal | Quantified Target |
|------|-------------------|
| Minimal onboarding | Learning curve < 1 hour |
| Type safety | 0 runtime type errors |
| High performance | 100k+ concurrent Agents |
| Observability | 100% coverage |
| Production-ready | 99.99% availability |

---

## Ecosystem

Hexagon is a complete AI Agent development ecosystem consisting of multiple repositories:

| Repository | Description |
|------------|-------------|
| **hexagon** | AI Agent framework core (orchestration, RAG, Graph, Hooks) |
| **ai-core** | AI capabilities library (LLM/Tool/Memory/Schema) |
| **toolkit** | Go general-purpose utility library (lang/crypto/net/cache/util) |
| **hexagon-ui** | Dev UI frontend (Vue 3 + TypeScript) |

### Ecosystem Dependency Graph

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
          │ (LLM/Tool/  │    │  (Utilities)│    │  (Dev UI)   │
          │   Memory)   │    │             │    │             │
          └──────┬──────┘    └─────────────┘    └─────────────┘
                 │
                 ▼
          ┌─────────────┐
          │   toolkit   │
          └─────────────┘
```

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
- LLM Provider abstraction and implementations
- RAG retrieval-augmented generation
- Tool system and memory system

**Infrastructure Layer**
- Observability (tracing, logging, metrics)
- Security protection (injection detection, PII, RBAC)
- Configuration management, caching, plugin system

---

## Core Interfaces

### Component Interface

Unified interface for all components (Agent, Tool, Chain, Graph):

```go
// Component is the unified interface for all components
type Component[I, O any] interface {
    // Name returns the component name
    Name() string

    // Description returns the component description
    Description() string

    // Run executes the component (non-streaming)
    Run(ctx context.Context, input I) (O, error)

    // Stream executes the component (streaming)
    Stream(ctx context.Context, input I) (Stream[O], error)

    // Batch executes the component in batch
    Batch(ctx context.Context, inputs []I) ([]O, error)

    // InputSchema returns the Schema for input parameters
    InputSchema() *Schema

    // OutputSchema returns the Schema for output parameters
    OutputSchema() *Schema
}
```

**Design highlights:**
- Generic support with compile-time type checking
- Unified execution model (synchronous/streaming/batch)
- Schema introspection capability for dynamic composition
- All components can be freely nested and composed

### Stream Interface

Streaming data processing capability:

```go
// Stream is the generic stream interface
type Stream[T any] interface {
    // Next reads the next element
    Next(ctx context.Context) (T, bool)

    // Err returns any error that occurred during stream processing
    Err() error

    // Close closes the stream and releases resources
    Close() error

    // Collect collects all elements into a slice
    Collect(ctx context.Context) ([]T, error)

    // ForEach executes an operation on each element
    ForEach(ctx context.Context, fn func(T) error) error
}
```

---

## Agent System

### Agent Interface

```go
// Agent is the core interface for an AI Agent
type Agent interface {
    Component[Input, Output]

    // ID returns the Agent's unique identifier
    ID() string

    // Role returns the Agent's role definition
    Role() Role

    // Tools returns the list of tools available to the Agent
    Tools() []tool.Tool

    // Memory returns the Agent's memory system
    Memory() memory.Memory

    // LLM returns the LLM Provider used by the Agent
    LLM() llm.Provider
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
    Content   string           `json:"content"`            // Final response
    ToolCalls []ToolCallRecord `json:"tool_calls,omitempty"` // Tool call records
    Usage     llm.Usage        `json:"usage,omitempty"`    // Token usage statistics
    Metadata  map[string]any   `json:"metadata,omitempty"` // Additional metadata
}
```

### Role System

```go
// Role defines an Agent's role
type Role struct {
    Name        string   // Role name
    Goal        string   // Role objective
    Backstory   string   // Background story
    Constraints []string // Constraints
    Capabilities []string // Capability list
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
}
```

### Four-Layer State Management

```go
// StateManager manages Agent state
type StateManager interface {
    // Turn is the turn-level state (single conversation turn)
    Turn() State

    // Session is the session-level state (multi-turn conversation)
    Session() State

    // Agent is the Agent's persistent state
    Agent() State

    // Global is the globally shared state
    Global() State
}
```

---

## RAG System

### Core Components

```go
// Document represents a document
type Document struct {
    ID        string         // Unique identifier
    Content   string         // Document content
    Metadata  map[string]any // Metadata
    Embedding []float32      // Vector embedding
    Score     float32        // Retrieval score
}

// Loader is the document loader interface
type Loader interface {
    Load(ctx context.Context) ([]Document, error)
}

// Splitter is the document splitter interface
type Splitter interface {
    Split(ctx context.Context, docs []Document) ([]Document, error)
}

// Indexer is the indexer interface
type Indexer interface {
    Index(ctx context.Context, docs []Document) error
    Delete(ctx context.Context, ids []string) error
}

// Retriever is the retriever interface
type Retriever interface {
    Retrieve(ctx context.Context, query string, opts ...RetrieveOption) ([]Document, error)
}

// Embedder is the vector embedder interface
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dimension() int
}

// VectorStore is the vector storage interface
type VectorStore interface {
    Add(ctx context.Context, docs []Document) error
    Search(ctx context.Context, embedding []float32, topK int, filter map[string]any) ([]Document, error)
}
```

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
| VectorStore | MemoryStore, QdrantStore, FAISSStore, PgVectorStore, RedisStore, MilvusStore, ChromaStore, PineconeStore, WeaviateStore |

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

```go
// Enable checkpoint saving
graph.WithCheckpointer(checkpointer).Build()

// Resume from a checkpoint
checkpoint, _ := checkpointer.Get(ctx, threadID)
graph.Run(ctx, checkpoint.State, graph.WithThread(checkpoint.Config))
```

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
guard := hexagon.NewPromptInjectionGuard()

// PII detection
guard := hexagon.NewPIIGuard()

// Guard chain
chain := hexagon.NewGuardChain(hexagon.ChainModeAll,
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
controller := hexagon.NewCostController(
    hexagon.WithBudget(10.0),            // $10 budget
    hexagon.WithMaxTokensTotal(100000),  // Total token limit
    hexagon.WithRequestsPerMinute(60),   // RPM limit
)
```

---

## Observability

### Tracer

```go
// Create a tracer
tracer := hexagon.NewTracer()
ctx := hexagon.ContextWithTracer(ctx, tracer)

// Start a Span
span := hexagon.StartSpan(ctx, "operation_name")
defer span.End()

span.SetAttribute("key", "value")
span.RecordError(err)
```

### Metrics

```go
// Create a metrics collector
m := hexagon.NewMetrics()

// Counter
m.Counter("agent_calls", "agent", "react").Inc()

// Histogram
m.Histogram("latency_ms", "operation", "chat").Observe(123.5)

// Gauge
m.Gauge("active_agents").Set(5)
```

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
│   ├── runnable.go               # Component/Runnable unified interface + Stream[T] + Schema alias
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
├── checkpoint/                   # Unified Checkpointer persistence
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
│   ├── otel/                     # OpenTelemetry + Langfuse export
│   ├── prometheus/               # Prometheus integration
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
├── store/                        # Storage
│   └── vector/                   # Vector stores
│       ├── faiss/                # FAISS
│       ├── pgvector/             # PgVector
│       ├── redis/                # Redis
│       ├── milvus/               # Milvus
│       ├── chroma/               # Chroma
│       ├── pinecone/             # Pinecone
│       └── weaviate/             # Weaviate
│
│   # Note: the Qdrant implementation lives in ai-core (ai-core/store/vector/qdrant)
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
├── deploy/                       # Deployment configs (Docker Compose/Helm/CI)
├── docs/                         # Public documentation
├── internal/                     # Internal implementation
│
├── hexagon.go                    # Main package entry (version: v0.5.0)
├── deprecated.go                 # Transitional re-exports (removed in next major)
├── go.mod
├── Makefile
└── README.md
```

---

## Dependencies

```
hexagon (main framework)
├── ai-core       ← AI capabilities (LLM/Tool/Memory/Schema)
└── toolkit       ← General utilities (lang/crypto/net/cache/util)
```

### ai-core — AI Capabilities Library

`github.com/hexagon-codes/ai-core`

Provides core abstractions for LLM, Tool, Memory, and Schema:

- `llm/` - LLM Provider interfaces + implementations (OpenAI, DeepSeek, Anthropic, Gemini, Qwen, Ark, Ollama)
- `tool/` - Tool system with function-based definition support
- `memory/` - Memory system with vector store support
- `schema/` - Automatic JSON Schema generation
- `streamx/` - Streaming response processing
- `template/` - Prompt template engine

### toolkit — Go General-Purpose Utility Library

`github.com/hexagon-codes/toolkit`

A production-grade Go utility library providing language enhancements, cryptography, networking, caching, goroutine pools, and other foundational capabilities:

- `lang/` - Language enhancements (conv, stringx, slicex, mapx, timex, contextx, errorx, syncx)
- `crypto/` - Cryptography (aes, rsa, sign)
- `net/` - Networking (httpx, sse, ip)
- `cache/` - Caching (local, redis, multi)
- `util/` - Utilities (retry, rate, idgen, logger, validator, poolx goroutine pool)
- `collection/` - Data structures (set, list, queue, stack)

---

## LLM Provider Support

| Provider | Status |
|----------|:------:|
| OpenAI (GPT-4, GPT-4o, o1, o3) | ✅ Complete |
| DeepSeek | ✅ Complete |
| Anthropic (Claude) | ✅ Complete |
| Google Gemini | ✅ Complete |
| Qwen (通义千问) | ✅ Complete |
| Ark (豆包) | ✅ Complete |
| Ollama (local models) | ✅ Complete |
