## ADDED Requirements

### Requirement: Agent discovers local binlog files

The agent SHALL determine the binlog directory from the MySQL server variable `log_bin_basename` (or from the `binlog_dir` config override) and SHALL list available binlog files via `SHOW BINARY LOGS`.

#### Scenario: Discover default directory
- **WHEN** the agent runs preflight or parse without a `binlog_dir` override
- **THEN** the binlog directory is resolved from `log_bin_basename` and the available files listed via `SHOW BINARY LOGS`

#### Scenario: Config override
- **WHEN** the agent config specifies a `binlog_dir`
- **THEN** that directory is used instead of the discovered one

### Requirement: Agent parses binlog files locally with mysqlbinlog

The agent SHALL parse binlog files from its local filesystem using mysqlbinlog, honoring targeted parse parameters: binlog file selection, target table, start time, end time, start position, and stop position.

#### Scenario: Targeted table filter
- **WHEN** a parse request includes a `target_table`
- **THEN** the parsed output is filtered to events for that table

#### Scenario: Time range
- **WHEN** a parse request includes start and end times
- **THEN** mysqlbinlog is invoked with the corresponding start/stop datetime options

#### Scenario: Position range
- **WHEN** a parse request includes start and stop positions
- **THEN** the parse honors the position range

#### Scenario: Binlog file selection
- **WHEN** a parse request specifies binlog file names
- **THEN** only those files are parsed

#### Scenario: mysqlbinlog not found
- **WHEN** mysqlbinlog cannot be located by auto-discovery or the `mysqlbinlog_path` override
- **THEN** the agent returns an error explaining the resolution failure

### Requirement: Agent resolves column metadata for reverse SQL

The agent SHALL resolve real column names from `information_schema` when generating reverse SQL from row events, rather than positional `col_N` placeholders.

#### Scenario: Column name lookup
- **WHEN** row events are converted to reverse SQL
- **THEN** the reverse SQL uses the actual column names of the target table

### Requirement: Agent returns parse and preflight results to the server

The agent SHALL respond to a `pitr_parse` command with the parsed events and generated reverse SQL (preview capped at 1000 entries), and SHALL respond to a `preflight` command with the per-check pass/fail results.

#### Scenario: Parse response
- **WHEN** the server sends a `pitr_parse` command
- **THEN** the agent returns the reverse SQL batch and a preview capped at 1000 entries as the command result

#### Scenario: Preflight response
- **WHEN** the server sends a `preflight` command
- **THEN** the agent runs the preflight checks and returns pass/fail for each check

#### Scenario: Parse failure
- **WHEN** parsing fails (e.g. missing mysqlbinlog, unreadable binlog file)
- **THEN** the agent responds with status `error` and a message describing the failure

### Requirement: Agent executes rollback locally with checkpointing

The agent SHALL execute the reverse SQL batch on its local MySQL connection in batches with foreign-key-aware ordering, SHALL persist a checkpoint after each batch, and SHALL push progress updates to the server during execution.

#### Scenario: Execute rollback
- **WHEN** the server sends a `pitr_execute` command with a reverse SQL batch
- **THEN** the agent executes the batches in order, persists checkpoints, and pushes progress updates

#### Scenario: Cancelled execution
- **WHEN** the agent receives a cancel during execution
- **THEN** the in-flight batch completes, remaining batches are skipped, and the partial result is reported
