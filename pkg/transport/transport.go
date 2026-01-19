// Package transport provides WebSocket and Yamux transport layer for Exio.
package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/sonnytaylor/exio/pkg/protocol"
)

var (
	// ErrSessionClosed is returned when operating on a closed session.
	ErrSessionClosed = errors.New("session closed")

	// ErrConnectionFailed is returned when the WebSocket connection fails.
	ErrConnectionFailed = errors.New("websocket connection failed")
)

// WebSocketUpgrader is the default upgrader for incoming WebSocket connections.
var WebSocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  16 * 1024,
	WriteBufferSize: 16 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins (security is handled by auth token)
	},
}

// wsConn wraps a WebSocket connection to implement net.Conn interface.
type wsConn struct {
	conn       *websocket.Conn
	reader     io.Reader
	readMu     sync.Mutex
	writeMu    sync.Mutex
	localAddr  net.Addr
	remoteAddr net.Addr
}

// WrapWebSocket wraps a WebSocket connection as a net.Conn for use with yamux.
func WrapWebSocket(conn *websocket.Conn) net.Conn {
	return &wsConn{
		conn:       conn,
		localAddr:  conn.LocalAddr(),
		remoteAddr: conn.RemoteAddr(),
	}
}

func (c *wsConn) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	for {
		if c.reader == nil {
			messageType, reader, err := c.conn.NextReader()
			if err != nil {
				return 0, err
			}
			if messageType != websocket.BinaryMessage {
				continue // Skip non-binary messages
			}
			c.reader = reader
		}

		n, err := c.reader.Read(b)
		if err == io.EOF {
			c.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (c *wsConn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	err := c.conn.WriteMessage(websocket.BinaryMessage, b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *wsConn) Close() error {
	return c.conn.Close()
}

func (c *wsConn) LocalAddr() net.Addr {
	return c.localAddr
}

func (c *wsConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.conn.SetReadDeadline(t); err != nil {
		return err
	}
	return c.conn.SetWriteDeadline(t)
}

func (c *wsConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (c *wsConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// Session represents a multiplexed tunnel session over WebSocket.
type Session struct {
	yamuxSession *yamux.Session
	wsConn       *websocket.Conn
	subdomain    string
	connectedAt  time.Time
	closed       bool
	closeMu      sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
}

// YamuxConfig returns the yamux configuration optimized for HTTP tunneling.
func YamuxConfig() *yamux.Config {
	config := yamux.DefaultConfig()
	config.AcceptBacklog = 256
	config.EnableKeepAlive = true
	config.KeepAliveInterval = protocol.HeartbeatInterval
	config.ConnectionWriteTimeout = protocol.WriteTimeout
	config.StreamOpenTimeout = 30 * time.Second
	config.StreamCloseTimeout = 5 * time.Minute
	config.LogOutput = io.Discard // Suppress yamux internal logging
	return config
}

// NewServerSession creates a new server-side session from a WebSocket connection.
func NewServerSession(wsConn *websocket.Conn, subdomain string) (*Session, error) {
	netConn := WrapWebSocket(wsConn)
	yamuxSession, err := yamux.Server(netConn, YamuxConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create yamux server: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Session{
		yamuxSession: yamuxSession,
		wsConn:       wsConn,
		subdomain:    subdomain,
		connectedAt:  time.Now(),
		ctx:          ctx,
		cancel:       cancel,
	}, nil
}

// NewClientSession creates a new client-side session from a WebSocket connection.
func NewClientSession(wsConn *websocket.Conn, subdomain string) (*Session, error) {
	netConn := WrapWebSocket(wsConn)
	yamuxSession, err := yamux.Client(netConn, YamuxConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create yamux client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Session{
		yamuxSession: yamuxSession,
		wsConn:       wsConn,
		subdomain:    subdomain,
		connectedAt:  time.Now(),
		ctx:          ctx,
		cancel:       cancel,
	}, nil
}

// OpenStream opens a new multiplexed stream (server -> client).
func (s *Session) OpenStream() (net.Conn, error) {
	s.closeMu.RLock()
	closed := s.closed
	s.closeMu.RUnlock()
	if closed {
		return nil, ErrSessionClosed
	}
	return s.yamuxSession.Open()
}

// AcceptStream accepts an incoming stream (client receives from server).
func (s *Session) AcceptStream() (net.Conn, error) {
	s.closeMu.RLock()
	closed := s.closed
	s.closeMu.RUnlock()
	if closed {
		return nil, ErrSessionClosed
	}
	// Don't hold the lock while accepting - Accept() blocks and would
	// prevent Close() from acquiring the write lock, causing a deadlock
	return s.yamuxSession.Accept()
}

// Close closes the session and all associated streams.
func (s *Session) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()

	var errs []error
	if err := s.yamuxSession.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := s.wsConn.Close(); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing session: %v", errs)
	}
	return nil
}

// IsClosed returns whether the session is closed.
func (s *Session) IsClosed() bool {
	s.closeMu.RLock()
	defer s.closeMu.RUnlock()
	return s.closed
}

// Subdomain returns the subdomain associated with this session.
func (s *Session) Subdomain() string {
	return s.subdomain
}

// ConnectedAt returns when the session was established.
func (s *Session) ConnectedAt() time.Time {
	return s.connectedAt
}

// Context returns the session's context.
func (s *Session) Context() context.Context {
	return s.ctx
}

// NumStreams returns the number of active streams.
func (s *Session) NumStreams() int {
	return s.yamuxSession.NumStreams()
}
