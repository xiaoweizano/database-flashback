package rollback

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Executor executes reverse-SQL batches against a MySQL database.
type Executor struct {
	db *sql.DB
}

// NewExecutor creates an Executor backed by the given *sql.DB.
func NewExecutor(db *sql.DB) *Executor {
	return &Executor{db: db}
}

// Execute applies the provided SQL statements as rollback batches. Each batch
// runs inside its own transaction: BEGIN + statements + COMMIT (or ROLLBACK on
// error). On error the executor stops immediately (unlike the connector-level
// ExecuteRollback which continues).
//
// Dynamic batch size: if a batch fails with MySQL error 1118 (ER_BIG_ROW) or
// 1206 (ER_LOCK_WAIT_TIMEOUT) the batch is retried at half its original size,
// repeatedly, down to a minimum of 10 statements. If the retry succeeds the
// remaining statements from the original batch are processed in subsequent
// batches.
func (e *Executor) Execute(ctx context.Context, sqls []string, opts ExecOptions) (*ExecResult, error) {
	if e.db == nil {
		return nil, fmt.Errorf("rollback: no database connection")
	}
	if len(sqls) == 0 {
		return &ExecResult{}, nil
	}

	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	if opts.MaxBatchBytes <= 0 {
		opts.MaxBatchBytes = 10 * 1024 * 1024 // 10 MB
	}

	result := &ExecResult{
		StartedAt: time.Now(),
	}

	// Dry-run mode: log and return.
	if opts.DryRun {
		for _, s := range sqls {
			log.Printf("[rollback-dry-run] %s", s)
		}
		result.CompletedAt = time.Now()
		return result, nil
	}

	// Build batches once; individual batches may be sub-divided on retry.
	batches := buildBatches(sqls, opts.BatchSize, opts.MaxBatchBytes)
	result.BatchesTotal = len(batches)

	for i, batch := range batches {
		executed, err := e.executeWithRetry(ctx, batch, opts)

		if err == nil {
			result.BatchesComplete++
			result.RowsAffected += int64(executed)
			if opts.OnBatch != nil {
				opts.OnBatch(BatchResult{
					BatchNum:      i,
					SQLCount:      executed,
					BytesEstimate: estimateBytes(batch),
					RowsAffected:  int64(executed),
				})
			}
			continue
		}

		// Non-retryable error or retries exhausted.
		errMsg := err.Error()
		if mysqlErr := new(mysql.MySQLError); errors.As(err, &mysqlErr) {
			errMsg = fmt.Sprintf("MySQL errno %d: %s", mysqlErr.Number, mysqlErr.Message)
		}

		result.Errors = append(result.Errors, BatchError{
			BatchNum: i,
			SQL:      batch[0],
			Error:    errMsg,
		})
		if opts.OnBatch != nil {
			opts.OnBatch(BatchResult{
				BatchNum: i,
				SQLCount: len(batch),
				Error:    err,
			})
		}
		break // stop on first unrecoverable batch failure
	}

	result.CompletedAt = time.Now()
	return result, nil
}

// executeWithRetry attempts to run batch inside a transaction. On retryable
// errors (1118 / 1206) it halves the effective batch size and retries, down
// to a minimum of 10 statements.
func (e *Executor) executeWithRetry(ctx context.Context, batch []string, opts ExecOptions) (int, error) {
	size := len(batch)

	for attempt := 0; ; attempt++ {
		// Ensure size is within valid bounds.
		if size < 10 {
			size = 10
		}
		if size > len(batch) {
			size = len(batch)
		}

		err := e.executeBatch(ctx, batch[:size])
		if err == nil {
			return size, nil
		}
		if !isRetryableError(err) {
			return 0, err
		}

		// Halve the size and retry; stop if we'd go below 10.
		newSize := size / 2
		if newSize < 10 || newSize >= size {
			return 0, err
		}
		size = newSize
	}
}

// executeBatch runs a single batch inside a transaction.
func (e *Executor) executeBatch(ctx context.Context, batch []string) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback() // no-op if already committed
	}()

	for _, stmt := range batch {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// isRetryableError returns true when the error is a MySQL error eligible for
// automatic batch-size reduction (1118 = row too large, 1206 = lock wait).
func isRetryableError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1118 || mysqlErr.Number == 1206
	}
	return false
}

// buildBatches groups SQL statements into batches bounded by count and total
// byte size (whichever is reached first).
func buildBatches(sqls []string, batchSize int, maxBatchBytes int64) [][]string {
	if len(sqls) == 0 {
		return nil
	}

	var batches [][]string
	var current []string
	var currentBytes int64

	for _, stmt := range sqls {
		stmtBytes := int64(len(stmt))

		if len(current) >= batchSize || (currentBytes+stmtBytes > maxBatchBytes && len(current) > 0) {
			batches = append(batches, current)
			current = nil
			currentBytes = 0
		}

		current = append(current, stmt)
		currentBytes += stmtBytes
	}

	if len(current) > 0 {
		batches = append(batches, current)
	}

	return batches
}

// estimateBytes returns the total byte length of all SQL statements combined.
func estimateBytes(sqls []string) int64 {
	var n int64
	for _, s := range sqls {
		n += int64(len(s))
	}
	return n
}
