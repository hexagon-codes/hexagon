# Changelog

本文件记录 hexagon（AI Agent 框架）的用户可见变更，遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 与 [SemVer](https://semver.org/lang/zh-CN/)。

## [Unreleased]

## [0.5.1]
> 维护版本：依赖升级 + 安全/并发缺陷修复。hexagon 公开 API 不变（SemVer patch）。

### Fixed
- **security/pii**：脱敏改为合并**任意重叠**的检测区间（此前仅去重完全相同区间）。部分重叠的检测结果（如电话与信用卡覆盖同一段数字）按各自偏移替换时会相互错位、把未脱敏的原始片段重新拼回输出，造成 PII 泄漏；现 `dedup`/`Anonymize` 共用合并逻辑并按区间字节回填 `Value`，杜绝错位残留。
- **tool/sandbox**：`executeDocker` 补齐附加文件路径穿越校验（拒绝 `..` 穿越与绝对路径），与 `executeProcess` 对齐，修复经 `filepath.Join` 写出临时目录、逃逸到宿主机文件系统的风险；守卫精确化为仅拦截真实穿越，不再误杀字面以 `..` 开头的合法文件名。
- **interrupt**：`WithDefault`/`WithValidator` 的类型擦除断言改为受检断言——类型不匹配返回 `ErrInvalidResume`，不再裸断言 panic。
- **runtime/runner**：工具去重作用域收敛为「仅续跑补跑」。正常热路径不再跨 turn 按 ID 跳过工具（provider 复用同一 tool-call ID 时仍执行并补配对结果，维持 tool_call/result 配对契约）；MaxTurns 耗尽时返回携带已计费用量/推理/工具记录的**部分结果**而非 `nil`。
- **agent/react**：`finishRun` 在运行时回传部分结果（如 MaxTurns）时上浮为部分 `Output`，便于调用方恢复部分工作与已计费用量。
- **llm/batch**：`OnBatchComplete` 改为在响应交付**之前**触发，消除 `BatchSubmit` 返回时完成回调可能尚未执行的时序竞态（修复 `TestCallbacks_Invoked` 约 20% flaky）。

### Changed
- 依赖升级：ai-core v0.1.4 → **v0.1.6**、toolkit v0.1.0 → **v0.2.0**（lockstep）。toolkit v0.2.0 的 `crypto/sign` `APISigner` wire 格式破坏性变更**不影响 hexagon**——本框架仅使用 `HMACSHA256` 原语，未引用 `APISigner`。

### Security
- security/pii 重叠区间泄漏修复属脱敏正确性加固（防 PII 残留）。
- tool/sandbox Docker 路径穿越守卫修复宿主机文件系统逃逸面。

## [0.5.0]
> 架构重构版本，升级依赖到 ai-core v0.1.4 + toolkit v0.1.0（Go 1.25）。

### Added
- runtime：统一 Agent 执行运行时（Runner + 执行策略）、DurableExecution（Resume 真 per-tool exactly-once）、三维 Budget + CostControl 统一抽象、SessionLane（分布式租约 + fencing）、错误模型 errkind、五级 PermissionMode、Context Compaction、长任务进度事件、SSE 事件 sink。
- agent：AgentTool（Agent 即工具的组合原语）+ Supervisor 调度。
- core：声明式 Compose（Pipe）+ Fallback/Retry/CircuitBreaker 弹性组合。
- orchestration/chain：Compile 期 I/O 类型校验（三规则）。
- rag/loader：文档 Parser 层 + 多模态 VLM seam。
- observe/otel：gen_ai 语义约定 + 全管线 Span 树 + Langfuse 导出器。
- mcp：动态发现 + 自动重连指数退避 + 多传输（stdio/SSE/HTTP）。
- evaluate/agenteval：Agent 质量基线。
- llm/structured：原生 json_schema 强制解码。

### Changed
- **BREAKING** 顶层 feature 包归组：`a2a → agent/a2a`、`artifact → agent/artifact`、`semantic → agent/semantic`、`skill → agent/skill`、`adw → rag/adw`（迁移期 `deprecated.go` 仍重导出旧入口）。
- **BREAKING** 裁剪冗余包：删除 `compose`/`process`/`flow`，以 `orchestration/graph` 为编排正统轴。
- 持久化收敛为单一 Checkpointer；stream 下沉 ai-core/streamx；媒体下沉 ai-core/media；安全/沙箱/blobstore 下沉 toolkit；Schema 统一走 ai-core/schema（`core.Schema` 类型别名）。
- 删除死代码 `core/schema.go`。
- examples 拆为独立 module（`go get hexagon` 不再拉入示例依赖图）。
- 依赖升级：ai-core v0.1.3 → v0.1.4、toolkit v0.0.6 → v0.1.0、Go → 1.25。

### Security
- mcp HTTP server 增加 ReadHeaderTimeout（防 Slowloris）。

## [0.4.8]
- 基线版本。
