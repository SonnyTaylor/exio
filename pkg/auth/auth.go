// Package auth provides authentication utilities for the Exio tunneling system.
package auth

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"strings"
)

const (
	// EnvAuthToken is the environment variable name for the shared authentication token.
	EnvAuthToken = "EXIO_TOKEN"

	// AuthorizationHeader is the HTTP header used for bearer token authentication.
	AuthorizationHeader = "Authorization"

	// BearerPrefix is the prefix for bearer token authentication.
	BearerPrefix = "Bearer "
)

var (
	// ErrMissingToken is returned when no authentication token is provided.
	ErrMissingToken = errors.New("missing authentication token")

	// ErrInvalidToken is returned when the provided token doesn't match.
	ErrInvalidToken = errors.New("invalid authentication token")

	// ErrTokenNotConfigured is returned when the server token is not configured.
	ErrTokenNotConfigured = errors.New("server authentication token not configured")
)

// Authenticator handles PSK-based authentication for Exio connections.
type Authenticator struct {
	token string
}

// NewAuthenticator creates a new authenticator with the given token.
// If token is empty, it attempts to read from the EXIO_TOKEN environment variable.
func NewAuthenticator(token string) (*Authenticator, error) {
	if token == "" {
		token = os.Getenv(EnvAuthToken)
	}
	if token == "" {
		return nil, ErrTokenNotConfigured
	}
	return &Authenticator{token: token}, nil
}

// NewAuthenticatorFromEnv creates a new authenticator from environment variables.
func NewAuthenticatorFromEnv() (*Authenticator, error) {
	return NewAuthenticator("")
}

// Validate checks if the provided token matches the configured token.
// Uses constant-time comparison to prevent timing attacks.
func (a *Authenticator) Validate(providedToken string) error {
	if providedToken == "" {
		return ErrMissingToken
	}
	if subtle.ConstantTimeCompare([]byte(a.token), []byte(providedToken)) != 1 {
		return ErrInvalidToken
	}
	return nil
}

// ValidateRequest extracts and validates the token from an HTTP request.
// Expects the token in the Authorization header as "Bearer <token>".
func (a *Authenticator) ValidateRequest(r *http.Request) error {
	authHeader := r.Header.Get(AuthorizationHeader)
	if authHeader == "" {
		return ErrMissingToken
	}

	if !strings.HasPrefix(authHeader, BearerPrefix) {
		return ErrInvalidToken
	}

	token := strings.TrimPrefix(authHeader, BearerPrefix)
	return a.Validate(token)
}

// Token returns the configured authentication token.
// This is used by clients to construct authorization headers.
func (a *Authenticator) Token() string {
	return a.token
}

// AuthorizationHeaderValue returns the full Authorization header value.
func (a *Authenticator) AuthorizationHeaderValue() string {
	return BearerPrefix + a.token
}
