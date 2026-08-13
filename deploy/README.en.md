<div align="right">Language: <a href="README.md">中文</a> | English</div>

# Hexagon Deployment Configuration

This directory contains two kinds of configuration:

- `docker-compose.yml`: starts only the local Qdrant, Redis, and PostgreSQL infrastructure.
- `helm/hexagon/`: a Helm template for integrating a custom Hexagon application image with Kubernetes.

The Hexagon repository is a Go library. It does not currently include an application Dockerfile or provide directly deployable application or DevUI executables. Releases use immutable SemVer tags; the repository does not automatically create GitHub Releases or build and publish container images.

## Docker Compose: Local Infrastructure

### Start

```bash
cd deploy
cp .env.example .env

# Adjust ports, the Redis password, and PostgreSQL credentials as needed
docker compose up -d
docker compose ps
```

The current Compose file starts only these services:

| Service | Container Ports | Default Host Ports | Data Volume |
|---------|-----------------|--------------------|-------------|
| Qdrant | 6333 / 6334 | 6333 / 6334 | `qdrant-data` |
| Redis Stack | 6379 / 8001 | 6379 / 8001 | `redis-data` |
| PostgreSQL + pgvector | 5432 | 5432 | `postgres-data` |

It does not start a Hexagon application or DevUI. Run the application separately on the host, in another container, or in the target environment, and connect it to this infrastructure.

### Environment Variables Used by Compose

| Variable | Default | Description |
|----------|---------|-------------|
| `QDRANT_HTTP_PORT` | `6333` | Qdrant HTTP host port |
| `QDRANT_GRPC_PORT` | `6334` | Qdrant gRPC host port |
| `REDIS_PORT` | `6379` | Redis host port |
| `REDIS_INSIGHT_PORT` | `8001` | Redis Insight host port |
| `REDIS_PASSWORD` | empty | Redis password |
| `POSTGRES_PORT` | `5432` | PostgreSQL host port |
| `POSTGRES_USER` | `hexagon` | PostgreSQL username |
| `POSTGRES_PASSWORD` | `hexagon` | PostgreSQL password |
| `POSTGRES_DB` | `hexagon` | PostgreSQL database name |

The LLM, application, and DevUI variables in `.env.example` are not consumed by the current `docker-compose.yml`; configure them in the actual application's runtime environment.

### Common Commands

```bash
cd deploy

docker compose up -d                       # Start the infrastructure
docker compose ps                          # Show service status
docker compose logs -f                     # Follow all infrastructure logs
docker compose logs -f qdrant redis postgres
docker compose down                        # Stop and retain data volumes
```

The deployment Makefile shortcuts that match the current Compose configuration are:

```bash
make up       # Start the infrastructure
make status   # Show status
make down     # Stop and retain data volumes
make clean    # Stop and delete data volumes
```

> `make clean` and `docker compose down -v` permanently delete the local Qdrant, Redis, and PostgreSQL data volumes. Confirm that the data is no longer needed first.

### Infrastructure Troubleshooting

```bash
cd deploy

docker compose config --services
docker compose ps
docker compose logs qdrant
docker compose logs redis
docker compose logs postgres

# Check Qdrant on the default port
curl -f http://localhost:6333/healthz

# Check PostgreSQL
docker compose exec postgres pg_isready -U hexagon

# Check Redis when no password is configured
docker compose exec redis redis-cli ping
```

Adjust the commands if you changed ports, users, or passwords.

## Helm: Application-Integration Template

`helm/hexagon/` can generate the following Kubernetes resources:

- a Hexagon application Deployment and Service;
- an optional DevUI Deployment and Service;
- optional Qdrant, Redis, and PostgreSQL StatefulSets and Services;
- an optional Ingress, ServiceAccount, and LLM API key Secret managed by this chart.

### Usage Boundary

The chart's default image values do not identify a runtime image published or verified by this repository. Before installation, replace `app.image.repository` and `app.image.tag`. When DevUI is enabled, also replace `devui.image.repository` and `devui.image.tag`.

The custom images must satisfy the fixed runtime contract in the templates:

| Component | Required Entry Point | Fixed Container Port | Health Check | Writable Paths |
|-----------|----------------------|----------------------|--------------|----------------|
| Application | `/app/hexagon serve` | 8000 | `GET /health` | `/tmp`, `/app/data` |
| DevUI, when enabled | `/app/hexagon devui` | 8080 | `GET /health` | `/tmp` |

The images must also:

- run as non-root UID/GID `10001`;
- support a read-only root filesystem;
- read `QDRANT_URL`, `REDIS_URL`, `REDIS_PASSWORD`, and `POSTGRES_DSN` from environment variables;
- read `OPENAI_API_KEY` or `DEEPSEEK_API_KEY` when the corresponding provider is required.

The DevUI template sets `DEVUI_ADDR=:8080` and uses `HEXAGON_APP_URL` to point to the application Service in the same Release. If an image does not meet these requirements, adjust the Helm templates or the custom image before deployment; do not use the default installation as-is.

### Image and Infrastructure Configuration Examples

The following command shows only the required configuration. Replace the image coordinates with your own verified artifact:

```bash
cd deploy

helm install hexagon helm/hexagon \
  -n hexagon --create-namespace \
  --set app.image.repository=registry.example.com/agent-runtime \
  --set app.image.tag=1.0.0 \
  --set devui.enabled=false
```

When enabling DevUI, provide an image that satisfies the DevUI entry-point contract:

```bash
helm install hexagon helm/hexagon \
  -n hexagon --create-namespace \
  --set app.image.repository=registry.example.com/agent-runtime \
  --set app.image.tag=1.0.0 \
  --set devui.enabled=true \
  --set devui.image.repository=registry.example.com/devui-runtime \
  --set devui.image.tag=1.0.0
```

The chart creates Qdrant, Redis, and PostgreSQL by default. To use external services, set each corresponding `enabled` value to `false` and fully configure `external.qdrant.url`, `external.redis.*`, and `external.postgres.dsn`.

### Current Secret Behavior

When `secrets.openaiApiKey` or `secrets.deepseekApiKey` is non-empty, the chart creates and references a Kubernetes Secret managed by this chart. The chart currently has no option for referencing an existing Secret and no External Secrets integration. Evaluate this behavior against the target environment's secret-management requirements before deployment, and never commit real secrets to the repository.

### Verification Responsibility

The current CI validates root-module formatting, dependency consistency, `go vet`, minimum/current Go tests, race detection, and vulnerability scanning. It does not render, install, or publish the Helm chart. In an environment with Helm installed and access to the target Kubernetes cluster, inspect the rendered manifests and verify the image contract before installing or upgrading the Release.
