package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

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
	} else {
		connCfg, err := resolveConnConfig(opts)
		if err != nil {
			return fmt.Errorf("flashback: resolve config: %w", err)
		}
		conn = connector.NewMySQLConnector()
		if err := conn.Connect(connCfg); err != nil {
			return fmt.Errorf("flashback: connect: %w", err)
		}
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

	// ---- Parse binlogs ----
	// Delegate to the connector's ParseBinlog, which will time-filter internally.
	parseRes, err := conn.ParseBinlog(ctx, connector.ParseRequest{
		BinlogFiles: binlogNames,
		TargetTable: opts.TargetTable,
		EndTime:     recoveryTime,
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
