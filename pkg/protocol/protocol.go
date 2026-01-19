// Package protocol defines shared types and constants for the Exio tunneling system.
package protocol

import (
	"net/http"
	"time"
)

const (
	// ConnectPath is the WebSocket upgrade endpoint for control plane connections.
	ConnectPath = "/_connect"

	// DefaultPort is the default server listening port.
	DefaultPort = 8080

	// HeartbeatInterval is the interval for WebSocket ping/pong keep-alive.
	HeartbeatInterval = 30 * time.Second

	// WriteTimeout is the timeout for writing to WebSocket connections.
	WriteTimeout = 10 * time.Second

	// ReadTimeout is the timeout for reading from WebSocket connections.
	ReadTimeout = 60 * time.Second

	// MaxReconnectDelay is the maximum delay for exponential backoff reconnection.
	MaxReconnectDelay = 30 * time.Second

	// InitialReconnectDelay is the initial delay for exponential backoff.
	InitialReconnectDelay = 1 * time.Second

	// SubdomainQueryParam is the query parameter for requesting a subdomain.
	SubdomainQueryParam = "subdomain"
)

// ConnectRequest represents the parameters for establishing a tunnel connection.
type ConnectRequest struct {
	Subdomain string `json:"subdomain"`
	Token     string `json:"token"`
}

// ConnectResponse is sent back to the client after connection establishment.
type ConnectResponse struct {
	Success   bool   `json:"success"`
	Subdomain string `json:"subdomain,omitempty"`
	PublicURL string `json:"public_url,omitempty"`
	Error     string `json:"error,omitempty"`
}

// TunnelInfo contains metadata about an active tunnel session.
type TunnelInfo struct {
	Subdomain    string    `json:"subdomain"`
	PublicURL    string    `json:"public_url"`
	LocalPort    int       `json:"local_port"`
	ConnectedAt  time.Time `json:"connected_at"`
	RequestCount int64     `json:"request_count"`
}

// RequestLog represents a logged HTTP request passing through the tunnel.
type RequestLog struct {
	Timestamp  time.Time     `json:"timestamp"`
	Method     string        `json:"method"`
	Path       string        `json:"path"`
	StatusCode int           `json:"status_code"`
	Duration   time.Duration `json:"duration"`
	BytesIn    int64         `json:"bytes_in"`
	BytesOut   int64         `json:"bytes_out"`
	RemoteAddr string        `json:"remote_addr"`
}

// ExtractSubdomain extracts the subdomain from an HTTP request's Host header.
// For example, "api-v2.dev.example.com" with baseDomain "dev.example.com" returns "api-v2".
func ExtractSubdomain(r *http.Request, baseDomain string) string {
	host := r.Host
	// Remove port if present
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			host = host[:i]
			break
		}
	}

	// Check if the host ends with the base domain
	if len(host) <= len(baseDomain) {
		return ""
	}

	// Extract subdomain (everything before the base domain minus the dot)
	suffix := "." + baseDomain
	if len(host) > len(suffix) && host[len(host)-len(suffix):] == suffix {
		return host[:len(host)-len(suffix)]
	}

	return ""
}

// RewriteHostHeader rewrites the Host header from the tunnel subdomain to localhost.
func RewriteHostHeader(r *http.Request, targetPort int) {
	r.Host = "127.0.0.1"
	if targetPort != 80 {
		r.Host = r.Host + ":" + itoa(targetPort)
	}
	r.Header.Set("Host", r.Host)
}

// itoa converts an integer to a string without importing strconv.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}

	var b [20]byte
	n := len(b)
	negative := i < 0
	if negative {
		i = -i
	}

	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}

	if negative {
		n--
		b[n] = '-'
	}

	return string(b[n:])
}
