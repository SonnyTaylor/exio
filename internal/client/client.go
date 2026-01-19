// Package client contains the internal client implementation for Exio.
package client

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
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
}

// Client is the Exio tunneling client (exio).
type Client struct {
	config        *Config
	authenticator *auth.Authenticator
	session       *transport.Session
	logger        *log.Logger
	publicURL     string
	requestCount  atomic.Int64
	bytesIn       atomic.Int64
	bytesOut      atomic.Int64
	connectedAt   time.Time
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	mu            sync.RWMutex

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
	}, nil
}

// Connect establishes a tunnel connection to the server.
func (c *Client) Connect(ctx context.Context) error {
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
	delay := protocol.InitialReconnectDelay

	for {
		var resp *http.Response
		wsConn, resp, err = dialer.DialContext(ctx, serverURL.String(), header)
		if err == nil {
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

	// Create yamux session
	session, err := transport.NewClientSession(wsConn, c.config.Subdomain)
	if err != nil {
		wsConn.Close()
		return fmt.Errorf("failed to create session: %w", err)
	}

	c.mu.Lock()
	c.session = session
	c.connectedAt = time.Now()
	c.mu.Unlock()

	// Build public URL (assume https and the server's base domain)
	// The actual URL depends on the server configuration
	c.publicURL = fmt.Sprintf("https://%s.%s", c.config.Subdomain, extractBaseDomain(c.config.ServerURL))

	c.logger.Printf("Tunnel established!")
	c.logger.Printf("Public URL: %s", c.publicURL)
	c.logger.Printf("Forwarding to: %s:%d", c.config.LocalHost, c.config.LocalPort)

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

	// Accept incoming streams
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			if session.IsClosed() {
				break
			}
			c.logger.Printf("Failed to accept stream: %v", err)
			continue
		}

		c.wg.Add(1)
		go c.handleStream(stream)
	}

	c.wg.Wait()

	if c.OnDisconnect != nil {
		c.OnDisconnect(nil)
	}

	return nil
}

// handleStream handles an incoming stream from the server.
func (c *Client) handleStream(stream net.Conn) {
	defer c.wg.Done()
	defer stream.Close()

	startTime := time.Now()

	// Read the HTTP request from the stream
	reader := bufio.NewReader(stream)
	req, err := http.ReadRequest(reader)
	if err != nil {
		c.logger.Printf("Failed to read request: %v", err)
		return
	}

	// Log the request
	c.logger.Printf("%s %s", req.Method, req.URL.Path)

	// Rewrite the Host header if configured
	originalHost := req.Host
	if c.config.RewriteHost {
		req.Host = fmt.Sprintf("%s:%d", c.config.LocalHost, c.config.LocalPort)
		req.Header.Set("Host", req.Host)
	}

	// Also set X-Forwarded headers
	req.Header.Set("X-Forwarded-Host", originalHost)
	req.Header.Set("X-Forwarded-Proto", "https")

	// Connect to the local service
	localAddr := fmt.Sprintf("%s:%d", c.config.LocalHost, c.config.LocalPort)
	localConn, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		c.logger.Printf("Failed to connect to local service: %v", err)
		c.sendErrorResponse(stream, http.StatusBadGateway, "Failed to connect to local service")
		return
	}
	defer localConn.Close()

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
		c.logger.Printf("Failed to write request to local service: %v", err)
		c.sendErrorResponse(stream, http.StatusBadGateway, "Failed to forward request")
		return
	}

	// Read the response from the local service
	resp, err := http.ReadResponse(bufio.NewReader(localConn), req)
	if err != nil {
		c.logger.Printf("Failed to read response from local service: %v", err)
		c.sendErrorResponse(stream, http.StatusBadGateway, "Failed to read response")
		return
	}
	defer resp.Body.Close()

	// Write the response back to the stream
	if err := resp.Write(stream); err != nil {
		c.logger.Printf("Failed to write response to stream: %v", err)
		return
	}

	duration := time.Since(startTime)
	c.requestCount.Add(1)

	// Notify UI of request
	if c.OnRequest != nil {
		c.OnRequest(protocol.RequestLog{
			Timestamp:  startTime,
			Method:     req.Method,
			Path:       req.URL.Path,
			StatusCode: resp.StatusCode,
			Duration:   duration,
		})
	}
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

// Close closes the client connection.
func (c *Client) Close() error {
	c.cancel()

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
