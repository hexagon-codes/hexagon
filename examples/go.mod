// examples 是独立模块，不属于 hexagon 框架的发布表面。
// 这样 go get github.com/hexagon-codes/hexagon 不会拉入示例及其依赖图。
// 本地开发可用工作区；交付验证设置 GOWORK=off，独立按明确版本构建。
module github.com/hexagon-codes/hexagon/examples

go 1.26.10

require (
	github.com/go-sql-driver/mysql v1.9.3
	github.com/hexagon-codes/ai-core v0.3.0
	github.com/hexagon-codes/hexagon v0.6.0
	github.com/hexagon-codes/toolkit v0.3.7
)

require (
	filippo.io/edwards25519 v1.1.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/modelcontextprotocol/go-sdk v1.5.0 // indirect
	github.com/openai/openai-go/v3 v3.91.0 // indirect
	github.com/redis/go-redis/v9 v9.18.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/time v0.8.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
