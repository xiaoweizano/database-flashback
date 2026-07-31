## Context

Current state (as of 2026-07-31):

- The **server** performs the entire PITR flow inside its own process: it takes a `mysql_dsn` from the web UI, runs preflight checks, resolves the binlog directory via `SHOW VARIABLES LIKE 'log_bin_basename'`, and then either reads binlog files from its own local disk (`os.Stat` + native parser) or shells out to `mysqlbinlog --read-from-remote-server` with the user's credentials. Rollback is executed over the same DSN.
- The **agent** (`cmd/agent`) is only an offline one-shot CLI (`flashback`). It already contains the better parsing pipeline: local mysqlbinlog invocation plus real column names resolved from `information_schema`.
- The **reverse WebSocket stack** (`internal/ws`) is fully built and tested but dead code: Hub (mTLS client-cert CN → agentID, duplicate rejection, revocation), agent `Client` (heartbeat, backoff reconnect, UUID-correlated commands), `Dispatcher` (no handlers registered), internal CA with CSR-based `cert_renewal`, and command types `preflight`, `pitr_parse`, `pitr_execute`, `status`, `shutdown`, `cert_renewal`.
- `internal/connector/types.go` already models targeted parsing: `ParseRequest{BinlogFiles, TargetTable, StartTime, EndTime, StartPos, StopPos}`.
- All server stores are in-memory; there is no database. `internal/config` already provides an AES-256-GCM encrypted agent config with `MySQLConfig`, `ServerConfig{URL, CertFile, KeyFile, CAFile}`, `DataDir`.

## Goals / Non-Goals

**Goals:**
- Agent runs as a persistent daemon on the MySQL host and becomes the sole provider of binlog data: it reads local binlog files, parses them with mysqlbinlog, and executes rollbacks on its own MySQL connection.
- Server stops accessing binlog files directly; the web wizard selects an agent instead of pasting a DSN.
- Web-supplied targeted parsing parameters (binlog files, target table, time range, position range) reach the agent and constrain the parse.
- Reuse the existing `internal/ws` stack and `flashback` pipeline; no new transport.
- Keep the offline `flashback` CLI working unchanged.

**Non-Goals:**
- No database or durable store for server state (stays in-memory; agent-connection state included).
- No gRPC or new transports beyond the existing reverse WebSocket.
- No streaming replication or continuous binlog tailing.
- No multi-agent load balancing beyond routing an operation to its selected agent.

## Decisions

### D1. New `serve` subcommand for the agent daemon
Add `mysql-pitr-agent serve --config=...` (cobra) rather than converting `flashback` into a daemon. `serve` runs the WS client loop and registers dispatcher handlers (`preflight`, `pitr_parse`, `pitr_execute`, `status`, `shutdown`) that reuse the connector + parse/rollback pipeline from `cmd/agent/flashback.go`.
**Rationale:** keeps the offline CLI intact and separates lifecycle (systemd service) from one-shot operation. Alternative considered: daemonize `flashback` with a flag — rejected, it couples CLI UX to daemon lifecycle.

### D2. Dedicated mTLS listener on the server
Server gets a second listener (env `AGENT_LISTEN_ADDR`, default `:9443`) that terminates TLS with client-cert verification and upgrades `/ws/agent` connections into the `Hub`. The public web API (`LISTEN_ADDR`) stays plain HTTP.
**Rationale:** agent traffic already carries cert files in config; forcing TLS onto the public web endpoint for a single route is worse. Alternative considered: same port with mTLS on `/ws/agent` — rejected, would require TLS (and cert rotation) on the user-facing web server.

### D3. Production `CertStorage` is file-backed JSON
Implement the `CertStorage` interface with a JSON file in the server data dir (`/var/lib/mysql-pitr/certs.json`): issued certs + revocation list survive restarts.
**Rationale:** no database exists; in-memory-only revocation would forget revocations on restart. Alternative: introduce a DB — out of scope (Non-Goals).

### D4. Request/response commands + agent-pushed progress
Server sends correlated commands via `hub.SendToAgent`; long-running `pitr_parse`/`pitr_execute` responses arrive when done. During execution the agent pushes `pitr_progress` messages (one new command type added to the protocol) that update the server-side operation progress; the existing REST `GET /api/pitr/{id}/progress` polling is unchanged.
**Rationale:** frontend already polls REST every 2s; adding a streaming transport for progress is unnecessary. Alternative considered: SSE from server — rejected as a duplicate transport.

### D5. Targeted parsing parameters reuse `ParseRequest`
The wizard gains optional fields: binlog file(s), start time, start position, stop position (end time already exists as recovery time). The server validates them and forwards them verbatim in the `pitr_parse` params; the agent maps them onto `ParseRequest` and the mysqlbinlog invocation.
**Rationale:** the type already exists and matches the web-supplied-parameter requirement exactly.

### D6. Rollback executes on the agent host
`pitr_execute` runs the reverse-SQL batch on the agent's local MySQL connection (batched, FK-ordered, checkpointed — reusing `internal/rollback` + `internal/checkpoint`). The server keeps the operation state machine; the agent keeps the credentials. Cancel is a new `pitr_cancel` command type; the agent aborts between batches and reports partial state.

### D7. Operation states reflect agent connectivity
The operation state machine gains agent-dependent handling: start is rejected when the selected agent is not connected/approved; an agent disconnect mid-operation fails the operation with reason `agent_offline`. No auto-resume; the user retries as a new operation.

### D8. Config extends the existing encrypted config
Agent daemon config reuses `internal/config.Config` (`ServerConfig` URL/certs/CA already map 1:1 onto the WS client), adding optional overrides for `binlog_dir` and `mysqlbinlog_path`. No new config mechanism, no plaintext credentials.

### D9. Deployment follows the existing artifacts
`deploy/agent.service` and the Docker agent image run `serve`; the agent install script is updated; the server image drops `mariadb-client` (no longer shells out to mysqlbinlog). Windows note: systemd is primary; Task Scheduler/NSSM documented as an alternative.

## Risks / Trade-offs

- [Long-running WS commands vs. HTTP polling] → `pitr_progress` push keeps the REST progress endpoint live; heartbeat (30s/90s) + backoff reconnect already exist in the client.
- [Agent offline mid-operation leaves partial state] → state machine marks `failed(agent_offline)`; rollback checkpoint data stays on the agent host for inspection; no auto-resume.
- [90-day client cert expiry] → existing CSR `cert_renewal` flow + file-backed CertStorage; renewal is handled inline by the Hub.
- [mTLS trust: any cert signed by our CA can impersonate an agentID] → Hub revocation check + server refuses to route operations to unapproved agents; agentID comes from the cert CN and must match a registered/approved agent.
- [Credentials on agent host are higher-stakes] → preflight still enforces least privilege (SELECT, REPLICATION SLAVE/CLIENT); config is encrypted at rest.
- [Removing server-side direct binlog access is a breaking change] → staged migration below; old DSN-based flow is rejected with a clear "agent required" error.

## Migration Plan

1. **Wire transport**: server `/ws/agent` mTLS endpoint + Hub + file CertStorage; agent `serve` subcommand with `status`/`shutdown` handlers. Agent connects; no behavior change on the PITR path.
2. **Route through agent**: register `preflight`/`pitr_parse`/`pitr_execute` handlers on the agent; server `runOperation` sends commands to the connected agent; wizard selects an agent and sends targeted parse params. The old DSN path stays as fallback.
3. **Remove direct access**: drop `mysql_dsn` from the wizard, remove server-side binlog file reads and mysqlbinlog shell-outs; reject starts against unconnected agents.
4. **Deploy**: update `deploy/agent.service`, Docker images, install script, docs. Rollback = redeploy previous server image (operations list stays compatible).

## Open Questions

- CertStorage backend at scale (file JSON is fine while stores are in-memory; revisit if a DB lands).
- Progress granularity: per-phase initially; `pitr_progress` payload can carry batch counts for finer granularity later.
- Whether to persist operation history (out of scope; in-memory today, unchanged by this change).
