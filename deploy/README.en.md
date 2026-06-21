<div align="right">Language: <a href="README.md">中文</a> | English</div>

# Hexagon Deployment Guide

## Quick Start

### Docker Compose (Recommended for New Users)

Start all services in one command, including Qdrant, Redis, PostgreSQL, and other infrastructure:

```bash
cd deploy

# 1. Configure environment variables
cp .env.example .env
# Edit .env and fill in your LLM API Key

# 2. Start
make up

# 3. Access
# Main app:     http://localhost:8000
# Dev UI:       http://localhost:8080
# Redis Insight: http://localhost:8001
```

### Docker Compose (Development Mode)

For team developers who have already started their infrastructure via [docker-dev-env](https://github.com/hexagon-codes/docker-dev-env):

```bash
cd deploy

# 1. Ensure the dev-net network and middleware are ready
# docker network create dev-net  (already provided by docker-dev-env)

# 2. Configure environment variables
cp .env.dev.example .env

# 3. Start (application services only, connecting to external middleware)
make dev-up
```

### Kubernetes / Helm

```bash
cd deploy

# Self-contained mode (with bundled middleware)
make helm-install

# Using external infrastructure
helm install hexagon helm/hexagon/ \
  -n hexagon --create-namespace \
  --set qdrant.enabled=false \
  --set redis.enabled=false \
  --set postgres.enabled=false \
  --set external.qdrant.url=http://my-qdrant:6333 \
  --set external.redis.url=my-redis:6379 \
  --set external.postgres.dsn="postgres://user:pass@my-pg:5432/hexagon?sslmode=disable"
```

## Deployment Options Comparison

| Option | Use Case | Command |
|--------|----------|---------|
| `docker compose up` | Quick evaluation, demos, single-node deployment | `make up` |
| `docker compose -f docker-compose.dev.yml up` | Team development (reusing docker-dev-env) | `make dev-up` |
| `helm install` | Kubernetes cluster, production environment | `make helm-install` |

## Directory Structure

```
deploy/
├── docker-compose.yml          # Infrastructure services (Qdrant / Redis / PostgreSQL)
├── .env.example                # Environment variable template for full mode
├── .env.dev.example            # Environment variable template for development mode
├── Makefile                    # Shortcut commands
└── helm/hexagon/               # Helm Chart
    ├── Chart.yaml
    ├── values.yaml             # Switchable between bundled/external
    ├── .helmignore
    └── templates/
        ├── _helpers.tpl
        ├── deployment.yaml     # App + DevUI
        ├── statefulset.yaml    # Qdrant / Redis / PostgreSQL
        ├── service.yaml
        ├── ingress.yaml
        ├── secret.yaml
        ├── serviceaccount.yaml
        └── NOTES.txt           # Post-install notes
```

## Configuration Reference

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `OPENAI_API_KEY` | (empty) | OpenAI API Key |
| `DEEPSEEK_API_KEY` | (empty) | DeepSeek API Key |
| `LOG_LEVEL` | info | Log level (debug/info/warn/error) |
| `QDRANT_URL` | http://qdrant:6333 | Qdrant connection URL |
| `REDIS_URL` | redis:6379 | Redis connection URL |
| `REDIS_PASSWORD` | (empty) | Redis password |
| `POSTGRES_DSN` | postgres://hexagon:hexagon@postgres:5432/hexagon?sslmode=disable | PostgreSQL DSN |

### Helm Bundled/External Switching

In `values.yaml`, each middleware component has an `enabled` toggle:

```yaml
# Use bundled component (default)
qdrant:
  enabled: true

# Use external component
qdrant:
  enabled: false
external:
  qdrant:
    url: "http://my-qdrant:6333"
```

## Common Commands

```bash
# Docker Compose
make up              # Start all services
make down            # Stop services
make logs            # View logs
make status          # Check status
make restart         # Restart application
make clean           # Stop and delete data

# Development mode
make dev-up          # Start (connecting to docker-dev-env)
make dev-down        # Stop
make dev-logs        # View logs

# Helm
make helm-install    # Install
make helm-upgrade    # Upgrade
make helm-uninstall  # Uninstall
make helm-template   # Preview rendered output
```

## Troubleshooting

### Application Fails to Start

```bash
# View container logs
docker compose logs hexagon-app

# Check health status
docker compose ps

# Enter container for debugging
docker compose exec hexagon-app sh
```

### Cannot Connect to Infrastructure

```bash
# Verify infrastructure health
docker compose ps qdrant redis postgres

# Test network connectivity
docker compose exec hexagon-app wget -q -O- http://qdrant:6333/healthz
```

### Insufficient Disk Space

```bash
# Clean up unused Docker resources
docker system prune -f

# Clean up data volumes (Warning: this will delete data)
docker compose down -v
```
