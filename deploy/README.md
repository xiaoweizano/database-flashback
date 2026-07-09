# MySQL PITR — Deployment Guide

This guide covers deploying the MySQL PITR (Point-in-Time Recovery) agent and
server in production.

---

## Table of Contents

1. [Quick Start (Docker)](#quick-start-docker)
2. [Quick Start (systemd / bare metal)](#quick-start-systemd--bare-metal)
3. [Configuration](#configuration)
4. [Docker Deployment](#docker-deployment)
5. [Systemd Deployment](#systemd-deployment)
6. [Building from Source](#building-from-source)
7. [Troubleshooting](#troubleshooting)

---

## Quick Start (Docker)

The fastest way to get everything running:

```bash
# Clone the repository
git clone https://github.com/a-shan/mysql-pitr.git
cd mysql-pitr

# Start all services
docker compose up -d

# Check status
docker compose ps
```

This starts:
- **mysql** — MySQL 8.0 with binary logging enabled (required for PITR)
- **agent** — The PITR agent that monitors binary logs
- **server** — Web dashboard + API server (bound to `localhost:8080`)

### Using pre-built images

```bash
export VERSION=v0.1.0  # or latest tag

docker run -d \
  --name mysql-pitr-agent \
  -e MYSQL_DSN="root:password@tcp(mysql-host:3306)/mysql" \
  ghcr.io/a-shan/mysql-pitr-agent:${VERSION}

docker run -d \
  --name mysql-pitr-server \
  -p 8080:8080 \
  -e DATABASE_URL="root:password@tcp(mysql-host:3306)/pitr_server" \
  ghcr.io/a-shan/mysql-pitr-server:${VERSION}
```

---

## Quick Start (systemd / bare metal)

### One-liner install

```bash
curl -fsSL https://github.com/a-shan/mysql-pitr/releases/latest/download/install.sh | sudo bash
```

This script:
1. Detects your OS and architecture
2. Downloads the latest agent binary from GitHub Releases
3. Installs it to `/usr/local/bin/mysql-pitr-agent`
4. Creates a default config at `/etc/agent/config.json`
5. Optionally installs and starts the systemd service

### Manual binary install

```bash
# Download
VERSION=v0.1.0
curl -fsSL -o mysql-pitr-agent-linux-amd64.tar.gz \
  "https://github.com/a-shan/mysql-pitr/releases/download/${VERSION}/mysql-pitr-agent-linux-amd64.tar.gz"

# Extract and install
tar -xzf mysql-pitr-agent-linux-amd64.tar.gz
sudo install -m 0755 mysql-pitr-agent /usr/local/bin/mysql-pitr-agent

# Create config directory
sudo mkdir -p /etc/agent

# Run
mysql-pitr-agent flashback --config=/etc/agent/config.json
```

---

## Configuration

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `MYSQL_DSN` | — | MySQL DSN for the agent: `user:password@tcp(host:port)/db` |
| `LISTEN_ADDR` | `:8080` | Address for the HTTP server to bind to |
| `DATABASE_URL` | — | MySQL DSN for the web server's application database |
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |

### Config file (`/etc/agent/config.json`)

```json
{
  "mysql_dsn": "root:password@tcp(127.0.0.1:3306)/mysql",
  "flashback_dir": "/var/lib/mysql-pitr/flashback",
  "listen_addr": ":8080",
  "log_level": "info"
}
```

---

## Docker Deployment

### Prerequisites

- Docker Engine 24+
- Docker Compose v2+

### Production docker-compose.yml

Use the included `docker-compose.yml` as a starting point. For production:

1. **Change default passwords** — override `MYSQL_ROOT_PASSWORD` and DSN values
2. **Persist MySQL data** — the compose file mounts a named volume for MySQL data
3. **Add TLS** — place the server behind a reverse proxy (nginx, Caddy, Traefik)
4. **Resource limits** — add `deploy.resources.limits` to each service

Example resource-constrained override (`docker-compose.override.yml`):

```yaml
services:
  agent:
    deploy:
      resources:
        limits:
          cpus: "1"
          memory: 512M
  server:
    deploy:
      resources:
        limits:
          cpus: "0.5"
          memory: 256M
```

### Building images yourself

```bash
# Agent only
docker build --target=agent -t mysql-pitr-agent .

# Server only (includes React frontend)
docker build --target=server -t mysql-pitr-server .

# Both with compose
docker compose build
```

---

## Systemd Deployment

### Prerequisites

- Linux (amd64 or arm64)
- systemd (v240+)
- MySQL 8.0+ accessible from the host

### Manual service setup

1. **Install the binary** (see [Quick Start](#quick-start-systemd--bare-metal) above)

2. **Create the systemd unit**:

```bash
sudo cp deploy/agent.service /etc/systemd/system/agent.service
sudo systemctl daemon-reload
```

3. **Configure the agent**:

```bash
sudo mkdir -p /etc/agent
# Edit the config file
sudo nano /etc/agent/config.json
```

4. **Optionally set environment variables** (overrides config file values):

```bash
sudo tee /etc/agent/env <<EOF
MYSQL_DSN=root:password@tcp(127.0.0.1:3306)/mysql
LOG_LEVEL=info
EOF
```

5. **Start the service**:

```bash
sudo systemctl enable --now agent
sudo systemctl status agent
```

### Service management

```bash
# Status
systemctl status agent

# Logs
journalctl -u agent -f

# Restart
systemctl restart agent

# Stop
systemctl stop agent
```

---

## Building from Source

### Prerequisites

- Go 1.22+
- Node.js 20+ (for frontend)
- npm (for frontend)

### Build agent

```bash
go build -ldflags="-s -w" -o bin/mysql-pitr-agent ./cmd/agent
```

### Build server (with frontend)

```bash
# Build frontend
cd web && npm ci && npm run build && cd ..

# Build server binary
go build -ldflags="-s -w" -o bin/mysql-pitr-server ./cmd/server
```

### Cross-compile

```bash
# linux/arm64
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/mysql-pitr-agent-linux-arm64 ./cmd/agent

# linux/amd64
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/mysql-pitr-agent-linux-amd64 ./cmd/agent
```

---

## Troubleshooting

| Problem | Likely cause | Fix |
|---|---|---|
| Agent can't connect to MySQL | Wrong DSN or network | Verify `MYSQL_DSN` env var; check MySQL is reachable |
| `binlog_format` must be ROW | MySQL not configured for ROW-based replication | Set `binlog_format=ROW` and `binlog_row_image=FULL` in my.cnf |
| Permission denied for /etc/agent | Install script not run as root | Run with `sudo` |
| Port 8080 already in use | Another process on that port | Change `LISTEN_ADDR` or use a reverse proxy on a different port |
| Server shows blank page | Frontend not built | Run `npm run build` in `web/` and ensure the binary can find the `web/dist` directory |
