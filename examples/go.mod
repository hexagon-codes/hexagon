// examples 是独立模块，不属于 hexagon 框架的发布表面。
// 这样 go get github.com/hexagon-codes/hexagon 不会拉入示例及其依赖图。
// 本地开发经仓库根的 go.work 解析生态依赖；发版时 go.work 移除，示例按版本号构建。
module github.com/hexagon-codes/hexagon/examples

go 1.25.5

require (
	github.com/hexagon-codes/ai-core v0.1.3
	github.com/hexagon-codes/hexagon v0.4.8
	github.com/hexagon-codes/toolkit v0.0.6
)
