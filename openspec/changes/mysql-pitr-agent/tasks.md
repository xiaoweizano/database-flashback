## 1. Agent daemon mode

- [x] 1.1 Add `serve` cobra subcommand in `cmd/agent/serve.go` (flags: `--config`), wiring the WS `Client` (ServerURL/certs/CA from config) with the dispatcher
- [x] 1.2 Register dispatcher handlers in `cmd/agent`: `status` (returns version, connection state, MySQL connectivity) and `shutdown` (graceful stop)
- [x] 1.3 Register `preflight` handler reusing `connector.Preflight` against the agent's local MySQL config; return per-check results in the command response
- [x] 1.4 Register `pitr_parse` handler reusing the `flashback` pipeline (local binlog dir discovery, mysqlbinlog invocation, `information_schema` column resolution); map `ParseRequest` params (binlog files, target table, start/end time, start/stop position) onto the parse
- [x] 1.5 Register `pitr_execute` handler running reverse SQL batches with `internal/rollback` + `internal/checkpoint`, pushing `pitr_progress` messages during execution and honoring cancel
- [x] 1.6 Add `pitr_progress` and `pitr_cancel` command types to `internal/ws/types.go`; implement cancel context propagation in the dispatcher/handlers

## 2. Server transport: hub wiring

- [x] 2.1 Add mTLS listener (`AGENT_LISTEN_ADDR`, default `:9443`) in `cmd/server/main.go` hosting `POST/GET /ws/agent` upgrade through `internal/ws/hub.Hub` with client-cert verification
- [x] 2.2 Implement production `CertStorage` (file-backed JSON in server data dir: issued certs + revocation list) and wire it into the hub
- [x] 2.3 Surface agent connection state from the hub through the agents API (`GET /api/agents`, `GET /api/agents/{id}`) so the web UI can show connected/offline
- [x] 2.4 Add server-side command correlation client (send command via hub, await matching response, timeout, `pitr_progress` listener) used by the PITR flow

## 3. PITR operation flow through the agent

- [x] 3.1 Rewrite `internal/server/pitr/handler.go` `runOperation` to drive preflight/parse/execute via the hub command client instead of direct connector/binlog access
- [x] 3.2 Change `POST /api/pitr/start` to require `agent_id` (connected + approved), reject unavailable agents, and drop `mysql_dsn` from the request schema; remove DSN storage from `Operation`
- [x] 3.3 Add agent connection state (connected/offline) and `agent_offline` failure reason to the operation state machine and `GET /api/pitr` / `GET /api/pitr/{id}` responses
- [x] 3.4 Wire cancel: `POST /api/pitr/{id}/cancel` sends `pitr_cancel` to the agent; mark operation cancelled
- [x] 3.5 Remove server-side direct binlog access: `os.Stat` local parse path, `parseBinlogRemote` mysqlbinlog shell-out, and `findMySQLBinlog`/`mysqlbinlog_path` handling in the server PITR flow
- [x] 3.6 Update progress handling: agent-pushed `pitr_progress` updates the operation; `GET /api/pitr/{id}/progress` continues to serve it

## 4. Web UI

- [x] 4.1 Update `PITRWizardPage.tsx` + `web/src/api/pitr.ts`: agent selection from connected agents, remove `mysql_dsn`, add optional targeted parsing fields (binlog files, start time, start position, stop position)
- [x] 4.2 Show agent connection state (connected/offline badge) on the agents page and in the PITR wizard agent picker
- [x] 4.3 Surface agent-related failure states (agent_offline, not approved) in operation list/detail and wizard error handling

## 5. Config and deployment

- [ ] 5.1 Extend `internal/config` agent config with optional `binlog_dir` and `mysqlbinlog_path` overrides (encrypted, same mechanism)
- [x] 5.2 Update `deploy/agent.service` and `deploy/agent-install.sh` to run `serve` as the daemon entrypoint
- [x] 5.3 Update Dockerfile/docker-compose agent image entrypoint to `serve`; remove `mariadb-client` from the server image (no longer shells out to mysqlbinlog)
- [x] 5.4 Document daemon deployment (systemd primary, Windows Task Scheduler/NSSM alternative) in `deploy/README.md` and docs

## 6. Tests and verification

- [x] 6.1 Unit tests: dispatcher handlers (preflight/parse/execute params mapping, progress push, cancel), file-backed CertStorage, server command correlation client timeout/cancel
- [x] 6.2 Hub integration test: agent connects with mTLS, receives commands, duplicate connection rejection, `pitr_progress` delivery
- [x] 6.3 End-to-end: docker-compose stack — agent `serve` connects to server, wizard creates an operation against the agent, targeted params constrain the parse, rollback executes, cancel works
- [x] 6.4 Regression: offline `flashback` CLI still works; existing agent register/approve flow unchanged; `go vet`/`go test` and CI pass
