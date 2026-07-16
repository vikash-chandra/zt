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
		t.Errorf("unexpected merged map content: %+v", results)
	}

	// Test size limitation/truncation
	resultsLimit, err := composite.SelectStocks(context.Background(), logger, nil, nil, "BULLISH", 2, 2.5)
	if err != nil {
		t.Fatalf("unexpected error in SelectStocks: %v", err)
	}

	if len(resultsLimit) != 2 {
		t.Errorf("expected size-truncated merged size of 2, got %d", len(resultsLimit))
	}
}
