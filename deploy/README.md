<div align="right">语言: 中文 | <a href="README.en.md">English</a></div>

# Hexagon 部署指南

## 快速开始

### Docker Compose（推荐新用户）

一键启动全部服务，包含 Qdrant、Redis、PostgreSQL 等基础设施：

```bash
cd deploy

# 1. 配置环境变量
cp .env.example .env
# 编辑 .env，填入 LLM API Key

# 2. 启动
make up

# 3. 访问
# 主应用:  http://localhost:8000
# Dev UI:  http://localhost:8080
# Redis Insight: http://localhost:8001
```

### Docker Compose（开发模式）

适用于已通过 [docker-dev-env](https://github.com/hexagon-codes/docker-dev-env) 启动基础设施的团队开发者：

```bash
cd deploy

# 1. 确保 dev-net 网络和中间件已就绪
# docker network create dev-net  (docker-dev-env 已提供)

# 2. 配置环境变量
cp .env.dev.example .env

# 3. 启动（仅应用服务，连接外部中间件）
make dev-up
```

### Kubernetes / Helm

```bash
cd deploy

# 自包含模式（含内置中间件）
make helm-install

# 使用外部基础设施
helm install hexagon helm/hexagon/ \
  -n hexagon --create-namespace \
  --set qdrant.enabled=false \
  --set redis.enabled=false \
  --set postgres.enabled=false \
  --set external.qdrant.url=http://my-qdrant:6333 \
  --set external.redis.url=my-redis:6379 \
  --set external.postgres.dsn="postgres://user:pass@my-pg:5432/hexagon?sslmode=disable"
```

## 部署方案对比

| 方案 | 适用场景 | 命令 |
|------|---------|------|
| `docker compose up` | 快速体验、演示、单机部署 | `make up` |
| `docker compose -f docker-compose.dev.yml up` | 团队开发（复用 docker-dev-env） | `make dev-up` |
| `helm install` | K8s 集群、生产环境 | `make helm-install` |

## 目录结构

```
deploy/
├── docker-compose.yml          # 基础设施服务（Qdrant / Redis / PostgreSQL）
├── .env.example                # 完整模式环境变量模板
├── .env.dev.example            # 开发模式环境变量模板
├── Makefile                    # 快捷命令
└── helm/hexagon/               # Helm Chart
    ├── Chart.yaml
    ├── values.yaml             # 内置/外部可切换
    ├── .helmignore
    └── templates/
        ├── _helpers.tpl
        ├── deployment.yaml     # App + DevUI
        ├── statefulset.yaml    # Qdrant / Redis / PostgreSQL
        ├── service.yaml
        ├── ingress.yaml
        ├── secret.yaml
        ├── serviceaccount.yaml
        └── NOTES.txt           # 安装后提示
```

## 配置参考

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `OPENAI_API_KEY` | (空) | OpenAI API Key |
| `DEEPSEEK_API_KEY` | (空) | DeepSeek API Key |
| `LOG_LEVEL` | info | 日志级别 (debug/info/warn/error) |
| `QDRANT_URL` | http://qdrant:6333 | Qdrant 连接地址 |
| `REDIS_URL` | redis:6379 | Redis 连接地址 |
| `REDIS_PASSWORD` | (空) | Redis 密码 |
| `POSTGRES_DSN` | postgres://hexagon:hexagon@postgres:5432/hexagon?sslmode=disable | PostgreSQL DSN |

### Helm 内置/外部切换

在 `values.yaml` 中，每个中间件都有 `enabled` 开关：

```yaml
# 使用内置组件（默认）
qdrant:
  enabled: true

# 使用外部组件
qdrant:
  enabled: false
external:
  qdrant:
    url: "http://my-qdrant:6333"
```

## 常用命令

```bash
# Docker Compose
make up              # 启动全部
make down            # 停止
make logs            # 查看日志
make status          # 查看状态
make restart         # 重启应用
make clean           # 停止并删除数据

# 开发模式
make dev-up          # 启动（连接 docker-dev-env）
make dev-down        # 停止
make dev-logs        # 查看日志

# Helm
make helm-install    # 安装
make helm-upgrade    # 升级
make helm-uninstall  # 卸载
make helm-template   # 预览渲染结果
```

## 故障排查

### 应用无法启动

```bash
# 查看容器日志
docker compose logs hexagon-app

# 检查健康状态
docker compose ps

# 进入容器调试
docker compose exec hexagon-app sh
```

### 连接不上基础设施

```bash
# 确认基础设施健康
docker compose ps qdrant redis postgres

# 测试网络连通性
docker compose exec hexagon-app wget -q -O- http://qdrant:6333/healthz
```

### 磁盘空间不足

```bash
# 清理未使用的 Docker 资源
docker system prune -f

# 清理数据卷（警告: 会删除数据）
docker compose down -v
```
