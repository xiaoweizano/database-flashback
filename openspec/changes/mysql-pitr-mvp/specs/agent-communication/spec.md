## ADDED Requirements

### Requirement: Reverse WebSocket tunnel (Agent side)
The Agent SHALL establish an outbound WebSocket connection to the Web platform.
The Agent SHALL authenticate the connection using a client TLS certificate (mTLS).
The Agent SHALL maintain a heartbeat every 30 seconds; if no response for 90 seconds, it SHALL close the connection and reconnect with exponential backoff (1s, 2s, 4s, 8s, max 60s).
The Agent SHALL support command multiplexing — multiple concurrent PITR operations on the same WebSocket connection.

#### Scenario: Successful WebSocket connection and authentication
- **WHEN** the Agent starts with a valid client certificate and platform URL
- **THEN** it establishes a WebSocket connection, completes the mTLS handshake, and receives a session ID from the platform

#### Scenario: Reconnect after network disruption
- **WHEN** the WebSocket connection drops due to a 30-second network outage
- **THEN** the Agent detects the disconnection via heartbeat timeout, waits 1 second, reconnects, and resumes normal operation

### Requirement: Agent command protocol
The Agent SHALL accept commands from the platform via the WebSocket tunnel.
Command format: `{"cmd": "<command_id>", "type": "<command_type>", "params": {...}}`
Response format: `{"cmd": "<command_id>", "status": "ok"|"error", "result": {...}}`
Supported command types: `preflight`, `pitr_parse`, `pitr_execute`, `status`, `shutdown`

#### Scenario: Execute preflight command from platform
- **WHEN** the platform sends: {"cmd": "req-001", "type": "preflight", "params": {"dsn_id": "mysql-1"}}
- **THEN** the Agent runs preflight checks and responds: {"cmd": "req-001", "status": "ok", "result": {"...preflight data..."}}

### Requirement: Platform WebSocket hub (Server side)
The Web platform SHALL maintain a WebSocket hub that accepts Agent connections.
The hub SHALL verify each incoming connection's client TLS certificate against the platform CA.
The hub SHALL maintain a map of connected agents by agent_id and route commands accordingly.
The hub SHALL emit agent connection/disconnection events for the audit log.
The hub SHALL limit concurrent connections per agent to 1 (reject duplicate connections).

#### Scenario: Agent connects with valid certificate
- **WHEN** an Agent connects with a client certificate signed by the platform CA
- **THEN** the hub extracts the agent_id from the certificate CN, registers the connection, and sends a session_confirmed message

#### Scenario: Reject connection from unknown certificate
- **WHEN** a connection attempt uses a client certificate not signed by the platform CA
- **THEN** the hub rejects the TLS handshake and logs the failed attempt

### Requirement: Platform CA certificate management
The platform SHALL include an internal CA that issues client certificates for Agents.
CA root certificate SHALL be generated during initial platform deployment.
Certificates SHALL be valid for 90 days with automatic renewal (Agent requests new cert 7 days before expiry).
The platform SHALL support certificate revocation — revoked agents cannot connect.

#### Scenario: Agent auto-renews certificate
- **WHEN** an agent's certificate is 85 days old (5 days before 90-day expiry)
- **THEN** the Agent sends a CSR via the WebSocket tunnel, the platform CA issues a new certificate, and the Agent rotates to the new cert without disconnection
