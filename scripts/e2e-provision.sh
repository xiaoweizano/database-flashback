#!/bin/sh
# ============================================================================
# E2E provisioning: register an agent, issue its mTLS certificate from the
# server's internal CA, and write the encrypted agent config.
#
# Runs once inside the `provision` compose service (agent image). Expects:
#   - server:8080 reachable (REST API)
#   - /var/lib/mysql-pitr/ca.json (server CA, created on server startup)
#   - /etc/agent writable (agent-config volume)
# ============================================================================
set -e

apk add --no-cache python3 openssl curl jq >/dev/null 2>&1

SERVER_URL="http://server:8080"
DATA_DIR="/var/lib/mysql-pitr"
CONFIG_DIR="/etc/agent"
PASSPHRASE="${PITR_PASSPHRASE:-pitr-test}"

echo "[provision] waiting for server CA..."
for i in $(seq 1 60); do
  [ -f "$DATA_DIR/ca.json" ] && break
  sleep 1
done
if [ ! -f "$DATA_DIR/ca.json" ]; then
  echo "[provision] ERROR: server CA not found after 60s"
  exit 1
fi

echo "[provision] extracting CA material..."
python3 - "$DATA_DIR/ca.json" "$CONFIG_DIR" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    data = json.load(f)
out = sys.argv[2]
with open(out + "/ca.pem", "w") as f:
    f.write(data["caCert"])
with open(out + "/ca-key.pem", "w") as f:
    f.write(data["caKey"])
print("[provision] wrote ca.pem and ca-key.pem")
PY

echo "[provision] registering agent via API..."
# Fixed credentials shared with scripts/e2e-test.sh so the host-side script
# can log in as the same user and see this org and agent.
EMAIL="e2e-provision@example.com"
PASS="e2e-pass-123"

curl -fsS -X POST "$SERVER_URL/api/auth/register" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASS\"}" >/dev/null 2>&1 || true

TOKEN=$(curl -fsS -X POST "$SERVER_URL/api/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASS\"}" | jq -r .token)

ORG_ID=$(curl -fsS -X POST "$SERVER_URL/api/orgs" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"E2E Org"}' | jq -r .organization.id)

AGENT_ID=$(curl -fsS -X POST "$SERVER_URL/api/agents/register" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"orgId\":\"$ORG_ID\",\"hostname\":\"agent-1\"}" | jq -r .agent.id)

echo "[provision] agent id: $AGENT_ID"

echo "[provision] issuing client certificate (CN=$AGENT_ID)..."
openssl ecparam -name prime256v1 -genkey -noout -out "$CONFIG_DIR/client-key.pem"
openssl req -new -key "$CONFIG_DIR/client-key.pem" -subj "/CN=$AGENT_ID" -out /tmp/client.csr
openssl x509 -req -in /tmp/client.csr \
  -CA "$CONFIG_DIR/ca.pem" -CAkey "$CONFIG_DIR/ca-key.pem" -CAcreateserial \
  -out "$CONFIG_DIR/client.pem" -days 90 -sha256 2>/dev/null

echo "[provision] writing encrypted agent config..."
cat > "$CONFIG_DIR/plain.json" <<EOF
{
  "mysql": {
    "host": "mysql",
    "port": 3306,
    "user": "root",
    "password": "pitr_test",
    "database": "pitr_test"
  },
  "server": {
    "url": "wss://server:9443/ws/agent",
    "cert_file": "$CONFIG_DIR/client.pem",
    "key_file": "$CONFIG_DIR/client-key.pem",
    "ca_file": "$CONFIG_DIR/ca.pem"
  },
  "data_dir": "/var/lib/mysql-pitr"
}
EOF

mysql-pitr-agent config encrypt \
  --input "$CONFIG_DIR/plain.json" \
  --output "$CONFIG_DIR/config.json" \
  --passphrase "$PASSPHRASE"

rm -f "$CONFIG_DIR/plain.json" "$CONFIG_DIR/ca-key.pem" "$CONFIG_DIR/ca-key.pem.srl"

echo "[provision] done — agent $AGENT_ID ready to start"
