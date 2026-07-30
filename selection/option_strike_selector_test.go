package selection

import (
	"testing"
)

func TestOptionStrikeSelector(t *testing.T) {
	selector := NewOptionStrikeSelector(nil)

	// Spot 24,340 -> Base 24,300. Offset = 300 points
	// Bullish -> Sell OTM PE at 24,000
	res, err := selector.SelectOTMStrike("NIFTY 50", 24340.0, "BULLISH", 300.0)
	if err != nil {
		t.Fatalf("unexpected error selecting OTM strike: %v", err)
	}

	if res.BaseStrike != 24300.0 {
		t.Errorf("expected BaseStrike 24300.0, got %f", res.BaseStrike)
	}
	if res.OptionType != "PE" {
		t.Errorf("expected OptionType PE for Bullish trend, got %s", res.OptionType)
	}
	if res.TargetStrike != 24000.0 {
		t.Errorf("expected TargetStrike 24000.0, got %f", res.TargetStrike)
	}

	// Bearish -> Sell OTM CE at 24,600
	bearRes, err := selector.SelectOTMStrike("NIFTY 50", 24340.0, "BEARISH", 300.0)
	if err != nil {
		t.Fatalf("unexpected error selecting OTM strike for BEARISH: %v", err)
	}
	if bearRes.OptionType != "CE" {
		t.Errorf("expected OptionType CE for Bearish trend, got %s", bearRes.OptionType)
	}
	if bearRes.TargetStrike != 24600.0 {
		t.Errorf("expected TargetStrike 24600.0, got %f", bearRes.TargetStrike)
	}
}
