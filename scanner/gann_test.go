package scanner

import "testing"

func TestCalculateGannLevels_StockAndIndex(t *testing.T) {
	// Stock example: RELIANCE at 1400
	rel := CalculateGannLevels("RELIANCE", "prev_close", 1400, 1415)
	if rel.BuyAbove <= 1400 || rel.SellBelow >= 1400 {
		t.Fatalf("expected BuyAbove > 1400 and SellBelow < 1400, got buy=%.2f sell=%.2f", rel.BuyAbove, rel.SellBelow)
	}
	if len(rel.BuyTargets) != 4 || len(rel.SellTargets) != 4 {
		t.Fatalf("expected 4 buy and 4 sell targets")
	}
	if rel.BuyTargets[0].Price <= rel.BuyAbove || rel.SellTargets[0].Price >= rel.SellBelow {
		t.Fatalf("targets not monotonic around pivots: %+v", rel)
	}

	// Index example: NIFTY 50 at 25650 with LTP above BuyAbove
	nifty := CalculateGannLevels("NIFTY 50", "prev_close", 25650, 25950)
	if nifty.ScaleFactor != 10 {
		t.Fatalf("expected scale_factor 10 for 25650, got %.2f", nifty.ScaleFactor)
	}
	if nifty.Zone != "ABOVE_BUY" {
		t.Fatalf("expected ABOVE_BUY for LTP 25950, got %s (buyAbove=%.2f)", nifty.Zone, nifty.BuyAbove)
	}
}
