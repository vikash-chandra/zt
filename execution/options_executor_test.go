package execution

import (
	"fmt"
	"testing"
	"time"
	"zerodha-trading/data"

	"go.uber.org/zap"
)

// MockBrokerClient implements data.BrokerClient for testing
type MockBrokerClient struct {
	LastOrderParams data.OrderParams
	LastVariety     string
	PlacedOrderID   string
	PlaceOrderErr   error
}

func (m *MockBrokerClient) SetAccessToken(token string) {}

func (m *MockBrokerClient) GetPositions() (data.Positions, error) {
	return data.Positions{}, nil
}

func (m *MockBrokerClient) GetOrders() ([]data.Order, error) {
	return []data.Order{}, nil
}

func (m *MockBrokerClient) PlaceOrder(variety string, params data.OrderParams) (data.OrderResponse, error) {
	m.LastVariety = variety
	m.LastOrderParams = params
	if m.PlaceOrderErr != nil {
		return data.OrderResponse{}, m.PlaceOrderErr
	}
	id := m.PlacedOrderID
	if id == "" {
		id = fmt.Sprintf("MOCK-ORD-%d", time.Now().UnixNano())
	}
	return data.OrderResponse{OrderID: id}, nil
}

func (m *MockBrokerClient) CancelOrder(variety string, orderID string, parentOrderID *string) (data.OrderResponse, error) {
	return data.OrderResponse{OrderID: orderID}, nil
}

func (m *MockBrokerClient) ModifyOrder(variety string, orderID string, params data.OrderParams) (data.OrderResponse, error) {
	m.LastVariety = variety
	m.LastOrderParams = params
	return data.OrderResponse{OrderID: orderID}, nil
}

func (m *MockBrokerClient) GetOrderHistory(orderID string) ([]data.Order, error) {
	return []data.Order{}, nil
}

func (m *MockBrokerClient) GetHistoricalData(instrumentToken int, interval string, fromTime time.Time, toTime time.Time, continuous bool, oi bool) ([]data.HistoricalData, error) {
	return []data.HistoricalData{}, nil
}

func (m *MockBrokerClient) GetInstrumentsByExchange(exchange string) (data.Instruments, error) {
	return data.Instruments{}, nil
}

func (m *MockBrokerClient) GetOHLC(keys ...string) (data.QuoteOHLC, error) {
	return data.QuoteOHLC{}, nil
}

func (m *MockBrokerClient) GetOrderMargins(params []data.OrderParams) ([]data.OrderMargins, error) {
	return []data.OrderMargins{}, nil
}

func (m *MockBrokerClient) GetQuote(instruments ...string) (map[string]data.Quote, error) {
	return map[string]data.Quote{}, nil
}

func (m *MockBrokerClient) GenerateSession(requestToken string, apiSecret string) (string, error) {
	return "mock-session", nil
}

func TestOptionsExecutor_PaperMode(t *testing.T) {
	logger := zap.NewNop()
	mockBroker := &MockBrokerClient{}
	exec := NewOptionsExecutor(mockBroker, logger, false) // LiveTrading = false

	// Test ExecuteOptionOrder in Paper Mode
	orderID, fillPrice, err := exec.ExecuteOptionOrder("NIFTY24200PE", "SELL", 65, 120.0)
	if err != nil {
		t.Fatalf("Expected no error in paper mode, got: %v", err)
	}
	if fillPrice != 120.0 {
		t.Errorf("Expected fill price 120.0, got: %f", fillPrice)
	}
	if orderID == "" {
		t.Errorf("Expected non-empty order ID in paper mode")
	}

	// Test PlaceOptionSLOrder in Paper Mode
	slID, errSL := exec.PlaceOptionSLOrder("NIFTY24200PE", 65, 180.0)
	if errSL != nil {
		t.Fatalf("Expected no error in paper SL placement, got: %v", errSL)
	}
	if slID == "" {
		t.Errorf("Expected non-empty SL order ID in paper mode")
	}
}

func TestOptionsExecutor_LiveMode_AggressiveLimitOrder(t *testing.T) {
	logger := zap.NewNop()
	mockBroker := &MockBrokerClient{PlacedOrderID: "LIVE-12345"}
	exec := NewOptionsExecutor(mockBroker, logger, true) // LiveTrading = true

	// 1. Test SELL Order (Aggressive Limit: 95% of LTP)
	orderID, fillPrice, err := exec.ExecuteOptionOrder("NIFTY24200PE", "SELL", 65, 100.0)
	if err != nil {
		t.Fatalf("Expected no error placing live SELL order, got: %v", err)
	}
	if orderID != "LIVE-12345" {
		t.Errorf("Expected order ID LIVE-12345, got: %s", orderID)
	}
	if fillPrice != 100.0 {
		t.Errorf("Expected fill price 100.0, got: %f", fillPrice)
	}

	// Verify Zerodha API Order Parameters for SELL
	p := mockBroker.LastOrderParams
	if p.Exchange != "NFO" {
		t.Errorf("Expected Exchange NFO, got: %s", p.Exchange)
	}
	if p.TradingSymbol != "NIFTY24200PE" {
		t.Errorf("Expected TradingSymbol NIFTY24200PE, got: %s", p.TradingSymbol)
	}
	if p.TransactionType != "SELL" {
		t.Errorf("Expected TransactionType SELL, got: %s", p.TransactionType)
	}
	if p.OrderType != "LIMIT" {
		t.Errorf("Expected OrderType LIMIT for Zerodha API compliance, got: %s", p.OrderType)
	}
	if p.Price != 95.0 { // 95% of 100.0
		t.Errorf("Expected SELL limit price 95.0 (95%% of LTP), got: %f", p.Price)
	}

	// 2. Test BUY Order (Aggressive Limit: 105% of LTP)
	_, _, errBUY := exec.ExecuteOptionOrder("NIFTY24200PE", "BUY", 65, 100.0)
	if errBUY != nil {
		t.Fatalf("Expected no error placing live BUY order, got: %v", errBUY)
	}
	pBuy := mockBroker.LastOrderParams
	if pBuy.Price != 105.0 { // 105% of 100.0
		t.Errorf("Expected BUY limit price 105.0 (105%% of LTP), got: %f", pBuy.Price)
	}
}

func TestOptionsExecutor_LiveMode_PlaceOptionSLOrder(t *testing.T) {
	logger := zap.NewNop()
	mockBroker := &MockBrokerClient{PlacedOrderID: "LIVE-SL-999"}
	exec := NewOptionsExecutor(mockBroker, logger, true) // LiveTrading = true

	// Test SL Order Placement with Trigger = 180.0
	slID, err := exec.PlaceOptionSLOrder("NIFTY24200PE", 65, 180.0)
	if err != nil {
		t.Fatalf("Expected no error placing live SL order, got: %v", err)
	}
	if slID != "LIVE-SL-999" {
		t.Errorf("Expected SL order ID LIVE-SL-999, got: %s", slID)
	}

	// Verify Zerodha SL-Limit API Order Parameters
	p := mockBroker.LastOrderParams
	if p.Exchange != "NFO" {
		t.Errorf("Expected Exchange NFO, got: %s", p.Exchange)
	}
	if p.OrderType != "SL" {
		t.Errorf("Expected OrderType SL (Stop Loss Limit) for Zerodha API compliance, got: %s", p.OrderType)
	}
	if p.TriggerPrice != 180.0 {
		t.Errorf("Expected TriggerPrice 180.0, got: %f", p.TriggerPrice)
	}
	expectedLimit := 189.0 // 105% of 180.0 = 189.0
	if p.Price != expectedLimit {
		t.Errorf("Expected SL Limit Price %f (105%% of Trigger), got: %f", expectedLimit, p.Price)
	}
}

func TestOptionsExecutor_ModifyOptionSLOrder(t *testing.T) {
	logger := zap.NewNop()
	mockBroker := &MockBrokerClient{}

	// 1. Live Mode Test
	execLive := NewOptionsExecutor(mockBroker, logger, true)
	err := execLive.ModifyOptionSLOrder("LIVE-SL-999", "NIFTY24200PE", 65, 140.0)
	if err != nil {
		t.Fatalf("Expected no error modifying live SL order, got: %v", err)
	}
	if mockBroker.LastOrderParams.TriggerPrice != 140.0 {
		t.Errorf("Expected modified trigger price 140.0, got: %f", mockBroker.LastOrderParams.TriggerPrice)
	}
	expectedLimit := 147.0 // 140 * 1.05
	if mockBroker.LastOrderParams.Price != expectedLimit {
		t.Errorf("Expected modified limit price %f, got: %f", expectedLimit, mockBroker.LastOrderParams.Price)
	}

	// 2. Paper Mode Test
	execPaper := NewOptionsExecutor(mockBroker, logger, false)
	errPaper := execPaper.ModifyOptionSLOrder("PAPER-SL-123", "NIFTY24200PE", 65, 130.0)
	if errPaper != nil {
		t.Fatalf("Expected no error for paper SL order modification, got: %v", errPaper)
	}
}

