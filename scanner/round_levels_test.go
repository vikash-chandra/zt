package scanner

import "testing"

func TestCalculateRound100Levels_IndicesOnly(t *testing.T) {
	nifty := CalculateRound100Levels("NIFTY 50", 25640, 25640, 5)
	if !nifty.IsIndex {
		t.Fatalf("expected NIFTY 50 to be recognized as index")
	}
	if nifty.NearestBelow != 25600 || nifty.NearestAbove != 25700 {
		t.Fatalf("expected 25600 / 25700 around 25640, got %.0f / %.0f", nifty.NearestBelow, nifty.NearestAbove)
	}
	if len(nifty.AboveLevels) != 5 || len(nifty.BelowLevels) != 5 {
		t.Fatalf("expected 5 above and 5 below levels, got %d / %d", len(nifty.AboveLevels), len(nifty.BelowLevels))
	}
	if nifty.AboveLevels[4] != 26100 || nifty.BelowLevels[4] != 25200 {
		t.Fatalf("unexpected boundary levels: above[4]=%.0f below[4]=%.0f", nifty.AboveLevels[4], nifty.BelowLevels[4])
	}

	// Exact 100-multiple boundary
	b, a := NearestRound100Levels(25600)
	if b != 25500 || a != 25700 {
		t.Fatalf("expected 25500 / 25700 around exact 25600, got %.0f / %.0f", b, a)
	}

	// Non-index stock should return IsIndex=false and empty slices
	rel := CalculateRound100Levels("RELIANCE", 1386, 1386, 5)
	if rel.IsIndex || len(rel.AboveLevels) != 0 || len(rel.BelowLevels) != 0 {
		t.Fatalf("expected RELIANCE not to generate index 100-levels, got %+v", rel)
	}
}
