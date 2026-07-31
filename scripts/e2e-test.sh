#!/usr/bin/env bash
# ============================================================================
# E2E Test Script for MySQL PITR (agent-based architecture, host MySQL)
# ============================================================================
#
# Prerequisites:
#   - Docker & Docker Compose installed
#   - A MySQL 8.0 on the HOST with binlog enabled (log-bin=mysql-bin,
#     binlog-format=ROW, binlog-row-image=FULL) and a mysql client
#   - .env configured per .env.example (MYSQL_HOST, MYSQL_PASSWORD,
#     MYSQL_BINLOG_DIR_HOST)
#   - Ports 8080 (server web), 9443 (server mTLS) must be free
#
# This script:
#   1. Starts provision + agent + server via docker compose (no mysql container)
#   2. Waits for the provision step (CA extraction, agent registration, certs)
#   3. Waits for the agent to connect to the server hub
#   4. Creates a test table on the HOST MySQL, mutates it, and runs a full
#      PITR operation through the API (preflight -> parse -> execute)
#   5. Verifies the operation completed and the flashback restored rows
#   6. Cleans up containers and volumes
#
# Usage:
#   ./scripts/e2e-test.sh          # run full test suite
#   ./scripts/e2e-test.sh --skip-cleanup  # keep containers running for debugging
# ============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
COMPOSE_FILE="${PROJECT_DIR}/docker-compose.yml"

SKIP_CLEANUP=false
for arg in "$@"; do
  case "$arg" in
    --skip-cleanup) SKIP_CLEANUP=true ;;
  esac
done

# Host MySQL connection (same .env the compose stack uses).
MYSQL_HOST="${MYSQL_HOST:-host.docker.internal}"
MYSQL_PORT="${MYSQL_PORT:-3306}"
MYSQL_USER="${MYSQL_USER:-root}"
MYSQL_PASSWORD="${MYSQL_PASSWORD:?set MYSQL_PASSWORD in .env}"
MYSQL_TEST_DB="${MYSQL_TEST_DB:-pitr_test}"

mysql_cmd() {
  mysql -h"$MYSQL_HOST" -P"$MYSQL_PORT" -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$@"
}

# ---------------------------------------------------------------------------
# Helper functions
# ---------------------------------------------------------------------------

log() {
  echo "[e2e] $(date '+%H:%M:%S') $*"
}

cleanup() {
  log "Cleaning up containers and volumes..."
  docker compose -f "$COMPOSE_FILE" down -v --remove-orphans 2>/dev/null || true
  log "Cleanup complete."
}

# ---------------------------------------------------------------------------
# Pre-flight
# ---------------------------------------------------------------------------

if ! command -v docker &>/dev/null; then
  echo "ERROR: Docker is not installed."
  exit 1
fi

COMPOSE_CMD="docker compose"
if command -v docker-compose &>/dev/null; then
  COMPOSE_CMD="docker-compose"
fi

log "Using compose command: ${COMPOSE_CMD}"

# Register cleanup unless --skip-cleanup was passed.
if [ "$SKIP_CLEANUP" = false ]; then
  trap cleanup EXIT
fi

# ---------------------------------------------------------------------------
# Step 1: Start services
# ---------------------------------------------------------------------------

log "Starting server, provision, and agent services..."
${COMPOSE_CMD} -f "$COMPOSE_FILE" up -d 2>&1

# ---------------------------------------------------------------------------
# Step 2: Verify host MySQL is reachable and binlog is enabled
# ---------------------------------------------------------------------------

log "Checking host MySQL at $MYSQL_HOST:$MYSQL_PORT..."
RETRIES=15
TIMEOUT=5
HEALTHY=false

for i in $(seq 1 $RETRIES); do
  if mysqladmin -h"$MYSQL_HOST" -P"$MYSQL_PORT" -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" ping --silent 2>/dev/null; then
    HEALTHY=true
    log "Host MySQL is reachable (attempt ${i})."
    break
  fi
  log "Waiting for host MySQL... (attempt ${i}/${RETRIES})"
  sleep "$TIMEOUT"
done

if [ "$HEALTHY" = false ]; then
  echo "ERROR: host MySQL not reachable within ${RETRIES} retries."
  echo "Check .env (MYSQL_HOST/MYSQL_PORT/MYSQL_USER/MYSQL_PASSWORD) and that MySQL is running."
  exit 1
fi

BINLOG_FORMAT=$(mysql_cmd -N -e "SHOW VARIABLES LIKE 'binlog_format'" 2>/dev/null | awk '{print $2}')
if [ "$BINLOG_FORMAT" != "ROW" ]; then
  echo "ERROR: host MySQL binlog_format is '${BINLOG_FORMAT}', expected ROW."
  echo "Set binlog_format=ROW and binlog_row_image=FULL in my.ini, then restart MySQL."
  exit 1
fi
log "binlog_format: ${BINLOG_FORMAT}"

mysql_cmd -e "CREATE DATABASE IF NOT EXISTS ${MYSQL_TEST_DB}"

# ---------------------------------------------------------------------------
# Step 3: Wait for provisioning and agent connection
# ---------------------------------------------------------------------------

log "Waiting for provision step (agent registration + certs)..."
if ! ${COMPOSE_CMD} -f "$COMPOSE_FILE" wait provision --timeout 120 2>/dev/null \
   && [ "$(${COMPOSE_CMD} -f "$COMPOSE_FILE" ps -q provision 2>/dev/null | wc -l)" -gt 0 ]; then
  echo "ERROR: provision step did not complete successfully."
  ${COMPOSE_CMD} -f "$COMPOSE_FILE" logs provision
  exit 1
fi

# Login with the provision step's credentials (fixed in e2e-provision.sh) so
# we see the org and agent it registered.
EMAIL="e2e-provision@example.com"
PASS="e2e-pass-123"
TOKEN=$(curl -fsS -X POST http://localhost:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASS\"}" | python3 -c "import sys,json;print(json.load(sys.stdin)['token'])")
ORG_ID=$(curl -fsS http://localhost:8080/api/orgs \
  -H "Authorization: Bearer $TOKEN" | python3 -c "import sys,json;print(json.load(sys.stdin)['organizations'][0]['id'])")

log "Waiting for agent to connect to the hub..."
AGENT_ID=""
for i in $(seq 1 30); do
  AGENTS=$(curl -fsS "http://localhost:8080/api/agents?orgId=$ORG_ID" -H "Authorization: Bearer $TOKEN")
  AGENT_ID=$(echo "$AGENTS" | python3 -c "import sys,json; d=json.load(sys.stdin)['agents']; print(d[0]['id'] if d else '')")
  STATUS=$(echo "$AGENTS" | python3 -c "import sys,json; d=json.load(sys.stdin)['agents']; print(d[0]['status'] if d else '')")
  if [ "$STATUS" = "online" ]; then
    log "Agent $AGENT_ID is online (attempt ${i})."
    break
  fi
  log "Waiting for agent... status=$STATUS (attempt ${i}/30)"
  sleep 2
done

if [ "$STATUS" != "online" ]; then
  echo "ERROR: agent did not connect to the server hub."
  ${COMPOSE_CMD} -f "$COMPOSE_FILE" logs agent
  ${COMPOSE_CMD} -f "$COMPOSE_FILE" logs server
  exit 1
fi

# ---------------------------------------------------------------------------
# Step 4: Create a test table, mutate it, and run a PITR operation
# ---------------------------------------------------------------------------

log "Creating test table and sample data..."

mysql_cmd "$MYSQL_TEST_DB" <<'SQL'
DROP TABLE IF EXISTS e2e_test;
CREATE TABLE e2e_test (
  id INT AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  amount DECIMAL(10,2) NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB;

INSERT INTO e2e_test (name, amount) VALUES
  ('Alice', 100.50),
  ('Bob', 200.75),
  ('Charlie', 300.00);
SQL

RECOVERY_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sleep 2

# Mutations we want the flashback to undo (all after RECOVERY_TIME).
mysql_cmd "$MYSQL_TEST_DB" <<'SQL'
UPDATE e2e_test SET amount = amount + 10 WHERE name = 'Alice';
DELETE FROM e2e_test WHERE name = 'Charlie';
SQL

log "Starting PITR operation via API (agent_id=$AGENT_ID, recovery_time=$RECOVERY_TIME)..."
START_RESP=$(curl -fsS -X POST http://localhost:8080/api/pitr/start \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"agent_id\":\"$AGENT_ID\",\"target_table\":\"${MYSQL_TEST_DB}.e2e_test\",\"recovery_time\":\"$RECOVERY_TIME\",\"mode\":\"execute\"}")
OP_ID=$(echo "$START_RESP" | python3 -c "import sys,json;print(json.load(sys.stdin)['operationId'])")
log "Operation $OP_ID started."

# ---------------------------------------------------------------------------
# Step 5: Poll the operation to completion
# ---------------------------------------------------------------------------

log "Polling operation status..."
FINAL_STATE=""
for i in $(seq 1 60); do
  STATE=$(curl -fsS "http://localhost:8080/api/pitr/$OP_ID/status" \
    -H "Authorization: Bearer $TOKEN" | python3 -c "import sys,json;print(json.load(sys.stdin)['state'])")
  case "$STATE" in
    completed|cancelled|failed) FINAL_STATE="$STATE"; break ;;
  esac
  sleep 2
done

if [ "$FINAL_STATE" != "completed" ]; then
  echo "ERROR: operation did not complete. Final state: ${FINAL_STATE:-unknown}"
  curl -fsS "http://localhost:8080/api/pitr/$OP_ID/status" -H "Authorization: Bearer $TOKEN"
  echo ""
  ${COMPOSE_CMD} -f "$COMPOSE_FILE" logs agent
  exit 1
fi
log "Operation completed."

# ---------------------------------------------------------------------------
# Step 6: Verify the flashback restored the deleted row
# ---------------------------------------------------------------------------

log "Verifying rollback result..."
ROW_COUNT=$(mysql_cmd "$MYSQL_TEST_DB" -N -e \
  "SELECT COUNT(*) FROM e2e_test WHERE name = 'Charlie'" 2>/dev/null | tr -d '[:space:]')
if [ "$ROW_COUNT" = "1" ]; then
  log "PASS: deleted row 'Charlie' restored by the rollback."
else
  echo "ERROR: expected 1 row for 'Charlie' after rollback, got '$ROW_COUNT'"
  exit 1
fi

# ---------------------------------------------------------------------------
# Results
# ---------------------------------------------------------------------------

echo ""
echo "============================================"
echo "  E2E TEST PASSED"
echo "============================================"
echo ""
log "All checks completed successfully."

# ---------------------------------------------------------------------------
# Cleanup (automatic via trap, or --skip-cleanup keeps containers)
# ---------------------------------------------------------------------------

if [ "$SKIP_CLEANUP" = true ]; then
  log "Containers left running (--skip-cleanup). Stop them with:"
  log "  ${COMPOSE_CMD} -f ${COMPOSE_FILE} down -v"
fi
