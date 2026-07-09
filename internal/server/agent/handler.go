package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/a-shan/mysql-pitr/internal/server/auth"
	"github.com/a-shan/mysql-pitr/internal/server/org"
)

// Handler serves agent HTTP endpoints.
type Handler struct {
	agentStore AgentStore
	orgStore   org.OrgStore
	jwtSecret  []byte
}

// NewHandler creates an agent Handler.
func NewHandler(agentStore AgentStore, orgStore org.OrgStore, jwtSecret []byte) *Handler {
	return &Handler{
		agentStore: agentStore,
		orgStore:   orgStore,
		jwtSecret:  jwtSecret,
	}
}

// ---------- request / response types ----------

type registerAgentRequest struct {
	Hostname     string `json:"hostname"`
	MySQLVersion string `json:"mySQLVersion,omitempty"`
}

type registerAgentResponse struct {
	Agent             *AgentRecord `json:"agent"`
	RegistrationToken string       `json:"registrationToken"`
}

type listAgentsResponse struct {
	Agents []*AgentRecord `json:"agents"`
}

type approveAgentResponse struct {
	Agent *AgentRecord `json:"agent"`
}

// ---------- helpers ----------

func userIDFromRequest(r *http.Request) string {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		return ""
	}
	return claims.UserID
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// ---------- handlers ----------

// Register creates a new agent registration. The agent is created in a
// pending state with a registration token that must be presented during the
// WebSocket handshake.
//
// POST /api/agents/register
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Determine which org this agent belongs to. The request can include an
	// explicit orgId in the body, or we use the first org the user belongs to.
	var req struct {
		OrgID        string `json:"orgId,omitempty"`
		Hostname     string `json:"hostname"`
		MySQLVersion string `json:"mySQLVersion,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Hostname == "" {
		writeError(w, http.StatusBadRequest, "hostname is required")
		return
	}

	// We need a way to find a user's orgs. For simplicity, we look up the org
	// membership via ListMembers for every org. This is O(n) but fine for
	// in-memory. In a real DB, you'd query a user_orgs join table.
	// For now, we iterate through all orgs stored and check membership.

	// Actually, the OrgStore doesn't have a "list by user" method. Let me
	// accept an explicit orgId in the body instead. This is a pragmatic design
	// choice for the MVP.

	orgID := req.OrgID
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "orgId is required")
		return
	}

	// Verify the user is a member of the specified org.
	members, err := h.orgStore.ListMembers(orgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "organisation not found")
		return
	}
	isMember := false
	for _, m := range members {
		if m.UserID == userID {
			isMember = true
			break
		}
	}
	if !isMember {
		writeError(w, http.StatusForbidden, "not a member of this organisation")
		return
	}

	token := generateRegToken()
	agent := &AgentRecord{
		OrgID:             orgID,
		Hostname:          req.Hostname,
		MySQLVersion:      req.MySQLVersion,
		Status:            "offline",
		RegistrationToken: token,
		Approved:          false,
	}

	if err := h.agentStore.Create(agent); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, registerAgentResponse{
		Agent:             agent,
		RegistrationToken: token,
	})
}

// List returns all agents belonging to the caller's organisations.
//
// GET /api/agents
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Determine which org to list. Default: the query param ?orgId=, or
	// iterate over all orgs the user belongs to.
	orgID := r.URL.Query().Get("orgId")
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "orgId query parameter is required")
		return
	}

	// Verify membership.
	members, err := h.orgStore.ListMembers(orgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "organisation not found")
		return
	}
	isMember := false
	for _, m := range members {
		if m.UserID == userID {
			isMember = true
			break
		}
	}
	if !isMember {
		writeError(w, http.StatusForbidden, "not a member of this organisation")
		return
	}

	agents, err := h.agentStore.ListByOrg(orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, listAgentsResponse{Agents: agents})
}

// Approve marks an agent as approved. Only organisation admins may approve
// agents.
//
// POST /api/agents/{id}/approve
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	agentID := chi.URLParam(r, "id")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "missing agent id")
		return
	}

	agent, err := h.agentStore.Get(agentID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Verify the requester is an admin of the agent's org.
	members, err := h.orgStore.ListMembers(agent.OrgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "organisation not found")
		return
	}
	isAdmin := false
	for _, m := range members {
		if m.UserID == userID && m.Role == "admin" {
			isAdmin = true
			break
		}
	}
	if !isAdmin {
		writeError(w, http.StatusForbidden,
			"only organisation admins can approve agents")
		return
	}

	agent.Approved = true
	agent.CertSerial = generateCertSerial() // placeholder for CA signing

	if err := h.agentStore.Update(agent); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, approveAgentResponse{Agent: agent})
}

// Get returns details for a single agent.
//
// GET /api/agents/{id}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	agentID := chi.URLParam(r, "id")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "missing agent id")
		return
	}

	agent, err := h.agentStore.Get(agentID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Verify the requester is a member of the agent's org.
	members, err := h.orgStore.ListMembers(agent.OrgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "organisation not found")
		return
	}
	isMember := false
	for _, m := range members {
		if m.UserID == userID {
			isMember = true
			break
		}
	}
	if !isMember {
		writeError(w, http.StatusForbidden,
			"not a member of the agent's organisation")
		return
	}

	writeJSON(w, http.StatusOK, agent)
}

// generateRegToken creates a random hex token for agent registration.
func generateRegToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// generateCertSerial creates a placeholder certificate serial number.
// In production this would be the serial of the signed x509 certificate.
func generateCertSerial() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "serial_" + hex.EncodeToString(b)
}
