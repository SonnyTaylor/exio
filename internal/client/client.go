// Package client contains the internal client implementation for Exio.
package client

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sonnytaylor/exio/pkg/auth"
	"github.com/sonnytaylor/exio/pkg/protocol"
	"github.com/sonnytaylor/exio/pkg/transport"
)

// Config holds the client configuration.
type Config struct {
	ServerURL   string
	Token       string
	Subdomain   string
	LocalPort   int
	LocalHost   string
	RewriteHost bool
	TunnelType  string // "http" or "tcp"
	BasicAuth   string // "user:pass" for HTTP basic auth protection
}

// ServerConfig holds the server's configuration returned from /_config endpoint.
type ServerConfig struct {
	RoutingMode string `json:"routing_mode"`
	BaseDomain  string `json:"base_domain"`
}

// Client is the Exio tunneling client (exio).
type Client struct {
	config        *Config
	serverConfig  *ServerConfig
	authenticator *auth.Authenticator
	session       *transport.Session
	logger        *log.Logger
	publicURL     string
	remotePort    int // For TCP tunnels
	requestCount  atomic.Int64
	bytesIn       atomic.Int64
	bytesOut      atomic.Int64
	connectedAt   time.Time
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	mu            sync.RWMutex
	quietMode     bool

	// Track active connections for graceful shutdown
	activeConns   map[net.Conn]struct{}
	activeConnsMu sync.Mutex

	// Callbacks for UI updates
	OnConnect    func(publicURL string)
	OnDisconnect func(err error)
	OnRequest    func(log protocol.RequestLog)
}

// New creates a new Exio client with the given configuration.
func New(config *Config) (*Client, error) {
	authenticator, err := auth.NewAuthenticator(config.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to create authenticator: %w", err)
	}

	if config.LocalHost == "" {
		config.LocalHost = "127.0.0.1"
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Client{
		config:        config,
		authenticator: authenticator,
		logger:        log.New(os.Stdout, "[exio] ", log.LstdFlags|log.Lmsgprefix),
		ctx:           ctx,
		cancel:        cancel,
		activeConns:   make(map[net.Conn]struct{}),
	}, nil
}

// fetchServerConfig queries the server's /_config endpoint to get routing mode.
func (c *Client) fetchServerConfig(ctx context.Context) error {
	configURL, err := url.Parse(c.config.ServerURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	configURL.Path = "/_config"

	req, err := http.NewRequestWithContext(ctx, "GET", configURL.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to create config request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// If we can't reach the config endpoint, assume subdomain mode for backward compatibility
		c.logger.Printf("Warning: Could not fetch server config, assuming subdomain mode: %v", err)
		c.serverConfig = &ServerConfig{
			RoutingMode: protocol.RoutingModeSubdomain,
			BaseDomain:  extractBaseDomain(c.config.ServerURL),
		}
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Older servers may not have /_config endpoint
		c.logger.Printf("Warning: Server config endpoint returned %d, assuming subdomain mode", resp.StatusCode)
		c.serverConfig = &ServerConfig{
			RoutingMode: protocol.RoutingModeSubdomain,
			BaseDomain:  extractBaseDomain(c.config.ServerURL),
		}
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read config response: %w", err)
	}

	var serverConfig ServerConfig
	if err := json.Unmarshal(body, &serverConfig); err != nil {
		return fmt.Errorf("failed to parse config response: %w", err)
	}

	c.serverConfig = &serverConfig
	return nil
}

// Connect establishes a tunnel connection to the server.
func (c *Client) Connect(ctx context.Context) error {
	// First, fetch server configuration to determine routing mode
	if err := c.fetchServerConfig(ctx); err != nil {
		return fmt.Errorf("failed to fetch server config: %w", err)
	}

	// Build the WebSocket URL
	serverURL, err := url.Parse(c.config.ServerURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	// Convert http(s) to ws(s)
	switch serverURL.Scheme {
	case "http":
		serverURL.Scheme = "ws"
	case "https":
		serverURL.Scheme = "wss"
	}

	serverURL.Path = protocol.ConnectPath
	q := serverURL.Query()
	q.Set(protocol.SubdomainQueryParam, c.config.Subdomain)

	// Set tunnel type (default to HTTP)
	tunnelType := c.config.TunnelType
	if tunnelType == "" {
		tunnelType = protocol.TunnelTypeHTTP
	}
	q.Set(protocol.TunnelTypeQueryParam, tunnelType)
	serverURL.RawQuery = q.Encode()

	c.logger.Printf("Connecting to %s", serverURL.String())

	// Create WebSocket dialer with auth header
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	header := http.Header{}
	header.Set(auth.AuthorizationHeader, c.authenticator.AuthorizationHeaderValue())

	// Connect with exponential backoff
	var wsConn *websocket.Conn
	var wsResp *http.Response
	delay := protocol.InitialReconnectDelay

	for {
		var resp *http.Response
		wsConn, resp, err = dialer.DialContext(ctx, serverURL.String(), header)
		if err == nil {
			wsResp = resp
			break
		}

		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return fmt.Errorf("authentication failed: invalid token")
			case http.StatusConflict:
				return fmt.Errorf("subdomain '%s' is already in use", c.config.Subdomain)
			case http.StatusBadRequest:
				return fmt.Errorf("invalid subdomain format")
			case http.StatusServiceUnavailable:
				return fmt.Errorf("no available TCP ports on server")
			}
		}

		c.logger.Printf("Connection failed: %v, retrying in %v", err, delay)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		// Exponential backoff
		delay = delay * 2
		if delay > protocol.MaxReconnectDelay {
			delay = protocol.MaxReconnectDelay
		}
	}

	// For TCP tunnels, get the assigned port from response headers
	var tcpPort int
	if tunnelType == protocol.TunnelTypeTCP && wsResp != nil {
		portStr := wsResp.Header.Get("X-Exio-TCP-Port")
		if portStr != "" {
			fmt.Sscanf(portStr, "%d", &tcpPort)
		}
	}

	// Create yamux session
	session, err := transport.NewClientSession(wsConn, c.config.Subdomain)
	if err != nil {
		wsConn.Close()
		return fmt.Errorf("failed to create session: %w", err)
	}

	c.mu.Lock()
	c.session = session
	c.connectedAt = time.Now()
	c.remotePort = tcpPort
	c.mu.Unlock()

	// Build public URL based on tunnel type and server's routing mode
	if tunnelType == protocol.TunnelTypeTCP {
		c.publicURL = fmt.Sprintf("tcp://%s:%d", c.serverConfig.BaseDomain, tcpPort)
	} else if c.serverConfig.RoutingMode == protocol.RoutingModePath {
		c.publicURL = fmt.Sprintf("https://%s/%s/", c.serverConfig.BaseDomain, c.config.Subdomain)
	} else {
		c.publicURL = fmt.Sprintf("https://%s.%s", c.config.Subdomain, c.serverConfig.BaseDomain)
	}

	if !c.quietMode {
		c.logger.Printf("Tunnel established!")
		c.logger.Printf("Public URL: %s", c.publicURL)
		c.logger.Printf("Forwarding to: %s:%d", c.config.LocalHost, c.config.LocalPort)
	}

	if c.OnConnect != nil {
		c.OnConnect(c.publicURL)
	}

	return nil
}

// Run starts accepting and forwarding traffic. Blocks until disconnected.
func (c *Client) Run(ctx context.Context) error {
	c.mu.RLock()
	session := c.session
	c.mu.RUnlock()

	if session == nil {
		return fmt.Errorf("not connected")
	}

	// Start the heartbeat goroutine
	c.wg.Add(1)
	go c.heartbeat()

	// Determine if we're handling TCP or HTTP
	isTCP := c.config.TunnelType == protocol.TunnelTypeTCP

	// Accept incoming streams
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			// Check if we're shutting down
			select {
			case <-c.ctx.Done():
				break
			default:
			}
			if session.IsClosed() {
				break
			}
			c.logger.Printf("Failed to accept stream: %v", err)
			continue
		}

		c.wg.Add(1)
		if isTCP {
			go c.handleTCPStream(stream)
		} else {
			go c.handleStream(stream)
		}
	}

	// Wait for all handlers to complete with a timeout
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All handlers completed cleanly
	case <-time.After(5 * time.Second):
		// Timeout - force close any remaining connections
		if !c.quietMode {
			c.logger.Printf("Shutdown timeout, forcing close...")
		}
		c.closeAllConns()
	}

	if c.OnDisconnect != nil {
		c.OnDisconnect(nil)
	}

	return nil
}

// handleStream handles an incoming stream from the server.
func (c *Client) handleStream(stream net.Conn) {
	defer c.wg.Done()
	defer stream.Close()

	// Track this stream for graceful shutdown
	c.trackConn(stream)
	defer c.untrackConn(stream)

	startTime := time.Now()

	// Check if we're shutting down
	select {
	case <-c.ctx.Done():
		return
	default:
	}

	// Read the HTTP request from the stream
	reader := bufio.NewReader(stream)
	req, err := http.ReadRequest(reader)
	if err != nil {
		// Don't log errors during shutdown
		select {
		case <-c.ctx.Done():
			return
		default:
			c.logger.Printf("Failed to read request: %v", err)
			return
		}
	}

	// Log the request (unless quiet mode is enabled)
	if !c.quietMode {
		c.logger.Printf("%s %s", req.Method, req.URL.Path)
	}

	// Validate Basic Auth if configured
	if c.config.BasicAuth != "" {
		if !c.validateBasicAuth(req) {
			c.sendUnauthorizedResponse(stream)
			return
		}
	}

	// Rewrite the Host header if configured
	originalHost := req.Host
	if c.config.RewriteHost {
		req.Host = fmt.Sprintf("%s:%d", c.config.LocalHost, c.config.LocalPort)
		req.Header.Set("Host", req.Host)
	}

	// Also set X-Forwarded headers
	req.Header.Set("X-Forwarded-Host", originalHost)
	req.Header.Set("X-Forwarded-Proto", "https")

	// Connect to the local service with context-aware dialing
	localAddr := net.JoinHostPort(c.config.LocalHost, fmt.Sprintf("%d", c.config.LocalPort))
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	localConn, err := dialer.DialContext(c.ctx, "tcp", localAddr)
	if err != nil {
		select {
		case <-c.ctx.Done():
			return
		default:
			c.logger.Printf("Failed to connect to local service: %v", err)
			c.sendErrorResponse(stream, http.StatusBadGateway, "Failed to connect to local service")
			return
		}
	}
	defer localConn.Close()

	// Track local connection for graceful shutdown
	c.trackConn(localConn)
	defer c.untrackConn(localConn)

	// Check if any buffered data exists
	buffered := reader.Buffered()
	if buffered > 0 {
		// Read buffered data and prepend to request body
		buf := make([]byte, buffered)
		reader.Read(buf)
		// We need to handle this buffered data properly
		// For now, we'll write it after the request
	}

	// Forward the request to the local service
	if err := req.Write(localConn); err != nil {
		select {
		case <-c.ctx.Done():
			return
		default:
			c.logger.Printf("Failed to write request to local service: %v", err)
			c.sendErrorResponse(stream, http.StatusBadGateway, "Failed to forward request")
			return
		}
	}

	// Read the response from the local service
	resp, err := http.ReadResponse(bufio.NewReader(localConn), req)
	if err != nil {
		select {
		case <-c.ctx.Done():
			return
		default:
			c.logger.Printf("Failed to read response from local service: %v", err)
			c.sendErrorResponse(stream, http.StatusBadGateway, "Failed to read response")
			return
		}
	}
	defer resp.Body.Close()

	// Wrap stream with counting writer to track bytes out
	countingStream := NewCountingWriter(stream, &c.bytesOut)

	// Write the response back to the stream
	if err := resp.Write(countingStream); err != nil {
		select {
		case <-c.ctx.Done():
			return
		default:
			c.logger.Printf("Failed to write response to stream: %v", err)
			return
		}
	}

	duration := time.Since(startTime)
	c.requestCount.Add(1)

	// Track request bytes (approximate - based on content length if available)
	if req.ContentLength > 0 {
		c.bytesIn.Add(req.ContentLength)
	}

	// Notify UI of request
	if c.OnRequest != nil {
		c.OnRequest(protocol.RequestLog{
			Timestamp:  startTime,
			Method:     req.Method,
			Path:       req.URL.Path,
			StatusCode: resp.StatusCode,
			Duration:   duration,
			BytesOut:   resp.ContentLength,
		})
	}
}

// handleTCPStream handles an incoming TCP stream from the server (raw bridging).
func (c *Client) handleTCPStream(stream net.Conn) {
	defer c.wg.Done()
	defer stream.Close()

	// Track this stream for graceful shutdown
	c.trackConn(stream)
	defer c.untrackConn(stream)

	// Check if we're shutting down
	select {
	case <-c.ctx.Done():
		return
	default:
	}

	// Connect to the local service
	localAddr := net.JoinHostPort(c.config.LocalHost, fmt.Sprintf("%d", c.config.LocalPort))
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	localConn, err := dialer.DialContext(c.ctx, "tcp", localAddr)
	if err != nil {
		select {
		case <-c.ctx.Done():
			return
		default:
			c.logger.Printf("Failed to connect to local service: %v", err)
			return
		}
	}
	defer localConn.Close()

	// Track local connection for graceful shutdown
	c.trackConn(localConn)
	defer c.untrackConn(localConn)

	c.requestCount.Add(1)

	// Log the connection (unless quiet mode)
	if !c.quietMode {
		c.logger.Printf("TCP connection bridged to %s", localAddr)
	}

	// Bidirectional copy
	var wg sync.WaitGroup
	wg.Add(2)

	// Stream -> Local
	go func() {
		defer wg.Done()
		written, _ := io.Copy(localConn, stream)
		c.bytesIn.Add(written)
	}()

	// Local -> Stream
	go func() {
		defer wg.Done()
		written, _ := io.Copy(stream, localConn)
		c.bytesOut.Add(written)
	}()

	wg.Wait()
}

// sendErrorResponse sends an HTTP error response to the stream.
func (c *Client) sendErrorResponse(stream net.Conn, statusCode int, message string) {
	resp := &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
	}
	resp.Header.Set("Content-Type", "text/plain")
	resp.Write(stream)
	stream.Write([]byte(message))
}

// validateBasicAuth checks if the request has valid Basic Auth credentials.
func (c *Client) validateBasicAuth(req *http.Request) bool {
	authHeader := req.Header.Get("Authorization")
	if authHeader == "" {
		return false
	}

	if !strings.HasPrefix(authHeader, "Basic ") {
		return false
	}

	encoded := strings.TrimPrefix(authHeader, "Basic ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}

	return string(decoded) == c.config.BasicAuth
}

// sendUnauthorizedResponse sends a 401 Unauthorized response with WWW-Authenticate header.
func (c *Client) sendUnauthorizedResponse(stream net.Conn) {
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Status:     http.StatusText(http.StatusUnauthorized),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
	}
	resp.Header.Set("Content-Type", "text/plain")
	resp.Header.Set("WWW-Authenticate", `Basic realm="Exio Tunnel"`)
	resp.Write(stream)
	stream.Write([]byte("Unauthorized"))
}

// heartbeat sends periodic pings to keep the connection alive.
func (c *Client) heartbeat() {
	defer c.wg.Done()

	ticker := time.NewTicker(protocol.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.mu.RLock()
			session := c.session
			c.mu.RUnlock()

			if session == nil || session.IsClosed() {
				return
			}
			// Yamux handles keep-alive internally, but we keep this for monitoring
		}
	}
}

// trackConn adds a connection to the active connections map.
func (c *Client) trackConn(conn net.Conn) {
	c.activeConnsMu.Lock()
	c.activeConns[conn] = struct{}{}
	c.activeConnsMu.Unlock()
}

// untrackConn removes a connection from the active connections map.
func (c *Client) untrackConn(conn net.Conn) {
	c.activeConnsMu.Lock()
	delete(c.activeConns, conn)
	c.activeConnsMu.Unlock()
}

// closeAllConns closes all tracked connections to unblock pending I/O.
func (c *Client) closeAllConns() {
	c.activeConnsMu.Lock()
	defer c.activeConnsMu.Unlock()

	for conn := range c.activeConns {
		conn.Close()
	}
	c.activeConns = make(map[net.Conn]struct{})
}

// Close closes the client connection.
func (c *Client) Close() error {
	c.cancel()

	// Close all active connections to unblock any pending I/O operations
	c.closeAllConns()

	c.mu.Lock()
	session := c.session
	c.session = nil
	c.mu.Unlock()

	if session != nil {
		return session.Close()
	}
	return nil
}

// PublicURL returns the public URL of the tunnel.
func (c *Client) PublicURL() string {
	return c.publicURL
}

// Config returns the client configuration.
func (c *Client) Config() *Config {
	return c.config
}

// SetQuietMode enables or disables quiet mode (suppresses default log output).
func (c *Client) SetQuietMode(quiet bool) {
	c.quietMode = quiet
}

// Stats returns tunnel statistics.
func (c *Client) Stats() (requestCount int64, bytesIn int64, bytesOut int64, connectedAt time.Time) {
	return c.requestCount.Load(), c.bytesIn.Load(), c.bytesOut.Load(), c.connectedAt
}

// extractBaseDomain extracts the base domain from a server URL.
func extractBaseDomain(serverURL string) string {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "localhost"
	}

	host := u.Host
	// Remove port if present
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			host = host[:i]
			break
		}
	}

	return host
}
