## ADDED Requirements

### Requirement: MySQL binlog ROW format reader
The Agent SHALL parse MySQL 5.7 and 8.0 binary log files in ROW format.
The parser MUST support the following binlog events: TABLE_MAP_EVENT, WRITE_ROWS_EVENT, UPDATE_ROWS_EVENT, DELETE_ROWS_EVENT.
The parser SHALL verify binlog checksum (CRC32) and report corruption errors.
The parser SHALL handle binlog files split across multiple physical files (rotation).

#### Scenario: Parse a single INSERT_ROWS event
- **WHEN** the parser reads a ROW format binlog containing an INSERT_ROWS_EVENT with 3 columns
- **THEN** it returns a RowEvent with type=INSERT, the correct column values, and the table ID matching a prior TABLE_MAP_EVENT

#### Scenario: Report CRC32 checksum mismatch
- **WHEN** the parser reads a binlog event with an invalid CRC32 checksum
- **THEN** it returns an error specifying the binlog position and the nature of the checksum failure

#### Scenario: Skip DDL events (ALTER TABLE)
- **WHEN** the parser encounters a QUERY_EVENT containing an ALTER TABLE or CREATE TABLE statement
- **THEN** it records the DDL as a marker event and continues parsing without error

### Requirement: Reverse SQL generation
The Agent SHALL generate reverse SQL for each parsed row event:
- INSERT rows → DELETE statements (matching by primary key or all columns)
- DELETE rows → INSERT statements (using the deleted row values)
- UPDATE rows → UPDATE statements (restoring original "before" values)
The Agent MUST preserve column order, data types, and character encoding.
The Agent SHALL handle NULL values, empty strings, and binary data (BLOB) correctly in generated SQL.

#### Scenario: Reverse an INSERT row event
- **WHEN** the parser produces a RowEvent with type=INSERT, columns (id=1, name='Alice', email='alice@example.com')
- **THEN** the generated reverse SQL is: `DELETE FROM \`table\` WHERE \`id\` = 1 AND \`name\` = 'Alice' AND \`email\` = 'alice@example.com' LIMIT 1;`

#### Scenario: Reverse an UPDATE row event
- **WHEN** the parser produces a RowEvent with type=UPDATE, before=(id=1, name='Alice'), after=(id=1, name='Bob')
- **THEN** the generated reverse SQL is: `UPDATE \`table\` SET \`name\` = 'Alice' WHERE \`id\` = 1;`

#### Scenario: Handle binary data in BLOB column
- **WHEN** a row event contains a BLOB/BINARY column with non-printable bytes
- **THEN** the reverse SQL MUST use hex encoding (X'...') for that column value

### Requirement: DDL detection in time range
The Agent SHALL detect whether any DDL events (ALTER, DROP, TRUNCATE, CREATE) exist within the user-selected recovery time range.
When DDL events are detected, the Agent MUST warn the user that schema changes occurred during the recovery window and the reverse SQL may not be accurate for events that span the DDL boundary.
The Agent SHALL track the schema context across DDL events so that row events after a DDL are mapped to the correct column structure.

#### Scenario: DDL warning for recovery window with ALTER TABLE
- **WHEN** the recovery time range contains an ALTER TABLE ADD COLUMN event and DML events on both sides
- **THEN** the Agent returns a preflight warning: "DDL detected in recovery window — events before and after schema change may have different column structures"
