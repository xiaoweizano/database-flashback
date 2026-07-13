package agent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a-shan/mysql-pitr/internal/server/auth"
	"github.com/a-shan/mysql-pitr/internal/server/org"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// test helpers

func setupAgentTest(t *testing.T) (*Handler, *InMemoryAgentStore, *org.InMemoryOrgStore, *auth.InMemoryUserStore, []byte) {
	t.Helper()
	agentStore := NewInMemoryAgentStore()
	orgStore := org.NewInMemoryOrgStore()
	userStore := auth.NewInMemoryUserStore()
	secret := []byte("agent-test-secret")
	handler := NewHandler(agentStore, orgStore, secret)
	return handler, agentStore, orgStore, userStore, secret
}

func createTestUser(t *testing.T, store *auth.InMemoryUserStore) string {
	t.Helper()
	user := &auth.User{
		Email:          t.Name() + "@example.com",
		HashedPassword: "hash",
	}
	err := store.Create(user)
	require.NoError(t, err)
	return user.ID
}

func createTestOrg(t *testing.T, store *org.InMemoryOrgStore, adminID string) *org.Organization {
	t.Helper()
	o := &org.Organization{Name: "Test Org"}
	err := store.Create(o)
	require.NoError(t, err)
	err = store.AddMember(o.ID, adminID, "admin")
	require.NoError(t, err)
	return o
}

func authenticatedRequest(t *testing.T, method, target string, body interface{}, userID string, _ []byte) *http.Request {
	t.Helper()
	req := newRequest(method, target, body)
	claims := &auth.Claims{UserID: userID, Email: "admin@example.com"}
	req = req.WithContext(auth.ContextWithClaims(req.Context(), claims))
	return req
}

func newRequest(method, target string, body interface{}) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	return httptest.NewRequest(method, target, &buf)
}

// ---------- Register ----------

func TestRegisterAgent_Success(t *testing.T) {
	h, _, orgStore, userStore, secret := setupAgentTest(t)
	adminID := createTestUser(t, userStore)
	o := createTestOrg(t, orgStore, adminID)

	req := authenticatedRequest(t, http.MethodPost, "/api/agents/register",
		map[string]interface{}{
			"orgId":    o.ID,
			"hostname": "db-server-1.example.com",
		}, adminID, secret)
	w := httptest.NewRecorder()
	h.Register(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp registerAgentResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "db-server-1.example.com", resp.Agent.Hostname)
	assert.Equal(t, o.ID, resp.Agent.OrgID)
	assert.Equal(t, "offline", resp.Agent.Status)
	assert.False(t, resp.Agent.Approved)
	assert.NotEmpty(t, resp.RegistrationToken)
}

func TestRegisterAgent_MissingHostname(t *testing.T) {
	h, _, orgStore, userStore, secret := setupAgentTest(t)
	adminID := createTestUser(t, userStore)
	o := createTestOrg(t, orgStore, adminID)

	req := authenticatedRequest(t, http.MethodPost, "/api/agents/register",
		map[string]interface{}{"orgId": o.ID}, adminID, secret)
	w := httptest.NewRecorder()
	h.Register(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegisterAgent_MissingOrgID(t *testing.T) {
	h, _, orgStore, userStore, secret := setupAgentTest(t)
	adminID := createTestUser(t, userStore)
	createTestOrg(t, orgStore, adminID)

	req := authenticatedRequest(t, http.MethodPost, "/api/agents/register",
		map[string]interface{}{"hostname": "db-1"}, adminID, secret)
	w := httptest.NewRecorder()
	h.Register(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegisterAgent_NonMember(t *testing.T) {
	h, _, orgStore, userStore, secret := setupAgentTest(t)
	adminID := createTestUser(t, userStore)
	nonMemberID := createTestUser(t, userStore)
	o := createTestOrg(t, orgStore, adminID)

	req := authenticatedRequest(t, http.MethodPost, "/api/agents/register",
		map[string]interface{}{
			"orgId": o.ID, "hostname": "db-1",
		}, nonMemberID, secret)
	w := httptest.NewRecorder()
	h.Register(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ---------- List ----------

func TestListAgents_Success(t *testing.T) {
	h, agentStore, orgStore, userStore, secret := setupAgentTest(t)
	adminID := createTestUser(t, userStore)
	o := createTestOrg(t, orgStore, adminID)

	// Register two agents directly.
	a1 := &AgentRecord{OrgID: o.ID, Hostname: "db-1", Status: "online"}
	a2 := &AgentRecord{OrgID: o.ID, Hostname: "db-2", Status: "offline"}
	require.NoError(t, agentStore.Create(a1))
	require.NoError(t, agentStore.Create(a2))

	req := authenticatedRequest(t, http.MethodGet, "/api/agents?orgId="+o.ID,
		nil, adminID, secret)
	w := httptest.NewRecorder()
	h.List(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp listAgentsResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Len(t, resp.Agents, 2)
}

func TestListAgents_NonMember(t *testing.T) {
	h, agentStore, orgStore, userStore, secret := setupAgentTest(t)
	adminID := createTestUser(t, userStore)
	otherID := createTestUser(t, userStore)
	o := createTestOrg(t, orgStore, adminID)

	a1 := &AgentRecord{OrgID: o.ID, Hostname: "db-1"}
	require.NoError(t, agentStore.Create(a1))

	req := authenticatedRequest(t, http.MethodGet, "/api/agents?orgId="+o.ID,
		nil, otherID, secret)
	w := httptest.NewRecorder()
	h.List(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// ---------- Approve ----------

func TestApproveAgent_Success(t *testing.T) {
	h, agentStore, orgStore, userStore, secret := setupAgentTest(t)
	adminID := createTestUser(t, userStore)
	o := createTestOrg(t, orgStore, adminID)

	agent := &AgentRecord{OrgID: o.ID, Hostname: "db-1", Approved: false}
	require.NoError(t, agentStore.Create(agent))

	req := authenticatedRequest(t, http.MethodPost,
		"/api/agents/"+agent.ID+"/approve", nil, adminID, secret)
	w := httptest.NewRecorder()
	h.Approve(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp approveAgentResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.True(t, resp.Agent.Approved)
	assert.NotEmpty(t, resp.Agent.CertSerial)

	// Verify the store is updated.
	updated, err := agentStore.Get(agent.ID)
	require.NoError(t, err)
	assert.True(t, updated.Approved)
}

func TestApproveAgent_NonAdmin(t *testing.T) {
	h, agentStore, orgStore, userStore, secret := setupAgentTest(t)
	adminID := createTestUser(t, userStore)
	memberID := createTestUser(t, userStore)
	o := createTestOrg(t, orgStore, adminID)

	// Add memberID as a regular member.
	require.NoError(t, orgStore.AddMember(o.ID, memberID, "member"))

	agent := &AgentRecord{OrgID: o.ID, Hostname: "db-1", Approved: false}
	require.NoError(t, agentStore.Create(agent))

	req := authenticatedRequest(t, http.MethodPost,
		"/api/agents/"+agent.ID+"/approve", nil, memberID, secret)
	w := httptest.NewRecorder()
	h.Approve(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestApproveAgent_NotFound(t *testing.T) {
	h, _, orgStore, userStore, secret := setupAgentTest(t)
	adminID := createTestUser(t, userStore)
	createTestOrg(t, orgStore, adminID)

	req := authenticatedRequest(t, http.MethodPost,
		"/api/agents/nonexistent/approve", nil, adminID, secret)
	w := httptest.NewRecorder()
	h.Approve(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ---------- Get ----------

func TestGetAgent_Success(t *testing.T) {
	h, agentStore, orgStore, userStore, secret := setupAgentTest(t)
	adminID := createTestUser(t, userStore)
	o := createTestOrg(t, orgStore, adminID)

	agent := &AgentRecord{
		OrgID: o.ID, Hostname: "db-1", Status: "online",
		MySQLVersion: "8.0.35",
	}
	require.NoError(t, agentStore.Create(agent))

	req := authenticatedRequest(t, http.MethodGet,
		"/api/agents/"+agent.ID, nil, adminID, secret)
	w := httptest.NewRecorder()
	h.Get(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp AgentRecord
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, agent.ID, resp.ID)
	assert.Equal(t, "db-1", resp.Hostname)
	assert.Equal(t, "8.0.35", resp.MySQLVersion)
}

func TestGetAgent_NonMember(t *testing.T) {
	h, agentStore, orgStore, userStore, secret := setupAgentTest(t)
	adminID := createTestUser(t, userStore)
	otherID := createTestUser(t, userStore)
	o := createTestOrg(t, orgStore, adminID)

	agent := &AgentRecord{OrgID: o.ID, Hostname: "db-1"}
	require.NoError(t, agentStore.Create(agent))

	req := authenticatedRequest(t, http.MethodGet,
		"/api/agents/"+agent.ID, nil, otherID, secret)
	w := httptest.NewRecorder()
	h.Get(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetAgent_NotFound(t *testing.T) {
	h, _, orgStore, userStore, secret := setupAgentTest(t)
	adminID := createTestUser(t, userStore)
	createTestOrg(t, orgStore, adminID)

	req := authenticatedRequest(t, http.MethodGet,
		"/api/agents/nonexistent", nil, adminID, secret)
	w := httptest.NewRecorder()
	h.Get(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
