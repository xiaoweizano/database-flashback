package rollback

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func newMockExecutor(t *testing.T) (*Executor, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := newMockDB(t)
	return NewExecutor(db), mock
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func TestNewExecutor(t *testing.T) {
	db, _ := newMockDB(t)
	e := NewExecutor(db)
	assert.NotNil(t, e)
	assert.Equal(t, db, e.db)
}

// ---------------------------------------------------------------------------
// Execute – nil DB
// ---------------------------------------------------------------------------

func TestExecute_NilDB(t *testing.T) {
	e := &Executor{db: nil}
	_, err := e.Execute(context.Background(), []string{"stmt1"}, ExecOptions{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no database connection")
}

// ---------------------------------------------------------------------------
// Execute – empty SQL
// ---------------------------------------------------------------------------

func TestExecute_EmptySQL(t *testing.T) {
	e, _ := newMockExecutor(t)
	result, err := e.Execute(context.Background(), nil, ExecOptions{})
	require.NoError(t, err)
	assert.Equal(t, 0, result.BatchesTotal)
	assert.Equal(t, 0, result.BatchesComplete)
	assert.Empty(t, result.Errors)

	result, err = e.Execute(context.Background(), []string{}, ExecOptions{})
	require.NoError(t, err)
	assert.Equal(t, 0, result.BatchesTotal)
}

// ---------------------------------------------------------------------------
// Execute – dry run
// ---------------------------------------------------------------------------

func TestExecute_DryRun(t *testing.T) {
	e, mock := newMockExecutor(t)

	result, err := e.Execute(context.Background(),
		[]string{"DELETE FROM t1", "DELETE FROM t2"},
		ExecOptions{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, 0, result.BatchesTotal)
	assert.Equal(t, 0, result.BatchesComplete)
	assert.Empty(t, result.Errors)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Execute – single batch success
// ---------------------------------------------------------------------------

func TestExecute_SingleBatch(t *testing.T) {
	e, mock := newMockExecutor(t)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM t1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM t2").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	result, err := e.Execute(context.Background(),
		[]string{"DELETE FROM t1", "DELETE FROM t2"},
		ExecOptions{BatchSize: 10})
	require.NoError(t, err)
	assert.Equal(t, 1, result.BatchesTotal)
	assert.Equal(t, 1, result.BatchesComplete)
	assert.Equal(t, int64(2), result.RowsAffected)
	assert.Empty(t, result.Errors)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Execute – multiple batches
// ---------------------------------------------------------------------------

func TestExecute_MultipleBatches(t *testing.T) {
	e, mock := newMockExecutor(t)

	// Batch 1.
	mock.ExpectBegin()
	mock.ExpectExec("stmt1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("stmt2").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	// Batch 2.
	mock.ExpectBegin()
	mock.ExpectExec("stmt3").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := e.Execute(context.Background(),
		[]string{"stmt1", "stmt2", "stmt3"},
		ExecOptions{BatchSize: 2})
	require.NoError(t, err)
	assert.Equal(t, 2, result.BatchesTotal)
	assert.Equal(t, 2, result.BatchesComplete)
	assert.Equal(t, int64(3), result.RowsAffected)
	assert.Empty(t, result.Errors)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Execute – batch fails, stop immediately
// ---------------------------------------------------------------------------

func TestExecute_BatchError_Stops(t *testing.T) {
	e, mock := newMockExecutor(t)

	// First batch fails.
	mock.ExpectBegin()
	mock.ExpectExec("stmt1").WillReturnError(errors.New("constraint violation"))
	mock.ExpectRollback()

	// Executor should NOT continue to the second batch (unlike connector).
	result, err := e.Execute(context.Background(),
		[]string{"stmt1", "stmt2"},
		ExecOptions{BatchSize: 1})
	require.NoError(t, err)
	assert.Equal(t, 0, result.BatchesComplete)
	assert.Len(t, result.Errors, 1)
	assert.Equal(t, 0, result.Errors[0].BatchNum)
	assert.Contains(t, result.Errors[0].Error, "constraint violation")
	assert.Equal(t, "stmt1", result.Errors[0].SQL)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Execute – dynamic batch size retry (error 1118)
// ---------------------------------------------------------------------------

func TestExecute_RetryError1118_ThenFail(t *testing.T) {
	e, mock := newMockExecutor(t)

	sqls := make([]string, 20)
	for i := range sqls {
		sqls[i] = "stmt1"
	}

	// First attempt with 20 statements: first exec fails with ER_BIG_ROW.
	mock.ExpectBegin()
	mock.ExpectExec("stmt1").WillReturnError(&mysql.MySQLError{Number: 1118, Message: "Row size too large"})
	mock.ExpectRollback()

	// Retry with halved batch (10): also fails.
	mock.ExpectBegin()
	mock.ExpectExec("stmt1").WillReturnError(&mysql.MySQLError{Number: 1118, Message: "Row size too large"})
	mock.ExpectRollback()

	result, err := e.Execute(context.Background(), sqls, ExecOptions{BatchSize: 20})
	require.NoError(t, err)
	assert.Equal(t, 0, result.BatchesComplete)
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0].Error, "1118")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExecute_RetryError1118_Success(t *testing.T) {
	e, mock := newMockExecutor(t)

	sqls := make([]string, 20)
	for i := range sqls {
		sqls[i] = "INSERT INTO t VALUES (1)"
	}

	// First attempt with 20 statements: first exec fails with ER_BIG_ROW.
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO t").WillReturnError(&mysql.MySQLError{Number: 1118, Message: "Row size too large"})
	mock.ExpectRollback()

	// Retry with halved batch (10): all succeed.
	mock.ExpectBegin()
	for i := 0; i < 10; i++ {
		mock.ExpectExec("INSERT INTO t").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()

	result, err := e.Execute(context.Background(), sqls, ExecOptions{BatchSize: 20})
	require.NoError(t, err)
	assert.Equal(t, 1, result.BatchesTotal)
	assert.Equal(t, 1, result.BatchesComplete)
	assert.Equal(t, int64(10), result.RowsAffected)
	assert.Empty(t, result.Errors)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Execute – dynamic batch size retry (error 1206)
// ---------------------------------------------------------------------------

func TestExecute_RetryError1206_ThenFail(t *testing.T) {
	e, mock := newMockExecutor(t)

	sqls := make([]string, 20)
	for i := range sqls {
		sqls[i] = "UPDATE t SET x=1"
	}

	// First attempt with 20: first exec fails with ER_LOCK_WAIT_TIMEOUT.
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE t").WillReturnError(&mysql.MySQLError{Number: 1206, Message: "Lock wait timeout"})
	mock.ExpectRollback()

	// Retry with 10: also fails.
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE t").WillReturnError(&mysql.MySQLError{Number: 1206, Message: "Lock wait timeout"})
	mock.ExpectRollback()

	result, err := e.Execute(context.Background(), sqls, ExecOptions{BatchSize: 20})
	require.NoError(t, err)
	assert.Equal(t, 0, result.BatchesComplete)
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0].Error, "1206")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExecute_RetryError1206_Success(t *testing.T) {
	e, mock := newMockExecutor(t)

	sqls := make([]string, 20)
	for i := range sqls {
		sqls[i] = "UPDATE t SET x=1"
	}

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE t").WillReturnError(&mysql.MySQLError{Number: 1206, Message: "Lock wait timeout"})
	mock.ExpectRollback()

	mock.ExpectBegin()
	for i := 0; i < 10; i++ {
		mock.ExpectExec("UPDATE t").WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()

	result, err := e.Execute(context.Background(), sqls, ExecOptions{BatchSize: 20})
	require.NoError(t, err)
	assert.Equal(t, 1, result.BatchesComplete)
	assert.Empty(t, result.Errors)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Execute – non-retryable error
// ---------------------------------------------------------------------------

func TestExecute_NonRetryableError(t *testing.T) {
	e, mock := newMockExecutor(t)

	// First attempt fails with a non-retryable MySQL error.
	mock.ExpectBegin()
	mock.ExpectExec("stmt1").WillReturnError(&mysql.MySQLError{Number: 1146, Message: "Table not found"})
	mock.ExpectRollback()

	result, err := e.Execute(context.Background(),
		[]string{"stmt1", "stmt2"},
		ExecOptions{BatchSize: 2})
	require.NoError(t, err)
	assert.Equal(t, 0, result.BatchesComplete)
	assert.Len(t, result.Errors, 1)
	assert.Contains(t, result.Errors[0].Error, "1146")
	assert.Contains(t, result.Errors[0].Error, "Table not found")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Execute – retry exhaustion (batch reduced to minimum 10 still fails)
// ---------------------------------------------------------------------------

func TestExecute_RetryExhaustion(t *testing.T) {
	e, mock := newMockExecutor(t)

	sqls := make([]string, 20)
	for i := range sqls {
		sqls[i] = "stmt1"
	}

	// Attempt 1: batch of 20 fails with 1118.
	mock.ExpectBegin()
	mock.ExpectExec("stmt1").WillReturnError(&mysql.MySQLError{Number: 1118, Message: "too big"})
	mock.ExpectRollback()

	// Retry with 10 also fails; 10/2=5 which is <10, so we stop.
	mock.ExpectBegin()
	mock.ExpectExec("stmt1").WillReturnError(&mysql.MySQLError{Number: 1118, Message: "too big"})
	mock.ExpectRollback()

	result, err := e.Execute(context.Background(), sqls, ExecOptions{BatchSize: 20})
	require.NoError(t, err)
	assert.Equal(t, 0, result.BatchesComplete)
	assert.Len(t, result.Errors, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Execute – with OnBatch callback
// ---------------------------------------------------------------------------

func TestExecute_OnBatchCallback(t *testing.T) {
	e, mock := newMockExecutor(t)

	mock.ExpectBegin()
	mock.ExpectExec("stmt1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	var cbResult BatchResult
	opts := ExecOptions{
		BatchSize: 10,
		OnBatch: func(br BatchResult) {
			cbResult = br
		},
	}

	result, err := e.Execute(context.Background(), []string{"stmt1"}, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, result.BatchesComplete)
	assert.Equal(t, 0, cbResult.BatchNum)
	assert.Equal(t, 1, cbResult.SQLCount)
	assert.NoError(t, cbResult.Error)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExecute_OnBatchCallbackError(t *testing.T) {
	e, mock := newMockExecutor(t)

	mock.ExpectBegin()
	mock.ExpectExec("stmt1").WillReturnError(errors.New("fail"))
	mock.ExpectRollback()

	var cbResult BatchResult
	opts := ExecOptions{
		BatchSize: 10,
		OnBatch: func(br BatchResult) {
			cbResult = br
		},
	}

	result, err := e.Execute(context.Background(), []string{"stmt1"}, opts)
	require.NoError(t, err)
	assert.Equal(t, 0, result.BatchesComplete)
	assert.Error(t, cbResult.Error)
	assert.Contains(t, cbResult.Error.Error(), "fail")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Execute – byte-bounded batches
// ---------------------------------------------------------------------------

func TestExecute_ByteBoundedBatches(t *testing.T) {
	e, mock := newMockExecutor(t)

	// Each statement is ~50 bytes. With MaxBatchBytes=60 each batch gets 1 stmt.
	stmt1 := "INSERT INTO t VALUES (1)"
	stmt2 := "INSERT INTO t VALUES (2)"

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(stmt1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(stmt2)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := e.Execute(context.Background(),
		[]string{stmt1, stmt2},
		ExecOptions{MaxBatchBytes: 25})
	require.NoError(t, err)
	assert.Equal(t, 2, result.BatchesTotal)
	assert.Equal(t, 2, result.BatchesComplete)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// buildBatches
// ---------------------------------------------------------------------------

func TestBuildBatches_Empty(t *testing.T) {
	assert.Nil(t, buildBatches(nil, 100, 10*1024*1024))
	assert.Nil(t, buildBatches([]string{}, 100, 10*1024*1024))
}

func TestBuildBatches_CountBound(t *testing.T) {
	sqls := []string{"a", "b", "c", "d", "e"}
	batches := buildBatches(sqls, 2, 10*1024*1024)
	assert.Len(t, batches, 3)
	assert.Equal(t, []string{"a", "b"}, batches[0])
	assert.Equal(t, []string{"c", "d"}, batches[1])
	assert.Equal(t, []string{"e"}, batches[2])
}

func TestBuildBatches_ByteBound(t *testing.T) {
	sqls := []string{"12345", "12345", "12345"} // 5 bytes each
	batches := buildBatches(sqls, 100, 12)        // 12 byte max
	assert.Len(t, batches, 2)
	assert.Equal(t, []string{"12345", "12345"}, batches[0]) // 10 bytes
	assert.Equal(t, []string{"12345"}, batches[1])          // 5 bytes
}

func TestBuildBatches_SingleElementExceedsLimit(t *testing.T) {
	// A single element that exceeds MaxBatchBytes should still go in its own batch.
	sqls := []string{string(make([]byte, 100))}
	batches := buildBatches(sqls, 100, 50)
	assert.Len(t, batches, 1)
	assert.Len(t, batches[0], 1)
}

// ---------------------------------------------------------------------------
// isRetryableError
// ---------------------------------------------------------------------------

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		retryable bool
	}{
		{"nil error", nil, false},
		{"generic error", errors.New("oops"), false},
		{"MySQL 1118", &mysql.MySQLError{Number: 1118, Message: "Row size too large"}, true},
		{"MySQL 1206", &mysql.MySQLError{Number: 1206, Message: "Lock wait timeout"}, true},
		{"MySQL 1146", &mysql.MySQLError{Number: 1146, Message: "Table not found"}, false},
		{"MySQL 1062", &mysql.MySQLError{Number: 1062, Message: "Duplicate entry"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.retryable, isRetryableError(tt.err))
		})
	}
}

// ---------------------------------------------------------------------------
// estimateBytes
// ---------------------------------------------------------------------------

func TestEstimateBytes(t *testing.T) {
	assert.Equal(t, int64(0), estimateBytes(nil))
	assert.Equal(t, int64(5), estimateBytes([]string{"hello"}))
	assert.Equal(t, int64(10), estimateBytes([]string{"hello", "world"}))
}

// ---------------------------------------------------------------------------
// Zero batch size (use default)
// ---------------------------------------------------------------------------

func TestExecute_ZeroBatchSizeUsesDefault(t *testing.T) {
	e, mock := newMockExecutor(t)

	// With BatchSize=0, the executor should default to 1000. Create enough
	// statements to spill into two batches if batch size were 1 — with the
	// default of 1000 everything fits in one batch.
	mock.ExpectBegin()
	mock.ExpectExec("stmt1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("stmt2").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := e.Execute(context.Background(),
		[]string{"stmt1", "stmt2"},
		ExecOptions{BatchSize: 0}) // zero → default
	require.NoError(t, err)
	assert.Equal(t, 1, result.BatchesTotal)
	assert.Equal(t, 1, result.BatchesComplete)
	assert.Equal(t, int64(2), result.RowsAffected)
	assert.Empty(t, result.Errors)
	assert.NoError(t, mock.ExpectationsWereMet())
}
