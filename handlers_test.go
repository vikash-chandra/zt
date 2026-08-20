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
	"time"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
)

type MockBrokerClient struct {
	AccessToken string
}

func (m *MockBrokerClient) SetAccessToken(token string) {
	m.AccessToken = token
}
func (m *MockBrokerClient) GetPositions() (data.Positions, error) { return data.Positions{}, nil }
func (m *MockBrokerClient) GetOrders() ([]data.Order, error)      { return nil, nil }
func (m *MockBrokerClient) PlaceOrder(variety string, params data.OrderParams) (data.OrderResponse, error) {
	return data.OrderResponse{}, nil
}
func (m *MockBrokerClient) CancelOrder(variety string, orderID string, parentOrderID *string) (data.OrderResponse, error) {
	return data.OrderResponse{}, nil
}
func (m *MockBrokerClient) GetOrderHistory(orderID string) ([]data.Order, error) { return nil, nil }
func (m *MockBrokerClient) GetHistoricalData(instrumentToken int, interval string, fromTime time.Time, toTime time.Time, continuous bool, oi bool) ([]data.HistoricalData, error) {
	return nil, nil
}
func (m *MockBrokerClient) GetInstrumentsByExchange(exchange string) (data.Instruments, error) {
	return nil, nil
}
func (m *MockBrokerClient) GetOHLC(keys ...string) (data.QuoteOHLC, error) { return nil, nil }
func (m *MockBrokerClient) GetOrderMargins(params []data.OrderParams) ([]data.OrderMargins, error) {
	return nil, nil
}
func (m *MockBrokerClient) ModifyOrder(variety string, orderID string, params data.OrderParams) (data.OrderResponse, error) {
	return data.OrderResponse{}, nil
}
func (m *MockBrokerClient) GetQuote(instruments ...string) (map[string]data.Quote, error) {
	return nil, nil
}
func (m *MockBrokerClient) GenerateSession(requestToken string, apiSecret string) (string, error) {
	return "", nil
}

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
		APIKey:      "api_key",
		AccessToken: "initial_token",
		TokenPrefix: "vcj:zt-token:",
	}

	bot := &TradingBot{
		cfg:        cfg,
		ctx:        context.Background(),
		logger:     logger,
		kiteClient: &MockBrokerClient{},
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
	reqBody, _ := json.Marshal(map[string]string{"request_token": "my_new_access_token"})
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
	reqBody, _ = json.Marshal(map[string]string{"request_token": "vcj:zt-token:my_secret_token_123"})
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
	reqBody, _ = json.Marshal(map[string]string{"request_token": ""})
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

func TestHandleConfigAll(t *testing.T) {
	logger, _ := monitoring.NewLogger("info")
	cfg := &config.Settings{
		RestartAllowedBefore: "09:15",
		RestartAllowedAfter:  "15:45",
	}

	bot := &TradingBot{
		cfg:             cfg,
		ctx:             context.Background(),
		logger:          logger,
		optIndexConfigs: make(map[string]*data.OptionsIndexConfig),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/config/all", nil)
	w := httptest.NewRecorder()

	bot.handleConfigAll(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}

	if _, ok := resp["server_time_ist"]; !ok {
		t.Errorf("expected server_time_ist in response")
	}
	if _, ok := resp["restart_window"]; !ok {
		t.Errorf("expected restart_window in response")
	}
}

func TestHandleSystemRestart_MarketHoursLock(t *testing.T) {
	logger, _ := monitoring.NewLogger("info")
	cfg := &config.Settings{
		RestartAllowedBefore: "09:15",
		RestartAllowedAfter:  "15:45",
	}

	bot := &TradingBot{
		cfg:    cfg,
		ctx:    context.Background(),
		logger: logger,
	}

	// 1. Test wrong HTTP method
	reqGet := httptest.NewRequest(http.MethodGet, "/api/system/restart", nil)
	wGet := httptest.NewRecorder()
	bot.handleSystemRestart(wGet, reqGet)
	if wGet.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 for GET, got %d", wGet.Code)
	}

	// 2. Test POST request
	reqPost := httptest.NewRequest(http.MethodPost, "/api/system/restart", nil)
	wPost := httptest.NewRecorder()
	bot.handleSystemRestart(wPost, reqPost)

	// Since current time is known (e.g. night 21:00 IST or during market),
	// we verify that the response status accurately matches the restart_allowed logic!
	loc, _ := time.LoadLocation("Asia/Kolkata")
	now := time.Now().In(loc)
	nowMins := now.Hour()*60 + now.Minute()
	allowedBefore := 9*60 + 15
	allowedAfter := 15*60 + 45
	isAllowed := nowMins < allowedBefore || nowMins >= allowedAfter

	if isAllowed {
		if wPost.Code != http.StatusOK {
			t.Errorf("expected status 200 during pre/post-market, got %d", wPost.Code)
		}
	} else {
		if wPost.Code != http.StatusForbidden {
			t.Errorf("expected status 403 during market hours lock, got %d", wPost.Code)
		}
		var errResp map[string]interface{}
		_ = json.Unmarshal(wPost.Body.Bytes(), &errResp)
		if !strings.Contains(errResp["error"].(string), "locked during live market hours") {
			t.Errorf("unexpected error message: %v", errResp["error"])
		}
	}
}
