package rollback

import "time"

// ExecOptions controls how rollback SQL is applied.
type ExecOptions struct {
	// BatchSize is the target number of SQL statements per batch (default 1000).
	BatchSize int
	// MaxBatchBytes is the maximum SQL text size per batch (default 10 MB).
	MaxBatchBytes int64
	// DryRun, when true, logs SQL without executing.
	DryRun bool
	// OnBatch is called after each batch completes (success or final failure).
	OnBatch func(batch BatchResult)
}

// BatchResult summarizes the outcome of a single batch execution.
type BatchResult struct {
	BatchNum      int
	SQLCount      int
	BytesEstimate int64
	RowsAffected  int64
	Error         error
	RetryAttempt  int // 0 = first try, 1+ = retry after dynamic resize
}

// ExecResult summarizes the outcome of the entire rollback execution.
type ExecResult struct {
	RowsAffected    int64
	BatchesTotal    int
	BatchesComplete int
	Errors          []BatchError
	CheckpointID    string
	StartedAt       time.Time
	CompletedAt     time.Time
}

// BatchError describes a single batch failure.
type BatchError struct {
	BatchNum int
	SQL      string // First SQL statement that failed
	Error    string
}
