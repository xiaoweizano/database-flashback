package main

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/a-shan/mysql-pitr/internal/checkpoint"
	"github.com/a-shan/mysql-pitr/internal/config"
	"github.com/a-shan/mysql-pitr/internal/connector"
	"github.com/a-shan/mysql-pitr/internal/parser"
	"github.com/a-shan/mysql-pitr/internal/rollback"
	"github.com/a-shan/mysql-pitr/internal/ws"
	wsagent "github.com/a-shan/mysql-pitr/internal/ws/agent"
)

// ServeOptions holds parameters for the `serve` daemon subcommand.
type ServeOptions struct {
	ConfigFile string
	Passphrase string
	AgentID    string
}

// serveDaemon holds shared state for the agent daemon command handlers.
type serveDaemon struct {
	cfg     *config.Config
	agentID string
	connCfg connector.ConnConfig

	client  *wsagent.Client
	started time.Time

	// cancels maps operationId to the cancel func of the in-flight handler
	// for that operation (used by pitr_cancel).
	cancelMu sync.Mutex
	cancels  map[string]context.CancelFunc

	rootCtx    context.Context
	rootCancel context.CancelFunc
	stopOnce   sync.Once
	stopCh     chan struct{}
}

func newServeDaemon(cfg *config.Config, agentID string) *serveDaemon {
	ctx, cancel := context.WithCancel(context.Background())
	return &serveDaemon{
		cfg:        cfg,
		agentID:    agentID,
		connCfg:    cfg.MySQL.BuildConnConfig(),
		started:    time.Now(),
		cancels:    make(map[string]context.CancelFunc),
		rootCtx:    ctx,
		rootCancel: cancel,
		stopCh:     make(chan struct{}),
	}
}

// commandResponse helpers

func okResp(cmd ws.Command, result interface{}) *ws.Response {
	return &ws.Response{Cmd: cmd.Cmd, Status: ws.StatusOK, Result: result}
}

func errResp(cmd ws.Command, format string, args ...interface{}) *ws.Response {
	return &ws.Response{Cmd: cmd.Cmd, Status: ws.StatusError, Error: fmt.Sprintf(format, args...)}
}

// paramString / paramUint32 read typed values from a command's params map.
func paramString(params map[string]interface{}, key string) string {
	s, _ := params[key].(string)
	return strings.TrimSpace(s)
}

func paramUint32(params map[string]interface{}, key string) uint32 {
	f, ok := params[key].(float64)
	if !ok || f <= 0 {
		return 0
	}
	return uint32(f)
}

// buildBinlogParseOpts maps command params onto a binlogParseOpts, applying
// the agent config's mysqlbinlog path override. Returns an error for
// malformed time params or a missing target table.
func buildBinlogParseOpts(params map[string]interface{}, cfg *config.Config) (binlogParseOpts, error) {
	opts := binlogParseOpts{
		TargetTable:     paramString(params, "targetTable"),
		MySQLBinlogPath: cfg.MySQLBinlogPath,
	}
	if opts.TargetTable == "" {
		return opts, fmt.Errorf("missing required param 'targetTable'")
	}
	if s := paramString(params, "startTime"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return opts, fmt.Errorf("invalid startTime %q: expected RFC3339", s)
		}
		opts.StartTime = &t
	}
	if s := paramString(params, "endTime"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return opts, fmt.Errorf("invalid endTime %q: expected RFC3339", s)
		}
		opts.EndTime = &t
	}
	opts.StartPos = paramUint32(params, "startPos")
	opts.StopPos = paramUint32(params, "stopPos")
	return opts, nil
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleStatus answers the `status` command with daemon and MySQL connectivity.
func (d *serveDaemon) handleStatus(ctx context.Context, cmd ws.Command) *ws.Response {
	return okResp(cmd, map[string]interface{}{
		"agentId":   d.agentID,
		"uptime":    time.Since(d.started).Round(time.Second).String(),
		"startedAt": d.started.Format(time.RFC3339),
		"mysql":     d.checkMySQL(ctx),
	})
}

func (d *serveDaemon) checkMySQL(ctx context.Context) map[string]interface{} {
	conn := connector.NewMySQLConnector()
	defer conn.Close()
	if err := conn.Connect(d.connCfg); err != nil {
		return map[string]interface{}{"connected": false, "error": err.Error()}
	}
	return map[string]interface{}{
		"connected": true,
		"host":      d.connCfg.Host,
		"port":      d.connCfg.Port,
		"database":  d.connCfg.Database,
	}
}

// handleShutdown stops the daemon gracefully. The response is flushed before
// the connection is closed.
func (d *serveDaemon) handleShutdown(ctx context.Context, cmd ws.Command) *ws.Response {
	d.stopOnce.Do(func() {
		close(d.stopCh)
		d.rootCancel()
	})
	return okResp(cmd, "shutting down")
}

// handlePreflight runs the preflight checks on the agent's local MySQL and
// returns per-check results plus the available binlog files.
func (d *serveDaemon) handlePreflight(ctx context.Context, cmd ws.Command) *ws.Response {
	conn := connector.NewMySQLConnector()
	if err := conn.Connect(d.connCfg); err != nil {
		return errResp(cmd, "connect to MySQL: %v", err)
	}
	defer conn.Close()

	pCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	pfRes, err := conn.Preflight(pCtx)
	if err != nil {
		return errResp(cmd, "preflight: %v", err)
	}

	binlogs, err := conn.GetBinlogFiles(pCtx)
	if err != nil {
		return errResp(cmd, "list binlog files: %v", err)
	}
	names := make([]string, 0, len(binlogs))
	var totalSize int64
	for _, bf := range binlogs {
		names = append(names, bf.Name)
		totalSize += bf.Size
	}

	binlogDir := d.cfg.BinlogDir
	if binlogDir == "" {
		if dir, err := resolveDataDir(d.connCfg); err == nil {
			binlogDir = dir
		}
	}

	return okResp(cmd, map[string]interface{}{
		"preflight":   pfRes,
		"binlogFiles": names,
		"totalSize":   totalSize,
		"binlogDir":   binlogDir,
	})
}

// handlePITRParse parses local binlog files via mysqlbinlog honouring the
// targeted parse parameters supplied by the platform and returns the reverse
// SQL batch (full list plus a preview capped at 1000 entries).
func (d *serveDaemon) handlePITRParse(ctx context.Context, cmd ws.Command) *ws.Response {
	params := cmd.Params
	opID := paramString(params, "operationId")

	opts, err := buildBinlogParseOpts(params, d.cfg)
	if err != nil {
		return errResp(cmd, "%v", err)
	}

	// Resolve the binlog files to parse: the requested subset, or all files.
	var selected []string
	if raw, ok := params["binlogFiles"].([]interface{}); ok && len(raw) > 0 {
		for _, f := range raw {
			if s, ok := f.(string); ok && s != "" {
				selected = append(selected, s)
			}
		}
	}
	if len(selected) == 0 {
		conn := connector.NewMySQLConnector()
		if err := conn.Connect(d.connCfg); err != nil {
			return errResp(cmd, "connect to MySQL: %v", err)
		}
		binlogs, err := conn.GetBinlogFiles(ctx)
		_ = conn.Close()
		if err != nil {
			return errResp(cmd, "list binlog files: %v", err)
		}
		for _, bf := range binlogs {
			selected = append(selected, bf.Name)
		}
	}
	if len(selected) == 0 {
		return errResp(cmd, "no binlog files to parse")
	}

	dataDir := d.cfg.BinlogDir
	if dataDir == "" {
		dir, err := resolveDataDir(d.connCfg)
		if err != nil {
			return errResp(cmd, "resolve binlog directory: %v", err)
		}
		dataDir = dir
	}
	paths := make([]string, 0, len(selected))
	for _, name := range selected {
		paths = append(paths, dataDir+name)
	}

	parseRes, err := parseBinlogWithMySQLBinlog(d.connCfg, paths, opts)
	if err != nil {
		return errResp(cmd, "parse binlogs: %v", err)
	}
	if len(parseRes.Events) == 0 {
		return okResp(cmd, map[string]interface{}{
			"operationId": opID,
			"totalRows":   0,
			"reverseSql":  []string{},
			"preview":     []interface{}{},
			"sqlSample":   "",
		})
	}

	sqls, err := parser.ReverseSQLBatch(parseRes.Events, nil)
	if err != nil {
		return errResp(cmd, "generate reverse SQL: %v", err)
	}

	maxEntries := len(sqls)
	if maxEntries > 1000 {
		maxEntries = 1000
	}
	preview := make([]map[string]interface{}, 0, maxEntries)
	for i := 0; i < maxEntries; i++ {
		ev := parseRes.Events[i]
		preview = append(preview, map[string]interface{}{
			"sequence":     i + 1,
			"sqlType":      string(ev.Type),
			"tableName":    ev.Table,
			"originalSql":  "",
			"reverseSql":   sqls[i],
			"rowsAffected": 1,
		})
	}
	sqlSample := ""
	if len(sqls) > 0 {
		sqlSample = sqls[0]
	}

	return okResp(cmd, map[string]interface{}{
		"operationId": opID,
		"totalRows":   len(parseRes.Events),
		"reverseSql":  sqls,
		"preview":     preview,
		"sqlSample":   sqlSample,
	})
}

// handlePITRExecute runs the reverse SQL batch on the agent's local MySQL
// connection, pushing progress updates and persisting checkpoints as it goes.
// A pitr_cancel command for the same operationId cancels the remaining work.
func (d *serveDaemon) handlePITRExecute(ctx context.Context, cmd ws.Command) *ws.Response {
	params := cmd.Params
	opID := paramString(params, "operationId")
	if opID == "" {
		return errResp(cmd, "missing required param 'operationId'")
	}

	var sqls []string
	if raw, ok := params["sql"].([]interface{}); ok {
		for _, s := range raw {
			if ss, ok := s.(string); ok && ss != "" {
				sqls = append(sqls, ss)
			}
		}
	}
	if len(sqls) == 0 {
		return errResp(cmd, "missing required param 'sql'")
	}

	batchSize := 100
	if f, ok := params["batchSize"].(float64); ok && f > 0 {
		batchSize = int(f)
	}

	// Per-operation cancelable context so pitr_cancel can abort execution
	// between batches.
	opCtx, opCancel := context.WithCancel(d.rootCtx)
	d.cancelMu.Lock()
	d.cancels[opID] = opCancel
	d.cancelMu.Unlock()
	defer func() {
		d.cancelMu.Lock()
		delete(d.cancels, opID)
		d.cancelMu.Unlock()
		opCancel()
	}()

	db, err := sql.Open("mysql", d.cfg.MySQL.BuildDSN())
	if err != nil {
		return errResp(cmd, "open MySQL: %v", err)
	}
	defer db.Close()

	executor := rollback.NewExecutor(db)
	cpManager := checkpoint.NewManager(d.cfg.DataDir)

	estTotal := (len(sqls) + batchSize - 1) / batchSize
	plan := checkpoint.CheckpointPlan{
		RecoveryID:   opID,
		TableName:    paramString(params, "targetTable"),
		TotalBatches: estTotal,
	}
	if s := paramString(params, "recoveryTime"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			plan.RecoveryTime = t
		}
	}
	if _, err := cpManager.CreateCheckpoint(opCtx, plan); err != nil {
		log.Printf("agent: checkpoint create failed for %s: %v", opID, err)
	}

	var rowsAffected int64
	execStart := time.Now()
	result, err := executor.Execute(opCtx, sqls, rollback.ExecOptions{
		BatchSize: batchSize,
		OnBatch: func(b rollback.BatchResult) {
			rowsAffected += b.RowsAffected
			completed := b.BatchNum
			if b.Error == nil {
				completed = b.BatchNum + 1
			}
			_ = cpManager.UpdateBatch(opCtx, opID, completed)
			estimatedRemaining := "calculating..."
			if completed > 0 {
				perBatch := time.Since(execStart) / time.Duration(completed)
				estimatedRemaining = (perBatch * time.Duration(estTotal-completed)).Round(time.Second).String()
			}
			d.pushProgress(map[string]interface{}{
				"operationId":        opID,
				"batchesComplete":    completed,
				"batchesTotal":       estTotal,
				"rowsRestored":       rowsAffected,
				"estimatedRemaining": estimatedRemaining,
			})
		},
	})
	if err != nil && opCtx.Err() == nil {
		return errResp(cmd, "execute rollback: %v", err)
	}

	// Mark the checkpoint complete unless the operation was cancelled.
	if opCtx.Err() == nil {
		if err := cpManager.Complete(opCtx, opID); err != nil {
			log.Printf("agent: checkpoint complete failed for %s: %v", opID, err)
		}
	}

	errors := make([]map[string]interface{}, 0, len(result.Errors))
	for _, e := range result.Errors {
		errors = append(errors, map[string]interface{}{
			"batchNum": e.BatchNum,
			"sql":      e.SQL,
			"error":    e.Error,
		})
	}

	return okResp(cmd, map[string]interface{}{
		"operationId":      opID,
		"cancelled":        opCtx.Err() != nil,
		"rowsAffected":     result.RowsAffected,
		"batchesCompleted": result.BatchesComplete,
		"batchesTotal":     result.BatchesTotal,
		"errors":           errors,
	})
}

// handlePITRCancel cancels the in-flight handler for the given operation.
func (d *serveDaemon) handlePITRCancel(ctx context.Context, cmd ws.Command) *ws.Response {
	opID := paramString(cmd.Params, "operationId")
	d.cancelMu.Lock()
	cancel, ok := d.cancels[opID]
	d.cancelMu.Unlock()
	if ok {
		cancel()
	}
	return okResp(cmd, map[string]interface{}{
		"operationId": opID,
		"cancelled":   ok,
	})
}

// pushProgress sends a best-effort pitr_progress notification to the platform.
func (d *serveDaemon) pushProgress(params map[string]interface{}) {
	if d.client == nil {
		return
	}
	_ = d.client.Send(ws.Command{
		Cmd:    fmt.Sprintf("progress-%s", params["operationId"]),
		Type:   ws.CmdPITRProgress,
		Params: params,
	})
}

// certCNFromFile extracts the CommonName from the first certificate in a PEM
// file. Used to derive the agent ID from the client certificate.
func certCNFromFile(certFile string) (string, error) {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return "", fmt.Errorf("read client certificate: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", fmt.Errorf("no PEM block in %s", certFile)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse client certificate: %w", err)
	}
	if cert.Subject.CommonName == "" {
		return "", fmt.Errorf("client certificate has empty CommonName")
	}
	return cert.Subject.CommonName, nil
}

// NewServeCommand creates the `agent serve` cobra command that runs the agent
// as a persistent daemon connected to the platform hub.
func NewServeCommand() *cobra.Command {
	opts := ServeOptions{}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the agent as a persistent daemon connected to the platform",
		Long: `Run the agent as a persistent daemon that maintains a long-lived mTLS
WebSocket connection to the mysql-pitr-server and serves binlog parse,
preflight, and rollback-execution commands for the platform.

The daemon reads the local binlog directory on this host and parses binlog
files with mysqlbinlog. Targeted parsing parameters (binlog files, target
table, time range, position range) are supplied by the platform with each
pitr_parse command.

The agent ID is taken from the --agent-id flag, or derived from the
CommonName of the mTLS client certificate when the flag is omitted.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.ConfigFile == "" {
				return fmt.Errorf("--config is required")
			}
			if opts.Passphrase == "" {
				return fmt.Errorf("--passphrase is required")
			}

			cfg, err := config.LoadConfig(opts.ConfigFile, opts.Passphrase)
			if err != nil {
				return fmt.Errorf("serve: load config: %w", err)
			}
			if cfg.Server.URL == "" {
				return fmt.Errorf("serve: config field server.url is required")
			}
			if cfg.Server.CertFile == "" || cfg.Server.KeyFile == "" || cfg.Server.CAFile == "" {
				return fmt.Errorf("serve: config fields server.cert_file, server.key_file, and server.ca_file are required")
			}

			agentID := opts.AgentID
			if agentID == "" {
				cn, err := certCNFromFile(cfg.Server.CertFile)
				if err != nil {
					return fmt.Errorf("serve: derive agent id from client certificate: %w (pass --agent-id explicitly)", err)
				}
				agentID = cn
			}

			d := newServeDaemon(cfg, agentID)

			client := wsagent.NewClient(wsagent.ClientConfig{
				ServerURL: cfg.Server.URL,
				CertFile:  cfg.Server.CertFile,
				KeyFile:   cfg.Server.KeyFile,
				CAPath:    cfg.Server.CAFile,
				AgentID:   agentID,
			})
			d.client = client

			dispatcher := wsagent.NewDispatcher()
			dispatcher.RegisterHandler(ws.CmdStatus, d.handleStatus)
			dispatcher.RegisterHandler(ws.CmdShutdown, d.handleShutdown)
			dispatcher.RegisterHandler(ws.CmdPreflight, d.handlePreflight)
			dispatcher.RegisterHandler(ws.CmdPITRParse, d.handlePITRParse)
			dispatcher.RegisterHandler(ws.CmdPITRExecute, d.handlePITRExecute)
			dispatcher.RegisterHandler(ws.CmdPITRCancel, d.handlePITRCancel)
			client.SetDispatcher(dispatcher)

			log.Printf("agent %s starting (platform %s, mysql %s:%d)", agentID, cfg.Server.URL, d.connCfg.Host, d.connCfg.Port)
			if err := client.Connect(cmd.Context()); err != nil {
				return fmt.Errorf("serve: connect to platform: %w", err)
			}

			// Certificates are renewed automatically when nearing expiry.
			if tlsCfg := client.TLSConfig(); tlsCfg != nil {
				wsagent.StartAutoRenew(d.rootCtx, client, tlsCfg, agentID)
			}

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

			select {
			case <-sigCh:
				log.Printf("agent %s received interrupt, shutting down", agentID)
			case <-d.stopCh:
				log.Printf("agent %s shutdown requested by platform", agentID)
			}

			// Give the shutdown response time to flush before closing.
			time.Sleep(500 * time.Millisecond)
			_ = client.Close()
			return nil
		},
		SilenceUsage: true,
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.ConfigFile, "config", "", "Encrypted config file path (required)")
	flags.StringVar(&opts.Passphrase, "passphrase", "", "Passphrase for config decryption (required)")
	flags.StringVar(&opts.AgentID, "agent-id", "", "Agent identifier (default: from client certificate CommonName)")

	return cmd
}
