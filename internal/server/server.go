package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sonnytaylor/exio/pkg/auth"
	"github.com/sonnytaylor/exio/pkg/logging"
	"github.com/sonnytaylor/exio/pkg/protocol"
	"github.com/sonnytaylor/exio/pkg/transport"
	"golang.org/x/time/rate"
)

// Config holds the server configuration.
type Config struct {
	Port         int
	Token        string
	BaseDomain   string
	RoutingMode  string // "path" or "subdomain"
	TCPPortStart int    // Start of TCP port allocation range
	TCPPortEnd   int    // End of TCP port allocation range
	RateLimit    int    // Requests per minute (0 = unlimited)
	Version      string // Server version for health endpoint
	LogFormat    string // "json" or "text" (default: "text")
}

// ConfigFromEnv creates a config from environment variables.
func ConfigFromEnv() *Config {
	port := 8080
	if p := os.Getenv("EXIO_PORT"); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}

	routingMode := os.Getenv("EXIO_ROUTING_MODE")
	if routingMode == "" {
		routingMode = protocol.RoutingModePath // Default to path-based routing
	}

	tcpPortStart := protocol.DefaultTCPPortStart
	if p := os.Getenv("EXIO_TCP_PORT_START"); p != "" {
		fmt.Sscanf(p, "%d", &tcpPortStart)
	}

	tcpPortEnd := protocol.DefaultTCPPortEnd
	if p := os.Getenv("EXIO_TCP_PORT_END"); p != "" {
		fmt.Sscanf(p, "%d", &tcpPortEnd)
	}

	rateLimit := 0
	if r := os.Getenv("EXIO_RATE_LIMIT"); r != "" {
		fmt.Sscanf(r, "%d", &rateLimit)
	}

	return &Config{
		Port:         port,
		Token:        os.Getenv("EXIO_TOKEN"),
		BaseDomain:   os.Getenv("EXIO_BASE_DOMAIN"),
		RoutingMode:  routingMode,
		TCPPortStart: tcpPortStart,
		TCPPortEnd:   tcpPortEnd,
		RateLimit:    rateLimit,
	}
}

// Server is the Exio tunneling server (exiod).
type Server struct {
	config        *Config
	registry      *SessionRegistry
	authenticator *auth.Authenticator
	httpServer    *http.Server
	logger        *logging.Logger
	wg            sync.WaitGroup
	startedAt     time.Time
	totalRequests atomic.Int64
}

// New creates a new Exio server with the given configuration.
func New(config *Config) (*Server, error) {
	authenticator, err := auth.NewAuthenticator(config.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to create authenticator: %w", err)
	}

	s := &Server{
		config:        config,
		registry:      NewSessionRegistry(config.TCPPortStart, config.TCPPortEnd),
		authenticator: authenticator,
		logger:        logging.New(os.Stdout, "[exiod] ", config.LogFormat == "json"),
		startedAt:     time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc(protocol.ConnectPath, s.handleConnect)
	mux.HandleFunc("/_config", s.handleConfig)
	mux.HandleFunc("/_health", s.handleHealth)
	mux.HandleFunc("/_metrics", s.handleMetrics)
	mux.HandleFunc("/", s.handleRequest)

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", config.Port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return s, nil
}

// Run starts the server and blocks until shutdown.
func (s *Server) Run(ctx context.Context) error {
	// Setup signal handling - buffered channel to not miss signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create a cancellable context for shutdown coordination
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start HTTP server
	serverErrChan := make(chan error, 1)
	go func() {
		s.logger.Info("Starting server", "port", s.config.Port)
		s.logger.Info("Base domain configured", "domain", s.config.BaseDomain)
		s.logger.Info("Routing mode configured", "mode", s.config.RoutingMode)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErrChan <- err
		}
		close(serverErrChan)
	}()

	// Wait for shutdown signal or server error
	var shutdownReason string
	select {
	case sig := <-sigChan:
		shutdownReason = fmt.Sprintf("Received signal %v", sig)
	case <-ctx.Done():
		shutdownReason = "Context cancelled"
	case err := <-serverErrChan:
		if err != nil {
			return fmt.Errorf("HTTP server error: %w", err)
		}
		return nil
	}

	s.logger.Info("Shutting down gracefully", "reason", shutdownReason)
	cancel() // Cancel context to signal all goroutines

	// Start graceful shutdown in background
	shutdownComplete := make(chan error, 1)
	go func() {
		shutdownComplete <- s.Shutdown()
	}()

	// Wait for graceful shutdown or force quit
	select {
	case err := <-shutdownComplete:
		return err
	case <-sigChan:
		s.logger.Warn("Forced shutdown requested")
		// Force close everything
		s.registry.CloseAll()
		s.httpServer.Close() // Force close, not graceful
		return nil
	}
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	activeTunnels := s.registry.Count()
	if activeTunnels > 0 {
		s.logger.Info("Closing active tunnels", "count", activeTunnels)
	}

	// Close all active sessions first to stop accepting new streams
	s.registry.CloseAll()

	// Shutdown HTTP server with timeout
	s.logger.Info("Stopping HTTP server")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		s.logger.Error("HTTP server shutdown error", "error", err)
		// Force close if graceful shutdown fails
		s.httpServer.Close()
	}

	// Wait for goroutines with timeout
	s.logger.Info("Waiting for connections to finish")
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("Server shutdown complete")
	case <-time.After(5 * time.Second):
		s.logger.Warn("Shutdown timeout, some connections may not have finished cleanly")
	}

	return nil
}

// handleConnect handles the WebSocket upgrade for new tunnel connections.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	// Authenticate the request
	if err := s.authenticator.ValidateRequest(r); err != nil {
		s.logger.Warn("Authentication failed", "error", err)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Get requested subdomain
	subdomain := r.URL.Query().Get(protocol.SubdomainQueryParam)
	if subdomain == "" {
		http.Error(w, "Missing subdomain parameter", http.StatusBadRequest)
		return
	}

	subdomain = strings.ToLower(subdomain)

	// Get tunnel type (default to HTTP)
	tunnelType := r.URL.Query().Get(protocol.TunnelTypeQueryParam)
	if tunnelType == "" {
		tunnelType = protocol.TunnelTypeHTTP
	}

	// Validate tunnel type
	if tunnelType != protocol.TunnelTypeHTTP && tunnelType != protocol.TunnelTypeTCP {
		http.Error(w, "Invalid tunnel type", http.StatusBadRequest)
		return
	}

	// Validate subdomain format
	if err := ValidateSubdomain(subdomain); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if subdomain is available
	if s.registry.Exists(subdomain) {
		http.Error(w, "Subdomain already in use", http.StatusConflict)
		return
	}

	// For TCP tunnels, allocate a port
	var tcpPort int
	var tcpListener net.Listener
	if tunnelType == protocol.TunnelTypeTCP {
		var err error
		tcpPort, err = s.registry.AllocateTCPPort(subdomain)
		if err != nil {
			s.logger.Error("Failed to allocate TCP port", "error", err)
			http.Error(w, "No available TCP ports", http.StatusServiceUnavailable)
			return
		}

		// Start TCP listener
		tcpListener, err = net.Listen("tcp", fmt.Sprintf(":%d", tcpPort))
		if err != nil {
			s.logger.Error("Failed to start TCP listener", "port", tcpPort, "error", err)
			s.registry.ReleaseTCPPort(tcpPort)
			http.Error(w, "Failed to start TCP listener", http.StatusInternalServerError)
			return
		}
	}

	// Prepare response headers for WebSocket upgrade
	responseHeaders := http.Header{}
	if tunnelType == protocol.TunnelTypeTCP && tcpPort > 0 {
		responseHeaders.Set("X-Exio-TCP-Port", fmt.Sprintf("%d", tcpPort))
	}

	// Upgrade to WebSocket
	wsConn, err := transport.WebSocketUpgrader.Upgrade(w, r, responseHeaders)
	if err != nil {
		s.logger.Error("WebSocket upgrade failed", "error", err)
		if tcpListener != nil {
			tcpListener.Close()
			s.registry.ReleaseTCPPort(tcpPort)
		}
		return
	}

	// Create yamux session
	session, err := transport.NewServerSession(wsConn, subdomain)
	if err != nil {
		s.logger.Error("Failed to create session", "error", err)
		wsConn.Close()
		if tcpListener != nil {
			tcpListener.Close()
			s.registry.ReleaseTCPPort(tcpPort)
		}
		return
	}

	// Create rate limiter if configured
	var limiter *rate.Limiter
	if s.config.RateLimit > 0 {
		// Convert requests per minute to requests per second
		rps := float64(s.config.RateLimit) / 60.0
		limiter = rate.NewLimiter(rate.Limit(rps), s.config.RateLimit) // burst = rate limit
	}

	// Register the session
	if err := s.registry.RegisterWithOptions(subdomain, session, tunnelType, tcpPort, tcpListener, limiter); err != nil {
		s.logger.Error("Failed to register session", "error", err)
		session.Close()
		if tcpListener != nil {
			tcpListener.Close()
			s.registry.ReleaseTCPPort(tcpPort)
		}
		return
	}

	var publicURL string
	if tunnelType == protocol.TunnelTypeTCP {
		publicURL = fmt.Sprintf("tcp://%s:%d", s.config.BaseDomain, tcpPort)
		s.logger.Info("TCP tunnel established", "subdomain", subdomain, "port", tcpPort)
	} else if s.config.RoutingMode == protocol.RoutingModePath {
		publicURL = fmt.Sprintf("https://%s/%s/", s.config.BaseDomain, subdomain)
		s.logger.Info("HTTP tunnel established", "url", publicURL, "subdomain", subdomain)
	} else {
		publicURL = fmt.Sprintf("https://%s.%s", subdomain, s.config.BaseDomain)
		s.logger.Info("HTTP tunnel established", "url", publicURL, "subdomain", subdomain)
	}

	// Handle session lifecycle
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.registry.Unregister(subdomain)
		defer session.Close()

		// For TCP tunnels, start accepting connections
		if tunnelType == protocol.TunnelTypeTCP && tcpListener != nil {
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.handleTCPListener(tcpListener, session, subdomain)
			}()
		}

		// Wait for session to close
		<-session.Context().Done()
		s.logger.Info("Tunnel closed", "subdomain", subdomain)
	}()
}

// handleTCPListener accepts incoming TCP connections and bridges them to the tunnel.
func (s *Server) handleTCPListener(listener net.Listener, session *transport.Session, subdomain string) {
	defer listener.Close()

	for {
		// Check if session is closed before accepting
		select {
		case <-session.Context().Done():
			return
		default:
		}

		conn, err := listener.Accept()
		if err != nil {
			// Check if listener was closed (shutdown in progress)
			select {
			case <-session.Context().Done():
				return
			default:
				// Check if it's a "use of closed network connection" error
				if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
					return
				}
				s.logger.Error("TCP accept error", "subdomain", subdomain, "error", err)
				continue
			}
		}

		// Get session entry for rate limiting
		entry, err := s.registry.Get(subdomain)
		if err != nil {
			conn.Close()
			continue
		}

		// Check rate limit
		if entry.RateLimiter != nil && !entry.RateLimiter.Allow() {
			s.logger.Warn("TCP rate limit exceeded", "subdomain", subdomain)
			conn.Close()
			continue
		}

		entry.RequestCount.Add(1)
		s.totalRequests.Add(1)

		// Handle connection in goroutine
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.bridgeTCPConnection(conn, session, subdomain)
		}()
	}
}

// bridgeTCPConnection bridges a TCP connection to the tunnel.
func (s *Server) bridgeTCPConnection(conn net.Conn, session *transport.Session, subdomain string) {
	defer conn.Close()

	// Open a new stream to the client
	stream, err := session.OpenStream()
	if err != nil {
		s.logger.Error("Failed to open stream for TCP tunnel", "subdomain", subdomain, "error", err)
		return
	}
	defer stream.Close()

	// Bidirectional copy
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(stream, conn)
	}()

	go func() {
		defer wg.Done()
		io.Copy(conn, stream)
	}()

	wg.Wait()
}

// handleRequest handles incoming HTTP requests and routes them to the appropriate tunnel.
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	var tunnelID string
	var fromPath bool

	if s.config.RoutingMode == protocol.RoutingModePath {
		// Extract tunnel ID from the first path segment
		tunnelID = protocol.ExtractTunnelIDFromPath(r.URL.Path)
		if tunnelID != "" && s.registry.Exists(tunnelID) {
			fromPath = true
		}

		// If no valid tunnel ID in path, try cookie
		if !fromPath {
			if cookie, err := r.Cookie("exio_tunnel"); err == nil && cookie.Value != "" {
				if s.registry.Exists(cookie.Value) {
					tunnelID = cookie.Value
					s.logger.Info("Cookie routing", "path", r.URL.Path, "tunnel", tunnelID)
				}
			}
		}

		// If still no tunnel, try Referer header as fallback
		if tunnelID == "" || !s.registry.Exists(tunnelID) {
			referer := r.Header.Get("Referer")
			if referer != "" {
				refererTunnelID := protocol.ExtractTunnelIDFromReferer(referer)
				if refererTunnelID != "" && s.registry.Exists(refererTunnelID) {
					tunnelID = refererTunnelID
					s.logger.Info("Referer routing", "path", r.URL.Path, "tunnel", tunnelID)
				}
			}
		}

		if tunnelID == "" || !s.registry.Exists(tunnelID) {
			http.Error(w, "Tunnel not found", http.StatusNotFound)
			return
		}

		// Set cookie for future requests (when accessing via path with tunnel ID)
		if fromPath {
			http.SetCookie(w, &http.Cookie{
				Name:     "exio_tunnel",
				Value:    tunnelID,
				Path:     "/",
				MaxAge:   3600, // 1 hour
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		}

		// Only strip tunnel ID prefix if it was in the path
		if fromPath {
			originalPath := r.URL.Path
			r.URL.Path = protocol.StripTunnelIDPrefix(r.URL.Path, tunnelID)
			r.RequestURI = r.URL.RequestURI()

			if r.URL.RawPath != "" {
				r.URL.RawPath = protocol.StripTunnelIDPrefix(r.URL.RawPath, tunnelID)
			}

			s.logger.Info("Path routing", "from", originalPath, "to", r.URL.Path, "tunnel", tunnelID)
		}
	} else {
		// Extract subdomain from Host header (existing behavior)
		tunnelID = protocol.ExtractSubdomain(r, s.config.BaseDomain)
		if tunnelID == "" {
			http.Error(w, "Invalid host", http.StatusNotFound)
			return
		}
	}

	// Look up session
	entry, err := s.registry.Get(tunnelID)
	if err != nil {
		http.Error(w, "Tunnel not found", http.StatusNotFound)
		return
	}

	// Check rate limit
	if entry.RateLimiter != nil && !entry.RateLimiter.Allow() {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}

	entry.RequestCount.Add(1)
	s.totalRequests.Add(1)

	// Open a new stream to the client
	stream, err := entry.Session.OpenStream()
	if err != nil {
		s.logger.Error("Failed to open stream", "tunnel", tunnelID, "error", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer stream.Close()

	// Write the HTTP request to the stream
	if err := r.Write(stream); err != nil {
		s.logger.Error("Failed to write request to stream", "error", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	// Read the response from the stream
	resp, err := http.ReadResponse(bufio.NewReader(stream), r)
	if err != nil {
		s.logger.Error("Failed to read response from stream", "error", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	io.Copy(w, resp.Body)
}

// handleHijackedRequest handles requests that may need connection hijacking (WebSocket passthrough).
func (s *Server) handleHijackedRequest(w http.ResponseWriter, r *http.Request, stream net.Conn) {
	// Check if this is a WebSocket upgrade request
	if r.Header.Get("Upgrade") == "websocket" {
		s.handleWebSocketPassthrough(w, r, stream)
		return
	}

	// For non-WebSocket requests, use the standard request handling
	// Write the HTTP request to the stream
	if err := r.Write(stream); err != nil {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	// Read and forward the response
	resp, err := http.ReadResponse(bufio.NewReader(stream), r)
	if err != nil {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// handleWebSocketPassthrough handles WebSocket upgrade requests through the tunnel.
func (s *Server) handleWebSocketPassthrough(w http.ResponseWriter, r *http.Request, stream net.Conn) {
	// Hijack the connection for bidirectional streaming
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "WebSocket passthrough not supported", http.StatusInternalServerError)
		return
	}

	conn, buf, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "Failed to hijack connection", http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	// Write the original request to the stream
	if err := r.Write(stream); err != nil {
		return
	}

	// Flush any buffered data
	if buf.Reader.Buffered() > 0 {
		buffered := make([]byte, buf.Reader.Buffered())
		buf.Read(buffered)
		stream.Write(buffered)
	}

	// Bidirectional copy
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(stream, conn)
		stream.Close()
	}()

	go func() {
		defer wg.Done()
		io.Copy(conn, stream)
		conn.Close()
	}()

	wg.Wait()
}

// handleConfig returns the server configuration for clients.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"routing_mode": s.config.RoutingMode,
		"base_domain":  s.config.BaseDomain,
	})
}

// handleHealth returns server health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(s.startedAt)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"version":        s.config.Version,
		"uptime_seconds": int64(uptime.Seconds()),
		"uptime_human":   uptime.Round(time.Second).String(),
		"active_tunnels": s.registry.Count(),
		"total_requests": s.totalRequests.Load(),
		"routing_mode":   s.config.RoutingMode,
	})
}

// ActiveTunnels returns the number of active tunnel connections.
func (s *Server) ActiveTunnels() int64 {
	return s.registry.Count()
}
