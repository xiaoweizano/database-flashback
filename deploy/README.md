# MySQL PITR — Deployment Guide

This guide covers deploying the MySQL PITR (Point-in-Time Recovery) system.

## Architecture

The system has two components:

- **agent** — deployed **on the MySQL host**. It reads the local binlog files,
  parses them with `mysqlbinlog`, executes rollbacks on its local MySQL
  connection, and serves the mysql-pitr-server over a long-lived mTLS
  WebSocket connection (`agent serve`). It never sends MySQL credentials to
  the server.
- **server** — the web dashboard + REST API. It never accesses binlog files
  or MySQL directly; all PITR operations are driven by sending commands to
  connected agents over the hub.

```
┌─────────────┐   wss:// (mTLS)   ┌──────────────┐
│  MySQL host │ ◄───────────────► │   server     │
│  + agent    │   commands/       │  :8080 web   │
│  (binlogs)  │   responses       │  :9443 mTLS  │
└─────────────┘                   └──────────────┘
```

---

## Table of Contents

1. [Quick Start (Docker)](#quick-start-docker)
2. [Quick Start (systemd / bare metal)](#quick-start-systemd--bare-metal)
3. [Configuration](#configuration)
4. [Docker Deployment](#docker-deployment)
5. [Systemd Deployment](#systemd-deployment)
6. [Windows Deployment](#windows-deployment)
7. [Building from Source](#building-from-source)
8. [Troubleshooting](#troubleshooting)

---

## Quick Start (Docker)

The compose stack includes a one-shot `provision` service that registers an
agent, issues its mTLS certificate from the server's internal CA, and writes
the encrypted agent config — so `docker compose up` brings up a working
agent-connected stack end to end.

```bash
git clone https://github.com/a-shan/mysql-pitr.git
cd mysql-pitr

# Start all services (provision runs once, then the agent connects)
docker compose up -d

# Watch the agent come online
docker compose logs -f agent
```

This starts:

- **mysql** — MySQL 8.0 with binary logging enabled (required for PITR)
- **provision** — one-shot: CA extraction, agent registration, cert issuance
- **agent** — `mysql-pitr-agent serve`, connected to the server hub
- **server** — web dashboard + API (`localhost:8080`) and the mTLS agent
  endpoint (`localhost:9443`)

---

## Quick Start (systemd / bare metal)

The agent runs as a daemon (`serve`) on the MySQL host.

### One-liner install

```bash
curl -fsSL https://github.com/a-shan/mysql-pitr/releases/latest/download/install.sh | sudo bash
```

The script installs the binary, writes a template config, and installs the
systemd unit. You must then:

1. Register the agent in the web console (Agents → Register) and copy its
   agent ID.
2. Issue an mTLS client certificate with CN = agent ID, signed by the
   server's internal CA (see below), or have the platform provision one.
3. Fill in `/etc/agent/config.json` (MySQL access, server URL, cert paths)
   and encrypt it.
4. Set the passphrase in `/etc/agent/env` and start the service.

### Issuing an agent certificate

The server maintains an internal CA at `AGENT_DATA_DIR/ca.json` (PEM strings
`caCert` / `caKey`). Generate a client cert signed by it:

```bash
openssl ecparam -name prime256v1 -genkey -noout -out /etc/agent/client-key.pem
openssl req -new -key /etc/agent/client-key.pem -subj "/CN=<agent-id>" -out /tmp/client.csr
openssl x509 -req -in /tmp/client.csr \
  -CA <(python3 -c "import json;print(json.load(open('ca.json'))['caCert'])") \
  -CAkey <(python3 -c "import json;print(json.load(open('ca.json'))['caKey'])") \
  -CAcreateserial -out /etc/agent/client.pem -days 90 -sha256
cp ca.pem /etc/agent/ca.pem   # server CA certificate
```

The agent ID defaults to the certificate's CommonName, so the CN must match
the ID registered in the web console.

---

## Configuration

### Server environment variables

| Variable | Default | Description |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | Address for the web API + dashboard |
| `AGENT_LISTEN_ADDR` | `:9443` | mTLS listener for the agent hub |
| `AGENT_DATA_DIR` | `./data` | Where the internal CA and revocations persist |
| `AGENT_CERT_HOSTS` | `localhost,server` | SANs for the server certificate agents verify |
| `JWT_SECRET` | insecure default | JWT signing key (always set in production) |
| `WEB_DIR` | `/usr/share/mysql-pitr/web` | Built frontend directory |

### Agent config file (`/etc/agent/config.json`)

The config is encrypted at rest (AES-256-GCM) with a passphrase. Write a
plaintext JSON and encrypt it:

```bash
mysql-pitr-agent config encrypt --input plain.json --output /etc/agent/config.json --passphrase 'change-me'
```

Plaintext shape:

```json
{
  "mysql": {
    "host": "127.0.0.1",
    "port": 3306,
    "user": "replicator",
    "password": "change-me",
    "database": "mysql"
  },
  "server": {
    "url": "wss://pitr.example.com:9443/ws/agent",
    "cert_file": "/etc/agent/client.pem",
    "key_file": "/etc/agent/client-key.pem",
    "ca_file": "/etc/agent/ca.pem"
  },
  "data_dir": "/var/lib/mysql-pitr",
  "binlog_dir": "",
  "mysqlbinlog_path": ""
}
```

| Field | Description |
|---|---|
| `mysql` | Credentials for the local MySQL (used for preflight, column metadata, rollback execution). Never transmitted to the server. |
| `server.url` | WebSocket endpoint, `wss://<server>:9443/ws/agent` |
| `server.cert_file` / `key_file` | mTLS client certificate (CN = agent ID) and key |
| `server.ca_file` | Server CA certificate used to verify the endpoint |
| `data_dir` | Where rollback checkpoints are persisted |
| `binlog_dir` | Optional override for the binlog directory (default: `log_bin_basename`) |
| `mysqlbinlog_path` | Optional override for the `mysqlbinlog` binary |

The MySQL user needs the privileges checked by preflight (SELECT on the
target schema, `REPLICATION SLAVE`, `REPLICATION CLIENT`).

---

## Docker Deployment

### Prerequisites

- Docker Engine 24+
- Docker Compose v2+

### Production docker-compose.yml

Use the included `docker-compose.yml` as a starting point. For production:

1. **Change default passwords** — override `MYSQL_ROOT_PASSWORD` and the
   provision/agent config values
2. **Persist MySQL data** — named volumes are already mounted; keep them
3. **Place the web server behind a reverse proxy** (nginx, Caddy, Traefik)
   with TLS; the agent endpoint (`:9443`) already speaks TLS with its own
   internal CA
4. **Resource limits** — add `deploy.resources.limits` to each service

### Building images yourself

```bash
# Agent only (includes mysqlbinlog via mariadb-client)
docker build --target=agent -t mysql-pitr-agent .

# Server only (includes React frontend; no mysql client needed)
docker build --target=server -t mysql-pitr-server .

# Both with compose
docker compose build
```

---

## Systemd Deployment

### Prerequisites

- Linux (amd64 or arm64)
- systemd (v240+)
- MySQL 8.0+ on the same host (so binlog files are locally readable)

### Manual service setup

1. **Install the binary** (see [Quick Start](#quick-start-systemd--bare-metal))

2. **Create the systemd unit**:

```bash
sudo cp deploy/agent.service /etc/systemd/system/agent.service
sudo systemctl daemon-reload
```

3. **Configure the agent** (plaintext → encrypted):

```bash
sudo mkdir -p /etc/agent
# Write plain.json per the shape above, then:
sudo mysql-pitr-agent config encrypt --input plain.json --output /etc/agent/config.json --passphrase 'change-me'
```

4. **Set the passphrase** (the unit reads `AGENT_PASSPHRASE` from env):

```bash
sudo tee /etc/agent/env <<EOF
AGENT_PASSPHRASE=change-me
EOF
sudo chmod 600 /etc/agent/env
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

## Windows Deployment

The agent runs on Windows too (e.g. MySQL on a Windows host). Run it as a
service with NSSM or Task Scheduler:

### NSSM

```powershell
nssm install mysql-pitr-agent "C:\Program Files\mysql-pitr\mysql-pitr-agent.exe" serve --config C:\etc\agent\config.json --passphrase change-me
nssm set mysql-pitr-agent AppDirectory "C:\Program Files\mysql-pitr"
nssm start mysql-pitr-agent
```

### Task Scheduler

Create a task that runs at startup with the same command line; tick
"Restart on failure". Note the default `mysqlbinlog_path` on Windows:
`C:\Program Files\MySQL\MySQL Server 8.0\bin\mysqlbinlog.exe` — set
`mysqlbinlog_path` in the config if it is not on PATH.

---

## Building from Source

### Prerequisites

- Go 1.22+
- Node.js 20+ (for frontend)

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
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/mysql-pitr-agent-linux-arm64 ./cmd/agent
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/mysql-pitr-agent-linux-amd64 ./cmd/agent
```

---

## Troubleshooting

| Problem | Likely cause | Fix |
|---|---|---|
| Agent can't connect to MySQL | Wrong credentials or network | Check `mysql` fields in the encrypted config; MySQL must be reachable from the agent host |
| Agent won't connect to the server | mTLS trust mismatch | Certificates must be signed by the server's internal CA (`AGENT_DATA_DIR/ca.json`); `server.url` hostname must match the server cert SAN (`AGENT_CERT_HOSTS`) |
| Agent connects but stays unapproved | Register flow not completed | Approve the agent in the web console (Agents page) |
| PITR start fails "agent is offline" | Agent not running/connected | Run `mysql-pitr-agent serve --config=... --passphrase=...`; check `journalctl -u agent` |
| Operation failed "agent_offline" | Agent disconnected mid-operation | Check agent logs/network; retry the operation once the agent reconnects |
| `mysqlbinlog` not found | MySQL tools not installed | Install `mysql-client`/`mariadb-client`, or set `mysqlbinlog_path` in the config |
| `binlog_format` must be ROW | MySQL not configured for ROW-based replication | Set `binlog_format=ROW` and `binlog_row_image=FULL` in my.cnf |
| Permission denied for /etc/agent | Install script not run as root | Run with `sudo` |
| Port 8080 already in use | Another process on that port | Change `LISTEN_ADDR` or use a reverse proxy on a different port |
| Server shows blank page | Frontend not built | Run `npm run build` in `web/` and ensure the binary can find `web/dist` |
