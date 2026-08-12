<div align="right">Language: <a href="comparison.md">中文</a> | English</div>

# AI Agent Framework Selection Reference

This document provides an objective starting point for evaluating Hexagon, LangChain, LangGraph, LlamaIndex, Eino, Semantic Kernel, and Spring AI. It does not rank them or replace validation against a real workload.

## Scope and Limitations

- Project positioning, primary implementation languages, and licenses come from each project's official repository.
- Hexagon capabilities are listed only when supported by the current source tree and documentation. The presence of an implementation does not prove that it meets a particular workload's performance, security, or reliability requirements.
- This repository has no unified benchmark covering these frameworks. There are no comparable measurements for image size, startup time, throughput, latency, memory, cost, or learning time, so this document provides no numbers or rankings for them.
- External projects change their features, versions, and support scope. Recheck their official documentation and release notes when selecting or upgrading a framework.

> **Current Hexagon status**: The project remains in the v0.x phase, with stability defined per module. See the [API Stability Policy](STABILITY.en.md) for the applicable guarantees.

## Project Metadata

| Project | Primary implementation / runtime | Concise official positioning | License | Primary sources |
|---------|----------------------------------|------------------------------|---------|-----------------|
| Hexagon | Go | This repository provides Agent, orchestration, RAG, runtime, observability, and security-related modules | Apache-2.0 | [Official repository](https://github.com/hexagon-codes/hexagon) · [License](https://github.com/hexagon-codes/hexagon/blob/main/LICENSE) |
| LangChain | Python; JS/TS is a separate implementation | A framework for building agents and LLM-powered applications | MIT | [Official repository](https://github.com/langchain-ai/langchain) · [License](https://github.com/langchain-ai/langchain/blob/master/LICENSE) |
| LangGraph | Python; JS/TS is a separate implementation | A low-level orchestration framework for long-running, stateful agents | MIT | [Official repository](https://github.com/langchain-ai/langgraph) · [License](https://github.com/langchain-ai/langgraph/blob/main/LICENSE) |
| LlamaIndex | Python | A data and document framework for building agentic applications | MIT | [Official repository](https://github.com/run-llama/llama_index) · [License](https://github.com/run-llama/llama_index/blob/main/LICENSE) |
| Eino | Go | A Go-conventional LLM application framework with components, an Agent development kit, and composition | Apache-2.0 | [Official repository](https://github.com/cloudwego/eino) · [License](https://github.com/cloudwego/eino/blob/main/LICENSE-APACHE) |
| Semantic Kernel | .NET, Python, Java | A model-agnostic SDK for building and orchestrating agents and multi-agent systems | MIT | [Official repository](https://github.com/microsoft/semantic-kernel) · [License](https://github.com/microsoft/semantic-kernel/blob/main/LICENSE) |
| Spring AI | Java / Spring | Spring-friendly APIs and abstractions for developing AI applications | Apache-2.0 | [Official repository](https://github.com/spring-projects/spring-ai) · [License](https://github.com/spring-projects/spring-ai/blob/main/LICENSE.txt) |

The “Concise official positioning” column paraphrases each project's own description. It is not an endorsement of quality, maturity, or fitness for a particular use case.

## Source-Verified Hexagon Surface

| Dimension | Current implementation | Source evidence |
|-----------|------------------------|-----------------|
| Execution abstractions | Generic `Runnable[I,O]` covers Invoke, Stream, Batch, Collect, Transform, and BatchStream; `Component[I,O]` is a compatibility interface | [`core/runnable.go`](../core/runnable.go) |
| Agents | ReAct, Team, Swarm, Handoff, sequential/parallel/loop Agent primitives, and team shared memory | [`agent/`](../agent/) |
| Orchestration | Graph and Workflow; the Graph package includes streaming execution, checkpoint/resume, barriers, and distributed execution-related implementations | [`orchestration/graph/`](../orchestration/graph/) · [`orchestration/workflow/`](../orchestration/workflow/) |
| RAG | Document, loading, splitting, indexing, retrieval, embedding, and vector-store interfaces plus related subpackages | [`rag/`](../rag/) |
| Providers and foundations | LLM, Tool, Memory, Schema, streaming, Qdrant, and related capabilities come from the pinned ai-core version; general-purpose primitives come from toolkit | [`go.mod`](../go.mod) · [Dependency topology](STABILITY.en.md#dependency-stability) |
| Observability | Hook Manager, tracing, metrics, OpenTelemetry, Prometheus, and Dev UI-related implementations | [`hooks/`](../hooks/) · [`observe/`](../observe/) |
| Security and budgets | Guards, PII handling, RBAC, cost control, permission middleware, and tool sandbox-related implementations | [`security/`](../security/) · [`runtime/middleware/`](../runtime/middleware/) · [`tool/sandbox/`](../tool/sandbox/) |
| Deployment assets | Docker Compose and Helm configurations | [`deploy/`](../deploy/) |

This table describes the available code surface only. Before adoption, validate it with the target models, data, tools, permission boundaries, and deployment environment.

## Objective Evaluation Dimensions

| Dimension | Questions to answer | Suggested evidence |
|-----------|---------------------|--------------------|
| Team and runtime | Which languages and runtimes does the team operate? Can the existing service, build, and operations stack integrate directly? | Minimal integration branch, build records, dependency inventory |
| Agent and orchestration model | Does the workload need a single Agent, deterministic workflows, dynamic Agents, or multi-agent collaboration? How are state and control flow represented? | Critical-user-journey prototype, state-machine and failure-path tests |
| Persistence and recovery | Are long-running work, checkpoints, human input, idempotent recovery, or cross-process execution required? | Interrupt/resume, duplicate execution, process restart, and network fault tests |
| Data and RAG | What are the contracts for data sources, parsing, splitting, retrieval, reranking, and vector storage? | Recall, correctness, migration, and isolation tests on representative data |
| Models and tools | Do required models, structured output, streaming protocols, Tools, MCP, or custom providers have maintainable integration seams? | API and protocol contract tests against pinned versions |
| Observability and evaluation | Can one request correlate Agent, model, tool, retrieval, and cost activity? | Traces, metrics, logs, evaluation samples, and troubleshooting exercises |
| Security boundaries | Which layer owns credentials, tenancy, tool permissions, network access, sandboxing, PII, and audit records? | Threat model, permission matrix, malicious-input and fail-closed tests |
| Deployment and operations | How will the target environment build, scale, upgrade, roll back, and troubleshoot the application? | Real artifacts, startup/shutdown, rolling-upgrade, and rollback exercises |
| API stability and governance | Do versioning, deprecation periods, upgrade guidance, ownership, and dependency cadence fit the team? | Release history, compatibility tests, maintenance process, and upgrade rehearsal |
| Performance and cost | What are the required concurrency, latency, throughput, memory, and model-cost budgets? | A benchmark run in the same environment with the same model and workload |

## Comparable POC Baseline

To avoid confusing framework differences with model, network, or data differences, evaluate candidates under the same conditions:

1. Pin the model and version, API endpoint, dataset, tool set, concurrency model, and timeout budget.
2. Implement the same critical journeys, including normal calls, streaming, tool failures, cancellation, retries, and recovery.
3. Record correctness, P50/P95/P99 latency, throughput, peak memory, model-call volume, and recovery outcomes.
4. Add contract tests for required RAG, permissions, observability, and deployment paths; do not use demo code as acceptance evidence.
5. Pin dependencies and record the hardware, operating system, configuration, and raw results needed to reproduce the experiment.

Performance measurements and selection conclusions are comparable only when these conditions are held constant.
