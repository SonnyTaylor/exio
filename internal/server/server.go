package server

import (
	"bufio"
	"context"
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
)

// Config holds the server configuration.
type Config struct {
	Port       int
	Token      string
	BaseDomain string
}

// ConfigFromEnv creates a config from environment variables.
func ConfigFromEnv() *Config {
	port := 8080
	if p := os.Getenv("EXIO_PORT"); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}

	return &Config{
		Port:       port,
		Token:      os.Getenv("EXIO_TOKEN"),
		BaseDomain: os.Getenv("EXIO_BASE_DOMAIN"),
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
		registry:      NewSessionRegistry(),
		authenticator: authenticator,
		logger:        log.New(os.Stdout, "[exiod] ", log.LstdFlags|log.Lmsgprefix),
	}

	mux := http.NewServeMux()
	mux.HandleFunc(protocol.ConnectPath, s.handleConnect)
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

	// Upgrade to WebSocket
	wsConn, err := transport.WebSocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	// Create yamux session
	session, err := transport.NewServerSession(wsConn, subdomain)
	if err != nil {
		s.logger.Printf("Failed to create session: %v", err)
		wsConn.Close()
		return
	}

	// Register the session
	if err := s.registry.Register(subdomain, session); err != nil {
		s.logger.Printf("Failed to register session: %v", err)
		session.Close()
		return
	}

	publicURL := fmt.Sprintf("https://%s.%s", subdomain, s.config.BaseDomain)
	s.logger.Printf("Tunnel established: %s", publicURL)

	// Handle session lifecycle
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.registry.Unregister(subdomain)
		defer session.Close()

		// Wait for session to close
		<-session.Context().Done()
		s.logger.Printf("Tunnel closed: %s", subdomain)
	}()
}

// handleRequest handles incoming HTTP requests and routes them to the appropriate tunnel.
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Extract subdomain from Host header
	subdomain := protocol.ExtractSubdomain(r, s.config.BaseDomain)
	if subdomain == "" {
		http.Error(w, "Invalid host", http.StatusNotFound)
		return
	}

	// Look up session
	entry, err := s.registry.Get(subdomain)
	if err != nil {
		http.Error(w, "Tunnel not found", http.StatusNotFound)
		return
	}

	entry.RequestCount.Add(1)

	// Open a new stream to the client
	stream, err := entry.Session.OpenStream()
	if err != nil {
		s.logger.Printf("Failed to open stream to %s: %v", subdomain, err)
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

// ActiveTunnels returns the number of active tunnel connections.
func (s *Server) ActiveTunnels() int64 {
	return s.registry.Count()
}
