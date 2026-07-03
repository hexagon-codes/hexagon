// examples 是独立模块，不属于 hexagon 框架的发布表面。
// 这样 go get github.com/hexagon-codes/hexagon 不会拉入示例及其依赖图。
// 本地开发经仓库根的 go.work 解析生态依赖；发版时 go.work 移除，示例按版本号构建。
module github.com/hexagon-codes/hexagon/examples

go 1.25.7

require (
	github.com/hexagon-codes/ai-core v0.2.0
	github.com/hexagon-codes/hexagon v0.5.7
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hexagon-codes/toolkit v0.2.6 // indirect
	github.com/modelcontextprotocol/go-sdk v1.5.0 // indirect
	github.com/redis/go-redis/v9 v9.18.0 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
