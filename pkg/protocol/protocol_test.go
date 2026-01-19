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

func TestExtractTunnelIDFromPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "simple path with tunnel ID",
			path: "/bold-owl-716/api/users",
			want: "bold-owl-716",
		},
		{
			name: "tunnel ID only",
			path: "/bold-owl-716",
			want: "bold-owl-716",
		},
		{
			name: "tunnel ID with trailing slash",
			path: "/bold-owl-716/",
			want: "bold-owl-716",
		},
		{
			name: "empty path",
			path: "",
			want: "",
		},
		{
			name: "root path only",
			path: "/",
			want: "",
		},
		{
			name: "deep nested path",
			path: "/my-tunnel/api/v2/users/123",
			want: "my-tunnel",
		},
		{
			name: "no leading slash",
			path: "tunnel-id/path",
			want: "tunnel-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTunnelIDFromPath(tt.path)
			if got != tt.want {
				t.Errorf("ExtractTunnelIDFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestStripTunnelIDPrefix(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		tunnelID string
		want     string
	}{
		{
			name:     "strip prefix from nested path",
			path:     "/bold-owl-716/api/users",
			tunnelID: "bold-owl-716",
			want:     "/api/users",
		},
		{
			name:     "strip prefix leaving root",
			path:     "/bold-owl-716",
			tunnelID: "bold-owl-716",
			want:     "/",
		},
		{
			name:     "strip prefix with trailing slash",
			path:     "/bold-owl-716/",
			tunnelID: "bold-owl-716",
			want:     "/",
		},
		{
			name:     "empty tunnel ID returns original path",
			path:     "/some/path",
			tunnelID: "",
			want:     "/some/path",
		},
		{
			name:     "mismatched prefix returns original",
			path:     "/other-id/api/users",
			tunnelID: "bold-owl-716",
			want:     "/other-id/api/users",
		},
		{
			name:     "deep nested path",
			path:     "/my-tunnel/api/v2/users/123",
			tunnelID: "my-tunnel",
			want:     "/api/v2/users/123",
		},
		{
			name:     "partial match should not strip",
			path:     "/bold-owl-716-extra/api",
			tunnelID: "bold-owl-716",
			want:     "/bold-owl-716-extra/api",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripTunnelIDPrefix(tt.path, tt.tunnelID)
			if got != tt.want {
				t.Errorf("StripTunnelIDPrefix(%q, %q) = %q, want %q", tt.path, tt.tunnelID, got, tt.want)
			}
		})
	}
}

func TestRoutingModeConstants(t *testing.T) {
	if RoutingModePath != "path" {
		t.Errorf("RoutingModePath = %q, want %q", RoutingModePath, "path")
	}
	if RoutingModeSubdomain != "subdomain" {
		t.Errorf("RoutingModeSubdomain = %q, want %q", RoutingModeSubdomain, "subdomain")
	}
}
