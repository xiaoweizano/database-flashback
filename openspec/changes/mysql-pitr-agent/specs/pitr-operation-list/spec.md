## MODIFIED Requirements

### Requirement: PITR operation list endpoint

The system SHALL provide a `GET /api/pitr` endpoint that returns PITR operations filtered by `org_id`, `from` (ISO timestamp), and `to` (ISO timestamp) query parameters. All parameters SHALL be optional. Each returned operation SHALL include its `agentId`, the agent's connection state (connected or offline) at the time of the response, and the operation state including agent-related failure reasons such as `agent_offline`.

#### Scenario: List operations by org
- **WHEN** a user sends `GET /api/pitr?org_id=abc123`
- **THEN** the response returns HTTP 200 with `{"operations": [...]}` containing all PITR operations for that org

#### Scenario: List operations filtered by time range
- **WHEN** a user sends `GET /api/pitr?org_id=abc123&from=2025-01-01T00:00:00Z&to=2025-01-31T23:59:59Z`
- **THEN** the response only includes operations with `createdAt` within the specified range

#### Scenario: Missing org_id
- **WHEN** a user sends `GET /api/pitr` without `org_id`
- **THEN** the response returns HTTP 400 with an error message

#### Scenario: Agent connection state included
- **WHEN** an operation's agent is offline
- **THEN** the operation entry includes the `agentId` with its connection state set to offline
