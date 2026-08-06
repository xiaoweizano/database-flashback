package pitr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/a-shan/mysql-pitr/internal/connector"
	"github.com/a-shan/mysql-pitr/internal/server/agent"
	"github.com/a-shan/mysql-pitr/internal/server/audit"
	"github.com/a-shan/mysql-pitr/internal/server/auth"
	"github.com/a-shan/mysql-pitr/internal/server/org"
	"github.com/a-shan/mysql-pitr/internal/ws"
	"github.com/a-shan/mysql-pitr/internal/ws/hub"
)

// AgentCommander is the subset of the agent hub the PITR flow needs. The
// production implementation is *hub.Hub; tests provide a fake.
type AgentCommander interface {
	IsConnected(agentID string) bool
	SendToAgent(ctx context.Context, agentID string, cmd ws.Command) (*ws.Response, error)
	SetProgressHandler(fn hub.ProgressHandler)
}

// Handler serves PITR workflow HTTP endpoints. Operations are executed by
// sending commands to a connected agent over the commander; the server never
// touches binlog files or MySQL credentials itself.
type Handler struct {
	opStore    OperationStore
	agentStore agent.AgentStore
	orgStore   org.OrgStore
	auditStore audit.AuditStore
	jwtSecret  []byte

	// commander is the agent command channel used to route operation phases
	// to the selected agent. May be nil in tests / minimal setups;
	// operations are rejected when nil.
	commander AgentCommander
}

// NewHandler creates a PITR Handler. commander may be nil for setups without
// an agent channel.
func NewHandler(
	opStore OperationStore,
	agentStore agent.AgentStore,
	orgStore org.OrgStore,
	auditStore audit.AuditStore,
	jwtSecret []byte,
	commander AgentCommander,
) *Handler {
	h := &Handler{
		opStore:    opStore,
		agentStore: agentStore,
		orgStore:   orgStore,
		auditStore: auditStore,
		jwtSecret:  jwtSecret,
		commander:  commander,
	}
	if commander != nil {
		// Agent-pushed progress notifications update the operation's
		// progress record, which the REST progress endpoint serves.
		commander.SetProgressHandler(func(agentID string, cmd ws.Command) {
			opID, _ := cmd.Params["operationId"].(string)
			if opID == "" {
				return
			}
			op, err := h.opStore.Get(opID)
			if err != nil {
				return
			}
			if op.State != StateExecuting {
				return
			}
			p := op.Progress
			if p == nil {
				p = &ProgressInfo{}
			}
			if v, ok := cmd.Params["batchesComplete"].(float64); ok {
				p.BatchesComplete = int(v)
			}
			if v, ok := cmd.Params["batchesTotal"].(float64); ok {
				p.BatchesTotal = int(v)
			}
			if v, ok := cmd.Params["rowsRestored"].(float64); ok {
				p.RowsRestored = int64(v)
			}
			if s, ok := cmd.Params["estimatedRemaining"].(string); ok {
				p.EstimatedRemaining = s
			}
			op.Progress = p
			_ = h.opStore.Update(op)
		})
	}
	return h
}

// ---------- request / response types ----------

type startRequest struct {
	AgentID      string   `json:"agent_id"`
	TargetTable  string   `json:"target_table"`
	RecoveryTime string   `json:"recovery_time"`
	Mode         string   `json:"mode"` // "preview" or "execute"
	BinlogFiles  []string `json:"binlog_files,omitempty"`
	StartTime    string   `json:"start_time,omitempty"`
	StartPos     *uint32  `json:"start_pos,omitempty"`
	StopPos      *uint32  `json:"stop_pos,omitempty"`
}

type startResponse struct {
	OperationID string         `json:"operationId"`
	Status      OperationState `json:"status"`
}

type statusResponse struct {
	ID             string           `json:"id"`
	OrgID          string           `json:"orgId"`
	AgentID        string           `json:"agentId"`
	AgentConnected bool             `json:"agentConnected"`
	TargetTable    string           `json:"targetTable"`
	RecoveryTime   time.Time        `json:"recoveryTime"`
	Mode           string           `json:"mode"`
	State          OperationState   `json:"state"`
	PreflightRes   *PreflightResult `json:"preflightResult,omitempty"`
	ParseRes       *ParseSummary    `json:"parseResult,omitempty"`
	ExecRes        *ExecSummary     `json:"execResult,omitempty"`
	Progress       *ProgressInfo    `json:"progress,omitempty"`
	Error          string           `json:"error,omitempty"`
	CreatedAt      time.Time        `json:"createdAt"`
	UpdatedAt      time.Time        `json:"updatedAt"`
}

type cancelResponse struct {
	OperationID string         `json:"operationId"`
	Status      OperationState `json:"status"`
}

type previewResponse struct {
	OperationID  string           `json:"operationId"`
	RowsAffected int64            `json:"rowsAffected"`
	SQLSample    string           `json:"sqlSample"`
	ReverseSql   []ReverseSqlEntry `json:"reverseSql,omitempty"`
	ParsedAt     time.Time        `json:"parsedAt"`
	State        OperationState   `json:"state"`
}

type progressResponse struct {
	OperationID        string         `json:"operationId"`
	State              OperationState `json:"state"`
	BatchesComplete    int            `json:"batchesComplete"`
	BatchesTotal       int            `json:"batchesTotal"`
	RowsRestored       int64          `json:"rowsRestored"`
	EstimatedRemaining string         `json:"estimatedRemaining"`
}

// Agent command result payloads (mirror the agent-side handlers).

type agentPreflightResult struct {
	Preflight   *connector.PreflightResult `json:"preflight"`
	BinlogFiles []string                   `json:"binlogFiles"`
	TotalSize   int64                      `json:"totalSize"`
	BinlogDir   string                     `json:"binlogDir"`
}

type agentParseResult struct {
	OperationID string            `json:"operationId"`
	TotalRows   int64             `json:"totalRows"`
	ReverseSql  []string          `json:"reverseSql"`
	Preview     []ReverseSqlEntry `json:"preview"`
	SQLSample   string            `json:"sqlSample"`
}

type agentBatchError struct {
	BatchNum int    `json:"batchNum"`
	SQL      string `json:"sql"`
	Error    string `json:"error"`
}

type agentExecuteResult struct {
	OperationID      string            `json:"operationId"`
	Cancelled        bool              `json:"cancelled"`
	RowsAffected     int64             `json:"rowsAffected"`
	BatchesCompleted int               `json:"batchesCompleted"`
	BatchesTotal     int               `json:"batchesTotal"`
	Errors           []agentBatchError `json:"errors"`
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

// newCmdID generates a unique command identifier for hub correlation.
func newCmdID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

// decodeResult round-trips a command response Result (map[string]interface{})
// into a typed struct.
func decodeResult(resp *ws.Response, out interface{}) error {
	data, err := json.Marshal(resp.Result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("unmarshal result: %w", err)
	}
	return nil
}

// ---------- handlers ----------

// List returns all PITR operations for an organisation, optionally filtered
// by time range.
//
// GET /api/pitr?org_id=X&from=ISO&to=ISO
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	orgID := r.URL.Query().Get("org_id")
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "org_id query parameter is required")
		return
	}

	// Verify org membership.
	members, err := h.orgStore.ListMembers(orgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "organisation not found")
		return
	}
	if !orgMemberContains(members, userID) {
		writeError(w, http.StatusForbidden, "not a member of this organisation")
		return
	}

	ops, err := h.opStore.ListByOrg(orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fromStr := r.URL.Query().Get("from")
	toStr := r.URL.Query().Get("to")

	var filtered []*Operation
	for _, op := range ops {
		if fromStr != "" {
			from, err := time.Parse(time.RFC3339, fromStr)
			if err == nil && op.CreatedAt.Before(from) {
				continue
			}
		}
		if toStr != "" {
			to, err := time.Parse(time.RFC3339, toStr)
			if err == nil && op.CreatedAt.After(to) {
				continue
			}
		}
		op.AgentConnected = h.agentConnected(op.AgentID)
		filtered = append(filtered, op)
	}
	if filtered == nil {
		filtered = []*Operation{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"operations": filtered})
}

// Start initiates a new PITR recovery operation against a connected agent.
// The operation is created in the preflight state and an asynchronous
// goroutine advances it through the state machine by sending commands to the
// agent over the hub.
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

	var startTime *time.Time
	if req.StartTime != "" {
		t, err := time.Parse(time.RFC3339, req.StartTime)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				"invalid start_time: expected ISO8601/RFC3339 format")
			return
		}
		startTime = &t
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

	// The agent must be approved and connected — operations run entirely
	// through the agent's command channel.
	if !agt.Approved {
		writeError(w, http.StatusConflict, "agent is not approved")
		return
	}
	if !h.agentConnected(req.AgentID) {
		writeError(w, http.StatusConflict, "agent is offline — start it with `mysql-pitr-agent serve` first")
		return
	}

	op := &Operation{
		OrgID:        agt.OrgID,
		AgentID:      req.AgentID,
		TargetTable:  req.TargetTable,
		RecoveryTime: recoveryTime,
		Mode:         req.Mode,
		BinlogFiles:  req.BinlogFiles,
		StartTime:    startTime,
		StartPos:     req.StartPos,
		StopPos:      req.StopPos,
		State:        StatePreflight,
	}

	if err := h.opStore.Create(op); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Launch the asynchronous workflow that drives the agent.
	go h.runOperation(op, operator)

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
		ID:             op.ID,
		OrgID:          op.OrgID,
		AgentID:        op.AgentID,
		AgentConnected: h.agentConnected(op.AgentID),
		TargetTable:    op.TargetTable,
		RecoveryTime:   op.RecoveryTime,
		Mode:           op.Mode,
		State:          op.State,
		PreflightRes:   op.PreflightRes,
		ParseRes:       op.ParseRes,
		ExecRes:        op.ExecRes,
		Progress:       op.Progress,
		Error:          op.Error,
		CreatedAt:      op.CreatedAt,
		UpdatedAt:      op.UpdatedAt,
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

	// Best-effort notification to the agent so in-flight work stops between
	// batches. The runOperation goroutine observes the cancelled state when
	// the command response arrives.
	if h.commander != nil {
		cCtx, cCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = h.commander.SendToAgent(cCtx, op.AgentID, ws.Command{
			Cmd:  newCmdID("cancel"),
			Type: ws.CmdPITRCancel,
			Params: map[string]interface{}{
				"operationId": op.ID,
			},
		})
		cCancel()
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
		ReverseSql:   op.ParseRes.ReverseSql,
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

// Execute runs the selected reverse-SQL statements for a previewed operation.
// The request body may carry the user-chosen statements ("sql"); when empty,
// the full generated batch is executed. The actual execution happens
// asynchronously on the agent — this endpoint only triggers it.
//
// POST /api/pitr/{id}/execute
func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromRequest(r)
	operator := emailFromRequest(r)
	if userID == "" || operator == "" {
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
		writeError(w, http.StatusForbidden, "not a member of this organisation")
		return
	}

	if op.State != StatePreviewed {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("cannot execute operation in state %q (run the preview first)", op.State))
		return
	}
	if op.ParseRes == nil {
		writeError(w, http.StatusConflict, "operation has no parsed result")
		return
	}
	if !h.agentConnected(op.AgentID) {
		writeError(w, http.StatusConflict, "agent is offline — start it with `mysql-pitr-agent serve` first")
		return
	}

	var req struct {
		SQL []string `json:"sql,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	sqls := req.SQL
	if len(sqls) == 0 {
		sqls = op.ParseRes.SQLs
	}
	if len(sqls) == 0 {
		writeError(w, http.StatusBadRequest, "no SQL statements to execute")
		return
	}

	go h.executeOperation(op, operator, sqls)

	writeJSON(w, http.StatusAccepted, startResponse{
		OperationID: op.ID,
		Status:      StateExecuting,
	})
}

// ---------- background operation execution ----------

// agentConnected reports whether the operation's agent currently has a live
// hub connection.
func (h *Handler) agentConnected(agentID string) bool {
	return h.commander != nil && h.commander.IsConnected(agentID)
}

// failOperation transitions the operation to the failed state with the given
// error message and persists the change.
func (h *Handler) failOperation(op *Operation, format string, args ...interface{}) {
	op.State = StateFailed
	op.Error = fmt.Sprintf(format, args...)
	_ = h.opStore.Update(op)
	log.Printf("pitr: operation %s failed: %s", op.ID, op.Error)
}

// runOperation advances the operation through the state machine by sending
// commands to the selected agent over the hub: preflight, pitr_parse, and
// pitr_execute. The server never accesses binlog files or MySQL directly.
func (h *Handler) runOperation(op *Operation, operator string) {
	defer func() {
		if r := recover(); r != nil {
			h.failOperation(op, "panic: %v", r)
		}
	}()

	if !h.agentConnected(op.AgentID) {
		h.failOperation(op, "agent %s is not connected (agent_offline)", op.AgentID)
		return
	}

	ctx := context.Background()

	// ================================================================
	// Phase 1: preflight -> confirmed
	// ================================================================
	pCtx, pCancel := context.WithTimeout(ctx, 30*time.Second)
	resp, err := h.commander.SendToAgent(pCtx, op.AgentID, ws.Command{
		Cmd:  newCmdID("preflight"),
		Type: ws.CmdPreflight,
		Params: map[string]interface{}{
			"operationId": op.ID,
		},
	})
	pCancel()
	if err != nil {
		h.failOperation(op, "preflight via agent: %v", err)
		return
	}
	if resp.Status == ws.StatusError {
		h.failOperation(op, "preflight failed: %s", resp.Error)
		return
	}

	var pf agentPreflightResult
	if err := decodeResult(resp, &pf); err != nil {
		h.failOperation(op, "decode preflight result: %v", err)
		return
	}
	if pf.Preflight == nil {
		h.failOperation(op, "preflight result missing preflight data")
		return
	}
	if pf.Preflight.Status == connector.PreflightFail {
		h.failOperation(op, "preflight checks failed (MySQL %s)", pf.Preflight.Version)
		return
	}

	if !h.tryTransition(op, StateConfirmed) {
		return
	}
	op.PreflightRes = &PreflightResult{
		CheckedAt:     time.Now(),
		BinlogFiles:   pf.BinlogFiles,
		EarliestTime:  time.Time{},
		EstimatedSize: pf.TotalSize,
	}
	_ = h.opStore.Update(op)

	// ================================================================
	// Phase 2: confirmed -> parsing -> previewed
	// ================================================================
	if !h.tryTransition(op, StateParsing) {
		return
	}

	parseParams := map[string]interface{}{
		"operationId": op.ID,
		"targetTable": op.TargetTable,
		"endTime":     op.RecoveryTime.Format(time.RFC3339),
	}
	if len(op.BinlogFiles) > 0 {
		parseParams["binlogFiles"] = op.BinlogFiles
	}
	if op.StartTime != nil {
		parseParams["startTime"] = op.StartTime.Format(time.RFC3339)
	}
	if op.StartPos != nil {
		parseParams["startPos"] = *op.StartPos
	}
	if op.StopPos != nil {
		parseParams["stopPos"] = *op.StopPos
	}

	parseCtx, parseCancel := context.WithTimeout(ctx, 30*time.Minute)
	resp, err = h.commander.SendToAgent(parseCtx, op.AgentID, ws.Command{
		Cmd:    newCmdID("parse"),
		Type:   ws.CmdPITRParse,
		Params: parseParams,
	})
	parseCancel()
	if err != nil {
		h.failOperation(op, "parse via agent: %v", err)
		return
	}
	if resp.Status == ws.StatusError {
		h.failOperation(op, "parse failed: %s", resp.Error)
		return
	}

	var parseRes agentParseResult
	if err := decodeResult(resp, &parseRes); err != nil {
		h.failOperation(op, "decode parse result: %v", err)
		return
	}
	if parseRes.TotalRows == 0 {
		// Include the binlog files the agent actually parsed so users can tell
		// a too-early recovery time (events exist but after the stop time) from
		// missing/purged binlogs (no files or files don't cover the range).
		files := "none available"
		if op.PreflightRes != nil && len(op.PreflightRes.BinlogFiles) > 0 {
			files = strings.Join(op.PreflightRes.BinlogFiles, ", ")
		}
		h.failOperation(op,
			"no row events found for table %q before %s (binlog files parsed: %s; "+
				"if the table was modified after the recovery time, pick a later recovery time)",
			op.TargetTable, op.RecoveryTime.Format(time.RFC3339), files)
		return
	}

	if !h.tryTransition(op, StatePreviewed) {
		return
	}
	op.ParseRes = &ParseSummary{
		ParsedAt:     time.Now(),
		RowsAffected: parseRes.TotalRows,
		SQLSample:    parseRes.SQLSample,
		ReverseSql:   parseRes.Preview,
		SQLs:         parseRes.ReverseSql,
	}
	_ = h.opStore.Update(op)

	h.recordAudit(op, operator, "previewed", "")

	// Preview mode stops here — execution only happens via the explicit
	// POST /api/pitr/{id}/execute endpoint (user selects the statements).
	if op.Mode == "preview" {
		return
	}

	h.executeOperation(op, operator, op.ParseRes.SQLs)
}

// executeOperation drives the previewed -> executing -> completed phase of the
// state machine by sending the given reverse-SQL statements to the agent. It
// runs asynchronously; batch progress is pushed back by the agent.
func (h *Handler) executeOperation(op *Operation, operator string, sqls []string) {
	defer func() {
		if r := recover(); r != nil {
			h.failOperation(op, "panic: %v", r)
		}
	}()

	if !h.agentConnected(op.AgentID) {
		h.failOperation(op, "agent %s is not connected (agent_offline)", op.AgentID)
		return
	}

	if !h.tryTransition(op, StateExecuting) {
		return
	}

	op.Progress = &ProgressInfo{
		BatchesTotal:      0,
		BatchesComplete:   0,
		RowsRestored:      0,
		EstimatedRemaining: "calculating...",
	}
	_ = h.opStore.Update(op)

	// Long-running; bounded by agent disconnect detection on the hub (the
	// pending command channel is drained when the agent drops).
	ctx := context.Background()
	resp, err := h.commander.SendToAgent(ctx, op.AgentID, ws.Command{
		Cmd:  newCmdID("execute"),
		Type: ws.CmdPITRExecute,
		Params: map[string]interface{}{
			"operationId":  op.ID,
			"sql":          sqls,
			"batchSize":    100,
			"targetTable":  op.TargetTable,
			"recoveryTime": op.RecoveryTime.Format(time.RFC3339),
		},
	})
	if err != nil {
		h.failOperation(op, "execute via agent: %v", err)
		return
	}
	if resp.Status == ws.StatusError {
		h.failOperation(op, "execute failed: %s", resp.Error)
		return
	}

	var execRes agentExecuteResult
	if err := decodeResult(resp, &execRes); err != nil {
		h.failOperation(op, "decode execute result: %v", err)
		return
	}

	if op.State == StateCancelled || execRes.Cancelled {
		op.State = StateCancelled
		_ = h.opStore.Update(op)
		return
	}

	if len(execRes.Errors) > 0 {
		h.failOperation(op, "execution errors: %v", execRes.Errors)
		return
	}

	op.State = StateCompleted
	op.ExecRes = &ExecSummary{
		ExecutedAt:   time.Now(),
		RowsRestored: execRes.RowsAffected,
		Duration:     "",
	}
	if op.Progress != nil {
		op.Progress.BatchesComplete = execRes.BatchesCompleted
		op.Progress.BatchesTotal = execRes.BatchesTotal
		op.Progress.EstimatedRemaining = "0s"
	}
	_ = h.opStore.Update(op)

	h.recordAudit(op, operator, "completed", "")
}

// tryTransition attempts a state transition. Returns false if the transition
// is invalid (e.g. cancelled by another goroutine).
func (h *Handler) tryTransition(op *Operation, to OperationState) bool {
	if !TransitionValid(op.State, to) {
		log.Printf("pitr: invalid state transition %s -> %s for op %s", op.State, to, op.ID)
		return false
	}
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
