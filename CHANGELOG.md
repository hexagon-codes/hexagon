# Changelog

本文件记录 hexagon（AI Agent 框架）的用户可见变更，遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 与 [SemVer](https://semver.org/lang/zh-CN/)。

## [Unreleased]

## [0.5.7]
> 功能版本：工具执行**状态一等化** + **有序内容块流 Blocks**（保真多步 ReAct 交错）+ 截断 rune/字节安全 + ai-core v0.1.11 / toolkit v0.2.3 lockstep。hexagon 公开 API 仅新增字段（SemVer minor）。

### Added
- **runtime/agent**：`ToolResult` 新增一等字段 `Status`（`success`/`error`）与 `DurationMs`——由框架在执行点据 `execErr` 与 ai-core `tool.Result.Success` 判定并测量耗时，客户端据此渲染成功/失败，**无需对结果正文做字符串嗅探**。对齐 `StopReason` 的「执行真相一等化」范式。零值（空串/0）向后兼容老快照。
- **runtime/agent**：`Result.Blocks` / `Output.Blocks`（`template.Blocks`）——**有序内容块流**（`text`/`tool_use`/`tool_result` 按执行序交错），修复 `Content` 单串 + `ToolCalls` 扁平数组无法表达多步 `text↔tool` 交错的结构性缺陷。SDK 消费者据此按序渲染，缺字段时回退 `Content` + `ToolCalls`；`tool_result` 状态/错误透传同 id 的 `ToolCallRecord`。

### Fixed
- **agent、rag/extractor、tool/python、tool/sandbox、tool/shell**：结果/输出截断由字节切片 `s[:maxLen]` 改为 **rune/字节安全截断**（`agent` 侧按 `[]rune` 切分，其余委托 `toolkit/lang/stringx.TruncateBytes`）。原实现当上限落在多字节 UTF-8（CJK/emoji）中间时会切断码点、产出 `U+FFFD`（�）。

### Changed
- 依赖升级（lockstep）：ai-core v0.1.8 → **v0.1.11**（提供 `template.Block` 有序内容块模型 + `BlockBuilder`）；toolkit v0.2.1 → **v0.2.3**（提供 `stringx.TruncateBytes` 按字节上限 rune-safe 截断）。
- **version**：`devFallbackVersion` 同步至 `0.5.7`（开发期回退基线，单一来源）。

## [0.5.5]
> 维护版本：修复「Hexagon engine」版本号在装机构建里**消失 / 谎报成 hexclaw 版本**（BUG-20260626 R1+R2）。hexagon 公开 API 不变（SemVer patch）。

### Fixed
- **version (`resolveVersion`)**：修复 hexagon 作为引擎依赖下沉后，前端侧栏「Hexagon engine」版本号显示异常的两类问题。抽出纯函数 `resolveVersionFromBuildInfo` 便于测试。
  - **R1**：依赖分支无条件返回 `dep.Version`——go.work / 装机开发构建里 hexagon 依赖被上报为 `"(devel)"`，透传给前端再被过滤 → 版本号消失。现对依赖分支补齐 `"(devel)"` 与空版本守卫（与主模块分支对称）。
  - **R2**：命中 hexagon 依赖但版本无效时仅跳出、随后无条件落到 `info.Main.Version`——而装机 sidecar 主模块是 hexclaw，其 VCS 戳记（如 `"v0.4.6+dirty"`）能通过守卫 → 把 Hexagon engine 谎报成 hexclaw 版本（实测 0.4.6）。现命中 hexagon 依赖且版本无效时**直接回退** `devFallbackVersion`，绝不使用主模块版本。
- **version**：`devFallbackVersion` 同步至 `0.5.5`（开发期回退基线，单一来源）。

## [0.5.4]
> 维护版本：RAG 文本截断改为 **rune-safe**，避免 CJK 字节切断产出乱码（BUG-20260625 F-4）+ ai-core v0.1.8 lockstep。hexagon 公开 API 不变（SemVer patch）。

### Fixed
- **rag/agentic、rag/citation、rag/corrective、rag/extractor、rag/selfrag**：`truncateText` 改为 **rune-safe 截断**（委托 `toolkit/lang/stringx.SubString`）。原以字节切片 `text[:maxLen]`，当 `maxLen` 落在多字节 UTF-8 字符（如 CJK）中间时会切断码点、产出乱码（BUG-20260625 F-4）；无需截断时（`head == text`）保持原样返回、不追加省略号，与旧实现「`len<=maxLen` 原样返回」语义一致。

### Changed
- 依赖升级：ai-core v0.1.7 → **v0.1.8**（lockstep）——同源修复 `media/image.truncateForError` 的 rune-safe 截断（BUG-20260625 F-4），与本次 rag 侧修复一致。

## [0.5.3]
> 功能版本：`StopReason` 升为一等终止原因，**达到轮次上限不再是错误**（对齐 Anthropic/OpenAI `stop_reason` 语义）+ MCP stdio server 支持注入 env（数据连接器地基）。**破坏性**：删除 `runtime.ErrMaxTurns` / `runtime.KindMaxTurns`，调用方改读 `Result.StopReason`（详见 Removed）。

### Added
- **runtime**：新增一等类型 `StopReason`（`StopReasonEndTurn` / `StopReasonMaxTurns`）及 `Result.StopReason` 字段——表达「为什么停」的**唯一**机制，始终随 `Result` 返回，调用方据此呈现（如 `max_turns` 时提示「可继续」），无需 `errors.Is` 反查。
- **agent**：`Output.StopReason` 与运行时 `Result.StopReason` 对齐，`outputFromRuntime` / `agentruntimeResultFromState` 一并回填。
- **mcp**：新增 `ConnectStdioServerV2WithEnv`（及顶层别名 `ConnectMCPStdioWithEnv`），向 stdio MCP 子进程注入额外环境变量——MySQL/Redis 等数据连接器靠 env 配凭证（`MYSQL_HOST`/`MYSQL_PASSWORD` 等）；env 合并进 `os.Environ()`（确定性排序，保留 `PATH` 否则 `npx`/`uvx` 不可用），`env` 为空时保持继承父进程、不改既有行为。

### Changed
- **runtime/runner**：达到 `MaxTurns` 由「返回错误」改为「正常终止」——`Run`/`Stream` 返回 `nil` error，携带已累积用量/推理/工具记录的**部分结果**，并标注 `StopReason=max_turns`；终止信号走 `EventRunFinished`（截断完成）而非 `EventRunFailed`，与自然终态一致，由 payload 的 `StopReason` 区分。`stateResult`/`snapshotResult` 一并标注 `StopReason`。

### Removed
- **破坏性**：移除 `runtime.ErrMaxTurns` 与 `runtime.ErrorKind` 的 `KindMaxTurns`。轮次耗尽不再产生 error，故 `Classify` 不再返回 `KindMaxTurns`。**迁移**：调用方将 `errors.Is(err, runtime.ErrMaxTurns)` 改为判断 `result.StopReason == runtime.StopReasonMaxTurns`。

## [0.5.2]
> 维护版本：核心并发/退避/SSE/SSRF 原语统一委托 toolkit（消除多份重复实现的防护漂移）+ config 防路径穿越 + 依赖升级。注：`CircuitBreaker` 状态回调时序由同步改异步（详见 Changed），调用方如依赖回调同步落地请留意。

### Changed
- **core/CircuitBreaker**：状态机委托 `toolkit/util/circuit.Breaker`，移除自维护的 atomic 状态/计数。**行为变更**：`OnStateChange` 回调由同步改为**异步**投递（channel + 后台 goroutine）；`Allow()` 须与随后**恰好一次** `RecordSuccess()`/`RecordFailure()` 配对（半开探测门控契约，生产 `RunnableWithCircuitBreaker.Invoke/Stream` 已满足）。
- **orchestration/workflow**：`calculateRetryInterval` 退避委托 `toolkit/util/retry.ExponentialBackoff`，退避序列（`InitialInterval` ×`Multiplier` 封顶 `MaxInterval`）逐项不变。
- **runtime/sse**：`SSEEventSink` 事件帧（`event:`/`data:`）委托 `toolkit/net/sse.Writer`（SSE 头部 + 线程安全 flush 一致）；注释帧 `: <text>\n\n` 仍走手写以保 wire 字节不变。
- 依赖升级：toolkit v0.2.0 → **v0.2.1**（`net/httpx.RawClient` 默认遵循 `HTTP(S)_PROXY`/`NO_PROXY`）、ai-core v0.1.6 → **v0.1.7**（lockstep）。

### Security
- **tool/http、tool/browser**：SSRF 校验复用 `toolkit/net/ssrf.ValidateURL`（元数据/localhost 阻断 + DNS 解析逐 IP 私网检查，抗 DNS rebinding），替换各处手写私网名单；`browser` 此前缺 DNS 解析检查，此次一并加强（scheme 仅 http/https 的限制保留在本地）。
- **config/EnvironmentManager**：`LoadAgentConfig`/`LoadTeamConfig`/`LoadWorkflowConfig`/`LoadRawConfig`/`SaveConfig`/`CopyConfig` 补 `validateConfigName` 防路径穿越（拒绝 `..`/路径分隔符/绝对路径），堵 `filepath.Join(baseDir, …, name)` 逃逸 baseDir 的面（gosec G703）。

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
