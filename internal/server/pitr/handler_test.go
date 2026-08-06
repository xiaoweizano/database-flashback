package pitr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a-shan/mysql-pitr/internal/connector"
	"github.com/a-shan/mysql-pitr/internal/server/agent"
	"github.com/a-shan/mysql-pitr/internal/server/audit"
	"github.com/a-shan/mysql-pitr/internal/server/auth"
	"github.com/a-shan/mysql-pitr/internal/server/org"
	"github.com/a-shan/mysql-pitr/internal/ws"
	"github.com/a-shan/mysql-pitr/internal/ws/hub"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCommander implements AgentCommander with canned responses per command
// type, so the operation state machine can be tested without a live agent.
type fakeCommander struct {
	connected   bool
	preflightFn func() (*ws.Response, error)
	parseFn     func() (*ws.Response, error)
	executeFn   func() (*ws.Response, error)
	sent        []ws.Command
}

func (f *fakeCommander) IsConnected(agentID string) bool { return f.connected }

func (f *fakeCommander) SendToAgent(ctx context.Context, agentID string, cmd ws.Command) (*ws.Response, error) {
	f.sent = append(f.sent, cmd)
	switch cmd.Type {
	case ws.CmdPreflight:
		return f.preflightFn()
	case ws.CmdPITRParse:
		return f.parseFn()
	case ws.CmdPITRExecute:
		return f.executeFn()
	default:
		return &ws.Response{Cmd: cmd.Cmd, Status: ws.StatusOK, Result: "ok"}, nil
	}
}

func (f *fakeCommander) SetProgressHandler(fn hub.ProgressHandler) {}

// successCommander returns a fake commander wired for a successful
// preflight → parse → execute flow.
func successCommander() *fakeCommander {
	return &fakeCommander{
		connected: true,
		preflightFn: func() (*ws.Response, error) {
			return &ws.Response{Cmd: "preflight", Status: ws.StatusOK, Result: agentPreflightResult{
				Preflight: &connector.PreflightResult{
					Status:  connector.PreflightPass,
					Version: "8.0.32",
					Checks: []connector.PreflightCheck{
						{Name: "MySQL Version", Status: connector.PreflightPass},
					},
				},
				BinlogFiles: []string{"mysql-bin.000001"},
				TotalSize:   1024,
			}}, nil
		},
		parseFn: func() (*ws.Response, error) {
			sqls := make([]string, 1250)
			preview := make([]ReverseSqlEntry, 1000)
			for i := range sqls {
				sqls[i] = "DELETE FROM `orders` WHERE `id` = 1 LIMIT 1;"
				if i < len(preview) {
					preview[i] = ReverseSqlEntry{Sequence: i + 1, SqlType: "DELETE", TableName: "orders", ReverseSql: sqls[i]}
				}
			}
			return &ws.Response{Cmd: "parse", Status: ws.StatusOK, Result: agentParseResult{
				TotalRows:  1250,
				ReverseSql: sqls,
				Preview:    preview,
				SQLSample:  sqls[0],
			}}, nil
		},
		executeFn: func() (*ws.Response, error) {
			return &ws.Response{Cmd: "execute", Status: ws.StatusOK, Result: agentExecuteResult{
				RowsAffected:     1250,
				BatchesCompleted: 10,
				BatchesTotal:     10,
			}}, nil
		},
	}
}

// test helpers

type testFixture struct {
	handler    *Handler
	opStore    *InMemoryOperationStore
	agentStore *agent.InMemoryAgentStore
	orgStore   *org.InMemoryOrgStore
	auditStore *audit.InMemoryAuditStore
	userStore  *auth.InMemoryUserStore
	commander  *fakeCommander
	secret     []byte
}

func setupTest(t *testing.T) *testFixture {
	t.Helper()
	opStore := NewInMemoryOperationStore()
	agentStore := agent.NewInMemoryAgentStore()
	orgStore := org.NewInMemoryOrgStore()
	auditStore := audit.NewInMemoryAuditStore()
	userStore := auth.NewInMemoryUserStore()
	secret := []byte("pitr-test-secret")
	commander := successCommander()
	handler := NewHandler(opStore, agentStore, orgStore, auditStore, secret, commander)
	return &testFixture{
		handler:    handler,
		opStore:    opStore,
		agentStore: agentStore,
		orgStore:   orgStore,
		auditStore: auditStore,
		userStore:  userStore,
		commander:  commander,
		secret:     secret,
	}
}

// userSeq makes fixture user emails unique even when two users are created
// within the same clock tick.
var userSeq int64

func (f *testFixture) createUser(t *testing.T) string {
	t.Helper()
	userSeq++
	user := &auth.User{
		Email:          fmt.Sprintf("%s-%d-%d@example.com", t.Name(), time.Now().UnixNano(), userSeq),
		HashedPassword: "hash",
	}
	err := f.userStore.Create(user)
	require.NoError(t, err)
	return user.ID
}

func (f *testFixture) createOrg(t *testing.T, userID string) string {
	t.Helper()
	org := &org.Organization{Name: "Test Org"}
	err := f.orgStore.Create(org)
	require.NoError(t, err)
	err = f.orgStore.AddMember(org.ID, userID, "admin")
	require.NoError(t, err)
	return org.ID
}

func (f *testFixture) createAgent(t *testing.T, orgID string) string {
	t.Helper()
	agt := &agent.AgentRecord{
		OrgID:    orgID,
		Hostname: "db-01.example.com",
		Approved: true,
	}
	err := f.agentStore.Create(agt)
	require.NoError(t, err)
	return agt.ID
}

func (f *testFixture) authenticatedRequest(t *testing.T, method, target string, body interface{}, userID string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, target, &buf)
	claims := &auth.Claims{UserID: userID, Email: "test@example.com"}
	req = req.WithContext(auth.ContextWithClaims(req.Context(), claims))
	return req
}

// authenticatedRouteRequest builds an authenticated request with a chi route
// context so chi.URLParam("id") resolves to id (handlers invoked directly,
// without a router, need this).
func (f *testFixture) authenticatedRouteRequest(t *testing.T, method, target, id string, body interface{}, userID string) *http.Request {
	t.Helper()
	req := f.authenticatedRequest(t, method, target, body, userID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// waitForOperationState polls the store until the operation reaches one of the
// desired states or the timeout expires.
func waitForOperationState(t *testing.T, store *InMemoryOperationStore, opID string, desired []OperationState, timeout time.Duration) *Operation {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		op, err := store.Get(opID)
		if err == nil {
			for _, d := range desired {
				if op.State == d {
					return op
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Final read to report current state.
	op, err := store.Get(opID)
	if err != nil {
		t.Fatalf("operation %s not found after timeout", opID)
	}
	t.Fatalf("operation %s did not reach desired state %v within %v (current: %s)",
		opID, desired, timeout, op.State)
	return nil
}

// ---------- Start ----------

func TestStart_Success_PreviewMode(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	req := f.authenticatedRequest(t, http.MethodPost, "/api/pitr/start", startRequest{
		AgentID:      agentID,
		TargetTable:  "orders",
		RecoveryTime: "2026-07-08T14:00:00Z",
		Mode:         "preview",
	}, userID)
	w := httptest.NewRecorder()
	f.handler.Start(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var resp startResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.OperationID)
	assert.Equal(t, StatePreflight, resp.Status)

	// Wait for the async simulation to reach previewed (should take ~1s).
	op := waitForOperationState(t, f.opStore, resp.OperationID,
		[]OperationState{StatePreviewed, StateCompleted, StateFailed, StateCancelled},
		5*time.Second)
	assert.Equal(t, StatePreviewed, op.State)
	assert.NotNil(t, op.PreflightRes)
	assert.NotNil(t, op.ParseRes)
	assert.Equal(t, int64(1250), op.ParseRes.RowsAffected)
}

func TestStart_Success_ExecuteMode(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	req := f.authenticatedRequest(t, http.MethodPost, "/api/pitr/start", startRequest{
		AgentID:      agentID,
		TargetTable:  "orders",
		RecoveryTime: "2026-07-08T14:00:00Z",
		Mode:         "execute",
	}, userID)
	w := httptest.NewRecorder()
	f.handler.Start(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var resp startResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.OperationID)
	assert.Equal(t, StatePreflight, resp.Status)

	// Wait for the async simulation to complete (should take ~4s).
	op := waitForOperationState(t, f.opStore, resp.OperationID,
		[]OperationState{StateCompleted, StateFailed, StateCancelled},
		10*time.Second)
	assert.Equal(t, StateCompleted, op.State)
	assert.NotNil(t, op.ExecRes)
	assert.Equal(t, int64(1250), op.ExecRes.RowsRestored)
	assert.NotNil(t, op.Progress)
	assert.Equal(t, 10, op.Progress.BatchesComplete)
}

func TestStart_MissingFields(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	tests := []struct {
		name string
		body map[string]string
	}{
		{"empty body", map[string]string{}},
		{"missing agent_id", map[string]string{"target_table": "x", "recovery_time": "2026-01-01T00:00:00Z", "mode": "preview"}},
		{"missing target_table", map[string]string{"agent_id": agentID, "recovery_time": "2026-01-01T00:00:00Z", "mode": "preview"}},
		{"missing recovery_time", map[string]string{"agent_id": agentID, "target_table": "x", "mode": "preview"}},
		{"missing mode", map[string]string{"agent_id": agentID, "target_table": "x", "recovery_time": "2026-01-01T00:00:00Z"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := f.authenticatedRequest(t, http.MethodPost, "/api/pitr/start", tc.body, userID)
			w := httptest.NewRecorder()
			f.handler.Start(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestStart_InvalidMode(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	req := f.authenticatedRequest(t, http.MethodPost, "/api/pitr/start", startRequest{
		AgentID:      agentID,
		TargetTable:  "orders",
		RecoveryTime: "2026-07-08T14:00:00Z",
		Mode:         "invalid",
	}, userID)
	w := httptest.NewRecorder()
	f.handler.Start(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStart_InvalidRecoveryTime(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	req := f.authenticatedRequest(t, http.MethodPost, "/api/pitr/start", startRequest{
		AgentID:      agentID,
		TargetTable:  "orders",
		RecoveryTime: "not-a-timestamp",
		Mode:         "preview",
	}, userID)
	w := httptest.NewRecorder()
	f.handler.Start(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStart_AgentNotFound(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	_ = f.createOrg(t, userID)

	req := f.authenticatedRequest(t, http.MethodPost, "/api/pitr/start", startRequest{
		AgentID:      "nonexistent",
		TargetTable:  "orders",
		RecoveryTime: "2026-07-08T14:00:00Z",
		Mode:         "preview",
	}, userID)
	w := httptest.NewRecorder()
	f.handler.Start(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStart_NotOrgMember(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	otherUser := f.createUser(t)
	orgID := f.createOrg(t, otherUser) // other user owns the org
	agentID := f.createAgent(t, orgID)

	req := f.authenticatedRequest(t, http.MethodPost, "/api/pitr/start", startRequest{
		AgentID:      agentID,
		TargetTable:  "orders",
		RecoveryTime: "2026-07-08T14:00:00Z",
		Mode:         "preview",
	}, userID)
	w := httptest.NewRecorder()
	f.handler.Start(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestStart_NoAuth(t *testing.T) {
	f := setupTest(t)

	req := httptest.NewRequest(http.MethodPost, "/api/pitr/start",
		bytes.NewReader([]byte(`{"agent_id":"x","target_table":"y","recovery_time":"2026-01-01T00:00:00Z","mode":"preview"}`)))
	w := httptest.NewRecorder()
	f.handler.Start(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ---------- Status ----------

func TestStatus_Success(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	op := &Operation{
		OrgID:        orgID,
		AgentID:      agentID,
		TargetTable:  "orders",
		RecoveryTime: time.Date(2026, 7, 8, 14, 0, 0, 0, time.UTC),
		Mode:         "preview",
		State:        StatePreviewed,
		ParseRes:     &ParseSummary{RowsAffected: 500, SQLSample: "DELETE FROM x;"},
	}
	err := f.opStore.Create(op)
	require.NoError(t, err)

	req := f.authenticatedRouteRequest(t, http.MethodGet, "/api/pitr/"+op.ID+"/status", op.ID, nil, userID)
	w := httptest.NewRecorder()
	f.handler.Status(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp statusResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, op.ID, resp.ID)
	assert.Equal(t, StatePreviewed, resp.State)
	assert.NotNil(t, resp.ParseRes)
	assert.Equal(t, int64(500), resp.ParseRes.RowsAffected)
}

func TestStatus_NotFound(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	f.createOrg(t, userID)

	req := f.authenticatedRouteRequest(t, http.MethodGet, "/api/pitr/nonexistent/status", "nonexistent", nil, userID)
	w := httptest.NewRecorder()
	f.handler.Status(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStatus_NoAuth(t *testing.T) {
	f := setupTest(t)

	req := httptest.NewRequest(http.MethodGet, "/api/pitr/xxx/status", nil)
	w := httptest.NewRecorder()
	f.handler.Status(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestStatus_NotMember(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	otherUser := f.createUser(t)
	orgID := f.createOrg(t, otherUser)
	agentID := f.createAgent(t, orgID)

	op := &Operation{
		OrgID:   orgID,
		AgentID: agentID,
		State:   StatePreflight,
	}
	err := f.opStore.Create(op)
	require.NoError(t, err)

	req := f.authenticatedRouteRequest(t, http.MethodGet, "/api/pitr/"+op.ID+"/status", op.ID, nil, userID)
	w := httptest.NewRecorder()
	f.handler.Status(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ---------- Cancel ----------

func TestCancel_Success(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	op := &Operation{
		OrgID:       orgID,
		AgentID:     agentID,
		TargetTable: "orders",
		State:       StatePreflight,
	}
	err := f.opStore.Create(op)
	require.NoError(t, err)

	req := f.authenticatedRouteRequest(t, http.MethodPost, "/api/pitr/"+op.ID+"/cancel", op.ID, nil, userID)
	w := httptest.NewRecorder()
	f.handler.Cancel(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp cancelResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, op.ID, resp.OperationID)
	assert.Equal(t, StateCancelled, resp.Status)

	// Verify audit entry was created.
	entries, err := f.auditStore.Query(audit.AuditFilter{OrgID: orgID})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "cancelled", entries[0].Status)
}

func TestCancel_InvalidState(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	op := &Operation{
		OrgID:   orgID,
		AgentID: agentID,
		State:   StateCompleted,
	}
	err := f.opStore.Create(op)
	require.NoError(t, err)

	req := f.authenticatedRouteRequest(t, http.MethodPost, "/api/pitr/"+op.ID+"/cancel", op.ID, nil, userID)
	w := httptest.NewRecorder()
	f.handler.Cancel(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestCancel_NotFound(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	f.createOrg(t, userID)

	req := f.authenticatedRouteRequest(t, http.MethodPost, "/api/pitr/nonexistent/cancel", "nonexistent", nil, userID)
	w := httptest.NewRecorder()
	f.handler.Cancel(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCancel_NoAuth(t *testing.T) {
	f := setupTest(t)

	req := httptest.NewRequest(http.MethodPost, "/api/pitr/xxx/cancel", nil)
	w := httptest.NewRecorder()
	f.handler.Cancel(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ---------- Preview ----------

func TestPreview_Success(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	op := &Operation{
		OrgID:       orgID,
		AgentID:     agentID,
		TargetTable: "orders",
		State:       StatePreviewed,
		ParseRes: &ParseSummary{
			ParsedAt:     time.Now(),
			RowsAffected: 750,
			SQLSample:    "DELETE FROM orders WHERE id IN (1, 2, 3);",
		},
	}
	err := f.opStore.Create(op)
	require.NoError(t, err)

	req := f.authenticatedRouteRequest(t, http.MethodGet, "/api/pitr/"+op.ID+"/preview", op.ID, nil, userID)
	w := httptest.NewRecorder()
	f.handler.Preview(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp previewResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, op.ID, resp.OperationID)
	assert.Equal(t, int64(750), resp.RowsAffected)
	assert.Contains(t, resp.SQLSample, "DELETE FROM orders")
}

func TestPreview_NotReady(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	op := &Operation{
		OrgID:   orgID,
		AgentID: agentID,
		State:   StatePreflight,
	}
	err := f.opStore.Create(op)
	require.NoError(t, err)

	req := f.authenticatedRouteRequest(t, http.MethodGet, "/api/pitr/"+op.ID+"/preview", op.ID, nil, userID)
	w := httptest.NewRecorder()
	f.handler.Preview(w, req)
	assert.Equal(t, http.StatusPreconditionFailed, w.Code)
}

func TestPreview_NoAuth(t *testing.T) {
	f := setupTest(t)

	req := httptest.NewRequest(http.MethodGet, "/api/pitr/xxx/preview", nil)
	w := httptest.NewRecorder()
	f.handler.Preview(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ---------- Progress ----------

func TestProgress_Success(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	op := &Operation{
		OrgID:   orgID,
		AgentID: agentID,
		State:   StateExecuting,
		Progress: &ProgressInfo{
			BatchesComplete:    3,
			BatchesTotal:       10,
			RowsRestored:       375,
			EstimatedRemaining: "21s",
		},
	}
	err := f.opStore.Create(op)
	require.NoError(t, err)

	req := f.authenticatedRouteRequest(t, http.MethodGet, "/api/pitr/"+op.ID+"/progress", op.ID, nil, userID)
	w := httptest.NewRecorder()
	f.handler.Progress(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp progressResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, op.ID, resp.OperationID)
	assert.Equal(t, StateExecuting, resp.State)
	assert.Equal(t, 3, resp.BatchesComplete)
	assert.Equal(t, 10, resp.BatchesTotal)
	assert.Equal(t, int64(375), resp.RowsRestored)
	assert.Equal(t, "21s", resp.EstimatedRemaining)
}

func TestProgress_NoProgressYet(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	op := &Operation{
		OrgID:   orgID,
		AgentID: agentID,
		State:   StatePreviewed,
	}
	err := f.opStore.Create(op)
	require.NoError(t, err)

	req := f.authenticatedRouteRequest(t, http.MethodGet, "/api/pitr/"+op.ID+"/progress", op.ID, nil, userID)
	w := httptest.NewRecorder()
	f.handler.Progress(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp progressResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, op.ID, resp.OperationID)
	assert.Equal(t, StatePreviewed, resp.State)
	assert.Equal(t, 0, resp.BatchesComplete)
	assert.Equal(t, 0, resp.BatchesTotal)
	assert.Equal(t, int64(0), resp.RowsRestored)
	assert.Equal(t, "", resp.EstimatedRemaining)
}

func TestProgress_NoAuth(t *testing.T) {
	f := setupTest(t)

	req := httptest.NewRequest(http.MethodGet, "/api/pitr/xxx/progress", nil)
	w := httptest.NewRecorder()
	f.handler.Progress(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestProgress_NotFound(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	f.createOrg(t, userID)

	req := f.authenticatedRouteRequest(t, http.MethodGet, "/api/pitr/nonexistent/progress", "nonexistent", nil, userID)
	w := httptest.NewRecorder()
	f.handler.Progress(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ---------- Execute (explicit, user-selected SQL) ----------

func TestExecute_SelectedSQL(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	// Start a preview operation and wait for it to reach the previewed state.
	req := f.authenticatedRequest(t, http.MethodPost, "/api/pitr/start", startRequest{
		AgentID:      agentID,
		TargetTable:  "orders",
		RecoveryTime: "2026-07-08T14:00:00Z",
		Mode:         "preview",
	}, userID)
	w := httptest.NewRecorder()
	f.handler.Start(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var startResp startResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&startResp))

	op := waitForOperationState(t, f.opStore, startResp.OperationID,
		[]OperationState{StatePreviewed, StateCompleted, StateFailed, StateCancelled}, 5*time.Second)
	require.Equal(t, StatePreviewed, op.State)

	// Execute only a subset of the generated statements.
	selected := []string{"DELETE FROM `orders` WHERE `id` = 1 LIMIT 1;"}
	req = f.authenticatedRouteRequest(t, http.MethodPost, "/api/pitr/"+op.ID+"/execute", op.ID,
		map[string]interface{}{"sql": selected}, userID)
	w = httptest.NewRecorder()
	f.handler.Execute(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)

	// The async execution must complete and send exactly the selected SQL.
	op = waitForOperationState(t, f.opStore, op.ID, []OperationState{StateCompleted, StateFailed, StateCancelled}, 5*time.Second)
	require.Equal(t, StateCompleted, op.State)
	require.NotNil(t, op.ExecRes)

	require.Len(t, f.commander.sent, 3) // preflight, parse, execute
	execCmd := f.commander.sent[2]
	assert.Equal(t, ws.CmdPITRExecute, execCmd.Type)
	sqls, ok := execCmd.Params["sql"].([]string)
	require.True(t, ok)
	assert.Equal(t, selected, sqls)
}

func TestExecute_EmptySQLUsesFullBatch(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	req := f.authenticatedRequest(t, http.MethodPost, "/api/pitr/start", startRequest{
		AgentID:      agentID,
		TargetTable:  "orders",
		RecoveryTime: "2026-07-08T14:00:00Z",
		Mode:         "preview",
	}, userID)
	w := httptest.NewRecorder()
	f.handler.Start(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	var startResp startResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&startResp))

	op := waitForOperationState(t, f.opStore, startResp.OperationID,
		[]OperationState{StatePreviewed, StateCompleted, StateFailed, StateCancelled}, 5*time.Second)
	require.Equal(t, StatePreviewed, op.State)

	// No "sql" in the body -> the full generated batch is executed.
	req = f.authenticatedRouteRequest(t, http.MethodPost, "/api/pitr/"+op.ID+"/execute", op.ID,
		map[string]interface{}{}, userID)
	w = httptest.NewRecorder()
	f.handler.Execute(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)

	op = waitForOperationState(t, f.opStore, op.ID, []OperationState{StateCompleted, StateFailed, StateCancelled}, 5*time.Second)
	require.Equal(t, StateCompleted, op.State)

	require.Len(t, f.commander.sent, 3)
	execCmd := f.commander.sent[2]
	sqls, ok := execCmd.Params["sql"].([]string)
	require.True(t, ok)
	require.Len(t, sqls, 1250) // full batch from the fake parse
}

func TestExecute_NotPreviewed(t *testing.T) {
	f := setupTest(t)
	userID := f.createUser(t)
	orgID := f.createOrg(t, userID)
	agentID := f.createAgent(t, orgID)

	op := &Operation{OrgID: orgID, AgentID: agentID, TargetTable: "orders", Mode: "preview", State: StatePreflight}
	require.NoError(t, f.opStore.Create(op))

	req := f.authenticatedRouteRequest(t, http.MethodPost, "/api/pitr/"+op.ID+"/execute", op.ID, nil, userID)
	w := httptest.NewRecorder()
	f.handler.Execute(w, req)
	require.Equal(t, http.StatusConflict, w.Code)
}

func TestExecute_NoAuth(t *testing.T) {
	f := setupTest(t)

	req := httptest.NewRequest(http.MethodPost, "/api/pitr/xxx/execute", nil)
	w := httptest.NewRecorder()
	f.handler.Execute(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
