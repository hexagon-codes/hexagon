<div align="right">语言: 中文 | <a href="comparison.en.md">English</a></div>

# AI Agent 框架选型参考

本文档提供 Hexagon、LangChain、LangGraph、LlamaIndex、Eino、Semantic Kernel 与 Spring AI 的客观选型入口。它不做主观排名，也不代替针对实际业务的技术验证。

## 阅读边界

- 项目定位、主要实现语言和许可证以各项目的官方仓库为一手来源。
- Hexagon 能力仅按本仓库当前源码和文档列出；“存在实现”不等于已经满足特定业务的性能、安全或可靠性要求。
- 本仓库没有覆盖上述框架的统一 benchmark。镜像大小、启动时间、吞吐、延迟、内存、成本和学习时间没有可比测试数据，因此本文不提供数字或排序。
- 外部项目的功能、版本和支持范围会变化。进入选型或升级阶段时，应重新检查对应官方文档和发布说明。

> **Hexagon 当前状态**：项目仍处于 v0.x 阶段，API 稳定性按模块划分；具体承诺见 [API 稳定性说明](STABILITY.md)。

## 项目元信息

| 项目 | 主要实现 / 运行时 | 官方定位简述 | 许可证 | 一手来源 |
|------|-----------------|-------------|--------|---------|
| Hexagon | Go | 本仓库提供 Agent、编排、RAG、运行时、可观测性与安全相关模块 | Apache-2.0 | [官方仓库](https://github.com/hexagon-codes/hexagon) · [许可证](https://github.com/hexagon-codes/hexagon/blob/main/LICENSE) |
| LangChain | Python；JS/TS 为独立实现 | 用于构建 Agent 和 LLM 应用的框架 | MIT | [官方仓库](https://github.com/langchain-ai/langchain) · [许可证](https://github.com/langchain-ai/langchain/blob/master/LICENSE) |
| LangGraph | Python；JS/TS 为独立实现 | 面向长时、有状态 Agent 的底层编排框架 | MIT | [官方仓库](https://github.com/langchain-ai/langgraph) · [许可证](https://github.com/langchain-ai/langgraph/blob/main/LICENSE) |
| LlamaIndex | Python | 用于构建 Agent 应用的数据与文档框架 | MIT | [官方仓库](https://github.com/run-llama/llama_index) · [许可证](https://github.com/run-llama/llama_index/blob/main/LICENSE) |
| Eino | Go | 遵循 Go 习惯的 LLM 应用开发框架，提供组件、Agent 开发工具和编排 | Apache-2.0 | [官方仓库](https://github.com/cloudwego/eino) · [许可证](https://github.com/cloudwego/eino/blob/main/LICENSE-APACHE) |
| Semantic Kernel | .NET、Python、Java | 用于构建和编排 Agent 与多 Agent 系统的模型无关 SDK | MIT | [官方仓库](https://github.com/microsoft/semantic-kernel) · [许可证](https://github.com/microsoft/semantic-kernel/blob/main/LICENSE) |
| Spring AI | Java / Spring | 面向 AI 应用的 Spring 风格 API 与抽象 | Apache-2.0 | [官方仓库](https://github.com/spring-projects/spring-ai) · [许可证](https://github.com/spring-projects/spring-ai/blob/main/LICENSE.txt) |

表中“官方定位简述”是对项目官方说明的压缩转述，不表示本文对质量、成熟度或适用性的背书。

## Hexagon 当前源码可证能力

| 维度 | 当前实现 | 代码依据 |
|------|---------|---------|
| 执行抽象 | 泛型 `Runnable[I,O]` 覆盖 Invoke、Stream、Batch、Collect、Transform 与 BatchStream；`Component[I,O]` 为兼容接口 | [`core/runnable.go`](../core/runnable.go) |
| Agent | ReAct、Team、Swarm、Handoff、顺序/并行/循环 Agent 原语，以及团队共享记忆 | [`agent/`](../agent/) |
| 编排 | Graph 与 Workflow；Graph 包含流式执行、检查点/恢复、Barrier 和分布式执行相关实现 | [`orchestration/graph/`](../orchestration/graph/) · [`orchestration/workflow/`](../orchestration/workflow/) |
| RAG | 文档、加载、分割、索引、检索、嵌入和向量存储接口，以及对应子包实现 | [`rag/`](../rag/) |
| Provider 与基础能力 | LLM、Tool、Memory、Schema、流和 Qdrant 等能力来自固定版本的 ai-core；通用基础原语来自 toolkit | [`go.mod`](../go.mod) · [依赖拓扑](STABILITY.md#依赖稳定性) |
| 可观测性 | Hook Manager、追踪、指标、OpenTelemetry、Prometheus 和 Dev UI 相关实现 | [`hooks/`](../hooks/) · [`observe/`](../observe/) |
| 安全与预算 | Guard、PII、RBAC、成本控制、权限中间件，以及工具沙箱相关实现 | [`security/`](../security/) · [`runtime/middleware/`](../runtime/middleware/) · [`tool/sandbox/`](../tool/sandbox/) |
| 部署材料 | Docker Compose 与 Helm 配置 | [`deploy/`](../deploy/) |

上表只说明代码表面存在。正式采用前仍需用目标模型、数据、工具、权限边界和部署环境进行回归与故障验证。

## 客观选型维度

| 维度 | 需要回答的问题 | 建议证据 |
|------|---------------|---------|
| 团队与运行时 | 团队维护哪种语言和运行时？现有服务、构建和运维体系能否直接接入？ | 最小集成分支、构建记录、依赖清单 |
| Agent 与编排模型 | 业务需要单 Agent、确定性工作流、动态 Agent，还是多 Agent 协作？状态和控制流如何表达？ | 关键用户场景原型、状态机与失败路径测试 |
| 持久化与恢复 | 是否需要长任务、检查点、人工介入、幂等恢复或跨进程执行？ | 中断/恢复、重复执行、进程重启和网络故障测试 |
| 数据与 RAG | 数据源、解析、切分、检索、重排和向量存储的契约是什么？ | 代表性数据集上的召回、正确性、迁移和隔离测试 |
| 模型与工具 | 必需的模型、结构化输出、流式协议、Tool、MCP 或自定义 Provider 是否有可维护接缝？ | 针对固定版本的 API/协议合同测试 |
| 可观测与评估 | 是否能关联一次请求中的 Agent、模型、工具、检索和成本？ | Trace、指标、日志、评估样本和故障定位演练 |
| 安全边界 | 凭证、租户、工具权限、网络访问、沙箱、PII 和审计由哪一层负责？ | 威胁模型、权限矩阵、恶意输入和失效关闭测试 |
| 部署运维 | 目标环境如何构建、扩缩、升级、回滚和排障？ | 真实制品、启动/关闭、滚动升级和回滚演练 |
| API 稳定与治理 | 版本策略、弃用期、升级说明、维护责任和依赖更新节奏是否匹配？ | 发布记录、兼容测试、维护流程和升级演练 |
| 性能与成本 | 目标并发、延迟、吞吐、内存和模型成本预算是什么？ | 同一环境、同一模型、同一负载下自行执行 benchmark |

## 可比较的 POC 基线

为避免把框架差异与模型、网络或数据差异混在一起，建议候选方案使用同一套验证条件：

1. 固定模型与版本、API 端点、数据集、工具集合、并发模型和超时预算。
2. 实现相同的核心旅程，包括正常调用、流式输出、工具失败、取消、重试和恢复。
3. 记录正确性、P50/P95/P99 延迟、吞吐、峰值内存、模型调用量和失败恢复结果。
4. 对需要的 RAG、权限、可观测性和部署链路分别建立合同测试，不以演示代码代替验收。
5. 固定依赖版本并记录复现实验的硬件、操作系统、配置和原始结果。

只有在上述条件一致时，性能数字和选型结论才具有可比性。
