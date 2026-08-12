# Hexagon Makefile
# 用于常见的开发任务

.PHONY: all build test lint fmt clean help

# 默认目标
all: fmt lint test build

# 构建
build:
	go build ./...

# 运行测试
test:
	go test -v -race -cover ./...

# 运行测试并生成覆盖率报告
test-coverage:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# 代码检查
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

# 格式化代码
fmt:
	go fmt ./...
	@if command -v goimports >/dev/null 2>&1; then \
		goimports -w -local github.com/hexagon-codes/hexagon .; \
	fi

# 代码检查 (vet)
vet:
	go vet ./...

# 清理
clean:
	rm -f coverage.out coverage.html
	go clean -cache

# 下载依赖
deps:
	go mod download
	go mod tidy

# 运行示例
run-quickstart:
	go run ./examples/quickstart/main.go

run-react:
	go run ./examples/react/main.go

# 帮助
help:
	@echo "Hexagon Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make              - 格式化、检查并测试代码"
	@echo "  make build        - 构建项目"
	@echo "  make test         - 运行测试"
	@echo "  make test-coverage  运行测试并生成覆盖率报告"
	@echo "  make lint         - 代码静态检查"
	@echo "  make fmt          - 格式化代码"
	@echo "  make vet          - Go vet 检查"
	@echo "  make clean        - 清理构建产物"
	@echo "  make deps         - 下载依赖"
	@echo "  make run-quickstart - 运行 quickstart 示例"
	@echo "  make run-react    - 运行 react 示例"
	@echo "  make help         - 显示帮助"
