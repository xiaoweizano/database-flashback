package connector

import "context"

// Connector defines the database connectivity interface for the MySQL PITR
// (Point-In-Time Recovery) flashback system.
//
// Implementations are responsible for connecting to a MySQL server, listing
// binary logs, delegating binlog parsing, executing rollback statements, and
// running pre-flight readiness checks.
type Connector interface {
	// Connect establishes a connection to the database using the provided config.
	Connect(cfg ConnConfig) error

	// GetBinlogFiles lists the binary log files currently available on the server.
	GetBinlogFiles(ctx context.Context) ([]BinlogFile, error)

	// ParseBinlog parses the given binlog files and returns row events that match
	// the supplied request (table filter, time range, etc.).
	//
	// The actual parsing work is delegated to the parser package; this method
	// orchestrates reading the raw bytes from the server and handing them off.
	ParseBinlog(ctx context.Context, req ParseRequest) (*ParseResult, error)

	// ExecuteRollback applies the provided SQL statements as a rollback. The
	// behaviour is governed by opts (batch size, dry-run flag).
	ExecuteRollback(ctx context.Context, sqls []string, opts ExecOptions) (*ExecResult, error)

	// Preflight runs a suite of readiness checks against the database and returns
	// a consolidated result. Checks include MySQL version, binlog format, user
	// privileges, disk space, column metadata, and foreign-key dependencies.
	Preflight(ctx context.Context) (*PreflightResult, error)

	// Close closes the underlying database connection and releases resources.
	Close() error
}
