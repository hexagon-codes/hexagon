# Changelog

本文件记录 hexagon（AI Agent 框架）的用户可见变更，遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 与 [SemVer](https://semver.org/lang/zh-CN/)。

## v0.6.0（2026-11-02）

- **graph / core**：消除StreamChannel发送与关闭竞态，为StreamRun终态独立预留槽位，组合流继续消费SourceEOF之后的数据。
- **a2a**：固定Stop所属服务器代次，拒绝停启交叠；停机期限不再被业务存储锁阻塞，宽限期限后取消在途HTTP上下文。
- **mcp**：在官方SDK把动态数字转为float64前保留工具Schema，标准HTTP和stdio往返保持精确数值及空约束。
- **browserruntime**：同轮Computer Use调用冻结模型输入截图尺寸，避免后续观察改变旧动作的坐标缩放。

- **agent**：补齐 Deep、ReAct、Sequential、Parallel、Loop、Reflection、Supervisor、SelfDiscovery、PlanExecute 的 Transform 关闭、取消及上游错误传播，保留各自 Run 算法。
- **graph**：流事件缓冲预留终止错误槽位；取消不再随机丢失 Error，消费者停止读取后取消仍可退出。
- **a2a**：Stop 关闭自行创建的默认 Store 清理协程；重启保留任务并恢复清理，注入 Store 继续由宿主管理。
- **mcp**：双向转换保留显式旧方言的 tuple items，不再静默放宽约束；复用 ai-core 的 Schema 表示，非法默认方言仍明确报错。

### Fixed

- core Retry/Fallback和Workflow步骤统一遵守中断、取消及未知结果的不可重放分类；Future并行组合保留完整错误树和结果，框架异步回调panic转为含栈失败。图的并行纯等待保存全部目标，StateMachine在取消边界停止新派发。
- Agent/Team流转换复用逐元素适配器的关闭与错误传播；同一Input的文本增量可按序Collect，Context冲突明确返回。结构化修正先核对模型非正常终态，保留原原因和已知用量。
- Artifact自动身份基于完整元组生成，避免会话/名称斜杠碰撞；A2A重复创建先查重，任务移动同步索引，配置存取隔离且只构造实际需要的默认存储；阻塞Handler不以Working静默结束SSE。
- MCP导入及导出保留Schema额外关键字；评估StopOnError停止后续派发，CustomEvaluator保留失败中的部分结果。同步事件、数值、子结果及未注册Concat的既有测试合同。
- A2A 任务存取和历史投影统一传播可失败复制的错误；不能隔离的值不被插入、覆盖或成功投影，失败创建不淘汰已有任务；常规 typed 数据、时间和大整数保持可用。

- MapReduce、Pregel 超级步与 Workflow ParallelStep 收敛分支事实并传递完整错误树，纯等待与混合失败分开决议；并行 ForEach 在实际 worker 中捕获 panic，保留普通失败重试及正常取消合同。
- 子 Agent 的 panic 在捕获点即标记不可盲重放，子输入和完整子输出保类型隔离；结果无法隔离时仍保留已执行结果与费用并明确禁止重派。
- MCP 联合类型及布尔 Schema 转换为等价约束，保留已有组合和递归引用；结构化结果工具不再把暂停、取消或未知结束原因改为自然完成。
- Artifact 兼容 Store 的读取故障不再被当作首次保存，避免覆盖旧版本；SQL Artifact 动态元数据读取保留 JSON 数字字面，评估结果采用时冻结可变对象。
- Fork 依据新运行的冻结请求重新计算入站摘要，跨 Store/标准 Submit 重复请求查回原记录，改变输入仍明确冲突。

- 并行组合收敛全部已派发分支并保留有序状态和完整错误树；成功分支或未知结果不盲重放，全部普通失败仍遵循重试策略。Pregel/All 按激活代次传播条件分支的 skipped 到达。
- Workflow hooks 和事件接收独立载荷并在实际回调边界处理 panic；完成前失败先持久化再通知，终态后观察失败保留已提交事实并显式返回。过期步骤结果不作为成功采用。
- 插件停止后重启在实际依赖代次变化时用冻结配置重新装配；Team 四种模式保留子结果、失败与费用，RoundRobin 不重放控制错误，已保存普通失败的恢复不抹去后续已保存事实。
- 内部 ModelSession 持有原预算起点，首次模型派发前保存；动态工具在计量前固定当轮绑定，迁移后的快照读取复用已验证载荷，不再由预算读取退回旧解码。
- MCP 双向保留递归 Schema 引用；A2A 阻塞调用对非法非终态响应返回标准错误并保留失败任务及产物，TaskStore 全部读取出口隔离嵌套值，未知 Part 保留原错误身份。
- Artifact 完整隔离嵌套 Metadata 并保留正常带时区时间；评估前固定唯一评估器身份，重复名称明确报错，关键失败不再被后续覆盖。

- Workflow 在实际步骤执行协程内记录 panic 失败；Workflow 与 Agent 工具适配统一遍历未知结果错误树，避免重复副作用。
- A2A 在原子准入时重读任务终态，拒绝旧快照重开已完成任务；Graph 流保留必要错误事件，Fork 使用原 StateCodec。
- 模型异常终态先于工具派发决议；失败 AfterTool 候选隔离完整结果，包装器重试撤回本次草稿并保留合法 Continue 前缀及用量。截断摘要不采用，压缩计量使用最终请求 Schema、工具和提示。
- 记忆记录、快照、历史和 Store 的 JSON 解码保留数值精度；插件实例保留实际依赖代次直到使用者排空，持久引用递归保存依赖。
- 原入站摘要独立于最新路由，重复 Submit 查回原接收；等待目标与快照原子保存，仅未消费的当前目标维持唤醒。预算起点在首次模型派发前持久化，并跨恢复累计。

本节对应本地代码交付目标；没有据此创建或推送 tag，也没有把本地候选 module proxy 验证写成 GitHub 或公共代理已可解析。应用可继续固定原版本，HexClaw 不要求升级；采用本版本的消费者自行完成下列迁移。

### BREAKING

- **Agent工具结果与动态数字**：AgentAsTool的Output为ChildRunResult，读取Child.Output.Content或ModelToolContent()；Memory Store动态JSON数值为json.Number，Go消费者调整float64断言并按需显式转换。JSON数值字面保持精度，完整子结果不再压成字符串。
- **自动产物ID**：新ID是不透明的完整元组身份；版本从Version字段读取。旧ID原样可读，显式ID的既有保存选择保持。
- **图状态复制**：`graph.State.Clone()` 改为 `(State, error)`；自定义状态和调用方必须处理复制错误，不能回退为共享可变引用。完整 `State` 返回值与声明写集分开，并行完整状态汇合需配置明确的 `StateMerger`，不能依赖最后完成者覆盖。
- **图模式**：默认构建模式为 DAG；循环图在 `Build` 前调用 `WithExecutionMode(GraphModePregel)`，需要时同时指定 `WithTriggerMode`。`RunPregelMode` 不再用于事后改变已编译模式；Run、Stream 与恢复消费同一计划。普通出边全部参与分发，一条路径到达 END 不代表全部分支结束。
- **压缩回调**：`middleware.Compactor` 改为 `func(context.Context, []llm.Message) ([]llm.Message, error)`。回调显式返回错误；`BeforeLLM` 保持中间件入口合同，向 Runner 透传错误，失败消息不被采用，原消息及实际摘要用量保留。
- **A2A 协议**：JSON-RPC 与 SSE 投影按 A2A 1.0 映射，不能继续发送旧 wire 载荷。消息/产物使用稳定身份，`DataPart.Data` 由 `map[string]any` 改为 `any`；直接索引的调用方应先按实际数据类型检查。内存对象与协议 wire 表示分开，旧持久 A2A JSON 需按源版本显式转换，不能仅更换协议版本常量。
- **构建基线**：最低 Go 为 `1.26.10`，依赖目标为 ai-core `v0.3.0`、Toolkit `v0.3.7`；根模块和独立 examples 模块分别维护依赖。只固定 Hexagon 版本不冻结其他直接依赖经 MVS 选出的版本。

### 持久数据与接入迁移

- Runtime 使用 `runtime.runspec.v1` 与 `runtime.snapshot.v1`，图执行持久封装使用 `graph.snapshot.v1`，同时保存实际 State codec、编译计划身份、pending result 与 adopted 状态。`Snapshot.Phase` 明确区分 `PhaseBeforeModel`、`PhasePendingTools`、`PhaseAfterTurn`、`PhaseFinal`，恢复按实际边界接续，不能仅凭 Step 统一加一导致跳轮。组件中断状态保存具体 type/version；宿主注册对应 codec 后恢复，不把 JSON map 当作原 Go 类型。
- `Snapshot.Codec` 标识当前快照载荷的编码，`RunSpec.Codec` 保留原始执行规格声明；已迁移载荷以自身 codec 读取，不能因原规格仍记录旧版本而重复迁移。`SnapshotCodecs.Register` 提供显式、无业务副作用的源格式迁移入口；迁移针对副本，失败保留原记录。旧轻量快照不自动获得任务、回执、原子提交和跨进程恢复保证；缺失原规格、资源版本或有效迁移时，应保留任务并报告原因，不从头重做付费动作。需要继续的旧任务可固定旧版本完成，fork 重执行须使用新运行身份并遵循原操作核对规则。
- `DurableRunner.Submit` 在接收前冻结实际选择的 Provider/Model 到 `RunSpec.SelectedProvider`；恢复使用原路由及资源合同，不能将请求别名当作实际路由，也不能静默切到当前 primary。合同允许的 fallback 仍按已固定策略处理。
- `ToolResult.Structured` 使用带 `Kind`、`Version`、`Data` 的 `StructuredPayload` DTO；消费者按明确版本解码，不依赖 `Raw` 在持久往返后仍实现原 Go 接口。`NeedsReconciliation` 表示原动作结果仍需核对，执行器可实现 `ToolResultReconciler` 查询原结果或幂等补登记，不得重新调用原业务动作；原失败 Task 保留，新核对 Task 独立保存其结果。
- 轻量 `Runner.Run/Stream` 可使用内存装配，不强制数据库。强持久装配由宿主注入 `*sql.DB`，显式调用 MySQL 适配层迁移，并提供 worker、执行权与接收/恢复入口；构造器不自动建表，也不新增独立持久化微服务。单实例内存/文件存储不等同多 worker 的数据库事务或机器掉电耐久。
- 必要事件与状态提交关联，已提交事件保留稳定 `Event.ID`/序号；`SSEEventSink.EmitStored` 输出已提交信封，`Follow` 按 EventStore 的序号追赶，消费者保留对应游标并去重。即时 token delta 没有持久恢复游标，仍按声明的 best-effort 合同处理。外部 Receipt、产物字节与运行状态分别核对，不承诺外部 exactly-once。
- 新增 `memory/store.ConditionalStore` 是可选能力，不扩大原 `MemoryStore` 方法集；需要条件更新的消费者显式使用该能力，并按实际后端声明原子范围。
- 新增可选 `RunSpec.IngressDigest`、`Snapshot.BudgetStartedAt`、`RunRecord/CommitRequest.WaitingTargets`，不需要新增 SQL 表。旧接收记录的摘要查回沿用原完整请求方案，不能伪造未保存的原入站事实；无预算起点的历史快照不能凭空补出原墙钟。等待目标由执行核声明，存储层不解析私有快照；已接收的同 ID Signal 保持幂等，新 ID 的已完成目标明确拒绝。

### Added

- **编排**：统一图执行计划、字段映射、可失败复制、确定性合并、逐任务结果保存/采用、子图与定向中断恢复；失败和未知结果不会因空队列或部分文本被当作成功。
- **runtime**：增加统一模型 attempt、受控请求变换、结构化结果验证、fallback 输出隔离、任务接收/提交/恢复与持久事件。`Request.Generation` 接受 ai-core 的 `GenerationOptions`；`Request.StructuredOutput` 固定最终结果约束，经对应模式验证采用后通过 `Result.Structured` 返回 JSON，不以 Content 看似 JSON 冒充已验证对象。
- **runtime/ModelSession**：`DefaultRunner.NewModelSession` 为摘要等内部模型任务独立取得资源 pin，持有自己的 State、事件和用量；宿主负责取消、等待与 `Close`。异步摘要使用独立会话，不能借用携带父运行活跃 State 的调用上下文；`Report` 的子调用事实不自动重复计入父账。
- **runtime/DynamicTools**：`NewDynamicTools` 创建 Run 局部绑定视图，由 `BindRun` 固定归属；`SnapshotTools`/`RestoreTools` 保存与还原定义及版本绑定。搜索任务的缓存结果也保留 `ToolSnapshot`，恢复不能只展示 schema 而缺少相同执行绑定，也不重新搜索最新实现来替代旧绑定。
- **runtime/middleware**：`CostControl.RecordAttempt` 优先于简便 `Record`，支持账本按实际 HTTP attempt ID 幂等入账；不能同时走两条累计路径。部分实报保留原始 usage 和缺失维度，不补造 TotalTokens；预算使用单独的已知用量下界，不把下界当作供应商实报总量。
- **agent**：公开 `NewToolExecutor(tools ...tool.Tool) runtime.ToolExecutor`，供标准 Runtime/DurableRunner 装配使用，保留子结果 DTO、卸载、可选核对及副作用分类能力；不公开或依赖 ReAct 的内部执行对象。工具句柄只读借用，连接与 Lease 的关闭责任仍归宿主。
- **agent / memory / artifact**：补充完整子任务结果、版本固定的文档 Skill、冻结记忆视图、历史窗口检索，以及工具结果/产物的完整回读和补登记能力。
- **plugin**：路由解析与 generation 使用权原子取得；候选装配成功后切换，旧运行持有资源至排空，准备失败归还资源，不用配置指针代替生命周期管理。
- **mcp**：新增共享 Tools、Resources、Prompts 与关闭生命周期的官方 SDK Session API，保留原 V2 连接签名；主动关闭本次已回收 stdio 进程时，预期 SIGTERM/SIGKILL 不再被误报为未关闭，其他退出与清理错误保持可见。

- **runtime**：新增 `DefaultRunner.Continue` 与 `ProviderCallError`，允许模型调用失败后使用重新装配的输入续接同次运行，并保留已完成工具结果、轮次和用量；新增 `ToolMediaMessageName` 标记运行时投影的工具媒体消息。
- **browserruntime**：提供通用浏览器会话、目标绑定、执行租约和官方 MCP 驱动；文件选择模态按会话及主体保留，支持跨手工调用接续与自有页面终态清理，权限与业务交付由调用方装配。
- **mcp**：Tools、Resources 与 Prompts 共用 Text／Image／Audio／Binary 投影，保留实际媒体字节并提供不含媒体编码的文本及描述信息；资源链接不主动下载。
- **runtime**：完整输出消息组保留 ProviderContinuation、工具身份及 typed 媒体；快照与压缩维护关联组，保留真实用量来源，流终态错误以权威 `Stream.Err()` 为准。

### Changed

- 当前根模块最低 Go 工具链为 `1.26.10`；运行时协议扩展按明确的候选依赖组合验证，正式发布依赖仍须单独确认可解析。

- 根模块与独立 `examples/` 模块的交付依赖目标为 ai-core **v0.3.0**、Toolkit **v0.3.7**。examples 使用 Hexagon **v0.6.0** 和 Go **1.26.10**，在无 `go.work`、无 `replace` 的独立模块中另行验证；不借旧 tag 编译当前新 API。

## [0.5.15] - 2026-10-21

> **版本策略例外：** 本版本以 patch 版本号交付 MCP 混合结果的输出表示变更；迁移要求见下方 `BREAKING`。该例外不改变后续 patch 的公开合同兼容承诺。

### Added

- **mcp**：新增 `PreservedToolResult`，保留完整的 MCP 协议结果；`ToolText()` 提供不含图片编码的文本投影，`ToolMessageContent()` 将返回图片投影为模型消息内容。
- **runtime**：成功工具结果的 `Raw` 实现 `ToolMessageContent() []llm.ContentPart` 时，将返回媒体及工具来源说明加入后续模型输入。
- **runtime/middleware/compaction**：新增 `ShouldCompact` 自定义触发条件及 `HeuristicCompress`、`RecentMessageBounds`、`HeuristicToolSummary`，分别支持启发式压缩、完整工具组窗口边界和工具交互摘要。

### Fixed

- **runtime**：将本轮实际产出与可压缩的模型输入分离，避免压缩造成历史回答混入或本轮内容丢失。新快照通过可选 `output_messages` 字段保存本轮产出；旧快照仍可读取，但没有该字段时沿用旧的输出恢复路径。
- **runtime/middleware/compaction**：启发式压缩保留用户原文与多模态请求，按工具调用组保留最近交互；窗口边界按调用 ID 找回对应调用，摘要按 ID 配对结果。默认 token 估算继续使用既有的 ai-core 中文感知实现，本次修正相关注释。

### BREAKING

- **MCP 输出表示**：同时含文本与结构化内容的成功结果，其 `tool.Result.Output` 由字符串变为 `*mcp.PreservedToolResult`；含非文本内容的成功结果也使用此类型。直接断言 `Output.(string)` 的调用方应按结果类型分支处理，使用 `ToolText()` 取得文本投影，或读取内嵌的 `CallToolResult`。纯文本结果仍返回字符串，纯结构化结果仍直接返回结构化内容；`isError` 仍返回失败结果及非 nil 错误，并在 `Output` 保留完整协议结果。

## [0.5.14] - 2026-09-14

### Added

- **mcp**：新增 `ProtocolError` 与 `StdioConnectError`，通过 `errors.As` 提供 `connect`、`initialize`、`tools/list` 阶段和可用的子进程退出状态、有限 stderr 摘要；`Unwrap` 保留原始错误链，调用方可继续使用 `errors.Is` 判断取消等原因。

### Fixed

- **MCP 工具发现与结果**：通过官方 SDK 迭代器读取全部分页工具；成功结果只有 `structuredContent`、没有文本内容时保留结构化输出。
- **MCP 连接生命周期**：初始化失败时关闭已建立的传输，工具枚举失败时关闭会话；stdio 子进程由 SDK 统一回收，设置 5 秒 `WaitDelay`，避免继承 stderr 的子进程使清理无界等待。退出状态从 SDK 连接关闭结果取得。
- **MCP 网络收尾**：SSE 和 Streamable HTTP 便捷入口为取消通知和会话 DELETE 请求分别设置 5 秒时限，释放响应体；不把普通工具调用或长连接统一限制为 5 秒。
- **CI 依赖判定**：只读模块模式继续验证实际依赖；不再用 `go mod tidy -diff` 阻断仅含冗余历史校验和的提交。

### Changed

- 根模块 ai-core 依赖由 **v0.2.10** 更新至 **v0.2.11**；最低 Go 版本仍为 **1.25.12**，toolkit 仍为 **v0.3.4**，`examples/` 独立模块依赖不变。
- **CI**：最低 Go 版本从 `go.mod` 读取，执行格式检查及 `go test -count=1 -vet=all ./...`；最新稳定 Go 版本承担 race 和 `govulncheck`，不再固定当前版工具链。依赖更新仍应执行 `go mod tidy`，普通业务发版无需修改 workflow 或工具版本。
- **文档与部署元数据**：同步中英文依赖基线、贡献/发布流程、MCP API 与生命周期说明；Helm Chart 更新为 **0.1.3**，`appVersion` 更新为 **0.5.14**。Chart 仍是自定义应用接入模板，不表示仓库已发布对应容器镜像。

### Security

- **MCP 诊断**：补齐引号包裹的凭据字段、URL 凭据以及 Bearer/Basic 认证文本的脱敏，诊断截断保持有效 UTF-8；不序列化传入的环境变量映射。

## [0.5.13] - 2026-08-14

### Fixed
- **testing/record**：`Recorder` 与 `Replayer` 现保留 ai-core 的可取消 Token 计数能力。支持 `llm.ContextTokenCounter` 的底层 Provider 会收到原始 context 与消息；仅支持旧接口的底层或回退 Provider 会快速返回 `llm.ErrContextTokenCountingUnsupported`，不再隐式执行无法中途取消的同步计数。无回退 Provider 的 `Replayer` 继续使用既有本地估算，并响应 context 取消。

## [0.5.12] - 2026-08-13

> **BREAKING / 版本策略例外：** 本批次包含相对 v0.5.9 的公开 API、最低 Go 版本与 Qdrant 持久数据合同变更。v0.5.10 与 v0.5.11 已错误地以 patch 版本发布，且提交拓扑与版本顺序倒置。维护者确认当前框架尚无外部使用者，批准 v0.5.12 作为一次性例外并撤回这两个异常版本；该例外不改变后续 v0.5.x patch 的兼容性承诺。

### Changed
- 根模块依赖相对 v0.5.9 升级：ai-core v0.2.4 → **v0.2.10**、toolkit v0.2.6 → **v0.3.4**；最低 Go 版本由 **1.25.7** 提升至 **1.25.12**。本次只更新 Hexagon 根模块；`examples` 是独立模块，继续使用其当前已发布版本依赖，不作调整。
- **CI**：工作流收敛为单一根模块门禁。Go 1.25.12 验证格式、依赖一致性、`go vet`、普通测试与 `govulncheck`；Go 1.26.5 只补充一次 race 测试。删除重复验证并持有写权限的自动 GitHub Release 工作流，发布只使用已通过 CI 的不可变 SemVer tag。
- **CI 降噪**：删除不具备可靠阻断能力的 API compatibility 和浮动下游分支工作流，以及 Codecov、非阻断 lint、重复的 build/download、手工 changelog 和空通知步骤；Dependabot 改为每月分组更新，每类最多保留一个 PR。
- **版本治理**：在 `go.mod` 中撤回 v0.5.10 与 v0.5.11，防止消费者继续选择提交拓扑倒置且不符合既定 patch 合同的版本。

### BREAKING
- **Go 构建基线**：根模块最低 Go 版本提升至 **1.25.12**；使用更早 Go 工具链的调用方须先升级工具链。
- **Qdrant 持久数据**：ai-core v0.2.7 将新集合的默认 point ID 从 legacy hash31 改为 SHA-256 派生 UUIDv8。已有集合应在升级前完成重建；迁移窗口可直接使用 `github.com/hexagon-codes/ai-core/store/vector/qdrant`，显式选择已弃用的 `PointIDLegacyHash31` 读取旧映射，但不得继续用于写入新数据，也不得在同一集合中混用两种策略。
- **core/CircuitBreaker**：`NewCircuitBreaker` 与 `WithCircuitBreaker` 现返回 `(..., error)`，调用方必须处理构造校验错误；`CircuitBreakerConfig` 新增独立的 `HalfOpenMaxRequests`。旧 `Allow`/`RecordSuccess`/`RecordFailure` 调用序列迁移为 `Acquire()` 获取同一次执行的 permit，并以恰好一次 `permit.Complete(err)` 完成。
- **agent/a2a Push**：`PushNotification` 改为构造后不可变的通知快照；`NewTaskStatusNotification`、`NewArtifactNotification`、`NewPushManager`、`NewWebhookPushService`、`NewAsyncPushService` 与 `NewDefaultPushService` 现返回 `(..., error)`。`PushService.Push` 改为接收 `*PushNotification`，`PushManager.Push` 改为从通知中取得任务 ID。移除旧 `RetryConfig`、`DefaultRetryConfig`、`WithRetryConfig`、`WithPushSignKey`、`HTTPPushService` 与 `NewHTTPPushService`；本版本不承诺自动 Webhook 重试，调用方不得依赖旧 PushManager 的隐式重试行为。
- **security/cost**：`NewController` 现返回 `(*Controller, error)` 并严格校验配置；`DefaultPricing` 由共享变量改为返回独立副本的函数；移除无实际强制点的 `WithMaxTokensPerSession`。调用方应处理构造错误，并使用 `WithMaxTokensPerRequest` / `WithMaxTokensTotal` 配合运行时预算中间件表达限制。
- **observe/otel**：重导出入口与 toolkit v0.3.4 对齐，`OTelTracer` / `OTelConfig` / `OTelOption` / `OTelSpan` / `NewOTelTracer` / `DefaultOTelConfig` 分别迁移为 `Tracer` / `Config` / `Option` / `Span` / `NewTracer` / `DefaultConfig`。移除 `WithEndpoint` 与 `WithBatchConfig`；OTLP 调用方应通过可失败的 `NewOTLPExporter` 创建导出器，并用 `SetExporter(ctx, exporter)` 转移生命周期所有权。
- **observe/prometheus**：`Collector` / `NewCollector` 迁移为 `Factory` / `NewFactory`，`PrometheusCounter` / `PrometheusGauge` / `PrometheusHistogram` / `PrometheusSummary` 分别迁移为 `Counter` / `Gauge` / `Histogram` / `Summary`；`NewExporter` 现返回 `(*Exporter, error)`，`DefaultBuckets` 与 `DefaultQuantiles` 由共享变量改为函数。

### Fixed
- **core.Retry**：兼容 toolkit v0.3.4 对 `MaxDelay` 的正值校验；`delay == 0` 时不再传入无效的 `retry.MaxDelay(0)`，仍立即执行首次调用加 `maxRetries` 次重试。正延迟的固定重试语义不变，耗尽时原始错误仍可由 `errors.Is` 匹配。
- **core.WithRetry**：兼容 toolkit v0.3.4 的严格配置校验，同时保留升级前语义：`MaxDelay == 0` 时立即重试，`Multiplier == 0` 时按 `InitialDelay` 固定重试，避免部分 `RetryConfig` 在首次 Runnable 调用前返回 `ErrInvalidConfig`。

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
