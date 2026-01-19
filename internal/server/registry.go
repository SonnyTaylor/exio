// Package server contains the internal server implementation for Exio.
package server

import (
	"errors"
	"net"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sonnytaylor/exio/pkg/protocol"
	"github.com/sonnytaylor/exio/pkg/transport"
	"golang.org/x/time/rate"
)

var (
	// ErrSubdomainTaken is returned when attempting to register an already-used subdomain.
	ErrSubdomainTaken = errors.New("subdomain already in use")

	// ErrSubdomainNotFound is returned when a subdomain doesn't exist in the registry.
	ErrSubdomainNotFound = errors.New("subdomain not found")

	// ErrInvalidSubdomain is returned when the subdomain format is invalid.
	ErrInvalidSubdomain = errors.New("invalid subdomain format")
)

// validSubdomainRegex matches valid subdomain patterns.
// Allows alphanumeric characters and hyphens, 3-63 characters.
var validSubdomainRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

// SessionRegistry manages active tunnel sessions indexed by subdomain.
// It is thread-safe for concurrent access.
type SessionRegistry struct {
	sessions sync.Map // map[string]*SessionEntry
	count    atomic.Int64

	// TCP port allocation
	tcpPortStart int
	tcpPortEnd   int
	tcpPorts     sync.Map // map[int]string (port -> subdomain)
	tcpPortMu    sync.Mutex
}

// SessionEntry wraps a session with metadata for the registry.
type SessionEntry struct {
	Session      *transport.Session
	RequestCount atomic.Int64
	RateLimiter  *rate.Limiter // nil means no rate limiting
	TunnelType   string        // "http" or "tcp"
	TCPPort      int           // Allocated TCP port (for TCP tunnels)
	TCPListener  net.Listener  // TCP listener (for TCP tunnels)
}

// NewSessionRegistry creates a new session registry.
func NewSessionRegistry(tcpPortStart, tcpPortEnd int) *SessionRegistry {
	if tcpPortStart == 0 {
		tcpPortStart = protocol.DefaultTCPPortStart
	}
	if tcpPortEnd == 0 {
		tcpPortEnd = protocol.DefaultTCPPortEnd
	}
	return &SessionRegistry{
		tcpPortStart: tcpPortStart,
		tcpPortEnd:   tcpPortEnd,
	}
}

// AllocateTCPPort finds and allocates an available TCP port for a tunnel.
func (r *SessionRegistry) AllocateTCPPort(subdomain string) (int, error) {
	r.tcpPortMu.Lock()
	defer r.tcpPortMu.Unlock()

	for port := r.tcpPortStart; port <= r.tcpPortEnd; port++ {
		if _, exists := r.tcpPorts.Load(port); !exists {
			r.tcpPorts.Store(port, subdomain)
			return port, nil
		}
	}
	return 0, errors.New("no available TCP ports")
}

// ReleaseTCPPort releases an allocated TCP port.
func (r *SessionRegistry) ReleaseTCPPort(port int) {
	r.tcpPorts.Delete(port)
}

// ValidateSubdomain checks if a subdomain is valid.
func ValidateSubdomain(subdomain string) error {
	subdomain = strings.ToLower(subdomain)

	// Check minimum length
	if len(subdomain) < 3 {
		return ErrInvalidSubdomain
	}

	// Check maximum length
	if len(subdomain) > 63 {
		return ErrInvalidSubdomain
	}

	if !validSubdomainRegex.MatchString(subdomain) {
		return ErrInvalidSubdomain
	}

	return nil
}

// Register adds a new session to the registry.
// Returns an error if the subdomain is already taken or invalid.
func (r *SessionRegistry) Register(subdomain string, session *transport.Session) error {
	return r.RegisterWithOptions(subdomain, session, protocol.TunnelTypeHTTP, 0, nil, nil)
}

// RegisterWithOptions adds a new session to the registry with additional options.
func (r *SessionRegistry) RegisterWithOptions(subdomain string, session *transport.Session, tunnelType string, tcpPort int, tcpListener net.Listener, limiter *rate.Limiter) error {
	subdomain = strings.ToLower(subdomain)

	if err := ValidateSubdomain(subdomain); err != nil {
		return err
	}

	entry := &SessionEntry{
		Session:     session,
		TunnelType:  tunnelType,
		TCPPort:     tcpPort,
		TCPListener: tcpListener,
		RateLimiter: limiter,
	}

	// Attempt to store, checking for existing entry
	if _, loaded := r.sessions.LoadOrStore(subdomain, entry); loaded {
		return ErrSubdomainTaken
	}

	r.count.Add(1)
	return nil
}

// Unregister removes a session from the registry.
func (r *SessionRegistry) Unregister(subdomain string) {
	subdomain = strings.ToLower(subdomain)
	if value, loaded := r.sessions.LoadAndDelete(subdomain); loaded {
		r.count.Add(-1)
		// Clean up TCP resources
		entry := value.(*SessionEntry)
		if entry.TCPListener != nil {
			entry.TCPListener.Close()
		}
		if entry.TCPPort > 0 {
			r.ReleaseTCPPort(entry.TCPPort)
		}
	}
}

// Get retrieves a session by subdomain.
func (r *SessionRegistry) Get(subdomain string) (*SessionEntry, error) {
	subdomain = strings.ToLower(subdomain)
	value, ok := r.sessions.Load(subdomain)
	if !ok {
		return nil, ErrSubdomainNotFound
	}
	return value.(*SessionEntry), nil
}

// Exists checks if a subdomain is registered.
func (r *SessionRegistry) Exists(subdomain string) bool {
	subdomain = strings.ToLower(subdomain)
	_, ok := r.sessions.Load(subdomain)
	return ok
}

// Count returns the number of active sessions.
func (r *SessionRegistry) Count() int64 {
	return r.count.Load()
}

// ForEach iterates over all sessions, calling fn for each.
// If fn returns false, iteration stops.
func (r *SessionRegistry) ForEach(fn func(subdomain string, entry *SessionEntry) bool) {
	r.sessions.Range(func(key, value interface{}) bool {
		return fn(key.(string), value.(*SessionEntry))
	})
}

// CloseAll closes all sessions in the registry.
func (r *SessionRegistry) CloseAll() {
	r.sessions.Range(func(key, value interface{}) bool {
		entry := value.(*SessionEntry)
		// Close TCP listener first to unblock Accept() calls
		if entry.TCPListener != nil {
			entry.TCPListener.Close()
		}
		entry.Session.Close()
		return true
	})
}
