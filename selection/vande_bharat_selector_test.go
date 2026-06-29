package selection

import (
	"testing"

	"zerodha-trading/config"
)

func TestVandeBharatSelectorName(t *testing.T) {
	cfg := &config.Settings{
		SectorMaxBuyPct:  2.5,
		SectorMaxSellPct: -3.0,
		StockMaxBuyPct:   2.5,
		StockMaxSellPct:  -2.5,
	}
	sel := NewVandeBharatSelector(cfg)

	if sel.Name() != "VANDE_BHARAT_SELECTOR" {
		t.Errorf("expected VANDE_BHARAT_SELECTOR, got %s", sel.Name())
	}
}
