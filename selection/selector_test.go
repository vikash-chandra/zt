package selection

import (
	"context"
	"testing"

	"zerodha-trading/data"

	"go.uber.org/zap"
)

func TestSelectorRegistry(t *testing.T) {
	selectors := InitializeSelectors([]string{"SECURITIES_FO", "INVALID_NAME"})

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
	MockWatchlist map[string]int64
}

func (m *MockSelector) Name() string { return "MOCK" }

func (m *MockSelector) SelectStocks(ctx context.Context, logger *zap.Logger, client interface{}, secMaster *data.SecurityMaster, bias string, size int, maxPctChange float64) (map[string]int64, error) {
	return m.MockWatchlist, nil
}
