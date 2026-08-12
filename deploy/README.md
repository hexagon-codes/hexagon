<div align="right">语言: 中文 | <a href="README.en.md">English</a></div>

# Hexagon 部署配置

本目录包含两类配置：

- `docker-compose.yml`：仅用于启动本地 Qdrant、Redis 和 PostgreSQL 基础设施。
- `helm/hexagon/`：用于把自定义 Hexagon 应用镜像接入 Kubernetes 的 Helm 模板。

Hexagon 本仓库是 Go 库。当前仓库不包含应用 Dockerfile，也不提供可直接部署的应用或 DevUI 可执行程序；当前发布流程只创建 GitHub Release，不构建或推送容器镜像。

## Docker Compose：本地基础设施

### 启动

```bash
cd deploy
cp .env.example .env

# 按需修改端口、Redis 密码和 PostgreSQL 账号后启动
docker compose up -d
docker compose ps
```

当前 Compose 文件只会启动以下服务：

| 服务 | 容器端口 | 默认宿主机端口 | 数据卷 |
|------|----------|----------------|--------|
| Qdrant | 6333 / 6334 | 6333 / 6334 | `qdrant-data` |
| Redis Stack | 6379 / 8001 | 6379 / 8001 | `redis-data` |
| PostgreSQL + pgvector | 5432 | 5432 | `postgres-data` |

它不会启动 Hexagon 应用或 DevUI。应用需由使用者在本机、其他容器或目标环境中单独运行，并连接上述基础设施。

### Compose 使用的环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `QDRANT_HTTP_PORT` | `6333` | Qdrant HTTP 宿主机端口 |
| `QDRANT_GRPC_PORT` | `6334` | Qdrant gRPC 宿主机端口 |
| `REDIS_PORT` | `6379` | Redis 宿主机端口 |
| `REDIS_INSIGHT_PORT` | `8001` | Redis Insight 宿主机端口 |
| `REDIS_PASSWORD` | 空 | Redis 密码 |
| `POSTGRES_PORT` | `5432` | PostgreSQL 宿主机端口 |
| `POSTGRES_USER` | `hexagon` | PostgreSQL 用户名 |
| `POSTGRES_PASSWORD` | `hexagon` | PostgreSQL 密码 |
| `POSTGRES_DB` | `hexagon` | PostgreSQL 数据库名 |

`.env.example` 中的 LLM、应用和 DevUI 变量不由当前 `docker-compose.yml` 消费，应在实际应用的运行环境中配置。

### 常用命令

```bash
cd deploy

docker compose up -d                       # 启动基础设施
docker compose ps                          # 查看服务状态
docker compose logs -f                     # 查看全部基础设施日志
docker compose logs -f qdrant redis postgres
docker compose down                        # 停止并保留数据卷
```

部署 Makefile 中与当前 Compose 配置一致的快捷命令为：

```bash
make up       # 启动基础设施
make status   # 查看状态
make down     # 停止并保留数据卷
make clean    # 停止并删除数据卷
```

> `make clean` 或 `docker compose down -v` 会永久删除 Qdrant、Redis 和 PostgreSQL 的本地数据卷，请先确认数据不再需要。

### 基础设施排查

```bash
cd deploy

docker compose config --services
docker compose ps
docker compose logs qdrant
docker compose logs redis
docker compose logs postgres

# 默认端口下检查 Qdrant
curl -f http://localhost:6333/healthz

# 检查 PostgreSQL
docker compose exec postgres pg_isready -U hexagon

# 未设置 Redis 密码时检查 Redis
docker compose exec redis redis-cli ping
```

若修改了端口、用户或密码，请同步调整检查命令。

## Helm：应用接入模板

`helm/hexagon/` 可以生成以下 Kubernetes 资源：

- Hexagon 应用 Deployment 和 Service；
- 可选的 DevUI Deployment 和 Service；
- 可选的 Qdrant、Redis 和 PostgreSQL StatefulSet 与 Service；
- 可选的 Ingress、ServiceAccount 和由本 Chart 管理的 LLM API Key Secret。

### 使用边界

当前 Chart 的默认镜像值不代表本仓库已发布或验证的运行镜像。安装前必须替换 `app.image.repository` 和 `app.image.tag`；启用 DevUI 时，还必须替换 `devui.image.repository` 和 `devui.image.tag`。

自定义镜像必须满足模板中的固定运行契约：

| 组件 | 必需入口 | 固定容器端口 | 健康检查 | 可写目录 |
|------|----------|--------------|----------|----------|
| 应用 | `/app/hexagon serve` | 8000 | `GET /health` | `/tmp`、`/app/data` |
| DevUI（启用时） | `/app/hexagon devui` | 8080 | `GET /health` | `/tmp` |

此外，镜像必须：

- 能以非 root 的 UID/GID `10001` 运行；
- 支持只读根文件系统；
- 从环境变量读取 `QDRANT_URL`、`REDIS_URL`、`REDIS_PASSWORD` 和 `POSTGRES_DSN`；
- 在需要相应 Provider 时读取 `OPENAI_API_KEY` 或 `DEEPSEEK_API_KEY`。

DevUI 模板会设置 `DEVUI_ADDR=:8080`，并通过 `HEXAGON_APP_URL` 指向同一 Release 的应用 Service。不满足以上约定时，必须先调整 Helm 模板或自定义镜像，不能直接使用默认安装命令。

### 镜像与基础设施配置示例

以下命令仅展示必需配置，镜像地址和版本需替换为使用者自己的已验证产物：

```bash
cd deploy

helm install hexagon helm/hexagon \
  -n hexagon --create-namespace \
  --set app.image.repository=registry.example.com/agent-runtime \
  --set app.image.tag=1.0.0 \
  --set devui.enabled=false
```

启用 DevUI 时，需另外提供满足 DevUI 入口契约的镜像：

```bash
helm install hexagon helm/hexagon \
  -n hexagon --create-namespace \
  --set app.image.repository=registry.example.com/agent-runtime \
  --set app.image.tag=1.0.0 \
  --set devui.enabled=true \
  --set devui.image.repository=registry.example.com/devui-runtime \
  --set devui.image.tag=1.0.0
```

Qdrant、Redis 和 PostgreSQL 默认由 Chart 创建。若改用外部服务，应把对应的 `enabled` 设为 `false`，并完整配置 `external.qdrant.url`、`external.redis.*` 和 `external.postgres.dsn`。

### Secret 现状

当 `secrets.openaiApiKey` 或 `secrets.deepseekApiKey` 非空时，Chart 会创建并引用一个由本 Chart 管理的 Kubernetes Secret。当前 Chart 没有引用既有 Secret 的配置项，也没有 External Secrets 集成。请根据目标环境的密钥管理要求评估后再部署，不要把真实密钥提交到仓库。

### 验证责任

当前 CI 只验证 Go 依赖一致性和 race 测试，不渲染、安装或发布 Helm Chart。使用者应在装有 Helm 且可访问目标 Kubernetes 集群的环境中，先检查渲染结果和镜像契约，再执行安装或升级。
