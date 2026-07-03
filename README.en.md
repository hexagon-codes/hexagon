<div align="right">Language: <a href="README.md">中文</a> | English</div>

<div align="center">

<img src=".github/assets/logo.jpg" alt="Hexagon Logo" width="160">

**The All-Around AI Agent Framework for the Go Ecosystem**

[![Go Reference](https://img.shields.io/badge/Go-1.25.7+-00ADD8?logo=go&logoColor=white)](https://pkg.go.dev/github.com/hexagon-codes/hexagon)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![CI](https://img.shields.io/badge/CI-passing-brightgreen)](https://github.com/hexagon-codes/hexagon/actions)

</div>

---

### 📖 About

**Hexagon** takes its name from the Chinese internet term "hexagonal warrior" (六边形战士), symbolizing balanced strength with no weak points.

We focus on six core dimensions — **ease of use, performance, extensibility, task orchestration, observability, and security** — striving for balanced excellence across every capability module. Hexagon is the premier enterprise-grade AI Agent development foundation for Go developers.

</p>

### 🚀 Key Features

* ⚡ **High Performance** │ Native Go concurrency engine, supporting 100k+ active Agents
* 🧩 **Ease of Use** │ Declarative API design, build a basic prototype in 3 lines of code
* 🛡️ **Security** │ Enterprise-grade sandbox isolation with comprehensive access control
* 🔧 **Extensibility** │ Plugin-based architecture supporting seamless custom component integration
* 🛠️ **Orchestration** │ Powerful graph orchestration engine for complex multi-level task pipelines
* 🔍 **Observability** │ Deep OpenTelemetry integration for full end-to-end tracing

---

## 🌐 Ecosystem

Hexagon is a complete AI Agent development ecosystem composed of multiple repositories:

| Repository | Description | Link |
|-----|------|------|
| **hexagon** | AI Agent framework core (orchestration, RAG, Graph, Hooks) | [github.com/hexagon-codes/hexagon](https://github.com/hexagon-codes/hexagon) |
| **ai-core** | AI capability library (LLM/Tool/Memory/Schema) | [github.com/hexagon-codes/ai-core](https://github.com/hexagon-codes/ai-core) |
| **toolkit** | Go general-purpose toolkit (lang/crypto/net/cache/util) | [github.com/hexagon-codes/toolkit](https://github.com/hexagon-codes/toolkit) |
| **hexagon-ui** | Dev UI frontend (Vue 3 + TypeScript) | [github.com/hexagon-codes/hexagon-ui](https://github.com/hexagon-codes/hexagon-ui) |

### 🧠 ai-core — AI Capability Library

Provides core abstractions for LLM, Tool, Memory, and Schema, with support for multiple LLM providers:

```go
import "github.com/hexagon-codes/ai-core/llm"
import "github.com/hexagon-codes/ai-core/llm/openai"
import "github.com/hexagon-codes/ai-core/tool"
import "github.com/hexagon-codes/ai-core/memory"
```

**Main modules:**
- `llm/` - LLM Provider interfaces + implementations (OpenAI, DeepSeek, Anthropic, Gemini, Qwen, Ark, Ollama)
- `llm/router/` - Smart model routing (task-aware + model capability profiles)
- `tool/` - Tool system with functional definitions
- `memory/` - Memory system with vector storage support
- `schema/` - Automatic JSON Schema generation
- `streamx/` - Streaming response processing
- `template/` - Prompt template engine

### 🛠️ toolkit — Go General-Purpose Toolkit

A production-grade Go utility package providing language enhancements, cryptography, networking, caching, goroutine pools, and other foundational capabilities:

```go
import "github.com/hexagon-codes/toolkit/lang/conv"      // type conversion
import "github.com/hexagon-codes/toolkit/lang/stringx"   // string utilities
import "github.com/hexagon-codes/toolkit/lang/syncx"     // concurrency utilities
import "github.com/hexagon-codes/toolkit/net/httpx"      // HTTP client
import "github.com/hexagon-codes/toolkit/net/sse"        // SSE client
import "github.com/hexagon-codes/toolkit/util/retry"     // retry mechanism
import "github.com/hexagon-codes/toolkit/util/idgen"     // ID generation
import "github.com/hexagon-codes/toolkit/util/poolx"     // goroutine pool
import "github.com/hexagon-codes/toolkit/cache/local"    // local cache
```

**Main modules:**
- `lang/` - Language enhancements (conv, stringx, slicex, mapx, timex, contextx, errorx, syncx)
- `crypto/` - Cryptography (aes, rsa, sign)
- `net/` - Networking (httpx, sse, ip)
- `cache/` - Caching (local, redis, multi)
- `util/` - Utilities (retry, rate, idgen, logger, validator, poolx goroutine pool)
- `collection/` - Data structures (set, list, queue, stack)

### 🎨 hexagon-ui — Dev UI Frontend

A development and debugging interface built with Vue 3 + TypeScript:

```bash
cd hexagon-ui
npm install
npm run dev
# Visit http://localhost:5173
```

**Features:**
- Real-time event streaming (SSE push)
- Metrics dashboard
- Event detail viewer
- LLM streaming output display

## ⚡ Quick Start

### 📦 Installation

```bash
go get github.com/hexagon-codes/hexagon
```

### ⚙️ Environment Setup

```bash
# OpenAI
export OPENAI_API_KEY=your-api-key

# or DeepSeek
export DEEPSEEK_API_KEY=your-api-key
```

### 🎯 3 Lines to Get Started

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

func main() {
    response, _ := hexagon.Chat(context.Background(), "What is Go?")
    fmt.Println(response)
}
```

### 🔧 Agent with Tools

```go
package main

import (
    "context"
    "fmt"
    "github.com/hexagon-codes/hexagon"
)

func main() {
    // Define a calculator tool
    type CalcInput struct {
        A  float64 `json:"a" desc:"first number" required:"true"`
        B  float64 `json:"b" desc:"second number" required:"true"`
        Op string  `json:"op" desc:"operator" required:"true" enum:"add,sub,mul,div"`
    }

    calculator := hexagon.NewTool("calculator", "Perform math calculations",
        func(ctx context.Context, input CalcInput) (float64, error) {
            switch input.Op {
            case "add": return input.A + input.B, nil
            case "sub": return input.A - input.B, nil
            case "mul": return input.A * input.B, nil
            case "div": return input.A / input.B, nil
            }
            return 0, fmt.Errorf("unknown operator")
        },
    )

    // Create an Agent with tools
    agent := hexagon.QuickStart(
        hexagon.WithTools(calculator),
        hexagon.WithSystemPrompt("You are a math assistant"),
    )

    output, _ := agent.Run(context.Background(), hexagon.Input{
        Query: "Calculate 123 * 456",
    })
    fmt.Println(output.Content)
}
```

### 🔍 RAG (Retrieval-Augmented Generation)

```go
// Create a RAG engine
engine := hexagon.NewRAGEngine(
    hexagon.WithRAGStore(hexagon.NewMemoryVectorStore()),
    hexagon.WithRAGEmbedder(hexagon.NewOpenAIEmbedder()),
)

// Index documents
engine.Index(ctx, []hexagon.Document{
    {ID: "1", Content: "Go supports concurrent programming"},
    {ID: "2", Content: "Go has a rich standard library"},
})

// Retrieve
docs, _ := engine.Retrieve(ctx, "Features of Go", hexagon.WithTopK(2))
```

### 📊 Graph Orchestration

```go
import "github.com/hexagon-codes/hexagon/orchestration/graph"

// Build a workflow graph
g, _ := graph.NewGraph[MyState]("workflow").
    AddNode("analyze", analyzeHandler).
    AddNode("process", processHandler).
    AddEdge(graph.START, "analyze").
    AddEdge("analyze", "process").
    AddEdge("process", graph.END).
    Build()

// Execute
result, _ := g.Run(ctx, initialState)
```

### 👥 Multi-Agent Team

```go
// Create a team
team := hexagon.NewTeam("research-team",
    hexagon.WithAgents(researcher, writer, reviewer),
    hexagon.WithMode(hexagon.TeamModeSequential),
)

// Execute
output, _ := team.Run(ctx, hexagon.Input{Query: "Write a technical article"})
```

## 🚀 Advanced Capabilities

### 🔀 Smart Model Router

Automatically selects the optimal model based on task type and complexity:

```go
import "github.com/hexagon-codes/ai-core/llm/router"

// Create a smart router
smartRouter := router.NewSmartRouter(baseRouter,
    router.WithAutoClassify(true),
)

// Request with routing context
routingCtx := router.NewRoutingContext(router.TaskTypeCoding, router.ComplexityMedium).
    WithPriority(router.PriorityQuality).
    RequireFunctions()

resp, decision, _ := smartRouter.CompleteWithRouting(ctx, req, routingCtx)
// decision contains: selected model, score, reason, and alternatives
```

**Features:**
- Task-aware routing (coding/reasoning/creative/analysis, etc.)
- Quality/cost/latency priority strategies
- 20+ predefined model capability profiles
- Routing history and statistical analysis

### ♻️ Durable Runtime

A unified Agent execution runtime with per-tool exactly-once snapshots and Resume:

```go
import (
    "github.com/hexagon-codes/hexagon/runtime"
    "github.com/hexagon-codes/hexagon/checkpoint"
)

// Checkpointer-backed durable execution: Resume after crash/interrupt without
// re-running already-completed tools.
durable := runtime.NewDurableExecution(checkpoint.NewMemory())
```

**Features:**
- per-tool exactly-once: completed tool calls are snapshotted and not replayed on Resume
- Unified Runner + execution strategies (ReAct / PlanExecute / Reflection share one runtime)
- Three-dimensional Budget (token/calls/duration) + unified CostControl abstraction
- Five-level PermissionMode and Context Compaction middleware (`runtime/middleware`)
- Long-running progress events + SSE event sink

### 🧬 Agent as Tool (AgentTool)

Wrap an Agent directly as a tool so it can be composed as a sub-capability of another Agent:

```go
import "github.com/hexagon-codes/hexagon/agent"

// Expose the researcher Agent as a tool callable by the supervisor
researcherTool := agent.NewAgentTool(researcher,
    agent.WithAgentToolName("research"),
    agent.WithAgentToolDescription("Research a topic and return key points"),
)

supervisor := hexagon.QuickStart(
    hexagon.WithTools(researcherTool),
    hexagon.WithSystemPrompt("You are an orchestrator; call sub-agents as needed"),
)
```

### 🧾 Native Structured Output

Decode strongly-typed Go structs via the provider's native `json_schema`:

```go
import "github.com/hexagon-codes/hexagon/llm/structured"

type Invoice struct {
    Number string  `json:"number"`
    Amount float64 `json:"amount"`
}

inv, _ := structured.Generate[Invoice](ctx, provider, "Parse this invoice: ...",
    structured.WithNativeJSONSchema("invoice"),
    structured.WithStrictMode(true),
)
```

### 📄 Agentic Document Workflows (ADW)

End-to-end document automation that goes beyond traditional RAG:

```go
import "github.com/hexagon-codes/hexagon/rag/adw"
import "github.com/hexagon-codes/hexagon/rag/adw/extractor"
import "github.com/hexagon-codes/hexagon/rag/adw/validator"

// Define an extraction schema
schema := adw.NewExtractionSchema("invoice").
    AddStringField("invoice_number", "Invoice Number", true).
    AddDateField("date", "Date", "YYYY-MM-DD", true).
    AddMoneyField("amount", "Amount", true).
    AddStringField("vendor", "Vendor", false)

// Create a processing pipeline
pipeline := adw.NewPipeline("invoice-processing").
    AddStep(adw.NewDocumentTypeDetectorStep()).
    AddStep(extractor.NewLLMExtractionStep(llmProvider, schema)).
    AddStep(extractor.NewEntityExtractionStep(llmProvider)).
    AddStep(validator.NewSchemaValidationStep(schema)).
    AddStep(adw.NewConfidenceCalculatorStep()).
    Build()

// Process documents
output, _ := pipeline.Process(ctx, adw.PipelineInput{
    Documents: documents,
    Schema:    schema,
})

// Access results
for _, doc := range output.Documents {
    fmt.Println("Invoice No:", doc.StructuredData["invoice_number"])
    fmt.Println("Entities:", doc.Entities)
    fmt.Println("Validation:", doc.IsValid())
}
```

**Features:**
- Document extensions: structured data/tables/entities/relations/validation errors
- Schema-driven structured extraction
- LLM extractors: entity/relation extraction
- Full validation: type/format/range/enum/regex
- Concurrent processing + hook system

### 🌐 A2A Protocol (Agent-to-Agent)

Implements the Google A2A protocol for standardized inter-Agent communication:

```go
import "github.com/hexagon-codes/hexagon/agent/a2a"

// Expose a Hexagon Agent as an A2A service
server := a2a.ExposeAgent(myAgent, "http://localhost:8080")
server.Start(":8080")

// Connect to a remote A2A Agent
client := a2a.NewClient("http://remote-agent.example.com")
card, _ := client.GetAgentCard(ctx)

// Send a message
task, _ := client.SendMessage(ctx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("Hello"),
})

// Streaming interaction
events, _ := client.SendMessageStream(ctx, req)
for event := range events {
    switch e := event.(type) {
    case *a2a.ArtifactEvent:
        fmt.Print(e.Artifact.GetTextContent())
    }
}
```

**Features:**
- Full A2A protocol implementation (AgentCard/Task/Message/Artifact)
- JSON-RPC 2.0 + SSE streaming responses
- Multiple authentication methods (Bearer Token/API Key/Basic Auth/RBAC)
- Agent discovery service (Registry/Static/Remote)
- Push notification support
- Seamless bridging with Hexagon Agents

## 💡 Design Philosophy

1. **Progressive Complexity** - 3 lines to get started, declarative configuration for intermediate use, graph orchestration for experts
2. **Convention over Configuration** - Sensible defaults, zero-config runnable
3. **Composition over Inheritance** - Small, focused components, flexible composition
4. **Explicit over Implicit** - Type-safe, compile-time checks
5. **Production First** - Built-in observability, graceful degradation

## 🏗️ Architecture

### 📐 Overall Architecture

<img src=".github/assets/architecture.png" alt="Hexagon Overall Architecture" width="800" style="height: auto;">

### 🔗 Ecosystem Dependencies

<img src=".github/assets/ecosystem.png" alt="Hexagon Ecosystem Dependencies" width="800" style="height: auto;">

### 📈 Data Flow

<img src=".github/assets/workflow.png" alt="Hexagon Data Flow" width="800" style="height: auto;">

## 🤖 LLM Support

| Provider | Status |
|----------|------|
| OpenAI (GPT-4, GPT-4o, o1, o3) | ✅ Supported |
| DeepSeek | ✅ Supported |
| Anthropic (Claude) | ✅ Supported |
| Google Gemini | ✅ Supported |
| Qwen (通义千问) | ✅ Supported |
| Ark / Doubao (豆包) | ✅ Supported |
| Ollama (local models) | ✅ Supported |

## 📁 Project Structure

```
hexagon/
├── agent/              # Agent core (ReAct/Role/Team/Handoff/State/Primitives/AgentTool/Supervisor)
│   ├── a2a/            # A2A protocol (Client/Server/Handler/Discovery)
│   ├── artifact/       # Artifact system
│   ├── semantic/       # Semantic capabilities
│   └── skill/          # Skill registry & signing
├── core/               # Unified interfaces (Component/Runnable, Stream[T], Compose/Fallback)
├── runtime/            # Unified execution runtime (Runner/DurableExecution/strategy/middleware)
│   ├── middleware/     # Budget/CostControl/Compaction/PermissionMode
│   └── strategy/       # Execution strategies (ReAct/PlanExecute/Reflection)
├── orchestration/      # Orchestration engine
│   ├── graph/          # Graph orchestration (state graph/checkpoints/Barrier/distributed/visualization)
│   ├── chain/          # Chain orchestration (compile-time I/O type checks)
│   ├── workflow/       # Workflow engine
│   └── planner/        # Planner
├── checkpoint/         # Unified Checkpointer persistence
├── interrupt/          # Interrupt & resume
├── rag/                # RAG system
│   ├── loader/         # Document loaders + Parser layer (Text/MD/CSV/XLSX/PPTX/DOCX/PDF/OCR)
│   ├── splitter/       # Document splitters (Character/Recursive/Markdown/Sentence/Token/Code)
│   ├── retriever/      # Retrievers (Vector/Keyword/Hybrid/HyDE/Adaptive/ParentDoc)
│   ├── reranker/       # Rerankers
│   ├── synthesizer/    # Response synthesizers
│   ├── adw/            # Agentic Document Workflows (extractor/validator)
│   ├── agentic/        # Agentic RAG
│   ├── corrective/     # Corrective RAG
│   ├── selfrag/        # Self-RAG
│   └── multimodal/     # Multimodal
├── llm/                # LLM orchestration layer
│   ├── structured/     # Native json_schema structured output
│   ├── batch/          # Batch calls
│   ├── conversation/   # Conversation management
│   ├── parser/         # Output parsing
│   └── template/       # Prompt templates
├── memory/store/       # Multi-Agent memory stores (InMemory/File/Redis/Persistent)
├── mcp/                # MCP protocol (discovery/auto-reconnect/multi-transport)
├── hooks/              # Hook system (Run/Tool/LLM/Retriever)
├── observe/            # Observability (Tracer/Metrics/OTel/Langfuse/DevUI/Replay)
├── security/           # Security (Guard/Guardrails/RBAC/Cost/Audit/Filter/PII/Tenant/Credential)
├── tool/               # Tool system (File/Python/Shell/Sandbox/HTTP/Search/DB/Browser...)
├── store/              # Storage
│   └── vector/         # Vector stores (FAISS/PgVector/Redis/Milvus/Chroma/Pinecone/Weaviate)
├── client/             # Client
├── plugin/             # Plugin system
├── config/             # Configuration management
├── evaluate/           # Evaluation system (agenteval/rag/metrics)
├── testing/            # Testing utilities (Mock/Record/E2E/Integration)
├── deploy/             # Deployment configs (Docker Compose/Helm Chart/CI)
├── examples/           # Example code (standalone module)
├── hexagon.go          # Top-level API (core entry symbols)
└── deprecated.go       # Transitional re-exports (removed in next major version)
```

## ⚠️ Recent Important Changes

### Architecture Refactor (v0.5.0)

v0.5.0 is a structural consolidation that also upgrades to ai-core v0.1.4 / toolkit v0.1.0 (Go 1.25):

- **Top-level feature packages grouped**: `a2a → agent/a2a`, `artifact → agent/artifact`, `semantic → agent/semantic`, `skill → agent/skill`, `adw → rag/adw`.
- **Orchestration canonicalized**: `orchestration/graph` is the canonical orchestration axis; the redundant `compose` / `process` / `flow` packages were removed.
- **Persistence consolidated**: unified into a single `Checkpointer` (`checkpoint/`).
- **De-duplication pushed down**: stream moved to `ai-core/streamx`, media to `ai-core/media`, security/sandbox/blobstore to `toolkit`; Schema now flows through `ai-core/schema` (`core.Schema` is a type alias).
- **New capabilities**: unified `runtime` (DurableExecution per-tool exactly-once + Resume, Budget/CostControl, five-level PermissionMode, Context Compaction), `agent.AgentTool`, MCP auto-reconnect, Langfuse exporter in `observe/otel`, compile-time type checks in `orchestration/chain`, a Parser layer in `rag/loader`, and native json_schema in `llm/structured`.
- **examples is a standalone module**: `go get github.com/hexagon-codes/hexagon` no longer pulls in the examples dependency graph.

> A `deprecated.go` top-level re-export shim is kept during the transition and will be removed in the next major version.

### Top-Level API Slimmed Down (v0.3.2-beta)

The exported symbols in `hexagon.go` have been trimmed from 98 down to **18 essential symbols**, keeping only the most commonly used entry points:

- `Chat()`, `ChatWithTools()`, `Run()` — convenience functions
- `QuickStart()` and option functions (`WithProvider`, `WithTools`, `WithSystemPrompt`, `WithMemory`)
- `NewTool()` — tool creation
- `SetDefaultProvider()` — set the default LLM provider
- Core type re-exports (`Input`, `Output`, `Tool`, `Memory`, `Message`, `Agent`, `Provider`)
- `Version` constants

All other exports have been moved to `deprecated.go` with deprecation comments and **will be removed in the next major version**.

**Migration:** Import the corresponding sub-packages directly instead of accessing them through the top-level package. For example:

```go
// Old way (deprecated)
team := hexagon.NewTeam("my-team", hexagon.WithAgents(a1, a2))
engine := hexagon.NewRAGEngine(hexagon.WithRAGStore(store))

// New way (recommended)
import "github.com/hexagon-codes/hexagon/agent"
import "github.com/hexagon-codes/hexagon/rag"

team := agent.NewTeam("my-team", agent.WithAgents(a1, a2))
engine := rag.NewEngine(rag.WithStore(store))
```

### Bug Fixes & Improvements

- **`RunWithStats` is now concurrency-safe** — uses local node copies, eliminating data races across goroutines
- **`ParallelForEachLoopNode` no longer deadlocks** — fixed deadlock on context cancellation
- **`RecursiveSplitter` guards against infinite loops** — automatic protection when overlap >= chunkSize
- **`SetDefaultProvider` timing fix** — now respected even if called before `Chat()`/`QuickStart()`

## 📚 Documentation

### 📄 Core Docs

| Document | Description |
|-----|------|
| [Quick Start](docs/QUICKSTART.md) | Get up and running with Hexagon in 5 minutes |
| [Architecture Design](docs/DESIGN.md) | Framework design philosophy and architecture |
| [API Reference](docs/API.md) | Complete API documentation |
| [Stability Guide](docs/STABILITY.md) | API stability and versioning policy |
| [Framework Comparison](docs/comparison.md) | Comparison with mainstream frameworks |

### 📖 Guides

| Guide | Description |
|-----|------|
| [Getting Started](docs/guides/getting-started.md) | Build your first Agent from scratch |
| [Agent Development](docs/guides/agent-guide.md) | Complete Agent development guide |
| [Advanced Agents](docs/guides/agent-development.md) | Advanced Agent development patterns |
| [RAG System](docs/guides/rag-guide.md) | Introduction to retrieval-augmented generation |
| [RAG Integration](docs/guides/rag-integration.md) | Deep integration of the RAG system |
| [Graph Orchestration](docs/guides/graph-orchestration.md) | Orchestrating complex workflows |
| [Multi-Agent](docs/guides/multi-agent.md) | Multi-Agent collaboration systems |
| [Plugin Development](docs/guides/plugin-guide.md) | Plugin system usage guide |
| [Observability](docs/guides/observability.md) | Tracing, metrics, and logging integration |
| [Security](docs/guides/security.md) | Security best practices |
| [Performance Optimization](docs/guides/performance-optimization.md) | Performance tuning guide |

### 💻 Examples

| Example | Description |
|-----|------|
| [examples/quickstart](examples/quickstart) | Quick start example |
| [examples/react](examples/react) | ReAct Agent example |
| [examples/rag](examples/rag) | RAG retrieval example |
| [examples/graph](examples/graph) | Graph orchestration example |
| [examples/team](examples/team) | Multi-Agent team example |
| [examples/handoff](examples/handoff) | Agent handoff example |
| [examples/chatbot](examples/chatbot) | Chatbot example |
| [examples/code-review](examples/code-review) | Code review example |
| [examples/data-analysis](examples/data-analysis) | Data analysis example |
| [examples/qdrant](examples/qdrant) | Qdrant vector store example |
| [examples/devui](examples/devui) | Dev UI example |

## 🖥️ Dev UI

A built-in development and debugging interface to inspect Agent execution in real time.

```go
import "github.com/hexagon-codes/hexagon/observe/devui"

// Create DevUI
ui := devui.New(
    devui.WithAddr(":8080"),
    devui.WithMaxEvents(1000),
)

// Start the service
go ui.Start()

// Visit http://localhost:8080
```

**Running the example:**

```bash
# Start the backend
go run examples/devui/main.go

# Start the frontend (hexagon-ui)
cd ../hexagon-ui
npm install
npm run dev
# Visit http://localhost:5173
```

## 🚢 Deployment

Hexagon provides three deployment options, covering everything from local development to production:

| Option | Use Case | Command |
|------|---------|------|
| Docker Compose (full mode) | Quick demo, standalone deployment | `make up` |
| Docker Compose (dev mode) | Team development (reusing docker-dev-env) | `make dev-up` |
| Helm Chart | Kubernetes clusters, production | `make helm-install` |

### Docker Quick Start

```bash
cd deploy
cp .env.example .env
# Edit .env and fill in your LLM API Key
make up

# Access
# Main app:  http://localhost:8000
# Dev UI:    http://localhost:8080
```

### Kubernetes / Helm

```bash
cd deploy
make helm-install

# Using external infrastructure
helm install hexagon helm/hexagon/ \
  -n hexagon --create-namespace \
  --set qdrant.enabled=false \
  --set external.qdrant.url=http://my-qdrant:6333
```

See the [Deployment Guide](deploy/README.md) for details.

## 🔨 Development

```bash
make build   # build
make test    # run tests
make lint    # lint
make fmt     # format
```

## 🤝 Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) to learn how to get involved.

## 📜 License

[Apache License 2.0](LICENSE)

```
Copyright 2026 hexagon-codes

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```
