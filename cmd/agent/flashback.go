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
	dataDir, err := resolveDataDir(connCfg)
	if err != nil {
		return fmt.Errorf("flashback: resolve binlog directory: %w", err)
	}
	log.Printf("MySQL data directory: %s", dataDir)

	paths := make([]string, 0, len(binlogNames))
	for _, name := range binlogNames {
		paths = append(paths, dataDir+name)
	}

	// ---- Parse binlogs via mysqlbinlog ----
	parseRes, err := parseBinlogWithMySQLBinlog(paths, opts.TargetTable, recoveryTime)
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

// parseBinlogWithMySQLBinlog uses the mysqlbinlog tool to parse binlog files
// and generate reverse SQL statements.
func parseBinlogWithMySQLBinlog(paths []string, targetTable string, recoveryTime time.Time) (*connector.ParseResult, error) {
	parts := strings.SplitN(targetTable, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid target table %q: expected schema.table format", targetTable)
	}

	// Build mysqlbinlog args.
	recoveryStr := recoveryTime.Format("2006-01-02 15:04:05")
	args := []string{
		"--no-defaults",
		"--base64-output=DECODE-ROWS",
		"--verbose",
		"--stop-datetime=" + recoveryStr,
	}
	args = append(args, paths...)

	cmd := exec.Command("mysqlbinlog", args...)
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

		// End of row block — flush.
		if inRow && !strings.HasPrefix(line, "###") {
			events = append(events, makeRowEvent(evType, parts[0], parts[1], values))
			values = nil
			inRow = false
		}
	}

	if inRow && len(values) > 0 {
		events = append(events, makeRowEvent(evType, parts[0], parts[1], values))
	}
	cmd.Wait()

	log.Printf("mysqlbinlog parsed %d row event(s) for %s", len(events), targetTable)
	return &connector.ParseResult{Events: events, TotalRows: int64(len(events))}, nil
}

// makeRowEvent constructs a RowEvent from parsed column values.
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
