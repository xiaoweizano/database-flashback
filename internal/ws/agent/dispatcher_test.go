package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/a-shan/mysql-pitr/internal/ws"
)

func TestNewDispatcher(t *testing.T) {
	d := NewDispatcher()
	if d == nil {
		t.Fatal("NewDispatcher returned nil")
	}
}

func TestRegisterAndDispatch(t *testing.T) {
	d := NewDispatcher()

	d.RegisterHandler(ws.CmdStatus, func(ctx context.Context, cmd ws.Command) *ws.Response {
		return &ws.Response{
			Cmd:    cmd.Cmd,
			Status: ws.StatusOK,
			Result: map[string]interface{}{"running": true},
		}
	})

	cmd := ws.Command{
		Cmd:  "test-uuid-1",
		Type: ws.CmdStatus,
	}

	resp := d.Dispatch(context.Background(), cmd)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Cmd != "test-uuid-1" {
		t.Errorf("expected Cmd %q, got %q", "test-uuid-1", resp.Cmd)
	}
	if resp.Status != ws.StatusOK {
		t.Errorf("expected Status %q, got %q", ws.StatusOK, resp.Status)
	}
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatal("expected Result to be map[string]interface{}")
	}
	running, ok := result["running"].(bool)
	if !ok || !running {
		t.Errorf("expected running=true, got %v", result["running"])
	}
}

func TestDispatchUnknownCommandType(t *testing.T) {
	d := NewDispatcher()

	cmd := ws.Command{
		Cmd:  "test-uuid-2",
		Type: "nonexistent",
	}

	resp := d.Dispatch(context.Background(), cmd)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Status != ws.StatusError {
		t.Errorf("expected Status %q, got %q", ws.StatusError, resp.Status)
	}
	if resp.Error == "" {
		t.Error("expected non-empty Error for unknown command type")
	}
	if resp.Cmd != "test-uuid-2" {
		t.Errorf("expected Cmd %q, got %q", "test-uuid-2", resp.Cmd)
	}
}

func TestRegisterOverwrite(t *testing.T) {
	d := NewDispatcher()

	// Register first handler.
	d.RegisterHandler(ws.CmdShutdown, func(ctx context.Context, cmd ws.Command) *ws.Response {
		return &ws.Response{Cmd: cmd.Cmd, Status: ws.StatusOK, Result: "first"}
	})

	// Overwrite with a different handler.
	d.RegisterHandler(ws.CmdShutdown, func(ctx context.Context, cmd ws.Command) *ws.Response {
		return &ws.Response{Cmd: cmd.Cmd, Status: ws.StatusOK, Result: "second"}
	})

	cmd := ws.Command{Cmd: "test-uuid-3", Type: ws.CmdShutdown}
	resp := d.Dispatch(context.Background(), cmd)
	if resp.Result != "second" {
		t.Errorf("expected Result %q, got %v", "second", resp.Result)
	}
}

func TestDispatchMultipleHandlers(t *testing.T) {
	d := NewDispatcher()

	d.RegisterHandler(ws.CmdPreflight, func(ctx context.Context, cmd ws.Command) *ws.Response {
		return &ws.Response{Cmd: cmd.Cmd, Status: ws.StatusOK, Result: "preflight-done"}
	})
	d.RegisterHandler(ws.CmdPITRParse, func(ctx context.Context, cmd ws.Command) *ws.Response {
		return &ws.Response{Cmd: cmd.Cmd, Status: ws.StatusOK, Result: "parsed"}
	})
	d.RegisterHandler(ws.CmdStatus, func(ctx context.Context, cmd ws.Command) *ws.Response {
		return &ws.Response{Cmd: cmd.Cmd, Status: ws.StatusOK, Result: "online"}
	})

	tests := []struct {
		cmdType string
		want    string
	}{
		{ws.CmdPreflight, "preflight-done"},
		{ws.CmdPITRParse, "parsed"},
		{ws.CmdStatus, "online"},
		{ws.CmdShutdown, ""}, // not registered
	}

	for _, tt := range tests {
		cmd := ws.Command{Cmd: "uuid", Type: tt.cmdType}
		resp := d.Dispatch(context.Background(), cmd)

		if tt.want == "" {
			if resp.Status != ws.StatusError {
				t.Errorf("cmdType %q: expected error, got %v", tt.cmdType, resp.Result)
			}
		} else {
			if resp.Status != ws.StatusOK {
				t.Errorf("cmdType %q: expected ok, got %q", tt.cmdType, resp.Status)
			}
			if resp.Result != tt.want {
				t.Errorf("cmdType %q: expected Result %q, got %v", tt.cmdType, tt.want, resp.Result)
			}
		}
	}
}

func TestConcurrentDispatch(t *testing.T) {
	d := NewDispatcher()

	d.RegisterHandler(ws.CmdStatus, func(ctx context.Context, cmd ws.Command) *ws.Response {
		return &ws.Response{Cmd: cmd.Cmd, Status: ws.StatusOK}
	})

	var wg sync.WaitGroup
	const goroutines = 50

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cmd := ws.Command{Cmd: "uuid", Type: ws.CmdStatus}
			resp := d.Dispatch(context.Background(), cmd)
			if resp == nil || resp.Status != ws.StatusOK {
				t.Errorf("goroutine %d: unexpected response: %+v", id, resp)
			}
		}(i)
	}

	wg.Wait()
}

func TestConcurrentRegisterAndDispatch(t *testing.T) {
	d := NewDispatcher()

	// Register one handler beforehand.
	d.RegisterHandler(ws.CmdStatus, func(ctx context.Context, cmd ws.Command) *ws.Response {
		return &ws.Response{Cmd: cmd.Cmd, Status: ws.StatusOK, Result: "status"}
	})

	var wg sync.WaitGroup

	// Concurrently register new handlers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each registration overwrites the same key.
			d.RegisterHandler("dynamic-"+ws.CmdStatus, func(ctx context.Context, cmd ws.Command) *ws.Response {
				return &ws.Response{Cmd: cmd.Cmd, Status: ws.StatusOK}
			})
		}()
	}

	// Concurrently dispatch.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := ws.Command{Cmd: "uuid", Type: ws.CmdStatus}
			resp := d.Dispatch(context.Background(), cmd)
			if resp == nil || resp.Status != ws.StatusOK {
				t.Errorf("concurrent dispatch failed: %+v", resp)
			}
		}()
	}

	wg.Wait()
}
