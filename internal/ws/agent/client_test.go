package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/a-shan/mysql-pitr/internal/ws"
	gorilla "github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// Test certificate helpers
// ---------------------------------------------------------------------------

// testCertBundle holds PEM-encoded certificates and keys for test mTLS.
type testCertBundle struct {
	CACert     []byte
	CAKey      []byte
	ServerCert []byte
	ServerKey  []byte
	ClientCert []byte
	ClientKey  []byte
}

// generateTestCerts creates a self-signed CA, then a server and client
// certificate signed by that CA. All use ECDSA P-256.
func generateTestCerts(t *testing.T) *testCertBundle {
	t.Helper()

	// --- CA ---
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA mTLS"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}

	// --- Server ---
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", "127.0.0.1"},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create server cert: %v", err)
	}

	// --- Client ---
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "test-agent-1"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caTemplate, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create client cert: %v", err)
	}

	return &testCertBundle{
		CACert:     pemEncode("CERTIFICATE", caDER),
		CAKey:      pemEncode("EC PRIVATE KEY", marshalECKey(caKey)),
		ServerCert: pemEncode("CERTIFICATE", serverDER),
		ServerKey:  pemEncode("EC PRIVATE KEY", marshalECKey(serverKey)),
		ClientCert: pemEncode("CERTIFICATE", clientDER),
		ClientKey:  pemEncode("EC PRIVATE KEY", marshalECKey(clientKey)),
	}
}

func pemEncode(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

func marshalECKey(key *ecdsa.PrivateKey) []byte {
	b, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		panic(err)
	}
	return b
}

// writeClientCerts writes the client PEM files to the given directory and
// returns the paths to CA, cert, and key files.
func writeClientCerts(t *testing.T, dir string, b *testCertBundle) (caPath, certPath, keyPath string) {
	t.Helper()

	caPath = filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, b.CACert, 0644); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	certPath = filepath.Join(dir, "client.crt")
	if err := os.WriteFile(certPath, b.ClientCert, 0644); err != nil {
		t.Fatalf("write client cert: %v", err)
	}
	keyPath = filepath.Join(dir, "client.key")
	if err := os.WriteFile(keyPath, b.ClientKey, 0600); err != nil {
		t.Fatalf("write client key: %v", err)
	}
	return
}

// ---------------------------------------------------------------------------
// Test WebSocket server
// ---------------------------------------------------------------------------

// newTestWSServer creates a TLS WebSocket test server that requires mTLS
// client certificates. Each new WebSocket connection is handled by the
// provided handler function. The server URL is returned and cleaned up
// automatically when the test finishes.
func newTestWSServer(t *testing.T, bundle *testCertBundle, handler func(conn *gorilla.Conn)) string {
	t.Helper()

	caBlock, _ := pem.Decode(bundle.CACert)
	if caBlock == nil {
		t.Fatal("failed to decode CA PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	serverCert, err := tls.X509KeyPair(bundle.ServerCert, bundle.ServerKey)
	if err != nil {
		t.Fatalf("load server cert pair: %v", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		MinVersion:   tls.VersionTLS12,
	}

	upgrader := &gorilla.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/agent", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("test WS server: upgrade: %v", err)
			return
		}
		handler(conn)
	})

	// Create a TLS listener on a random port.
	listener, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatalf("create TLS listener: %v", err)
	}

	srv := &http.Server{Handler: mux}
	t.Cleanup(func() {
		_ = srv.Close()
		_ = listener.Close()
	})

	go func() {
		_ = srv.Serve(listener)
	}()

	addr := listener.Addr().(*net.TCPAddr)
	return fmt.Sprintf("wss://127.0.0.1:%d/ws/agent", addr.Port)
}

// ---------------------------------------------------------------------------
// Client base config helper
// ---------------------------------------------------------------------------

func clientConfigForTest(t *testing.T, bundle *testCertBundle, serverURL string) (ClientConfig, *Client) {
	t.Helper()
	dir := t.TempDir()
	caPath, certPath, keyPath := writeClientCerts(t, dir, bundle)

	cfg := ClientConfig{
		ServerURL: serverURL,
		CertFile:  certPath,
		KeyFile:   keyPath,
		CAPath:    caPath,
		AgentID:   "test-agent-1",
	}
	return cfg, NewClient(cfg)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestNewClient(t *testing.T) {
	cfg := ClientConfig{
		ServerURL: "wss://localhost:9999/ws/agent",
		CertFile:  "/nonexistent/cert.pem",
		KeyFile:   "/nonexistent/key.pem",
		CAPath:    "/nonexistent/ca.pem",
		AgentID:   "test-agent",
	}
	c := NewClient(cfg)
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close on fresh client: %v", err)
	}
}

func TestConnectCertFileNotFound(t *testing.T) {
	cfg := ClientConfig{
		ServerURL: "wss://localhost:9999/ws/agent",
		CertFile:  "/nonexistent/cert.pem",
		KeyFile:   "/nonexistent/key.pem",
		CAPath:    "/nonexistent/ca.pem",
	}
	c := NewClient(cfg)
	err := c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error with nonexistent cert files")
	}
}

func TestConnectAndSendCommand(t *testing.T) {
	bundle := generateTestCerts(t)

	serverCmdCh := make(chan ws.Command, 1)
	serverDone := make(chan struct{})

	handler := func(conn *gorilla.Conn) {
		defer close(serverDone)
		defer conn.Close()

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var cmd ws.Command
		if err := json.Unmarshal(msg, &cmd); err != nil {
			t.Errorf("server: unmarshal command: %v", err)
			return
		}
		serverCmdCh <- cmd

		resp := ws.Response{
			Cmd:    cmd.Cmd,
			Status: ws.StatusOK,
			Result: map[string]interface{}{"echo": true},
		}
		data, _ := json.Marshal(resp)
		_ = conn.WriteMessage(gorilla.TextMessage, data)
	}

	serverURL := newTestWSServer(t, bundle, handler)
	cfg, client := clientConfigForTest(t, bundle, serverURL)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	cmd := ws.Command{
		Cmd:  "test-cmd-1",
		Type: ws.CmdStatus,
		Params: map[string]interface{}{
			"detail": "full",
		},
	}

	resp, err := client.SendCommand(ctx, cmd)
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if resp == nil {
		t.Fatal("SendCommand returned nil response")
	}
	if resp.Cmd != "test-cmd-1" {
		t.Errorf("expected Cmd %q, got %q", "test-cmd-1", resp.Cmd)
	}
	if resp.Status != ws.StatusOK {
		t.Errorf("expected Status %q, got %q", ws.StatusOK, resp.Status)
	}

	select {
	case received := <-serverCmdCh:
		if received.Cmd != "test-cmd-1" {
			t.Errorf("server received Cmd %q, expected %q", received.Cmd, "test-cmd-1")
		}
		if received.Type != ws.CmdStatus {
			t.Errorf("server received Type %q, expected %q", received.Type, ws.CmdStatus)
		}
		if received.Params["detail"] != "full" {
			t.Errorf("server received Params detail=%v, expected 'full'", received.Params["detail"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive command")
	}
}

func TestSendCommandNotConnected(t *testing.T) {
	cfg := ClientConfig{
		ServerURL: "wss://localhost:9999/ws/agent",
		CertFile:  "testdata/nonexistent.pem",
		KeyFile:   "testdata/nonexistent.pem",
		CAPath:    "testdata/nonexistent.pem",
	}
	client := NewClient(cfg)

	_, err := client.SendCommand(context.Background(), ws.Command{
		Cmd: "no-conn", Type: ws.CmdStatus,
	})
	if err == nil {
		t.Fatal("expected error when not connected")
	}
}

func TestClientCloseTerminatesPending(t *testing.T) {
	bundle := generateTestCerts(t)

	// Server that never responds.
	serverDone := make(chan struct{})
	handler := func(conn *gorilla.Conn) {
		defer close(serverDone)
		// Read everything and drop it (no responses sent).
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}

	serverURL := newTestWSServer(t, bundle, handler)
	cfg, client := clientConfigForTest(t, bundle, serverURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Send a command (will hang waiting for response).
	errCh := make(chan error, 1)
	go func() {
		_, err := client.SendCommand(context.Background(), ws.Command{
			Cmd: "will-close", Type: ws.CmdStatus,
		})
		errCh <- err
	}()

	// Give the command time to be sent.
	time.Sleep(200 * time.Millisecond)

	// Close the client — the pending SendCommand should get an error.
	if err := client.Close(); err != nil {
		t.Logf("Close: %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected error after Close, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SendCommand did not unblock after Close")
	}
}

func TestClientDispatchesIncomingCommand(t *testing.T) {
	bundle := generateTestCerts(t)

	serverDone := make(chan struct{})

	handler := func(conn *gorilla.Conn) {
		defer close(serverDone)
		defer conn.Close()

		// Wait briefly for client to be fully set up.
		time.Sleep(200 * time.Millisecond)

		// Send a command to the client.
		cmd := ws.Command{
			Cmd:  "srv-cmd-1",
			Type: ws.CmdStatus,
		}
		data, _ := json.Marshal(cmd)
		if err := conn.WriteMessage(gorilla.TextMessage, data); err != nil {
			return
		}

		// Read the response from the agent.
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var resp ws.Response
		if err := json.Unmarshal(msg, &resp); err != nil {
			t.Errorf("server: unmarshal response: %v", err)
			return
		}
		if resp.Cmd != "srv-cmd-1" {
			t.Errorf("expected response Cmd %q, got %q", "srv-cmd-1", resp.Cmd)
		}
		if resp.Status != ws.StatusOK {
			t.Errorf("expected Status %q, got %q", ws.StatusOK, resp.Status)
		}
		if resp.Result != "status-ok" {
			t.Errorf("expected Result %q, got %v", "status-ok", resp.Result)
		}
	}

	serverURL := newTestWSServer(t, bundle, handler)
	cfg, client := clientConfigForTest(t, bundle, serverURL)
	defer client.Close()

	// Register a dispatcher.
	disp := NewDispatcher()
	disp.RegisterHandler(ws.CmdStatus, func(ctx context.Context, cmd ws.Command) *ws.Response {
		return &ws.Response{
			Cmd:    cmd.Cmd,
			Status: ws.StatusOK,
			Result: "status-ok",
		}
	})
	client.SetDispatcher(disp)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	select {
	case <-serverDone:
		// Server completed its checks.
	case <-time.After(5 * time.Second):
		t.Fatal("test timeout waiting for server")
	}
}

func TestClientNoDispatcherForIncomingCommand(t *testing.T) {
	bundle := generateTestCerts(t)

	serverDone := make(chan struct{})

	handler := func(conn *gorilla.Conn) {
		defer close(serverDone)
		defer conn.Close()

		time.Sleep(200 * time.Millisecond)

		cmd := ws.Command{
			Cmd:  "srv-cmd-2",
			Type: ws.CmdPITRParse,
		}
		data, _ := json.Marshal(cmd)
		_ = conn.WriteMessage(gorilla.TextMessage, data)

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var resp ws.Response
		if err := json.Unmarshal(msg, &resp); err != nil {
			t.Errorf("server: unmarshal response: %v", err)
			return
		}
		if resp.Cmd != "srv-cmd-2" {
			t.Errorf("expected Cmd %q, got %q", "srv-cmd-2", resp.Cmd)
		}
		if resp.Status != ws.StatusError {
			t.Errorf("expected Status %q, got %q", ws.StatusError, resp.Status)
		}
		if resp.Error == "" {
			t.Error("expected non-empty error message")
		}
	}

	serverURL := newTestWSServer(t, bundle, handler)
	cfg, client := clientConfigForTest(t, bundle, serverURL)
	defer client.Close()
	// No dispatcher set.

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("test timeout waiting for server")
	}
}

func TestConcurrentSendCommands(t *testing.T) {
	bundle := generateTestCerts(t)

	handler := func(conn *gorilla.Conn) {
		defer conn.Close()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var cmd ws.Command
			if err := json.Unmarshal(msg, &cmd); err != nil {
				continue
			}
			resp := ws.Response{Cmd: cmd.Cmd, Status: ws.StatusOK}
			data, _ := json.Marshal(resp)
			_ = conn.WriteMessage(gorilla.TextMessage, data)
		}
	}

	serverURL := newTestWSServer(t, bundle, handler)
	cfg, client := clientConfigForTest(t, bundle, serverURL)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	var wg sync.WaitGroup
	const concurrentCmds = 30

	for i := 0; i < concurrentCmds; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cmd := ws.Command{
				Cmd:  fmt.Sprintf("concurrent-cmd-%d", id),
				Type: ws.CmdStatus,
			}
			resp, err := client.SendCommand(ctx, cmd)
			if err != nil {
				t.Errorf("SendCommand %d: %v", id, err)
				return
			}
			if resp == nil {
				t.Errorf("cmd %d: nil response", id)
				return
			}
			if resp.Status != ws.StatusOK {
				t.Errorf("cmd %d: expected ok, got %q", id, resp.Status)
			}
		}(i)
	}

	wg.Wait()
}

func TestClientReconnectAfterDisconnect(t *testing.T) {
	bundle := generateTestCerts(t)

	var (
		mu         sync.Mutex
		connCount  int
		acceptConn bool
	)

	handler := func(conn *gorilla.Conn) {
		mu.Lock()
		connCount++
		accept := acceptConn
		mu.Unlock()

		defer conn.Close()

		if !accept {
			// Close immediately — client will see connection drop.
			return
		}

		// Handle normally.
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var cmd ws.Command
			if err := json.Unmarshal(msg, &cmd); err != nil {
				continue
			}
			resp := ws.Response{Cmd: cmd.Cmd, Status: ws.StatusOK}
			data, _ := json.Marshal(resp)
			_ = conn.WriteMessage(gorilla.TextMessage, data)
		}
	}

	serverURL := newTestWSServer(t, bundle, handler)
	cfg, client := clientConfigForTest(t, bundle, serverURL)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// First connection will be rejected.
	mu.Lock()
	acceptConn = false
	mu.Unlock()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Wait for reconnect attempts. The first connection is dropped immediately,
	// then backoff kicks in (1s, 2s, 4s...). Allow enough time for retries.
	time.Sleep(5 * time.Second)

	// Now accept connections.
	mu.Lock()
	acceptConn = true
	mu.Unlock()

	// Wait for reconnect to succeed.
	time.Sleep(3 * time.Second)

	// Verify we can send a command.
	cmd := ws.Command{Cmd: "reconnect-test", Type: ws.CmdStatus}
	resp, err := client.SendCommand(ctx, cmd)
	if err != nil {
		t.Fatalf("SendCommand after reconnect: %v", err)
	}
	if resp.Status != ws.StatusOK {
		t.Errorf("expected ok, got %q", resp.Status)
	}

	mu.Lock()
	if connCount < 2 {
		t.Errorf("expected at least 2 connections (1 rejected + reconnects), got %d", connCount)
	}
	mu.Unlock()
}

func TestClientDoneChannel(t *testing.T) {
	bundle := generateTestCerts(t)

	handler := func(conn *gorilla.Conn) {
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	}

	serverURL := newTestWSServer(t, bundle, handler)
	cfg, client := clientConfigForTest(t, bundle, serverURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	done := client.Done()
	if done == nil {
		t.Fatal("Done() returned nil channel")
	}

	// Close the client.
	_ = client.Close()

	select {
	case <-done:
		// Channel closed as expected.
	case <-time.After(3 * time.Second):
		t.Fatal("Done() channel was not closed after Close()")
	}
}

func TestClientMultipleClose(t *testing.T) {
	bundle := generateTestCerts(t)

	handler := func(conn *gorilla.Conn) {
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	}

	serverURL := newTestWSServer(t, bundle, handler)
	cfg, client := clientConfigForTest(t, bundle, serverURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Close multiple times — should not panic.
	if err := client.Close(); err != nil {
		t.Logf("First Close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Logf("Second Close: %v", err)
	}
}
