package protocol

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractSubdomain(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		baseDomain string
		want       string
	}{
		{
			name:       "simple subdomain",
			host:       "myapp.dev.example.com",
			baseDomain: "dev.example.com",
			want:       "myapp",
		},
		{
			name:       "subdomain with hyphen",
			host:       "my-cool-app.dev.example.com",
			baseDomain: "dev.example.com",
			want:       "my-cool-app",
		},
		{
			name:       "subdomain with port",
			host:       "myapp.dev.example.com:8080",
			baseDomain: "dev.example.com",
			want:       "myapp",
		},
		{
			name:       "no subdomain - exact match",
			host:       "dev.example.com",
			baseDomain: "dev.example.com",
			want:       "",
		},
		{
			name:       "different domain",
			host:       "other.domain.com",
			baseDomain: "dev.example.com",
			want:       "",
		},
		{
			name:       "nested subdomain",
			host:       "api.v2.dev.example.com",
			baseDomain: "dev.example.com",
			want:       "api.v2",
		},
		{
			name:       "empty host",
			host:       "",
			baseDomain: "dev.example.com",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tt.host

			got := ExtractSubdomain(req, tt.baseDomain)
			if got != tt.want {
				t.Errorf("ExtractSubdomain() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRewriteHostHeader(t *testing.T) {
	tests := []struct {
		name       string
		targetPort int
		wantHost   string
	}{
		{
			name:       "standard port",
			targetPort: 3000,
			wantHost:   "127.0.0.1:3000",
		},
		{
			name:       "port 80",
			targetPort: 80,
			wantHost:   "127.0.0.1",
		},
		{
			name:       "high port",
			targetPort: 8080,
			wantHost:   "127.0.0.1:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = "original.example.com"

			RewriteHostHeader(req, tt.targetPort)

			if req.Host != tt.wantHost {
				t.Errorf("RewriteHostHeader() Host = %q, want %q", req.Host, tt.wantHost)
			}
			if req.Header.Get("Host") != tt.wantHost {
				t.Errorf("RewriteHostHeader() Header = %q, want %q", req.Header.Get("Host"), tt.wantHost)
			}
		})
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{3000, "3000"},
		{65535, "65535"},
		{-1, "-1"},
		{-42, "-42"},
	}

	for _, tt := range tests {
		got := itoa(tt.input)
		if got != tt.want {
			t.Errorf("itoa(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConstants(t *testing.T) {
	// Ensure constants have sensible values
	if ConnectPath != "/_connect" {
		t.Errorf("ConnectPath = %q, want %q", ConnectPath, "/_connect")
	}
	if DefaultPort != 8080 {
		t.Errorf("DefaultPort = %d, want %d", DefaultPort, 8080)
	}
	if HeartbeatInterval <= 0 {
		t.Error("HeartbeatInterval should be positive")
	}
	if MaxReconnectDelay <= InitialReconnectDelay {
		t.Error("MaxReconnectDelay should be greater than InitialReconnectDelay")
	}
}
