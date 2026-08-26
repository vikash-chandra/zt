package selection

import (
	"testing"
	"time"

	"zerodha-trading/data"
)

func TestMonthlyExpiryAndRollOver(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Kolkata")

	// Test 1: Mid-month date (e.g. 10 Aug 2026) -> Should select 27 Aug 2026 (last Thursday)
	aug10 := time.Date(2026, 8, 10, 10, 0, 0, 0, loc)
	expDate := GetMonthlyExpiryDate(aug10, 7)
	if expDate.Format("2006-01-02") != "2026-08-27" {
		t.Errorf("expected monthly expiry 2026-08-27, got %s", expDate.Format("2006-01-02"))
	}

	// Test 2: User example on 12 Aug 2026 -> Should select 27 Aug 2026
	aug12 := time.Date(2026, 8, 12, 9, 20, 0, 0, loc)
	expDate12 := GetMonthlyExpiryDate(aug12, 7)
	if expDate12.Format("2006-01-02") != "2026-08-27" {
		t.Errorf("expected monthly expiry 2026-08-27 for 12 Aug, got %s", expDate12.Format("2006-01-02"))
	}

	// Test 3: User example on 24 Aug 2026 (<= 7 days before 27 Aug) -> Should roll over to 24 Sep 2026
	aug24 := time.Date(2026, 8, 24, 10, 0, 0, 0, loc)
	rollExpDate := GetMonthlyExpiryDate(aug24, 7)
	if rollExpDate.Format("2006-01-02") != "2026-09-24" {
		t.Errorf("expected rollover monthly expiry 2026-09-24 for 24 Aug, got %s", rollExpDate.Format("2006-01-02"))
	}
}

func TestTargetPremiumStrikeSelector(t *testing.T) {
	selector := NewOptionStrikeSelector(nil)

	// 1. Test NIFTY 50 (NFO, 50-step, Thursday)
	resNifty, err := selector.SelectStrikeByTargetPremium("NIFTY 50", 24340.0, "BULLISH", 100.0, "MONTHLY", 7, nil)
	if err != nil {
		t.Fatalf("unexpected error selecting strike for NIFTY 50: %v", err)
	}
	if resNifty.BaseStrike != 24350.0 {
		t.Errorf("expected NIFTY BaseStrike 24350.0 (50 step), got %f", resNifty.BaseStrike)
	}
	if resNifty.OptionType != "PE" {
		t.Errorf("expected OptionType PE, got %s", resNifty.OptionType)
	}
	if resNifty.Exchange != "NFO" {
		t.Errorf("expected Exchange NFO, got %s", resNifty.Exchange)
	}

	// 2. Test BANK NIFTY (NFO, 100-step, Thursday)
	resBank, err := selector.SelectStrikeByTargetPremium("BANKNIFTY", 51240.0, "BEARISH", 250.0, "MONTHLY", 7, nil)
	if err != nil {
		t.Fatalf("unexpected error selecting strike for BANKNIFTY: %v", err)
	}
	if resBank.BaseStrike != 51200.0 {
		t.Errorf("expected BANKNIFTY BaseStrike 51200.0 (100 step), got %f", resBank.BaseStrike)
	}
	if resBank.OptionType != "CE" {
		t.Errorf("expected OptionType CE, got %s", resBank.OptionType)
	}
	if resBank.Exchange != "NFO" {
		t.Errorf("expected Exchange NFO, got %s", resBank.Exchange)
	}

	// 3. Test BSE SENSEX (BFO, 100-step, Friday)
	resSensex, err := selector.SelectStrikeByTargetPremium("SENSEX", 80420.0, "BULLISH", 250.0, "MONTHLY", 7, nil)
	if err != nil {
		t.Fatalf("unexpected error selecting strike for SENSEX: %v", err)
	}
	if resSensex.BaseStrike != 80400.0 {
		t.Errorf("expected SENSEX BaseStrike 80400.0, got %f", resSensex.BaseStrike)
	}
	if resSensex.Exchange != "BFO" {
		t.Errorf("expected SENSEX Exchange BFO, got %s", resSensex.Exchange)
	}
}

// MockBrokerClient implements minimal data.BrokerClient for testing
type mockBrokerQuoteClient struct {
	quotes map[string]data.Quote
}

func (m *mockBrokerQuoteClient) SetAccessToken(token string) {}
func (m *mockBrokerQuoteClient) GenerateSession(requestToken string, apiSecret string) (string, error) {
	return "", nil
}
func (m *mockBrokerQuoteClient) GetOrderHistory(orderID string) ([]data.Order, error) {
	return nil, nil
}
func (m *mockBrokerQuoteClient) GetQuote(keys ...string) (map[string]data.Quote, error) {
	return m.quotes, nil
}
func (m *mockBrokerQuoteClient) GetInstrumentsByExchange(exchange string) (data.Instruments, error) {
	return nil, nil
}
func (m *mockBrokerQuoteClient) GetHistoricalData(token int, interval string, from, to time.Time, continuous, oi bool) ([]data.HistoricalData, error) {
	return nil, nil
}
func (m *mockBrokerQuoteClient) GetOHLC(keys ...string) (data.QuoteOHLC, error) {
	return nil, nil
}
func (m *mockBrokerQuoteClient) GetOrderMargins(params []data.OrderParams) ([]data.OrderMargins, error) {
	return nil, nil
}
func (m *mockBrokerQuoteClient) PlaceOrder(variety string, params data.OrderParams) (data.OrderResponse, error) {
	return data.OrderResponse{}, nil
}
func (m *mockBrokerQuoteClient) ModifyOrder(variety string, orderID string, params data.OrderParams) (data.OrderResponse, error) {
	return data.OrderResponse{}, nil
}
func (m *mockBrokerQuoteClient) CancelOrder(variety string, orderID string, parentOrderID *string) (data.OrderResponse, error) {
	return data.OrderResponse{}, nil
}
func (m *mockBrokerQuoteClient) GetPositions() (data.Positions, error) {
	return data.Positions{}, nil
}
func (m *mockBrokerQuoteClient) GetOrders() ([]data.Order, error) {
	return nil, nil
}

func TestSelectStrikeByTargetPremium_MonthlyVsWeeklyAndPremiumPick(t *testing.T) {
	secMaster := data.NewSecurityMaster(nil, nil, nil)
	mockBroker := &mockBrokerQuoteClient{
		quotes: map[string]data.Quote{
			"NFO:NIFTY26SEP24300PE": {LastPrice: 155.0},
			"NFO:NIFTY26SEP24200PE": {LastPrice: 98.5}, // Nearest to target 100.0! (diff 1.5)
			"NFO:NIFTY26SEP24100PE": {LastPrice: 62.0},
			"NFO:NIFTY2690124300PE": {LastPrice: 104.0}, // Weekly nearest to 100.0! (diff 4.0)
			"NFO:NIFTY2690124200PE": {LastPrice: 55.0},
		},
	}

	expWeekly := time.Date(2026, 9, 1, 15, 30, 0, 0, data.ISTLocation)
	expMonthly := time.Date(2026, 9, 29, 15, 30, 0, 0, data.ISTLocation)

	secMaster.InjectOptionInstruments("NFO", data.Instruments{
		{Name: "NIFTY", TradingSymbol: "NIFTY2690124300PE", InstrumentType: "PE", Expiry: expWeekly, Strike: 24300, Exchange: "NFO"},
		{Name: "NIFTY", TradingSymbol: "NIFTY2690124200PE", InstrumentType: "PE", Expiry: expWeekly, Strike: 24200, Exchange: "NFO"},
		{Name: "NIFTY", TradingSymbol: "NIFTY26SEP24300PE", InstrumentType: "PE", Expiry: expMonthly, Strike: 24300, Exchange: "NFO"},
		{Name: "NIFTY", TradingSymbol: "NIFTY26SEP24200PE", InstrumentType: "PE", Expiry: expMonthly, Strike: 24200, Exchange: "NFO"},
		{Name: "NIFTY", TradingSymbol: "NIFTY26SEP24100PE", InstrumentType: "PE", Expiry: expMonthly, Strike: 24100, Exchange: "NFO"},
	})

	selector := NewOptionStrikeSelector(secMaster)

	// 1. Test MONTHLY with target premium 100.0:
	// Must pick NIFTY26SEP24200PE (LTP 98.5, closest to 100.0) with expiry 2026-09-29
	resMonthly, err := selector.SelectStrikeByTargetPremium("NIFTY 50", 24350.0, "BULLISH", 100.0, "MONTHLY", 5, mockBroker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resMonthly.OptionSymbol != "NIFTY26SEP24200PE" {
		t.Errorf("expected NIFTY26SEP24200PE, got %s", resMonthly.OptionSymbol)
	}
	if resMonthly.ExpiryDate != "2026-09-29" {
		t.Errorf("expected ExpiryDate 2026-09-29, got %s", resMonthly.ExpiryDate)
	}
	if resMonthly.SelectedLTP != 98.5 {
		t.Errorf("expected SelectedLTP 98.5, got %f", resMonthly.SelectedLTP)
	}

	// 2. Test WEEKLY with target premium 100.0:
	// Must pick NIFTY2690124300PE (LTP 104.0, closest to 100.0) with expiry 2026-09-01
	resWeekly, err := selector.SelectStrikeByTargetPremium("NIFTY 50", 24350.0, "BULLISH", 100.0, "WEEKLY", 5, mockBroker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resWeekly.OptionSymbol != "NIFTY2690124300PE" {
		t.Errorf("expected NIFTY2690124300PE, got %s", resWeekly.OptionSymbol)
	}
	if resWeekly.ExpiryDate != "2026-09-01" {
		t.Errorf("expected ExpiryDate 2026-09-01, got %s", resWeekly.ExpiryDate)
	}
	if resWeekly.SelectedLTP != 104.0 {
		t.Errorf("expected SelectedLTP 104.0, got %f", resWeekly.SelectedLTP)
	}
}

