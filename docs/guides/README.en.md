<div align="right">Language: <a href="README.md">中文</a> | English</div>

# Hexagon Guides

This directory indexes Hexagon guides for getting started, agents, RAG, orchestration, collaboration, plugins, and operations. The guides explain common usage; treat the current source, [`go.mod`](../../go.mod), and tests as the authorities for exported APIs, dependency versions, and actual behavior, respectively.

## Ecosystem and Current Baseline

| Module | Role | Current Root-Module Declaration |
|--------|------|---------------------------------|
| **hexagon** | AI Agent framework core | Go `1.25.12` |
| [**ai-core**](https://github.com/hexagon-codes/ai-core) | Core AI capability library | `v0.2.10` |
| [**toolkit**](https://github.com/hexagon-codes/toolkit) | General-purpose Go toolkit | `v0.3.4` |

This table is a snapshot of the current root module. Treat [`go.mod`](../../go.mod) as the single source of truth for subsequent dependency updates.

### `examples` Module Boundary

[`examples/`](../../examples/) has its own [`examples/go.mod`](../../examples/go.mod) and is not part of the root module's release surface. Running `go build ./...` or `go test ./...` from the repository root does not automatically cover this nested module. Resolve dependencies and verify the examples from their own directory:

```bash
cd examples
GOWORK=off go test ./...
```

`examples/go.mod` manages dependency versions independently and may differ from the root-module baseline. When root dependencies change, decide separately whether to align and verify the examples.

## Guide Index

### Getting Started and Core Capabilities

- [**Getting Started Guide**](./getting-started.en.md): installation, a minimal example, and core concepts.
- [**Agent Development Guide**](./agent-development.en.md): creating agents, adding tools and memory, configuration, and debugging.
- [**Agent Capabilities Guide**](./agent-guide.en.md): agent types, middleware, state, teams, and streaming topics.
- [**RAG Integration Guide**](./rag-integration.en.md): loading, splitting, vector storage, retrieval, reranking, and synthesis.
- [**RAG User Guide**](./rag-guide.en.md): supplementary reference for RAG components and complete pipelines.

### Orchestration, Collaboration, and Extension

- [**Graph Orchestration Best Practices**](./graph-orchestration.en.md): conditional branches, parallel execution, resumption, and checkpoints.
- [**Multi-Agent Collaboration Guide**](./multi-agent.en.md): roles, teams, handoffs, networking, and consensus.
- [**A2A Protocol Guide**](./a2a-protocol.en.md): A2A clients, servers, authentication, and agent discovery.
- [**Plugin Development Guide**](./plugin-guide.en.md): plugin registration, lifecycle, dependency resolution, and health checks.

### Operations and Quality

- [**Observability Integration Guide**](./observability.en.md): hook integration, OpenTelemetry, Prometheus, logging, and Dev UI.
- [**Security Configuration Guide**](./security.en.md): input protection, PII, RBAC, cost controls, and auditing.
- [**Performance Optimization Guide**](./performance-optimization.en.md): agent, RAG, multi-agent, and runtime optimization guidance.

## Deployment Configuration Boundary

The [deployment configuration guide](../../deploy/README.en.md) covers only two kinds of configuration. They are not three directly runnable application deployment modes:

- **Docker Compose local infrastructure**: starts only Qdrant, Redis, and PostgreSQL; it does not start a Hexagon application or Dev UI.
- **Helm application-integration template**: integrates a user-built and user-verified application image with Kubernetes. Before installation, replace the images and verify the template's entry-point, port, health-check, and security-context contracts.

This repository currently contains no application Dockerfile and publishes no directly deployable application or Dev UI container image. To deploy an application, first build a runnable program with the Hexagon library and produce your own image.

## Other Resources

- [Quick Start](../QUICKSTART.en.md)
- [API Documentation](../API.en.md)
- [Design Document](../DESIGN.en.md)
- [Stability Notes](../STABILITY.en.md)
- [Framework Comparison](../comparison.en.md)
- [Example Code](../../examples/)

## Get Help

- [GitHub Issues](https://github.com/hexagon-codes/hexagon/issues)

## License

Hexagon is open source under the Apache License 2.0.
