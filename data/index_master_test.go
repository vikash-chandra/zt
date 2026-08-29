package data

import (
	"context"
	"testing"
	"time"
)

func TestIndexMasterRegistry(t *testing.T) {
	tests := []struct {
		input           string
		expectedName    string
		expectedPrefix  string
		expectedToken   int64
		expectedExch    string
		expectedLot     int
		expectedWeekday time.Weekday
	}{
		{"NIFTY 50", "NIFTY 50", "NIFTY", 256265, "NFO", 65, time.Thursday},
		{"nifty", "NIFTY 50", "NIFTY", 256265, "NFO", 65, time.Thursday},
		{"BANKNIFTY", "NIFTY BANK", "BANKNIFTY", 260105, "NFO", 15, time.Wednesday},
		{"Nifty Bank", "NIFTY BANK", "BANKNIFTY", 260105, "NFO", 15, time.Wednesday},
		{"SENSEX", "BSE SENSEX", "SENSEX", 265, "BFO", 20, time.Friday},
		{"BSE SENSEX", "BSE SENSEX", "SENSEX", 265, "BFO", 20, time.Friday},
		{"FINNIFTY", "FINNIFTY", "FINNIFTY", 257801, "NFO", 65, time.Tuesday},
		{"MIDCPNIFTY", "MIDCPNIFTY", "MIDCPNIFTY", 288009, "NFO", 120, time.Monday},
	}

	for _, tt := range tests {
		spec, found := ResolveIndexSpec(tt.input)
		if !found {
			t.Errorf("expected to find spec for %s", tt.input)
			continue
		}
		if spec.Name != tt.expectedName {
			t.Errorf("for input %s: expected Name %s, got %s", tt.input, tt.expectedName, spec.Name)
		}
		if spec.CleanPrefix != tt.expectedPrefix {
			t.Errorf("for input %s: expected CleanPrefix %s, got %s", tt.input, tt.expectedPrefix, spec.CleanPrefix)
		}
		if spec.SpotToken != tt.expectedToken {
			t.Errorf("for input %s: expected SpotToken %d, got %d", tt.input, tt.expectedToken, spec.SpotToken)
		}
		if spec.OptionsExchange != tt.expectedExch {
			t.Errorf("for input %s: expected OptionsExchange %s, got %s", tt.input, tt.expectedExch, spec.OptionsExchange)
		}
		if spec.BaseLotSize != tt.expectedLot {
			t.Errorf("for input %s: expected BaseLotSize %d, got %d", tt.input, tt.expectedLot, spec.BaseLotSize)
		}
		if spec.ExpiryWeekday != tt.expectedWeekday {
			t.Errorf("for input %s: expected ExpiryWeekday %v, got %v", tt.input, tt.expectedWeekday, spec.ExpiryWeekday)
		}
	}
}

func TestParseOptionExpiryFromSymbol(t *testing.T) {
	tests := []struct {
		symbol   string
		expected string
	}{
		// Monthly options
		{"FINNIFTY26SEP26000PE", "2026-09-29"},   // Last Tuesday of Sep 2026
		{"NIFTY26SEP24350PE", "2026-09-24"},      // Last Thursday of Sep 2026
		{"BANKNIFTY26SEP50000CE", "2026-09-30"},  // Last Wednesday of Sep 2026
		{"SENSEX26SEP80000CE", "2026-09-25"},     // Last Friday of Sep 2026
		{"MIDCPNIFTY26SEP13000PE", "2026-09-28"}, // Last Monday of Sep 2026
		// Weekly options
		{"NIFTY2690124350PE", "2026-09-01"},
		{"NIFTY2690824350CE", "2026-09-08"},
		{"NIFTY26O1524350CE", "2026-10-15"},
		{"NIFTY26N1924350PE", "2026-11-19"},
		{"NIFTY26D2424350CE", "2026-12-24"},
	}

	for _, tt := range tests {
		t.Run(tt.symbol, func(t *testing.T) {
			got := ParseOptionExpiryFromSymbol(tt.symbol)
			if got != tt.expected {
				t.Errorf("ParseOptionExpiryFromSymbol(%s) = %s, expected %s", tt.symbol, got, tt.expected)
			}
		})
	}
}

func TestGetIndexOptionChain_MonthlyVsWeekly(t *testing.T) {
	sm := NewSecurityMaster(nil, nil, nil)
	sm.optCache = make(map[string]Instruments)
	sm.optCacheTime = make(map[string]time.Time)

	// Mock NFO instruments: 1 weekly (2026-09-01) and 1 monthly (2026-09-29)
	expWeekly := time.Date(2026, 9, 1, 15, 30, 0, 0, ISTLocation)
	expMonthly := time.Date(2026, 9, 29, 15, 30, 0, 0, ISTLocation)

	sm.optCache["NFO"] = Instruments{
		{
			Name:           "NIFTY",
			TradingSymbol:  "NIFTY2690124350PE",
			InstrumentType: "PE",
			Expiry:         expWeekly,
			Strike:         24350,
			Exchange:       "NFO",
		},
		{
			Name:           "NIFTY",
			TradingSymbol:  "NIFTY26SEP24350PE",
			InstrumentType: "PE",
			Expiry:         expMonthly,
			Strike:         24350,
			Exchange:       "NFO",
		},
	}
	sm.optCacheTime["NFO"] = time.Now()

	// 1. MONTHLY test: Must pick 2026-09-29 monthly contract
	monthlyChain, err := sm.GetIndexOptionChain(context.Background(), "NIFTY 50", "PE", "MONTHLY", 5)
	if err != nil {
		t.Fatalf("GetIndexOptionChain MONTHLY returned error: %v", err)
	}
	if len(monthlyChain) == 0 {
		t.Fatal("expected at least 1 contract for MONTHLY")
	}
	if monthlyChain[0].TradingSymbol != "NIFTY26SEP24350PE" {
		t.Errorf("expected NIFTY26SEP24350PE for MONTHLY, got %s", monthlyChain[0].TradingSymbol)
	}
	if monthlyChain[0].Expiry.Format("2006-01-02") != "2026-09-29" {
		t.Errorf("expected expiry 2026-09-29 for MONTHLY, got %s", monthlyChain[0].Expiry.Format("2006-01-02"))
	}

	// 2. WEEKLY test: Must pick 2026-09-01 weekly contract
	weeklyChain, err := sm.GetIndexOptionChain(context.Background(), "NIFTY 50", "PE", "WEEKLY", 5)
	if err != nil {
		t.Fatalf("GetIndexOptionChain WEEKLY returned error: %v", err)
	}
	if len(weeklyChain) == 0 {
		t.Fatal("expected at least 1 contract for WEEKLY")
	}
	if weeklyChain[0].TradingSymbol != "NIFTY2690124350PE" {
		t.Errorf("expected NIFTY2690124350PE for WEEKLY, got %s", weeklyChain[0].TradingSymbol)
	}
	if weeklyChain[0].Expiry.Format("2006-01-02") != "2026-09-01" {
		t.Errorf("expected expiry 2026-09-01 for WEEKLY, got %s", weeklyChain[0].Expiry.Format("2006-01-02"))
	}
}
