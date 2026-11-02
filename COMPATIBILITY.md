# 兼容性与稳定性策略 — hexagon

hexagon 是 Hexagon 生态的 **AI Agent 框架（L2）**，向下依赖 toolkit（L0）与 ai-core（L1），向上由独立产品消费。框架的导出 API、最低 Go 版本和持久数据格式共同构成发布兼容性合同。

## SemVer 承诺
- 遵循 [SemVer](https://semver.org/lang/zh-CN/)。**导出标识符（公开 API）**是兼容性契约。
- **v0.x 阶段**：patch 不得破坏公开合同；破坏式变更只能随 minor 发布，并必须在 CHANGELOG 显著标注 `BREAKING`、列出迁移方式。
- **v1.0 及以后**：minor 与 patch 只能包含兼容变更；破坏式变更只能随 major 发布。
- 内部包（`internal/`）、未导出标识符、`examples/`、`docs/` 不在契约内。

## 当前发布判定

- 当前本地交付目标为 **v0.6.0**，按 breaking minor 处理 Clone、Compactor、图默认模式和 A2A wire/数据合同变化，迁移清单见 [CHANGELOG](CHANGELOG.md)。本次没有创建或推送 tag，也没有公共代理可解析性证据；本地候选模块验证与正式发布分别记录。
- 交付依赖目标为 Go **1.26.10**、ai-core **v0.3.0** 与 Toolkit **v0.3.7**。三个库独立版本化，最终消费者需记录实际 MVS build list；工作区编译成功不能代替版本化模块组合证据。
- 轻量 Run/Stream 不强制数据库；强持久装配需要宿主的连接、显式迁移、worker 与恢复入口。Runtime/图/中断状态保存各自 codec 和计划身份，旧快照不会自动获得新保证。缺少原资源版本或迁移时保留记录并明确失败；`SnapshotCodecs` 的迁移失败不得改写原记录。应用可继续固定旧 tag 完成原任务，HexClaw 不要求升级。
- 发布版本来自目标提交的不可变 tag；开发构建使用构建注入或 module build info，未知时保留 `unknown`。文档或 Helm `appVersion` 的更新不代表 tag、镜像或 GitHub Release 已发布。

## v0.6.0 公共接入与恢复合同

下表对应本地交付目标的公共合同；新增入口不要求普通轻量 Run/Stream 使用 SQL，也不自动授予强持久恢复保证。现有应用可以继续固定原 tag，HexClaw 不要求升级或由框架提供应用兼容分支。

| 公共入口 / 数据 | 消费方必须遵守的合同 |
|---|---|
| `Request.Generation`、`Request.StructuredOutput`、`Result.Structured` | Generation 使用 ai-core `GenerationOptions`，准备时冻结后传到实际请求；结构化最终结果按 `StructuredOutputSpec` 验证后采用，读取独立的 Structured JSON，不把文本形态当成验证通过 |
| `Snapshot.Phase` | 保留 BeforeModel、PendingTools、AfterTurn、Final 的实际边界，连同 Step、等待和已完成工具记录恢复；不能统一 Step+1 或重做已完成工具 |
| `Snapshot.Codec` / `RunSpec.Codec` | 前者表示当前载荷编码，后者保留原始规格声明；显式纯迁移不得调用 Provider/工具或修改原记录，已迁移载荷按新 codec 读取，不能重复迁移 |
| `DurableRunner.Submit` / `RunSpec.SelectedProvider` | 接受前冻结实际 Provider/Model；恢复核对原路由及资源，不能静默使用当前 primary；合法 fallback 按固定策略执行 |
| `ToolResult.Structured` / `StructuredPayload` | Kind、Version、Data 是持久 DTO 合同；升级时按明确版本解码，不能依靠 Raw 对象的运行期方法恢复子结果或产物 |
| `NeedsReconciliation` / `ToolResultReconciler` | 核对只查询原结果或幂等补登记，不重做原业务动作；保留原失败 Task，以独立核对 Task 保存后续事实，未处理或查询失败仍保留核对需求 |
| `DynamicTools` | 每个 Run 使用独立绑定视图；`BindRun` 固定归属，`SnapshotTools`/`RestoreTools` 恢复原定义及资源引用。缓存搜索结果也必须带 ToolSnapshot，不能仅恢复名字/schema 或偷偷改用最新执行器 |
| `DefaultRunner.NewModelSession` | 独立 pin、State 和用量，不借用父运行活跃 State 的调用上下文；宿主负责工作 goroutine、取消、等待及 Close，子 Report 通过稳定尝试身份关联，不能重复计入父账 |
| `CostControl.RecordAttempt` | 设置后优先于 Record；持久账本按实际 HTTP attempt ID 幂等入账。部分 usage 的缺失维度保持未知，预算下界与供应商实报 TotalTokens 分开 |
| `SSEEventSink.EmitStored` / `Follow` | 仅已提交事件信封携带持久 ID/序号；Follow 使用 EventStore 序号追赶，消费者记录游标并去重。即时 delta 不提供持久恢复游标 |
| `agent.NewToolExecutor` | 将现有 ai-core tools 装配给 `runtime.Config.ToolExecutor`，再按需接 DurableRunner；无需公开 ReAct 内部对象。宿主仍负责工具连接、Lease 及强持久依赖的生命周期 |
| `RunSpec.IngressDigest` / `SubmissionDigest` | 接收按原输入与身份去重，重复 Submit 不重新解析 latest；持久接收适配器和宿主查回使用同一摘要算法。Signal 身份不冒充 Submit，原输入变化明确冲突；旧记录保留旧算法的原条件查回 |
| `Snapshot.BudgetStartedAt` | 首次模型派发前保存预算墙钟起点；恢复不重新起算。原快照没有该事实时不能自动补得历史保证；普通轻量执行仍无需持久依赖 |
| `WaitingTargets` | nil 表示未声明/保持，非 nil 空列表表示当前没有等待；执行核随快照声明完整目标。原子提交只为当前等待集的未消费输入保持 queued；过期目标不盲消费，同 ID 查回不再派发。宿主选择恢复输入时必须与当前等待集对应 |
| `PrepareModelRequest` / `EventModelDraftReplaced` | 计量与派发共用完整请求派生；不修改传入历史。草稿替换事件以 `replacement_text` 撤回本包装调用的废弃尝试，保留 Continue 之前的前缀；实际费用和回执仍逐次保存 |

## 插件依赖代次与恢复

- 插件实例在初始化时取得声明依赖的使用权，依赖使用权保留到该实例 Stop 完成。单独更新 Provider 可以立即发布新根路由；仍运行的 Tool 继续使用原 Provider，旧 Provider 的 `Retirement.Wait` 等待实际依赖者排空，不表示更新发布失败。
- `DependencyFromContext(ctx, name)` 在 Init/Start/Stop 回调中返回固定依赖。候选集内的插件通过此入口取得候选依赖，历史恢复通过此入口取得原版本依赖；插件必须缓存这份实际依赖，不能再用当前 `Registry.Get` 替换它。旧插件直接使用 Registry.Get 时按生命周期当时实际可见的代次固定；路由在未完成生命周期回调期间变化且无法确认绑定时，清理未发布实例并明确返回错误，不停止已有运行实例。
- `Lease.Plugin(name)` 优先返回显式选择的根；只请求 Tool 时，其隐式 Provider 是该 Tool 的实际依赖。根 Provider v2 和 Tool 所依赖的 Provider v1 可同时持有；同名多版本的隐式依赖没有显式根选择时，`Plugin(name)` 返回 false，调用方应使用已经装配好的 Tool。`References()` 和 `RecoverableReferences()` 返回显式根，并通过 `Reference.Dependencies` 递归记录实际依赖，不把旧依赖伪装成当前路由。
- 恢复独立重建每份固定依赖，不发布旧路由。无法从原引用或生命周期上下文证明依赖绑定时返回 `plugin.ErrDependencyUnavailable`，不静默改用 latest。跨层的 RunPreparer 宿主适配器应在恢复路径把该错误与 `runtime.ErrUnrecoverableSpec` 一起包装并保留原错误，随后禁止派发 Provider；Runtime 核心不依赖 plugin 包。没有依赖树的旧引用仅能恢复实际无依赖的插件，有依赖的旧任务应由原固定版本继续或提供明确迁移。

## 历史版本策略例外

- v0.5.10 与 v0.5.11 已包含相对 v0.5.9 的公开 API、最低 Go 版本和 Qdrant 持久数据合同变更，但以 patch 版本发布，且两者的提交拓扑与版本顺序倒置。
- 维护者确认当前框架尚无外部使用者，批准 **v0.5.12** 作为一次性版本策略例外：保留上述合同变化，撤回 v0.5.10 与 v0.5.11，并以 v0.5.12 建立唯一可消费基线。迁移项见 CHANGELOG 的 v0.5.12 `BREAKING` 段。
- 该例外不改变后续兼容性承诺；除明确记录的版本策略例外外，v0.5.x patch 不得包含破坏式合同变化。

## 仓库门禁
- 根 CI 固定设置 `GOWORK=off`、`GOTOOLCHAIN=local` 与 `GOFLAGS=-mod=readonly`，避免本地 workspace 或其他模块掩盖发布依赖问题。最低 Go 版本从根 `go.mod` 读取，检查格式并以 `go test -count=1 -vet=all ./...` 执行完整 vet 和普通测试；最新稳定 Go 版本执行 race 和 `govulncheck`。实际缺失或不一致的依赖仍失败，冗余历史 `go.sum` 条目不作为门禁。
- 本仓没有自动发布工作流。维护者只在目标提交的根 CI 成功后创建不可变 SemVer tag；Go 模块以该 tag 发布，GitHub Release 为可选的人工元数据。
- `examples/` 是独立 Go module，不属于根模块发布表面，也不进入根 CI；本次目标为 Go 1.26.10、Hexagon v0.6.0、ai-core v0.3.0 与 Toolkit v0.3.7。按自身模块单独验证，不通过旧版本依赖或 workspace 冒充当前 API 接入成功。
- 本仓不运行绑定 `main`、`latest` 等浮动外部分支的下游门禁，也不以临时 `go.work` 覆盖作为发布验收证据。
- 公共 API、最低 Go 版本或持久数据合同发生变化时，必须在评审中判定 SemVer 级别，并同步 CHANGELOG；精简 CI 不自动执行 `gorelease` 或具名消费者测试。

## 弃用流程
- v1 及以后，弃用先标 `// Deprecated: 用 X 替代。将在 vN 移除。`，保留 ≥1 个 minor 周期并记录，到期才可按 major 规则删除。
- v0.x 已明确采用一次性迁移的 breaking minor，可直接调整对应入口，不为旧应用保留并行实现或 shim；实际删除必须逐项列入 CHANGELOG。既有 `deprecated.go` 中未在本次清单列明删除的导出仍存在，不能仅凭政策说明推定已经清除。
- 移除导出 API = major（v0.x 为 minor + BREAKING 标注）。

## v1.0 API 冻结
- v0.x 处于快速演进期，只允许在 minor 中携带显著标注且提供迁移方式的破坏式变更。
- v1.0 前完成一次公共 API 收敛（编排轴/schema/顶层包归组），冻结导出表面，此后不兼容变更仅随 major 版本发布。

## 消费者升级责任
- 每个消费者必须 pin 明确的 Hexagon 发布版本，不得以 `main`、`latest` 或其他浮动分支作为可重复构建依据。
- v0.x patch 按政策不得包含破坏式变更；升级 minor 前必须检查 CHANGELOG 的 `BREAKING` 段、完成迁移，并在消费者自己的仓库执行单元、集成、API 与端到端等适用回归。
- 各仓库独立确定版本号和发布时间，不要求统一升版。发布依赖不得保留本地 `replace` 或依赖临时 `go.work`；跨仓验证应基于准备发布的明确版本。
