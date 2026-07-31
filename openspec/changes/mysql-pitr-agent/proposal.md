## Why

Today the mysql-pitr-server must reach MySQL binlog files itself — either from its own filesystem or by shelling out to `mysqlbinlog --read-from-remote-server` with credentials the user pastes into the web UI (`mysql_dsn`). In the common deployment (server container, MySQL on a separate host), this coupling is fragile, exposes DB credentials to the web layer, and can't see binlogs the MySQL OS user can't read. Deploying a small agent on the MySQL host — which owns the binlog files, parses them locally via mysqlbinlog, and serves the results to the server over a secure channel — removes that coupling and makes remote PITR a first-class, credential-free path.

## What Changes

- **New agent daemon mode**: `mysql-pitr-agent` runs as a persistent daemon on the MySQL host (new `serve`/`daemon` subcommand beside the existing offline `flashback`), reading the local binlog directory and invoking mysqlbinlog on local files.
- **Wire up the existing reverse WS tunnel**: the already-built `internal/ws` stack (hub, agent client, dispatcher, internal CA, `preflight`/`pitr_parse`/`pitr_execute`/`status`/`shutdown` command protocol) becomes live: server exposes an mTLS `/ws/agent` endpoint backed by the Hub; the agent dials it, registers command handlers, and serves binlog data.
- **Server PITR operations execute through the agent**: `runOperation` in `internal/server/pitr/handler.go` sends commands to the connected agent instead of accessing binlog files directly. Targeted parsing parameters (binlog files, target table, start/end time, start/stop position) are passed from the web UI through the server to the agent.
- **Web wizard no longer requires `mysql_dsn`**: PITR creation targets a connected agent; DB credentials move from the browser to the agent's local encrypted config.
- **Agent-local execution of rollback**: `pitr_execute` runs the reverse-SQL batch on the MySQL host using the agent's own connection config (checkpointing preserved).
- **BREAKING** for the existing execution path: operations created with a DSN against an unconnected agent are rejected; the server's direct binlog access (local filesystem + `mysqlbinlog --read-from-remote-server` fallback) is removed.

## Capabilities

### New Capabilities

- `agent-daemon`: the agent runs as a persistent daemon (systemd-managed) on the MySQL host, holds the MySQL connection config locally (encrypted), maintains a long-lived mTLS connection to the server, and restarts automatically.
- `agent-binlog-service`: the agent reads the local binlog directory and parses binlog files with mysqlbinlog, honoring targeted parse parameters (binlog files, target table, start/end time, start/stop position); it serves parsed event data, reverse SQL, previews, and preflight results to the server over the command channel.
- `pitr-agent-execution`: the server routes PITR operations (preflight, parse, preview, execute, progress, cancel) to the connected agent via the command channel instead of direct MySQL/binlog access; targeted parse parameters flow from the web UI through the server to the agent.

### Modified Capabilities

- `pitr-operation-list` (from `web-agent-and-audit`): operation status must reflect agent connection state (e.g. agent offline during an operation), and the create form drops the DSN/credentials requirement.

## Impact

- **Code**: `cmd/agent` (new daemon subcommand + dispatcher handler registration reusing `cmd/agent/flashback.go` pipeline), `internal/server/router.go` (mTLS `/ws/agent` endpoint), `internal/server/pitr/handler.go` + `store.go` (agent-driven operation flow, removal of DSN/binlog access), `internal/ws` (hub/CA wiring, production `CertStorage`), `internal/config` (agent daemon config fields).
- **Web**: `web/src/pages/pitr/PITRWizardPage.tsx` and `web/src/api/pitr.ts` (drop `mysql_dsn`, add binlog/time/position targeting parameters); agents page shows connection state.
- **Deploy**: `deploy/agent.service` (daemon mode), Dockerfile/docker-compose agent image entrypoint, agent install script; server image no longer needs mariadb-client for the binlog path.
- **API**: new `POST /ws/agent` (mTLS WebSocket); PITR start request schema changes (agent-id-based, no DSN); agent status surfaced via existing agents API.
- **Dependencies**: gorilla/websocket (already in go.mod); no new runtime dependencies for the server's binlog path.
