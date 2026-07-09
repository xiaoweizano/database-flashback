package audit

import (
	"fmt"
	"sync"
	"time"
)

// AuditEntry records a single PITR operation for audit trail purposes.
type AuditEntry struct {
	OperationID  string    `json:"operationId"`
	Operator     string    `json:"operator"`
	Timestamp    time.Time `json:"timestamp"`
	OrgID        string    `json:"orgId"`
	AgentID      string    `json:"agentId"`
	TargetTable  string    `json:"targetTable"`
	RecoveryTime time.Time `json:"recoveryTime"`
	RowsAffected int64     `json:"rowsAffected"`
	Status       string    `json:"status"`
	ErrorDetails string    `json:"errorDetails,omitempty"`
}

// AuditFilter holds optional query parameters used when filtering audit
// entries.
type AuditFilter struct {
	OrgID   string
	From    time.Time
	To      time.Time
	Status  string
	AgentID string
}

// AuditStore defines the persistence contract for audit log entries.
type AuditStore interface {
	Append(entry *AuditEntry) error
	Query(filter AuditFilter) ([]AuditEntry, error)
	ListAll() ([]AuditEntry, error)
}

// InMemoryAuditStore is a thread-safe in-memory implementation of AuditStore.
type InMemoryAuditStore struct {
	mu      sync.RWMutex
	entries []AuditEntry
}

// NewInMemoryAuditStore creates an initialised InMemoryAuditStore.
func NewInMemoryAuditStore() *InMemoryAuditStore {
	return &InMemoryAuditStore{
		entries: make([]AuditEntry, 0),
	}
}

// Append adds an audit entry to the store.
func (s *InMemoryAuditStore) Append(entry *AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	s.entries = append(s.entries, *entry)
	return nil
}

// Query filters audit entries by the supplied filter. Only OrgID is required;
// all other fields are optional.
func (s *InMemoryAuditStore) Query(filter AuditFilter) ([]AuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if filter.OrgID == "" {
		return nil, fmt.Errorf("orgId filter is required")
	}

	var result []AuditEntry
	for _, e := range s.entries {
		if e.OrgID != filter.OrgID {
			continue
		}
		if !filter.From.IsZero() && e.Timestamp.Before(filter.From) {
			continue
		}
		if !filter.To.IsZero() && e.Timestamp.After(filter.To) {
			continue
		}
		if filter.Status != "" && e.Status != filter.Status {
			continue
		}
		if filter.AgentID != "" && e.AgentID != filter.AgentID {
			continue
		}
		result = append(result, e)
	}
	if result == nil {
		return []AuditEntry{}, nil
	}
	return result, nil
}

// ListAll returns all audit entries in insertion order.
func (s *InMemoryAuditStore) ListAll() ([]AuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]AuditEntry, len(s.entries))
	copy(result, s.entries)
	return result, nil
}
