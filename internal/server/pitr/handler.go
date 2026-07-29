package pitr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/a-shan/mysql-pitr/internal/config"
	"github.com/a-shan/mysql-pitr/internal/connector"
	"github.com/a-shan/mysql-pitr/internal/parser"
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
	AgentID         string `json:"agent_id"`
	TargetTable     string `json:"target_table"`
	RecoveryTime    string `json:"recovery_time"`
	Mode            string `json:"mode"` // "preview" or "execute"
	MySQLDSN        string `json:"mysql_dsn"`
	MySQLBinlogPath string `json:"mysqlbinlog_path,omitempty"`
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
		filtered = append(filtered, op)
	}
	if filtered == nil {
		filtered = []*Operation{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"operations": filtered})
}

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
	if req.AgentID == "" || req.TargetTable == "" || req.RecoveryTime == "" || req.Mode == "" || req.MySQLDSN == "" {
		writeError(w, http.StatusBadRequest,
			"agent_id, target_table, recovery_time, mode, and mysql_dsn are required")
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
		OrgID:           agt.OrgID,
		AgentID:         req.AgentID,
		TargetTable:     req.TargetTable,
		RecoveryTime:    recoveryTime,
		Mode:            req.Mode,
		DSN:             req.MySQLDSN,
		MySQLBinlogPath: req.MySQLBinlogPath,
		State:           StatePreflight,
	}

	if err := h.opStore.Create(op); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Launch the asynchronous workflow simulation. In production this would
	// be dispatched to a worker queue.
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

// findMySQLBinlog locates the mysqlbinlog binary. If mysqlbinlogPath is
// non-empty it is validated and returned directly. Otherwise the function
// checks PATH, MySQL basedir, and common installation paths.
func findMySQLBinlog(mysqlbinlogPath string, ctx context.Context, conn *connector.MySQLConnector) (string, error) {
	// 0. Explicit path from user config — use it directly.
	if mysqlbinlogPath != "" {
		if fi, err := os.Stat(mysqlbinlogPath); err == nil && fi.Mode().IsRegular() {
			abs, _ := filepath.Abs(mysqlbinlogPath)
			return abs, nil
		}
		return "", fmt.Errorf("specified mysqlbinlog path %q does not exist", mysqlbinlogPath)
	}

	// 1. Try PATH first (supports both mysqlbinlog and mariadb-binlog).
	for _, name := range []string{"mysqlbinlog", "mariadb-binlog"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}

	// 2. Try MySQL basedir (SHOW VARIABLES LIKE 'basedir').
	basedir, err := conn.GetBasedir(ctx)
	if err == nil && basedir != "" {
		for _, name := range []string{"mysqlbinlog", "mysqlbinlog.exe", "mariadb-binlog"} {
			candidate := filepath.Join(basedir, "bin", name)
			if fi, statErr := os.Stat(candidate); statErr == nil && fi.Mode().IsRegular() {
				abs, _ := filepath.Abs(candidate)
				return abs, nil
			}
		}
	}

	// 3. Common installation paths.
	commonPaths := []string{
		"/usr/bin/mysqlbinlog",
		"/usr/bin/mariadb-binlog",
		"/usr/local/mysql/bin/mysqlbinlog",
		"/opt/homebrew/bin/mysqlbinlog",
		"C:\\Program Files\\MySQL\\MySQL Server 8.0\\bin\\mysqlbinlog.exe",
		"C:\\Program Files\\MySQL\\MySQL Server 8.4\\bin\\mysqlbinlog.exe",
		"C:\\Program Files\\MySQL\\MySQL Server 9.0\\bin\\mysqlbinlog.exe",
	}
	for _, p := range commonPaths {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			abs, _ := filepath.Abs(p)
			return abs, nil
		}
	}

	return "", fmt.Errorf("mysqlbinlog not found; install mysql-client or specify the path in the mysqlbinlog_path field")
}

// parseBinlogRemote uses mysqlbinlog --read-from-remote-server to parse binlog
// files from a remote MySQL server. This is the fallback when binlog files are
// not accessible on the local filesystem.
func (h *Handler) parseBinlogRemote(mysqlbinlogPath string, cfg connector.ConnConfig, binlogNames []string, targetTable string, recoveryTime time.Time) (*connector.ParseResult, error) {
	parts := strings.SplitN(targetTable, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid target table %q: expected schema.table format", targetTable)
	}
	recoveryStr := recoveryTime.Format("2006-01-02 15:04:05")
	args := []string{
		"--no-defaults",
		"--base64-output=DECODE-ROWS",
		"--verbose",
		"--stop-datetime=" + recoveryStr,
		"--read-from-remote-server",
		"--host=" + cfg.Host,
		"--port=" + strconv.Itoa(cfg.Port),
		"--user=" + cfg.User,
		"--password=" + cfg.Password,
		"--protocol=TCP",
	}
	args = append(args, binlogNames...)

	cmd := exec.Command(mysqlbinlogPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mysqlbinlog pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("mysqlbinlog stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mysqlbinlog start: %w", err)
	}
	
	go func() {
		s := bufio.NewScanner(stderr)
		for s.Scan() {
			if t := s.Text(); t != "" {
				log.Printf("pitr: mysqlbinlog: %s", t)
			}
		}
	}()
	
	tablePattern := "`" + parts[0] + "`.`" + parts[1] + "`"
	var events []connector.RowEvent
	var evType connector.EventType
	var values []interface{}
	inRow := false
	
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "### DELETE FROM "+tablePattern) {
			if inRow {
				events = append(events, makeRowEvent(evType, parts[0], parts[1], values))
			}
			evType = connector.DeleteEvent
			values = nil
			inRow = true
			continue
		}
		if strings.Contains(line, "### INSERT INTO "+tablePattern) {
			if inRow {
				events = append(events, makeRowEvent(evType, parts[0], parts[1], values))
			}
			evType = connector.InsertEvent
			values = nil
			inRow = true
			continue
		}
		if strings.Contains(line, "### UPDATE "+tablePattern) {
			if inRow {
				events = append(events, makeRowEvent(evType, parts[0], parts[1], values))
			}
			evType = connector.UpdateEvent
			values = nil
			inRow = true
			continue
		}
		if inRow && strings.HasPrefix(line, "###   @") {
			eq := strings.IndexByte(line, '=')
			if eq > 0 {
				val := parseColumnValue(line[eq+1:])
				values = append(values, val)
			}
		}
		if inRow && !strings.HasPrefix(line, "###") {
			events = append(events, makeRowEvent(evType, parts[0], parts[1], values))
			values = nil
			inRow = false
		}
	}
	if inRow && len(values) > 0 {
		events = append(events, makeRowEvent(evType, parts[0], parts[1], values))
	}
	_ = cmd.Wait()
	log.Printf("pitr: mysqlbinlog remote parsed %d row event(s) for %s", len(events), targetTable)
	return &connector.ParseResult{Events: events, TotalRows: int64(len(events))}, nil
}

// makeRowEvent constructs a RowEvent from mysqlbinlog parsed column values.
func makeRowEvent(typ connector.EventType, db, table string, vals []interface{}) connector.RowEvent {
	ev := connector.RowEvent{Type: typ, Database: db, Table: table}
	switch typ {
	case connector.DeleteEvent:
		m := make(map[string]interface{}, len(vals))
		for i, v := range vals {
			m[fmt.Sprintf("col_%d", i)] = v
		}
		ev.Before = m
	case connector.InsertEvent:
		m := make(map[string]interface{}, len(vals))
		for i, v := range vals {
			m[fmt.Sprintf("col_%d", i)] = v
		}
		ev.After = m
	case connector.UpdateEvent:
		if len(vals)%2 == 0 {
			half := len(vals) / 2
			before := make(map[string]interface{}, half)
			after := make(map[string]interface{}, half)
			for i := 0; i < half; i++ {
				before[fmt.Sprintf("col_%d", i)] = vals[i]
				after[fmt.Sprintf("col_%d", i)] = vals[half+i]
			}
			ev.Before = before
			ev.After = after
		}
	}
	return ev
}

// parseColumnValue parses a column value from mysqlbinlog text output.
func parseColumnValue(s string) interface{} {
	s = strings.TrimSpace(s)
	if s == "NULL" || s == "'NULL'" {
		return nil
	}
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") {
		return strings.Trim(s, "'")
	}
	var i int64
	if _, err := fmt.Sscanf(s, "%d", &i); err == nil {
		if strings.Contains(s, ".") {
			var f float64
			fmt.Sscanf(s, "%f", &f)
			return f
		}
		return i
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err == nil {
		return f
	}
	return s
}

// ---------- background operation execution ----------

// failOperation transitions the operation to the failed state with the given
// error message and persists the change.
func (h *Handler) failOperation(op *Operation, format string, args ...interface{}) {
	op.State = StateFailed
	op.Error = fmt.Sprintf(format, args...)
	_ = h.opStore.Update(op)
	log.Printf("pitr: operation %s failed: %s", op.ID, op.Error)
}

// runOperation advances the operation through the state machine using real
// MySQL connections, binlog parsing, and SQL execution. It replaces the
// previous simulateOperation which used hardcoded fake data.
func (h *Handler) runOperation(op *Operation, operator string) {
	defer func() {
		if r := recover(); r != nil {
			h.failOperation(op, "panic: %v", r)
		}
	}()

	// ---- Parse DSN ----
	connCfg, err := config.ParseDSNToConnConfig(op.DSN)
	if err != nil {
		h.failOperation(op, "invalid MySQL DSN: %v", err)
		return
	}

	// ---- Connect ----
	conn := connector.NewMySQLConnector()
	if err := conn.Connect(connCfg); err != nil {
		h.failOperation(op, "connect to MySQL failed: %v", err)
		return
	}
	defer conn.Close()

	ctx := context.Background()

	// ================================================================
	// Phase 1: preflight -> confirmed
	// ================================================================
	pCtx, pCancel := context.WithTimeout(ctx, 30*time.Second)
	defer pCancel()

	pfRes, err := conn.Preflight(pCtx)
	if err != nil {
		h.failOperation(op, "preflight error: %v", err)
		return
	}
	if pfRes.Status == connector.PreflightFail {
		h.failOperation(op, "preflight checks failed")
		return
	}

	binlogs, err := conn.GetBinlogFiles(pCtx)
	if err != nil {
		h.failOperation(op, "list binlog files: %v", err)
		return
	}
	binlogNames := make([]string, len(binlogs))
	var totalSize int64
	for i, bf := range binlogs {
		binlogNames[i] = bf.Name
		totalSize += bf.Size
	}

	binlogDir, err := conn.GetBinlogDir(pCtx)
	if err != nil {
		h.failOperation(op, "resolve binlog directory: %v", err)
		return
	}
	pCancel()

	paths := make([]string, 0, len(binlogNames))
	for _, name := range binlogNames {
		paths = append(paths, filepath.Join(binlogDir, name))
	}

	if !h.tryTransition(op, StateConfirmed) {
		return
	}
	op.PreflightRes = &PreflightResult{
		CheckedAt:     time.Now(),
		BinlogFiles:   binlogNames,
		EarliestTime:  time.Time{},
		EstimatedSize: totalSize,
	}
	_ = h.opStore.Update(op)

	// ================================================================
	// Phase 2: confirmed -> parsing -> previewed
	// ================================================================
	if !h.tryTransition(op, StateParsing) {
		return
	}

	var parseRes *connector.ParseResult

	// Check if binlog files are accessible locally; if not, try mysqlbinlog remote.
	if len(paths) > 0 {
		if _, statErr := os.Stat(paths[0]); os.IsNotExist(statErr) {
			log.Printf("pitr: binlog files not accessible locally, trying mysqlbinlog remote for op %s", op.ID)
			mysqlbinlogPath, findErr := findMySQLBinlog(op.MySQLBinlogPath, ctx, conn)
			if findErr != nil {
				h.failOperation(op, "parse binlogs (remote): %v", findErr)
				return
			}
			parseRes, err = h.parseBinlogRemote(mysqlbinlogPath, connCfg, binlogNames, op.TargetTable, op.RecoveryTime)
			if err != nil {
				h.failOperation(op, "parse binlogs (remote): %v", err)
				return
			}
		}
	}

	// Fall back to local native parser if remote was not attempted or not needed.
	if parseRes == nil {
		bp := parser.NewBinlogParser()
		bp.SetSkipChecksum(true)
		parseRes, err = bp.ParseFiles(paths, parser.ParseOptions{
			EndTime:     op.RecoveryTime,
			TargetTable: op.TargetTable,
		})
		if err != nil {
			h.failOperation(op, "parse binlogs: %v", err)
			return
		}
	}

	reverseSqls, err := parser.ReverseSQLBatch(parseRes.Events, nil)
	if err != nil {
		h.failOperation(op, "generate reverse SQL: %v", err)
		return
	}

	maxEntries := len(reverseSqls)
	if maxEntries > 1000 {
		maxEntries = 1000
	}
	reverseEntries := make([]ReverseSqlEntry, 0, maxEntries)
	for i := 0; i < maxEntries; i++ {
		ev := parseRes.Events[i]
		reverseEntries = append(reverseEntries, ReverseSqlEntry{
			Sequence:     i + 1,
			SqlType:      string(ev.Type),
			TableName:    ev.Table,
			OriginalSql:  "",
			ReverseSql:   reverseSqls[i],
			RowsAffected: 1,
		})
	}

	sqlSample := ""
	if len(reverseSqls) > 0 {
		sqlSample = reverseSqls[0]
	}

	if !h.tryTransition(op, StatePreviewed) {
		return
	}
	op.ParseRes = &ParseSummary{
		ParsedAt:     time.Now(),
		RowsAffected: int64(len(reverseSqls)),
		SQLSample:    sqlSample,
		ReverseSql:   reverseEntries,
	}
	_ = h.opStore.Update(op)

	h.recordAudit(op, operator, "previewed", "")

	// Preview mode stops here
	if op.Mode == "preview" {
		return
	}

	// ================================================================
	// Phase 3: previewed -> executing -> completed
	// ================================================================
	if !h.tryTransition(op, StateExecuting) {
		return
	}

	batchSize := 100
	totalBatches := int(math.Ceil(float64(len(reverseSqls)) / float64(batchSize)))
	if totalBatches < 1 {
		totalBatches = 1
	}

	op.Progress = &ProgressInfo{
		BatchesTotal:      totalBatches,
		BatchesComplete:   0,
		RowsRestored:      0,
		EstimatedRemaining: "calculating...",
	}
	_ = h.opStore.Update(op)

	var totalRows int64
	execStart := time.Now()

	for i := 0; i < len(reverseSqls); i += batchSize {
		end := i + batchSize
		if end > len(reverseSqls) {
			end = len(reverseSqls)
		}
		batch := reverseSqls[i:end]

		_, err := conn.ExecuteRollback(ctx, batch, connector.ExecOptions{
			BatchSize: len(batch),
		})
		if err != nil {
			h.failOperation(op, "execution error at batch %d: %v",
				op.Progress.BatchesComplete, err)
			return
		}

		totalRows += int64(len(batch))
		op.Progress.BatchesComplete++
		op.Progress.RowsRestored = totalRows

		elapsed := time.Since(execStart)
		if op.Progress.BatchesComplete > 0 {
			perBatch := elapsed / time.Duration(op.Progress.BatchesComplete)
			remaining := perBatch * time.Duration(totalBatches-op.Progress.BatchesComplete)
			op.Progress.EstimatedRemaining = remaining.Round(time.Second).String()
		}
		_ = h.opStore.Update(op)
	}

	op.State = StateCompleted
	op.ExecRes = &ExecSummary{
		ExecutedAt:   time.Now(),
		RowsRestored: totalRows,
		Duration:     time.Since(execStart).Round(time.Second).String(),
	}
	op.Progress.BatchesComplete = totalBatches
	op.Progress.EstimatedRemaining = "0s"
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
