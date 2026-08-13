<div align="right">语言: 中文 | <a href="README.en.md">English</a></div>

# Hexagon 使用指南

本目录汇总 Hexagon 的入门、Agent、RAG、编排、协作、插件和运维指南。指南用于说明常见用法；导出 API、依赖版本和实际行为分别以当前源码、[`go.mod`](../../go.mod) 和测试为准。

## 生态与当前基线

| 模块 | 定位 | 根模块当前声明 |
|------|------|----------------|
| **hexagon** | AI Agent 框架核心 | Go `1.25.12` |
| [**ai-core**](https://github.com/hexagon-codes/ai-core) | AI 基础能力库 | `v0.2.10` |
| [**toolkit**](https://github.com/hexagon-codes/toolkit) | Go 通用工具库 | `v0.3.4` |

上表是当前根模块的依赖快照，后续更新以 [`go.mod`](../../go.mod) 为唯一准确信息源。

### `examples` 模块边界

[`examples/`](../../examples/) 有独立的 [`examples/go.mod`](../../examples/go.mod)，不属于根模块的发布表面。根目录执行的 `go build ./...` 或 `go test ./...` 不会自动覆盖这个嵌套模块；示例应在其目录中独立解析依赖和验证：

```bash
cd examples
GOWORK=off go test ./...
```

`examples/go.mod` 独立管理依赖版本，可能与根模块基线不同；升级根模块依赖时，需要单独决定是否同步并验证示例。

## 指南索引

### 入门与核心能力

- [**快速入门指南**](./getting-started.md)：安装、最简示例和核心概念。
- [**Agent 开发指南**](./agent-development.md)：创建 Agent、接入工具与记忆、配置和调试。
- [**Agent 能力指南**](./agent-guide.md)：Agent 类型、中间件、状态、团队与流式输出主题。
- [**RAG 集成指南**](./rag-integration.md)：加载、分割、向量存储、检索、重排序与合成流程。
- [**RAG 使用指南**](./rag-guide.md)：RAG 组件和完整管道的补充参考。

### 编排、协作与扩展

- [**图编排最佳实践**](./graph-orchestration.md)：条件分支、并行、中断恢复与检查点。
- [**多 Agent 协作指南**](./multi-agent.md)：角色、Team、Handoff、网络与共识。
- [**A2A 协议指南**](./a2a-protocol.md)：A2A 客户端、服务端、认证和 Agent 发现。
- [**插件开发指南**](./plugin-guide.md)：插件注册、生命周期、依赖解析和健康检查。

### 运维与质量

- [**可观测性集成指南**](./observability.md)：Hook 接入、OpenTelemetry、Prometheus、日志与 Dev UI。
- [**安全防护配置指南**](./security.md)：输入防护、PII、RBAC、成本控制和审计。
- [**性能优化指南**](./performance-optimization.md)：Agent、RAG、多 Agent 和运行时优化建议。

## 部署配置边界

[部署配置说明](../../deploy/README.md)只覆盖两类配置，不代表三种可直接运行的应用部署模式：

- **Docker Compose 本地基础设施**：只启动 Qdrant、Redis 和 PostgreSQL，不启动 Hexagon 应用或 Dev UI。
- **Helm 应用接入模板**：用于把使用者自行构建并验证的应用镜像接入 Kubernetes；安装前必须替换镜像并核对模板约定的入口、端口、健康检查和安全上下文。

本仓库当前不包含应用 Dockerfile，也不发布可直接部署的应用或 Dev UI 容器镜像。需要部署应用时，请先基于 Hexagon 库实现运行程序并生成自己的镜像。

## 其他资源

- [快速开始](../QUICKSTART.md)
- [API 文档](../API.md)
- [设计文档](../DESIGN.md)
- [稳定性说明](../STABILITY.md)
- [框架对比](../comparison.md)
- [示例代码](../../examples/)

## 获取帮助

- [GitHub Issues](https://github.com/hexagon-codes/hexagon/issues)

## 许可证

Hexagon 采用 Apache License 2.0 许可证开源。
