package rollback

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/a-shan/mysql-pitr/internal/connector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newMockPreflightRunner(t *testing.T, recTime time.Time) (*PreflightRunner, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewPreflightRunner(db, recTime), mock
}

// ---------------------------------------------------------------------------
// Run – all pass
// ---------------------------------------------------------------------------

func TestPreflightRun_AllPass(t *testing.T) {
	r, mock := newMockPreflightRunner(t, time.Time{})

	// 1. Version.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("8.0.32"))

	// 2. binlog_format.
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_format'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_format", "ROW"))

	// 3. binlog_row_image.
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_row_image'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_row_image", "FULL"))

	// 4. SHOW BINARY LOGS.
	mock.ExpectQuery(regexp.QuoteMeta("SHOW BINARY LOGS")).
		WillReturnRows(sqlmock.NewRows([]string{"Log_name", "File_size"}).
			AddRow("mysql-bin.000001", int64(1024)).
			AddRow("mysql-bin.000002", int64(2048)))

	// 4b. binlog_expire_logs_seconds.
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_expire_logs_seconds'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_expire_logs_seconds", "604800"))

	// 5. Permissions.
	mock.ExpectQuery(regexp.QuoteMeta("SHOW GRANTS")).
		WillReturnRows(sqlmock.NewRows([]string{"Grants for user"}).
			AddRow("GRANT SELECT, REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'u'@'%'"))

	// 6. Disk space.
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'datadir'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("datadir", "/var/lib/mysql"))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT SUM(data_length + index_length) FROM information_schema.tables")).
		WillReturnRows(sqlmock.NewRows([]string{"SUM(data_length + index_length)"}).AddRow(int64(1048576)))

	result, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, connector.PreflightPass, result.Status)
	assert.Equal(t, "8.0.32", result.Version)
	assert.Len(t, result.Checks, 6)

	// Version check.
	assert.Equal(t, connector.PreflightPass, result.Checks[0].Status)
	// Binlog format.
	assert.Equal(t, connector.PreflightPass, result.Checks[1].Status)
	// Row image.
	assert.Equal(t, connector.PreflightPass, result.Checks[2].Status)
	// Binlog availability.
	assert.Equal(t, connector.PreflightPass, result.Checks[3].Status)
	assert.Contains(t, result.Checks[3].Message, "2 binary logs")
	// Permissions.
	assert.Equal(t, connector.PreflightPass, result.Checks[4].Status)
	// Disk space.
	assert.Equal(t, connector.PreflightPass, result.Checks[5].Status)
	assert.Contains(t, result.Checks[5].Message, "/var/lib/mysql")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Version check
// ---------------------------------------------------------------------------

func TestPreflightRun_VersionFail(t *testing.T) {
	r, mock := newMockPreflightRunner(t, time.Time{})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("5.6.40"))

	result, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, connector.PreflightFail, result.Status)
	assert.Equal(t, connector.PreflightFail, result.Checks[0].Status)
	assert.Contains(t, result.Checks[0].Message, "5.6")
	assert.Contains(t, result.Checks[0].Message, "5.7+")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPreflightRun_VersionQueryError(t *testing.T) {
	r, mock := newMockPreflightRunner(t, time.Time{})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnError(sql.ErrConnDone)

	result, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, connector.PreflightFail, result.Status)
	assert.Equal(t, connector.PreflightFail, result.Checks[0].Status)
	assert.Contains(t, result.Checks[0].Message, "cannot query version")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// binlog_format
// ---------------------------------------------------------------------------

func TestPreflightRun_BinlogFormatNotRow(t *testing.T) {
	r, mock := newMockPreflightRunner(t, time.Time{})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("8.0.32"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_format'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_format", "STATEMENT"))

	result, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, connector.PreflightFail, result.Status)
	assert.Equal(t, connector.PreflightFail, result.Checks[1].Status)
	assert.Contains(t, result.Checks[1].Message, "STATEMENT")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// binlog_row_image
// ---------------------------------------------------------------------------

func TestPreflightRun_BinlogRowImageWarn(t *testing.T) {
	r, mock := newMockPreflightRunner(t, time.Time{})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("8.0.32"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_format'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_format", "ROW"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_row_image'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_row_image", "MINIMAL"))

	// Remaining preflight checks: binlog availability, permissions, disk space.
	mock.ExpectQuery(regexp.QuoteMeta("SHOW BINARY LOGS")).
		WillReturnRows(sqlmock.NewRows([]string{"Log_name", "File_size"}).
			AddRow("mysql-bin.000001", int64(1024)))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_expire_logs_seconds'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_expire_logs_seconds", "604800"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW GRANTS")).
		WillReturnRows(sqlmock.NewRows([]string{"Grants for user"}).
			AddRow("GRANT SELECT, REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'u'@'%'"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'datadir'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("datadir", "/var/lib/mysql"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT SUM(data_length + index_length) FROM information_schema.tables")).
		WillReturnRows(sqlmock.NewRows([]string{"SUM(data_length + index_length)"}).AddRow(int64(1048576)))

	result, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, connector.PreflightPass, result.Status) // warn should not cause FAIL
	assert.Equal(t, connector.PreflightWarn, result.Checks[2].Status)
	assert.Contains(t, result.Checks[2].Message, "MINIMAL")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Binlog availability
// ---------------------------------------------------------------------------

func TestPreflightRun_NoBinlogs(t *testing.T) {
	r, mock := newMockPreflightRunner(t, time.Time{})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("8.0.32"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_format'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_format", "ROW"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_row_image'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_row_image", "FULL"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW BINARY LOGS")).
		WillReturnRows(sqlmock.NewRows([]string{"Log_name", "File_size"}))

	result, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, connector.PreflightFail, result.Status)
	assert.Equal(t, connector.PreflightFail, result.Checks[3].Status)
	assert.Contains(t, result.Checks[3].Message, "no binary logs")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPreflightRun_BinlogRetentionExpired(t *testing.T) {
	// Recovery time is 30 days ago; binlogs expire after 7 days.
	recTime := time.Now().Add(-30 * 24 * time.Hour)
	r, mock := newMockPreflightRunner(t, recTime)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("8.0.32"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_format'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_format", "ROW"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_row_image'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_row_image", "FULL"))

	// 2 binlog files, but retention is only 7 days and recovery is 30 days ago.
	mock.ExpectQuery(regexp.QuoteMeta("SHOW BINARY LOGS")).
		WillReturnRows(sqlmock.NewRows([]string{"Log_name", "File_size"}).
			AddRow("mysql-bin.000001", int64(1024)).
			AddRow("mysql-bin.000002", int64(2048)))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_expire_logs_seconds'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_expire_logs_seconds", "604800")) // 7 days

	result, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, connector.PreflightFail, result.Status)
	assert.Equal(t, connector.PreflightFail, result.Checks[3].Status)
	assert.Contains(t, result.Checks[3].Message, "recovery time")
	assert.Contains(t, result.Checks[3].Message, "expire")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPreflightRun_BinlogExpireLogsDaysFallback(t *testing.T) {
	r, mock := newMockPreflightRunner(t, time.Time{})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("8.0.32"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_format'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_format", "ROW"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_row_image'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_row_image", "FULL"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW BINARY LOGS")).
		WillReturnRows(sqlmock.NewRows([]string{"Log_name", "File_size"}).
			AddRow("mysql-bin.000001", int64(1024)))
	// binlog_expire_logs_seconds not available (MySQL < 8.0).
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_expire_logs_seconds'")).
		WillReturnError(sql.ErrConnDone)
	// Fallback to expire_logs_days.
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'expire_logs_days'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("expire_logs_days", "7"))

	mock.ExpectQuery(regexp.QuoteMeta("SHOW GRANTS")).
		WillReturnRows(sqlmock.NewRows([]string{"Grants for user"}).
			AddRow("GRANT SELECT, REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'u'@'%'"))

	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'datadir'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("datadir", "/var/lib/mysql"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT SUM(data_length + index_length) FROM information_schema.tables")).
		WillReturnRows(sqlmock.NewRows([]string{"SUM(data_length + index_length)"}).AddRow(int64(1048576)))

	result, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, connector.PreflightPass, result.Status)
	assert.Contains(t, result.Checks[3].Message, "retention")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Permissions
// ---------------------------------------------------------------------------

func TestPreflightRun_PermissionsMissing(t *testing.T) {
	r, mock := newMockPreflightRunner(t, time.Time{})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("8.0.32"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_format'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_format", "ROW"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_row_image'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_row_image", "FULL"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW BINARY LOGS")).
		WillReturnRows(sqlmock.NewRows([]string{"Log_name", "File_size"}).
			AddRow("mysql-bin.000001", int64(1024)))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_expire_logs_seconds'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_expire_logs_seconds", "604800"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW GRANTS")).
		WillReturnRows(sqlmock.NewRows([]string{"Grants for user"}).
			AddRow("GRANT INSERT ON *.* TO 'u'@'%'"))

	result, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, connector.PreflightFail, result.Status)
	assert.Equal(t, connector.PreflightFail, result.Checks[4].Status)
	assert.Contains(t, result.Checks[4].Message, "missing")
	assert.Contains(t, result.Checks[4].Message, "SELECT")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPreflightRun_PermissionsAllPrivileges(t *testing.T) {
	r, mock := newMockPreflightRunner(t, time.Time{})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("8.0.32"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_format'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_format", "ROW"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_row_image'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_row_image", "FULL"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW BINARY LOGS")).
		WillReturnRows(sqlmock.NewRows([]string{"Log_name", "File_size"}).
			AddRow("mysql-bin.000001", int64(1024)))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_expire_logs_seconds'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_expire_logs_seconds", "604800"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW GRANTS")).
		WillReturnRows(sqlmock.NewRows([]string{"Grants for user"}).
			AddRow("GRANT ALL PRIVILEGES ON *.* TO 'u'@'%'"))

	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'datadir'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("datadir", "/var/lib/mysql"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT SUM(data_length + index_length) FROM information_schema.tables")).
		WillReturnRows(sqlmock.NewRows([]string{"SUM(data_length + index_length)"}).AddRow(int64(1048576)))

	result, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, connector.PreflightPass, result.Status)
	assert.Equal(t, connector.PreflightPass, result.Checks[4].Status)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Disk space
// ---------------------------------------------------------------------------

func TestPreflightRun_DiskSpaceNoData(t *testing.T) {
	r, mock := newMockPreflightRunner(t, time.Time{})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT VERSION()")).
		WillReturnRows(sqlmock.NewRows([]string{"VERSION()"}).AddRow("8.0.32"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_format'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_format", "ROW"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_row_image'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_row_image", "FULL"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW BINARY LOGS")).
		WillReturnRows(sqlmock.NewRows([]string{"Log_name", "File_size"}).
			AddRow("mysql-bin.000001", int64(1024)))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'binlog_expire_logs_seconds'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("binlog_expire_logs_seconds", "604800"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW GRANTS")).
		WillReturnRows(sqlmock.NewRows([]string{"Grants for user"}).
			AddRow("GRANT ALL ON *.* TO 'u'@'%'"))
	mock.ExpectQuery(regexp.QuoteMeta("SHOW VARIABLES LIKE 'datadir'")).
		WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("datadir", "/var/lib/mysql"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT SUM(data_length + index_length) FROM information_schema.tables")).
		WillReturnRows(sqlmock.NewRows([]string{"SUM(data_length + index_length)"}).AddRow(nil))

	result, err := r.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, connector.PreflightPass, result.Status)
	assert.Equal(t, connector.PreflightPass, result.Checks[5].Status)
	assert.Contains(t, result.Checks[5].Message, "/var/lib/mysql")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func TestParseVersion_MajorOnly(t *testing.T) {
	maj, min, patch := parseVersion("5.7.42-log")
	assert.Equal(t, 5, maj)
	assert.Equal(t, 7, min)
	assert.Equal(t, 42, patch)
}

func TestParseVersion_Invalid(t *testing.T) {
	maj, min, patch := parseVersion("not-a-version")
	assert.Equal(t, 0, maj)
	assert.Equal(t, 0, min)
	assert.Equal(t, 0, patch)
}

func TestFormatBytes(t *testing.T) {
	assert.Equal(t, "500 B", formatBytes(500))
	assert.Equal(t, "1.0 KB", formatBytes(1024))
	assert.Equal(t, "1.0 MB", formatBytes(1024*1024))
	assert.Equal(t, "1.0 GB", formatBytes(1024*1024*1024))
}

func TestFormatDuration(t *testing.T) {
	assert.Equal(t, "7d", formatDuration(7*24*time.Hour))
	assert.Equal(t, "1d", formatDuration(24*time.Hour))
	assert.Equal(t, "5s", formatDuration(5*time.Second))
}
