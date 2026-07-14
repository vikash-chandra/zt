package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"

	"github.com/zerodha/gokiteconnect/v4"
)

type mockTicker struct {
	data.RobustKiteTicker
	token string
}

func (m *mockTicker) SetAccessToken(t string) {
	m.token = t
}

func TestHandleConfigAccessToken(t *testing.T) {
	// Create a temporary .env file
	tmpFile, err := os.CreateTemp("", "test_env_*.env")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	tmpFile.Close()

	// Initialize TradingBot fields enough to prevent panics
	logger, err := monitoring.NewLogger("info")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	cfg := &config.Settings{
		AccessToken: "initial_token",
		TokenPrefix: "vcj:zt-token:",
	}

	bot := &TradingBot{
		cfg:        cfg,
		ctx:        context.Background(),
		logger:     logger,
		kiteClient: kiteconnect.New("api_key"),
		ticker:     &data.RobustKiteTicker{}, // we can update access token on this directly
	}

	// We'll write the env helper test against tmpPath. For handleConfigAccessToken,
	// it uses ".env" hardcoded, which might modify the actual workspace .env.
	// To prevent changing the user's local .env in the workspace, we should mock or
	// temporarily copy the .env, or handle the test gracefully.
	// Let's backup the workspace .env if it exists, run our test, and restore it!
	var envBackup []byte
	envExists := false
	if _, err := os.Stat(".env"); err == nil {
		envBackup, _ = os.ReadFile(".env")
		envExists = true
	}

	defer func() {
		if envExists {
			_ = os.WriteFile(".env", envBackup, 0644)
		} else {
			_ = os.Remove(".env")
		}
	}()

	// 1. Test standard token submission
	reqBody, _ := json.Marshal(map[string]string{"access_token": "my_new_access_token"})
	req := httptest.NewRequest(http.MethodPost, "/api/config/access-token", bytes.NewBuffer(reqBody))
	w := httptest.NewRecorder()

	bot.handleConfigAccessToken(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	if bot.cfg.AccessToken != "my_new_access_token" {
		t.Errorf("expected AccessToken to be 'my_new_access_token', got '%s'", bot.cfg.AccessToken)
	}

	// 2. Test token with prefix: vcj:zt-token:
	reqBody, _ = json.Marshal(map[string]string{"access_token": "vcj:zt-token:my_secret_token_123"})
	req = httptest.NewRequest(http.MethodPost, "/api/config/access-token", bytes.NewBuffer(reqBody))
	w = httptest.NewRecorder()

	bot.handleConfigAccessToken(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	if bot.cfg.AccessToken != "my_secret_token_123" {
		t.Errorf("expected parsed AccessToken to be 'my_secret_token_123', got '%s'", bot.cfg.AccessToken)
	}

	// Verify it wrote correctly to the .env file
	envContent, err := os.ReadFile(".env")
	if err != nil {
		t.Fatalf("failed to read .env: %v", err)
	}
	if !strings.Contains(string(envContent), "KITE_ACCESS_TOKEN=my_secret_token_123") {
		t.Errorf("expected .env to contain updated token, got:\n%s", string(envContent))
	}

	// 3. Test empty token validation
	reqBody, _ = json.Marshal(map[string]string{"access_token": ""})
	req = httptest.NewRequest(http.MethodPost, "/api/config/access-token", bytes.NewBuffer(reqBody))
	w = httptest.NewRecorder()

	bot.handleConfigAccessToken(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for empty token, got %d", w.Code)
	}

	// 4. Test wrong method (GET instead of POST)
	req = httptest.NewRequest(http.MethodGet, "/api/config/access-token", nil)
	w = httptest.NewRecorder()

	bot.handleConfigAccessToken(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for GET request, got %d", w.Code)
	}
}
