# Changelog

本文件记录 hexagon（AI Agent 框架）的用户可见变更，遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 与 [SemVer](https://semver.org/lang/zh-CN/)。

## [Unreleased]
### Added
- runtime：DurableExecution（Resume 真 exactly-once）、三维 Budget + CostControl 统一抽象、SessionLane（分布式租约 + fencing）、错误模型 errkind、五级 PermissionMode、Context Compaction、长任务进度事件。
- compose：声明式 Pipe + Fallback/Retry/CircuitBreaker 弹性组合。
- orchestration/chain：Compile 期 I/O 类型校验（三规则）。
- rag/loader：文档 Parser 层 + 多模态 VLM seam。
- observe/otel：gen_ai 语义约定 + 全管线 Span 树 + Langfuse 导出器。
- mcp：动态发现 + 自动重连指数退避 + 多传输（stdio/SSE/HTTP）。
- evaluate/agenteval：Agent 质量基线。
- llm/structured：原生 json_schema 强制解码。

### Changed
- 持久化收敛为单一 Checkpointer；编排正统化（裁剪 advisor/process/flow）；stream 下沉 ai-core/streamx；媒体下沉 ai-core/media；安全/沙箱/blobstore 下沉 toolkit。

### Security
- mcp HTTP server 增加 ReadHeaderTimeout（防 Slowloris）。

## [0.4.8]
- 基线版本。
