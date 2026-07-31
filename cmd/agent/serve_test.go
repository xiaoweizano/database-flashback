package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-shan/mysql-pitr/internal/config"
	"github.com/a-shan/mysql-pitr/internal/ws"
)

func testServeDaemon(t *testing.T) *serveDaemon {
	t.Helper()
	cfg := &config.Config{
		MySQL: config.MySQLConfig{
			Host: "127.0.0.1", Port: 3306, User: "u", Password: "p", Database: "d",
		},
		DataDir:         t.TempDir(),
		MySQLBinlogPath: "/opt/mysql/bin/mysqlbinlog",
	}
	return newServeDaemon(cfg, "agent-1")
}

// ---------------------------------------------------------------------------
// Param helpers
// ---------------------------------------------------------------------------

func TestParamString(t *testing.T) {
	assert.Equal(t, "abc", paramString(map[string]interface{}{"k": " abc "}, "k"))
	assert.Equal(t, "", paramString(map[string]interface{}{"k": 42}, "k"))
	assert.Equal(t, "", paramString(map[string]interface{}{}, "k"))
}

func TestParamUint32(t *testing.T) {
	assert.Equal(t, uint32(123), paramUint32(map[string]interface{}{"k": float64(123)}, "k"))
	assert.Equal(t, uint32(0), paramUint32(map[string]interface{}{"k": float64(-5)}, "k"))
	assert.Equal(t, uint32(0), paramUint32(map[string]interface{}{"k": "abc"}, "k"))
}

// ---------------------------------------------------------------------------
// buildBinlogParseOpts — targeted parse parameter mapping
// ---------------------------------------------------------------------------

func TestBuildBinlogParseOpts_Full(t *testing.T) {
	d := testServeDaemon(t)
	params := map[string]interface{}{
		"targetTable": "mydb.orders",
		"startTime":   "2026-07-01T00:00:00Z",
		"endTime":     "2026-07-02T00:00:00Z",
		"startPos":    float64(4),
		"stopPos":     float64(1024),
	}
	opts, err := buildBinlogParseOpts(params, d.cfg)
	require.NoError(t, err)

	assert.Equal(t, "mydb.orders", opts.TargetTable)
	require.NotNil(t, opts.StartTime)
	assert.Equal(t, "2026-07-01T00:00:00Z", opts.StartTime.UTC().Format(time.RFC3339))
	require.NotNil(t, opts.EndTime)
	assert.Equal(t, "2026-07-02T00:00:00Z", opts.EndTime.UTC().Format(time.RFC3339))
	assert.Equal(t, uint32(4), opts.StartPos)
	assert.Equal(t, uint32(1024), opts.StopPos)
	// Config override flows through.
	assert.Equal(t, "/opt/mysql/bin/mysqlbinlog", opts.MySQLBinlogPath)
}

func TestBuildBinlogParseOpts_Defaults(t *testing.T) {
	d := testServeDaemon(t)
	opts, err := buildBinlogParseOpts(map[string]interface{}{
		"targetTable": "mydb.orders",
	}, d.cfg)
	require.NoError(t, err)
	assert.Nil(t, opts.StartTime)
	assert.Nil(t, opts.EndTime)
	assert.Equal(t, uint32(0), opts.StartPos)
	assert.Equal(t, uint32(0), opts.StopPos)
}

func TestBuildBinlogParseOpts_Errors(t *testing.T) {
	d := testServeDaemon(t)

	_, err := buildBinlogParseOpts(map[string]interface{}{}, d.cfg)
	assert.ErrorContains(t, err, "targetTable")

	_, err = buildBinlogParseOpts(map[string]interface{}{
		"targetTable": "mydb.orders",
		"startTime":   "not-a-time",
	}, d.cfg)
	assert.ErrorContains(t, err, "startTime")

	_, err = buildBinlogParseOpts(map[string]interface{}{
		"targetTable": "mydb.orders",
		"endTime":     "not-a-time",
	}, d.cfg)
	assert.ErrorContains(t, err, "endTime")
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func TestHandlePITRCancel_CancelsInFlightOperation(t *testing.T) {
	d := testServeDaemon(t)

	opCtx, cancel := context.WithCancel(context.Background())
	d.cancels["op_1"] = cancel

	resp := d.handlePITRCancel(context.Background(), ws.Command{
		Cmd: "cancel-1", Type: ws.CmdPITRCancel,
		Params: map[string]interface{}{"operationId": "op_1"},
	})
	require.NotNil(t, resp)
	assert.Equal(t, ws.StatusOK, resp.Status)

	// The in-flight operation context must now be cancelled.
	assert.ErrorIs(t, opCtx.Err(), context.Canceled)
}

func TestHandlePITRCancel_UnknownOperation(t *testing.T) {
	d := testServeDaemon(t)
	resp := d.handlePITRCancel(context.Background(), ws.Command{
		Cmd: "cancel-2", Type: ws.CmdPITRCancel,
		Params: map[string]interface{}{"operationId": "op_missing"},
	})
	require.NotNil(t, resp)
	assert.Equal(t, ws.StatusOK, resp.Status)
}

func TestHandleShutdown(t *testing.T) {
	d := testServeDaemon(t)
	resp := d.handleShutdown(context.Background(), ws.Command{Cmd: "s", Type: ws.CmdShutdown})
	require.NotNil(t, resp)
	assert.Equal(t, ws.StatusOK, resp.Status)

	select {
	case <-d.stopCh:
	case <-time.After(2 * time.Second):
		t.Fatal("stopCh was not closed by shutdown")
	}
}

func TestHandleStatus_ReportsMySQLConnectivity(t *testing.T) {
	d := testServeDaemon(t)
	resp := d.handleStatus(context.Background(), ws.Command{Cmd: "s", Type: ws.CmdStatus})
	require.NotNil(t, resp)
	assert.Equal(t, ws.StatusOK, resp.Status)

	result, ok := resp.Result.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "agent-1", result["agentId"])

	mysql, ok := result["mysql"].(map[string]interface{})
	require.True(t, ok)
	// No MySQL is reachable in the test environment.
	assert.Equal(t, false, mysql["connected"])
}
