package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSQLInjectionProtection verifies that malicious SQL payloads cannot bypass authentication
func TestSQLInjectionProtection(t *testing.T) {
	sqlPayloads := []string{
		"' OR '1'='1",
		"admin' --",
		"' OR 1=1 --",
		"admin' /*",
		"'; DROP TABLE users; --",
		"' UNION SELECT 1, 'admin', 'hash', 'admin' --",
		"\\'; DROP TABLE users; --",
	}

	for _, payload := range sqlPayloads {
		// Verify dummy hash comparison runs safely without panics
		DummyHashComparison(payload)

		// Verify constant-time check handles malicious string lengths safely
		if CheckPassword("$2a$12$e8Y68O4tqGqVqF0n9kQk5OPP5.vUvY01qBqZqAqZqZqZqZqZqZqZq", payload) {
			t.Errorf("Security vulnerability: SQL payload unexpectedly matched password hash: %s", payload)
		}
	}
}

// TestBruteForceProtection verifies that repeated failed login attempts from an IP trigger rate limiting
func TestBruteForceProtection(t *testing.T) {
	limiter := NewLoginRateLimiter(5, 5*timeMinute)
	attackerIP := "203.0.113.195"

	// 5 failed attempts
	for i := 1; i <= 5; i++ {
		if !limiter.IsAllowed(attackerIP) {
			t.Fatalf("Attempt %d should have been allowed", i)
		}
		limiter.RecordFailure(attackerIP)
	}

	// 6th attempt MUST be blocked
	if limiter.IsAllowed(attackerIP) {
		t.Fatalf("Attempt 6 should be blocked by rate limiter")
	}

	// Legitimate user from different IP must NOT be blocked
	legitIP := "198.51.100.42"
	if !limiter.IsAllowed(legitIP) {
		t.Fatalf("Legitimate IP should not be affected by attacker IP lockout")
	}
}

// TestRBACForbiddenForUserRole verifies that non-admin users cannot access admin endpoints
func TestRBACForbiddenForUserRole(t *testing.T) {
	adminEndpoint := RequireRole("admin")(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"restarted"}`))
	})

	// User role request
	req := httptest.NewRequest("POST", "/api/system/restart", nil)
	ctx := context.WithValue(req.Context(), UserRoleContextKey, "user")
	ctx = context.WithValue(ctx, UsernameContextKey, "standard_trader")
	w := httptest.NewRecorder()

	adminEndpoint(w, req.WithContext(ctx))

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected HTTP 403 Forbidden for 'user' role on admin restart endpoint, got %d", w.Code)
	}

	if !strings.Contains(w.Body.String(), "Forbidden") {
		t.Errorf("Expected forbidden error message in response body, got: %s", w.Body.String())
	}
}

const timeMinute = 60 * 1000 * 1000 * 1000 // 1 minute in nanoseconds
