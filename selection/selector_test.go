package selection

import (
	"context"
	"testing"
	"zerodha-trading/config"
	"zerodha-trading/data"

	"go.uber.org/zap"
)

func TestSelectorRegistry(t *testing.T) {
	selectors := InitializeSelectors([]string{"SECURITIES_FO", "INVALID_NAME"}, &config.Settings{}, nil)

	if len(selectors) != 1 {
		t.Errorf("expected registry size of 1, got %d", len(selectors))
	}

	foSelector, exists := selectors["SECURITIES_FO"]
	if !exists {
		t.Fatal("expected SECURITIES_FO selector to be registered")
	}

	if foSelector.Name() != "SECURITIES_FO" {
		t.Errorf("expected selector name SECURITIES_FO, got %s", foSelector.Name())
	}
}

// MockSelector implements selection.Selector for testing integration behavior
type MockSelector struct {
	NameStr       string
	MockWatchlist map[string]int64
}

func (m *MockSelector) Name() string { return m.NameStr }

func (m *MockSelector) SelectStocks(ctx context.Context, logger *zap.Logger, client data.BrokerClient, secMaster *data.SecurityMaster, bias string, size int, maxPctChange float64) (map[string]int64, error) {
	return m.MockWatchlist, nil
}

func TestCompositeSelector(t *testing.T) {
	sel1 := &MockSelector{NameStr: "SEL1", MockWatchlist: map[string]int64{"TCS": 1364481, "RELIANCE": 1333761}}
	sel2 := &MockSelector{NameStr: "SEL2", MockWatchlist: map[string]int64{"INFY": 408065, "TCS": 1364481}}

	composite := NewCompositeSelector([]Selector{sel1, sel2})
	if composite.Name() != "SEL1+SEL2" {
		t.Errorf("expected composite name 'SEL1+SEL2', got '%s'", composite.Name())
	}

	logger := zap.NewNop()
	results, err := composite.SelectStocks(context.Background(), logger, nil, nil, "BULLISH", 15, 2.5)
	if err != nil {
		t.Fatalf("unexpected error in SelectStocks: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected merged size of 3, got %d", len(results))
	}

	if results["TCS"] != 1364481 || results["RELIANCE"] != 1333761 || results["INFY"] != 408065 {
		t.Errorf("unexpected token mapping in composite results: %v", results)
	}
}

func TestPriorityRankingResolution(t *testing.T) {
	configs := DefaultStockSelectionConfigs()
	// Default configs: PDH_PDL is Rank 1, FO is Rank 7, QUANT_SCANNER is Rank 9
	// Let's customize PDH_PDL level shift to -2.0%
	pdhCfg := configs["PDH_PDL"]
	pdhCfg.LevelShiftPct = -2.0
	configs["PDH_PDL"] = pdhCfg

	// Symbol TCS has multiple candidate selectors: ["FO", "PDH-PDL", "QUANT_SCANNER"]
	winner, shift := ResolveWinningSelector("TCS", []string{"FO", "PDH-PDL", "QUANT_SCANNER"}, configs)
	if winner != "PDH_PDL" {
		t.Errorf("expected winner PDH_PDL, got %s", winner)
	}
	if shift != -2.0 {
		t.Errorf("expected shift -2.0, got %f", shift)
	}

	// Disable PDH_PDL -> winner should be next highest rank (FO)
	pdhCfg.Enabled = false
	configs["PDH_PDL"] = pdhCfg

	winner2, shift2 := ResolveWinningSelector("TCS", []string{"FO", "PDH-PDL", "QUANT_SCANNER"}, configs)
	if winner2 != "FO" {
		t.Errorf("expected winner FO when PDH_PDL disabled, got %s", winner2)
	}
	if shift2 != 0.0 {
		t.Errorf("expected shift 0.0, got %f", shift2)
	}
}

func TestCalculateLevelShiftedPrice(t *testing.T) {
	// 1. High Breakout: Ref PDH = 200.0, Shift = -2.0% -> Expected 200 * 0.98 = 196.0
	shiftedHigh := CalculateLevelShiftedPrice(200.0, -2.0, 0.05)
	if shiftedHigh != 196.0 {
		t.Errorf("expected shifted price 196.0, got %f", shiftedHigh)
	}

	// 2. Low Breakdown: Ref PDL = 100.0, Shift = +2.0% -> Expected 100 * 1.02 = 102.0
	shiftedLow := CalculateLevelShiftedPrice(100.0, 2.0, 0.05)
	if shiftedLow != 102.0 {
		t.Errorf("expected shifted price 102.0, got %f", shiftedLow)
	}

	// 3. Default 0.0% Shift -> Exact price
	unchanged := CalculateLevelShiftedPrice(150.0, 0.0, 0.05)
	if unchanged != 150.0 {
		t.Errorf("expected 150.0, got %f", unchanged)
	}
}

func TestCompositeSizeLimitation(t *testing.T) {
	sel1 := &MockSelector{NameStr: "SEL1", MockWatchlist: map[string]int64{"TCS": 1364481, "RELIANCE": 1333761}}
	sel2 := &MockSelector{NameStr: "SEL2", MockWatchlist: map[string]int64{"INFY": 408065, "TCS": 1364481}}
	composite := NewCompositeSelector([]Selector{sel1, sel2})
	logger := zap.NewNop()

	// Test size limitation/truncation
	resultsLimit, err := composite.SelectStocks(context.Background(), logger, nil, nil, "BULLISH", 2, 2.5)
	if err != nil {
		t.Fatalf("unexpected error in SelectStocks: %v", err)
	}

	if len(resultsLimit) != 2 {
		t.Errorf("expected size-truncated merged size of 2, got %d", len(resultsLimit))
	}
}
