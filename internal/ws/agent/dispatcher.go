package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/a-shan/mysql-pitr/internal/ws"
)

// HandlerFunc processes a command and returns a response. Implementations
// must be safe for concurrent invocation when registered with a Dispatcher.
type HandlerFunc func(ctx context.Context, cmd ws.Command) *ws.Response

// Dispatcher routes incoming commands to registered handlers by command type.
// It is safe for concurrent use via a RWMutex.
type Dispatcher struct {
	mu       sync.RWMutex
	handlers map[string]HandlerFunc
}

// NewDispatcher returns an initialized Dispatcher with no registered handlers.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		handlers: make(map[string]HandlerFunc),
	}
}

// RegisterHandler binds handler to the given command type. If a handler was
// already registered for cmdType it is replaced.
func (d *Dispatcher) RegisterHandler(cmdType string, handler HandlerFunc) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.handlers[cmdType] = handler
}

// Dispatch looks up the handler registered for cmd.Type and invokes it. If no
// handler is found, a response with status "error" is returned.
func (d *Dispatcher) Dispatch(ctx context.Context, cmd ws.Command) *ws.Response {
	d.mu.RLock()
	handler, ok := d.handlers[cmd.Type]
	d.mu.RUnlock()

	if !ok {
		return &ws.Response{
			Cmd:    cmd.Cmd,
			Status: ws.StatusError,
			Error:  fmt.Sprintf("unknown command type: %s", cmd.Type),
		}
	}

	return handler(ctx, cmd)
}
