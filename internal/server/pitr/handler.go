package pitr

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/a-shan/mysql-pitr/internal/server/agent"
	"github.com/a-shan/mysql-pitr/internal/server/audit"
	"github.com/a-shan/mysql-pitr/internal/server/auth"
	"github.com/a-shan/mysql-pitr/internal/server/org"
)

// Handler serves PITR workflow HTTP endpoints.
type Handler struct {
	opStore    OperationStore
	agentStore agent.AgentStore
	orgStore   org.OrgStore
	auditStore audit.AuditStore
	jwtSecret  []byte
}

// NewHandler creates a PITR Handler.
func NewHandler(
	opStore OperationStore,
	agentStore agent.AgentStore,
	orgStore org.OrgStore,
	auditStore audit.AuditStore,
	jwtSecret []byte,
) *Handler {
	return &Handler{
		opStore:    opStore,
		agentStore: agentStore,
		orgStore:   orgStore,
		auditStore: auditStore,
		jwtSecret:  jwtSecret,
	}
}

// ---------- request / response types ----------

type startRequest struct {
	AgentID      string `json:"agent_id"`
	TargetTable  string `json:"target_table"`
	RecoveryTime string `json:"recovery_time"`
	Mode         string `json:"mode"` // "preview" or "execute"
}

type startResponse struct {
	OperationID string         `json:"operationId"`
	Status      OperationState `json:"status"`
}

type statusResponse struct {
	ID           string          `json:"id"`
	OrgID        string          `json:"orgId"`
	AgentID      string          `json:"agentId"`
	TargetTable  string          `json:"targetTable"`
	RecoveryTime time.Time       `json:"recoveryTime"`
	Mode         string          `json:"mode"`
	State        OperationState  `json:"state"`
	PreflightRes *PreflightResult `json:"preflightResult,omitempty"`
	ParseRes     *ParseSummary   `json:"parseResult,omitempty"`
	ExecRes      *ExecSummary    `json:"execResult,omitempty"`
	Progress     *ProgressInfo   `json:"progress,omitempty"`
	Error        string          `json:"error,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

type cancelResponse struct {
	OperationID string         `json:"operationId"`
	Status      OperationState `json:"status"`
}

type previewResponse struct {
	OperationID  string        `json:"operationId"`
	RowsAffected int64         `json:"rowsAffected"`
	SQLSample    string        `json:"sqlSample"`
	ParsedAt     time.Time     `json:"parsedAt"`
	State        OperationState `json:"state"`
}

type progressResponse struct {
	OperationID        string         `json:"operationId"`
	State              OperationState `json:"state"`
	BatchesComplete    int            `json:"batchesComplete"`
	BatchesTotal       int            `json:"batchesTotal"`
	RowsRestored       int64          `json:"rowsRestored"`
	EstimatedRemaining string         `json:"estimatedRemaining"`
}

// ---------- helpers ----------

func userIDFromRequest(r *http.Request) string {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		return ""
	}
	return claims.UserID
}

func emailFromRequest(r *http.Request) string {
	claims := auth.ClaimsFromContext(r.Context())
	if claims == nil {
		return ""
	}
	return claims.Email
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

// Start initiates a new PITR recovery operation. The operation is created in
// the preflight state and an asynchronous goroutine advances it through the
// state machine. The response returns immediately with the operation ID.
//
// POST /api/pitr/start
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	operator := emailFromRequest(r)
	if userID == "" || operator == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.AgentID == "" || req.TargetTable == "" || req.RecoveryTime == "" || req.Mode == "" {
		writeError(w, http.StatusBadRequest,
			"agent_id, target_table, recovery_time, and mode are required")
		return
	}
	if req.Mode != "preview" && req.Mode != "execute" {
		writeError(w, http.StatusBadRequest, "mode must be \"preview\" or \"execute\"")
		return
	}

	recoveryTime, err := time.Parse(time.RFC3339, req.RecoveryTime)
	if err != nil {
		writeError(w, http.StatusBadRequest,
			"invalid recovery_time: expected ISO8601/RFC3339 format")
		return
	}

	// Fetch the agent to determine the organisation.
	agt, err := h.agentStore.Get(req.AgentID)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	// Verify the requesting user is a member of the agent's org.
	members, err := h.orgStore.ListMembers(agt.OrgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "organisation not found")
		return
	}
	if !orgMemberContains(members, userID) {
		writeError(w, http.StatusForbidden,
			"not a member of the agent's organisation")
		return
	}

	op := &Operation{
		OrgID:        agt.OrgID,
		AgentID:      req.AgentID,
		TargetTable:  req.TargetTable,
		RecoveryTime: recoveryTime,
		Mode:         req.Mode,
		State:        StatePreflight,
	}

	if err := h.opStore.Create(op); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Launch the asynchronous workflow simulation. In production this would
	// be dispatched to a worker queue.
	go h.simulateOperation(op, operator)

	writeJSON(w, http.StatusCreated, startResponse{
		OperationID: op.ID,
		Status:      op.State,
	})
}

// Status returns the current state and result data for an operation.
//
// GET /api/pitr/{id}/status
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	opID := chi.URLParam(r, "id")
	if opID == "" {
		writeError(w, http.StatusBadRequest, "missing operation id")
		return
	}

	op, err := h.opStore.Get(opID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Verify org membership.
	members, err := h.orgStore.ListMembers(op.OrgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "organisation not found")
		return
	}
	if !orgMemberContains(members, userID) {
		writeError(w, http.StatusForbidden,
			"not a member of this organisation")
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{
		ID:           op.ID,
		OrgID:        op.OrgID,
		AgentID:      op.AgentID,
		TargetTable:  op.TargetTable,
		RecoveryTime: op.RecoveryTime,
		Mode:         op.Mode,
		State:        op.State,
		PreflightRes: op.PreflightRes,
		ParseRes:     op.ParseRes,
		ExecRes:      op.ExecRes,
		Progress:     op.Progress,
		Error:        op.Error,
		CreatedAt:    op.CreatedAt,
		UpdatedAt:    op.UpdatedAt,
	})
}

// Cancel attempts to cancel a running operation. Cancellation is only valid
// from the preflight, parsing, or previewed states.
//
// POST /api/pitr/{id}/cancel
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	opID := chi.URLParam(r, "id")
	if opID == "" {
		writeError(w, http.StatusBadRequest, "missing operation id")
		return
	}

	op, err := h.opStore.Get(opID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Verify org membership.
	members, err := h.orgStore.ListMembers(op.OrgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "organisation not found")
		return
	}
	if !orgMemberContains(members, userID) {
		writeError(w, http.StatusForbidden,
			"not a member of this organisation")
		return
	}

	if !TransitionValid(op.State, StateCancelled) {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("cannot cancel operation in state %q", op.State))
		return
	}

	op.State = StateCancelled
	if err := h.opStore.Update(op); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Record audit entry.
	_ = h.auditStore.Append(&audit.AuditEntry{
		OperationID: op.ID,
		Operator:    emailFromRequest(r),
		Timestamp:   time.Now(),
		OrgID:       op.OrgID,
		AgentID:     op.AgentID,
		TargetTable: op.TargetTable,
		Status:      string(StateCancelled),
	})

	writeJSON(w, http.StatusOK, cancelResponse{
		OperationID: op.ID,
		Status:      op.State,
	})
}

// Preview returns the parsed results for an operation that has reached the
// previewed state.
//
// GET /api/pitr/{id}/preview
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	opID := chi.URLParam(r, "id")
	if opID == "" {
		writeError(w, http.StatusBadRequest, "missing operation id")
		return
	}

	op, err := h.opStore.Get(opID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Verify org membership.
	members, err := h.orgStore.ListMembers(op.OrgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "organisation not found")
		return
	}
	if !orgMemberContains(members, userID) {
		writeError(w, http.StatusForbidden,
			"not a member of this organisation")
		return
	}

	if op.ParseRes == nil {
		writeError(w, http.StatusPreconditionFailed,
			"preview not available until parsing phase is complete")
		return
	}

	writeJSON(w, http.StatusOK, previewResponse{
		OperationID:  op.ID,
		RowsAffected: op.ParseRes.RowsAffected,
		SQLSample:    op.ParseRes.SQLSample,
		ParsedAt:     op.ParseRes.ParsedAt,
		State:        op.State,
	})
}

// Progress returns the current execution progress for an operation that is in
// the executing state. Frontends poll this endpoint every 2 seconds.
//
// GET /api/pitr/{id}/progress
func (h *Handler) Progress(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	opID := chi.URLParam(r, "id")
	if opID == "" {
		writeError(w, http.StatusBadRequest, "missing operation id")
		return
	}

	op, err := h.opStore.Get(opID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Verify org membership.
	members, err := h.orgStore.ListMembers(op.OrgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "organisation not found")
		return
	}
	if !orgMemberContains(members, userID) {
		writeError(w, http.StatusForbidden,
			"not a member of this organisation")
		return
	}

	if op.Progress == nil {
		// Return a zero-value progress response for non-executing states.
		writeJSON(w, http.StatusOK, progressResponse{
			OperationID: op.ID,
			State:       op.State,
		})
		return
	}

	writeJSON(w, http.StatusOK, progressResponse{
		OperationID:        op.ID,
		State:              op.State,
		BatchesComplete:    op.Progress.BatchesComplete,
		BatchesTotal:       op.Progress.BatchesTotal,
		RowsRestored:       op.Progress.RowsRestored,
		EstimatedRemaining: op.Progress.EstimatedRemaining,
	})
}

// ---------- background simulation ----------

// simulateOperation advances the operation through the state machine in a
// background goroutine. Each phase includes a small artificial delay to
// simulate real processing time. In production this would be replaced by
// actual preflight checks, binlog parsing, and SQL execution.
func (h *Handler) simulateOperation(op *Operation, operator string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("pitr: simulateOperation panicked for op %s: %v", op.ID, r)
		}
	}()

	// Phase 1: preflight -> confirmed
	if !h.tryTransition(op, StateConfirmed) {
		return
	}
	time.Sleep(200 * time.Millisecond)
	op.PreflightRes = &PreflightResult{
		CheckedAt:     time.Now(),
		BinlogFiles:   []string{"mysql-bin.000042", "mysql-bin.000043"},
		EarliestTime:  time.Now().Add(-24 * time.Hour),
		EstimatedSize: 256 * 1024 * 1024,
	}
	_ = h.opStore.Update(op)

	// Phase 2: confirmed -> parsing
	if !h.tryTransition(op, StateParsing) {
		return
	}
	time.Sleep(300 * time.Millisecond)
	_ = h.opStore.Update(op)

	// Phase 3: parsing -> previewed
	if !h.tryTransition(op, StatePreviewed) {
		return
	}
	time.Sleep(500 * time.Millisecond)
	op.ParseRes = &ParseSummary{
		ParsedAt:     time.Now(),
		RowsAffected: 1250,
		SQLSample:    "DELETE FROM orders WHERE id IN (1001, 1002, ..., 1250);",
	}
	_ = h.opStore.Update(op)

	if op.Mode == "preview" {
		h.recordAudit(op, operator, "previewed", "")
		return
	}

	// Phase 4: previewed -> executing
	if !h.tryTransition(op, StateExecuting) {
		return
	}
	time.Sleep(200 * time.Millisecond)
	totalBatches := 10
	op.Progress = &ProgressInfo{
		BatchesTotal:      totalBatches,
		BatchesComplete:   0,
		RowsRestored:      0,
		EstimatedRemaining: "30s",
	}
	_ = h.opStore.Update(op)

	// Simulate batched execution with incremental progress.
	for i := 1; i <= totalBatches; i++ {
		time.Sleep(300 * time.Millisecond)
		op.Progress.BatchesComplete = i
		op.Progress.RowsRestored = int64(i) * 125
		remaining := totalBatches - i
		op.Progress.EstimatedRemaining = fmt.Sprintf("%ds", remaining*3)
		_ = h.opStore.Update(op)
	}

	// Final: executing -> completed
	op.State = StateCompleted
	op.ExecRes = &ExecSummary{
		ExecutedAt:   time.Now(),
		RowsRestored: op.Progress.RowsRestored,
		Duration:     fmt.Sprintf("%.1fs", float64(totalBatches)*0.3),
	}
	op.Progress.BatchesComplete = totalBatches
	op.Progress.EstimatedRemaining = "0s"
	_ = h.opStore.Update(op)

	h.recordAudit(op, operator, "completed", "")
}

// tryTransition attempts a state transition. Returns false if the transition
// is invalid (e.g. cancelled by another goroutine).
func (h *Handler) tryTransition(op *Operation, to OperationState) bool {
	MustTransition(op.State, to)
	op.State = to
	if err := h.opStore.Update(op); err != nil {
		log.Printf("pitr: failed to transition op %s to %s: %v", op.ID, to, err)
		return false
	}
	return true
}

// recordAudit appends an audit log entry for the operation.
func (h *Handler) recordAudit(op *Operation, operator, status, errDetails string) {
	rows := int64(0)
	if op.ExecRes != nil {
		rows = op.ExecRes.RowsRestored
	} else if op.ParseRes != nil {
		rows = op.ParseRes.RowsAffected
	}

	_ = h.auditStore.Append(&audit.AuditEntry{
		OperationID:  op.ID,
		Operator:     operator,
		Timestamp:    time.Now(),
		OrgID:        op.OrgID,
		AgentID:      op.AgentID,
		TargetTable:  op.TargetTable,
		RecoveryTime: op.RecoveryTime,
		RowsAffected: rows,
		Status:       status,
		ErrorDetails: errDetails,
	})
}

func orgMemberContains(members []org.Member, userID string) bool {
	for _, m := range members {
		if m.UserID == userID {
			return true
		}
	}
	return false
}
