## 1. Foundation

- [ ] 1.1 Initialize Go module (`go.mod`) with project structure: cmd/agent/, cmd/server/, internal/{parser,rollback,checkpoint,ws,connector,config}, pkg/, web/
- [ ] 1.2 Implement database Connector interface and MySQL Connector (connect, binlog listing, privilege check, schema queries)
- [ ] 1.3 Set up project build system (Makefile, Dockerfile templates, linting, CI foundation)

## 2. Agent: binlog Parser

- [ ] 2.1 Implement MySQL binlog file reader — open/close/seek, binlog magic number validation, CRC32 checksum verification
- [ ] 2.2 Implement binlog event header parser — timestamp, event type, server-id, event length, position
- [ ] 2.3 Implement TABLE_MAP_EVENT parser — table ID, database/table name, column count, column type array
- [ ] 2.4 Implement ROW event parsers — WRITE_ROWS (INSERT), DELETE_ROWS, UPDATE_ROWS — with column metadata resolution from TABLE_MAP
- [ ] 2.5 Implement column value decoders: integer types (TINYINT-INT8-BIGINT), string/VARCHAR, DATE/DATETIME/TIMESTAMP, NULL, DECIMAL
- [ ] 2.6 Implement reverse SQL generation: INSERT rows → DELETE (PK match or all columns), DELETE rows → INSERT (original values), UPDATE rows → UPDATE (restore before-image)
- [ ] 2.7 Implement BLOB/TEXT/JSON column handling (hex encoding for binary data, JSON serialization)
- [ ] 2.8 Implement DDL event detection in binlog stream — record DDL marker (ALTER/CREATE/DROP), maintain schema context, warn user
- [ ] 2.9 Implement time-range filter for binlog events — return only events within user-selected recovery window

## 3. Agent: Rollback Engine

- [ ] 3.1 Implement batch transaction executor — split reverse SQL into batches of 1000 rows or 10MB, each batch in its own transaction
- [ ] 3.2 Implement checkpoint manager — persist batch progress to JSON file, detect resume from crash
- [ ] 3.3 Implement FK dependency ordering — query INFORMATION_SCHEMA for FK relationships, sort table processing order
- [ ] 3.4 Implement all 6 preflight checks: binlog availability, binlog_format=ROW check, DDL detection in window, permission check (REPLICATION SLAVE/CLIENT, SELECT), disk space check, MySQL version check
- [ ] 3.5 Implement dynamic batch size reduction for oversized transactions (BLOB/TEXT causing InnoDB limits)

## 4. Agent: WebSocket Communication

- [ ] 4.1 Implement Agent WebSocket client — outbound connection with mTLS, 30s heartbeat, exponential backoff reconnect (1s→60s max)
- [ ] 4.2 Implement Agent command dispatcher — JSON command/response protocol: preflight, pitr_parse, pitr_execute, status, shutdown
- [ ] 4.3 Implement platform WebSocket hub — connection pool, client cert → agent_id mapping, concurrent command routing, duplicate connection rejection
- [ ] 4.4 Implement platform internal CA — root certificate generation, CSR endpoint, client certificate signing (90-day validity), auto-renewal endpoint
- [ ] 4.5 Implement certificate lifecycle — 7-day-before-expiry renewal, revocation list, Agent-side certificate rotation without connection drop

## 5. Agent: Offline CLI Mode

- [ ] 5.1 Implement `agent flashback` CLI command — parse flags (--mysql-dsn, --target-table, --recovery-time, --output, --dry-run)
- [ ] 5.2 Implement offline workflow — connect to MySQL directly, run preflight, parse binlog, generate reverse SQL, execute or output to file
- [ ] 5.3 Implement encrypted config storage — AES-256-GCM config file encryption, key derivation from user-supplied passphrase, environment variable substitution
- [ ] 5.4 Optional: HashiCorp Vault integration for database credential management

## 6. Web Console Backend

- [ ] 6.1 Implement user auth system — register/login (email+password), JWT with 24h expiry + refresh tokens, session management
- [ ] 6.2 Implement organization management — create org, invite members, role-based access (admin/member)
- [ ] 6.3 Implement Agent registration REST API — registration token approval, certificate provisioning, status tracking (online/offline/error)
- [ ] 6.4 Implement PITR workflow REST API — POST /api/pitr/start, GET /api/pitr/:id/status, POST /api/pitr/:id/cancel
- [ ] 6.5 Implement PITR workflow state machine — states: preflight → confirmed → parsing → previewed → executing → completed/failed/cancelled
- [ ] 6.6 Implement audit logging system — store each operation with operator, timestamp, table, rows, status; queryable + exportable to CSV
- [ ] 6.7 Implement real-time execution progress polling — return current batch, total batches, rows restored, estimated time remaining

## 7. Web Console Frontend

- [ ] 7.1 Initialize React project (Vite + TypeScript + routing + layout)
- [ ] 7.2 Implement login/registration pages and org management pages (invite members, role management)
- [ ] 7.3 Implement Agent management page — list agents with status, view details, copy deploy command
- [ ] 7.4 Implement 5-step PITR wizard: (1) connect/select agent, (2) select table + time, (3) preflight review, (4) preview changes, (5) execute with real-time progress
- [ ] 7.5 Implement operation history page — filterable audit log table, CSV export
- [ ] 7.6 Implement error/empty/loading states across all pages — connection failed, no agents, large result set, concurrent operation warning
- [ ] 7.7 Install and configure Playwright E2E test framework; write first E2E test for PITR wizard flow

## 8. Deployment & CI/CD

- [ ] 8.1 Create Dockerfile.agent (multi-stage Go build → scratch/alpine) and Dockerfile.server (Go backend + nginx static file serve or separate Node build)
- [ ] 8.2 Create docker-compose.yml — agent + server + MySQL test instance for integration testing
- [ ] 8.3 Create GitHub Actions CI workflow — lint, unit test, integration test (Testcontainers), build binaries (linux/amd64, linux/arm64)
- [ ] 8.4 Create GitHub Actions release workflow — build binaries, build Docker images, create GitHub Release with changelog
- [ ] 8.5 Create Agent deployment guide and one-liner install script (`curl | bash`) with Docker and systemd support
- [ ] 8.6 Create systemd unit file for Agent — auto-restart, log to journald, configurable data/checkpoint directories

## 9. Testing & Hardening

- [ ] 9.1 Write unit tests for binlog parser — each ROW event type, column type, error condition (corrupted binlog, truncated event, checksum mismatch)
- [ ] 9.2 Write integration tests for rollback engine — batch commit, checkpoint persistence, resume from crash, FK ordering, large dataset stress test
- [ ] 9.3 Write integration tests for WebSocket tunnel — connect/disconnect/reconnect, command/response, mTLS auth, concurrent commands
- [ ] 9.4 Write full E2E test (Docker Compose) — spin up Agent + MySQL + backend → simulate DELETE → PITR → verify data restored
- [ ] 9.5 Write frontend E2E tests (Playwright) — PITR wizard flow, error states, empty states
- [ ] 9.6 Write integration tests for Web backend API — auth, org mgmt, agent registration, PITR workflow lifecycle, audit log queries
- [ ] 9.7 Error handling and logging audit — verify every error path returns actionable message, no silent failures
