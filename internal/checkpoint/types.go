package checkpoint

import "time"

// CheckpointPlan describes the intent to create a new checkpoint.
type CheckpointPlan struct {
	RecoveryID   string
	TableName    string
	RecoveryTime time.Time
	TotalBatches int
	TableOrder   []string // FK-aware table processing order
}

// Checkpoint represents a persisted rollback-execution checkpoint.
type Checkpoint struct {
	RecoveryID       string    `json:"recoveryId"`
	TableName        string    `json:"tableName"`
	RecoveryTime     time.Time `json:"recoveryTime"`
	TotalBatches     int       `json:"totalBatches"`
	CompletedBatches int       `json:"completedBatches"`
	TableOrder       []string  `json:"tableOrder"`
	LastCommitAt     time.Time `json:"lastCommitAt"`
	Status           string    `json:"status"`           // "in_progress", "complete", "failed"
	Checksum         string    `json:"checksum,omitempty"` // SHA256 of final state
}
