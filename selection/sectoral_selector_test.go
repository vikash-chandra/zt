package selection

import (
	"testing"

	"zerodha-trading/config"
)

func TestSectoralSelectorName(t *testing.T) {
	cfg := &config.Settings{
		SectorMaxBuyPct:  2.5,
		SectorMaxSellPct: -3.0,
		StockMaxBuyPct:   2.5,
		StockMaxSellPct:  -2.5,
	}
	sel := NewSectoralSelector(cfg)

	if sel.Name() != "SECTORAL_SELECTOR" {
		t.Errorf("expected SECTORAL_SELECTOR, got %s", sel.Name())
	}
}
