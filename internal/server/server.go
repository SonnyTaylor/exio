package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sonnytaylor/exio/pkg/auth"
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
	logger        *log.Logger
	wg            sync.WaitGroup
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
		logger:        log.New(os.Stdout, "[exiod] ", log.LstdFlags|log.Lmsgprefix),
	}

	mux := http.NewServeMux()
	mux.HandleFunc(protocol.ConnectPath, s.handleConnect)
	mux.HandleFunc("/_config", s.handleConfig)
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
	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server
	go func() {
		s.logger.Printf("Starting server on port %d", s.config.Port)
		s.logger.Printf("Base domain: %s", s.config.BaseDomain)
		s.logger.Printf("Routing mode: %s", s.config.RoutingMode)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Printf("HTTP server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	select {
	case sig := <-sigChan:
		s.logger.Printf("Received signal %v, shutting down...", sig)
	case <-ctx.Done():
		s.logger.Printf("Context cancelled, shutting down...")
	}

	return s.Shutdown()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	// Close all active sessions
	s.registry.CloseAll()

	// Shutdown HTTP server
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("error shutting down HTTP server: %w", err)
	}

	s.wg.Wait()
	s.logger.Printf("Server shutdown complete")
	return nil
}

// handleConnect handles the WebSocket upgrade for new tunnel connections.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	// Authenticate the request
	if err := s.authenticator.ValidateRequest(r); err != nil {
		s.logger.Printf("Authentication failed: %v", err)
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
			s.logger.Printf("Failed to allocate TCP port: %v", err)
			http.Error(w, "No available TCP ports", http.StatusServiceUnavailable)
			return
		}

		// Start TCP listener
		tcpListener, err = net.Listen("tcp", fmt.Sprintf(":%d", tcpPort))
		if err != nil {
			s.logger.Printf("Failed to start TCP listener on port %d: %v", tcpPort, err)
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
		s.logger.Printf("WebSocket upgrade failed: %v", err)
		if tcpListener != nil {
			tcpListener.Close()
			s.registry.ReleaseTCPPort(tcpPort)
		}
		return
	}

	// Create yamux session
	session, err := transport.NewServerSession(wsConn, subdomain)
	if err != nil {
		s.logger.Printf("Failed to create session: %v", err)
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
		s.logger.Printf("Failed to register session: %v", err)
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
		s.logger.Printf("TCP tunnel established: %s (port %d)", subdomain, tcpPort)
	} else if s.config.RoutingMode == protocol.RoutingModePath {
		publicURL = fmt.Sprintf("https://%s/%s/", s.config.BaseDomain, subdomain)
		s.logger.Printf("HTTP tunnel established: %s", publicURL)
	} else {
		publicURL = fmt.Sprintf("https://%s.%s", subdomain, s.config.BaseDomain)
		s.logger.Printf("HTTP tunnel established: %s", publicURL)
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
		s.logger.Printf("Tunnel closed: %s", subdomain)
	}()
}

// handleTCPListener accepts incoming TCP connections and bridges them to the tunnel.
func (s *Server) handleTCPListener(listener net.Listener, session *transport.Session, subdomain string) {
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			// Check if listener was closed
			select {
			case <-session.Context().Done():
				return
			default:
				s.logger.Printf("TCP accept error for %s: %v", subdomain, err)
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
			s.logger.Printf("TCP rate limit exceeded for %s", subdomain)
			conn.Close()
			continue
		}

		entry.RequestCount.Add(1)

		// Handle connection in goroutine
		go s.bridgeTCPConnection(conn, session, subdomain)
	}
}

// bridgeTCPConnection bridges a TCP connection to the tunnel.
func (s *Server) bridgeTCPConnection(conn net.Conn, session *transport.Session, subdomain string) {
	defer conn.Close()

	// Open a new stream to the client
	stream, err := session.OpenStream()
	if err != nil {
		s.logger.Printf("Failed to open stream for TCP tunnel %s: %v", subdomain, err)
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
					s.logger.Printf("Cookie routing: %s (tunnel: %s)", r.URL.Path, tunnelID)
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
					s.logger.Printf("Referer routing: %s (tunnel: %s)", r.URL.Path, tunnelID)
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

			s.logger.Printf("Path routing: %s -> %s (tunnel: %s)", originalPath, r.URL.Path, tunnelID)
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

	// Open a new stream to the client
	stream, err := entry.Session.OpenStream()
	if err != nil {
		s.logger.Printf("Failed to open stream to %s: %v", tunnelID, err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer stream.Close()

	// Write the HTTP request to the stream
	if err := r.Write(stream); err != nil {
		s.logger.Printf("Failed to write request to stream: %v", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
		return
	}

	// Read the response from the stream
	resp, err := http.ReadResponse(bufio.NewReader(stream), r)
	if err != nil {
		s.logger.Printf("Failed to read response from stream: %v", err)
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

// ActiveTunnels returns the number of active tunnel connections.
func (s *Server) ActiveTunnels() int64 {
	return s.registry.Count()
}
