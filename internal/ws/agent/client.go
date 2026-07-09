package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/a-shan/mysql-pitr/internal/ws"
	gorilla "github.com/gorilla/websocket"
)

const (
	pingInterval     = 30 * time.Second
	pongWait         = 90 * time.Second
	reconnectMinWait = 1 * time.Second
	reconnectMaxWait = 60 * time.Second
	writeTimeout     = 10 * time.Second
)

// ClientConfig configures an outbound mTLS WebSocket connection to the
// platform hub.
type ClientConfig struct {
	// ServerURL is the platform WebSocket endpoint, e.g.
	// "wss://platform.example.com/ws/agent".
	ServerURL string
	// CertFile is the path to the mTLS client certificate in PEM format.
	CertFile string
	// KeyFile is the path to the mTLS client private key in PEM format.
	KeyFile string
	// CAPath is the path to the CA root certificate (PEM) for verifying the
	// server certificate.
	CAPath string
	// AgentID uniquely identifies this agent instance on the platform.
	AgentID string
}

// Client manages an outbound WebSocket connection to the platform hub with
// mTLS authentication, automatic reconnection with exponential backoff, and
// heartbeat pings every 30 seconds.
type Client struct {
	config ClientConfig

	conn   *gorilla.Conn
	connMu sync.RWMutex

	dialer *gorilla.Dialer

	// writeMu serializes concurrent writes to the WebSocket connection, since
	// gorilla's WriteMessage is not safe for concurrent use.
	writeMu sync.Mutex

	// pending maps command IDs to response channels for in-flight commands.
	pending   map[string]chan *ws.Response
	pendingMu sync.RWMutex

	// dispatcher is optional; if set, incoming commands are routed through it.
	dispatcher *Dispatcher

	ctx    context.Context
	cancel context.CancelFunc

	closeOnce sync.Once
	closed    chan struct{}

	// Backoff state.
	mu               sync.Mutex
	reconnectAttempt int
}

// NewClient creates a Client with the given config. Call Connect to establish
// the WebSocket connection and start automatic reconnection.
func NewClient(cfg ClientConfig) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		config:  cfg,
		pending: make(map[string]chan *ws.Response),
		ctx:     ctx,
		cancel:  cancel,
		closed:  make(chan struct{}),
	}
}

// SetDispatcher attaches a Dispatcher that will handle incoming commands
// received from the platform. If not set, incoming commands receive a default
// "unknown command type" response.
func (c *Client) SetDispatcher(d *Dispatcher) {
	c.dispatcher = d
}

// Connect establishes the initial outbound WebSocket connection with mTLS and
// starts the auto-reconnect loop. If called when already connected it returns
// nil (no-op).
func (c *Client) Connect(ctx context.Context) error {
	tlsConfig, err := c.buildTLSConfig()
	if err != nil {
		return fmt.Errorf("ws client: build tls config: %w", err)
	}

	c.dialer = &gorilla.Dialer{
		TLSClientConfig:  tlsConfig,
		HandshakeTimeout: 45 * time.Second,
	}

	conn, _, err := c.dialer.DialContext(ctx, c.config.ServerURL, nil)
	if err != nil {
		return fmt.Errorf("ws client: initial dial: %w", err)
	}

	c.setConn(conn)

	// Start the connection manager that handles reconnection automatically.
	go c.connectionManager()

	return nil
}

// SendCommand marshals the command as JSON, sends it over the WebSocket, and
// waits for a matching response keyed by cmd.Cmd. The response is delivered
// via an internal channel. If the context is cancelled or the client is
// closed before a response arrives, an error is returned.
func (c *Client) SendCommand(ctx context.Context, cmd ws.Command) (*ws.Response, error) {
	ch := make(chan *ws.Response, 1)

	c.pendingMu.Lock()
	c.pending[cmd.Cmd] = ch
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, cmd.Cmd)
		c.pendingMu.Unlock()
	}()

	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("ws client: marshal command: %w", err)
	}

	c.writeMu.Lock()
	conn := c.getConn()
	if conn == nil {
		c.writeMu.Unlock()
		return nil, fmt.Errorf("ws client: not connected")
	}

	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		c.writeMu.Unlock()
		return nil, fmt.Errorf("ws client: set write deadline: %w", err)
	}
	if err := conn.WriteMessage(gorilla.TextMessage, data); err != nil {
		c.writeMu.Unlock()
		return nil, fmt.Errorf("ws client: write: %w", err)
	}
	c.writeMu.Unlock()

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, fmt.Errorf("ws client: closed")
	}
}

// Close gracefully shuts down the client, cancels all pending commands, and
// closes the WebSocket connection. Safe to call multiple times.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.cancel()

		c.connMu.Lock()
		if c.conn != nil {
			// Send a close frame for a clean shutdown.
			c.writeMu.Lock()
			_ = c.conn.WriteMessage(
				gorilla.CloseMessage,
				gorilla.FormatCloseMessage(gorilla.CloseNormalClosure, ""),
			)
			c.writeMu.Unlock()
			err = c.conn.Close()
			c.conn = nil
		}
		c.connMu.Unlock()

		// Drain pending channels so waiting SendCommand callers unblock.
		c.pendingMu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.pendingMu.Unlock()

		close(c.closed)
	})
	return err
}

// Done returns a channel that is closed when the client has been fully shut
// down (after Close is called and all goroutines have exited).
func (c *Client) Done() <-chan struct{} {
	return c.closed
}

// ---------------------------------------------------------------------------
// Internal: TLS configuration
// ---------------------------------------------------------------------------

func (c *Client) buildTLSConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(c.config.CertFile, c.config.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}

	caPEM, err := os.ReadFile(c.config.CAPath)
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("no CA certificates found in %s", c.config.CAPath)
	}

	u, err := url.Parse(c.config.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		ServerName:   u.Hostname(),
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// ---------------------------------------------------------------------------
// Internal: connection manager (reconnect loop)
// ---------------------------------------------------------------------------

// connectionManager runs in a goroutine and manages the connection lifecycle.
// It sleeps on disconnect with exponential backoff, dials again, and starts a
// new read-pump goroutine for each fresh connection.
func (c *Client) connectionManager() {
	defer func() {
		c.connMu.Lock()
		if c.conn != nil {
			_ = c.conn.Close()
			c.conn = nil
		}
		c.connMu.Unlock()
	}()

	for c.ctx.Err() == nil {
		conn := c.getConn()
		if conn == nil {
			if err := c.reconnect(); err != nil {
				// Context was cancelled (client closing).
				return
			}
			conn = c.getConn()
			if conn == nil {
				continue
			}
		}

		// Block inside readPump until the connection dies or the client is
		// shut down.
		c.readPump(conn)

		// The connection is no longer usable. Clear it if it is still the
		// current conn (i.e. not already replaced by a concurrent Close).
		c.compareAndSwapConn(conn, nil)
	}
}

// reconnect performs a single connection attempt after exponential backoff.
// Returns nil on success or if the context is cancelled.
func (c *Client) reconnect() error {
	wait := c.nextBackoff()

	select {
	case <-time.After(wait):
	case <-c.ctx.Done():
		return c.ctx.Err()
	}

	conn, _, err := c.dialer.DialContext(c.ctx, c.config.ServerURL, nil)
	if err != nil {
		c.mu.Lock()
		c.reconnectAttempt++
		c.mu.Unlock()
		return nil // caller will retry
	}

	c.setConn(conn)
	c.mu.Lock()
	c.reconnectAttempt = 0
	c.mu.Unlock()
	return nil
}

// nextBackoff returns the next exponential-backoff duration with random
// jitter, clamped to [reconnectMinWait, reconnectMaxWait].
func (c *Client) nextBackoff() time.Duration {
	c.mu.Lock()
	attempt := c.reconnectAttempt
	c.mu.Unlock()

	backoff := time.Duration(1<<uint(attempt)) * reconnectMinWait
	if backoff > reconnectMaxWait {
		backoff = reconnectMaxWait
	}

	// Add jitter: 0-25% of backoff.
	jitter := time.Duration(rand.Int63n(int64(backoff / 4)))
	return backoff + jitter
}

// ---------------------------------------------------------------------------
// Internal: read pump (single-goroutine reader)
// ---------------------------------------------------------------------------

// readPump reads messages from the WebSocket connection in a loop. It is
// intended to be the only goroutine that reads from conn (per the WebSocket
// spec). It also sets up the pong handler and read deadline for heartbeat
// detection. readPump returns when the connection dies or the client shuts
// down.
func (c *Client) readPump(conn *gorilla.Conn) {
	defer func() {
		_ = conn.Close()
	}()

	// Extend the read deadline every time a pong arrives.
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	if err := conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		return
	}

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			// Connection is dead; return so the connection manager can
			// reconnect.
			return
		}

		c.handleMessage(message)
	}
}

// handleMessage routes a raw JSON message. Responses are forwarded to the
// pending channel for the matching command ID; commands are dispatched via
// the optional Dispatcher.
func (c *Client) handleMessage(message []byte) {
	// Try to parse as a Response first (the common case for agent-initiated
	// flows).
	var resp ws.Response
	if err := json.Unmarshal(message, &resp); err == nil && resp.Cmd != "" && resp.Status != "" {
		c.pendingMu.RLock()
		ch, ok := c.pending[resp.Cmd]
		c.pendingMu.RUnlock()
		if ok {
			select {
			case ch <- &resp:
			default:
			}
			return
		}
	}

	// Try to parse as a Command (incoming request from the platform).
	var cmd ws.Command
	if err := json.Unmarshal(message, &cmd); err == nil && cmd.Cmd != "" && cmd.Type != "" {
		c.handleIncomingCommand(cmd)
		return
	}

	// Unknown message format — silently drop. In production this would use
	// structured logging.
}

// handleIncomingCommand dispatches the command through the optional
// Dispatcher and sends the response back over the WebSocket.
func (c *Client) handleIncomingCommand(cmd ws.Command) {
	dispatch := c.dispatcher
	var resp *ws.Response

	if dispatch != nil {
		resp = dispatch.Dispatch(c.ctx, cmd)
	} else {
		resp = &ws.Response{
			Cmd:    cmd.Cmd,
			Status: ws.StatusError,
			Error:  fmt.Sprintf("unknown command type: %s", cmd.Type),
		}
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return
	}

	c.writeMu.Lock()
	conn := c.getConn()
	if conn == nil {
		c.writeMu.Unlock()
		return
	}

	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_ = conn.WriteMessage(gorilla.TextMessage, data)
	c.writeMu.Unlock()
}

// ---------------------------------------------------------------------------
// Internal: heartbeat
// ---------------------------------------------------------------------------

// startHeartbeat launches a goroutine that sends a WebSocket Ping frame every
// pingInterval. It stops when ctx is cancelled. The caller must ensure
// heartbeat is restarted after each reconnection.
func (c *Client) startHeartbeat(conn *gorilla.Conn) {
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				c.writeMu.Lock()
				_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
				_ = conn.WriteControl(gorilla.PingMessage, nil, time.Now().Add(writeTimeout))
				c.writeMu.Unlock()
			case <-c.ctx.Done():
				return
			}
		}
	}()
}

// ---------------------------------------------------------------------------
// Internal: thread-safe conn accessors
// ---------------------------------------------------------------------------

func (c *Client) setConn(conn *gorilla.Conn) {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.conn = conn

	// Start heartbeat for the new connection.
	c.startHeartbeat(conn)
}

func (c *Client) getConn() *gorilla.Conn {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.conn
}

func (c *Client) compareAndSwapConn(old, new *gorilla.Conn) {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn == old {
		c.conn = new
	}
}
