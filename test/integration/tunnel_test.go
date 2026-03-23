package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sonnytaylor/exio/internal/client"
	"github.com/sonnytaylor/exio/internal/server"
)

const testToken = "integration-test-token-abc123"

// freePort returns an available TCP port on localhost.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// waitForServer polls the health endpoint until the server is ready.
func waitForServer(t *testing.T, port int) {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/_health", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server on port %d did not become ready", port)
}

// newServer creates and starts an Exio server, returning a cancel function for shutdown.
func newServer(t *testing.T, cfg *server.Config) context.CancelFunc {
	t.Helper()
	s, err := server.New(cfg)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
	waitForServer(t, cfg.Port)
	return cancel
}

// connectClient creates a client, connects it to the server, and starts the run loop.
// Optional config modifiers can be passed to customize the client config.
func connectClient(t *testing.T, serverPort, localPort int, subdomain string, opts ...func(*client.Config)) *client.Client {
	t.Helper()
	cfg := &client.Config{
		ServerURL: fmt.Sprintf("http://127.0.0.1:%d", serverPort),
		Token:     testToken,
		Subdomain: subdomain,
		LocalPort: localPort,
		LocalHost: "127.0.0.1",
	}
	for _, opt := range opts {
		opt(cfg)
	}
	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	c.SetQuietMode(true)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	go c.Run(context.Background())
	time.Sleep(200 * time.Millisecond)
	return c
}

// backendPort extracts the port from an httptest.Server.
func backendPort(t *testing.T, s *httptest.Server) int {
	t.Helper()
	return s.Listener.Addr().(*net.TCPAddr).Port
}

// --- HTTP Tunnel Tests ---

func TestHTTPTunnelGET(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "test-value")
		fmt.Fprint(w, "hello from backend")
	}))
	defer backend.Close()

	serverPort := freePort(t)
	cancel := newServer(t, &server.Config{
		Port:        serverPort,
		Token:       testToken,
		BaseDomain:  "localhost",
		RoutingMode: "path",
	})
	defer cancel()

	c := connectClient(t, serverPort, backendPort(t, backend), "get-test")
	defer c.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/get-test/", serverPort))
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello from backend" {
		t.Errorf("body = %q, want %q", body, "hello from backend")
	}

	if got := resp.Header.Get("X-Custom"); got != "test-value" {
		t.Errorf("X-Custom header = %q, want %q", got, "test-value")
	}
}

func TestHTTPTunnelPOST(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "echo: %s", body)
	}))
	defer backend.Close()

	serverPort := freePort(t)
	cancel := newServer(t, &server.Config{
		Port:        serverPort,
		Token:       testToken,
		BaseDomain:  "localhost",
		RoutingMode: "path",
	})
	defer cancel()

	c := connectClient(t, serverPort, backendPort(t, backend), "post-test")
	defer c.Close()

	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:%d/post-test/data", serverPort),
		"text/plain",
		strings.NewReader("request body content"),
	)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "echo: request body content" {
		t.Errorf("body = %q, want %q", body, "echo: request body content")
	}
}

func TestHTTPTunnelPathStripping(t *testing.T) {
	var receivedPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	serverPort := freePort(t)
	cancel := newServer(t, &server.Config{
		Port:        serverPort,
		Token:       testToken,
		BaseDomain:  "localhost",
		RoutingMode: "path",
	})
	defer cancel()

	c := connectClient(t, serverPort, backendPort(t, backend), "strip-test")
	defer c.Close()

	// /strip-test/api/users should arrive at backend as /api/users
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/strip-test/api/users", serverPort))
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	resp.Body.Close()

	if receivedPath != "/api/users" {
		t.Errorf("backend received path = %q, want %q", receivedPath, "/api/users")
	}
}

func TestHTTPTunnelMultipleRequests(t *testing.T) {
	var count int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		fmt.Fprintf(w, "request %d", count)
	}))
	defer backend.Close()

	serverPort := freePort(t)
	cancel := newServer(t, &server.Config{
		Port:        serverPort,
		Token:       testToken,
		BaseDomain:  "localhost",
		RoutingMode: "path",
	})
	defer cancel()

	c := connectClient(t, serverPort, backendPort(t, backend), "multi-test")
	defer c.Close()

	for i := 1; i <= 5; i++ {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/multi-test/", serverPort))
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		expected := fmt.Sprintf("request %d", i)
		if string(body) != expected {
			t.Errorf("request %d: body = %q, want %q", i, body, expected)
		}
	}
}

func TestHTTPTunnelSubdomainRouting(t *testing.T) {
	var receivedPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		fmt.Fprint(w, "subdomain routing works")
	}))
	defer backend.Close()

	serverPort := freePort(t)
	cancel := newServer(t, &server.Config{
		Port:        serverPort,
		Token:       testToken,
		BaseDomain:  "tunnel.example.com",
		RoutingMode: "subdomain",
	})
	defer cancel()

	c := connectClient(t, serverPort, backendPort(t, backend), "myapp")
	defer c.Close()

	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/data", serverPort), nil)
	req.Host = "myapp.tunnel.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "subdomain routing works" {
		t.Errorf("body = %q, want %q", body, "subdomain routing works")
	}

	// Path should NOT be stripped in subdomain mode
	if receivedPath != "/api/data" {
		t.Errorf("backend path = %q, want %q", receivedPath, "/api/data")
	}
}

func TestHTTPTunnelXForwardedHeaders(t *testing.T) {
	var gotForwardedHost, gotForwardedProto string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotForwardedHost = r.Header.Get("X-Forwarded-Host")
		gotForwardedProto = r.Header.Get("X-Forwarded-Proto")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	serverPort := freePort(t)
	cancel := newServer(t, &server.Config{
		Port:        serverPort,
		Token:       testToken,
		BaseDomain:  "localhost",
		RoutingMode: "path",
	})
	defer cancel()

	c := connectClient(t, serverPort, backendPort(t, backend), "fwd-test")
	defer c.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/fwd-test/", serverPort))
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	resp.Body.Close()

	if gotForwardedProto != "https" {
		t.Errorf("X-Forwarded-Proto = %q, want %q", gotForwardedProto, "https")
	}
	if gotForwardedHost == "" {
		t.Error("X-Forwarded-Host is empty, want non-empty")
	}
}

// --- Authentication & Conflict Tests ---

func TestTunnelAuthenticationFailure(t *testing.T) {
	serverPort := freePort(t)
	cancel := newServer(t, &server.Config{
		Port:        serverPort,
		Token:       testToken,
		BaseDomain:  "localhost",
		RoutingMode: "path",
	})
	defer cancel()

	cfg := &client.Config{
		ServerURL: fmt.Sprintf("http://127.0.0.1:%d", serverPort),
		Token:     "wrong-token",
		Subdomain: "auth-fail",
		LocalPort: 9999,
		LocalHost: "127.0.0.1",
	}
	c, err := client.New(cfg)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	c.SetQuietMode(true)

	err = c.Connect(context.Background())
	if err == nil {
		c.Close()
		t.Fatal("expected auth error, got nil")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("error = %q, want it to contain 'authentication failed'", err)
	}
}

func TestTunnelSubdomainConflict(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	serverPort := freePort(t)
	cancel := newServer(t, &server.Config{
		Port:        serverPort,
		Token:       testToken,
		BaseDomain:  "localhost",
		RoutingMode: "path",
	})
	defer cancel()

	c1 := connectClient(t, serverPort, backendPort(t, backend), "conflict-test")
	defer c1.Close()

	// Second client with same subdomain should fail
	cfg := &client.Config{
		ServerURL: fmt.Sprintf("http://127.0.0.1:%d", serverPort),
		Token:     testToken,
		Subdomain: "conflict-test",
		LocalPort: backendPort(t, backend),
		LocalHost: "127.0.0.1",
	}
	c2, err := client.New(cfg)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	c2.SetQuietMode(true)

	err = c2.Connect(context.Background())
	if err == nil {
		c2.Close()
		t.Fatal("expected conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error = %q, want it to contain 'already in use'", err)
	}
}

// --- Lifecycle Tests ---

func TestTunnelDisconnectCleanup(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer backend.Close()

	serverPort := freePort(t)
	cancel := newServer(t, &server.Config{
		Port:        serverPort,
		Token:       testToken,
		BaseDomain:  "localhost",
		RoutingMode: "path",
	})
	defer cancel()

	c := connectClient(t, serverPort, backendPort(t, backend), "cleanup-test")

	// Verify tunnel works
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/cleanup-test/", serverPort))
	if err != nil {
		t.Fatalf("request before disconnect: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status before disconnect = %d, want 200", resp.StatusCode)
	}

	// Disconnect client
	c.Close()
	time.Sleep(500 * time.Millisecond)

	// Tunnel should be gone
	resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%d/cleanup-test/", serverPort))
	if err != nil {
		t.Fatalf("request after disconnect: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status after disconnect = %d, want 404", resp.StatusCode)
	}
}

// --- TCP Tunnel Tests ---

func TestTCPTunnelRoundTrip(t *testing.T) {
	// Start a local TCP echo server
	tcpBackend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tcp listen: %v", err)
	}
	defer tcpBackend.Close()
	tcpBackendPort := tcpBackend.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := tcpBackend.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()

	serverPort := freePort(t)
	tcpPortStart := freePort(t)
	cancel := newServer(t, &server.Config{
		Port:         serverPort,
		Token:        testToken,
		BaseDomain:   "localhost",
		RoutingMode:  "path",
		TCPPortStart: tcpPortStart,
		TCPPortEnd:   tcpPortStart + 10,
	})
	defer cancel()

	c := connectClient(t, serverPort, tcpBackendPort, "tcp-test", func(cfg *client.Config) {
		cfg.TunnelType = "tcp"
	})
	defer c.Close()

	// Parse the remote TCP port from public URL (format: tcp://localhost:PORT)
	publicURL := c.PublicURL()
	var remotePort int
	fmt.Sscanf(publicURL, "tcp://localhost:%d", &remotePort)
	if remotePort == 0 {
		t.Fatalf("failed to parse remote port from %q", publicURL)
	}

	// Connect to the tunnel's TCP port
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", remotePort), 5*time.Second)
	if err != nil {
		t.Fatalf("tcp dial: %v", err)
	}
	defer conn.Close()

	msg := "hello tcp tunnel"
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatalf("tcp write: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len(msg))
	n, err := io.ReadFull(conn, buf)
	if err != nil {
		t.Fatalf("tcp read: %v", err)
	}
	if string(buf[:n]) != msg {
		t.Errorf("tcp response = %q, want %q", buf[:n], msg)
	}
}

// --- Server Endpoint Tests ---

func TestHealthEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	serverPort := freePort(t)
	cancel := newServer(t, &server.Config{
		Port:        serverPort,
		Token:       testToken,
		BaseDomain:  "localhost",
		RoutingMode: "path",
		Version:     "test-1.0",
	})
	defer cancel()

	// Health before any tunnels
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/_health", serverPort))
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	var health map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&health)
	resp.Body.Close()

	if health["status"] != "ok" {
		t.Errorf("status = %v, want ok", health["status"])
	}
	if health["version"] != "test-1.0" {
		t.Errorf("version = %v, want test-1.0", health["version"])
	}
	if tunnels, ok := health["active_tunnels"].(float64); !ok || tunnels != 0 {
		t.Errorf("active_tunnels = %v, want 0", health["active_tunnels"])
	}

	// Connect a client and check again
	c := connectClient(t, serverPort, backendPort(t, backend), "health-test")
	defer c.Close()

	resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%d/_health", serverPort))
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	json.NewDecoder(resp.Body).Decode(&health)
	resp.Body.Close()

	if tunnels, ok := health["active_tunnels"].(float64); !ok || tunnels != 1 {
		t.Errorf("active_tunnels = %v, want 1", health["active_tunnels"])
	}
}

func TestConfigEndpoint(t *testing.T) {
	serverPort := freePort(t)
	cancel := newServer(t, &server.Config{
		Port:        serverPort,
		Token:       testToken,
		BaseDomain:  "tunnel.example.com",
		RoutingMode: "subdomain",
	})
	defer cancel()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/_config", serverPort))
	if err != nil {
		t.Fatalf("config request: %v", err)
	}
	defer resp.Body.Close()

	var config map[string]string
	json.NewDecoder(resp.Body).Decode(&config)

	if config["routing_mode"] != "subdomain" {
		t.Errorf("routing_mode = %q, want %q", config["routing_mode"], "subdomain")
	}
	if config["base_domain"] != "tunnel.example.com" {
		t.Errorf("base_domain = %q, want %q", config["base_domain"], "tunnel.example.com")
	}
}

// --- Rate Limiting Tests ---

func TestRateLimiting(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer backend.Close()

	serverPort := freePort(t)
	cancel := newServer(t, &server.Config{
		Port:        serverPort,
		Token:       testToken,
		BaseDomain:  "localhost",
		RoutingMode: "path",
		RateLimit:   1, // 1 req/min, burst=1
	})
	defer cancel()

	c := connectClient(t, serverPort, backendPort(t, backend), "rate-test")
	defer c.Close()

	// First request should succeed (uses burst token)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/rate-test/", serverPort))
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("first request status = %d, want 200", resp.StatusCode)
	}

	// Second request immediately should be rate limited
	resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%d/rate-test/", serverPort))
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("second request status = %d, want 429", resp.StatusCode)
	}
}
