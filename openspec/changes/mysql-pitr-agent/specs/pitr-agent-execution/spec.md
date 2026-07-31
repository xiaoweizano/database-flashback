## ADDED Requirements

### Requirement: PITR operations target a connected agent

A PITR operation SHALL be created against a connected, approved agent selected from the web UI, and SHALL NOT collect a MySQL DSN. The server SHALL reject start requests for agents that are not connected or not approved.

#### Scenario: Start with connected agent
- **WHEN** a user starts a PITR operation with an `agent_id` whose agent is connected and approved
- **THEN** the operation is created and executed through that agent

#### Scenario: Unavailable agent
- **WHEN** a user starts a PITR operation for an agent that is not connected or not approved
- **THEN** the server rejects the request with an error explaining the agent state

#### Scenario: No DSN collected
- **WHEN** a user submits the PITR create form
- **THEN** the payload contains no MySQL DSN and the server stores no database credentials

### Requirement: Server routes operation phases to the agent

The server SHALL execute the preflight, parse, and execute phases by sending correlated commands over the agent command channel, and SHALL NOT read binlog files or invoke mysqlbinlog itself.

#### Scenario: Preflight via agent
- **WHEN** an operation starts
- **THEN** the server sends a `preflight` command to the selected agent and waits for its result

#### Scenario: Parse via agent
- **WHEN** preflight passes
- **THEN** the server sends a `pitr_parse` command with the targeted parse parameters to the agent

#### Scenario: Execute via agent
- **WHEN** the parse produces a reverse SQL batch and the user confirms execution
- **THEN** the server sends a `pitr_execute` command to the agent

#### Scenario: No direct binlog access
- **WHEN** an operation runs
- **THEN** the server performs no binlog file reads and no mysqlbinlog shell-outs

#### Scenario: Agent response timeout
- **WHEN** the agent does not respond to a command within the configured timeout
- **THEN** the operation is marked failed with a timeout reason

### Requirement: Targeted parsing parameters from the web

The PITR create form SHALL accept optional targeted parsing parameters (binlog file names, start time, start position, stop position) in addition to the target table and recovery time, and the server SHALL forward them in the `pitr_parse` command.

#### Scenario: Optional parameters forwarded
- **WHEN** a user provides targeted parsing parameters
- **THEN** the parameters reach the agent's parse request unchanged

#### Scenario: Defaults when omitted
- **WHEN** a user omits the targeted parsing parameters
- **THEN** the agent parses using the operation defaults (recovery time as end time)

### Requirement: Progress and cancel flow through the agent channel

The server SHALL update operation progress from agent-pushed progress messages, and SHALL propagate a user cancel to the agent as a cancel command.

#### Scenario: Progress update
- **WHEN** the agent pushes a progress message during a long-running phase
- **THEN** the REST progress endpoint reflects the updated progress

#### Scenario: Cancel propagation
- **WHEN** a user cancels an operation
- **THEN** the server sends a cancel command to the agent and marks the operation cancelled

### Requirement: Operation state reflects agent connectivity

Each operation SHALL record its `agent_id`, and the server SHALL expose the agent's connection state with the operation. When the agent disconnects during an operation, the operation SHALL fail with reason `agent_offline`.

#### Scenario: Agent disconnects mid-operation
- **WHEN** the agent disconnects while an operation is running
- **THEN** the operation is marked failed with reason `agent_offline`

#### Scenario: Connection state surfaced
- **WHEN** a user views an operation or its agent
- **THEN** the agent's current connection state (connected or offline) is displayed
