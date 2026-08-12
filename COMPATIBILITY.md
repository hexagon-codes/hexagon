# 兼容性与稳定性策略 — hexagon

hexagon 是 Hexagon 生态的 **AI Agent 框架（L2）**，向下依赖 toolkit（L0）与 ai-core（L1），向上由独立产品消费。框架的导出 API、最低 Go 版本和持久数据格式共同构成发布兼容性合同。

## SemVer 承诺
- 遵循 [SemVer](https://semver.org/lang/zh-CN/)。**导出标识符（公开 API）**是兼容性契约。
- **v0.x 阶段**：patch 不得破坏公开合同；破坏式变更只能随 minor 发布，并必须在 CHANGELOG 显著标注 `BREAKING`、列出迁移方式。
- **v1.0 及以后**：minor 与 patch 只能包含兼容变更；破坏式变更只能随 major 发布。
- 内部包（`internal/`）、未导出标识符、`examples/`、`docs/` 不在契约内。

## 当前发布判定
- 相对最新已发布的 v0.5.9，当前待发布内容包含公开 API、最低 Go 版本和 Qdrant 持久数据合同的破坏式变更。
- 因此下一版本必须至少为 **v0.6.0**，不得作为 v0.5.x patch 发布；具体迁移项以 CHANGELOG 的 Unreleased `BREAKING` 段为准。

## 仓库门禁
- 根 CI 固定设置 `GOWORK=off`，只按根 `go.mod` 验证 `go mod tidy -diff` 与 `go test -count=1 -race ./...`，避免本地 workspace 或其他模块掩盖发布依赖问题。
- Tag 发布在同样的 `GOWORK=off` 根模块边界重新验证后才执行发布；验证 job 只读，写权限仅授予不检出仓库代码的发布 job。
- `examples/` 是独立 Go module，不属于根模块发布表面，也不进入根 CI；其依赖本次保持不变。以后若修改 `examples/`，应在该模块目录按其自身 `go.mod` 单独 build/test。
- 本仓不运行绑定 `main`、`latest` 等浮动外部分支的下游门禁，也不以临时 `go.work` 覆盖作为发布验收证据。
- 公共 API、最低 Go 版本或持久数据合同发生变化时，必须在评审中判定 SemVer 级别，并同步 CHANGELOG；当前精简 CI 不自动执行 `gorelease`。

## 弃用流程
- 弃用先标 `// Deprecated: 用 X 替代。将在 vN 移除。`，保留 ≥1 个 minor 周期，CHANGELOG 记录，到期才删。
- 重构期的再导出 shim（`deprecated.go`）属过渡技术债，到期统一清理。
- 移除导出 API = major（v0.x 为 minor + BREAKING 标注）。

## v1.0 API 冻结
- v0.x 处于快速演进期，只允许在 minor 中携带显著标注且提供迁移方式的破坏式变更。
- v1.0 前完成一次公共 API 收敛（编排轴/schema/顶层包归组），冻结导出表面，此后不兼容变更仅随 major 版本发布。

## 消费者升级责任
- 每个消费者必须 pin 明确的 Hexagon 发布版本，不得以 `main`、`latest` 或其他浮动分支作为可重复构建依据。
- v0.x patch 按政策不得包含破坏式变更；升级 minor 前必须检查 CHANGELOG 的 `BREAKING` 段、完成迁移，并在消费者自己的仓库执行单元、集成、API 与端到端等适用回归。
- 各仓库独立确定版本号和发布时间，不要求统一升版。发布依赖不得保留本地 `replace` 或依赖临时 `go.work`；跨仓验证应基于准备发布的明确版本。
