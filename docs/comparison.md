<div align="right">语言: 中文 | <a href="comparison.en.md">English</a></div>

# AI Agent 框架综合对比

本文档对比 Hexagon 与主流 AI Agent 框架的能力差异，帮助开发者选择合适的框架。

## 框架概览

| 维度 | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **语言** | **Go** | Python/JS | Python/JS | Python | **Go** | C#/Python | Java |
| **开发者** | hexagon-codes | LangChain Inc. | LangChain Inc. | LlamaIndex Inc. | 字节跳动 | Microsoft | VMware |
| **协议** | Apache 2.0 | MIT | MIT | MIT | Apache 2.0 | MIT | Apache 2.0 |
| **定位** | 全能型 Agent 框架 | LLM 应用框架 | 图编排引擎 | RAG 数据框架 | 流式 AI 框架 | 企业级 AI 编排 | Spring 生态 AI |
| **生态** | hexagon + ai-core + toolkit | LangSmith + LangServe | LangSmith | LlamaHub | 独立 | Azure 生态 | Spring 生态 |

## 六维能力评分

Hexagon 聚焦 **易用性、性能、扩展性、任务编排、可观测性、安全性** 六大核心维度：

| 维度 | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| ⚡ **性能** | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| 🧩 **易用性** | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ |
| 🛡️ **安全性** | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| 🔧 **扩展性** | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| 🛠️ **编排力** | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ |
| 🔍 **可观测** | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ |

> **设计目标**：Hexagon 致力于实现各能力模块的均衡卓越，为 Go 开发者打造企业级落地首选的 AI Agent 开发基座。
>
> **当前状态**：v0.5.0，部分功能仍在完善中。生态系统和社区成熟度相比 LangChain/LlamaIndex 等成熟框架仍有差距。

## 核心特性对比

| 特性 | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **统一组件接口** | ✅ Component[I,O] | ✅ Runnable | ✅ | ✅ Component | ✅ Runnable | ✅ Kernel | ✅ |
| **原生流式处理** | ✅ Stream[T] | ✅ | ✅ | ⚠️ 部分 | ✅ 核心能力 | ✅ | ✅ |
| **编译时类型安全** | ✅ 泛型 | ❌ 运行时 | ❌ 运行时 | ❌ 运行时 | ✅ | ✅ | ✅ |
| **图编排引擎** | ✅ 完整 | ⚠️ 基础 | ✅ 核心能力 | ❌ | ✅ Graph | ✅ | ❌ |
| **检查点/恢复** | ⚠️ 基础 | ❌ | ✅ | ❌ | ⚠️ | ✅ | ❌ |
| **Human-in-Loop** | ✅ | ⚠️ 手动 | ✅ | ❌ | ⚠️ | ✅ | ⚠️ |

## LLM Provider 支持

| Provider | Hexagon (ai-core) | LangChain | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|----------|:-----------------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **OpenAI** | ✅ GPT-4/4o/o1/o3 | ✅ | ✅ | ✅ | ✅ | ✅ |
| **DeepSeek** | ✅ | ✅ | ⚠️ | ✅ | ⚠️ | ⚠️ |
| **Anthropic** | ✅ Claude | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Google Gemini** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| **通义千问** | ✅ | ✅ | ⚠️ | ✅ | ⚠️ | ⚠️ |
| **豆包 (Ark)** | ✅ | ⚠️ | ❌ | ✅ | ❌ | ❌ |
| **Ollama** | ✅ 本地模型 | ✅ | ✅ | ✅ | ✅ | ✅ |

## RAG 能力对比

| 能力 | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **文档加载器** | ✅ 10+ 格式 (含 XLSX/PPTX/OCR) | ✅ 丰富 | - | ✅ 最丰富 | ⚠️ 基础 | ⚠️ | ✅ |
| **语义分割** | ✅ 7 种分割器 | ✅ | - | ✅ 最强 | ⚠️ | ⚠️ | ⚠️ |
| **向量存储** | ✅ 8 种 (Qdrant/FAISS/PgVector/Redis/...) | ✅ | - | ✅ | ✅ | ✅ | ✅ |
| **混合检索** | ✅ Vector+Keyword+HyDE+Adaptive | ✅ | - | ✅ | ⚠️ | ⚠️ | ⚠️ |
| **重排序** | ✅ | ✅ | - | ✅ | ⚠️ | ❌ | ❌ |
| **响应合成策略** | ✅ Refine/Compact/Tree | ⚠️ 基础 | - | ✅ 最丰富 | ⚠️ | ⚠️ | ⚠️ |

> **注意**：Milvus 向量存储当前为实验性内存模拟实现，生产环境建议使用 Qdrant 或 Chroma。

## 多 Agent 能力对比

| 能力 | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **角色系统** | ✅ Role | ❌ | ❌ | ❌ | ❌ | ✅ Persona | ❌ |
| **团队协作** | ✅ 4种模式 | ❌ | ⚠️ 手动 | ❌ | ⚠️ | ⚠️ | ❌ |
| **Agent 通信** | ✅ A2A 协议 | ❌ | ✅ 消息传递 | ❌ | ⚠️ | ⚠️ | ❌ |
| **Handoff 交接** | ✅ | ❌ | ✅ | ❌ | ❌ | ⚠️ | ❌ |
| **共识机制** | ✅ | ❌ | ⚠️ | ❌ | ❌ | ❌ | ❌ |

**Hexagon 团队协作模式：**
- Sequential (顺序执行)
- Hierarchical (层级分发)
- Collaborative (协作讨论)
- RoundRobin (轮询执行)

## 可观测性对比

| 能力 | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **OpenTelemetry** | ✅ 原生集成 | ⚠️ 集成 | ⚠️ 集成 | ⚠️ | ✅ | ✅ | ✅ |
| **Prometheus** | ✅ 原生 | ❌ | ❌ | ❌ | ⚠️ | ⚠️ | ✅ Micrometer |
| **钩子/回调** | ✅ 4层钩子 | ✅ Callbacks | ✅ | ⚠️ | ✅ Callbacks | ✅ Filters | ⚠️ |
| **Dev UI** | ✅ 内置免费 | ✅ LangSmith💰 | ✅ LangSmith💰 | ❌ | ❌ | ❌ | ❌ |
| **全链路追踪** | ✅ | ⚠️ | ⚠️ | ⚠️ | ✅ | ✅ | ✅ |

**Hexagon 钩子系统：**
- RunHook (Agent 执行)
- ToolHook (工具调用)
- LLMHook (LLM 调用)
- RetrieverHook (检索调用)

## 安全防护对比

| 能力 | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **Prompt 注入检测** | ✅ Guard Chain | ⚠️ 第三方 | ⚠️ | ⚠️ | ❌ | ✅ | ⚠️ |
| **PII 脱敏** | ✅ Luhn 校验 | ⚠️ 第三方 | ⚠️ | ❌ | ❌ | ✅ | ❌ |
| **RBAC 权限** | ✅ | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ Spring |
| **成本控制** | ✅ | ❌ | ❌ | ❌ | ❌ | ⚠️ | ❌ |
| **沙箱隔离** | ✅ | ⚠️ | ⚠️ | ❌ | ❌ | ⚠️ | ❌ |
| **审计日志** | ✅ | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ |

## 部署运维对比

| 特性 | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **镜像大小** | ~20MB | ~500MB+ | ~500MB+ | ~500MB+ | ~30MB | ~200MB+ | ~200MB+ |
| **启动时间** | <100ms | 2-5s | 2-5s | 2-5s | <100ms | 5-15s | 5-15s |
| **并发能力** | 100k+ Agent | GIL 限制 | GIL 限制 | GIL 限制 | 100k+ | 线程池 | 线程池 |
| **单二进制部署** | ✅ (含 Docker/Helm) | ❌ | ❌ | ❌ | ✅ | ❌ | ❌ |
| **内存占用** | 低 | 高 | 高 | 高 | 低 | 中 | 中 |

## 开发体验对比

| 特性 | Hexagon | LangChain | LangGraph | LlamaIndex | Eino | Semantic Kernel | Spring AI |
|------|:-------:|:---------:|:---------:|:----------:|:----:|:---------------:|:---------:|
| **最简代码** | 3 行 | 15 行 | 30 行 | 10 行 | 10 行 | 20 行 | 15 行 |
| **学习曲线** | <1 小时 | 4-8 小时 | 8-16 小时 | 2-4 小时 | 2-4 小时 | 4-8 小时 | 2-4 小时 |
| **类型提示** | ✅ 编译时 | ⚠️ 运行时 | ⚠️ 运行时 | ⚠️ 运行时 | ✅ 编译时 | ✅ 编译时 | ✅ 编译时 |
| **文档质量** | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| **中文文档** | ✅ 原生 | ⚠️ 社区 | ⚠️ 社区 | ⚠️ 社区 | ✅ 原生 | ⚠️ 社区 | ⚠️ 社区 |
| **生态丰富度** | ⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ |

## 图例说明

| 符号 | 含义 |
|:----:|------|
| ✅ | 完整支持 |
| ⚠️ | 部分支持 |
| ❌ | 不支持 |
| 💰 | 付费功能 |
| - | 不适用 |

## 适用场景推荐

| 场景 | 首选 | 备选 |
|------|------|------|
| **Go 技术栈** | Hexagon | Eino |
| **Java/Spring 技术栈** | Spring AI | - |
| **.NET 企业应用** | Semantic Kernel | - |
| **复杂 RAG 应用** | LlamaIndex | Hexagon |
| **复杂工作流编排** | LangGraph | Hexagon |
| **高性能流式处理** | Eino | Hexagon |
| **快速原型/学习** | LangChain | Eino |
| **高并发生产环境** | Eino | Hexagon |
| **企业级安全要求** | Semantic Kernel | Hexagon |

> **说明**：Hexagon 仍处于 beta 阶段，生产环境使用请充分测试。对于关键业务场景，建议先考虑社区成熟的方案。

## Hexagon vs Eino 详细对比

由于 Hexagon 和 Eino 都是 Go 语言 AI Agent 框架，这里做更详细的对比：

| 维度 | Hexagon | Eino |
|------|---------|------|
| **设计理念** | 六边形均衡，全能型 | 流式优先，组件化 |
| **核心接口** | Component[I,O] 泛型接口 | Runnable 接口 |
| **背景支持** | 开源社区项目 | 字节跳动内部验证 |
| **成熟度** | Beta 阶段 | 生产级 |
| **生态系统** | hexagon + ai-core + toolkit + hexagon-ui | 独立仓库 |
| **图编排** | ✅ 支持 (检查点基础实现) | ✅ 完整支持 |
| **多 Agent** | ✅ 角色/团队/通信/交接/共识 | ⚠️ 基础支持 |
| **RAG 能力** | ✅ 完整管道 (加载/分割/检索/重排/合成) | ⚠️ 基础检索 |
| **安全防护** | ✅ 注入检测/PII/RBAC/成本控制 | ❌ 需自行实现 |
| **可观测性** | ✅ OTel + Prometheus + Dev UI | ✅ OTel + Callbacks |
| **国产 LLM** | ✅ 通义/豆包/DeepSeek | ✅ 通义/豆包/DeepSeek |
| **生产验证** | ⚠️ 待验证 | ✅ 字节内部大规模使用 |

**选择建议**：
- 需要完整 Agent 能力（多 Agent、RAG、安全、可观测）→ **Hexagon**（请充分测试）
- 侧重流式处理和生产稳定性 → **Eino**
- 对稳定性要求高的生产环境 → 建议先评估 **Eino**

## Hexagon 差异化优势

### 🚀 核心特性

* ⚡ **高性能** │ 原生 Go 驱动，极致并发，支持 100k+ 活跃 Agent
* 🧩 **易用性** │ 声明式 API 设计，3 行代码极速构建基础原型
* 🛡️ **安全性** │ 企业级沙箱隔离，内置完备的权限管控与防护
* 🔧 **扩展性** │ 插件化架构，支持高度自定义的组件无缝集成
* 🛠️ **编排力** │ 强大的图编排引擎，轻松驾驭复杂的多级任务链路
* 🔍 **可观测** │ 深度集成 OpenTelemetry，实现全链路透明追踪

### 💡 设计理念

1. **渐进式复杂度** - 入门 3 行代码，进阶声明式配置，专家图编排
2. **约定优于配置** - 合理默认值，零配置可运行
3. **组合优于继承** - 小而专注的组件，灵活组合
4. **显式优于隐式** - 类型安全，编译时检查
5. **生产优先** - 内置可观测性，优雅降级

### 🌐 完整生态系统

| 仓库 | 说明 |
|-----|------|
| **hexagon** | AI Agent 框架核心 (编排、RAG、Graph、Hooks) |
| **ai-core** | AI 基础能力库 (LLM/Tool/Memory/Schema) |
| **toolkit** | Go 通用工具库 (lang/crypto/net/cache/util) |
| **hexagon-ui** | Dev UI 前端 (Vue 3 + TypeScript) |

## 技术对标

| 维度 | Hexagon 实现 | 对标方案 |
|------|-------------|---------|
| 易用性 | 3 行入门 + 渐进式 API | OpenAI Swarm 极简风格 |
| 性能 | Go 原生并发 + 零分配流处理 | 字节 Eino 流式架构 |
| 扩展性 | `Component[I,O]` 统一接口 | LangChain Runnable |
| 编排 | 图编排 + 多 Agent 协作 | LangGraph |
| 可观测 | 钩子 + OTel + Prometheus + Dev UI | LangChain Callbacks |
| 安全 | Guard Chain + RBAC + 成本控制 | Semantic Kernel Filters |
| RAG | 完整管道 + 多策略合成 | LlamaIndex |
