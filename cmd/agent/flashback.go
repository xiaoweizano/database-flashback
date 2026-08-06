package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/spf13/cobra"

	"github.com/a-shan/mysql-pitr/internal/config"
	"github.com/a-shan/mysql-pitr/internal/connector"
	"github.com/a-shan/mysql-pitr/internal/parser"
)

// FlashbackOptions holds all input parameters for a flashback operation.
type FlashbackOptions struct {
	// Connector is an optional pre-configured connector. When nil, one is created
	// from DSN (or config file). Inject a mock for testing.
	Connector connector.Connector

	// CLI flags
	DSN          string
	TargetTable  string
	RecoveryTime string
	OutputFile   string
	DryRun       bool
	BatchSize    int
	ConfigFile   string
	Passphrase   string
}

// RunFlashback executes the flashback workflow:
//  1. Connect to MySQL (from DSN or encrypted config)
//  2. Run preflight checks
//  3. List available binlog files
//  4. Parse binlog files within the recovery time range
//  5. Generate reverse SQL
//  6. If --dry-run: print to stdout
//  7. If --output: write to file
//  8. If neither flag: execute against MySQL
func RunFlashback(ctx context.Context, opts FlashbackOptions) error {
	// ---- Parse recovery time ----
	recoveryTime, err := time.Parse(time.RFC3339, opts.RecoveryTime)
	if err != nil {
		return fmt.Errorf("flashback: parse recovery-time %q: %w", opts.RecoveryTime, err)
	}

	// ---- Connect ----
	conn := opts.Connector
	if conn != nil {
		defer conn.Close()
	}
	var connCfg connector.ConnConfig
	if conn == nil {
		var err error
		connCfg, err = resolveConnConfig(opts)
		if err != nil {
			return fmt.Errorf("flashback: resolve config: %w", err)
		}
		conn = connector.NewMySQLConnector()
		if err := conn.Connect(connCfg); err != nil {
			return fmt.Errorf("flashback: connect: %w", err)
		}
		defer conn.Close()
		log.Printf("connected to MySQL at %s:%d (database: %s)", connCfg.Host, connCfg.Port, connCfg.Database)
	}

	// ---- Preflight ----
	preflightRes, err := conn.Preflight(ctx)
	if err != nil {
		return fmt.Errorf("flashback: preflight: %w", err)
	}
	if preflightRes.Status == connector.PreflightFail {
		return fmt.Errorf("flashback: preflight FAILED — aborting")
	}
	log.Printf("preflight: %s (MySQL %s)", preflightRes.Status, preflightRes.Version)
	for _, c := range preflightRes.Checks {
		if c.Message != "" {
			log.Printf("  [%s] %s: %s", c.Status, c.Name, c.Message)
		}
	}

	// ---- List binlog files ----
	binlogs, err := conn.GetBinlogFiles(ctx)
	if err != nil {
		return fmt.Errorf("flashback: list binlogs: %w", err)
	}
	if len(binlogs) == 0 {
		return fmt.Errorf("flashback: no binlog files found on server")
	}
	log.Printf("found %d binlog file(s)", len(binlogs))

	binlogNames := make([]string, len(binlogs))
	for i, bf := range binlogs {
		binlogNames[i] = bf.Name
	}

	// Resolve binlog directory and build file paths.
	dataDir, err := conn.GetBinlogDir(ctx)
	if err != nil || dataDir == "" {
		// Fall back to a fresh discovery connection (e.g. when the injected
		// connector cannot resolve the directory).
		dataDir, err = resolveDataDir(connCfg)
		if err != nil {
			return fmt.Errorf("flashback: resolve binlog directory: %w", err)
		}
	}
	log.Printf("MySQL data directory: %s", dataDir)

	paths := binlogPaths(dataDir, binlogNames)

	// ---- Parse binlogs via mysqlbinlog ----
	parseRes, err := mysqlbinlogParse(connCfg, paths, binlogParseOpts{
		TargetTable: opts.TargetTable,
		EndTime:     &recoveryTime,
	})
	if err != nil {
		return fmt.Errorf("flashback: parse binlogs: %w", err)
	}
	if len(parseRes.Events) == 0 {
		return fmt.Errorf("flashback: no row events found for table %q before %s", opts.TargetTable, opts.RecoveryTime)
	}
	log.Printf("found %d row event(s) for table %q", len(parseRes.Events), opts.TargetTable)

	// ---- Generate reverse SQL ----
	sqls, err := parser.ReverseSQLBatch(parseRes.Events, nil)
	if err != nil {
		return fmt.Errorf("flashback: generate reverse SQL: %w", err)
	}
	log.Printf("generated %d reverse SQL statement(s)", len(sqls))

	// ---- Resolve output mode ----
	switch {
	case opts.DryRun:
		// Print to stdout with log prefix
		for _, s := range sqls {
			log.Printf("[flashback-dry-run] %s", s)
		}
		log.Printf("dry-run complete: %d statement(s) would be executed", len(sqls))

	case opts.OutputFile != "":
		// Write to file
		if err := writeSQLToFile(opts.OutputFile, sqls); err != nil {
			return fmt.Errorf("flashback: write output: %w", err)
		}
		log.Printf("wrote %d statement(s) to %s", len(sqls), opts.OutputFile)

	default:
		// Execute against MySQL
		execRes, err := conn.ExecuteRollback(ctx, sqls, connector.ExecOptions{
			BatchSize: opts.BatchSize,
			DryRun:    false,
		})
		if err != nil {
			return fmt.Errorf("flashback: execute rollback: %w", err)
		}
		log.Printf("rollback execution complete: %d batches, %d rows affected",
			execRes.BatchesCompleted, execRes.RowsAffected)
		if len(execRes.Errors) > 0 {
			log.Printf("WARNING: %d batch error(s) occurred", len(execRes.Errors))
			for _, e := range execRes.Errors {
				log.Printf("  error: %s", e)
			}
		}
	}

	return nil
}

// resolveConnConfig determines the connection configuration from either the
// --config file, the --mysql-dsn flag, or the built-in config.
func resolveConnConfig(opts FlashbackOptions) (connector.ConnConfig, error) {
	// If --config is provided, load from encrypted config file.
	if opts.ConfigFile != "" {
		cfg, err := config.LoadConfig(opts.ConfigFile, opts.Passphrase)
		if err != nil {
			return connector.ConnConfig{}, fmt.Errorf("load config: %w", err)
		}
		return cfg.MySQL.BuildConnConfig(), nil
	}

	// If --mysql-dsn is provided, parse it.
	if opts.DSN != "" {
		return config.ParseDSNToConnConfig(opts.DSN)
	}

	return connector.ConnConfig{}, fmt.Errorf("either --mysql-dsn or --config must be provided")
}

// resolveDataDir queries MySQL for the data directory path.
func resolveDataDir(cfg connector.ConnConfig) (string, error) {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 3306
	}

	mysqlCfg := mysql.NewConfig()
	mysqlCfg.User = cfg.User
	mysqlCfg.Passwd = cfg.Password
	mysqlCfg.Net = "tcp"
	mysqlCfg.Addr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	mysqlCfg.ParseTime = true

	db, err := sql.Open("mysql", mysqlCfg.FormatDSN())
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var name, value string
	if err := db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'log_bin_basename'").Scan(&name, &value); err != nil {
		return "", fmt.Errorf("query log_bin_basename: %w", err)
	}
	if value == "" {
		return "", fmt.Errorf("log_bin_basename is empty — binary logging may not be enabled")
	}
	// Extract directory from the full path (e.g. "/var/log/mysql/mysql-bin" -> "/var/log/mysql/").
	dir := filepath.Dir(value)
	if dir != "." {
		value = dir + string(filepath.Separator)
	} else {
		value = ""
	}
	return value, nil
}

// binlogPaths joins a binlog directory with file names. The directory may or
// may not end with a path separator (the agent config's binlog_dir field is
// written without one, e.g. "/var/lib/mysql"); filepath.Join handles both.
func binlogPaths(dataDir string, names []string) []string {
	paths := make([]string, 0, len(names))
	for _, n := range names {
		paths = append(paths, filepath.Join(dataDir, n))
	}
	return paths
}

// mysqlbinlogParse is the parse entry point used by RunFlashback. It is a
// package-level variable so tests can substitute a fake.
var mysqlbinlogParse = parseBinlogWithMySQLBinlog

// binlogParseOpts controls how a binlog parse is constrained. All fields are
// optional; a nil/zero field means "no constraint".
type binlogParseOpts struct {
	TargetTable     string
	StartTime       *time.Time
	EndTime         *time.Time
	StartPos        uint32
	StopPos         uint32
	MySQLBinlogPath string
}

// mysqlbinlogDateTime formats a UTC instant for mysqlbinlog's
// --start-datetime/--stop-datetime arguments. mysqlbinlog interprets these
// strings in the LOCAL timezone of the machine it runs on (converting them to
// UTC for comparison with event timestamps), so the instant must be expressed
// in the local zone first — otherwise the effective stop time drifts by the
// local UTC offset.
func mysqlbinlogDateTime(t time.Time) string {
	return t.In(time.Local).Format("2006-01-02 15:04:05")
}

// parseBinlogWithMySQLBinlog uses the mysqlbinlog tool to parse binlog files
// and generate reverse SQL statements.
func parseBinlogWithMySQLBinlog(cfg connector.ConnConfig, paths []string, opts binlogParseOpts) (*connector.ParseResult, error) {
	parts := strings.SplitN(opts.TargetTable, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid target table %q: expected schema.table format", opts.TargetTable)
	}

	// Query real column names from MySQL.
	colNames, err := queryColumnNames(cfg, parts[0], parts[1])
	if err != nil {
		log.Printf("WARNING: could not query column names: %v, using positional names", err)
	}

	// Build mysqlbinlog args.
	args := []string{
		"--no-defaults",
		"--base64-output=DECODE-ROWS",
		"--verbose",
	}
	if opts.StartTime != nil {
		args = append(args, "--start-datetime="+mysqlbinlogDateTime(*opts.StartTime))
	}
	if opts.EndTime != nil {
		args = append(args, "--stop-datetime="+mysqlbinlogDateTime(*opts.EndTime))
	}
	if opts.StartPos > 0 {
		args = append(args, fmt.Sprintf("--start-position=%d", opts.StartPos))
	}
	if opts.StopPos > 0 {
		args = append(args, fmt.Sprintf("--stop-position=%d", opts.StopPos))
	}
	args = append(args, paths...)

	binlogBinary := opts.MySQLBinlogPath
	if binlogBinary == "" {
		binlogBinary = "mysqlbinlog"
	}
	cmd := exec.Command(binlogBinary, args...)
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
				log.Printf("mysqlbinlog: %s", t)
			}
		}
	}()

	tablePattern := "`" + parts[0] + "`.`" + parts[1] + "`"
	var events []connector.RowEvent
	scanner := bufio.NewScanner(stdout)
	var evType connector.EventType
	var values []interface{}
	inRow := false

	for scanner.Scan() {
		line := scanner.Text()

		if strings.Contains(line, "### DELETE FROM "+tablePattern) {
			if inRow {
				events = append(events, makeRowEvent(evType, parts[0], parts[1], values, colNames))
			}
			evType = connector.DeleteEvent
			values = nil
			inRow = true
			continue
		}
		if strings.Contains(line, "### INSERT INTO "+tablePattern) {
			if inRow {
				events = append(events, makeRowEvent(evType, parts[0], parts[1], values, colNames))
			}
			evType = connector.InsertEvent
			values = nil
			inRow = true
			continue
		}
		if strings.Contains(line, "### UPDATE "+tablePattern) {
			if inRow {
				events = append(events, makeRowEvent(evType, parts[0], parts[1], values, colNames))
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

		// End of row block — flush.
		if inRow && !strings.HasPrefix(line, "###") {
			events = append(events, makeRowEvent(evType, parts[0], parts[1], values, colNames))
			values = nil
			inRow = false
		}
	}

	if inRow && len(values) > 0 {
		events = append(events, makeRowEvent(evType, parts[0], parts[1], values, colNames))
	}
	waitErr := cmd.Wait()
	if waitErr != nil && len(events) == 0 {
		// A failed mysqlbinlog run (e.g. missing/unreadable binlog file)
		// silently yields zero events; surface it instead of reporting
		// "no row events found".
		return nil, fmt.Errorf("mysqlbinlog failed: %w", waitErr)
	}
	if waitErr != nil {
		log.Printf("mysqlbinlog exited with error after parsing %d event(s): %v", len(events), waitErr)
	}

	log.Printf("mysqlbinlog parsed %d row event(s) for %s", len(events), opts.TargetTable)
	return &connector.ParseResult{Events: events, TotalRows: int64(len(events))}, nil
}

// makeRowEvent constructs a RowEvent from parsed column values.
func makeRowEvent(typ connector.EventType, db, table string, vals []interface{}, colNames []string) connector.RowEvent {
	name := func(i int) string {
		if i < len(colNames) && colNames[i] != "" {
			return colNames[i]
		}
		return fmt.Sprintf("col_%d", i)
	}

	ev := connector.RowEvent{Type: typ, Database: db, Table: table}
	switch typ {
	case connector.DeleteEvent:
		m := make(map[string]interface{}, len(vals))
		for i, v := range vals {
			m[name(i)] = v
		}
		ev.Before = m
	case connector.InsertEvent:
		m := make(map[string]interface{}, len(vals))
		for i, v := range vals {
			m[name(i)] = v
		}
		ev.After = m
	case connector.UpdateEvent:
		if len(vals)%2 == 0 {
			half := len(vals) / 2
			before := make(map[string]interface{}, half)
			after := make(map[string]interface{}, half)
			for i := 0; i < half; i++ {
				before[name(i)] = vals[i]
				after[name(i)] = vals[half+i]
			}
			ev.Before = before
			ev.After = after
		}
	}
	return ev
}

// queryColumnNames retrieves column names from MySQL information_schema.
func queryColumnNames(cfg connector.ConnConfig, dbName, tblName string) ([]string, error) {
	mysqlCfg := mysql.NewConfig()
	mysqlCfg.User = cfg.User
	mysqlCfg.Passwd = cfg.Password
	mysqlCfg.Net = "tcp"
	mysqlCfg.Addr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	mysqlCfg.ParseTime = true

	db, err := sql.Open("mysql", mysqlCfg.FormatDSN())
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx,
		"SELECT COLUMN_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION",
		dbName, tblName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// parseColumnValue parses a column value from mysqlbinlog output.
func parseColumnValue(s string) interface{} {
	s = strings.TrimSpace(s)
	if s == "NULL" || s == "'NULL'" {
		return nil
	}
	// Integer
	if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") {
		return strings.Trim(s, "'")
	}
	// Try int first
	var i int64
	if _, err := fmt.Sscanf(s, "%d", &i); err == nil {
		// Check if it was actually a float
		if strings.Contains(s, ".") {
			var f float64
			fmt.Sscanf(s, "%f", &f)
			return f
		}
		return i
	}
	// Float
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err == nil {
		return f
	}
	return s
}

// writeSQLToFile writes the generated SQL statements to the given file path.
func writeSQLToFile(path string, sqls []string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	for _, s := range sqls {
		if _, err := fmt.Fprintln(f, s); err != nil {
			return fmt.Errorf("write: %w", err)
		}
	}

	return f.Close()
}

// NewFlashbackCommand creates the `agent flashback` cobra command.
func NewFlashbackCommand() *cobra.Command {
	opts := FlashbackOptions{}

	cmd := &cobra.Command{
		Use:   "flashback",
		Short: "Perform point-in-time recovery via binlog flashback",
		Long: `Perform point-in-time recovery by parsing MySQL binary logs and
generating reverse SQL statements to undo changes made before a
specified recovery time.

The workflow:
  1. Connects to MySQL (via DSN or encrypted config file)
  2. Runs preflight readiness checks
  3. Lists available binary log files
  4. Parses binlog events for the target table before the recovery time
  5. Generates reverse SQL (INSERT→DELETE, DELETE→INSERT, UPDATE→SET old values)
  6. Either dry-runs, writes to a file, or executes against MySQL
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate required flags.
			if opts.ConfigFile == "" && opts.DSN == "" {
				return fmt.Errorf("either --mysql-dsn or --config is required")
			}
			if opts.RecoveryTime == "" {
				return fmt.Errorf("--recovery-time is required")
			}
			if opts.TargetTable == "" {
				return fmt.Errorf("--target-table is required")
			}
			if opts.ConfigFile != "" && opts.Passphrase == "" {
				return fmt.Errorf("--passphrase is required when using --config")
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			return RunFlashback(ctx, opts)
		},
		SilenceUsage: true,
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.DSN, "mysql-dsn", "", "MySQL DSN (e.g. user:pass@tcp(host:3306)/db)")
	flags.StringVar(&opts.TargetTable, "target-table", "", "Target table in schema.table format (required)")
	flags.StringVar(&opts.RecoveryTime, "recovery-time", "", "ISO8601 timestamp for recovery point (required)")
	flags.StringVar(&opts.OutputFile, "output", "", "File path for reverse SQL output")
	flags.BoolVar(&opts.DryRun, "dry-run", false, "Print reverse SQL without executing")
	flags.IntVar(&opts.BatchSize, "batch-size", 1000, "Batch size for execution")
	flags.StringVar(&opts.ConfigFile, "config", "", "Encrypted config file path")
	flags.StringVar(&opts.Passphrase, "passphrase", "", "Passphrase for config decryption")

	return cmd
}
