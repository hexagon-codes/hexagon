<div align="right">语言: 中文 | <a href="CONTRIBUTING.en.md">English</a></div>

# 贡献指南

感谢你对 Hexagon 项目的关注！我们欢迎各种形式的贡献。

## 行为准则

请保持友善和尊重。我们致力于创建一个开放、包容的社区。

## 如何贡献

### 报告 Bug

1. 确保 Bug 尚未被报告 - 搜索 [Issues](https://github.com/hexagon-codes/hexagon/issues)
2. 如果找不到相关 Issue，创建一个新的
3. 使用清晰的标题和详细的描述
4. 提供复现步骤、期望行为和实际行为
5. 附上相关的代码片段或错误日志

### 建议功能

1. 搜索现有 Issues，确保没有重复
2. 创建新 Issue，使用 `[Feature]` 前缀
3. 清晰描述功能需求和使用场景
4. 如果可能，提供实现思路

### 提交代码

#### 准备工作

1. 安装 `go.mod` 声明的 Go 版本或更高版本（当前最低版本为 Go 1.25.12）
2. Fork 仓库
3. 克隆到本地：
   ```bash
   git clone https://github.com/YOUR_USERNAME/hexagon.git
   cd hexagon
   ```
4. 添加上游仓库：
   ```bash
   git remote add upstream https://github.com/hexagon-codes/hexagon.git
   ```
5. 创建分支：
   ```bash
   git checkout -b feature/your-feature-name
   ```

#### 开发流程

1. 运行与 `CI / Test` 作业一致的必需门禁：
   ```bash
   GOWORK=off go mod tidy -diff
   GOWORK=off go test -count=1 -race ./...
   ```

2. 按需运行本地辅助检查。这些命令方便开发，但不是当前 CI 门禁：
   ```bash
   make fmt      # 格式化代码
   make lint     # 静态检查（需要本地安装 golangci-lint）
   ```

3. 编写测试覆盖你的更改

4. 提交代码：
   ```bash
   git add .
   git commit -m "feat: add your feature description"
   ```

5. 推送到你的 Fork：
   ```bash
   git push origin feature/your-feature-name
   ```

6. 创建 Pull Request

#### Commit 规范

使用 [Conventional Commits](https://www.conventionalcommits.org/) 规范：

- `feat`: 新功能
- `fix`: Bug 修复
- `docs`: 文档更新
- `style`: 代码格式调整
- `refactor`: 重构
- `test`: 测试相关
- `chore`: 构建、工具相关

示例：
```
feat(agent): add ReAct agent implementation
fix(llm): handle rate limit errors
docs: update README with quick start guide
```

## 代码规范

### Go 代码风格

- 遵循 [Effective Go](https://go.dev/doc/effective_go)
- 使用 `gofmt` 格式化代码
- 使用 `golangci-lint` 进行静态检查
- 保持函数简短（建议不超过 50 行）
- 变量命名清晰，避免缩写

### 接口设计

- 接口应该小而专注
- 优先使用组合而非继承
- 使用 `context.Context` 作为第一个参数
- 错误处理使用 `fmt.Errorf("...: %w", err)` 包装

### 文档

- 所有导出的函数、类型必须有文档注释
- 文档注释以被注释对象的名称开头
- 提供使用示例（Example 函数）

### 测试

- 为变更覆盖正常、错误和边界行为
- 使用表驱动测试
- Mock 外部依赖
- 测试文件命名：`xxx_test.go`

## 项目结构

```
hexagon/
├── agent/         # Agent 核心（含 a2a / artifact / semantic / skill）
├── core/          # 核心接口（Component / Runnable / Stream）
├── orchestration/ # 编排层（graph / chain / workflow / planner）
├── rag/           # RAG 系统（含 adw 等）
├── runtime/       # 统一执行运行时（middleware / strategy）
├── observe/       # 可观测性（tracer / metrics / otel / devui）
├── security/      # 安全防护（guard / rbac / cost / audit 等）
├── tool/          # 工具系统
├── store/         # 存储（vector/*）
├── internal/      # 内部实现（不对外暴露）
├── examples/      # 示例代码（独立 module）
├── testing/       # 测试辅助与集成测试
└── docs/          # 文档
```

> 注：本项目不使用 `pkg/` 目录，符合 Go 社区主流实践。

## Pull Request 流程

1. PR 标题使用 Conventional Commits 格式
2. 填写 PR 模板中的所有必填项
3. 确保 `CI / Test` 检查通过
4. 等待代码审查
5. 根据反馈修改
6. 合并后删除分支

## 发布流程

版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)：

- `MAJOR.MINOR.PATCH`
- MAJOR: 不兼容的 API 更改
- MINOR: 向后兼容的功能新增
- PATCH: 向后兼容的 Bug 修复

发布由匹配 `v*` 的 Git 标签触发：

1. `Release / Validate` 执行 `go mod tidy -diff` 和全量 race 测试
2. 验证通过后，`Release / Publish` 创建 GitHub Release 并自动生成发布说明
3. 标签名称包含 `-`（例如 `v0.6.0-rc.1`）时发布为预发布版本

v0.x 阶段的兼容性与 BREAKING 约定见 [COMPATIBILITY.md](COMPATIBILITY.md)。

## 获取帮助

- 查阅 [文档](docs/)
- 提交 [Issue](https://github.com/hexagon-codes/hexagon/issues)

## 许可证

通过贡献代码，你同意你的贡献将在 [Apache License 2.0](LICENSE) 下发布。
