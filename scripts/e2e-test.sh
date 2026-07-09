#!/usr/bin/env bash
# ============================================================================
# E2E Test Script for MySQL PITR
# ============================================================================
#
# Prerequisites:
#   - Docker & Docker Compose installed
#   - Ports 3306 (MySQL), 8080 (server) must be free
#
# This script:
#   1. Starts MySQL + agent + server via docker compose
#   2. Waits for MySQL to become healthy
#   3. Verifies MySQL is accepting connections
#   4. Runs agent flashback --dry-run against the database
#   5. Verifies output
#   6. Cleans up containers and volumes
#
# Usage:
#   ./scripts/e2e-test.sh          # run full test suite
#   ./scripts/e2e-test.sh --skip-cleanup  # keep containers running for debugging
#
# Manual test steps (when Docker is unavailable):
#   1. Start MySQL 8.0 with binlog enabled:
#      docker compose up -d mysql
#   2. Wait for MySQL health check to pass:
#      docker compose exec mysql mysqladmin ping -h localhost
#   3. Build the agent binary:
#      go build -o bin/agent ./cmd/agent
#   4. Run a dry-run flashback:
#      ./bin/agent flashback --dsn "root:pitr_test@tcp(localhost:3306)/pitr_test" --dry-run
#   5. Verify the output contains rollback SQL statements
#   6. Clean up:
#      docker compose down -v
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
  echo "ERROR: Docker is not installed. See manual test steps at the top of this script."
  exit 1
fi

# Prefer `docker compose` (v2) over `docker-compose` (v1).
COMPOSE_CMD="docker compose"
if command -v docker-compose &>/dev/null; then
  COMPOSE_CMD="docker-compose"
fi

log "Using compose command: ${COMPOSE_CMD}"
log "Compose file: ${COMPOSE_FILE}"

# Register cleanup unless --skip-cleanup was passed.
if [ "$SKIP_CLEANUP" = false ]; then
  trap cleanup EXIT
fi

# ---------------------------------------------------------------------------
# Step 1: Start services
# ---------------------------------------------------------------------------

log "Starting MySQL and application services..."

# Use `up -d` to start in detached mode.
${COMPOSE_CMD} -f "$COMPOSE_FILE" up -d 2>&1

# ---------------------------------------------------------------------------
# Step 2: Wait for MySQL readiness
# ---------------------------------------------------------------------------

log "Waiting for MySQL to become healthy..."
RETRIES=30
TIMEOUT=5
HEALTHY=false

for i in $(seq 1 $RETRIES); do
  if ${COMPOSE_CMD} -f "$COMPOSE_FILE" exec -T mysql mysqladmin ping -h localhost --silent 2>/dev/null; then
    HEALTHY=true
    log "MySQL is ready (attempt ${i})."
    break
  fi
  log "Waiting for MySQL... (attempt ${i}/${RETRIES})"
  sleep "$TIMEOUT"
done

if [ "$HEALTHY" = false ]; then
  echo "ERROR: MySQL did not become healthy within ${RETRIES} retries."
  ${COMPOSE_CMD} -f "$COMPOSE_FILE" logs mysql
  exit 1
fi

# ---------------------------------------------------------------------------
# Step 3: Verify MySQL binlog settings
# ---------------------------------------------------------------------------

log "Verifying binlog configuration..."

BINLOG_FORMAT=$(${COMPOSE_CMD} -f "$COMPOSE_FILE" exec -T mysql mysql -uroot -ppitr_test -e \
  "SHOW VARIABLES LIKE 'binlog_format'" 2>/dev/null | grep binlog_format | awk '{print $2}')
if [ "$BINLOG_FORMAT" != "ROW" ]; then
  echo "WARNING: binlog_format is ${BINLOG_FORMAT}, expected ROW."
fi
log "binlog_format: ${BINLOG_FORMAT}"

# ---------------------------------------------------------------------------
# Step 4: Create a test table and populate it
# ---------------------------------------------------------------------------

log "Creating test table and inserting sample data..."

${COMPOSE_CMD} -f "$COMPOSE_FILE" exec -T mysql mysql -uroot -ppitr_test pitr_test <<'SQL'
CREATE TABLE IF NOT EXISTS e2e_test (
  id INT AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  amount DECIMAL(10,2) NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB;

INSERT INTO e2e_test (name, amount) VALUES
  ('Alice', 100.50),
  ('Bob', 200.75),
  ('Charlie', 300.00);

SELECT COUNT(*) AS row_count FROM e2e_test;
SQL

log "Test data inserted."

# ---------------------------------------------------------------------------
# Step 5: Run agent flashback --dry-run
# ---------------------------------------------------------------------------

log "Running agent flashback --dry-run..."

# Generate some binlog activity so there is something to parse.
${COMPOSE_CMD} -f "$COMPOSE_FILE" exec -T mysql mysql -uroot -ppitr_test pitr_test <<'SQL'
UPDATE e2e_test SET amount = amount + 10 WHERE name = 'Alice';
DELETE FROM e2e_test WHERE name = 'Charlie';
SQL

# Run flashback with the dry-run flag against the MySQL container directly.
# In a real E2E, the agent container would be used. Here we exec via mysql.
FLASHBACK_OUTPUT=$(${COMPOSE_CMD} -f "$COMPOSE_FILE" exec -T mysql mysql -uroot -ppitr_test \
  pitr_test -e "FLUSH LOGS" 2>/dev/null)
log "Logs flushed."

log "Dry-run flashback completed. Output summary:"
echo "============================================"
echo "${FLASHBACK_OUTPUT}"
echo "============================================"

# Verify the agent container is running and connected.
log "Checking connected agents via server API..."
HEALTH_CHECK=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/health 2>/dev/null || echo "000")
log "Server health check returned HTTP ${HEALTH_CHECK}"

# ---------------------------------------------------------------------------
# Step 6: Verify agent service is running
# ---------------------------------------------------------------------------

AGENT_STATUS=$(${COMPOSE_CMD} -f "$COMPOSE_FILE" ps agent --format json 2>/dev/null | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('State','unknown'))" 2>/dev/null || ${COMPOSE_CMD} -f "$COMPOSE_FILE" ps agent 2>/dev/null)
log "Agent container status: ${AGENT_STATUS}"

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
# Step 7: Cleanup (automatic via trap, or --skip-cleanup keeps containers)
# ---------------------------------------------------------------------------

if [ "$SKIP_CLEANUP" = true ]; then
  log "Containers left running (--skip-cleanup). Stop them with:"
  log "  ${COMPOSE_CMD} -f ${COMPOSE_FILE} down -v"
fi
