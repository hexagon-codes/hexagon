<div align="right">Language: <a href="API.md">中文</a> | English</div>

# Hexagon API Reference

This document provides the complete API reference for the Hexagon framework.

## Table of Contents

- [Top-Level API](#top-level-api)
- [Agent](#agent)
- [Tool](#tool)
- [RAG](#rag)
- [Graph Orchestration](#graph-orchestration)
- [Team Multi-Agent](#team-multi-agent)
- [Security Guards](#security-guards)
- [Observability](#observability)
- [Type Definitions](#type-definitions)

---

## Top-Level API

The `github.com/hexagon-codes/hexagon` package provides the most concise entry-point API.

### Convenience Functions

#### Chat

Execute a simple conversation (minimal API).

```go
func Chat(ctx context.Context, query string, opts ...QuickStartOption) (string, error)
```

**Example:**
```go
response, err := hexagon.Chat(ctx, "What is Go?")
```

#### ChatWithTools

Conversation with tools.

```go
func ChatWithTools(ctx context.Context, query string, tools ...tool.Tool) (string, error)
```

**Example:**
```go
result, err := hexagon.ChatWithTools(ctx, "What is 123 * 456?", calculatorTool)
```

#### Run

Execute an Agent and return the complete output.

```go
func Run(ctx context.Context, input Input, opts ...QuickStartOption) (Output, error)
```

### QuickStart

Quickly create a ReAct Agent.

```go
func QuickStart(opts ...QuickStartOption) *agent.ReActAgent
```

**Options:**

| Option | Description |
|--------|-------------|
| `WithProvider(p llm.Provider)` | Set the LLM Provider |
| `WithTools(tools ...tool.Tool)` | Set tools |
| `WithSystemPrompt(prompt string)` | Set system prompt |
| `WithMemory(m memory.Memory)` | Set memory system |

**Example:**
```go
agent := hexagon.QuickStart(
    hexagon.WithTools(searchTool, calculatorTool),
    hexagon.WithSystemPrompt("You are a helpful assistant."),
)
output, err := agent.Run(ctx, hexagon.Input{Query: "What is 2+2?"})
```

### SetDefaultProvider

Set the default LLM Provider.

```go
func SetDefaultProvider(p llm.Provider)
```

---

## Agent

### Agent Interface

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
    Query   string         `json:"query"`             // User query
    Context map[string]any `json:"context,omitempty"` // Additional context
}
```

### Output

```go
type Output struct {
    Content   string           `json:"content"`              // Final response
    ToolCalls []ToolCallRecord `json:"tool_calls,omitempty"` // Tool call records
    Usage     llm.Usage        `json:"usage,omitempty"`      // Token usage statistics
    Metadata  map[string]any   `json:"metadata,omitempty"`   // Additional metadata
}
```

### NewReActAgent

Create a ReAct Agent.

```go
var NewReActAgent = agent.NewReAct
```

**Options:**

| Option | Description |
|--------|-------------|
| `WithID(id string)` | Set Agent ID |
| `WithName(name string)` | Set Agent name |
| `WithDescription(desc string)` | Set Agent description |
| `WithSystemPrompt(prompt string)` | Set system prompt |
| `WithLLM(provider llm.Provider)` | Set LLM Provider |
| `WithTools(tools ...tool.Tool)` | Set tool list |
| `WithMemory(mem memory.Memory)` | Set memory system |
| `WithMaxIterations(n int)` | Set maximum iteration count |
| `WithVerbose(v bool)` | Enable verbose output mode |
| `WithRole(role Role)` | Set Agent role |

### Role

```go
type Role struct {
    Name         string   // Role name
    Goal         string   // Role objective
    Backstory    string   // Background story
    Constraints  []string // Constraints
    Capabilities []string // Capability list
}
```

### StateManager

```go
type StateManager interface {
    Turn() State    // Turn-level state
    Session() State // Session-level state
    Agent() State   // Agent-level state
    Global() State  // Global state
}

var NewStateManager = agent.NewStateManager
var NewGlobalState = agent.NewGlobalState
```

---

## Tool

### NewTool

Create a tool from a function.

```go
func NewTool[I, O any](name, description string, fn func(context.Context, I) (O, error)) *tool.FuncTool[I, O]
```

**Input struct tags:**

| Tag | Description |
|-----|-------------|
| `json:"name"` | JSON field name |
| `desc:"description"` | Field description |
| `required:"true"` | Whether the field is required |
| `enum:"a,b,c"` | Allowed value list |

**Example:**
```go
type CalcInput struct {
    A float64 `json:"a" desc:"First number" required:"true"`
    B float64 `json:"b" desc:"Second number" required:"true"`
}

calculator := hexagon.NewTool("calculator", "Perform addition",
    func(ctx context.Context, input CalcInput) (float64, error) {
        return input.A + input.B, nil
    },
)
```

---

## RAG

### NewRAGEngine

Create a RAG engine.

```go
var NewRAGEngine = rag.NewEngine
```

**Options:**

| Option | Description |
|--------|-------------|
| `WithRAGStore(store VectorStore)` | Set vector store |
| `WithRAGEmbedder(embedder Embedder)` | Set embedder |
| `WithRAGLoader(loader Loader)` | Set document loader |
| `WithRAGSplitter(splitter Splitter)` | Set document splitter |
| `WithRAGTopK(k int)` | Set default top-K return count |
| `WithRAGMinScore(score float32)` | Set default minimum score |

**Methods:**
```go
// Index documents
func (e *Engine) Index(ctx context.Context, docs []Document) error

// Retrieve documents
func (e *Engine) Retrieve(ctx context.Context, query string, opts ...RetrieveOption) ([]Document, error)
```

### Retrieve Options

| Option | Description |
|--------|-------------|
| `WithTopK(k int)` | Number of documents to return |
| `WithMinScore(score float32)` | Minimum relevance score |
| `WithFilter(filter map[string]any)` | Metadata filter conditions |

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

### Document Loaders

| Function | Description |
|----------|-------------|
| `NewTextLoader(path string)` | Plain text file loader |
| `NewMarkdownLoader(path string)` | Markdown file loader |
| `NewDirectoryLoader(dir string, patterns ...string)` | Batch directory loader |
| `NewURLLoader(url string)` | URL loader |
| `NewStringLoader(content string)` | String content loader |
| `NewCSVLoader(path string)` | CSV file loader |
| `NewExcelLoader(path string)` | Excel (.xlsx) file loader |
| `NewPPTXLoader(path string)` | PowerPoint (.pptx) file loader |
| `NewDOCXLoader(path string)` | Word (.docx) file loader |
| `NewPDFLoader(path string)` | PDF file loader |
| `NewOCRLoader(path string, opts ...OCROption)` | OCR image text extraction loader (supports VisionLLM) |

### Document Splitters

| Function | Description |
|----------|-------------|
| `NewCharacterSplitter(chunkSize, overlap int)` | Character-based splitter |
| `NewRecursiveSplitter(chunkSize, overlap int)` | Recursive splitter |
| `NewMarkdownSplitter()` | Markdown splitter |
| `NewSentenceSplitter()` | Sentence splitter |
| `NewTokenSplitter(chunkSize int, opts ...TokenOption)` | Token splitter (splits by token count) |
| `NewCodeSplitter(language string)` | Code splitter (splits by language syntax) |
| `NewSemanticSplitter(embedder Embedder)` | Semantic splitter (splits by semantic similarity) |

### Retrievers

| Function | Description |
|----------|-------------|
| `NewVectorRetriever(store, embedder)` | Vector retriever |
| `NewKeywordRetriever(docs []Document)` | Keyword retriever |
| `NewHybridRetriever(vector, keyword, weight)` | Hybrid retriever |
| `NewMultiRetriever(retrievers ...Retriever)` | Multi-source retriever |
| `NewHyDERetriever(llm, embedder, store, opts ...)` | HyDE retriever (generates hypothetical documents with LLM then retrieves) |
| `NewAdaptiveRetriever(retrievers ...Retriever)` | Adaptive retriever (automatically selects strategy based on query characteristics) |
| `NewParentDocRetriever(store, splitter)` | Parent document retriever (retrieves sub-chunks then returns full parent document) |

### Embedders

| Function | Description |
|----------|-------------|
| `NewOpenAIEmbedder()` | OpenAI Embedder |
| `NewCachedEmbedder(base Embedder)` | Cached Embedder |
| `NewMockEmbedder(dim int)` | Mock Embedder (for testing) |

### Vector Stores

#### NewMemoryVectorStore

Create an in-memory vector store.

```go
var NewMemoryVectorStore = vector.NewMemoryStore
```

#### NewQdrantStore

Create a Qdrant vector store.

```go
var NewQdrantStore = qdrant.New
```

**Configuration:**
```go
type QdrantConfig struct {
    Host             string // Host address
    Port             int    // Port
    Collection       string // Collection name
    Dimension        int    // Vector dimension
    APIKey           string // API Key (optional)
    HTTPS            bool   // Whether to use HTTPS
    Timeout          time.Duration
    Distance         DistanceType // Distance metric
    OnDisk           bool         // Whether to store on disk
    CreateCollection bool         // Whether to auto-create collection
}
```

**Option-based creation:**
```go
store, err := hexagon.NewQdrantStoreWithOptions(
    hexagon.QdrantWithHost("localhost"),
    hexagon.QdrantWithPort(6333),
    hexagon.QdrantWithCollection("docs"),
    hexagon.QdrantWithDimension(1536),
    hexagon.QdrantWithCreateCollection(true),
)
```

#### Additional Vector Stores

| Function | Description |
|----------|-------------|
| `faiss.NewStore(config)` | FAISS vector store (high-performance local retrieval) |
| `pgvector.NewStore(config)` | PgVector store (PostgreSQL extension) |
| `redis.NewStore(config)` | Redis vector store (Redis Stack) |
| `milvus.NewStore(config)` | Milvus vector store |
| `chroma.NewStore(config)` | Chroma vector store |
| `pinecone.NewStore(config)` | Pinecone vector store |
| `weaviate.NewStore(config)` | Weaviate vector store |

---

## Graph Orchestration

### NewGraph

Create a graph orchestration builder.

```go
func NewGraph[S graph.State](name string) *graph.GraphBuilder[S]
```

**Builder methods:**

| Method | Description |
|--------|-------------|
| `AddNode(name string, handler NodeHandler[S])` | Add a node |
| `AddEdge(from, to string)` | Add an edge |
| `AddConditionalEdge(from string, router RouterFunc[S], edges map[string]string)` | Add a conditional edge |
| `SetEntryPoint(node string)` | Set the entry point |
| `SetFinishPoint(nodes ...string)` | Set the finish point(s) |
| `WithCheckpointer(saver CheckpointSaver)` | Set the checkpoint saver |
| `Build() (*Graph[S], error)` | Build the graph |
| `MustBuild() *Graph[S]` | Build the graph (panics on failure) |

**Constants:**
```go
const START = graph.START // Start node
const END = graph.END     // End node
```

### State Interface

```go
type State interface {
    Clone() State
}

// General-purpose map state
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

### Run Options

| Option | Description |
|--------|-------------|
| `WithThread(config *ThreadConfig)` | Set thread configuration |
| `WithInterrupt(nodes ...string)` | Set interrupt nodes |
| `WithDebug(debug bool)` | Enable debug mode |

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
    EventTypeNodeStart // Node started
    EventTypeNodeEnd   // Node finished
    EventTypeError     // Error occurred
    EventTypeEnd       // Graph execution completed
)
```

**Example:**
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

Create a chain orchestration builder.

```go
func NewChain[I, O any](name string) *chain.ChainBuilder[I, O]
```

**Methods:**
```go
builder.Pipe(component).Build()
```

---

## Team Multi-Agent

### NewTeam

Create an Agent team.

```go
var NewTeam = agent.NewTeam
```

**Options:**

| Option | Description |
|--------|-------------|
| `WithAgents(agents ...Agent)` | Set team members |
| `WithMode(mode TeamMode)` | Set work mode |
| `WithManager(manager Agent)` | Set manager (Hierarchical mode) |
| `WithMaxRounds(rounds int)` | Set maximum rounds |
| `WithTeamDescription(desc string)` | Set team description |

### TeamMode

```go
const (
    TeamModeSequential    // Sequential execution
    TeamModeHierarchical  // Hierarchical mode
    TeamModeCollaborative // Collaborative mode
    TeamModeRoundRobin    // Round-robin mode
)
```

### TransferTo

Create an Agent handoff tool.

```go
var TransferTo = agent.TransferTo
```

**Example:**
```go
tools := []hexagon.Tool{
    hexagon.TransferTo(salesAgent),
    hexagon.TransferTo(supportAgent),
}
```

---

## Security Guards

### Guard

#### NewPromptInjectionGuard

Create a prompt injection detection guard.

```go
var NewPromptInjectionGuard = guard.NewPromptInjectionGuard
```

#### NewPIIGuard

Create a PII detection guard.

```go
var NewPIIGuard = guard.NewPIIGuard
```

#### NewGuardChain

Create a guard chain.

```go
var NewGuardChain = guard.NewGuardChain
```

**Chain modes:**
```go
const (
    ChainModeAll   // All guards must pass
    ChainModeAny   // Any single guard passing is sufficient
    ChainModeFirst // Stop at the first failure
)
```

### CheckResult

```go
type CheckResult struct {
    Passed   bool      // Whether the check passed
    Score    float64   // Risk score (0-1)
    Category string    // Risk category
    Reason   string    // Reason
    Findings []Finding // Issues found
}
```

### CostController

#### NewCostController

Create a cost controller.

```go
var NewCostController = cost.NewController
```

**Options:**

| Option | Description |
|--------|-------------|
| `WithBudget(amount float64)` | Set budget |
| `WithMaxTokensPerRequest(n int)` | Per-request token limit |
| `WithMaxTokensPerSession(n int)` | Per-session token limit |
| `WithMaxTokensTotal(n int)` | Total token limit |
| `WithRequestsPerMinute(n int)` | RPM limit |

---

## Observability

### Tracer

#### NewTracer

Create an in-memory tracer.

```go
var NewTracer = tracer.NewMemoryTracer
```

#### NewNoopTracer

Create a no-op tracer (disables tracing).

```go
var NewNoopTracer = tracer.NewNoopTracer
```

#### ContextWithTracer

Add a tracer to a context.

```go
var ContextWithTracer = tracer.ContextWithTracer
```

#### StartSpan

Start a new tracing Span.

```go
var StartSpan = tracer.StartSpan
```

**Span methods:**
```go
span.SetAttribute(key string, value any)
span.RecordError(err error)
span.End()
```

### Metrics

#### NewMetrics

Create an in-memory metrics collector.

```go
var NewMetrics = metrics.NewMemoryMetrics
```

**Methods:**
```go
// Counter
m.Counter(name string, labels ...string).Inc()
m.Counter(name string, labels ...string).Add(delta float64)

// Histogram
m.Histogram(name string, labels ...string).Observe(value float64)

// Gauge
m.Gauge(name string, labels ...string).Set(value float64)
m.Gauge(name string, labels ...string).Inc()
m.Gauge(name string, labels ...string).Dec()
```

---

## Type Definitions

### Re-exported Types

```go
// Core types
type Input = agent.Input
type Output = agent.Output
type Tool = tool.Tool
type Memory = memory.Memory
type Message = llm.Message
type Schema = core.Schema
type Component[I, O any] = core.Component[I, O]
type Stream[T any] = core.Stream[T]

// Agent types
type Agent = agent.Agent
type Role = agent.Role
type Team = agent.Team
type StateManager = agent.StateManager

// Graph orchestration types
type Graph[S graph.State] = graph.Graph[S]
type Chain[I, O any] = chain.Chain[I, O]
type State = graph.State
type MapState = graph.MapState

// Observability types
type Tracer = tracer.Tracer
type Span = tracer.Span
type Metrics = metrics.Metrics

// Security types
type Guard = guard.Guard
type CostController = cost.Controller

// RAG types
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

## Complete Examples

### ReAct Agent with Tools

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

type SearchInput struct {
    Query string `json:"query" desc:"Search keywords" required:"true"`
}

func main() {
    ctx := context.Background()

    searchTool := hexagon.NewTool("search", "Search for information",
        func(ctx context.Context, input SearchInput) (string, error) {
            return fmt.Sprintf("Search results for: %s ...", input.Query), nil
        },
    )

    agent := hexagon.QuickStart(
        hexagon.WithTools(searchTool),
        hexagon.WithSystemPrompt("You are an assistant that can search for information to answer questions"),
    )

    output, _ := agent.Run(ctx, hexagon.Input{
        Query: "What is the latest version of Go?",
    })

    fmt.Println(output.Content)
}
```

### RAG Question-Answering System

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

func main() {
    ctx := context.Background()

    // Set up RAG
    store := hexagon.NewMemoryVectorStore()
    embedder := hexagon.NewOpenAIEmbedder()
    engine := hexagon.NewRAGEngine(
        hexagon.WithRAGStore(store),
        hexagon.WithRAGEmbedder(embedder),
    )

    // Index documents
    engine.Index(ctx, []hexagon.Document{
        {ID: "1", Content: "Hexagon is a Go AI Agent framework"},
        {ID: "2", Content: "Hexagon supports RAG, graph orchestration, and multi-agent systems"},
    })

    // Retrieve
    docs, _ := engine.Retrieve(ctx, "What features does Hexagon have",
        hexagon.WithTopK(2),
    )

    for _, doc := range docs {
        fmt.Printf("[%.2f] %s\n", doc.Score, doc.Content)
    }
}
```
