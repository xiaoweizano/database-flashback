package org

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a-shan/mysql-pitr/internal/server/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// test helpers

func setupOrgTest(t *testing.T) (*Handler, *InMemoryOrgStore, *auth.InMemoryUserStore, []byte) {
	t.Helper()
	orgStore := NewInMemoryOrgStore()
	userStore := auth.NewInMemoryUserStore()
	secret := []byte("org-test-secret")
	handler := NewHandler(orgStore, userStore, secret)
	return handler, orgStore, userStore, secret
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

func authenticatedRequest(t *testing.T, method, target string, body interface{}, userID string, _ []byte) *http.Request {
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

// ---------- Create ----------

func TestCreateOrg_Success(t *testing.T) {
	h, _, userStore, secret := setupOrgTest(t)
	userID := createTestUser(t, userStore)

	req := authenticatedRequest(t, http.MethodPost, "/api/orgs",
		createOrgRequest{Name: "Acme Corp"}, userID, secret)
	w := httptest.NewRecorder()
	h.Create(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp createOrgResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "Acme Corp", resp.Organization.Name)
	assert.NotEmpty(t, resp.Organization.ID)
}

func TestCreateOrg_MissingName(t *testing.T) {
	h, _, userStore, secret := setupOrgTest(t)
	userID := createTestUser(t, userStore)

	req := authenticatedRequest(t, http.MethodPost, "/api/orgs",
		createOrgRequest{}, userID, secret)
	w := httptest.NewRecorder()
	h.Create(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateOrg_NoAuth(t *testing.T) {
	h, _, _, _ := setupOrgTest(t)

	req := httptest.NewRequest(http.MethodPost, "/api/orgs",
		bytes.NewReader([]byte(`{"name":"Test"}`)))
	w := httptest.NewRecorder()
	h.Create(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ---------- Invite ----------

func TestInvite_Success(t *testing.T) {
	h, orgStore, userStore, secret := setupOrgTest(t)
	userID := createTestUser(t, userStore)

	// Create org.
	org := &Organization{Name: "Test Org"}
	err := orgStore.Create(org)
	require.NoError(t, err)
	err = orgStore.AddMember(org.ID, userID, "admin")
	require.NoError(t, err)

	req := authenticatedRequest(t, http.MethodPost, "/api/orgs/"+org.ID+"/invite",
		nil, userID, secret)
	w := httptest.NewRecorder()
	h.Invite(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp inviteResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Code)
}

func TestInvite_NonAdmin(t *testing.T) {
	h, orgStore, userStore, secret := setupOrgTest(t)
	userID := createTestUser(t, userStore)

	org := &Organization{Name: "Test Org"}
	err := orgStore.Create(org)
	require.NoError(t, err)
	err = orgStore.AddMember(org.ID, userID, "member")
	require.NoError(t, err)

	req := authenticatedRequest(t, http.MethodPost, "/api/orgs/"+org.ID+"/invite",
		nil, userID, secret)
	w := httptest.NewRecorder()
	h.Invite(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestInvite_NonMember(t *testing.T) {
	h, orgStore, userStore, secret := setupOrgTest(t)
	userID := createTestUser(t, userStore)
	otherUser := createTestUser(t, userStore)

	org := &Organization{Name: "Test Org"}
	err := orgStore.Create(org)
	require.NoError(t, err)
	err = orgStore.AddMember(org.ID, otherUser, "admin")
	require.NoError(t, err)

	req := authenticatedRequest(t, http.MethodPost, "/api/orgs/"+org.ID+"/invite",
		nil, userID, secret)
	w := httptest.NewRecorder()
	h.Invite(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestInvite_NonexistentOrg(t *testing.T) {
	h, _, userStore, secret := setupOrgTest(t)
	userID := createTestUser(t, userStore)

	req := authenticatedRequest(t, http.MethodPost, "/api/orgs/nonexistent/invite",
		nil, userID, secret)
	w := httptest.NewRecorder()
	h.Invite(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ---------- AcceptInvite ----------

func TestAcceptInvite_Success(t *testing.T) {
	h, orgStore, userStore, secret := setupOrgTest(t)
	adminID := createTestUser(t, userStore)
	memberID := createTestUser(t, userStore)

	org := &Organization{Name: "Test Org"}
	err := orgStore.Create(org)
	require.NoError(t, err)
	err = orgStore.AddMember(org.ID, adminID, "admin")
	require.NoError(t, err)

	invite, err := orgStore.CreateInvite(org.ID, adminID)
	require.NoError(t, err)

	req := authenticatedRequest(t, http.MethodPost, "/api/orgs/"+org.ID+"/accept",
		acceptInviteRequest{Code: invite.Code}, memberID, secret)
	w := httptest.NewRecorder()
	h.AcceptInvite(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp acceptInviteResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "Test Org", resp.Organization.Name)

	// Verify membership.
	members, err := orgStore.ListMembers(org.ID)
	require.NoError(t, err)
	assert.Len(t, members, 2)
}

func TestAcceptInvite_WrongCode(t *testing.T) {
	h, orgStore, userStore, secret := setupOrgTest(t)
	adminID := createTestUser(t, userStore)
	memberID := createTestUser(t, userStore)

	org := &Organization{Name: "Test Org"}
	err := orgStore.Create(org)
	require.NoError(t, err)
	err = orgStore.AddMember(org.ID, adminID, "admin")
	require.NoError(t, err)

	req := authenticatedRequest(t, http.MethodPost, "/api/orgs/"+org.ID+"/accept",
		acceptInviteRequest{Code: "bad-code"}, memberID, secret)
	w := httptest.NewRecorder()
	h.AcceptInvite(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAcceptInvite_OrgMismatch(t *testing.T) {
	h, orgStore, userStore, secret := setupOrgTest(t)
	adminID := createTestUser(t, userStore)
	memberID := createTestUser(t, userStore)

	org1 := &Organization{Name: "Org1"}
	org2 := &Organization{Name: "Org2"}
	err := orgStore.Create(org1)
	require.NoError(t, err)
	err = orgStore.Create(org2)
	require.NoError(t, err)
	err = orgStore.AddMember(org1.ID, adminID, "admin")
	require.NoError(t, err)

	invite, err := orgStore.CreateInvite(org1.ID, adminID)
	require.NoError(t, err)

	// Try to accept invite for org1 on org2's endpoint.
	req := authenticatedRequest(t, http.MethodPost, "/api/orgs/"+org2.ID+"/accept",
		acceptInviteRequest{Code: invite.Code}, memberID, secret)
	w := httptest.NewRecorder()
	h.AcceptInvite(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ---------- ListMembers ----------

func TestListMembers_Success(t *testing.T) {
	h, orgStore, userStore, secret := setupOrgTest(t)
	userID := createTestUser(t, userStore)
	otherID := createTestUser(t, userStore)

	org := &Organization{Name: "Test Org"}
	err := orgStore.Create(org)
	require.NoError(t, err)
	err = orgStore.AddMember(org.ID, userID, "admin")
	require.NoError(t, err)
	err = orgStore.AddMember(org.ID, otherID, "member")
	require.NoError(t, err)

	req := authenticatedRequest(t, http.MethodGet, "/api/orgs/"+org.ID+"/members",
		nil, userID, secret)
	w := httptest.NewRecorder()
	h.ListMembers(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp memberResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Len(t, resp.Members, 2)
}

func TestListMembers_NonMember(t *testing.T) {
	h, orgStore, userStore, secret := setupOrgTest(t)
	userID := createTestUser(t, userStore)
	otherID := createTestUser(t, userStore)

	org := &Organization{Name: "Test Org"}
	err := orgStore.Create(org)
	require.NoError(t, err)
	err = orgStore.AddMember(org.ID, otherID, "admin")
	require.NoError(t, err)

	req := authenticatedRequest(t, http.MethodGet, "/api/orgs/"+org.ID+"/members",
		nil, userID, secret)
	w := httptest.NewRecorder()
	h.ListMembers(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestListMembers_NonexistentOrg(t *testing.T) {
	h, _, userStore, secret := setupOrgTest(t)
	userID := createTestUser(t, userStore)

	req := authenticatedRequest(t, http.MethodGet, "/api/orgs/nonexistent/members",
		nil, userID, secret)
	w := httptest.NewRecorder()
	h.ListMembers(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
