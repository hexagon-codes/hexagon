<div align="right">Language: <a href="STABILITY.md">中文</a> | English</div>

# Hexagon API Stability Policy

This document describes the API stability levels and compatibility guarantees for each module in the Hexagon framework.

## Versioning

Hexagon follows [Semantic Versioning](https://semver.org/):

```
MAJOR.MINOR.PATCH[-PRERELEASE]
```

- **MAJOR**: Incompatible API changes
- **MINOR**: Backward-compatible new features
- **PATCH**: Backward-compatible bug fixes
- **PRERELEASE**: Pre-release identifier (alpha, beta, GA)

The current Hexagon root module requires **Go 1.25.12 or later**. The repository's `examples/` directory is a separate Go module, is outside the root module's release surface, and does not automatically track root-module dependencies.

## Stability Levels

| Level | Description | Compatibility Guarantee |
|:---:|------|-----------|
| **Stable** | Production-ready API | Backward-compatible within a MAJOR version |
| **Beta** | Feature-complete; minor API adjustments possible | Best-effort compatibility within a MINOR version |
| **Alpha** | Experimental; API may change significantly | No compatibility guarantee |
| **Deprecated** | Scheduled for removal in a future version | Retained for at least 1 MINOR version |

## Module Stability

### Stable

The following APIs are backward-compatible within v1.x:

**Top-level API** (`github.com/hexagon-codes/hexagon`)
- `Chat()`, `ChatWithTools()`, `Run()`
- `QuickStart()` and its option functions (`WithProvider`, `WithTools`, `WithSystemPrompt`, `WithMemory`)
- `NewTool()`, `SetDefaultProvider()`
- Exported types (`Input`, `Output`, `Tool`, `Memory`, `Message`, `Agent`, `Provider`)

**Core interfaces** (`github.com/hexagon-codes/hexagon/core`)
- `Runnable[I, O]` and `Component[I, O]` interfaces
- `StreamReader[T]` type alias (backed by `github.com/hexagon-codes/ai-core/streamx`)
- `Schema` type alias (backed by `github.com/hexagon-codes/ai-core/llm`)

**Agent** (`github.com/hexagon-codes/hexagon/agent`)
- `Agent` interface
- `Input`, `Output` types
- `NewReAct()` and its option functions
- `Role` type

**Graph orchestration** (`github.com/hexagon-codes/hexagon/orchestration/graph`)
- `State` interface
- `MapState` type
- `NewGraph[S]()` builder
- `Graph[S].Run()`, `Graph[S].Stream()`
- `START`, `END` constants

### Beta

The following APIs are feature-complete but may be adjusted in MINOR versions:

**Multi-Agent** (`github.com/hexagon-codes/hexagon/agent`)
- `Team` and its option functions
- `TeamMode` constants
- `TransferTo()`, `SwarmRunner`
- `StateManager` interface

**RAG** (`github.com/hexagon-codes/hexagon/rag`)
- `Engine` and its option functions
- `Document`, `Loader`, `Splitter`, `Retriever`, `Indexer`, `Embedder` interfaces
- Built-in loader, splitter, and retriever implementations

**Security guards** (`github.com/hexagon-codes/hexagon/security/guard`)
- `Guard` interface
- `NewPromptInjectionGuard()`, `NewPIIGuard()`
- `GuardChain` and its modes

**Cost control** (`github.com/hexagon-codes/hexagon/security/cost`)
- `Controller`, `NewController()`, and its option functions
- `CheckRequest()`, `BudgetCostFunc()`, and `RecordUsageFunc()`

**Observability**
- `github.com/hexagon-codes/hexagon/observe/tracer`: `Tracer`, `Span`, and `NewMemoryTracer()`
- `github.com/hexagon-codes/hexagon/observe/metrics`: `Metrics` and `NewMemoryMetrics()`

### Alpha

The following APIs are experimental and subject to significant changes:

**Workflow** (`github.com/hexagon-codes/hexagon/orchestration/workflow`)
- `Workflow`, `Step` types
- Persistence interfaces

**Unified runtime** (`github.com/hexagon-codes/hexagon/runtime`)
- `Runner`, `DurableExecution` (per-tool exactly-once + Resume)
- `github.com/hexagon-codes/hexagon/runtime/middleware`: `BudgetControlConfig`, `BudgetControl` (unified per-run and cross-run budgets), `Compaction`, and `PermissionMode`
- `github.com/hexagon-codes/hexagon/runtime/strategy`: ReAct/PlanExecute/Reflection execution strategies

**Durable checkpoints** (`github.com/hexagon-codes/hexagon/checkpoint`)
- Unified `Checkpointer` interface (Memory/File implementations)

**Checkpointing** (`github.com/hexagon-codes/hexagon/orchestration/graph`)
- `CheckpointSaver` interface
- Redis checkpoint implementation
- Interrupt and resume functionality
- Distributed execution, Barrier synchronization, node caching

**Vector stores**
- The RAG-layer `VectorStore` interface is in `github.com/hexagon-codes/hexagon/rag`.
- The general vector-store contract and in-memory implementation are in `github.com/hexagon-codes/ai-core/store/vector`; the Qdrant implementation is in `github.com/hexagon-codes/ai-core/store/vector/qdrant`.
- Hexagon-owned adapters live in `github.com/hexagon-codes/hexagon/store/vector/faiss`, `github.com/hexagon-codes/hexagon/store/vector/pgvector`, `github.com/hexagon-codes/hexagon/store/vector/redis`, `github.com/hexagon-codes/hexagon/store/vector/milvus`, `github.com/hexagon-codes/hexagon/store/vector/chroma`, `github.com/hexagon-codes/hexagon/store/vector/pinecone`, and `github.com/hexagon-codes/hexagon/store/vector/weaviate`. There is no importable `github.com/hexagon-codes/hexagon/store/vector` root package.

Starting with ai-core v0.2.7, new Qdrant collections default to SHA-256-derived UUIDv8 point IDs. To migrate an existing collection, import `github.com/hexagon-codes/ai-core/store/vector/qdrant` directly, explicitly select `PointIDLegacyHash31` to read and export the old data, then rebuild it into a new collection using the UUIDv8 default. Do not mix both ID strategies in one collection.

**Advanced retrievers** (`github.com/hexagon-codes/hexagon/rag/retriever`)
- `HyDERetriever` - Hypothetical Document Embeddings retrieval
- `AdaptiveRetriever` - Adaptive retrieval
- `ParentDocRetriever` - Parent document retrieval

**Advanced loaders** (`github.com/hexagon-codes/hexagon/rag/loader`)
- `ExcelLoader`, `PPTXLoader`, `CSVLoader` - Office file loading
- `OCRLoader` - VisionLLM OCR text extraction

**Advanced splitters** (`github.com/hexagon-codes/hexagon/rag/splitter`)
- `TokenSplitter` - Token-count-based splitting
- `CodeSplitter` - Code syntax-aware splitting

**Memory sharing** (`github.com/hexagon-codes/hexagon/agent`)
- `SharedMemory`, `SharedMemoryProxy`, and `WithSharedMemory()` implement automatic memory sharing across multiple agents

**Agent primitives** (`github.com/hexagon-codes/hexagon/agent`)
- `ParallelAgent`, `SequentialAgent`, and `LoopAgent` primitives

**MCP protocol** (`github.com/hexagon-codes/hexagon/mcp`)
- MCP protocol support

### Deprecated

**Top-level re-exports** (`github.com/hexagon-codes/hexagon` — `deprecated.go`)

Starting from v0.3.2-beta, the exported symbols in `hexagon.go` have been trimmed from 98 to a small set of core entry symbols. All convenience aliases previously exposed through the top-level package have been moved to `deprecated.go` and will be removed in the next major version.

> v0.5.0 further grouped `a2a`/`artifact`/`semantic`/`skill` under `agent/` and `adw` under `rag/`, and removed the `compose`/`process`/`flow` packages. During the transition, `deprecated.go` still re-exports the old entry points for these symbols.

Deprecation is communicated through `Deprecated:` comments in Go documentation and migration docs. Compatibility re-exports do not emit runtime warnings merely because callers use an old entry point.

Affected symbols include but are not limited to:

- Orchestration: `NewGraph()`, `NewChain()`, `START`, `END`
- Multi-Agent: `NewTeam()`, `TransferTo()`, `WithAgents()`, `WithMode()`, `TeamMode*` constants
- Observability: `NewTracer()`, `NewMetrics()`, `StartSpan()`, `ContextWithTracer()`
- Security: `NewPromptInjectionGuard()`, `NewPIIGuard()`, `NewCostController()`
- RAG: `NewRAGEngine()`, `NewRAGPipeline()`, loader/splitter/retriever/indexer/embedder factory functions
- Vector stores: `NewMemoryVectorStore()`, `NewQdrantStore()`, `Qdrant*` options and constants
- LLM: `NewOpenAI()`, `OpenAIWith*` options, `NewLLMRouter()`, role constants
- State management: `NewStateManager()`, `NewGlobalState()`
- MCP protocol: `ConnectMCPServer()`, `ConnectMCPStdio()`, `ConnectMCPSSE()`, `NewMCPServer()`
- Memory stores: `NewInMemoryStore()`, `NewFileStore()`, `NewRedisStore()`, `NewPersistentMemory()`
- Event stream: `NewEventStream()`, `Event*` constants
- Skill system: `NewSkillRegistry()`, `NewHMACSigner()`
- All deprecated type aliases (`Graph`, `Chain`, `State`, `MapState`, `Tracer`, `Span`, `Metrics`, `Guard`, etc.)

**Migration:** Import the corresponding sub-packages directly. For example:

```go
// Old way (deprecated)
team := hexagon.NewTeam("my-team", hexagon.WithAgents(a1, a2))

// New way (recommended)
import "github.com/hexagon-codes/hexagon/agent"
team := agent.NewTeam("my-team", agent.WithAgents(a1, a2))
```

## Compatibility Policy

### Backward-Compatible Changes

The following changes are considered backward-compatible:

- Adding new exported functions, types, or constants
- Adding optional parameters to existing functions (via the options function pattern)
- Improving error messages
- Bug fixes

### Breaking Changes

The following changes are considered breaking (require a MAJOR version bump):

- Removing or renaming exported functions, types, or constants
- Changing function signatures (parameters or return values)
- Changing an exported interface, including adding, removing, or modifying methods (Go interfaces have no default methods; adding a method breaks external implementers)
- Changing the semantics of existing behavior

## Deprecation Process

1. **Mark deprecated**: Add a `Deprecated:` annotation in documentation and code comments
2. **Migration guide**: Provide instructions for migrating to the new API
3. **No runtime side effects**: Do not emit warnings merely for deprecation, so library code does not pollute caller logs
4. **Retention period**: Retain the deprecated API for at least 1 MINOR version cycle
5. **Removal**: Remove in the next MAJOR version

## Import Path Stability

The following import paths are stable:

```go
import "github.com/hexagon-codes/hexagon"                     // top-level API
import "github.com/hexagon-codes/hexagon/agent"               // Agent and shared memory
import "github.com/hexagon-codes/hexagon/core"                // core interfaces
import "github.com/hexagon-codes/hexagon/orchestration/graph" // graph orchestration
import "github.com/hexagon-codes/hexagon/rag"                 // RAG and RAG VectorStore
import "github.com/hexagon-codes/hexagon/runtime/middleware"  // runtime middleware
import "github.com/hexagon-codes/hexagon/security/cost"       // cost control
import "github.com/hexagon-codes/hexagon/security/guard"      // security guards
import "github.com/hexagon-codes/hexagon/observe/tracer"      // tracing
import "github.com/hexagon-codes/hexagon/observe/metrics"     // metrics
```

A stable import path does not mean every API in that package has reached the Stable level; the module classifications above define the applicable compatibility guarantee.

Packages under `internal/` are not public and may change at any time.

## Dependency Stability

The current dependency topology of the Hexagon root module is:

- L0: `toolkit v0.3.4`.
- L1: `ai-core v0.2.7`, which itself also requires `toolkit v0.3.4`.
- L2: the Hexagon root module directly requires both versions and resolves one toolkit version under Go's minimal version selection.

| Dependency | Version | Description |
|-----|------|------|
| `github.com/hexagon-codes/ai-core` | v0.2.7 | AI capability library |
| `github.com/hexagon-codes/toolkit` | v0.3.4 | Go general-purpose toolkit |

The root module requires Go 1.25.12 or later. The `examples/` module maintains its own `go.mod` and pins published versions; it is outside this table and must not be assumed to move in lockstep with the root module. Public API changes in dependencies must first be adapted and regression-tested in Hexagon, then shipped under Hexagon's own version.

## Feedback

For questions or suggestions regarding API stability:

- Submit a [GitHub Issue](https://github.com/hexagon-codes/hexagon/issues)
