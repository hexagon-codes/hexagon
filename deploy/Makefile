# Hexagon 部署 Makefile
#
# 提供 Docker Compose 和 Helm 部署的快捷命令
#
# 使用方式:
#   make help          # 查看所有命令
#   make up            # 启动本地基础设施
#   make helm-template # 渲染 Helm 模板

.PHONY: help up down dev-up dev-down build logs status clean helm-install helm-upgrade helm-uninstall helm-template

# 默认目标
help: ## 显示帮助信息
	@echo "Hexagon 部署命令："
	@echo ""
	@echo "  Docker Compose（本地基础设施）:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | grep -v dev- | grep -v helm- | awk 'BEGIN {FS = ":.*?## "}; {printf "    \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "  Helm（应用接入模板）:"
	@grep -E '^helm-[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "    \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ============== Docker Compose 完整模式 ==============

up: ## 启动 Qdrant、Redis 和 PostgreSQL
	docker compose up -d

down: ## 停止本地基础设施
	docker compose down

build: # 当前 Compose 服务均使用外部镜像；保留兼容入口但不在帮助中展示
	docker compose build

logs: ## 查看全部基础设施日志（实时跟踪）
	docker compose logs -f

status: ## 查看服务状态
	docker compose ps

clean: ## 停止服务并删除数据卷
	docker compose down -v

restart: ## 重启全部基础设施服务
	docker compose restart

# ============== Docker Compose 开发模式 ==============

dev-up: # 旧开发模式（依赖仓库未提供的 docker-compose.dev.yml，不在帮助中展示）
	@docker network inspect dev-net > /dev/null 2>&1 || \
		(echo "创建 dev-net 网络..." && docker network create dev-net)
	docker compose -f docker-compose.dev.yml up -d

dev-down:
	docker compose -f docker-compose.dev.yml down

dev-build:
	docker compose -f docker-compose.dev.yml build

dev-logs:
	docker compose -f docker-compose.dev.yml logs -f

dev-status:
	docker compose -f docker-compose.dev.yml ps

# ============== Helm ==============

RELEASE_NAME ?= hexagon
NAMESPACE ?= hexagon
CHART_DIR := helm/hexagon

helm-install: # 需先按部署文档替换并验证镜像；不在帮助中展示
	helm install $(RELEASE_NAME) $(CHART_DIR) \
		-n $(NAMESPACE) --create-namespace

helm-upgrade: # 需先按部署文档替换并验证镜像；不在帮助中展示
	helm upgrade $(RELEASE_NAME) $(CHART_DIR) \
		-n $(NAMESPACE)

helm-uninstall: # 兼容入口，不在帮助中展示
	helm uninstall $(RELEASE_NAME) -n $(NAMESPACE)

helm-template: ## 预览生成的 K8s 资源（不实际部署）
	helm template $(RELEASE_NAME) $(CHART_DIR)

helm-install-external: # 需先按部署文档替换并验证镜像；不在帮助中展示
	helm install $(RELEASE_NAME) $(CHART_DIR) \
		-n $(NAMESPACE) --create-namespace \
		--set qdrant.enabled=false \
		--set redis.enabled=false \
		--set postgres.enabled=false \
		--set external.qdrant.url=$(QDRANT_URL) \
		--set external.redis.url=$(REDIS_URL) \
		--set external.postgres.dsn=$(POSTGRES_DSN)
