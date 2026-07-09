## ADDED Requirements

### Requirement: Batch rollback execution
The Agent SHALL execute reverse SQL in batches, each batch wrapped in its own database transaction.
The default batch size SHALL be 1000 rows or 10MB of SQL text, whichever is reached first.
When a batch transaction succeeds, the Agent SHALL commit it and record a checkpoint.
When a batch transaction fails with an error, the Agent SHALL rollback that batch and stop execution.
For BLOB/TEXT columns that cause oversized transactions, the batch size MUST be dynamically reduced.

#### Scenario: Successful batch execution
- **WHEN** the Agent executes 2500 reverse SQL statements with batch_size=1000
- **THEN** it commits 3 batches successfully and records checkpoints after batches 1, 2, and 3

#### Scenario: Batch failure with rollback
- **WHEN** the 3rd batch (statements 2001-2500) encounters a foreign key violation
- **THEN** the Agent rolls back that batch, reports 2000 successfully restored rows, and presents the error to the user

#### Scenario: Resume from checkpoint after agent restart
- **WHEN** the Agent crashes after committing batch 2 (2000 rows) and restarts
- **THEN** it reads the last checkpoint file, detects batches 1-2 are complete, and resumes execution from batch 3

### Requirement: FK-aware batch ordering
The Agent SHALL detect foreign key dependencies between affected tables and order batches accordingly.
A table with no FK dependencies SHALL be processed before tables that reference it.
The checkpoint manager SHALL record the FK-aware ordering plan at initialization time.

#### Scenario: FK-ordered batch processing
- **WHEN** the recovery window affects table `orders` (FK → `customers`) and table `customers`
- **THEN** the Agent processes `customers` rows first, then `orders` rows, respecting the FK constraint

#### Scenario: No FK dependencies (simple ordering)
- **WHEN** the recovery window affects only table `logs` (no FK references)
- **THEN** the Agent processes rows in chronological binlog order without reordering

### Requirement: Preflight check system
The Agent SHALL run a preflight check before any PITR operation.
Preflight checks include:
1. **binlog availability**: Check the earliest available binlog timestamp against user's selected recovery time
2. **binlog format**: Confirm binlog_format=ROW and binlog_row_image=FULL
3. **DDL detection**: Check if DDL events exist in the recovery window (warn user)
4. **Permission check**: Verify REPLICATION SLAVE, REPLICATION CLIENT, and SELECT privileges
5. **Disk space**: Check available disk space for temporary binlog processing
6. **Connection check**: Verify database connection and MySQL version compatibility (5.7 or 8.0)

#### Scenario: Preflight passes (ready to recover)
- **WHEN** all preflight checks pass
- **THEN** the Agent returns: {status: "pass", details: {binlog_earliest: "2026-07-08T10:00:00Z", format: "ROW", ddl_warning: null, permissions: ["REPLICATION_SLAVE", "REPLICATION_CLIENT", "SELECT"], disk_free_gb: 50}}

#### Scenario: Selected time before earliest binlog
- **WHEN** the user selects recovery time "2026-06-01T00:00:00Z" but earliest binlog is "2026-07-01T00:00:00Z"
- **THEN** the Agent returns: {status: "fail", reason: "Selected time (2026-06-01) is before earliest available binlog (2026-07-01). Cannot recover."}

### Requirement: Checkpoint persistence
The Agent SHALL persist checkpoint state to a local file at <agent_data_dir>/checkpoints/<recovery_id>.json.
The checkpoint file SHALL contain: recovery_id, table_name, recovery_time, total_batches, completed_batches, affected_table_dependencies, and the last committed batch sequence number.
After the recovery completes successfully, the checkpoint file SHALL be marked as complete and retained for audit purposes.

#### Scenario: Checkpoint file written after each batch
- **WHEN** the Agent completes batch 2 of 5
- **THEN** the checkpoint file contains: completed_batches=2, last_commit_timestamp=<ISO8601>

#### Scenario: Checkpoint file marked complete
- **WHEN** the Agent finishes the final batch (5 of 5)
- **THEN** the checkpoint file is updated with status="complete" and a final checksum of all rolled back rows
