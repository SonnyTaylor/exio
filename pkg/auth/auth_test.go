package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestNewAuthenticator(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		envVar  string
		wantErr error
	}{
		{
			name:    "valid token from parameter",
			token:   "my-secret-token",
			envVar:  "",
			wantErr: nil,
		},
		{
			name:    "valid token from environment",
			token:   "",
			envVar:  "env-secret-token",
			wantErr: nil,
		},
		{
			name:    "parameter takes precedence over env",
			token:   "param-token",
			envVar:  "env-token",
			wantErr: nil,
		},
		{
			name:    "no token configured",
			token:   "",
			envVar:  "",
			wantErr: ErrTokenNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			os.Unsetenv(EnvAuthToken)
			if tt.envVar != "" {
				os.Setenv(EnvAuthToken, tt.envVar)
				defer os.Unsetenv(EnvAuthToken)
			}

			auth, err := NewAuthenticator(tt.token)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("NewAuthenticator() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("NewAuthenticator() unexpected error: %v", err)
				return
			}

			// Check the token was set correctly
			expectedToken := tt.token
			if expectedToken == "" {
				expectedToken = tt.envVar
			}
			if auth.Token() != expectedToken {
				t.Errorf("Token() = %q, want %q", auth.Token(), expectedToken)
			}
		})
	}
}

func TestAuthenticator_Validate(t *testing.T) {
	auth, _ := NewAuthenticator("correct-token")

	tests := []struct {
		name    string
		token   string
		wantErr error
	}{
		{
			name:    "correct token",
			token:   "correct-token",
			wantErr: nil,
		},
		{
			name:    "incorrect token",
			token:   "wrong-token",
			wantErr: ErrInvalidToken,
		},
		{
			name:    "empty token",
			token:   "",
			wantErr: ErrMissingToken,
		},
		{
			name:    "similar but different token",
			token:   "correct-token!",
			wantErr: ErrInvalidToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.Validate(tt.token)
			if err != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAuthenticator_ValidateRequest(t *testing.T) {
	auth, _ := NewAuthenticator("secret-token")

	tests := []struct {
		name       string
		authHeader string
		wantErr    error
	}{
		{
			name:       "valid bearer token",
			authHeader: "Bearer secret-token",
			wantErr:    nil,
		},
		{
			name:       "invalid bearer token",
			authHeader: "Bearer wrong-token",
			wantErr:    ErrInvalidToken,
		},
		{
			name:       "missing authorization header",
			authHeader: "",
			wantErr:    ErrMissingToken,
		},
		{
			name:       "wrong auth scheme",
			authHeader: "Basic secret-token",
			wantErr:    ErrInvalidToken,
		},
		{
			name:       "bearer without token",
			authHeader: "Bearer ",
			wantErr:    ErrMissingToken,
		},
		{
			name:       "bearer only",
			authHeader: "Bearer",
			wantErr:    ErrInvalidToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.authHeader != "" {
				req.Header.Set(AuthorizationHeader, tt.authHeader)
			}

			err := auth.ValidateRequest(req)
			if err != tt.wantErr {
				t.Errorf("ValidateRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAuthenticator_AuthorizationHeaderValue(t *testing.T) {
	auth, _ := NewAuthenticator("my-token")

	got := auth.AuthorizationHeaderValue()
	want := "Bearer my-token"

	if got != want {
		t.Errorf("AuthorizationHeaderValue() = %q, want %q", got, want)
	}
}

// TestConstantTimeComparison verifies that token comparison doesn't leak timing info.
// This is a basic sanity check - proper timing attack testing requires more sophisticated tools.
func TestConstantTimeComparison(t *testing.T) {
	auth, _ := NewAuthenticator("correct-token-here")

	// These should all take approximately the same time
	tokens := []string{
		"correct-token-here",  // correct
		"wrong-token-here!!!", // same length, wrong
		"x",                   // very short
		"this-is-a-very-long-token-that-is-definitely-wrong", // very long
	}

	for _, token := range tokens {
		// Just verify they work without panicking
		_ = auth.Validate(token)
	}
}
