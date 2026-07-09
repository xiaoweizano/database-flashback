## ADDED Requirements

### Requirement: User authentication and organization management
The Web platform SHALL support user registration and login (email + password or OAuth).
Each user SHALL belong to an organization (org).
Organization admins SHALL be able to invite members and manage their roles (admin / member).
Sessions SHALL use JWT tokens with 24-hour expiry and refresh token support.

#### Scenario: User registers and creates org
- **WHEN** a new user registers with email and password
- **THEN** the system creates a user account and prompts them to create or join an organization

#### Scenario: Org admin invites a member
- **WHEN** an org admin enters an email address to invite a new member
- **THEN** the system sends an invitation email; the recipient creates an account and joins the org with role=member

### Requirement: Agent registration and management
The Web platform SHALL support registering new Agents.
Registration flow: Agent generates a registration token → admin approves the token in console → Agent receives a client certificate from the platform CA.
The platform SHALL display a list of registered Agents with status (online/offline/error), MySQL version, and last seen timestamp.
Agent SHALL be associated with a specific organization.

#### Scenario: Admin approves agent registration
- **WHEN** an Agent registration request appears in the Web Console admin panel
- **THEN** the admin reviews the request details (hostname, MySQL version) and clicks "Approve" — the platform CA issues a certificate, and the Agent receives it via WebSocket

### Requirement: 5-step PITR workflow wizard
The Web platform SHALL provide a 5-step guided PITR recovery wizard:

1. **连接确认** (Connect): Select an Agent, verify it's online, review MySQL version and binlog config
2. **选择目标表** (Select Table): Browse and select the table to recover, enter recovery time point
3. **预检结果** (Preflight Review): View preflight results (binlog range, format check, DDL warnings). User confirms to proceed
4. **预览变更** (Preview Changes): View estimated affected rows, sample reverse SQL, and data difference summary. User confirms to execute
5. **执行结果** (Execute Result): Real-time progress bar showing batch execution, checkpoint status. Result summary: rows restored, errors, duration

Each step SHALL have "Back" and "Cancel" buttons. "Cancel" aborts the entire operation.

#### Scenario: Complete PITR wizard flow (happy path)
- **WHEN** a user completes all 5 steps of the PITR wizard successfully
- **THEN** the system records the operation in the audit log with: operator, timestamp, target table, recovery time, rows affected, and status="completed"

#### Scenario: User cancels mid-operation
- **WHEN** a user clicks "Cancel" during step 3 (after preflight, before execution)
- **THEN** the system cancels the operation, no SQL is executed, and the audit log records: status="cancelled_by_user"

#### Scenario: Preflight fails (binlog expired)
- **WHEN** step 3 (preflight) returns a failure — selected time before earliest binlog
- **THEN** the wizard displays the error message from the Agent, disables the "Continue" button, and suggests the user select an earlier table or later recovery time

### Requirement: Operation audit log
The Web platform SHALL maintain an immutable audit log of all PITR operations.
Each audit entry SHALL contain: operation_id, operator (user email), timestamp, org_id, agent_id, target_database, target_table, recovery_time, rows_affected, status (completed/failed/cancelled), and error_details (if any).
The audit log SHALL be viewable in the Web Console with filtering by date range, agent, and status.
The audit log SHALL be exportable as CSV.

#### Scenario: View operations filtered by date
- **WHEN** an admin views the audit log and filters by "last 7 days"
- **THEN** the system displays only operations from the past 7 days, showing all audit entry fields

### Requirement: Real-time batch execution progress
During PITR execution (step 5), the Web Console SHALL display real-time progress: completed batches, total batches, estimated rows restored, and estimated time remaining.
The frontend SHALL poll the backend every 2 seconds for the current execution status during step 5.
If the Agent disconnects during execution, the Web Console SHALL display a "Connection lost — agent may still be running" message and display the last known checkpoint state.

#### Scenario: Progress updates during batch execution
- **WHEN** the Agent is executing batch 3 of 10
- **THEN** the Web Console shows: "Progress: 3/10 batches | 12,000/40,000 rows restored | ~30s remaining"

#### Scenario: Agent disconnect during execution
- **WHEN** the WebSocket drops while batch execution is in progress
- **THEN** the Web Console shows: "Connection to agent lost — batch 2 confirmed complete. Agent will resume from checkpoint 3 when connection is restored."
