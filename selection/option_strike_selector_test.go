package selection

import (
	"testing"
	"time"
)

func TestMonthlyExpiryAndRollOver(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Kolkata")

	// Test 1: Mid-month date (e.g. 10 Aug 2026) -> Should select 27 Aug 2026 (last Thursday)
	aug10 := time.Date(2026, 8, 10, 10, 0, 0, 0, loc)
	expDate := GetMonthlyExpiryDate(aug10, 7)
	if expDate.Format("2006-01-02") != "2026-08-27" {
		t.Errorf("expected monthly expiry 2026-08-27, got %s", expDate.Format("2006-01-02"))
	}

	// Test 2: User example on 12 Aug 2026 -> Should select 27 Aug 2026
	aug12 := time.Date(2026, 8, 12, 9, 20, 0, 0, loc)
	expDate12 := GetMonthlyExpiryDate(aug12, 7)
	if expDate12.Format("2006-01-02") != "2026-08-27" {
		t.Errorf("expected monthly expiry 2026-08-27 for 12 Aug, got %s", expDate12.Format("2006-01-02"))
	}

	// Test 3: User example on 24 Aug 2026 (<= 7 days before 27 Aug) -> Should roll over to 24 Sep 2026
	aug24 := time.Date(2026, 8, 24, 10, 0, 0, 0, loc)
	rollExpDate := GetMonthlyExpiryDate(aug24, 7)
	if rollExpDate.Format("2006-01-02") != "2026-09-24" {
		t.Errorf("expected rollover monthly expiry 2026-09-24 for 24 Aug, got %s", rollExpDate.Format("2006-01-02"))
	}
}

func TestTargetPremiumStrikeSelector(t *testing.T) {
	selector := NewOptionStrikeSelector(nil)

	// 1. Test NIFTY 50 (NFO, 50-step, Thursday)
	resNifty, err := selector.SelectStrikeByTargetPremium("NIFTY 50", 24340.0, "BULLISH", 100.0, "MONTHLY", 7, nil)
	if err != nil {
		t.Fatalf("unexpected error selecting strike for NIFTY 50: %v", err)
	}
	if resNifty.BaseStrike != 24350.0 {
		t.Errorf("expected NIFTY BaseStrike 24350.0 (50 step), got %f", resNifty.BaseStrike)
	}
	if resNifty.OptionType != "PE" {
		t.Errorf("expected OptionType PE, got %s", resNifty.OptionType)
	}
	if resNifty.Exchange != "NFO" {
		t.Errorf("expected Exchange NFO, got %s", resNifty.Exchange)
	}

	// 2. Test BANK NIFTY (NFO, 100-step, Thursday)
	resBank, err := selector.SelectStrikeByTargetPremium("BANKNIFTY", 51240.0, "BEARISH", 250.0, "MONTHLY", 7, nil)
	if err != nil {
		t.Fatalf("unexpected error selecting strike for BANKNIFTY: %v", err)
	}
	if resBank.BaseStrike != 51200.0 {
		t.Errorf("expected BANKNIFTY BaseStrike 51200.0 (100 step), got %f", resBank.BaseStrike)
	}
	if resBank.OptionType != "CE" {
		t.Errorf("expected OptionType CE, got %s", resBank.OptionType)
	}
	if resBank.Exchange != "NFO" {
		t.Errorf("expected Exchange NFO, got %s", resBank.Exchange)
	}

	// 3. Test BSE SENSEX (BFO, 100-step, Friday)
	resSensex, err := selector.SelectStrikeByTargetPremium("SENSEX", 80420.0, "BULLISH", 250.0, "MONTHLY", 7, nil)
	if err != nil {
		t.Fatalf("unexpected error selecting strike for SENSEX: %v", err)
	}
	if resSensex.BaseStrike != 80400.0 {
		t.Errorf("expected SENSEX BaseStrike 80400.0, got %f", resSensex.BaseStrike)
	}
	if resSensex.Exchange != "BFO" {
		t.Errorf("expected SENSEX Exchange BFO, got %s", resSensex.Exchange)
	}
}
