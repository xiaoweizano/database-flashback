package pitr

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Operation represents a point-in-time recovery operation and its current
// state within the workflow state machine.
type Operation struct {
	ID           string          `json:"id"`
	OrgID        string          `json:"orgId"`
	AgentID      string          `json:"agentId"`
	TargetTable  string          `json:"targetTable"`
	RecoveryTime time.Time       `json:"recoveryTime"`
	Mode         string          `json:"mode"` // "preview" or "execute"
	State        OperationState  `json:"state"`
	PreflightRes *PreflightResult `json:"preflightResult,omitempty"`
	ParseRes     *ParseSummary   `json:"parseResult,omitempty"`
	ExecRes      *ExecSummary    `json:"execResult,omitempty"`
	Progress     *ProgressInfo   `json:"progress,omitempty"`
	Error        string          `json:"error,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

// PreflightResult contains the output of the preflight check phase.
type PreflightResult struct {
	CheckedAt     time.Time `json:"checkedAt"`
	BinlogFiles   []string  `json:"binlogFiles"`
	EarliestTime  time.Time `json:"earliestTime"`
	EstimatedSize int64     `json:"estimatedSize"`
}

// ParseSummary contains the output of the binlog parsing phase.
type ParseSummary struct {
	ParsedAt     time.Time `json:"parsedAt"`
	RowsAffected int64     `json:"rowsAffected"`
	SQLSample    string    `json:"sqlSample"`
}

// ExecSummary contains the output of the recovery execution phase.
type ExecSummary struct {
	ExecutedAt   time.Time `json:"executedAt"`
	RowsRestored int64     `json:"rowsRestored"`
	Duration     string    `json:"duration"`
}

// ProgressInfo holds real-time execution progress data, updated as batches are
// processed during the executing phase.
type ProgressInfo struct {
	BatchesComplete   int    `json:"batchesComplete"`
	BatchesTotal      int    `json:"batchesTotal"`
	RowsRestored      int64  `json:"rowsRestored"`
	EstimatedRemaining string `json:"estimatedRemaining"`
}

// OperationStore defines the persistence contract for PITR operations.
type OperationStore interface {
	Create(op *Operation) error
	Get(id string) (*Operation, error)
	Update(op *Operation) error
	ListByOrg(orgID string) ([]*Operation, error)
}

// InMemoryOperationStore is a thread-safe in-memory implementation of
// OperationStore.
type InMemoryOperationStore struct {
	mu  sync.RWMutex
	ops map[string]*Operation
}

// NewInMemoryOperationStore creates an initialised InMemoryOperationStore.
func NewInMemoryOperationStore() *InMemoryOperationStore {
	return &InMemoryOperationStore{
		ops: make(map[string]*Operation),
	}
}

// Create stores a new operation, generating an ID and setting timestamps if
// they are not already populated.
func (s *InMemoryOperationStore) Create(op *Operation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if op.ID == "" {
		op.ID = generateOpID()
	}
	if op.CreatedAt.IsZero() {
		op.CreatedAt = time.Now()
	}
	if op.UpdatedAt.IsZero() {
		op.UpdatedAt = op.CreatedAt
	}
	if op.State == "" {
		op.State = StatePreflight
	}
	s.ops[op.ID] = op
	return nil
}

// Get retrieves an operation by its ID.
func (s *InMemoryOperationStore) Get(id string) (*Operation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	op, ok := s.ops[id]
	if !ok {
		return nil, fmt.Errorf("operation not found: %s", id)
	}
	return op, nil
}

// Update replaces an existing operation record. The UpdatedAt timestamp is
// automatically refreshed.
func (s *InMemoryOperationStore) Update(op *Operation) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.ops[op.ID]; !ok {
		return fmt.Errorf("operation not found: %s", op.ID)
	}
	op.UpdatedAt = time.Now()
	s.ops[op.ID] = op
	return nil
}

// ListByOrg returns all operations belonging to the given organisation.
func (s *InMemoryOperationStore) ListByOrg(orgID string) ([]*Operation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Operation
	for _, op := range s.ops {
		if op.OrgID == orgID {
			result = append(result, op)
		}
	}
	if result == nil {
		return []*Operation{}, nil
	}
	return result, nil
}

func generateOpID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "op_" + hex.EncodeToString(b)
}
