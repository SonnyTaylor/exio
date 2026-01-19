package server

import (
	"sync"
	"testing"
)

func TestValidateSubdomain(t *testing.T) {
	tests := []struct {
		name      string
		subdomain string
		wantErr   bool
	}{
		// Valid subdomains
		{"simple lowercase", "myapp", false},
		{"with numbers", "app123", false},
		{"with hyphens", "my-cool-app", false},
		{"minimum length", "abc", false},
		{"mixed alphanumeric", "test42app", false},
		{"starts with number", "123app", false},

		// Invalid subdomains
		{"too short", "ab", true},
		{"empty", "", true},
		{"starts with hyphen", "-myapp", true},
		{"ends with hyphen", "myapp-", true},
		// Note: uppercase is normalized to lowercase before validation, so "MyApp" becomes "myapp" and is valid
		{"contains underscore", "my_app", true},
		{"contains dot", "my.app", true},
		{"too long", "this-subdomain-is-way-too-long-and-should-fail-validation-because-its-over-63-chars", true},
		{"special characters", "my@app", true},
		{"spaces", "my app", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSubdomain(tt.subdomain)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSubdomain(%q) error = %v, wantErr %v", tt.subdomain, err, tt.wantErr)
			}
		})
	}
}

func TestSessionRegistry_RegisterAndGet(t *testing.T) {
	registry := NewSessionRegistry(0, 0)

	// Test registration with nil session (for unit testing - real usage would have actual session)
	// We can't fully test without a real transport.Session, but we can test the map logic

	t.Run("count starts at zero", func(t *testing.T) {
		if registry.Count() != 0 {
			t.Errorf("Count() = %d, want 0", registry.Count())
		}
	})

	t.Run("exists returns false for non-existent", func(t *testing.T) {
		if registry.Exists("nonexistent") {
			t.Error("Exists() should return false for non-existent subdomain")
		}
	})

	t.Run("get returns error for non-existent", func(t *testing.T) {
		_, err := registry.Get("nonexistent")
		if err != ErrSubdomainNotFound {
			t.Errorf("Get() error = %v, want %v", err, ErrSubdomainNotFound)
		}
	})
}

func TestSessionRegistry_SubdomainCollision(t *testing.T) {
	registry := NewSessionRegistry(0, 0)

	// First registration should succeed (with nil session for testing)
	err := registry.Register("myapp", nil)
	if err != nil {
		t.Fatalf("First Register() failed: %v", err)
	}

	// Second registration with same name should fail
	err = registry.Register("myapp", nil)
	if err != ErrSubdomainTaken {
		t.Errorf("Second Register() error = %v, want %v", err, ErrSubdomainTaken)
	}

	// Case-insensitive collision
	err = registry.Register("MYAPP", nil)
	if err != ErrSubdomainTaken {
		t.Errorf("Case-insensitive Register() error = %v, want %v", err, ErrSubdomainTaken)
	}
}

func TestSessionRegistry_Unregister(t *testing.T) {
	registry := NewSessionRegistry(0, 0)

	// Register
	registry.Register("myapp", nil)
	if registry.Count() != 1 {
		t.Errorf("Count() after register = %d, want 1", registry.Count())
	}

	// Unregister
	registry.Unregister("myapp")
	if registry.Count() != 0 {
		t.Errorf("Count() after unregister = %d, want 0", registry.Count())
	}

	// Should be able to register again
	err := registry.Register("myapp", nil)
	if err != nil {
		t.Errorf("Register() after unregister failed: %v", err)
	}
}

func TestSessionRegistry_UnregisterNonExistent(t *testing.T) {
	registry := NewSessionRegistry(0, 0)

	// Should not panic or change count
	registry.Unregister("nonexistent")
	if registry.Count() != 0 {
		t.Errorf("Count() = %d, want 0", registry.Count())
	}
}

func TestSessionRegistry_ForEach(t *testing.T) {
	registry := NewSessionRegistry(0, 0)

	// Register multiple
	registry.Register("app1", nil)
	registry.Register("app2", nil)
	registry.Register("app3", nil)

	// Count iterations
	var count int
	registry.ForEach(func(subdomain string, entry *SessionEntry) bool {
		count++
		return true
	})

	if count != 3 {
		t.Errorf("ForEach iterated %d times, want 3", count)
	}

	// Test early exit
	count = 0
	registry.ForEach(func(subdomain string, entry *SessionEntry) bool {
		count++
		return false // Stop after first
	})

	if count != 1 {
		t.Errorf("ForEach with early exit iterated %d times, want 1", count)
	}
}

func TestSessionRegistry_ConcurrentAccess(t *testing.T) {
	registry := NewSessionRegistry(0, 0)
	var wg sync.WaitGroup

	// Concurrent registrations
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			subdomain := "app" + string(rune('a'+n%26)) + string(rune('0'+n%10)) + string(rune('a'+n%26))
			registry.Register(subdomain, nil)
		}(i)
	}

	wg.Wait()

	// Some registrations may have collided, but it shouldn't panic
	if registry.Count() < 1 {
		t.Error("Expected some successful registrations")
	}
}

func TestSessionRegistry_InvalidSubdomains(t *testing.T) {
	registry := NewSessionRegistry(0, 0)

	invalidSubdomains := []string{
		"ab",        // too short
		"-start",    // starts with hyphen
		"end-",      // ends with hyphen
		"has space", // contains space
		"has.dot",   // contains dot
	}

	for _, subdomain := range invalidSubdomains {
		err := registry.Register(subdomain, nil)
		if err != ErrInvalidSubdomain {
			t.Errorf("Register(%q) error = %v, want %v", subdomain, err, ErrInvalidSubdomain)
		}
	}
}

func BenchmarkRegistry_Register(b *testing.B) {
	registry := NewSessionRegistry(0, 0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		subdomain := "bench" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26))
		registry.Register(subdomain, nil)
	}
}

func BenchmarkRegistry_Get(b *testing.B) {
	registry := NewSessionRegistry(0, 0)
	registry.Register("testapp", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		registry.Get("testapp")
	}
}
