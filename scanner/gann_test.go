package scanner

import "testing"

func TestCalculateGannLevels_StockAndIndex(t *testing.T) {
	// Stock example: RELIANCE at 1400 (default 5 above and 5 below from GANN LEVELs.pdf)
	rel := CalculateGannLevels("RELIANCE", "prev_close", 1400, 1415)
	if rel.BuyAbove <= 1400 || rel.SellBelow >= 1400 {
		t.Fatalf("expected BuyAbove > 1400 and SellBelow < 1400, got buy=%.2f sell=%.2f", rel.BuyAbove, rel.SellBelow)
	}
	if len(rel.BuyTargets) != 5 || len(rel.SellTargets) != 5 {
		t.Fatalf("expected 5 buy and 5 sell targets, got %d / %d", len(rel.BuyTargets), len(rel.SellTargets))
	}
	if rel.BuyTargets[1].Price <= rel.BuyAbove || rel.SellTargets[1].Price >= rel.SellBelow {
		t.Fatalf("targets not monotonic: %+v", rel)
	}

	// Index example: NIFTY 50 at 24120 -> PDF row 24025..24336 has 24103 (90 deg) below and 24141 (135 deg) above
	nifty := CalculateGannLevelsWithCount("NIFTY 50", "prev_close", 24120, 24150, 5)
	if nifty.BuyAbove != 24141 || nifty.SellBelow != 24103 {
		t.Fatalf("expected PDF levels 24141 / 24103 around 24120, got %.2f / %.2f", nifty.BuyAbove, nifty.SellBelow)
	}
	if nifty.Zone != "ABOVE_BUY" {
		t.Fatalf("expected ABOVE_BUY for LTP 24150, got %s (buyAbove=%.2f)", nifty.Zone, nifty.BuyAbove)
	}

	// Configurable line count
	custom := CalculateGannLevelsWithCount("NIFTY 50", "custom", 24120, 24120, 8)
	if len(custom.BuyTargets) != 8 || len(custom.SellTargets) != 8 {
		t.Fatalf("expected 8 above and 8 below levels, got %d / %d", len(custom.BuyTargets), len(custom.SellTargets))
	}
}
