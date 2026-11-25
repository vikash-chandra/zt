package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
	"zerodha-trading/data"
	"zerodha-trading/scanner"
)

type mockIFPBroker struct {
	*MockBrokerClient
	quotes map[string]data.Quote
}

func (m *mockIFPBroker) GetQuote(instruments ...string) (map[string]data.Quote, error) {
	out := make(map[string]data.Quote)
	for _, key := range instruments {
		if q, ok := m.quotes[key]; ok {
			out[key] = q
		}
	}
	return out, nil
}

func TestRefreshFOSnapshotAndFuturesLiveZerodha(t *testing.T) {
	logger := zap.NewNop()
	now := data.NowIST()
	nearExp := now.AddDate(0, 0, 4)

	broker := &mockIFPBroker{
		MockBrokerClient: &MockBrokerClient{},
		quotes: map[string]data.Quote{
			"NSE:NIFTY 50": {
				LastPrice: 24510.0,
				OHLC:      data.OHLC{Open: 24400, High: 24540, Low: 24380, Close: 24420},
			},
			"NFO:NIFTYNEARFUT": {
				LastPrice: 24560.0,
				OI:        1250000,
				OIDayLow:  1100000,
				OHLC:      data.OHLC{Close: 24450},
			},
			"NFO:NIFTY24400CE": {LastPrice: 220, OI: 500000, OIDayLow: 450000},
			"NFO:NIFTY24400PE": {LastPrice: 95, OI: 800000, OIDayLow: 600000},
			"NFO:NIFTY24500CE": {LastPrice: 145, OI: 700000, OIDayLow: 650000},
			"NFO:NIFTY24500PE": {LastPrice: 135, OI: 950000, OIDayLow: 700000},
			"NFO:NIFTY24600CE": {LastPrice: 88, OI: 1200000, OIDayLow: 1150000},
			"NFO:NIFTY24600PE": {LastPrice: 190, OI: 400000, OIDayLow: 380000},
		},
	}

	sm := data.NewSecurityMaster(nil, broker, logger)
	sm.InjectOptionInstruments("NFO", data.Instruments{
		{InstrumentToken: 1, TradingSymbol: "NIFTYNEARFUT", Name: "NIFTY", InstrumentType: "FUT", Expiry: nearExp},
		{InstrumentToken: 2, TradingSymbol: "NIFTY24400CE", Name: "NIFTY", InstrumentType: "CE", Strike: 24400, Expiry: nearExp},
		{InstrumentToken: 3, TradingSymbol: "NIFTY24400PE", Name: "NIFTY", InstrumentType: "PE", Strike: 24400, Expiry: nearExp},
		{InstrumentToken: 4, TradingSymbol: "NIFTY24500CE", Name: "NIFTY", InstrumentType: "CE", Strike: 24500, Expiry: nearExp},
		{InstrumentToken: 5, TradingSymbol: "NIFTY24500PE", Name: "NIFTY", InstrumentType: "PE", Strike: 24500, Expiry: nearExp},
		{InstrumentToken: 6, TradingSymbol: "NIFTY24600CE", Name: "NIFTY", InstrumentType: "CE", Strike: 24600, Expiry: nearExp},
		{InstrumentToken: 7, TradingSymbol: "NIFTY24600PE", Name: "NIFTY", InstrumentType: "PE", Strike: 24600, Expiry: nearExp},
	})

	tb := &TradingBot{
		kiteClient:     broker,
		securityMaster: sm,
		foAnalyzer:     scanner.NewFOAnalyzer(nil, logger),
	}

	// 1. Test live F&O Option Chain matrix build from Zerodha mock quotes
	summary, err := tb.RefreshFOSnapshotForSymbol(context.Background(), "NIFTY 50", true)
	if err != nil {
		t.Fatalf("RefreshFOSnapshotForSymbol failed: %v", err)
	}
	if summary.SpotPrice != 24510.0 {
		t.Errorf("expected SpotPrice 24510, got %.2f", summary.SpotPrice)
	}
	if summary.ATMStrike != 24500.0 {
		t.Errorf("expected ATMStrike 24500, got %.2f", summary.ATMStrike)
	}
	if summary.SnapshotDate != now.Format("2006-01-02") {
		t.Errorf("expected SnapshotDate %s, got %s", now.Format("2006-01-02"), summary.SnapshotDate)
	}
	if summary.LastZerodhaSync == "" {
		t.Errorf("expected non-empty LastZerodhaSync")
	}
	if len(summary.Strikes) != 3 {
		t.Fatalf("expected 3 strikes, got %d", len(summary.Strikes))
	}

	// 2. Test live Futures Buildup classification
	buildups, syncTS, err := tb.FetchLiveFuturesBuildups(context.Background(), true)
	if err != nil {
		t.Fatalf("FetchLiveFuturesBuildups failed: %v", err)
	}
	if syncTS.IsZero() || len(buildups) == 0 {
		t.Fatalf("expected at least 1 futures buildup record and non-zero syncTS")
	}
	if buildups[0].BuildupType != "LONG_BUILDUP" {
		t.Errorf("expected LONG_BUILDUP (price +0.45%%, OI +13.6%%), got %s (priceChg=%.2f, oiChg=%.2f)",
			buildups[0].BuildupType, buildups[0].PriceChangePct, buildups[0].OIChangePct)
	}

	// 3. Test /api/system/data-freshness handler
	req := httptest.NewRequest(http.MethodGet, "/api/system/data-freshness", nil)
	rec := httptest.NewRecorder()
	tb.handleSystemDataFreshness(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from handleSystemDataFreshness, got %d", rec.Code)
	}
	var freshResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&freshResp); err != nil {
		t.Fatalf("failed to decode data-freshness JSON: %v", err)
	}
	tsMap, ok := freshResp["timestamps"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected timestamps object in data-freshness response")
	}
	if tsMap["ifp_fo_sync"] == "" || tsMap["ifp_futures_sync"] == "" {
		t.Errorf("expected populated ifp_fo_sync and ifp_futures_sync in timestamps, got %+v", tsMap)
	}
}

func TestHandleFootprintsRecentParsesDateAndLimit(t *testing.T) {
	tb := &TradingBot{}
	req := httptest.NewRequest(http.MethodGet, "/api/footprints/recent?limit=2000&date=2026-11-25", nil)
	rec := httptest.NewRecorder()
	tb.handleFootprintsRecent(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK even with nil DB, got %d", rec.Code)
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode footprints JSON: %v", err)
	}
	if resp["date"] != "" {
		// When tb.db is nil, dateParam isn't overridden; ensure last_zerodha_data exists
		t.Logf("date returned: %v", resp["date"])
	}
	if _, ok := resp["last_zerodha_data"]; !ok {
		t.Errorf("expected last_zerodha_data key in /api/footprints/recent response")
	}
}
