## ADDED Requirements

### Requirement: Offline CLI flashback mode
The Agent SHALL support a local-only flashback mode via the command line, without requiring a WebSocket connection to the platform.
The command interface SHALL be: `agent flashback --mysql-dsn=<DSN> --target-table=<table> --recovery-time=<ISO8601> [--output=<file>] [--dry-run]`
In offline mode, the Agent SHALL: (1) connect directly to the MySQL instance, (2) run preflight checks, (3) parse binlog files, (4) generate reverse SQL to stdout or a file.
The `--dry-run` flag SHALL output the reverse SQL without executing it.
The `--output=<file>` flag SHALL write the reverse SQL to a file for manual review.

#### Scenario: Offline flashback with dry-run
- **WHEN** the user runs: `agent flashback --mysql-dsn="user:pass@tcp(localhost:3306)/db" --target-table="orders" --recovery-time="2026-07-08T14:30:00Z" --dry-run`
- **THEN** the Agent connects to MySQL, runs preflight checks, parses binlog, generates reverse SQL statements, and outputs them to stdout without executing

#### Scenario: Offline flashback execution
- **WHEN** the user runs: `agent flashback --mysql-dsn="user:pass@tcp(localhost:3306)/db" --target-table="orders" --recovery-time="2026-07-08T14:30:00Z" --output=rollback.sql`
- **THEN** the Agent generates reverse SQL, writes it to rollback.sql, and additionally executes it against the MySQL instance

### Requirement: Offline mode error handling
In offline mode, if the Agent cannot connect to the MySQL instance, it SHALL exit with a clear error message indicating the connection failure reason.
If the MySQL instance does not have binlog enabled or binlog_format is not ROW, the Agent SHALL exit with a specific error message explaining the requirement.
If the specified recovery time is out of binlog retention range, the Agent SHALL list the available binlog date range.

#### Scenario: MySQL connection failure in offline mode
- **WHEN** the MySQL DSN is unreachable (e.g., wrong host/port)
- **THEN** the Agent prints: "Error: Cannot connect to MySQL at <host>:<port> — <error details>" and exits with code 1

#### Scenario: Missing binlog in offline mode
- **WHEN** MySQL has binlog_format=STATEMENT (not ROW)
- **THEN** the Agent prints: "Error: binlog_format must be ROW for PITR recovery. Current: STATEMENT" and exits with code 1
