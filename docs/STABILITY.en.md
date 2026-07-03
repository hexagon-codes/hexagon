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
- `Component[I, O]` interface
- `Stream[T]` interface
- `Schema` type

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

**Security** (`github.com/hexagon-codes/hexagon/security`)
- `Guard` interface
- `NewPromptInjectionGuard()`, `NewPIIGuard()`
- `GuardChain` and its modes
- `CostController` and its option functions

**Observability** (`github.com/hexagon-codes/hexagon/observe`)
- `Tracer`, `Span` interfaces
- `Metrics` interface
- `NewTracer()`, `NewMetrics()`

### Alpha

The following APIs are experimental and subject to significant changes:

**Workflow** (`github.com/hexagon-codes/hexagon/orchestration/workflow`)
- `Workflow`, `Step` types
- Persistence interfaces

**Unified runtime** (`github.com/hexagon-codes/hexagon/runtime`)
- `Runner`, `DurableExecution` (per-tool exactly-once + Resume)
- `runtime/middleware`: Budget/CostControl/Compaction/PermissionMode
- `runtime/strategy`: ReAct/PlanExecute/Reflection execution strategies

**Durable checkpoints** (`github.com/hexagon-codes/hexagon/checkpoint`)
- Unified `Checkpointer` interface (Memory/File implementations)

**Checkpointing** (`github.com/hexagon-codes/hexagon/orchestration/graph`)
- `CheckpointSaver` interface
- Redis checkpoint implementation
- Interrupt and resume functionality
- Distributed execution, Barrier synchronization, node caching

**Vector stores** (`github.com/hexagon-codes/hexagon/store/vector`)
- `VectorStore` interface
- Qdrant, FAISS, PgVector, Redis, Milvus, Chroma, Pinecone, Weaviate implementations

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

**Memory sharing** (`github.com/hexagon-codes/hexagon/memory`)
- Automatic memory sharing across multiple agents

**Agent primitives** (`github.com/hexagon-codes/hexagon/agent`)
- `Parallel`, `Sequential`, `Route` primitives

**MCP protocol** (`github.com/hexagon-codes/hexagon/mcp`)
- MCP protocol support

### Deprecated

**Top-level re-exports** (`github.com/hexagon-codes/hexagon` — `deprecated.go`)

Starting from v0.3.2-beta, the exported symbols in `hexagon.go` have been trimmed from 98 to a small set of core entry symbols. All convenience aliases previously exposed through the top-level package have been moved to `deprecated.go` and will be removed in the next major version.

> v0.5.0 further grouped `a2a`/`artifact`/`semantic`/`skill` under `agent/` and `adw` under `rag/`, and removed the `compose`/`process`/`flow` packages. During the transition, `deprecated.go` still re-exports the old entry points for these symbols.

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
- Adding methods with default implementations to interfaces
- Improving error messages
- Bug fixes

### Breaking Changes

The following changes are considered breaking (require a MAJOR version bump):

- Removing or renaming exported functions, types, or constants
- Changing function signatures (parameters or return values)
- Changing interface definitions
- Changing the semantics of existing behavior

## Deprecation Process

1. **Mark deprecated**: Add a `Deprecated` annotation in documentation and code comments
2. **Migration guide**: Provide instructions for migrating to the new API
3. **Runtime warning**: Emit a deprecation warning at runtime
4. **Retention period**: Retain the deprecated API for at least 1 MINOR version cycle
5. **Removal**: Remove in the next MAJOR version

## Import Path Stability

The following import paths are stable:

```go
import "github.com/hexagon-codes/hexagon"                  // top-level API
import "github.com/hexagon-codes/hexagon/agent"            // Agent
import "github.com/hexagon-codes/hexagon/core"             // core interfaces
import "github.com/hexagon-codes/hexagon/orchestration/graph" // graph orchestration
import "github.com/hexagon-codes/hexagon/rag"              // RAG
import "github.com/hexagon-codes/hexagon/security/guard"   // security guard
import "github.com/hexagon-codes/hexagon/observe/tracer"   // tracing
import "github.com/hexagon-codes/hexagon/observe/metrics"  // metrics
```

Packages under `internal/` are not public and may change at any time.

## Dependency Stability

Hexagon depends on the following external libraries:

| Dependency | Version | Description |
|-----|------|------|
| `github.com/hexagon-codes/ai-core` | v0.2.0 | AI capability library |
| `github.com/hexagon-codes/toolkit` | v0.2.6 | Go general-purpose toolkit |

Breaking changes to the public APIs of these dependencies will be reflected in Hexagon's own version number.

## Feedback

For questions or suggestions regarding API stability:

- Submit a [GitHub Issue](https://github.com/hexagon-codes/hexagon/issues)
- Join [GitHub Discussions](https://github.com/hexagon-codes/hexagon/discussions)
