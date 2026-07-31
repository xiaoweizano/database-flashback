## ADDED Requirements

### Requirement: Agent runs as a persistent daemon

The agent SHALL provide a `serve` subcommand that runs as a persistent daemon: it loads the encrypted config, establishes a long-lived mTLS WebSocket connection to the server, and stays running to accept commands until stopped.

#### Scenario: Start daemon
- **WHEN** a user runs `mysql-pitr-agent serve --config=/etc/agent/config.json`
- **THEN** the daemon loads the config, connects to the server, and remains running

#### Scenario: Invalid config on startup
- **WHEN** the config file cannot be loaded or decrypted at daemon startup
- **THEN** the daemon logs an error and exits with a non-zero status

#### Scenario: Daemon restart
- **WHEN** the daemon process is restarted (e.g. by systemd)
- **THEN** it reconnects to the server and resumes accepting commands

### Requirement: Agent maintains connection with automatic reconnection

The agent SHALL keep the WebSocket connection alive with heartbeat messages and SHALL reconnect with exponential backoff (1s up to 60s, with jitter) when the connection is lost.

#### Scenario: Connection loss
- **WHEN** the connection to the server drops
- **THEN** the agent retries the connection with exponential backoff until it reconnects

#### Scenario: Heartbeat timeout
- **WHEN** no data is exchanged for the heartbeat interval
- **THEN** the agent sends a heartbeat ping within 30 seconds and considers the connection dead if no pong arrives within 90 seconds

### Requirement: Agent stores MySQL credentials locally

The agent SHALL store the MySQL connection configuration in the encrypted config file on the agent host, and SHALL NOT transmit MySQL credentials to the server in any command or response payload.

#### Scenario: Credentials never transmitted
- **WHEN** the agent serves preflight, parse, or execute results to the server
- **THEN** the MySQL password does not appear in any payload sent over the WebSocket

#### Scenario: Config encrypted at rest
- **WHEN** the agent config file contains a MySQL password
- **THEN** the file is encrypted with AES-256-GCM and requires the passphrase to load

### Requirement: Agent registers on connect

The agent SHALL identify itself to the server with the agentID from its mTLS client certificate CN on every connection, and the server SHALL associate the connection with that agent's registration record.

#### Scenario: Connect with client certificate
- **WHEN** the agent establishes an mTLS connection using its client certificate
- **THEN** the server maps the connection to the agentID from the certificate CN and records it as connected

#### Scenario: Duplicate connection
- **WHEN** the same agentID connects a second time while already connected
- **THEN** the server rejects the duplicate connection

### Requirement: Agent handles server-initiated shutdown

The agent SHALL respond to a `shutdown` command by stopping gracefully.

#### Scenario: Graceful shutdown
- **WHEN** the server sends a `shutdown` command
- **THEN** the agent finishes any in-flight command, closes the connection, and exits cleanly
