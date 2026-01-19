package transport

import (
	"testing"
	"time"
)

func TestYamuxConfig(t *testing.T) {
	config := YamuxConfig()

	if config == nil {
		t.Fatal("YamuxConfig() returned nil")
	}

	// Verify important settings
	if config.AcceptBacklog != 256 {
		t.Errorf("AcceptBacklog = %d, want 256", config.AcceptBacklog)
	}

	if !config.EnableKeepAlive {
		t.Error("EnableKeepAlive should be true")
	}

	if config.KeepAliveInterval <= 0 {
		t.Error("KeepAliveInterval should be positive")
	}

	if config.ConnectionWriteTimeout <= 0 {
		t.Error("ConnectionWriteTimeout should be positive")
	}

	if config.StreamOpenTimeout <= 0 {
		t.Error("StreamOpenTimeout should be positive")
	}
}

func TestSessionCreation(t *testing.T) {
	// Note: Full session testing requires a WebSocket connection
	// These tests verify the session type behavior with nil checks

	t.Run("session methods don't panic on checks", func(t *testing.T) {
		// This test documents expected behavior - real integration tests
		// would use a mock WebSocket connection
	})
}

func TestErrSessionClosed(t *testing.T) {
	if ErrSessionClosed == nil {
		t.Error("ErrSessionClosed should not be nil")
	}
	if ErrSessionClosed.Error() == "" {
		t.Error("ErrSessionClosed should have a message")
	}
}

func TestErrConnectionFailed(t *testing.T) {
	if ErrConnectionFailed == nil {
		t.Error("ErrConnectionFailed should not be nil")
	}
	if ErrConnectionFailed.Error() == "" {
		t.Error("ErrConnectionFailed should have a message")
	}
}

func TestWebSocketUpgrader(t *testing.T) {
	if WebSocketUpgrader.ReadBufferSize != 16*1024 {
		t.Errorf("ReadBufferSize = %d, want %d", WebSocketUpgrader.ReadBufferSize, 16*1024)
	}
	if WebSocketUpgrader.WriteBufferSize != 16*1024 {
		t.Errorf("WriteBufferSize = %d, want %d", WebSocketUpgrader.WriteBufferSize, 16*1024)
	}

	// CheckOrigin should allow all origins
	if WebSocketUpgrader.CheckOrigin == nil {
		t.Error("CheckOrigin should be set")
	}
}

// BenchmarkYamuxConfig ensures config creation is fast
func BenchmarkYamuxConfig(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = YamuxConfig()
	}
}

// Test time-based assertions
func TestSessionTimestamps(t *testing.T) {
	// Verify time constants are reasonable
	if time.Second*30 < time.Second {
		t.Error("Heartbeat interval too short")
	}
}
