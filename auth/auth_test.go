package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"zerodha-trading/data"
)

func TestJWTGenerationAndValidation(t *testing.T) {
	secret := "test-super-secret-key-at-least-32-chars-long"
	user := &data.User{
		ID:       42,
		Username: "vikash_admin",
		Role:     "admin",
		FullName: "Vikash Chandra",
	}

	// 1. Valid token
	tokenStr, err := GenerateJWT(user, secret, 12)
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}

	claims, err := ValidateJWT(tokenStr, secret)
	if err != nil {
		t.Fatalf("ValidateJWT failed on valid token: %v", err)
	}

	if claims.UserID != 42 || claims.Username != "vikash_admin" || claims.Role != "admin" {
		t.Errorf("claims mismatch: got %+v", claims)
	}

	// 2. Tampered token / Wrong secret
	_, err = ValidateJWT(tokenStr, "wrong-secret-key")
	if err == nil {
		t.Errorf("expected error when validating with wrong secret, got nil")
	}

	// 3. Expired token
	expiredTokenStr, err := GenerateJWT(user, secret, -1) // negative expiry
	if err != nil {
		t.Fatalf("GenerateJWT failed: %v", err)
	}
	_, err = ValidateJWT(expiredTokenStr, secret)
	if err == nil {
		t.Errorf("expected error for expired token, got nil")
	}
}

func TestPasswordHashingAndVerification(t *testing.T) {
	password := "SecureTrader@2026!"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if !CheckPassword(hash, password) {
		t.Errorf("CheckPassword returned false for matching password")
	}

	if CheckPassword(hash, "WrongPassword123") {
		t.Errorf("CheckPassword returned true for wrong password")
	}
}

func TestLoginRateLimiter(t *testing.T) {
	rl := NewLoginRateLimiter(3, 1*time.Minute)
	testIP := "192.168.1.100"

	if !rl.IsAllowed(testIP) {
		t.Errorf("initial state should be allowed")
	}

	rl.RecordFailure(testIP)
	rl.RecordFailure(testIP)
	if !rl.IsAllowed(testIP) {
		t.Errorf("2 failures should still be allowed when max is 3")
	}

	rl.RecordFailure(testIP)
	if rl.IsAllowed(testIP) {
		t.Errorf("3 failures should block subsequent attempts")
	}

	rl.Reset(testIP)
	if !rl.IsAllowed(testIP) {
		t.Errorf("reset should unblock IP")
	}
}

func TestAuthMiddleware(t *testing.T) {
	secret := "test-secret-key-32-characters-minimum"
	mw := AuthMiddleware(secret)

	// Mock protected handler
	protectedHandler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK:" + GetUsername(r.Context())))
	}))

	// 1. Unauthenticated browser request -> 302 redirect to /login
	reqBrowser := httptest.NewRequest("GET", "/zt", nil)
	wBrowser := httptest.NewRecorder()
	protectedHandler.ServeHTTP(wBrowser, reqBrowser)
	if wBrowser.Code != http.StatusFound || wBrowser.Header().Get("Location") != "/login" {
		t.Errorf("expected 302 redirect to /login, got status %d, location: %s", wBrowser.Code, wBrowser.Header().Get("Location"))
	}

	// 2. Unauthenticated API request -> 401 Unauthorized JSON
	reqAPI := httptest.NewRequest("GET", "/api/positions", nil)
	wAPI := httptest.NewRecorder()
	protectedHandler.ServeHTTP(wAPI, reqAPI)
	if wAPI.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for API, got %d", wAPI.Code)
	}

	// 3. Whitelisted public path /login -> 200 OK without token
	publicHandler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	reqLogin := httptest.NewRequest("GET", "/login", nil)
	wLogin := httptest.NewRecorder()
	publicHandler.ServeHTTP(wLogin, reqLogin)
	if wLogin.Code != http.StatusOK {
		t.Errorf("expected 200 OK for /login, got %d", wLogin.Code)
	}

	// 4. Authenticated request with valid cookie
	user := &data.User{ID: 10, Username: "operator", Role: "user"}
	tokenStr, _ := GenerateJWT(user, secret, 12)
	reqAuth := httptest.NewRequest("GET", "/api/positions", nil)
	reqAuth.AddCookie(&http.Cookie{Name: "vridhi_jwt", Value: tokenStr})
	wAuth := httptest.NewRecorder()
	protectedHandler.ServeHTTP(wAuth, reqAuth)
	if wAuth.Code != http.StatusOK {
		t.Errorf("expected 200 OK for authenticated cookie, got %d", wAuth.Code)
	}
	if wAuth.Body.String() != "OK:operator" {
		t.Errorf("expected body OK:operator, got %s", wAuth.Body.String())
	}
}

func TestRequireRoleRBAC(t *testing.T) {
	adminRoleHandler := RequireRole("admin")(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// 1. User with "user" role calling admin endpoint -> 403 Forbidden
	reqUser := httptest.NewRequest("POST", "/api/system/restart", nil)
	ctxUser := context.WithValue(reqUser.Context(), UserRoleContextKey, "user")
	wUser := httptest.NewRecorder()
	adminRoleHandler(wUser, reqUser.WithContext(ctxUser))
	if wUser.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for user role, got %d", wUser.Code)
	}

	// 2. User with "admin" role calling admin endpoint -> 200 OK
	reqAdmin := httptest.NewRequest("POST", "/api/system/restart", nil)
	ctxAdmin := context.WithValue(reqAdmin.Context(), UserRoleContextKey, "admin")
	wAdmin := httptest.NewRecorder()
	adminRoleHandler(wAdmin, reqAdmin.WithContext(ctxAdmin))
	if wAdmin.Code != http.StatusOK {
		t.Errorf("expected 200 OK for admin role, got %d", wAdmin.Code)
	}
}
