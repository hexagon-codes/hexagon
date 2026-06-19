# 兼容性与稳定性策略 — hexagon

hexagon 是 Hexagon 生态的 **AI Agent 框架（L2）**，向下依赖 toolkit（L0）/ai-core（L1），向上被 hexclaw（L3）等独立产品消费。框架的导出 API 是下游能否安心 pin 版本、避免被迫 lockstep 的契约基础。

## SemVer 承诺
- 遵循 [SemVer](https://semver.org/lang/zh-CN/)。**导出标识符（公开 API）**是兼容性契约。
- **patch / minor 不得破坏导出 API**（仅加法）；破坏式变更只能在 **major**（v0.x 阶段允许在 minor 破坏，但必须在 CHANGELOG 显著标注 BREAKING）。
- 内部包（`internal/`）、未导出标识符、`examples/`、`docs/` 不在契约内。

## 自动门禁
1. **API 兼容性检测**：`.github/workflows/api-compat.yml` 用 `gorelease` 对照上一 tag 检测破坏式变更，提示版本号应如何升。
2. **下游接缝契约**：`.github/workflows/downstream.yml` 在 go.work 下用本仓改动跑直接消费者（hexclaw）的 build+test —— 下游绿才算接口未破。

## 弃用流程
- 弃用先标 `// Deprecated: 用 X 替代。将在 vN 移除。`，保留 ≥1 个 minor 周期，CHANGELOG 记录，到期才删。
- 重构期的再导出 shim（`deprecated.go`）属过渡技术债，到期统一清理。
- 移除导出 API = major（v0.x 为 minor + BREAKING 标注）。

## v1.0 API 冻结
- v0.x 处于快速演进期，允许在 minor 破坏（伴随 BREAKING 标注 + 下游 lockstep）。
- v1.0 前完成一次公共 API 收敛（编排轴/schema/顶层包归组），冻结导出表面，此后 `api-compat.yml` 由提示性转为破坏式变更即 CI 红。

## 升级建议（给下游 hexclaw 等）
- pin 明确版本；minor/patch 可放心升；见到 BREAKING 标注再评估迁移。
- 跨仓机制：四仓按版本号依赖、无 replace，改代码层即可，版本号到发版时统一 bump。
