package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-shan/mysql-pitr/internal/config"
	"github.com/a-shan/mysql-pitr/internal/connector"
)

// ---------------------------------------------------------------------------
// Mock connector
// ---------------------------------------------------------------------------

type mockConnector struct {
	connectErr      error
	preflightResult *connector.PreflightResult
	preflightErr    error
	binlogFiles     []connector.BinlogFile
	binlogErr       error
	parseResult     *connector.ParseResult
	parseErr        error
	execResult      *connector.ExecResult
	execErr         error
	closeCalled     bool
}

func (m *mockConnector) Connect(cfg connector.ConnConfig) error {
	return m.connectErr
}

func (m *mockConnector) GetBinlogFiles(ctx context.Context) ([]connector.BinlogFile, error) {
	return m.binlogFiles, m.binlogErr
}

func (m *mockConnector) ParseBinlog(ctx context.Context, req connector.ParseRequest) (*connector.ParseResult, error) {
	return m.parseResult, m.parseErr
}

func (m *mockConnector) ExecuteRollback(ctx context.Context, sqls []string, opts connector.ExecOptions) (*connector.ExecResult, error) {
	return m.execResult, m.execErr
}

func (m *mockConnector) Preflight(ctx context.Context) (*connector.PreflightResult, error) {
	return m.preflightResult, m.preflightErr
}

func (m *mockConnector) Close() error {
	m.closeCalled = true
	return nil
}

// defaultMock creates a mock connector configured for a successful flashback.
func defaultMock() *mockConnector {
	return &mockConnector{
		preflightResult: &connector.PreflightResult{
			Status:  connector.PreflightPass,
			Version: "8.0.32",
			Checks: []connector.PreflightCheck{
				{Name: "MySQL Version", Status: connector.PreflightPass, Message: "8.0.32"},
				{Name: "Binlog Format", Status: connector.PreflightPass},
				{Name: "Binary Logging Enabled", Status: connector.PreflightPass},
				{Name: "User Privileges", Status: connector.PreflightPass},
				{Name: "Database Size", Status: connector.PreflightPass},
				{Name: "Column Metadata", Status: connector.PreflightPass},
				{Name: "Foreign Key Dependencies", Status: connector.PreflightPass},
			},
		},
		binlogFiles: []connector.BinlogFile{
			{Name: "mysql-bin.000001", Size: 1024},
			{Name: "mysql-bin.000002", Size: 2048},
		},
		parseResult: &connector.ParseResult{
			TotalRows: 2,
			Events: []connector.RowEvent{
				{
					Type:      connector.InsertEvent,
					Database:  "mydb",
					Table:     "orders",
					Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
					After: map[string]interface{}{
						"id":   int64(1),
						"name": "widget",
					},
				},
				{
					Type:      connector.UpdateEvent,
					Database:  "mydb",
					Table:     "orders",
					Timestamp: time.Date(2024, 1, 15, 10, 5, 0, 0, time.UTC),
					Before: map[string]interface{}{
						"id":   int64(1),
						"name": "widget",
					},
					After: map[string]interface{}{
						"id":   int64(1),
						"name": "gadget",
					},
				},
			},
		},
		execResult: &connector.ExecResult{
			RowsAffected:    2,
			BatchesCompleted: 1,
		},
	}
}

// ---------------------------------------------------------------------------
// Test: NewFlashbackCommand flag registration
// ---------------------------------------------------------------------------

func TestFlashbackCommand_Flags(t *testing.T) {
	cmd := NewFlashbackCommand()
	// Execute with --help to verify flags are registered
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})
	err := cmd.Execute()
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "--mysql-dsn")
	assert.Contains(t, output, "--target-table")
	assert.Contains(t, output, "--recovery-time")
	assert.Contains(t, output, "--output")
	assert.Contains(t, output, "--dry-run")
	assert.Contains(t, output, "--batch-size")
	assert.Contains(t, output, "--config")
	assert.Contains(t, output, "--passphrase")
}

func TestFlashbackCommand_ValidatesRequiredFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no flags", []string{}},
		{"missing target-table", []string{"--mysql-dsn=root:pass@tcp(127.0.0.1:3306)/db", "--recovery-time=2024-01-15T10:00:00Z"}},
		{"missing recovery-time", []string{"--mysql-dsn=root:pass@tcp(127.0.0.1:3306)/db", "--target-table=mydb.orders"}},
		{"config without passphrase", []string{"--config=/path/to/config", "--target-table=mydb.orders", "--recovery-time=2024-01-15T10:00:00Z"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewFlashbackCommand()
			buf := new(bytes.Buffer)
			cmd.SetErr(buf)
			cmd.SetArgs(tt.args)

			// Execute should return an error
			err := cmd.Execute()
			assert.Error(t, err, "expected error for args: %v", tt.args)
		})
	}
}

// ---------------------------------------------------------------------------
// Test: RunFlashback with mock
// ---------------------------------------------------------------------------

func TestRunFlashback_DryRun(t *testing.T) {
	mock := defaultMock()
	opts := FlashbackOptions{
		Connector:    mock,
		DSN:          "root:pass@tcp(127.0.0.1:3306)/mydb",
		TargetTable:  "mydb.orders",
		RecoveryTime: "2024-01-15T11:00:00Z",
		DryRun:       true,
		BatchSize:    1000,
	}

	err := RunFlashback(context.Background(), opts)
	require.NoError(t, err)
	assert.True(t, mock.closeCalled, "connector should be closed")
}

func TestRunFlashback_OutputFile(t *testing.T) {
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "rollback.sql")

	mock := defaultMock()
	opts := FlashbackOptions{
		Connector:    mock,
		DSN:          "root:pass@tcp(127.0.0.1:3306)/mydb",
		TargetTable:  "mydb.orders",
		RecoveryTime: "2024-01-15T11:00:00Z",
		OutputFile:   outputPath,
		BatchSize:    1000,
	}

	err := RunFlashback(context.Background(), opts)
	require.NoError(t, err)

	// Verify file contents
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "DELETE FROM")
	assert.Contains(t, content, "UPDATE")
}

func TestRunFlashback_Execute(t *testing.T) {
	mock := defaultMock()
	opts := FlashbackOptions{
		Connector:    mock,
		DSN:          "root:pass@tcp(127.0.0.1:3306)/mydb",
		TargetTable:  "mydb.orders",
		RecoveryTime: "2024-01-15T11:00:00Z",
		BatchSize:    1000,
	}

	err := RunFlashback(context.Background(), opts)
	require.NoError(t, err)
}

func TestRunFlashback_PreflightFail(t *testing.T) {
	mock := defaultMock()
	mock.preflightResult = &connector.PreflightResult{
		Status:  connector.PreflightFail,
		Version: "5.7.42",
		Checks: []connector.PreflightCheck{
			{Name: "MySQL Version", Status: connector.PreflightFail, Message: "MySQL 5.7.42 detected; 8.0+ required"},
		},
	}

	opts := FlashbackOptions{
		Connector:    mock,
		DSN:          "root:pass@tcp(127.0.0.1:3306)/mydb",
		TargetTable:  "mydb.orders",
		RecoveryTime: "2024-01-15T11:00:00Z",
	}

	err := RunFlashback(context.Background(), opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "preflight FAILED")
}

func TestRunFlashback_NoEvents(t *testing.T) {
	mock := defaultMock()
	mock.parseResult = &connector.ParseResult{
		TotalRows: 0,
		Events:    []connector.RowEvent{},
	}

	opts := FlashbackOptions{
		Connector:    mock,
		DSN:          "root:pass@tcp(127.0.0.1:3306)/mydb",
		TargetTable:  "mydb.orders",
		RecoveryTime: "2024-01-15T11:00:00Z",
	}

	err := RunFlashback(context.Background(), opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no row events found")
}

func TestRunFlashback_NoBinlogs(t *testing.T) {
	mock := defaultMock()
	mock.binlogFiles = []connector.BinlogFile{}

	opts := FlashbackOptions{
		Connector:    mock,
		DSN:          "root:pass@tcp(127.0.0.1:3306)/mydb",
		TargetTable:  "mydb.orders",
		RecoveryTime: "2024-01-15T11:00:00Z",
	}

	err := RunFlashback(context.Background(), opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no binlog files found")
}

func TestRunFlashback_ConnectorProvided(t *testing.T) {
	// When using a pre-injected connector, DSN is not required.
	mock := defaultMock()
	opts := FlashbackOptions{
		Connector:    mock,
		TargetTable:  "mydb.orders",
		RecoveryTime: "2024-01-15T11:00:00Z",
		DryRun:       true,
	}

	err := RunFlashback(context.Background(), opts)
	require.NoError(t, err)
}

func TestRunFlashback_InvalidRecoveryTime(t *testing.T) {
	mock := defaultMock()
	opts := FlashbackOptions{
		Connector:    mock,
		DSN:          "root:pass@tcp(127.0.0.1:3306)/mydb",
		TargetTable:  "mydb.orders",
		RecoveryTime: "not-a-timestamp",
		DryRun:       true,
	}

	err := RunFlashback(context.Background(), opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse recovery-time")
}

// ---------------------------------------------------------------------------
// Test: resolveConnConfig
// ---------------------------------------------------------------------------

func TestResolveConnConfig_FromDSN(t *testing.T) {
	opts := FlashbackOptions{
		DSN: "user:pass@tcp(host.example.com:3307)/mydb?tls=skip-verify",
	}
	cc, err := resolveConnConfig(opts)
	require.NoError(t, err)
	assert.Equal(t, "host.example.com", cc.Host)
	assert.Equal(t, 3307, cc.Port)
	assert.Equal(t, "user", cc.User)
	assert.Equal(t, "pass", cc.Password)
	assert.Equal(t, "mydb", cc.Database)
}

func TestResolveConnConfig_FromConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.conf")

	// We need to create a valid encrypted config file.
	// Use the internal config package's SaveConfig.
	cfg := &config.Config{
		MySQL: config.MySQLConfig{
			Host:     "192.168.1.50",
			Port:     3306,
			User:     "replicator",
			Password: "s3cret!",
			Database: "prod",
		},
	}
	err := config.SaveConfig(cfgPath, "test-passphrase", cfg)
	require.NoError(t, err)

	opts := FlashbackOptions{
		ConfigFile: cfgPath,
		Passphrase: "test-passphrase",
	}
	cc, err := resolveConnConfig(opts)
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.50", cc.Host)
	assert.Equal(t, 3306, cc.Port)
	assert.Equal(t, "replicator", cc.User)
	assert.Equal(t, "s3cret!", cc.Password)
	assert.Equal(t, "prod", cc.Database)
}

func TestResolveConnConfig_Neither(t *testing.T) {
	opts := FlashbackOptions{}
	_, err := resolveConnConfig(opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "either --mysql-dsn or --config")
}

// ---------------------------------------------------------------------------
// Test: writeSQLToFile
// ---------------------------------------------------------------------------

func TestWriteSQLToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.sql")

	sqls := []string{
		"DELETE FROM `orders` WHERE `id` = 1 LIMIT 1;",
		"INSERT INTO `orders` (`id`, `name`) VALUES (2, 'widget');",
	}

	err := writeSQLToFile(path, sqls)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := strings.TrimSpace(string(data))
	lines := strings.Split(content, "\n")
	assert.Len(t, lines, 2)
	assert.Equal(t, sqls[0], lines[0])
	assert.Equal(t, sqls[1], lines[1])
}

func TestWriteSQLToFile_InvalidPath(t *testing.T) {
	err := writeSQLToFile("/nonexistent/dir/output.sql", []string{"SELECT 1"})
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Test: Root command wiring
// ---------------------------------------------------------------------------

func TestNewRootCommand_HasFlashbackSubcommand(t *testing.T) {
	root := NewRootCommand()
	assert.NotNil(t, root)

	// Find the flashback subcommand
	var flashbackCmd *cobra.Command
	for _, sub := range root.Commands() {
		if sub.Use == "flashback" {
			flashbackCmd = sub
			break
		}
	}
	require.NotNil(t, flashbackCmd, "root command should have flashback subcommand")

	// Verify flashback flags are accessible through the subcommand
	assert.NotNil(t, flashbackCmd.Flags().Lookup("mysql-dsn"))
	assert.NotNil(t, flashbackCmd.Flags().Lookup("target-table"))
	assert.NotNil(t, flashbackCmd.Flags().Lookup("recovery-time"))
}

// ---------------------------------------------------------------------------
// Test: Error reporting edge cases
// ---------------------------------------------------------------------------

func TestRunFlashback_ParseError(t *testing.T) {
	mock := defaultMock()
	mock.parseErr = errors.New("binlog file corrupted")

	opts := FlashbackOptions{
		Connector:    mock,
		DSN:          "root:pass@tcp(127.0.0.1:3306)/mydb",
		TargetTable:  "mydb.orders",
		RecoveryTime: "2024-01-15T11:00:00Z",
		DryRun:       true,
	}

	err := RunFlashback(context.Background(), opts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse binlogs")
}
