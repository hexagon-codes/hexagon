<div align="right">Language: <a href="comparison.md">中文</a> | English</div>

# AI Agent Framework Comprehensive Comparison

This document compares Hexagon against mainstream AI Agent frameworks to help developers choose the right tool.

## Framework Overview

| Dimension | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **Language** | **Go** | Python/JS | Python/JS | Python | **Go** | C#/Python | Java |
| **Developer** | hexagon-codes | LangChain Inc. | LangChain Inc. | LlamaIndex Inc. | ByteDance | Microsoft | VMware |
| **License** | Apache 2.0 | MIT | MIT | MIT | Apache 2.0 | MIT | Apache 2.0 |
| **Focus** | All-around Agent framework | LLM application framework | Graph orchestration engine | RAG data framework | Streaming AI framework | Enterprise AI orchestration | Spring ecosystem AI |
| **Ecosystem** | hexagon + ai-core + toolkit | LangSmith + LangServe | LangSmith | LlamaHub | Standalone | Azure ecosystem | Spring ecosystem |

## Six-Dimension Capability Scores

Hexagon focuses on six core dimensions: **ease of use, performance, extensibility, task orchestration, observability, and security**:

| Dimension | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| ⚡ **Performance** | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| 🧩 **Ease of Use** | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ |
| 🛡️ **Security** | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| 🔧 **Extensibility** | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| 🛠️ **Orchestration** | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ |
| 🔍 **Observability** | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ |

> **Design Goal**: Hexagon strives for balanced excellence across all capability dimensions, providing Go developers with a production-ready AI Agent development foundation.
>
> **Current Status**: v0.5.0; some features are still being refined. Ecosystem and community maturity are still behind established frameworks like LangChain and LlamaIndex.

## Core Feature Comparison

| Feature | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **Unified component interface** | ✅ Component[I,O] | ✅ Runnable | ✅ | ✅ Component | ✅ Runnable | ✅ Kernel | ✅ |
| **Native streaming** | ✅ Stream[T] | ✅ | ✅ | ⚠️ Partial | ✅ Core capability | ✅ | ✅ |
| **Compile-time type safety** | ✅ Generics | ❌ Runtime | ❌ Runtime | ❌ Runtime | ✅ | ✅ | ✅ |
| **Graph orchestration engine** | ✅ Full | ⚠️ Basic | ✅ Core capability | ❌ | ✅ Graph | ✅ | ❌ |
| **Checkpoint / resume** | ⚠️ Basic | ❌ | ✅ | ❌ | ⚠️ | ✅ | ❌ |
| **Human-in-the-loop** | ✅ | ⚠️ Manual | ✅ | ❌ | ⚠️ | ✅ | ⚠️ |

## LLM Provider Support

| Provider | Hexagon (ai-core) | LangChain | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|----------|:-----------------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **OpenAI** | ✅ GPT-4/4o/o1/o3 | ✅ | ✅ | ✅ | ✅ | ✅ |
| **DeepSeek** | ✅ | ✅ | ⚠️ | ✅ | ⚠️ | ⚠️ |
| **Anthropic** | ✅ Claude | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Google Gemini** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Qwen (Tongyi)** | ✅ | ✅ | ⚠️ | ✅ | ⚠️ | ⚠️ |
| **Doubao (Ark)** | ✅ | ⚠️ | ❌ | ✅ | ❌ | ❌ |
| **Ollama** | ✅ Local models | ✅ | ✅ | ✅ | ✅ | ✅ |

## RAG Capability Comparison

| Capability | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **Document loaders** | ✅ 10+ formats (incl. XLSX/PPTX/OCR) | ✅ Rich | - | ✅ Most extensive | ⚠️ Basic | ⚠️ | ✅ |
| **Semantic splitting** | ✅ 7 splitters | ✅ | - | ✅ Most advanced | ⚠️ | ⚠️ | ⚠️ |
| **Vector stores** | ✅ 8 options (Qdrant/FAISS/PgVector/Redis/...) | ✅ | - | ✅ | ✅ | ✅ | ✅ |
| **Hybrid retrieval** | ✅ Vector+Keyword+HyDE+Adaptive | ✅ | - | ✅ | ⚠️ | ⚠️ | ⚠️ |
| **Reranking** | ✅ | ✅ | - | ✅ | ⚠️ | ❌ | ❌ |
| **Response synthesis** | ✅ Refine/Compact/Tree | ⚠️ Basic | - | ✅ Most extensive | ⚠️ | ⚠️ | ⚠️ |

> **Note**: The Milvus vector store is currently an experimental in-memory simulation. For production use, Qdrant or Chroma is recommended.

## Multi-Agent Capability Comparison

| Capability | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **Role system** | ✅ Role | ❌ | ❌ | ❌ | ❌ | ✅ Persona | ❌ |
| **Team collaboration** | ✅ 4 modes | ❌ | ⚠️ Manual | ❌ | ⚠️ | ⚠️ | ❌ |
| **Agent communication** | ✅ A2A protocol | ❌ | ✅ Message passing | ❌ | ⚠️ | ⚠️ | ❌ |
| **Handoff** | ✅ | ❌ | ✅ | ❌ | ❌ | ⚠️ | ❌ |
| **Consensus mechanism** | ✅ | ❌ | ⚠️ | ❌ | ❌ | ❌ | ❌ |

**Hexagon Team Collaboration Modes:**
- Sequential
- Hierarchical
- Collaborative
- RoundRobin

## Observability Comparison

| Capability | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **OpenTelemetry** | ✅ Native integration | ⚠️ Integration | ⚠️ Integration | ⚠️ | ✅ | ✅ | ✅ |
| **Prometheus** | ✅ Native | ❌ | ❌ | ❌ | ⚠️ | ⚠️ | ✅ Micrometer |
| **Hooks / Callbacks** | ✅ 4-layer hooks | ✅ Callbacks | ✅ | ⚠️ | ✅ Callbacks | ✅ Filters | ⚠️ |
| **Dev UI** | ✅ Built-in, free | ✅ LangSmith💰 | ✅ LangSmith💰 | ❌ | ❌ | ❌ | ❌ |
| **End-to-end tracing** | ✅ | ⚠️ | ⚠️ | ⚠️ | ✅ | ✅ | ✅ |

**Hexagon Hook System:**
- RunHook (Agent execution)
- ToolHook (tool invocation)
- LLMHook (LLM call)
- RetrieverHook (retrieval call)

## Security Comparison

| Capability | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **Prompt injection detection** | ✅ Guard Chain | ⚠️ Third-party | ⚠️ | ⚠️ | ❌ | ✅ | ⚠️ |
| **PII redaction** | ✅ Luhn validation | ⚠️ Third-party | ⚠️ | ❌ | ❌ | ✅ | ❌ |
| **RBAC** | ✅ | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ Spring |
| **Cost control** | ✅ | ❌ | ❌ | ❌ | ❌ | ⚠️ | ❌ |
| **Sandbox isolation** | ✅ | ⚠️ | ⚠️ | ❌ | ❌ | ⚠️ | ❌ |
| **Audit logging** | ✅ | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ |

## Deployment & Operations Comparison

| Feature | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **Image size** | ~20MB | ~500MB+ | ~500MB+ | ~500MB+ | ~30MB | ~200MB+ | ~200MB+ |
| **Startup time** | <100ms | 2-5s | 2-5s | 2-5s | <100ms | 5-15s | 5-15s |
| **Concurrency** | 100k+ agents | GIL-limited | GIL-limited | GIL-limited | 100k+ | Thread pool | Thread pool |
| **Single binary deployment** | ✅ (incl. Docker/Helm) | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ |
| **Memory footprint** | Low | High | High | High | Low | Medium | Medium |

## Developer Experience Comparison

| Feature | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **Minimal code** | 3 lines | 15 lines | 30 lines | 10 lines | 10 lines | 20 lines | 15 lines |
| **Learning curve** | <1 hour | 4-8 hours | 8-16 hours | 2-4 hours | 2-4 hours | 4-8 hours | 2-4 hours |
| **Type hints** | ✅ Compile-time | ⚠️ Runtime | ⚠️ Runtime | ⚠️ Runtime | ✅ Compile-time | ✅ Compile-time | ✅ Compile-time |
| **Documentation quality** | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| **Chinese documentation** | ✅ Native | ⚠️ Community | ⚠️ Community | ⚠️ Community | ✅ Native | ⚠️ Community | ⚠️ Community |
| **Ecosystem richness** | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ |

## Legend

| Symbol | Meaning |
|:----:|------|
| ✅ | Full support |
| ⚠️ | Partial support |
| ❌ | Not supported |
| 💰 | Paid feature |
| - | Not applicable |

## Use Case Recommendations

| Scenario | First Choice | Alternative |
|------|------|------|
| **Go tech stack** | Hexagon | Eino |
| **Java/Spring tech stack** | Spring AI | - |
| **.NET enterprise apps** | Semantic Kernel | - |
| **Complex RAG applications** | LlamaIndex | Hexagon |
| **Complex workflow orchestration** | LangGraph | Hexagon |
| **High-performance streaming** | Eino | Hexagon |
| **Rapid prototyping / learning** | LangChain | Eino |
| **High-concurrency production** | Eino | Hexagon |
| **Enterprise-grade security requirements** | Semantic Kernel | Hexagon |

> **Note**: Hexagon is still in beta. Please conduct thorough testing before using it in production. For mission-critical scenarios, consider more mature community solutions first.

## Hexagon vs Eino — Detailed Comparison

Since both Hexagon and Eino are Go-based AI Agent frameworks, here is a more detailed side-by-side comparison:

| Dimension | Hexagon | Eino |
|------|---------|------|
| **Design philosophy** | Hexagonal balance, all-around | Streaming-first, component-based |
| **Core interface** | `Component[I,O]` generic interface | `Runnable` interface |
| **Backing** | Open-source community project | Validated internally at ByteDance |
| **Maturity** | Beta stage | Production-grade |
| **Ecosystem** | hexagon + ai-core + toolkit + hexagon-ui | Standalone repository |
| **Graph orchestration** | ✅ Supported (basic checkpoint) | ✅ Full support |
| **Multi-Agent** | ✅ Role/Team/Communication/Handoff/Consensus | ⚠️ Basic support |
| **RAG capability** | ✅ Full pipeline (load/split/retrieve/rerank/synthesize) | ⚠️ Basic retrieval |
| **Security** | ✅ Injection detection/PII/RBAC/Cost control | ❌ Must implement manually |
| **Observability** | ✅ OTel + Prometheus + Dev UI | ✅ OTel + Callbacks |
| **Domestic LLMs** | ✅ Qwen/Doubao/DeepSeek | ✅ Qwen/Doubao/DeepSeek |
| **Production validation** | ⚠️ Pending validation | ✅ Large-scale internal use at ByteDance |

**Recommendation:**
- Need full Agent capabilities (multi-agent, RAG, security, observability) → **Hexagon** (please test thoroughly)
- Focus on streaming performance and production stability → **Eino**
- High-stability production environments → Evaluate **Eino** first

## Hexagon's Differentiating Strengths

### Core Features

* ⚡ **High Performance** | Native Go concurrency, supports 100k+ active agents
* 🧩 **Ease of Use** | Declarative API design, build a basic prototype in 3 lines of code
* 🛡️ **Security** | Enterprise-grade sandbox isolation with built-in access control and protection
* 🔧 **Extensibility** | Plugin-based architecture for seamless custom component integration
* 🛠️ **Orchestration** | Powerful graph orchestration engine for complex multi-level task pipelines
* 🔍 **Observability** | Deep OpenTelemetry integration for full end-to-end transparent tracing

### Design Philosophy

1. **Progressive complexity** - 3 lines to get started, declarative config for intermediate use, graph orchestration for experts
2. **Convention over configuration** - Sensible defaults, zero-config out of the box
3. **Composition over inheritance** - Small, focused components that compose flexibly
4. **Explicit over implicit** - Type-safe, compile-time verified
5. **Production-first** - Built-in observability, graceful degradation

### Complete Ecosystem

| Repository | Description |
|-----|------|
| **hexagon** | AI Agent framework core (orchestration, RAG, Graph, Hooks) |
| **ai-core** | AI capability library (LLM/Tool/Memory/Schema) |
| **toolkit** | Go general-purpose toolkit (lang/crypto/net/cache/util) |
| **hexagon-ui** | Dev UI frontend (Vue 3 + TypeScript) |

## Technical Benchmarks

| Dimension | Hexagon Implementation | Reference |
|------|-------------|---------|
| Ease of use | 3-line entry + progressive API | OpenAI Swarm minimalist style |
| Performance | Native Go concurrency + zero-allocation streaming | ByteDance Eino streaming architecture |
| Extensibility | `Component[I,O]` unified interface | LangChain Runnable |
| Orchestration | Graph orchestration + multi-agent collaboration | LangGraph |
| Observability | Hooks + OTel + Prometheus + Dev UI | LangChain Callbacks |
| Security | Guard Chain + RBAC + cost control | Semantic Kernel Filters |
| RAG | Full pipeline + multi-strategy synthesis | LlamaIndex |
